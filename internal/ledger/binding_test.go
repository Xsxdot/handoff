// 绑定席位与活性租约的行为锁（B156.2 契约 §3.2 / §4「绑定与心跳」八条）。
// 本文件只测 Store 公开门面；缝级断言（client.LedgerClient 实现侧）在
// internal/ledger/api/api_test.go。carrier 断言直查 SQL 列：该字段本期无
// 任何 struct/wire 投影（澄清一：只存不解释），更高缝上构造不出该断言。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
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

func TestBindSeatAndRebindSeatUseAtomicSeatContract(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "席位", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := proto.EncodeSeatIdentity("codex", "thread-01")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c.ID, first, proto.SeatSourceBind); err != nil {
		t.Fatalf("空座 bind: %v", err)
	}
	got, err := s.GetCard(c.ID)
	if err != nil || got.DriverSession != first || got.DriverSource != string(proto.SeatSourceBind) || got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("bind 应写规范身份、来源和时间: err=%v card=%+v", err, got)
	}
	if evs := countTakeoverEvents(t, s, c.ID); len(evs) != 0 {
		t.Fatalf("bind 不应落 takeover 事件: %+v", evs)
	}
	second, _ := proto.EncodeSeatIdentity("opencode", "thread-02")
	if err := s.BindSeat(c.ID, second, proto.SeatSourceCoordinate); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("已有合法席位 bind 应冲突: %v", err)
	}
	if after, _ := s.GetCard(c.ID); after.DriverSession != first || after.DriverSource != string(proto.SeatSourceBind) {
		t.Fatalf("bind 冲突不得修改席位: %+v", after)
	}
	if err := s.RebindSeat(c.ID, second, proto.SeatSourceCoordinate, "wrong"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("expect 不符应冲突: %v", err)
	}
	unchanged, _ := s.GetCard(c.ID)
	if unchanged.DriverSession != first || unchanged.DriverSource != string(proto.SeatSourceBind) {
		t.Fatalf("CAS 冲突不得修改身份和来源: %+v", unchanged)
	}
	if err := s.RebindSeat(c.ID, second, proto.SeatSourceCoordinate, first); err != nil {
		t.Fatalf("正确 expect 换绑: %v", err)
	}
	final, _ := s.GetCard(c.ID)
	if final.DriverSession != second || final.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("换绑应覆盖身份和来源: %+v", final)
	}
	evs := countTakeoverEvents(t, s, c.ID)
	if len(evs) != 1 || evs[0].Actor != second {
		t.Fatalf("换绑应恰落一条新身份 actor 事件: %+v", evs)
	}
	var payload map[string]string
	if err := json.Unmarshal(evs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["from"] != first || payload["to"] != second {
		t.Fatalf("换绑事件 payload 应精确 from/to: %v", payload)
	}
}

func TestBindSeatRejectsLegacyAndRebindRejectsEmptySeat(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "旧席位", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE cards SET driver_session = ? WHERE id = ?`, "cli:old@host", c.ID); err != nil {
		t.Fatal(err)
	}
	identity, _ := proto.EncodeSeatIdentity("codex", "thread-03")
	if err := s.BindSeat(c.ID, identity, proto.SeatSourceBind); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("非法旧席位也应视为占用: %v", err)
	}
	legacy, _ := s.GetCard(c.ID)
	if legacy.DriverSession != "cli:old@host" || legacy.DriverSource != "" {
		t.Fatalf("非法旧席位必须原样保留: %+v", legacy)
	}
	if err := s.RebindSeat(c.ID, identity, proto.SeatSourceBind, ""); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("空座 rebind 应拒绝: %v", err)
	}
	if err := s.RebindSeat("missing", identity, proto.SeatSourceBind, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知卡应返回 ErrNotFound: %v", err)
	}
}

func TestRebindSeatRejectsSourceOnlyLegacySeat(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "来源孤儿席位", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE cards SET driver_source = ? WHERE id = ?`, string(proto.SeatSourceCoordinate), c.ID); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RebindSeat(c.ID, "cli:codex#replacement", proto.SeatSourceBind, ""); !errors.Is(err, ErrBadState) {
		t.Fatalf("仅来源席位应拒绝换绑并返回状态错误: %v", err)
	}
	after, err := s.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.DriverSession != before.DriverSession || after.DriverSource != before.DriverSource {
		t.Fatalf("非法旧席位拒绝时不得改动: before=%+v after=%+v", before, after)
	}
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

