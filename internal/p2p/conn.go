package p2p

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/sctp"
)

// Conn 是经直连路径的可靠有序字节流（net.Conn）。sctp 流是消息语义——小缓冲读
// 报 ErrShortBuffer、单条写超 64 KiB 报错、Close 只关写方向——本类型把它适配成
// 字节流，并把 sctp association 与 ICE 代理的生命周期收在 Close 里。
type Conn struct {
	agent  *Agent
	ice    *ice.Conn
	assoc  *sctp.Association
	stream *sctp.Stream
	path   Path

	rmu     sync.Mutex
	rbuf    []byte
	pending []byte
	wmu     sync.Mutex

	selectable atomic.Bool
	deadOnce   sync.Once
	dead       chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

func newConn(a *Agent, iceConn *ice.Conn, assoc *sctp.Association, stream *sctp.Stream) *Conn {
	c := &Conn{agent: a, ice: iceConn, assoc: assoc, stream: stream,
		path: a.selectedPath(), rbuf: make([]byte, readBufSize), dead: make(chan struct{})}
	c.selectable.Store(true)
	a.setOnState(func(s ice.ConnectionState) {
		switch s {
		case ice.ConnectionStateDisconnected:
			c.selectable.Store(false)
		case ice.ConnectionStateConnected, ice.ConnectionStateCompleted:
			c.selectable.Store(true)
		case ice.ConnectionStateFailed, ice.ConnectionStateClosed:
			c.selectable.Store(false)
			c.markDead()
		}
	})
	// 注册回调前就可能已 Failed。
	select {
	case <-a.failed:
		c.selectable.Store(false)
		c.markDead()
	default:
	}
	return c
}

func (c *Conn) markDead() { c.deadOnce.Do(func() { close(c.dead) }) }

// Path 返回选中候选对的类型与地址族（可进日志）。
func (c *Conn) Path() Path { return c.path }

// Dead 在 ICE 进入 Failed/Closed、底层读写出错或 Close 后关闭。
func (c *Conn) Dead() <-chan struct{} { return c.dead }

// Selectable 为 false 表示 ICE 处于 Disconnected：新流不应再选这条路径。
func (c *Conn) Selectable() bool {
	select {
	case <-c.dead:
		return false
	default:
	}
	return c.selectable.Load()
}

// Read 实现字节流读：一条 sctp 消息可分多次读出。
func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if len(c.pending) == 0 {
		n, err := c.stream.Read(c.rbuf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.markDead()
			}
			return 0, err
		}
		c.pending = c.rbuf[:n]
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// Write 按 maxWriteChunk 切片写出；sctp 已开阻塞写（背压有界）。
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	written := 0
	for written < len(p) {
		end := written + maxWriteChunk
		if end > len(p) {
			end = len(p)
		}
		n, err := c.stream.Write(p[written:end])
		written += n
		if err != nil {
			c.markDead()
			return written, err
		}
	}
	return written, nil
}

// Close 关流、关 association、优雅关闭 ICE 代理（等待其 goroutine 退出）。幂等。
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		c.markDead()
		_ = c.stream.Close()
		// Abort 先发 ABORT（≤200ms 刷出）：对端读侧立即报错，不必等 ICE 超时（~10s）
		// 才发现直连已关。Close 再回收 association 状态（幂等）。
		c.assoc.Abort("handoff p2p closed")
		c.closeErr = c.assoc.Close()
		if err := c.agent.Close(); c.closeErr == nil {
			c.closeErr = err
		}
	})
	return c.closeErr
}

func (c *Conn) LocalAddr() net.Addr                { return c.ice.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr               { return c.ice.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error      { return c.stream.SetDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }

// discardLoggerFactory 丢弃 pion 的全部日志：pion 的 Debug/Warn 会打出候选串
// （内含 ufrag），与 relay/mux.go 丢弃 yamux 日志同理——生命周期由本包带上下文的
// slog 记录。
type discardLoggerFactory struct{}

func (discardLoggerFactory) NewLogger(string) logging.LeveledLogger { return discardLogger{} }

type discardLogger struct{}

func (discardLogger) Trace(string)          {}
func (discardLogger) Tracef(string, ...any) {}
func (discardLogger) Debug(string)          {}
func (discardLogger) Debugf(string, ...any) {}
func (discardLogger) Info(string)           {}
func (discardLogger) Infof(string, ...any)  {}
func (discardLogger) Warn(string)           {}
func (discardLogger) Warnf(string, ...any)  {}
func (discardLogger) Error(string)          {}
func (discardLogger) Errorf(string, ...any) {}
