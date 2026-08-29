# B292 拆解：小队并发按成员载体设置、跨队封顶、空位协调者优先

> **状态：待拍板（2026-08-29，handoff executor 出稿）**
>
> 上游 spec：`docs/superpowers/specs/2026-08-29-b292-squad-member-concurrency-design.md`，
> 头部状态为「已批准（用户，2026-08-29）」；上游契约：
> `docs/superpowers/specs/b292-contract.md`，头部为「上游状态：已批准」且冻结物随
> `codegraph/target.json` 与 `codegraph/diffs/cards-B292-charter.json` 冻结，均已亲读核实。
> 有效基线：`acc/b156.2-156.3`；当前执行分支：`cards/B292-charter-2`。
>
> 本稿是提案，不是实现。B292 为 L3 轻档；本节点只产出子系统清单、契约核对、单张
> 后续实现卡、缺陷族对抗审查和真机清单；不扇出并行子卡，CLI/Web/清队欠账归下一轮
> `plan → implement`。

## 待拍板岔口（集中清单）

契约语义与生产接缝已经冻结，下面只列契约 §8 明确留给 plan 的实现选择；在协调者裁决前，
实现轮不得把任一方案写成事实。

### P1：`handoff squad create/set --member` 的成员政策位字符串语法

契约只冻结解析后的 `proto.SquadMember{carrier,max_concurrency}`，没有冻结 CLI 字符串
语法。两案均保留重复 `--member`、空缺/0=不限和正整数=政策上限：

- **方案 A**：`--member <carrier>[:<positive-int>]`，如 `--member c1:2`；裸
  `--member c1` 向后兼容。优点是一个成员一次提交、易读；代价是载体名若允许冒号需
  转义或被拒，错误在 CLI 解析层处理。
- **方案 B**：`--member <carrier>` 与独立的
  `--member-concurrency <carrier>=<positive-int>` 配对。优点是载体名和数值边界
  清晰；代价是两个重复 flag 的配对/重复项错误更多，且脚本更冗长。

**待拍板：选择 CLI 语法；裁决必须在 plan 区头回写。** `--max-concurrency`
不属于选择，它已由契约冻结为不存在，任何实现方案都必须使旧 flag 被拒且不发送顶层键。

### P2：按载体适配清队而不新增公开接缝的实现形态

目标语义已冻结为“协调者 `ErrNoSlot` 只回填当前请求并继续；不能堵住另一载体上的
执行者”，但契约同时冻结了现有公开入口，不得新增 `PopReadyFor(carrier)` 一类
跨域 API。可讨论：

- **方案 A**：在 `drainQueuesOnce` 的一轮内用局部 deferred 集合暂存本次不适配请求，
  继续从现有 `PopReady` 取其它请求，轮末统一 `Enqueue` 回填。优点是不增 contract
  接缝；代价是需钉住 batch 上限、重复入队和中途错误时的回填顺序。
- **方案 B**：新增带载体谓词的 scheduling 公开选择入口。选择逻辑干净，但这是新接缝，
  与 contract §1/§4/§8 的“不新增公开入口”冲突；若要走此案，必须先退回 contract，
  本轮不得直接实现。

**待拍板：是否接受方案 A 作为当前冻结契约下的唯一实现形态；若不接受，先退回
contract，不得以 B 偷加 API。**

### P3：CLI/Web 非法政策值的统一行为

契约冻结了缺席/0=不限、正整数=政策位，但没有把非空非正数、非整数或过长输入的用户
体验写成 wire 契约。两案均不得把解析失败当成成功：

- **方案 A**：CLI 本地解析和 Web 表单校验都拒绝非法值，显示含合法示例的错误且不发
  请求。优点是不会意外放宽政策；代价是旧脚本中脏值会直接失败。
- **方案 B**：保留输入但在提交前显式规范化为空/不限，并给出可见警告。优点是兼容
  性更宽；代价是拼写错误可能变成无上限，必须由 UI/CLI 输出明确警告，不能静默。

**待拍板：非法政策值选拒绝还是显式规范化；P1 只决定 CLI 语法，P3 裁决同时约束
CLI 与 Web 的错误/警告文案和不拨号断言。**

## 一、触及子系统清单（以 `codegraph/best.json` 顶层领域为准）

`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . domains` 实测给出的
parent 为空顶层领域共 12 个；B292 只触及下表 4 个。类型按项目实例化清单 §2：接缝
对面是仓内自有代码、可用测试闭环的标为逻辑型。

