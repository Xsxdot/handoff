// 席位状态版本（B409 U5 有界候选读契约第 8/9 条）：所有席位变更路径原子递增、
// 单调不复用。会话订阅扫描器用它检测「候选枚举与最终判定观察不同席位状态」
// 的页（含 A→member→A 的 ABA——集合值比较发现不了，版本比较必须发现）。
package ledger

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// cleanupB409U5Fixture 按 run 标记与卡号清掉本测试写入的 PG 夹具行（专用库只
// 保证可丢弃，不承诺每次运行前为空；按 actor 标记清理做到互不干扰）。
func cleanupB409U5Fixture(t *testing.T, s *Store, run string, cardIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, id := range cardIDs {
			if _, err := s.db.Exec(`DELETE FROM card_events WHERE card_id = $1`, id); err != nil {
				t.Errorf("清理 B409 U5 卡事件夹具: %v", err)
			}
			if _, err := s.db.Exec(`DELETE FROM seat_bearings WHERE card_id = $1`, id); err != nil {
				t.Errorf("清理 B409 U5 承载夹具: %v", err)
			}
			if _, err := s.db.Exec(`DELETE FROM cards WHERE id = $1`, id); err != nil {
				t.Errorf("清理 B409 U5 卡夹具: %v", err)
			}
		}
		if _, err := s.db.Exec(`DELETE FROM card_events WHERE actor = $1`, run); err != nil {
			t.Errorf("清理 B409 U5 事件夹具: %v", err)
		}
	})
}

func newSeatRevisionFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.PutWorkflow("charter", WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	card, err := s.CreateCard(NewCard{Project: "p", Title: "席位一致性夹具", Actor: "u5-tester"})
	if err != nil {
		t.Fatal(err)
	}
	return s, card.ID
}

func mustSeatRevision(t *testing.T, s *Store) int64 {
	t.Helper()
	rev, err := s.SeatRevision()
	if err != nil {
		t.Fatalf("读席位版本: %v", err)
	}
	return rev
}

func mustBind(t *testing.T, s *Store, card, identity string) {
	t.Helper()
	if err := s.BindSeat(card, identity, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatalf("坐下 %s → %s: %v", card, identity, err)
	}
}

func mustRebind(t *testing.T, s *Store, card, identity, expect string) {
	t.Helper()
	if err := s.RebindSeat(card, identity, proto.SeatSourceBind, expect, SeatBearing{}); err != nil {
		t.Fatalf("换绑 %s → %s: %v", card, identity, err)
	}
}

// 无席位写入时版本存在且稳定；每个席位变更路径（Bind/Rebind/Clear/终态清座）
// 各自原子递增一次。
func TestSeatRevisionIncrementsOnEverySeatWritePath(t *testing.T) {
	s, card := newSeatRevisionFixture(t)

	base := mustSeatRevision(t, s)
	if again := mustSeatRevision(t, s); again != base {
		t.Fatalf("无变更时版本必须稳定：base=%d again=%d", base, again)
	}

	mustBind(t, s, card, "cli:a#s1")
	afterBind := mustSeatRevision(t, s)
	if afterBind != base+1 {
		t.Fatalf("BindSeat 必须原子递增一次：base=%d after=%d", base, afterBind)
	}

	mustRebind(t, s, card, "cli:b#s2", "cli:a#s1")
	afterRebind := mustSeatRevision(t, s)
	if afterRebind != afterBind+1 {
		t.Fatalf("RebindSeat 必须原子递增一次：after_bind=%d after=%d", afterBind, afterRebind)
	}

	if err := s.ClearSeat(card); err != nil {
		t.Fatal(err)
	}
	afterClear := mustSeatRevision(t, s)
	if afterClear != afterRebind+1 {
		t.Fatalf("ClearSeat 必须原子递增一次：after_rebind=%d after=%d", afterRebind, afterClear)
	}

	// 终态路径（CloseCard → clearSeatTx）：先坐下再关卡。
	mustBind(t, s, card, "cli:a#s1")
	beforeClose := mustSeatRevision(t, s)
	if err := s.CloseCard(card, "取消", "tester"); err != nil {
		t.Fatalf("关卡: %v", err)
	}
	afterClose := mustSeatRevision(t, s)
	if afterClose != beforeClose+1 {
		t.Fatalf("CloseCard 清座必须原子递增一次：before=%d after=%d", beforeClose, afterClose)
	}
}

// 席位回到旧集合时版本不得复用（契约第 9 条）：A→member→A 的 ABA 之后版本
// 严格大于任一历史读数——这是集合值比较做不到的判据。
func TestSeatRevisionNotReusedAfterABA(t *testing.T) {
	s, card := newSeatRevisionFixture(t)
	const member = "cli:member#m1"
	const other = "cli:other#o1"

	rev0 := mustSeatRevision(t, s)
	mustBind(t, s, card, member) // 状态 A 集合 = {member}
	revMember1 := mustSeatRevision(t, s)
	mustRebind(t, s, card, other, member) // → {other}
	revOther := mustSeatRevision(t, s)
	mustRebind(t, s, card, member, other) // 回到 {member}：集合值与 revMember1 时刻相同
	revMember2 := mustSeatRevision(t, s)

	if revMember2 <= revOther || revMember2 <= revMember1 || revOther <= revMember1 || revMember1 <= rev0 {
		t.Fatalf("席位版本必须严格单调且不复用：rev0=%d member1=%d other=%d member2=%d",
			rev0, revMember1, revOther, revMember2)
	}
}

// 席位写被拒（CAS 冲突等）不递增版本：版本只跟着已提交的席位状态走。
func TestSeatRevisionUnchangedOnRejectedSeatWrite(t *testing.T) {
	s, card := newSeatRevisionFixture(t)
	mustBind(t, s, card, "cli:a#s1")
	base := mustSeatRevision(t, s)

	err := s.BindSeat(card, "cli:b#s2", proto.SeatSourceBind, SeatBearing{})
	if err == nil {
		t.Fatal("非空座再坐下必须被拒")
	}
	if after := mustSeatRevision(t, s); after != base {
		t.Fatalf("被拒的席位写不得递增版本：base=%d after=%d", base, after)
	}
}

// 设 LEDGER_TEST_PG_DSN 时在隔离 PG 上复跑同一金样（契约第 51–55 条的方言
// 同构要求覆盖席位版本语义）。
func TestPGSeatRevisionSameSemantics(t *testing.T) {
	s := newB409PGStore(t)
	if _, err := s.PutWorkflow("charter", WorkflowDef{States: []string{"待办", "完成"}}); err != nil {
		t.Fatal(err)
	}
	run := fmt.Sprintf("u5-seatrev-%d", time.Now().UnixNano())
	card, err := s.CreateCard(NewCard{Project: "p", Title: run, Actor: run})
	if err != nil {
		t.Fatal(err)
	}
	cleanupB409U5Fixture(t, s, run, card.ID)

	base := mustSeatRevision(t, s)
	mustBind(t, s, card.ID, "cli:pg-a#s1")
	afterBind := mustSeatRevision(t, s)
	if afterBind <= base {
		t.Fatalf("PG BindSeat 必须递增版本：base=%d after=%d", base, afterBind)
	}
	mustRebind(t, s, card.ID, "cli:pg-b#s2", "cli:pg-a#s1")
	mustRebind(t, s, card.ID, "cli:pg-a#s1", "cli:pg-b#s2")
	afterABA := mustSeatRevision(t, s)
	if afterABA <= afterBind+1 {
		t.Fatalf("PG 的 ABA 路径必须两次递增：after_bind=%d after_aba=%d", afterBind, afterABA)
	}
}
