//go:build unix

// ptyreclaim_test.go —— agentd 启动认领与三态目录决策的测试。
//
// 职责：验证 live/dead/broken 与缺失根目录四条启动边界，以及日志可审计性。
// 边界：不启动真实 shell、不测试 socket 帧；Host 只接收登记结果，进程生命周期由
// sessdir 的文件锁在测试中模拟。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestPtyReclaimLiveAdopts(t *testing.T) {
	requireLockSupport(t)
	root := t.TempDir()
	meta := reclaimMeta("live")
	if err := sessdir.Create(root, meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	lock, err := prochost.AcquireLock(sessdir.LockPath(root, meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	list := s.pty.List()
	if len(list) != 1 || list[0].ID != meta.ID || list[0].BasePath != meta.BasePath {
		t.Fatalf("List = %+v，期望登记 live 会话", list)
	}
	if _, err := os.Stat(sessdir.Dir(root, meta.ID)); err != nil {
		t.Fatalf("live 目录不应删除: %v", err)
	}
}

func TestPtyReclaimDeadRemoves(t *testing.T) {
	root := t.TempDir()
	meta := reclaimMeta("dead")
	if err := sessdir.Create(root, meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessdir.Dir(root, meta.ID)); !os.IsNotExist(err) {
		t.Fatalf("dead 目录应删除，stat err=%v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("dead 会话不应登记: %+v", list)
	}
}

func TestPtyReclaimBrokenLeavesAndLogs(t *testing.T) {
	requireLockSupport(t)
	root := t.TempDir()
	if err := sessdir.Create(root, "broken"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessdir.MetaPath(root, "broken"), []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := prochost.AcquireLock(sessdir.LockPath(root, "broken"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	s, logs := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessdir.Dir(root, "broken")); err != nil {
		t.Fatalf("broken 目录不应删除: %v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("broken 会话不应登记: %+v", list)
	}
	if !bytes.Contains(logs.Bytes(), []byte("PTY 会话目录异常")) {
		t.Fatalf("日志缺少 broken Error: %s", logs.String())
	}
}

func TestPtyReclaimMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "never-created")
	s, _ := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatalf("首次启动根目录不存在不应报错: %v", err)
	}
	if list := s.pty.List(); len(list) != 0 {
		t.Fatalf("List = %+v，期望空", list)
	}
}

// TestGracefulShutdownKeepsPtySession 优雅 Trigger 与信号共用的关停路径不得杀 PTY。
//
// 不起真实 hostproc：reclaim 脚手架已经把一个 live 会话登记进 Host；若通用 cleanup
// 错误调用 ShutdownPtySessions，假 socket 会让 Close 失败并留下可审计的停止警告，
// 因此同时断言会话仍登记且没有进入 Close 失败分支。
func TestGracefulShutdownKeepsPtySession(t *testing.T) {
	requireLockSupport(t)
	root := t.TempDir()
	meta := reclaimMeta("graceful")
	if err := sessdir.Create(root, meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := sessdir.WriteMeta(root, meta); err != nil {
		t.Fatal(err)
	}
	lock, err := prochost.AcquireLock(sessdir.LockPath(root, meta.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	s, logs := reclaimServer(root)
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: http.NewServeMux()}
	sd := NewShutdown(quietLogger())
	wdCanceled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- sd.serveWithListeners([]net.Listener{ln}, httpSrv,
			s.GracefulShutdownCleanup(func() { close(wdCanceled) }))
	}()
	waitListening(t, ln.Addr().String())
	sd.Trigger("update:v-next")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关停应返回 nil: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("优雅关停未返回")
	}
	select {
	case <-wdCanceled:
	default:
		t.Fatal("优雅关停应取消 watchdog")
	}
	if list := s.pty.List(); len(list) != 1 || list[0].ID != meta.ID {
		t.Fatalf("优雅关停后 PTY 会话不应被移除: %+v", list)
	}
	if bytes.Contains(logs.Bytes(), []byte("停止 PTY 会话失败")) {
		t.Fatalf("优雅关停不应进入 PTY Close 路径: %s", logs.String())
	}
}

func waitReclaimFile(t *testing.T, path string) {
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

func startReclaimablePty(t *testing.T, s *Server, id string) (late string, done <-chan error) {
	t.Helper()
	root := s.ptyRoot()
	home := t.TempDir()
	late = filepath.Join(home, "b234-late")
	if err := sessdir.Create(root, id); err != nil {
		t.Fatal(err)
	}
	spec := hostproc.Spec{
		Root: root, ID: id, BasePath: home, BaseKind: "home", Cwd: home,
		Shell: "/bin/sh", Env: []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm-256color"},
		Cols: 80, Rows: 24,
		InitCommand: `trap 'exit 0' TERM; trap 'printf late > "$HOME/b234-late"' EXIT; : > "$HOME/b234-ready"; while :; do :; done`,
	}
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(sessdir.Dir(root, id), "spec.json")
	if err := os.WriteFile(specPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- hostproc.Run(specPath) }()
	waitReclaimFile(t, filepath.Join(home, "b234-ready"))
	entries, err := sessdir.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reclaimPtySessions(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.ID == id && entry.State == sessdir.StateLive {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Scan 未得到 live 会话: %+v", entries)
	}
	if _, ok := s.pty.Get(id); !ok {
		t.Fatalf("reclaimPtySessions 后 Host 未登记 %s", id)
	}
	return late, result
}

func TestReclaimedPtyDeleteWaitsForShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	late, done := startReclaimablePty(t, env.srv, "b234-delete")
	req, err := http.NewRequest(http.MethodDelete, env.ts.URL+"/api/pty/sessions/b234-delete", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status=%d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("DELETE 返回后 late marker 不存在: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hostproc.Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reclaimed hostproc 未退出")
	}
	if list := env.srv.pty.List(); len(list) != 0 {
		t.Fatalf("DELETE 后 Host.List=%+v", list)
	}
}

func TestShutdownPtySessionsWaitsForReclaimedShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上 PTY 不支持")
	}
	env := newTestAgentdEnv(t)
	late, done := startReclaimablePty(t, env.srv, "b234-shutdown")
	env.srv.shutdownPtySessions(context.Background())
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("shutdownPtySessions 返回后 late marker 不存在: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hostproc.Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownPtySessions 后 hostproc 未退出")
	}
	if list := env.srv.pty.List(); len(list) != 0 {
		t.Fatalf("shutdownPtySessions 后 Host.List=%+v", list)
	}
}

func reclaimServer(root string) (*Server, *bytes.Buffer) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &Server{pty: ptyhost.New(root, "", log), ptyRootPath: root, log: log}, &logs
}

func reclaimMeta(id string) sessdir.Meta {
	return sessdir.Meta{ID: id, BasePath: "/repo/a", BaseKind: "workspace", Cwd: "/repo/a",
		Shell: "/bin/sh", PID: 4242, ProtoVersion: 1}
}

func requireLockSupport(t *testing.T) {
	t.Helper()
	if !prochost.LockSupported() {
		t.Skip("本平台不支持文件锁，判活语义不成立")
	}
}
