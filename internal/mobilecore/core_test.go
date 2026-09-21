// B369 直通竖切（重档法定步骤）：一次真实调用穿全链，测试钉在主缝（库缝形态 =
// 夹具直调）。
//
// 路径：proto 编码配对 bundle → Core.Pair 解码 → DefaultDial（relay.NewDialer +
// client.NewRelay）→ 真 WSS → fake relay 终结 E2E 与 app-yamux → HTTP 打到上游
// handler → Core 回环反代 → 调用方拿到响应。
//
// 同时锁两条回环门禁断言：反代透传不注入 Authorization；上游看到的 Host 是
// loopback 名（relay 投递会过对端 hostGuard）。
package mobilecore

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
)

// startFakeRelay 起一个行为等价的对端：终结 E2E + app-yamux，把每条 app 流交给
// upstream handler（模拟对端 agentd.Handler）。用 relay 的导出 wire API 搭建，
// 因此这条测试独立复核配对/连接核与 relay 线格式的咬合。
func startFakeRelay(t *testing.T, token, account, node string, upstream http.Handler) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		f, err := relay.Decode(data)
		if err != nil || f.Type != relay.Connect {
			_ = ws.Close(websocket.StatusPolicyViolation, "bad connect")
			return
		}
		okData, err := relay.Encode(relay.Frame{Type: relay.ConnectOK, Account: account})
		if err != nil {
			return
		}
		if err := ws.Write(ctx, websocket.MessageText, okData); err != nil {
			return
		}
		raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		secure, err := relay.SecureServer(ctx, raw, token, account, node)
		if err != nil {
			return
		}
		appMux, err := yamux.Server(secure, yamuxConfig())
		if err != nil {
			return
		}
		for {
			stream, err := appMux.Accept()
			if err != nil {
				return
			}
			go func(s net.Conn) {
				_ = http.Serve(&singleConnListener{conn: s}, upstream)
			}(stream)
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

// yamuxConfig 关闭 yamux 默认 stderr 日志（与 relay 生产口径一致）。
func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	cfg.Logger = nil
	return cfg
}

type singleConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.conn })
	if c == nil {
		return nil, net.ErrClosed
	}
	return c, nil
}
func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// TestPairVerticalSlice 是主缝竖切：一份 bundle 走完编码→解码→拨号→回环反代→
// 真实 HTTP 往返。写死结果（pong）不构成子卡的「已有活路径」。
func TestPairVerticalSlice(t *testing.T) {
	const (
		token   = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
		account = "acc1"
		node    = "devbox"
	)
	var seenAuth, seenHost string
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenHost = r.Host
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "pong")
	})
	relayURL, cleanup := startFakeRelay(t, token, account, node, upstream)
	defer cleanup()

	bundle := proto.PairBundle{
		Version: proto.PairVersion,
		Relay:   &proto.PairRelay{URL: relayURL, Credential: "pipecred"},
		Machines: []proto.PairMachine{
			{Name: "devbox", Token: token, Node: node},
		},
		ExpiresAt: time.Now().Add(time.Minute),
	}
	payload, err := proto.EncodePairBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}

	core := New(nil, slog.Default())
	defer core.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := core.Pair(ctx, payload)
	if err != nil {
		t.Fatalf("配对失败: %v", err)
	}
	if len(res.Machines) != 1 || !res.Machines[0].Online || res.Machines[0].Origin == "" {
		t.Fatalf("配对结果应一台在线机带 origin: %+v", res.Machines)
	}
	origin := res.Machines[0].Origin

	// 一次真实调用穿回环反代：GET origin/api/status。
	resp, err := http.Get(origin + "/api/status")
	if err != nil {
		t.Fatalf("经回环反代请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Fatalf("上游响应应为 pong，得到 %q（status=%d）", body, resp.StatusCode)
	}
	// 回环门禁：反代不注入凭据。
	if seenAuth != "" {
		t.Fatalf("反代不得注入 Authorization，上游看到 %q", seenAuth)
	}
	// Host 必须是 loopback 名（relay 投递要过对端 hostGuard）。
	if !strings.HasPrefix(seenHost, "localhost") {
		t.Fatalf("上游 Host 应为 loopback 名，得到 %q", seenHost)
	}
}

// TestPairPartialBundle 锁离线机语义：单机拨号失败标 offline，不整单失败。
func TestPairPartialBundle(t *testing.T) {
	bundle := proto.PairBundle{
		Version: proto.PairVersion,
		Relay:   &proto.PairRelay{URL: "wss://127.0.0.1:1/relay", Credential: "pipecred"},
		Machines: []proto.PairMachine{
			{Name: "offline", Token: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", Node: "offline"},
		},
	}
	payload, err := proto.EncodePairBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	core := New(nil, slog.Default())
	defer core.Close()
	res, err := core.Pair(context.Background(), payload)
	if err != nil {
		t.Fatalf("部分 bundle 不应整单失败: %v", err)
	}
	if len(res.Machines) != 1 || res.Machines[0].Online {
		t.Fatalf("离线机应标 offline: %+v", res.Machines)
	}
}
