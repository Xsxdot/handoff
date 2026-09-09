# B351 远端孤儿回收与重试改名：breakdown 提案

状态：**已拍板**（2026-09-09；协调者维持 F1–F4 冻结，无新岔口）
卡：B351
定级：**L3 轻档**；路由：contract → breakdown →（单轮）implement → review → acceptance → finish
有效基线：`cards/B233.1-charter-7` @ `6097892b`（不切换、不越过）
上游 spec：`docs/superpowers/specs/b351.md`（头部：已批准，2026-09-09）
冻结 contract：`docs/superpowers/specs/b351-contract.md`（头部：本提交随冻结物冻结）
本稿台账：`docs/superpowers/ledgers/2026-09-09-b351-breakdown-ledger.md`
图依据：`codegraph/best.json`；父为空的顶层领域以该文件为准
角色边界：本稿只出提案，不写实现代码、不建卡、不派发、不调用 handoff CLI；实际扇出与拍板归协调者。

## 0. 待拍板岔口清单（集中）

本稿**没有新增待拍板岔口**。下列事项是 spec/contract 已冻结的实现边界，列在稿首是为了防止 implement 重新把冻结语义当作偏好：

| 编号 | 已冻结事项 | 本稿吸收的边界 | 裁决 | 理由 |
|---|---|---|---|---|
| F1 | 扇出形态 | L3 轻档的一张跨域 `B351-impl` 子卡；T1–T4 只是同一轮 implement 的内部顺序，不拆并行功能卡。 | **维持冻结** | spec 轻档；拆并行会把轮次计数和停 task 拆散。实现挂 B351，不另开子卡号。 |
| F2 | 失败轮次载体 | 独立 count-only `card_dispatch_rounds` 事实，与 `card_tasks` purpose 计数相加；不写 `EvDispatched`、不写 `card_tasks`、不造事件。 | **维持冻结** | 用户选 B；加法（含第三次 `-3`）已由 spec C1 钉死。 |
| F3 | 补偿位置与方向 | `ViaTemplate` 失败出口先记轮次、再调用注入的 `DispatchCompensator`；ledgerstep 不 import client/agentd，账本不主动调 Stop。 | **维持冻结** | 两处生产装配共用同一失败出口；不把补偿塞进 Transport。 |
| F4 | 补偿动作与命名 | `Stop` 后 `Reclaim(force=true)`；Stop 409 仍 Reclaim，Stop/Reclaim 404 软成功；永不删分支；同 purpose 失败计入后续 `-2`，失败后成功再派第三次为 `-3`。 | **维持冻结** | 用户选 B；守 B77 不删分支、B233.4 留树必须再 reclaim。 |

若协调者要改 F1–F4 任一项，必须退回 spec/contract 对应冻结物；本稿不自行吸收产品或契约分叉。

## 1. 触及子系统清单与派卡资格核

`codegraph/best.json` 的 `.domains` 中 `parent == null` 共 15 个顶层领域；本卡有界地触及下列 4 个。类型直接取 `best.json`：logic 为机内可闭环，boundary 只能在机内验请求/响应与装配形状，涉及外部 agentd、远端网络、git/worktree、进程状态的行为列入真机清单。

| 子系统 id | best.json 类型 | 本卡有界文件集 | 触及理由 |
|---|---|---|---|
| `d_ledger` | 逻辑型 | `internal/ledger/store.go`、`internal/ledger/dispatch_rounds.go`、`internal/ledger/events.go`、`internal/ledger/tasks.go`；`internal/ledger/store_test.go`、`internal/ledger/ddl_parity_test.go`、`internal/ledger/events_test.go`；`internal/ledgerstep/dispatch.go`、`internal/ledgerstep/runner.go` 及对应 `*_test.go` | 图将 `Store.PurposeRounds` 与 `Dispatcher.ViaTemplate` 归入账本域；本域权威保存失败轮次、读加法计数，并在一次 ViaTemplate 中串接失败出口。`RecordDispatchAndLinkTask` 当前图缺节点，按源码位置核对。 |
| `d_transport` | 边界型 | `internal/client/client.go`、`internal/client/compensate.go`、`internal/client/client_test.go`；只读既有 `internal/targetclient/pool.go` 及其测试，不扩写入面 | 复合补偿在 client 内解释既有 Stop/Reclaim HTTP 状态码；目标机直连/relay 选路仍是外部现实，机内只用 fake HTTP 验契约形状。 |
| `d_cli` | 逻辑型 | `cmd/card_dispatch.go`、`cmd/card_node.go`；`cmd/card_dispatch_test.go`、`cmd/dispatch_test.go`、必要的 `cmd/card_node` 相关测试 | 裸 `card dispatch` 是 Dispatcher 的生产装配点；`runStepDispatch` 是明确排除项，只作“不复制 Dispatcher 补偿装配”的反面核对。 |
| `d_gateway` | 边界型 | `internal/agentd/cardstep.go`、`internal/agentd/server.go` 的 `clientForTarget` 入口；`internal/agentd/cardstep_test.go`、`cardstep_local_test.go`、必要的 `integration_test.go` | agentd `startCardStep` 是第二个生产装配点；clientForTarget 的本机/Pool 选路连接外部 agentd/relay，机内只验装配调用与 fake HTTP 形状。best 图将 `k_agentd_Server` 归 `d_gateway`。 |

