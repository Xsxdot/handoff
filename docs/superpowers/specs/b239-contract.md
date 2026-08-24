# B239 契约增量：把「认领」一分为二——归属锁（人尺度）+ 运行锁（运行尺度）

**状态**：contract 轮冻结稿（2026-08-25）。**用户批准的是上游 spec
（`docs/superpowers/specs/2026-08-24-b239-claim-lock-split.md`，2026-08-24 已批准），
不是本文档**——本文档是 spec 契约语义到现状代码的翻译，签名与行为边界以本文为准。
**冻结物**：本文档、`codegraph/target.json`、`codegraph/diffs/cards-B239-charter.json`
与本提交的 Ticket 0 骨架，随本提交冻结。
**本节点**：charter / contract；交棒：`breakdown`。

上游 spec 的「现状事实」表已由本节点逐条对工作树复核，全部命中；两处行号漂移
（`AcquireMirrorLease` 的真实定义处、charter v9 线上读数不可复验）见 §1.4。

## 1. 现状查证

### 1.1 归属链路现状

| 契约事实 | 现状证据 |
| --- | --- |
| `ClaimCard(id, to, expect, session)` 把状态 CAS 与写驱动并进同一事务 | `internal/ledger/move.go#Store.ClaimCard`（`internal/ledger/move.go:145-165`） |
| 裸 dispatch 守卫判「状态==进行中」，报文拼 `DriverSession`（无驱动卡照拒，B213） | `cmd/card_dispatch.go:208-210` |
| 裸 dispatch 认领硬编码转入「进行中」（charter 流必然失败，B237） | `cmd/card_dispatch.go:213` |
| 派发失败回滚 = MoveCard 回原列 + ReleaseCard | `cmd/card_dispatch.go:236-242` |
| `ReleaseCard` 带 `AND driver_session = ?`；0 行时记日志后返回 nil——非持有者 no-op 且假成功 | `internal/ledger/move.go#Store.ReleaseCard`（`internal/ledger/move.go:175-201`，no-op 分支在 `:188-191`） |
| `TakeoverCard(id, session, actor)` 无条件覆盖 + 落 `EvDriverTakeover` payload `{from,to}` | `internal/ledger/tasks.go#Store.TakeoverCard`（`internal/ledger/tasks.go:122-145`） |
| actor 不带 pid、session 带 pid；`--step` 把带 pid 的 session 当 Actor 发给 agentd | `cmd/ledgercli.go#ledgerActor`（`cmd/ledgercli.go:47-53`）、`cmd/ledgercli.go#ledgerSession`（`cmd/ledgercli.go:56-62`）、`cmd/card_node.go:40` |
| agentd 把请求 actor 当驱动会话装配 StepRunner，编排跑在后台 goroutine | `internal/agentd/cardstep.go#Server.startCardStep`（`internal/agentd/cardstep.go:41-64`，Session 注入 `:46`，goroutine `:59-62`） |
| 编排取锁失败（`ClaimDriver`）直接 return，不落任何卡事件 | `internal/ledgerstep/runner.go#StepRunner.Run` 的 `internal/ledgerstep/runner.go:93-96` |
| 编排体内部异常统一转等人（needs_human + 评论），唯独入口失败在保护之外 | `internal/ledgerstep/node.go:157-178`；`haltForHuman` 三件套 `internal/ledgerstep/node.go:66-80` |
| 编排结束 defer `ReleaseCard`——归属随回合消亡，正是两种寿命被压进一个字段的实锤 | `internal/ledgerstep/runner.go:98-104` |
| 在飞集合是进程内 map，重启即清（文件头注明刻意取舍） | `internal/agentd/cardstep.go:80-100`、`:10-13` |
| 看板 conflict 徽标判据是「状态==进行中」 | `internal/agentd/ledgerapi.go:186-196` |
| `StatusDoing` 生产路径消费者仅四处：dispatch 守卫/认领/回滚 + 徽标 | grep 全仓非测试命中：`internal/ledger/types.go:14`（定义）、`cmd/card_dispatch.go:208,213,239`、`internal/agentd/ledgerapi.go:187` |

### 1.2 依赖库既成行为（与签名同等承重）

