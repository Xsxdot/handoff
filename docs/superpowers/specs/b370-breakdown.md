# B370 镜像身份闸降级判定：breakdown 拆解提案

状态：**出稿待拍板**（2026-09-14；本稿由 handoff executor 出稿，本地协调者拍板）
卡：B370
标题：镜像身份闸降级判定：旧 envelope 缺 node/attempt 投影时改用 source 列匹配
定级：**L3 轻档**；路由：contract → breakdown →（单轮）implement → review → acceptance → finish
有效基线：`cards/B233.1-charter-7`（本卡合并目标；当前分支 `cards/B370-charter-6` 不切换、不越过）
上游 spec：`docs/superpowers/specs/b370.md`（头部：**已批准**，2026-09-14，用户原话「批准，进 contract」）
冻结 contract：`docs/superpowers/specs/b370-contract.md`（头部：上游已批准；**冻结状态：本提交随 target.json、diffs/cards-B370-charter-4.json 与 Ticket 0 骨架同批冻结**）
本稿台账：`docs/superpowers/ledgers/2026-09-14-b370-breakdown-ledger.md`
图依据：`codegraph/best.json`（父为空的顶层领域即子系统清单，类型取 `type`）；分支视图 `codegraph/diffs/cards-B370-charter-4.json`
角色边界：本文是**提案**；不写实现代码、不建卡、不派发、不调用 handoff CLI、不起新 executor。扇出与拍板归协调者。

---

## 0. 待拍板岔口清单（集中，拍板者按此裁决）

本稿只有一处**已成事实的边界澄清**（写回 `b370-contract.md` §12，非自由偏好）；以下 F1–F7 是真正的岔口，一律**待拍板**，本稿不自行选定往下写。

| 编号 | 岔口 | 方案与取舍 | 本稿倾向 |
| --- | --- | --- | --- |
| **F1** | `node` 缺失/为空时「定位」的是哪条快照，防迟到的牙齿从哪来 | **A（推荐）**：取该事件所属卡上**最新合格快照**为「当前身份」，再比 `ev.SourceTask == snapshot.Attempt && ev.SourceTarget == snapshot.Target`；不等=拦。防迟到完整，代价是同卡多节点并发时可能误杀。**B**：以 `ev.SourceTask` 反查「该任务自己的」合格快照，再只比 `Target`；`source_task` 等于自身恒真，等同放弃 source_task 维度的迟到判定。 | **A**。依据 spec §74 矩阵与 §79「`TestB2336StaleAttemptDoesNotWake` 保持绿」：`task-empty`（node/attempt 空串、source_task=task-empty）事件在该回归里必须**不唤醒**，只有 A 能同时满足「空身份应交付」与「该回归保绿」；B 会让它落 branch 3 放行而转红。仍请拍板确认。 |
| **F2** | 契约 §3.2 B 分支 2「快照无工作流身份」与分支 3「无快照」在实现里怎么区分 | 冻结的 `CurrentWorkflowAttempt(st, cardID, node)` 只返回 `(snapshot, found)` **二态**，无法表达「有 `EvDispatched` 但无合格身份」与「完全没有 `EvDispatched`」。**A（推荐）**：在 `internal/client` 包内新增**未导出**三态取数（有合格快照 / 有派发但无身份 / 无派发），导出面不变、不新增跨域边，两个 reason 按契约区分。**B**：复用 `CurrentWorkflowAttempt` + 单独再扫一遍「有无 EvDispatched」；两次扫全卡，两处判断可能不一致。**C**：合并 branch 2/3 为一个 reason；与 §3.2 B 与 §3.1 常量注释冲突，需退回 contract。 | **A**。 |
| **F3** | 身份非空且缺 `source_task` 时返回哪个 reason | 契约 §3.2 A 末句把「缺 `source_task`」并入「`source_task` 不匹配」的 `WakeGateStaleAttempt` 桶；而 §3.1 常量 `WakeGateMissingSourceTask` 注释写「保留给 implement：身份非空且缺 source_task 的独立 reason」。两者不自洽。**A**：返回 `WakeGateMissingSourceTask`（可观测性更细）。**B**：返回 `WakeGateStaleAttempt`（贴 §3.2 A 字面）。 | **A**，但需拍板。两值都在冻结枚举内，任一选择都不退回 contract。 |
| **F4** | `JudgeMirroredWake` 收到 `st == nil`（或 `*ledger.Store` 未装配）的语义 | 契约 §10 遗留项。**A（推荐）**：函数入口显式判 nil 返回带上下文的错误，不 panic。**B**：依赖调用方守卫（agentd 消费点已判 `s.ledger == nil`；cmd 侧 `openLedger` 成功后才调用，永不为 nil），函数不重复判。 | **A**，防御性更强且消费点已各自有错误路径。 |
| **F5** | reason 落日志的载体 | 契约 §3.2 C/§3.3 要求「两个消费点按 reason 落日志」，而 Ticket 0 骨架已在共享函数 `JudgeMirroredWake` 内 `slog`。**A（推荐）**：两个消费点各自补一条按 `decision.Reason` 的日志（贴契约字面，且在消费点上下文里带 event/card/seq）。**B**：共享函数日志即视为留痕，消费点不再重复，避免同一判定两处日志。 | **A**，但 §3.2 C 的「两个消费点」是契约字面，选 B 需协调者认可。 |
| **F6** | 子卡形态 | **A（推荐）**：**单卡单轮**，`B370-impl` 内部 T0→T4（同 B349/B353 轻档先例）；两处消费点承载同一条规则，拆并行子卡会把规则拆散。**B**：拆「共享实现」与「两消费点穿缝」为多卡。 | **A**。spec 定级理由已写「拆并行子卡会把规则拆散——同 B353」。 |
| **F7** | 实现轮的图产出落点 | 实现会删 2 个已入基线图的符号（`n_cmd_cardWaitCurrentWorkflowAttempt`、`n_agentd_Server_currentWorkflowAttempt`），可能增 1 个未导出取数；契约冻结的 diff 挂在 `cards-B370-charter-4`，当前分支 `cards/B370-charter-6` 无 diff。**A（推荐）**：实现轮在自己的分支重扫并落新 `codegraph/diffs/<implement-branch>.json`（含 nodesDeleted/Added），让本分支 `codegraph --view <branch> check` 为对照判据。**B**：实现轮不动图，删除的符号等 `absorb`（分支合回主线）统一重扫。 | **A**；B 会让契约声明的 `d_transport→d_ledger` 活跃边在本分支无视图背书（同 §8 known-red 过渡态）。 |

