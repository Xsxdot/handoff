package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
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
	wantStates := []string{"待办", "进行中", "已完成"}
	if len(got.Def.Nodes) != len(wantStates) {
		t.Fatalf("Nodes 应补出 %d 个，得到 %d", len(wantStates), len(got.Def.Nodes))
	}
	for i, want := range wantStates {
		node := got.Def.Nodes[i]
		if node.Name != want {
			t.Fatalf("节点[%d] = %q，want %q", i, node.Name, want)
		}
		wantNext := ""
		if i+1 < len(wantStates) {
			wantNext = wantStates[i+1]
		}
		if node.Next != wantNext {
			t.Fatalf("节点[%d] Next = %q，want %q", i, node.Next, wantNext)
		}
		if node.Dispatch || node.Verdict || node.CarryCardContext || node.Template != "" {
			t.Fatalf("补出的节点必须是纯人工列: %+v", node)
		}
	}
	if got.Def.Nodes[2].Gate.RequireAcceptance != true {
		t.Fatalf("Gate 应并入对应节点: %+v", got.Def.Nodes[2].Gate)
	}
}

func TestBoardLayoutRoundTripAndValidation(t *testing.T) {
	s := newTestStore(t)
	valid := &proto.BoardLayout{
		Columns:       []string{"收集", "沟通", "实现", "验收", "完成"},
		StateToColumn: map[string]string{"待办": "收集", "终止": "完成"},
		Fallback:      "实现",
	}
	if _, err := s.PutWorkflow("board", WorkflowDef{Nodes: []NodeDef{{Name: "待办"}}, Board: valid}); err != nil {
		t.Fatalf("合法看板布局应写入: %v", err)
	}
	got, err := s.GetWorkflow("board", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Def.Board == nil || len(got.Def.Board.Columns) != 5 || got.Def.Board.Columns[0] != "收集" ||
		got.Def.Board.StateToColumn["待办"] != "收集" || got.Def.Board.Fallback != "实现" {
		t.Fatalf("看板布局往返失败: %+v", got.Def.Board)
	}
	for name, layout := range map[string]*proto.BoardLayout{
		"四列":    {Columns: []string{"a", "b", "c", "d"}, Fallback: "a"},
		"重复列":   {Columns: []string{"a", "a", "b", "c", "d"}, Fallback: "a"},
		"空列":    {Columns: []string{"a", "", "b", "c", "d"}, Fallback: "a"},
		"兜底不在列": {Columns: []string{"a", "b", "c", "d", "e"}, Fallback: "z"},
		"映射不在列": {Columns: []string{"a", "b", "c", "d", "e"}, StateToColumn: map[string]string{"待办": "z"}, Fallback: "a"},
	} {
		if _, err := s.PutWorkflow("bad-board-"+name, WorkflowDef{Nodes: []NodeDef{{Name: "待办"}}, Board: layout}); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		} else if !errors.Is(err, ErrBadState) {
			t.Fatalf("%s 应包装 ErrBadState: %v", name, err)
		}
	}
}

