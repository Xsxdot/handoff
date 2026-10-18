# B435 契约增量：协调者统一模型——一套 wait、一个匹配函数、席位模式、驻场者来信

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b435.md`，头部「**上游状态：r2 已批准**（用户 2026-10-18 批准…）」——本轮对工作树复核一致，无需回写）
**级别：L3 ｜ 选档复核：重档确认**（spec §2：≥7 个子系统契约面；本轮冻结四条缝均有独立消费者，见 §2）
**冻结状态：本提交冻结 spec §2 的四条缝（① 两个 wait 的 stdout/退出码/游标；② 匹配函数判定表；③ 席位模式 wire；④ 驻场者来信正文）。Ticket 0 骨架编译通过、11+1 支契约测试全绿（§10）；分支视图 diff `codegraph/diffs/cards-B435-charter-1.json` 同批提交；`codegraph/target.json` 不改（§11）。三项退回 spec（R1–R3，§9）不在冻结范围内。**
**有效基线：** `cards/B435-charter-1` @ `7f860685`（基 `origin/main` `88e4d4e2`）
**架构形态：** 按子系统分域的平铺领域包，无横向分层（沿用 `codegraph/best.json`；本卡不新增领域）
**交棒：** breakdown（spec §7：Wave 0 = S2 + S4a + 席位模式字段）。

本文档把已批准 spec r2 的四条缝翻译成现状代码可接的签名、wire 字面量、判定表与游标语义。本节点只落空壳与金样本（§10）；wait 改造、消费循环改接、驻场者登记、迁移、B434 退役全部列入欠账（§12），不能被「已有空壳」冒充完成。条目编号按缝分段：C-1xx = ①，C-2xx = ②，C-3xx = ③，C-4xx = ④。

---

## 1. 现状查证

### 1.1 已查证签名与代码事实

行号是本轮（`7f860685`）读数；原始读数与命令见台账 `b435-ledger.md` §五.2。spec 行号取自 `c944b6b7`，除注明外与本树一致。

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| card wait 命令面 | `Use: "wait <id>"`；flag 只有 `--subtree/--follow/--timeout`，**无 `--since`、无持久游标** | `cmd/card_wait.go#runCardWait`（`:84`；命令 `:66-76`，flag `:396-401`） |
| card wait 起点 | 启动时 `st.MaxSeq()`，经 `st.Follow(ctx, members…)` 只读成员卡事件 | `cmd/card_wait.go:106`、`:160`；`internal/ledger/follow.go:13-14`（无卡事件不在内） |
| card wait 行格式 | 首行 `card_snapshot`（键 type/card_id/subtree/from_seq/members/actionable/needs）；其后每行一条 `ledger.Event` 原样 JSON | `cmd/card_wait.go:41-63`、`:352`、`:191`；`internal/ledger/types.go:173-183` |
| card wait 可动作集 | needs_human/needs_cleared/decision_* 恒可动作；room_message 只判 `kind==user && !by_system`（`by_system:null` 不可动作）；task_mirrored 经身份闸 + 策略 | `cmd/card_wait.go#cardWaitEventActionable`（`:233`；`:235-237`、`:238-253`、`:254-285`） |
| card wait 退出码 | 一次性首个可动作输出后 0；全终态 0（stderr「子树已全部完成」）；超时 124 | `cmd/card_wait.go:154-158`、`:203-225` |
| card wait 收不到会话消息 | 会话消息 `card_id` 恒空，Follow 只取成员卡事件 | `internal/collab/service.go:271`、`internal/ledger/follow.go:13-14` |
| session wait 命令面 | `Use: "wait <member>"`，`--since/--timeout/--follow` | `cmd/session.go:67`、`:897-902` |
| session wait 游标 | 缺省读 `SessionDeliveryCursorContext(ctx, member)`；积压单行 `SessionBacklog`、实时逐条 `SessionWake`，输出成功后 `AdvanceSessionDeliveryCursor(member, seq)`；`--since` 只读 | `cmd/session.go:307`、`:389`、`:456`、`:299-310` |
| session wait 命中 | `MessageWakeTargets` 结果逐字 `target == member` | `cmd/session.go:501-524`（`:517`；spec 记 `:516`，漂移 1 行） |
| 交付游标表 | `session_delivery_cursors(member PK, last_seq, updated_at)`，只升不降 | `internal/ledger/session_delivery_cursor.go:14-52` |
| 候选超集 | mentions（身份精确 ∪ 当前席位卡号）与 reply（→member 作者）两支，限 `card_id IS NULL`；**无「无寻址」分支** | `internal/ledger/session_candidates.go:62-135` |
| 寻址纯函数 | `ResolveDelivery(msg, replyAuthor, resolveSeat) []string`（可多人）、`IsAddressed` | `internal/collab/room/delivery.go#ResolveDelivery`（`:30`）、`#IsAddressed`（`:66`） |
| 寻址门面 | `MessageWakeTargets`：结构行 → nil；@卡号 → 该卡 `DriverSession`（不限本会话） | `internal/collab/sessions.go#Service.MessageWakeTargets`（`:220`）、`:233-255` |
| AddressesCard | 卡无席位恒 false；**生产调用方为零** | `internal/collab/sessions.go#Service.AddressesCard`（`:258`） |
| push 消费分流 | `automationWakeEvents` → 房间消息 `roomMessageWakeEvents`（`bySeat` 只收本会话卡席位，外部身份只 Info）；needs_human → `desktopNeedsHumanWakes` | `internal/agentd/wakeconsumer.go#Server.automationWakeEvents`（`:301`）、`#Server.roomMessageWakeEvents`（`:337`；`:352`、`:366-386`、`:397`） |
| 上走 | `resolveWakeCard`：bind 有桌面端记录 → 本卡；起始卡 bind 无记录 → 空；祖先 bind 无记录 → 继续上走；**房间消息唤醒也经它**（`:748`） | `internal/agentd/wakeconsumer.go#Server.resolveWakeCard`（`:502`；bind 分支 `:521-539`） |
| bind 跳过四处 | keystone `Wake` bind → Forget；`wakeCoordinatorRoundID` bind 分支；`resolveWakeCard`；`desktopNeedsHumanWakes` 非 bind 不唤醒 | `internal/keystone/keystone.go:123-126`；`internal/agentd/scheddrain.go:463-490`；`wakeconsumer.go:521-539`；`internal/agentd/desktopwake.go:1053` |
| queue_release 两分支 | 桌面端 bind 放行；远端 coordinate 显式失败回填 | `internal/agentd/scheddrain.go:478-484`、`:509-520` |
| 席位写面 | `BindSeat` 同事务落 `EvDriverSeatBound{to}` 并递增 seat revision；`RebindSeat`；`clearSeatTx` 清三列 + 承载 + 桌面端记录 | `internal/ledger/binding.go#Store.BindSeat`（`:34`）、`#Store.RebindSeat`（`:89`）、`:282-297` |
| 承载执法按来源 | `validateSeatBearing`：coordinate 必有承载、bind 必零承载；`SetSeatBearing` 只收 coordinate | `internal/ledger/binding.go:216-229`、`:175` |
| 席位身份 | `cli:<cli>#<session_id>` | `internal/proto/seat.go#ParseSeatIdentity`（`:53`） |
| 成员身份 | `user:<name>` / `agent:<name>`；`cli:<user>@<host>`、`web:<host>` 不属本词表 | `internal/proto/identity.go#ParseMemberIdentity`（`:57`；注释 `:6-8`） |
| 作者生产者 | 控制台 `user:<console_user>`；CLI `session send` 缺省 `cli:<USER>@<host>`、`--agent` → `agent:<n>`、`--cli/--session` → 席位 | `internal/agentd/roomsapi.go:469-497`；`cmd/session.go:881-895`；`cmd/ledgercli.go:44-51` |
| 席位 wire | `Card/CardView.driver_session/driver_source` omitempty；`CoordinatorRebindReq.Mode`（wire 键 `mode`）已被换绑方式占用 | `internal/proto/ledger.go:32-33`、`:176-177`；`internal/proto/ledger_wire_test.go:26-63` |
| card show | 单 JSON `{card: ledger.Card, …}` | `cmd/card.go:108-143`；`internal/ledger/types.go:156-157` |
| 信箱写入 | `$HOME/.handoff/desktop-wake/<cliSession>.%019d.md`，tmp + Link 原子；写信进程本机 HOME | `internal/agentd/desktopwake.go#writeDesktopLetter`（`:437`，名 `:457`）、`:976-986` |
| 卡级来信 | 首行「卡 X 有 N 件新事（账本 seq a–b）」+ 围栏渲染 | `internal/agentd/desktopwake.go:217-246`、`:1377` 起 |
| mod 取信 | 只认首行 `[handoff]` 与 `<sessionId>.<19 位>.md`；只搬运正文不解析 | `plugins/claude-desktop-wake/hooks/register.ts:18-38`、`:94-123` |
| 每机消费循环 | 每台 agentd 一个自动化循环，共享账本 + wake_claims 去重 | `internal/agentd/scheddrain.go:54-66` |

