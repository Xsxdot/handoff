# B370 breakdown 台账（镜像身份闸降级判定）

节点：breakdown（上游 spec `docs/superpowers/specs/b370.md` 头部「已批准」；冻结 contract `docs/superpowers/specs/b370-contract.md` 头部「上游已批准 / 本提交冻结」——开工核对通过）。
卡：B370　有效基线：`cards/B233.1-charter-7`　当前分支：`cards/B370-charter-6`。
一行一条历史读数；命令与原始输出照抄，不做二次加工。

## 0. 执行形态与边界

1. 2026-09-14：执行形态 = handoff 派发，executor 出稿、本地协调者拍板（纪律块允许的单上下文兜底不适用：本节点是 handoff executor，出稿与拍板已在两个上下文）。本回合只写法定产出 `docs/superpowers/specs/b370-breakdown.md`、本台账，并回写 `b370-contract.md` §12 边界澄清；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。
2. 2026-09-14：本稿只有一处已成事实的边界澄清（三态定位属包内实现面，回写 contract §12）；真正岔口 F1–F7 全部标「待拍板」，稿首集中列出，不自行选定。

## 1. 工作树与上游读数

3. `git rev-parse HEAD` → `758157aabc148f35e8c0bbf777694429ebc68101`。
4. `git status --short --branch` → `## cards/B370-charter-6`（工作树干净）。
5. `git log -6 --oneline` 原始输出：
   ```
   758157aa contract(B370): 镜像身份闸降级判定冻结——共享符号骨架 + 降级方向
   44258345 docs(B371): spec 定稿(已批准，L1 快道)——build-deploy.sh 自检对无自动戳容错
   f4b818fa docs(B370): spec 定稿(已批准)——镜像身份闸降级判定(能判定才拦)；台账与 roadmap 残余落账
   b5ebef69 docs(B233.28): spec 已批准，任务/项目走编排门面
   56c7a9c7 fix(B233.27): 打错机器用独立哨兵，补 handleDispatch 流程注释
   4ef20442 feat(B233.27): 派发拒绝非本机接收机
   ```
6. 上游状态位（读文件头）：`b370.md:3` `状态：**已批准**（2026-09-14，用户原话「批准，进 contract」）`；`b370-contract.md:3-5` `上游状态：已批准`、`冻结状态：本提交随 codegraph/target.json、codegraph/diffs/cards-B370-charter-4.json 与 Ticket 0 骨架同批冻结`。
7. 参照先例：`b349-breakdown.md`、`b353-breakdown.md`（轻档单卡 + T0–T4 内部单元）；`b349-breakdown-ledger.md`、`b353-breakdown-ledger.md`（台账格式）。

## 2. 代码图命令与原始输出

8. `codegraph summary --repo .` → `本仓库有代码图：5299 节点 / 6618 边 / 26 领域（codegraph/）`。
9. `codegraph domains --repo .`（顶层 `parent==null`）：`d_cli,d_collab,d_execution,d_gateway,d_keystone,d_ledger,d_maintenance,d_orchestration,d_policy,d_protocol,d_scheduling,d_sessions,d_transport,d_web,d_workspace`（15 个）。
10. `best.json` 顶层领域类型：`d_orchestration=logic,d_gateway=boundary,d_workspace=boundary,d_execution=boundary,d_sessions=boundary,d_transport=boundary,d_protocol=logic,d_ledger=logic,d_collab=logic,d_cli=logic,d_web=logic,d_policy=logic,d_maintenance=boundary,d_scheduling=logic,d_keystone=logic`。
11. 容器映射：`k_cmd_fn→d_cli`；`k_agentd_fn`/`k_agentd_Server`→`d_gateway`；`k_client_fn`/`k_client_model`→`d_transport_channel`（父 `d_transport`）；`k_ledger_Store→d_ledger`；`k_proto_model→d_protocol`。
12. `codegraph sym` 实测（本轮）：
    - `cardWaitEventActionable` → `d_cli` `cmd/card_wait.go:240` `anchor=ok`。
    - `Server.acceptsCurrentWorkflowAttempt` → `d_gateway` `internal/agentd/wakeconsumer.go:109` `anchor=ok`。
    - `Server.consumeAutomationEventsOnce` → `anchor=moved`；`automationWakeEvent` → `anchor=moved`。
    - `Store.EventsFromAsc` → `d_ledger` `internal/ledger/events.go:63` `anchor=ok`。
    - `DispatchSnapshot` → `m_ledger_DispatchSnapshot` `internal/ledger/events.go:115` `anchor=ok`。
    - `WaitDeliveryPolicy` → `n_client_WaitDeliveryPolicy` `d_transport_channel` `internal/client/delivery.go:23` `anchor=ok`。
