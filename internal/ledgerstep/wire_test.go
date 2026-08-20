package ledgerstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestFinalMessageFromEventsUsesProtocolFields(t *testing.T) {
	events := []proto.Event{
		{Seq: 1, Type: proto.EventTypeProgress, Payload: json.RawMessage(`{"text":"working"}`)},
		{Seq: 2, Type: proto.EventTypeCompleted, Payload: json.RawMessage(`{"branch":"cards/B1-implement","commit":"abc","summary":"最终审阅报文"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "最终审阅报文" {
		t.Fatalf("completed summary: %v %q", err, message)
	}

	events = []proto.Event{{Seq: 3, Type: proto.EventTypeTurnFailed,
		Payload: json.RawMessage(`{"fail_reason":"回合失败原文"}`)}}
	message, err = finalMessageFromEvents(events)
	if err != nil || message != "回合失败原文" {
		t.Fatalf("turn_failed reason: %v %q", err, message)
	}

	if _, err := finalMessageFromEvents([]proto.Event{{Type: proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"branch":"x"}`)}}); err == nil {
		t.Fatal("缺最终文本应报错")
	}
}

func TestTaskBranchReadsLatestDispatchSnapshot(t *testing.T) {
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(ledger.NewCard{Title: "task branch", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDispatch(card.ID, ledger.DispatchSnapshot{Branch: "cards/old", Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDispatch(card.ID, ledger.DispatchSnapshot{Branch: "cards/latest", Actor: "test"}); err != nil {
		t.Fatal(err)
	}
	branch, err := taskBranch(s, card)
	if err != nil || branch != "cards/latest" {
		t.Fatalf("latest branch: %v %q", err, branch)
	}
}

// codex 收尾会在 completed 之后再补一条 turn_failed（app-server 断连），
// 那是传输层假警报。取报文必须认 completed——否则环节拿到的是断连文案，
// 裁决必然解析失败、每次审阅都白白转人工。2026-08-19 真机实测踩中。
func TestFinalMessagePrefersCompletedOverTrailingTurnFailed(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"审阅中"}`)},
		{Type: proto.EventTypeCompleted, Payload: []byte(`{"summary":"审阅结论\n` + "```" + `handoff-verdict\n{\"verdict\":\"pass\"}\n` + "```" + `"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"codex 连接断开: EOF"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil {
		t.Fatalf("取报文: %v", err)
	}
	if !strings.Contains(message, "handoff-verdict") {
		t.Fatalf("应取 completed 的报文，实得: %q", message)
	}
}

// 真失败（没有 completed）仍要拿到失败原文，交上游转人工。
func TestFinalMessageFallsBackToFailure(t *testing.T) {
	events := []proto.Event{
		{Type: proto.EventTypeProgress, Payload: []byte(`{"text":"起手"}`)},
		{Type: proto.EventTypeTurnFailed, Payload: []byte(`{"fail_reason":"模型 400"}`)},
	}
	message, err := finalMessageFromEvents(events)
	if err != nil || message != "模型 400" {
		t.Fatalf("失败回退: %v %q", err, message)
	}
}

// 环节等的必须是「回合终态」而不是「首个可动作事件」：审阅同样要过权限
// 门、也可能发工单，醒在这些事件上就去取报文必然报「没有最终报文」。
func TestWaitForTurnEndSkipsNonTerminalEvents(t *testing.T) {
	seq := []proto.EventType{
		proto.EventTypePermissionRequest,
		proto.EventTypeQuestion,
		proto.EventTypePermissionRequest,
		proto.EventTypeCompleted,
	}
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		ev := &proto.Event{Type: seq[calls]}
		calls++
		return ev, nil
	})
	if err != nil {
		t.Fatalf("等终态: %v", err)
	}
	if calls != len(seq) {
		t.Fatalf("应一直等到 completed（%d 次），实际 %d 次", len(seq), calls)
	}
}

// turn_failed 也是回合终态：executor 还活着，但这一回合结束了，报文在
// fail_reason 里——不能继续等下去把环节挂死。
func TestWaitForTurnEndAcceptsTurnFailed(t *testing.T) {
	calls := 0
	err := waitForTurnEnd(context.Background(), func(context.Context) (*proto.Event, error) {
		calls++
		return &proto.Event{Type: proto.EventTypeTurnFailed}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("turn_failed 应立即收口: err=%v calls=%d", err, calls)
	}
}

// TestMergeScriptUsesDetachedWorktree D3：合并不得 checkout 主工作区。
// 落点必须是 origin/<基线> 且带 --detach——两条缺一：
//   - 不 detach：主工作区正停在基线分支上时，git 拒绝同一分支被两处 checkout
//   - 用本地基线名：本地那份可能陈旧，合并起点就错了
func TestMergeScriptUsesDetachedWorktree(t *testing.T) {
	script := mergeScript("feat/x", "integration/y")
	if strings.Contains(script, "git checkout") {
		t.Fatalf("合并脚本不得 checkout 主工作区：\n%s", script)
	}
	if !strings.Contains(script, "git worktree add --detach") {
		t.Fatalf("必须用 --detach 建临时 worktree：\n%s", script)
	}
	if !strings.Contains(script, "'origin/integration/y'") {
		t.Fatalf("临时 worktree 落点必须是 origin/<基线>：\n%s", script)
	}
	if !strings.Contains(script, `trap 'git worktree remove --force "$tmp"' EXIT`) {
		t.Fatalf("必须有 trap 清理，否则失败路径会留残骸：\n%s", script)
	}
}

// TestMergeScriptPushesBothRefs D1 + D4：先补工作分支，最后推基线。
func TestMergeScriptPushesBothRefs(t *testing.T) {
	script := mergeScript("feat/x", "integration/y")
	if !strings.Contains(script, "git push origin 'feat/x':'feat/x'") {
		t.Fatalf("缺工作分支补齐（D1）：\n%s", script)
	}
	if !strings.Contains(script, "git push origin HEAD:'integration/y'") {
		t.Fatalf("缺基线推送（D4），且必须用 HEAD: 显式 refspec：\n%s", script)
	}
	// 红线是「推送不得强推」，不是「脚本里不许出现 --force」——trap 里的
	// git worktree remove --force 是清理临时目录所必需（目录有改动时不带
	// --force 会被拒，清理失败会留下残骸）。所以按行判，只盯 git push。
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, "git push") {
			continue
		}
		if strings.Contains(line, "--force") || strings.Contains(line, "--force-with-lease") || strings.Contains(line, " -f ") {
			t.Fatalf("推送不得强推：%s", line)
		}
	}
}

// TestObjectiveScriptSyncsWorkBranch D1 的另一半：客观判据同样从 origin 取
// 工作分支，同样要先补齐，否则它比合并更早撞 couldn't find remote ref。
func TestObjectiveScriptSyncsWorkBranch(t *testing.T) {
	script := objectiveScript("feat/x", "integration/y")
	if !strings.Contains(script, "git push origin 'feat/x':'feat/x'") {
		t.Fatalf("客观判据脚本缺工作分支补齐：\n%s", script)
	}
	if !strings.Contains(script, workBranchMissingMarker) {
		t.Fatalf("客观判据脚本缺缺失阶梯：\n%s", script)
	}
}