| 既成行为 | 出处 | 对本卡的约束 |
| --- | --- | --- |
| Store 可注入时钟：`s.now` 非空则 `timeNow()` 返回假时钟，生产回退 `time.Now` | `internal/ledger/store.go:35-47` | 运行锁过期判定必须走 `s.timeNow()`，测试推时间不真等 5 分钟（spec 测试决定 5/10） |
| 全部账本写经单一 `mutate` 事务串行（PG advisory lock / SQLite 单连接）；事件经 sink 提交后推送 | `internal/ledger/store.go:152-185` | 取得/续租/释放/抢占各为一次 mutate；抢占事件与覆盖写必须同事务 |
| 时间编解码：PG timestamptz / SQLite RFC3339Nano 文本 | `internal/ledger/store.go:124-146` | `card_run_locks` 两方言 DDL 各写一份，走 `tval/toTime` |
| schema 只有 CREATE TABLE IF NOT EXISTS，无迁移助手；新表零迁移风险 | `internal/ledger/store.go:195-197,198-333` | 运行锁落独立表 `card_run_locks`，不动 `cards` 列（spec 弃选表第 5 行） |
| TTL 租约先例：单行表 CAS、过期即接任、注入时钟判定 | `internal/ledger/mirror.go#Store.AcquireMirrorLease`（`internal/ledger/mirror.go:92-127`） | 运行锁语义照此节奏扩展到多行（每卡一行） |
| HTTP 错误映射收口：ErrNotFound→404，ErrCASConflict/ErrBadState 等→409 | `internal/agentd/ledgerapi.go:65-76` | 归属拒绝用 ErrCASConflict 包装 → CLI 退出码非零、HTTP 409，无需新增哨兵 |
| web 渲染卡事件 payload 时按 body/reason/text 优先级泛化取值 | `web/src/app/cards/CardDrawer.tsx:125-138` | 抢占事件 payload 加 `reason` 人读短句即可在看板直读；不新增前端代码 |

### 1.3 对侧常量执法

| 常量 | 生产者 | 消费者 | 结论 |
| --- | --- | --- | --- |
| `EvDriverTakeover="driver_takeover"` | 仅 `TakeoverCard`（`internal/ledger/tasks.go:133`） | **零程序消费者**（Go/web 全仓 grep 非测试零命中；web 泛化渲染不按类型分支） | 活跃事实源但无下游耦合；运行锁抢占复用它 + `reason` 字段不污染任何消费者（spec 契约语义 5 的判定条件成立） |
| `StatusDoing="进行中"` | 本卡改造后将**零生产写入者** | 改造前仅 §1.1 末行四处，全部在本卡范围内 | 常量保留为骨架锚点不删（spec 实现决定 6）；改造后它退化为纯词表位 |
| `driver_heartbeat_at` 列 | ClaimCard/ClaimDriver/TakeoverCard 写认领时刻 | `cmd/status.go:95-99`、`web/src/app/cards/CardDrawer.tsx:367-370` 只读展示 | 列名不改（spec Out of Scope）；归属侧继续写「认领时刻」语义 |
| `ErrCASConflict` | ClaimCard/ClaimDriver 拒绝路径 | `ledgerErr` 映射 409（`internal/agentd/ledgerapi.go:69-72`） | 归属/运行两类拒绝都复用它做错误识别，报文另带持有者原文 |
| `proto.CardStepReq.Actor` | CLI `card_node.go:40`、看板 legacy fallback `web:<addr>`（`internal/agentd/ledgerapi.go:443-447`） | agentd 日志 + Dispatcher/StepRunner 装配（`internal/agentd/cardstep.go:46-48`） | 字段不删不改名；语义收窄为「发起方归属身份」，不再兼任运行标识 |

### 1.4 上游 spec 引用的勘正与存疑项

- **`AcquireMirrorLease` 出处漂移**：spec 写 `internal/ledgermirror/mirror.go:170#AcquireMirrorLease`，
  那是消费方调用点；定义在 `internal/ledger/mirror.go:94`。符号存在，不构成覆盖债。