### 1.2 依赖既成行为（与签名同等承重）

- **`encoding/json` 键序 = struct 字段声明序**，`omitempty` 零值不出键：card wait 两种行与 `SessionWake/SessionBacklog` 的逐字金样本依赖它（Go 标准库既成行为；本轮以 `TestB435CardWaitLineShapes` 逐字锁，不靠声明）。
- **`json.Encoder.Encode` 每值末尾写一个 `\n`**：card wait 与 session wait「一行一 JSON」靠它（`cmd/card_wait.go:124`、`cmd/session.go:334`），金样本含末尾换行。
- **mod 先删后提交、只认首行标记**：正文形状改动对 mod 透明（`register.ts:85-91`、`:106-121`）；本卡据此不改 mod（mod 已装在用户机，零 import 独立交付物）。

### 1.3 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 真正生产者 | 真正消费者 | 结论 |
| --- | --- | --- | --- |
| `proto.SeatSourceBind/Coordinate` | `card_driver.go:65/:163`、`scheddrain.go:325/:328/:608` | 判定点 9 处（台账 §五.2 全集） | 活跃；本卡起判定改读 mode，来源词只作记录（C-315–C-318） |
| `Service.AddressesCard` | — | **零生产调用方**（只测试） | **疑似漂移**；并入匹配函数时删除，不作事实源照抄 |
| `Service.MessageWakeTargets` | collab | `wakeconsumer.go:352`、`cmd/session.go:512` | 活跃；降为 `Recipients` 内部步骤，collab 外调用归零（C-233/C-234） |
| `session_delivery_cursors.member` | `cmd/session.go:389/:456` | `cmd/session.go:307` | 活跃；列值语义改为「地址游标键」（C-112、C-135） |
| `proto.DesktopWakeMarker` / `DesktopWakeDirRel` | agentd 写信 | mod `register.ts:18/:20` | 活跃；保留（C-405 CDC 锁） |
| `proto.ClaudeNewSessionURLPrefix` / `ClaudeNewQMaxChars` | 控制台坐下按钮、简报 | 薄壳通道 | 随 D10 退役后零生产者 → 删除（§7） |
| mod `submitPrefill`（预填 `[handoff]` 代发） | 深链 `claude://code/new?q=`（D10 退役） | mod | 退役后**无生产者**（疑似漂移）；mod 零改动原则下保留，不在本卡删 |
| `EvSeatDesktopSet` | `SetSeatDesktop` | 消费循环清失败段 `wakeconsumer.go:730-734` | 随 `seat_desktops` 退役（§7） |
| `CoordinatorWakeResp.LetterUnopened` | 被转交端 | 发起端 `transferCoordinatorWake` | spec D10 保留；对驻场者可用性挂 R2 |

### 1.4 图覆盖债

`codegraph` 二进制不在 PATH，本轮以 `go build` 出 scratch 二进制直调（台账 §五.3）。`sym` 命中：`Service.MessageWakeTargets`（**图中签名陈旧**，仍是无 error 旧体）、`Service.AddressesCard`、`ResolveDelivery`、`Server.roomMessageWakeEvents`、`Server.resolveWakeCard`、`runCardWait`、`cardWaitEventActionable`、`writeDesktopLetter`、`Store.BindSeat`、`Server.desktopNeedsHumanWakes`、`Server.wakeCoordinatorRoundID`、`Service.Wake`。**未命中**：`runSessionWait`、`sessionWaitMatch`（`cmd/session.go`）——`who-calls Service.MessageWakeTargets` 因此漏掉 CLI 调用方；本文对二者只用 `file:line`。

---

## 2. 户口、生产者/消费者与依赖方向

不新增领域。按 `codegraph/best.json`：agentd 消费循环与桌面端桥属 **d_gateway**（spec §8 记作 d_scheduling 的「agentd 消费循环」即此；`internal/scheduling` 才是 d_scheduling）。spec §8 禁令按实际落点施行。

| 缝 | 冻结理由 | 生产者 | 独立消费者 |
| --- | --- | --- | --- |
| ① wait stdout/退出码/游标 | 对外承诺：其他 agent 的 skill 文本只靠它与 handoff 交互 | d_cli（`cmd/card_wait.go`、`cmd/session.go`） | 任意 CLI agent（codex/opencode/claude…）、桌面端组织者 |
| ② 匹配函数判定表 | 四个工作单元共同依赖同一判定 | d_collab（`internal/collab/recipients.go`） | d_gateway 消费循环、d_gateway 信箱桥、d_cli 两个 wait |
| ③ 席位模式 wire | 跨进程/跨子系统字面量 | d_ledger（列）、d_cli（`--mode`）、d_gateway（launch/rebind 写 push） | d_keystone、d_gateway 消费循环、d_cli card wait、d_web 控制台 |
| ④ 驻场者来信正文 | 跨进程 wire；mod 是零 import 独立交付物且已装在用户机 | d_gateway 信箱桥 | mod（不改）、桌面端组织者会话 |

依赖方向（全部落既有 `target.json` 契约面，零新方向、零预算变化）：

- d_gateway → d_collab：`collab.Service.Recipients`（「collab 入站门面」entries）。
- d_cli → d_collab：`collab.Service.Matches`（同上，进程内调用，沿 `cmd/room.go#roomServiceFor` 组装点）。
- d_collab → d_ledger：`client.LedgerClient.SessionResident`（接口缝，零调用预算不变）。
- d_collab / d_gateway / d_cli / d_ledger → d_protocol：`proto.SeatMode`、`proto.DeliveryAddress`、`proto.Delivery`、`proto.SessionResident`、`proto.RenderResidentLetter`（「proto 实体」「proto（包级函数）」entries）。
- **禁止**（spec §8，§4 源码守卫兑现）：d_keystone 读席位来源词；d_gateway 消费循环持有事件白名单之外的寻址判定；d_cli 自带匹配逻辑；d_gateway 信箱桥自拼判定。

---

## 3. 契约增量：精确签名

### 3.1 ① 两个 wait（d_cli；无 Ticket 0 骨架——沿用既有命令，形状由金样本锁）

```text
handoff card wait <id> [--subtree] [--follow] [--timeout <dur>] [--since <seq>]     # 新增 --since
handoff session wait <session> [--since <seq>] [--timeout <dur>] [--follow]          # 位置参数由成员身份改为会话号
```

