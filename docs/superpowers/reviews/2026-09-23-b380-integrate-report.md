# B380 集成报告（integrate 节点，2026-09-23）

- 父卡：B380（card wait 建连快照把 5 天前已答工单当未决：卡流缺关单镜像）。
- 分支：`cards/B380-charter-5`（P1=甲 单轮闭合、无子系统扇出，全部工作直接落在本分支，无独立子分支合并动作）。
- 集成起点：`814ef046`（implement(B380): S1 文档同步）。
- 执行者：charter:integrate 节点。红线：并入主线与归档留协调者裁决，本文到「集成分支就绪 + 报告」为止。
- 协调者裁定（回答工单 6ed714a7）：P1=甲 正确，全量测试与契约对照在本分支做。

---

## 一、合分支核对（integrate 纪律第 1 件前半）

| 项 | 读数 |
|---|---|
| 子系统扇出 | **无**——breakdown P1=甲（单轮闭合）、P2=乙（S1 并入 implement），无实现子卡、无独立子分支 |
| `cards/B380-charter-4` | 是 HEAD 祖先（`merge-base --is-ancestor` → 是），implement 已在链上 |
| `cards/B380-review-1/2` | 与 HEAD 同 hash（`814ef046`），无未合提交 |
| 合并动作 | **不适用**——集成分支即工作分支；并入 `cards/B233.1-charter-7` 留协调者 |

## 二、全量核算（三段律全量的法定位置）

### 2.1 编译与全量 Go 测试

| 命令 | 读数 |
|---|---|
| `go build ./...` | **退出 0** |
| `go test ./... -count=1` | **63 包 ok；6 包无测试；1 包 FAIL**（唯一失败包 `internal/client`） |
| 失败测试 | 恰 1 支：`TestProductionHTTPClientCallersAreGatewayOnly` |
| 失败原文 | `execution_test.go:173: 生产代码不得自取 HTTPClient 拼请求（…）: internal/agentd/drop.go` |

**红窗归因（与 contract §6 / plan §4 已知既有红逐字一致）**：

- `internal/client` `TestProductionHTTPClientCallersAreGatewayOnly`：**基线即红**（B272 遗留，报 `internal/agentd/drop.go`）。contract §6 明文「去掉本卡全部改动后仍 FAIL」；本卡 `git diff e5e428a5..HEAD` 触及面不含该守卫与 `drop.go`。**非接缝缺陷，不修。**
- 多/少/漂移：**无**——恰 1 支、报文与基线逐字同。

三包子集复核（本轮新鲜，协调者指示单包避争抢）：

```
ok  github.com/Xsxdot/handoff/internal/ledger         0.557s   # TestOpenTicketsTerminal
ok  github.com/Xsxdot/handoff/internal/orchestration  0.255s   # TestB380
ok  github.com/Xsxdot/handoff/internal/approval       0.132s   # TestB380
```

### 2.2 web 面（环境性未跑通，非本卡）

| 命令 | 读数 |
|---|---|
| `cd web && npx vitest run` | **Startup Error**：`Cannot find package '@tailwindcss/vite'`（`web/node_modules` 不存在） |
| `cd web && npm run typecheck` | `sh: 1: tsc: not found` |

归因：**环境性**——`web/node_modules` 未安装；本卡 `git diff e5e428d..HEAD -- web/` 改动 **0 文件**。不冒充全绿；vitest/typecheck 本轮**未验证**（缺依赖，非测试失败）。处置：装依赖后由协调者补跑，或在 acceptance 环境执行。

### 2.3 红窗最终态核算

| 窗 | 终态要求 | 实测 | 判定 |
|---|---|---|---|
| `go test ./...` | 恰 1 支已知红（client 守卫） | 恰 `TestProductionHTTPClientCallersAreGatewayOnly` 1 支 | ✓ 恰等 |
| 三包子集 | 全 ok | 3/3 ok | ✓ |
| `codegraph check` | exit 0 | exit 0 | ✓ |

**红窗核算通过，不熔断。**

## 三、契约对照（integrate 纪律第 2 件）

### 3.1 codegraph 全量 vs 分支视图

