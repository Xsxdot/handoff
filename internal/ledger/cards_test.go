package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// seedStore 建好显式测试工作流的测试库——建卡类测试共用；生产打开账本不种子。
func seedStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
	return s
}

func mustChild(t *testing.T, s *Store, parentID, title string) Card {
	t.Helper()
	card, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "bug", Parent: parentID, Actor: "test"})
	if err != nil {
		t.Fatalf("mustChild: %v", err)
	}
	return card
}

// TestChildrenOfReturnsDirectChildren 只返回直接子卡，按 id 排序。
func TestChildrenOfReturnsDirectChildren(t *testing.T) {
	s := seedStore(t)
	root := mk(t, s, "根卡")
	childB := mustChild(t, s, root.ID, "子卡 B")
	childA := mustChild(t, s, root.ID, "子卡 A")
	grand := mustChild(t, s, childA.ID, "孙卡")

	got, err := s.ChildrenOf(root.ID)
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应有 2 个直接子卡，实得 %d：%+v", len(got), got)
	}
	// 孙卡不该出现：本方法只一层，递归是抽屉里再点一次的事
	for _, brief := range got {
		if brief.ID == grand.ID {
			t.Fatalf("孙卡 %s 不该出现在直接子卡里", grand.ID)
		}
	}
	if got[0].ID > got[1].ID {
		t.Fatalf("应按 id 排序，实得 %s, %s", got[0].ID, got[1].ID)
	}
	byID := map[string]CardBrief{got[0].ID: got[0], got[1].ID: got[1]}
	if byID[childA.ID].Title != "子卡 A" || byID[childB.ID].Title != "子卡 B" {
		t.Fatalf("标题没带出来：%+v", got)
	}
	if byID[childA.ID].Status == "" {
		t.Fatalf("状态没带出来：%+v", got)
	}
}

// TestChildrenOfEmptyForLeaf 叶子卡返回空切片而不是错误——
// 「没有子卡」是正常态，抽屉据此整区不渲染。
func TestChildrenOfEmptyForLeaf(t *testing.T) {
	s := seedStore(t)
	leaf := mk(t, s, "叶子卡")
	got, err := s.ChildrenOf(leaf.ID)
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("叶子卡应为空，实得 %+v", got)
	}
}

// TestChildrenOfUnknownCardErrors 卡不存在要报错（映射 404），
// 不能与「有卡但没子卡」都返回空——那样前端分不出「打错 id」和「真没有」。
func TestChildrenOfUnknownCardErrors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ChildrenOf("B-不存在"); err == nil {
		t.Fatal("未知卡应报错")
	}
}

