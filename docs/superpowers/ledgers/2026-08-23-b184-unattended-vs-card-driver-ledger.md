# B184 执行 ledger

- 2026-08-23 Task 1 完成；范围 `408cd912..6ebaa16e`。spec/代码质量双裁决通过；`go test ./cmd/ -count=1` 全绿，`git diff --check` 通过。变异自检：将 lookup 结果强制忽略后，`TestAttendanceReportsCardDriverInsteadOfOrphan` 原始失败为 `有卡驱动时不应标无人值守: {Unattended:true CardID: Driver: HeartbeatAge:0s}`；已恢复并复跑通过。
- 2026-08-23 Task 1 第 1 轮修复；范围 `6ebaa16e`。将兼容旧调用的 variadic `renderStatus` 收窄为显式 `renderStatusWithLookup`，避免接受并静默忽略多余 lookup；定向回归与格式检查通过。
