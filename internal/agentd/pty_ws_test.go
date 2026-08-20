package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// dialPty 建一条 /ws/pty 连接并断言首帧是 attached。
func dialPty(t *testing.T, env *testAgentdEnv, id string, since uint64) (*websocket.Conn, proto.PtyControl) {
	t.Helper()
	url := strings.Replace(env.ts.URL, "http://", "ws://", 1) +
		"/ws/pty?session=" + id + "&since=" + itoa(since)
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨 /ws/pty 失败: %v", err)
	}
	typ, data, err := c.Read(context.Background())
	if err != nil {
		t.Fatalf("读首帧: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("首帧类型 = %v，期望 text（attached 控制帧）", typ)
	}
	var ctrl proto.PtyControl
	if err := json.Unmarshal(data, &ctrl); err != nil {
		t.Fatalf("解析 attached 帧: %v；原文=%s", err, data)
	}
	if ctrl.Type != proto.PtyCtrlAttached {
		t.Fatalf("首帧 type = %q，期望 attached", ctrl.Type)
	}
	return c, ctrl
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

// readUntil 累积 binary 帧直到出现 want。
func readUntil(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var sb strings.Builder
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("读帧失败: %v；累计:\n%s", err, sb.String())
		}
		if typ != websocket.MessageBinary {
			continue // 控制帧，本用例不关心
		}
		sb.Write(data)
		if strings.Contains(sb.String(), want) {
			return sb.String()
		}
	}
}

// 打字 → 回显：binary 帧双向跑通。
func TestPtyWSEchoRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c, ctrl := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	if ctrl.Truncated {
		t.Error("新会话不该报 truncated")
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("echo WS_OK\n")); err != nil {
		t.Fatalf("写按键: %v", err)
	}
	readUntil(t, c, "WS_OK")
}

// text 控制帧 resize 生效。
func TestPtyWSResize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c, _ := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	msg, _ := json.Marshal(proto.PtyControl{Type: proto.PtyCtrlResize, Cols: 132, Rows: 43})
	if err := c.Write(context.Background(), websocket.MessageText, msg); err != nil {
		t.Fatalf("写 resize: %v", err)
	}
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("stty size\n"))
	readUntil(t, c, "43 132")
}

// 断开重连带 since：只补没看过的那段。
func TestPtyWSResumeSince(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	c1, _ := dialPty(t, env, s.ID, 0)
	// macOS 的 PTY 行规程会先把按键回显发回主端，且逐片到达——waitFor 命中
	// `echo ROUND1` 的**回显**就返回了，游标会停在回显处。为让「只补未读段」
	// 的断言在 macOS 上也成立，命令文本用引号断开：shell 里 `echo ROUN"D1"`
	// 输出 ROUND1（引号串与裸文本拼接），而回显 `echo ROUN"D1"` 不含连续的
	// ROUND1 子串，waitFor 只能等到真正的命令输出。
	_ = c1.Write(context.Background(), websocket.MessageBinary, []byte("echo ROUN\"D1\"\n"))
	readUntil(t, c1, "ROUND1")
	cur, _ := env.srv.pty.Get(s.ID)
	_ = c1.Close(websocket.StatusNormalClosure, "")

	_ = env.srv.pty.Write(s.ID, []byte("echo ROUND2\n"))
	time.Sleep(500 * time.Millisecond)

	c2, ctrl := dialPty(t, env, s.ID, cur.BytesOut)
	defer c2.Close(websocket.StatusNormalClosure, "")
	if ctrl.Truncated {
		t.Error("这点输出装得下，不该 truncated")
	}
	got := readUntil(t, c2, "ROUND2")
	if strings.Contains(got, "ROUND1") {
		t.Errorf("since 之前的内容不该重放，实得:\n%s", got)
	}
}

// 会话已退出时建连：先灌历史、再发 exit 帧、再正常关闭——不是错误路径。
func TestPtyWSAttachToExitedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	t.Setenv("HOME", t.TempDir())
	s := ptyCreate(t, env, `{"base_kind":"home","cols":80,"rows":24}`)
	t.Cleanup(func() { _ = env.srv.pty.Close(s.ID) })

	_ = env.srv.pty.Write(s.ID, []byte("echo BYE; exit 5\n"))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if g, ok := env.srv.pty.Get(s.ID); ok && g.ExitCode != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	c, _ := dialPty(t, env, s.ID, 0)
	defer c.Close(websocket.StatusNormalClosure, "")
	sawBye, exitCode := false, -1
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			break // 服务端已 close(1000)
		}
		if typ == websocket.MessageBinary && bytes.Contains(data, []byte("BYE")) {
			sawBye = true
			continue
		}
		if typ == websocket.MessageText {
			var ctrl proto.PtyControl
			_ = json.Unmarshal(data, &ctrl)
			if ctrl.Type == proto.PtyCtrlExit && ctrl.ExitCode != nil {
				exitCode = *ctrl.ExitCode
			}
		}
	}
	if !sawBye {
		t.Error("已退出会话的历史输出必须先灌给用户看最后一眼")
	}
	if exitCode != 5 {
		t.Errorf("exit 控制帧的 exit_code = %d，期望 5", exitCode)
	}
}

// 会话不存在：1008 policy violation（「你这个请求不合法，别重连」）。
func TestPtyWSUnknownSession(t *testing.T) {
	env := newTestAgentdEnv(t)
	url := strings.Replace(env.ts.URL, "http://", "ws://", 1) + "/ws/pty?session=nope&since=0"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("拨 /ws/pty: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "")
	_, _, rerr := c.Read(context.Background())
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("关闭码 = %v，期望 1008（不该让前端一直重连）", websocket.CloseStatus(rerr))
	}
}
