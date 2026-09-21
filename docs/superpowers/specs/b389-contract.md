# B389 契约增量：协调者席位的承载记录与唤醒跨机路由

**上游状态：已批准**（源：B389 卡上的方案 + 用户 2026-09-19 夜的两条裁定；卡上方案经独立子 agent 评审为 `approve-with-changes`，必须改项已并入本文件）
**级别：L3 轻档**（跨账本/调度/agentd/CLI 四域，但一轮实现可闭合；与 B312 同形）
**有效基线：`cards/B233.1-charter-7` @ `241b71720`**
**架构形态：按子系统分域的平铺领域包，无横向 controller/service/dao 分层**（`codegraph/best.json`）
**冻结状态：本提交随 `codegraph/target.json` 与 `codegraph/diffs/cards-B233.1-charter-7.json` 冻结**
**修订轮（2026-09-21，review 退回三项）**：§3.1.1 认领键由 `seq` 单键改为 `(card,seq)` 复合键（原理由被实测证伪，见 §6-2）；§4-29 锁点重做（原测试对 `AdmitSeatCarrier`/`LaunchAdmit` 不可区分，见 §4-29）；新增多卡扇出冻结条目 §4-33–35 与旧表迁移条目 §4-36。本轮证据见 §5.1。
**修订轮 2（2026-09-21，B390 复评 F1 回写）**：§3.2 第 5 步与 §3.5 新增**准入失败豁免条款**——准入满员（`ErrNoSlot`）是可恢复**排队态而非失败态**：不 `CompleteWake`、不标 seen，保留认领挡住终局前缀水位，下一轮按轮询节拍自然重试；同卡连续失败达阈值恰落一次 `needs_human`（去重）。§3.5.4 的退避规则与 §4-31 收窄为**仅适用于非准入失败**。新增冻结条目 §4-37–42。本轮证据见 §5.2，拍板记录见 §6-5。回写依据见 `docs/superpowers/specs/b390-contract.md`。

## 0. 用户前提（不可挑战，逐条落进本契约）

1. 每台机器都可能承载协调者或执行者。**协调者会话在哪个载体（carrier）上创建，后续就由该载体所在机器的 agentd 续接。**
2. 共库（多台 agentd 共用同一份账本）是多机协作的基础；**同一条事件只应被认领和处理一次**；处理者读出席位绑定的载体与机器，把**唤醒请求路由到那台机器执行**。
3. **首次拉起必须记录实际载体归属，后续唤醒不得重新选载体。**
4. 不接受"远端席位就跳过、等那台机器自己消费事件"。
5. 卡已终态仍反复触发唤醒、失败事件再引发唤醒，两个自激缺陷都要收口。
6. `needs_human` **不触发协调者自动唤醒**（对齐 B358 契约），但**保留**卡级订阅输出与会话列表待办展示；主 agent 看到后按故障原因处置，不机械地再次 `@` 原协调者。

## 1. 现状查证与边界

| 现状签名/字面值 | 代码事实出处 | 本契约变化 |
| --- | --- | --- |
| `func (s *Store) BindSeat(id, identity string, source proto.SeatSource) error` | `internal/ledger/binding.go#Store.BindSeat`（`:32-70`） | 增加承载参数；**承载记录的写入与席位写在同一 mutate 事务内**（原子） |
| `func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string) error` | `internal/ledger/binding.go#Store.RebindSeat`（`:74-124`） | 同上：换绑与承载记录**同事务覆写**，不允许出现"席位换了、承载还是旧的" |
| `RewnewDriverLease` / `DropDriverLease` / `DriverLeaseOf` | `internal/ledger/binding.go`（`:126-` 起） | 不改：活性租约与承载记录是两件事（前者是"会话活着吗"，后者是"会话在哪儿"） |
| `driver_carrier` 兼容列 | `internal/ledger/store.go:416` 的 `ALTER TABLE cards ADD COLUMN driver_carrier TEXT`；契约 `docs/superpowers/specs/b312-contract.md` §2.2「只作兼容存量，不读、不写、不映射到席位」；`internal/ledger/binding.go:1-3` 文件头「不能成为席位的第二真源」 | **继续不读不写**。本卡新增独立承载记录，不复用该列、不改它的兼容语义 |
| `func EncodeSeatIdentity(cli, sessionID string) (string, error)` / `ParseSeatIdentity(raw string) (cli, sessionID string, err error)` | `internal/proto/seat.go#EncodeSeatIdentity` / `#ParseSeatIdentity`（语法 `cli:<cli>#<session_id>`：恰一个 `#`、两段非空无空白、`cli` 不含 `:`） | 不变。**承载信息不得塞进席位身份**（会把格式与全部校验撑破） |
| `type SessionRef struct { CLI, SessionID, Machine, HomeDir, Workdir, Model string }` | `internal/keysclient/keysclient.go#SessionRef`（`:23-33`） | 承载记录的字段来源：恢复一次 `Resume` 所需的 CLI/环境/机器，逐项从载体登记与回合结果取，不猜 |
| `func (s *Service) Wake(ctx context.Context, card string, evs []WakeEvent, spec keysclient.SessionSpec) (RoundResult, error)` | `internal/keystone/keystone.go#Service.Wake`（`:94-126`） | 签名不变；**调用方改为从承载记录构造 `spec`**，不再现挑载体 |
| `func (s *Server) wakeCoordinatorRound(ctx, card string, evs []keystone.WakeEvent) (keystone.RoundResult, error)` | `internal/agentd/scheddrain.go#Server.wakeCoordinatorRound`（`:333-412`；`:357` `LaunchAdmit(squad)` 现挑载体，`:382` 本机 `keystone.Wake`） | 归属判定**前移到 `LaunchAdmit` 之前**；远端归属走转交面；本机归属按承载记录构造 spec |
| `func (s *Server) launchCoordinatorRoundWithExpect(...)` | `internal/agentd/scheddrain.go`（`:255` `LaunchAdmit`、`:312-314` CAS 写席位） | 拉起成功后把**本次实际挑中的载体**写成承载记录（与席位同事务） |
| `func (s *Store) CloseCard(...)` | `internal/ledger/cards.go#Store.CloseCard`（只改 `status`，不清席位） | 终态转移**在同一事务内清席位与承载记录** |
| 唤醒判据 `automationWakeEvent` / 批次 `pendingByCard` / 失败 `return` | `internal/agentd/wakeconsumer.go`（`:88-128` / `:319` / `:458`） | 判据收口（`needs_human` 移出）；批次接认领；失败不再断整轮 |
| `automationCursor`（单调游标） | `internal/agentd/automation_cursor.go`（`:63-81`） | 语义收紧为**终局前缀水位**（见 §3.1） |
| 两方言建表与迁移 | `internal/ledger/store.go`（PG 建表 `:205-300`、SQLite 建表 `:330-400`、加列迁移 `:408-433` 容忍 `duplicate column`/`already exists`） | 新增 `CREATE TABLE IF NOT EXISTS` 两方言各一条，无需加列迁移 |
| `func (s *Store) mutate(func(*sql.Tx, *eventSink) error) error` | `internal/ledger/store.go#Store.mutate`（PG 上先取 `pg_advisory_xact_lock`，全部账本写串行化） | 认领与承载写入都走它——**排他靠事务串行化，不靠概率** |