13. 图覆盖债（`codegraph sym` 原始报错）：`JudgeMirroredWake` → `Error: 符号 "JudgeMirroredWake" 不在图中（图未覆盖或名字有误）；近似候选: []`；`CurrentWorkflowAttempt` → 同上（近似候选列出 `acceptsCurrentWorkflowAttempt`/`currentWorkflowAttempt`/`cardWaitCurrentWorkflowAttempt`）。即 Ticket 0 新符号未入基线图；`cardWaitCurrentWorkflowAttempt`、`Server.currentWorkflowAttempt` 仍在基线图（`anchor=ok`）。已记入 breakdown §7。
14. 分支视图：`codegraph/diffs/cards-B370-charter-4.json`（`view=cards/B370-charter-4`，`base=af838fd2…`）：5 `nodesAdded`（`m_client_WakeGateReason`/`m_client_WakeGateDecision`/`m_client_WakeGateEvent`/`n_client_CurrentWorkflowAttempt`/`n_client_JudgeMirroredWake`）、2 `nodesModified`（`n_cmd_cardWaitEventActionable`、`n_agentd_Server_acceptsCurrentWorkflowAttempt`）、8 `edgesAdded`（含 `n_agentd_Server_acceptsCurrentWorkflowAttempt→n_client_JudgeMirroredWake`、`n_cmd_cardWaitEventActionable→n_client_JudgeMirroredWake`、`n_client_CurrentWorkflowAttempt→n_ledger_Store_EventsFromAsc`、`n_client_CurrentWorkflowAttempt→m_ledger_DispatchSnapshot`）。
15. `codegraph --repo . --view cards-B370-charter-4 check` → 退出码 0，fails 空（尾部读数 `"misplacedSkipped": 0`）。
16. `codegraph --repo . validate` → 退出码 0，views 含 `cards-B370-charter-4`。
17. 无视图契约闸（本分支 known-red，原文）：`go test ./cmd/ -run '^TestRepoContractGate$' -count=1` →
    ```
    --- FAIL: TestRepoContractGate (0.06s)
        graph_gate_test.go:38: 契约违规 [dead-contract] 契约 d_transport→d_ledger 声明的方向没有活跃 call、implements 或组装点豁免边（期望在该方向看到至少一条跨子系统边）
    FAIL
    FAIL   github.com/Xsxdot/handoff/cmd   0.070s
    ```
    退出码 1。与 contract §8 一致（baseline 未重扫），不归因本卡、不绕闸；本分支对照判据是视图 check（读数 15）。

## 3. 现状代码事实读数

18. 依赖边：`go list -f '{{join .Imports "\n"}}' ./internal/client | grep internal/ledger` → 命中（`internal/client` 现依赖 `internal/ledger`，为 Ticket 0 新增方向 `d_transport→d_ledger` 的实况）。`grep -rn 'handoff/cmd' internal/` → 无输出（无 `agentd→cmd` 反向依赖）。
19. 旧扫描残留（grep 原始读数）：
    - `cmd/card_wait.go:195 func cardWaitCurrentWorkflowAttempt(...)` 仅有定义与注释，无调用方。
    - `internal/agentd/wakeconsumer.go:57 func (s *Server) currentWorkflowAttempt(...)` 仅有定义与注释，无调用方。
    即两份旧扫描已是死代码，退役可在实现轮安全进行。
20. 消费点接线现状：`cmd/card_wait.go:276` 与 `internal/agentd/wakeconsumer.go:120` 均已调 `client.JudgeMirroredWake`，填 `client.WakeGateEvent`。
21. envelope 生产形状：`internal/ledger/mirror.go:62` `payload := fmt.Sprintf(`{"node":%q,"attempt":%q,"task_type":%q,"payload":%s}` ...)`；source 三列落 `source_target/source_task/source_seq`，不复制进 JSON（注释明写）。旧写入者版本（B233.6 之前 `0e3ca8c3`）只写 `{"task_type":…,"payload":…}`，无 `node`/`attempt`（spec 台账 §1 条目 6/7）。
22. `JudgeMirroredWake` 骨架现状（`internal/client/wakegate.go:128`）：身份缺失/为空时返回 `Deliver=false, Reason=WakeGateEmptyEnvelopeIdentity`（保守，等价旧行为）；三分支放行未激活。
23. `CurrentWorkflowAttempt` 现状（`:75`）：返回 `(snapshot, found)` 二态；合格条件 `Node==node && Node!="" && Attempt!="" && TaskID==Attempt`；分页 500 读到尾。
24. 两处消费点既有测试夹具：`cmd/card_wait_test.go#TestB349CardWaitSourceIdentity`（`:411`）已含 `Node:"",Attempt:""` 的 `missing-identity` 双空事件（现期望不输出）；`internal/agentd/wakeconsumer_test.go#TestB2336StaleAttemptDoesNotWake`（`:617`）含 `task-empty`（`Node:"",Attempt:""`、source_task=task-empty）事件，期望不唤醒。两者即 spec §79「保持绿」与 F1 定位键语义的直接约束来源。
25. `internal/client` 无 `wakegate_test.go`（`ls` → `No such file or directory`）；本包测试现无 `internal/ledger` 导入（`grep -rln internal/ledger internal/client/*_test.go` 无输出）——T1 直测需新增导入。

