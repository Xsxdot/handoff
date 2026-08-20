package ledger

import (
	"errors"
	"strings"
	"testing"
)

func TestWorkflowLegacyDefStillDecodes(t *testing.T) {
	s := newTestStore(t)
	// 老 def：只有 States/Gates，没有 Nodes。存量卡钉的就是这种行。
	if _, err := s.PutWorkflow("legacy", WorkflowDef{
		States: []string{"待办", "进行中", "已完成"},
		Gates:  map[string]Gate{"已完成": {RequireAcceptance: true}},
	}); err != nil {
		t.Fatalf("写老 def: %v", err)
	}
	got, err := s.GetWorkflow("legacy", 0)
	if err != nil {
		t.Fatalf("读老 def: %v", err)
	}
	if len(got.Def.States) != 3 {
		t.Fatalf("States 丢了: %v", got.Def.States)
	}
	// 读出时应补出等价的纯人工节点序列，且顺序与 States 一致、按序 Next。
	if len(got.Def.Nodes) != 3 {
		t.Fatalf("Nodes 应补出 3 个，得到 %d", len(got.Def.Nodes))
	}
	if got.Def.Nodes[0].Name != "待办" || got.Def.Nodes[0].Next != "进行中" {
		t.Fatalf("首节点补错: %+v", got.Def.Nodes[0])
	}
	if got.Def.Nodes[2].Next != "" {
		t.Fatalf("末节点不该有 Next: %+v", got.Def.Nodes[2])
	}
	if got.Def.Nodes[0].Dispatch || got.Def.Nodes[0].Verdict {
		t.Fatalf("补出的节点必须是纯人工列: %+v", got.Def.Nodes[0])
	}
	if got.Def.Nodes[2].Gate.RequireAcceptance != true {
		t.Fatalf("Gate 应并入对应节点: %+v", got.Def.Nodes[2].Gate)
	}
}

func TestWorkflowNodesProjectToStates(t *testing.T) {
	s := newTestStore(t)
	// 先 seed 模板：本用例引用了 feature-impl，而 Task 2 会给 PutWorkflow 加上
	// 模板存在性校验。现在补这一行，等 Task 2 落地时这个用例不会回头变红。
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	// 新 def：只给 Nodes，States/Gates 应由写入侧派生出来，
	// 好让 MoveCard 等既有消费者一行不改地继续工作。
	if _, err := s.PutWorkflow("nodeform", WorkflowDef{
		Nodes: []NodeDef{
			{Name: "待办", Next: "进行中"},
			{Name: "进行中", Next: "待审阅", Dispatch: true, Template: "feature-impl"},
			{Name: "待审阅", Gate: Gate{RequireAcceptance: true}},
		},
	}); err != nil {
		t.Fatalf("写节点形 def: %v", err)
	}
	got, err := s.GetWorkflow("nodeform", 0)
	if err != nil {
		t.Fatalf("读节点形 def: %v", err)
	}
	want := []string{"待办", "进行中", "待审阅"}
	if len(got.Def.States) != len(want) {
		t.Fatalf("States 未派生: %v", got.Def.States)
	}
	for i, state := range want {
		if got.Def.States[i] != state {
			t.Fatalf("States[%d] = %q, want %q", i, got.Def.States[i], state)
		}
	}
	if !got.Def.Gates["待审阅"].RequireAcceptance {
		t.Fatalf("Gates 未派生: %+v", got.Def.Gates)
	}
	if !got.Def.Nodes[1].Dispatch || got.Def.Nodes[1].Template != "feature-impl" {
		t.Fatalf("Nodes 原样保存失败: %+v", got.Def.Nodes[1])
	}
}

func TestDefaultWorkflowsAreNodeForm(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed 工作流: %v", err)
	}
	feature, err := s.GetWorkflow("feature", 0)
	if err != nil {
		t.Fatalf("读 feature 流: %v", err)
	}
	if len(feature.Def.Nodes) == 0 {
		t.Fatalf("出厂工作流应是节点形")
	}
	var review, merge *NodeDef
	for i := range feature.Def.Nodes {
		switch feature.Def.Nodes[i].Name {
		case "待审阅":
			review = &feature.Def.Nodes[i]
		case "待合并":
			merge = &feature.Def.Nodes[i]
		}
	}
	if review == nil || !review.Dispatch || !review.Verdict || !review.CarryCardContext {
		t.Fatalf("待审阅应是派发+裁决+带卡上下文: %+v", review)
	}
	if review.OnFail != "进行中" {
		t.Fatalf("审阅未过应退回进行中，实际 %q", review.OnFail)
	}
	if merge == nil || !merge.Dispatch {
		t.Fatalf("待合并应是派发型节点: %+v", merge)
	}
	// main 上的合并必须留人工——出厂默认不能自动往主线合。
	found := false
	for _, base := range merge.HumanBases {
		if base == "main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("出厂合并节点必须把 main 列入人工清单: %+v", merge.HumanBases)
	}
	// States 投影必须仍在，看板与 MoveCard 靠它。
	if len(feature.Def.States) != len(feature.Def.Nodes) {
		t.Fatalf("States 投影缺失: %v", feature.Def.States)
	}
}