`card wait` 输出 = 卡事件段（照旧）∪ 房间命中段（新）：

| 段 | 地址 | 起点 | 持久游标 | 行形状 |
| --- | --- | --- | --- | --- |
| 卡事件段 | 不按地址（成员卡集合） | 启动时 `MaxSeq`（或 `--since`） | 无 | `ledger.Event` 行（照旧） |
| 房间命中段 | `<id>` 卡的席位地址，且仅 mode=pull | 持久游标（或 `--since`）；无游标行 → 该身份最近坐下/换绑事件 seq | 键 = 席位身份 | `ledger.Event` 行（type=room_message，card_id=""） |

`session wait <session>` 地址 = 该会话的驻场者；输出沿用 `SessionBacklog` / `SessionWake`（形状不变）。

### 3.2 ② 匹配函数（d_collab 导出；Ticket 0 已落空壳）

```go
// internal/collab/recipients.go
func (s *Service) Recipients(msg proto.RoomMessage, author string) (proto.Delivery, error)
func (s *Service) Matches(msg proto.RoomMessage, author string, addr proto.DeliveryAddress) (bool, error)
func (s *Service) defaultRecipient(sessionID string) (addr proto.DeliveryAddress, reason proto.NoRecipientReason, ok bool, err error) // 包内唯一默认收件人产出点

// internal/collab/client/client.go（LedgerClient 增一方法；实现 internal/ledger/api.Facade，Ticket 0 恒报未实现）
SessionResident(sessionID string) (proto.SessionResident, bool, error)

// internal/proto/delivery.go
type DeliveryAddressKind string            // "seat" | "resident"
type DeliveryAddress struct { Kind DeliveryAddressKind; Session, Card, Identity string } // json: kind/session/card,omitempty/identity
func DeliveryCursorKey(a DeliveryAddress) string
type NoRecipientReason string              // structural | not_session_room | non_human_unaddressed | addressed_unresolved | root_no_seat | needs_mention
type Delivery struct { Addresses []DeliveryAddress; Default bool; NoRecipient NoRecipientReason }
func IsHumanAuthor(actor string) bool      // ⇔ ParseMemberIdentity 成功且种类 user
const SystemNotifierIdentity = "agent:handoff"
```

**判定表**（`Recipients`，按序首个命中即定）：

| # | 条件 | 结果 |
| --- | --- | --- |
| 1 | `BySystem` 或 `kind=pointer` 或载荷 `by_system:null` | 空集，`structural` |
| 2 | `msg.Room` 不是会话房间 | 空集，`not_session_room` |
| 3 | `IsAddressed(msg, replyAuthor)` 为真 | 显式分支：`ResolveDelivery` 每个 target → 本会话某卡当前席位身份 ⇒ 该卡席位地址；本会话驻场者身份 ⇒ 驻场者地址；其余丢弃。全丢 → 空集，`addressed_unresolved`。**不回落默认收件人** |
| 4 | 无寻址且 `!IsHumanAuthor(author)` | 空集，`non_human_unaddressed` |
| 5 | 无寻址、人类作者、会话有驻场者 | `[驻场者地址]`，`Default=true` |
| 6 | 无寻址、人类作者、无驻场者、根卡恰一张且有席位 | `[该卡席位地址]`，`Default=true` |
| 7 | 同上但该根卡空座 | 空集，`root_no_seat` |
| 8 | 同上但根卡零张或多张 | 空集，`needs_mention` |

会话根卡 = 会话内 `parent` 为空、或 `parent` 不在本会话卡集合中的卡。

### 3.3 ③ 席位模式（d_protocol 字面量 + d_ledger 列；Ticket 0 已落字面量与 DTO 键）

```go
// internal/proto/seat_mode.go
type SeatMode string
const ( SeatModePush SeatMode = "push"; SeatModePull SeatMode = "pull" )
func ParseSeatMode(raw string) (SeatMode, error)

// internal/proto/ledger.go
Card.DriverMode     string `json:"driver_mode,omitempty"`
CardView.DriverMode string `json:"driver_mode,omitempty"`
// internal/proto/sessions.go
SessionCard.SeatMode string `json:"seat_mode,omitempty"`
```

```sql
-- 两方言（实现节点落进 ddlStatements 与加列迁移）
ALTER TABLE cards ADD COLUMN driver_mode TEXT;   -- '' | 'push' | 'pull'
```

| 写入路径 | mode |
| --- | --- |
| `card bind <id>`（无 `--mode`）/ `--mode pull` | pull |
| `card bind --mode push` | **不冻结，退回 R1** |
| `card coordinate <id>`（不注册 `--mode`） | push |
| `card rebind --self` | pull |
| `card rebind --launch`、agentd `RebindSeat`（`scheddrain.go:325/:608`）、`BindSeat`（`:328`） | push |
| `clearSeatTx` | '' |
| 迁移存量非空座且 mode 为空 | bind → pull，coordinate → push |

### 3.4 ④ 驻场者来信（d_protocol 正文；文件名与目录沿用 B434）

```go
// internal/proto/resident_letter.go
const ResidentLetterTakeCmd = "handoff session wait "
type ResidentLetter struct { Session string; Count int; MinSeq, MaxSeq int64 }
func RenderResidentLetter(l ResidentLetter) string
// internal/proto/sessions.go
const SessionMemberResident = "resident"
type SessionResident struct { Session, Identity, DesktopSession, CLISession, Machine string; SatAt time.Time }
// json: session / identity / desktop_session,omitempty / cli_session,omitempty / machine,omitempty / sat_at
```

正文（金样本 `internal/proto/testdata/b435_resident_letter.golden`）：

```text
[handoff] 会话 session:12 有 3 条新消息寻址到你（账本 seq 101–108）
取信：handoff session wait session:12
```

文件名 `<SessionResident.CLISession>.<19 位零填充 UnixNano>.md`，目录 `$HOME/.handoff/desktop-wake`，tmp + Link 原子写——`writeDesktopLetter` 与 `b434-contract.md` 条 67–70 原样沿用。mod 对照：`register.ts` 只认首行 `[handoff]` 与上述文件名（C-405 CDC）。

---

## 4. 原子冻结清单

「T0」= 本轮 Ticket 0 已写对应测试且已跑 PASS（§10）；其余为实现节点对账条目。

### 4.1 ① `card wait`