> 若协调者对 F1–F5 的裁决改变契约 §3.2 判定正文或 §3.1 导出面，应先修订/重冻 `b370-contract.md` 再扇出实现；本稿不自行吸收。

---

## 1. 触及子系统清单与派卡资格核

`codegraph/best.json` 中 `parent` 为空的顶层领域是本卡子系统清单，类型直接取 `type`；容器映射（`codegraph/domains` 与 `best.json`）：`k_cmd_fn`→`d_cli`、`k_agentd_fn`/`k_agentd_Server`→`d_gateway`、`k_client_fn`/`k_client_model`→`d_transport_channel`（子域，父 `d_transport`）、`k_ledger_Store`→`d_ledger`、`k_proto_model`→`d_protocol`。

| 子系统 id | 类型（best） | 本卡有界文件集 | 触及理由 |
| --- | --- | --- | --- |
| `d_transport` | boundary | **新建** `internal/client/wakegate.go`（Ticket 0 已落，实现轮改判定正文 + 包内未导出取数）、**新建测试** `internal/client/wakegate_test.go` | 共享判定与快照取数的宿主（与 `WaitDeliveryPolicy` 同包）；本卡唯一新实现的落点。 |
| `d_ledger` | logic | **只读** `internal/ledger/events.go#Store.EventsFromAsc`、`#DispatchSnapshot`；**不改** `internal/ledger/mirror.go`、schema、`mirrorSkip` | 提供 `EvDispatched` 快照与 source 三列；不新增派生查询。 |
| `d_cli` | logic | `cmd/card_wait.go`（退役 `cardWaitCurrentWorkflowAttempt`、补 reason 日志）、`cmd/card_wait_test.go` | 消费点一：`card wait` stdout 是否叫醒；`--subtree` 取事件所属卡。 |
| `d_gateway` | boundary | `internal/agentd/wakeconsumer.go`（退役 `Server.currentWorkflowAttempt`、补 reason 日志）、`internal/agentd/wakeconsumer_test.go`；装配接缝 `internal/agentd/server.go#Server.SetupAutomation`、`cmd/agentd.go#setupLedger` | 消费点二：自动化唤醒；`s.ledger` 由 `setupLedger` 先 `SetLedger` 后 `SetupAutomation` 装配。 |
| `d_protocol` | logic（相邻，无改动） | 只读 `internal/proto/ledger.go#LedgerEvent` 的 source 三列 | 行为依赖既有 wire DTO；本卡不加字段、不改事件类型、不改 HTTP/前端契约。 |
| `d_orchestration` | logic（相邻，无改动） | 只读 `internal/agentd/server.go` 的 cursor 装配；不改语义 | 唤醒被拦后仍走既有 `seen`+游标推进；本卡不改游标。 |

**边界型说明（不落派卡红线）**：`d_transport` 与 `d_gateway` 在 best 里是 boundary（接缝对面是网络/relay/真实 agentd）。本卡触及的**具体面**是「对进程内 `ledger.Store` 的纯判定」与「消费循环内的布尔出口」，其外部现实（网络、relay、真实旧写入者）**不在本卡改动面内**；因此这两处的行为在机内用真实 SQLite 夹具可闭环，真实网络/主机行为按 §6 真机清单交协调者。

### 1.1 派卡资格四条逐项核

| 子系统 | ①有界文件集 | ②契约面可枚举 | ③依赖 DAG 无环 | ④类型 | 结论 |
| --- | --- | --- | --- | --- | --- |
| `d_transport` | `wakegate.go` + `wakegate_test.go` 两文件 | 契约 §3.1 导出面（2 func + 3 model + 6 const）、§3.2 判定正文 | 新增方向 `d_transport→d_ledger` 已冻结；`internal/ledger` 闭包不含 `internal/client`（`go list -deps` 无命中） | boundary（本面逻辑可闭环） | 通过，主实现面 |
| `d_ledger` | 只读两符号，零生产文件改动 | `EventsFromAsc` 签名、`DispatchSnapshot` 字段已冻结 | 只被 `d_transport` 读，无回边 | logic | 通过，不派独立卡 |
| `d_cli` | `card_wait.go` + `card_wait_test.go` | 契约 §5.5 #43–#46、#50 | `d_cli→d_transport` entry 已声明（预算 12 不变） | logic | 通过，不拆卡 |
| `d_gateway` | `wakeconsumer.go` + `wakeconsumer_test.go`（必要时 server 装配只读） | 契约 §5.5 #47–#49 | `d_gateway→d_transport` entry 已声明（预算 9 不变） | boundary（本面逻辑可闭环） | 通过，不拆卡 |

### 1.2 竖切债核对

`internal/agentd` 是 61 文件平铺大包，但本卡不把整包作为上下文：身份闸只圈 `wakeconsumer.go` 的判定接线与旧扫描退役，cursor/装配只读 `server.go` 的既有字段。`cmd` 是 CLI 入口聚合（正当扁平），只圈 `card_wait.go`。**能圈出有界文件集，不插竖切还债卡**。实现若需改上述集合之外的生产文件，必须退回协调者重核边界，不得以「同目录」放宽。

---

## 2. 契约增量核对

### 2.1 上游状态位核对（读文件头，不靠会话记忆）

