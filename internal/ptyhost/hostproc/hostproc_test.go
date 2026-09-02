//go:build unix

package hostproc_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
	"github.com/Xsxdot/handoff/internal/ptytestroot"
)

// startHost 在后台跑一个真的 ptyhost 主体，返回会话根目录与会话 id。
func startHost(t *testing.T) (root, id string) {
	t.Helper()
	root = shortTempDir(t)
	id = "s1"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "},
		Cols: 80, Rows: 24,
	}
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(root, id, "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hostproc.Run(specPath) }()
	t.Cleanup(func() {
		if c, err := net.Dial("unix", sessdir.SockPath(root, id)); err == nil {
			_ = wire.WriteControl(c, wire.Control{Type: wire.CtrlKill})
			_ = c.Close()
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})
	waitSessionReady(t, root, id)
	return root, id
}

// waitSessionReady 等到会话**真正**就绪：socket 与 meta.json 都在。
//
// why 不能只等 socket：Run 先 net.Listen、**之后**才 WriteMeta，socket 出现只是
// 中途副产物。并发负载下这两步之间的窗口被拉大，只等 socket 的调用方会读到
// 「meta.json 不存在」——2026-08-20 并行跑 ./internal/ptyhost/... 时
// TestMetaWrittenWithPidAndVersion 正是这么红的。
//
// 注意：waitSock 仍然保留给「只需要连上 socket、不读 meta」的调用方——那里等 socket
// 就是正确判据，换成本函数只会白等一个用不着的条件。
func waitSessionReady(t *testing.T, root, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sessdir.SockPath(root, id)); err == nil {
			if _, err := sessdir.ReadMeta(root, id); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("会话迟迟没就绪（socket 或 meta.json 缺失）: root=%s id=%s", root, id)
}

// shortTempDir 给 hostproc 的 Unix socket 留出路径空间，并复用 ptyhost 客户端的唯一
// 根目录决策。hostproc 用 s1 做白盒会话 ID，但仍按 UUID 最大长度预留空间。
func shortTempDir(t *testing.T) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	decision, err := ptytestroot.Resolve(
		ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, logger)
	if err != nil {
		t.Skipf("PTY 测试根目录不可用: %v", err)
		return ""
	}
	t.Cleanup(decision.Cleanup)
	return decision.Root
}

func waitSock(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket 迟迟没出现: %s", path)
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("文件迟迟没出现: %s", path)
		}
		<-ticker.C
	}
}

// TestMetaWrittenWithPidAndVersion 起来之后 meta.json 必须齐活。
func TestMetaWrittenWithPidAndVersion(t *testing.T) {
	root, id := startHost(t)
	m, err := sessdir.ReadMeta(root, id)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if m.PID != os.Getpid() || m.ProtoVersion != wire.ProtoVersion || m.Shell != "/bin/sh" {
		t.Fatalf("meta = %+v", m)
	}
}

// TestAttachEchoesInput 打通「写进去 → 从订阅里出来」这一整条。
func TestAttachEchoesInput(t *testing.T) {
	root, id := startHost(t)
	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	_, _, ctrl, err := wire.ReadFrame(c)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if ctrl == nil || ctrl.Type != wire.CtrlAttached || ctrl.ProtoVersion != wire.ProtoVersion {
		t.Fatalf("首帧 = %+v，期望 attached", ctrl)
	}

	if err := wire.WriteData(c, []byte("echo HANDOFF_MARK\n")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, c, "HANDOFF_MARK") {
		t.Fatal("没等到 shell 的回显")
	}
}

// waitFor 持续读帧直到看到 want 或超时。
func waitFor(t *testing.T, c net.Conn, want string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var acc []byte
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		kind, data, _, err := wire.ReadFrame(c)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if err == io.EOF {
				return false
			}
			continue
		}
		if kind == wire.KindData {
			acc = append(acc, data...)
			if len(acc) > 1<<16 {
				acc = acc[len(acc)-(1<<15):]
			}
			for i := 0; i+len(want) <= len(acc); i++ {
				if string(acc[i:i+len(want)]) == want {
					return true
				}
			}
		}
	}
	return false
}

