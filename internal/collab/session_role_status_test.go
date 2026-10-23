package collab

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestApplySessionTaskObservationAlignsWaitingReviewWithLatestRound(t *testing.T) {
	makeDetail := func(summary string, tone string) *proto.SessionDetail {
		return &proto.SessionDetail{RoleStatus: &proto.SessionRoleStatus{Roles: []proto.SessionRole{
			{ID: "coordinator:B1", Kind: "coordinator", CardID: "B1", State: "waiting", Since: timePtr(), WaitingFor: "协调者",
				Basis: proto.SessionRoleBasis{Source: "task_mirrored", Seq: 10, TaskID: "task-1", DispatchSeq: 5, TaskType: "completed"}},
			{ID: "executor:task-1", Kind: "executor", CardID: "B1", TaskID: "task-1", Target: "worker", State: "idle", Since: timePtr(),
				Basis:  proto.SessionRoleBasis{Source: "task_mirrored", Seq: 10, TaskID: "task-1", DispatchSeq: 5, TaskType: "completed"},
				Recent: []proto.SessionRoleActivity{{Source: "task_mirrored", Summary: summary, Tone: tone}}},
		}}}
	}
	completed := makeDetail("执行回合已结束", "neutral")
	ApplySessionTaskObservations(completed, map[string]SessionTaskObservation{"task-1": {Target: "worker", State: string(proto.TaskStateWaitingReview)}})
	if got := roleByKind(t, completed, "executor").State; got != "idle" {
		t.Fatalf("completed round at waiting_review = %q, want idle", got)
	}
	if got := roleByKind(t, completed, "coordinator").State; got != "waiting" {
		t.Fatalf("coordinator after completed round = %q, want waiting", got)
	}
	if roleByKind(t, completed, "executor").Since == nil {
		t.Fatal("waiting_review snapshot must preserve the observed completed start when it preserves idle state")
	}

	failed := makeDetail("执行回合明确失败", "failed")
	for i := range failed.RoleStatus.Roles {
		if failed.RoleStatus.Roles[i].Kind == "executor" {
			failed.RoleStatus.Roles[i].Basis.TaskType = "turn_failed"
		}
	}
	ApplySessionTaskObservations(failed, map[string]SessionTaskObservation{"task-1": {Target: "worker", State: string(proto.TaskStateWaitingReview)}})
	if got := roleByKind(t, failed, "executor"); got.State != "exception" || got.Conclusion != "执行回合明确失败" {
		t.Fatalf("turn_failed must remain visible at waiting_review: %+v", got)
	}

	ApplySessionTaskObservations(failed, map[string]SessionTaskObservation{"task-1": {Target: "worker", State: string(proto.TaskStateRunning)}})
	if got := roleByKind(t, failed, "executor").State; got != "running" {
		t.Fatalf("new running snapshot should clear current failure state: %q", got)
	}
	if got := roleByKind(t, failed, "coordinator").State; got != "unknown" {
		t.Fatalf("new running snapshot should clear stale coordinator wait: %q", got)
	}
	if roleByKind(t, failed, "executor").Since != nil {
		t.Fatal("new running snapshot without a proven start must clear the old turn start")
	}
}

func TestTaskSnapshotDoesNotEraseNewerWakeOrDeliveryFailure(t *testing.T) {
	newDetail := func(taskType string, seq, coordinatorDispatch, executorDispatch int64) *proto.SessionDetail {
		return &proto.SessionDetail{RoleStatus: &proto.SessionRoleStatus{Roles: []proto.SessionRole{
			{ID: "coordinator:B1", Kind: "coordinator", CardID: "B1", State: "exception", Since: timePtr(),
				Basis: proto.SessionRoleBasis{Source: "wake_round", Seq: 30, TaskID: "task-1", DispatchSeq: coordinatorDispatch}},
			{ID: "executor:task-1", Kind: "executor", CardID: "B1", TaskID: "task-1", Target: "worker", State: "exception", Since: timePtr(),
				Basis:  proto.SessionRoleBasis{Source: "task_mirrored", Seq: seq, TaskID: "task-1", DispatchSeq: executorDispatch, TaskType: taskType},
				Recent: []proto.SessionRoleActivity{{Source: "task_mirrored", Summary: "工单应答未送达执行者: connection reset", Tone: "failed"}}},
		}}}
	}
	failure := newDetail("delivery_failed", 10, 5, 5)
	ApplySessionTaskObservations(failure, map[string]SessionTaskObservation{"task-1": {Target: "worker", State: string(proto.TaskStateWaitingAnswer)}})
	if got := roleByKind(t, failure, "executor"); got.State != "exception" || got.Since == nil {
		t.Fatalf("waiting_answer snapshot must preserve delivery failure and its known start: %+v", got)
	}
	if got := roleByKind(t, failure, "coordinator").State; got != "exception" {
		t.Fatalf("same-dispatch wake failure must survive stale waiting_answer snapshot, got %q", got)
	}

	newRound := newDetail("completed", 10, 5, 6)
	ApplySessionTaskObservations(newRound, map[string]SessionTaskObservation{"task-1": {Target: "worker", State: string(proto.TaskStateRunning)}})
	if got := roleByKind(t, newRound, "executor"); got.State != "running" || got.Since != nil {
		t.Fatalf("new round running should supersede stale failure without inventing a start: %+v", got)
	}
	if got := roleByKind(t, newRound, "coordinator"); got.State != "unknown" || got.Basis.DispatchSeq != 6 {
		t.Fatalf("new dispatch task snapshot should supersede prior wake evidence: %+v", got)
	}
}

func TestTaskMirrorRecentSummariesKeepTicketAndFailureMeaning(t *testing.T) {
	tests := []struct {
		name, taskType, payload, wantSummary, wantTone string
	}{
		{"permission", "permission_request", `{"ticket_id":"t1"}`, "出现待处理权限请求", "needs_you"},
		{"question", "question", `{"ticket_id":"t2"}`, "执行任务提出需要答复的问题", "needs_you"},
		{"answered", "ticket_answered", `{"ticket_id":"t1"}`, "协调者已提交工单答复", "neutral"},
		{"delivery failure", "delivery_failed", `{"ticket_id":"t1","reason":"pipe closed"}`, "工单应答未送达执行者: pipe closed", "failed"},
		{"turn failure", "turn_failed", `{"fail_reason":"agent exited"}`, "执行回合明确失败: agent exited", "failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"task_type": tc.taskType, "payload": json.RawMessage(tc.payload)})
			if err != nil {
				t.Fatal(err)
			}
			activity, ok := roleActivity(proto.LedgerEvent{Type: "task_mirrored", Payload: raw})
			if !ok || activity.Summary != tc.wantSummary || activity.Tone != tc.wantTone {
				t.Fatalf("recent activity = (%+v,%t), want summary=%q tone=%q", activity, ok, tc.wantSummary, tc.wantTone)
			}
		})
	}
}

func roleByKind(t *testing.T, detail *proto.SessionDetail, kind string) proto.SessionRole {
	t.Helper()
	for _, role := range detail.RoleStatus.Roles {
		if role.Kind == kind {
			return role
		}
	}
	t.Fatalf("missing role kind %q", kind)
	return proto.SessionRole{}
}

func timePtr() *time.Time {
	now := time.Now().UTC()
	return &now
}