## 4. 编译与测试读数（本轮实跑）

26. `go build ./...` → 退出码 0。
27. `go vet ./internal/client/ ./internal/agentd/ ./cmd/` → 退出码 0。
28. `gofmt -l internal/client/wakegate.go cmd/card_wait.go internal/agentd/wakeconsumer.go` → 输出 `internal/agentd/wakeconsumer.go`（既有格式问题，contract 台账 §5 条目 22 已判非本卡引入；本轮不改，避免越界）。
29. `go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait' -count=1` → `ok  	github.com/Xsxdot/handoff/cmd	20.097s`。
30. `go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB2336WakeconsumerEnvelopeJSONBoundaries|TestB353AutomationUsesWaitDeliveryPolicy' -count=1` → `ok  	github.com/Xsxdot/handoff/internal/agentd	2.556s`。
31. `git diff --check` → 退出码 0（无空白错误）。

## 5. 符号锚自检（产出物写完前亲跑）

32. `codegraph --repo . resolve --doc docs/superpowers/specs/b370-breakdown.md` → 退出码 0；`anchors` 共 13：11 `anchor=ok`、2 `anchor=moved`（`cmd/card_wait_test.go#TestB349CardWaitSourceIdentity`、`internal/agentd/wakeconsumer_test.go#TestB349AutomationSourceIdentity`——测试函数未入图，词边界命中）；无 `vanished`/`file_missing`。坏锚 0，无需修。

## 6. 关键判断

33. 2026-09-14：本稿**不退回 contract**。契约 §3.1 导出面、§3.2 判定正文、§3.3 接线、§4 依赖方向均吸收进 T1–T3；F1–F5 是契约留白处的取舍（不是新接缝），F6/F7 是实现组织与图产出落点，均在契约允许面内。
34. 2026-09-14：F1 判定为「定位键=该卡最新合格快照」而非「以 source_task 反查该任务自身快照」——依据是 `TestB2336StaleAttemptDoesNotWake` 的 `task-empty` 事件须不唤醒（spec §79 明确「保持绿」）。这是唯一能同时满足 §5.4 #36/#37（空身份应交付）与该回归保绿的语义；仍标待拍板，因为契约 §3.2 B 分支 1 的「该 task 的派发快照」措辞可有两种解读。
35. 2026-09-14：F2 判定为「包内未导出三态取数」——因为冻结的 `CurrentWorkflowAttempt` 只返回二态，无法区分分支 2/3；三态不属导出契约面，故不退回 contract，只回写 §12。
36. 2026-09-14：边界澄清已回写 `b370-contract.md` §12（三态定位属包内实现面、F1 定位键、§3.1/§3.2 缺 source_task reason 不自洽），不只活在拆解稿里。

## 7. 收口提交（历史读数）

37. 2026-09-14：本节点产出物 `docs/superpowers/specs/b370-breakdown.md`、本台账与 `b370-contract.md` §12 回写同批提交；命令 `git add docs/superpowers/specs/b370-breakdown.md docs/superpowers/specs/b370-contract.md docs/superpowers/ledgers/2026-09-14-b370-breakdown-ledger.md && git commit -F -`。提交当时 `git log --oneline -1` 原始输出：`a5446915 docs(B370): 拆解出稿——身份闸降级三分支单卡 T0–T4 与 F1–F7 待拍板`；`git show --stat --oneline HEAD` 原始输出尾部：`3 files changed, 423 insertions(+)`，文件含 `docs/superpowers/specs/b370-breakdown.md`、`docs/superpowers/specs/b370-contract.md`、`docs/superpowers/ledgers/2026-09-14-b370-breakdown-ledger.md`。本条回填后 amend 一次收进同一提交（amend 会换 hash，收口判据是工作树干净，不 chase hash）。
