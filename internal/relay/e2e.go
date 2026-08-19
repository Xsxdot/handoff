// 职责：coordinator↔executor 端到端加密（HKDF PSK + Noise NNpsk0）；边界：不认识
// relay 控制协议，只把一条裸 net.Conn 升级成加密 net.Conn；PSK 源自 handoff
// token，relay 永不接触。
package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
	"golang.org/x/crypto/hkdf"
)

const (
	saltLen         = 32
	e2eInfoPrefix   = "handoff-e2e-v1"
	maxFrameLen     = 1<<16 - 1
	noiseTagLen     = 16
	maxPlaintext    = maxFrameLen - noiseTagLen
	lengthPrefixLen = 4
	appPrefixLen    = 2
)

// SecureClient performs the initiator side of the E2E handshake and returns an
// encrypted net.Conn. The salt is public per-session randomness; token-derived
// PSK and Noise ephemeral keys provide authentication and confidentiality.
func SecureClient(ctx context.Context, raw net.Conn, token, account, node string) (net.Conn, error) {
	return secure(ctx, raw, token, account, node, true)
}

// SecureServer performs the responder side of the E2E handshake and returns an
// encrypted net.Conn.
func SecureServer(ctx context.Context, raw net.Conn, token, account, node string) (net.Conn, error) {
	return secure(ctx, raw, token, account, node, false)
}

// DerivePSKForTest exposes the fixed derivation for cross-implementation test
// vectors. Production callers should use SecureClient/SecureServer.
func DerivePSKForTest(token, account, node string, salt []byte) ([]byte, error) {
	return derivePSK(token, account, node, salt)
}

// DerivePSK derives the E2E PSK using the relay protocol's stable HKDF domain.
func DerivePSK(token, account, node string, salt []byte) ([]byte, error) {
	return derivePSK(token, account, node, salt)
}

// derivePSK uses domain separation and identity binding: changing account or
// node changes the key and prevents cross-pairing confusion.
func derivePSK(token, account, node string, salt []byte) ([]byte, error) {
	if len(salt) != saltLen {
		return nil, fmt.Errorf("e2e salt must be %d bytes, got %d", saltLen, len(salt))
	}
	info := []byte(e2eInfoPrefix + "|" + account + "|" + node)
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(token), salt, info), key); err != nil {
		return nil, fmt.Errorf("derive e2e PSK: %w", err)
	}
	return key, nil
}

func secure(ctx context.Context, raw net.Conn, token, account, node string, initiator bool) (net.Conn, error) {
	if raw == nil {
		return nil, errors.New("e2e: nil raw connection")
	}
	stop := watchContext(ctx, raw)
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	salt := make([]byte, saltLen)
	if initiator {
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("generate e2e salt: %w", err)
		}
		// salt 非机密，只保证每会话 PSK 派生不同；机密性来自 token 与 Noise 临时密钥。
		if err := writeLengthPrefixed(raw, salt); err != nil {
			return nil, fmt.Errorf("send e2e salt: %w", err)
		}
	} else if err := readLengthPrefixed(raw, salt, saltLen); err != nil {
		return nil, fmt.Errorf("receive e2e salt: %w", err)
	}

	psk, err := derivePSK(token, account, node, salt)
	if err != nil {
		return nil, err
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Pattern:               noise.HandshakeNN,
		Initiator:             initiator,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize e2e handshake: %w", err)
	}

	var send, recv *noise.CipherState
	if initiator {
		msg, _, _, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, fmt.Errorf("write e2e handshake: %w", err)
		}
		if err := writeLengthPrefixed(raw, msg); err != nil {
			return nil, fmt.Errorf("send e2e handshake: %w", err)
		}
		reply, err := readFrame(raw, noise.MaxMsgLen)
		if err != nil {
			return nil, fmt.Errorf("read e2e handshake: %w", err)
		}
		msg, send, recv, err = hs.ReadMessage(nil, reply)
		if err != nil {
			return nil, fmt.Errorf("read e2e handshake: %w", err)
		}
		_ = msg
	} else {
		msg, err := readFrame(raw, noise.MaxMsgLen)
		if err != nil {
			return nil, fmt.Errorf("receive e2e handshake: %w", err)
		}
		if _, _, _, err := hs.ReadMessage(nil, msg); err != nil {
			return nil, fmt.Errorf("read e2e handshake: %w", err)
		}
		msg, send, recv, err = hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, fmt.Errorf("write e2e handshake: %w", err)
		}
		if err := writeLengthPrefixed(raw, msg); err != nil {
			return nil, fmt.Errorf("send e2e handshake: %w", err)
		}
		// Noise Split returns the initiator's sending state first. The responder
		// must reverse the pair for its local send/receive directions.
		send, recv = recv, send
	}
	if send == nil || recv == nil {
		return nil, errors.New("e2e handshake completed without cipher states")
	}
	return &secureConn{raw: raw, send: send, recv: recv}, nil
}