## 2. 承载记录

### 2.1 表（两方言同形，除类型）

```sql
CREATE TABLE IF NOT EXISTS seat_bearings (
  card_id   TEXT PRIMARY KEY,
  identity  TEXT NOT NULL,
  carrier   TEXT NOT NULL,
  machine   TEXT NOT NULL,
  home_dir  TEXT NOT NULL,
  workdir   TEXT NOT NULL,
  model     TEXT NOT NULL,
  bound_at  TIMESTAMPTZ NOT NULL   -- SQLite 方言为 TEXT
)
```

`identity` 是**一致性见证**，不是席位真源：它必须恒等于同事务写入的 `cards.driver_session`，读取方一律以 `cards.driver_session` 为席位来源。存在的意义是让"席位与承载不一致"在库内可判（同事务断言相等）。

### 2.2 类型与精确签名

```go
// internal/ledger/types.go
type SeatBearing struct {
	Carrier string // 载体登记名（sched.Carrier.Name）
	Machine string // 载体所在机器名
	HomeDir string // 恢复环境：Resume 用的 HOME
	Workdir string
	Model   string
}

// internal/ledger/binding.go —— 唯一写面，承载与席位同事务
func (s *Store) BindSeat(id, identity string, source proto.SeatSource, bearing SeatBearing) error
func (s *Store) RebindSeat(id, identity string, source proto.SeatSource, expect string, bearing SeatBearing) error
func (s *Store) SeatBearingOf(id string) (SeatBearing, bool, error) // 读面：无记录返回 ok=false
func (s *Store) ClearSeat(id string) error                          // 显式清席位+承载（终态与修复路径共用）

// internal/ledger/cards.go —— 终态在同一事务内清
func (s *Store) CloseCard(id string, ...) error // 签名按现状不动，行为增加"原子清席位与承载"
```

规则逐条冻结：

1. `source == coordinate` 时 `bearing` **必填**：`Carrier`、`Machine` 均非空且无首尾空白；`HomeDir`/`Workdir`/`Model` 允许空串（表示该 CLI 用默认环境）。
2. `source == bind` 时 `bearing` **必须为零值**：人尺度坐下没有载体归属（`bind` 席位的唤醒本就是 no-op，见 B312 §7.1）。传非零值报 `ErrBadState`，不静默忽略。
3. `identity` 与 `bearing` 的写入在**同一个 mutate 事务**内完成；任一步失败整事务回滚，**不得留下"席位已写、承载未写"或反向的中间态**。
4. `RebindSeat` 成功后承载记录**整体覆写**（不是 merge）：新会话可能换机器、换载体、换环境。
5. `ClearSeat` 在同一事务内清空 `driver_session`/`driver_source`/`driver_heartbeat_at` 并删除承载行；幂等（已空座仍成功）。
6. 终态转移（`CloseCard` 等）在**它自己的事务内**调用清席位语义，不允许"先关单、后台再清"。
7. 读面 `SeatBearingOf` 不返回席位身份判定：**空座/非法席位由 `cards` 两列判**，承载记录的存在与否不参与"是不是空座"。

### 2.3 存量无归属的显式修复路径

存量 `coordinate` 席位是在本契约之前落的，**没有承载记录**。它们的处置必须显式，不允许任何静默分支：

| 场景 | 行为 |
| --- | --- |
| 唤醒遇到 `coordinate` 席位但无承载记录 | **不重选载体、不试跑**。落恰一条 `EvSeatBearingMissing`（新事件类型，payload `{"seat":"<identity>"}`），并按"需要人"展示（订阅输出与会话列表待办照常），本轮该卡跳过；同卡同席位在承载补齐前不重复落事件（幂等） |
| 人工修复（已知会话在哪台机器） | `handoff card seat bearing set <id> --carrier <carrier>`：机器名从载体登记读出（不手输）；CAS 断言卡上席位未变；写承载记录后该卡恢复可唤醒 |
| 人工修复（会话已不可用/不可知） | 走既有 `handoff card rebind <id> --launch`：新回合产生新会话与新承载记录，旧承载随换绑整体覆写 |

