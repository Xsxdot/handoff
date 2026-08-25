// 绑定席位与活性租约的行为锁（B156.2 契约 §3.2 / §4「绑定与心跳」八条）。
// 本文件只测 Store 公开门面；缝级断言（client.LedgerClient 实现侧）在
// internal/ledger/api/api_test.go。carrier 断言直查 SQL 列：该字段本期无
// 任何 struct/wire 投影（澄清一：只存不解释），更高缝上构造不出该断言。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 锁 Open 的迁移义务：旧库（无 driver_carrier 列）升级后可读可写、
// 存量绑定保活、二次 Open 幂等。
func TestOpenMigratesLegacyCardsForCarrierAndLeaseTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 旧形态 cards 表：照抄 store.go SQLite 分支但去掉 driver_carrier，
	// 再塞一行带绑定的存量卡——模拟升级前的真实部署。
	if _, err := legacy.Exec(`CREATE TABLE cards (
		id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
		terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
		project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
		workflow_name TEXT NOT NULL, workflow_version INTEGER NOT NULL,
		attachments TEXT NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
		base_branch TEXT, driver_session TEXT, driver_heartbeat_at TEXT,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := legacy.Exec(`INSERT INTO cards (id, title, status, priority, project,
		workflow_name, workflow_version, attachments, driver_session, created_at, updated_at)
		VALUES ('B9001','存量卡','进行中','中','p','bug',1,'[]','cli:old@h',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("旧库升级 Open 失败: %v", err)
	}
	defer s.Close()
	var carrier sql.NullString
	if err := s.db.QueryRow(`SELECT driver_carrier FROM cards WHERE id = 'B9001'`).Scan(&carrier); err != nil {
		t.Fatalf("升列后应可读 driver_carrier（存量行取零值）: %v", err)
	}
	got, err := s.GetCard("B9001")
	if err != nil || got.DriverSession != "cli:old@h" {
		t.Fatalf("存量绑定不得丢失: %v %+v", err, got)
	}
	if _, err := s.db.Exec(`INSERT INTO driver_leases (session, expires_at) VALUES (?, ?)`,
		"sess-mig", s.tval(time.Now())); err != nil {
		t.Fatalf("driver_leases 应已建表: %v", err)
	}
	s.Close()
	// 幂等：第二次 Open 不得因重复加列报错（双文案容忍的回归点）。
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("二次 Open 应幂等: %v", err)
	}
	s2.Close()
}

// leaseExpires 直查租约行，供断言「库里的真实值」而非经被测方法的读回。
func leaseExpires(t *testing.T, s *Store, session string) (time.Time, bool) {
	t.Helper()
	var expiresAt any
	err := s.db.QueryRow(s.q(`SELECT expires_at FROM driver_leases WHERE session = ?`), session).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatalf("读租约行: %v", err)
	}
	return toTime(expiresAt), true
}

// §4 条7：upsert 自己那行；他人行不动；常量冻结值；时间走注入时钟；
// 空 session 拒绝（照 ClaimCard 空 owner 先例 move.go:146-149）。
func TestRenewDriverLeaseUpsertsOwnRowOnly(t *testing.T) {
	s := seedStore(t)
	if DriverLeaseTTL != 5*time.Minute || DriverLeaseRenewInterval != 2*time.Minute {
		t.Fatalf("租期常量被改: ttl=%v interval=%v", DriverLeaseTTL, DriverLeaseRenewInterval)
	}
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	cur := base
	s.now = func() time.Time { return cur }

	if ok, err := s.RenewDriverLease("sess-a", time.Minute); err != nil || !ok {
		t.Fatalf("首建应生效: %v %v", ok, err)
	}
	expA, ok := leaseExpires(t, s, "sess-a")
	if !ok || !expA.Equal(base.Add(time.Minute)) {
		t.Fatalf("首建行 expires_at=%v want %v", expA, base.Add(time.Minute))
	}
	cur = base.Add(30 * time.Second)
	if ok, err := s.RenewDriverLease("sess-b", 3*time.Minute); err != nil || !ok {
		t.Fatalf("第二会话首建: %v %v", ok, err)
	}
	if expA2, _ := leaseExpires(t, s, "sess-a"); !expA2.Equal(expA) {
		t.Fatalf("续 sess-b 不得动 sess-a 行: %v → %v", expA, expA2)
	}
	cur = base.Add(time.Minute)
	if ok, err := s.RenewDriverLease("sess-a", 2*time.Minute); err != nil || !ok {
		t.Fatalf("续期: %v %v", ok, err)
	}
	if expA3, _ := leaseExpires(t, s, "sess-a"); !expA3.Equal(cur.Add(2 * time.Minute)) {
		t.Fatalf("续期应按注入时钟重算: got %v want %v", expA3, cur.Add(2*time.Minute))
	}
	if ok, err := s.RenewDriverLease("", time.Minute); err == nil || ok {
		t.Fatalf("空 session 应拒绝: ok=%v err=%v", ok, err)
	}
	if _, ok := leaseExpires(t, s, ""); ok {
		t.Fatal("空 session 不得落行")
	}
}

