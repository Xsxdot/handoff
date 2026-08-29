# B292 契约增量：小队成员载体政策位与跨队物理上限

**上游状态：已批准**（源 spec：`docs/superpowers/specs/2026-08-29-b292-squad-member-concurrency-design.md`，头部状态行已核实）
**级别：L3 轻档**
**冻结状态：本提交随 `codegraph/target.json` 与 `codegraph/diffs/cards-B292-charter.json` 冻结**

## 1. 现状查证与边界

本仓架构形态是按子系统分域的平铺领域包，无横向 controller/service/dao 分层。编制规则仍归
`d_scheduling`；`d_gateway` 只做 HTTP 解析、错误翻译和调用编排，`d_cli` 与 `d_web` 只消费
登记 wire。B292 不新增依赖方向，不把调度规则复制到网关、CLI 或前端。

spec 头部状态为「已批准（用户，2026-08-29）」；本契约以该状态为上游前提。

已按现状代码核对的入口与类型如下（行号只作本轮读数，符号名是代码事实定位）：

| 现状签名/字面值 | 代码事实出处 | B292 契约变化 |
| --- | --- | --- |
| `type Carrier struct { ... Model string; MaxConcurrency int; Healthy bool }` | `internal/scheduling/scheduling.go:49-60` | 形状不变；`Model` 的空值与非空值参与载体身份，`MaxConcurrency` 仍是载体物理上限 |
| `type SquadMember struct { Carrier string; MaxConcurrency int }` / `type Squad` | `internal/scheduling/scheduling.go:70-81` | 成员对象承载政策位；删除队级 `MaxConcurrency` |
| `func (s *Service) PutSquad(q Squad, expect int) error` | `internal/scheduling/scheduling.go:204-217` | 校验每个 `SquadMember.Carrier` 已登记；空成员仍合法 |
| `func (s *Service) Admit(req IgnitionRequest) (Binding, error)` | `internal/scheduling/scheduling.go:229-239` | 保持签名；按成员逐个尝试，任一成员政策位和载体物理位同时有位即成功 |
| `func (s *Service) LaunchAdmit(squadName string) (Binding, error)` | `internal/scheduling/scheduling.go:241-252` | 保持签名；协调者小队复用同一成员级准入 |
| `func (s *Service) Release(squadName, carrierName string) error` | `internal/scheduling/scheduling.go:254-260` | 保持签名；归还成员政策键与载体物理键 |
| `func (s *Service) Enqueue(req IgnitionRequest, kind string) (int, error)` | `internal/scheduling/scheduling.go:262-292` | 队列 wire 不变；满员请求仍持久排队 |
| `func (s *Service) PopReady(kind string) (IgnitionRequest, bool, error)` | `internal/scheduling/scheduling.go:294-336` | 队内排序不变；载体适配性由清队前的真实准入裁定 |
| `var QueueKinds = []string{KindLaunchQueue, KindIgnitionQueue}` | `internal/scheduling/scheduling.go:23-34` | 字面值和先后不变；不再把先清拉起实现成失败即终止全轮 |
| `var ErrNoSlot = errors.New(...)` | `internal/scheduling/scheduling.go:140-144` | 哨兵保持；仅“所有健康成员均无政策或物理位”返回它 |
| `var ErrNoHealthy = errors.New(...)` | `internal/scheduling/scheduling.go:140-144` | 哨兵保持；没有健康成员与满员继续分流 |
| `func (s *Server) drainQueuesOnce(ctx context.Context) (int, error)` | `internal/agentd/scheddrain.go:84-124` | 协调者 `ErrNoSlot` 回队并继续本轮；其它错误仍按既有错误路径处理 |
| `func (s *Server) launchCoordinatorRound(ctx context.Context, card, source string) (keystone.RoundResult, error)` | `internal/agentd/scheddrain.go:175-207` | `LaunchAdmit` 错误仍包装为 `coordinatorAdmissionError`，供清队按 `errors.Is` 分流 |
| `func (s *Server) handleSquadPut(w http.ResponseWriter, r *http.Request)` | `internal/agentd/schedapi.go:119-163` | PUT body 接收成员对象；旧顶层 `max_concurrency` 不参与写入 |
| `type SquadMember` / `type SquadView` / `type SquadInput` | `internal/proto/scheduling.go:23-61` | `members` 改为成员对象数组；二者均删除顶层 `max_concurrency` |
| `func (c *Client) Squads(ctx context.Context) (*proto.SquadsResp, error)` | `internal/client/squads.go:33-47` | 签名与 GET 路径不变；解码新 `members` 形状 |
| `func (c *Client) PutSquad(ctx context.Context, name string, expect int, in proto.SquadInput) (*proto.SquadPutResp, error)` | `internal/client/squads.go:67-76` | 签名与 PUT 路径不变；发送成员+政策位对象 |
| `export interface SquadMember` / `SquadView` / `SquadInput` 与 `putSquad` | `web/src/api/scheduling.ts:18-49,126-130` | TS 镜像成员对象；小队请求不再有队级上限 |
| `squadCreateCmd` / `squadSetCmd` / `squadListCmd` | `cmd/squad.go:37-146` | 删除 `--max-concurrency`；成员参数形成成员对象，列表不展示队级总帽 |