`EvSeatBearingMissing` 只在"该唤醒本可能有承载"时落：`bind` 席位、空座、终态卡都不落。

## 3. 唤醒认领与跨机路由

### 3.1 认领（exactly-once）

```sql
CREATE TABLE IF NOT EXISTS wake_claims (
  card        TEXT NOT NULL,
  seq         BIGINT NOT NULL,      -- SQLite 方言 INTEGER；取自 card_events.seq（全局唯一）
  holder      TEXT NOT NULL,        -- 机器名 + agentd 实例标识
  lease_until TIMESTAMPTZ NOT NULL, -- SQLite 方言 TEXT
  done_at     TIMESTAMPTZ,          -- 空=在飞
  PRIMARY KEY (card, seq)
)
```
索引 `CREATE INDEX IF NOT EXISTS idx_wake_claims_seq ON wake_claims(seq)` 支撑 `WakeClaimsBefore` 的水位扫描（约束 (card,seq) 的主键索引以 card 为前导，按 seq 范围扫会退化为全表扫）。

```go
// internal/ledger/wakeclaim.go
func (s *Store) ClaimWake(seq int64, card, holder string, ttl time.Duration) (bool, error) // 拿到=false 表示别人持有
func (s *Store) CompleteWake(seq int64, card, holder string) error                         // 只允许持有者收尾（key=(card,seq)）
func (s *Store) WakeClaimsBefore(seq int64) (inFlight []int64, err error)                  // 读面：某水位之前仍在飞的认领（DISTINCT seq）
```

1. 认领键是 **`(card, seq)` 复合键**（`card_events.seq` 全局唯一 `BIGSERIAL`，同一事件可合法扇出到多张卡）。排他语义收窄为 **「同一张卡的同一事件只允许一个持有者」**：扇出场景下每张卡各自可被认领。**不得退回 `seq` 单键**——`roomMessageWakeEvents`（`internal/agentd/wakeconsumer.go:261`）对一条 `room_message` 事件按寻址扇出 `len(targets)` 个 `keystone.WakeEvent`（`:315`），每卡一条 wake；单键会让第二张卡认领失败并被 `claimWakeBatch` 静默跳过（`:391`），游标越过该 seq，第二张卡永久丢唤醒。实测见 §5.1。
2. 排他靠 `mutate` 的事务串行化（PG 上 `pg_advisory_xact_lock`）：事务内"读现有行 + 过期接管 + 插入"是**真排他**。过期接管沿用镜像租约既有形态（`internal/ledger/mirror.go#Store.AcquireMirrorLease`）。`ClaimWake` 的冲突判定与过期接管都以 `(card,seq)` 定位行：**不同卡的同 seq 不是竞争关系**，A 卡持有 `(A,8)` 不阻止 B 卡认领 `(B,8)`。
3. **游标语义（终局前缀水位）**：唤醒消费的游标只能推进到"最大的、其之前所有 seq 都已终局的前缀"——终局 = 本机处理完 / 对端已 `done_at` / 该 seq 不属于可唤醒事件。**在飞的认领挡住宿主推进游标**，否则对端崩溃或超时后该事件被永久跳过（丢事件）。`WakeClaimsBefore` 必须以 `SELECT DISTINCT seq ... WHERE seq < ? AND done_at IS NULL` 聚合：同一 seq 在多卡上有多个认领行时，**任一行在飞就挡住对应水位**，不能因另一卡已终局就放行。
4. 认领在**占名额与试跑之前**：拿不到认领的机器直接跳过该 seq，不占本机协调者名额、不发起任何回合（这正是修掉"两台互抢名额"的那一刀）。**本卡范围的跳过**：某卡的 `(card,seq)` 被他机持有时只跳过该卡该事件，不影响同批其他卡（扇出场景正是如此）。

### 3.2 归属解析与路由

唤醒处理顺序（严格按序，前一步不通过不进下一步）：

1. 读 `cards` 席位两列：空座、非法席位 → 不唤醒（B312 语义不变）；`bind` 来源 → no-op；卡已终态 → `Forget` + 不再唤醒。
2. 按 §3.1 认领该批次的事件（批次内逐 seq 认领该卡的 `(card,seq)`，全部拿到才处理；拿到部分则只处理拿到的）。扇出到多张卡时，每张卡各自认领自己的 `(card,seq)`，互不排他。
3. 读 `SeatBearingOf(card)`：
   - 无记录且 `source == coordinate` → §2.3 的显式修复路径，跳过。
   - 有记录 → 取 `Machine` 判定归属。
4. **归属判定在任何名额申请之前**：
   - `Machine` 是本机（含 `IsSelfTarget`）→ 本机执行：从承载记录构造 `keysclient.SessionSpec{CLI, HomeDir, Model, Workdir}`（CLI 由席位身份解出），按**冻结载体**申请名额（§3.4），再 `keystone.Wake`。
   - `Machine` 非本机 → **转交**（§3.3）：本机不申请协调者名额、不发起回合、不落席位。
5. 处理结果：**非准入失败与成功**都要 `CompleteWake(seq, card, holder)`（key 为 `(card,seq)`）；失败额外落"需要人"展示并进入退避（§3.5）。**准入满员例外**（B390 §修订轮 2）：协调者席位准入返回 `ErrNoSlot`（可恢复排队态）时**不 `CompleteWake`、不标 seen**，认领留在飞挡住终局前缀水位（§3.1.3），下一轮按 `automationPollInterval` 节拍自然重读到并重试；不进退避（§3.5.4 只辖非准入失败），连续失败达阈值恰落一次 `needs_human`（去重），见 §3.5.5。

