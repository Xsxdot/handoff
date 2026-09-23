// b382_work_branch_test.go —— B382 人工登记工作分支的账本回归。
//
// 职责：锁住「工作分支的来源顺序：非审阅 dispatched 快照 → 人工登记 → 报错」，
// 以及登记写入口的审计/覆盖/清除语义。缝见 docs/superpowers/specs/b382.md §6。
// 边界：只跑 SQLite 账本（newTestStore/seedStore 基座），不碰 git、不碰 agentd。
package ledger

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestB382WorkBranchErrorPointsToRegistration 是红锚（spec §6 接缝 1 断言②）：
// 卡既无快照也无登记时，WorkBranch 的报错必须指路登记命令，且保留 ErrNotFound 根因。
// 基线现状文案是「…没有非审阅的 dispatched 快照（还没派过实现轮？）」，不含
// 「work-branch」——本用例在基线可编译且红。
func TestB382WorkBranchErrorPointsToRegistration(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "无快照无登记")
	_, err := s.WorkBranch(card.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实得 %v", err)
	}
	for _, want := range []string{"work-branch", card.ID, "登记"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错应指路登记（缺 %q）：%v", want, err)
		}
	}
}

// TestB382WorkBranchReturnsRegisteredBranch 接缝 1 断言①（承重）：无快照 +
// 有登记 → 返回登记的分支；Target/TaskID 为空（登记不携带来源机，见 plan §0 P1）。
func TestB382WorkBranchReturnsRegisteredBranch(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "人工实现")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	got, err := s.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != "feat/b382-work" || got.Target != "" || got.TaskID != "" {
		t.Fatalf("登记分支读数 = %+v", got)
	}
}

// TestB382WorkBranchSnapshotWinsOverRegistration 接缝 1 断言③（生效范围裁定）：
// 有非审阅快照时登记被忽略（applied=false），WorkBranch 返回快照。
func TestB382WorkBranchSnapshotWinsOverRegistration(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "两者都有")
	if err := s.RecordDispatch(card.ID, DispatchSnapshot{
		Template: "feature-impl", Target: "mac-02", TaskID: "T-impl",
		Branch: "cards/B382-implement", Purpose: PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("落快照: %v", err)
	}
	applied, err := s.RegisterWorkBranch(card.ID, "feat/should-ignore", "cli:u@h")
	if err != nil {
		t.Fatalf("登记: %v", err)
	}
	if applied {
		t.Fatal("有快照时登记不应生效（applied 应为 false）")
	}
	got, err := s.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if got.Branch != "cards/B382-implement" || got.Target != "mac-02" || got.TaskID != "T-impl" {
		t.Fatalf("快照应胜出，实得 %+v", got)
	}
}

// TestB382RegisterLastWinsAndClear 接缝 2：再次登记最后一条胜；空串清除。
func TestB382RegisterLastWinsAndClear(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "覆盖与清除")
	for _, b := range []string{"feat/one", "feat/two"} {
		if _, err := s.RegisterWorkBranch(card.ID, b, "cli:u@h"); err != nil {
			t.Fatalf("登记 %s: %v", b, err)
		}
	}
	if got, err := s.WorkBranch(card.ID); err != nil || got.Branch != "feat/two" {
		t.Fatalf("最后一条应胜出：got=%+v err=%v", got, err)
	}
	if _, err := s.RegisterWorkBranch(card.ID, "", "cli:u@h"); err != nil {
		t.Fatalf("清除登记: %v", err)
	}
	if _, err := s.WorkBranch(card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("清除后应 ErrNotFound，实得 %v", err)
	}
}

// TestB382RegisterAuditTrail 接缝 2：登记事件流可查「谁/何时/哪条」，穿过 appendEvent
// 的真实 JSON 序列化与 SQLite 存取边界。
func TestB382RegisterAuditTrail(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "审计留痕")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/audit", "cli:alice@host"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var found Event
	for _, e := range events {
		if e.Type == EvWorkBranchRegistered {
			found = e
		}
	}
	if found.Seq == 0 {
		t.Fatal("未找到 work_branch_registered 事件")
	}
	if found.Actor != "cli:alice@host" {
		t.Fatalf("actor（谁）= %q", found.Actor)
	}
	if found.CreatedAt.IsZero() {
		t.Fatal("created_at（何时）不应为零值")
	}
	var payload struct {
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("解码登记载荷: %v", err)
	}
	if payload.Branch != "feat/audit" {
		t.Fatalf("payload.branch（哪条）= %q", payload.Branch)
	}
}

// TestB382RegisterClearLeavesAudit 接缝 2 边界：清除也留痕（空 branch 的登记事件）。
func TestB382RegisterClearLeavesAudit(t *testing.T) {
	s := seedStore(t)
	card := mk(t, s, "清除留痕")
	if _, err := s.RegisterWorkBranch(card.ID, "feat/x", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if _, err := s.RegisterWorkBranch(card.ID, "", "cli:u@h"); err != nil {
		t.Fatalf("清除: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	regs := 0
	for _, e := range events {
		if e.Type == EvWorkBranchRegistered {
			regs++
		}
	}
	if regs != 2 {
		t.Fatalf("清除应留下第二条登记事件，实得 %d 条", regs)
	}
}

// TestB382WorkBranchRegistrationJSONRoundTrip 序列化边界（内部锁声明见 §10）：
// 载荷 encode∘decode 恒等，含空串（清除）分支；并断言 branch 键恒在场（字段缺失与
// 空值在本语义下同为「清除」，故不引入指针类型，但用 map 探键区分「键在且为空」）。
func TestB382WorkBranchRegistrationJSONRoundTrip(t *testing.T) {
	for _, branch := range []string{"feat/x", ""} {
		raw, err := json.Marshal(WorkBranchRegistration{Branch: branch})
		if err != nil {
			t.Fatalf("marshal %q: %v", branch, err)
		}
		var back WorkBranchRegistration
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal %q: %v", branch, err)
		}
		if back.Branch != branch {
			t.Fatalf("roundtrip %q -> %q", branch, back.Branch)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal map: %v", err)
		}
		if _, ok := m["branch"]; !ok {
			t.Fatalf("载荷必须恒带 branch 键：%s", raw)
		}
	}
}
