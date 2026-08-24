# B239（含并入的 B213、B237）：把「认领」拆成归属锁与运行锁

> 级别：**L3**　档位：**轻档**　路由：contract → breakdown →（单轮）implement → review → acceptance → 图对账/finish
> 状态：**已批准** —— 2026-08-24 用户批准，并授权无人值守推进（原话「spec批准，进入无人值守模式，剩下的你推进就可以了」）。
> 执行器约定：linux-01 的 opencode，模型 `opencode/x-preview-f-free`；不可用退到 codex。
> 承载卡：B239；B213、B237 `card merge` 并入。
> 前置裁决：本 spec **保留** 2026-08-23 decision #1（废除驱动租约 TTL）对**归属**的结论，
> 并指出该裁决未覆盖的另一半——见「与 8-23 裁决的关系」。

## 问题陈述

三张卡是同一句话的三种表现：**「谁在占着这张卡」在系统里有三个互不认识的答案。**

| 答案 | 存在哪 | 寿命 |
|---|---|---|
| 卡的状态列是不是「进行中」 | 账本 `cards.status` | 人尺度（一直到有人移列） |
| `cards.driver_session` | 账本字段 | **永久**：只有持有者本人调用释放才消失 |
| agentd 的在飞集合 | **进程内存**的 map | agentd 一重启即清空 |

三条缺陷各自坐在其中一个错配上：

1. **B237**：裸 `card dispatch`（不带 `--step`）认领时把状态硬写成常量「进行中」
   （`internal/ledger/types.go:14`），而现役唯一的 charter 流没有这一列 → 该路径在
   charter 流上必然失败。
2. **B213**：同一条路径的守卫用「状态 == 进行中」当「已被认领」的代理，对没有任何
   驱动的卡照拒，报文里驱动名是空的（「已被认领（驱动 ）」）。它今天被 B237 掩盖着
   ——ClaimCard 更早一步就失败，走不到这个守卫；**只修 B237 不动守卫，B213 当场重现。**
3. **B239**：`--step` 路径把 `driver_session` 写成**发起方**的身份
   （`cli:user@host#pid`，`cmd/card_node.go:40`），而真正占用的是 agentd 里那个编排
   goroutine（`internal/agentd/cardstep.go:60` 起的 `go func`）。CLI 进程在 HTTP 202
   返回那一刻就死了。释放靠 `internal/ledgerstep/runner.go:98` 的 defer——agentd 一崩
   （B225 那次），defer 不跑，字段永久留着一个不存在的持有者。而两条逃生门都是坏的：
   `card takeover` 把归属发给同样即将死亡的本次 CLI 调用；`card release` 的 SQL 带
   `AND driver_session = ?`，非持有者调用是 no-op **却返回 `{"ok":true}`**。

根因一句话：**`driver_session` 一个字段同时承担了两种寿命差几个数量级的语义**——
「这张卡归谁在推」（人尺度）与「这一轮节点正在跑，别人别插」（运行尺度）。
一个字段无法同时满足两者：给它加超时就违反「人还在时不该被静默接管」，
不加超时就必然泄漏。

另有一个**独立但同源**的伤害面：`--step` 是 202 受理，编排跑在 agentd 后台。
编排体内部的失败都规矩地落成了卡事件（`internal/ledgerstep/node.go:157` 起，
派发失败/取不到报文/裁决解不开/缺产出物一律 `needs_human` + 卡评论），
**唯独取锁失败发生在那片保护之外**（`internal/ledgerstep/runner.go:93`），只写进
`~/.handoff/agentd.log`。而 CLI 打印的是「已受理；进展见 `handoff card wait`」，
`card wait` 只读卡的事件流（`cmd/card_wait.go:96`）——于是失败静默：卡上零事件、
看板毫无动静、`card wait` 干等。B239 实测「连派三次才从日志里 grep 到真因」。

## 与 8-23 裁决的关系（不推翻，是补上它没覆盖的一半）

decision #1（2026-08-23，用户）废除了驱动租约的 TTL，理由逐字：

> 一张卡被主会话领取后，这个主会话的汇报者是**人**，不是随时可能崩溃的东西；
> 另外「租约到期为什么要被别的会话领走」这条本身讲不通。

**这条结论对「归属」成立，本 spec 原样保留**：归属不因时间流逝转移，转移只能显式。