不列为实现域：`d_execution`、`d_workspace`、`d_protocol`、`d_orchestration` 等不改文件；真实 executor 停止、git worktree 回收与跨机 transport 只作为边界验收清单，不用夹具把行为事实写成已验证结论。

### 1.1 四条派卡资格逐项核

| 子系统 | 1. 有界文件集 | 2. 契约面可枚举 | 3. 依赖 DAG 无环 | 4. 类型 | 资格结论 |
|---|---|---|---|---|---|
| `d_ledger` | 上表已圈出 schema、计数、ViaTemplate 失败出口及测试；禁止把整个 `internal/ledger`/`internal/ledgerstep` 目录视作文件集 | contract §3.1 #1–#23、§3.2 #24–#35、§3.5 #62–#68；`DispatchCompensator`/`RecordDispatchRound` 为 Ticket 0 已冻结窄缝 | `ViaTemplate` 只读/写 `d_ledger`，补偿由回调出站；`d_ledger` 不回调 client，无回边 | 逻辑型 | 通过，主编排/账本接缝；不拆成独立并行卡 |
| `d_transport` | `Client.StopAndReclaim` 与既有 Stop/Reclaim 客户端测试可圈出；`targetclient` 只作既有选路入口 | contract §3.3 #36–#51；不改 HTTP 路径、请求体以外的 wire 形状或服务端状态语义 | `d_transport` 只被 CLI/agentd 注入闭包消费；不让 client 反调 ledger | 边界型 | 通过，机内验 HTTP/ctx/状态码形状；真机验远端资源事实 |
| `d_cli` | 裸 dispatch 装配与排除项各自是明确文件；测试可穿过命令 RunE/targetClient | contract §3.4 #52–#55、#60–#61；复用已有 targetClient 与 cleanup | `d_cli → d_transport` 是 target.json 已有方向，预算仍 10；不新增 CLI→ledger/新命令 | 逻辑型 | 通过，不另派 CLI 子卡 |
| `d_gateway` | `startCardStep`、`clientForTarget` 与现有 agentd 测试可圈出；不把全部 server.go 作为范围 | contract §3.4 #56–#61；复用本机/Pool 选路，不新增 HTTP endpoint | `d_gateway → d_transport` 是 target.json 已有方向，预算仍 9；ledgerstep 通过注入不形成 gateway→ledger 反向边 | 边界型 | 通过；机内验装配/请求形状，真机验 agentd/relay/执行面 |

### 1.2 竖切债核对

当前没有圈不出的触点，不插竖切还债卡：账本表/读面、ViaTemplate、client 复合方法、CLI 装配和 agentd 装配分别可圈出。`internal/agentd/server.go` 仅纳入 `Server.clientForTarget` 符号附近的既有选路入口，不得借“同包”扩大为全 server；`internal/ledger/events.go` 仅纳入 `RecordDispatchAndLinkTask`、`PurposeRounds`、`WorkBranch` 所需读写，不借“同文件”改其它事件语义。

## 2. 契约增量核对

### 2.1 上游状态位、基线与图

- spec 头部明确为「已批准（2026-09-09）」；本稿引用文件状态，不以会话记忆替代。
- contract 头部明确声明随 `codegraph/target.json`、分支视图、Ticket 0 骨架与台账冻结；本稿把它作为已冻结物，不改冻结正文。其冻结声明不是实现成功声明，三个 `...Unwired` 空壳仍须由 implement 接线。
- spec 的有效基线是 `cards/B233.1-charter-7 @ 6097892b`；当前工作树仅包含本卡 contract 冻结提交，不切换分支、不越过该功能线基线。
- `codegraph/diffs/cards-B351-charter.json` 只提供 `DispatchCompensator`、`Store.RecordDispatchRound`、`Client.StopAndReclaim` 与 `Dispatcher` 字段的 Ticket 0 视图；新节点不能被本稿当成已接线生产调用。
- 图查询已命中 `Store.PurposeRounds`、`Dispatcher.ViaTemplate`、`Client.Stop`、`Client.Reclaim`、`targetClient`、`Server.startCardStep`、`Server.clientForTarget`、`Server.stepTransport`；`Store.RecordDispatchAndLinkTask` 与 `ddlStatements` 不在现状图中，前者按 `internal/ledger/events.go:167`、后者按 `internal/ledger/store.go:201` 源码定位，未把缺图节点冒充图事实。

### 2.2 冻结条目逐条对照

以下逐项核对 contract §3 的 68 条原子断言；归属的是本稿内部单元，不是外部派发卡。

