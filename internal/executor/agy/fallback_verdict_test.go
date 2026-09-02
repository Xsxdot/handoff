package agy

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitAt(t, dir, "init", "-q")
	gitAt(t, dir, "checkout", "-b", "main")
	gitAt(t, dir, "config", "user.email", "test@handoff.dev")
	gitAt(t, dir, "config", "user.name", "handoff test")
	if err := os.WriteFile(dir+"/.keep", []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func gitCommitRepo(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", msg)
}

func newAdapterWithFakeGit(t *testing.T, branch string, hasNew bool) (*Adapter, *runState, string) {
	t.Helper()
	repo := initGitRepo(t)
	gitAt(t, repo, "checkout", "-b", branch)
	startCommit := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	if hasNew {
		gitCommitRepo(t, repo, "impl.go", "package main\n", "feat: impl")
	}
	head := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &runState{
		taskID:      "T-1",
		repoPath:    repo,
		session:     "sess-1",
		startCommit: startCommit,
		evCh:        make(chan executor.AdapterEvent, 8),
		stopCh:      make(chan struct{}),
	}
	return a, r, head
}

func TestFallbackClassifyWithNewCommit(t *testing.T) {
	a, r, head := newAdapterWithFakeGit(t, "feat/x", true)
	a.fallbackClassify(r, "I have completed the task.")

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil {
			t.Fatalf("预期收到 result 事件，实得 %+v", ev)
		}
		if ev.Result.OK {
			t.Fatalf("B74 规范：无 trailer 有新提交必须判 OK: false，实得 %+v", ev.Result)
		}
		if ev.Result.Branch != "feat/x" || ev.Result.CommitHash != head {
			t.Fatalf("分支/commit 不符合预期: branch=%s commit=%s, want feat/x %s",
				ev.Result.Branch, ev.Result.CommitHash, head)
		}
		if !strings.Contains(ev.Result.FailReason, "相对回合起点有新提交") {
			t.Fatalf("FailReason 未正确说明有新提交: %s", ev.Result.FailReason)
		}
	default:
		t.Fatalf("未收到事件")
	}
}

func TestFallbackClassifyWithoutNewCommitWithText(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "feat/x", false)
	a.fallbackClassify(r, "Where should I start?")

	select {
	case ev := <-r.evCh:
		if ev.Type != "question" || ev.Text != "Where should I start?" {
			t.Fatalf("无新提交且有正文应转 question，实得 %+v", ev)
		}
	default:
		t.Fatalf("未收到事件")
	}
}

func TestFallbackClassifyWithoutNewCommitEmptyText(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "feat/x", false)
	a.fallbackClassify(r, "   ")

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil {
			t.Fatalf("预期收到 result 事件，实得 %+v", ev)
		}
		if ev.Result.OK {
			t.Fatalf("零文本无提交必须判失败，实得 %+v", ev.Result)
		}
		if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
			t.Fatalf("VoidReason 应为 TurnDiscipline，实得 %s", ev.Result.VoidReason)
		}
	default:
		t.Fatalf("未收到事件")
	}
}
