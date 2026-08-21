// unlinkedrows_test.go —— 钉住「未挂账」行的两条排除规则。
//
// 为什么单独一个文件而不是塞进 ledgerapi_test.go：这里验的是纯函数，
// 不需要起 agentd/ledger/target 那一整套环境，测试跑起来才不依赖网络。
package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func rowIDs(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		id, _ := row["task_id"].(string)
		out = append(out, id)
	}
	return out
}

func TestUnlinkedRowsForSkipsLinkedAndTerminal(t *testing.T) {
	tasks := []proto.TaskView{
		{Task: proto.Task{ID: "t-pending", Name: "待执行", State: proto.TaskStatePending}},
		{Task: proto.Task{ID: "t-running", Name: "进行中", State: proto.TaskStateRunning}},
		{Task: proto.Task{ID: "t-answer", Name: "等答复", State: proto.TaskStateWaitingAnswer}},
		{Task: proto.Task{ID: "t-review", Name: "待审阅", State: proto.TaskStateWaitingReview}},
		{Task: proto.Task{ID: "t-done", Name: "已完成", State: proto.TaskStateCompleted}},
		{Task: proto.Task{ID: "t-failed", Name: "已失败", State: proto.TaskStateFailed}},
		{Task: proto.Task{ID: "t-linked", Name: "已挂卡", State: proto.TaskStateRunning}},
	}
	linked := map[string]bool{"mac-02/t-linked": true}

	got := rowIDs(unlinkedRowsFor("mac-02", tasks, linked))
	want := []string{"t-pending", "t-running", "t-answer", "t-review"}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
}

// 挂账索引按 target 前缀区分：另一台机器上的同名 task 不能被误判为已挂账。
func TestUnlinkedRowsForScopesLinkedByTarget(t *testing.T) {
	tasks := []proto.TaskView{{Task: proto.Task{ID: "t-1", State: proto.TaskStateRunning}}}
	linked := map[string]bool{"linux-01/t-1": true}

	if got := rowIDs(unlinkedRowsFor("mac-02", tasks, linked)); len(got) != 1 || got[0] != "t-1" {
		t.Fatalf("rows = %v, want [t-1]（linux-01 的挂账不该盖住 mac-02）", got)
	}
	if got := rowIDs(unlinkedRowsFor("linux-01", tasks, linked)); len(got) != 0 {
		t.Fatalf("rows = %v, want 空（linux-01/t-1 已挂账）", got)
	}
}

// 全是终态时返回空切片而不是 nil：调用方要把它 append 进 rows 并 JSON 编码，
// count 必须是 0 而不是 null。
func TestUnlinkedRowsForAllTerminalReturnsEmptyNotNil(t *testing.T) {
	tasks := []proto.TaskView{
		{Task: proto.Task{ID: "a", State: proto.TaskStateCompleted}},
		{Task: proto.Task{ID: "b", State: proto.TaskStateFailed}},
	}
	rows := unlinkedRowsFor("mac-02", tasks, nil)
	if rows == nil {
		t.Fatal("rows = nil, want 非 nil 空切片")
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %v, want 空", rowIDs(rows))
	}
}