#### 失败轮次与账本（contract §3.1）

| 条目 | 本稿归属 | 越界结论 |
|---|---|---|
| #1 | T1/T2 | **不越界。** 仅 Transport `err == nil` 后记轮次。 |
| #2 | T2 | **不越界。** Transport 失败不记轮次。 |
| #3 | T2→T1 | **不越界。** 写闸关闭记一条 `card×purpose` 失败事实。 |
| #4 | T2→T1 | **不越界。** 快照/挂账失败记一条失败事实。 |
| #5 | T2 | **不越界。** 写闸失败不写 `EvDispatched`。 |
| #6 | T2 | **不越界。** 写闸失败不写 `card_tasks`。 |
| #7 | T2 | **不越界。** 回滚型落账失败不留 `EvDispatched`。 |
| #8 | T2 | **不越界。** 回滚型落账失败不留 `card_tasks`。 |
| #9 | T1 | **不越界。** 失败轮次不追加任何 ledger event。 |
| #10 | T1 | **不越界。** 失败轮次不写 `card_tasks`。 |
| #11 | T1/T2 | **不越界。** 失败轮次写错不替换写闸/落账原错。 |
| #12 | T1 | **不越界。** `PurposeRounds = card_tasks` purpose 计数 + 失败轮次计数，禁止 `max`。 |
| #13 | T1 | **不越界。** `ReviewRounds` 继续走同一 additive 口径。 |
| #14 | T1 | **不越界。** 不读取 `CountRounds`。 |
| #15 | T1 | **不越界。** 不从 `EvDispatched` 反向推导失败计数。 |
| #16 | T1 | **不越界。** 失败行只表达一次失败尝试，不加 task/target/branch/actor。 |
| #17 | T1 | **不越界。** `card_id` 非空且 FK 到 cards。 |
| #18 | T1 | **不越界。** `purpose` 非空。 |
| #19 | T1 | **不越界。** `created_at` 非空并使用 Store 时钟。 |
| #20 | T1 | **不越界。** PG/SQLite 都建同名表。 |
| #21 | T1 | **不越界。** 两方言都建 `(card_id,purpose)` 查询索引。 |
| #22 | T1 | **不越界。** `RecordDispatchRound` 复用 `mutate` 与 `timeNow`。 |
| #23 | T1 | **不越界。** 只写失败轮次表，不触发 event listener。 |

#### 补偿回调与原始错误（contract §3.2）

| 条目 | 本稿归属 | 越界结论 |
|---|---|---|
| #24 | T2 | **不越界。** 回调形状固定为 `ctx,target,taskID`。 |
| #25 | T2/T4 | **不越界。** target 使用 ViaTemplate/Transport 的归一目标。 |
| #26 | T2 | **不越界。** taskID 只来自本次 Transport 返回值。 |
| #27 | T2 | **不越界。** 写闸失败先记轮次，再调用非 nil 钩子。 |
| #28 | T2 | **不越界。** 落账失败先记轮次，再调用非 nil 钩子。 |
| #29 | T2 | **不越界。** 每个失败尝试至多一次钩子。 |
| #30 | T2 | **不越界。** 钩子错只日志，带 card/target/task/原始错误上下文。 |
| #31 | T2 | **不越界。** 钩子错不替换 ViaTemplate 原错。 |
| #32 | T2 | **不越界。** nil 钩子仍记轮次并日志。 |
| #33 | T2 | **不越界。** nil 钩子不 panic。 |
| #34 | T2 | **不越界。** 不从补偿路径进入 Transport，不创建第二 task。 |
| #35 | T2/T3/T4 | **不越界。** 不删分支、不全机扫描、不造命令/HTTP 入口。 |

#### Stop/Reclaim client 语义（contract §3.3）

| 条目 | 本稿归属 | 越界结论 |
|---|---|---|
| #36 | T3 | **不越界。** Stop 使用收到的同一 ctx。 |
| #37 | T3 | **不越界。** Stop nil 后 Reclaim。 |
| #38 | T3 | **不越界。** Stop 409 后 Reclaim。 |
| #39 | T3 | **不越界。** Stop 404 软成功。 |
| #40 | T3 | **不越界。** Stop 其它错不 Reclaim。 |
| #41 | T3 | **不越界。** Reclaim force 恒为 true。 |
| #42 | T3 | **不越界。** Reclaim 404 软成功。 |
| #43 | T3 | **不越界。** `ErrReclaimUnsupported` 视为旧端软成功。 |
| #44 | T3 | **不越界。** Reclaim 非 404 错原样作为补偿错。 |
| #45 | T3 | **不越界。** Stop 409 + Reclaim 404 返回 nil。 |
| #46 | T3 | **不越界。** Stop nil + Reclaim 非 404 返回补偿错。 |
| #47 | T3 | **不越界。** 继续既有两个 POST 路径。 |
| #48 | T3 | **不越界。** 不增请求体字段，Reclaim 仍 `{"force":true}`。 |
| #49 | T3 | **不越界。** 不新设 client timeout，ctx 继续由 do 透传。 |
| #50 | T3 | **不越界。** 复合方法不删任务分支。 |
| #51 | T3 | **不越界。** 不改变任务状态机之外账本状态。 |

