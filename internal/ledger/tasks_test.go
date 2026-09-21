package ledger

import (
	"errors"
	"testing"
)

func TestB2336TaskLinkProjection(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "工作流投影")
	if err := s.LinkTask(card.ID, "mac-02", "task-review", "review", "test"); err != nil {
		t.Fatalf("LinkTask review: %v", err)
	}
	if err := s.LinkTask(card.ID, "mac-02", "task-old", "implement", "test"); err != nil {
		t.Fatalf("LinkTask old: %v", err)
	}
	if err := s.LinkTask(card.ID, "mac-02", "task-plain", "implement", "test"); err != nil {
		t.Fatalf("LinkTask plain: %v", err)
	}
	if err := s.RecordDispatch(card.ID, DispatchSnapshot{
		Target: "mac-02", TaskID: "task-review", Node: "review", Attempt: "attempt-review",
		Branch: "cards/" + card.ID + "-review", Purpose: PurposeReview, Actor: "test",
	}); err != nil {
		t.Fatalf("RecordDispatch review: %v", err)
	}
	if err := s.RecordDispatch(card.ID, DispatchSnapshot{
		Target: "mac-02", TaskID: "task-old", Node: "review", Attempt: "",
		Branch: "cards/" + card.ID + "-old", Purpose: PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("RecordDispatch old: %v", err)
	}
	if err := s.RecordDispatch(card.ID, DispatchSnapshot{
		Target: "mac-02", TaskID: "task-plain", Branch: "cards/" + card.ID + "-plain",
		Purpose: PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("RecordDispatch plain: %v", err)
	}

	links, err := s.TasksOf(card.ID)
	if err != nil {
		t.Fatalf("TasksOf: %v", err)
	}
	if len(links) != 3 {
		t.Fatalf("TasksOf length=%d, want 3: %+v", len(links), links)
	}
	byTask := make(map[string]TaskLink, len(links))
	for _, link := range links {
		byTask[link.TaskID] = link
	}
	if got := byTask["task-review"]; got.Node != "review" || got.Attempt != "attempt-review" {
		t.Fatalf("workflow projection=%+v, want node/attempt review/attempt-review", got)
	}
	if got := byTask["task-old"]; got.Node != "" || got.Attempt != "" {
		t.Fatalf("empty attempt must not be guessed: %+v", got)
	}
	if got := byTask["task-plain"]; got.Node != "" || got.Attempt != "" {
		t.Fatalf("plain dispatch must not be guessed: %+v", got)
	}

	allLinks, err := s.AllTaskLinks()
	if err != nil {
		t.Fatalf("AllTaskLinks: %v", err)
	}
	allByTask := make(map[string]TaskLink, len(allLinks))
	for _, link := range allLinks {
		allByTask[link.TaskID] = link
	}
	if got := allByTask["task-review"]; got.Node != "review" || got.Attempt != "attempt-review" {
		t.Fatalf("AllTaskLinks projection=%+v, want node/attempt review/attempt-review", got)
	}
}

func TestLinkTask(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.LinkTask(card.ID, "mac-02", "T1234", "implement", "test"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// 同 (target, task) 重复挂账拒绝——一个 task 至多挂一张卡（主键约束转干净错误）
	card2 := mk(t, s, "另一张")
	if err := s.LinkTask(card2.ID, "mac-02", "T1234", "review", "test"); err == nil {
		t.Fatal("重复挂账应拒")
	}
	links, err := s.TasksOf(card.ID)
	if err != nil || len(links) != 1 || links[0].TaskID != "T1234" || links[0].Purpose != "implement" {
		t.Fatalf("TasksOf: %v %+v", err, links)
	}
	allLinks, err := s.AllTaskLinks()
	if err != nil || len(allLinks) != 1 || allLinks[0].CardID != card.ID {
		t.Fatalf("AllTaskLinks: %v %+v", err, allLinks)
	}
	// 反查：task → 卡
	cardID, err := s.CardOfTask("mac-02", "T1234")
	if err != nil || cardID != card.ID {
		t.Fatalf("CardOfTask: %v %q", err, cardID)
	}
	if _, err := s.CardOfTask("mac-02", "无此任务"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("幽灵 task: %v", err)
	}
}

func TestClaimCardIsDisabled(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.ClaimCard(card.ID, "session-A"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧认领入口应停用: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil || got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("旧认领不得写席位: %v %+v", err, got)
	}
}

func TestTakeoverCardIsDisabled(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "卡")
	if err := s.TakeoverCard(card.ID, "session-new", "cli:test@example"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧接管入口应停用: %v", err)
	}
	got, err := s.GetCard(card.ID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("旧接管不得写席位: %+v", got)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == EvDriverTakeover {
			t.Fatalf("旧接管不得落事件: %+v", event)
		}
	}
}
