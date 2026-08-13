// fallback_verdict_test.go —— grok finishTurn 兜底分支的裁决测试（B74）。
//
// 兜底嵌在 finishTurn 的 switch kind default 分支里，测试直接驱动 finishTurn：
// 事实源是真实临时 git 仓库（GitTurnStatus 真跑 git 子进程，桩不了），
// hasNew=true 时在回合起点后补一次提交。askedViaTool 抑制与零文本守卫两道闸
// 各有一条回归护栏——它们在本 plan 里一行不动。
package grok

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// gitAt 在 dir 里执行 git 命令，失败即 Fatal（测试环境问题，不是被测行为）。
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initGitRepo 造一个带初始提交的干净仓库（main 分支），返回仓库路径。
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitAt(t, dir, "init", "-q")
	gitAt(t, dir, "checkout", "-b", "main")
	gitAt(t, dir, "config", "user.email", "test@handoff.dev")
	gitAt(t, dir, "config", "user.name", "handoff test")
	if err := os.WriteFile(dir+"/.keep", []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// gitCommitRepo 在 repo 里提交一个新文件，模拟「executor 干了活」。
func gitCommitRepo(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", msg)
}

// newAdapterWithFakeGit 造一个带真实 git 事实源的回合收尾环境。
//
// 返回：adapter、runState（repoPath=真仓库，startCommit=回合起点，sessionID 已填）、
// 当前 HEAD 实况。hasNew=true 时 startCommit 之后已补一次提交。
func newAdapterWithFakeGit(t *testing.T, branch string, hasNew bool) (*Adapter, *runState, string) {
	t.Helper()
	repo := initGitRepo(t)
	gitAt(t, repo, "checkout", "-b", branch)
	startCommit := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	if hasNew {
		gitCommitRepo(t, repo, "impl.go", "package main\n", "feat: impl")
	}
	head := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	a := New(nil)
	r := &runState{taskID: "T-1", sessionID: "sess-1", repoPath: repo,
		startCommit: startCommit,
		evCh:        make(chan executor.AdapterEvent, 8),
		acc:         newTurnAccumulator(), pending: map[string]pendingPerm{}}
	return a, r, head
}

// appendTurnText 把一段文本灌进回合累积器（模拟模型流式输出正文）。
func appendTurnText(r *runState, text string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.acc.feedRaw([]byte(`{"jsonrpc":"2.0","method":"session/update","params":` +
		`{"update":{"sessionUpdate":"agent_message_chunk","content":` +
		`{"type":"text","text":` + mustJSONString(text) + `}}}}`))
}

// okOutcome 构造 stopReason=end_turn 的收尾 outcome（否则走更早的「回合非正常收尾」分支）。
func okOutcome() ACPResult {
	return ACPResult{Result: json.RawMessage(`{"stopReason":"end_turn"}`)}
}

// lastEvent 取事件通道里最后一条事件；没有则 t.Fatal。
func lastEvent(t *testing.T, r *runState) executor.AdapterEvent {
	t.Helper()
	var last executor.AdapterEvent
	for {
		select {
		case ev := <-r.evCh:
			last = ev
		default:
			if last.Type == "" {
				t.Fatal("事件通道里没有事件")
			}
			return last
		}
	}
}

func TestNoTrailerWithNewCommitDoesNotDeclareCompletion(t *testing.T) {
	a, r, head := newAdapterWithFakeGit(t, "handoff/T1", true /*hasNew*/)
	appendTurnText(r, "我把 Task 5 的三个方案列出来了，你选哪个？")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, r)
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
	// 旧实现那句固定文案必须消失：它把 git 实况说成了完成的依据
	if strings.Contains(ev.Result.Summary, "按 git 新提交判定完成") {
		t.Fatalf("旧固定文案仍在: %q", ev.Result.Summary)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由不对: %q", ev.Result.VoidReason)
	}
}

func TestNoTrailerAskedViaToolStillSuppresses(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "handoff/T1", false /*hasNew*/)
	appendTurnText(r, "已调用一次提问工具；本回合结束。")
	r.noteAskedViaTool()
	a.finishTurn(r, okOutcome())

	// 本 plan 不动这条：已走工具提问时兜底闭嘴，不补第二张工单
	for {
		select {
		case ev := <-r.evCh:
			if ev.Type == "question" {
				t.Fatalf("已走工具提问时不该补工单，却发了 %+v", ev)
			}
		default:
			return
		}
	}
}

func TestNoTrailerZeroTextStillFailsWithLiveExecutor(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "handoff/T1", false /*hasNew*/)
	appendTurnText(r, "   \n ")
	a.finishTurn(r, okOutcome())

	ev := lastEvent(t, r)
	if ev.Type != "result" || ev.Result.OK {
		t.Fatalf("零文本应发失败 result，got %+v", ev)
	}
	if ev.Result.VoidReason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("零文本时 executor 还活着，作废理由应为纪律类，got %q",
			ev.Result.VoidReason)
	}
}

func TestNoTrailerWithoutNewCommitStillAsks(t *testing.T) {
	a, r, _ := newAdapterWithFakeGit(t, "handoff/T1", false /*hasNew*/)
	appendTurnText(r, "A/B/C 三选一，你定？")
	a.finishTurn(r, okOutcome())

	if ev := lastEvent(t, r); ev.Type != "question" {
		t.Fatalf("无新提交时应转 question（本 plan 不动这条分支），got %q", ev.Type)
	}
}
