# B380 breakdown 节点台账（2026-09-23）

卡：B380「card wait 建连快照把 5 天前已答工单当未决：卡流缺关单镜像（ticket_answered）」。
节点：breakdown（charter）。分支：`cards/B380-charter-2`（不切换）。
产出：`docs/superpowers/specs/b380-breakdown.md`（+ 契约末尾澄清回写一行）。
角色：handoff 派发形态（executor 出稿，本地协调者拍板）。

## 事实流水（命令与原始输出）

1. **基线/HEAD**：`git rev-parse --abbrev-ref HEAD` → `cards/B380-charter-2`；`git rev-parse HEAD` → `2a5378abda55c6d634134ec8da520dd999e881f6`（contract 提交）；`git status --short` → 空。
2. **上游 spec 落点**：`docs/superpowers/specs/b380.md` 在工作树**不存在**；
   `git merge-base --is-ancestor 117bbd5a HEAD` → exit 1（spec 不是本分支祖先）；
   `git branch -a --contains 117bbd5a` → `remotes/origin/cards/B380-charter-1`。
   全文经 `git show 117bbd5a:docs/superpowers/specs/b380.md` 读得（108 行，头部「上游状态：**已批准**」）；草稿 `68035fa3`、批准回写 `117bbd5a`。
3. **契约头部**：`b380-contract.md:1-7` 有上游 spec/级别/基线/图，但**无「冻结状态」状态行**（对比 `b370-contract.md:9`、`b374-contract.md:9`）。冻结事实由提交 `2a5378ab` message（「契约冻结」）承载。
4. **契约提交改动面**：`git diff --stat e5e428a5 HEAD` → 5 生产文件（`facade.go`/`manager.go`/`client.go`/`taskstate.go`/`proto.go`）+ 3 测试 + contract + ledger，10 文件。
5. **三个写入点在位复核**：`grep -rn 'EventTypeTicketAnswered' --include=*.go internal/ cmd/ | grep -v _test.go` → 仅 `internal/orchestration/facade.go:127`、`internal/orchestration/manager.go:2577`、`internal/approval/client.go:430`（另 proto.go 定义/注释、delivery.go 策略、taskstate.go 本地常量）。**无第四处**。
6. **C-1 发布片段在位**：`facade.go` AnswerTicket append 成功 `m.hub.Publish(evt)`；`manager.go` approvePermission 同款；`client.go` `c.hooks.Hub != nil` 时 Publish。
7. **C-2 终态关单在位**：`internal/ledger/taskstate.go:188` `case evTicketsVoided, "completed", "failed", "archived":` 清该任务全部未决 key；与 `mirrorTaskTerminal`（:22，只收 archived/failed）集合不同且注释已写明「不得合并」。
8. **C-3 冻结项核对**：`WaitDeliveryPolicy`（`internal/client/delivery.go:23`）仍把 `ticket_answered` 判 false；`MirrorWatermark`（`internal/ledger/mirror.go:104`）仍 `MAX(source_seq)`；镜像信封（mirror.go:62）仍 `{"node","attempt","task_type","payload"}`；`tickets_voided` 无 Publish（`ticketvoid.go:9` 注释 + grep 无 Publish）；payload schema 逐字不变（`contracts.go:115` / `approval/client.go:551` 均 `ticket_id`/`answer`）。
9. **镜像链证据**：镜像走 `StreamEventsOnce`/`ForwardedStreamEventsOnce`（`internal/client/client.go:1647` / `capabilities.go:37`），该路径**不过** `WaitDeliveryPolicy`（`client.go:1669` 注释：策略在应用谓词，传输必须仍见到该帧）；`mirrorSkip`（`internal/ledgermirror/mirror.go:179`）**不含** `ticket_answered`。→ C-1 发布会真的进镜像。
10. **终态不变式证据**：`VoidTicketsWithAudit`（`internal/orchestration/ticketvoid.go:49`）在 `manager.go:3491/3616`、`contracts.go:242` 的终态/对账收口调用 → 终态即无挂起工单，C-2 关单不误杀。
11. **域归属（best.json）**：`codegraph --repo . sym` 实测 —— `Manager.approvePermission`/`Client.consult`/`Hub.Publish`/`Store.AppendEvent` → `d_orchestration`（`internal/approval` 归 d_orchestration）；`Store.OpenTickets` 未入图，`Store.OpenTicketCounts`/`mirrorTaskTerminal`/`Store.AppendMirroredEvent`/`Store.MirrorWatermark`/`Mirror.subscribe` → `d_ledger`；`WaitDeliveryPolicy`/`StreamEventsOnce` → `d_transport_channel`（父 d_transport）；`Server.handleEvents`/`Server.handleCardsList` → `d_gateway`；`m_proto_Event` → `d_protocol`；`cmd/` → `d_cli`（`encodeCardWaitSnapshot` 未入图）。
12. **图覆盖债**：`codegraph sym 'OpenTickets'` → 「不在图中…近似候选 []」；`Store.OpenTickets`/`Manager.AnswerTicket`/`EventTypeTicketAnswered`/`encodeCardWaitSnapshot` 均未命中（与 contract §7 一致）。
13. **编译**（本轮新鲜）：`go build ./...` → exit 0（无输出）。
14. **冻结测试**（本轮新鲜）：
    - `go test ./internal/ledger/ -run TestOpenTicketsTerminal -count=1` → `ok github.com/Xsxdot/handoff/internal/ledger 1.171s`
    - `go test ./internal/orchestration/ -run TestB380 -count=1` → `ok github.com/Xsxdot/handoff/internal/orchestration 0.376s`
    - `go test ./internal/approval/ -run TestB380 -count=1` → `ok github.com/Xsxdot/handoff/internal/approval 0.323s`