func TestCreateCardAllocatesBNumbers(t *testing.T) {
	s := seedStore(t)
	// 迁移前的新库要先垫号，防与 markdown 总账撞号（spec §2.1 B 号分配）
	if err := s.EnsureMinB(156); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}
	c1, err := s.CreateCard(NewCard{Title: "第一张", Project: "handoff", Workflow: "feature", Actor: "test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c1.ID != "B157" {
		t.Fatalf("垫号后首卡应为 B157，得 %s", c1.ID)
	}
	c2, _ := s.CreateCard(NewCard{Title: "第二张", Project: "handoff", Workflow: "bug", Actor: "test"})
	if c2.ID != "B158" {
		t.Fatalf("连续编号: %s", c2.ID)
	}
	// 子卡走点号位
	ch1, err := s.CreateCard(NewCard{Title: "子一", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if err != nil || ch1.ID != "B157.1" {
		t.Fatalf("子卡: %v %s", err, ch1.ID)
	}
	ch2, _ := s.CreateCard(NewCard{Title: "子二", Project: "handoff", Workflow: "feature", Parent: c1.ID, Actor: "test"})
	if ch2.ID != "B157.2" {
		t.Fatalf("子卡连续: %s", ch2.ID)
	}
	// 初始态 = 工作流首态；钉最新版本
	if c1.Status != StatusTodo || c1.WorkflowVersion != 1 {
		t.Fatalf("初始态/版本: %+v", c1)
	}
	// 出生事件落流
	events, err := s.EventsFromAsc([]string{c1.ID}, 0, 10)
	if err != nil || len(events) == 0 || events[0].Type != EvCardCreated {
		t.Fatalf("出生事件: %v %+v", err, events)
	}
}

// TestCreateCardAllocatesIndependentProjectPrefixes 验证新项目前缀从各自的 1
// 起步，且 handoff 的历史 B 水位不会污染其它前缀；子卡只继承父卡号的前缀。
func TestCreateCardAllocatesIndependentProjectPrefixes(t *testing.T) {
	s := seedStore(t)
	if err := s.EnsureMinB(173); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}

	handoff, err := s.CreateCard(NewCard{Title: "handoff 卡", Project: "handoff", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("handoff 建卡: %v", err)
	}
	if handoff.ID != "B174" {
		t.Fatalf("handoff 应续 B 水位，得 %s", handoff.ID)
	}
	charter, err := s.CreateCard(NewCard{Title: "charter 卡", Project: "charter", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("charter 建卡: %v", err)
	}
	if charter.ID != "C1" {
		t.Fatalf("charter 应从 C1 起步，得 %s", charter.ID)
	}
	sq, err := s.CreateCard(NewCard{Title: "sq 卡", Project: "sq", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("sq 建卡: %v", err)
	}
	if sq.ID != "S1" {
		t.Fatalf("sq 应从 S1 起步，得 %s", sq.ID)
	}
	child, err := s.CreateCard(NewCard{Title: "charter 子卡", Project: "charter", Workflow: "bug", Parent: charter.ID, Actor: "test"})
	if err != nil {
		t.Fatalf("charter 子卡: %v", err)
	}
	if child.ID != "C1.1" {
		t.Fatalf("子卡应继承 C 前缀，得 %s", child.ID)
	}
}

// TestCreateCardRejectsPrefixCollision 建卡不能在前缀撞车时静默回退到 B；
// 显式配置后才允许继续建卡。
func TestCreateCardRejectsPrefixCollision(t *testing.T) {
	s := seedStore(t)
	_, err := s.CreateCard(NewCard{Title: "benchmarking 卡", Project: "benchmarking", Workflow: "bug", Actor: "test"})
	if err == nil {
		t.Fatal("benchmarking 的自动 B 前缀撞 handoff 时应拒绝建卡")
	}
	for _, want := range []string{"benchmarking", "handoff", "card prefix"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("撞车错误应含 %q，实得 %v", want, err)
		}
	}
	if _, err := s.GetCard("B1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("撞车拒绝后不应落 B1，实得 %v", err)
	}
	if err := s.SetCardPrefix("benchmarking", "BE"); err != nil {
		t.Fatalf("显式配置前缀: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "benchmarking 卡", Project: "benchmarking", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("显式前缀后建卡: %v", err)
	}
	if card.ID != "BE1" {
		t.Fatalf("显式前缀应生成 BE1，得 %s", card.ID)
	}
}

func TestCreateCardRejectsProjectWithoutASCIIPrefix(t *testing.T) {
	s := seedStore(t)
	_, err := s.CreateCard(NewCard{Title: "无字母项目", Project: "项目123", Workflow: "bug", Actor: "test"})
	if err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Fatalf("无 ASCII 字母项目应拒绝并说明原因，实得 %v", err)
	}
}

func TestSetCardPrefixValidationAndExistingCards(t *testing.T) {
	s := seedStore(t)
	for _, prefix := range []string{"", "b", "ABCDE", "A1", "中文"} {
		if err := s.SetCardPrefix("demo", prefix); err == nil {
			t.Fatalf("非法前缀 %q 应拒绝", prefix)
		}
	}
	if err := s.SetCardPrefix("demo", "DE"); err != nil {
		t.Fatalf("设前缀: %v", err)
	}
	if err := s.SetCardPrefix("demo", "DE"); err != nil {
		t.Fatalf("重复设相同前缀应幂等: %v", err)
	}
	if _, err := s.CreateCard(NewCard{Title: "demo 卡", Project: "demo", Workflow: "bug", Actor: "test"}); err != nil {
		t.Fatalf("demo 建卡: %v", err)
	}
	if err := s.SetCardPrefix("demo", "D"); err == nil || !strings.Contains(err.Error(), "已有卡") {
		t.Fatalf("已有卡后改前缀应拒绝，实得 %v", err)
	}
	if err := s.SetCardPrefix("other", "DE"); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("前缀被占用时应指出占用项目，实得 %v", err)
	}
}

func TestCreateCardValidation(t *testing.T) {
	s := seedStore(t)
	if _, err := s.CreateCard(NewCard{Project: "p", Workflow: "feature"}); err == nil {
		t.Fatal("空标题应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "不存在的流"}); err == nil {
		t.Fatal("未知工作流应拒绝")
	}
	if _, err := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Parent: "B999"}); err == nil {
		t.Fatal("父卡不存在应拒绝")
	}
}

// 建子卡要在父卡 timeline 留痕：审计链能回答「这张子卡为什么存在、
// 什么时候从谁身上拆出来的」。复用 EvComment+refs（AddBlocks 同款），
// 不新增事件类型。
func TestCreateChildLeavesParentTimelineEvent(t *testing.T) {
	s := seedStore(t)
	parent := mk(t, s, "母卡")
	child := mustChild(t, s, parent.ID, "拆出的子卡")

	events, err := s.EventsFromAsc([]string{parent.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读父卡事件: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == EvComment && strings.Contains(string(event.Payload), child.ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("父卡 timeline 应有指向 %s 的建子卡事件，实得 %d 条事件", child.ID, len(events))
	}
}

// 递归护栏：父链上同名工作流的嵌套上限（spec §8.3）。子卡可绑任意工作流
// ——包括父卡自己那个，递归是组合性质；护栏挡的是失控（拆解节点把活原样
// 再拆给自己直到永远）。异名工作流不受此限。
func TestCreateCardRejectsDeepWorkflowNesting(t *testing.T) {
	s := seedStore(t)
	newBug := func(parent string) (Card, error) {
		return s.CreateCard(NewCard{Title: "层", Project: "p", Workflow: "bug", Parent: parent, Actor: "test"})
	}
	level1, err := newBug("")
	if err != nil {
		t.Fatalf("第 1 层: %v", err)
	}
	level2, err := newBug(level1.ID)
	if err != nil {
		t.Fatalf("第 2 层: %v", err)
	}
	level3, err := newBug(level2.ID)
	if err != nil {
		t.Fatalf("第 3 层（达上限，应放行）: %v", err)
	}
	if _, err := newBug(level3.ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("第 4 层应被护栏拒（wrap ErrBadState），实得: %v", err)
	}
	// 异名工作流不占同一个计数：feature 卡挂在三层 bug 之下照常放行
	if _, err := s.CreateCard(NewCard{Title: "异名", Project: "p", Workflow: "feature",
		Parent: level3.ID, Actor: "test"}); err != nil {
		t.Fatalf("异名工作流不该被拒: %v", err)
	}
}

func TestUpdateCardAttachAccept(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "feature", Actor: "test"})
	if _, err := s.AttachFile(card.ID, "spec", "specs/x.md", "test"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := s.AttachFile(card.ID, "plan", "specs/x.md", "test"); err != nil {
		t.Fatalf("同 path 不同 kind attach: %v", err)
	}
	if err := s.SetAcceptance(card.ID, "跑通判据一", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if len(got.Attachments) != 2 || got.Attachments[0].Kind != "spec" ||
		got.Attachments[1].Kind != "plan" || got.Attachments[0].Path != "specs/x.md" ||
		got.Attachments[1].Path != "specs/x.md" {
		t.Fatalf("附件: %+v", got.Attachments)
	}
	if got.AcceptanceCriteria != "跑通判据一" {
		t.Fatalf("判据: %q", got.AcceptanceCriteria)
	}
	// 同 path 重复 attach 幂等（不追加重复项）
	_, _ = s.AttachFile(card.ID, "spec", "specs/x.md", "test")
	got, _ = s.GetCard(card.ID)
	if len(got.Attachments) != 2 {
		t.Fatalf("attach 不幂等: %+v", got.Attachments)
	}
	if _, err := s.DetachFile(card.ID, "spec:specs/x.md", "test"); err != nil {
		t.Fatalf("精确 detach: %v", err)
	}
	got, _ = s.GetCard(card.ID)
	if len(got.Attachments) != 1 || got.Attachments[0].Kind != "plan" {
		t.Fatalf("精确 detach 不得摘掉另一 kind: %+v", got.Attachments)
	}
	if _, err := s.DetachFile(card.ID, "specs/x.md", "test"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ = s.GetCard(card.ID)
	if len(got.Attachments) != 0 {
		t.Fatalf("detach 未生效: %+v", got.Attachments)
	}
}

func TestAttachmentMutationResults(t *testing.T) {
	s := seedStore(t)
	card, err := s.CreateCard(NewCard{Title: "附件返回值", Project: "p", Workflow: "feature", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}

	added, err := s.AttachFile(card.ID, "spec", "docs/b250.md", "test")
	if err != nil || !added {
		t.Fatalf("首次 attach 应返回新增 true: added=%v err=%v", added, err)
	}
	added, err = s.AttachFile(card.ID, "spec", "docs/b250.md", "test")
	if err != nil || added {
		t.Fatalf("重复 attach 应返回新增 false: added=%v err=%v", added, err)
	}
	added, err = s.AttachFile(card.ID, "plan", "docs/b250.md", "test")
	if err != nil || !added {
		t.Fatalf("同 path 不同 kind 应返回新增 true: added=%v err=%v", added, err)
	}

	removed, err := s.DetachFile(card.ID, "spec:docs/b250.md", "test")
	if err != nil || len(removed) != 1 || removed[0].Kind != "spec" || removed[0].Path != "docs/b250.md" {
		t.Fatalf("精确 detach 应返回摘掉清单: removed=%+v err=%v", removed, err)
	}
	removed, err = s.DetachFile(card.ID, "docs/b250.md", "test")
	if err != nil || len(removed) != 1 || removed[0].Kind != "plan" || removed[0].Path != "docs/b250.md" {
		t.Fatalf("裸 path detach 应返回剩余清单: removed=%+v err=%v", removed, err)
	}
	removed, err = s.DetachFile(card.ID, "docs/b250.md", "test")
	if err != nil || len(removed) != 0 {
		t.Fatalf("重复裸 path detach 应返回空清单: removed=%+v err=%v", removed, err)
	}
}

func TestUpdateCardMeta(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "旧标题")
	if err := s.UpdateCardMeta(c.ID, "新标题", "高", "test"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "高" {
		t.Fatalf("未生效: %+v", got)
	}
	// 只改一项：另一项保持
	_ = s.UpdateCardMeta(c.ID, "", "低", "test")
	got, _ = s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "低" {
		t.Fatalf("空串应保持原值: %+v", got)
	}
}

func TestSetCardBaseBranch(t *testing.T) {
	t.Run("write and audit", func(t *testing.T) {
		s := seedStore(t)
		card := mk(t, s, "基线可写")
		if err := s.SetCardBaseBranch(card.ID, "cards/B205-charter", "test"); err != nil {
			t.Fatalf("写基线: %v", err)
		}
		got, err := s.GetCard(card.ID)
		if err != nil || got.BaseBranch != "cards/B205-charter" {
			t.Fatalf("读回自身基线: %+v err=%v", got, err)
		}
		effective, err := s.EffectiveBaseBranch(card.ID)
		if err != nil || effective != "cards/B205-charter" {
			t.Fatalf("读回有效基线: %q err=%v", effective, err)
		}
		events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		found := false
		for _, event := range events {
			if event.Type == EvComment && strings.Contains(string(event.Payload), "更新卡基线") {
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				found = true
			}
		}
		if !found || payload["base_branch"] != "cards/B205-charter" {
			t.Fatalf("comment payload 不完整: found=%v payload=%v", found, payload)
		}
	})

	t.Run("clear preserves parent", func(t *testing.T) {
		s := seedStore(t)
		parent, err := s.CreateCard(NewCard{Title: "父卡", Project: "p", Workflow: "bug", BaseBranch: "parent/base", Actor: "test"})
		if err != nil {
			t.Fatal(err)
		}
		child := mustChild(t, s, parent.ID, "子卡")
		if err := s.SetCardBaseBranch(child.ID, "", "test"); err != nil {
			t.Fatal(err)
		}
		got, _ := s.GetCard(child.ID)
		if got.BaseBranch != "" {
			t.Fatalf("自身覆盖未清除: %q", got.BaseBranch)
		}
		effective, err := s.EffectiveBaseBranch(child.ID)
		if err != nil || effective != "parent/base" {
			t.Fatalf("父链继承错误: %q err=%v", effective, err)
		}
		events, _ := s.EventsFromAsc([]string{child.ID}, 0, 100)
		var payload map[string]any
		found := false
		for _, event := range events {
			if event.Type == EvComment && strings.Contains(string(event.Payload), "更新卡基线") {
				_ = json.Unmarshal(event.Payload, &payload)
				found = true
			}
		}
		value, exists := payload["base_branch"]
		if !found || !exists || value != "" {
			t.Fatalf("清除必须保留存在且为空的键: found=%v exists=%v value=%v", found, exists, value)
		}
		if err := s.SetCardBaseBranch(child.ID, "child/override", "test"); err != nil {
			t.Fatal(err)
		}
		parentAfter, _ := s.GetCard(parent.ID)
		if parentAfter.BaseBranch != "parent/base" {
			t.Fatalf("子卡写入改了父卡: %q", parentAfter.BaseBranch)
		}
	})

	t.Run("missing card", func(t *testing.T) {
		s := seedStore(t)
		if err := s.SetCardBaseBranch("B205-missing", "cards/missing", "test"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err=%v, want ErrNotFound", err)
		}
	})

	t.Run("first dispatched including review freezes", func(t *testing.T) {
		s := seedStore(t)
		card := mk(t, s, "已派发")
		first := DispatchSnapshot{Template: "feature-impl", Target: "acc", TaskID: "impl-1",
			Branch: "cards/B205-first", Purpose: PurposeImplement, Actor: "test"}
		review := DispatchSnapshot{Template: "review-generic", Target: "acc", TaskID: "review-1",
			Branch: "cards/B205-review", Purpose: PurposeReview, Actor: "test"}
		if err := s.RecordDispatch(card.ID, first); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordDispatch(card.ID, review); err != nil {
			t.Fatal(err)
		}
		events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
		var firstEvent Event
		for _, event := range events {
			if event.Type == EvDispatched {
				firstEvent = event
				break
			}
		}
		err := s.SetCardBaseBranch(card.ID, "cards/should-reject", "test")
		if !errors.Is(err, ErrBadState) {
			t.Fatalf("err=%v, want ErrBadState", err)
		}
		for _, want := range []string{"cards/B205-first", firstEvent.CreatedAt.Format(time.RFC3339Nano)} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("冻结错误缺少 %q: %v", want, err)
			}
		}
		got, _ := s.GetCard(card.ID)
		if got.BaseBranch != "" {
			t.Fatalf("拒绝路径改了卡: %q", got.BaseBranch)
		}
	})

	t.Run("review-only dispatched freezes", func(t *testing.T) {
		s := seedStore(t)
		card := mk(t, s, "只审阅派发")
		review := DispatchSnapshot{Template: "review-generic", Target: "acc", TaskID: "review-only-1",
			Branch: "cards/B205-review-only", Purpose: PurposeReview, Actor: "test"}
		if err := s.RecordDispatch(card.ID, review); err != nil {
			t.Fatal(err)
		}
		events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var reviewEvent Event
		for _, event := range events {
			if event.Type == EvDispatched {
				reviewEvent = event
				break
			}
		}
		if reviewEvent.Seq == 0 {
			t.Fatal("应找到唯一审阅派发事件")
		}
		err = s.SetCardBaseBranch(card.ID, "cards/should-reject", "test")
		if !errors.Is(err, ErrBadState) {
			t.Fatalf("只派审阅轮也应冻结基线，err=%v", err)
		}
		for _, want := range []string{"cards/B205-review-only", reviewEvent.CreatedAt.Format(time.RFC3339Nano)} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("冻结错误缺少 %q：%v", want, err)
			}
		}
	})

	t.Run("comment failure rolls back card", func(t *testing.T) {
		s := seedStore(t)
		card := mk(t, s, "事务回滚")
		_, err := s.db.Exec("CREATE TRIGGER fail_base_comment BEFORE INSERT ON card_events WHEN NEW.type = 'comment' BEGIN SELECT RAISE(ABORT, 'forced base comment failure'); END")
		if err != nil {
			t.Fatal(err)
		}
		err = s.SetCardBaseBranch(card.ID, "cards/rollback", "test")
		if err == nil || !strings.Contains(err.Error(), "forced base comment failure") {
			t.Fatalf("comment 失败未透出: %v", err)
		}
		got, _ := s.GetCard(card.ID)
		if got.BaseBranch != "" {
			t.Fatalf("事件失败后基线仍存在: %q", got.BaseBranch)
		}
		events, _ := s.EventsFromAsc([]string{card.ID}, 0, 100)
		for _, event := range events {
			if event.Type == EvComment && strings.Contains(string(event.Payload), "cards/rollback") {
				t.Fatalf("事件失败后仍有 comment: %+v", event)
			}
		}
	})
}

func TestCloseAndRevive(t *testing.T) {
	s := seedStore(t)
	card, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "bug", Actor: "test"})
	if err := s.CloseCard(card.ID, "无效理由", "test"); err == nil {
		t.Fatal("非受控 reason 应拒绝")
	}
	if err := s.CloseCard(card.ID, CloseShelved, "test"); err != nil {
		t.Fatalf("close: %v", err)
	}
	got, _ := s.GetCard(card.ID)
	if got.Status != StatusClosed || got.TerminateReason != CloseShelved {
		t.Fatalf("终止态: %+v", got)
	}
	if err := s.ReviveCard(card.ID, "test"); err != nil {
		t.Fatalf("revive: %v", err)
	}
	got, _ = s.GetCard(card.ID)
	if got.Status != StatusTodo || got.TerminateReason != "" {
		t.Fatalf("复活态: %+v", got)
	}
	// 取消/废弃不可复活
	_ = s.CloseCard(card.ID, CloseCancelled, "test")
	if err := s.ReviveCard(card.ID, "test"); err == nil || !strings.Contains(err.Error(), "搁置") {
		t.Fatalf("取消卡不应可复活: %v", err)
	}
}