| 顶层领域（best.json） | 类型 | 本卡实际职责/契约别名 | 触及边界与结论 |
|---|---|---|---|
| `d_scheduling` | 逻辑型 | 编制规则；成员政策键、载体物理键、逐成员准入、释放、`ErrNoSlot`/`ErrNoHealthy` 分流。 | 对面是 `schedclient.Registry` 与自有 agentd 调用方；契约方向仍只有既有 `d_scheduling → d_ledger` Registry，规则不复制到其它域。 |
| `d_coordination` | 逻辑型 | 覆盖子域 `d_coordination_api`/`d_coordination_task`/`d_coordination_cli`；契约中分别以 `d_gateway`、`d_orchestration`、`d_cli` 记载的 HTTP/清队/命令面。 | HTTP 解析和清队编排消费 scheduling 公开入口；不在 agentd/CLI 重写准入规则。实际域映射已在 contract §10 边界修订记录回写。 |
| `d_protocol` | 逻辑型 | Go↔TS `SquadMember`、`SquadView`、`SquadInput` 及可执行 fixture。 | 只承载 DTO/JSON；不承担角色、限额或排队业务判断。 |
| `d_web` | 逻辑型 | `getSquads`/`putSquad` 镜像、设置页小队弹窗每成员政策输入与展示。 | jsdom/TypeScript 可闭环验证；真实浏览器/webview 表现列入真机清单，不把夹具推广为真机事实。 |

不触及 `d_transport`：`internal/client/squads.go` 的方法签名与通用 JSON 请求面在 contract
中冻结为不变，本卡只把其作为只读边界核验，不新开客户端接缝；其它顶层领域也不在本卡。

### 派卡资格四条逐核

虽然本轮不扇出，仍按架构法第一条核资格：

1. **有界文件集**：后续只允许一张 `B292-I0` 实现卡，生产入口、fixture、CLI 和 Web
   测试文件均在该卡末尾列明；没有以整个 `internal/agentd`、`cmd` 或 `web/src` 为
   目标的泛卡。
2. **契约面可枚举**：只消费 contract §2–§6 已冻结的实体、端点、公开方法、键和值；
   P2 方案 B 若需新 API 必须退回 contract，故不把它偷塞进实现卡。
3. **依赖可排 DAG**：单卡内部只有“编制准入/计数 → 清队消费 → wire/CLI/Web 回归 →
   组合验收”的有向顺序，不存在并行子卡之间的文件竞争。
4. **类型已标注**：4 个触及顶层领域均为逻辑型；Web 的真实浏览器和 agentd 重启等
   外部行为不冒充机内测试结论，统一转真机清单。

当前图 `check --view cards-B292-charter` 的既有 `container-misplaced`、
`container-unplaced`、legacy 和 oversized-package warnings 不改变上述有界切片；本卡
不插竖切还债卡，因为明确文件集可圈出。图中未覆盖的函数入口见“图覆盖债”，不扩成新接缝。

## 二、契约增量核对（逐条）

结论先行：本拆解**不新增接缝、不改变冻结语义，不退回 contract**。本节点唯一的边界
澄清是图顶层与既有 contract 别名的对应关系，已写入 `b292-contract.md` §10；没有把
澄清只留在本稿。

### §1 现状、边界与依赖

| 冻结点 | 对照本拆解 | 结论 |
|---|---|---|
| 编制规则只归 `d_scheduling` | I0 的计数、准入、释放验收均从 `internal/scheduling` 公开方法观察 | 不越界；gateway/CLI/Web 不复制规则 |
| `d_gateway` 只做 HTTP 解析、错误翻译、编排 | `handleSquadPut`、`drainQueuesOnce` 只消费 scheduling 入口 | 不越界；错误哨兵继续 `errors.Is` 分流 |
| `d_cli`/`d_web` 只消费登记 wire | CLI/Web 只形成成员对象，最终写入 PUT | 不越界；不直连 Registry |
| 不新增依赖方向 | I0 不新加 import 方向；`internal/client` 只作已冻结面核验 | 不越界 |

### §2 精确实体、wire 与登记兼容

| 冻结点 | 对照本拆解 | 结论 |
|---|---|---|
| `SquadMember{Carrier, MaxConcurrency}` | I0 验收成员对象与 `max_concurrency` 的 `omitempty` 行为 | 不越界；不改字段名/标签 |
| `Squad{Name,Role,Members []SquadMember}` | I0 删除队级总帽的反面断言 | 不越界；空成员仍合法 |
| 旧 `members:["c1"]` 规范化为不限成员对象 | scheduling registry roundtrip 验收 | 不越界；旧顶层总帽不复制 |
| `SquadView`/`SquadInput` 成员对象数组 | Go fixture、HTTP 投影、TS fixture 三段链路验收 | 不越界；不添加顶层 `max_concurrency` |
| GET/PUT `/api/squads` 路径、`expect` 与成功响应不变 | gateway/TS fetch 回归 | 不越界；`internal/client` 签名不改 |
| decoder 未启用 `DisallowUnknownFields`，旧顶层键应被忽略且不生效 | HTTP 反面测试请求带旧键、回读无旧键 | 不越界；类型错误仍 400 |
| 成员 0/缺席语义为不限；正数才表达政策位 | Go raw JSON key presence + TS `undefined`/number 断言 | 不越界；不把 Go `int` 改成新 wire 类型 |
| CLI 删除 `--max-concurrency`，成员语法留 plan | P1 记录语法岔口，I0 只消费裁决 | 不越界；不在本稿自行定语法 |

