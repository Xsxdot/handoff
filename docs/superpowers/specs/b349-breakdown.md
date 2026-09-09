# B349+B352 消费端 source identity 闸与自动化水位拆解提案

状态：**出稿待拍板**（2026-09-09；本稿由 handoff executor 出稿）
卡：B349（B352 并入；不另开实现卡）
定级：**L3 轻档**；路由：contract → breakdown → 单轮 implement → review → acceptance → finish
有效基线：cards/B233.1-charter-7 @ f2129a25（当前分支不切换、不越过）
上游 spec：docs/superpowers/specs/b349.md（头部：已批准）
冻结 contract：docs/superpowers/specs/b349-contract.md（头部：冻结状态为本提交冻结）
本稿台账：docs/superpowers/ledgers/2026-09-09-b349-breakdown-ledger.md
图依据：codegraph/best.json；父为空的顶层领域以该文件为准
角色边界：本文是提案；不写实现代码、不建卡、不派发、不调用 handoff CLI。实际扇出与拍板归协调者。

## 0. 待拍板岔口清单（集中）

本稿没有新增待拍板岔口。以下选项已由用户补充或上游冻结，列出是为了防止
implement 把它们重新解释成实现偏好：

| 编号 | 已冻结事项 | 本稿采用的边界 |
| --- | --- | --- |
| F1 | L3 轻档是否扇出 | 不扇出外部子卡；T0–T4 是同一轮 implement 的内部有序单元，接缝由一个上下文连续验收。 |
| F2 | B352 是否独立拆解 | 不独立；自动化 cursor 与 B349 身份闸同一份拆解、同一轮实现。 |
| F3 | 当前 attempt 规则归属 | 闸在 wakeconsumer 与 card wait 两个消费点；ledger 只提供既有事件读面，不揽当前 attempt 派生查询。 |
| F4 | source 身份字段 | 当前身份是事件所属卡、节点的最新合格 EvDispatched 快照的 Attempt + Target；强制 TaskID == Attempt；SourceSeq 不参与身份比较。 |
| F5 | target 空值 | 当前快照 target 与镜像 source_target 同为空时通过；一空一非空拒绝。空 target 不是缺身份。 |
| F6 | 自动化水位 | 写本机 agentd DataDir/automation-cursor.json；SetupAutomation 读回；内存 cursor 先更新再 Save；至少一次，不复用 room/wait cursor，不写共享账本。 |

若协调者要改动 F1–F6 任一项，应先退回 spec/contract 对应冻结物；本稿不自行
吸收产品或契约分叉。

## 1. 触及子系统清单与派卡资格核

best.json 当前父为空的顶层领域共 15 个；本卡实际触及下列 5 个。类型直接取
best.json：logic 为机内可闭环，boundary 只验契约形状，外部行为列入真机清单。

| 子系统 id | 类型 | 本卡有界文件集 | 触及理由 |
| --- | --- | --- | --- |
| d_protocol | 逻辑型 | 已完成的 internal/proto/ledger.go、internal/proto/contract_fixture_test.go | Ticket 0 的 LedgerEvent source 三列是唯一 wire DTO；本轮不再新增字段。 |
| d_ledger | 逻辑型 | 只读入口 internal/ledger/events.go#Store.EventsFromAsc、#DispatchSnapshot、internal/ledger/api/api.go#Facade.EventsFromAsc、internal/ledger/follow.go#Store.Follow；Ticket 0 投影回归 internal/ledger/api/api_test.go | 提供既有事件/派发快照事实；不新增 ledger 派生 API，不把当前 attempt 判定下沉。 |
| d_orchestration | 逻辑型 | internal/agentd/wakeconsumer.go、internal/agentd/server.go（cursor 组装跨 Server/agentd fn 两容器）、internal/agentd/scheddrain.go、新 internal/agentd/automation_cursor.go；对应 internal/agentd/wakeconsumer_test.go、必要的 server_test.go | 自动化消费、身份闸、cursor 读回/落盘和成功/失败推进均在此闭环。 |
| d_cli | 逻辑型 | cmd/card_wait.go、cmd/card_wait_test.go | card wait 在既有 Store.Follow 回调上执行同一道身份闸并决定 stdout。 |
| d_gateway | 边界型 | 已完成的 internal/agentd/ledgerapi.go、internal/agentd/ledgerapi_test.go；internal/agentd/server.go#Server.SetupAutomation 为装配接缝 | HTTP 卡详情的 source 投影是 Ticket 0 前置；Server/DataDir 是 agentd 外部边界，机内只验 wire/装配形状，真实重启与权限见真机清单。 |

### 1.1 相邻但不派实现文件的领域

d_transport（boundary）的 client.WaitDeliveryPolicy 是唯一任务类型事实源，
本卡只在闸通过后调用，不改其集合；d_keystone（logic）的 Service.Decide、
WakeEvent 与真实 resume/launch 是既有消费结果，不改其规则；d_web 不改 TS
类型和交互，HTTP JSON 的新增可选键由旧客户端忽略。它们不新增子卡，也不扩大
本卡文件集。真实 transport/Keystone/浏览器行为均不由机内夹具推断。

### 1.2 四条派卡资格逐项核

