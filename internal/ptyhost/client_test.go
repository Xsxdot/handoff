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
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
	"github.com/Xsxdot/handoff/internal/ptytestroot"
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

func waitClientFile(path string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		<-ticker.C
	}
}

func releaseClientFIFO(t *testing.T, path string) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			done <- err
			return
		}
		_, writeErr := f.WriteString("release\n")
		closeErr := f.Close()
		if writeErr != nil {
			done <- writeErr
			return
		}
		done <- closeErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("释放 ptyhost shell trap FIFO: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ptyhost shell trap 未打开 release FIFO")
	}
}

func TestClientOpenCloseWaitsForPtyhostAndShell(t *testing.T) {
	root := shortRoot(t)
	home := t.TempDir()
	release := filepath.Join(home, "b234-release")
	h := ptyhost.New(root, buildHandoff(t), testLog())
	sess, err := h.Open(ptyhost.OpenOptions{
		BasePath: home, BaseKind: "home", Shell: "/bin/sh",
		Env: append(os.Environ(), "HOME="+home), Cols: 80, Rows: 24,
		InitCommand: `mkfifo "$HOME/b234-release"; trap 'exit 0' TERM; trap 'cat "$HOME/b234-release" >/dev/null; printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"; while :; do :; done`,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if sess.PID <= 0 {
		t.Fatalf("Open PID=%d，期望真实 ptyhost 子进程", sess.PID)
	}
	if !waitClientFile(filepath.Join(home, "b234-ready"), 3*time.Second) {
		t.Fatal("Open InitCommand 未建立 ready marker")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- h.Close(sess.ID) }()
	early := false
	select {
	case err := <-closeDone:
		early = true
		if err != nil {
			t.Fatalf("提前返回的 Close 错误: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
	}
	if early {
		releaseClientFIFO(t, release)
		if !waitClientFile(filepath.Join(home, "b234-late"), time.Second) {
			t.Fatal("提前返回的 Close 后 EXIT trap 仍未写入 late marker")
		}
		t.Fatal("Host.Close 在 EXIT trap 写入 late marker 前返回")
	}
	releaseClientFIFO(t, release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close 未等待 ptyhost 与 shell 收摊")
	}
	if _, err := os.Stat(filepath.Join(home, "b234-late")); err != nil {
		t.Fatalf("Close 返回后 late marker 不存在: %v", err)
	}
	if _, err := os.Stat(sessdir.Dir(root, sess.ID)); !os.IsNotExist(err) {
		t.Fatalf("Close 后会话目录仍存在: %v", err)
	}
	if list := h.List(); len(list) != 0 {
		t.Fatalf("Close 后 Host.List=%+v", list)
	}
}

// TestClientRegistersPtyhostForPressure 真实 hostproc 会话经 Adopt 后必须成为可验证凭据。
//
// why 不再对整机进程数取两次样断言差值恰为一：CheckAdmission 读的是**实时的整机计数**，
// 而 go test ./... 期间几十个测试进程在并发起落，两次采样之间基数就会漂
// （2026-08-20 实测 703 → 702，净漂 2，断言必挂）。扣减判据本身已由
// internal/prochost/ptyhost_pressure_test.go 用固定进程快照确定性覆盖；按那个文件
// 写明的分工，本测试只负责「真实会话产出正确凭据」这一半，不重复验计数效果。
func TestClientRegistersPtyhostForPressure(t *testing.T) {
	_, h, id, _ := startClientHost(t)
	sess, ok := h.Get(id)
	if !ok || sess.PID <= 0 {
		t.Fatalf("Get = %+v, ok=%v，期望活 ptyhost 会话", sess, ok)
	}

	credentials := prochost.PtyhostCredentials()
	idx := -1
	for i := range credentials {
		if credentials[i].PID == sess.PID {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("会话 PID %d 未出现在 ptyhost 凭据里: %+v", sess.PID, credentials)
	}
	// 启动时刻是扣减判据的另一半：PID 会被复用，PID+启动时刻才能唯一指认一个进程。
	// 少了它，prochost 那边会把复用同一 PID 的陌生进程当成 ptyhost 扣掉。
	if credentials[idx].StartedAt <= 0 {
		t.Fatalf("凭据缺少启动时刻: %+v", credentials[idx])
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
	_, h, id, done, late := startClientHostWithExitMarker(t)
	if err := h.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("Close 返回后 late marker 不存在: %v", err)
	}
	if list := h.List(); len(list) != 0 {
		t.Fatalf("Close 后 List = %+v", list)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hostproc.Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ptyhost 未在 Close 后退出")
	}
}

func TestCloseDoesNotTreatControlEOFAsSuccess(t *testing.T) {
	root := shortRoot(t)
	id := "b234-eof"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	meta := sessdir.Meta{
		ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", CreatedAt: time.Now(), PID: os.Getpid(), ProtoVersion: wire.ProtoVersion,
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			_, _, _, _ = wire.ReadFrame(conn)
			_ = conn.Close()
		}
		close(serverDone)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = sessdir.Remove(root, id)
	})
	h := ptyhost.New(root, "", testLog())
	h.Adopt([]sessdir.Entry{{ID: id, Meta: meta, State: sessdir.StateLive}})
	err = h.Close(id)
	if err == nil {
		t.Fatal("control EOF 且会话目录仍在时 Close 不得返回成功")
	}
	if !strings.Contains(err.Error(), id) && !strings.Contains(err.Error(), "超时") {
		t.Fatalf("Close 错误缺少 session/wait 上下文: %v", err)
	}
	if list := h.List(); len(list) != 0 {
		t.Fatalf("失败 Close 后登记未清除: %+v", list)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("fake control server 未消费 CtrlKill")
	}
}

func TestCloseDoesNotTreatControlTimeoutAsSuccess(t *testing.T) {
	root := shortRoot(t)
	id := "b234-timeout"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	meta := sessdir.Meta{
		ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", CreatedAt: time.Now(), PID: os.Getpid(), ProtoVersion: wire.ProtoVersion,
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			_, _, _, _ = wire.ReadFrame(conn)
			time.Sleep(1500 * time.Millisecond)
			_ = conn.Close()
		}
		close(serverDone)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = sessdir.Remove(root, id)
	})
	h := ptyhost.New(root, "", testLog())
	h.Adopt([]sessdir.Entry{{ID: id, Meta: meta, State: sessdir.StateLive}})
	err = h.Close(id)
	if err == nil {
		t.Fatal("control timeout 且会话目录仍在时 Close 不得返回成功")
	}
	if !strings.Contains(err.Error(), id) && !strings.Contains(err.Error(), "超时") {
		t.Fatalf("Close 错误缺少 session/wait 上下文: %v", err)
	}
	if list := h.List(); len(list) != 0 {
		t.Fatalf("失败 Close 后登记未清除: %+v", list)
	}
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fake control server 未完成 timeout 场景")
	}
}

