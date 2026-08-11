// backlog_internal_test.go —— computeBacklog 的计算契约测试。
//
// 职责：钉住「三个计数各自独立算、不做减法」「seq 全局不连续」「截断判据」三条。
// 边界：不涉及网络与 cursor 落盘，那些在 follow_test.go 里覆盖。
package client

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// permEv 构造一条带 ticket_id 的 permission_request 事件。
func permEv(seq int64, ticketID string) proto.Event {
	p, _ := json.Marshal(map[string]string{"ticket_id": ticketID})
	return proto.Event{Seq: seq, TaskID: "t1", Type: proto.EventTypePermissionRequest, Payload: p}
}

func TestComputeBacklogNoBacklog(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{permEv(100, "a")}}
	if got := computeBacklog("t1", 100, snap); got != nil {
		t.Fatalf("水位等于 cursor 时应无积压，got %+v", got)
	}
	if got := computeBacklog("t1", 100, &AttachInfo{}); got != nil {
		t.Fatalf("空事件窗口应无积压，got %+v", got)
	}
}

// TestComputeBacklogCountsIndependently 钉住 spec §3.2：三个计数各自独立。
// 夹具刻意让「断网前就欠着的老工单」（seq 90 ≤ cursor 100）出现在 actionable 里，
// 若实现写成 stale = missed - len(actionable) 会算错。
func TestComputeBacklogCountsIndependently(t *testing.T) {
	snap := &AttachInfo{
		Task: proto.TaskView{Task: proto.Task{State: proto.TaskStateWaitingAnswer}},
		PendingTickets: []proto.Ticket{
			{ID: "old", Kind: "gate"},  // seq 90，断网前就欠着
			{ID: "new1", Kind: "gate"}, // seq 109，间隙内且仍待处置
		},
		RecentEvents: []proto.Event{
			permEv(90, "old"),
			permEv(104, "done1"), // 已被审批链答掉
			permEv(109, "new1"),
			permEv(117, "done2"), // 已被审批链答掉
		},
	}
	got := computeBacklog("t1", 100, snap)
	if got == nil {
		t.Fatal("有积压却返回 nil")
	}
	if got.Missed != 3 {
		t.Fatalf("missed = %d, want 3（seq 104/109/117；90 在 cursor 之前）", got.Missed)
	}
	if got.Stale != 2 {
		t.Fatalf("stale = %d, want 2（done1/done2 已不在 pending）", got.Stale)
	}
	if len(got.Actionable) != 2 {
		t.Fatalf("actionable = %d, want 2（pending_tickets 全量，含断网前的 old）", len(got.Actionable))
	}
}

// TestComputeBacklogGlobalSeqNotContiguous 钉住 spec §3.3：seq 是全局
// AUTOINCREMENT，跨任务共享，ToSeq-FromSeq 不是条数。
func TestComputeBacklogGlobalSeqNotContiguous(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{
		{Seq: 90, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 104, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 109, TaskID: "t1", Type: proto.EventTypeStalled},
		{Seq: 117, TaskID: "t1", Type: proto.EventTypeCompleted},
	}}
	got := computeBacklog("t1", 100, snap)
	if got.Missed != 3 {
		t.Fatalf("missed = %d, want 3（不是 ToSeq-FromSeq=17）", got.Missed)
	}
	if got.ToSeq != 117 || got.FromSeq != 100 {
		t.Fatalf("区间 = [%d,%d], want [100,117]", got.FromSeq, got.ToSeq)
	}
}

// TestComputeBacklogSkipsAuditEvents 验证摘要计数与流过滤同口径。
func TestComputeBacklogSkipsAuditEvents(t *testing.T) {
	snap := &AttachInfo{RecentEvents: []proto.Event{
		{Seq: 101, TaskID: "t1", Type: proto.EventTypeProgress},
		{Seq: 102, TaskID: "t1", Type: proto.EventTypeApproverDecision},
		{Seq: 103, TaskID: "t1", Type: proto.EventTypeApproverDisabled},
		{Seq: 104, TaskID: "t1", Type: proto.EventTypeCompleted},
	}}
	if got := computeBacklog("t1", 100, snap).Missed; got != 1 {
		t.Fatalf("missed = %d, want 1（三类审计/进度事件不计）", got)
	}
}

// TestComputeBacklogTruncation 钉住 spec §3.5 的截断判据：窗口最旧一条仍晚于
// cursor ⇒ 无法证明覆盖了整个间隙 ⇒ 标 truncated。
func TestComputeBacklogTruncation(t *testing.T) {
	covered := &AttachInfo{RecentEvents: []proto.Event{permEv(100, "a"), permEv(104, "b")}}
	if computeBacklog("t1", 100, covered).MissedTruncated {
		t.Fatal("窗口最旧一条 seq=100 <= cursor，不该标截断")
	}
	truncated := &AttachInfo{RecentEvents: []proto.Event{permEv(104, "b")}}
	if !computeBacklog("t1", 100, truncated).MissedTruncated {
		t.Fatal("窗口最旧一条 seq=104 > cursor=100，必须标截断")
	}
}

// TestComputeBacklogShape 钉住线格式的两个固定值。
func TestComputeBacklogShape(t *testing.T) {
	snap := &AttachInfo{
		Task:         proto.TaskView{Task: proto.Task{State: proto.TaskStateFailed}},
		RecentEvents: []proto.Event{permEv(104, "a")},
	}
	got := computeBacklog("task-xyz", 100, snap)
	if got.Type != BacklogSummaryType {
		t.Fatalf("type = %q, want %q", got.Type, BacklogSummaryType)
	}
	if got.TaskID != "task-xyz" {
		t.Fatalf("task_id = %q, want task-xyz", got.TaskID)
	}
	if got.State != proto.TaskStateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Actionable == nil {
		t.Fatal("actionable 必须是数组而非 nil：JSON 里 null 与 [] 对消费方是两回事")
	}
}