func TestCreateCardEmptyWorkflowRequiresUniqueWorkflow(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		s := newTestStore(t)
		_, err := s.CreateCard(NewCard{Title: "无流", Project: "p", Actor: "test"})
		if err == nil || !strings.Contains(err.Error(), "先用 workflow put") {
			t.Fatalf("零工作流应指引先建流: %v", err)
		}
	})

	t.Run("one", func(t *testing.T) {
		s := newTestStore(t)
		if _, err := s.PutWorkflow("charter", WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
			t.Fatal(err)
		}
		card, err := s.CreateCard(NewCard{Title: "唯一流", Project: "p", Actor: "test"})
		if err != nil {
			t.Fatalf("建卡: %v", err)
		}
		if card.WorkflowName != "charter" || card.Status != "待办" {
			t.Fatalf("唯一工作流应自动采用: %+v", card)
		}
	})

	t.Run("many", func(t *testing.T) {
		s := newTestStore(t)
		for _, name := range []string{"charter", "other"} {
			if _, err := s.PutWorkflow(name, WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := s.CreateCard(NewCard{Title: "多流", Project: "p", Actor: "test"})
		if err == nil || !strings.Contains(err.Error(), "显式指定 --workflow") ||
			!strings.Contains(err.Error(), "charter") || !strings.Contains(err.Error(), "other") {
			t.Fatalf("多工作流应列出选项并要求显式指定: %v", err)
		}
	})
}