101. `card wait <id>` 的命令名、位置参数与 `--subtree/--follow/--timeout` 语义不变。
102. 新增 `--since <seq>`：排他起点；卡事件段与房间命中段都从 `seq > N` 读。
103. 给了 `--since` 时不读持久游标。
104. 给了 `--since` 时不写持久游标。
105. stdout 首行恒为 `card_snapshot` 行，逐字同 `TestB435CardWaitLineShapes` 快照样本（T0）。
106. 其后每行是一条 `ledger.Event` 原样 JSON，逐字同金样本卡事件行（T0）；不新增行类型、不包裹。
107. 房间命中行 = `type="room_message"`、`card_id=""` 的事件行，逐字同金样本房间行（T0）。
108. 卡事件段可动作集不变：needs_human / needs_cleared / decision_opened / decision_answered 恒输出。
109. 卡事件段 task_mirrored 仍经 `client.JudgeMirroredWake` + `client.WaitDeliveryPolicy`。
110. 卡事件段起点：未给 `--since` 时 = 命令启动时 `MaxSeq`（不回放历史卡事件）。
111. `cardWaitEventActionable` 的 room_message 分支（`kind==user && !by_system`，`card_wait.go:238-253`）删除。
112. 房间命中段的持久游标键 = `DeliveryCursorKey(席位地址)` = 席位身份原样。
113. 房间命中段地址 = `<id>` 卡自身席位：`{seat, Session=该卡所属会话, Card=<id>, Identity=当前 driver_session}`。
114. `--subtree` 不扩展房间命中段地址（子卡席位各自 wait）。
115. 卡席位 mode≠pull（push 或空座）或卡不属任何会话时，房间命中段零输出，卡事件段照常。
116. 房间命中判定只经 `collab.Service.Matches`。
117. 房间命中段候选页的枚举与判定观察同一席位状态（沿用 B409r2 条 6–17 的 revision 护栏）。
118. 未给 `--since` 时，房间命中段先补收积压 `(游标, 启动时 MaxSeq]`。
119. 席位地址无游标行时，积压下界 = 该身份最近一条坐下/换绑事件（`EvDriverSeatBound`，或 `to=` 该身份的 `EvDriverTakeover`）的 seq。
120. 积压命中逐条以事件行输出，seq 升序，不合并成摘要行。
121. 积压段所有行先于实时段任何一行。
122. 房间命中行 stdout 写成功后才把持久游标推进到该行 seq。
123. stdout 写失败不推进持久游标，命令非 0 退出。
124. 持久游标写失败 → 命令非 0 退出（已写出的那行允许下次重投）。
125. 同一进程内不重复输出同一 seq。
126. 一次性模式：首个可动作行（积压命中、实时房间命中或卡事件）输出成功后退出 0。
127. 成员全部终态 → stderr「子树已全部完成」、退出 0（不变）。
128. `--timeout` 到点退出 124（一次性 = 总时长，`--follow` = 空闲上限，不变）。
129. 其余错误退出非 0 且不等于 124。

### 4.2 ① `session wait`

131. 用法改为 `handoff session wait <session> [--since <seq>] [--timeout <dur>] [--follow]`。
132. 位置参数不是会话房间 id（`room.IsSessionRoom` 为假）→ 非 0（≠124）退出、给出形状提示；旧 `session wait <member>` 形态落此支。
133. 会话无驻场者 → 非 0（≠124）退出，提示 `handoff session sit`，不阻塞。
134. 地址 = 启动时读到的驻场者：`{resident, Session=<session>, Identity=驻场者身份}`。
135. 持久游标键 = `DeliveryCursorKey(驻场者地址)` = `resident:<session>/<identity>`（T0 编码金样本）。
136. 不再以裸成员身份读写 `session_delivery_cursors`。
137. 命中判定只经 `collab.Service.Matches`（显式寻址驻场者 ∪ 默认收件人落到驻场者）。
138. 积压摘要行 `SessionBacklog` 形状不变（`testdata/session_backlog.json` 金样本不改）。
139. `SessionBacklog.member` 的值 = 驻场者身份。
140. 实时行 `SessionWake` 键集沿用 B358 条 43（`TestSessionWakeFixture` 不改）。
141. `SessionWake.unread` = `ListSessions(驻场者身份)` 中该会话的 Unread（条 44 同一投影）。
142. 输出即推进：stdout 写成功后推进到输出覆盖的最大命中 seq（B409r2 条 33/34/58/59）。
143. `--since` 只读回放（B409r2 条 36–38）。
144. 退出码：命中 0；超时 124；`--follow` 收 SIGINT/SIGTERM 退 0（条 46、B365 不变）。

### 4.3 ① 两命令共性与候选读

145. `cmd/card_wait.go`、`cmd/session.go` 非测试源码不调用 `MessageWakeTargets`、`AddressesCard`、`IsHumanAuthor`，不读 `.Mentions`（源码守卫）。
146. 两命令的游标存取经同一 `session_delivery_cursors` 表、以 `DeliveryCursorKey` 为键。
147. 顶层 `handoff wait <task>`（`cmd/wait.go:91`）不变。
148. 候选超集对席位地址 = 既有两支 ∪「该卡所属会话房间内、`mentions` 缺键或为空数组的无卡 room_message」。
149. 候选超集对驻场者地址 = 身份 @ ∪ reply→驻场者作者 ∪「该会话房间内 `mentions` 缺键或为空数组的无卡 room_message」。
150. 新支不按 `reply_to` 过滤（无效回复按无寻址处理，须进候选；权威裁决在匹配函数）。
151. 新支同受 B409r2 条 45–55 约束（有界返回、两方言同集同序、错误与取消语义）。

### 4.4 ② 匹配函数判定表

201. 人类作者 ⇔ `user:<name>`；`agent:*`、`agent:handoff`、席位、`cli:<u>@<h>`、`web:*` 均非人类（T0 `TestB435IsHumanAuthor`）。
202. 结构行（`BySystem` / `kind=pointer` / 载荷 `by_system:null`）→ 空集，`structural`（条 26 留）。
203. 非会话房间消息 → 空集，`not_session_room`。
204. `IsAddressed` 为真时只走显式分支，绝不回落默认收件人。
205. 显式分支：target 等于本会话某卡当前席位身份 → 该卡席位地址。
206. 地址种类字面量为 `seat` / `resident`（T0）。
207. 游标键编码：席位 = 身份原样；驻场者 = `resident:<session>/<identity>`；缺段或未知种类 = 空串（T0）。
208. 成员种类 `resident` 字面量（T0）。
209. `NoRecipientReason` 六值逐字冻结（T0）。
210. 系统发信身份 `agent:handoff`（T0）。
211. `Recipients(msg, author) (proto.Delivery, error)` 签名（T0 编译期断言）。
212. `Matches(msg, author, addr) (bool, error)` 签名（T0 编译期断言）。
213. 默认收件人产出点返回单个地址，返回值中无切片/映射/数组/通道（T0 `TestB435DefaultRecipientReturnsSingleAddress`）。
214. 显式分支：target 等于本会话驻场者身份 → 驻场者地址。
215. 显式分支：其余 target（空座卡、非本会话卡的席位、未驻场的 user:/agent:）丢弃。
216. 显式分支全部丢弃 → 空集，`addressed_unresolved`。
217. 显式分支人数 = 去重后地址数（ResolveDelivery 条 20–25 不变）。
218. 无寻址 + 非人类作者 → 空集，`non_human_unaddressed`。
219. 无寻址 + 人类作者 + 有驻场者 → 恰驻场者地址，`Default=true`。
220. 无寻址 + 人类作者 + 无驻场者 + 根卡恰一张且有席位 → 恰该卡席位地址，`Default=true`。
221. 同上，根卡恰一张但空座 → 空集，`root_no_seat`。
222. 同上，根卡零张或多张 → 空集，`needs_mention`。
223. 会话根卡 = 会话内 parent 为空或 parent 不在本会话卡集合的卡。
224. 默认收件人不排除作者本人。
225. `Default=true` ⇒ `len(Addresses)==1`。
226. `Matches(msg,a,addr)` ⇔ `addr` 逐字段等于 `Recipients(msg,a).Addresses` 中某项。
227. 地址解析与席位模式无关：push、pull 席位同为地址。
228. 匹配读账本失败返回错误；消费方不得把错误当未命中。
229. push 消费循环：席位地址且 mode=push → 恰一次 keystone `WakeMessage{Card:该卡}`（条 47 修订）。
230. push 消费循环：席位地址且 mode=pull → 零 keystone Wake。
231. push 消费循环：驻场者地址 → 零 keystone Wake。
232. 房间消息唤醒不沿 parent 上走：唤醒卡 = 命中地址的卡。
233. `wakeconsumer.go` 房间分流函数体调用 `Recipients`，不调用 `MessageWakeTargets`/`AddressesCard`（守卫 `:356` 改写）。
234. `desktopwake.go`（信箱桥）不调用 `MessageWakeTargets`/`AddressesCard`/`IsHumanAuthor`，不读 `.Mentions`；判「给不给驻场者写信」只经 `Recipients` 或 `Matches`。
235. 卡自动化事件的起始卡为 pull 席位 → 不起回合、不上走。
236. 卡自动化事件空座沿 parent 上走：遇 push 席位祖先 → 该祖先接手。
237. 卡自动化事件上走遇 pull 席位祖先 → 跳过继续上走（与现状无记录 bind 祖先同判）。
238. `needs_human` 不唤醒任何席位（`desktopNeedsHumanWakes` 退役；`automationWakeEvent` 不变）。
239. `ResolveDelivery` 签名与纯函数语义不变（条 19–27；`TestResolveDeliveryShapeHasNoMemberSet` 不改）。
240. `AddressesCard` 删除（疑似漂移，§1.3）。
241. `MessageWakeTargets` 在 collab 包外零调用。
242. `TestWakePathSourceUsesAddressing` 的禁用 helper 名单保留，扫描范围自动覆盖 `recipients.go`。
243. 系统通知 = agentd 以 `agent:handoff` 为作者 Send 的普通寻址消息：`by_system=false`。
244. 系统通知的 `Mentions` 恰为 `[驻场者身份]`。
245. 项目房间无驻场者 → 不发系统通知（只落卡事件）。