| 子系统 | 1. 有界文件集 | 2. 契约面可枚举 | 3. 依赖 DAG 无环 | 4. 类型 | 结论 |
| --- | --- | --- | --- | --- | --- |
| d_protocol | Ticket 0 文件与回归已点名；本轮零新增生产文件 | source 三列、omitempty、零值兼容 | 仅被 d_ledger/d_orchestration/d_gateway 使用 | 逻辑型 | 通过，但作为已完成前置，不再单独扇出 |
| d_ledger | 只读入口与既有测试已点名；禁止新增派生接口 | EventsFromAsc、DispatchSnapshot、Facade 投影 | U0 提供事实，消费点向下读，无回边 | 逻辑型 | 通过；不派独立 ledger 卡 |
| d_orchestration | wakeconsumer、Server 装配、cursor 文件和对应测试可圈定 | contract §§2.2、2.4 的身份与水位原子断言 | U0 → T1/T3 → T4，keystone 仍是既有出站口 | 逻辑型 | 通过，主实现面 |
| d_cli | card wait 生产/测试两文件可圈定 | contract §2.3、原子项 #31–#38 | U0/ledger 事实 → T2 → T4，无新事件总线 | 逻辑型 | 通过，不拆卡 |
| d_gateway | 投影 Ticket 0 与 SetupAutomation 装配点可圈定 | contract §2.1、§2.4 的 wire/装配约束 | d_gateway 只装配/暴露，消费逻辑仍在 d_orchestration | 边界型 | 通过；机内验形状，真机验实际重启/文件权限 |

### 1.3 竖切债核对

internal/agentd 是大包，但本稿不把整包作为上下文：身份闸只圈
wakeconsumer.go，cursor 只圈 server.go 的字段/SetupAutomation 与新文件，
自动化循环入口只圈 scheddrain.go 的既有调用关系；HTTP 投影已在 Ticket 0。
因此没有“圈不出文件集”的触点，不插竖切还债卡。实现若需要改动上述集合之外的
生产文件，必须退回协调者重新核边界，不能以“同目录”放宽。

## 2. 契约增量核对

### 2.1 上游状态与接缝结论

- spec 文件头已写 状态：已批准（2026-09-09，用户原话「推进吧」）；本稿不以
  会话记忆替代该状态。
- contract 文件头已写上游 spec 已批准，并写明本提交冻结；本稿把它作为已冻结物，
  不在 breakdown 里偷偷改字段、事件、命令、HTTP 路径或依赖方向。
- 本稿只使用 contract 已有接缝：wire DTO/两处投影、两处消费闸、既有派发快照
  读面和 DataDir 装配点；没有发现新接缝，**不退回 contract**。
- 边界澄清没有新增：d_gateway 的 HTTP 投影已由 Ticket 0 落地，d_web 不改
  类型是 contract 已冻结的兼容边界；本稿不另写一条只活在稿内的澄清。

### 2.2 冻结条目逐条归属

