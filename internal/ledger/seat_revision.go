// 席位状态版本（B409 U5 有界候选读契约第 6–9 条）。
//
// 会话订阅的候选读按「member 当前席位」枚举候选页，再由
// collab.Service.MessageWakeTargets 对同一页做最终判定。两段之间若发生席位
// 变更，枚举与判定会观察到不同状态；本文件提供的单调版本号是检测该竞态的
// 判据——不比较席位集合值（A→member→A 的 ABA 会让集合值前后相等而逃过检测，
// 契约拍板明确拒绝该方案）。版本只增不复用：任何已提交的席位状态变更都会
// 得到新值，历史值永不复现。
//
// 增量只挂在席位写路径上（BindSeat/RebindSeat/clearSeatTx），与会位写在同一
// mutate 事务内原子提交；「事件已提交而版本未动」的窗口不存在。被拒的席位
// 写（CAS 冲突等）不递增——版本跟着已提交状态走。
//
// 偶发的多余递增是安全的：版本只用于「页内是否变化」的等值比较，多递增只会
// 让扫描器多重读一页，不会漏检（漏检才是缺陷方向）。
package ledger

import (
	"database/sql"
	"fmt"
	"strconv"
)

// seatRevisionKey 是席位版本计数器的 ledger_meta 键（复用 sessions.go 的
// nextSessionSeqTx 同款读-增-写模式，跨方言同形）。
const seatRevisionKey = "seat_revision"

// SeatRevision 读取当前席位状态版本。无任何席位写入历史时返回 0。
func (s *Store) SeatRevision() (int64, error) {
	var raw string
	err := s.db.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = ?`), seatRevisionKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读席位状态版本: %w", err)
	}
	rev, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析席位状态版本 %q: %w", raw, err)
	}
	return rev, nil
}

// bumpSeatRevisionTx 在调用方事务内递增席位版本（读-增-写 ledger_meta 计数器）。
// 必须与席位列写入同事务，保证「版本变化 ⇔ 状态变化」原子可见。
func (s *Store) bumpSeatRevisionTx(tx *sql.Tx) error {
	var raw string
	err := tx.QueryRow(s.q(`SELECT value FROM ledger_meta WHERE key = ?`), seatRevisionKey).Scan(&raw)
	current := int64(0)
	if err == nil {
		current, _ = strconv.ParseInt(raw, 10, 64)
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("读席位状态版本: %w", err)
	}
	if _, err := tx.Exec(s.q(`INSERT INTO ledger_meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`), seatRevisionKey, strconv.FormatInt(current+1, 10)); err != nil {
		return fmt.Errorf("递增席位状态版本: %w", err)
	}
	return nil
}