### 4.5 ③ 席位模式

301. 字面量 `push`（T0）。
302. 字面量 `pull`；`ParseSeatMode` 只接受这两值，大小写敏感、不去空白（T0）。
303. 落账位置 `cards.driver_mode` 列（两方言 TEXT）。
304. `driver_mode` 与 `driver_session/driver_source` 同一事务写入（BindSeat / RebindSeat）。
305. `Card` 与 `CardView` wire 键 `driver_mode`，空座不出键（T0）。
306. 卡 wire 不出现 `mode` 键（T0）。
307. `SessionCard` wire 键 `seat_mode`，空座不出键（T0）。
308. `clearSeatTx` 同事务把 `driver_mode` 置空。
309. 非空座席位 `driver_mode ∈ {push, pull}`；空座为空串。
310. `card show` 输出 `card.driver_mode` = 列值。
311. `card bind <id>` 不带 `--mode` → pull。
312. `card bind <id> --mode pull` → pull。
313. `card bind <id> --mode <非 push/pull>` → 非 0 退出，且不坐下。
314. `card coordinate <id>` → push，且不注册 `--mode`。
315. keystone `Wake` 对 pull 席位 no-op（替换 `keystone.go:123` 的来源判定）。
316. `wakeCoordinatorRoundID` 对 pull 席位零回合（替换 `scheddrain.go:463` 的来源判定）。
317. `resolveWakeCard` 按 mode 判（替换 `wakeconsumer.go:521` 的来源判定）。
318. `keystone.go`、`wakeconsumer.go`、`scheddrain.go` 非测试源码不出现 `DriverSource ==` / `DriverSource !=` 比较（源码守卫；写入调用传参不在禁令内）。
319. queue_release 合成唤醒（无账本事件）对 pull 席位直接放行（零结果、不回填）。
320. queue_release 对 push 本机席位起回合（不变）。
321. queue_release 对 push 远端席位显式失败回填（`scheddrain.go:509-520` 不变）。
322. push 席位无承载记录 → 既有缺承载显式路径（`EvSeatBearingMissing` + needs_human）不变。
323. `card rebind --self` → pull。
324. `card rebind --launch` 与 agentd 侧换绑/重建 → push。
325. 迁移：存量非空座、`driver_mode` 为空的行按来源回填（bind → pull，coordinate → push）。
326. 上条迁移幂等（二次执行零改动）；是全系统唯一以来源词决定模式之处。
327. 会话详情 `SessionCard.seat_mode` = 该卡 `driver_mode`。
328. 同一条命中消息：pull 席位 → keystone Wake 0 次且 `card wait` 输出 1 行；push 席位 → keystone Wake 1 次且 `card wait` 输出 0 行。

### 4.6 ④ 驻场者来信

401. 正文逐字节等于 `RenderResidentLetter` 金样本（T0）。
402. 正文恰两行：首行以 `[handoff] ` 开头；第二行 = `取信：handoff session wait <session>`（T0）。
403. 正文不含命中消息的正文、作者身份或任何第三方原文。
404. 文件名 `<CLISession>.<19 位零填充 UnixNano>.md`、目录与原子写沿用 `b434-contract.md` 条 67–70。
405. mod 零改动：`register.ts` 的标记、目录、取信正则与 `asUser: true` 字面量仍在（T0 CDC `TestB435ModStillMatchesLetterWire`）。
406. `SessionResident` 键集：全量 {cli_session, desktop_session, identity, machine, sat_at, session}；无桌面端地址时 {identity, sat_at, session}（T0）。
407. 一封信 = 一个驻场者地址的一批命中；`Count/MinSeq/MaxSeq` 覆盖本批。
408. 信的触发 = `Recipients` 给出该驻场者地址（含系统通知、人类无 @、显式 @驻场者）。
409. 驻场者无 `cli_session` → 零写信。
410. 写信不推进 `session_delivery_cursors`（B434 条 82 沿用）。
411. 写信不落账本事件、不经 keystone（B434 条 81 沿用）。
412. 写信后对 `desktop_session` 调 continue 深链（B434 条 83–84 沿用）。
413. 驻场者身份须为统一记法（`ValidateMemberIdentity`）。
414. 一房间至多一名驻场者（第二次 sit 被拒）。

---

## 5. B358 修订记录（`b358-contract.md` §4；由 finish 节点回写，本轮不改其正文）

| 条 | 处置 | 修订文本 |
| --- | --- | --- |
| 14 | 改 | 会话成员 = `Members`（显式，人与主 agent）+ 会话内各卡的当前席位 + **房间驻场者**（驻场者登记，一房间至多一名）；不新增其它成员表。 |
| 19, 20–25, 27 | 留 | 原文不变；默认收件人在其上的匹配函数层实现（本契约 §3.2）。 |
| 26 | 留 | 原文不变。 |
| 28 | 改 | `AddressesCard` 删除；「消息是否寻址命中该卡当前席位」由 `Service.Matches(msg, author, 席位地址)` 判定，卡无席位或账本缺失时不命中（语义等价，C-226/C-240）。 |
| 29 | 改 | 无寻址消息：人类作者（`user:` 记法）→ 唤醒恰一个默认收件人（有驻场者送驻场者，否则会话根卡恰一张且有席位送该卡席位，否则不唤醒）；非人类作者 → 不唤醒任何人。 |
| 30 | 改 | 唤醒路径只对匹配函数给出的地址唤醒；默认收件人分支命中 ≤ 1 个地址；显式寻址按发送者写下的人数（条 20–25）；按成员集合的广播形状删除。 |
| 31 | 改 | 系统结构事件（入群/移出/归档/席位变更/needs_human/needs_cleared）本身不唤醒任何人；其中 needs_human 额外触发一条项目房间通知（`agent:handoff` 普通寻址消息 @驻场者，C-243–C-245）。 |
| 39 | 改 | 订阅入口为 `handoff session wait <session> [--since <seq>] [--timeout <dur>] [--follow]`：作为该会话的驻场者收件，命中 = 匹配函数给出该会话驻场者地址（C-131–C-137）。 |
| 40 | 留 | 原文不变；现行缺省起点口径已由 session reliable-mentions contract v1 第 1 条替换为「读持久交付游标」（B409r2「被替代的既有条文」），本卡沿用该现行口径并把游标键改为地址键（C-135）。 |
| 41, 43, 44, 45, 46 | 留 | 原文不变（43 的「该成员」读作「驻场者身份」）。 |
| 42 | 留 | 命中判定唯一入口仍在 d_collab，由 `MessageWakeTargets` 扩为匹配函数 `Recipients/Matches`；通道内无第二份判定（C-145）。 |
| 47 | 改 | wakeconsumer 对会话房间消息按匹配函数地址分流：席位地址且 mode=push → 恰一次 keystone `WakeMessage{Card:该卡}`；席位地址且 mode=pull → 零 Wake，由 `card wait` 交付；驻场者地址 → 零 Wake（C-229–C-231）。 |
| 48, 50 | 留 | 原文不变。 |
| 49 | 改 | `card wait` 可经匹配函数收到寻址命中本卡 pull 席位的无卡会话房间消息（读侧为有界候选读，不走 Follow）；`Store.Follow` 本身语义不变，无卡事件继续被多路 wait 排除（C-148、C-115）。 |
| 守卫 `wakeconsumer_b358_test.go:62` | 改 | 人类作者无寻址 → 默认收件人（本会话单根卡 push 席位恰一次 Wake）；`agent:` 作者无寻址 → 零 Wake。 |
| 守卫 `:253`、`:274`、`:326`；`delivery_gate_test.go:25` | 留 | — |
| 守卫 `:356`、`delivery_gate_test.go:118` | 改 | `:356`：房间分流函数体必须调用 `Recipients`、不得调用 `MessageWakeTargets`（C-233）；`:118`：禁用 helper 名单保留，另加「默认收件人产出点返回单个地址」的形状守卫（C-213，T0 已在 collab 包）。 |

