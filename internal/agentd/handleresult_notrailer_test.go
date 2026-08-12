// handleresult_notrailer_test.go —— handleResult 的 !OK 分支把 git 实况透传进
// failed 事件、作废理由由 result 侧提供而非硬编码（B74 的 agentd 侧落地）。
//
// 依赖 main 上已有的两块：voidTicketsWithAudit（B63）会产 tickets_voided 审计
// 事件，本文件直接断言它的 Reason 字段；newFailedPayload（B73）带 ProcUsage，
// git 实况由同一构造器带上，本文件断言它没有丢。
package agentd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
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

	ev := lastEventOfType(t, m, "t1", string(proto.EventTypeFailed))
	var p failedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Branch != "handoff/T1" {
		t.Fatalf("branch 未透传到 failed payload，got %q", p.Branch)
	}
	if p.CommitHash != "abc1234def" {
		t.Fatalf("commit 未透传到 failed payload，got %q", p.CommitHash)
	}
}

func TestFailedPayloadOmitsGitTruthWhenAbsent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t2", proto.TaskStateRunning)
	m.handleResult("t2", executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}})

	ev := lastEventOfType(t, m, "t2", string(proto.EventTypeFailed))
	raw := string(ev.Payload)
	// omitempty 必须真的生效：绝大多数 failed（崩溃、看门狗判死）没有 git 实况，
	// 空字段出现在 payload 里会让下游以为「查过 git 且分支是空」
	if strings.Contains(raw, `"branch"`) || strings.Contains(raw, `"commit"`) {
		t.Fatalf("无 git 实况时不该出现 branch/commit 字段: %s", raw)
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
	var p ticketsVoidedPayload
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
	var p ticketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonExecutorGone {
		t.Fatalf("未填时应回落到缺省理由，got %q", p.Reason)
	}
}
