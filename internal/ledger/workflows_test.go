package ledger

import "testing"

func TestEnsureDefaultWorkflows(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	wf, err := s.GetWorkflow("feature", 0) // 0 = 最新版
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wf.Version != 1 || wf.Def.States[1] != "已出spec" {
		t.Fatalf("feature 流不符: %+v", wf)
	}
	if g := wf.Def.Gates["已出spec"]; g.RequireAttachment != "spec" {
		t.Fatalf("gate 缺失: %+v", wf.Def.Gates)
	}
	// 幂等：重复 seed 不产生新版本
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if wf2, _ := s.GetWorkflow("feature", 0); wf2.Version != 1 {
		t.Fatalf("seed 不幂等，版本涨到 %d", wf2.Version)
	}
}

func TestPutWorkflowVersioning(t *testing.T) {
	s := newTestStore(t)
	def := WorkflowDef{States: []string{"待办", "进行中", "已完成"}}
	version, err := s.PutWorkflow("bugx", def)
	if err != nil || version != 1 {
		t.Fatalf("v1: %d %v", version, err)
	}
	def.States = []string{"待办", "进行中", "待审阅", "已完成"}
	version, err = s.PutWorkflow("bugx", def)
	if err != nil || version != 2 {
		t.Fatalf("v2: %d %v", version, err)
	}
	// 旧版本仍可读（不可变版本化：钉在 v1 的卡随时能取回自己的形状）
	old, err := s.GetWorkflow("bugx", 1)
	if err != nil || len(old.Def.States) != 3 {
		t.Fatalf("v1 被改动: %+v %v", old, err)
	}
}