### §3 准入、计数、释放 1–17

| 条 | 冻结断言 | I0 计划验收映射 | 结论 |
|---:|---|---|---|
| 1 | `Admit` 只收 executor，`LaunchAdmit` 只收 coordinator | scheduling 角色正反面测试 | 不越界，签名不变 |
| 2 | 不健康成员不参与；无健康成员为 `ErrNoHealthy` | 空成员/不健康成员对照测试 | 不越界，不能混成排队 |
| 3 | 每个健康成员同时检查政策与物理计数 | 两级 cap 的成功/拒绝测试 | 不越界 |
| 4 | 政策键为 `sched_running/squad/<squad>/<carrier>` | 读库逐键断言 | 不越界，不造第三类键 |
| 5 | 物理键为 `sched_running/carrier/<carrier>` | 跨队总量断言 | 不越界 |
| 6 | 成员上限 ≤0 不设限 | 0/缺席 wire 与准入测试 | 不越界 |
| 7 | 载体上限 ≤0 不设限 | 载体不限与成员有限对照 | 不越界 |
| 8 | 任一健康成员两级有位即可成功，Binding 返回具体载体 | 前序满、后序有位测试 | 不越界，不等待绑定成员 |
| 9 | 前序成员满而后序有位必须继续尝试 | `Admit` 生产入口测试 | 不越界 |
| 10 | 全部健康成员任一级满才 `ErrNoSlot` | 全满转排队测试 | 不越界 |
| 11 | 成员上限和可大于物理上限，跨队总量仍封顶 | 两队共享载体、成功数≤物理上限 | 不越界 |
| 12 | 成员 CAS 成功后物理 CAS 失败必须回滚 | 注入冲突/回滚计数测试 | 不越界，保守方向不变 |
| 13 | `Release(squad,carrier)` 递减成员键与物理键 | 释放逐键断言 | 不越界 |
| 14 | Release 幂等、不负数、无队级键 | 重复释放与负数反面断言 | 不越界 |
| 15 | 请求覆盖优先，载体字段兜底；空模型走 CLI 默认语义 | Binding 覆盖矩阵与载体 fixture | 不越界 |
| 16 | 同 CLI 空模型与显式模型是不同载体/物理键 | 两载体身份隔离测试 | 不越界，不合池 |
| 17 | 不抢占在跑绑定 | 已占用计数不被新准入改变测试 | 不越界 |

### §4 空位分配与清队 18–24

| 条 | 冻结断言 | I0 计划验收映射 | 结论 |
|---:|---|---|---|
| 18 | `QueueKinds=[launch_queue,ignition_queue]`，同角色排序不变 | queue fixture 与清队顺序测试 | 不越界，不改字面值 |
| 19 | 载体空位优先消费能用该载体的协调者 | 同载体适配的协调者/执行者对照 | 不越界 |
| 20 | 无适配协调者才允许适配执行者消费 | 两载体队列测试 | 不越界 |
| 21 | coordinator `ErrNoSlot` 回填并继续本轮 | `drainQueuesOnce` 接缝测试 | 不越界；替换现状失败即 return |
| 22 | coordinator 非 `ErrNoSlot` 继续既有回填/错误处置 | 注入非哨兵错误测试 | 不越界，不放宽其它错误 |
| 23 | 等载体 A 的协调者不得阻断载体 B 执行者 | 同一清队轮观察另一载体执行者 | 不越界 |
| 24 | 每次重出队重新准入，不持久化排队时 Binding | dequeue→Admit/LaunchAdmit 入口测试 | 不越界 |

### §5 常量、错误、客户端默认行为与 §6 可执行冻结