#### 两处生产组装（contract §3.4）

| 条目 | 本稿归属 | 越界结论 |
|---|---|---|
| #52 | T4 | **不越界。** 裸 card dispatch 的 Dispatcher 注入非 nil 补偿。 |
| #53 | T4 | **不越界。** CLI 闭包复用 `targetClient(target)`。 |
| #54 | T4 | **不越界。** CLI 闭包调用 `StopAndReclaim(ctx, taskID)`。 |
| #55 | T4 | **不越界。** CLI 调用 targetClient 返回的 cleanup。 |
| #56 | T4 | **不越界。** agentd `startCardStep` 注入非 nil 补偿。 |
| #57 | T4 | **不越界。** agentd 闭包复用 `Server.clientForTarget(target)`。 |
| #58 | T4 | **不越界。** agentd 闭包调用 `StopAndReclaim(ctx, taskID)`。 |
| #59 | T4 | **不越界。** 不复制 Pool/relay 选路规则。 |
| #60 | T4 | **不越界。** `runStepDispatch` 不复制 Dispatcher 补偿装配。 |
| #61 | T2/T4 | **不越界。** 只用本次 ViaTemplate 已解析 target，不从 taskID 反查机器。 |

#### 重试命名（contract §3.5）

| 条目 | 本稿归属 | 越界结论 |
|---|---|---|
| #62 | T2 | **不越界。** 首次非 review purpose 无后缀。 |
| #63 | T1/T2 | **不越界。** 后续非 review 使用 `PurposeRounds+1`。 |
| #64 | T1/T2 | **不越界。** review 使用 `ReviewRounds+1`。 |
| #65 | T1/T2 | **不越界。** 落账失败轮次进入下次编号。 |
| #66 | T1/T2 | **不越界。** 写闸失败轮次进入下次编号。 |
| #67 | T2 | **不越界。** Transport 失败不增加编号。 |
| #68 | T1/T2 | **不越界。** CountRounds reset 不减少派发历史计数。 |

**契约增量结论：不退回 contract。** 本稿只安排冻结接缝的实现顺序与验收；不新增字段、事件、HTTP 路径、命令、依赖方向或第二计数事实源。若 implement 需要改任一 Ticket 0 签名、把失败写入事件/`card_tasks`、改变 Stop/Reclaim 状态码处置、扫描全机 task、删分支或让 `d_ledger` import client，必须停止并退回 contract。

### 2.3 边界澄清回写核对

本轮没有产生超出 contract 的新边界澄清，因而不修改冻结 contract：

- `DispatchCompensator` 是 ledgerstep 的进程内注入回调，不是 `d_ledger → d_transport` 的 import 依赖；真实 client 仍由 CLI/agentd 组装。
- `target==""` 是既有本机目标语义，不是“跳过补偿”；两处补偿必须沿 Transport 同一选路。
- `runStepDispatch` 直接 POST 本机 agentd，不是 Dispatcher 装配点；不得在 CLI 复制第三套钩子。
- `card_dispatch_rounds` 是非叫醒、count-only 账本事实；不复用 `EvNeedsHuman`、`EvDispatched` 或任何可动作事件。

以上均已写在 contract §1、§2、§3、§4、§5；本稿只引用，不把澄清留在稿内孤立存活。

## 3. 行为闭环核对

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
|---|---|---|---|---|
| Transport 返回 taskID 后，写闸关闭 | 本次归一 target/taskID + `card_dispatch_rounds(card_id,purpose)` 失败行 | `ViaTemplate` 失败出口；下一次 `PurposeRounds` | 当前命令返回 `ErrWriteGateClosed` 原错；best-effort Stop→Reclaim；下一次同 purpose 分支为 `-2` | B351-impl / T1-T2 |
| Transport 返回 taskID 后，快照/挂账事务失败 | 同上；该事务回滚，不留 `EvDispatched`/`card_tasks` | `ViaTemplate` 失败出口；下一次 `PurposeRounds` | 当前命令返回落账原错；远端 task 被补偿；下一次同 purpose 不复用首轮名 | B351-impl / T1-T2 |
| 一次失败轮次后第二次派发成功，再次派发同 purpose | `card_tasks` 成功行数 + 永久失败轮次行数的 additive 计数 | `PurposeRounds` → ViaTemplate 分支命名 | 1 失败 + 1 成功后计数为 2，第三次 Transport 收到 `…-3`；不撞成功的 `…-2` | B351-impl / T1-T2 |
| review purpose 在写闸/挂账失败后重派 | 同一 additive 失败轮次 + `ReviewRounds` | ViaTemplate review 分支命名 | 下一次为 `…-review-2`，不回到 `review-1` | B351-impl / T1-T2 |
| 补偿钩子收到失败尝试 | Transport 返回的 taskID 与 ViaTemplate 已解析 target | CLI `targetClient` 或 agentd `clientForTarget` → `Client.StopAndReclaim` | Stop 成功或 409 后发一次 force reclaim；补偿错只日志，不替换落账/写闸原错 | B351-impl / T2-T4 |
| target 为空的本机派发在同一失败缝失败 | 与 Transport 同解析的空 target、本机 client、taskID | CLI/agentd 生产补偿闭包 | 本机同样 Stop+Reclaim；空 target 不成为免补偿分支 | B351-impl / T3-T4 |
| 失败尝试未成功挂账 | `EvDispatched`、`card_tasks`、`WorkBranch` 与 `CountRounds` 的既有事实面 | mirror/身份闸/WorkBranch/裁决计数 | 不产生派发成功镜像、不成为当前 attempt、不增加裁决轮；只有 PurposeRounds 看见 count-only 失败行 | B351-impl / T1-T2 |

