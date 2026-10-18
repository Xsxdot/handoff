// B435 席位模式落账测试（契约 b435-contract.md §4.5 C-303/304/308/309/325/326）。
//
// 职责：锁 cards.driver_mode 的迁移回填与幂等、显式写面（BindSeatMode/RebindSeatMode）
// 与清座同事务写 mode、事务失败三列一起回滚、测试垫片（旧 BindSeat/RebindSeat）
// 的来源→模式委托，以及「包外生产代码不调旧名」的源码守卫（plan Q1 甲）。
// 边界：只测 Store 门面与真实 SQLite；PG 孪生在 b435_seat_mode_pg_test.go 门控。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b435LegacyCardsDDL 是 B435 前的 SQLite cards 表形态（有 driver_source、无 driver_mode）。
const b435LegacyCardsDDL = `CREATE TABLE cards (
	id TEXT PRIMARY KEY, title TEXT NOT NULL, status TEXT NOT NULL,
	terminate_reason TEXT, priority TEXT NOT NULL DEFAULT '中',
	project TEXT NOT NULL, parent_id TEXT REFERENCES cards(id),
	workflow_name TEXT NOT NULL, workflow_version INTEGER NOT NULL,
	attachments TEXT NOT NULL DEFAULT '[]', acceptance_criteria TEXT,
	base_branch TEXT, driver_session TEXT, driver_source TEXT, driver_heartbeat_at TEXT,
	created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`

type b435SeatRow struct {
	ID, Session, Source, Mode string
}