它未覆盖的是：`--step` 路径上的持有者**根本不是人的会话**，是一个几毫秒后就死的
CLI 进程；而该裁决为泄漏留的逃生门（「可解——一条 takeover」，见
`docs/superpowers/specs/2026-08-23-b189-drop-driver-lease-ttl.md` 备注）
经 B239 实测是坏的。所以这里要补的不是「把 TTL 加回归属」，而是**把运行尺度的占用
从归属字段里分出来**，让它拥有自己的、与运行同生共死的寿命。

## 现状事实（本轮工作树读数，交 contract 对工作树复核）

| 事实 | 出处 |
|---|---|
| 裸 dispatch 的守卫判状态、认领写「进行中」 | `cmd/card_dispatch.go:208`、`:213` |
| `ClaimCard` 把状态 CAS 与写驱动并进同一事务 | `internal/ledger/move.go#Store.ClaimCard` |
| `ClaimDriver` 只写驱动、不动状态；注释明写「非空的他会话永不因时间流逝自动释放」 | `internal/ledger/tasks.go#Store.ClaimDriver` |
| `ReleaseCard` 带 `AND driver_session = ?`，非持有者是 no-op 且不报错 | `internal/ledger/move.go#Store.ReleaseCard` |
| `TakeoverCard` 无条件覆盖并落 `driver_takeover` 事件（payload from/to） | `internal/ledger/tasks.go#Store.TakeoverCard` |
| CLI 的驱动身份带 pid；actor 不带 pid | `cmd/ledgercli.go:47`（actor）、`cmd/ledgercli.go:60`（session） |
| `--step` 把 `ledgerSession()` 作为 actor 发给 agentd，agentd 拿它当驱动会话 | `cmd/card_node.go:40`、`internal/agentd/cardstep.go:46` |
| 编排取锁失败直接 return，不写任何卡事件 | `internal/ledgerstep/runner.go:93` |
| 编排体内部一切异常都转 `needs_human` + 卡评论 | `internal/ledgerstep/node.go:157` 起 |
| agentd 在飞集合是进程内 map，重启即清空（注释标为刻意取舍） | `internal/agentd/server.go:166`、`internal/agentd/cardstep.go:26` |
| 看板 conflict 徽标的判据是「状态 == 进行中」，charter 流下恒不触发 | `internal/agentd/ledgerapi.go:187` |
| 账本 Store 已有可注入时钟，注释点名用于「lease 过期判定这类时序逻辑」 | `internal/ledger/store.go:38` |
| 账本域的 schema 只有 `CREATE TABLE IF NOT EXISTS`，无迁移助手；task 域有逐列 ALTER 先例 | `internal/ledger/store.go:202`；`internal/store/store.go:227` |
| 同仓已有一套可用的 TTL 租约先例（镜像子系统的 leader 选举） | `internal/ledgermirror/mirror.go:170#AcquireMirrorLease` |
| 现存泄漏实例：B231 状态「已完成」仍持有 `cli:...#60676`，认领时刻停在 08-24 18:20 | `handoff card show B231` 读数 |
| charter v9 states 无「进行中」 | `handoff workflow show charter` 读数 |

## 方案

### 采纳：一分为二——归属锁（人尺度）+ 运行锁（运行尺度）

**归属锁**：回答「这张卡归谁在推」。

- 持有者是**人尺度身份**（`cli:user@host` 这一档，**不带 pid**）——同一个人换个进程
  仍是持有者，`release` 因此第一次真正可用。这直接消灭 B239 的「takeover 发给将死
  进程」与「release 恒 no-op」。
- **不因时间流逝转移**（8-23 裁决原样保留），转移只能显式 `card takeover`，落事件。
- **不再兼任状态**：认领归属**不改卡的状态列**。裸 dispatch 从此只认领、只派发，
  不把卡推去「进行中」——B237 消失；守卫改判归属锁而不是状态列——B213 消失。

**运行锁**：回答「这一轮节点正在跑吗、谁在跑」。

- 持有者是**承载编排的那次运行**（agentd 里那个 goroutine 的运行标识），不是发起方。
- **带租期**：运行期间由编排本身续租；运行结束（成功或失败）立即释放。
- **租期到了只意味着"上一轮已经不在了，可以开下一轮"，不意味着卡易主**——它不表示
  所有权，因此不触碰 8-23 裁决反对的那个失败模式。
- **抢占落卡事件**；被抢者续租失败即**停止对这张卡的一切写**（不移列、不落裁决、
  不挂附件），并在卡上留一条说明。远端任务不强杀——那会留下一个没人收的任务。