| 冻结项 | 归属单元 | 越界核对 |
| --- | --- | --- |
| #1 LedgerEvent.SourceTarget string | T0 前置 | 已由 Ticket 0 提供；不新增第二 source 字段。 |
| #2 LedgerEvent.SourceTask string | T0 前置 | 已由 Ticket 0 提供；不复制进 envelope。 |
| #3 LedgerEvent.SourceSeq int64 | T0 前置 | 已由 Ticket 0 提供；只作为源序号保留。 |
| #4 source_target + omitempty | T0 前置 | 只保留既有 JSON tag，不改键名/零值语义。 |
| #5 source_task + omitempty | T0 前置 | 缺失与空字符串在投影测试中区分；不从 payload 补值。 |
| #6 source_seq + omitempty | T0 前置 | 缺失与 0 在投影测试中区分；不拿它做 attempt 身份。 |
| #7–#9 Facade 镜像三列直通 | T0 前置 | Facade.EventsFromAsc 逐事件走 eventWire，不扩读路径。 |
| #10–#12 HTTP 卡详情三列直通 | T0 前置 | GET /api/cards/{id} 继续走 ledgerEventWire，不新 endpoint。 |
| #13–#14 卡原生事件零值 | T0 前置 | ""/""/0 保持；不把卡原生事件套 source 闸。 |
| #15 零值 JSON 不出 source 键 | T0 前置 | 由 omitempty 与真实 JSON 回归锁住。 |
| #16 source 不进 envelope | T1/T2 | 消费点只读 wire/ledger 列；不改 AppendMirroredEvent payload。 |
| #17–#19 当前快照合格条件/最新 seq/TaskID 等于 Attempt | T1/T2 | 各消费点按事件所属卡、同 node 分页到尾；坏快照仅审计，不算当前。 |
| #20–#21 envelope node/attempt 非空 | T1/T2 | 缺失、null、空字符串闭集拒绝；保持生产 decoder 错误路径。 |
| #22 envelope attempt 等于 snapshot.Attempt | T1/T2 | 不是只做 envelope 自洽比较。 |
| #23 SourceTask == snapshot.Attempt | T1/T2 | source 列与 envelope 必须指向同一当前 task。 |
| #24 SourceTarget == snapshot.Target | T1/T2 | 同空通过，一空一非空拒绝。 |
| #25 双空 target 通过 | T1/T2 | 正例必须穿过真实消费调用方。 |
| #26 一空一非空拒绝 | T1/T2 | 反例必须同时断言不 wake / 不 stdout。 |
| #27 SourceSeq 不参与匹配 | T1/T2 | source seq 与源序号不一致仍可按身份通过；去重仍归 Store。 |
| #28 无当前快照不 wake | T1/T2 | 不回退到 B353 类型表。 |
| #29 旧 attempt 不 wake | T1/T2 | 事件留账本、消费见过但不生成 Wake/stdout。 |
| #30 缺 source_task 不 wake 且循环继续 | T1 | 返回 false,nil 语义；后续事件仍可消费。 |
| #31 事件所属卡取快照 | T2 | ev.CardID 是查询卡；不是 wait 根卡。 |
| #32 --subtree 不替换事件卡 | T2 | 根卡只给成员集合，子卡事件仍按子卡读快照。 |
| #33 先身份闸后 WaitDeliveryPolicy | T1/T2 | 不复制类型集合，不让无身份事件进入策略判断。 |
| #34 卡原生事件绕过 source 闸 | T1/T2 | needs_*、decision、真人 room message 保持旧动作语义。 |
| #35 EvTaskMirrored 字面值不变 | T4 | 不新增/改名事件类型。 |
| #36 EvDispatched 字面值不变 | T4 | 只读取既有派发快照。 |
| #37 不新增 wait 命令 | T2 | 只改既有 card wait 分类，不新增 CLI surface。 |
| #38 不新增 HTTP endpoint | T0/T3 | 投影沿既有卡详情；cursor 为内部 DataDir 文件。 |
| #39 cursor 路径 | T3 | 只用 DataDir/automation-cursor.json。 |
| #40–#41 SetupAutomation 读回、StartAutomation 不读回 | T3 | 装配与循环职责不互换。 |
| #42 文件不存在起点 0 | T3 | 首次运行不把 MAX(seq) 当起点。 |
| #43 内存先于 Save | T3 | 失败可重放，禁止先落盘再唤醒。 |
| #44 attach 暂缓不推进 | T3 | 沿现有 early return，不写 cursor 文件。 |
| #45 automationSeen 不落盘 | T3 | 文件只含 Seq，不把内存去重表变成第二状态源。 |
| #46 同 DataDir 新 Server 读回 | T3 | 验收必须新建 Server、调用 SetupAutomation 后再消费。 |
| #47 cursor 后新事件仍可消费 | T3 | 新事件 seq 大于已保存水位时产生一次 wake。 |
| #48 Save 前崩溃允许重复 wake（至少一次） | T3 | 用 Save 失败/重启模型锁住不丢事件，不声称恰好一次。 |
| #49 不写共享 ledger | T3 | ledger 仍只存产品事件，不存 agentd 消费进度。 |
| #50 不复用协调者 wait cursor | T3 | 不读 $HOME/.handoff/cursors、room-cursors.json。 |

**结论：**没有新接缝、没有需退回 contract 的字段或 API。若 implement 需要新增
事件类型、HTTP 路径、ledger 派生接口、共享 cursor 表、TS 交互或第二套任务类型
策略，必须先退回 contract，不能在本节点边做边加。

## 3. 行为闭环核对

| 触发者 | 权威事实/载体 | 消费者 | 可观察结果 | 归属单元 |
| --- | --- | --- | --- | --- |
| 当前 workflow task 产生 task_mirrored | 事件所属卡的 EvDispatched 快照 + envelope node/attempt + source_task/source_target | Server.consumeAutomationEventsOnce → acceptsCurrentWorkflowAttempt → automationWakeEvent | 当前身份且类型可动作时同卡合并为一次 Wake；旧/错/无快照只留账本 | T1 |
| 主会话 card wait 收到镜像事件 | 同一事件所属卡的最新合格派发快照；--subtree 仅提供成员集合 | runCardWait → cardWaitEventActionable | 当前可动作事件编码一行 JSON；旧/错/无快照不写 stdout | T2 |
| 账本 Facade/HTTP 读取镜像事件 | ledger.Event 的三列 source identity | eventWire、ledgerEventWire 与真实 Facade/HTTP JSON | source 三列保留；卡原生零值与旧 JSON 金样本不变 | T0 |
| agentd 完成一轮消费或失败推进 | 本机 automationCursor 与 DataDir/automation-cursor.json | SetupAutomation 读回 + consumeAutomationEventsOnce | 重启后从已保存 seq 续拉；Save 前中断允许至少一次重复 wake，不丢新事件 | T3 |
| 自动化消费遇到 B353 审计类型 | 唯一 client.WaitDeliveryPolicy | 两个消费点在身份闸之后调用 | 审计事件不 wake/stdout；可动作事件仍按当前身份通过 | T1/T2 |
| 卡原生 needs/decision/真人房间事件到达 | ledger.Event.Type 与既有 payload | wakeconsumer/card wait 的卡原生分支 | 不因没有 source 三列被过滤；既有可观察动作保持 | T1/T2 |