- **「charter v9 states 无进行中」未在本工作树复验**：该读数出自 spec 作者的
  `handoff workflow show charter` 实测；本回合受平台禁令（不调 handoff CLI）只能复核
  仓内种子 `deploy/workflows/charter-v4.json`（其节点名不含「进行中」，方向一致）。
  此项标「引上游读数」。这不影响本卡正确性：守卫改判归属锁后，状态列内容不再参与认领判定。

## 2. 契约增量

### 2.1 归属锁面（`d_ledger`，Store 门面）

**身份契约**：归属持有者是人尺度身份，格式沿用现状 `ledgerActor()` 的
`cli:<user>@<host>`（`cmd/ledgercli.go:47-53`）。**pid 后缀从归属链路消失**
（spec 实现决定 5）：`ledgerSession()`（`cmd/ledgercli.go:56-62`）的全部生产调用点
（`cmd/card_driver.go:17,41`、`cmd/card_dispatch.go:213,217,240`、`cmd/card_node.go:40`）
迁移到 `ledgerActor()` 后，实现轮删除 `ledgerSession()`。看板遗留 fallback
actor（`internal/agentd/ledgerapi.go:446` 的 `web:<r.RemoteAddr>`）收敛为 host 档
`web:<host>`——RemoteAddr 带端口会让同一浏览器两次点击拿到两个不同「身份」，
第二次会被自己的旧归属挡住；收敛到 host 与 `cli:user@host` 同档。

**`ClaimCard` 收窄为唯一归属认领入口**：

```go
func (s *Store) ClaimCard(id, owner string) error
```

现状签名 `ClaimCard(id, to, expect, session string)`（`internal/ledger/move.go:145`）
的 `to/expect` 两参删除。规则（一次 mutate 事务内完成）：

1. `id` 不存在 → 可 `errors.Is` 识别的 `ErrNotFound`；
2. 卡处于终态（`已完成`/`终止`）→ `ErrBadState`（今天这层拒绝由顺带的 moveCardTx 提供，
   解耦后必须显式补回，否则裸 dispatch 能给终止卡认领）；
3. `owner` 为空串 → 参数错误（不许静默清空归属）；
4. 已有非空他主且 ≠ `owner` → 错误 wrap `ErrCASConflict`，报文含持有者标识；
   **无论对方认领时刻多近或多久以前，照拒**——钉死 8-23 decision #1（防 TTL 从归属侧回流）；
5. 同主重入 → 幂等成功（换进程重试路径依赖它）；
6. 成功写 `cards.driver_session = owner`、`driver_heartbeat_at = 认领时刻`（沿用现状
   `move.go:158-159` 的写法）；
7. **不改状态列、不落状态转移事件**——认领与 CAS 彻底解耦（spec 实现决定 6）；
   认领本身也不落新事件（无新词表位；归属可从 `driver_session` 直读）。

**`ClaimDriver` 删除**：`internal/ledger/tasks.go#Store.ClaimDriver`
（`internal/ledger/tasks.go:94-117`）解耦后与收窄版 `ClaimCard` 完全同义，双符号等价入口
必然后漂移。唯一生产调用方 `internal/ledgerstep/runner.go:93` 改调 `ClaimCard(cardID, r.Session)`。
「驱动」这个词根本身就是要消灭的混淆源（B239 根因陈述）。

**`ReleaseCard` 语义反转**（签名不变 `(id, session string) error`）：

1. 卡不存在 → `ErrNotFound`（今天是静默 nil，`move.go:188-191`——行为变更点）；
2. 无主卡 → 幂等成功（空转不是错误）;
3. 自己持有 → 清 `driver_session`+`driver_heartbeat_at`，成功；
4. **他主持有 → 可见失败**：错误 wrap `ErrCASConflict` 且报文含当前持有者标识，
   归属未被改动。今天此处是静默 no-op + CLI 打印 `{"ok":true}`
   （`cmd/card_driver.go:26-32`），这是本卡的核心行为反转（spec 用户故事 5）。
5. 原 doc 注释「非持有者调用是无操作而不是报错」随实现一并改写。

**`TakeoverCard` 不动**：签名与无条件覆盖语义保持
（`internal/ledger/tasks.go:122-145`），落 `EvDriverTakeover` payload `{from,to}` 形状不变；
变的只有调用方身份档（§2.4）。它是归属唯一的显式转移通道，永不过期（8-23 裁决）。