## 6. 其它既有契约的连带修订（由 finish 节点回写）

- **B409r2**（`2026-09-25-session-bounded-candidate-read-contract.md`）：条 2「可能定向到订阅 member」读作「可能命中订阅地址」；条 3–5 外增一支「无 mentions 的同会话无卡 room_message」（C-148–C-150）；条 19「命中订阅 member」读作「`Matches` 命中订阅地址」；条 28「无持久交付游标时 last_delivered_seq=0」对**席位地址**改为 C-119 的下界，对驻场者地址不变；条 56/57 不变。
- **B389**（`b389-contract.md` §2.2 规则 1/2、条 210–212、拍板 4）：本轮**不改**——承载形态执法仍按来源词（写面完整性，非唤醒判定）；`card bind --mode push` 的承载来源随 R1 裁决再议。
- **B399**：push 回合沿用 30 分钟上界、回合内续租、超时保留 session（spec §9），本卡不改其任何条目。

## 7. B434 处置清单（`b434-contract.md`；D10）

| 处置 | wire / 符号（b434-contract.md 出处） | 冻结条目 | 金样本/测试 |
| --- | --- | --- | --- |
| 退役 | `seat_desktops` 表、`SeatDesktop`、`SetSeatDesktop`/`SeatDesktopOf`/`deleteSeatDesktopTx`、`EvSeatDesktopSet`（§3.3 `:162-188`） | 45–63、66、85–111（卡级桌面端分流与失败段） | `cmd/card_desktop_seat_b434_test.go`、`internal/agentd/desktopwake_fail_test.go`、`desktopwake_remote_test.go`、`desktopwake_slice_test.go` 中桌面端席位用例随删 |
| 退役 | `card bind --desktop-session/--machine/--via`、`card seat desktop set`（§3.10 `:472-498`） | 42–61 中桌面端部分 | 同上 |
| 退役 | `card coordinate --desktop`、client `DesktopLauncherOpen`、`POST /api/cards/{id}/coordinator/desktop-launch`（§3.9 `:437-446`、§3.10 `:483-489`） | 252–254、257–261 | `cmd/card_coordinate_desktop_b434_test.go` 随删 |
| 退役 | `GET …/desktop-launch` 拉起材料与简报 `DesktopLaunchMaterial`（§3.2 `:141-160`、§3.5 `:216-248`） | 1–14、200–202 | `internal/agentd/desktopwake_material_test.go` 随删 |
| 退役 | composer 深链 `ClaudeNewSessionURLPrefix`、`ClaudeNewQMaxChars`、TS `claudeDeepLink.ts`（§3.1 `:122-139`、§3.12 `:510-548`） | 15–27 | `claudeDeepLink.test.ts` 随删；`TestB434DesktopWireLiterals` **改**：删 new/QMax 两断言，保留 marker/dir/prefix/continue |
| 退役 | 薄壳通道的 new-session 形态（§3.11 `:499-508`） | 31 | `external_deeplink_test.go` **改**：`/new` 改为被拒；28–30、32–41 保留 |
| 退役 | 拉起器代开 `openViaDesktopLauncher`、`renderLauncherLetter`、`/api/desktop/deliver` 与 `DesktopDeliverReq/Resp`、`DesktopLauncherOpenResp`（§3.2、§3.9.1 `:447-471`） | 144–158、194–222、243–251 | `desktopwake_launcher_test.go`、`TestB434DesktopDeliverReqWireKeys` 随删（若 R2 选候选乙，按新 DTO 另立，不沿用 card 键） |
| 退役 | 卡级信件 `desktopLetter` 与围栏渲染层（§3.6 `:249-341`） | 71–80、263–275、280 | `desktopwake_letter_test.go`、`TestB434DesktopQuoteFenceLiterals/QuoteEscape/LetterAuthorRoster/LetterNoBareQuote`、`testdata/b434_desktop_letter.golden` 随删 |
| 退役 | needs_human 唤醒桌面端席位 `desktopNeedsHumanWakes`、`desktopNeedsHumanSystemActors`（§3.8.1 `:408-436`） | 177–188、191 | `TestB434NeedsHumanSystemActorTable`、`desktopwake_needs_test.go` 随删 |
| 退役 | 控制台「用 Claude Code 坐下」「复制简报」「让拉起器代开」与 `desktopSeatCopy.ts`（§3.12） | 194–199、203–210、223–242 | `desktopSeatCopy.test.ts`、`CoordinatorPanel.test.tsx` 中对应用例随删 |
| 退役 | `handoff launcher register/show/unregister`、`handoff status` 拉起器段（§3.10） | 139–143、159–161 | `cmd/launcher_b434_test.go` 随删 |
| 语义替换 | `desktop_launchers` 表（§3.4 `:190-215`）→ 驻场者桌面端地址（`SessionResident` 的 identity/desktop_session/cli_session/machine） | 139–143 由 C-406、C-413、C-414 取代 | 新 DTO 金样本 `TestB435SessionResidentWire`（T0） |
| 改写 | 授权规则 `DesktopLetterAuthorizationRule` 与 SKILL「占座」节、CLAUDE.md 常设授权片段（§3.6 `:336`） | 262、276–278 | `TestB434DesktopLetterAuthorizationRuleLiteral` 随新文案改写（组织者授权，文案归 plan） |
| 保留 | 信箱目录与原子写入 `writeDesktopLetter`（§3.6 `:249-266`） | 67–70、81–82 | `desktopwake_mailbox_test.go` 保留 |
| 保留 | continue 深链 `claudeContinueURL`、`ClaudeContinueURLPrefix`、`ValidateDesktopSessionID`、`DesktopSessionPrefix` | 32、55–59、83–84 | 保留 |
| 保留 | mod 与 `skill install`（§3.13 `:550-566`） | 112–138、279 | `register.test.ts`、`claudemod_test.go` 保留；预填分支 121–124 无生产者（§1.3） |
| 保留 | 异机转交 `transferCoordinatorWake` + `CoordinatorWakeResp.LetterUnopened`（§3.7 `:342-373`） | 165–176 | `TestB434CoordinatorWakeRespLetterUnopenedWire` 保留；对驻场者是否可用挂 R2 |
| 保留 | `card needs` actor 改席位身份（§3.8.1 `:433`）、`needs_cleared` 不唤醒 | 189–190、192–193 | 保留 |
| 保留 | 依赖与图 | 162–164 | — |
| 保留 | 不带 `--desktop` 的 `card coordinate`、`card bind` 既有 stdout | 42–44、255–256、64–65 | 保留（64 的「来源仍为 bind」与 C-311 并存） |