func TestEnsureDefaultWorkflowsDoesNotOverwrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	if _, err := s.PutWorkflow("feature", WorkflowDef{Nodes: []NodeDef{{Name: "我自己的列"}}}); err != nil {
		t.Fatalf("写用户版本: %v", err)
	}
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, _ := s.GetWorkflow("feature", 0)
	if len(got.Def.Nodes) != 1 || got.Def.Nodes[0].Name != "我自己的列" {
		t.Fatalf("seed 覆盖了用户改过的工作流: %+v", got.Def.Nodes)
	}
}

func TestPutWorkflowRejectsBadNodes(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	cases := []struct {
		name string
		def  WorkflowDef
		want string // 错误里必须出现的关键词
	}{
		{"空节点名", WorkflowDef{Nodes: []NodeDef{{Name: ""}}}, "节点名"},
		{"重名节点", WorkflowDef{Nodes: []NodeDef{{Name: "A"}, {Name: "A"}}}, "重复"},
		{"派发缺模板", WorkflowDef{Nodes: []NodeDef{{Name: "A", Dispatch: true}}}, "模板"},
		{"模板不存在", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Template: "查无此模板"},
		}}, "查无此模板"},
		{"Next 悬空", WorkflowDef{Nodes: []NodeDef{{Name: "A", Next: "B"}}}, "B"},
		{"OnFail 悬空", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Verdict: true, Template: "review-generic", OnFail: "B"},
		}}, "B"},
		{"Verdict 不带 Dispatch", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Verdict: true, Template: "review-generic"},
		}}, "Dispatch"},
		{"MaxRounds 不带 Verdict", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Template: "feature-impl", MaxRounds: 3},
		}}, "Verdict"},
		{"MaxRounds 为负", WorkflowDef{Nodes: []NodeDef{
			{Name: "A", Dispatch: true, Verdict: true, Template: "review-generic", MaxRounds: -1},
		}}, "MaxRounds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.PutWorkflow("bad", tc.def)
			if err == nil {
				t.Fatalf("非法节点应被拒，却写成功了")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误里应提到 %q，实际: %v", tc.want, err)
			}
			if !errors.Is(err, ErrBadState) {
				t.Fatalf("应包装 ErrBadState，实际: %v", err)
			}
		})
	}
}

func TestPutWorkflowAcceptsGoodNodes(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	version, err := s.PutWorkflow("good", WorkflowDef{Nodes: []NodeDef{
		{Name: "待办", Next: "进行中"},
		{Name: "进行中", Next: "待审阅", Dispatch: true, Template: "feature-impl", CarryCardContext: true},
		{Name: "待审阅", Dispatch: true, Verdict: true, Template: "review-generic",
			CarryCardContext: true, MaxRounds: 3, Next: "已完成", OnFail: "进行中"},
		{Name: "已完成"},
	}})
	if err != nil {
		t.Fatalf("合法节点定义被拒: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
}

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

func TestMigrateCardWorkflow(t *testing.T) {
	s := newTestStore(t)
	def := WorkflowDef{States: []string{"待办", "进行中", "已完成"}}
	_, _ = s.PutWorkflow("wf", def)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "wf", Actor: "test"})
	def.States = []string{"待办", "评审中", "已完成"} // v2 删掉了「进行中」
	_, _ = s.PutWorkflow("wf", def)

	// 卡在 v1 的「待办」——v2 里仍有该状态，迁移放行
	if err := s.MigrateCardWorkflow(card.ID, 2, "test"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.WorkflowVersion != 2 {
		t.Fatalf("版本未迁: %+v", got)
	}
	// 迁回 v1、推进到「进行中」再迁 v2：当前状态不在新版，拒绝（防在途卡悬空）
	_ = s.MigrateCardWorkflow(card.ID, 1, "test")
	_ = s.MoveCard(card.ID, "进行中", "", "test")
	if err := s.MigrateCardWorkflow(card.ID, 2, "test"); err == nil {
		t.Fatal("状态悬空应拒")
	}
}
