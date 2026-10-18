// b434_retire_migrate.go —— B435 X：B434 两张退役表（seat_desktops / desktop_launchers）
// 的一次性清除迁移与留痕（plan b435.5 §0 Q1 甲 / Q7 甲；拆解 P5 甲；契约 §7）。
//
// 职责：Open 时探测两表；存在则在同一事务里「取写锁 → 探测 → 读全部行 → DROP →
// 把被删行并入 ledger_meta 留痕键」，提交后对每张受影响的卡打一条 Warn。
//
// 边界：
//   - 留痕只进 ledger_meta 单键 retiredB434RowsKey（无投影、无消费者、不触发推送）；
//     不写 card_events、不改卡席位（Q7 甲：受影响卡的 pull 席位原样保留）。
//   - 留痕值是「批次数组」：每次真删到行就追加一批；与已留痕行全字段相同的行去重
//     （旧二进制重建表后再写同一行的混版本场景，N2）。删到零新行不写键。
//   - 只读回、不回填：回退旧版本不会还原两表（旧二进制只重建空表），数据只在留痕里。
//
// 留痕值 JSON 形状（供真机 sqlite3/psql 读回，字段名即契约，勿改）：
//
//	[{"migrated_at":"<UTC RFC3339Nano>",
//	  "seat_desktops":[{"card_id","identity","desktop_session","machine","via","bound_at"}…],
//	  "desktop_launchers":[{"project","desktop_session","cli_session","machine","actor","registered_at"}…]}…]
//
// 时间列（bound_at / registered_at）两方言统一规范成 UTC RFC3339Nano 字符串；
// SQLite 旧串解析不了时保留原串（宁可原样留痕，不丢、不猜）。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// retiredB434RowsKey 是 ledger_meta 中 B434 退役两表留痕的键名。
const retiredB434RowsKey = "b435.retired_b434_rows"

// retiredB434SeatHint 是对每张受影响卡 Warn 的固定提示（Q7 甲）。
const retiredB434SeatHint = "桌面端信箱唤醒已退役（B435）：要继续在桌面端收消息，请在项目房间 handoff session sit <项目房间> --as <身份> --desktop-session <local_…>，或对该卡 handoff card rebind / handoff card wait"

// retiredSeatDesktopRow 是 seat_desktops 一行的留痕形态（列名即 JSON 键）。
type retiredSeatDesktopRow struct {
	CardID         string `json:"card_id"`
	Identity       string `json:"identity"`
	DesktopSession string `json:"desktop_session"`
	Machine        string `json:"machine"`
	Via            string `json:"via"`
	BoundAt        string `json:"bound_at"`
}

// retiredDesktopLauncherRow 是 desktop_launchers 一行的留痕形态（列名即 JSON 键）。
type retiredDesktopLauncherRow struct {
	Project        string `json:"project"`
	DesktopSession string `json:"desktop_session"`
	CLISession     string `json:"cli_session"`
	Machine        string `json:"machine"`
	Actor          string `json:"actor"`
	RegisteredAt   string `json:"registered_at"`
}

// retiredB434Batch 是一次真删到新行的迁移所追加的一批留痕。
type retiredB434Batch struct {
	MigratedAt       string                      `json:"migrated_at"`
	SeatDesktops     []retiredSeatDesktopRow     `json:"seat_desktops"`
	DesktopLaunchers []retiredDesktopLauncherRow `json:"desktop_launchers"`
}

// normalizeRetiredTime 把两方言扫描出的时间列规范成 UTC RFC3339Nano；解析不了的
// 文本原样返回（旧二进制写坏的串也要可读回），NULL 为空串。
func normalizeRetiredTime(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case []byte:
		return normalizeRetiredTime(string(t))
	case string:
		if p, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return p.UTC().Format(time.RFC3339Nano)
		}
		return t
	default:
		return fmt.Sprint(t)
	}
}