```
$ codegraph --repo . check
CHECK_EXIT=0    fails=0  warns=41  assignedContainers 338 = viewContainers 338

$ codegraph --repo . validate
VALIDATE_EXIT=1  issues 恰 2 条（与 contract §6 逐字一致）：
  [cards-B272-charter] 新增容器 k_dropdir_fn 引用不存在的基线领域 d_coordination_api
  [cards-B374-charter] 新增容器 k_collab_model 已存在于基线，containersAdded 只接受新容器
views 列表无 B380（本分支无 codegraph/diffs/cards-B380-*.json——合法：只改函数体、未引入新符号）
```

- **全量对照**：check fails **清零**（0）。
- **分支视图对照**：B380 视图不存在（合法无新符号），无分支侧 fail 可对；validate 2 条均为**他分支**存量，非本卡引入。
- **契约错配暴露数：0**——integrate 纪律要求此处应为 0；§3.2 逐条核未发现任何「实现与契约语义相左」。

### 3.2 contract §1 冻结条目逐条核（本轮亲读源码）

| 条目 | 实测 | 结论 |
|---|---|---|
| C-1a `facade.go` AnswerTicket | `:127` AppendEvent → `:133` `m.hub.Publish(evt)`；失败只 Warn 不发布；幂等 `applied=false` 不 append 不发布 | ✓ 逐字在位 |
| C-1b `manager.go` approvePermission | `:2577` AppendEvent → `:2582` `m.hub.Publish(evt)` | ✓ |
| C-1c `approval/client.go` consult | `:430` AppendEvent → `:435-437` `else if c.hooks.Hub != nil { c.hooks.Hub.Publish(evt) }`；`Hooks` 无新字段（:62-77 结构逐字核） | ✓ |
| C-2 `taskstate.go` OpenTickets | `:188` `case evTicketsVoided, "completed", "failed", "archived":` 清任务全 key；注释写明与 `mirrorTaskTerminal` 不得合并 | ✓ |
| C-3 `WaitDeliveryPolicy` | `delivery.go:29` `ticket_answered` → false（函数体零改动） | ✓ 冻结 |
| C-3 `MirrorWatermark` | `mirror.go:104` 仍 `MAX(source_seq)` | ✓ |
| C-3 payload schema | `contracts.go:115-118` / `approval/client.go:551-554` 均 `ticket_id`/`answer` 逐字 | ✓ |
| C-3 `tickets_voided` 无 Publish | `ticketvoid.go:9` 注记 + 全仓 grep 无 `hub.Publish(tickets_voided)` | ✓ |
| C-3 `proto.go` 注记 | `:104-108` 已改述「**会 Publish**（B380）……但客户端不可交付」 | ✓ |
| C-3 三发布点收全 | 非测试 `grep EventTypeTicketAnswered` 恰 3 处写入 + proto 定义 + delivery 策略；**无第四处** | ✓ |

`codegraph resolve --doc`：`b380-contract.md` / `b380-breakdown.md` 均 exit 0。

### 3.3 图覆盖债（本节点 sym 实测）

| 符号 | `codegraph sym` | 处置 |
|---|---|---|
| `Store.OpenTickets` | 不在图中 | 只用普通路径，不带 `#Symbol` 锚 |
| `EventTypeTicketAnswered` | 不在图中 | 同上 |
| `encodeCardWaitSnapshot` | 不在图中 | 同上 |
| `AnswerTicket` | 命中 `n_store_Store_AnswerTicket`（非 orchestration 方法） | orchestration 侧只用普通路径 |
| `Manager.approvePermission` / `Client.consult` / `Hub.Publish` / `WaitDeliveryPolicy` | 命中 | 可用锚 |

未以 grep 顶替图查询：先跑 `sym` 判未命中，再回落 grep 复核写入点数量。

### 3.4 棘轮

**无提请。** 本卡接线未让任何接缝的存量直调下降（只新增 Publish 扇出，未替换任何直调）；`target.json`/`baseline.json`/`best.json` 零改动；check fails 0、legacyHits 与基线同构。`target.json` 本轮零改动。

## 四、生产闭环对账（integrate 纪律第 3 件；breakdown §4 五格）

三态口径：**已验证** = 生产载体亲读在位 + 支撑测试本轮实测绿；**真机待验** = 机内夹具构造不出的跨机/真进程行为。

