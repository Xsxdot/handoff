package client

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b370Store 起一个真实 SQLite 账本并返回一张已建好的卡。
// 边界：只服务 B370 直测；不写镜像事件（JudgeMirroredWake 直接吃投影结构）。
func b370Store(t *testing.T) (*ledger.Store, string) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.PutWorkflow("bug", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "B370 身份闸", Project: "p", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	return st, card.ID
}

// b370Dispatch 写一条 EvDispatched 快照；TaskID 由 task 决定，用于构造 TaskID != Attempt 的不合格快照。
func b370Dispatch(t *testing.T, st *ledger.Store, cardID, target, task, node, attempt string) {
	t.Helper()
	if err := st.RecordDispatch(cardID, ledger.DispatchSnapshot{
		Target: target, TaskID: task, Node: node, Attempt: attempt,
		Branch: "cards/" + cardID + "-" + attempt, Purpose: ledger.PurposeReview, Actor: "test",
	}); err != nil {
		t.Fatalf("写派发快照 target=%s task=%s node=%s attempt=%s: %v", target, task, node, attempt, err)
	}
}

func b370Ptr(s string) *string { return &s }

func TestB370CurrentWorkflowAttempt(t *testing.T) {
	t.Run("latest qualified snapshot wins and TaskID != Attempt is skipped", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		b370Dispatch(t, st, cardID, "t", "task-x", "review", "a2") // TaskID != Attempt，不合格
		b370Dispatch(t, st, cardID, "t", "a3", "review", "a3")
		snap, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || !found || snap.Attempt != "a3" {
			t.Fatalf("CurrentWorkflowAttempt=%+v found=%v err=%v，want attempt=a3", snap, found, err)
		}
	})
	t.Run("node mismatch is not identity", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		_, found, err := CurrentWorkflowAttempt(st, cardID, "implement")
		if err != nil || found {
			t.Fatalf("异节点查询 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("empty node query is never identity", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		_, found, err := CurrentWorkflowAttempt(st, cardID, "")
		if err != nil || found {
			t.Fatalf("空节点查询 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("no dispatch means not found", func(t *testing.T) {
		st, cardID := b370Store(t)
		_, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || found {
			t.Fatalf("无派发 found=%v err=%v，want false/nil", found, err)
		}
	})
	t.Run("pagination reaches tail beyond first page", func(t *testing.T) {
		st, cardID := b370Store(t)
		for i := 0; i < 501; i++ {
			if _, err := st.AddComment(cardID, fmt.Sprintf("audit-%d", i), "普通", "test"); err != nil {
				t.Fatalf("写审计事件 %d: %v", i, err)
			}
		}
		b370Dispatch(t, st, cardID, "t", "a-tail", "review", "a-tail")
		snap, found, err := CurrentWorkflowAttempt(st, cardID, "review")
		if err != nil || !found || snap.Attempt != "a-tail" {
			t.Fatalf("分页尾部快照=%+v found=%v err=%v，want attempt=a-tail", snap, found, err)
		}
	})
}

func TestB370JudgeMirroredWakeEmptyIdentity(t *testing.T) {
	t.Run("missing keys and source equals current dispatch delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 1, TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("缺键空身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("empty string keys and source equals current dispatch delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 2, Node: b370Ptr(""), Attempt: b370Ptr(""),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("空串空身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("snapshot without workflow identity delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "task-no-identity", "", "")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 3, TaskType: "question", SourceTask: "task-no-identity", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateSnapshotNoWorkflowIdentity {
			t.Fatalf("无身份快照 dec=%+v err=%v，want deliver/snapshot_without_workflow_identity", dec, err)
		}
	})
	t.Run("no dispatch snapshot delivers", func(t *testing.T) {
		st, cardID := b370Store(t)
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 4, TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateSnapshotNotFound {
			t.Fatalf("无派发 dec=%+v err=%v，want deliver/snapshot_not_found", dec, err)
		}
	})
	t.Run("source mismatch blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-new", "review", "a-new")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 5, TaskType: "question", SourceTask: "a-old", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("source 不等 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("nonempty node locates by that node", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-impl", "implement", "a-impl")
		b370Dispatch(t, st, cardID, "t", "a-review", "review", "a-review")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 6, Node: b370Ptr("review"), Attempt: b370Ptr(""),
			TaskType: "question", SourceTask: "a-review", SourceTarget: "t",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("按 node 定位 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
}

func TestB370JudgeMirroredWakeNonEmptyIdentity(t *testing.T) {
	t.Run("current attempt with matching source passes and ignores source_seq", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 7, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t", SourceSeq: 999999,
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("当前身份 dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("both empty target passes", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 8, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "",
		})
		if err != nil || !dec.Deliver || dec.Reason != WakeGateCurrentAttempt {
			t.Fatalf("双空 target dec=%+v err=%v，want deliver/current_attempt", dec, err)
		}
	})
	t.Run("one empty target blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 9, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("一空一非空 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("stale attempt blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a-new", "review", "a-new")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 10, Node: b370Ptr("review"), Attempt: b370Ptr("a-old"),
			TaskType: "question", SourceTask: "a-new", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("旧 attempt dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
	t.Run("missing source task has its own reason", func(t *testing.T) {
		st, cardID := b370Store(t)
		b370Dispatch(t, st, cardID, "t", "a1", "review", "a1")
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 11, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateMissingSourceTask {
			t.Fatalf("缺 source_task dec=%+v err=%v，want block/missing_source_task", dec, err)
		}
	})
	t.Run("no qualified snapshot blocks", func(t *testing.T) {
		st, cardID := b370Store(t)
		dec, err := JudgeMirroredWake(st, WakeGateEvent{
			CardID: cardID, Seq: 12, Node: b370Ptr("review"), Attempt: b370Ptr("a1"),
			TaskType: "question", SourceTask: "a1", SourceTarget: "t",
		})
		if err != nil || dec.Deliver || dec.Reason != WakeGateStaleAttempt {
			t.Fatalf("无合格快照 dec=%+v err=%v，want block/stale_attempt", dec, err)
		}
	})
}

func TestB370JudgeMirroredWakeNilStore(t *testing.T) {
	if _, err := JudgeMirroredWake(nil, WakeGateEvent{CardID: "X1", Seq: 1, TaskType: "question"}); err == nil {
		t.Fatal("st == nil 必须返回带上下文的错误，不得 panic")
	}
}