### 2.2 运行锁面（`d_ledger` 新符号，Ticket 0 空壳已在 `internal/ledger/runlock.go`）

```go
type RunLock struct {
    CardID     string    `json:"card_id"`
    Node       string    `json:"node"`
    Holder     string    `json:"holder"`
    AcquiredAt time.Time `json:"acquired_at"`
    ExpiresAt  time.Time `json:"expires_at"`
}

func (s *Store) AcquireRunLock(cardID, node, holder string, ttl time.Duration) (RunLock, bool, error)
func (s *Store) RenewRunLock(cardID, holder string, ttl time.Duration) (bool, error)
func (s *Store) ReleaseRunLock(cardID, holder string) error
func (s *Store) RunLockOf(cardID string) (RunLock, bool, error)
func (s *Store) AllRunLocks() ([]RunLock, error)
```

**存储**：独立表 `card_run_locks`，主键 `card_id`（一卡至多一条在飞运行，DB 层兜底
story 10），DDL 已随 Ticket 0 进 `ensureSchema` 两方言分支
（PG `internal/ledger/store.go:250-253`、SQLite `:316-319`），零迁移。

**`AcquireRunLock` 规则**（一次 mutate 事务内完成判定+写入+事件；时钟一律 `s.timeNow()`）：

1. `cardID` 不存在 → `ErrNotFound`；
2. 无行 → INSERT（`acquired_at=now`、`expires_at=now+ttl`），acquired=true，返回新行；
   **首次取得不落转移事件**（运行开始/结束若都落事件，长回合高频派发的卡会被噪声淹没；
   可观测性由读面 §2.5 与抢占事件承担）；
3. 有行且 holder 相同 → 刷新 `expires_at=now+ttl`（`acquired_at` 不动），acquired=true；
4. 他主持有且 `expires_at > now` → 拒绝：acquired=false，返回现存行
   （谁在跑、哪个节点、租期到几点——story 2 的报文素材），不加改动、不算错误；
5. 他主持有且 `expires_at <= now` → **抢占**：覆盖 node/holder/acquired_at/expires_at，
   acquired=true，同事务落 `EvDriverTakeover`（actor=新 holder，
   payload `{"from":旧holder,"to":新holder,"reason":<人读中文短句>}`）。

**`RenewRunLock`**：`UPDATE expires_at = now+ttl WHERE card_id=? AND holder=?`；
0 行 → `(false, nil)`——false 是「已失去写权」的权威信号；≥1 行 → `(true, nil)`。

**`ReleaseRunLock`**：`DELETE … WHERE card_id=? AND holder=?`；非持有者/无行都是成功 nil。
不对称说明：归属释放的非持有者是**可见失败**（人要的是确认），运行释放的非持有者是
no-op（清理是尽力而为，失去信号已在 Renew；被抢者的 defer 不该在日志里炸出假警报）。

**`RunLockOf` / `AllRunLocks`**：原样返回行（第二个返回值/行存在性），
**不过滤过期**——「是否正在跑」= 行存在 && `ExpiresAt.After(now)`，消费侧用同一时钟判。
读面不落 mutate，不走写事务。

**租期常量值冻结**（位置归 plan）：TTL = 5 分钟、续租间隔 = 2 分钟
（spec 实现决定 2，B196 真机实测节奏，不是新猜的数字）。

### 2.3 编排缝（`internal/ledgerstep/runner.go#StepRunner.Run`，缝 2）

`StepRunner` 字段契约（Ticket 0 已落）：`Session` 收窄为发起方**归属身份**；
新增 `RunHolder string` = 本次编排运行标识，由 agentd 每次启动编排时生成
（全局唯一、含机器线索便于人读取证；精确构造归 plan/implement），
空值时运行锁路径必须拒绝放行而不是静默退化（`internal/ledgerstep/runner.go:23-45`）。

`Run` 的法定顺序与行为：

1. `nodeFor` 失败 → **先落卡再返回错误**（今天 `runner.go:59-63` 直接 return）；
2. `Session == ""`（会话未设置）→ 同上落卡（今天 `runner.go:88-92` 直接 return）；
3. `AcquireRunLock(cardID, nodeName, r.RunHolder, ttl)`：
   - acquired=false → 落卡（needs_human + 评论：谁在跑/哪个节点/租期到几点 + 原因原文）
     再返回错误——story 3 的主场景，今天 `runner.go:93-96` 的静默 return 是 B239 伤害面的另一半；
   - acquired=true 且发生抢占 → 抢占事件已由缝 1 落账，编排不得重复落；