| # | 行为（§4 简写） | 触发者会写 | 权威事实载体 | 消费者会读 | 结果可观察 | 三态 |
|---|---|---|---|---|---|---|
| 1 | 存量幽灵单随终态自愈 | 既有 `card_events` 终态镜像（B369 等早已在卡流） | `task_mirrored` `task_type∈{completed,failed,archived}` | `OpenTickets` `:188` → `cmd/card_wait.go:357` / `ledgerapi.go:198` | 快照不再列已答单；`open_tickets` 一致 | **已验证**（投影测试绿）；真机 #1 |
| 2 | 运行中人工 reply 关单 | `AnswerTicket` `:127-133` Append+Publish | store `ticket_answered` + hub 实时流 | `/ws/events`（先订阅后补发 `handlers.go:1451`）→ `mirrorSkip` 不含之 → `OpenTickets` | 数秒内 `task_mirrored(ticket_answered)` | **已验证**（TestB380AnswerTicketPublishes 绿）；真机 #2 |
| 3 | Manager 中介审批者批准 | `approvePermission` `:2577-2582` | 同上 | 同上 | 同上（answer=allow） | **已验证**（TestB380ApprovePermissionPublishes 绿）；真机 #3 |
| 4 | approval 面审批者批准 | `consult` `:430-437` 经 `Hooks.Hub` | 同上；生产装配 `approval_client.go:37 Hub: m.hub` | 同上 | 同上 | **已验证**（TestB380Consult… 绿）；生产装配**机内无断言** → 真机 #4 |
| 5 | 卡详情计数与快照同尺 | 派生自同一 `OpenTickets` 重放 | `OpenTicketCounts`（taskstate.go:213） | `ledgerapi.go:198` → 徽标 | `open_tickets` 一致 | **已验证** |
| 6 | wait 不唤醒 | —（零改动） | `WaitDeliveryPolicy` `delivery.go:29` false | `client.waitOnce` / `FollowEvents` / `automationWakeEvent` | 不发唤醒行 | **已验证**（策略冻结 + 单包 wait 族 ok）；真机 #6 |
| 7 | 文档表述与现状一致 | S1 三份文档 | SKILL/README/roadmap | 人 | 断言脚本 `B380_S1_ASSERT_OK` | **已验证**（本轮亲跑） |

**镜像链承重证据（亲读）**：`mirrorSkip` 仅 Progress/ApproverDecision/ApproverDisabled 三条，**不含** `ticket_answered`；`StreamEventsOnce` 路径不过 `WaitDeliveryPolicy`（`client.go:1669`「策略在应用谓词，传输必须仍见到该帧」）；`handleEvents` 先 `hub.Subscribe` 后 store 补发。→ 发布即进镜像，C-1 与 C-2 不冲突。

**闭环结论**：7 行五格全齐、触发者/载体/消费者/可观察结果均有着落；无「只有接口/空壳/测试替身」的承诺。跨机端到端归真机（P4=甲，不补机内集成回归）。

**契约错配暴露数：0**（纪律第 2 件要求）。

## 五、接缝缺陷记账（integrate 纪律第 4 件）

| # | 缺陷 | 发现处 | 现象 | 根因 | 接缝归属 | 去向 |
|---|---|---|---|---|---|---|
| 1 | `delivery.go` 注释漂移 | 本轮契约 §1 逐条核 | `WaitDeliveryPolicy` 头注仍写「审计类在服务端只入库不 Publish，实时流本就见不到」——对 B380 后的 `ticket_answered` 失真 | contract C-3 只要求 `proto.go` 注记随代码修订，漏了 `delivery.go` 同族头注（两处注释描述同一交付性事实，只改了一处） | `d_protocol` 注记 ↔ `d_transport_channel` 策略头注 | **本轮已修**（一行注释，行为零变；`go build`+wait 族测试绿） |
| 2 | 全量唯一红 | 本轮 `go test ./...` | client 守卫 FAIL | B272 遗留基线红（contract §6 已记） | 非本卡接缝 | 披露留协调者，不修 |
| 3 | web 依赖缺失 | 本轮 vitest/typecheck | Startup Error / tsc not found | 环境无 `web/node_modules`；本卡零触 web | 非产品接缝 | 未验证披露；装依赖后补跑归协调者 |
| 4 | `README.zh-CN.md:247` 同族措辞未同步 | plan §7 残余（预测在先） | 「`progress`、审批链审计事件只入库不唤醒」 | 不在 breakdown §3.1④ 有界文件集，plan 显式不动 | 文档面边界 | **残余**（见 §六）；动它须另走流程 |
| 5 | spec 文件不在本分支 | breakdown §2.4 / P3 | `docs/superpowers/specs/b380.md` 工作树不存在 | P3=甲：落点维持 `origin/cards/B380-charter-1@117bbd5a`，合并时协调者并入 | 文档可及性 | **残余**；归 finish（协调者） |