- spec `docs/superpowers/specs/b370.md:3`：`状态：**已批准**（2026-09-14，用户原话「批准，进 contract」）` —— 已批准，核对通过。
- contract `docs/superpowers/specs/b370-contract.md:3-5`：`上游状态：已批准`、`冻结状态：本提交随 codegraph/target.json、codegraph/diffs/cards-B370-charter-4.json 与 Ticket 0 骨架同批冻结` —— 已冻结，核对通过。
- 契约 §1 已写上游状态位核对条目；本稿引用文件头，不用会话记忆替代。

### 2.2 冻结物逐组对照

| 冻结物 | 本稿吸收位置 | 越界结论 |
| --- | --- | --- |
| §3.1 导出面：`WakeGateReason`（6 值）、`WakeGateDecision`、`WakeGateEvent`（8 字段）、`CurrentWorkflowAttempt`、`JudgeMirroredWake` | T1 | **不越界**：导出面一个符号不增不删；新增三态取数为**包内未导出**，不属导出面（见 §12 修订记录）。 |
| §3.1 `CurrentWorkflowAttempt` 语义（合格条件、最新 seq、分页到尾） | T1 | **不越界**：语义保持；分支 2/3 的三态由包内取数承载，不改变本符号契约。 |
| §3.2 A 身份非空路径（5 项比对、缺 source_task 闭集拒绝、`!found` 不叫醒） | T1 | **不越界**：按 B349 正文保留。缺 source_task 的 reason 值见 F3，待拍板。 |
| §3.2 B 身份缺失/为空三分支（能判定才拦） | T1 | **不越界**：三分支语义照契约实现；定位键语义见 F1 待拍板。 |
| §3.2 C reason 可观测 | T1/T2 | **不越界**：reason 由 `WakeGateDecision` 携带；落日志载体见 F5 待拍板。 |
| §3.3 两消费点接线（`card wait` 不 Encode / wakeconsumer 翻布尔并走 seen+游标） | T2 | **不越界**：消费点既有动作不变，不新增事件/命令/HTTP。 |
| §4 依赖方向：`d_transport→d_ledger`（budget 0）；`d_cli`/`d_gateway`→`d_transport` entry 补 `client（包级函数）` | T1/T4 | **不越界**：不新增方向；包内未导出取数仍在 `d_transport_channel` 容器内，不产生新跨域边。 |
| §5.1–5.4 原子冻结清单全部条目 | T1/T3 | **不越界**：逐条落到 T1（共享）+ T3（穿缝）；#20–#24 直测见 T1。 |
| §5.5 穿调用方接缝（禁止只测 helper） | T3 | **不越界**：两消费点各跑一遍断言矩阵，穿过 `runCardWait`/`consumeAutomationEventsOnce`。 |
| §6 三重闸门四项拍板记录 | 全文 | **不越界**：本稿不重开四项拍板；F 项只在契约留白处提问。 |
| §7.1 Ticket 0（共享符号 + 两处调用点已落；三分支保保守） | T0 | **不越界**：Ticket 0 是已完成前置，不重开。 |
| §7.3 图产出（target + `cards-B370-charter-4` diff 冻结） | T4 | **不越界**：本稿不改 target；实现轮图产出落点见 F7。 |
| §8 known-red `dead-contract d_transport→d_ledger`（baseline 未重扫） | T4 | **不越界**：本分支仍以视图 `cards-B370-charter-4` check 为对照判据；无视图红为合并前过渡态。 |
| §9 本节点欠账 1–4 | T1/T2/T3 | **不越界**：本稿把它们变成实现轮的验收判据，不写成已生效。 |
| §10 移交 plan 附区 4 项 | T2/T4 | **不越界**：降级定位细节、旧扫描退役、reason 日志、nil 处理分别落 T1/T2（F4/F5 待拍板）。 |

**契约增量结论**：**不退回 contract**。没有需要新接缝的发现；F1–F5 若裁决改变判定正文或导出面，才需回退重冻。核对中做出的**边界澄清**（三态定位属包内实现面、不属导出契约面）已按纪律回写 `b370-contract.md` §12，不只活在稿内。

---

## 3. 子卡清单与依赖 DAG

按 F6 推荐 A（轻档单卡单轮），本稿提出一张跨域实现子卡 `B370-impl`，其内部为 T0→T4 的有序单元；T0–T4 **不是**可独立派发的外部子卡。

```text
T0 Ticket 0 前置复核（已落，不重开）
 └──> T1 internal/client：激活降级三分支 + 包内三态/源列定位 + 直测
        └──> T2 两消费点：退役旧扫描 + reason 日志 + 参数透传
               └──> T3 两消费点穿缝矩阵测试 + B349 测试重定基线
                      └──> T4 全接缝回归 + 图/文档门禁 + 真机清单交接
```

| 内部单元 | 前置 | 产出与交棒 |
| --- | --- | --- |
| T0 | spec/contract/HEAD | 固定文件集、Ticket 0 已落事实、无新接缝结论；不改运行时代码。 |
| T1 | T0 | 共享判定三分支与包内取数闭环；`CurrentWorkflowAttempt` 直测；交 T2/T3 同一权威。 |
| T2 | T1 | 两消费点接共享符号、退役两份旧扫描、补 reason 日志；交 T3 穿缝。 |
| T3 | T2 | 两消费点各跑断言矩阵（含反面），B349 三测试重定基线、B2336 保绿。 |
| T4 | T3 | 全接缝回归、图锚/契约门禁、真机清单交接。 |

### 3.1 子卡 `B370-impl`（提案）

#### ①契约引用
- `docs/superpowers/specs/b370-contract.md`：§3.1 导出面、§3.2 A/B/C 判定正文、§3.3 消费点接线、§4 依赖方向与预算、§5.1–5.5 原子冻结清单、§8 known-red、§9 欠账、§10 移交项。
- `docs/superpowers/specs/b370.md`：§方案、测试决定（接缝清单矩阵）、Out of Scope、备注。
- 入口锚点见 §3.2–§3.4 各单元 ④。

