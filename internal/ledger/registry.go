// 注册表：载体、小队、点火队列等非卡实体的版本化持久化原语。
//
// 为什么是通用 KV 而不是逐实体建表（B156.3 契约拍板记录④）：账本是持久化
// 设施，编制域才是这些实体的规则所有者——账本只承诺「版本化 + CAS + 全局
// 写入序」，不理解 body 里的业务字段；实体形状的演进因此不牵动账本 schema。
// 消费方只有 internal/ledger/api 门面，域外不得直接使用本文件。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RegistryEntry 是读出的注册表行。Seq 是全表单调写入序（出队 FIFO 的依据），
// Body 是写入方序列化好的 JSON——账本不解释其内容。
type RegistryEntry struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Seq     int64           `json:"seq"`
	Body    json.RawMessage `json:"body"`
}

// RegistryPut 以 CAS 语义写入一条注册表实体，返回新版本号。
//
// expectVersion 必须等于当前版本；0 表示必须不存在（新建）。冲突返回
// ErrCASConflict 包装错误，与 MoveCard 同款判据。版本从 1 起，成功即 +1。
func (s *Store) RegistryPut(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	var version int
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if kind == "" || id == "" {
			return fmt.Errorf("registry put: kind 与 id 不能为空")
		}
		if len(body) == 0 {
			return fmt.Errorf("registry put: %s/%s: body 不能为空", kind, id)
		}
		var current int
		scanErr := tx.QueryRow(s.q(`SELECT version FROM registry WHERE kind = ? AND id = ?`), kind, id).Scan(&current)
		switch {
		case scanErr == nil:
		case errors.Is(scanErr, sql.ErrNoRows):
			current = 0 // 不存在的行按版本 0 处理：expect 0 = 新建，expect N>0 必冲突
		default:
			return fmt.Errorf("registry put 读当前版本: %w", scanErr)
		}
		if current != expectVersion {
			return fmt.Errorf("registry %s/%s 版本冲突: 期望 %d 实际 %d: %w", kind, id, expectVersion, current, ErrCASConflict)
		}
		version = current + 1
		if current == 0 {
			_, err := tx.Exec(s.q(`INSERT INTO registry (kind, id, version, seq, body, actor, updated_at)
				VALUES (?, ?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM registry), ?, ?, ?)`),
				kind, id, version, string(body), actor, s.tval(time.Now()))
			if err != nil {
				return fmt.Errorf("registry insert: %w", err)
			}
			return nil
		}
		result, err := tx.Exec(s.q(`UPDATE registry SET version = ?, seq = (SELECT COALESCE(MAX(seq), 0) + 1 FROM registry),
			body = ?, actor = ?, updated_at = ? WHERE kind = ? AND id = ? AND version = ?`),
			version, string(body), actor, s.tval(time.Now()), kind, id, current)
		if err != nil {
			return fmt.Errorf("registry update: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("registry %s/%s 并发修改: %w", kind, id, ErrCASConflict)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// RegistryGet 读一条注册表实体；不存在返回 ErrNotFound。
func (s *Store) RegistryGet(kind, id string) (RegistryEntry, error) {
	row := s.db.QueryRow(s.q(`SELECT kind, id, version, seq, body FROM registry WHERE kind = ? AND id = ?`), kind, id)
	var e RegistryEntry
	var body string
	if err := row.Scan(&e.Kind, &e.ID, &e.Version, &e.Seq, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RegistryEntry{}, fmt.Errorf("registry %s/%s: %w", kind, id, ErrNotFound)
		}
		return RegistryEntry{}, fmt.Errorf("registry get: %w", err)
	}
	e.Body = json.RawMessage(body)
	return e, nil
}

// RegistryList 按 kind 列出全部实体，按 seq 升序（写入序 = 出队序的自然读法）。
func (s *Store) RegistryList(kind string) ([]RegistryEntry, error) {
	rows, err := s.db.Query(s.q(`SELECT kind, id, version, seq, body FROM registry WHERE kind = ? ORDER BY seq ASC`), kind)
	if err != nil {
		return nil, fmt.Errorf("registry list: %w", err)
	}
	defer rows.Close()
	var out []RegistryEntry
	for rows.Next() {
		var e RegistryEntry
		var body string
		if err := rows.Scan(&e.Kind, &e.ID, &e.Version, &e.Seq, &body); err != nil {
			return nil, err
		}
		e.Body = json.RawMessage(body)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RegistryDelete 以 CAS 语义删除一条注册表实体。expectVersion 必须等于当前
// 版本（0 表示「当前必须是新建未改过的」不存在合法场景——删除前先 Get 取版本）。
func (s *Store) RegistryDelete(kind, id string, expectVersion int, actor string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		result, err := tx.Exec(s.q(`DELETE FROM registry WHERE kind = ? AND id = ? AND version = ?`),
			kind, id, expectVersion)
		if err != nil {
			return fmt.Errorf("registry delete: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("registry %s/%s 删除冲突: 版本非 %d 或已不存在: %w", kind, id, expectVersion, ErrCASConflict)
		}
		return nil
	})
}
