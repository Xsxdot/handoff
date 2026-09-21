# B370 implement 台账（镜像身份闸降级判定）

节点：implement（输入 `docs/superpowers/plans/b370-plan.md`；breakdown §9 已拍板 F1–F7 全 A）。
卡：B370　基线分支：`cards/B233.1-charter-7`（合并目标）　本分支：`cards/B370-charter-8`。
一行一条历史读数；命令与原始输出照抄。

## 1. T0 前置复核（只读）

1. 2026-09-15：`go build ./...` 退出码 0；`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0。
2. 2026-09-15：`go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait' -count=1` → `ok  github.com/Xsxdot/handoff/cmd  20.482s`。
3. 2026-09-15：`go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB2336WakeconsumerEnvelopeJSONBoundaries|TestB353AutomationUsesWaitDeliveryPolicy' -count=1` → `ok  github.com/Xsxdot/handoff/internal/agentd  3.569s`。
4. 2026-09-15：`go test ./internal/client/ -count=1` → `ok  github.com/Xsxdot/handoff/internal/client  9.602s`。
5. 2026-09-15：`grep -rn "cardWaitCurrentWorkflowAttempt\|currentWorkflowAttempt" --include=*.go .`（排除 `_test.go`）只命中定义处与 `wakegate.go` 文件头注释：`cmd/card_wait.go:192/195`、`internal/agentd/wakeconsumer.go:53/57`，无第三调用方。
6. 2026-09-15：`go test ./internal/client/ -run 'TestB370' -count=1` → `ok ... [no tests to run]`——新测试尚未写，T1 起点。

## 2. T1 internal/client 共享判定激活（TDD 红→绿）

7. 2026-09-15：先写 `internal/client/wakegate_test.go`（计划 §5.2 全文），再跑 `go test ./internal/client/ -run 'TestB370' -count=1` → 红：`TestB370JudgeMirroredWakeEmptyIdentity` 6 子测全失败、`TestB370JudgeMirroredWakeNonEmptyIdentity/missing_source_task_has_its_own_reason` 失败（got `stale_attempt`）、`TestB370JudgeMirroredWakeNilStore` 失败（nil store 未返回错误）。失败原因是功能缺失（空身份仍保守拦截、缺 source_task reason 未区分、nil 未判），非 typo。
8. 2026-09-15：按计划 §5.2 全文改 `internal/client/wakegate.go`（新增未导出 `scanCardDispatchSnapshots`/`judgeEmptyEnvelopeIdentity`、`CurrentWorkflowAttempt` 委托取数、`JudgeMirroredWake` 判 nil）。`go test ./internal/client/ -run 'TestB370' -count=1` → `ok github.com/Xsxdot/handoff/internal/client 3.161s`，四支顶层测试全 PASS。
9. 2026-09-15：变异自验①（改语义、命中唯一）：`ev.SourceTask == snapshot.Attempt && ev.SourceTarget == snapshot.Target` → `||`（`count(old)==1`）。`go build ./internal/client/` → BUILD_OK；`go test ... -run TestB370` → `TestB370JudgeMirroredWakeEmptyIdentity/source_mismatch_blocks` 变红。用 `cp /tmp/wakegate.go.bak` 还原后不再用 `git checkout`（见 §4 事故）。
10. 2026-09-15：变异自验②（F3 reason 唯一性）：`Reason: WakeGateMissingSourceTask` → `Reason: WakeGateStaleAttempt`（`count(old)==1`）。编译 OK，`missing_source_task_has_its_own_reason` 变红。还原。
11. 2026-09-15：`gofmt -l internal/client/wakegate.go internal/client/wakegate_test.go` 无输出。

## 3. T2/T3 两消费点退役与穿缝矩阵

12. 2026-09-15：`cmd/card_wait.go` 删除 `cardWaitCurrentWorkflowAttempt` 全函数、`cardWaitEventActionable` 的 `EvTaskMirrored` 尾段补 `slog.Error`（判定失败）与 `slog.Info`（含 card/seq/type/task_type/deliver/reason）；`internal/agentd/wakeconsumer.go` 删除 `Server.currentWorkflowAttempt` 全方法、`acceptsCurrentWorkflowAttempt` 补 reason 日志、`consumeAutomationEventsOnce` 的粗粒度 reason `stale_attempt` → `wake_gate_rejected`。
13. 2026-09-15：`go build ./...` 退出码 0；`go vet ./cmd/ ./internal/agentd/` 退出码 0；`grep -rn "cardWaitCurrentWorkflowAttempt\|currentWorkflowAttempt" --include=*.go cmd/ internal/agentd/` 无命中（exit 1）。
14. 2026-09-15：`gofmt -d internal/agentd/wakeconsumer.go` 只剩既有 `:186` 注释空行差异（`:230` 断句段，非本卡引入，plan §3.2/台账 §3 条目 17 已记）；`cmd/card_wait.go` 无差异。本卡新增区块已格式化。
15. 2026-09-15：T2 前置回归 `go test ./cmd/ -run 'TestB349CardWait|TestB353CardWait' -count=1` → `ok github.com/Xsxdot/handoff/cmd 20.524s`；`go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB353Automation|TestAutomationEventMappingThroughConsumer' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 3.788s`。
16. 2026-09-15：追加 T3 穿缝测试：`cmd/card_wait_test.go#TestB370CardWaitDegradedIdentity`（8 情形，入口 `runCardWait`→`Store.Follow`→`json.Encoder`）→ `ok github.com/Xsxdot/handoff/cmd 17.120s`；`internal/agentd/wakeconsumer_test.go#TestB370AutomationDegradedIdentity`（8 情形）+ `#TestB370AutomationBlockedEventSeenAndContinues` → `ok github.com/Xsxdot/handoff/internal/agentd 2.014s`。
17. 2026-09-15：变异自验③（穿缝有牙）：把 `JudgeMirroredWake` 的空身份入口改回保守 `Deliver=false/empty_workflow_identity`（`count(old)==1`、编译 OK）。`cmd` 侧 `TestB370CardWaitDegradedIdentity` 4 个交付子测变红、`agentd` 侧 `TestB370AutomationDegradedIdentity` 4 个唤醒子测变红；还原后两包 `TestB370` 复绿（`ok internal/client 3.160s`）。三支 B349 与 B2336 保绿在上述回归中已验。

