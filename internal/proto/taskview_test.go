// proto 包测试：钉住 TaskView 的线格式兼容性。
package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// TestTaskViewWireCompatible 验证 TaskView 的 JSON 与 Task **逐键兼容**：
// 字段提升后 Task 的每一个键都在原位，只多出一个 watchers。
//
// 缺陷形态：若有人把 Watchers 写成具名字段（如 `Task proto.Task`）而非嵌入，
// 线格式会变成 {"task":{...},"watchers":0}，所有老客户端当场解不出任务。
func TestTaskViewWireCompatible(t *testing.T) {
	task := proto.Task{ID: "t1", Name: "n1", State: proto.TaskStateRunning}
	view := proto.TaskView{Task: task, Watchers: 2}

	var fromTask, fromView map[string]any
	b1, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("序列化 Task: %v", err)
	}
	b2, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("序列化 TaskView: %v", err)
	}
	if err := json.Unmarshal(b1, &fromTask); err != nil {
		t.Fatalf("反序列化 Task: %v", err)
	}
	if err := json.Unmarshal(b2, &fromView); err != nil {
		t.Fatalf("反序列化 TaskView: %v", err)
	}
	for k, want := range fromTask {
		got, ok := fromView[k]
		if !ok {
			t.Errorf("TaskView 丢了 Task 的键 %q", k)
			continue
		}
		if !jsonEqual(got, want) {
			t.Errorf("键 %q: TaskView = %v, Task = %v", k, got, want)
		}
	}
	if len(fromView) != len(fromTask)+1 {
		t.Errorf("TaskView 的键数 = %d, want %d（Task 的键 + watchers 一个）",
			len(fromView), len(fromTask)+1)
	}
	if fromView["watchers"] != float64(2) {
		t.Errorf("watchers = %v, want 2", fromView["watchers"])
	}
}

// TestTaskViewDecodesIntoOldTask 验证老客户端（只认 proto.Task）解 TaskView 不破。
func TestTaskViewDecodesIntoOldTask(t *testing.T) {
	b, err := json.Marshal(proto.TaskView{
		Task:     proto.Task{ID: "t1", Name: "n1", State: proto.TaskStateRunning},
		Watchers: 3,
	})
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	var old proto.Task
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatalf("老客户端解码失败: %v", err)
	}
	if old.ID != "t1" || old.State != proto.TaskStateRunning {
		t.Errorf("老客户端解出的任务不对: %+v", old)
	}
}

// jsonEqual 比较两个 encoding/json 解出的任意值。
func jsonEqual(a, b any) bool {
	ba, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ba) == string(bb)
}
