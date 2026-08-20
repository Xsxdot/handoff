//go:build unix

// client_test.go —— agentd 客户端与独立 ptyhost 之间的行为测试。
//
// 职责：覆盖启动、stat、订阅续传、复用写入、断开与显式 kill 的客户端契约。
// 边界：不测试浏览器 WebSocket，也不把 hostproc 内部实现当作客户端状态；对端只通过
// Unix socket 与 wire 帧可见。
package ptyhost_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
)

func TestClientOpenList(t *testing.T) {
	root := shortRoot(t)
	exe := buildHandoff(t)
	h := ptyhost.New(root, exe, testLog())
	sess, err := h.Open(ptyhost.OpenOptions{
		BasePath: root, BaseKind: "workspace", Shell: "/bin/sh",
		Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "}, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = h.Close(sess.ID) })
	if sess.BasePath != root || sess.Shell != "/bin/sh" || sess.PID <= 0 {
		t.Fatalf("session = %+v", sess)
	}
	list := h.List()
	if len(list) != 1 || list[0].ID != sess.ID || list[0].BasePath != root || list[0].PID != sess.PID {
		t.Fatalf("List = %+v", list)
	}
}

// TestClientRegistersPtyhostForPressure 真实 hostproc 会话经 Adopt 后必须成为可验证凭据，
// 从机器级压力计数中扣除；hostproc 在本测试进程的 goroutine 中运行，所以差值应恰为一。
func TestClientRegistersPtyhostForPressure(t *testing.T) {
	_, h, id, _ := startClientHost(t)
	if sess, ok := h.Get(id); !ok || sess.PID <= 0 {
		t.Fatalf("Get = %+v, ok=%v，期望活 ptyhost 会话", sess, ok)
	}

	withPtyhost := prochost.CheckAdmission()
	if !withPtyhost.Known {
		t.Skip("本平台无法读机器级进程压力")
	}
	// 取掉 Host 在 New 时安装的提供者，得到同一时刻的未排除基线；
	// t.Cleanup 先恢复为空，避免影响本包后续测试进程。
	prochost.SetPtyhostCredentialProvider(nil)
	t.Cleanup(func() { prochost.SetPtyhostCredentialProvider(nil) })
	withoutPtyhost := prochost.CheckAdmission()
	if !withoutPtyhost.Known {
		t.Skip("本平台无法读机器级进程压力")
	}
	if withoutPtyhost.Used-withPtyhost.Used != 1 {
		t.Fatalf("未排除 used=%d，排除后 used=%d，期望差值 1",
			withoutPtyhost.Used, withPtyhost.Used)
	}
}

func TestClientWriteAttach(t *testing.T) {
	_, h, id, _ := startClientHost(t)
	att, err := h.Attach(id, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer att.Detach()
	if err := h.Write(id, []byte("echo HANDOFF_MARK\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !attachmentContains(att, "HANDOFF_MARK") {
		t.Fatal("订阅没有收到 shell 回显")
	}
}

func TestClientTwoAttachmentsStat(t *testing.T) {
	_, h, id, _ := startClientHost(t)
	a, err := h.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := h.Attach(id, 0)
	if err != nil {
		a.Detach()
		t.Fatal(err)
	}
	defer a.Detach()
	defer b.Detach()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sess, ok := h.Get(id)
		if ok && sess.Attached == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	sess, _ := h.Get(id)
	t.Fatalf("Attached = %d，期望 2", sess.Attached)
}

func TestClientDetachKeepsSession(t *testing.T) {
	_, h, id, _ := startClientHost(t)
	a, err := h.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.Detach()
	time.Sleep(50 * time.Millisecond)
	if _, ok := h.Get(id); !ok {
		t.Fatal("Detach 后本地会话登记不应消失")
	}
	b, err := h.Attach(id, 0)
	if err != nil {
		t.Fatalf("Detach 后重新 Attach: %v", err)
	}
	b.Detach()
}

func TestClientCloseRemovesSession(t *testing.T) {
	_, h, id, done := startClientHost(t)
	if err := h.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if list := h.List(); len(list) != 0 {
		t.Fatalf("Close 后 List = %+v", list)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hostproc.Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ptyhost 未在 Close 后退出")
	}
}

func TestClientProtoMismatch(t *testing.T) {
	root, h, id, _ := startClientHost(t)
	m, err := sessdir.ReadMeta(root, id)
	if err != nil {
		t.Fatal(err)
	}
	m.ProtoVersion = 99
	if err := sessdir.WriteMeta(root, m); err != nil {
		t.Fatal(err)
	}
	entries, err := sessdir.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	h.Adopt(entries)
	if _, err := h.Attach(id, 0); !errors.Is(err, ptyhost.ErrProtoMismatch) {
		t.Fatalf("Attach err = %v，期望 ErrProtoMismatch", err)
	}
	if list := h.List(); len(list) != 1 || list[0].ID != id || !list[0].Incompatible {
		t.Fatalf("版本错配会话不应从 List 消失: %+v", list)
	}
}

func TestClientOpenTimeoutCleansDirectory(t *testing.T) {
	root := shortRoot(t)
	h := ptyhost.New(root, "/bin/true", testLog())
	started := time.Now()
	_, err := h.Open(ptyhost.OpenOptions{BasePath: root, Shell: "/bin/sh"})
	if err == nil {
		t.Fatal("/bin/true 不会提供 socket，Open 应失败")
	}
	if time.Since(started) >= 3*time.Second {
		t.Fatalf("Open 超过 3 秒才失败: %v", time.Since(started))
	}
	entries, scanErr := sessdir.Scan(root)
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if len(entries) != 0 {
		t.Fatalf("超时后残留会话: %+v", entries)
	}
}

// startClientHost 起一个真实 hostproc，并让客户端从扫描结果登记它。
func startClientHost(t *testing.T) (root string, h *ptyhost.Host, id string, done chan error) {
	t.Helper()
	root = shortRoot(t)
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
	done = make(chan error, 1)
	go func() {
		done <- hostproc.Run(specPath)
		close(done)
	}()
	waitSocket(t, sessdir.SockPath(root, id))
	entries, err := sessdir.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	h = ptyhost.New(root, "", testLog())
	h.Adopt(entries)
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
	return root, h, id, done
}

func attachmentContains(att *ptyhost.Attachment, want string) bool {
	if bytes.Contains(att.Backlog, []byte(want)) {
		return true
	}
	deadline := time.After(3 * time.Second)
	var acc []byte
	for {
		select {
		case b, ok := <-att.Out:
			if !ok {
				return bytes.Contains(acc, []byte(want))
			}
			acc = append(acc, b...)
			if bytes.Contains(acc, []byte(want)) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func buildHandoff(t *testing.T) string {
	t.Helper()
	root := shortRoot(t)
	out := filepath.Join(root, "handoff")
	cmd := exec.Command("go", "build", "-o", out, "../..")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build handoff: %v\n%s", err, body)
	}
	if err := os.Chmod(out, 0o700); err != nil {
		t.Fatalf("chmod handoff: %v", err)
	}
	path, err := filepath.Abs(out)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func shortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(".", "pc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func waitSocket(t *testing.T, path string) {
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

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
