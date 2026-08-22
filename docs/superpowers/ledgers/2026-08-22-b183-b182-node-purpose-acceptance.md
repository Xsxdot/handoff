# B183 + B182 节点用途与验收判据执行 ledger

- Task 1 修复轮 1：`runner.go` 传递节点判据开关时发现 `NodeDef.OmitAcceptance` 尚未存在；补齐零值兼容字段，commit 范围为 `internal/ledger/types.go`。
- Task 1 完成：用途覆盖已贯通模板派发、分支/基线/轮次、挂账快照、节点 runner 与回归测试；`go test ./internal/ledgerstep/ ./internal/ledger/` 通过，commit 范围为 Task 1 后端文件及本 ledger。