- 权威落在账本，不在 agentd 内存。agentd 的在飞 map 降格为本进程的快速去重。
  副产品：看板与 CLI 从此看得见「这张卡的哪个节点正在哪台机器上跑、租期到几点」。

**失败可见性**（与选哪把锁无关，必做）：编排入口方法里、`RunOnce` 之外的一切失败
（节点解不开、会话未设置、运行锁取不到）**必须与 `RunOnce` 内部同形地落卡**——
`needs_human` + 一条带原文的卡评论。判据是 `card wait` 看得见。

### 弃选

| 方案 | 弃选理由 |
|---|---|
| **给 `driver_session` 加回 TTL（单锁模型）** | 推翻 8-23 decision #1：人还在时被静默接管，两个驱动同时在飞且互不知情——当时判定为比卡住更坏，本 spec 不重新打开这个判断 |
| **只修逃生门**（takeover 不发给将死进程 + release 不假成功 + 失败落卡） | 泄漏仍会发生，只是从「无解」变成「每次手动一条命令」。而泄漏的成因是身份错配，不是缺工具；留着它等于把同一个坑每周踩一次 |
| **持有者改成 agentd 实例 id + 重启时自清** | 修掉本次泄漏的主因，但换机器、实例名变更、进程被 SIGKILL 后重装等形态仍留死锁；且它把"清理"绑在启动时刻这一个点上，agentd 长期不重启就永远不清 |
| **运行锁只留在 agentd 内存里（今天的形态）** | 重启即清是它唯一的"释放"手段，且跨实例、跨机器不可见；卡上看不到「正在跑」，`card wait` 也照不出来。可观测性是本卡伤害面的一半，留在内存里等于不修 |
| **在 `cards` 表上加运行锁字段** | 账本域今天没有迁移助手（只有 `CREATE TABLE IF NOT EXISTS`），加列要引入逐列 ALTER 那套；而运行锁天然是「一条运行记录」，独立表零迁移风险，且给 B225 的恢复留了落点 |

## 用户故事

1. 作为协调者，我 `card dispatch <卡> --step <节点>` 之后 agentd 崩了/我合了盖，
   重新派发这张卡时**不会**被一个不存在的持有者挡住——上一轮的运行锁已随租期失效。
2. 作为协调者，运行锁被挡时报文告诉我：**谁在跑、哪个节点、租期还剩多久**，
   而不是一句「可能被并发抢先」。
3. 作为协调者，`card dispatch --step` 的失败（含取不到运行锁）**出现在卡的事件流上**，
   我 `card wait` 就能看见，不必去 grep `agentd.log`。
4. 作为协调者，我领了一张卡去开会两小时，回来它**还是我的**——归属不因时间转移。
5. 作为协调者，`card release <id>` 在我确实是持有者时真的释放；**我不是持有者时它
   明确告诉我持有者是谁并失败**，不再返回一句骗人的 `{"ok":true}`。
6. 作为协调者，`card takeover <id>` 之后归属真的落在**我这个人**名下，
   下一条 `card release` 能把它交还——不再是发给一个转瞬即逝的进程。
7. 作为协调者，裸 `card dispatch <id>`（不带 `--step`）在 charter 流上**能跑通**，
   并且**不把卡挪去「进行中」**——它只认领归属并派发。
8. 作为协调者，一张没有任何驱动的卡执行裸 dispatch **不会**被告知「已被认领（驱动 ）」。
9. 作为看板用户，卡上「有任务失败」的冲突徽标按**运行锁**判定，
   不再依赖一个 charter 流里根本不存在的状态列。
10. 作为协调者，两个会话同时对同一张卡派同一个节点时，**只有一个跑得起来**，
    另一个被干净拒绝并被告知持有者——这条保护今天由 `driver_session` 提供，
    改造后由运行锁提供，**不许在换锁的过程中丢失**。

## 契约语义与接缝（L3）

**跨越的子系统**（顶层领域 = 子系统，见架构法第一条）：`d_ledger`（`internal/ledger`
与 `internal/ledgerstep`）与 `d_coordination`（`internal/agentd` 与 `cmd`）。

**契约语义（定语义，签名归 contract 节点）**：

1. **两把锁都归账本所有**（架构法第五条：规则归数据所有者）。归属锁与运行锁的取得、
   续租、释放、抢占判定，全部收口在 `d_ledger` 的 Store 契约面上；`d_coordination`
   只调用，不自己算过期、不自己判持有。