每行都有触发者、权威载体、消费者、结果与归属单元；未发现只活在接口、测试或
无人认领格子的产品承诺。source_seq 不是身份事实，因此只作为“身份其余条件成立
时仍通过”的回归行为，不另造产品闭环。

## 4. 单轮实现内部单元与依赖 DAG（不扇出）

DAG（T0–T4 不是可独立派发子卡）：

Ticket 0：已冻结并已落地的 wire/投影前置（543f47c4）
→ T1 wakeconsumer 身份闸与真实消费回归
→ T2 card wait 同闸、事件所属卡与 subtree 回归
→ T3 automation cursor 装配、持久化与重启模型
→ T4 全接缝回归、图锚/文档门禁与真机清单交接

实现者必须在同一轮中保留这条顺序。T1/T2 不能各自发明身份规则，T3 不能把
cursor 逻辑塞进 ledger，T4 不能用 helper 单测替代生产调用方行为。

### T0：Ticket 0 wire 与投影前置复核（已完成，不重开）

#### ①契约引用

- contract §§1.1、2.1、3 #1–#16、§6.1。
- internal/proto/ledger.go#LedgerEvent、internal/ledger/api/api.go#eventWire、
  internal/ledger/api/api.go#Facade.EventsFromAsc、internal/agentd/ledgerapi.go#ledgerEventWire、
  卡详情 HTTP 投影。

#### ②意图与为什么

确认消费端能看到 source 三列，且 source 仍是账本列的唯一权威；不因本轮身份闸
再造 DTO、不把 source 塞回 envelope、不让 omitempty 改写既有零值金样本。该单元
属于冻结前置，不是 implement 新任务；当前 HEAD 已包含 Ticket 0。

#### ③验收

- go test ./internal/ledger/api -run '^TestFacadeEventsFromAscPreservesSourceIdentity$' -count=1
  命中真实 Facade 读面并返回 ok；断言非空 source 三列、卡原生零值、缺席与
  显式空/零值可区分。
- go test ./internal/agentd -run '^TestCardDetailProjectsMirroredSourceIdentity$' -count=1
  命中真实 GET /api/cards/{id} 投影并返回 ok；断言 HTTP events[] 三列。
- go test ./internal/proto -count=1 返回 ok；零值 LedgerEvent 的 JSON 不出
  三个 source 键，且 source 不出现在 task_mirrored envelope。
- **生命周期/状态机中断：无，因为** T0 只编码/投影数据，不创建 goroutine、task、
  临时目录或 cursor；进程重启只重新执行普通 JSON 读写。
- **静默失败/误导报错：有防线。** Facade/HTTP 测试必须穿过真实编码入口；投影
  失败返回原始错误，不能只断言 helper 返回值或把丢列报成成功。
- **跨平台假设：无，因为** Go JSON 键和 HTTP 响应形状不依赖路径/进程组；不同
  agentd/代理/浏览器实际网络行为仍未验证，需真机。
- **假红/假绿测试：有防线。** 真实 Facade 与 HTTP 回归、缺失/空/零反断言会使
  “只改 struct 未接两处投影”变红；不能把本前置测试当作消费闸已生效。
- **门禁绕过：无，因为** T0 只读账本并编码，不新增写/执行入口，也不改变权限门。
- **序列化边界：** ledger.Event → eventWire → proto.LedgerEvent → Facade/HTTP JSON
  是必须穿过的链路；card wait 的 json.Encoder(ledger.Event)归 T2，不能由 T0
  代替。无新增枚举；无 token/唯一性/隔离承重安全属性，因为 T0 不产生安全状态。

#### ④入口指针（有界文件集）

internal/proto/ledger.go#LedgerEvent；internal/ledger/api/api.go#Facade.EventsFromAsc、
#eventWire；internal/agentd/ledgerapi.go#ledgerEventWire、卡详情 handler；
internal/ledger/api/api_test.go#TestFacadeEventsFromAscPreservesSourceIdentity；
internal/agentd/ledgerapi_test.go#TestCardDetailProjectsMirroredSourceIdentity。

### T1：wakeconsumer 当前派发身份闸

#### ①契约引用

- contract §2.2、原子项 #16–#30、#33–#36、§6.2。
- internal/agentd/wakeconsumer.go#Server.currentWorkflowAttempt、
  #Server.acceptsCurrentWorkflowAttempt、#automationWakeEvent、
  #Server.consumeAutomationEventsOnce。
- 既有事实入口：internal/ledger/api/api.go#Facade.EventsFromAsc、
  internal/ledger/events.go#Store.EventsFromAsc、#DispatchSnapshot、
  internal/client/delivery.go#WaitDeliveryPolicy。

#### ②意图与为什么

让自动化消费者把“这个镜像是否属于当前节点派发”与“这个任务类型是否可动作”
保持为两道闸：前者比较事件所属卡最新 EvDispatched 的 Attempt + Target、
envelope node/attempt、wire SourceTask/SourceTarget；后者仍唯一调用
WaitDeliveryPolicy。旧 attempt、错 target、source task 不一致、无当前快照和缺
source task 的事件可以审计但不能叫醒；ledger 不拥有这条派生判断。

#### ③验收

- 运行 go test ./internal/agentd -run 'Test(B349|B2336StaleAttempt|B353Automation)' -count=1；
  命令必须命中新增/迁移后的调用方测试并返回 ok，不得以 no tests to run 通过。