### 3.3 转交面（agentd → agentd）

```text
POST /api/cards/{id}/coordinator/wake
```

```go
// internal/proto/ledger.go
type CoordinatorWakeReq struct {
	Seat   string             `json:"seat"`             // 承载记录里的席位身份（CAS 见证）
	Events []LedgerEvent      `json:"events"`           // 本批唤醒事件（含 seq）
	Holder string             `json:"holder,omitempty"` // 认领者标识，供对端日志串联
}

type CoordinatorWakeResp struct {
	CoordinatorLaunchResp            // 复用既有响应形状，不造第二套
	HandledBy             string `json:"handled_by,omitempty"` // 实际执行的机器名
}
```

```go
// internal/client/coordinator.go
func (c *Client) CoordinatorWake(ctx context.Context, cardID string, req proto.CoordinatorWakeReq) (*proto.CoordinatorWakeResp, error)
```

规则：

1. 出站走 **agentd 的 target 客户端池**（`Server.pool`/`clientForTarget`，带 `cfg.Targets[machine].Token` 与防环头 `X-Handoff-Forwarded: 1`），**不使用**客户端 `?machine=` 的入站转发面（语义不同，会被防环头改向）。
2. 对端收到后按 §3.2 第 4 步的"本机执行"分支执行；执行前**再次校验** `Seat` 与账本当前 `driver_session` 相等（CAS），不等则返回 409 且不改任何状态。
3. 对端执行结果**只回执，不改本机任何东西**：席位重建由对端自己用既有 `RebindSeat(expect=旧身份)` 落库（共库下本机下次读卡自然看到新身份）。
4. 转交失败（不可达/超时/409）**不重选载体**：落"需要人"展示 + 退避；认领照常收尾，游标不因它永久阻塞。
5. 端点落在既有 `s.auth(mux)` 之内，Bearer 一致；不新增鉴权形态。

### 3.4 冻结载体准入

唤醒执行必须按承载记录里的载体申请名额，**不得再挑载体**：

```go
// internal/scheduling/scheduling.go —— 新增：按载体 + 小队两键 CAS，接受协调者角色
func (s *Scheduling) AdmitSeatCarrier(squad, carrier string) (Binding, error)
```

1. 两键 = 小队成员键 + 载体物理键（沿用 `OccupancyKeys`）；`carrier` 必须是该小队的成员，否则拒绝。
2. 与 `LaunchAdmit` 的差别只有一条：**载体由调用方冻结指定**，不做候选遍历与打分；其余名额语义（两级计数、释放、角色校验）一致。
3. 现有 `AdmitFrozen` 明确拒绝协调者小队（`ErrRoleMismatch`）、`AdmitCarrier` 不占成员键——两者都不能替代本方法，本卡新增面是必需的，不是重复建设。
4. `launch` 路径（首次拉起）继续用 `LaunchAdmit(squad)` 挑载体，挑中后由它返回的 `binding.Carrier` 写承载记录——**"首次挑、后续不挑"就落在这一条上**。

### 3.5 判据收口与不堵流

1. `automationWakeEvent` 把 `EvNeedsHuman`（及本卡新引入的 `EvSeatBearingMissing`）**移出唤醒映射**；`needs_human` 不再是唤醒源（对齐 B358 契约规则 31）。
2. **保留**两条展示通路：卡级订阅输出（`session wait` / 卡事件订阅）与会话列表"需要你"待办——判据收口不得改这两条；实现时以它们的既有测试仍绿为准。
3. 删除"把自生 `needs_human` 标 seen"的补丁（判据收口后它没有存在理由）。
4. 单卡处理失败**不得 `return` 断整轮**：留痕（展示）+ 退避（同卡同 seq 在退避窗口内不重复试跑）+ 继续处理同批与后续卡。退避窗与重试上限取常量，写进实现并在测试里断言"相邻两轮不重复试跑同一 seq"。**本条的退避仅辖非准入失败**；准入满员（`ErrNoSlot`）走 §3.5.5 的排队态重试，不进本条退避。
5. **准入失败的排队态语义（B390 修订轮 2 新增）**：协调者准入返回 `ErrNoSlot` 时，按**可恢复排队态**处置，与账本故障/承载缺失/席位非法等**非准入失败态**分流：
   - **不 `CompleteWake`、不标 seen**：认领留在飞，挡住终局前缀水位（`WakeClaimsBefore` 以 `DISTINCT seq` 聚合，任一行在飞即挡），游标不越过该 seq；
   - **下一轮自然重试**：节拍 = 既有 `automationPollInterval=2s` 轮询本身，**不设秒级退避**（同持有者可续期认领，下一轮重读到同 seq 并再次尝试准入）；
   - **恰一次 `needs_human`**：同卡连续准入失败计数达阈值（实现取 3）落**恰一条** `needs_human`（按「次数==阈值」去重，之后继续重试不重复落）；成功或遇非准入错误即清零计数；
   - **非准入失败不受本条影响**：仍按 §3.2 第 5 步完成 `CompleteWake`、标 seen、进退避（§3.5.4 / §4-31），不断整轮。

## 4. 原子冻结清单

每一条独立 pass/fail：