#### ②意图与为什么
把 B349 冻结的「缺身份即拒绝」反转为「能证明迟到才拦」：身份缺失/为空时改用 source 列与当前派发快照比对，只在能证明是旧 attempt 时才拦；同时把两处近重复的快照扫描退役、判定正文收在 `internal/client` 一处共享符号，两个消费点（`card wait` stdout 与 agentd 自动化唤醒）执行同一规则。用户在现网被 100% 静默误伤（4289 条镜像 0 条带 `node` 键，836 条挂账仅 13 条能投影身份），而闸护住的确凿迟到是少数；本卡消除这种同罚代价。本卡不承担「旧写入者为何写入偏斜」的侦测（Out of Scope，落 roadmap），也不回放历史镜像。

#### ③验收（T0–T4 内部单元，判据行为化）

**T0：Ticket 0 前置复核（已完成，不重开）**
- 运行 `go build ./...` 退出码 0；`go vet ./internal/client/ ./internal/agentd/ ./cmd/` 退出码 0。（本轮实跑读数见台账 §4。）
- 运行 `go test ./cmd/ -run 'TestB349CardWaitSourceIdentity|TestB349CardWaitSubtreeUsesEventCardIdentity|TestB353CardWait' -count=1` 与 `go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB2336WakeconsumerEnvelopeJSONBoundaries|TestB353AutomationUsesWaitDeliveryPolicy' -count=1` 命中既有夹具并返回 ok（本轮读数：`ok github.com/Xsxdot/handoff/cmd 20.097s`、`ok github.com/Xsxdot/handoff/internal/agentd 2.556s`）。
- 确认 Ticket 0 事实：`internal/client/wakegate.go` 已导出 §3.1 全部符号；两处消费点已调 `client.JudgeMirroredWake`；`cmd/card_wait.go#cardWaitCurrentWorkflowAttempt`、`internal/agentd/wakeconsumer.go#Server.currentWorkflowAttempt` 已无调用方（grep 只有定义）。
- **生命周期/状态机中断：无，因为** T0 只读代码与规格，不创建 goroutine/进程/临时目录。
- **静默失败/误导报错：无新增，因为** T0 不改任何错误路径。
- **跨平台假设：无，因为** 只跑本机 Go 构建与单测。
- **假红/假绿测试：有防线。** 判据命中具体测试名而非 `no tests to run`；Ticket 0 的「身份缺失保保守」是过渡态，不得当成本卡行为已生效。
- **门禁绕过：无，因为** 不新增写/执行入口。
- **序列化边界：无，因为** T0 不新增字段与投影。
- **枚举新值过白名单：无，因为** T0 不新增枚举值。
- **承重安全属性：无，因为** T0 不实现安全属性。
- ④契约引用（见上）；**入口指针**：`internal/client/wakegate.go`、`cmd/card_wait.go#cardWaitEventActionable`、`internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`、`docs/superpowers/specs/b370-contract.md`。

**T1：共享判定激活（`internal/client/wakegate.go` + 新 `wakegate_test.go`）**
- 运行 `go test ./internal/client/ -run 'TestB370|TestCurrentWorkflowAttempt' -count=1` 命中新增直测并返回 ok；测试必须开到真实 `ledger.Open` 的 SQLite（本包测试现无 `internal/ledger` 导入，需新增测试导入）。
- 直测 `CurrentWorkflowAttempt`（契约 §5.2 #20–#24）：只接受 `Node!="" && Attempt!="" && TaskID==Attempt` 的 `EvDispatched`；同 node 多快照取最大 seq；`TaskID!=Attempt` 不作身份；>500 行分页到尾不误报无快照；无合格快照 `found=false`。
- 直测 `JudgeMirroredWake` 三分支（§5.4 #36–#42）：缺 `node`/`attempt` 键、键为空串两种形态分别构造，断言 `Deliver`；身份为空且卡上有合格快照且 source 列**相等**→交付、**不等**→拦截（F1=A）；快照无工作流身份→交付；无快照→交付；拦截 reason=`WakeGateStaleAttempt`；放行 reason 非空。
- 反向断言：身份非空路径（§5.3 #25–#35）保持——旧 attempt、`source_task`/`source_target` 不匹配、缺 `source_task`、`!found` 均 `Deliver=false`；`Target` 两边都空算匹配、一空一非空拦截。
- **生命周期/状态机中断：无，因为** 纯同步函数，只读 ledger；进程重启只重跑普通读。
- **静默失败/误导报错：有防线。** 快照读取/解码错误必须返回带 card/seq/type 的错误，不把「无法判断」吞成放行；`st==nil` 语义见 F4（推荐显式错误）。
- **跨平台假设：无，因为** Go map/字符串比较与 SQLite 读不依赖 OS 路径/进程组。
- **假红/假绿测试：有防线。** 直测断言 `Deliver` 与 `reason` 两个正交量（不能只断 deliver），且同时有「放行」与「拦截」两面；分页用例必须真写 >500 行；断言的是判定语义而非某个私有 switch 写法。
- **门禁绕过：无，因为** 只读账本，不新增写/执行入口。
- **序列化边界：** 事件 payload 的 `node`/`attempt` 用 `*string` 区分「缺键」与「空串」；测试须用 JSON roundtrip（真实 `AppendMirroredEvent` 产出的 envelope 或手写 raw JSON）覆盖：缺键 vs `""` vs `null` vs 非空四态，并断言 `json.Unmarshal` 后 `nil` 与 `&""` 可分辨。source 三列从 `ledger.Event` 列读取，不从 payload 猜。
- **枚举新值过白名单：** `WakeGateReason` 6 个新值流经 `WakeGateDecision.Reason` → 两个消费点日志；逐处确认两个消费点（T2）都能打印任意 reason，无中间白名单挡死。`EvTaskMirrored`/`EvDispatched` 字面值不变。
- **承重安全属性有测试锁住：** 「确凿旧 attempt 不叫醒」与「双空 target 仍叫醒」各需能变红的直测/穿缝测试（B349 已冻）；本单元至少锁住判定层。
- **④入口指针（有界文件集）**：生产 `internal/client/wakegate.go`（`#JudgeMirroredWake`、`#CurrentWorkflowAttempt` + 包内未导出三态/源列取数）；测试 `internal/client/wakegate_test.go`（新建）。**不得**扩到 `internal/ledger` schema、`mirror.go`、`mirrorSkip`、`WaitDeliveryPolicy` 或第二个策略源。