- 真实 Facade.EventsFromAsc → consumeAutomationEventsOnce 链路构造同卡同 node 的
  当前快照：TaskID == Attempt、target 与 source target 同为空、source task 等于
  Attempt、可动作 task type；结果是一次 Wake，且同卡事件仍只合并一次。
- 反向断言逐条变红：旧 attempt、错 source_task、错 source_target、一空一非空、
  无当前快照、envelope node/attempt 缺失或为空均不 Wake；事件仍在 ledger，后续
  合法事件仍能消费；缺 source task 不终止消费循环。
- 当前快照 TaskID != Attempt 时不得作为身份；同 node 多条 EvDispatched 必须
  取最大事件 seq 的合格快照；扫描超过 500 行仍到达分页尾，不能误报无快照。
- source seq 与源序号不一致时，只要其它身份条件成立仍 Wake；该反例锁住“误把
  source 三元组全等当当前 attempt”。WaitDeliveryPolicy 必须在身份闸通过后调用，
  audit 类型不 Wake，真实可动作类型仍 Wake。
- malformed envelope 仍返回含 card/seq/type 的可行动错误；合法但不匹配的事件
  返回 false,nil、标记 seen、消费轮继续，不把拒绝误报成唤醒成功。
- **生命周期/状态机中断：有风险。** 本单元不改变 Keystone 的启动/恢复归属；
  消费进程在扫描与 Wake 之间重启/派发换 attempt 的实际竞态未验证，需真机。cursor
  推进与 Save 由 T3 验收，不能在 T1 以“内存 seen”宣称跨重启不重复。
- **静默失败/误导报错：有防线。** malformed 是错误并带上下文；缺 source task、
  旧 attempt、错 target 是审计跳过并有结构化日志；不能返回成功而没有 Wake，也不能
  把缺快照回退成 B353 类型表。
- **跨平台假设：有边界风险。** target 字符串比较本身不依赖路径，但跨机 target 命名、
  mirror 真实写入和执行器事件产生属外部现实，未验证，需真机。
- **假红/假绿测试：有防线。** 测试必须穿过 consumeAutomationEventsOnce，有旧 attempt、
  source 列不一致、双空 target、source_seq 不参与和无快照反面；只测
  acceptsCurrentWorkflowAttempt 私有返回值不算通过。高并发派发切换与真实 Keystone
  唤醒未验证，需真机。
- **门禁绕过：无新增写门，因为** T1 只读派发快照和 source 列，失败不删账本、不写
  卡状态；但扫描后到 Wake 前不是事务锁，检查/动作 TOCTOU 的真实结果未验证，需真机。
- **序列化边界：** ledger.Event → Facade → proto.LedgerEvent、envelope JSON、
  DispatchSnapshot JSON、source 三列都必须有断言；缺失与空值分开。无新增枚举，
  EvTaskMirrored/EvDispatched 字面值保持；承重安全属性“旧 attempt 隔离、双空
  target 可用、source task 必须一致”各有能变红测试。

#### ④入口指针（有界文件集）

生产：internal/agentd/wakeconsumer.go#Server.currentWorkflowAttempt、
#Server.acceptsCurrentWorkflowAttempt、#automationWakeEvent、
#Server.consumeAutomationEventsOnce。读面：internal/ledger/events.go#Store.EventsFromAsc、
#DispatchSnapshot、internal/ledger/api/api.go#Facade.EventsFromAsc。
测试：internal/agentd/wakeconsumer_test.go 现有 B233.6/B353 consumer seam 与新增
B349 正反例。目标生产改动不得扩到 ledger schema、mirrorSkip、keystone 规则或
WaitDeliveryPolicy。

### T2：card wait 同一道身份闸

#### ①契约引用

- contract §2.3、原子项 #20–#27、#31–#38、§6.2。
- cmd/card_wait.go#runCardWait、#cardWaitEventActionable、
  internal/ledger/follow.go#Store.Follow、internal/ledger/events.go#Store.EventsFromAsc。

#### ②意图与为什么

使主会话 stdout 与自动化 Wake 使用同一身份规则：task_mirrored 先由事件所属卡
和 envelope node 查询最新派发快照，再比 Attempt/Target/SourceTask/SourceTarget，
通过后才问 WaitDeliveryPolicy。--subtree 只决定 Follow 的成员集，不把根卡
冒充事件卡；卡原生 needs/decision/真人 room message 继续按既有规则动作。

#### ③验收

- 运行 go test ./cmd -run 'Test(B349|B353)CardWait' -count=1，命中新增与迁移
  夹具并返回 ok。所有可动作 task_mirrored 夹具先写一条匹配的 RecordDispatch；
  不能让旧的“只有 AppendMirroredEvent”夹具继续伪造当前 attempt。
- 通过 runCardWait 真实 Follow/Encoder 断言：当前 attempt + 可动作类型输出一行
  原始 ledger.Event JSON；旧 attempt、错 source task/target、无当前快照、plain/缺
  node/attempt 的镜像不输出；source 三列不被 stdout 编码丢失。