| 冻结点 | 对照本拆解 | 结论 |
|---|---|---|
| `ErrNoSlot`/`ErrNoHealthy` 保留且分流不混 | I0 检查 scheduling、scheddispatch、coordapi、drain 四处消费 | 不越界；`coordapi` 只读核验，不改映射 |
| `KindLaunchQueue`/`KindIgnitionQueue` 保留 | `QueueKinds`、清队 switch、GET queue fixture | 不越界；无枚举增量 |
| `Carrier.Model` 空值不合池 | carrier fixture 与 Binding 行为测试 | 不越界 |
| client 1 MiB 读限与 context 超时不改变 | `internal/client/squads.go`/`client.go` 只读核对 | 不越界，不新增网络约束 |
| Go、TS、准入计数、清队四类金样本必须存在 | I0 验收列出四条真实边界回归 | 不越界；本节点不把未运行结果写成通过 |

### §7 拍板记录与 §8 移交区

契约 §7 的四项不可逆决定全部沿用：成员键仍是 `squad/<队>/<载体>`；队级总帽完全删除；
同 CLI 不合物理池；协调者 `ErrNoSlot` 只回当前请求并继续。P1/P2 只承接 §8 的实现
欠账，不推翻这四项决定。成员对象兼容 helper 的当前骨架位置已经存在于 scheduling，
实现轮若要搬动必须保持 §2.1 语义并在测试中穿真实 registry JSON。

### 上游状态和边界澄清回写

- spec「已批准」和 contract「已批准/已冻结」头部均已亲读核对，不能只依赖会话记忆。
- 本轮澄清：best 顶层 `d_coordination` 包含 API/task/CLI 子域，contract 文字中的
  `d_gateway`/`d_orchestration`/`d_cli` 是既有职责别名；`internal/client` 不形成新
  `d_transport` 接缝。该澄清已回写 contract §10，结论为不退回 contract。

## 三、子卡清单与依赖 DAG（不扇出）

本节点只提出一张后续实现卡，不创建并行 assignment。单卡内部 DAG 如下：

~~~
contract freeze / P1-P2 裁决
          │
          ▼
B292-I0：编制准入与计数安全属性
          │
          ├──► agentd 清队：适配性、协调者回填、另一载体继续
          ├──► Go/HTTP/TS/CLI/Web 登记 wire 回归
          └──► 组合门禁 + 变异复验 + 真机清单交协调者
~~~

### B292-I0：成员政策位准入、跨队物理封顶与登记面闭环（单实现卡）

#### ①契约引用

引用 `b292-contract.md` §1–§6 全部冻结项，重点为 §2.1/§2.2 成员对象与兼容读、§3 条
1–17 两级准入与计数键、§4 条 18–24 空位适配/清队回填、§5 错误与默认行为、§6 四类
金样本；引用 spec 的“规则仍只在编制域收口”“空位协调者优先”“不抢占”“本期不做
账户池/B293”等决定。P1/P2 裁决回写后才可进入实现轮；本卡不新增公开 API、不恢复
队级总并发、不合并同 CLI 物理池。

#### ②意图与为什么

让一次小队领任务在成员载体之间自由选择，同时把成员政策位与载体物理位分别落到
冻结键上；让清队知道“当前空位能服务谁”，在协调者无位时只回填当前请求而不把其它
载体的执行者拖停；让登记面从 HTTP 到 Go/TS、CLI 和 Web 始终传同一成员对象。

规则所有权留在 scheduling，agentd 只编排入口和错误/队列状态，协议只承载 wire，CLI
与 Web 只形成/展示 wire。这样测试可以从 `Admit`/`LaunchAdmit`、`drainQueuesOnce`、
真实 HTTP JSON 和真实 React 用户交互分别观察调用方依赖的行为，不把内部 helper 的
正确性误当成接缝已通。

#### ③验收（按子系统类型分流；本节点未运行下游实现验收）

所有下列实现轮判据都必须给出命令或测试的可复现结果；本节点只提交提案，未把这些
预期结果冒写为本轮实测通过。

**A. `d_scheduling`（逻辑型，机内可闭环）**

- `go test ./internal/scheduling -count=1` 返回该包 `ok`。测试必须从公开
  `Service.Admit`/`Service.LaunchAdmit`/`Service.Release` 观察：前序成员满而后序有位
  时绑定后序载体；所有健康成员满返回 `ErrNoSlot`；无健康成员返回 `ErrNoHealthy`；
  空/0 成员政策不限；重复 Release 后两级计数仍为 0 且不出现负数。
- 真实 registry JSON roundtrip 断言：同一载体被两个小队使用时成功总数不超过载体
  `MaxConcurrency`；成员上限之和可大于物理上限；最终只出现
  `sched_running/squad/<队>/<载体>` 和 `sched_running/carrier/<载体>`，不存在
  `sched_running/squad/<队>` 或第三类 `member/` 键。