**T2：两消费点接线与旧扫描退役（`cmd/card_wait.go` + `internal/agentd/wakeconsumer.go`）**
- 运行 `go build ./...` 退出码 0；`go vet ./cmd/ ./internal/agentd/` 退出码 0。
- 删除 `cmd/card_wait.go#cardWaitCurrentWorkflowAttempt` 与 `internal/agentd/wakeconsumer.go#Server.currentWorkflowAttempt`；grep 确认全仓无残留定义/引用（当前只有定义与注释，删除安全）。
- 两消费点按 F5（推荐 A）补 `decision.Reason` 日志：`cardWaitEventActionable` 在 `!decision.Deliver` 与放行两路按 reason 落日志；`acceptsCurrentWorkflowAttempt` 翻布尔前按 reason 落日志。日志字段含 card/seq/type/reason，放行与拦截都留痕（§3.2 C）。
- `card wait` 被拦不 Encode；wakeconsumer 被拦仍记 `seen` 并推进游标（既有路径不变）；`--subtree` 仍用事件所属卡 `ev.CardID`（§5.5 #45）。
- `WaitDeliveryPolicy` 仍在身份闸**之后**调用，不复制集合（§5.5 #46）。
- **生命周期/状态机中断：有风险。** 退役旧扫描不改消费循环退出/游标语义；但真实 agentd 重启、ack/游标推进与派发换 attempt 的竞态**未验证，需真机**。
- **静默失败/误导报错：有防线。** 解码失败/缺 `task_type`/`payload` 仍带 card/seq/type 返回错误，不静默降级审计；reason 日志让「降级放行」与「按迟到拦截」都可从日志对质。
- **跨平台假设：有边界风险。** target 字符串比较不依赖 OS，但跨机 target 命名与真实写入属外部现实，**未验证，需真机**。
- **假红/假绿测试：有防线。** 退役后若仍有代码调用旧符号，编译即红；日志/reason 断言不能只断「有日志」而不看 reason 值。
- **门禁绕过：无新增写门，因为** 两消费点只读账本 + 落日志；wakeconsumer 的 `seen`/游标仍是既有 `automationMu` 保护路径。
- **序列化边界：** 消费点把 `ledger.Event`/`proto.LedgerEvent` 投影成 `client.WakeGateEvent`，逐字段核对 `CardID/Seq/Node/Attempt/TaskType/SourceTask/SourceTarget/SourceSeq`；`Node/Attempt` 的指针形态在投影处保留。`--subtree` 参数透传不改事件卡。
- **枚举新值过白名单：** 见 T1；本单元确认两消费点无 switch 丢弃 reason。
- **承重安全属性：** 被拦事件在 wakeconsumer 必须记 `seen` 且消费循环继续（§5.5 #49，B349 既有回归 `TestB2336StaleAttemptDoesNotWake` 保绿）；卡原生事件不走身份闸（§5.5 #50）。
- **④入口指针（有界文件集）**：`cmd/card_wait.go#cardWaitEventActionable`、`cmd/card_wait.go#runCardWait`、`internal/agentd/wakeconsumer.go#Server.acceptsCurrentWorkflowAttempt`、`#Server.consumeAutomationEventsOnce`；读面 `internal/ledger/events.go#Store.EventsFromAsc`、`#DispatchSnapshot`、`internal/ledger/api/api.go#Facade.EventsFromAsc`、`internal/client/delivery.go#WaitDeliveryPolicy`。**不得**改 `mirrorSkip`、镜像写入、WaitDeliveryPolicy 集合、HTTP/前端。

**T3：两消费点穿缝矩阵 + B349 测试重定基线**
- 运行 `go test ./cmd/ -run 'TestB349CardWait|TestB353CardWait' -count=1` 与 `go test ./internal/agentd/ -run 'TestB349AutomationSourceIdentity|TestB2336StaleAttemptDoesNotWake|TestB353Automation' -count=1`，均返回 ok 且命中具体测试名。
- 同一断言矩阵在 `cmd/card_wait_test.go` 与 `internal/agentd/wakeconsumer_test.go` 各跑一遍（穿过 `runCardWait`→`Store.Follow`→`json.Encoder`，与 `consumeAutomationEventsOnce`→keystone/runner）：
  | 情形 | 期望 |
  | --- | --- |
  | 缺 `node`/`attempt` 键（旧写入者形态） | 交付（新） |
  | 键在但为空串（无身份派发） | 交付（新） |
  | 快照无工作流身份 | 交付（新） |
  | 无该 task 派发快照 | 交付（新；反转 B349 的 `!found`） |
  | 身份非空且等于当前派发 | 交付（保留） |
  | 身份非空但为旧 attempt | 拦截（保留，防迟到） |
  | 类型表判假（`permission_auto_allow` 等七类） | 拦截（保留） |