每行均有触发者、权威载体、消费者、结果与归属子卡；没有只活在接口、测试或无人认领格子里的产品承诺。崩溃窗口与真实跨机资源行为不被上表假装闭环，见第 5 节真机清单。

## 4. 子卡清单与依赖 DAG

### 4.1 DAG

```text
Ticket 0 空壳（已冻结，不重开）
       ┌─────────────┴─────────────┐
       ▼                           ▼
T1 失败轮次表、事务写入、       T2 StopAndReclaim
   PurposeRounds/ReviewRounds
       └─────────────┬─────────────┘
                     ▼
       T3 ViaTemplate 失败出口与原始错误
                     │
                     ▼
       T4 CLI/agentd 两处生产装配 + 全接缝回归/图锚
```

T1–T4 是一张实现子卡内的连续工作单元，不是可独立派发的并行子卡。T1 与 T2 都只依赖已冻结 Ticket 0；T3 必须同时吸收 T1 的轮次写入和 T2 的复合 client 语义，才能在两条失败出口执行“先记轮次、再补偿”；T4 只能在两条失败出口的原始错误与补偿顺序已闭合后验收。若实现发现需要扩大文件集，先回到协调者核边界，不以同目录为由放宽。

### 4.2 子卡 B351-impl：失败派发轮次与远端 Stop+Reclaim 接缝

#### ①契约引用

- `docs/superpowers/specs/b351-contract.md` §§1–7，尤其 §3 #1–#68、§4 三个三重闸门决定、§6 Ticket 0 边界。
- `docs/superpowers/specs/b351.md` §§2–10、用户故事 1–3、测试决定、Out of Scope。
- 图锚：`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`、`internal/ledger/events.go#Store.PurposeRounds`、`internal/client/client.go#Client.Stop`、`internal/client/client.go#Client.Reclaim`、`cmd/card_dispatch.go#targetClient`、`internal/agentd/cardstep.go#Server.startCardStep`、`internal/agentd/cardstep.go#Server.stepTransport`、`internal/agentd/server.go#Server.clientForTarget`；Ticket 0 视图锚：`internal/ledgerstep/dispatch.go#DispatchCompensator`、`internal/ledger/dispatch_rounds.go#Store.RecordDispatchRound`、`internal/client/compensate.go#Client.StopAndReclaim`。
- 图缺覆盖的现状入口按源码定位：`internal/ledger/events.go:167` 的 `RecordDispatchAndLinkTask`、`internal/ledger/store.go:201` 的 `ddlStatements`。

#### ②意图与为什么

把“Transport 已受理、账本未挂账”收成一条可重试的失败事实：账本以不叫醒的 count-only 行记住耗费轮次，ViaTemplate 在写闸关闭和快照/挂账回滚两个出口统一执行“先记轮次、再补偿、返回原错”，client 在已有 Stop/Reclaim 语义内完成资源补偿，两个生产组装点各自沿本次 Transport 的 target 选路。这样保留 B233.6 的失败不当成功、B39 的已接管保护、B77 的不删分支和 B349 的身份闸，同时令下一次同 purpose 派发改名而不撞残留首轮分支。

本子卡不承担新 HTTP/事件/命令、全机扫描、Transport 前 pending、崩溃窗口、补偿失败后的后台追踪、真实 executor 行为、跨机 relay 可靠性或分支删除；这些边界不能由 fake 夹具推出。

#### ③验收

**T1：账本事实与加法计数（逻辑型，机内闭环）**