1. `seat_bearings` 在 PG DDL 中存在。
2. `seat_bearings` 在 SQLite DDL 中存在。
3. 旧库重复 `Open` 不因新表失败（`CREATE IF NOT EXISTS` 幂等）。
4. `BindSeat(coordinate)` 不传承载即失败（`ErrBadState`），且**席位未被写入**。
5. `BindSeat(bind)` 传非零承载即失败，且席位未被写入。
6. `BindSeat(coordinate)` 成功后 `SeatBearingOf` 返回同一载体与机器。
7. `BindSeat` 的席位与承载在同一事务：注入承载写入失败时 `driver_session` 仍为空。
8. `RebindSeat` 成功后承载整体覆写（换机器/换载体/换环境三样都变）。
9. `RebindSeat` CAS 不符时**席位与承载都不变**。
10. `ClearSeat` 清空三列并删除承载行；重复调用幂等成功。
11. 终态转移后席位为空且承载行不存在。
12. `SeatBearingOf` 对无记录卡返回 `ok=false` 且不报错。
13. 承载记录的 `identity` 恒等于同一行的 `driver_session`（读写两侧一致）。
14. `ClaimWake` 同一 `(card,seq)` 并发只允许一个 `true`；**不同卡的同 seq 互不排他**（两张卡都能各自拿到 `(card,seq)` 的 `true`）。
15. `ClaimWake` 在租约过期后允许第二者拿到。
16. `CompleteWake` 非持有者调用失败且不改行；**对同一 seq 的另一张卡收尾不影响本卡认领行**。
17. 在飞认领挡住宿主游标推进：存在 `done_at` 为空且未过期的 `(card,seq)` 时，游标不得越过该 seq；**同一 seq 多卡中任一行在飞即挡住该水位**。
18. `needs_human` 不再触发唤醒（`resumes`/`launches` 计数为 0）。
19. 卡级订阅输出仍能收到 `needs_human`。
20. 会话列表"需要你"待办仍显示 `needs_human`。
21. `coordinate` 席位无承载时唤醒**不发起任何回合**且不占名额。
22. 上述场景落恰一条 `EvSeatBearingMissing`，同席位重复唤醒不重复落。
23. 远端归属时本机 `resumes==0 && launches==0`（不跑回合）。
24. 远端归属时本机协调者名额两键计数不变。
25. 远端归属时恰发出一次 `POST /api/cards/{id}/coordinator/wake`，body 含本批 `seq` 集合。
26. 对端 `Seat` 与账本席位不符时返回 409 且不改状态。
27. 对端执行成功后的席位重建用 `RebindSeat(expect=旧身份)`，本机读卡看到新身份。
28. 唤醒执行走 `AdmitSeatCarrier`：载体非该小队成员时拒绝。
29. 唤醒执行真正打到承载记录里的载体，且**不经 `LaunchAdmit` 的候选遍历**。锁点必须能让「换成 `LaunchAdmit`」变红——断言回合进行中 `carrier/<承载.Carrier>` 键被占用（`LaunchAdmit` 按小队顺序回落到首个成员，该键为 0），或注入一个只在冻结准入路径出现的可观测副作用。原测试 `TestB389WakeUsesFrozenCarrierNotLaunchAdmit`（`internal/agentd/scheddrain_test.go:578`）在回合结束后的计数上断言，对 `AdmitSeatCarrier`/`LaunchAdmit` 不可区分，本轮实测已证（§5.1），必须重做。
30. 单卡处理失败不阻断同批其他卡（B 卡仍被唤醒）。
31. 单卡**非准入**失败后进入退避：相邻两轮不重复试跑同一 `(card,seq)`。（准入失败的排队态重试见 §4-38，不走本条退避。）
32. 终态卡收到任意事件不再唤醒（`resumes`/`launches` 为 0）。
33. 多卡扇出：一条 `room_message` 事件寻址命中两张各有 `coordinate` 席位的卡时，**两张卡各被唤醒恰一次**（`resumes`/`launches` 命中共 2 次，`processed==2`）。
34. 多卡扇出后游标推进到该 seq 之外（`cursor >= seq`），且两张卡的 `(card,seq)` 认领行都已 `done_at` 收尾——游标不得被扇出中的任一卡永久挡住。
35. 多卡扇出中，若某张卡的 `(card,seq)` 已被他机持有，只有该卡跳过，**另一张卡仍被唤醒**（排他收窄为按卡，不误伤扇出兄弟）。
36. 旧库（基线 `wake_claims(seq)` 单键表）升级到本契约定型后，`ClaimWake`/`CompleteWake`/`WakeClaimsBefore` 在新表结构上工作：存量单键表要么被显式迁移成 `(card,seq)` 主键，要么在 `Open` 时被安全重建（见 §5.1 的 `DROP`/`CREATE` 处置），不出现"查询用 `WHERE card=?` 撞上无 card 主键的旧表"这类静默错配。
37. 准入失败（`ErrNoSlot`）时不 `CompleteWake`：同卡同 seq 的认领行在失败后仍 `done_at IS NULL`（在飞）。
38. 准入失败后认领挡水位：存在该在飞认领时，`CursorWatermark` 返回小于该 seq 的水位、游标不越过该 seq；下一轮消费重读到同 seq 并**再次尝试准入**（同一批 N 轮背靠背，准入被尝试 ≥ N 次）。
39. 准入失败连续达阈值恰落**一次** `needs_human`：阈值前不落；第 3 次恰落一条；第 4 次及以后不再新增（去重）。
40. 非准入失败不落 `needs_human`、不按节拍重试：走 §4-30/§4-31 的终局路径（`CompleteWake` + 退避），N 轮内准入只被尝试 1 次。
41. 准入失败成功后连续计数清零：一次成功唤醒后再遇到准入失败，`needs_human` 重新从 1 累计到阈值再落一次（不因历史计数而静默）。
42. 准入失败仍收尾本次消费轮：单卡准入持续失败不阻断同批其他卡，轮末正常返回（不 panic、不空转死循环）。

## 5. 可执行冻结与本轮验证

本节点（契约落地）本轮实跑的命令与原始结果：