func TestCloseMissingSessionLogsPhaseAndElapsed(t *testing.T) {
	var logs bytes.Buffer
	h := ptyhost.New(t.TempDir(), "", slog.New(slog.NewTextHandler(&logs, nil)))
	if err := h.Close("b234-missing"); !errors.Is(err, ptyhost.ErrNoSession) {
		t.Fatalf("Close missing session err=%v, want ErrNoSession", err)
	}
	line := logs.String()
	for _, field := range []string{"session=b234-missing", "phase=lookup", "elapsed=", "cause="} {
		if !strings.Contains(line, field) {
			t.Fatalf("Close 早期错误日志缺少 %q: %q", field, line)
		}
	}
}

func TestCloseSuccessLogsPhaseAndElapsed(t *testing.T) {
	root := shortRoot(t)
	id := "b234-success-log"
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	meta := sessdir.Meta{
		ID: id, BasePath: root, BaseKind: "workspace", Cwd: root,
		Shell: "/bin/sh", CreatedAt: time.Now(), PID: os.Getpid(), ProtoVersion: wire.ProtoVersion,
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sessdir.SockPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan struct{})
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			_, _, _, _ = wire.ReadFrame(conn)
			_ = conn.Close()
			_ = sessdir.Remove(root, id)
		}
		close(serverDone)
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = sessdir.Remove(root, id)
	})
	var logs bytes.Buffer
	h := ptyhost.New(root, "", slog.New(slog.NewTextHandler(&logs, nil)))
	h.Adopt([]sessdir.Entry{{ID: id, Meta: meta, State: sessdir.StateLive}})
	if err := h.Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("fake control server 未消费 CtrlKill")
	}
	line := logs.String()
	for _, field := range []string{"session=" + id, "pid=", "wait_path=session_dir", "phase=complete", "elapsed="} {
		if !strings.Contains(line, field) {
			t.Fatalf("Close 成功日志缺少 %q: %q", field, line)
		}
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
	return startClientHostWithSpec(t, hostproc.Spec{
		BasePath: "", BaseKind: "workspace", Cwd: "",
		Shell: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "PS1=$ "},
		Cols: 80, Rows: 24,
	})
}