- 用事件所属子卡与根卡不同的 --subtree 夹具断言：子卡事件按子卡 EvDispatched
  查身份，根卡快照不能放行或拒绝它；一空一非空 target 拒绝、双空 target 放行；
  source_seq 不一致但其它身份匹配仍输出。
- B353 回归仍成立：permission_auto_allow、permission_reuse 等策略假事件不
  输出，delivery_failed 等可动作事件在身份匹配后输出；卡原生 needs/decision/真人
  room message 不因无 source 列被过滤。损坏 payload 返回带 card/seq/type 的错误而不是
  静默跳过。
- **生命周期/状态机中断：有风险。** Store.Follow 的取消、轮询/通知和 follow idle
  goroutine 沿既有路径收尾；本卡不创建第二条订阅。PG LISTEN、SQLite WAL、CLI 进程
  被杀后实际收尾和输出重复行为未验证，需真机。
- **静默失败/误导报错：有防线。** 身份不匹配是明确不输出且保留 ledger；payload
  解码/快照读取错误必须返回错误上下文，不能把“无法判断”伪装成审计过滤或成功等待。
- **跨平台假设：有边界风险。** stdout JSON 与 source 比较不依赖 OS；Follow 的
  通知/轮询、文件/数据库锁和远程账本行为未验证，需真机。
- **假红/假绿测试：有防线。** 必须从真实 runCardWait 经过 Store.Follow 到
  json.Encoder，并断言正反输出行、事件所属卡和 subtree；只测分类 helper，或不补
  EvDispatched 的旧夹具，均判失败。并发写入与真实通知时序未验证，需真机。
- **门禁绕过：无新增执行/写门，因为** card wait 只读账本并写 stdout，不改变卡/task；
  但快照查询与 Encode 间存在派发换 attempt 的观察窗口，不能把单测结果表述成原子
  绑定，真实 TOCTOU 未验证，需真机。
- **序列化边界：** 覆盖 card_events source 列 → ledger.Event → stdout JSON，
  并区分 source_target 缺席/空串/非空、source_seq 缺席/0/非零。无新增枚举；承重
  属性“旧 attempt 不打 stdout、当前 attempt 打 stdout、双空 target 可动作”必须各有
  能变红的调用方测试。

#### ④入口指针（有界文件集）

生产：cmd/card_wait.go#runCardWait、#cardWaitEventActionable；读面：
internal/ledger/follow.go#Store.Follow、internal/ledger/events.go#Store.EventsFromAsc、
#DispatchSnapshot、internal/client/delivery.go#WaitDeliveryPolicy。测试：
cmd/card_wait_test.go 现有 B353 follow/错误/终态测试及新增事件所属卡、source identity、
双空 target、无快照正反例。不得新增 wait 命令、HTTP endpoint 或 ledger 派生 API。

### T3：本机自动化 cursor 装配与至少一次水位

#### ①契约引用

- contract §2.4、原子项 #39–#50、§6.2–§6.3。
- internal/agentd/server.go#Server.SetupAutomation、
  internal/agentd/scheddrain.go#Server.StartAutomation、
  internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce。
- 既有同类介质：DataDir/room-cursors.json；但 owner、文件名和失败语义不得复用。

#### ②意图与为什么

把自动化消费进度从纯内存变成当前 agentd 的私有、可恢复事实：装配时读回，消费轮
在现有“真正推进内存 cursor”的成功/失败出口之后保存；attach 暂缓、读错、解码错、
尚未推进的路径不保存。Save 前崩溃允许同批事件再次唤醒，保证至少一次；共享 ledger、
协调者 wait cursor、room cursor 与 automationSeen 都不承载这份状态。

#### ③验收

- 运行 go test ./internal/agentd -run 'Test(B349|AutomationCursor)' -count=1；命中
  新增持久化/重启/失败时序测试并返回 ok。
- 在临时 DataDir 先以新 Server + SetupAutomation 消费一轮，再创建新 Server、同一
  DataDir、再次 SetupAutomation 并直接调用 consumeAutomationEventsOnce：已保存 seq
  之前的事件不再次 Wake；随后追加 seq 更大的可动作事件，该事件可 Wake。文件不存在
  时起点为 0，不能用账本 MAX(seq) 跳过事件。
- 读取真实 automation-cursor.json 断言 JSON 只有 seq，父目录/文件权限沿 contract
  为 0700/0600；断言既不修改 room-cursors.json，也不写 $HOME/.handoff/cursors
  或共享 ledger。
- 注入 Save 失败或等价的持久化失败：内存 cursor 已按 contract 先更新，消费轮返回
  非 nil 错误，不能报成功且伪称已保存；新进程/再次消费仍可重试未落盘事件。attach
  暂缓时文件水位不前移，解除 attach 后同一事件仍可 Wake。
- 文件损坏/非 JSON/读取权限错误必须留下可行动日志并按 contract 以 0 启动；不把
  错误吞成“已有水位”。automationSeen 不出现在文件，重启后的重复行为以 cursor
  与至少一次语义解释，不宣称恰好一次。
- **生命周期/状态机中断：有风险且是本单元主验收。** agentd 重启从文件续拉，Save
  前中断允许重唤醒，attach early return 不推进；真实 SIGKILL、DataDir 锁竞争、磁盘
  写一半后的恢复需真机验证，不能由临时目录单测外推。