```text
$ gofmt -l internal cmd                                    → （空）
$ go build ./...                                           → exit 0
$ go vet ./internal/ledger/ ./cmd/                         → （无输出，exit 0）
$ go test ./internal/ledger/... ./internal/collab/... \
    ./internal/keystone/... ./internal/ledgerstep/... ./cmd/... -count=1
ok  github.com/Xsxdot/handoff/internal/ledger        2.819s
ok  github.com/Xsxdot/handoff/internal/ledger/api    0.708s
ok  github.com/Xsxdot/handoff/internal/collab        2.844s
ok  github.com/Xsxdot/handoff/internal/collab/room   1.512s
ok  github.com/Xsxdot/handoff/internal/keystone      2.003s
ok  github.com/Xsxdot/handoff/internal/ledgerstep    3.518s
ok  github.com/Xsxdot/handoff/cmd                   49.557s
$ go test ./internal/ledger/ -run 'TestSeatBearing|TestDDLDialectParity' -count=1
ok  github.com/Xsxdot/handoff/internal/ledger  0.773s
```

金样本落盘：`internal/ledger/bearing_test.go` 锁住第 1–10、12、13 条（含"承载写入失败整事务回滚"的拆表注入）。

### 5.1 修订轮实测证据（2026-09-21，本节点在 `$TMPDIR` 隔离副本实跑）

Review 退回的三项，本轮逐条在 HEAD `6f20dfce` 的 `git archive` 副本上复现与验证（改动仅发生在副本，工作树未动）：

**(a) §3.1.1 单键认领导致扇出丢唤醒（实测证实）**

在副本 `internal/agentd/` 内落一支双卡扇出复现（一条 `room_message` @ 两张各有 coordinate 席位的卡，走真实 `consumeAutomationEventsOnce`）：

```text
$ go test ./internal/agentd/ -run TestZZB389FanoutRepro -count=1 -v
    ... 唤醒认领成功 seq=8 card=B1 holder=handoff#466354
    ... 唤醒认领让过 seq=8 card=B2 holder=handoff#466354    ← B2 被静默跳过
    FANOUT_ROUND1 processed=1 resumes=1 cursor=8             ← cursor 越过 seq，B2 永久丢唤醒
    FANOUT_ROUND2 processed=0 err=<nil> resumes_total=1      ← 第二轮也补不回来
```

把认领改为 `(card,seq)` 复合键（DDL 主键 + 三个方法 + `WakeClaimsBefore` 改 `DISTINCT seq`）后同测：

```text
    ... 唤醒认领成功 seq=8 card=B1 holder=handoff#466919
    ... 唤醒认领成功 seq=8 card=B2 holder=handoff#466919     ← 两张卡各自认领成功
    FANOUT_ROUND1 processed=2 resumes=2 cursor=8             ← 待补：resumes==2 且 processed==2 即 §4-33
```

复合键改动后既有 `go test ./internal/ledger/ ./internal/agentd/ -count=1` 全绿：

```text
ok  github.com/Xsxdot/handoff/internal/ledger   23.518s
ok  github.com/Xsxdot/handoff/internal/agentd  180.518s
```

结论：原理由「取 seq 本身可以保证排他」被证伪——一条事件可合法扇出到多张卡，`seq` 单键让第二张卡认领失败、游标越过、永久丢唤醒。§6-2 的拍板记录随之修订。

**(b) §4-29 原锁点不可区分（实测证实）**

把副本 `internal/agentd/scheddrain.go:449` 的 `AdmitSeatCarrier(squad.Name, bearing.Carrier)` 换成 `LaunchAdmit(squad.Name)`，跑原锁点：

```text
$ go test ./internal/agentd/ -run 'TestB389WakeUsesFrozenCarrierNotLaunchAdmit' -count=1
ok  github.com/Xsxdot/handoff/internal/agentd  0.223s     ← 四包全绿，锁点抓不住
```

改用「回合进行中读两级计数」的锁点（在 `Resume` 内读 `runningCountIn`）：

```text
冻结路径（AdmitSeatCarrier）：ZZOBS carrier/coord-carrier=0 carrier/coord-carrier-2=1 ... PASS
换成 LaunchAdmit：       ZZOBS carrier/coord-carrier=1 carrier/coord-carrier-2=0
                        回合进行中冻结载体 carrier/coord-carrier-2 应占用=1，实得 0  → FAIL
```

另一条等效锁点（记录型 `SchedulingClient` 装饰器，断言 `AdmitSeatCarrier` 被以 `("coord","coord-carrier-2")` 调用恰一次且 `LaunchAdmit` 零调用）：

```text
冻结路径：ZZLOCK frozen=[coord/coord-carrier-2] launch=0   → PASS
换成 LaunchAdmit：ZZLOCK frozen=[] launch=1 → FAIL
```

两条锁点都可区分，选其一落实现节点（推荐「回合进行中占用计数」——它是 §4-29 与 §4-24 的同一族观测，不新造接缝）。

**(c) 新增扇出条目已随 (a) 的实测确定**

§4-33–35 的期望值（`processed==2`、`resumes==2`、`cursor>=seq`、按卡认领互不误伤）均来自 (a) 的实跑读数。

逐条状态：

- 已落并跑过：1–10、12、13；
- 第 11 条（终态转移原子清席位）**属第 3 片**，本节点不碰 `CloseCard`——避免把两个域的行为混进一个提交；
- 第 14–32 条跨 agentd/scheduling/CLI，逐条列为**实现节点欠账**（不静默放行）：
  - [ ] 认领与游标：14–17（`wake_claims` 表结构已随本提交冻结，三个方法体属第 1 片）；
  - [ ] 判据收口与展示保留：18–20；
  - [ ] 承载缺失的显式路径：21–22；
  - [ ] 转交面与名额：23–29（**29 的锁点本轮重做，见 §4-29 与 §5.1(b)**）；
  - [ ] 不堵流与退避、终态：30–32；