## 4. 事故与恢复（诚实记录）

18. 2026-09-15：T1 第一次变异还原时误用 `git checkout -- internal/client/wakegate.go`，把已实现的 wakegate.go 打回 HEAD 骨架（工作树未提交），导致后续一次 `go test -run TestB370` 假红。用变异前备份 `cp /tmp/wakegate.go.bak internal/client/wakegate.go` 恢复实现，复跑 `TestB370` 绿。此后变异还原一律用 `cp` 备份，不再用 `git checkout`。此事故不改变最终工作树内容，已复验。

## 5. T4 全接缝回归与图门禁

19. 2026-09-15：`go build ./...` 退出码 0；`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0；`git diff --check` 退出码 0。
20. 2026-09-15：`go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -count=1` 原始读数：`ok internal/client 14.115s`、`ok internal/ledger 23.198s`、`ok internal/agentd 173.111s`、`cmd` FAIL——唯一失败是 `--- FAIL: TestRepoContractGate`，报 `契约违规 [dead-contract] 契约 d_transport→d_ledger 声明的方向没有活跃 call、implements 或组装点豁免边`——即 contract §8 已记的 known-red（baseline 未重扫），符合 plan §8.1 允许的唯一失败。
21. 2026-09-15：F7=A 重扫：`(cd scripts/codegraph-rescan && go run . --repo ../.. --old ../../codegraph/baseline.json --out /tmp/b370-rescan/baseline.new.json)` stderr 末行 `nodes=5364 containers=338 edges=6742 implements=65 projections=460 lifecycle=133 packages=89`。按 plan §8.2 脚本派生 `codegraph/diffs/cards-B370-charter-8.json`（分支实际名 charter-8，非 plan 字面 charter-7）：added 7（契约 5 符号 + `n_client_scanCardDispatchSnapshots` + `n_client_judgeEmptyEnvelopeIdentity`）、modified 2（`n_cmd_cardWaitEventActionable`/`n_agentd_Server_acceptsCurrentWorkflowAttempt`）、deleted 2（`n_cmd_cardWaitCurrentWorkflowAttempt`/`n_agentd_Server_currentWorkflowAttempt`）、edgesAdded 13、edgesDeleted 6——与 plan §8.2 预期逐项一致。
22. 2026-09-15：`codegraph --repo . validate` 退出码 0，views 列表含 `cards-B370-charter-8`；`codegraph --repo . --view cards-B370-charter-8 check` 退出码 0、`fails: []`。
23. 2026-09-15：无视图 `codegraph --repo . check` 退出码 1，`fails` 1 条 `dead-contract d_transport→d_ledger`（baseline 未重扫的已知红，原文记录，不归因本卡、不绕闸）。
24. 2026-09-15：`codegraph --view cards-B370-charter-8 sym cardWaitCurrentWorkflowAttempt` 返回 `anchor:"vanished"`（视图 `nodesDeleted` 生效，已不作为活跃节点）；无视图 `sym cardWaitCurrentWorkflowAttempt` 仍命中基线（baseline 未重扫，符合预期）。
25. 2026-09-15：`codegraph --repo . resolve --doc docs/superpowers/plans/b370-plan.md` 退出码 0；17 锚：7 `ok`、9 `moved`、0 `vanished`/`file_missing`。
26. 2026-09-15：`gofmt -l` 对本卡五文件：`cmd/card_wait.go`/`cmd/card_wait_test.go`/`internal/client/wakegate.go`/`internal/client/wakegate_test.go`/`internal/agentd/wakeconsumer_test.go` 无输出；`internal/agentd/wakeconsumer.go` 只剩既有 `:186` 注释空行差异（非本卡引入）。

## 6. 提交（历史读数）

27. 2026-09-15：`git add` 本节点产出（三生产文件、三测试文件、图 diff、本台账）后 `git commit`；提交当时 `git log --oneline -1` 原始输出：`6ec5c127 implement(B370): 身份闸降级判定——能判定才拦 + source 列匹配`；`git show --stat --oneline HEAD` 原始输出 `8 files changed, 1105 insertions(+), 140 deletions(-)`。本条回填后 amend 一次收进同一提交（amend 会换 hash，收口判据是工作树干净，不 chase hash）。
