package codex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/codex"
)

// 没有 threadId 就没法 resume——判不可恢复，且这不是错误
func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{TaskID: "T1", TaskDir: t.TempDir()})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 threadId 时不应判活")
	}
}

// 没有 serve.json 同理
func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T2", TaskDir: t.TempDir(), SessionID: "thread-1"})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 serve.json 时不应判活")
	}
}

// 进程已死且不允许冷恢复 → 保持不可恢复，且旧 tmux 会话要先回收
func TestResumeColdDisallowedStaysDead(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	killed := make(chan string, 1)
	restore := codex.SwapTmuxKillForTest(func(s string) error { killed <- s; return nil })
	defer restore()

	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T3", TaskDir: dir, RepoPath: dir, SessionID: "thread-1", Cold: false})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Alive {
		t.Fatal("不允许冷恢复时不应判活")
	}
	if out.Note == "" {
		t.Fatal("必须给出判死原因，审核者要能看懂为什么任务没恢复")
	}
	select {
	case s := <-killed:
		if s == "" {
			t.Fatal("回收的会话名为空")
		}
	default:
		t.Fatal("冷恢复前必须先回收旧 tmux 会话，否则重起时会撞名")
	}
}

// 冷恢复时任务目录已被归档清理 → 判不可恢复，不越界重建
func TestResumeColdRefusesWhenTaskDirGone(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	gone := filepath.Join(dir, "not-exist")
	restore := codex.SwapTmuxKillForTest(func(string) error { return nil })
	defer restore()

	a := codex.New(nil)
	out, _ := a.Resume(executor.ResumeReq{
		TaskID: "T4", TaskDir: dir, RepoPath: gone, SessionID: "thread-1", Cold: true})
	if out.Alive {
		t.Fatal("工作区已不存在时不应判活（重建是 Dispatch 的职责）")
	}
}

// Reap：运行态丢失时也要能按确定性会话名兜底回收（B20）
func TestReapFallsBackToDeterministicName(t *testing.T) {
	killed := make(chan string, 1)
	restore := codex.SwapTmuxKillForTest(func(s string) error { killed <- s; return nil })
	defer restore()

	a := codex.New(nil)
	if err := a.Reap("abcdef1234", t.TempDir()); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	select {
	case s := <-killed:
		if s != "handoff-abcdef12" {
			t.Fatalf("会话名 = %s，应为 handoff-<id8>", s)
		}
	default:
		t.Fatal("Reap 必须真的尝试回收")
	}
}

func writeDeadServeInfo(t *testing.T, dir string) {
	t.Helper()
	// 端口指向一个必然连不上的地址，让 Alive() 判死
	body := `{"session":"handoff-deadbeef","task_dir":"` + dir + `","port":1}`
	if err := os.WriteFile(filepath.Join(dir, "serve.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed serve.json: %v", err)
	}
}