- **修订轮新增欠账**：
  - [ ] 认领键改 `(card,seq)` 复合键：DDL 两方言主键、`idx_wake_claims_seq` 索引、`ClaimWake`/`CompleteWake`（签名加 `card`）与 `WakeClaimsBefore`（`DISTINCT seq`）属第 1 片返工（本轮只在副本验证，未落本分支）；
  - [ ] 多卡扇出与按卡排他：33–35（锁在 agentd 消费循环，属第 4 片）；
  - [ ] 旧单键表迁移：36（属第 1 片的 `Open` 迁移处置）。

### 5.2 修订轮 2 实测证据（2026-09-21，B390 实现树上实跑）

本轮为 **B390 复评 F1 的契约回写**：把 B390 已落地的「准入失败=排队态、不 complete、保认领挡水位、按 2s 节拍重试、恰一次 needs_human」从「plan 层临时裁决」升格为本契约的显式豁免条款，并给出可判 pass/fail 的冻结条目 §4-37–42。回写前状态：`git log --oneline -S "不 completeWakeBatch" -- docs/` 只有 B390 的 plan/implement 文件，`b389-contract.md` 零命中（复评 F1 原文即据此判 fail）。

本轮亲跑读数（工作树 `cards/B390-charter-5`，HEAD `a65baefc`，只跑不改实现）：

```text
$ go test ./internal/agentd/ -run 'TestB390|TestB389WakeBackoff' -count=1 -v
--- PASS: TestB390NonAdmissionErrorDoesNotStall (0.51s)   ← §4-40：非准入不重试、不落 needs_human
--- PASS: TestB390WakeStallRedLoop (0.26s)               ← §4-37/38/39：不 complete、挡水位重试、恰一次
--- PASS: TestB389WakeBackoffSkipsSameSeqNextRound (0.30s) ← §4-31：非准入退避不回退
ok  github.com/Xsxdot/handoff/internal/agentd  1.080s
$ go build ./...                                        → BUILD_EXIT=0
$ codegraph --repo . check                              → CHECK_EXIT=0
```

§4-37–42 的现有载体对应（行号为本轮读数；`Server.recordAdmissionStall`/`Server.clearWakeStall`/`admissionStalled` 是本卡新增符号、**不在 baseline 图中**，故用 `file:line` 锚，属图覆盖债）：`internal/agentd/wakeconsumer.go:59`（`recordAdmissionStall`：阈值 3 恰一次 + 去重）、`internal/agentd/wakeconsumer.go:84`（`clearWakeStall`：成功/非准入清零）、`internal/agentd/wakeconsumer.go:47`（`admissionStalled`：只认 `coordinatorAdmissionError` + `scheduling.ErrNoSlot`）、`internal/agentd/b390_wake_stall_test.go#TestB390WakeStallRedLoop`（§4-37–39、§4-41 的回路，含去重断言 `RETRY(d)`）、`internal/agentd/wakeconsumer.go:659-671`（准入分支 `continue`，不 complete）。

**诚实差异**：§4-40 的现有反例夹具 `nonAdmissionScheduling` 用 `scheduling.ErrNoHealthy` 桩，不覆盖「认领后真实 `Resume` 失败」那一支；后者由既有 `TestB389WakeBackoffSkipsSameSeqNextRound` 覆盖（走 `recordWakeBackoff`），两条合看是 §4-40 的全貌，单看任一条都不完整。

## 6. 三重闸门拍板记录

1. **承载记录用独立小表 `seat_bearings`，不塞进 `cards` 列、不复用 `driver_carrier`。** 牵动 DDL、读写面、换绑与终态清理，回改要动多域；没有本上下文时后人会问"为什么不放卡上"或直接把 `driver_carrier` 用起来；被否方案是"卡上四列"与"启用兼容列"。不做第二席位真源——`identity` 只是同事务见证，读取一律以 `cards.driver_session` 为准。
2. **认领键取 `(card,seq)` 复合键，游标是终局前缀水位。**（2026-09-21 修订：原条为「取 `seq` 本身」，其前提「同一事件只会唤醒一张卡、`seq` 单键即可排他」被实测证伪——`roomMessageWakeEvents` 对一条 `room_message` 按寻址扇出多条 wake，同一 seq 可合法出现在多张卡上。单键会让第二张卡认领失败并被静默跳过、游标越过、永久丢唤醒，见 §5.1(a)。这不是「反过来写不会变红」的裁决——**多卡扇出即触发**，只是需要一条双卡夹具才照得到。）排他语义收窄为「同一张卡的同一事件只允许一个持有者」，扇出场景每卡各自可被认领；游标的「在飞挡住推进」必须按 seq 的**任一**认领行在飞来判（`DISTINCT seq`）。被否方案是纯靠镜像租约（只覆盖镜像不回执唤醒）与按卡去重（无法表达同卡多事件）。后人看到 `(card,seq)` 复合键容易「顺手」退回单键以「简化」——那会重新打开扇出丢事件，且只有多卡夹具能照到。
3. **唤醒执行按承载记录的冻结载体申请名额，不重选载体。** 首次拉起挑载体、后续只认记录；没有本上下文时后人会在唤醒路径复用 `LaunchAdmit(squad)` 让功能"看起来能跑"，那会让同一张卡在不同机器上反复改归属并互抢名额；被否方案是继续用 `LaunchAdmit` 并"事后纠正"。**锁点必须能区分两者**（§4-29）：回合结束后的计数归零读数对两条路径都成立，照不出差异；要用回合进行中的占用读数或记录型准入调用。
4. **承载记录只随 `coordinate` 席位存在**：`bind` 席位必须为零承载（人尺度坐下没有载体归属）。后人会想"给 bind 也补个机器名"让界面统一；被否方案是给所有席位都写承载、以及"缺失时按本机补齐"。
5. **准入失败（`ErrNoSlot`）是排队态而非失败态，不 `CompleteWake`、保留认领挡水位、按轮询节拍重试。**（2026-09-21 修订轮 2 随 B390 落地回写。）难逆转：它改了 §3.2 第 5 步「无论成功失败都要 `CompleteWake`」与 §4-31 退避这两条已冻结语义，回改要动认领/游标/退避三处；无上下文会惊讶：后人看到失败分支**不**收尾认领、**不**进退避、游标被一条"已完成失败"的事件挡住，会想「补上 `CompleteWake` 让它前进」——那恰好重新打开 B390 要修的静默停摆（B332 卡死 leader 名额后整条唤醒链死 18 小时的根因）；真取舍：被否方案是（甲）失败即 `CompleteWake` + 退避（现状，会导致「失败一次就再也不试」）、（乙）内存重试表跨重启丢（B390 plan 原案，被协调者 P2 裁决弃）。**「反过来写不会变红」——补回 `CompleteWake` 后现有测试不自动红**（B390 复评已实测：这处偏离只有专门夹具照得到），故必须靠本拍板记录 + §4-37/38 冻结条目锁住。与 §3.5.5 的「不设秒级退避」同源：节拍 = 轮询本身，否则背靠背 N 轮夹具转不红/绿。

