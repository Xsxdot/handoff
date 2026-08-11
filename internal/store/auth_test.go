// store 鉴权表测试：ticket 的一次性与并发原子性、明文不落库、会话增删查与吊销。
package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xushixin/handoff/internal/store"
)

// openTestStore 打开一个临时库并注册清理，同时返回库文件路径（供白盒断言旁路开库）。
func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handoff.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

// TestConsumeAuthTicketOnce 钉死 spec §12 断言 4：同一张 ticket 第二次消费必败。
func TestConsumeAuthTicketOnce(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now()
	hash := store.HashCredential("plain-ticket")
	if err := st.CreateAuthTicket(hash, "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	device, expires, err := st.ConsumeAuthTicket(hash, now)
	if err != nil {
		t.Fatalf("首次消费应成功，得到: %v", err)
	}
	if device != "mbp" {
		t.Errorf("设备名 = %q，期望 mbp", device)
	}
	if !expires.After(now) {
		t.Errorf("过期时刻 %v 应晚于 %v", expires, now)
	}
	if _, _, err := st.ConsumeAuthTicket(hash, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("二次消费应 ErrNotFound，得到: %v", err)
	}
}

// TestConsumeAuthTicketConcurrent 钉死 spec §12 断言 5：并发消费恰好一个成功。
//
// 这条测的是 SQL 条件 UPDATE 的原子性本身——若实现退化成「先查后改」，
// 本测试会看到多个成功。
func TestConsumeAuthTicketConcurrent(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now()
	hash := store.HashCredential("race-ticket")
	if err := st.CreateAuthTicket(hash, "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	const n = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		okN  int
		errs []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.ConsumeAuthTicket(hash, now)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okN++
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				errs = append(errs, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("出现非预期错误: %v", errs)
	}
	if okN != 1 {
		t.Fatalf("成功消费次数 = %d，期望恰好 1", okN)
	}
}

// TestAuthTicketPlaintextNotStored 钉死 spec §12 断言 7：ticket 明文不落库。
func TestAuthTicketPlaintextNotStored(t *testing.T) {
	st, path := openTestStore(t)
	now := time.Now()
	const plain = "super-secret-plaintext"
	if err := st.CreateAuthTicket(store.HashCredential(plain), "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	if got := dumpColumn(t, path, "SELECT id FROM auth_tickets"); got == plain {
		t.Fatalf("库中出现 ticket 明文: %q", got)
	}
}

// TestSessionLifecycle 覆盖会话的建、查（按哈希/按 id）、列、续期、吊销。
func TestSessionLifecycle(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	sess := &store.Session{
		ID:         "sess-1",
		TokenHash:  store.HashCredential("cookie-plain"),
		DeviceName: "mbp / Safari",
		CreatedAt:  now,
		ExpiresAt:  now.Add(30 * 24 * time.Hour),
		LastSeenAt: now,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := st.SessionByTokenHash(store.HashCredential("cookie-plain"))
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if got.ID != "sess-1" || got.DeviceName != "mbp / Safari" || got.RevokedAt != nil {
		t.Fatalf("查回的会话不对: %+v", got)
	}
	if _, err := st.SessionByTokenHash(store.HashCredential("wrong")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("错误 cookie 应 ErrNotFound，得到: %v", err)
	}

	newExpires := now.Add(60 * 24 * time.Hour)
	newSeen := now.Add(time.Hour)
	if err := st.TouchSession("sess-1", newSeen, newExpires); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, err = st.SessionByID("sess-1")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !got.ExpiresAt.Equal(newExpires) || !got.LastSeenAt.Equal(newSeen) {
		t.Fatalf("续期未写回: expires=%v last_seen=%v", got.ExpiresAt, got.LastSeenAt)
	}

	list, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("会话条数 = %d，期望 1", len(list))
	}

	if err := st.RevokeSession("sess-1", now.Add(2*time.Hour)); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	got, err = st.SessionByID("sess-1")
	if err != nil {
		t.Fatalf("吊销后仍应能查到（用于展示与复验）: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("吊销后 RevokedAt 仍为 nil")
	}
	if err := st.RevokeSession("sess-1", now.Add(3*time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("重复吊销应 ErrNotFound，得到: %v", err)
	}
	if err := st.RevokeSession("不存在", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("吊销不存在会话应 ErrNotFound，得到: %v", err)
	}
}

// dumpColumn 旁路开库读一列的第一行，用于「明文不落库」这类白盒断言。
//
// 为什么另开一个连接而不加一个 Store 的导出查询方法：只为测试而在生产 API 上
// 开一个任意 SQL 的口子，代价远大于这里多两行。
func dumpColumn(t *testing.T, path, query string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("旁路打开库: %v", err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRow(query).Scan(&v); err != nil {
		t.Fatalf("查询 %s: %v", query, err)
	}
	return v
}