// §4 条6+8：不存在→exists=false；读面不过滤过期（RunLockOf 同一约定）；
// Drop 幂等删。
func TestDriverLeaseReadsReturnExpiredRowsAndMissing(t *testing.T) {
	s := seedStore(t)
	if _, ok, err := s.DriverLeaseOf("ghost"); err != nil || ok {
		t.Fatalf("不存在 session 应 false,nil: %v %v", ok, err)
	}
	all, err := s.AllDriverLeases()
	if err != nil || len(all) != 0 {
		t.Fatalf("空表应返回空切片: %v %v", all, err)
	}
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	if ok, _ := s.RenewDriverLease("stale", -time.Minute); !ok {
		t.Fatal("负 TTL 造过期行应生效")
	}
	lease, ok, err := s.DriverLeaseOf("stale")
	if err != nil || !ok || !lease.ExpiresAt.Before(base) || lease.Session != "stale" {
		t.Fatalf("读面不过滤过期行且带 Session: %+v ok=%v err=%v", lease, ok, err)
	}
	if err := s.DropDriverLease("stale"); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if err := s.DropDriverLease("stale"); err != nil { // 再删仍 nil＝幂等
		t.Fatalf("删除应幂等: %v", err)
	}
	if _, ok, _ := s.DriverLeaseOf("stale"); ok {
		t.Fatal("删除后行应消失")
	}
}

// 看板批量判活的输入契约：全量、按 session 排序、不过滤过期。
func TestAllDriverLeasesReturnsAllRowsSorted(t *testing.T) {
	s := seedStore(t)
	base := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	for _, tc := range []struct {
		sess string
		off  time.Duration
	}{
		{"sess-c", time.Minute}, {"sess-a", -time.Minute}, {"sess-b", 3 * time.Minute},
	} {
		if ok, err := s.RenewDriverLease(tc.sess, tc.off); err != nil || !ok {
			t.Fatalf("造行 %s: %v %v", tc.sess, ok, err)
		}
	}
	all, err := s.AllDriverLeases()
	if err != nil || len(all) != 3 {
		t.Fatalf("应 3 行含过期行: %+v %v", all, err)
	}
	if !(all[0].Session < all[1].Session && all[1].Session < all[2].Session) {
		t.Fatalf("应按 session 升序: %+v", all)
	}
	for _, l := range all {
		if l.ExpiresAt.IsZero() {
			t.Fatalf("ExpiresAt 未解码: %+v", l)
		}
	}
}

// carrierOf 直查列值。carrier 本期无 struct/wire 投影（澄清一），SQL 层是
// 唯一能构造「原样存取」断言的入口（内部锁声明见计划 §4）。
func carrierOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	var carrier sql.NullString
	if err := s.db.QueryRow(s.q(`SELECT driver_carrier FROM cards WHERE id = ?`), id).Scan(&carrier); err != nil {
		t.Fatalf("读 carrier 列: %v", err)
	}
	return carrier.String
}

