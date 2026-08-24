# B185 拆解提案：`card dispatch --step` 入驻 agentd

日期：2026-08-24

节点：`charter:breakdown` · B185 · L3「轻档」

上游：[spec](2026-08-23-b185-step-runner-in-agentd.md)（状态：已批准）

冻结物：[contract](b185-contract.md)（状态：已冻结，本轮不得改动）

## 0. 形态声明与待拍板清单

本稿由 handoff executor 出稿，协调者负责拍板；本稿不写实现代码、不建卡、不派发。
B185 是 L3 轻档，**不扇出子卡**：下面的 U0–U4 是同一轮 implement 的有序改动单元，
不是可独立派发的远程任务。顺序是给下一轮 plan 的作业顺序和判断点。

以下岔口不在本稿自行裁决：

| 编号 | 岔口 | 方案 | 出稿者建议（仍待拍板） |
|---|---|---|---|
| P1 | CLI 如何提交本机 agentd | (a) 给既有 `internal/client/client.go#Client` 增加 card-step 方法，复用 Bearer、请求错误和唯一拨号器；(b) 在 `cmd` 直接写 HTTP | **(a)**。`internal/client` 已是 agentd 唯一拨号方；(b) 会复制鉴权/错误处理并制造第二条拨号路径。若选 (b)，须先重核目标图与契约边界。 |
| P2 | 202 成功后的 CLI 输出中“任务标识”具体指什么 | (a) 不改冻结响应 `{"ok":true}`，CLI 输出已知的卡号+节点名并提示 `card wait`；(b) 扩大响应以返回 task id | **(a)**。当前环节是多轮编排，agentd 受理时未必有单一 task id；(b) 会越出冻结 contract，必须退回 contract。 |
| P3 | “要求内联本地文件”的拒绝如何在当前无 `plan_path` wire 上落地 | (a) 严格解码，`plan_path` 等未知字段直接 400；(b) 宽松解码但只在未来显式字段出现时拒绝 | **(a)**。当前 `CardStepReq` 没有本地文件字段，严格解码可避免调用方误以为文件已被 agentd 带过去；若选 (b)，必须另补可测的拒绝入口，不能让 implement 守卫退化成节点名白名单。 |

P1–P3 由协调者裁决后，按下文推荐顺序进入同一轮；未裁决前不应把建议解释为新的冻结签名。

## 1. 触及子系统清单与派卡资格核

子系统 id/type 以 `codegraph/target.json` 的 `subsystems` 为准；项目实例化清单
`2026-08-21-handoff-instantiation-checklist.md` §2 给出同一类型判定。虽然本仓当前图的
字段名已是 `subsystems`，纪律中的“domains 数组”语义在本图上仍按该清单解释。

| 子系统 | 类型 | 下一轮计划触及的有界文件集 | 触及方式 |
|---|---|---|---|
| `d_contract` 契约域 | 逻辑型 | `internal/proto/` 的新 `CardStepReq` 类型文件/现有类型入口、`internal/proto/contract_fixture_test.go`、`web/src/api/testdata/CardStepReq.json` | 新增 Go↔TS 请求固定件；不改既有类型语义 |
| `d_controlplane` 控制面 API 域 | 逻辑型 | `internal/agentd/ledgerapi.go`、`internal/agentd/cardstep.go`、既有 `internal/agentd/ledgerapi_test.go`、`internal/agentd/cardstep_test.go` | 解码/规范化、守卫、异步装配、四个覆盖项和 actor 投影 |
| `d_cli` CLI 域 | 逻辑型 | `cmd/card_node.go`、必要的 `cmd/card_dispatch.go` 装配处、既有 `cmd/card_dispatch_test.go`/`cmd/card_node_test.go` | `--step` 从同步 runner 改为本机 202 提交；保留 flag 语义 |
| `d_remote` 远端连接域 | 边界型 | `internal/client/client.go`、既有 `internal/client/client_test.go` | 复用既有 agentd HTTP 客户端，新增 step 端点的线格式方法；真实 agentd/网络行为须真机验收 |
| `d_web` Web 控制台域 | 逻辑型（本次无 UI 行为改动） | `web/src/api/ledger.ts`、`web/src/api/ledger.test.ts`、`web/src/api/contract.test.ts`、新固定件 | 增加请求类型镜像；`runCardStep` 继续发送 legacy `{step}` |

本轮拆解文档实际只新增本文件和台账；以上是下一轮实现的目标文件集，不是本轮实现变更。
`d_ledger` 不列为改动域：B203/B214 的 `StepRunner` 字段和优先级规则已存在，本卡只把
请求值装配进去并复用它们。

### 1.1 四条派卡资格逐项核

