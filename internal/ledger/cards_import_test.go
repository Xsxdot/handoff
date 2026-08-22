// 显式 ID 导入通道（Store.ImportCard）的账本域测试：撞号、缺父、
// 与 min_b 水位的关系、导入后自动取号的落点。
package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// 导入成功路径：号按给的落、工作流钉最新版、初始态 = 首态、
// card_created 事件带导入来源标注。
func TestImportCardExplicitID(t *testing.T) {
	s := seedStore(t)
	card, err := s.ImportCard("B42", "backlog.md", NewCard{
		Title: "存量行", Project: "handoff", Priority: "高", Actor: "test"})
	if err != nil {
		t.Fatalf("导入: %v", err)
	}
	if card.ID != "B42" {
		t.Fatalf("应保原号 B42，得 %s", card.ID)
	}
	// 与普通卡零行为差别：工作流缺省 triage、钉最新版本、初始态 = 首态
	if card.WorkflowName != "triage" || card.WorkflowVersion != 1 || card.Status != StatusTodo {
		t.Fatalf("导入卡与普通卡不一致: %+v", card)
	}
	got, err := s.GetCard("B42")
	if err != nil {
		t.Fatalf("回读: %v", err)
	}
	if got.Title != "存量行" || got.Priority != "高" || got.Project != "handoff" {
		t.Fatalf("字段没落对: %+v", got)
	}
	events, err := s.EventsFromAsc([]string{"B42"}, 0, 10)
	if err != nil || len(events) == 0 || events[0].Type != EvCardCreated {
		t.Fatalf("出生事件: %v %+v", err, events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("解事件负载: %v", err)
	}
	if payload["imported"] != true || payload["import_source"] != "backlog.md" {
		t.Fatalf("card_created 应标注导入来源，实得 %+v", payload)
	}
}

// 撞已存在 ID 必须拒绝——导入覆盖既有卡等于静默丢账，比报错难发现得多。
func TestImportCardRejectsExistingID(t *testing.T) {
	s := seedStore(t)
	existing := mk(t, s, "先来的")
	if _, err := s.ImportCard(existing.ID, "", NewCard{
		Title: "后来的", Project: "handoff", Actor: "test"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("撞号应拒（wrap ErrBadState），实得: %v", err)
	}
	// 被撞的卡一字未动
	got, _ := s.GetCard(existing.ID)
	if got.Title != "先来的" {
		t.Fatalf("撞号拒绝后原卡被改了: %+v", got)
	}
}

// 点号子卡要求父卡已存在；父卡在则挂上去并在父卡 timeline 留痕。
func TestImportCardChildRequiresParent(t *testing.T) {
	s := seedStore(t)
	if _, err := s.ImportCard("B77.1", "", NewCard{
		Title: "孤儿子卡", Project: "handoff", Actor: "test"}); err == nil {
		t.Fatal("父卡不存在应拒绝")
	}
	if _, err := s.ImportCard("B77", "", NewCard{
		Title: "父卡", Project: "handoff", Actor: "test"}); err != nil {
		t.Fatalf("导入父卡: %v", err)
	}
	child, err := s.ImportCard("B77.1", "", NewCard{
		Title: "子卡", Project: "handoff", Actor: "test"})
	if err != nil {
		t.Fatalf("父卡在时导入子卡: %v", err)
	}
	if child.ID != "B77.1" || child.ParentID != "B77" {
		t.Fatalf("父子关系没建对: %+v", child)
	}
	// 父卡 timeline 留痕（与 CreateCard 同款，导入不例外）
	events, err := s.EventsFromAsc([]string{"B77"}, 0, 100)
	if err != nil {
		t.Fatalf("读父卡事件: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == EvComment && strings.Contains(string(event.Payload), "B77.1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("父卡 timeline 缺建子卡留痕，实得 %d 条事件", len(events))
	}
}

// 导入不受 min_b 约束：水位只管自动取号，低于水位的历史号照样导得进。
func TestImportCardIgnoresMinB(t *testing.T) {
	s := seedStore(t)
	if err := s.EnsureMinB(200); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}
	card, err := s.ImportCard("B12", "backlog.md", NewCard{
		Title: "水位以下的历史号", Project: "handoff", Actor: "test"})
	if err != nil {
		t.Fatalf("水位以下的号应导得进: %v", err)
	}
	if card.ID != "B12" {
		t.Fatalf("应保原号 B12，得 %s", card.ID)
	}
	// 水位仍管自动取号：新建卡落 B201 而不是 B13
	fresh, err := s.CreateCard(NewCard{Title: "新卡", Project: "handoff", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if fresh.ID != "B201" {
		t.Fatalf("导入不该动水位，新卡应 B201，得 %s", fresh.ID)
	}
}

// 导入高于水位的号之后，自动取号要跳过它——nextTopID 取「现存最大号」
// 与 min_b 的较大者，导入号自然被算进「现存最大号」。
func TestImportCardShiftsAutoAllocation(t *testing.T) {
	s := seedStore(t)
	if err := s.EnsureMinB(100); err != nil {
		t.Fatalf("EnsureMinB: %v", err)
	}
	if _, err := s.ImportCard("B300", "backlog.md", NewCard{
		Title: "高于水位的导入号", Project: "handoff", Actor: "test"}); err != nil {
		t.Fatalf("导入: %v", err)
	}
	fresh, err := s.CreateCard(NewCard{Title: "导入之后的新卡", Project: "handoff", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	if fresh.ID != "B301" {
		t.Fatalf("自动取号应跳过导入号，应 B301，得 %s", fresh.ID)
	}
	// 子卡位同理：导入 B300.5 后，自动分配的下一个子位是 B300.6
	if _, err := s.ImportCard("B300.5", "backlog.md", NewCard{
		Title: "点号子卡", Project: "handoff", Actor: "test"}); err != nil {
		t.Fatalf("导入子卡: %v", err)
	}
	child, err := s.CreateCard(NewCard{
		Title: "自动分配的子卡", Project: "handoff", Parent: "B300", Actor: "test"})
	if err != nil {
		t.Fatalf("建子卡: %v", err)
	}
	if child.ID != "B300.6" {
		t.Fatalf("子位应跳过导入号，应 B300.6，得 %s", child.ID)
	}
}

// 非法 id 形态与缺字段在开事务前就该拒掉。
func TestImportCardValidation(t *testing.T) {
	s := seedStore(t)
	for _, id := range []string{"", "153", "B", "Bxx", "B1.", "b1"} {
		if _, err := s.ImportCard(id, "", NewCard{
			Title: "t", Project: "p", Actor: "test"}); err == nil {
			t.Fatalf("非法 id %q 应拒绝", id)
		}
	}
	if _, err := s.ImportCard("B9", "", NewCard{Project: "p", Actor: "test"}); err == nil {
		t.Fatal("空标题应拒绝")
	}
	if _, err := s.ImportCard("B9", "", NewCard{Title: "t", Actor: "test"}); err == nil {
		t.Fatal("空 project 应拒绝")
	}
	if _, err := s.ImportCard("B9", "", NewCard{
		Title: "t", Project: "p", Workflow: "不存在的流", Actor: "test"}); err == nil {
		t.Fatal("未知工作流应拒绝")
	}
	// 上面全拒之后账本里不该留下 B9
	if _, err := s.GetCard("B9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("被拒的导入不该落卡: %v", err)
	}
}