## 7. 移交 plan 附区（不计冻结条目）

- 由 plan 吸收：`seat_bearings`/`wake_claims` 两方言 DDL 与迁移的机械落地。
- 由 plan 吸收：`BindSeat`/`RebindSeat` 五个生产调用点（`cmd/card_driver.go:37,125`、`internal/agentd/scheddrain.go:312,314,398`）与测试夹具的参数补齐。
- 由 plan 吸收：`handoff card seat bearing set` 的命令面细节与错误文案。
- 由 plan 吸收：转交客户端的重试/退避常量与"需要人"展示文案。

## 8. 本节点法定产出与欠账声明

1. **契约增量文档**：本文件。现状签名带 `file#Symbol` 符号锚；关键行号为本轮亲自核对（改动前基线 `241b71720`：`internal/ledger/binding.go:32/74`、`internal/ledger/store.go:416`、PG 语句表 `:204` 起 / SQLite `:388` 起、`ddlStatements` 的方言对齐防线在 `internal/ledger/store_test.go`）。修订轮新增代码事实出处：`internal/agentd/wakeconsumer.go:261`（`roomMessageWakeEvents`）、`:315`（每卡一条 wake 的扇出点）、`:379`（`claimWakeBatch`）、`:391`（拿不到认领即静默跳过）、`:401`（`completeWakeBatch`）；`internal/ledger/wakeclaim.go:37/54/82/110`（单键表的四句 SQL）、`:27/76/109/133`（四个方法签名）；`internal/agentd/scheddrain.go:449`（`AdmitSeatCarrier` 调用点）；`internal/agentd/scheddrain_test.go:578`（原 §4-29 锁点）。修订轮 2（B390 F1 回写）新增代码事实出处：`internal/agentd/wakeconsumer.go:59`（`Server.recordAdmissionStall`，本卡新增符号，未入 baseline 图）/ `:84`（`Server.clearWakeStall`，同上）/ `:47`（`admissionStalled`，同上）、`internal/agentd/wakeconsumer.go:659-671`（准入分支不 complete 的 `continue`）、`internal/agentd/b390_wake_stall_test.go`（§4-37–39/41 载体）。
2. **Ticket 0 骨架**：两方言 DDL（`seat_bearings`、`wake_claims`、索引）、`SeatBearing` 类型与其形态执法、四个新签名（`BindSeat`/`RebindSeat` 加承载参数、`SeatBearingOf`、`ClearSeat`）、五个生产调用点与 60 处测试调用点补齐。本轮 `go build ./...` 退出码 0、`go vet` 无输出、`gofmt -l` 为空、受影响七包测试全绿（原始输出见 §5）。
3. **金样本**：`internal/ledger/bearing_test.go` 落第 1–10、12、13 条并本轮实跑通过；第 11 条随第 3 片落。§4-37–42（修订轮 2）由 B390 的 `internal/agentd/b390_wake_stall_test.go` 承载，本轮实跑通过（§5.2）。
4. **三重闸门**：本文件 §6 记录 4 项命中决定；无其它未记录决定。第 2 项于 2026-09-21 修订轮改写（原理由被实测证伪）；第 5 项于修订轮 2 新增（B390 准入失败豁免，命中「反过来写不会变红」型）。
5. **欠账（显式，不静默）**：
   - `codegraph/target.json` **未追加本卡条目**：该文件是 `{from,to,entries[]}` 的 51 条契约数组，追加需按域对逐条落位。本节点只改既有边的语义（ledger 写面新增承载记录、agentd 组装点写承载），**未新增跨域依赖方向**；落位与 `codegraph/diffs/cards-B233.1-charter-7.json` 视图 diff 由第 1 片随代码提交补齐（新符号 `SeatBearing`/`SeatBearingOf`/`ClearSeat`/`ClaimWake` 届时入图）。
   - 契约 §4 第 11、14–32 条为实现节点欠账，分片归属见 §5；修订轮新增 §4-33–36 与 §4-29 锁点重做（见 §5 末）。