4. `ClaimCard(cardID, r.Session)` 认领归属（持久）：他主或终态拒绝 → 同样落卡再返回错误
   （spec 失败可见性清单列了三项，本条把「归属被拒」并入同一类处理——同为编排入口、
   `RunOnce` 之外的失败，漏掉它等于留半个静默面）；
5. 续租循环：每 2 分钟一次 `RenewRunLock`，随回合 ctx 取消而停，不留泄漏 goroutine
   （spec 实现决定 3）。**续租失败（false）后的写权边界**：
   - 允许：那条说明性 comment（一次性，EvComment，body 说明已被接跑、本回合停止写卡）；
     远端任务的继续等待与归档（远端任务不强杀，spec 实现决定 4）；
   - 禁止：移列、落裁决（RecordReviewVerdict）、挂附件、打/撤等人标记——
     一切其余卡写一律不得再由本回合发起（story 11 / spec 测试决定 11）；
   - 不打 needs_human：卡已在新持有者手里，前持有者的红旗只会制造假「需要你」。
   达成机制（gate 钩子还是别的）归 plan，可观察边界以此为准；
6. defer `ReleaseRunLock(cardID, r.RunHolder)`——成功、失败、被抢后一律执行
   （今天 `runner.go:98-104` 的 defer `ReleaseCard` **删除**：归属持久化后不再随回合消亡）。

**落卡的同形要求**：上述每个落卡动作 = `AddComment`(kind 普通，body 含原因原文) +
`MarkNeedsHuman`(reason)（`internal/ledger/events.go:264-277`、`internal/ledgerstep/node.go:70-80`
既有形状），判据是 `card wait` 看得见——它只 Follow 卡的事件流（`cmd/card_wait.go:96`）。

纯人工列（`!node.Dispatch`）提前返回路径（`runner.go:76-80`）不取运行锁、不认领归属，维持现状。

### 2.4 CLI 面（对人契约，两条可观察行为变更须进 review 对账清单）

| 命令 | 变更 |
| --- | --- |
| `card release <id>`（`cmd/card_driver.go:12-34`） | 传 `ledgerActor()`；**非持有者时命令失败**（非零退出，stderr 含当前持有者），成功仍打印 `{"ok":true}`。【变更点一】 |
| `card takeover <id>`（`cmd/card_driver.go:36-58`） | 传 `ledgerActor()`；归属落到人，下一条 release 能交还（story 6）。payload `to` 从此是人尺度标识。 |
| 裸 `card dispatch <id>`（`cmd/card_dispatch.go:185-245`） | 守卫 `:208-210` 改判归属锁（他主才拒，报文含持有者；**无驱动的卡照常放行**，story 8）；`:213` 改 `ClaimCard(id, ledgerActor())`，**不转状态列**（story 7，charter 流从此跑得通）；失败回滚 `:236-242` 只退归属（删 MoveCard 回退——没有状态转移要回退了）。【变更点二】 |
| `card dispatch --step`（`cmd/card_node.go:22-52`） | `Actor` 传 `ledgerActor()`（`:40`）；CLI 仍是 202 即返，运行互斥感知不到 CLI 死活——那本来就是错的。 |

### 2.5 看板徽标与 agentd 装配

- conflict 徽标判据（`internal/agentd/ledgerapi.go:186-196`）：
  `view.Status == ledger.StatusDoing` → **「存在未过期运行锁（`AllRunLocks`/`RunLockOf` +
  `ExpiresAt > now`）且 LatestTaskStates 最新 task 态 == failed」**。状态列从此与徽标无关
  （story 9）。需要跨进程可见时读账本，不在 agentd 内存里找真相（spec 契约语义 1/2）。
- `startCardStep` 的进程内在飞 map（`internal/agentd/cardstep.go:80-100`）**保留为快速去重**，
  权威判定让位给账本运行锁（spec 契约语义 2：降格，不删除）。
