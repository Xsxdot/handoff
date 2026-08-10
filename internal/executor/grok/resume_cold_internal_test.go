package grok

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

// quietLogger 返回丢弃所有输出的 logger（供不依赖 t 的纯函数级用例直接构造）。
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// writeDeadServeInfo 写一个指向必然探不活端口的恢复凭据（proc.json）。
func writeDeadServeInfo(t *testing.T, dir string) {
	t.Helper()
	if err := writeProcInfo(dir, &procInfo{
		Handle: prochost.Handle{LockPath: filepath.Join(dir, lockFileName)},
		Port:   1, Secret: "x",
	}); err != nil {
		t.Fatal(err)
	}
}

// startServeFn 是 startServe 缝的类型别名（func 字面量不支持 ... 占位）。
type startServeFn = func(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error)

// swapStartServe 替换包级 startServe 执行点，返回恢复函数。
func swapStartServe(fn startServeFn) func() {
	old := startServe
	startServe = fn
	return func() { startServe = old }
}

// TestResumeColdDisallowedStaysDead Cold=false 时进程已死即判不可恢复（启动恢复语义不变）。
func TestResumeColdDisallowedStaysDead(t *testing.T) {
	// 造一个 serve.json 指向一个必然探不活的端口
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	a := New(quietLogger())
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: t.TempDir(), SessionID: "sess-1", Cold: false})
	if err != nil {
		t.Fatal(err)
	}
	if out.Alive {
		t.Fatalf("Cold=false 且进程已死应判不可恢复")
	}
}

// TestResumeColdRestartFailureIsNotAnError 冷恢复起不来是可预期现场
// （配额耗尽、凭据过期），按不可恢复处理而非程序错误——返回 error 会让
// manager 侧的日志把它当故障刷 Error，掩盖真正的程序缺陷。
func TestResumeColdRestartFailureIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	restore := swapStartServe(func(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error) {
		return nil, errors.New("配额耗尽")
	})
	defer restore()

	a := New(quietLogger())
	out, err := a.Resume(executor.ResumeReq{TaskID: "t1", TaskDir: dir,
		RepoPath: t.TempDir(), SessionID: "sess-1", Cold: true})
	if err != nil {
		t.Fatalf("起不来不应返回 error，应判不可恢复: %v", err)
	}
	if out.Alive {
		t.Fatalf("起不来应 Alive=false")
	}
	if out.Note == "" {
		t.Fatalf("Note 必须写清为什么恢复不了，审核者要看到这句")
	}
}

// TestResumeColdMutualExclusion 并发两次冷恢复只允许一次真的去拉进程——
// 两个 serve 抢同一个会话是数据损坏级别的后果。
func TestResumeColdMutualExclusion(t *testing.T) {
	var starts int32
	restore := swapStartServe(func(ctx context.Context, repoPath, taskID, taskDir, model string, env []string, log *slog.Logger) (*Proc, error) {
		atomic.AddInt32(&starts, 1)
		time.Sleep(50 * time.Millisecond) // 拉长窗口，让第二个必然撞进来
		return nil, errors.New("测试不真起进程")
	})
	defer restore()

	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	a := New(quietLogger())
	req := executor.ResumeReq{TaskID: "t1", TaskDir: dir, RepoPath: t.TempDir(),
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