- 运行 `go test ./internal/ledger -run '^(TestB351|TestPurposeRounds|TestDDLDialectParity)' -count=1`，退出码为 0；新增/调整测试必须实际打开 SQLite 并覆盖 PG/SQLite DDL 文本的同名表、非空约束、FK 与 `(card_id,purpose)` 索引。
- 通过真实 `Store.RecordDispatchRound` 写入一次失败事实后，`PurposeRounds(card,purpose)` 与 `ReviewRounds(card)` 分别按 `card_tasks` 行数加失败行数返回；1 次失败 + 1 次成功挂账必须得到 2，第三次 ViaTemplate 的 branch 必为 `…-3`/`…-review-3`，不能用 `max` 或成功后作废失败行。
- 失败轮次行只含 `card_id/purpose/created_at` 语义；读回时 `WorkBranch` 仍因没有成功 `EvDispatched` 返回 `ErrNotFound`，`CountRounds` 不因该行增加，失败行不出 event listener。
- 以同一 `Store.mutate`/`Store.timeNow` 写入，并覆盖卡不存在、DB 写入失败、重复/并发读写的错误传播；失败轮次写入失败不能被测试夹具吞掉。

**T2：ViaTemplate 失败出口（逻辑型，必须穿过调用方）**

- 运行 `go test ./internal/ledgerstep -run '^(TestB351|TestViaTemplateSecondRoundGetsNumberedBranch|TestViaTemplateEmptyTargetIsLocal)' -count=1`，退出码为 0；测试必须调用真实 `Dispatcher.ViaTemplate`，不得只测一个记录/命名 helper。
- Transport 返回 `(taskID, baseCommit, nil)` 且 WriteGate 变 false：失败轮次恰好一行、补偿钩子恰好一次，参数是本次归一 target 与 taskID；无 `EvDispatched`、无 `card_tasks`、无第二次 Transport；返回错误 `errors.Is(err, ErrWriteGateClosed)` 为真。
- Transport 成功而 `RecordDispatchAndLinkTask` 失败：事务中快照与挂账都回滚；先落失败轮次再调用钩子；返回值仍 `errors.Is` 原始“快照与挂账落账”错误；成功路径首轮无后缀的既有测试继续通过。
- 补偿钩子返回错误、钩子为 nil、失败轮次写入失败三种反例都必须能变红：均不 panic；补偿/轮次错误只写结构化日志，不能顶替写闸/落账原错；失败轮次写入失败时仍调用一次补偿。
- `PurposeRounds`/`ReviewRounds` 的下一轮命名必须由 ViaTemplate 实际读取，不能在测试中手写 `-2` 绕过挂号；Transport 自身返回错误时钩子调用次数为 0、失败轮次不增加。

**T3：client StopAndReclaim（边界型：机内只验契约形状）**

- 运行 `go test ./internal/client -run '^(TestB351|TestReclaimOnOldAgentdReportsUnsupported|TestReclaimUnknownTaskIsNotMistakenForUnsupported|TestReclaimForceCarriesIntoRequestBody)' -count=1`，退出码为 0；测试用 fake HTTP 穿过真实 `Client.StopAndReclaim`、`Client.Stop`、`Client.Reclaim`，不能只替换两个方法的函数指针。
- 断言同一个 ctx 进入 Stop 和后续 Reclaim；Stop 200 后按顺序发 Stop→Reclaim 一次，Stop 409 也发 Reclaim 一次；Reclaim body 可被 JSON 解码为 `force=true`；Stop 其它非 404/409 错误不发 Reclaim。
- Stop 404、Reclaim 404、旧端 `ErrReclaimUnsupported` 均返回补偿软成功；Stop 409 + Reclaim 404 也返回 nil；Reclaim 409/5xx/网络错按 contract 返回补偿错。不得让复合方法改变既有 HTTP 方法、路径、错误类型或新增 timeout。
- fake HTTP 只能证明 `Client` 请求/响应/ctx 形状；“远端 task 真正停止、managed worktree 真正消失、分支仍存在”**未验证，需真机**，不得从 fake HTTP 断言外部资源事实。

**T4：CLI/agentd 生产装配与全接缝（CLI 逻辑型；agentd/transport 边界型）**

- 运行 `go test ./cmd ./internal/agentd -run '^TestB351' -count=1`，退出码为 0；生产字面量 `&ledgerstep.Dispatcher{...}` 只有裸 `card dispatch` 与 `Server.startCardStep` 两处补偿非 nil。测试应穿过两处装配，不只构造一个手写 Dispatcher。
- CLI 补偿闭包实际调用 `targetClient(target)`，使用其返回 cleanup，并调用所选 client 的 `StopAndReclaim(ctx, taskID)`；agentd 闭包实际使用 `Server.clientForTarget(target)`，不复制 Pool/relay 规则。`target==""` 的 fake HTTP 请求必须落到与 Transport 同一的本机 client。
- `cmd/card_node.go#runStepDispatch` 的直接 POST 路径不新增 Dispatcher/第二套补偿装配；节点失败仍由既有 runner 上抛到 `haltForHuman` 的出口，不新增成功事件或成功输出。
- 运行 `go test ./internal/ledgerstep ./internal/ledger ./internal/client ./cmd ./internal/agentd -run '^TestB351' -count=1`，退出码为 0；至少一条回归必须从 ViaTemplate → 生产闭包 → 真实 Client fake HTTP 穿过 Stop/Reclaim，并反向断言无事件、无挂账、无第二 task、无分支删除。
- 运行 `go build ./...`、`git diff --check`，均退出码为 0；运行 `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B351-charter resolve --doc docs/superpowers/specs/b351-breakdown.md`，退出码为 0，锚点只能是 `ok`/`moved`。全量 `go test ./... -count=1` 需记录原始输出；当前 contract 台账已记录的既有 `d_execution→d_orchestration` dead-contract/dead-interface 失败不得被误报成 B351 通过或归因于本卡。