// TestStatReportsLiveFacts stat 是活事实的唯一来源。
func TestStatReportsLiveFacts(t *testing.T) {
	root, id := startHost(t)
	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlStat}); err != nil {
		t.Fatal(err)
	}
	_, _, ctrl, err := wire.ReadFrame(c)
	if err != nil {
		t.Fatal(err)
	}
	if ctrl == nil || ctrl.Type != wire.CtrlStatResp {
		t.Fatalf("ctrl = %+v", ctrl)
	}
	if ctrl.Cols != 80 || ctrl.Rows != 24 {
		t.Fatalf("尺寸 = %dx%d，期望 80x24", ctrl.Cols, ctrl.Rows)
	}
	if ctrl.ExitCode != nil {
		t.Fatalf("shell 还活着，exit_code 必须缺席，得到 %d", *ctrl.ExitCode)
	}
}

// TestSinceResumeAfterReconnect 这是断线后滚屏续传的核心承诺。
func TestSinceResumeAfterReconnect(t *testing.T) {
	root, id := startHost(t)

	c1, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c1, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteData(c1, []byte("echo BEFORE_DROP\n")); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, c1, "BEFORE_DROP") {
		t.Fatal("没等到第一段输出")
	}
	_ = c1.Close()

	c2, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := wire.WriteControl(c2, wire.Control{Type: wire.CtrlAttach, Since: 0}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c2); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, c2, "BEFORE_DROP") {
		t.Fatal("重连后回放里没有断线前的输出——since 续传没保住")
	}
}

// TestDetachDoesNotKill 断开订阅不能杀掉会话。
func TestDetachDoesNotKill(t *testing.T) {
	root, id := startHost(t)
	c1, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c1, wire.Control{Type: wire.CtrlAttach}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := wire.ReadFrame(c1); err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()

	time.Sleep(200 * time.Millisecond)
	c2, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatalf("断开订阅之后会话就没了: %v", err)
	}
	defer c2.Close()
	if err := wire.WriteControl(c2, wire.Control{Type: wire.CtrlStat}); err != nil {
		t.Fatal(err)
	}
	if _, _, ctrl, err := wire.ReadFrame(c2); err != nil || ctrl == nil {
		t.Fatalf("会话应仍然可用: err=%v ctrl=%v", err, ctrl)
	}
}

// TestKillEndsProcessAndCleansDir kill 帧要杀干净并清目录。
func TestKillEndsProcessAndCleansDir(t *testing.T) {
	root := shortTempDir(t)
	id := "k1"
	home := t.TempDir()
	ready := filepath.Join(home, "b234-ready")
	late := filepath.Join(home, "b234-late")
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "HOME=" + home},
		Cols: 80, Rows: 24,
		InitCommand: `trap 'exit 0' TERM; trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"; while :; do :; done`,
	}
	body, _ := json.Marshal(spec)
	specPath := filepath.Join(root, id, "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hostproc.Run(specPath) }()
	waitSock(t, sessdir.SockPath(root, id))
	waitFile(t, ready)

	c, err := net.Dial("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteControl(c, wire.Control{Type: wire.CtrlKill}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run 应正常返回: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("收到 kill 之后 Run 没有返回")
	}
	if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("kill 之后会话目录应被清掉: %v", err)
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("Run 返回后 late marker 不存在: %v", err)
	}
}

// TestSecondInstanceRefuses 同一个会话目录不能被两个 ptyhost 同时占。
func TestSecondInstanceRefuses(t *testing.T) {
	root, id := startHost(t)
	err := hostproc.Run(filepath.Join(root, id, "spec.json"))
	if err == nil {
		t.Fatal("第二个实例应被锁挡下")
	}
}
