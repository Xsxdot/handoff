package proto

// b453_rerun_wire_test.go —— F17：任务快照新可选键 rerun_of / rerun_by，形状 {"task","machine"}；
// 缺席不出键（旧客户端与新客户端读旧任务都不受影响）。
import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskRerunLinksWireShape(t *testing.T) {
	b, err := json.Marshal(Task{ID: "t1", RerunBy: &TaskRef{Task: "t2", Machine: "linux-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"rerun_by":{"task":"t2","machine":"linux-01"}`) {
		t.Fatalf("rerun_by 形状不符: %s", b)
	}
	if strings.Contains(string(b), `"rerun_of"`) {
		t.Fatalf("缺席的 rerun_of 不应出键: %s", b)
	}
	var back Task
	if err := json.Unmarshal([]byte(`{"id":"t3","rerun_of":{"task":"t1","machine":""}}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.RerunOf == nil || back.RerunOf.Task != "t1" {
		t.Fatalf("rerun_of 解码失败: %+v", back.RerunOf)
	}
}