- 两载体一个 `Model==""`、一个显式模型时，成功 Binding 的载体名和模型兜底彼此
  独立；请求的 target/executor/model 覆盖优先级逐项断言。该测试锁的是调用方依赖的
  Binding 行为，不是 `bindingFor` 单独返回值。
- 并发 N 路准入同一/跨小队共享载体时，成功数严格受两级上限约束；删掉两级上界判断
  的变异必须使测试翻红，恢复后再绿。CAS 重试预算耗尽必须按 contract 的既有错误
  分流记录，不得把 `ErrRetryExhausted` 静默伪装成健康问题。

**B. `d_coordination` 的 agentd 清队面（逻辑型，机内可闭环）**

- `go test ./internal/agentd -run 'Scheduling|Automation|Queue' -count=1` 返回
  `ok`（实现轮若测试命名不同，必须在 ledger 记录实际命令与原始输出）。`drainQueuesOnce`
  的接缝测试准备：拉起队的协调者对所有成员 `ErrNoSlot`，点火队另有能用载体 B 的
  执行者；一次清队轮结束后协调者请求仍在队列，执行者已通过其真实派发入口，不能只
  断言 `processed`。
- 同一载体同时有协调者与执行者时，协调者先消费该空位；协调者只适配载体 A、载体 B
  仍可用时，A 的协调者回填不阻断 B 的执行者。`QueueKinds` 和同角色排序的 ready →
  priority → FIFO 结果逐项断言。
- 注入 `ErrNoSlot` 与非 `ErrNoSlot` 两类失败：前者回填当前请求并继续本轮，后者仍按
  既有错误路径回填/停止；错误判断必须经 `errors.Is`，不得按字符串匹配。
- 重新出队必须再次调用 `LaunchAdmit`/`Admit`，排队记录中不能持久化 Binding；清队
  中途 batch 到限或回填失败时，未处理请求的队列状态必须有明确可行动日志。实际 agentd
  重启窗口不由机内夹具宣称已验证，见真机清单。

**C. `d_protocol` + `d_coordination_api`（逻辑型，序列化/HTTP 可闭环）**

- `go test ./internal/proto ./internal/agentd -run 'ContractFixtures|Squad|Scheduling' -count=1`
  返回 `ok`。Go fixture、HTTP `handleSquadPut` 真实请求、response decode、TS fixture
  必须穿同一成员对象链路：`[{"carrier":"c1","max_concurrency":2}]` 保留 2；0/缺席
  不输出键；旧顶层 `max_concurrency` 输入被忽略且回读不出现；空队保留 `members:[]`。
- 成员引用不存在由 HTTP 返回 400 且保留可行动错误；角色/类型/expect 的既有分类不
  被移到 handler；`ErrNoSlot` 与 `ErrNoHealthy` 的协调者/执行者消费映射不混淆。
- `internal/proto/contract_fixture_test.go` 的 Go fixture 与
  `web/src/api/testdata/SquadsResp.json` 的 TS fixture 字段、缺席/零值语义一致；任何
  手写投影漏掉 `MaxConcurrency` 必须使真实链路测试失败。

**D. `d_coordination_cli`（逻辑型，命令入口可闭环）**

- 在 P1 裁决后的语法下，`go test ./cmd -run 'Squad' -count=1` 返回 `ok`；stub HTTP
  必须实际收到成员对象数组、政策位数值和 `expect`，不得收到顶层
  `max_concurrency`。`list` 表格和 NDJSON 展示每成员政策位，不生成队级总帽。
- `--max-concurrency` 被 Cobra 拒绝；非空非法成员政策值本地返回含语法示例的可行动
  错误且不拨号（若 P1 选择服务端拒绝方案，必须先证明 wire 有可表达的错误形状，
  不得把无法解析的字符串静默当不限）。成员名含空格/斜杠/中文的参数被完整地放进
  `carrier`，不被 CLI 拆坏。
- test 必须走 `squadCreateCmd`/`squadSetCmd`/`squadListCmd` 的真实 Cobra 调用和
  stub HTTP；只测 `squadMemberInputs` helper 不算接缝验收。

**E. `d_web`（逻辑型，TypeScript/React 可闭环；真实浏览器见真机）**

- `cd web && npm run typecheck` 返回退出码 0；
  `npm test -- --run src/app/settings/SchedulingPage.test.tsx src/api/scheduling.fetch.test.ts src/api/contract.test.ts`
  返回列出的测试通过。设置页小队弹窗对每个已登记载体同时提供勾选与政策并发输入，
  保存后 `putSquad` 请求成员对象带对应数值，留空/0 不带键；小队卡片显示每成员政策位，
  没有队级总帽输入或展示。