15. **图门禁**：`codegraph --repo . check` → exit 0（仅既有 warns：best-dangling / budget-raised / legacy / oversized-package / prefix-family，含 `cmd` 57 文件与 `card` 前缀族 10 文件，非本卡新增）。`codegraph --repo . validate` → exit 1，`issues` 恰为 contract §6 所记两条：`[cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api`、`[cards-B374-charter] 新增容器 k_collab_model 已存在于基线`。**非本卡引入，不修。**
16. **测试替身事实**：`internal/orchestration` 的 B380 测试用真实 `*Hub`（`newTestManager` → `newTestManagerWithApprover`）；`internal/approval` 的 B380 测试用 `testHub` 假替身（`client_test.go:26`，`Publish` 只 append）；生产装配 `Hub: m.hub`（`internal/orchestration/approval_client.go:37`）无机内断言。
17. **文档表述现状**：`skills/handoff/SKILL.md:95-96` 把七类并列称「**不会**唤醒 wait（**只入库**）」——对 `ticket_answered` 在 C-1 后不再准确；`README.md:432-434` 表述为「filters the same seven audit types at the application consumer」，说的是交付过滤，仍准确。`docs/roadmap.md` 无 B380 / 「逐 seq」/「watermark 对账」条目（`grep` 无输出）。
18. **`codegraph resolve --doc`**（收口前亲跑）：见本轮提交前执行记录（下文）。

19. **`codegraph resolve --doc`（亲跑，结果）**：
    - `codegraph --repo . resolve --doc docs/superpowers/specs/b380-breakdown.md` → exit 0；6 个 `#Symbol` 锚（`WaitDeliveryPolicy`/`Client.StreamEventsOnce`/`encodeCardWaitSnapshot`/`Hub.Publish`/`VoidTicketsWithAudit`/`Manager.bindApproval`）全部 `ok` 或 `moved`（moved = 行号漂移、节点在，非坏锚）。
    - `codegraph --repo . resolve --doc docs/superpowers/specs/b380-contract.md` → exit 0；含 `Manager.approvePermission`/`Client.consult`/`Store.MirrorWatermark` 等 12 锚全 `ok`/`moved`。
20. **契约回写**：`b380-contract.md` 末尾新增「## 9. 修订记录（breakdown 出稿轮，2026-09-23）」四条边界澄清（§2.4），与产出同批提交。

## 提交事实（历史读数）

```
$ git add docs/superpowers/specs/b380-breakdown.md docs/superpowers/ledgers/2026-09-23-b380-breakdown-ledger.md docs/superpowers/specs/b380-contract.md
$ git commit -q -m "breakdown(B380): 关单镜像修复拆解提案——代码单轮闭合 + S1 文档收口 + P1-P4 待拍板 …"
$ git log --oneline -1
011a1b33 breakdown(B380): 关单镜像修复拆解提案——代码单轮闭合 + S1 文档收口 + P1-P4 待拍板
```

随后按纪律 amend 一次把本条台账收进同批提交（hash 会变，属 git 事实；不 chase）。