func b435SeatSnapshot(t *testing.T, s *Store) []b435SeatRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, COALESCE(driver_session,''), COALESCE(driver_source,''),
		COALESCE(driver_mode,'') FROM cards ORDER BY id`)
	if err != nil {
		t.Fatalf("读席位快照: %v", err)
	}
	defer rows.Close()
	var out []b435SeatRow
	for rows.Next() {
		var r b435SeatRow
		if err := rows.Scan(&r.ID, &r.Session, &r.Source, &r.Mode); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// C-325/C-326：旧库升级按来源回填；未知来源不回填；二次 Open 逐行逐列不变。
func TestB435DriverModeMigrationBackfill(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-b435.db")
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(b435LegacyCardsDDL); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range []b435SeatRow{
		{ID: "B1", Session: "cli:codex#bind1", Source: "bind"},
		{ID: "B2", Session: "cli:codex#coord1", Source: "coordinate"},
		{ID: "B3", Session: "", Source: ""},
		{ID: "B4", Session: "cli:codex#odd1", Source: "weird"},
	} {
		if _, err := legacy.Exec(`INSERT INTO cards (id, title, status, priority, project,
			workflow_name, workflow_version, attachments, driver_session, driver_source, created_at, updated_at)
			VALUES (?, '存量', '进行中', '中', 'p', 'bug', 1, '[]', ?, ?, ?, ?)`,
			r.ID, r.Session, r.Source, now, now); err != nil {
			t.Fatal(err)
		}
	}
	legacy.Close()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("旧库升级 Open 失败: %v", err)
	}
	first := b435SeatSnapshot(t, s)
	want := []b435SeatRow{
		{ID: "B1", Session: "cli:codex#bind1", Source: "bind", Mode: "pull"},
		{ID: "B2", Session: "cli:codex#coord1", Source: "coordinate", Mode: "push"},
		{ID: "B3"},
		{ID: "B4", Session: "cli:codex#odd1", Source: "weird", Mode: ""},
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("回填结果不符:\n got=%+v\nwant=%+v", first, want)
	}
	card, err := s.GetCard("B1")
	if err != nil || card.DriverMode != "pull" {
		t.Fatalf("Card 投影应读出 driver_mode=pull: %v %+v", err, card)
	}
	s.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("二次 Open 应幂等: %v", err)
	}
	defer s2.Close()
	if second := b435SeatSnapshot(t, s2); !reflect.DeepEqual(first, second) {
		t.Fatalf("二次 Open 后快照必须完全相同（C-326 幂等）:\n first=%+v\nsecond=%+v", first, second)
	}
}

func b435Card(t *testing.T, s *Store, title string) Card {
	t.Helper()
	c, err := s.CreateCard(NewCard{Title: title, Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func b435Seat(t *testing.T, s *Store, id string) b435SeatRow {
	t.Helper()
	var r b435SeatRow
	r.ID = id
	// 占位符走 s.q()：X 的 PG 孪生复用本帮手，裸 `?` 在 pgx 下是 SQLSTATE 42601（B435.5 F1）。
	if err := s.db.QueryRow(s.q(`SELECT COALESCE(driver_session,''), COALESCE(driver_source,''),
		COALESCE(driver_mode,'') FROM cards WHERE id = ?`), id).Scan(&r.Session, &r.Source, &r.Mode); err != nil {
		t.Fatal(err)
	}
	return r
}

var b435Bearing = SeatBearing{Carrier: "car", Machine: "m1"}

// C-304/C-308/C-309：显式写面与清座同事务写 mode；旧签名垫片按来源委托。
func TestB435SeatWritesMode(t *testing.T) {
	s := seedStore(t)
	pullID, pushID := "cli:codex#pull-1", "cli:codex#push-1"

	t.Run("显式写面 bind→pull、coordinate→push、双向换绑、清座", func(t *testing.T) {
		a := b435Card(t, s, "a")
		if err := s.BindSeatMode(a.ID, pullID, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, a.ID); got.Mode != "pull" || got.Session != pullID || got.Source != "bind" {
			t.Fatalf("bind 写面应写 pull: %+v", got)
		}
		if err := s.RebindSeatMode(a.ID, pushID, proto.SeatSourceCoordinate, proto.SeatModePush, pullID, b435Bearing); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, a.ID); got.Mode != "push" || got.Session != pushID || got.Source != "coordinate" {
			t.Fatalf("pull→push 换绑应写 push: %+v", got)
		}
		if err := s.RebindSeatMode(a.ID, pullID, proto.SeatSourceBind, proto.SeatModePull, pushID, SeatBearing{}); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, a.ID); got.Mode != "pull" {
			t.Fatalf("push→pull 换绑应写 pull: %+v", got)
		}
		if err := s.ClearSeat(a.ID); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, a.ID); got != (b435SeatRow{ID: a.ID}) {
			t.Fatalf("清座后三列必须全空: %+v", got)
		}
	})

	t.Run("终态关单清 mode", func(t *testing.T) {
		b := b435Card(t, s, "b")
		if err := s.BindSeatMode(b.ID, pushID, proto.SeatSourceCoordinate, proto.SeatModePush, b435Bearing); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseCard(b.ID, CloseShelved, "t"); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, b.ID); got.Mode != "" || got.Session != "" {
			t.Fatalf("终态关单后 driver_mode 必须为空: %+v", got)
		}
	})

	t.Run("非法 mode 与 bind+push 拒绝且不坐下", func(t *testing.T) {
		c := b435Card(t, s, "c")
		for _, tc := range []struct {
			source proto.SeatSource
			mode   proto.SeatMode
		}{
			{proto.SeatSourceBind, "PULL"},
			{proto.SeatSourceBind, ""},
			{proto.SeatSourceBind, "auto"},
			{proto.SeatSourceBind, proto.SeatModePush},
		} {
			err := s.BindSeatMode(c.ID, pullID, tc.source, tc.mode, SeatBearing{})
			if !errors.Is(err, ErrBadState) {
				t.Fatalf("source=%s mode=%q 应拒绝: %v", tc.source, tc.mode, err)
			}
			if got := b435Seat(t, s, c.ID); got != (b435SeatRow{ID: c.ID}) {
				t.Fatalf("拒绝时不得坐下: %+v", got)
			}
		}
	})

	t.Run("旧签名垫片：bind→pull、coordinate→push", func(t *testing.T) {
		d := b435Card(t, s, "d")
		if err := s.BindSeat(d.ID, pullID, proto.SeatSourceBind, SeatBearing{}); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, d.ID); got.Mode != "pull" {
			t.Fatalf("垫片 bind 应写 pull: %+v", got)
		}
		if err := s.RebindSeat(d.ID, pushID, proto.SeatSourceCoordinate, pullID, b435Bearing); err != nil {
			t.Fatal(err)
		}
		if got := b435Seat(t, s, d.ID); got.Mode != "push" {
			t.Fatalf("垫片 coordinate 换绑应写 push: %+v", got)
		}
	})
}

// C-304：事务内失败（落事件被触发器拒绝）→ 三列一起不变。
func TestB435SeatModeRollsBackWithSeat(t *testing.T) {
	s := seedStore(t)
	empty := b435Card(t, s, "空座")
	seated := b435Card(t, s, "已坐")
	if err := s.BindSeatMode(seated.ID, "cli:codex#old", proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	beforeEmpty, beforeSeated := b435Seat(t, s, empty.ID), b435Seat(t, s, seated.ID)
	if _, err := s.db.Exec(`CREATE TRIGGER b435_fail_events BEFORE INSERT ON card_events
		BEGIN SELECT RAISE(ABORT, 'b435 注入失败'); END`); err != nil {
		t.Fatalf("建失败注入触发器: %v", err)
	}
	if err := s.BindSeatMode(empty.ID, "cli:codex#new", proto.SeatSourceCoordinate, proto.SeatModePush, b435Bearing); err == nil {
		t.Fatal("注入失败下坐下应报错")
	}
	if err := s.RebindSeatMode(seated.ID, "cli:codex#new", proto.SeatSourceCoordinate, proto.SeatModePush, "cli:codex#old", b435Bearing); err == nil {
		t.Fatal("注入失败下换绑应报错")
	}
	if got := b435Seat(t, s, empty.ID); got != beforeEmpty {
		t.Fatalf("坐下失败三列必须回滚: before=%+v after=%+v", beforeEmpty, got)
	}
	if got := b435Seat(t, s, seated.ID); got != beforeSeated {
		t.Fatalf("换绑失败三列必须回滚: before=%+v after=%+v", beforeSeated, got)
	}
}

// plan Q1 甲 守卫：internal/ledger 包外非测试 Go 源码不调用旧名 BindSeat/RebindSeat
// （生产路径上的来源→模式推导只在迁移一处，C-326 澄清行）。
func TestB435LegacySeatWriteNotCalledInProduction(t *testing.T) {
	root := filepath.Join("..", "..")
	legacyCall := regexp.MustCompile(`\.(BindSeat|RebindSeat)\(`)
	ledgerDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "web", "plugins", ".claude":
				return filepath.SkipDir
			}
			abs, _ := filepath.Abs(path)
			if abs == ledgerDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for i, line := range strings.Split(string(raw), "\n") {
			if legacyCall.MatchString(line) {
				hits = append(hits, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("扫描面过小（%d 个文件），守卫可能失效", scanned)
	}
	if len(hits) > 0 {
		t.Fatalf("包外生产代码不得调用旧席位写面（改用 BindSeatMode/RebindSeatMode 显式传 mode）:\n%s", strings.Join(hits, "\n"))
	}
}