- `req.Actor` 语义收窄为发起方归属身份（§1.3 末行）；运行标识由 agentd 现算注入
  `StepRunner.RunHolder`。HTTP 受理流程不变：202 只代表受理
  （`internal/agentd/ledgerapi.go:403-497`）。

### 2.6 依赖方向

不新增任何跨子系统依赖方向：`d_coordination → d_ledger` 单向（cmd 与 agentd 只调
`ledger.Store` 门面）；运行锁全部判定住在 `internal/ledger`（含 `internal/ledgerstep`，
同属 `d_ledger`）。`codegraph/target.json` 本提交把 `d_gateway → d_ledger` 的 entries
补为 `["ledger.Store","ledgerstep.StepRunner"]`，把协调侧可触面钉死在门面 + 编排器两个入口。

## 3. 原子化冻结清单

每行一个可独立判 pass/fail 的断言：

**缝 1（账本锁面）**

1. `ClaimCard` 对不存在卡返回 `ErrNotFound`。
2. `ClaimCard` 对终态卡（已完成/终止）返回 `ErrBadState`。
3. `ClaimCard` 在他主持有时拒绝，错误可 `errors.Is(ErrCASConflict)` 且报文含持有者标识。
4. 他主持有时，无论其认领时刻多久以前，`ClaimCard` 照拒（注入时钟推远仍拒）。
5. 同一 owner 重入 `ClaimCard` 成功（幂等）。
6. `ClaimCard` 成功后 `cards.status` 与认领前相同（一字节都不动）。
7. `ClaimCard` 成功后 `driver_session=owner` 且 `driver_heartbeat_at` 为本次认领时刻。
8. `ReleaseCard` 对不存在卡返回 `ErrNotFound`。
9. 无主卡 `ReleaseCard` 返回成功。
10. 持有者本人 `ReleaseCard` 成功且两个字段清空。
11. 非持有者 `ReleaseCard` 返回可 `errors.Is(ErrCASConflict)` 的错误，报文含持有者，且归属未被改动。
12. `TakeoverCard` 覆盖归属并在事件流留下 `driver_takeover` 事件，payload 含 from/to。
13. `AcquireRunLock` 对不存在卡返回 `ErrNotFound`。
14. 租期内他人 `AcquireRunLock` 得到 acquired=false，返回行含持有者/节点/到期时刻，且原行未被改动。
15. 用注入时钟把现存行推过租期后，他人 `AcquireRunLock` acquired=true，覆盖四个字段。
16. 第 15 条发生时卡事件流出现 `driver_takeover` 事件，payload 含 from/to/reason。
17. 首次取得（此前无行）不产生任何卡事件。
18. 同 holder 重入 `AcquireRunLock` 刷新 `expires_at` 且不动 `acquired_at`。
19. `RenewRunLock` 持有者为真 → true 且 `expires_at=now+ttl`；非持有者/无行 → false 且无副作用。
20. `ReleaseRunLock` 后立即可被任何人取得，不必等租期。
21. 非持有者 `ReleaseRunLock` 返回 nil 且不动他人行。
22. `RunLockOf`/`AllRunLocks` 原样返回已过期行（存在性≠在跑，过滤责任在消费侧）。
23. `ensureSchema` 打开后 `card_run_locks` 表存在且 PK 为 card_id（SQLite 与 PG 同构）。

**缝 2（编排入口）**

24. 节点解不开 → `Run` 返回错误**且**卡上有 needs_human + 含原因原文的评论。
25. Session 未设置 → 同上（落卡 + 返回错误）。
26. 运行锁被拒（acquired=false）→ `Run` 返回错误且卡上出现 needs_human + 评论，
    评论含「谁在跑、哪个节点、租期到几点」。
27. 归属被拒（他主/终态）→ 同上落卡后再返回错误。
28. 回合正常结束 → 运行锁行已消失（不必等租期），`driver_session` 保持为本轮 owner。
29. 回合失败路径结束 → 运行锁行同样已消失。
30. 长回合期间至少发生一次续租（注入时钟推进断言，不真等 5 分钟）。
31. 续租返回 false 后：移列/裁决落账/挂附件/打撤等人标记均不再发生；
    卡上恰有一条说明 comment（一次性）。