| 子系统 | 1. 有界文件集 | 2. 契约面可枚举 | 3. 依赖 DAG 无环 | 4. 类型标注 | 结论 |
|---|---|---|---|---|---|
| `d_contract` | Go 类型/fixture/TS 镜像，均能列出 | `CardStepReq` 六字段、omitempty、actor 必填、legacy 例外 | U0 是所有消费者前置，不依赖消费者 | 逻辑型 | 四条通过，可作为 U0 |
| `d_controlplane` | handler + cardstep 两个生产文件和两处既有测试 | `POST /api/cards/{id}/step`、规范化 actor、StepRunner 四项投影、implement 守卫 | U0 类型 → U2 handler；不反向依赖 CLI | 逻辑型，HTTP 是接缝 | 四条通过；虽命中 agentd“单包≥40文件”盲区，但本次只碰两个既有文件，不插竖切还债卡 |
| `d_cli` | `card_node`/dispatch 测试和既有 root endpoint | CLI flags → `CardStepReq` → 既有本机 endpoint | U1 客户端 + U2 服务端 → U3 CLI | 逻辑型 | 四条通过，可作为 U3 |
| `d_remote` | 既有 `Client` 与 client 测试一处 API | 方法、路径、JSON、202/error body | U0 类型 → U1 client；不新增回调或拨号器 | 边界型 | 四条通过；机内只验 HTTP 形状，目标机/真实服务见真机清单 |
| `d_web` | 既有 ledger API/contract 测试和一个 fixture | TS 请求镜像、legacy `{step}` 形状 | U0 生成 fixture 后同步；不依赖 UI | 逻辑型 | 四条通过；不改看板调用签名 |

`internal/agentd` 的大包竖切判据已显式回答：本次能圈出 handler/cardstep 两个文件族，
没有把无关 agentd 文件塞入功能单元；若实现中需要再动其它 agentd 家族，必须回到
协调者重新审查文件边界。

## 2. 契约增量核对

上游状态位已写在文件头：spec 为“已批准”，contract 为“契约冻结”。本次拆解不引用
会话记忆中的状态，不改两个上游文件。

| contract 冻结物 | 计划如何使用 | 越界结论 |
|---|---|---|
| 端点 `POST /api/cards/{id}/step` 与 `Content-Type: application/json` | U1 通过既有 `Client` 发往原端点；U2 保持 handler 路由 | 不越界，不新增 URL |
| 唯一请求类型 `internal/proto.CardStepReq` | U0 新增精确六字段：`step`、`target`、`executor`、`model`、`extra`、`actor` | 不越界，不另造 CLI/agentd DTO 作为 wire 类型 |
| `step` 必填、非空，节点由卡钉工作流查找 | U2 仍交给 `StepRunner.nodeFor`，不恢复 `review|merge` 白名单 | 不越界 |
| `target`/`executor`/`model`/`extra` 可选、空值语义冻结 | U3 逐字段填请求；U2 一一投影到既有 `StepRunner`，不在 handler 重算优先级 | 不越界 |
| CLI > 节点覆盖 > 模板；executor 实际改变时空 model 切断下层 model | U2 只装配 `StepRunner`；U4 复跑既有 B203 runner/dispatch 测试 | 不越界，不复制第三处规则引擎 |
| `actor` 规范请求必填且非空；CLI 为 `ledgerSession()` | U3 使用 `cmd/ledgercli.go#ledgerSession`；U2 规范化后同时写 `Session` 与 `Dispatcher.Actor` | 不越界，不改为 `ledgerActor` 或 agentd 身份 |
| legacy 看板原始 `{step}` 允许缺 actor，由 agentd 补 `web:<r.RemoteAddr>` | U2 只对 actor 键缺席走 fallback；显式 `actor:""` 仍拒绝 | 不越界；看板 `runCardStep` 不改 |
| implement 守卫主语是“是否要求内联调用方本地文件” | U2 删除按节点名硬拒；当前 `CardStepReq` 不带 `plan_path`，对未知本地文件字段按 P3 处理 | 不越界；不新增 `plan_path` |
| `startCardStep` 将 actor 同时落 `StepRunner.Session`/`Dispatcher.Actor`；覆盖项入同一 runner | U2 保留同一 actor 和四项覆盖投影；不引入 TTL/心跳 | 不越界 |
| 成功响应 HTTP 202、body `{"ok":true}` | U1 只把 202+ok 当受理成功；U3 立即返回，不读回合终态 | 不越界，不改变 `card wait` |
| 固定件由 Go 生成，`-update` 才能重写，Web 侧逐字节镜像 | U0 只走既有 `TestContractFixtures` 机制；不手写 JSON 代替生成 | 不越界 |

### 2.1 既有接缝与边界澄清

复用的完整链路是：

```text
CLI flags + ledgerSession
        │
        ▼
既有 internal/client Client（本机 agentd）
        │ POST /api/cards/{id}/step
        ▼
handleCardStep → startCardStep → StepRunner
        │
        ▼
既有 stepTransport → client.Dispatch → 目标机任务
```

这不是新增业务接缝：端点、`StepRunner`、`stepTransport`、`Client` 拨号器均已存在；
新增的只是同一端点请求的冻结字段和 CLI 使用该端点的路径。`PlanPath` 仍属于不带
`--step` 的 CLI 直派路径，绝不能经过上述 wire；这也是 implement 守卫与 U3 `--plan`
拒绝测试的边界。

以下手写投影必须列入实现文件/测试清单：