- **静默失败/误导报错：有防线。** Load/JSON/权限错误记日志且从 0 开始；Save 错误
  使消费轮失败并保留重试机会，不能把“内存更新”报成“持久化成功”，也不能以 MAX(seq)
  静默丢事件。
- **跨平台假设：有边界风险。** 路径必须由 filepath.Join(DataDir, ...) 形成；tmp+
  rename 的跨平台原子性受 OS 限制，contract 已要求不宣称绝对原子。Windows/Unix
  权限、rename 替换和真实重启均未验证，需真机。
- **假红/假绿测试：有防线。** 测试必须读真实文件、创建新 Server 后再消费，并有
  Save 失败、attach 暂缓、新 seq、文件不存在/损坏反面；只断言 s.automationCursor
  内存值会对“重启恢复”假绿。真实进程崩溃与多 agentd 争用未验证，需真机。
- **门禁绕过：有写路径但无用户执行面。** 新文件只能由 agentd 内部组装点和既有
  automationMu 保护的消费路径写入，不能新增 CLI/HTTP/共享表入口；同 DataDir 多进程
  写竞争依赖既有 DataDir lock，检查/rename 的竞态未验证，需真机。
- **序列化边界：** automationCursorDisk{Seq} encode/decode、缺文件、损坏 JSON、
  Seq:0 与存在性必须有断言；不把 seen map 序列化。无新增事件枚举；承重安全属性
  “至少一次、不因先落盘而丢事件、attach 不推进”各有可变红测试。

#### ④入口指针（有界文件集）

新文件：internal/agentd/automation_cursor.go。装配/生命周期：
internal/agentd/server.go#Server.SetupAutomation、internal/agentd/scheddrain.go#Server.StartAutomation。
消费接线：internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce。
测试：internal/agentd/wakeconsumer_test.go、必要的 internal/agentd/server_test.go。
不改 internal/collab/cursor、internal/ledger schema、协调者 cursor 或 mirror watermark。

### T4：全接缝回归、图锚与交接门禁

#### ①契约引用

- contract §§3–7 全部冻结项，尤其 §6.2 的四组穿缝测试与本稿 T0–T3 的归属。
- 本稿行为闭环表、真机清单和图覆盖债。

#### ②意图与为什么

在单轮实现结束时确认 Ticket 0、两个消费闸和 cursor 没有各自变绿却在接缝处漂移：
真实事件列、envelope、派发快照、策略表、stdout、DataDir 文件必须共享同一份冻结语义。
T4 只做交接所需的回归和门禁，不借机新增功能、端点、枚举或第二实现路径。

#### ③验收

- go test ./internal/proto ./internal/ledger/api ./internal/agentd ./cmd -count=1 返回
  ok；随后 go test ./... -count=1 返回 ok。每个新增测试必须命中，不能把
  no tests to run 当绿。
- go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b349-breakdown.md
  返回退出码 0；所有 file#Symbol 锚为 ok/moved，无坏锚。codegraph validate
  的原始结果必须记录；codegraph check 若仍报告基线既有违规，原文记录，不归因于
  B349，也不把失败改写成通过。
- git diff --check 与法定文件集合检查返回 0；生产改动只落在 T0–T3 已列集合，
  出现未声明生产文件即退回拆解，不以同目录放宽。
- **生命周期/状态机中断：有风险。** 机内测试锁 cursor/attach/取消/失败语义；真实
  agentd、Keystone、executor、mirror 在重启和网络断线下的端到端事实未验证，需真机。
- **静默失败/误导报错：有防线。** 失败命令、坏 payload、身份拒绝、Save 失败和
  HTTP/Facade 投影都必须保留原始上下文；没有“测试全绿即真实唤醒成功”的结论。
- **跨平台假设：有风险。** Go 单测覆盖当前 OS 的 JSON/文件/SQLite 行为；真实目标
  OS、PG LISTEN、relay、executor webview、权限模型未验证，需真机。
- **假红/假绿测试：有防线。** 既有测试之外必须保留旧 attempt、source mismatch、
  双空、无快照、subtree 事件所属卡、Save 失败和新 Server 读回等反面；测试锁的是
  consumeAutomationEventsOnce/runCardWait 的产品行为，不是私有 helper。
- **门禁绕过：有风险。** 本卡新写路径只有内部 cursor；Wake/Encode 前身份检查与
  动作不是跨账本事务，TOCTOU 和多 agentd 争用只能真机验证。不得新增 CLI/HTTP/ledger
  旁路以绕过既有权限或数据所有者。
- **序列化边界：** T0 的 Go Facade/HTTP、T1 的 envelope/source/快照、T2 的 stdout、
  T3 的 cursor JSON 四处都必须有真实边界断言；缺失与零值必须可区分。无新枚举；旧
  EvTaskMirrored/EvDispatched 白名单逐处保持；旧 attempt 隔离、至少一次和双空
  target 是承重安全属性，均需变异复验可红。

#### ④入口指针（有界文件集）

T0–T3 已列文件的并集；文档门禁为本文件、对应台账和上游 spec/contract 的只读核对。
不新增 production 文件集合之外的改动，不修改上游冻结文件。