32. 编排全程不再调用 `ReleaseCard`（归属不随回合消亡）。

**CLI / 看板**

33. `card release` 非持有者 → 非零退出且 stderr 含当前持有者；持有者 → `{"ok":true}`。
34. 裸 dispatch 对无驱动卡放行，报文永不出现「已被认领（驱动 ）」。
35. 裸 dispatch 对他主持有的卡拒绝且报文含持有者。
36. 裸 dispatch 成功后卡停留在原状态列（charter 流可跑通，不被推去「进行中」）。
37. 裸 dispatch 失败回滚只清归属、不动状态列。
38. conflict 徽标 = 存在未过期运行锁且最新 task 态 failed；「进行中」状态不再参与判定。
39. `ledgerSession()` 符号在生产代码中不存在；pid 词形不出现在 `driver_session` 新写入里。

**变异测试要打的点**（承接 spec）：把「非持有者释放返回成功」改回去 → 断言 11 红；
给归属加任何过期判据 → 断言 4 红；把「认领转状态」改回去 → 断言 6 红。

## 4. Ticket 0 边界与交棒欠账

本提交只落了以下空壳与镜像（编译通过，无可观测业务行为）：

- `internal/ledger/runlock.go`：`RunLock` 类型与五个 Store 方法签名，方法体直返零值。
- `internal/ledger/store.go`：`card_run_locks` 表 DDL 进 `ensureSchema` 两方言分支
  （结构事实，非行为；`ledger.Open` 即建表）。
- `internal/ledgerstep/runner.go`：`StepRunner.RunHolder` 字段 + `Session` 注释收窄。

实现节点必须补齐的欠账（不能被「已有空壳」冒充完成）：

1. §2.1 归属四操作的新语义与断言 1–12（含 `ClaimCard` 签名收窄、`ClaimDriver` 删除）。
2. §2.2 运行锁五方法的真实实现与断言 13–23（过期判定走 `s.timeNow()`）。
3. 缝 2 编排改造与断言 24–32（失败落卡三连、续租循环、写权 gate、defer 替换）。
4. §2.4 四条 CLI 变更与断言 33–37；`ledgerSession()` 删除与断言 39。
5. §2.5 徽标判据替换（断言 38）与 agentd `RunHolder` 生成装配。
6. 看板遗留 fallback actor 的 host 档归一（`internal/agentd/ledgerapi.go:446`）。
7. 抢占事件 payload 金样本断言（from/to/reason 三键）并入缝 1 测试或既有
   `internal/proto/contract_fixture_test.go` 风格的 fixture——web 按 reason 优先级直读，
   payload 形状事实上是看板 wire 的一部分。

## 5. 拍板记录（三重闸门命中）

### 5.1 归属认领收口为 `ClaimCard` 单符号，删除 `ClaimDriver`

难逆转：跨 `internal/ledger`、`internal/ledgerstep`、`cmd` 三处的调用方与测试都要跟着动；
无上下文会惊讶：后人会问「为什么删一个能用的导出函数再改另一个的签名」；真取舍：保留
双符号等价入口（零迁移成本，永久漂移风险）对收口单符号（一轮机械迁移，名字与语义对齐——
claim 就是归属锁，「驱动」词根正是 B239 要消灭的混淆源）。反过来写（保留 ClaimDriver 删
ClaimCard）不会有测试变红，故记录在此。

### 5.2 `--step` 编排认领归属且**持久**，运行结束只退运行锁

难逆转：改变 `driver_session` 的生命周期语义，影响 release/takeover/status 展示与看板；
无上下文会惊讶：今天的代码在 `runner.go:98-104` 明明白白 defer 释放，「跑完不退归属」像是
忘了清理；真取舍：退归属（回到随回合消亡的老路，归属锁退化成第二把运行锁）对比持久
（人尺度语义成立，代价是他人的后续 step 需显式 takeover——这正是 8-23 裁决钦点的显式转移）。
spec 实现决定 5 只说了身份降档没说寿命归属，此处把它定死：pid 消失 + 寿命归人。

### 5.3 抢占复用 `driver_takeover` 事件类型 + `reason` 人读短句；首取不落事件

