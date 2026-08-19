package opencode

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

// swapStartServeForTest 替换包级 startServe 执行点，返回恢复函数。
func swapStartServeForTest(fn func(ctx context.Context, repoPath, taskID, markRoot, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error)) func() {
	old := startServe
	startServe = fn
	return func() { startServe = old }
}

// coldRestoreServer 是冷恢复测试用的 fake serve：GET /session 返回可控会话列表，
// POST /session 返回新 id。探活（/ 根路径）返回 200。
func coldRestoreServer(t *testing.T, sessions []string, newID string) (*httptest.Server, int) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/session" && r.Method == http.MethodGet:
			list := make([]map[string]any, 0, len(sessions))
			for _, s := range sessions {
				list = append(list, map[string]any{"id": s})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(list)
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"id": newID})
		case r.URL.Path == "/event":
			// SSE 订阅：测试只验 Resume 的返回，订阅循环保持连接即可
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	return ts, port
}

// TestResumeColdVerifiesSessionStillExists 冷恢复必须确认原会话仍在 serve 的
// 会话列表里才算 cold；不在就得降级 fresh 并如实播报，不能默认它还在。
func TestResumeColdVerifiesSessionStillExists(t *testing.T) {
	quietLog(t)
	ts, port := coldRestoreServer(t, []string{"other-session"}, "sess-new")
	defer ts.Close()

	dir := t.TempDir()
	repo := t.TempDir()
	// proc.json 指向一个必然探不活的旧端口与空锁（Alive=false），触发冷恢复
	if err := writeProcInfo(dir, &procInfo{Handle: prochost.Handle{LockPath: filepath.Join(dir, "proc.lock")}, Port: 1, Password: "p"}); err != nil {
		t.Fatal(err)
	}
	restore := swapStartServeForTest(func(ctx context.Context, repoPath, taskID, markRoot, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error) {
		return &Proc{Handle: prochost.Handle{PID: 1234, LockPath: filepath.Join(taskDir, "proc.lock")}, Port: port, Password: "p"}, nil
	})
	defer restore()
	a := New(quietLogger())

	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: repo, SessionID: "gone-session", Cold: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != executor.ResumeModeFresh {
		t.Fatalf("原会话已不在应降级 fresh，实际 %s", out.Mode)
	}
	if out.SessionID == "gone-session" || out.SessionID == "" {
		t.Fatalf("fresh 必须返回新会话 id 供 manager 落库，实际 %q", out.SessionID)
	}
}

// TestResumeColdKeepsSessionWhenPresent 原会话仍在 → Mode=cold，会话 id 不变。
func TestResumeColdKeepsSessionWhenPresent(t *testing.T) {
	quietLog(t)
	ts, port := coldRestoreServer(t, []string{"sess-1"}, "sess-new")
	defer ts.Close()

	dir := t.TempDir()
	repo := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{Handle: prochost.Handle{LockPath: filepath.Join(dir, "proc.lock")}, Port: 1, Password: "p"}); err != nil {
		t.Fatal(err)
	}
	restore := swapStartServeForTest(func(ctx context.Context, repoPath, taskID, markRoot, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error) {
		return &Proc{Handle: prochost.Handle{PID: 1234, LockPath: filepath.Join(taskDir, "proc.lock")}, Port: port, Password: "p"}, nil
	})
	defer restore()
	a := New(quietLogger())

	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: repo, SessionID: "sess-1", Cold: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != executor.ResumeModeCold {
		t.Fatalf("原会话仍在应 Mode=cold，实际 %s", out.Mode)
	}
	if out.SessionID != "sess-1" {
		t.Fatalf("cold 会话 id 应保持不变，实际 %q", out.SessionID)
	}
}

// TestResumeColdMutualExclusion 并发两次冷恢复只允许一次真的去拉进程——
// 两个 serve 抢同一个会话是数据损坏级别的后果（spec §6 约束 1）。
func TestResumeColdMutualExclusion(t *testing.T) {
	quietLog(t)
	var starts int32
	restore := swapStartServeForTest(func(ctx context.Context, repoPath, taskID, markRoot, taskDir, configPath string, env []string, log *slog.Logger) (*Proc, error) {
		atomic.AddInt32(&starts, 1)
		time.Sleep(50 * time.Millisecond) // 拉长窗口，让第二个必然撞进来
		return &Proc{Handle: prochost.Handle{PID: 1234, LockPath: filepath.Join(taskDir, "proc.lock")}, Port: 1, Password: "p"}, nil
	})
	defer restore()

	dir := t.TempDir()
	repo := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{Handle: prochost.Handle{LockPath: filepath.Join(dir, "proc.lock")}, Port: 1, Password: "p"}); err != nil {
		t.Fatal(err)
	}
	a := New(quietLogger())
	req := executor.ResumeReq{TaskID: "t1", TaskDir: dir, RepoPath: repo,
		SessionID: "sess-1", Cold: true}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a.Resume(req) }()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&starts); n != 1 {
		t.Fatalf("并发冷恢复应只拉起一次进程，实际 %d 次", n)
	}
}