1. `cmd/card_node.go`：flag 值 → `proto.CardStepReq`；actor 必须来自 `ledgerSession`。
2. `internal/client/client.go`：`CardStepReq` → JSON 请求体/URL。
3. `internal/agentd/ledgerapi.go`：JSON → `CardStepReq`，并区分 actor 键缺席和空字符串。
4. `internal/agentd/cardstep.go`：规范化请求 → `StepRunner` 的 `Session`、四项覆盖和
   `Dispatcher.Actor`。
5. `internal/ledgerstep/runner.go`/`dispatch.go`：既有 runner → `TemplateDispatch` /
   `DispatchOpts`，只复用，不新增一份优先级逻辑。
6. `internal/proto/contract_fixture_test.go` → `web/src/api/testdata/CardStepReq.json`
   → `web/src/api/contract.test.ts` 的 TS 镜像。

本次没有需要回写 contract 的新边界澄清：legacy fallback、`PlanPath` 不上 wire、
actor 双落点与目标图入口均已在 contract §§1–4/目标图冻结。若 P1 选 direct HTTP、或
P2 要返回新 task id，则不属于本拆解可自行吸收的澄清，必须退回 contract。

### 2.2 契约疑点

spec 用户故事 1 要求命令“打印任务标识”，但 contract 的成功响应冻结为
`{"ok":true}`，而一个节点回合可能产生多个 task，受理时也未必已有唯一 task id。
本稿按 P2 建议把“任务标识”解释为已知的“卡号 + 节点名”，并把 `card wait <卡>` 作为
进展入口；若协调者认为必须打印 task id，应先修订并重新冻结 contract，不能在 U3 私自
加响应字段。

## 3. 有序改动单元与依赖 DAG

```text
contract/spec 状态已落文件
             │
             ▼
U0  CardStepReq + Go/Web 固定件
             │
       ┌─────┴─────┐
       ▼           ▼
U1 client step   U2 agentd 解码/装配/守卫
       └─────┬─────┘
             ▼
U3 CLI --step 改为本机 202 提交
             │
             ▼
U4 既有接缝回归 + CHANGELOG + 全量门禁
             │
             ▼
协调者真机清单 → review / acceptance
```

### U0 · 落 `CardStepReq` 与固定件（`d_contract` + `d_web`）

**①契约引用**

- contract §1：精确六字段、`omitempty`、actor 必填规范。
- contract §4：固定样本必须六键非空，唯一用 `-update` 生成，Web 侧同步类型/测试。
- contract 目标图：`d_cli → d_contract`、`d_controlplane → d_contract` 的
  `proto.CardStepReq`。

**②意图与为什么**

先把跨进程形状钉死，后续 handler/client/CLI 都以同一个 Go 类型为准。样本同时填满
六个键，防止 `omitempty` 或键名漂移在两端各自绿；Web 只增加请求镜像，不让 legacy
看板为了 fixture 被迫发送 actor。

**③验收**

- 实现后运行 `go test ./internal/proto/ -run TestContractFixtures -update`，命令返回
  `ok`，并只生成 `web/src/api/testdata/CardStepReq.json` 的预期快照；随后运行
  `go test ./internal/proto/ -run TestContractFixtures -count=1`，命令返回 `ok`。
- `CardStepReq.json` 逐字节含 `step`、`target`、`executor`、`model`、`extra`、`actor`；
  空可选字段的缺席语义不由 fixture 样本伪造，交给 handler/client 反面测试。
- 在 `web` 运行 `npm run test -- src/api/contract.test.ts src/api/ledger.test.ts` 和
  `npm run typecheck`，均返回 0；`ledger.test.ts` 仍断言 `runCardStep` 的 body 恰为
  `{ step: "..." }`，没有 actor。
- 缺陷族结论：序列化边界由真实 Go marshal + fixture + TS import 穿过；假红/假绿由
  逐字节和 legacy body 反断言锁住。无生命周期/权限/跨平台行为，因为 U0 是纯类型和
  固定件；无新增枚举，因为 `CardStepReq` 只含字符串字段；无承重安全属性，因为它
  不产生凭据/租约，只为后续 actor 测试提供形状。

**④入口指针（有界文件集）**

- `internal/proto/`（新增类型放在现有纯类型入口，按仓内文件组织落位）；
- `internal/proto/contract_fixture_test.go#TestContractFixtures`；
- `web/src/api/ledger.ts#runCardStep`、`web/src/api/ledger.test.ts`；
- `web/src/api/contract.test.ts`、`web/src/api/testdata/CardStepReq.json`。

### U1 · 复用既有 client 拨号器承载 step 请求（`d_remote`）

**①契约引用**

- contract §1、§2.1：请求体和 actor 原文逐字传递。
- contract §1：202 + `{"ok":true}` 成功；非 2xx 错误必须保留 agentd body。
- P1：推荐在既有 `internal/client/client.go#Client` 增加方法，不在 `cmd` 另写 HTTP。

**②意图与为什么**

把 `CardStepReq` 通过已有 `Client.do`、Bearer 和 `httpError` 送到本机端点；客户端方法
只负责传输，不决定节点合法性、优先级、actor fallback 或等待终态。这样本机 agentd
不可用时 CLI 能得到可行动的连接失败，且没有“本机 agentd 不可用就回落本地 runner”的
第二宿主。

