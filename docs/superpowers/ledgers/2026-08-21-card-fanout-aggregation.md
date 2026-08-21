# 子卡扇出聚合闸与递归护栏执行账本

- 2026-08-21：Task 1 双裁决通过；spec 符合：新增 `Gate.RequireChildrenDone`，父卡转移到目标节点前在同一事务检查直接子卡，已完成/终止均算完结，无子卡空洞放行，拒绝错误包装 `ErrGateBlocked` 并点名未完结子卡；代码质量：拒绝路径 Warn 带 `card/to/pending`，查询使用 `s.q`，中文 why 注释完整，`go test ./internal/ledger/` 与 `gofmt -l internal web 2>/dev/null` 通过。Task 1 commit 范围：`internal/ledger/types.go`、`internal/ledger/move.go`、`internal/ledger/move_children_gate_test.go`、本 ledger。
- 2026-08-21：Task 2 双裁决通过；spec 符合：`CardView` 新增 `children_total/children_done`，只统计直接子卡，完结语义统一为已完成或终止，统计为查询期派生不落列；代码质量：全量状态表避免列表过滤漏计，父子查询使用 `s.q`，失败带“读父子表”上下文，中文 why 注释完整，`go test ./internal/ledger/` 与 `gofmt -l internal web 2>/dev/null` 通过。Task 2 commit 范围：`internal/ledger/types.go`、`internal/ledger/derived.go`、`internal/ledger/derived_children_test.go`、本 ledger。