2. **依赖方向不变且单向**：`d_coordination → d_ledger`。不新增反向依赖，
   agentd 不成为「谁在跑」的真相源，它进程内的在飞集合降为本进程去重的加速结构。
3. **归属锁语义**：持有者是人尺度身份；非空且非本人时拒绝取得；不随时间失效；
   释放只对持有者生效且**非持有者调用是可见的失败**（语义变更点，今天是静默 no-op）；
   转移显式且落事件。
4. **运行锁语义**：持有者是承载编排的一次运行；带租期与续租；租期过期即可被新的运行
   取得，取得时落卡事件；**续租失败即失去对该卡的写权**。运行结束必释放。
5. **事件契约**：运行锁的抢占与节点失败必须落在卡的事件流上（`card wait` 的唯一通道）。
   抢占复用既有的驱动转移事件类型并在 payload 标明原因，不新增词表位除非 contract
   判定复用会污染既有消费者。
6. **CLI 语义契约**（对人的契约面）：`card release` 的退出语义变更；裸 `card dispatch`
   不再改变卡的状态列。两条都是可观察行为变更，须在 review 的对账清单里点名。

**接缝清单**（两条，符号 + 调用方）：

| 缝 | 符号 | 调用方 | 覆盖什么 |
|---|---|---|---|
| 1 | `internal/ledger` 的锁契约面（存量 `Store.ClaimCard` / `Store.ClaimDriver` / `Store.ReleaseCard` / `Store.TakeoverCard`，加运行锁新符号） | `cmd/card_dispatch.go:213`、`cmd/card_driver.go:26`、`cmd/card_driver.go:50`、`internal/ledgerstep/runner.go:93`、`internal/ledgerstep/runner.go:99`、`internal/agentd/ledgerapi.go:187` | 归属与运行两套语义的全部判定：取得/拒绝/释放/非持有者释放/转移/过期可抢。过期判定用 `internal/ledger/store.go:38` 已有的可注入时钟，**不许靠真实等待** |
| 2 | `internal/ledgerstep#StepRunner.Run` | `internal/agentd/cardstep.go:45`（agentd 编排）、CLI 经 HTTP 到同一处 | 编排侧行为：取不到运行锁时卡上留事件；运行期间续租；回合结束（含失败）释放；续租失败后停止对卡的写 |

两条缝都是有生产调用方的导出符号，不是为覆盖分支抽出的私有函数（假缝禁令已逐条核）。
缝 1 是最高可测缝——不起 agentd、不真派发即可覆盖全部所有权判定；缝 2 只覆盖缝 1
表达不了的编排时序。

## 实现决定

1. **运行锁落成账本里一张独立记录**，不动 `cards` 表结构（账本域无迁移助手，见现状
   事实）。记录至少能回答：哪张卡、哪个节点、谁持有、租期到几点。
2. **租期与续租沿用已被验证过的节奏**：租期 5 分钟、续租间隔 2 分钟。这两个数字是
   2026-08-23 B196 在真卡真回合上观察确认过的（心跳间隔 2 分 00 秒，远小于 TTL），
   不是新猜的。具体常量位置归 plan。
3. **续租随回合上下文取消而停**，不留后台 goroutine 泄漏。
4. **续租失败不强杀远端任务**，只停止对卡的写并落一条卡事件——硬中止会留下一个没人
   收的任务，那比归属丢失更坏（沿用 B196 的同一条取舍，收紧了"不写卡"这一半）。
5. **归属身份去掉 pid**：驱动身份从 `cli:user@host#pid` 降到 `cli:user@host`。
   pid 的原始理由（区分同机并发会话）由**运行锁**承担，归属层不再需要它。
6. **`ClaimCard` 的状态 CAS 与归属认领解耦**：认领不再附带状态转移。
   `StatusDoing` 常量本身不删（骨架锚点，别的流可能用），但生产路径不再写它。
7. **看板 conflict 徽标改判运行锁**（`internal/agentd/ledgerapi.go:187`）。
8. **不引入"驱动会话存活性探测"**（沿用 8-23 裁决的方向：显式，而不是换一个更聪明的猜测）。

## 测试决定（接缝清单见上）

必须覆盖的断言：

**缝 1（账本锁契约面）**

1. 归属非空且非本人 → 取得被拒，报文含持有者；**心跳/认领时刻很久以前也照拒**
   （这条钉死 8-23 裁决，防止 TTL 从归属侧偷偷回流）；
