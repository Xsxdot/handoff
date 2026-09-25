// handleresult_notrailer_test.go —— handleResult 的 !OK 分支把 git 实况透传进
// turn_failed 事件、作废理由由 result 侧提供而非硬编码（B74 的 agentd 侧落地；
// B100 后回合失败事件由 failed 改为 turn_failed，payload 构造器未换）。
//
// 依赖 main 上已有的两块：voidTicketsWithAudit（B63）会产 tickets_voided 审计
// 事件，本文件直接断言它的 Reason 字段；newFailedPayload（B73）带 ProcUsage，
// git 实况由同一构造器带上，本文件断言它没有丢。
package orchestration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// lastEventOfType 取任务最后一条指定类型的事件；没有则 t.Fatal。
func lastEventOfType(t *testing.T, m *Manager, taskID string, typ string) proto.Event {
	t.Helper()
	evs, err := m.st.EventsFrom(taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if string(evs[i].Type) == typ {
			return evs[i]
		}
	}
	t.Fatalf("未找到 %s 事件，共 %d 条", typ, len(evs))
	return proto.Event{}
}

func TestFailedPayloadCarriesGitTruth(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t1", proto.TaskStateRunning)
	m.handleResult("t1", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, Branch: "handoff/T1", CommitHash: "abc1234def",
		FailReason: "回合结束但未输出协议 trailer；git 实况 handoff/T1@abc1234；回合末尾：干完了",
		VoidReason: executor.VoidReasonTurnDiscipline,
	}})

	ev := lastEventOfType(t, m, "t1", string(proto.EventTypeTurnFailed))
	var p FailedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Branch != "handoff/T1" {
		t.Fatalf("branch 未透传到 turn_failed payload，got %q", p.Branch)
	}
	if p.CommitHash != "abc1234def" {
		t.Fatalf("commit 未透传到 turn_failed payload，got %q", p.CommitHash)
	}
}

func TestCompletedPayloadCarriesFinalTextAsOptionalField(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	createRunningTask(t, st, "final-text")
	finalText := "审阅正文\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"
	m.handleResult("final-text", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: "handoff/B176", CommitHash: "abc", Summary: "简短摘要", FinalText: finalText,
	}})

	ev := lastEventOfType(t, m, "final-text", string(proto.EventTypeCompleted))
	var payload CompletedPayload
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.FinalText == nil || *payload.FinalText != finalText {
		t.Fatalf("completed payload 未保全 final_text: %+v", payload)
	}
	if payload.Summary != "简短摘要" || payload.Branch != "handoff/B176" || payload.CommitHash != "abc" {
		t.Fatalf("新增字段不应改变既有 payload: %+v", payload)
	}

	createRunningTask(t, st, "legacy-result")
	m.handleResult("legacy-result", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: "handoff/legacy", CommitHash: "def", Summary: "旧消费者摘要",
	}})
	legacy := lastEventOfType(t, m, "legacy-result", string(proto.EventTypeCompleted))
	if strings.Contains(string(legacy.Payload), `"final_text"`) {
		t.Fatalf("无正文时新增字段必须省略，保持 additive optional: %s", legacy.Payload)
	}
}

func TestFailedPayloadOmitsGitTruthWhenAbsent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t2", proto.TaskStateRunning)
	m.handleResult("t2", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})

	ev := lastEventOfType(t, m, "t2", string(proto.EventTypeTurnFailed))
	raw := string(ev.Payload)
	// omitempty 必须真的生效：绝大多数回合失败（崩溃、看门狗判死）没有 git 实况，
	// 空字段出现在 payload 里会让下游以为「查过 git 且分支是空」
	if strings.Contains(raw, `"branch"`) || strings.Contains(raw, `"commit"`) {
		t.Fatalf("无 git 实况时不该出现 branch/commit 字段: %s", raw)
	}
}

// TestFailedPayloadCarriesFailureClassAdditively 锁 B402：failure_class 是
// Result → turn_failed payload 透传的 additive/omitempty 字段；有字段精确保留，
// 无字段时旧 wire 形状一字不变（旧消费者按缺字段 fail-closed）。
func TestFailedPayloadCarriesFailureClassAdditively(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t-fc", proto.TaskStateRunning)
	m.handleResult("t-fc", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK:           false,
		FailReason:   "供应商文案随便写",
		FailureClass: proto.FailureClassZeroText,
	}})
	ev := lastEventOfType(t, m, "t-fc", string(proto.EventTypeTurnFailed))
	var p FailedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.FailureClass != proto.FailureClassZeroText {
		t.Fatalf("failure_class 未从 Result 透传到 turn_failed payload: %q", p.FailureClass)
	}

	createRunningTask(t, st, "t-fc-legacy")
	m.handleResult("t-fc-legacy", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})
	legacy := lastEventOfType(t, m, "t-fc-legacy", string(proto.EventTypeTurnFailed))
	if strings.Contains(string(legacy.Payload), "failure_class") {
		t.Fatalf("未分类时不该出现 failure_class 字段（旧 wire 形状）: %s", legacy.Payload)
	}
}

func TestVoidReasonComesFromResultNotHardcoded(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t3", proto.TaskStateRunning)
	m.handleResult("t3", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "回合结束但未输出协议 trailer",
		VoidReason: executor.VoidReasonTurnDiscipline,
	}})

	ev := lastEventOfType(t, m, "t3", string(proto.EventTypeTicketsVoided))
	var p TicketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonTurnDiscipline {
		t.Fatalf("作废理由被硬编码覆盖，got %q", p.Reason)
	}
	if strings.Contains(p.Reason, "已终结") {
		t.Fatal("executor 还活着，审计不得记它已终结")
	}
}

func TestVoidReasonDefaultsToExecutorGone(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t4", proto.TaskStateRunning)
	// 绝大多数失败路径不填 VoidReason（进程退出、看门狗判死确实是 executor 没了）
	m.handleResult("t4", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})

	ev := lastEventOfType(t, m, "t4", string(proto.EventTypeTicketsVoided))
	var p TicketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonExecutorGone {
		t.Fatalf("未填时应回落到缺省理由，got %q", p.Reason)
	}
}

// TestFailedEventTerminalCarriesNoFailureClass 锁 B402：failed 终态（Stop /
// 对账，manager.go 用字面 "" 构造）不得带 failure_class——只有 turn_failed 的
// 明确零文本分支才可自动续接（contract §3-10 / §4）。
func TestFailedEventTerminalCarriesNoFailureClass(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t-failed-fc", proto.TaskStateRunning)
	if _, err := m.Stop(context.Background(), "t-failed-fc"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ev := lastEventOfType(t, m, "t-failed-fc", string(proto.EventTypeFailed))
	if strings.Contains(string(ev.Payload), "failure_class") {
		t.Fatalf("failed 终态不得带 failure_class: %s", ev.Payload)
	}
}
