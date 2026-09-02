# B278 acceptance 台账

- 2026-08-28 卡进 acceptance。审查 pass，对象 `1fc1ecd64`，分支 `cards/B278-charter-2`。review 任务 `d46a8231` completed.commit 与 implement 同一 SHA。
- 复跑（本机 worktree）：`go build ./...` 退出 0。
  - `TestResolveLocalBaseBranchUses(OriginTipWhenOriginIsAhead|LocalTipWhenLocalIsAhead)|FallsBackWhenOriginUnavailable|RejectsDivergedTips` 随 `TestResolveBaseBranchConcurrentFetchUsesRemoteRef|TestResolveBaselineAndBranchShareRepoFetchLock|TestResolveBaseBranchLockContentionHasIndependentSentinel|TestWriteDispatchErrorKeepsFetchLockContentionDistinct|TestDispatchWireLocalBaseBranch` → `ok github.com/Xsxdot/handoff/internal/agentd 3.622s`。
  - `TestBuildPromptIncludesOutputPathWithoutCardContext|TestNodeStepDatePrefixed|ExactDeclared|UnrelatedDate|ViaTemplateCarriesTransportBaseCommit` → `ok github.com/Xsxdot/handoff/internal/ledgerstep 0.649s`。
  - `TestWire` ledger `ok 0.464s`；`TestCardShowLedgerWireUsesSnakeCase` `ok github.com/Xsxdot/handoff/cmd 0.536s`；`TestLedgerAPI` `ok 1.754s`。
- 变异（均先唯一锚点+编译过，已还原）：
  - M1 origin 快进改回本地尖端 → `TestResolveLocalBaseBranchUsesOriginTipWhenOriginIsAhead` FAIL。
  - M2 `RecordDispatch` 的 `BaseCommit` 写空串 → `TestViaTemplateCarriesTransportBaseCommitIntoResultAndSnapshot` FAIL。
  - M3 `withRepoFetchLock` 改成直接 `fn()` → `TestResolveBaseBranchConcurrentFetchUsesRemoteRef` FAIL，原文含 `cannot lock ref`。
  - M4 锁竞争耗尽改包 `ErrBaseCommitMissing` → `TestResolveBaseBranchLockContentionHasIndependentSentinel` FAIL。
  - M5 去掉 prompt 日期前缀句 → `TestBuildPromptIncludesOutputPathWithoutCardContext` FAIL。
  - M6 `TaskLink.TaskID` tag 改回 `TaskID` → `TestCardShowLedgerWireUsesSnakeCase` FAIL。
- 真机：`go run . card show B278` 的 `tasks[0]` 键为 `card_id/target/task_id/purpose/created_at`，无 `TaskID`。本卡无 relations。本机 `GET /api/cards/B278` 未带鉴权（16 字节），HTTP PascalCase 负例由 `TestLedgerAPI` httptest 锁定。现役 agentd 未换二进制，不要求活 GET 出现新键。
- review 三项 minor 记账不修：`manager.go:445-446` / `ledgerstep/dispatch.go:40-42` 仍写「不得补拉」；`isAncestor` 把 merge-base 非零当非祖先（函数注释已限定输入为已读到的 sha）。implement 自报 `cmd/init_test.go` /tmp 只读属环境，与 B273/B276 同族。
- 无本卡 codegraph/diffs 视图，跳过图对账。