func TestWorkflowNodesProjectToStates(t *testing.T) {
	s := newTestStore(t)
	// 先 seed 模板：本用例引用了 feature-impl，而 Task 2 会给 PutWorkflow 加上
	// 模板存在性校验。现在补这一行，等 Task 2 落地时这个用例不会回头变红。
	if err := seedTestTemplates(t, s); err != nil {
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

func TestWorkflowNodeCarriesPurposeAndAcceptanceSwitch(t *testing.T) {
	s := newTestStore(t)
	if err := seedTestTemplates(t, s); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	_, err := s.PutWorkflow("node-fields", WorkflowDef{Nodes: []NodeDef{{
		Name: "待审阅", Dispatch: true, Template: "feature-impl", OmitAcceptance: true,
		Override: NodeOverride{Purpose: PurposeReview},
	}}})
	if err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	got, err := s.GetWorkflow("node-fields", 0)
	if err != nil {
		t.Fatalf("读工作流: %v", err)
	}
	if len(got.Def.Nodes) != 1 {
		t.Fatalf("节点数量应为 1，实得 %d", len(got.Def.Nodes))
	}
	node := got.Def.Nodes[0]
	if node.Override.Purpose != PurposeReview || !node.OmitAcceptance {
		t.Fatalf("节点字段穿序列化边界失败: %+v", node)
	}
}

func TestPutWorkflowRejectsBadNodes(t *testing.T) {
	s := newTestStore(t)
	if err := seedTestTemplates(t, s); err != nil {
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
	if err := seedTestTemplates(t, s); err != nil {
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
	if _, err := s.MigrateCardWorkflow(card.ID, "wf", 2, "待办", "test"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.WorkflowVersion != 2 {
		t.Fatalf("版本未迁: %+v", got)
	}
	// 迁回 v1、推进到「进行中」再迁 v2：当前状态不在新版，拒绝（防在途卡悬空）
	_, _ = s.MigrateCardWorkflow(card.ID, "wf", 1, "待办", "test")
	_ = s.MoveCard(card.ID, "进行中", "", "test")
	if _, err := s.MigrateCardWorkflow(card.ID, "wf", 2, "进行中", "test"); err == nil {
		t.Fatal("状态悬空应拒")
	}
}

// TestMigrateRejectsInFlight 卡有环节在飞时拒绝迁移——门禁在事务内，
// 所以 CLI 与 HTTP 两个入口共享同一道门（契约拍板记录④）。
func TestMigrateRejectsInFlight(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "在飞拒迁", Project: "p", Workflow: "triage", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Target: "acc", TaskID: "T-1", Branch: "cards/" + c.ID + "-T-1",
		Purpose: PurposeImplement, Template: "feature-impl", Actor: "test",
	}); err != nil {
		t.Fatalf("写派发事件: %v", err)
	}
	_, err = s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "test")
	if !errors.Is(err, ErrStepInFlight) {
		t.Fatalf("在飞时应拒绝迁移并包 ErrStepInFlight，实得 %v", err)
	}
	// 拒绝必须是「没动」，不是「动了一半」
	got, _ := s.GetCard(c.ID)
	if got.WorkflowName == "bug" {
		t.Fatal("被拒的迁移不该改动卡")
	}
}

// TestMigrateWritesMigrationEvent 迁移落 EvWorkflowMigrated，payload 能回答
// 从哪到哪——审计链要能解释「这张卡为什么换了流程」。
func TestMigrateWritesMigrationEvent(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "迁移留痕", Project: "p", Workflow: "triage", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if _, err := s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "tester"); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.Type != EvWorkflowMigrated {
			continue
		}
		found = true
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解码迁移事件: %v", err)
		}
		for _, k := range []string{"from_workflow", "from_status", "to_workflow", "to_status", "to_version"} {
			if _, ok := p[k]; !ok {
				t.Fatalf("迁移事件缺字段 %q: %v", k, p)
			}
		}
		if p["to_workflow"] != "bug" || p["from_workflow"] != "triage" {
			t.Fatalf("迁移事件的来去不对: %v", p)
		}
		if e.Actor != "tester" {
			t.Fatalf("操作者应留痕，实得 %q", e.Actor)
		}
	}
	if !found {
		t.Fatal("没有 EvWorkflowMigrated 事件")
	}
}

// TestMigrateLeavesChildrenAlone 子卡不随父卡迁（基准语义 5）。
func TestMigrateLeavesChildrenAlone(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "父卡")
	child := mustChild(t, s, parent.ID, "子卡") // mustChild 建的是 bug 流子卡
	if _, err := s.MigrateCardWorkflow(parent.ID, "feature", 0, StatusTodo, "test"); err != nil {
		t.Fatalf("迁父卡: %v", err)
	}
	got, err := s.GetCard(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowName != "bug" {
		t.Fatalf("子卡不该随父卡迁，实得 %q", got.WorkflowName)
	}
}

