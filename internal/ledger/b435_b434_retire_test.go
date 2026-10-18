// B435.5（X）B434 两表退役迁移测试（plan b435.5 §4 段 0、§0 Q1 甲 / Q7 甲，H1/H2）。
//
// 职责：用「含存量行的旧账本副本」锁住退役迁移的行为——两表删除、被删行逐字段
// 留痕可读回、卡席位不动、每张受影响卡一条 Warn、幂等（二次零改动）、旧二进制
// 重建表后再迁移只追加新行、留痕写失败整体回滚（H1）、并发升级恰一份留痕（H2）、
// 新库不写留痕键。
// 边界：只测 Store 门面与真实 SQLite；PG 孪生在 b435_b434_retire_pg_test.go 门控。
// 夹具不依赖 ddlStatements 是否还登记两表：两表一律由本文件的旧 DDL 建出。
package ledger

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b435RetireEnv 是一份「待升级的账本」：dsn 交给 Open，raw 给夹具与读回走旁路连接。
type b435RetireEnv struct {
	t   *testing.T
	dsn string
	pg  bool
}

func (e *b435RetireEnv) q(query string) string {
	return (&Store{dialect: map[bool]dialect{true: dialectPG, false: dialectSQLite}[e.pg]}).q(query)
}

// raw 开一条旁路连接（不经 Open，不跑迁移），用后由 t.Cleanup 关闭。
func (e *b435RetireEnv) raw() *sql.DB {
	e.t.Helper()
	var db *sql.DB
	var err error
	if e.pg {
		db, err = sql.Open("pgx", e.dsn)
	} else {
		db, err = sql.Open("sqlite", e.dsn+"?_pragma=busy_timeout(5000)")
	}
	if err != nil {
		e.t.Fatalf("开旁路连接: %v", err)
	}
	e.t.Cleanup(func() { db.Close() })
	return db
}

// b435RetireLegacyDDL 是 B434 时代两表的建表语句（与 ea2db47b 的 ddlStatements 逐列一致）。
func b435RetireLegacyDDL(pg bool) []string {
	ts := "TEXT"
	if pg {
		ts = "TIMESTAMPTZ"
	}
	return []string{
		`CREATE TABLE IF NOT EXISTS seat_desktops (
			card_id TEXT PRIMARY KEY, identity TEXT NOT NULL, desktop_session TEXT NOT NULL,
			machine TEXT NOT NULL, via TEXT NOT NULL, bound_at ` + ts + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS desktop_launchers (
			project TEXT PRIMARY KEY, desktop_session TEXT NOT NULL, cli_session TEXT NOT NULL,
			machine TEXT NOT NULL, actor TEXT NOT NULL, registered_at ` + ts + ` NOT NULL)`,
	}
}

func (e *b435RetireEnv) createLegacyTables(db *sql.DB) {
	e.t.Helper()
	for _, stmt := range b435RetireLegacyDDL(e.pg) {
		if _, err := db.Exec(stmt); err != nil {
			e.t.Fatalf("建旧表: %v", err)
		}
	}
}

// legacyTime 把夹具时间按方言写成旧二进制会写出的形态：SQLite 列是 TEXT（raw 为
// 非空时原样写入，用来造「解析不了」的存量串）；PG 列是 timestamptz。
func (e *b435RetireEnv) legacyTime(at time.Time, raw string) any {
	if e.pg {
		return at
	}
	if raw != "" {
		return raw
	}
	return at.Format(time.RFC3339Nano)
}

func (e *b435RetireEnv) insertSeat(db *sql.DB, r retiredSeatDesktopRow, at time.Time, raw string) {
	e.t.Helper()
	if _, err := db.Exec(e.q(`INSERT INTO seat_desktops (card_id, identity, desktop_session, machine, via, bound_at)
		VALUES (?, ?, ?, ?, ?, ?)`), r.CardID, r.Identity, r.DesktopSession, r.Machine, r.Via, e.legacyTime(at, raw)); err != nil {
		e.t.Fatalf("写存量 seat_desktops 行: %v", err)
	}
}

func (e *b435RetireEnv) insertLauncher(db *sql.DB, r retiredDesktopLauncherRow, at time.Time, raw string) {
	e.t.Helper()
	if _, err := db.Exec(e.q(`INSERT INTO desktop_launchers (project, desktop_session, cli_session, machine, actor, registered_at)
		VALUES (?, ?, ?, ?, ?, ?)`), r.Project, r.DesktopSession, r.CLISession, r.Machine, r.Actor, e.legacyTime(at, raw)); err != nil {
		e.t.Fatalf("写存量 desktop_launchers 行: %v", err)
	}
}

func (e *b435RetireEnv) tableExists(db *sql.DB, name string) bool {
	e.t.Helper()
	var n int
	var err error
	if e.pg {
		err = db.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_name = $1 AND table_schema = current_schema()`, name).Scan(&n)
	} else {
		err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	}
	if err != nil {
		e.t.Fatalf("探测表 %s: %v", name, err)
	}
	return n > 0
}