- 用户输入负数、小数、非数字和超长值时，必须按 P1/P3 裁决显示可行动错误或明确
  规范化结果；不得把非法文本静默当“不限”。保存失败时弹窗和草稿保留，409 错误可
  指向刷新/重试；这锁的是用户行为，不是 `optionalConcurrency` helper 的孤立返回值。
- fixture 需覆盖默认模型载体、显式模型载体、空队以及 `max_concurrency` 缺席/数值；
  React 测试不能只 mock 成功响应后断言调用次数。

**组合门禁（实现轮）**

- `go build ./...` 与 `go vet ./internal/scheduling ./internal/agentd ./internal/proto ./cmd`
  均返回退出码 0；`gofmt -l` 对改动 Go 文件无输出；`git diff --check` 无输出。
- 先运行上面分域测试，再运行 `go test ./... -count=1`；任何失败把原始输出写入
  B292 breakdown ledger，不以“环境原因”替换报错。
- 变异复验至少覆盖：删成员政策上限、删载体物理上限、改回队级键、把空模型与显式
  模型合池、让 coordinator `ErrNoSlot` 直接 return、删掉旧顶层键的负向断言、删掉
  Web 每成员输入。未实际翻红的变异均标“未验证”。

**缺陷族对抗审查（结论属于本卡验收）**

| 触及域 | 生命周期 / 状态机中断 | 静默失败 / 误导报错 | 跨平台假设 | 假红 / 假绿测试 | 门禁绕过 |
|---|---|---|---|---|---|
| `d_scheduling` | 不新建进程、工单或临时目录，因为本域只持久化 registry 计数/队列；释放由绑定回合结束路径负责。PopReady→准入→释放之间若 agentd 重启是否遗留队列/计数，**未验证，需真机**。 | `ErrNoHealthy` 与 `ErrNoSlot` 分开；CAS/registry 错误上浮而不是报成功；物理 CAS 失败回滚成员计数且回滚失败保持保守高计数。 | 计数键与 JSON 逻辑不依赖路径分隔符；真实 SQLite 多进程锁、Windows 文件语义**未验证，需真机**。 | 必须从 `Admit`/`LaunchAdmit`/`Release` 测调用方行为；N>M 并发、后序成员有位、重复 Release 和删上界变异分别有反面断言，不能只测 `acquire`。 | scheduling 没有新的执行写路径；Registry CAS 同时保护登记与运行计数，避免“先检查再动作”的无锁放行。跨进程/软链式 TOCTOU 不在 Go 夹具可证明范围，**未验证，需真机**。 |
| `d_coordination`（agentd） | 清队出队后失败请求的责任是本轮回填；协调者 ErrNoSlot 不再终止整轮。回填/清队窗口中 agentd 重启、重复消费与计数归还，**未验证，需真机**。 | 仅 `errors.Is(ErrNoSlot)` 继续；非该错误不被吞。回填失败必须日志含 kind/card/node/cause/requeue error；不能返回成功而丢请求。 | Go 编排本身不依赖平台路径；真实 agentd 的进程信号、SQLite 锁和多实例清队**未验证，需真机**。 | 测试必须真实入队、真实 `drainQueuesOnce`、同时断言协调者仍在队列和另一载体执行者已走入口；夹具中的 runner 不代表真实 CLI 行为。 | 清队不新增旁路执行入口；所有准入重新走 scheduling 公开方法，HTTP/CLI 写入仍经既有 auth/withScheduling。P2 若新增 selector 将越过冻结 contract，必须退回。 |
| `d_protocol` | DTO 不创建生命周期资源，因为它只编码/解码；agentd 重启时未持久化的半条 HTTP body 恢复行为**未验证，需真机**。 | 成员 policy 2/0/缺席通过键存在性区分；旧顶层键被忽略但不生效；解码类型错、成员缺失、CAS 冲突均走明确状态码。 | 标准 JSON 不依赖 OS；不同 JSON 实现/字符集和客户端版本互操作**未验证，需真机**。 | Go fixture、HTTP decode/encode、TS fixture 必须至少有一条穿真实 wire；只测 struct 或常量会假绿。 | proto 无权限门；它不能通过新增 DTO 字段绕开 agentd 的 `withScheduling`、鉴权或 scheduling 规则，入口测试必须验证这一点。 |
| `d_web` | 页面保存失败保留弹窗/草稿，不新建后台任务；浏览器刷新/关闭时未提交草稿是否恢复，**未验证，需真机**。 | 非法政策输入不能静默变不限；409/网络错误需显示可行动信息；成功回读要以服务端数据更新，不能只改本地乐观状态。 | 数字输入、中文/斜杠载体名、`undefined` 与 0 的 JSON 行为在不同浏览器**未验证，需真机**。 | 测试用 user-event 打开弹窗、勾选成员、输入每成员上限、保存并检查请求 body；不得只测 `optionalConcurrency` 或 mock 调用次数。 | Web 不直写 Registry、不自行做物理准入；保存统一走 `putSquad`→既有 `/api/squads` auth/expect 门。真实浏览器 cookie/auth 与并发编辑**未验证，需真机**。 |