// TestMigrateCannotBypassGate 迁移不能用来跳过目标流的 gate：
// 卡缺 contract 附件 → 迁到无闸流 → 再迁回有 contract 闸的列，最后一步仍须被拒。
// 这是拆解 §4.4 的结论落成的回归网。
func TestMigrateCannotBypassGate(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "绕闸尝试")
	// domain/契约冻结 需要 contract 附件
	if _, err := s.MigrateCardWorkflow(c.ID, "domain", 0, "契约冻结", "test"); err == nil {
		t.Fatal("缺 contract 附件不该能迁进契约冻结列")
	}
	if _, err := s.MigrateCardWorkflow(c.ID, "bug", 0, StatusDoing, "test"); err != nil {
		t.Fatalf("迁到无闸流应允许（场景 B 降级）: %v", err)
	}
	if _, err := s.MigrateCardWorkflow(c.ID, "domain", 0, "契约冻结", "test"); err == nil {
		t.Fatal("绕一圈回来仍须被目标 gate 拒绝")
	}
}

func TestWorkflowNodeProducesRoundTripAndPresence(t *testing.T) {
	s := newTestStore(t)
	if err := seedTestTemplates(t, s); err != nil {
		t.Fatalf("seed 模板: %v", err)
	}
	want := &NodeOutput{Kind: "doc", Path: "docs/superpowers/specs/b201-breakdown.md"}
	if _, err := s.PutWorkflow("produces", WorkflowDef{Nodes: []NodeDef{{
		Name: "breakdown", Dispatch: true, Template: "feature-impl", Produces: want,
	}}}); err != nil {
		t.Fatalf("写带产出的工作流: %v", err)
	}
	got, err := s.GetWorkflow("produces", 0)
	if err != nil {
		t.Fatalf("读带产出的工作流: %v", err)
	}
	if got.Def.Nodes[0].Produces == nil || *got.Def.Nodes[0].Produces != *want {
		t.Fatalf("产出声明未 round-trip: %+v", got.Def.Nodes[0].Produces)
	}

	var missing NodeDef
	if err := json.Unmarshal([]byte("{\"name\":\"plan\"}"), &missing); err != nil {
		t.Fatalf("解码缺失字段: %v", err)
	}
	if missing.Produces != nil {
		t.Fatalf("字段缺失必须保持 nil: %+v", missing.Produces)
	}

	var zero NodeDef
	if err := json.Unmarshal([]byte("{\"name\":\"plan\",\"produces\":{\"kind\":\"\",\"path\":\"\"}}"), &zero); err != nil {
		t.Fatalf("解码显式零值: %v", err)
	}
	if zero.Produces == nil || zero.Produces.Kind != "" || zero.Produces.Path != "" {
		t.Fatalf("显式零值必须保留为非 nil 指针: %+v", zero.Produces)
	}
}

func TestWorkflowRejectsIncompleteProduces(t *testing.T) {
	cases := []struct {
		name   string
		output *NodeOutput
	}{
		{name: "missing kind", output: &NodeOutput{Path: "docs/x.md"}},
		{name: "missing path", output: &NodeOutput{Kind: "doc"}},
		{name: "all empty", output: &NodeOutput{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if err := seedTestTemplates(t, s); err != nil {
				t.Fatalf("seed 模板: %v", err)
			}
			_, err := s.PutWorkflow("invalid-"+tc.name, WorkflowDef{Nodes: []NodeDef{{
				Name: "node", Dispatch: true, Template: "feature-impl", Produces: tc.output,
			}}})
			if err == nil || !errors.Is(err, ErrBadState) || !strings.Contains(err.Error(), "produces") {
				t.Fatalf("不完整 produces 未按 ErrBadState 拒绝: %v", err)
			}
		})
	}
}

func TestLegacyWorkflowNodeHasNoProduces(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.PutWorkflow("legacy-produces", WorkflowDef{
		States: []string{"待办", "完成"},
	}); err != nil {
		t.Fatalf("写 legacy 工作流: %v", err)
	}
	got, err := s.GetWorkflow("legacy-produces", 0)
	if err != nil {
		t.Fatalf("读 legacy 工作流: %v", err)
	}
	for _, node := range got.Def.Nodes {
		if node.Produces != nil {
			t.Fatalf("legacy 节点不应凭空带 produces: %+v", node)
		}
	}
}