## 5. 真机清单（所有条目均为“未验证，需真机”）

1. 在真实目标 agentd/执行器产生 question、permission、delivery_failed、completed、
   failed 等事件，验证 source_task/source_target 与当前派发快照真实一致时分别让
   wakeconsumer 和 card wait --follow 产生预期动作；旧 attempt、错机器和缺 source
   task 只留账本。
2. 覆盖本机 target 为空的真实派发与镜像；验证双空 target 仍唤醒/输出，不能把空串
   当成缺身份。
3. 覆盖事件卡为 subtree 子卡、wait 根卡不同的真实账本流；验证快照查询不串卡。
4. 在 agentd 重启前后分别安排“已消费”“尚未消费”“Save 前中断”“attach 暂缓”窗口，
   验证 DataDir cursor 续拉、至少一次和不丢新事件；检查实际文件权限、损坏文件和
   磁盘空间错误的用户可行动日志。
5. 覆盖 SQLite 轮询与 PG LISTEN、直连与 relay、协调者进程重启/网络断线；验证不
   因通知延迟、游标重连或源事件重复而错误 wake/stdout。
6. 覆盖真实 Keystone attach 占用与 Wake 竞态、并发派发换 attempt、多个 agentd/目标
   机器的 target 命名；确认机内“读快照后动作”的 TOCTOU 结果符合产品接受语义。
7. 在支持的 Unix/Windows 目标验证 filepath.Join、tmp+rename、0700/0600 和 agentd
   DataDir lock；不得把当前 Unix 单测结果外推成跨平台原子性。
8. 验证真实 executor/外部服务的事件生产链，不以 fake transport、fake Keystone 或
   夹具生成的 card_events 证明“事件确实来自目标 task”。

## 6. 图覆盖债与文档自检指针

- best.json 容器映射已核：k_proto_model→d_protocol、k_ledger_Store 与
  k_ledger_api_Facade→d_ledger、k_agentd_fn→d_orchestration、k_agentd_Server→d_gateway、
  k_cmd_fn→d_cli。
- 图中可用锚：m_proto_LedgerEvent、n_agentd_ledgerEventWire、
  n_ledger_Store_EventsFromAsc、n_ledger_Store_Follow；currentWorkflowAttempt、
  acceptsCurrentWorkflowAttempt、eventWire、WaitDeliveryPolicy、
  cardWaitEventActionable 等私有/未覆盖符号以源码查证，不把图缺席当作代码缺席。
- 本稿推荐符号锚；codegraph resolve --doc 必须在收口前亲跑。坏锚先修本稿，不用
  行号漂移掩盖入口变更。
- 本稿不新增 codegraph/diffs/<branch>.json：contract 已冻结“Ticket 0 无新生产
  符号视图 diff”，身份与 cursor 符号属于 implement 期间的源码覆盖债；若实现阶段
  真增跨域生产边，按目标图流程另行处理，不能在本拆解稿伪造图已更新。

## 7. 出稿自检

### 7.1 可解析符号锚

本稿使用的生产入口锚点：

- `internal/proto/ledger.go#LedgerEvent`
- `internal/ledger/api/api.go#Facade.EventsFromAsc`
- `internal/ledger/api/api.go#eventWire`
- `internal/ledger/events.go#Store.EventsFromAsc`
- `internal/ledger/events.go#DispatchSnapshot`
- `internal/ledger/follow.go#Store.Follow`
- internal/agentd/wakeconsumer.go:54-92（图未覆盖的私有 currentWorkflowAttempt，按源码行段查证）
- internal/agentd/wakeconsumer.go:97-130（图未覆盖的私有 acceptsCurrentWorkflowAttempt，按源码行段查证）
- `internal/agentd/wakeconsumer.go#automationWakeEvent`
- `internal/agentd/wakeconsumer.go#Server.consumeAutomationEventsOnce`
- `internal/agentd/server.go#Server.SetupAutomation`
- `internal/agentd/scheddrain.go#Server.StartAutomation`
- `cmd/card_wait.go#runCardWait`
- `cmd/card_wait.go#cardWaitEventActionable`
- `internal/client/delivery.go#WaitDeliveryPolicy`

图未覆盖的私有符号以源码为准；若 resolve 对其中任一锚报 vanished，先修正锚，
不得把图覆盖债误写成实现缺失。

1. 子系统清单已按 best 顶层领域标类型，且每个实现面有界；L3 不扇出，T0–T4 是
   单轮内部顺序。
2. spec/contract 状态已从文件头核对；契约 #1–#50 逐条归属，没有新接缝，未改上游。
3. 行为闭环每行五格完整，T1/T2/T3/T4 均有归属；没有只活在接口或 helper 的产品承诺。
4. 每个内部单元均含契约引用、意图、行为化验收、入口指针；缺陷族逐族回答，并把
   “无，因为……”与“未验证，需真机”分开。
5. 序列化、无新枚举、承重安全属性均显式核对；旧 B353 夹具缺派发快照已列为假绿
   防线与实现验收，不作为当前行为事实。
6. 收口前必须亲跑并记录 codegraph resolve --doc、git diff --check、文件状态和
   法定测试命令；没有实际输出的命令不得写成已通过。
