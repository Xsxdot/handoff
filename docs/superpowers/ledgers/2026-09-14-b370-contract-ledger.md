# B370 contract 台账（镜像身份闸降级判定）

节点：contract（上游 spec `docs/superpowers/specs/b370.md`，头部状态行「已批准」（2026-09-14，用户原话「批准，进 contract」）——开工核对通过）。
卡：B370　基线分支：`cards/B233.1-charter-7`（本卡合并目标）　本分支：`cards/B370-charter-4`。
一行一条历史读数；命令与原始输出照抄，不做二次加工。

## 0. 落点裁决

1. 2026-09-14：承重岔口（共享实现落点包）按用户裁决 **A = `internal/client`**，与 `WaitDeliveryPolicy` 同包（消费策略住一处）；新增契约方向 `d_transport→d_ledger` 必须在 target 图显式声明；不得造成 agentd→cmd 反向依赖。
2. 2026-09-14：用户补充裁决——落地方案按 contract 节点法定产出办：方向声明与可编译骨架**同批提交**；骨架落共享符号与两处调用点接线，活跃边由骨架调用点提供；骨架期只加调用点与空壳判定（三分支可先返回与旧行为等价的保守结果并在文档写明），**两份旧拷贝的退役留给 implement 轮**，不进契约提交。

## 1. 现状查证（命令与原始输出）

3. 上游状态位核对：`sed -n '1,12p' docs/superpowers/specs/b370.md` 首行原文 `状态：**已批准**（2026-09-14，用户原话「批准，进 contract」）`。
4. 闸的两处实现（现状）：
   - `cmd/card_wait.go#cardWaitEventActionable`（`:240`），含 `cardWaitCurrentWorkflowAttempt`（`:195`）。
   - `internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`（`:110`），含 `Server.currentWorkflowAttempt`（`:57`）。
   - `codegraph sym` 两者 `anchor=ok`，域 `d_cli` / `d_gateway`。
5. 依赖事实（`go list -deps`）：
   - `internal/client` 当前**不**依赖 `internal/ledger`：`go list -f '{{join .Imports "\n"}}' ./internal/client/...` 输出无 `internal/ledger`。
   - `internal/ledger` 依赖闭包里无 `internal/client`：`go list -deps ./internal/ledger/... | grep internal/client$` 无输出（无环）。
   - `cmd` 依赖 `internal/client` 与 `internal/agentd`；`internal/agentd` 依赖 `internal/client`、`internal/ledger`；无 `agentd→cmd` 反向依赖（`grep -rn 'handoff/cmd' internal/` 无命中）。
6. 图事实（`codegraph check` 原始读数，本节点开工时）：
   - 无视图 `check` 当时 fails 为空（`go test ./cmd -run '^TestRepoContractGate$' -count=1` → `ok ... 0.078s`）。
   - 现状 `d_cli→d_transport` 预算 12、`d_gateway→d_transport` 预算 9、`d_transport→d_protocol` 预算 0，均无 `d_transport→d_ledger` 条目。
7. `baseline.json` 未含 B370 符号（`n_client_WakeGate*` 等），其 meta 为 `commit af838fd2 / branch cards/B233-residual`（`.agents` 于 2026-09-14 重扫，早于本卡）。

## 2. 机制查证（dead-contract 与视图关系）

8. 2026-09-14：读 charter/graph v0.10.0（`go.mod` 钉 `github.com/Xsxdot/charter/graph v0.10.0`）源码 `codegraph/check.go`：`dead-contract` 只认**视图内**活跃 call/implements/组装点豁免边；`check` 无视图时视图 = 纯 `baseline.json`（`cli.go:76-90`）。
9. 2026-09-14：实验（临时在 target 追加 `d_transport→d_ledger` 空方向）`codegraph check` 原始 fails：`[{"kind":"dead-contract","from":"d_transport","to":"d_ledger","detail":"契约 d_transport→d_ledger 声明的方向没有活跃 call、implements 或组装点豁免边..."}]`，随后已还原。
10. 2026-09-14：本提交两处调用点接线后，重扫工作树（`scripts/codegraph-rescan`，临时 out，不落库）得到新增跨域边 `n_client_CurrentWorkflowAttempt→n_ledger_Store_EventsFromAsc`、`n_client_CurrentWorkflowAttempt→m_ledger_DispatchSnapshot`、`n_cmd_cardWaitEventActionable→n_client_JudgeMirroredWake`、`n_agentd_Server_acceptsCurrentWorkflowAttempt→n_client_JudgeMirroredWake` 等 8 条（详见 `codegraph/diffs/cards-B370-charter-4.json#edgesAdded`）。

## 3. 图产出与闸读数

