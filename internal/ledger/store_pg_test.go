// PG 冒烟：真 PG 上跑 schema + 基本读写。默认 skip，设
// LEDGER_TEST_PG_DSN 后启用（判据⑩落地前审核者本地跑一次）。
package ledger

import (
	"os"
	"testing"
)

func newPGStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 冒烟")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPGSchema(t *testing.T) {
	s := newPGStore(t)
	if _, err := s.db.Exec("SELECT * FROM cards LIMIT 0"); err != nil {
		t.Fatalf("cards 表: %v", err)
	}
}

// TestPGSmokeEndToEnd 在真 PG 上过一遍核心链路：seed→建卡→gate→CAS→
// 合并→裁决→事件序完整。SQLite 全量测试覆盖逻辑，这里只验方言差异
// （$N 占位、RETURNING、JSONB、partial index、pg_notify 不报错）。
//
// 清理段会删除账本表中的数据，因此 LEDGER_TEST_PG_DSN 必须指向专用测试库。
func TestPGSmokeEndToEnd(t *testing.T) {
	s := newPGStore(t)
	if err := s.EnsureDefaultWorkflows(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.EnsureMinB(9000); err != nil { // 高位垫号，避免与库内已有数据撞
		t.Fatalf("minb: %v", err)
	}
	card, err := s.CreateCard(NewCard{Title: "pg 冒烟", Project: "smoke", Workflow: "feature", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MoveCard(card.ID, "已出spec", "", "pgtest"); err == nil {
		t.Fatal("gate 在 PG 上应同样拒绝")
	}
	if err := s.AttachFile(card.ID, "spec", "s.md", "pgtest"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := s.MoveCard(card.ID, "已出spec", "", "pgtest"); err != nil {
		t.Fatalf("gate 放行: %v", err)
	}
	member, err := s.CreateCard(NewCard{Title: "成员", Project: "smoke", Workflow: "bug", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if err := s.MergeCards([]string{member.ID}, card.ID, "pgtest"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	decision, err := s.OpenDecision(card.ID, "冒烟请示", []string{"a", "b"}, "pgtest")
	if err != nil {
		t.Fatalf("decision: %v", err)
	}
	if err := s.AnswerDecision(decision.ID, "a", "pgtest"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	events, err := s.EventsFromAsc([]string{card.ID, member.ID}, 0, 100)
	if err != nil || len(events) < 5 {
		t.Fatalf("事件序: %v n=%d", err, len(events))
	}
	// 清理冒烟数据；此测试只能使用专用测试库。
	for _, table := range []string{"card_events", "card_relations", "decisions", "cards"} {
		if _, err := s.db.Exec(`DELETE FROM ` + table + ` WHERE TRUE`); err != nil {
			t.Logf("清理 %s: %v", table, err)
		}
	}
}