组装点已查证为 `internal/agentd/server.go:567` 的 `registerSchedulingRoutes` 调用和
`internal/agentd/server.go:2253-2283` 的 `Server.SetupAutomation`；本卡只改变其既有
`scheduling.Service` 入站调用和登记 wire，不在组装点新增具体实现认识。

## 2. 精确类型与登记 wire

### 2.1 编制域实体

下游 Ticket 0 采用以下可编译形状；字段名和 JSON 标签是契约，不由实现节点另行发明：

```go
// internal/scheduling/scheduling.go
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type Squad struct {
	Name    string        `json:"name"`
	Role    SquadRole     `json:"role"`
	Members []SquadMember `json:"members"`
}
```

`SquadMember.Carrier` 是具体载体名，不是 CLI 名；`MaxConcurrency <= 0`（缺席或 0）表示该
成员政策位不限。成员顺序保留登记顺序，只在多个成员都能成功时作为决胜，不构成等待绑定。

存量 registry 的兼容读规则单独冻结：旧 body 的 `members:["c1", "c2"]` 读入时规范化为
`members:[{"carrier":"c1"},{"carrier":"c2"}]`；旧顶层 `max_concurrency` 丢弃，不复制到任何
成员。下一次成功写回只产生新成员对象形状，不再产生顶层队级键。新写入若成员引用不存在，
`PutSquad` 仍以 `ErrNotFound` 臂上浮；空数组是合法空队。

### 2.2 HTTP/Go/TypeScript wire

`internal/proto/scheduling.go` 的确切形状为：

```go
type SquadMember struct {
	Carrier        string `json:"carrier"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

