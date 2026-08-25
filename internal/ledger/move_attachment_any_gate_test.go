// 择一附件门：RequireAttachmentAny。卡带清单里任意一种 kind 即放行。
//
// 存在的理由（B226）：charter 流一条列序服务多条路径——L2 走
// spec→plan→implement（有 plan 无 breakdown），L3 轻档走
// contract→breakdown→implement（有 breakdown 无 plan）。单值门必然
// 顾此失彼，而门真正要保证的是「有一份可执行的工作单」，不是它叫什么名字。
package ledger

import (
	"errors"
	"strings"
	"testing"
)

// anyGateStore 建一条带择一门的两列工作流：待办 →（闸）implement。
func anyGateStore(t *testing.T) *Store {
	t.Helper()
	s := seedStore(t)
	if _, err := s.PutWorkflow("anygate", WorkflowDef{Nodes: []NodeDef{
		{Name: "待办", Next: "implement"},
		{Name: "implement", Gate: Gate{RequireAttachmentAny: []string{"plan", "breakdown"}}},
	}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	return s
}

func mkAnyGate(t *testing.T, s *Store, title string) Card {
	t.Helper()
	card, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "anygate", Actor: "test"})
	if err != nil {
		t.Fatalf("建 anygate 卡: %v", err)
	}
	return card
}

// 两种 kind 都没有 → 拒，且错误文案把两种候选都列出来（只报一种会让人
// 补错附件再撞一次）。
func TestAttachmentAnyGateBlocksWhenNoneMatch(t *testing.T) {
	s := anyGateStore(t)
	card := mkAnyGate(t, s, "无附件")

	err := s.MoveCard(card.ID, "implement", "", "test")
	if !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("应被择一门拦下，实得: %v", err)
	}
	for _, kind := range []string{"plan", "breakdown"} {
		if !strings.Contains(err.Error(), kind) {
			t.Fatalf("错误应列出候选 %q，实得: %v", kind, err)
		}
	}

	// 挂一种不在清单里的附件仍应被拒——择一不是「有附件就行」。
	if _, err := s.AttachFile(card.ID, "spec", "docs/s.md", "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}
	if err := s.MoveCard(card.ID, "implement", "", "test"); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("清单外的 kind 不应放行，实得: %v", err)
	}
}

// 清单里任意一种都放行——两条路径各验一次，避免只测了 plan 而 breakdown
// 那条（正是本门存在的理由）零覆盖。
func TestAttachmentAnyGateAcceptsEitherKind(t *testing.T) {
	for _, kind := range []string{"plan", "breakdown"} {
		t.Run(kind, func(t *testing.T) {
			s := anyGateStore(t)
			card := mkAnyGate(t, s, "带 "+kind)
			if _, err := s.AttachFile(card.ID, kind, "docs/"+kind+".md", "test"); err != nil {
				t.Fatalf("挂 %s: %v", kind, err)
			}
			if err := s.MoveCard(card.ID, "implement", "", "test"); err != nil {
				t.Fatalf("带 %s 附件应放行，实得: %v", kind, err)
			}
		})
	}
}

// 同一路径同时登记为 spec 与 plan 后，charter 的 implement 择一门必须真的放行。
// 这条是 B250 的端到端牙齿：只断言附件数组长度，改坏门仍可能假绿。
func TestAttachmentKindsOnSamePathUnlockImplementGate(t *testing.T) {
	s := anyGateStore(t)
	card := mkAnyGate(t, s, "同一路径双 kind")
	for _, kind := range []string{"spec", "plan"} {
		if _, err := s.AttachFile(card.ID, kind, "docs/b250.md", "test"); err != nil {
			t.Fatalf("挂 %s: %v", kind, err)
		}
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读双 kind 卡: %v", err)
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("同路径双 kind 应各自保留: %+v", got.Attachments)
	}
	if err := s.MoveCard(card.ID, "implement", "", "test"); err != nil {
		t.Fatalf("双 kind 应通过 implement 择一门: %v", err)
	}
}

// 单值门与择一门是 AND：两个都设就两个都要过。
// 这条钉住的是「加了择一门不会把既有单值门弱化成或」——弱化是静默的，
// 只有正面断言才发现得了。
func TestAttachmentAnyGateAndsWithSingleGate(t *testing.T) {
	s := seedStore(t)
	if _, err := s.PutWorkflow("bothgate", WorkflowDef{Nodes: []NodeDef{
		{Name: "待办", Next: "implement"},
		{Name: "implement", Gate: Gate{
			RequireAttachment:    "contract",
			RequireAttachmentAny: []string{"plan", "breakdown"},
		}},
	}}); err != nil {
		t.Fatalf("PutWorkflow: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "两门", Project: "p", Workflow: "bothgate", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}

	// 只满足择一门，缺 contract → 仍拒。
	if _, err := s.AttachFile(card.ID, "breakdown", "docs/b.md", "test"); err != nil {
		t.Fatalf("挂 breakdown: %v", err)
	}
	if err := s.MoveCard(card.ID, "implement", "", "test"); !errors.Is(err, ErrGateBlocked) {
		t.Fatalf("缺单值门要求的 contract 应拒，实得: %v", err)
	}

	if _, err := s.AttachFile(card.ID, "contract", "docs/c.md", "test"); err != nil {
		t.Fatalf("挂 contract: %v", err)
	}
	if err := s.MoveCard(card.ID, "implement", "", "test"); err != nil {
		t.Fatalf("两门都满足应放行，实得: %v", err)
	}
}

// 带择一门的节点必须真的进 Gates map。
// 这条防的是 withStatesFromNodes 的空判定漏掉新字段——那个失败模式是
// 门被静默丢弃、卡畅通无阻，比报错危险得多。
func TestAttachmentAnyGateSurvivesStatesDerivation(t *testing.T) {
	def := WorkflowDef{Nodes: []NodeDef{
		{Name: "待办", Next: "implement"},
		{Name: "implement", Gate: Gate{RequireAttachmentAny: []string{"plan", "breakdown"}}},
	}}.withStatesFromNodes()

	gate, ok := def.Gates["implement"]
	if !ok {
		t.Fatal("只设了 RequireAttachmentAny 的节点被判成空 gate，静默丢出 Gates map——门无声失效")
	}
	if len(gate.RequireAttachmentAny) != 2 {
		t.Fatalf("择一清单应有 2 项，实得: %v", gate.RequireAttachmentAny)
	}
}