难逆转：事件词表位一旦有了类型化消费者就收不回；无上下文会惊讶：「运行锁抢占为什么落
driver_takeover」——答案是对侧常量执法证明它零程序消费者、而 web 泛化渲染会优先把
`payload.reason` 当摘要直读（CardDrawer.tsx:125-138），复用即免费获得看板可读性；
真取舍：新增 `run_lock_preempted` 词表位（语义诚实，但要动 types.go 词表 + web 无增量收益）
对比复用（spec 钦点、零消费风险、reason 字段消歧）。同时定死：首次取得不落转移事件，
否则长回合卡的 timeline 会被开跑噪声淹没——这条反过来写没有任何现有测试会红。

## 6. 可执行冻结与图

- **金样本/CDC：无命中。** 本卡新增契约面没有哈希、密钥派生或跨进程 wire 编码：
  运行锁不出进程边界（Go 结构体直调），事件 payload 是库内 JSON 且消费方为零个类型化读者
  （§1.3）。payload 三键的金样本欠账已列入 §4 第 7 条，由实现轮随行为测试落地。
  本提交没有可观测行为，因此不伪造行为金样本。
- **目标图**：`codegraph/target.json` 的 `d_gateway → d_ledger` entries 补为
  `["ledger.Store","ledgerstep.StepRunner"]`（协调侧触面钉死在 Store 门面 + 编排器）；
  `d_cli → d_ledger` 维持 `["ledger.Store"]`。不新增依赖方向，不把方法名伪装成 container entry。
- **分支视图**：`codegraph/diffs/cards-B239-charter.json` 记录 Ticket 0 六个新符号
  （`RunLock` 模型 + 五个 Store 方法）与 `StepRunner` 的字段变更；下游 breakdown/plan 以
  `--view cards-B239-charter` 叠加查询可命中全部新契约符号。
- **架构形态声明**：存量项目沿用既有声明——顶层领域即子系统（`d_ledger`、`d_coordination`、
  …），本卡跨 `d_ledger` 与 `d_coordination` 两个子系统，依赖单向
  （`d_coordination → d_ledger`），与 target.json 冻结面一致。

## 7. 交棒声明

| 法定产出 | 本轮证据 |
| --- | --- |
| spec 状态位 | 上游 `docs/superpowers/specs/2026-08-24-b239-claim-lock-split.md:4` 「状态：**已批准**」，开工核对一致，无需回写 |
| 契约增量文档 | 本文件 §1–§6；全部签名有 `file:line` 现状证据与 `file#Symbol` 锚 |
| 目标图与分支视图 | `codegraph/target.json`、`codegraph/diffs/cards-B239-charter.json`，随本提交冻结；`graph validate` issues=None，`graph check --view cards-B239-charter` fails=[]，`graph sym --view … Store.AcquireRunLock` anchor=ok |
| Ticket 0 编译 | 本轮 `go build ./...` → exit 0；`go vet ./internal/ledger ./internal/ledgerstep ./cmd ./internal/agentd` → exit 0；`gofmt -l` 三个触碰文件 → 空 |
| 可执行冻结 | 哈希/密钥派生/跨进程 wire：无命中（§6）；payload 金样本列为实现轮欠账 §4.7 |
| 三重闸门拍板 | §5 共 3 条；未命中项：无其他命中 |

**交棒对象**：`breakdown`。实现节点须先读本文档与上游 spec，按 §3 清单逐条落地、
§4 欠账逐条销账；不得把 Ticket 0 空壳当作行为已实现，不得在归属侧引入任何形式的过期判定。

## 8. 修订记录

- 2026-08-25（breakdown 轮，拆解稿 `docs/superpowers/specs/b239-breakdown.md` §二）：边界澄清两条，均不退回 contract。**澄清一**：本文行文的 `d_coordination` 不是 codegraph 域 id——图上该职责分属 `d_gateway`（internal/agentd）与 `d_cli`（cmd），子系统 id 以 `codegraph/best.json` 为准，依赖方向与 §6 冻结面不变。**澄清二**：`web:<r.RemoteAddr>` 在 `internal/agentd/ledgerapi.go` 共七处（:89/:367/:386/:446/:522/:542/:693），仅 :446（CardStepReq fallback actor，会成锁身份）随本卡收敛为 host 档；其余六处是事件审计署名，不动。
