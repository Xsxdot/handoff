# 桌面端与 CLI 版本同步 + 新版通知执行 ledger

起点：`67cc7a74`

范围：Task 2–Task 11；Task 1 与 Task 12 由审核者负责。

- Task 2 完成；双裁决第 1 轮通过（spec 符合、代码质量通过）；字典序变异按预期击穿，已恢复。commit 范围：`internal/selfupdate/`、`desktop/internal/shell/release.go`、`desktop/internal/shell/release_test.go`、本 ledger。
- Task 3 完成；双裁决第 1 轮通过（改名、四态判据、保守 busy 判据均符合）；两条变异均按预期击穿，已恢复。`desktop` 全量测试与 gofmt 通过；根全量测试未通过，原始失败为既有 `internal/executor/claudecode` 的 Unix socket 路径超长（`路径过长（120/122 字节，上限 107）`）。commit 范围：`desktop/internal/shell/`、`desktop/main.go`、本 ledger。
