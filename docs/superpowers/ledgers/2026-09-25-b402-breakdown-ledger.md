# B402 breakdown 节点台账（2026-09-25）

卡：B402「派发节点回合零文本收尾（流中断）：裁决解析失败 + 落等人，每起丢一轮（今晚两起实录）」
节点：breakdown（charter）。分支：`cards/B402-charter-2`（不切换）。有效基线：`cards/B402-spec` @ `5d6607fc`。
产出：`docs/superpowers/specs/b402-breakdown.md`（+ 契约 §10 修订记录回写）。
角色：handoff 派发形态（executor 出稿，本地协调者拍板）。

## 事实流水（命令与原始输出）

1. **基线/HEAD**：`git branch --show-current` → `cards/B402-charter-2`；`git rev-parse HEAD` → `d83657fdaa21604900c076f525ad0e89e73795ab`（contract 提交）；`git status --porcelain` → 空。`git merge-base --is-ancestor 5d6607fc HEAD` → 真（spec 提交是本分支祖先）。
2. **上游 spec/契约落点**：`docs/superpowers/specs/b402.md`、`docs/superpowers/specs/b402-contract.md` 均在工作树/本分支。spec 头部「状态：已批准」、contract 头部「上游状态：已批准」「冻结状态：本提交随 codegraph/target.json 与 codegraph/diffs/cards-B402-charter.json 冻结」逐字读得。
3. **契约提交改动面**：`git show --stat HEAD` → 26 文件（proto/failure.go、ledgerstep/{wire,runner,node}.go 与测试、executor.Result、五家 adapter、orchestration/{contracts,manager}.go、视图 diff、contract + ledger），1064 insertions / 47 deletions。**Ticket 0 是完整运行时实现，非 TODO 骨架**（`grep TODO/FIXME/panic("not implemented")` 于触碰文件 → 无命中）。
4. **域归属（`codegraph --repo . --view cards-B402-charter sym`）**：`FailureClass → d_protocol`；`TurnEnd`/`failureClassOf`/`waitForTurnEnd`/`waitForTurnEndGrace`/`StepRunner.awaitNode` → `d_ledger`；`FailedPayload`/`NewFailedPayload`/`Manager.handleResult`/`Manager.Continue` → `d_orchestration`；`Result` → `d_execution_contract`（`codegraph sym Result` 首命中 `d_execution_adapters` 的 codex 同名模型，按 file 定位 `internal/executor/executor.go` 为 `d_execution_contract`）；`NoTrailerResult` → `d_execution_contract`；`Server.handleContinue`/`Server.runStep`/`Server.handleEvents` → `d_gateway`；`Client.Continue` → `d_transport_channel`。顶层子系统清单由 `codegraph domains` 读得（parent 为空者）。
5. **本轮新鲜验证**：
   - `go build ./...` → `BUILD_EXIT=0`（无输出）。
   - `go vet ./...` → `VET_EXIT=0`（无输出）。
   - `go test ./internal/ledgerstep/ ./internal/orchestration/ ./internal/proto/ ./internal/executor/... -count=1` → 全绿，`TEST_EXIT=0`（ledgerstep 17.163s、orchestration 62.039s、proto 0.054s、五家 adapter + fake/rawtap/turn 全 ok）。
6. **图门禁**：`codegraph --repo . check` → `CHECK_EXIT=0`（fails=[]）；`codegraph --repo . --view cards-B402-charter check` → `VIEW_CHECK_EXIT=0`（fails=[]）；`codegraph --repo . validate` → `VALIDATE_EXIT=1`，`issues` 恰为既存两条：`[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器`。非本卡引入，不修。
7. **roadmap 现状**：`grep -n "B402" docs/roadmap.md` → 无命中；spec §10 三条后续项未登记，落 S4。
8. **符号锚**：`codegraph --repo . resolve --doc docs/superpowers/specs/b402-breakdown.md` → `RESOLVE_EXIT=0`，25 个 `#Symbol` 锚全 `ok`/`moved`，坏锚 0。首跑 1 个 `file_missing`（`codex/adapter.go#Adapter.finishTurn` 缺 `internal/executor/` 前缀）+ 4 个裸文件名锚，已修后复跑通过。
9. **契约基线锚**：`codegraph --repo . resolve --doc docs/superpowers/specs/b402-contract.md` → `RESOLVE_EXIT=0`，锚全 `ok`/`moved`（含 `NoTrailerResult`/`Manager.handleResult`/`Manager.Continue` 等）。
10. **spec §9.2 缺口核对（读测试源码，非推测）**：`grep FailureClass|failure_class` 于 `internal/**/*_test.go` → 27 命中，正例在五家 adapter、wire 两例、payload additive、`b402_retry_test`；**反例未落**：邻近分支不分类（0）、`zero_text→普通 turn_failed`（0）、`NoTrailerResult` 不分类（0）、`failed` 由 handleResult 产出（结构上不经过）、diff/产出/发布/写闸早退 FinishTask=0（仅解析失败例）、HTTP/WS/mirror 不丢分类（0）、日志断言（0）。落 S1/S2/S3。
11. **跨进程 e2e 夹具先例**：`internal/orchestration/gateway_http_test.go`（外部测试包 `orchestration_test`，真 `*Manager` 挂真 `agentd.Server` + `httptest`）；`internal/agentd/server_test.go`/`receiver_occupancy_test.go` 已有 `/continue` 用例。S3 可自足。

## 提交事实（历史读数）

提交前待提交文件：`docs/superpowers/specs/b402-breakdown.md`、`docs/superpowers/specs/b402-contract.md`（§10 回写）、本台账。

```
$ git add docs/superpowers/specs/b402-breakdown.md docs/superpowers/specs/b402-contract.md docs/superpowers/ledgers/2026-09-25-b402-breakdown-ledger.md
$ git commit -q -m "breakdown(B402): 零文本单次续接拆解提案——代码单轮闭合 + S1-S5 测试/文档/图子卡 + P1-P5 待拍板"
$ git log --oneline -1
77ebea64 breakdown(B402): 零文本单次续接拆解提案——代码单轮闭合 + S1-S5 测试/文档/图子卡 + P1-P5 待拍板
```

随后按纪律 amend 一次把本条台账收进同批提交（hash 会变，属 git 事实；不 chase）。
