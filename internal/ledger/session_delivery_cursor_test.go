package ledger

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// 交付水位跨 Store 实例共享，且迟到的较小值不能倒退已交付进度。
func TestSessionDeliveryCursorPersistsAndAdvancesMonotonically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	seq, err := a.SessionDeliveryCursor("agent:main")
	if err != nil || seq != 0 {
		t.Fatalf("初始水位=%d err=%v", seq, err)
	}
	if err := a.AdvanceSessionDeliveryCursor("agent:main", 14); err != nil {
		t.Fatal(err)
	}
	if err := a.AdvanceSessionDeliveryCursor("agent:main", 12); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	seq, err = b.SessionDeliveryCursor("agent:main")
	if err != nil || seq != 14 {
		t.Fatalf("重开后水位=%d err=%v", seq, err)
	}
	seq, err = b.SessionDeliveryCursor("agent:other")
	if err != nil || seq != 0 {
		t.Fatalf("另一成员水位=%d err=%v", seq, err)
	}
}

// 设 LEDGER_TEST_PG_DSN 时，验证两条独立 PG 连接消费同一成员水位。
func TestPGSessionDeliveryCursorSharedAcrossConnections(t *testing.T) {
	a := newPGStore(t)
	b := newPGStore(t)
	member := fmt.Sprintf("agent:cursor-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = a.db.Exec(a.q(`DELETE FROM session_delivery_cursors WHERE member=?`), member)
	})
	if err := a.AdvanceSessionDeliveryCursor(member, 23); err != nil {
		t.Fatal(err)
	}
	seq, err := b.SessionDeliveryCursor(member)
	if err != nil || seq != 23 {
		t.Fatalf("另一连接水位=%d err=%v", seq, err)
	}
	if err := b.AdvanceSessionDeliveryCursor(member, 21); err != nil {
		t.Fatal(err)
	}
	seq, err = a.SessionDeliveryCursor(member)
	if err != nil || seq != 23 {
		t.Fatalf("迟到写入后水位=%d err=%v", seq, err)
	}
}