// countTakeoverEvents 取该卡的 EvDriverTakeover 事件切片。
func countTakeoverEvents(t *testing.T, s *Store, id string) []Event {
	t.Helper()
	events, err := s.EventsFromAsc([]string{id}, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, e := range events {
		if e.Type == EvDriverTakeover {
			out = append(out, e)
		}
	}
	return out
}

// §4 条1：ClaimCardAs 与 ClaimCard 同语义全集＋另写 driver_carrier 列。
func TestClaimCardAsWritesCarrierColumns(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "载体认领", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)

	if err := s.ClaimCardAs("B99999", "cli:a@h", "console:x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	if err := s.ClaimCardAs(c.ID, "", "console:x"); err == nil {
		t.Fatal("空 owner 应被拒")
	}
	if err := s.ClaimCardAs(c.ID, "cli:a@h", "console:macbook"); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != before.Status || got.DriverSession != "cli:a@h" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("语义应与 ClaimCard 一致（不改状态、写认领时刻）: %+v", got)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "console:macbook" {
		t.Fatalf("carrier 应落列: %q", gotCarrier)
	}
	if evs := countTakeoverEvents(t, s, c.ID); len(evs) != 0 {
		t.Fatalf("认领不落事件: %+v", evs)
	}
	if err := s.ClaimCardAs(c.ID, "cli:b@h", "other"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("他主持有应 CAS 冲突: %v", err)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "console:macbook" {
		t.Fatalf("冲突路径不得动 carrier: %q", gotCarrier)
	}
	if err := s.ClaimCardAs(c.ID, "cli:a@h", "console:macbook"); err != nil {
		t.Fatalf("同 owner 重入应幂等: %v", err)
	}
	_ = s.MoveCard(c.ID, StatusDone, "", "t")
	if err := s.ClaimCardAs(c.ID, "cli:c@h", "x"); !errors.Is(err, ErrBadState) {
		t.Fatalf("终态卡认领应 ErrBadState: %v", err)
	}
}

// §4 条2：既有 ClaimCard 行为逐字节不变＋内部转调传空载体。
// 变异靶：把 ClaimCard 恢复成旧体内联 UPDATE（不写 carrier）→ 归零断言红。
func TestLegacyClaimCardDelegatesWithEmptyCarrier(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "转调", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.ClaimCardAs(c.ID, "cli:a@h", "desktop:y"); err != nil {
		t.Fatalf("预置载体认领: %v", err)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "desktop:y" { // 前置非空，防断言空转
		t.Fatalf("前置载体应已落列: %q", gotCarrier)
	}
	// 老 API 重入（同 owner 幂等）：走转调路径，空载体覆盖历史值＝未登记载体。
	if err := s.ClaimCard(c.ID, "cli:a@h"); err != nil {
		t.Fatalf("既有 ClaimCard 重入: %v", err)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "" {
		t.Fatalf("ClaimCard 转调应传空载体: %q", gotCarrier)
	}
	if got, _ := s.GetCard(c.ID); got.DriverSession != "cli:a@h" || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("既有语义不得变: %+v", got)
	}
}

// §4 条3反面：expect≠当前 → ErrCASConflict 且零列变更、零事件追加；
// 附不存在卡 ErrNotFound 与空目标会话拒绝。
func TestRebindDriverConflictKeepsEverything(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "换绑冲突", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.RebindDriver(c.ID, "sess-b", "car-b", ""); err != nil {
		t.Fatalf("无绑定卡 expect=\"\" 应成功: %v", err)
	}
	before, _ := s.GetCard(c.ID)
	beforeCarrier := carrierOf(t, s, c.ID)
	if err := s.RebindDriver(c.ID, "sess-c", "car-c", "wrong"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("expect 不符应 CAS 冲突: %v", err)
	}
	after, _ := s.GetCard(c.ID)
	if after.DriverSession != before.DriverSession || !after.DriverHeartbeatAt.Equal(before.DriverHeartbeatAt) {
		t.Fatalf("冲突路径不得改任何列: %+v → %+v", before, after)
	}
	if carrierOf(t, s, c.ID) != beforeCarrier {
		t.Fatalf("冲突路径不得改任何列: carrier %q → %q", beforeCarrier, carrierOf(t, s, c.ID))
	}
	if evs := countTakeoverEvents(t, s, c.ID); len(evs) != 1 {
		t.Fatalf("冲突不得追加事件: %d", len(evs))
	}
	if err := s.RebindDriver("B99999", "x", "y", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在卡应 ErrNotFound: %v", err)
	}
	if err := s.RebindDriver(c.ID, "", "car", "sess-b"); err == nil {
		t.Fatal("目标会话为空应被拒")
	}
	if after2, _ := s.GetCard(c.ID); after2.DriverSession != "sess-b" {
		t.Fatalf("空目标被拒后绑定不变: %q", after2.DriverSession)
	}
}

// §4 条5 金样本：成功覆写两列并落恰一条 EvDriverTakeover，
// payload 恰 from/to 两键（缺键多键都红），actor=新会话。
func TestRebindDriverSuccessGoldPayload(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "换绑金样本", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.ClaimCardAs(c.ID, "sess-old", "car-old"); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindDriver(c.ID, "sess-new", "car-new", "sess-old"); err != nil {
		t.Fatalf("正确前值应成功: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "sess-new" {
		t.Fatalf("新绑定未覆写: %q", got.DriverSession)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "car-new" {
		t.Fatalf("新载体未覆写: %q", gotCarrier)
	}
	evs := countTakeoverEvents(t, s, c.ID)
	if len(evs) != 1 {
		t.Fatalf("应恰一条 takeover: %d", len(evs))
	}
	if evs[0].Actor != "sess-new" {
		t.Fatalf("actor 应为新会话: %q", evs[0].Actor)
	}
	var payload map[string]string
	if err := json.Unmarshal(evs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["from"] != "sess-old" || payload["to"] != "sess-new" {
		t.Fatalf("payload 应恰有 from/to 两键: %v", payload)
	}
}

// expect="" 分派销账（api.go Facade.BindDriver 注释遗留）：要求当前无绑定。
func TestRebindDriverEmptyExpectRequiresUnbound(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "空期望", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.RebindDriver(c.ID, "first", "car1", ""); err != nil {
		t.Fatalf("无绑定时空 expect 应成功: %v", err)
	}
	if err := s.RebindDriver(c.ID, "second", "car2", ""); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("有绑定时空 expect 应 CAS 冲突: %v", err)
	}
}

// 澄清一反面断言：carrier 只存不解释——空格/斜杠/Unicode/超长串原样存取。
func TestCarrierOpaqueRoundTrip(t *testing.T) {
	s := seedStore(t)
	carriers := []string{
		"",
		"with space",
		"slash/ed/path",
		"控制台·桌面🚀",
		strings.Repeat("x", 4096),
		"key:value; semi=eq&more",
	}
	for i, carrier := range carriers {
		c, _ := s.CreateCard(NewCard{Title: fmt.Sprintf("不透明%d", i), Project: "p", Workflow: "bug", Actor: "t"})
		sess := fmt.Sprintf("sess-%d", i)
		if err := s.ClaimCardAs(c.ID, sess, carrier); err != nil {
			t.Fatalf("carrier %q 认领: %v", carrier, err)
		}
		if got := carrierOf(t, s, c.ID); got != carrier {
			t.Fatalf("carrier 存取不改写: got %q(len %d) want %q(len %d)", got, len(got), carrier, len(carrier))
		}
		if err := s.RebindDriver(c.ID, sess+"-next", carrier+"|rebind", sess); err != nil {
			t.Fatalf("carrier %q 换绑: %v", carrier, err)
		}
		if got := carrierOf(t, s, c.ID); got != carrier+"|rebind" {
			t.Fatalf("换绑 carrier 不改写: got %q want %q", got, carrier+"|rebind")
		}
	}
}
