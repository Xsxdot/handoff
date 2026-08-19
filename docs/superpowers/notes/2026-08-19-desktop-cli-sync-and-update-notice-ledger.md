# 桌面端与 CLI 版本同步 + 新版通知执行 ledger

起点：`67cc7a74`

范围：Task 2–Task 11；Task 1 与 Task 12 由审核者负责。

- Task 2 完成；双裁决第 1 轮通过（spec 符合、代码质量通过）；字典序变异按预期击穿，已恢复。commit 范围：`internal/selfupdate/`、`desktop/internal/shell/release.go`、`desktop/internal/shell/release_test.go`、本 ledger。
- Task 3 完成；双裁决第 1 轮通过（改名、四态判据、保守 busy 判据均符合）；两条变异均按预期击穿，已恢复。`desktop` 全量测试与 gofmt 通过；根全量测试未通过，原始失败为既有 `internal/executor/claudecode` 的 Unix socket 路径超长（`路径过长（120/122 字节，上限 107）`）。commit 范围：`desktop/internal/shell/`、`desktop/main.go`、本 ledger。
- Task 4 完成；双裁决第 1 轮通过（TAG 去 v 注入两个 Info.plist 版本键且先于实际 `package` 构建）；顺序变异与删除 `CFBundleVersion` 变异均按预期击穿，已恢复。根包测试与 gofmt 通过。commit 范围：`.github/workflows/release.yml`、`release_workflow_test.go`、本 ledger。
- Task 5 完成；双裁决第 1 轮通过（DoSync 四步顺序、skill 非致命、Activate 失败停止、临时文件清理均符合）；四条变异均按预期击穿，已恢复。`desktop` 全量测试与 gofmt 通过；根全量测试仍受既有 `internal/executor/claudecode` Unix socket 路径超长失败（原始为 `路径过长（119/121/122 字节，上限 107）`）。commit 范围：`desktop/internal/shell/sync.go`、`desktop/internal/shell/sync_test.go`、本 ledger。
- Task 6 完成；双裁决第 1 轮通过（版本相等、探测后只催一次、取消/90 秒 deadline 错误均符合）；四个原计划变异均被击穿，其中两个原测试假门已分别改写为三次探测与可注入时钟后重新验证，已恢复。`desktop` 全量测试与 gofmt 通过。GUI/真机未涉及。commit 范围：`desktop/internal/shell/waitback.go`、`desktop/internal/shell/waitback_test.go`、`desktop/internal/shell/waitback_internal_test.go`、本 ledger。