退役以「生产路径删除 + 对应测试删除」为准，`desktop_launchers`/`seat_desktops` 的数据迁移按 spec §9（SQLite + PG 双方言、幂等、同一迁移内删旧列）。表中测试文件按文件名归档，个别文件混有保留用例（如 `desktopwake_remote_test.go` 含转交用例），由 plan 逐用例拆分。

---

## 8. 拍板记录（三重闸门）

**D-A 驻场者来信是「门铃」：只报会话、条数、seq 区间与取信命令，原文不进信；交付唯一经 `session wait`。**
难逆转：交付通道数决定游标数——改成「信内含原文」需要信箱桥（d_gateway）写 d_ledger 的交付游标（违背 B434 条 82）或接受两套进度，同时牵动 mod 提交的信任边界（B434 拍板 D7 的围栏/名册/授权规则要复活）与组织者恢复（S8）语义，跨 gateway/ledger/cli/skill 四处。无上下文会惊讶：B434 的卡级信内嵌原文、带围栏与作者名册，后人看到驻场者信只有两行会以为「漏了内容」并顺手「补上」。真取舍：被否方案甲——沿用 B434 卡级信形态（原文 + 围栏）且写信不推进游标：组织者从游标恢复时会重放已经由信交付并处理过的消息；被否方案乙——信含原文且写信即推进驻场者游标：组织者若同时跑 `session wait`，同一消息经两条通道各交付一次，且「信落盘 = 已交付」与 mod「先删后提交、提交失败即丢」（`register.ts:109-120`）冲突。**反过来写不会有任何测试变红**（C-401 只锁当前正文；改成含原文的新金样本即可过）——这是本记录存在的主要理由。显式不做：信内不放消息正文、不放作者身份、不放未读数。

**D-B 「解析结果 ≤ 1 人」只约束默认收件人分支；显式寻址人数按发送者写下的人数。**
难逆转：匹配函数返回形状（`Delivery.Addresses` 为切片、默认分支为单值）被 d_gateway 消费循环、两个 wait、信箱桥四方编译依赖，源码守卫（C-213）与 B358 条 20–25 的金样本同时依赖这一切分。无上下文会惊讶：spec §4 E / D4 / §9.1 条 30 都写「恒 ≤ 1 人」，后人读到 `Addresses []DeliveryAddress` 会以为违反 spec 而改成单值或截断首个。真取舍：被否方案——一切消息 ≤1（多 @ 只取首个或拒发）：推翻 spec 同表明文「留」的条 23/25，且 spec 未给多 @ 的处置，等于让契约替 spec 发明规则；本方案的依据是同一张 §9.1 表把条 20–25 列为「留」、§3「找别人显式 @」与 §4 E「每次 @ 很麻烦」的上下文只针对无寻址消息。**请协调者在交棒时确认此读法**（若 spec 意图是前者，退回 spec 改条 20–25）。

**D-C 席位地址游标无行时，积压下界取该身份最近一次坐下/换绑事件 seq，而非 0。**
审过不立：可逆（单包 CLI 读法，改回 0 只动 `cmd/card_wait.go`），不满足「难逆转」；以 C-119 冻结为条目，理由记台账 §五.4。

审过不立（逐条记判定依据，防「空着与没审过不可区分」）：
- 地址类型放 proto 而非 collab——零方向变更的落点选择，被否方案（collab 自有类型）会撞 `d_gateway→d_collab` 只许门面容器的 check，有工具变红，不满足「反写无测试变红」的惊讶条件。
- wire 键名 `driver_mode`/`seat_mode` 而非 `mode`——有 T0 断言（C-306），反写即红。
- 游标键编码——有 T0 金样本（C-207），反写即红。
- card wait 卡事件段起点仍为 MaxSeq——spec D2「照旧」直接决定，无取舍空间。

---

## 9. 退回 spec（不自选；协调者/用户裁决后回本节点补签）

- **R1 `card bind --mode push` 的承载来源。** push 起回合需要承载（carrier + machine，`scheddrain.go:496-520`）；B389 规则 1/2 按来源词执法「bind 必零承载」（`binding.go:217`），`SetSeatBearing` 只收 coordinate（`:175`）——spec D1 允许的 bind+push 在现有写面上不可运行。两处规则分属 d_ledger（承载形态）与 d_gateway（push 驱动），spec 未定。候选：(a) 本期拒绝 `bind --mode push`（退出非 0，指路 `card coordinate`）；(b) `card bind --mode push` 增 `--carrier/--machine`，B389 规则 1/2 改按 mode；(c) bind+push 允许零承载、走缺承载修复路径，`SetSeatBearing` 放开到 push 席位。本轮只冻结 bind 缺省 pull（C-311）与非法值拒绝（C-313）。
- **R2 驻场者跨机写信路径。** spec D10 保留「异机投递沿既有 `transferCoordinatorWake` + `LetterUnopened`」，但该端点是卡路径 `/api/cards/{id}/coordinator/wake`、以 `card.DriverSession == req.Seat` CAS（`coordapi.go:178-195`），驻场者不是卡席位，保留项与驻场者语义不相容。候选：(甲) 只由驻场者桌面端地址所在机的 agentd 写信（每台 agentd 都跑消费循环，`scheddrain.go:54-66`），不转交；(乙) 以会话 + 驻场者身份为键新立转交 DTO（可复用 `/api/desktop/deliver` 路由但不沿用 `card` 键）。本轮冻结信正文与文件名（C-401–C-406），不冻结由哪台机器写。
- **R3 S1 剧本与 D4 人类作者判据冲突。** S1 用 CLI `session send` 无 @ 发言期望唤醒协调者；CLI 人形发言作者是 `cli:<USER>@<host>`（`cmd/ledgercli.go:44-51`、`cmd/session.go:881-897`），按 D4 字面（`user:` 记法）不是人类作者 → 不触发默认收件人。本轮按 spec 字面冻结 C-201。候选：(a) S1 改用控制台/移动端发言；(b) d_cli 让人形 `session send` 出示 `user:` 身份（如读 `console_user` 配置或 `--as user:<n>`），属 d_cli 新面，非本轮四缝。

---

## 10. Ticket 0 与可执行冻结

### 10.1 Ticket 0 已落（零业务行为）

| 文件 | 内容 | 是否骨架 |
| --- | --- | --- |
| `internal/proto/seat_mode.go`（新） | `SeatMode`、`SeatModePush/Pull`、`ParseSeatMode` | 字面量解析即 wire 定义 |
| `internal/proto/delivery.go`（新） | `DeliveryAddressKind/DeliveryAddress`、`DeliveryCursorKey`、`NoRecipientReason` 六值、`Delivery`、`IsHumanAuthor`、`SystemNotifierIdentity` | 编码与判据即 wire 定义 |
| `internal/proto/resident_letter.go`（新） | `ResidentLetterTakeCmd`、`ResidentLetter`、`RenderResidentLetter` | 正文渲染即 wire 定义 |
| `internal/proto/ledger.go`（改） | `Card.DriverMode`、`CardView.DriverMode` | DTO 键 |
| `internal/proto/sessions.go`（改） | `SessionMemberResident`、`SessionCard.SeatMode`、`SessionResident` | DTO |
| `internal/collab/recipients.go`（新） | `Service.Recipients/Matches/defaultRecipient` | **空壳**：恒返回 `errRecipientsNotImplemented` |
| `internal/collab/client/client.go`（改） | `LedgerClient.SessionResident` | 接口缝 |
| `internal/ledger/api/api.go`（改） | `Facade.SessionResident` | **空壳**：恒返回未实现错误（不报「无驻场者」冒充成功） |
| `internal/collab/readmodel_test.go`（改） | `fakeLC.SessionResident` 编译期占位 | 测试替身 |