- 重定基线：`cmd/card_wait_test.go#TestB349CardWaitSourceIdentity`、`#TestB349CardWaitSubtreeUsesEventCardIdentity`、`internal/agentd/wakeconsumer_test.go#TestB349AutomationSourceIdentity` 随新规则改写（现夹具中 `Node:"",Attempt:""` 且 source 列等于当前派发的事件将改判为**交付**）；`#TestB2336StaleAttemptDoesNotWake` 保持绿（其 `task-empty` 空身份事件因 source_task 与最新合格快照 attempt-new 不等而拦截）。
- 反向断言逐条变红：旧 attempt 不输出/不唤醒、错 source_task、错 source_target、一空一非空 target、缺 source_task 不终止消费循环；被拦事件仍在账本、后续合法事件仍能消费。
- **生命周期/状态机中断：有风险。** `Store.Follow` 取消/轮询与 agentd 重启的真实收尾**未验证，需真机**；机内锁 attach 暂缓与失败升级反例。
- **静默失败/误导报错：有防线。** 坏 payload 返回带 card/seq/type 的错误；不能把「无法判断」伪装成审计过滤或成功等待。
- **跨平台假设：有边界风险。** JSON/stdout/比较不依赖 OS；Follow 通知、DB 锁、PG LISTEN 行为**未验证，需真机**。
- **假红/假绿测试：有防线。** 测试穿过真实调用方（禁只测 helper）；每个正断言配反面；断言输出行可 `json.Unmarshal` 回原事件、source 三列不丢；夹具必须补匹配的 `RecordDispatch`，不得让「只有 AppendMirroredEvent」的旧夹具伪造当前 attempt（B349 已立的假绿防线）。
- **门禁绕过：有风险。** 快照查询与 Encode/Wake 之间存在派发换 attempt 的观察窗口，非事务锁；真实 TOCTOU 与多 agentd 争用**未验证，需真机**。
- **序列化边界：** 覆盖 `card_events` source 三列 → `ledger.Event` → stdout JSON，与 `ledger.Event` → `eventWire` → `proto.LedgerEvent` → consumer；区分 `source_target` 缺席/空串/非空、`source_seq` 缺席/0/非零；卡原生事件三列零值不伪造。
- **枚举新值过白名单：** 见 T1/T2；矩阵含「类型表判假仍拦截」行，确认新 reason 未绕过 `WaitDeliveryPolicy`。
- **承重安全属性：** 「旧 attempt 隔离」「双空 target 可用」「被拦不停消费循环」各有能变红的穿缝测试。
- **④入口指针（有界文件集）**：`cmd/card_wait_test.go`、`internal/agentd/wakeconsumer_test.go`（可扩既有夹具，不扩成目录级改动）。**不得**新增 wait 命令、HTTP endpoint 或 ledger 派生 API。

**T4：全接缝回归、图/文档门禁与交接**
- 运行 `go test ./internal/client ./internal/ledger ./internal/agentd ./cmd -count=1` 返回 ok；随后 `go test ./... -count=1` 返回 ok（每条新测试必须命中，不能以 `no tests to run` 当绿）。
- 运行 `codegraph --repo . resolve --doc docs/superpowers/specs/b370-breakdown.md` 退出码 0、无坏锚（坏锚先修本稿）；`codegraph --repo . --view cards-B370-charter-4 check` 退出码 0、fails 空（本分支对照判据）。若 F7 选 A，另落实现分支 diff 并更新视图。
- `go build ./...`、`go vet ./internal/client/ ./internal/agentd/ ./cmd/`、`git diff --check` 均退出码 0；生产改动只落 T1–T3 已列集合。
- 复核 §8 known-red：无视图 `codegraph check` 仍报 `dead-contract d_transport→d_ledger`（baseline 未重扫），原文记录，不归因本卡、不绕闸；absorb 后转绿。
- **生命周期/状态机中断：有风险。** 机内锁判定/消费/游标语义；真实 agentd、Keystone、executor、mirror 在重启/断线下的端到端**未验证，需真机**。
- **静默失败/误导报错：有防线。** 失败命令/坏 payload/身份拒绝/图门禁都保留原始上下文；没有「测试全绿即真实唤醒成功」的结论。
- **跨平台假设：有风险。** Go 单测只覆盖当前 OS；真实目标 OS/PG LISTEN/relay**未验证，需真机**。
- **假红/假绿测试：有防线。** 保留旧 attempt、source 不匹配、双空、无快照、subtree 事件卡、缺 source_task 等反面；测试锁的是 `runCardWait`/`consumeAutomationEventsOnce` 的产品行为，不是私有 helper。
- **门禁绕过：有风险。** 本卡无新写路径（只删死代码、加日志）；Wake/Encode 前检查与动作非跨账本事务，TOCTOU 只能真机验。
- **序列化边界：** T1 的 payload 指针四态、T2 的投影、T3 的 stdout/consumer JSON、T0 的 wire 投影四处均有真实边界断言；缺失与零值可分辨。
- **枚举新值过白名单：** 逐处复核最终实现的 `WakeGateReason` 分支与两个消费点日志，确认入口/判定/消费者三处同值。
- **承重安全属性：** 复验 T1/T3 的可红测试仍在，变异复验能红。
- **④入口指针**：T1–T3 文件集并集；图产出 `codegraph/diffs/<implement-branch>.json`；台账 `docs/superpowers/ledgers/2026-09-14-b370-*-ledger.md`（实现轮补）。

#### ④入口指针（子卡并集，有界文件集）
- 生产：`internal/client/wakegate.go`、`cmd/card_wait.go`、`internal/agentd/wakeconsumer.go`。
- 测试：`internal/client/wakegate_test.go`（新）、`cmd/card_wait_test.go`、`internal/agentd/wakeconsumer_test.go`。
- 图：`codegraph/diffs/<implement-branch>.json`（F7 选 A）。
- 只读引用：`internal/ledger/events.go`、`internal/ledger/api/api.go`、`internal/ledger/mirror.go`、`internal/proto/ledger.go`、`internal/agentd/server.go`、`cmd/agentd.go`。

---

## 4. 跨子系统行为闭环核对