**项目第六族候选：webview / 平台表现差异**

本卡触及 Web 表单但不新增 clipboard、postMessage、cookie 策略或专用 webview API，因而
没有新的 webview 协议接缝；这不是“无风险”结论。标准 DOM number input、same-origin
fetch、中文/特殊字符输入在 Chromium/WKWebView/Wails 实机差异仍**未验证，需真机**，列入
真机清单。若实现引入浏览器专有 API，必须停止并重新做缺陷族/contract 边界核对。

**追加设问：序列化边界**

新增/变更字段 `SquadMember.MaxConcurrency` 从产生到消费的手写点全部列为：

1. scheduling `Squad` JSON 编码/兼容读取（`internal/scheduling/scheduling.go` 中
   `SquadMember` 与 `UnmarshalJSON`）；
2. `internal/agentd/schedapi.go` 的 `handleSquadPut` 输入投影与 `squadView` 输出投影；
3. `internal/proto/scheduling.go` 的 `SquadMember`/`SquadView`/`SquadInput` 编码；
4. `cmd/squad.go` 的 `squadMemberInputs`、set 编辑回路和 `formatSquadMembers`；
5. `web/src/api/scheduling.ts` 的 `SquadMember`/`putSquad`；
6. `SchedulingPage.tsx` 的 draft、每成员输入和 save body；
7. Go fixture 与 TS `SquadsResp.json`。

必须有一条真实 `scheduling registry JSON → HTTP PUT/GET → proto JSON → TS fixture/API`
回归，另有 CLI stub 穿过命令参数到 HTTP body；缺席与 0 用 raw JSON key presence、TS
`undefined`/`0` 区分。两端各自通过不能替代这条链路。roundtrip 属性测试可作为实现轮
加严项，但不能用随机生成替代上述固定金样本。

**追加设问：枚举新值过既有白名单**

本卡没有新增状态名、事件类型、kind 或角色枚举值，**无新增枚举白名单，因为**角色仍
是 `executor|coordinator`、队列仍是 `launch_queue|ignition_queue`，成员政策位是数值字段。
实现轮仍须逐处复核 `QueueKinds`、`drainQueuesOnce` 的 kind switch、`ErrNoSlot`/
`ErrNoHealthy` 的 `errors.Is` 分流；若为 CLI 语法新增枚举 token 或别名，必须在 plan
补齐所有校验器并把它视为 contract 变更候选。

**追加设问：承重安全属性有测试锁住**

本卡的承重安全属性不是“代码恰好如此”：

- 物理载体跨小队唯一封顶，删物理上限测试必须红；
- 成员政策键按 `(squad,carrier)` 隔离，改成队级或共享键必须红；
- 成员选中与物理占用必须成对成功，物理 CAS 失败回滚且不可超发；
- Release 幂等、不可负和不抢占必须各有可红断言；
- coordinator `ErrNoSlot` 只能影响当前请求，另一载体执行者同轮通过的测试必须红。

这些测试均需从生产入口或真实 wire 进入；未实际完成变异复验的项目标“未验证”。

#### ④入口指针与有界文件集

**预计修改/新增测试仅限以下文件（不得扩大到整个目录）：**

~~~
internal/scheduling/scheduling.go
internal/scheduling/scheduling_test.go
internal/scheduling/registry_read_test.go
internal/agentd/scheddrain.go
internal/agentd/scheddrain_test.go
internal/agentd/schedapi.go
internal/agentd/schedapi_test.go
internal/agentd/scheddispatch_test.go
internal/proto/scheduling.go
internal/proto/contract_fixture_test.go
cmd/squad.go
cmd/squad_test.go
web/src/api/scheduling.ts
web/src/api/contract.test.ts
web/src/api/scheduling.fetch.test.ts
web/src/api/testdata/SquadsResp.json
web/src/app/settings/SchedulingPage.tsx
web/src/app/settings/SchedulingPage.test.tsx
~~~

**只读入口核验，不得借机改写：**

~~~
internal/agentd/coordapi.go
internal/agentd/coordapi_test.go
internal/client/squads.go
internal/client/client.go
internal/agentd/server.go
~~~