// record 读留痕键的原始值；ok=false 表示键不存在（不是空数组）。
func (e *b435RetireEnv) record(db *sql.DB) (string, bool) {
	e.t.Helper()
	var v string
	err := db.QueryRow(e.q(`SELECT value FROM ledger_meta WHERE key = ?`), retiredB434RowsKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		e.t.Fatalf("读留痕键: %v", err)
	}
	return v, true
}

// batches 经包内读函数解码留痕（Open 会再跑一次迁移——幂等，读到的就是持久值）。
func (e *b435RetireEnv) batches() []retiredB434Batch {
	e.t.Helper()
	s, err := Open(e.dsn)
	if err != nil {
		e.t.Fatalf("读留痕 Open: %v", err)
	}
	defer s.Close()
	got, err := s.retiredB434Batches()
	if err != nil {
		e.t.Fatalf("解码留痕: %v", err)
	}
	return got
}

// upgrade 模拟新二进制升级：只 Open（迁移在 ensureSchema 里接线；段 0 曾直调迁移函数）。
func (e *b435RetireEnv) upgrade() error {
	s, err := Open(e.dsn)
	if err != nil {
		return err
	}
	return s.Close()
}

// b435RetireFixture 是 ea2db47b 形态的存量：两张 bind/pull 席位卡各一行桌面端记录
// （via page/launcher，bound_at 不同，第二行带非 UTC 偏移）、一个项目的拉起器登记
// （SQLite 下 registered_at 是解析不了的旧串，验「保留原串」）。
type b435RetireFixture struct {
	seats     []retiredSeatDesktopRow
	launchers []retiredDesktopLauncherRow
	cards     []b435SeatRow
}

func newB435RetireFixture(e *b435RetireEnv) b435RetireFixture {
	e.t.Helper()
	s, err := Open(e.dsn)
	if err != nil {
		e.t.Fatalf("夹具 Open: %v", err)
	}
	if err := seedTestWorkflows(e.t, s); err != nil {
		e.t.Fatal(err)
	}
	c1 := b435Card(e.t, s, "b434 存量一")
	c2 := b435Card(e.t, s, "b434 存量二")
	sid1, sid2 := "cli:claude#retire-sid-1", "cli:claude#retire-sid-2"
	if err := s.BindSeatMode(c1.ID, sid1, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		e.t.Fatal(err)
	}
	if err := s.BindSeatMode(c2.ID, sid2, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		e.t.Fatal(err)
	}
	cards := []b435SeatRow{b435Seat(e.t, s, c1.ID), b435Seat(e.t, s, c2.ID)}
	s.Close()

	db := e.raw()
	e.createLegacyTables(db)
	t1 := time.Date(2026, 10, 1, 2, 3, 4, 123456000, time.UTC)
	t2 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	t3 := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	seats := []retiredSeatDesktopRow{
		{CardID: c1.ID, Identity: sid1, DesktopSession: "local_retire-desk-1", Machine: "", Via: "page",
			BoundAt: "2026-10-01T02:03:04.123456Z"},
		{CardID: c2.ID, Identity: sid2, DesktopSession: "local_retire-desk-2", Machine: "mac-pro", Via: "launcher",
			BoundAt: "2026-10-02T02:00:00Z"},
	}
	launcher := retiredDesktopLauncherRow{Project: "p", DesktopSession: "local_retire-launch", CLISession: "retire-launch-cli",
		Machine: "", Actor: "user:xsx", RegisteredAt: "legacy-not-a-time"}
	rawLaunch := "legacy-not-a-time"
	if e.pg {
		launcher.RegisteredAt = "2026-09-30T23:59:59Z"
		rawLaunch = ""
	}
	e.insertSeat(db, seats[0], t1, "")
	e.insertSeat(db, seats[1], t2, "")
	e.insertLauncher(db, launcher, t3, rawLaunch)
	return b435RetireFixture{seats: seats, launchers: []retiredDesktopLauncherRow{launcher}, cards: cards}
}