**缺陷族对抗审查（本子卡验收栏）**

1. **生命周期 / 状态机中断：**
   - 本地路径不创建新的持久进程、任务、工单或临时目录；单次 ViaTemplate 的失败出口只做有序账本写入和同步补偿。`Stop` 留树、`Reclaim` 要终态，因此必须保持 Stop 成功或 409 后再 Reclaim(force=true)；Stop 404 作为无对象收尾。
   - Transport 成功后进程在失败轮次落盘前重启、失败轮次落盘后尚未补偿时重启，以及补偿网络失败后远端 task 仍 running 的实际状态**未验证，需真机**；前两项是 spec OOS，后一项交由人工 stop/reclaim，不能在机内夹具里宣称自动恢复。补偿不得放进 B39 `executorStarted` defer，也不得因进程重启去删已接管分支。
2. **静默失败 / 误导报错：**
   - Transport 错误不记耗费、不补偿；写闸/落账原错保持 ViaTemplate 返回值；耗费写错与补偿错只日志并带 card/target/task 上下文，不把副作用报成成功，也不创建第二 task。nil 钩子必须显式日志且不 panic。
   - Stop/Reclaim 的 404/409/旧端软成功与非软错误必须由真实 client 错误形状区分；补偿失败不追加可动作事件、不输出“回收成功”给 wait。fake HTTP 可验传播，远端用户实际文案/退出码与 agentd 日志**未验证，需真机**。
   - **无“报成功但没做”的新增窗口，因为**失败路径统一返回原始账本/写闸错误且不构造 `DispatchResult`；没有 `EvDispatched`/`card_tasks` 就不能把失败伪装成成功派发。
3. **跨平台假设：**
   - PG/SQLite DDL 与索引必须分别回归；Go client 继续复用既有 HTTP/relay、ctx 和 target 选路，不新增 shell、HOME、进程组或路径拼接规则。跨方言数据库建表行为与不同 OS 的实际 git/worktree/进程终止**未验证，需真机**。
   - 空 target、本机登记别名、直连目标、relay 目标的实际选路与回收要在真机清单分别执行；Linux fake HTTP 不能推出 Windows/macOS agentd、代理关闭、权限模型或真实 relay 行为。
4. **假红 / 假绿测试：**
   - T1 必须实读 Store；T2 必须穿过 ViaTemplate；T3 必须穿过真实 Client HTTP；T4 必须穿过两处生产 Dispatcher 装配。只测 `PurposeRounds`、只 spy 钩子、只断言日志或只造 HTTP 响应均不足。
   - 正向断言必须配反面：无 `EvDispatched`、无 `card_tasks`、无 WorkBranch、无 CountRounds 增加、无第二 Transport、无分支删除；Stop 409、404、Reclaim 404、nil 钩子、耗费写失败都必须有能变红的测试。1 失败 + 1 成功 + 第三次 `-3` 是 C1 的承重反例。
   - 同一调用的补偿至多一次可由 spy+真实 ViaTemplate 验证；跨多个并发调用的全局分支唯一性不是本 contract 承诺，不把它写成通过结论。真实负载、PG 锁竞争、relay 重连和 executor 行为**未验证，需真机**。
5. **门禁绕过：**
   - 新增的账本写入只能经 `Store.RecordDispatchRound` → `Store.mutate`，卡 FK、purpose 非空、时间字段和方言 schema 共同执法；不经 appendEvent，不绕过事件 listener。新增执行动作只能经既有 Stop/Reclaim HTTP，服务端重新检查终态/managed/脏树，客户端不造 delete branch 入口。
   - **无绕过，因为**本卡不新增命令、HTTP 路径、权限门或全机扫描；Stop 409 后仍由 Reclaim 重新检查终态，Reclaim force 仅放宽脏树清除，不放宽任务/分支身份。检查与动作之间远端状态可能变化，失败按补偿错日志处理，真实 TOCTOU 行为**未验证，需真机**。
6. **序列化边界：**
   - 失败轮次是 SQL 行，不新增 JSON/proto/事件字段；不存在第二个手写 wire 投影。既有 Reclaim `force` JSON 是唯一需穿过的请求序列化边界，T3 必须实际解码请求体并断言 true；不能以两个包各自单测替代。
   - target/taskID 是进程内回调标量，不从 taskID 反查或重新编码；缺少/空 target 的本机语义用显式空字符串测试，不用零值猜测成功。