未落（刻意）：DDL 与迁移、`card bind --mode` flag、两个 wait 的改造、消费循环改接、驻场者登记与 `session sit/stand`、TS 镜像——均为实现节点行为或无法本机编译验证（`web/node_modules` 缺），进 §12。① 不建骨架（沿用既有命令），只补现状金样本。

### 10.2 契约测试（本轮新增，全部已跑 PASS）

- `internal/proto/b435_contract_test.go`：`TestB435SeatModeLiterals`（C-301/302）、`TestB435CardDriverModeWireKey`（C-305/306）、`TestB435SessionCardSeatModeWireKey`（C-307）、`TestB435SessionResidentWire`（C-208/406）、`TestB435DeliveryAddressCursorKey`（C-206/207）、`TestB435NoRecipientVocabulary`（C-209/210）、`TestB435IsHumanAuthor`（C-201）、`TestB435ResidentLetterGolden`（C-401/402，`testdata/b435_resident_letter.golden`）、`TestB435ModStillMatchesLetterWire`（C-405，CDC 读 `register.ts`）。
- `internal/collab/recipients_b435_test.go`：编译期签名断言（C-211/212）、`TestB435DefaultRecipientReturnsSingleAddress`（C-213）、`TestB435RecipientsShellNeverSucceeds`（空壳不得成功；实现节点删）。
- `cmd/card_wait_b435_test.go`：`TestB435CardWaitLineShapes`（C-105/106/107，锁现状，改造后不得改期望值）。

### 10.3 本轮实际跑过的命令（原始输出见台账 §五.5）

- `go build ./...` → 退出 0。
- `go vet ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` → 退出 0。
- `go test ./internal/proto/ ./internal/collab/... ./internal/ledger/api/` → 4 包 ok，退出 0。
- `go test -count=1 -run TestB435 ./internal/proto/ ./internal/collab/` → ok/ok。
- `go test -run 'TestB435|TestB353CardWait|TestB409CardWait' -v github.com/Xsxdot/handoff/cmd` → 15 支 PASS，`ok`。
- `go test -count=1 -run NONE_B435 ./...` → 全仓测试二进制编译通过，退出 0。

无哈希、密钥派生或加密编码命中。

---

## 11. 依赖方向、组装点、预算与图

- **`codegraph/target.json`：不改**——无新方向、无新组装点、无预算变化（§2）。组装点仍是 `main.go`、`internal/agentd/server.go`、`cmd/agentd.go`；CLI 侧仍是 `cmd/room.go#roomServiceFor`。
- **分支视图 diff**：`codegraph/diffs/cards-B435-charter-1.json`（分支 `cards/B435-charter-1` 扁平化），新增 15 节点（7 model + 8 func）、修改 4 节点（`Card`/`CardView`/`SessionCard`/`LedgerClient`）、新增 1 边（`IsHumanAuthor → ParseMemberIdentity`）；常量与哨兵不入图。
- `codegraph validate` → `issues: null`，视图含 `cards-B435-charter-1`。
- `codegraph --view cards-B435-charter-1 check` → 3 处 over-budget，**与不带视图的基线 check 完全相同**（d_cli→d_maintenance、d_gateway→d_collab、d_ledger→d_protocol 存量），本视图未引入新违规。

## 12. 本节点欠账（实现节点逐条补齐，不得静默带走）

1. **R1/R2/R3 未裁**：bind+push 承载、驻场者跨机写信、S1 人形作者——裁决后回本节点补签对应条目。
2. **D-B 读法待协调者确认**（§8）。
3. 两个 wait 的改造与 C-101–C-151 全部行为条目（Wave 0 至少 C-101–C-129、C-145–C-151 的席位半边）。
4. 匹配函数落码替换空壳；删除 `TestB435RecipientsShellNeverSucceeds`；守卫改写（§5 三行）。
5. `cards.driver_mode` DDL（两方言）、加列迁移与回填（C-303、C-325/326）；`ledger.Card` 投影与 Facade 映射。
6. `card bind --mode`、`card rebind` 写 mode、`card show`/`session detail` 投影（C-310–C-314、C-323/324、C-327）。
7. 驻场者登记表、`Store` 读写、`Facade.SessionResident` 落码；`session sit/stand`（含驻场者身份来源 flag，§13）。
8. 消费循环改接（C-229–C-238）、四处来源判定改读 mode 与源码守卫 C-318。
9. 信箱桥改写为驻场者门铃信（C-401–C-412）；驻场者写信失败的处置（B434 失败段随卡级席位退役，驻场者侧无现成语义——归 plan，必要时回 contract）。
10. B434 退役清单（§7）落地与迁移；`desktop_launchers`/`seat_desktops` 行迁移留痕（S7）。
11. TS 镜像：`web/src/api/ledger.ts` 的 `SeatMode`/`driver_mode`、会话 DTO 的 `seat_mode`/`resident`、`contract.test.ts` fixture（本机无 `node_modules`，未编译验证）。
12. 图覆盖债：`runSessionWait`、`sessionWaitMatch` 未入图；`Service.MessageWakeTargets` 图中签名陈旧。
13. 系统通知（C-243–C-245）与项目房间（D6）的落码；needs_human 通知事件表（D8）。

## 13. 移交 plan 附区（不占冻结名额、不参与逐条对账；plan 出稿时吸收并在此标注销区）

- 默认收件人的根卡计算可复用 `GetSession(id).Cards` + `GetCard` 逐卡读 `ParentID`；不必新增 LedgerClient 方法。
- `session sit` 需要一个驻场者身份来源（spec 未给 flag）；建议 `--as user:<n>|agent:<n>`，缺省读控制台 `console_user`——属 d_cli 实现选择，若成为他方依赖再回 contract。
- card wait 房间命中段与卡事件段可共用一个按 fence 推进的轮询循环（两段各自读到同一 `MaxSeq` 上界后合并按 seq 输出），满足 C-121/C-125；不建议两 goroutine 各自写 stdout。
- 信箱门铃按「消费循环一批 × 一个驻场者地址」聚合成一封（C-407）；`Count` 取本批命中条数。
- `SessionMember` 的 `resident` 种类在 `sessionMembers`（`internal/collab/sessions.go:386`）派生时追加一行，`Identity` 为驻场者身份；S9 升格后同一人以 resident 与 seat 两行出现。

## 14. 本轮法定核对（收尾自检 5 项）

1. **契约增量文档落盘**：本文件。§1 现状签名带 `file:line`/`file#Symbol` 与提交 `7f860685`；§4 每条有缝归属，§2 有冻结理由与生产者/消费者，§3 给兼容边界；新建面引用本轮 Ticket 0（§10.1）。
2. **图**：引入新符号 → 分支视图 diff `codegraph/diffs/cards-B435-charter-1.json` 与骨架同批提交；`target.json` 未触及（记明不适用，不造空变更）；`validate` 零 issue。
3. **Ticket 0**：需要（②③④ 新建面两侧独立编译）；`go build ./...` 退出 0、全仓测试二进制编译退出 0（§10.3）。① 不建骨架。
4. **可执行冻结**：wire 形状（mode 字面量与键、地址游标键、`SessionResident` 键集、来信正文、mod 对照、card wait 两种行）均有金样本/CDC 且本轮跑过 PASS（§10.2）；`SessionWake`/`SessionBacklog` 沿用既有金样本不改。
5. **拍板记录**：命中两条（D-A 门铃信、D-B ≤1 的作用域），另审过不立五条并记依据（§8）。
