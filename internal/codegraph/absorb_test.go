package codegraph

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// absorb 后写盘再重载，图与併入结果逐字段等价——穿真实序列化边界，
// 只比内存对象会漏 json tag 缺陷。
func TestAbsorbRoundTrip(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatal(err)
	}
	d, err := LoadDiff("testdata/repo", "branch-x")
	if err != nil {
		t.Fatal(err)
	}
	merged := Absorb(g, d)
	// nodesAdded 进图、nodesDeleted 出图、edgesAdded/implementsAdded 进表
	for id := range d.NodesAdded {
		if _, ok := merged.Nodes[id]; !ok {
			t.Fatalf("added 节点 %s 未併入", id)
		}
	}
	for _, id := range d.NodesDeleted {
		if _, ok := merged.Nodes[id]; ok {
			t.Fatalf("deleted 节点 %s 仍在", id)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "codegraph"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveGraph(dir, merged); err != nil {
		t.Fatalf("写盘: %v", err)
	}
	reloaded, err := LoadGraph(dir)
	if err != nil {
		t.Fatalf("重载: %v", err)
	}
	if !reflect.DeepEqual(merged, reloaded) {
		t.Fatal("写盘重载后不等价——序列化链路丢数据")
	}
}

// 入参不可变：absorb 失败重试的前提。
func TestAbsorbDoesNotMutateInput(t *testing.T) {
	g, err := LoadGraph("testdata/repo")
	if err != nil {
		t.Fatal(err)
	}
	before := len(g.Nodes)
	d, err := LoadDiff("testdata/repo", "branch-x")
	if err != nil {
		t.Fatal(err)
	}
	_ = Absorb(g, d)
	if len(g.Nodes) != before {
		t.Fatal("Absorb 改了入参 Graph")
	}
}
