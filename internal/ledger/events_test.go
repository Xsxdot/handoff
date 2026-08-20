package ledger

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCommentRefsAutoRelate(t *testing.T) {
	s := seedStore(t)
	a, b := mk(t, s, "a"), mk(t, s, "b")
	event, err := s.AddComment(a.ID, "这个问题与 #"+b.ID+" 同源", "普通", "test")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	var payload struct {
		Body string   `json:"body"`
		Refs []string `json:"refs"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	if len(payload.Refs) != 1 || payload.Refs[0] != b.ID {
		t.Fatalf("refs 解析: %+v", payload)
	}
	// 引用自动落 relates 边
	relations, _ := s.RelationsOf(a.ID)
	found := false
	for _, relation := range relations {
		if relation.Type == RelRelates && relation.To == b.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺 relates 边: %+v", relations)
	}
	// 引用不存在的卡：评论照发、边不建、不报错（评论是记录不是校验）
	if _, err := s.AddComment(a.ID, "见 #B9999", "普通", "test"); err != nil {
		t.Fatalf("幽灵引用不应报错: %v", err)
	}
	// 更正类评论
	event2, _ := s.AddComment(a.ID, "上一条口径不对", "更正", "test")
	_ = json.Unmarshal(event2.Payload, &payload)
	if payload.Body == "" {
		t.Fatal("更正评论 body 丢失")
	}
	// 重置评论：payload 带 human_reset_node（Plan C 回合计数清零的落账形态）
	event3, err := s.AddCommentReset(a.ID, "人工看过，重新计数", "普通", "test", "review")
	if err != nil {
		t.Fatalf("reset comment: %v", err)
	}
	var resetPayload map[string]any
	_ = json.Unmarshal(event3.Payload, &resetPayload)
	if resetPayload["human_reset_node"] != "review" {
		t.Fatalf("缺 human_reset_node: %v", resetPayload)
	}
	if _, err := s.AddCommentReset(a.ID, "x", "普通", "test", ""); err == nil {
		t.Fatal("空节点名应拒")
	}
}

func TestAcceptanceAndNeeds(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "a")
	if err := s.RecordAcceptance(a.ID, true, "真机跑通 go test", "test"); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := s.MarkNeedsHuman(a.ID, "审阅超轮", "test"); err != nil {
		t.Fatalf("needs: %v", err)
	}
	views, _ := s.ListCards(CardFilter{Project: "p"})
	var view CardView
	for _, candidate := range views {
		if candidate.ID == a.ID {
			view = candidate
		}
	}
	if view.NeedsReason != "审阅超轮" {
		t.Fatalf("等人派生: %+v", view)
	}
	if err := s.ClearNeedsHuman(a.ID, "test"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	views, _ = s.ListCards(CardFilter{Project: "p"})
	for _, candidate := range views {
		if candidate.ID == a.ID && candidate.NeedsReason != "" {
			t.Fatalf("等人未清除: %+v", candidate)
		}
	}
}

// TestRecordBranchMerged 合并环节的外部动作必须落账：推了什么、合进哪里。
func TestRecordBranchMerged(t *testing.T) {
	s := seedStore(t)
	a := mk(t, s, "待合并")
	if err := s.RecordBranchMerged(a.ID, "feat/x", "integration/y", true, "node:merge"); err != nil {
		t.Fatalf("RecordBranchMerged: %v", err)
	}
	events, err := s.EventsFromAsc([]string{a.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.Type != EvBranchMerged {
			continue
		}
		found = true
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("解 payload: %v", err)
		}
		if p["pushed_work_branch"] != true {
			t.Fatalf("pushed_work_branch 应为 true: %v", p)
		}
		if p["merged_into"] != "integration/y" || p["pushed_base"] != "integration/y" {
			t.Fatalf("分支字段不对: %v", p)
		}
	}
	if !found {
		t.Fatalf("没落 %s 事件", EvBranchMerged)
	}
}

func TestRecordDispatch(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "要派的卡")
	err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "feature-impl", TemplateVersion: 1, DisciplineName: "review",
		Target: "mac-02", TaskID: "T9", Branch: "cards/" + c.ID + "-implement",
		PlanPath: "plans/x.md", Actor: "cli:me@host",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 10)
	found := false
	for _, event := range evs {
		if event.Type == EvDispatched {
			found = true
			var payload map[string]any
			_ = json.Unmarshal(event.Payload, &payload)
			if payload["discipline_name"] != "review" || payload["template_version"] != float64(1) {
				t.Fatalf("快照字段: %+v", payload)
			}
		}
	}
	if !found {
		t.Fatal("缺 dispatched 事件")
	}
}

func TestSubtree(t *testing.T) {
	s := seedStore(t)
	root := mk(t, s, "root")
	child, _ := s.SplitCard(root.ID, "c1", "test")
	_, _ = s.SplitCard(child.ID, "cc", "test")
	member := mk(t, s, "m")
	_ = s.MergeCards([]string{member.ID}, root.ID, "test")
	ids, err := s.Subtree(root.ID)
	if err != nil {
		t.Fatalf("subtree: %v", err)
	}
	// 根 + 两级后代 + 并入成员 = 4
	if len(ids) != 4 {
		t.Fatalf("子树成员 %v", ids)
	}
}

// WorkBranch 必须跳过审阅轮：审阅只读、跑在工作分支上不新开分支。
// 直接取「最后一条 dispatched」会在审阅之后指向审阅分支——合并节点会去
// 合一条只读分支，第二轮审阅会撞第一轮的同名分支（真机实测过）。
func TestWorkBranchSkipsReviewRounds(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "有实现也有审阅")
	if _, err := s.WorkBranch(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没派过实现轮应报 ErrNotFound: %v", err)
	}
	_ = s.LinkTask(c.ID, "acc", "T-impl", PurposeImplement, "test")
	if err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "feature-impl", Target: "acc", TaskID: "T-impl",
		Branch: "cards/" + c.ID + "-implement", Purpose: PurposeImplement, Actor: "test"}); err != nil {
		t.Fatalf("记实现派发: %v", err)
	}
	_ = s.LinkTask(c.ID, "acc", "T-review", PurposeReview, "test")
	if err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "review-generic", Target: "acc", TaskID: "T-review",
		Branch: "cards/" + c.ID + "-implement", Purpose: PurposeReview, Actor: "test"}); err != nil {
		t.Fatalf("记审阅派发: %v", err)
	}
	got, err := s.WorkBranch(c.ID)
	if err != nil || got != "cards/"+c.ID+"-implement" {
		t.Fatalf("工作分支应为实现轮的分支: %q %v", got, err)
	}

	// 老快照没有 purpose 字段时，回落到挂账表查用途
	_ = s.LinkTask(c.ID, "acc", "T-review-2", PurposeReview, "test")
	_ = s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "review-generic", Target: "acc", TaskID: "T-review-2",
		Branch: "cards/" + c.ID + "-review", Actor: "test"})
	if got, err = s.WorkBranch(c.ID); err != nil || got != "cards/"+c.ID+"-implement" {
		t.Fatalf("无 purpose 的审阅快照应经挂账表识别并跳过: %q %v", got, err)
	}
}