**③验收**（边界型：机内只验契约形状，真实服务行为归真机）

- 实现后运行 `go test ./internal/client -run 'TestCardStep' -count=1`；命令必须命中
  至少一条用例并返回 `ok`，不能是 `no tests to run`。
- httptest handler 收到 `POST /api/cards/B185/step`、Bearer 和 JSON；全字段样本中
  六个键和值逐字匹配，actor 为带 PID 的 CLI 字符串，不得被 client 改成 `web:`。
- 可选字段为空时，测试使用 `map[string]json.RawMessage` 或 `value, ok` 检查键缺席，
  不把“字段缺失”和“值为零”混在一起；actor/step 的非空校验由 U2 负责。
- 202 且 body `{"ok":true}` 返回 nil；400/409/连接失败返回带原始 status/body 或
  `ErrUnreachable` 的错误，绝不能把拒绝报成成功。
- 缺陷族结论：生命周期无，因为 client 不创建 task/worktree，只受 context 控制；静默
  失败由 status/body 断言锁住；跨平台行为（真实本机 agentd 地址、代理/网络）未验证，
  需真机；假绿由真实 `httptest` HTTP 序列化而非直接调用 helper 避免；门禁无，因为
  client 只复用既有 Bearer，不新增权限入口；序列化边界覆盖 `CardStepReq → do`；无新增
  枚举；无新增 token/唯一性属性，因为 actor 唯一性由 U3/U2 负责。

**④入口指针（有界文件集）**

- `internal/client/client.go#Client.Dispatch`（沿用同一 `do`/错误处理模式）；
- `internal/client/client.go`（新增 step 方法，不另造 HTTP client）；
- `internal/client/client_test.go`（沿用 `newTestClientEnv`/httptest 基座）。

### U2 · agentd 解码、守卫与异步装配（`d_controlplane`）

**①契约引用**

- contract §1：`CardStepReq`、step/actor 非空与 legacy actor fallback。
- contract §2：`cli:<user>@<hostname>#<pid>` 原文同时落 `Session`/`Dispatcher.Actor`。
- contract §3：守卫主语是内联本地文件，不是 implement；当前 step 请求恒无
  `PlanPath`。
- contract §1/§2：四个一次性覆盖项进入同一 `StepRunner`，202 与同卡在飞 409 保持。

**②意图与为什么**

让 handler 先把“规范请求”和“legacy 看板请求”归一成一个内部请求，再启动既有
`startCardStep` goroutine。规范 actor 不能为空，legacy 只在原始 JSON 缺 actor 时补
`web:<r.RemoteAddr>`；显式空 actor 不能悄悄变成 web 身份。`StepRunner` 仍是唯一编排
真相源，覆盖项交给既有 `dispatchNode`/`ViaTemplate`，不在 agentd 复制 B203 规则。

implement 只要没有内联文件就放行；`--step --plan`/`plan_path` 不得被当作已成功携带
到 agentd。按 P3 建议，未知字段拒绝能把本地文件请求挡在解码层；若采用宽松解码，必须
补等价的、能变红的拒绝判据，不能只删旧的 implement 分支。

**③验收**

- 实现后运行（测试名可按落地命名，但不得跳过这些行为）
  `go test ./internal/agentd -run 'TestCardStep(Returns202|SecondReturns409|AcceptsImplementWithoutInlineFile|LegacyActorFallback|RejectsEmptyActor|RejectsInlineLocalFile|PropagatesRequestFields)' -count=1`；命令命中用例且包返回 `ok`。
- `{"step":"implement","actor":"cli:u@h#123"}` 返回 202 并启动后台 runner；不再因节点名 400。
  带 `plan_path` 或等价内联本地文件请求返回 400，且没有启动 runner；这条反面断言在
  删除文件守卫/改成无条件放行时必须变红。
- `{"step":"review"}`（actor 键缺席）返回 202，捕获到的 runner 的
  `Session`/`Dispatcher.Actor` 均为 `web:<RemoteAddr>`；`{"step":"review","actor":""}`
  返回 400。缺 step、空 step 也返回 400，不进入 `startCardStep`。
- 规范请求同时带非空 `target`/`executor`/`model`/`extra` 时，测试捕获同一个
  `StepRunner`，确认四个值一一到位；actor 不被覆盖。随后运行既有 B203 回归：
  `go test ./internal/ledgerstep -run 'Test(ViaTemplateExecutorModelOverridesAndPairRule|ViaTemplateSameExecutorKeepsTemplateModel|RunnerExecutorModelOverridePriorityAndPairRule|RunnerSameExecutorKeepsNodeModel)$' -count=1`，返回 `ok`，不改这些断言。
- 同卡在飞仍为 409，后台结束后槽位释放；`TestCardStepReturns202` 证明 HTTP 不等待
  回合终态。失败写入既有事件/日志链，不在 HTTP 202 时伪报环节已成功。