func watchContext(ctx context.Context, raw net.Conn) func() {
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = raw.Close()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

func writeLengthPrefixed(w io.Writer, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	var prefix [lengthPrefixLen]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	if err := writeFull(w, prefix[:]); err != nil {
		return err
	}
	return writeFull(w, payload)
}

func readLengthPrefixed(r io.Reader, dst []byte, expected int) error {
	var prefix [lengthPrefixLen]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n != uint32(expected) {
		return fmt.Errorf("unexpected frame length %d, want %d", n, expected)
	}
	_, err := io.ReadFull(r, dst)
	return err
}

func readFrame(r io.Reader, max int) ([]byte, error) {
	var prefix [lengthPrefixLen]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > uint32(max) {
		return nil, fmt.Errorf("frame length %d exceeds maximum %d", n, max)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

type secureConn struct {
	raw  net.Conn
	send *noise.CipherState
	recv *noise.CipherState

	writeMu   sync.Mutex
	readMu    sync.Mutex
	bufMu     sync.Mutex
	readBuf   []byte
	closeOnce sync.Once
}

func (c *secureConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	c.bufMu.Lock()
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		c.bufMu.Unlock()
		return n, nil
	}
	c.bufMu.Unlock()

	ciphertext, err := readAppFrame(c.raw)
	if err != nil {
		return 0, err
	}
	plaintext, err := c.recv.Decrypt(nil, nil, ciphertext)
	if err != nil {
		return 0, fmt.Errorf("decrypt e2e frame: %w", err)
	}
	n := copy(p, plaintext)
	if n < len(plaintext) {
		c.bufMu.Lock()
		c.readBuf = append(c.readBuf, plaintext[n:]...)
		c.bufMu.Unlock()
	}
	return n, nil
}

func (c *secureConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for written < len(p) {
		n := len(p) - written
		if n > maxPlaintext {
			n = maxPlaintext
		}
		ciphertext, err := c.send.Encrypt(nil, nil, p[written:written+n])
		if err != nil {
			return written, fmt.Errorf("encrypt e2e frame: %w", err)
		}
		if err := writeAppFrame(c.raw, ciphertext); err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

func writeAppFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("app frame too large: %d", len(payload))
	}
	var prefix [appPrefixLen]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(payload)))
	if err := writeFull(w, prefix[:]); err != nil {
		return err
	}
	return writeFull(w, payload)
}

func readAppFrame(r io.Reader) ([]byte, error) {
	var prefix [appPrefixLen]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(prefix[:])
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *secureConn) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.raw.Close() })
	return err
}
func (c *secureConn) LocalAddr() net.Addr                { return c.raw.LocalAddr() }
func (c *secureConn) RemoteAddr() net.Addr               { return c.raw.RemoteAddr() }
func (c *secureConn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *secureConn) SetReadDeadline(t time.Time) error  { return c.raw.SetReadDeadline(t) }
func (c *secureConn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }
