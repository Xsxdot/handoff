// fallback_verdict_test.go —— opencode 兜底分类的裁决测试（B74）。
//
// 本文件直接驱动 fallbackClassify（不起 fake server、不推 SSE），事实源是
// 真实临时 git 仓库：GitTurnStatus 真跑 git 子进程（exec.Command），桩不了，
// 用 t.TempDir() + git init 造真仓库，hasNew=true 时在回合起点后补一次提交。
package opencode

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// newAdapterWithFakeGit 造一个带真实 git 事实源的兜底分类环境。
//
// 返回：adapter、runState（repoPath=真仓库，startCommit=回合起点，session 已填）、
// 当前 HEAD 实况。hasNew=true 时 startCommit 之后已补一次提交。
//
// 为什么用真仓库而不是桩：GitTurnStatus 真跑 git 子进程（exec.Command），
// 桩不了——注入一个假的 branch/commit 等于测一个不存在的接线。initGit/gitCommit
// 复用 adapter_test.go 的既有 helper；分支名经 checkout -b 落到实处，让
// rev-parse --abbrev-ref HEAD 真能返回被测的 branch 名。
func newAdapterWithFakeGit(t *testing.T, branch string, hasNew bool) (*Adapter, *runState, string) {
	t.Helper()
	repo := initGit(t)
	gitAt(t, repo, "checkout", "-b", branch)
	startCommit := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	if hasNew {
		gitCommit(t, repo, "impl.go", "package main\n", "feat: impl")
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

// newAdapterWithGitError 造一个 git 查询必然失败的环境：repoPath 指向非 git 目录。
func newAdapterWithGitError(t *testing.T) (*Adapter, *runState) {
	t.Helper()
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &runState{
		taskID:      "T-1",
		repoPath:    t.TempDir(), // 非 git 目录：GitTurnStatus 必失败
		session:     "sess-1",
		startCommit: "",
		evCh:        make(chan executor.AdapterEvent, 8),
		stopCh:      make(chan struct{}),
	}
	return a, r
}

// drainEvents 把缓冲通道里已到达的事件全部取走（fallbackClassify 是同步调用，
// 返回后事件必已在缓冲里，无需等待）。
func drainEvents(r *runState) []executor.AdapterEvent {
	var evs []executor.AdapterEvent
	for {
		select {
		case ev := <-r.evCh:
			evs = append(evs, ev)
		default:
			return evs
		}
	}
}

func TestFallbackWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, head := newAdapterWithFakeGit(t, "handoff/T1", true /*hasNew*/)
	a.fallbackClassify(r, "我把 Task 5 的三个方案列出来了，你选哪个？")

	events := drainEvents(r)
	if len(events) != 1 {
		t.Fatalf("应恰好发一条事件，got %d", len(events))
	}
	ev := events[0]
	if ev.Type != "result" {
		t.Fatalf("有新提交时应发 result，got %q", ev.Type)
	}
	if ev.Result.OK {
		t.Fatal("无 trailer 的回合绝不能报 OK——这正是 B74 的假完成")
	}
	if ev.Result.Branch != "handoff/T1" || ev.Result.CommitHash != head {
		t.Fatalf("git 实况未留在结构化字段: branch=%q commit=%q want=%q",
			ev.Result.Branch, ev.Result.CommitHash, head)
	}
	if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
		t.Fatalf("失败原因缺判定依据: %s", ev.Result.FailReason)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
}

func TestFallbackWithoutNewCommitStillAsks(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "handoff/T1", false /*hasNew*/)
	a.fallbackClassify(r, "A/B/C 三选一，你定？")

	events := drainEvents(r)
	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}

func TestFallbackWithGitErrorStillAsks(t *testing.T) {
	a, r := newAdapterWithGitError(t)
	a.fallbackClassify(r, "有个问题要确认")

	events := drainEvents(r)
	if len(events) != 1 || events[0].Type != "question" {
		t.Fatalf("git 查询失败时应转 question（本 plan 不动这条分支），got %+v", events)
	}
}