- 缺陷族结论：
  - 生命周期/状态机中断：在飞槽位沿用 `claimCardStep`/defer release；本卡不实现 agentd
    重启后的回合恢复，孤儿回合的真实处置属于 B225，需真机清单明确未验证。
  - 静默失败/误导报错：空 step/actor、未知节点、在飞冲突和内联文件均返回可行动
    4xx；202 只表示受理，不写成功终态。错误体必须保留既有节点/占用者信息。
  - 跨平台假设：`web:<RemoteAddr>` 的真实地址格式、agentd 重启/网络行为和目标机 executor
    皆是外部现实，未验证，需真机；本单元不拼路径、不碰 webview。
  - 假红/假绿：测试捕获真实 goroutine 传入的 `StepRunner`，同时断言否定分支“未启动”；
    不把伪造 runner 或单看 HTTP 202 当成字段落地证明。
  - 门禁绕过：路由仍走既有 `withLedger`/Bearer 门；本地文件请求在执行前拒绝，不能
    借 implement 节点名或未知字段绕过；无新增 TOCTOU 写路径，因为本单元不读本地 plan。
  - 序列化边界：真实 handler 解码覆盖 legacy 缺席、显式空值和六字段非空样本；投影到
    runner 另有捕获断言。
  - 枚举白名单：无，因为没有新增状态/事件/kind；节点仍按卡钉工作流查找，implement
    放行不是新增节点枚举。
  - 承重安全属性：`Session == Dispatcher.Actor` 且保留同卡单飞是本次承重属性，捕获测试
    可因只改其中一处而变红；无一次性 token 新属性，因为本卡不产生 token。
  - webview/平台表现差异：无，因为不改浏览器 API 或看板调用；legacy body 的机内 HTTP
    形状仍由 U0/U4 锁住。

**④入口指针（有界文件集）**

- `internal/agentd/ledgerapi.go#handleCardStep`；
- `internal/agentd/cardstep.go#Server.startCardStep`、`internal/agentd/cardstep.go#Server.stepTransport`；
- `internal/agentd/ledgerapi_test.go`（现有 202/409/implement/node-name 用例迁移与补强）；
- `internal/agentd/cardstep_test.go`（现有槽位/释放测试和 runner 捕获）。

### U3 · CLI `--step` 改为本机 agentd 202 提交（`d_cli`）

**①契约引用**

- spec「方案」「实现决定」：CLI 不再本地编排，无本机 agentd fallback，202 后立刻返回。
- contract §2：actor 使用 `cmd/ledgercli.go#ledgerSession`，不能用 `ledgerActor`。
- contract §1：四项覆盖逐字段上 wire；看板兼容不是 CLI 发送 actor 的理由。
- P2：成功输出不能私自扩充冻结响应；P3：`--plan` 不能随 step 进入 inline-file 路径。

**②意图与为什么**

把 `cmd/card_node.go#runStepDispatch` 从同步 `StepRunner.Run` 改成调用既有本机 endpoint
的短请求：本机 agentd 成为唯一编排宿主，CLI 进程退出不带走后台回合。`--target` 在
这里仍是**请求中的一次性目标覆盖**，不是把请求拨到远端；提交端点固定是本机 agentd。
`--executor`、`--model`、`--extra` 原样进入请求，`--plan` 与 `--step` 组合在本机发送前
拒绝，避免用户以为本地文件已被 agentd 接管。成功输出采用协调者对 P2 的裁决；推荐只
打印卡号/节点和 `handoff card wait <卡>`，不输出旧 `Outcome`。

**③验收**

- 实现后运行 `go test ./cmd -run 'TestCardDispatch(StepSubmitsToLocalAgentd|StepReturnsImmediately|StepCarriesOverrides|StepUsesPIDActor|StepRejectsPlan|StepNoLocalFallback)$' -count=1`；命中用例且返回 `ok`。
- 用 `httptest` 配置为本机 `TargetEndpoint`，收到的 URL 必须是本机 `/api/cards/{id}/step`；
  body 的 target/executor/model/extra 与 flags 一致，actor 格式含当前 PID；即使请求的
  target 是 `mac-02`，拨号端仍是本机 agentd。
- server 立即回 202 时，CLI 在短请求返回，不调用本地 `StepRunner.Run`、不读取
  `Outcome`、不跟随事件流；stdout 按 P2 裁决包含稳定的卡/节点标识和 `card wait` 入口，
  不出现旧 Outcome 结构。
- `--step ... --plan path` 在发送前返回可行动错误，httptest 计数为 0；关闭/不存在
  本机 agentd 时返回清楚的连接失败，且不回落 `cliTransport` 本地 runner。
- 既有 `TestCardDispatchStepExecutorModelFlags`、`TestCardDispatchStepExtraReachesPrompt`
  不能原样继续假定 CLI 自己跑 `TemplateDispatch`：应迁移为真实 HTTP body 断言；非 step
  路径的 B203/B214 旧夹具继续保留。