2. 同一人（不同 pid）取得归属 → 放行（幂等与换进程重试路径依赖它）；
3. 非持有者调用释放 → **可见失败**，且归属未被改动（今天此处是静默 no-op + 假成功，
   这条是本卡的核心行为反转）；
4. 转移后归属变更且事件流里有转移事件，payload 带 from/to；
5. 运行锁：租期内他人取得被拒；**用注入时钟推过租期后**他人取得成功并落抢占事件；
6. 运行锁释放后立即可被取得，不必等租期；
7. 归属认领**不改变卡的状态列**（钉 B237/B213 的根因）。

**缝 2（编排入口）**

8. 运行锁取不到 → `Run` 返回错误**且卡上出现事件**（`needs_human` + 评论含原因原文）；
9. 回合结束（含失败路径）→ 运行锁已释放；
10. 长回合期间续租至少发生一次（用注入时钟或续租钩子断言，**不真等 5 分钟**）；
11. 续租失败后编排不再对卡做写（移列/裁决/挂附件一律不发生），且卡上留有说明事件。

变异测试要打的点：把「非持有者释放返回成功」改回去，第 3 条必须变红；
把归属的过期判据加回去，第 1 条必须变红。

## Out of Scope

| 不做 | 分类 |
|---|---|
| 编排状态持久化与 agentd 重启后接着跑完在飞节点 | 本期不做、后续要做——**已有卡 B225**，不重复落 roadmap。本卡修好后它从「永久挡死」降级为「丢一轮」 |
| 把编排搬到常驻机（笔记本退化为遥控器） | 本期不做、后续要做——牵扯控制台、鉴权、配置、部署形态，是另一个量级；落 roadmap |
| Web 控制台加释放/接管入口与运行锁展示 | 本期不做、后续要做（今天 Web 只读 `driver_session` 展示，见 `web/src/app/cards/CardDrawer.tsx:369`）；落 roadmap |
| 给归属锁加任何形式的自动过期 | **永不做**——8-23 decision #1 的直接结论 |
| 给 `card wait` 加认领 | **永不做**——观察不该变成抢占（B196 已裁） |
| 驱动会话存活性探测 | **永不做**——把 TTL 换成另一种猜测 |
| 改 `driver_heartbeat_at` 列名 | **永不做**——改列名要迁移，收益只有名字好看 |
| 删除裸 `card dispatch` 路径 | 本期不做——本卡把它修对；是否长期保留是产品面问题，不在此判 |
| `handoff` skill / `product-backlog` 里关于"裸 dispatch 必然失败，不要用"「驱动权泄漏 CLI 侧无解」两处文案的回流 | 归 finish 节点的文档对齐，不占实现范围；**但必须做**，否则文档会继续教已被修好的绕行 |

## 备注

- **图覆盖债：无。** 本 spec 引用的符号 `Store.ClaimCard` / `Store.ClaimDriver` /
  `Store.ReleaseCard` / `Store.TakeoverCard` / `StepRunner.Run` / `NodeStep.RunOnce` /
  `Server.startCardStep` / `Store.MarkNeedsHuman` 均在 `codegraph` baseline 命中。
  （查询时用过的 `ReleaseDriver`/`TakeoverDriver`/`HeartbeatDriver` 未命中，
  前两个是我查询时名字写错，第三个是 2026-08-23 随 TTL 废除一并删除的历史符号，
  两者都不构成覆盖债。）
- **定级复核（按定稿范围）**：改动跨 `d_ledger` 与 `d_coordination` 两个子系统的
  契约面——账本锁的导出面语义变更、CLI 命令的可观察行为变更、agentd 从"真相源"
  降为消费者——判 **L3**。
- **选档复核**：两个子系统里只有 `d_ledger` 侧工作量明显超过流程固定成本（约 70 分钟），
  `d_coordination` 侧（续租协程、失败落卡、两条 CLI 报文、一处徽标判据）只有几十分钟
  且强依赖账本锁的**真实行为**——扇出后它只能对着骨架 mock 验收，并行收益抵不过多一轮
  integrate 的固定成本。故判**轻档**：契约冻结照做（跨子系统契约面必须冻），实现归一轮。
- 三卡关系：B239 为承载卡（优先级高、含可观测性那一半），B213 与 B237 并入。
  B213 今天不可复现是**被 B237 掩盖**，不是已修——修复方案里第 7 条断言专钉这一点。