**本期不处理的缺口落 `docs/roadmap.md` 核对**：watermark 逐 seq 对账 + `tickets_voided` 发布语义两条已由 S1 落账（`grep -E '逐 seq|watermark.*对账'` → `:858` 命中）。#4/#5 是有卡/P3 承载的残余，不另塞 roadmap（有归属不重复记）。

## 六、回旋镖对账（integrate 纪律第 5 件）

| 预测（plan/breakdown） | 实测 | 差异 |
|---|---|---|
| P1=甲：无实现子卡、代码已冻结合全绿 | 三包子集 3/3 ok、build 0；无子分支可合 | **命中** |
| plan §4 已知既有红恰 2 条（client 守卫 + validate 2 条他分支） | 全量恰 1 支同红；validate 恰同 2 条 | **命中，无漂移** |
| plan T3 断言脚本 S1 全绿 | `B380_S1_ASSERT_OK` exit 0 | **命中** |
| plan：有界文件集 3 份，不扩围 | implement 改动恰 3 份；本节点另修 `delivery.go` 注释 1 行（接缝缺陷 #1，非扩 plan 范围——是集成新发现） | **计划外但有记账** |
| plan §7 残余：`README.zh-CN.md:247` 不动 | 确实未动（`:247` 仍在） | **命中** |
| P4=甲：不补机内端到端 | 无新集成测试文件 | **命中** |
| plan：web 全量不属本卡声明范围（只声明 go build+三包+断言） | web 因缺依赖未跑通 | **预测未覆盖 web 环境**——集成阶段按全量口径补跑时暴露；记为环境披露（缺陷 #3），非行为缺陷 |
| breakdown 真机 6 条归协调者 | 本轮零执行 | **命中预期**（交棒 §七） |
| 耗时预测 | 各台账无耗时数据 | 无法对账（与 B358 同款流程发现） |

## 七、交棒（集成完成 ≠ 完成）

- **下一步：acceptance**。真机清单 **6 条**（breakdown §6）**全部未执行**，登记防孤儿：
  1. 存量自愈：真实旧账本 `card wait B369 --subtree` 首行不再列已答工单——**未执行**
  2. 运行中人工 reply 跨机：远端 reply 后数秒内本机卡流现 `task_mirrored(ticket_answered)`——**未执行**
  3. 三类审批者路径（Manager 实时/reuse/approval 面）各落关单镜像——**未执行**
  4. 生产装配核对：真实 agentd 里 `Hub: m.hub` 生效（approval 发布进同一 hub）——**未执行**
  5. 断流/慢订阅残余：重连后 C-2 终态收口、长任务中途多轮工单观察——**未执行**
  6. wait 不唤醒：真实 `card wait`/`handoff wait` 不发唤醒行——**未执行**
- 另：web vitest/typecheck 因缺 `node_modules` **未验证**，装依赖后补跑。
- 本轮全部 Go 核算为机内读数；上述 6+1 条在台账登记「未执行/未验证」。
- **并入主线**（merge 到 `cards/B233.1-charter-7`、absorb、归档）**留协调者裁决**。本分支无 view diff、无 nodesDeleted，absorb 无图副作用。

## 八、本轮受控文件集

- `internal/client/delivery.go`（仅头注 3 行，行为零变）
- 本报告 + 台账（`docs/superpowers/ledgers/2026-09-23-b380-integrate-ledger.md`）

越界零：`codegraph/{target,baseline,best}.json`、业务逻辑代码、web、`README.zh-CN.md`、spec 搬运均未触碰。未调用 handoff CLI、未起新 executor、未派发子任务。
