package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// 端到端：浏览器 → 本机 agentd → 远端 agentd → 远端 ptyhost，字节双向透传。
func TestForwardWSPtyEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, remote, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = remote.srv.pty.Close(s.ID) })

	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=" + s.ID + "&since=0&machine=devbox"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("经本机拨远端 /ws/pty: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// 首帧必须是远端原样发出的 attached 控制帧——反代不解析、不改写
	typ, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("读首帧: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("首帧类型 = %v，期望 text", typ)
	}
	var ctrl proto.PtyControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("解析 attached: %v；原文=%s", err, data)
	}
	if ctrl.Type != proto.PtyCtrlAttached {
		t.Fatalf("首帧 type = %q，期望 attached", ctrl.Type)
	}

	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("echo PROXY_OK\n")); err != nil {
		t.Fatalf("上行写: %v", err)
	}
	readUntil(t, c, "PROXY_OK")
}

// 远端够不着：**在升级之前**回 502 带原文，而不是升级成功后再关。
//
// 这条是分诊的关键：升级成功再关，前端只会看到「连上了又断了」，会一直重连；
// 502 才让它知道是本机与目标机之间的问题。
func TestForwardWSUnreachableRemoteYields502(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=any&since=0&machine=devbox"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err == nil {
		t.Fatal("远端不可达时握手必须失败")
	}
	if resp == nil || resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态码 = %v，期望 502", resp)
	}
}

// 机器名不在 targets 里：400，且点名它。与 REST 的 forwardIfRequested 一致。
func TestForwardWSUnknownMachine(t *testing.T) {
	local := newTestAgentdEnv(t)
	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=any&since=0&machine=ghost"
	_, resp, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err == nil {
		t.Fatal("未知机器名必须被拒")
	}
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %v，期望 400", resp)
	}
}

// 远端主动关闭时，关闭码与原因要传回浏览器——否则前端分不清「会话被拒」
// 与「网络断了」，1008 的终止分支就永远走不到。
func TestForwardWSPropagatesCloseStatus(t *testing.T) {
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 远端上没有这个会话 → 远端 close(1008)，反代必须原样传回
	url := strings.Replace(local.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=missing&since=0&machine=devbox"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨号: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "")
	_, _, rerr := c.Read(context.Background())
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 = %v，期望 1008 原样传回", websocket.CloseStatus(rerr))
	}
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 = %v，期望 1008 原样传回", websocket.CloseStatus(rerr))
	}
}