- 缺陷族结论：
  - 生命周期/状态机中断：CLI 只持有短 HTTP 请求，后台收尾归 agentd；CLI 中断不应释放/重
    新 claim 本地账本。agentd 重启导致回合恢复仍是 B225，未验证，需真机。
  - 静默失败/误导报错：202 与回合成功分开；agentd 不可用不得成功、不回落、不打印旧
    Outcome。`--plan` 错误要说明 step 不接收调用方本地文件。
  - 跨平台假设：本机地址/令牌由既有 `TargetEndpoint` 决议，真实 loopback、IPv6、服务
    管理器和权限仍需真机；本机集成是边界现实，未验证。
  - 假红/假绿：CLI 测试必须穿真实 `client.Client`/httptest 并计时或用 handler channel
    证明立即返回；不能只替换旧 `swapDispatchTransportWithOpts` 回调。负面断言为“旧
    `StepRunner.Run` 路径未触发”和“--plan 未发请求”。
  - 门禁绕过：本机 agentd 的 Bearer/账本门仍生效；不为 CLI 另开匿名 endpoint；在检查
    `--plan` 与发送之间不读/转发文件，故无新增 TOCTOU 文件窗口。
  - 序列化边界：flags→`CardStepReq`→client JSON 的真实 HTTP body 逐字段断言；actor
    使用可区分的带 PID 字符串，不用默认零值。
  - 枚举白名单：无，因为 step 仍是工作流节点名，不在 CLI 新增 `implement`/`review`
    白名单。
  - 承重安全属性：CLI 不再本地持有驱动锁，唯一 actor 由请求携带；“agentd 不可用不
    fallback”用测试锁住，否则会重新出现双宿主。
  - webview/平台表现差异：无，因为 CLI 单元不触碰浏览器；看板兼容由 U0/U4 的 legacy
    body 反断言覆盖。

**④入口指针（有界文件集）**

- `cmd/card_node.go#runStepDispatch`；
- `cmd/card_dispatch.go` 的 `--step` 分支与已有 flag 装配；
- `cmd/card_dispatch_test.go`（step overrides/extra 迁移为 HTTP 装配断言）；
- `cmd/card_node_test.go`（flag/help/inline plan 反断言）；
- `cmd/root.go#TargetEndpoint`、`cmd/root.go#newTargetClient`（只复用，不改 endpoint 语义）。

### U4 · 回归、变异对照与变更说明

**①契约引用**

- spec「实现决定」要求记录两条对外行为变化；
- contract §4 固定件同步；
- 本稿 §4/§5 的接缝清单、真机清单和缺陷族结论。

**②意图与为什么**

把 U0–U3 的局部绿收敛为一条穿过真实 JSON/CLI/agentd/runner 的闭环，并在
`CHANGELOG.md` `[Unreleased]` 写明：`--step` 不再打印 `Outcome`，且新增本机 agentd
硬依赖。变异对照只用于实现轮复验，变异结束必须还原，不能把变异代码提交。

**③验收**

- 实现后按顺序运行：

  ```text
  go test ./internal/proto -run TestContractFixtures -count=1
  go test ./internal/client ./internal/ledgerstep -count=1
  go test ./internal/agentd ./cmd -count=1
  (cd web && npm run test -- src/api/contract.test.ts src/api/ledger.test.ts)
  (cd web && npm run typecheck)
  go test ./...
  git diff --check
  ```

  每条命令都必须实际返回 0；未跑到的命令不得在 review/acceptance 中写成通过。
- 运行 `go test ./internal/proto/ -run TestContractFixtures -update` 只能在确认样本和
  Go 类型都已 review 后执行；生成后的 fixture 必须与同一提交一起审查。
- `CHANGELOG.md` `[Unreleased]` 同时包含两条行为变化；不记录 B225 恢复、B189 TTL、
  card wait 或 UI 改动，因为它们不在本卡。
- 缺陷族结论：五族、序列化边界、枚举白名单、承重安全属性均以 U1–U3 验收为准，
  全量回归只能证明机内闭环，不能替代真机清单；无新增 webview 行为，因为 `runCardStep`
  调用签名和 body 仍冻结为 legacy 形状。

**④入口指针（有界文件集）**

- `CHANGELOG.md` `[Unreleased]`；
- U0–U3 列明的既有测试入口；
- 变更结束后由协调者按本稿真机清单执行，不增加 `card wait`/UI 文件。

## 4. 测试落点：既有夹具是否够用

结论不是“两个夹具都够”一句带过，而是逐缝区分：