// retiredTableExistsTx 在迁移事务内探测表是否存在（两方言先例同 wakeclaim_migrate.go）。
func (s *Store) retiredTableExistsTx(tx *sql.Tx, name string) (bool, error) {
	var n int
	var err error
	if s.dialect == dialectPG {
		err = tx.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_name = $1 AND table_schema = current_schema()`, name).Scan(&n)
	} else {
		err = tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	}
	if err != nil {
		return false, fmt.Errorf("探测退役表 %s: %w", name, err)
	}
	return n > 0, nil
}

func readRetiredSeatDesktopsTx(tx *sql.Tx) ([]retiredSeatDesktopRow, error) {
	rows, err := tx.Query(`SELECT card_id, identity, desktop_session, machine, via, bound_at FROM seat_desktops`)
	if err != nil {
		return nil, fmt.Errorf("读 seat_desktops: %w", err)
	}
	defer rows.Close()
	out := []retiredSeatDesktopRow{}
	for rows.Next() {
		var r retiredSeatDesktopRow
		var at any
		if err := rows.Scan(&r.CardID, &r.Identity, &r.DesktopSession, &r.Machine, &r.Via, &at); err != nil {
			return nil, fmt.Errorf("扫 seat_desktops 行（已扫 %d）: %w", len(out), err)
		}
		r.BoundAt = normalizeRetiredTime(at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 seat_desktops: %w", err)
	}
	// Go 侧字节序：PG 默认 collation 可能非 C，ORDER BY 两方言会分叉。
	slices.SortFunc(out, func(a, b retiredSeatDesktopRow) int { return strings.Compare(a.CardID, b.CardID) })
	return out, nil
}

func readRetiredDesktopLaunchersTx(tx *sql.Tx) ([]retiredDesktopLauncherRow, error) {
	rows, err := tx.Query(`SELECT project, desktop_session, cli_session, machine, actor, registered_at FROM desktop_launchers`)
	if err != nil {
		return nil, fmt.Errorf("读 desktop_launchers: %w", err)
	}
	defer rows.Close()
	out := []retiredDesktopLauncherRow{}
	for rows.Next() {
		var r retiredDesktopLauncherRow
		var at any
		if err := rows.Scan(&r.Project, &r.DesktopSession, &r.CLISession, &r.Machine, &r.Actor, &at); err != nil {
			return nil, fmt.Errorf("扫 desktop_launchers 行（已扫 %d）: %w", len(out), err)
		}
		r.RegisteredAt = normalizeRetiredTime(at)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 desktop_launchers: %w", err)
	}
	slices.SortFunc(out, func(a, b retiredDesktopLauncherRow) int { return strings.Compare(a.Project, b.Project) })
	return out, nil
}

// decodeRetiredB434Batches 解码留痕值；空串视为无批次。
func decodeRetiredB434Batches(value string) ([]retiredB434Batch, error) {
	if value == "" {
		return nil, nil
	}
	var out []retiredB434Batch
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, fmt.Errorf("解码留痕 %s: %w", retiredB434RowsKey, err)
	}
	return out, nil
}

// rowKey 是按行全字段去重的键（表名 + 行 JSON）。
func retiredRowKey(table string, row any) string {
	b, _ := json.Marshal(row) // 纯字符串字段的结构体，Marshal 不会失败
	return table + "\x00" + string(b)
}

// migrateRetireB434Tables 清除 B434 两张退役表并留痕（每次 Open 都跑，幂等）。
//
// 返回：nil = 两表都已不存在（本次删掉或本就没有）且被删行已全部在留痕里；任一
// 步失败整个事务回滚（两表与行原样、留痕不变）并返回带步骤的错误，Open 随之失败
// ——不带病起来。并发：PG 取与 mutate 同一把 advisory xact lock，SQLite 以首条写语句
// 取写锁（跨进程在 busy_timeout 上排队），之后才探测——后到者看到的是已删后的状态。
func (s *Store) migrateRetireB434Tables() error {
	dialectName := map[bool]string{true: "postgres", false: "sqlite"}[s.dialect == dialectPG]
	tx, err := s.db.Begin()
	if err != nil {
		log().Warn("B434 退役迁移开事务失败", "dialect", dialectName, "cause", err)
		return fmt.Errorf("B434 退役迁移开事务: %w", err)
	}
	defer tx.Rollback() // 提交后是 no-op
	if s.dialect == dialectPG {
		_, err = tx.Exec(`SELECT pg_advisory_xact_lock(915601)`)
	} else {
		// 为什么先写后读：SQLite deferred 事务若首条是 SELECT，升级写锁时在 WAL 下直接
		// SQLITE_BUSY（busy_timeout 不覆盖，R SitResident 同一教训）；零行 UPDATE 让事务
		// 一开始就取写锁并排队。
		_, err = tx.Exec(`UPDATE ledger_meta SET value = value WHERE key = ?`, retiredB434RowsKey)
	}
	if err != nil {
		log().Warn("B434 退役迁移取写锁失败", "dialect", dialectName, "cause", err)
		return fmt.Errorf("B434 退役迁移取写锁: %w", err)
	}
	seatExists, err := s.retiredTableExistsTx(tx, "seat_desktops")
	if err != nil {
		log().Warn("B434 退役迁移探测失败", "dialect", dialectName, "table", "seat_desktops", "cause", err)
		return err
	}
	launcherExists, err := s.retiredTableExistsTx(tx, "desktop_launchers")
	if err != nil {
		log().Warn("B434 退役迁移探测失败", "dialect", dialectName, "table", "desktop_launchers", "cause", err)
		return err
	}
	if !seatExists && !launcherExists {
		log().Debug("B434 退役表已不存在，跳过迁移", "dialect", dialectName)
		return nil
	}
	log().Info("开始 B434 退役表迁移", "dialect", dialectName,
		"seat_desktops", seatExists, "desktop_launchers", launcherExists)

	seats := []retiredSeatDesktopRow{}
	launchers := []retiredDesktopLauncherRow{}
	if seatExists {
		if seats, err = readRetiredSeatDesktopsTx(tx); err != nil {
			log().Warn("B434 退役迁移读行失败", "dialect", dialectName, "table", "seat_desktops", "cause", err)
			return err
		}
		if _, err := tx.Exec(`DROP TABLE seat_desktops`); err != nil {
			log().Warn("B434 退役迁移删表失败", "dialect", dialectName, "table", "seat_desktops", "cause", err)
			return fmt.Errorf("删退役表 seat_desktops: %w", err)
		}
	}
	if launcherExists {
		if launchers, err = readRetiredDesktopLaunchersTx(tx); err != nil {
			log().Warn("B434 退役迁移读行失败", "dialect", dialectName, "table", "desktop_launchers", "cause", err)
			return err
		}
		if _, err := tx.Exec(`DROP TABLE desktop_launchers`); err != nil {
			log().Warn("B434 退役迁移删表失败", "dialect", dialectName, "table", "desktop_launchers", "cause", err)
			return fmt.Errorf("删退役表 desktop_launchers: %w", err)
		}
	}

	var existing string
	if err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = ?`), retiredB434RowsKey).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log().Warn("B434 退役迁移读留痕失败", "dialect", dialectName, "key", retiredB434RowsKey, "cause", err)
		return fmt.Errorf("读留痕 %s: %w", retiredB434RowsKey, err)
	}
	batches, err := decodeRetiredB434Batches(existing)
	if err != nil {
		log().Warn("B434 退役迁移留痕值损坏", "dialect", dialectName, "key", retiredB434RowsKey, "cause", err)
		return err
	}
	seen := map[string]bool{}
	for _, b := range batches {
		for _, r := range b.SeatDesktops {
			seen[retiredRowKey("seat_desktops", r)] = true
		}
		for _, r := range b.DesktopLaunchers {
			seen[retiredRowKey("desktop_launchers", r)] = true
		}
	}
	batch := retiredB434Batch{
		MigratedAt:       s.timeNow().UTC().Format(time.RFC3339Nano),
		SeatDesktops:     []retiredSeatDesktopRow{},
		DesktopLaunchers: []retiredDesktopLauncherRow{},
	}
	for _, r := range seats {
		if !seen[retiredRowKey("seat_desktops", r)] {
			batch.SeatDesktops = append(batch.SeatDesktops, r)
		}
	}
	for _, r := range launchers {
		if !seen[retiredRowKey("desktop_launchers", r)] {
			batch.DesktopLaunchers = append(batch.DesktopLaunchers, r)
		}
	}
	newRows := len(batch.SeatDesktops) + len(batch.DesktopLaunchers)
	if newRows > 0 {
		value, err := json.Marshal(append(batches, batch))
		if err != nil {
			log().Warn("B434 退役迁移编码留痕失败", "dialect", dialectName, "cause", err)
			return fmt.Errorf("编码留痕: %w", err)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO ledger_meta (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`), retiredB434RowsKey, string(value)); err != nil {
			log().Warn("B434 退役迁移写留痕失败", "dialect", dialectName, "key", retiredB434RowsKey, "cause", err)
			return fmt.Errorf("写留痕 %s: %w", retiredB434RowsKey, err)
		}
	}
	if err := tx.Commit(); err != nil {
		log().Warn("B434 退役迁移提交失败", "dialect", dialectName, "cause", err)
		return fmt.Errorf("提交 B434 退役迁移: %w", err)
	}
	// 提交后才告警：回滚的迁移没有「受影响的卡」。
	for _, r := range seats {
		log().Warn("卡的桌面端席位记录已随 B434 退役清除，席位保留但不再经桌面端信箱唤醒",
			"card", r.CardID, "identity", r.Identity, "hint", retiredB434SeatHint)
	}
	log().Info("B434 退役表迁移完成", "dialect", dialectName, "key", retiredB434RowsKey,
		"seat_desktops_dropped", len(seats), "desktop_launchers_dropped", len(launchers),
		"rows_recorded", newRows, "batches", len(batches)+map[bool]int{true: 1, false: 0}[newRows > 0])
	return nil
}

// retiredB434Batches 读回留痕（测试与排障用）；键不存在返回 nil。
func (s *Store) retiredB434Batches() ([]retiredB434Batch, error) {
	var value string
	err := s.db.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = ?`), retiredB434RowsKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读留痕 %s: %w", retiredB434RowsKey, err)
	}
	return decodeRetiredB434Batches(value)
}