11. 2026-09-14：分支视图 `codegraph/diffs/cards-B370-charter-4.json` 落 5 新符号（`WakeGateReason`/`WakeGateDecision`/`WakeGateEvent` 三 model + `CurrentWorkflowAttempt`/`JudgeMirroredWake` 两 func）、2 改动符号（两处消费点签名/行号）与 8 新边。
12. 2026-09-14：`codegraph validate` 退出码 0，views 列表含 `cards-B370-charter-4`。
13. 2026-09-14：`codegraph --view cards-B370-charter-4 check` 原始 fails 空（`[]`），退出码 0；`d_transport->d_ledger` 命中为 0（活跃边由 `client（包级函数）` 容器承载，entry 已声明）。
14. 2026-09-14：无视图 `codegraph check` 仍 fails 1 条 `dead-contract d_transport→d_ledger`——原因是 `baseline.json` 未重扫（无 B370 符号/边），属合并前过渡态；同 B229/B353 先例（`docs/superpowers/ledgers/2026-08-25-b229-graph-recheck-ledger.md:11`、`docs/superpowers/ledgers/2026-09-09-b353-plan-ledger.md:89`）。`cmd/graph_gate_test.go#TestRepoContractGate` 跑无视图基线，故本分支内**已知红**，absorb 后转绿（`absorb` 命令语义：分支合并回主线后执行）。

## 4. 编译与测试读数

15. 2026-09-14：`go build ./...` 退出码 0。
16. 2026-09-14：`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0。
17. 2026-09-14：`go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait' -count=1` → `ok github.com/Xsxdot/handoff/cmd 20.511s`。
18. 2026-09-14：`go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB2336WakeconsumerEnvelopeJSONBoundaries|TestAutomationEventMappingThroughConsumer|TestB353AutomationUsesWaitDeliveryPolicy' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 3.663s`。
19. 2026-09-14：`go test ./internal/client/ -count=1` → `ok github.com/Xsxdot/handoff/internal/client 9.247s`。

## 5. 符号锚与已知红复核

20. 2026-09-14：`codegraph --repo . resolve --doc docs/superpowers/specs/b370-contract.md` 退出码 0，13 锚：11 `ok`、2 `moved`（`internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`、`cmd/graph_gate_test.go#TestRepoContractGate`——图中无对应节点，源码词边界命中；`moved` 不判失败，仅 `vanished`/`file_missing` 非零退出）。
21. 2026-09-14：复核「本分支重扫 baseline 能否消 known-red」——用工作树重扫产物（临时 out，不落库）对既有视图跑 `ValidateDiff` 等价检查，`cards-B358-charter.json` 的 `nodesDeleted` 有 11 项在新基线中不存在（如 `n_collab_room_KindAllowed`、`n_web_app_rooms_RoomPanel`），重扫会令该视图 `validate` 失败。裁定：不在本分支重扫 baseline，known-red 由 `absorb`（分支合并回主线后）承接；`codegraph --view cards-B370-charter-4 check` 为本分支的对照判据（fails 空）。
22. 2026-09-14：`gofmt -l internal/agentd/wakeconsumer.go` 有输出，但 `git stash` 后同一文件仍列名，确认为**既有**格式问题（本提交未引入）；本提交新增/改动的 `internal/client/wakegate.go`、`cmd/card_wait.go` 无 `gofmt` 差异。
23. 2026-09-14：收口复核 `go test ./cmd/ -run '^TestRepoContractGate$' -count=1` 原始失败行：`契约违规 [dead-contract] 契约 d_transport→d_ledger 声明的方向没有活跃 call、implements 或组装点豁免边`；退出码 1。即 §8 所述已知红（无视图基线未重扫），未做任何绕闸动作。
24. 2026-09-14：`go build ./...` 退出码 0；`go test ./internal/client/ ./internal/ledger/ -count=1` → 两包 `ok`；`go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait|TestCardWait|TestWaitRejectsCardFlag' -count=1` → `ok github.com/Xsxdot/handoff/cmd 22.454s`；`go test ./internal/agentd/ -run <B349/B2336/B353 消费面>' -count=1` → `ok github.com/Xsxdot/handoff/internal/agentd 4.395s`。

## 6. 冻结提交（历史读数）

25. 2026-09-14：本节点冻结即提交，命令 `git add ... && git commit -F -`；提交当时的 `git log --oneline -1` 原始输出：`2786f844 contract(B370): 镜像身份闸降级判定冻结——共享符号骨架 + 降级方向`。`git show --stat --oneline HEAD` 原始输出：`2786f844 ... 7 files changed, 728 insertions(+), 49 deletions(-)`，文件含 `cmd/card_wait.go`、`codegraph/diffs/cards-B370-charter-4.json`、`codegraph/target.json`、`docs/superpowers/ledgers/2026-09-14-b370-contract-ledger.md`、`docs/superpowers/specs/b370-contract.md`、`internal/agentd/wakeconsumer.go`、`internal/client/wakegate.go`。本条回填后 amend 一次收进同一提交（amend 会换 hash，收口判据是工作树干净，不 chase hash）。
26. 2026-09-14：提交后复核 `go build ./...` 退出码 0；`codegraph --repo . --view cards-B370-charter-4 check` 退出码 0、fails 空。