| 接缝 | 既有夹具 | 够覆盖什么 | 缺口与补法 | 是否新建产品接缝 |
|---|---|---|---|---|
| Go `internal/proto` → Web fixture/TS | `TestContractFixtures`、`web/src/api/contract.test.ts` | 六字段键名、omitempty、逐字节 Go→JSON→TS 形状 | 需要把 `CardStepReq` 样本、fixture、TS import/type test 接入；不能只手写前端 | 否，复用固定件机制 |
| CLI flags → 本机 step HTTP | `swapDispatchTransportWithOpts`、旧 `TestCardDispatchStep*` | 旧同步 runner 的 `TemplateDispatch` 覆盖项 | **不够**：搬宿主后该回调不经过新 CLI→本机 endpoint。把既有 step 测试迁到 `httptest + TargetEndpoint + Client`，断言 URL/body/立即返回；不增加生产 endpoint 或第二拨号器 | 否，测试补穿既有 endpoint |
| handler → StepRunner | `TestCardStepReturns202`、409、node-name 与 `cardstep_test` 槽位测试 | 202、单卡单飞、释放、任意节点名 | **不够**：须补规范 actor、legacy 缺席/显式空、四项覆盖捕获、inline-file 反断言；仍落既有测试文件 | 否，复用 `startCardStep` |
| B203 覆盖优先级/成对规则 | `TestViaTemplateExecutorModelOverridesAndPairRule`、同名 executor 两条 runner/dispatch 用例 | 节点/模板/调用方优先级与同名 executor 保留下层 model | 既有用例是逻辑基线；U2 只需证明 wire 值到达同一 `StepRunner`，不在 agentd 新写规则 | 否 |
| CLI → agentd → remote task | 既有 client/agentd httptest 基座 | 机内 HTTP 形状和装配链 | 目标机真实网络、executor、服务重启不由 fake 证明，列真机清单 | 否，沿既有 `stepTransport` |

因此“缺口”是测试覆盖缺口，不是需要新增业务接缝；新增的断言全部穿既有 endpoint/拨号器。

## 5. 缺陷族对抗审查与变异必查表

### 5.1 逐族结论总表

| 缺陷族 | 本卡正面结论与对应验收 |
|---|---|
| 生命周期 / 状态机中断 | CLI 进程中断后不再承担回合收尾，agentd goroutine/既有槽位负责本轮；agentd 重启后的回合恢复不在本卡，属于 B225，标为“未验证，需真机”，不能把当前无恢复实现写成已解决。U2 仍用结束后释放测试防孤儿槽位。 |
| 静默失败 / 误导报错 | 所有入口区分 JSON 错、空 step/actor、未知节点、inline file、409 和 202；202 只代表受理。client 保留非 2xx 原始 body，CLI 无 agentd 时干净失败且无本地 fallback；对应 U1–U3 反面断言。 |
| 跨平台假设 | JSON/actor/目标机字段本身无路径拼接；但本机 loopback、Bearer、服务管理、网络、真实 executor 是边界现实，未验证，需真机。Windows/Linux/relay 不得由 Linux httptest 绿推断。 |
| 假红 / 假绿测试 | fixture 逐字节、handler 捕获真实 runner、CLI 通过真实 HTTP、旧路径未触发、拒绝路径零请求/零副作用，组成正反夹逼。只断言 202 或只调用两端 helper 都不算闭环。并发/重启/真实 executor 仍列真机。 |
| 门禁绕过 | 继续走既有 Bearer/`withLedger`/卡节点查找/单卡在飞门；不新增匿名 CLI endpoint，不把 `--plan` 文件读入 step wire。检查和动作之间不引入文件路径窗口；真实进程/权限门行为仍需真机。 |
| 序列化边界 | 逐处清单见 §2.1；Go fixture 锁六键，client 真实 HTTP 锁请求，handler 用 key-presence 区分 actor 缺席/空值，runner 捕获锁投影，Web legacy test 锁 `{step}`。两端各自绿不替代穿线测试。 |
| 枚举新值过既有白名单 | 无，因为本卡不新增状态名、事件类型、kind 或节点白名单；`implement` 只是移除错误的节点名拒绝，节点仍由卡工作流查找。若实现新增 `plan_path`/kind，属于越界。 |
| 承重安全属性有测试锁住 | `Session == Dispatcher.Actor`、PID actor、同卡单飞、202 不等终态、agentd 不可用不 fallback、inline file 不放行均各有可变红断言；没有新增一次性 token/隔离凭据，所以不扩写不存在的安全属性。 |
| webview / 平台表现差异 | 无，因为本卡不改 Web API 调用签名、不调用浏览器能力；`runCardStep` 的 legacy body 仍由 Web 测试反断言。若真机看板通过 Wails/WebView 操作，仍须协调者在真机确认 202 提示与事件流。 |

### 5.2 变异必查对照表