入口符号锚（可解析或会被再锚定）：`internal/scheduling/scheduling.go#SquadMember`、
`internal/scheduling/scheduling.go#Squad`、`internal/scheduling/scheduling.go#Admit`、
`internal/scheduling/scheduling.go#LaunchAdmit`、`internal/scheduling/scheduling.go#Release`、
`internal/scheduling/scheduling.go#PopReady`、`internal/agentd/scheddrain.go#drainQueuesOnce`、
`internal/agentd/scheddrain.go#launchCoordinatorRound`、
`internal/agentd/schedapi.go#handleSquadPut`、`internal/agentd/schedapi.go#squadView`、
`internal/agentd/scheddispatch.go#admitSquadStep`、`internal/proto/scheduling.go#SquadMember`、
`internal/proto/scheduling.go#SquadView`、`internal/proto/scheduling.go#SquadInput`、
`cmd/squad.go#squadCreateCmd`、`cmd/squad.go#squadSetCmd`、`cmd/squad.go#squadListCmd`、
`cmd/squad.go#squadMemberInputs`、`cmd/squad.go#formatSquadMembers`、
`web/src/api/scheduling.ts#SquadMember`、`web/src/api/scheduling.ts#putSquad`、
`web/src/app/settings/SchedulingPage.tsx#SchedulingPage`。

图视图当前实际收录并已验证的模型锚为：
`internal/scheduling/scheduling.go#SquadMember`、`#Squad`、`internal/proto/scheduling.go#SquadMember`、
`#SquadView`、`#SquadInput`、`web/src/api/scheduling.ts#SquadMember`、`#SquadView`、
`#SquadInput`；其余入口即使 `resolve` 能以 `moved` 再锚定，也不在 `sym` 节点覆盖内，
属于本卡图覆盖债，不得借此发明新接缝。

## 四、真机清单（未验证，需真机，归协调者执行）

1. **agentd 重启窗口**：在 `PopReady` 已删除、协调者 `LaunchAdmit` 返回
   `ErrNoSlot`、回填进行中、两级计数已部分占用和回合即将 Release 的窗口重启实际
   agentd；确认请求不丢、不重复消费、计数不永久泄漏，孤儿队列/计数由明确责任方回收。
2. **真实跨载体优先级**：两台真实载体/真实 agentd 配置中，载体 A 无位、载体 B 有位；
   运行真实协调者与执行者队列，确认协调者优先只作用于可用同一载体的请求，A 的等待
   不饿死 B 的执行者，不抢占在跑任务。
3. **真实模型身份**：同 CLI 一条模型列为空、一条显式填当前默认模型或 flash，分别
   运行实际 CLI；确认空模型随 CLI 默认、显式模型钉住，两者不共享物理计数，也不把
   真实账户额度误宣称已被调度层解决（账户池仍属 B293/roadmap）。
4. **多进程并发与 TOCTOU**：在目标机用多个实际 agentd/调用者并发登记、准入、释放，
   观察 SQLite/registry CAS、文件锁与跨进程计数，确认检查和动作之间没有可超发窗口。
5. **CLI shell 现实**：Linux/macOS/Windows 的真实 shell 下执行已拍板的重复
   `--member` 语法，覆盖空格、斜杠、中文和冒号边界；确认引号、转义和错误退出码与
   命令帮助一致。
6. **浏览器与 webview**：在 Chromium 与项目支持的 WKWebView/Wails 容器打开设置页，
   实际填写每成员政策位、保存、刷新并并发编辑；确认 number input、same-origin
   fetch、特殊载体名、409 错误和草稿保留行为，不能用 jsdom 结果代替。
7. **真实认证/权限面**：从实际未登录、过期会话和合法会话分别访问 `/api/squads`，
   确认新增/修改登记只能走既有 auth/hostGuard/expect 门，没有 CLI 或 Web 旁路；门的
   并发 TOCTOU 结论与本清单第 4 条一致，机内单测不能推广。

## 五、图覆盖与本节点自检

- 当前 B292 图视图 `check` 已实测 `fails: []`；已有 warnings 原样保留，不把它们升级
  为本卡失败。`validate` 的未扫描入口和函数 `sym` 缺口属于图覆盖债，不是新契约接缝。
- 本节点结束前必须亲跑：
  `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . resolve --doc docs/superpowers/specs/b292-breakdown.md`
  （坏锚即修）、`git diff --check`、暂存区文件范围检查和最终 `git status --short --branch`。
- 本节点没有实现代码、没有并行 assignment、没有 handoff CLI 调用。协调者拍板后须把
  P1/P2 裁决和理由回写本稿首部，将状态更新为「已拍板（日期）」并与契约修订记录同批
  提交；在此之前本稿状态必须保持「待拍板」。