type SquadView struct {
	Name    string        `json:"name"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
	Version int           `json:"version"`
}

type SquadInput struct {
	Name    string        `json:"name,omitempty"`
	Role    string        `json:"role"`
	Members []SquadMember `json:"members"`
}
```

其中 `GET /api/squads` 保持 `SquadsResp{Carriers, Squads}` 和版本字段；端点仍为：

- `GET /api/squads`：`internal/agentd/schedapi.go:35-40` 注册，返回小队成员对象数组；
- `PUT /api/squads/squads/{name}?expect=N`：同一注册点，body 为 `SquadInput`；成功仍返回
  `SquadPutResp{Name, Version}`，状态仍为 200；
- `GET /api/queue`、队列元素和 `POST /api/cards/{id}/coordinator/launch` 均不改 wire。

`handleSquadPut` 仍只做 body 解码、路径名一致性、调用 `PutSquad` 和错误分类。当前使用的
`json.NewDecoder(r.Body).Decode(&in)` 未调用 `DisallowUnknownFields`；Go 标准库只有在
`d.disallowUnknownFields` 为真时才保存未知字段错误（`/usr/local/go/src/encoding/json/decode.go:739-741`）。
因此旧请求顶层 `max_concurrency` 被忽略且不生效；这不是新业务分支。成员必须是上述对象
形状，类型错误仍由解码失败返回 400。

Go `encoding/json` 的 `omitempty` 对 0 会省略字段（`/usr/local/go/src/encoding/json/encode.go:107-110`）；
所以成员政策为 0 或缺席时，成员对象中没有 `max_concurrency`，语义均为不限。`SquadInput`
本身不再有 `max_concurrency` 字段，CLI 与 Web 不得主动发送它。

TypeScript 镜像为：

```ts
export interface SquadMember {
  carrier: string
  max_concurrency?: number
}
export interface SquadView {
  name: string
  role: string
  members: SquadMember[]
  version: number
}
export interface SquadInput {
  name?: string
  role: string
  members: SquadMember[]
}
```

`putSquad(name, expect, input)` 的签名、URL 编码、`expect` 查询参数及成功响应保持现状；
其余载体字段仍按现有 `CarrierInput/CarrierView` 镜像。

CLI 的冻结边界是：`squad create` 与 `squad set` 不再注册或读取 `--max-concurrency`；每个
成员提交必须带具体 `carrier` 和可选的正整数 `max_concurrency`，缺席/0 发送为不限；`squad list`
表格与 NDJSON 只展示成员对象中的政策位，不生成队级总帽。成员参数的具体字符串解析属于
CLI 包内实现选择，移交 plan；解析后的 `proto.SquadMember` 形状不可变。

## 3. 准入、计数与释放

以下为冻结清单，每条均是独立的 pass/fail 断言：

1. `Service.Admit` 只接受 `RoleExecutor` 小队；`Service.LaunchAdmit` 只接受 `RoleCoordinator` 小队，二者签名不变。
2. 成员引用的载体不健康时不参与准入候选；没有任何健康成员时返回 `ErrNoHealthy`。
3. 对每个健康成员，准入同时检查该成员政策计数和该成员载体物理计数。
4. 成员政策计数键精确为 `sched_running/squad/<squad>/<carrier>`。
5. 载体物理计数键精确为 `sched_running/carrier/<carrier>`。
6. `SquadMember.MaxConcurrency <= 0` 时成员政策计数不设上限。
7. `Carrier.MaxConcurrency <= 0` 时载体物理计数不设上限。
8. 一个健康成员同时满足两级上限即可成功，成功返回的 `Binding.Carrier` 是该成员的具体载体名。
9. 前序成员政策位满而后序成员有位时，准入必须尝试后序成员，不得返回 `ErrNoSlot`。
10. 所有健康成员的任一级名额都满时才返回 `ErrNoSlot`；该错误供调用方转排队。
11. 成员政策上限之和可大于载体物理上限；同一物理载体的跨小队占用总数不得超过其物理上限。
12. 成功准入仍先 CAS 增加成员政策键，再 CAS 增加载体物理键；后者失败时回滚前者，保守计数方向不变。
13. `Release(squad, carrier)` 递减精确的 `sched_running/squad/<squad>/<carrier>` 与 `sched_running/carrier/<carrier>` 两键。
14. 释放仍是幂等的，任一计数不降到负数；不新增队级 `sched_running/squad/<squad>` 键。
15. `Binding.Target/Executor/Model` 仍按请求覆盖优先、载体字段兜底；`Carrier.Model == ""` 表示不写模型覆盖，执行者使用该 CLI 当时默认模型。
16. 同一 CLI 的空模型载体与显式模型载体是两个不同载体；它们不共享物理计数键。
17. 成员选择不抢占已在运行的绑定；载体空位只影响新的准入。

准入的生产调用面保持为 `internal/agentd/scheddispatch.go:47-94` 的执行者节点入口和
`internal/agentd/scheddrain.go:173-247` 的协调者入口；二者都只能调用公开的 `Admit` /
`LaunchAdmit`，不直接调用 `admitInto` 或 `acquire`。

## 4. 空位分配与清队

以下清单只冻结清队接缝，不把内部循环另列为缝：

18. `QueueKinds` 仍是 `[launch_queue, ignition_queue]`，同角色队列内部继续按就绪度、卡优先级、入队序排列。
19. 对某载体的空位，拉起队列中只有“含该载体且该成员两级均有位”的协调者请求可优先消费它。
20. 若没有符合条件的协调者请求，点火队列中能使用该载体的执行者请求才可消费该空位。
21. `drainQueuesOnce` 取出协调者请求后，若 `launchCoordinatorRound` 返回的包装错误可由 `errors.Is` 判定为 `ErrNoSlot`，必须回填该请求并继续处理本轮，不得 `return` 终止整轮。
22. `drainQueuesOnce` 对协调者的非 `ErrNoSlot` 错误仍回填并按既有错误处置；本卡不放宽其它失败。
23. 协调者等待载体 A 的 `ErrNoSlot` 不得阻断载体 B 上仍可准入的执行者请求。
24. 队列请求每次重新出队都重新走 `LaunchAdmit` 或 `Admit`，不得把排队时选择的载体持久化为绑定。

`coordinatorAdmissionError` 的生产者是 `launchCoordinatorRound` 与 `wakeCoordinatorRound`
（`internal/agentd/scheddrain.go:175-183,212-223`），HTTP 协调者端点当前消费它并把
`ErrNoSlot` 映射为 409（`internal/agentd/coordapi.go:114-139`）；该常量消费链保持，清队
只新增对 `errors.Is` 的局部判定。

## 5. 对侧常量、依赖行为与边界

| 常量/字面值 | 生产者 | 消费者 | 结论 |
| --- | --- | --- | --- |
| `ErrNoSlot` | `admitInto` 经 `acquire` 在所有健康成员无位时产生 | `admitSquadStep`（`scheddispatch.go:75-91`）、`coordapi.go:127-135`、本卡清队分支 | 活跃事实源；保留原哨兵，不把 `ErrNoHealthy` 混入 |
| `ErrNoHealthy` | `admitInto` 在无健康成员时产生 | `admitSquadStep` 泛化上浮、协调者错误泛化路径 | 活跃事实源；仍代表配置/健康问题而非排队 |
| `KindLaunchQueue` | `scheduling.go:28` 定义，`Enqueue` 与清队 switch 使用 | `QueueKinds`、`scheddrain.go:90-112`、GET queue 投影 | 活跃，字面值不改 |
| `KindIgnitionQueue` | `scheduling.go:29` 定义，`Enqueue` 与清队 switch 使用 | 同上 | 活跃，字面值不改 |
| `Carrier.Model` 空串 | `bindingFor`（`internal/scheduling/scheduling.go:458-473`）取载体兜底 | `launchCoordinatorRound` / 执行者派发消费 `Binding.Model` | 活跃；空与显式模型不可合池 |

本卡没有新的网络库、请求体大小、超时、保活或握手约束。既有 client 行为继续承重：
`internal/client/squads.go:20-30` 用 `io.LimitReader(..., 1<<20)` 后 `json.Unmarshal` 整体解码；
`internal/client/client.go:399-439` 的 `do` 由调用方 context 施加请求时限，底层 client 不新增
全局超时。B292 不复制或改变这些默认行为。

## 6. 可执行冻结、视图与验证

本卡命中可执行冻结的是成员/政策位 wire 形状和 `sched_running` 键形状。下游实现必须在
真实生产入口补齐并运行以下金样本：

1. Go `SquadsResp` fixture：成员为 `[{"carrier":"c1","max_concurrency":2}]`；0/缺席成员政策位不出键；顶层 `max_concurrency` 不出键。
2. TypeScript `SquadsResp` fixture 与 Go fixture 字节一致，覆盖空队和默认模型载体。
3. 准入计数金样本：两小队共享一个载体时只允许物理上限个成功；每次成功只出现一个成员政策键和一个载体键，不能出现 `sched_running/squad/<队>`。
4. 清队接缝测试：协调者对全部成员 `ErrNoSlot` 后回队，本轮仍放行另一载体可用的执行者。

本轮已实测（结果以命令输出为准）：

- `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . check`：退出码 0，`fails: []`。
- `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . validate`：退出码 0；图报告仍有 6 个未扫描入口，已列入下节图覆盖债。
- `go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . sym m_scheduling_SquadMember --view cards-B292-charter`、对应 proto/web 成员符号：均命中 `anchor: ok`、`status: added`。

Ticket 0 已落新成员类型、成员对象 wire 与成员计数键接线；清队 `ErrNoSlot` 继续本轮的运行时行为仍未在本节点实现，交给实现节点并由 §6.4 测试锁住。

## 7. 三重闸门拍板记录

1. **政策键采用 `sched_running/squad/<squad>/<carrier>`，不另造第三类计数前缀。** 这是
跨 scheduling、agentd、持久 registry 和释放路径的难逆 wire 决定；没有上下文时后人会把
“按成员”误修成独立 `member/` 池；被否方案是保留队级键或新增 `member/<队>/<载体>`，前者
继续放大队级总帽，后者破坏 B156.3 的两类计数边界。不做队级总帽，不做第三类前缀。
2. **队级总并发彻底删除，旧值既不回填成员也不参与准入。** 这是持久存量、HTTP、CLI、Web
四面的不可逆迁移；没有本文上下文时把旧总帽复制到每个成员是最自然的“兼容修复”，但会放大
并发且改变旧语义；被否方案是双字段并存或平均分配旧值。不做双政策、不做旧值翻译。
3. **同 CLI 不合并物理池，`Carrier.Model` 空值与显式值保持身份差异。** 这是载体登记、调度
计数、派发模型覆盖和真实额度之间的跨域身份决定；后人可能为减少配置项而按 CLI 合池；被否
方案是按 CLI 共享物理键，但它会让空模型随 CLI 默认变化与钉死模型错误共享上限。不做 CLI
级合池；账户真实额度探测留 B293/roadmap。
4. **协调者 `ErrNoSlot` 只回填当前请求并继续清队。** 这是清队循环与协调者优先级的难逆行为
决定；没有上下文时“协调者优先”容易被修成协调者失败即全局让路；被否方案是沿用当前
`return processed, nil`，它会阻断其它载体执行者。不做失败即停整轮。

## 8. 移交 plan 附区

以下查证期实现级决定不占冻结条目，交给 breakdown/plan 吸收：

- CLI `--member` 字符串到 `proto.SquadMember` 的具体分隔语法；plan 吸收后在区头标注已吸收日期。
- 成员对象在 scheduling registry 兼容读中的具体 helper/自定义 `UnmarshalJSON` 放置；语义按 §2.1。
- 清队扫描“某空位适配请求”的具体排序/扫描数据结构；语义按 §4，不新造公开入口。

## 9. 图覆盖债与本节点欠账

- 图覆盖债：`codegraph validate` 仍报告 6 个未扫描入口；基线中的调度旧模型节点已由本
  分支视图修改，新 `SquadMember` 三语言节点在 `codegraph/diffs/cards-B292-charter.json`
  中并入视图。调度关键函数仍以本轮实读的 `file:line` 锚引用。
- 欠账：清队 `ErrNoSlot` 继续本轮、CLI 成员政策字符串解析仍交实现节点；本节点已完成成员
  类型/wire/键接线骨架并有本轮 Go/TS 编译与测试证据。`best.json` 结构树不变。

## 10. 拆解节点边界修订记录

- **2026-08-29（B292 breakdown）**：`codegraph/best.json` 中 parent 为空的顶层
  `d_coordination` 覆盖本卡实际触及的 `d_coordination_api`、`d_coordination_task`、
  `d_coordination_cli`；spec/target 文字中的 `d_gateway`、`d_orchestration`、`d_cli`
  是这些既有控制面职责的别名，不是本卡新增领域或依赖方向。`internal/client` 的
  既有客户端面只承接已冻结 wire，不新增 `d_transport` 接缝。该澄清只校正图层级与
  契约面归属，不改变 §1–§9 的冻结语义，不退回 contract。