| 变异 | 预期变红的验收 |
|---|---|
| U0 把 `actor` 改成 `omitempty` 或漏进 fixture | `TestContractFixtures`、Web contract test、client 全字段 body 断言 |
| U1 把路径改错、丢 Bearer 或把 400 当成功 | `internal/client` CardStep HTTP/错误传播测试；CLI no-fallback 测试 |
| U1 把可选空值编码成显式零值 | client key-presence 断言，区分缺键和 `""` |
| U2 规范请求 actor 缺席也直接拒绝 | `LegacyActorFallback` 红；看板 legacy body 回归红 |
| U2 把显式 `actor:""` 当 legacy fallback | `RejectsEmptyActor` 红，防匿名请求进入驱动锁 |
| U2 将 actor 只写 `Session` 或只写 `Dispatcher.Actor` | `PropagatesRequestFields`/actor 双落点捕获红，B189 CAS 语义失守 |
| U2 丢 `target`/`executor`/`model`/`extra` 任一字段 | handler runner 捕获 + client body 穿线红 |
| U2 在 agentd 重写覆盖优先级或恢复旧规则 | 四条 B203 runner/dispatch 测试红；同名 executor model 反例红 |
| U2 保留 `if step == implement` | `AcceptsImplementWithoutInlineFile` 红 |
| U2 无条件放行带 `plan_path`/未知 inline 字段 | `RejectsInlineLocalFile` 红，且应观察 runner 未启动 |
| U2 把空 step 当合法、绕过 `nodeFor` | 空 step 400/未知节点错误测试红 |
| U2 将异步改回同步或 HTTP 等待终态 | `Returns202`/CLI immediate-return 测试红；真实命令超时窗口出现 |
| U3 仍调用本地 `StepRunner.Run` | CLI httptest 请求计数为 0 或立即返回/旧 Outcome 反断言红 |
| U3 把 `--target` 当拨号目标 | CLI URL 应为本机而 body target 为 `mac-02` 的测试红 |
| U3 用 `ledgerActor()`/固定 `web:` | PID actor 格式与双进程真机清单红/未通过 |
| U3 `--plan` 静默忽略或带上 wire | `StepRejectsPlan` 的错误/零请求断言红，或触发 U2 inline-file 反断言 |
| U3 agentd 不可用时回落本地 runner | `StepNoLocalFallback` 红；这是本卡消灭双宿主的承重测试 |
| Web `runCardStep` 改为发送 actor 或改签名 | `web/src/api/ledger.test.ts` 的精确 `{step}` 断言红 |
| 删除 202/409/slot-release 旧门 | agentd 既有 `Returns202`、`SecondReturns409`、`cardstep_test` 红 |

## 6. 真机清单（未验证，需真机；归协调者执行）

1. **本机 agentd 硬依赖**：在真实配置启用 ledger、启动本机 agentd，执行一张真实卡的
   `card dispatch <id> --step <node>`；确认命令在受理后立即返回、输出稳定卡/节点标识和
   `card wait` 入口，回合继续由 agentd 运行。停止本机 agentd 后重复，确认 CLI 给清楚
   连接失败且不在 CLI 进程内偷偷跑第二份编排。
2. **覆盖项真实生效**：在目标机真实模板/节点同时设置 executor/model，分别用同名和
   不同名 CLI 覆盖，检查真实 dispatched 事件和 executor 请求：同名 executor + 空 model
   保留下层 model，不同名 executor + 空 model 切断下层 model，`extra` 只进入本轮 prompt。
   机内 B203 测试不能证明真实 executor 读取到了字段。
3. **actor 并发区分**：同一用户/同一主机启动两个 CLI 进程对同卡发 step，确认 timeline/
   driver conflict 能区分 `#pid`，第二个不能被匿名 `web:` 合并；真实 CAS/时序属真机行为。
4. **legacy 看板兼容**：在真实 Web/Wails 控制台点击现有节点按钮，确认仍发送 `{step}`、
   agentd 补 `web:<RemoteAddr>` 后返回 202，事件流仍可由 `card wait`/看板观察；不能以
   CLI 规范 actor 测试代替。
5. **implement 与本地文件边界**：真实 CLI 不带 `--plan` 的 implement 可提交；带
   `--plan <调用方本地文件>` 明确拒绝且文件未被上传/读取。若另有调用方能构造 inline
   file 请求，确认服务端仍拒绝；当前冻结 wire 不提供该字段。
6. **目标机/平台/网络**：至少在协调者登记的真实目标机上确认 agentd→executor、relay/直连、
   Windows/Linux 路径与权限表现；机内 httptest 只验形状，不证明外部现实。
7. **宿主中断边界**：在 agentd 启动回合后重启/终止 agentd，记录本卡已知行为（回合可能
   孤儿化）；不得把它写成 B185 已恢复。恢复验收归 B225，B189 TTL/心跳不在本卡重引。

## 7. 交稿自检

- [x] 待拍板岔口集中在 §0，正文中的建议没有伪装成裁决。
- [x] 触及子系统均引用目标图并标注 logic/boundary，四条派卡资格逐项核对。
- [x] spec/contract 状态位已在上游文件头，契约冻结物逐条核对；没有新增 URL/字段/业务接缝。
- [x] L3 轻档明确不扇出；U0–U4 每个单元均含契约引用、意图、行为化验收、入口指针和有界文件集。
- [x] 看板 `{step}` 兼容、B203 覆盖/成对规则、implement inline-file 守卫均有单独接缝与反面判据。
- [x] 通用五族、序列化边界、枚举白名单、承重安全属性和 webview 候选族均逐族回答；无风险项写明“无，因为……”。
- [x] 所有依赖真实 agentd、目标机、executor、并发、WebView、重启的行为均汇总为“未验证，需真机”。
- [x] 仅产出拆解提案，不改 `cmd/`、`internal/agentd`、`internal/ledgerstep`、`internal/proto`、`web/` 实现，不改 contract，不建卡、不派发。

协调者裁决 §0 后，按 U0→U4 在一轮 implement 中执行；若 P1/P2 需要改变 wire 或拨号
边界，先回 contract 冻结，再开始实现。