// 兼容入口不得再写协调者席位或 carrier。
func TestClaimCardAsDoesNotWriteSeat(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "载体认领", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)

	if err := s.ClaimCardAs("B99999", "cli:a@h", "console:x"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧入口应明确停用: %v", err)
	}
	if err := s.ClaimCardAs(c.ID, "cli:a@h", "console:macbook"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧入口应明确停用: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Status != before.Status || got.DriverSession != before.DriverSession || !got.DriverHeartbeatAt.Equal(before.DriverHeartbeatAt) {
		t.Fatalf("旧入口不得改变席位: before=%+v after=%+v", before, got)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "" {
		t.Fatalf("旧入口不得写 carrier: %q", gotCarrier)
	}
	if evs := countTakeoverEvents(t, s, c.ID); len(evs) != 0 {
		t.Fatalf("停用入口不落事件: %+v", evs)
	}
}

func TestLegacyClaimCardDoesNotWriteSeat(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "转调", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.ClaimCard(c.ID, "cli:a@h"); !errors.Is(err, ErrBadState) {
		t.Fatalf("既有 ClaimCard 应明确停用: %v", err)
	}
	if got, _ := s.GetCard(c.ID); got.DriverSession != "" || !got.DriverHeartbeatAt.IsZero() {
		t.Fatalf("既有 ClaimCard 不得改变席位: %+v", got)
	}
}

// §4 条3反面：expect≠当前 → ErrCASConflict 且零列变更、零事件追加；
// 附不存在卡 ErrNotFound 与空目标会话拒绝。
func TestRebindDriverConflictKeepsEverything(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "换绑冲突", Project: "p", Workflow: "bug", Actor: "t"})
	before, _ := s.GetCard(c.ID)
	beforeCarrier := carrierOf(t, s, c.ID)
	if err := s.RebindDriver(c.ID, "sess-c", "car-c", "wrong"); !errors.Is(err, ErrBadState) {
		t.Fatalf("旧换绑入口应明确停用: %v", err)
	}
	after, _ := s.GetCard(c.ID)
	if after.DriverSession != before.DriverSession || !after.DriverHeartbeatAt.Equal(before.DriverHeartbeatAt) {
		t.Fatalf("冲突路径不得改任何列: %+v → %+v", before, after)
	}
	if carrierOf(t, s, c.ID) != beforeCarrier {
		t.Fatalf("冲突路径不得改任何列: carrier %q → %q", beforeCarrier, carrierOf(t, s, c.ID))
	}
	if evs := countTakeoverEvents(t, s, c.ID); len(evs) != 0 {
		t.Fatalf("冲突不得追加事件: %d", len(evs))
	}
}

// §4 条5 金样本：成功覆写两列并落恰一条 EvDriverTakeover，
// payload 恰 from/to 两键（缺键多键都红），actor=新会话。
func TestRebindDriverSuccessGoldPayload(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "换绑金样本", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.BindSeat(c.ID, "cli:codex#sess-old", proto.SeatSourceCoordinate); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindSeat(c.ID, "cli:codex#sess-new", proto.SeatSourceCoordinate, "cli:codex#sess-old"); err != nil {
		t.Fatalf("正确前值应成功: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.DriverSession != "cli:codex#sess-new" || got.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("新绑定未覆写: %q", got.DriverSession)
	}
	if gotCarrier := carrierOf(t, s, c.ID); gotCarrier != "" {
		t.Fatalf("新流程不得覆写 carrier: %q", gotCarrier)
	}
	evs := countTakeoverEvents(t, s, c.ID)
	if len(evs) != 1 {
		t.Fatalf("应恰一条 takeover: %d", len(evs))
	}
	if evs[0].Actor != "cli:codex#sess-new" {
		t.Fatalf("actor 应为新会话: %q", evs[0].Actor)
	}
	var payload map[string]string
	if err := json.Unmarshal(evs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["from"] != "cli:codex#sess-old" || payload["to"] != "cli:codex#sess-new" {
		t.Fatalf("payload 应恰有 from/to 两键: %v", payload)
	}
}

func TestRebindDriverEmptyExpectRequiresUnbound(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "空期望", Project: "p", Workflow: "bug", Actor: "t"})
	if err := s.RebindSeat(c.ID, "cli:codex#first", proto.SeatSourceBind, ""); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("空座换绑应 CAS 冲突: %v", err)
	}
	if err := s.BindSeat(c.ID, "cli:codex#first", proto.SeatSourceBind); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindSeat(c.ID, "cli:codex#second", proto.SeatSourceBind, ""); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("有绑定但 expect 为空应 CAS 冲突: %v", err)
	}
}

// 澄清一反面断言：旧 carrier 列仍可读取，但新席位写面不触碰它。
func TestCarrierOpaqueRoundTrip(t *testing.T) {
	s := seedStore(t)
	c, _ := s.CreateCard(NewCard{Title: "不透明", Project: "p", Workflow: "bug", Actor: "t"})
	carrier := strings.Repeat("x", 4096)
	if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_carrier = ? WHERE id = ?`), carrier, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSeat(c.ID, "cli:codex#opaque", proto.SeatSourceBind); err != nil {
		t.Fatal(err)
	}
	if got := carrierOf(t, s, c.ID); got != carrier {
		t.Fatalf("新席位写面不得改 carrier: got len=%d want len=%d", len(got), len(carrier))
	}
}
