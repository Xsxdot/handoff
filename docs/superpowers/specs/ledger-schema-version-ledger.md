# 台账：账本 schema 版本闸

- 2026-10-19 建 worktree `.claude/worktrees/ledger-schema-version`，分支 fix/ledger-schema-version 自 main a429b193（fix/ledger-open-retry 已合入）。
- 现状读数：store.go:704 ensureSchema 每次 Open 全跑；ALTER cards ×3（store.go:734/745、migrateDriverMode:665）；b434 迁移取 advisory 915601（b434_retire_migrate.go:183）；ensureOpenTicketProjection 稳态只读、自带 version（ticketprojection.go:165）。
- 判断：投影水位检查留闸外（数据水位非 schema）；SQLite 不加闸（委托方要求路径不变）；整数单调标记优于 DDL 指纹（混跑不互相覆盖）。
- 预见影响：TestPGOpenBoundedWhenCardsLocked（open_bounded_pg_test.go:16）在新语义下二次 Open 会成功，需改为清标记形状；taskstate_projection_test.go:357 DROP 投影表后重开，若走 PG 需清标记。
- spec 起草完成，待批准。