func startClientHostWithExitMarker(t *testing.T) (root string, h *ptyhost.Host, id string, done chan error, late string) {
	home := t.TempDir()
	root, h, id, done = startClientHostWithSpec(t, hostproc.Spec{
		BasePath: home, BaseKind: "home", Cwd: home,
		Shell: "/bin/sh", Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
		Cols: 80, Rows: 24,
		InitCommand: `trap 'exit 0' TERM; trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"; while :; do :; done`,
	})
	return root, h, id, done, filepath.Join(home, "b234-late")
}

func startClientHostWithSpec(t *testing.T, spec hostproc.Spec) (root string, h *ptyhost.Host, id string, done chan error) {
	t.Helper()
	root = shortRoot(t)
	id = "s1"
	spec.Root = root
	spec.ID = id
	if spec.BasePath == "" {
		spec.BasePath = root
	}
	if spec.Cwd == "" {
		spec.Cwd = root
	}
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
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
	waitSessionReady(t, root, id)
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
	// 预热：Open 只给 socket 的出现留 3s（client.go 的 socketWait），而**首次** exec
	// 一个刚编出来的二进制要付页缓存冷启动、macOS 还要付首次代码签名校验的代价。
	// 并发负载下这笔开销能把 3s 吃光，于是 Open 超时、测试红——2026-08-20 实测：
	// 与全仓 go test ./... 同时跑时 6 轮里红 3 轮，错误是「等待 ptyhost socket 超时」。
	// 空跑一次把这笔代价挪到计时窗口之外。本测试要验的是 Open 的接线，不是冷启动耗时。
	// 失败忽略：预热本身不是判据，真有问题会在后面的 Open 上如实暴露。
	_ = exec.Command(path, "--version").Run()
	return path
}

// shortRoot 造一个**既短又不在包目录内**的会话根目录。
//
// 两个约束同时成立才行：
//   - 短：root 下要建 unix socket，路径有 ~104 字节上限（macOS）。t.TempDir()
//     在 macOS 上落在 /var/folders/…/T/ 那条长路径下，会把 socket 路径顶爆。
//   - 不在包目录内：曾经在当前包目录创建临时根来满足「短」，代价是临时目录出现在
//     ./... 的包枚举里，全量并发跑时偶发撞红 TestWindowsCrossCompiles（B186）。
//
// 于是交给 ptytestroot 在 /tmp 与仓库点号目录之间择位。
func shortRoot(t *testing.T) string {
	t.Helper()
	decision, err := ptytestroot.Resolve(
		ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, testLog())
	if err != nil {
		t.Skipf("PTY 测试根目录不可用: %v", err)
		return ""
	}
	t.Cleanup(decision.Cleanup)
	return decision.Root
}

// waitSessionReady 等到会话**真正**就绪：socket 与 meta.json 都在。
//
// why 不能只等 socket：hostproc 先 net.Listen（hostproc.go 的 Run）、**之后**才
// WriteMeta，socket 出现只是中途副产物。并发负载下这两步之间的窗口被拉大，只等
// socket 的调用方会读到「meta.json 不存在」——2026-08-20 全仓并行跑时
// TestClientProtoMismatch 正是这么红的（读会话元数据 ...: no such file or directory）。
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

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