func (e *b435RetireEnv) cardSnapshot() []b435SeatRow {
	e.t.Helper()
	db := e.raw()
	rows, err := db.Query(`SELECT id, COALESCE(driver_session,''), COALESCE(driver_source,''),
		COALESCE(driver_mode,'') FROM cards ORDER BY id`)
	if err != nil {
		e.t.Fatalf("读席位快照: %v", err)
	}
	defer rows.Close()
	var out []b435SeatRow
	for rows.Next() {
		var r b435SeatRow
		if err := rows.Scan(&r.ID, &r.Session, &r.Source, &r.Mode); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func newB435RetireEnvSQLite(t *testing.T) *b435RetireEnv {
	return &b435RetireEnv{t: t, dsn: filepath.Join(t.TempDir(), "ledger.db")}
}

// captureB435Logs 把默认 logger 换成 JSON 缓冲，返回解析好的记录读取器。
func captureB435Logs(t *testing.T) func() []map[string]any {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&lockedWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var out []map[string]any
		for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal(line, &rec); err != nil {
				t.Fatalf("日志行不是 JSON: %q", line)
			}
			out = append(out, rec)
		}
		return out
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// retireWarns 挑出迁移对受影响卡打的 Warn（带 hint 字段的那条）。
func retireWarns(recs []map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range recs {
		if r["level"] == "WARN" && r["hint"] == retiredB434SeatHint {
			out = append(out, r)
		}
	}
	return out
}

// checkB435RetireCleared 是「清除 + 留痕」的共同断言：两表不在、留痕恰一批且逐字段
// 等于夹具、卡席位三列逐字不变。
func checkB435RetireCleared(t *testing.T, e *b435RetireEnv, fx b435RetireFixture) {
	t.Helper()
	db := e.raw()
	for _, tbl := range []string{"seat_desktops", "desktop_launchers"} {
		if e.tableExists(db, tbl) {
			t.Fatalf("迁移后表 %s 仍存在", tbl)
		}
	}
	got := e.batches()
	if len(got) != 1 {
		t.Fatalf("留痕应恰一批，got=%d: %+v", len(got), got)
	}
	if _, err := time.Parse(time.RFC3339Nano, got[0].MigratedAt); err != nil || !strings.HasSuffix(got[0].MigratedAt, "Z") {
		t.Fatalf("migrated_at 应是 UTC RFC3339Nano: %q (%v)", got[0].MigratedAt, err)
	}
	if !reflect.DeepEqual(got[0].SeatRows, fx.seats) {
		t.Fatalf("seat_desktops 留痕与存量不逐字段相等:\n got=%+v\nwant=%+v", got[0].SeatRows, fx.seats)
	}
	if !reflect.DeepEqual(got[0].LauncherRows, fx.launchers) {
		t.Fatalf("desktop_launchers 留痕与存量不逐字段相等:\n got=%+v\nwant=%+v", got[0].LauncherRows, fx.launchers)
	}
	snap := e.cardSnapshot()
	for _, want := range fx.cards {
		i := slices.IndexFunc(snap, func(r b435SeatRow) bool { return r.ID == want.ID })
		if i < 0 || snap[i] != want {
			t.Fatalf("Q7 甲：受影响卡席位必须逐字不变 want=%+v snapshot=%+v", want, snap)
		}
	}
}

// 段 0 首支：清除 + 逐字段留痕 + 席位不动 + 每卡恰一条 Warn。
func TestB435RetireMigrationClearsAndRecords(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	fx := newB435RetireFixture(e)
	logs := captureB435Logs(t)
	if err := e.upgrade(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	warns := retireWarns(logs())
	checkB435RetireCleared(t, e, fx)
	if len(warns) != len(fx.seats) {
		t.Fatalf("每张受影响卡恰一条 Warn：want=%d got=%d %+v", len(fx.seats), len(warns), warns)
	}
	for _, seat := range fx.seats {
		n := 0
		for _, w := range warns {
			if w["card"] == seat.CardID {
				n++
				if w["identity"] != seat.Identity {
					t.Fatalf("Warn 席位身份不符: %+v want %s", w, seat.Identity)
				}
				line, _ := json.Marshal(w)
				if strings.Contains(string(line), seat.DesktopSession) {
					t.Fatalf("Warn 不得带桌面端会话 id: %s", line)
				}
			}
		}
		if n != 1 {
			t.Fatalf("卡 %s 应恰一条 Warn，got=%d", seat.CardID, n)
		}
	}
	if !strings.Contains(retiredB434SeatHint, "session sit") || !strings.Contains(retiredB434SeatHint, "--desktop-session") ||
		!strings.Contains(retiredB434SeatHint, "card rebind") || !strings.Contains(retiredB434SeatHint, "card wait") {
		t.Fatalf("Q7 甲提示须指向 session sit --desktop-session / card rebind / card wait: %q", retiredB434SeatHint)
	}
}

// 二次升级零改动：留痕值字节相等、卡快照相等、两表仍不在。
func TestB435RetireMigrationIdempotent(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	fx := newB435RetireFixture(e)
	if err := e.upgrade(); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	db := e.raw()
	first, ok := e.record(db)
	if !ok {
		t.Fatal("首次迁移后留痕键必须存在")
	}
	snap := e.cardSnapshot()
	logs := captureB435Logs(t)
	if err := e.upgrade(); err != nil {
		t.Fatalf("二次迁移: %v", err)
	}
	second, _ := e.record(db)
	if first != second {
		t.Fatalf("二次迁移留痕必须字节相等:\n first=%s\nsecond=%s", first, second)
	}
	if again := e.cardSnapshot(); !reflect.DeepEqual(snap, again) {
		t.Fatalf("二次迁移卡快照变了: %+v → %+v", snap, again)
	}
	if w := retireWarns(logs()); len(w) != 0 {
		t.Fatalf("二次迁移不得再告警: %+v", w)
	}
	checkB435RetireCleared(t, e, fx)
}

// N2：旧二进制把两表按旧 DDL 重建——空表再迁移只删表；有新行时追加一批、旧批不变、
// 与已留痕行全字段相同的行去重。
func TestB435RetireMigrationRecreatedEmptyTables(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	fx := newB435RetireFixture(e)
	if err := e.upgrade(); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	db := e.raw()
	first, _ := e.record(db)

	e.createLegacyTables(db)
	if err := e.upgrade(); err != nil {
		t.Fatalf("空表再迁移: %v", err)
	}
	if e.tableExists(db, "seat_desktops") || e.tableExists(db, "desktop_launchers") {
		t.Fatal("重建的空表必须再被删除")
	}
	if v, _ := e.record(db); v != first {
		t.Fatalf("空表再迁移留痕必须不变:\n before=%s\n after=%s", first, v)
	}

	e.createLegacyTables(db)
	dup := fx.seats[0]
	e.insertSeat(db, dup, time.Date(2026, 10, 1, 2, 3, 4, 123456000, time.UTC), "")
	newLaunch := retiredDesktopLauncherRow{Project: "q", DesktopSession: "local_retire-launch-q", CLISession: "retire-q",
		Machine: "linux-01", Actor: "user:xsx", RegisteredAt: "2026-10-05T00:00:00Z"}
	e.insertLauncher(db, newLaunch, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), "")
	if err := e.upgrade(); err != nil {
		t.Fatalf("有新行再迁移: %v", err)
	}
	got := e.batches()
	if len(got) != 2 {
		t.Fatalf("新行应追加恰一批，got=%d: %+v", len(got), got)
	}
	if !reflect.DeepEqual(got[0].SeatRows, fx.seats) || !reflect.DeepEqual(got[0].LauncherRows, fx.launchers) {
		t.Fatalf("旧批必须不变: %+v", got[0])
	}
	if len(got[1].SeatRows) != 0 {
		t.Fatalf("与已留痕行全字段相同的行必须去重: %+v", got[1].SeatRows)
	}
	if !reflect.DeepEqual(got[1].LauncherRows, []retiredDesktopLauncherRow{newLaunch}) {
		t.Fatalf("新批应只含新行: %+v", got[1].LauncherRows)
	}
}

// H1：留痕写失败 → 迁移报错、两表与行原样、留痕键不存在。
func TestB435RetireMigrationAtomic(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	newB435RetireFixture(e)
	db := e.raw()
	before := b435RetireDump(t, db)
	if _, err := db.Exec(fmt.Sprintf(`CREATE TRIGGER b435_inject_fail BEFORE INSERT ON ledger_meta
		WHEN NEW.key = '%s' BEGIN SELECT RAISE(ABORT, 'b435 注入留痕写失败'); END`, retiredB434RowsKey)); err != nil {
		t.Fatal(err)
	}
	err := e.upgrade()
	if err == nil || !strings.Contains(err.Error(), "b435 注入留痕写失败") {
		t.Fatalf("留痕写失败必须让迁移报错并带原因，got=%v", err)
	}
	if !e.tableExists(db, "seat_desktops") || !e.tableExists(db, "desktop_launchers") {
		t.Fatal("H1：事务回滚后两表必须原样在")
	}
	if after := b435RetireDump(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("H1：回滚后行必须原样:\n before=%v\n  after=%v", before, after)
	}
	if v, ok := e.record(db); ok {
		t.Fatalf("H1：回滚后留痕键不得存在: %s", v)
	}
}

func b435RetireDump(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT card_id || '|' || identity || '|' || desktop_session || '|' || machine || '|' || via || '|' || bound_at FROM seat_desktops ORDER BY card_id`,
		`SELECT project || '|' || desktop_session || '|' || cli_session || '|' || machine || '|' || actor || '|' || registered_at FROM desktop_launchers ORDER BY project`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("转储: %v", err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		rows.Close()
	}
	return out
}

// H2：两个实例并发升级同一副本 → 都不报错、留痕中每行恰一次。
func TestB435RetireMigrationConcurrentOpen(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	fx := newB435RetireFixture(e)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = e.upgrade()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发升级 #%d 报错: %v", i, err)
		}
	}
	checkB435RetireCleared(t, e, fx)
}

// 新库（无存量行）：迁移 no-op，不写留痕键（键不存在，而不是空数组）。
func TestB435RetireMigrationFreshLedger(t *testing.T) {
	e := newB435RetireEnvSQLite(t)
	if err := e.upgrade(); err != nil {
		t.Fatalf("新库迁移: %v", err)
	}
	db := e.raw()
	if v, ok := e.record(db); ok {
		t.Fatalf("新库不得写留痕键: %s", v)
	}
	if e.tableExists(db, "seat_desktops") || e.tableExists(db, "desktop_launchers") {
		t.Fatal("新库迁移后两表不得存在")
	}
}
