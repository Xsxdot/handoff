// 账本 schema 版本闸（spec docs/superpowers/specs/ledger-schema-version.md）。
//
// 职责：PG 账本在 ledger_meta 存整数 schema 版本标记；Open 读到标记 ≥ 代码版本时
// 跳过全部 DDL 与一次性迁移。为什么：ALTER TABLE cards 即使列已存在也先申请
// AccessExclusiveLock，CLI 每次调用都 Open 一次，在共享库上排出 cards 锁护航。
// 边界：只管「要不要跑建库」与标记读写；建库本身仍是 ensureSchema 的闸内段。
// SQLite 不过闸（单机文件，无护航问题）。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
)

// ledgerSchemaVersion 代码期望的 PG 账本 schema 版本。改 ddlStatements 或在
// ensureSchema 闸内新增迁移时必须加 1（DDL 改动由 TestSchemaVersionPinsDDL 把关，
// Go 侧迁移测不到）。只能加法式演进：高版本库对低版本二进制必须是超集。
const ledgerSchemaVersion = 1

// schemaVersionKey 是 schema 版本标记在 ledger_meta 中的键名。
const schemaVersionKey = "schema_version"

// pgUndefinedTable 是 PG「表不存在」的 SQLSTATE。
const pgUndefinedTable = "42P01"

// pgSchemaCurrent 报告 PG 库内 schema 版本标记是否已 ≥ 代码版本（可跳过建库）。
// 返回库内版本（缺失为 0）供日志用。ledger_meta 不存在（新库/极老库）、无标记行、
// 标记不可解析都按「不是当前版本」处理，不报错；其它读错误上抛（由
// IsTransientOpenError 分类）。
func (s *Store) pgSchemaCurrent() (stored int, current bool, err error) {
	var raw string
	err = s.db.QueryRow(`SELECT value FROM ledger_meta WHERE key = $1`, schemaVersionKey).Scan(&raw)
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == pgUndefinedTable:
		log().Info("账本无 ledger_meta 表，按新库建 schema", "code_version", ledgerSchemaVersion)
		return 0, false, nil
	case errors.Is(err, sql.ErrNoRows):
		log().Info("账本无 schema 版本标记，执行建库", "code_version", ledgerSchemaVersion)
		return 0, false, nil
	case err != nil:
		log().Warn("读 schema 版本标记失败", "cause", err)
		return 0, false, fmt.Errorf("读 schema 版本标记: %w", err)
	}
	stored, convErr := strconv.Atoi(raw)
	if convErr != nil {
		log().Warn("schema 版本标记不可解析，按过期处理并重跑建库", "raw", raw, "cause", convErr)
		return 0, false, nil
	}
	return stored, stored >= ledgerSchemaVersion, nil
}

// writePGSchemaVersion 在闸内建库全部成功后把标记抬到代码版本。只升不降：库内已有
// 更大的合法整数则不覆盖（新旧二进制混跑时不把版本拉回去）；库内值不可解析则覆盖。
// 不取账本写锁 915601——单条 upsert 本身原子，与 mutate 无读-判-写耦合。
func (s *Store) writePGSchemaVersion() error {
	_, err := s.db.Exec(`INSERT INTO ledger_meta (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
		WHERE CASE WHEN ledger_meta.value ~ '^[0-9]{1,9}$'
			THEN ledger_meta.value::int < EXCLUDED.value::int ELSE true END`,
		schemaVersionKey, strconv.Itoa(ledgerSchemaVersion))
	if err != nil {
		log().Warn("写 schema 版本标记失败", "version", ledgerSchemaVersion, "cause", err)
		return fmt.Errorf("写 schema 版本标记: %w", err)
	}
	return nil
}
