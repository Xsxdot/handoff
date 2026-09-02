# B286 acceptance 台账

- 2026-08-28 review 轮相对 implement 改了 `cmd/card_node.go` 与 `internal/ledgerstep/node.go` 各一处**函数注释**（`b8eec02c`）。无行为 diff。opening 把自己当实现者。注释属实，协调者 cherry-pick 进 `cards/B286-charter-2`（`e1c33929`），记违纪：review 动了生产文件，但不是 C1.5 那种自审自批实现。
- 2026-08-28 复跑 `go test ./cmd -count=1` ok 6.788s；`go test ./internal/ledgerstep -count=1` ok 1.596s（含注释提交后）。
- 2026-08-28 M1 唯一：`if !isReviewLedgerPath(path)` 加 `false &&`。`TestNodeStepReviewPurposeRejectsOutOfBoundsPaths` FAIL `Action:pass`。回滚复绿。
- 2026-08-28 M2 唯一：`Reason != "派发失败"` 改 `==`。`TestCardDispatchStepReportsNewDispatchFailure` FAIL 必须非零退出。回滚复绿。
- 2026-08-28 M3 唯一：`observed.Dispatch = nil`。`TestCardDispatchStepReportsNewDispatchSnapshot` FAIL 缺少 comment 正文。回滚复绿。
- 真机活 `card dispatch --step` 要新 CLI + 新本机 agentd。现役 agentd 未重装，本卡不代重装。合 main 门是上述两包测试。
- 无代码图视图 diff，跳过图对账。无原型。