7. **枚举新值过既有白名单：**
   - **无，因为**本卡不新增状态名、事件类型、kind、HTTP reason 或 proto 枚举；失败事实不复用 `EvNeedsHuman`/`EvDispatched`/`EvTaskMirrored`，不会新增 wait/wake 白名单路径。既有 Stop/Reclaim 404/409 仍由 client 错误类型消费。
8. **承重安全属性有测试锁住：**
   - 不删分支、失败不成为 `EvDispatched`、一次失败+成功后第三轮必须是 `-3`、Stop 409 仍 force Reclaim、Reclaim force=true、nil 钩子不 panic、补偿至多一次，均须由 T1–T4 的可变红测试锁定；不能只凭实现恰好如此。
   - count-only 失败行不得携带 task identity/target/branch/actor，避免把不存在的本地 task 当当前 attempt；WorkBranch/CountRounds 反例必须随回归一起断言。

#### ④入口指针（有界文件集）

- 账本：`internal/ledger/store.go#Store.mutate`、`internal/ledger/store.go#Store.timeNow`、`internal/ledger/events.go#Store.PurposeRounds`、`internal/ledger/events.go#Store.ReviewRounds`、`internal/ledger/events.go:167`、`internal/ledger/dispatch_rounds.go#Store.RecordDispatchRound`。
- 编排：`internal/ledgerstep/dispatch.go#Dispatcher.ViaTemplate`、`internal/ledgerstep/dispatch.go#DispatchCompensator`、`internal/ledgerstep/runner.go#StepRunner.dispatchNode`。
- client：`internal/client/client.go#Client.Stop`、`internal/client/client.go#Client.Reclaim`、`internal/client/compensate.go#Client.StopAndReclaim`。
- 装配：`cmd/card_dispatch.go#targetClient`、`cmd/card_dispatch.go#cliTransport`、`cmd/card_node.go#runStepDispatch`、`internal/agentd/cardstep.go#Server.startCardStep`、`internal/agentd/cardstep.go#Server.stepTransport`、`internal/agentd/server.go#Server.clientForTarget`。
- 测试入口：`internal/ledgerstep/dispatch_test.go`、`internal/ledger/events_test.go`、`internal/ledger/ddl_parity_test.go`、`internal/client/client_test.go`、`cmd/card_dispatch_test.go`、`internal/agentd/cardstep_test.go`/`cardstep_local_test.go`；实现新增的 B351 测试应留在上述有界文件集中。

## 5. 真机清单（机内未验证项汇总）

以下均标为**未验证，需真机**，不得用 fake HTTP、SQLite 或本地夹具改写成已通过：

1. CLI 裸 `card dispatch` 在真实目标机上 Transport 已返回并且协调者账本真实写闸/挂账失败时，task 是否真的被 Stop，managed worktree 是否真的被 force Reclaim，原 task 分支是否仍在，命令是否仍展示落账/写闸原错。
2. `card dispatch --step`/控制台节点同一写闸失败缝：agentd 真实 `clientForTarget` 选路、空 target 本机、直连登记名、远端 Pool/relay 目标，均执行一次 Stop+Reclaim；节点失败是否真实进入既有 `haltForHuman`。
3. Stop 已终态返回 409 的并发窗口：随后 Reclaim(force=true) 是否真的清掉 managed worktree；Stop/Reclaim 404、旧 agentd 双 404、网络断连时资源是否留下并可由人工入口回收。
4. 第二次、第三次真实派发：首轮残留分支保留，失败后新分支为 `-2`，失败后成功再派为 `-3`/`review-3`，真实 git checkout 不再报 `already exists`；分支不被删除。
5. Transport 成功后进程重启/agentd 重启、耗费轮次落盘失败、补偿请求中途断链后的孤儿 task 与 worktree 状态；这些是 spec OOS 或人工接管项，不在本卡机内验收中伪造“自动修复”。
6. PostgreSQL advisory lock/迁移、真实 relay/代理、Linux/Windows/macOS 的 HTTP、git worktree、权限与进程组语义；机内 DDL/fake HTTP 只证明契约形状。

## 6. 交棒与自检

- 交棒对象：协调者拍板后派发 `B351-impl`；implement 先读本稿、spec、contract，再按 T1→T4 在同一上下文实现和验收。
- 本节点不创建实现卡、不派发、不改 codegraph 目标图；当前 Ticket 0 空壳不能作为功能完成证据。
- 协调者拍板后，必须把 F1–F4 的裁决与理由回写本节第 0 节，头部状态改为「已拍板（日期）」并与本稿同批提交；在此之前本稿明确保持「待拍板」。
- 交棒前法定自检：`git diff --check`、`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . --view cards-B351-charter resolve --doc docs/superpowers/specs/b351-breakdown.md`；命令未实际跑到 0 之前，不得写成通过。
