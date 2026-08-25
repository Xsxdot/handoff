// 运行锁契约测试：覆盖租期内互斥、过期接管、续租持有者校验、释放与序列化金样本。
// 边界：只通过 Store 门面验证运行锁行为，不测试 agentd 的编排与展示。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func rlCard(t *testing.T, s *Store) Card {
	t.Helper()
	c, err := s.CreateCard(NewCard{Title: "运行锁", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func countEvents(t *testing.T, s *Store, cardID string) int {
	t.Helper()
	events, err := s.EventsFromAsc([]string{cardID}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(events)
}

func TestAcquireRunLockBasics(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	if _, _, err := s.AcquireRunLock("B99999", "n", "run:h1#1#1", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	before := countEvents(t, s, c.ID)
	lock, acquired, err := s.AcquireRunLock(c.ID, "implement", "run:h1#1#1", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("首取: acquired=%v err=%v", acquired, err)
	}
	if lock.Holder != "run:h1#1#1" || lock.Node != "implement" || !lock.ExpiresAt.After(lock.AcquiredAt) {
		t.Fatalf("首取行不符: %+v", lock)
	}
	if got := countEvents(t, s, c.ID); got != before {
		t.Fatalf("首取不得落卡事件: %d → %d", before, got)
	}
	other, acquired2, err := s.AcquireRunLock(c.ID, "review", "run:h2#2#2", time.Minute)
	if err != nil || acquired2 {
		t.Fatalf("租期内他人应被拒: acquired=%v err=%v", acquired2, err)
	}
	if other.Holder != "run:h1#1#1" || other.Node != "implement" {
		t.Fatalf("被拒应返回现存行: %+v", other)
	}
	after, ok, _ := s.RunLockOf(c.ID)
	if !ok || after.Holder != lock.Holder || after.Node != lock.Node ||
		!after.AcquiredAt.Equal(lock.AcquiredAt) || !after.ExpiresAt.Equal(lock.ExpiresAt) {
		t.Fatalf("被拒路径不得改原行: %+v vs %+v", after, lock)
	}
	cur := time.Now()
	s.now = func() time.Time { return cur }
	re, acq3, err := s.AcquireRunLock(c.ID, "implement", "run:h1#1#1", 2*time.Minute)
	if err != nil || !acq3 {
		t.Fatalf("同 holder 重入: %v %v", acq3, err)
	}
	if !re.AcquiredAt.Equal(lock.AcquiredAt) || !re.ExpiresAt.Equal(cur.Add(2*time.Minute)) {
		t.Fatalf("重入应只刷租期: %+v", re)
	}
	okRenewed, err := s.RenewRunLock(c.ID, "run:h1#1#1", 3*time.Minute)
	if err != nil || !okRenewed {
		t.Fatalf("持有者续租: %v %v", okRenewed, err)
	}
	row, _, _ := s.RunLockOf(c.ID)
	if !row.ExpiresAt.Equal(cur.Add(3 * time.Minute)) {
		t.Fatalf("续租后 expires_at=%v want %v", row.ExpiresAt, cur.Add(3*time.Minute))
	}
	foreigner, err := s.RenewRunLock(c.ID, "run:other#9#9", time.Minute)
	if foreigner || err != nil {
		t.Fatalf("非持有者续租应 false,nil: %v %v", foreigner, err)
	}
	row2, _, _ := s.RunLockOf(c.ID)
	if !row2.ExpiresAt.Equal(row.ExpiresAt) {
		t.Fatalf("非持有者续租不得有副作用: %+v vs %+v", row2, row)
	}
}

func TestAcquireRunLockPreemptsExpiredWithGoldPayload(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	base := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	cur := base
	s.now = func() time.Time { return cur }
	if _, acq, err := s.AcquireRunLock(c.ID, "implement", "run:old#1#1", time.Minute); err != nil || !acq {
		t.Fatalf("预占: %v %v", acq, err)
	}
	cur = base.Add(2 * time.Minute)
	lock, acq2, err := s.AcquireRunLock(c.ID, "review", "run:new#2#2", 5*time.Minute)
	if err != nil || !acq2 {
		t.Fatalf("过期后应可取得: %v %v", acq2, err)
	}
	if lock.Holder != "run:new#2#2" || lock.Node != "review" ||
		!lock.AcquiredAt.Equal(cur) || !lock.ExpiresAt.Equal(cur.Add(5*time.Minute)) {
		t.Fatalf("抢占应覆盖四个字段: %+v", lock)
	}
	events, err := s.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found *Event
	for i := range events {
		if events[i].Type == EvDriverTakeover {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatal("过期抢占必须落 driver_takeover 事件")
	}
	if found.Actor != "run:new#2#2" {
		t.Fatalf("抢占事件 actor 应为新 holder: %q", found.Actor)
	}
	var payload map[string]string
	if err := json.Unmarshal(found.Payload, &payload); err != nil {
		t.Fatalf("payload 解码: %v", err)
	}
	if len(payload) != 3 || payload["from"] != "run:old#1#1" || payload["to"] != "run:new#2#2" || payload["reason"] == "" {
		t.Fatalf("payload 应恰有 from/to/reason: %v", payload)
	}
}

func TestReleaseRunLockSemantics(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	if _, acq, err := s.AcquireRunLock(c.ID, "n", "run:a#1#1", time.Minute); err != nil || !acq {
		t.Fatalf("取得: %v %v", acq, err)
	}
	if err := s.ReleaseRunLock(c.ID, "run:b#2#2"); err != nil {
		t.Fatalf("非持有者释放应返回 nil: %v", err)
	}
	row, ok, _ := s.RunLockOf(c.ID)
	if !ok || row.Holder != "run:a#1#1" {
		t.Fatalf("非持有者释放不得动他人行: ok=%v holder=%q", ok, row.Holder)
	}
	if err := s.ReleaseRunLock(c.ID, "run:a#1#1"); err != nil {
		t.Fatalf("持有者释放: %v", err)
	}
	if _, ok, _ := s.RunLockOf(c.ID); ok {
		t.Fatal("释放后行应消失")
	}
	if _, acq, _ := s.AcquireRunLock(c.ID, "n2", "run:c#3#3", time.Minute); !acq {
		t.Fatal("释放后必须立即可被任何人取得")
	}
}

func TestRunLockReadsReturnExpiredRowsAsIs(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	if _, acq, err := s.AcquireRunLock(c.ID, "n", "run:old#1#1", -time.Minute); err != nil || !acq {
		t.Fatalf("负 TTL 造过期行: %v %v", acq, err)
	}
	row, ok, err := s.RunLockOf(c.ID)
	if err != nil || !ok || row.Holder != "run:old#1#1" || !row.ExpiresAt.Before(time.Now()) {
		t.Fatalf("应为未过滤的过期行: ok=%v err=%v row=%+v", ok, err, row)
	}
	all, err := s.AllRunLocks()
	if err != nil || len(all) != 1 || all[0].CardID != c.ID {
		t.Fatalf("AllRunLocks 应含过期行: %+v %v", all, err)
	}
}

func TestCardRunLocksPKIsCardID(t *testing.T) {
	s := seedStore(t)
	c := rlCard(t, s)
	insert := func(holder string) error {
		return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
			now := time.Now()
			_, err := tx.Exec(s.q(`INSERT INTO card_run_locks
				(card_id, node, holder, acquired_at, expires_at) VALUES (?,?,?,?,?)`),
				c.ID, "n", holder, s.tval(now), s.tval(now.Add(time.Minute)))
			return err
		})
	}
	if err := insert("run:first#1#1"); err != nil {
		t.Fatalf("首插应成功（表存在）: %v", err)
	}
	if err := insert("run:second#2#2"); err == nil {
		t.Fatal("同卡第二行必须撞 PK(card_id)")
	} else if _, ok, _ := s.RunLockOf(c.ID); !ok {
		t.Fatal("撞 PK 后首行不得被动")
	}
}