只核 spec 承诺的跨子系统可观察行为；每行五格，归属子卡存在。

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属子卡 |
| --- | --- | --- | --- | --- |
| 旧写入者写出的 `task_mirrored`（envelope 缺 `node`/`attempt`），其 source 列等于该卡当前派发 | envelope 缺键（`*string==nil`）+ `card_events.source_task/source_target` + 该卡 `EvDispatched` 快照 | `cardWaitEventActionable` → `JudgeMirroredWake`；`acceptsCurrentWorkflowAttempt` → `JudgeMirroredWake` | `card wait` 输出一行原始事件 / wakeconsumer 产生一次唤醒；reason=`current_attempt` | B370-impl/T1、T3 |
| 旧 attempt 的 `task_mirrored`（身份为空或非空，source 列与当前快照不等） | 同上 | 同上 | 不输出 / 不唤醒；wakeconsumer 记 `seen` 并推进游标；reason=`stale_attempt`；事件留账本 | B370-impl/T1、T2、T3 |
| 身份为空且该卡无合格快照 | `EventsFromAsc` 扫完无合格 `EvDispatched` | 同上 | 交付；reason=`snapshot_not_found`（可观测） | B370-impl/T1、T3 |
| 身份为空且该卡只有无工作流身份的派发快照 | 有 `EvDispatched` 但 `Node==""`/`Attempt==""`/`TaskID!=Attempt` | 同上 | 交付；reason=`snapshot_without_workflow_identity` | B370-impl/T1、T3 |
| 当前身份非空且等于当前派发（含双空 target） | envelope node/attempt + source 三列 + 快照 `Attempt`/`Target` | 同上 | 交付（B349 保留） | B370-impl/T1、T3 |
| 类型表判假的镜像（`permission_auto_allow` 等七类） | `client.WaitDeliveryPolicy` 唯一事实源 | 两消费点在身份闸之后调用 | 身份闸通过但类型表判假 → 不输出/不唤醒；事件留账本 | B370-impl/T3 |
| 卡原生事件（`needs_*`/decision/真人 room message） | `ledger.Event.Type` 与既有 payload | 两消费点的卡原生分支 | 不走身份闸，既有可观察动作不变 | B370-impl/T3 |
| agentd 被拦的镜像事件 | `automationSeen` + `automationCursor` | `consumeAutomationEventsOnce` | 记 `seen`、推进游标、消费循环继续 | B370-impl/T2、T3 |

每行有触发者、权威载体、消费者、结果与归属；未发现只活在接口、测试或无人认领格子的产品承诺。`source_seq` 不是身份事实，不单独造闭环。

---

## 5. 缺陷族对抗审查（逐族正面回答）

**1. 生命周期 / 状态机中断**
- T1 共享函数是纯同步读，不创建 goroutine/进程/临时目录；进程中途重启只重跑普通读，无孤儿资源。
- T2/T3 消费点退役旧扫描不改变消费循环退出、`seen`、游标推进与 `Store.Follow` 取消路径；但**真实 agentd 重启、Keystone attach 变更、executor/mirror 在重启与断线下的收尾未验证，需真机**（见 §6）。
- **无「谁收尾」缺口，因为** 本卡不新增后台生命周期；被拦事件由既有 `automationMu`/游标路径承载。

**2. 静默失败 / 误导报错**
- 每条错误路径的传播契约：快照读/解码错误 → `JudgeMirroredWake` 返回带 card/seq/type 的错误 → 消费点带 ctx 上抛，不静默降级审计；解码失败/缺 `task_type`/`payload` 仍报错。
- 存在「报成功但没做」的新窗口？**无，因为** 本卡不新增写入事实；`card wait` 仍只在 `Encode` 成功后以默认模式退出；wakeconsumer 被拦仍记 seen。
- reason 可观测让「降级放行」与「按迟到拦截」都可能从日志对质（§3.2 C）；这是本卡对「不知道它哑了」的直接修复。

**3. 跨平台假设**
- payload 键缺失/空串、source 列比较、JSON/stdout 不依赖 OS 路径/进程组/权限模型/webview。
- **有边界风险：** 跨机 `target` 命名、真实旧写入者版本、网络/relay 行为属外部现实，**未验证，需真机**；不以本机 SQLite 单测外推。

**4. 假红 / 假绿测试**
- T1 直测断 `Deliver`+`reason` 两面；T3 穿过真实调用方（`runCardWait`→`Store.Follow`→encoder，`consumeAutomationEventsOnce`→keystone/runner），禁只测 helper。
- 每个正断言配反面（旧 attempt、source 不匹配、双空、无快照、缺 source_task、类型假集合）；分页用例真写 >500 行；`Node/Attempt` 指针四态 JSON roundtrip。
- 夹具里的行为假设对应真机项：旧写入者缺键形态、空串形态、真实多节点同卡并发 → §6。
- 测试锁的是调用方可观察行为（stdout 行、Wake/processed、seen/游标），不是私有 switch 写法；换实现不改需求不会无意义转红。
- **未验证，需真机：** 真实负载/并发下 `Store.Follow` seq 顺序、同卡合并、seen/cursor 不重复。

**5. 门禁绕过**
- 新增写路径/执行路径？**无，因为** 本卡只删死代码、改判定与加日志；不新增 CLI/HTTP/共享表入口，不新增 executor 调用。
- 门覆盖全部表面？两处消费点是全仓唯一身份闸消费点（spec 备注：除这两处外无第三个），同一规则经同一共享符号，不产生「通道分裂」。
- 检查与动作之间有窗口吗：快照查询与 Encode/Wake 非事务，**真实并发下门是否仍关着未验证，需真机**。
- 权限门：本卡不触碰权限/审批门，`WaitDeliveryPolicy` 集合不复制。

**6. 序列化边界**
- 新增字段？**无**（本卡不加 wire 字段）；但必须核对手写投影链：`card_events` 列 → `ledger.Event` → `eventWire` → `proto.LedgerEvent` → `WakeGateEvent` → `JudgeMirroredWake`；`ledger.Event` → stdout JSON；envelope JSON 的 `node`/`attempt` → `*string`。
- 每条链路有真实边界回归：缺键 vs `""` vs `null` vs 非空；`source_target` 缺席/空串/非空；`source_seq` 缺席/0/非零；卡原生零值不伪造 source 键。推荐对 envelope 做 roundtrip 属性测试（已有 `FuzzB2336WakeconsumerEnvelopeJSONRoundTrip` 可扩）。
- 「两端各自有测试」≠「这条链路有测试」：T3 必须有一条穿过真实消费调用方的矩阵回归。

**7. 枚举新值过既有白名单**
- 新引入 `WakeGateReason` 6 个取值，流经：`WakeGateDecision.Reason` → `cardWaitEventActionable` 日志分支、`acceptsCurrentWorkflowAttempt` 日志分支。逐处确认两个消费点都按任意 reason 打印，无中间 switch/白名单挡死。
- 既有白名单核对：`client.WaitDeliveryPolicy` 七项假集合、`mirrorSkip` 三项、agentd 卡原生 event switch、`proto.EventType` 词表——本卡不改其集合，身份闸在类型表之前，顺序不承重。
- `EvTaskMirrored`/`EvDispatched` 字面值不变。

**8. 承重安全属性有测试锁住**
- 「确凿旧 attempt 不叫醒」（防迟到）、「双空 target 仍叫醒」、「被拦不停消费循环」是承重属性，各有能变红的直测（T1）与穿缝测试（T3）；`TestB2336StaleAttemptDoesNotWake` 必须保绿。
- 一次性 token/唯一性/隔离：**不命中**，因为本卡不新增 token、ticket、session、权限凭据或唯一性约束。

**9. webview / 平台表现差异候选族**
- **无，因为** 本卡不触及 `d_web`、Wails、Chromium、cookie、剪贴板、拖放或浏览器 API；CLI stdout 是文本协议，不用 webview fixture 冒充。

---

## 6. 真机清单（全部「未验证，需真机」，归协调者执行）

1. 现网旧写入者（如 mac-02 旧构建）真实产出的 `task_mirrored` envelope 形态：缺键 vs 空串；确认降级放行分支实际命中，`card wait --follow` 在 Claude Code/grok Monitor 上真能逐行唤醒。
2. 确凿旧 attempt 的真实迟到事件：确认 `card wait` 无 stdout、agentd 不唤醒、事件留账本且 reason 日志可查。
3. 同卡多节点/多 workflow 并发的真实账本流：确认 F1 的定位键语义不误杀合法事件、也不放行旧 attempt。
4. 本机 target 为空的真实派发与镜像：确认双空 target 仍唤醒/输出，空串不当缺身份。
5. `--subtree` 场景下事件子卡与 wait 根卡不同的真实流：确认按事件子卡查快照。
6. 真实 agentd 装配（`cmd/agentd.go#setupLedger` 先 `SetLedger` 后 `SetupAutomation`）与重启：确认 `s.ledger` 非 nil、消费循环被拦后 seen/游标推进、重启不重放已叫人事件。
7. 跨机 `target` 命名与 relay/断线：确认 source 列比较语义在真实命名下仍成立。
8. 历史 4289 条无 `node` 键镜像：本卡**不回溯唤醒**（消费从不回放）；确认真实消费从新事件起按新规则行为，旧账本可查。
9. 真实并发派发换 attempt 与 attach 变更：确认「读快照后动作」的 TOCTOU 结果符合产品接受语义。

---

## 7. 图覆盖债与文档自检指针

- `codegraph sym` 实测（本轮）：`JudgeMirroredWake`、`CurrentWorkflowAttempt`、`WakeGateEvent`、`WakeGateReason`、`WakeGateDecision` 均**不在基线图**（`内部符号不在图中`）；`cardWaitCurrentWorkflowAttempt`、`Server.currentWorkflowAttempt` 在基线图（`d_cli`/`d_gateway`），实现轮删除后需分支重扫反映 `nodesDeleted`。这属**图覆盖债**，不当作代码缺失。
- 图中可用锚：`n_cmd_cardWaitEventActionable`、`n_agentd_Server_acceptsCurrentWorkflowAttempt`、`n_agentd_Server_consumeAutomationEventsOnce`（moved）、`n_ledger_Store_EventsFromAsc`、`m_ledger_DispatchSnapshot`、`n_client_WaitDeliveryPolicy`、`n_agentd_Server_SetupAutomation`。
- 本稿不新增 `codegraph/diffs/<branch>.json`：breakdown 不改生产符号；实现轮的图产出落点见 F7（推荐 A）。
- 本稿推荐 `file#Symbol` 符号锚；收口前亲跑 `codegraph resolve --doc docs/superpowers/specs/b370-breakdown.md`（结果落台账）。

---

## 8. 出稿自检（逐项核对）

1. **产出四样齐全**：§1 子系统清单每带类型（best `type` 直取）、§2 契约增量逐组有结论、§3 子卡四段式且判据行为化、§5 缺陷族逐族含「无，因为……」。✔
2. **待拍板岔口集中**：F1–F7 全部列在 §0，正文不散落未标岔口。✔
3. **「未验证，需真机」汇总**：§6 真机清单 9 条。✔
4. **每张子卡有界文件集核过**：§3.1 各 T ④ 列出并核对；圈不出者已插竖切债结论（§1.2：无，能圈出）。✔
5. **行为闭环每行五格完整**：§4 每行有触发者/载体/消费者/结果/归属，归属子卡存在。✔
6. **契约状态位核对**：spec 已批准、contract 已冻结，从文件头读出（§2.1）。✔
7. **边界澄清回写**：三态定位属包内实现面 → 已落 `b370-contract.md` §12。✔
8. **未亲自跑到结果的命令未写成结论**：本轮实跑命令与原始输出在台账；未跑的（如全量 `go test ./...`、实现轮测试）明确标为待实现/待验。✔

---

## 9. 拍板记录区（留空，待协调者回填）

协调者拍板后，此处逐条回写 §0 F1–F7 的裁决与理由，并把头部状态行改为「已拍板（日期）」，与裁决同批提交；状态行与裁决记录不一致视同未拍板。
