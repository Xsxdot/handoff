# B434 契约增量：Claude 桌面端会话当协调者——深链创建、信箱唤醒、会话内坐席

**上游状态：已批准**（源 spec：`docs/superpowers/specs/b434.md`，头部状态行「**状态**：**已批准**（r2；用户 2026-10-16：「批准 B434 spec r2。回写批准到 spec 头部，把卡移到 contract 列。」）」——本轮已对工作树复核一致，无需回写）
**级别：L3 ｜ 选档复核：重档确认**（依据见 §8）
**冻结状态：r2 修复轮（2026-10-16，第二轮审阅后，跨机失败路径收口）；r1 修复轮（审阅 fail 后）：本提交随 Ticket 0 骨架、字面量金样本、spec r2.1 勘误与台账冻结；`codegraph/target.json` 不变（无新方向，§5）；分支视图 diff 与直通竖切因本机无 Go/Node 工具链未落，列欠账（§9）**
**有效基线：** `main` @ `e020380a`（worktree `handoff-B434`，分支 `cards/B434-charter-1`）
**架构形态：** 按子系统分域的平铺领域包，无横向 controller/service/dao 分层（沿用 `codegraph/best.json`；本卡不新增领域）
**交棒：** breakdown。**P1–P3 已由协调者拍板（§7.1）；仍带两项待用户裁决（P4 阻塞 S3 控制台入口、P5 随退回 spec 候选 R1），breakdown 可先拆 S1/S2，S3 控制台部分与 needs_human 唤醒条目待裁决。**

本文档把已批准 spec r2 的「契约语义与接缝」翻译成现状代码可接的签名、HTTP/CLI 面、文件 wire 与依赖方向。本节点只落空壳（§9.1）；写信箱、发深链、拼简报、唤醒分支、CLI 与控制台接线全部列入交棒欠账（§10），不能被「已有空壳」冒充完成。

---

## 1. 现状查证

### 1.1 已查证签名与代码事实

行号是本轮（`e020380a`）读数，未来以符号锚决议。原始读数见台账「contract 节点」小节。

| 接缝 | 现状代码事实 | 现状出处 |
| --- | --- | --- |
| 协调能力矩阵 | 仅 opencode `CapCoordination=true`；claude/grok/agy/codex 为 false | `internal/executor/capability.go#BaselineReport`（`:185-204`） |
| 叫机器人能力闸 | `coordinatorRunner.Launch` 以 `reg.Require(name, CapCoordination)` 拒绝非 opencode | `internal/agentd/server.go#coordinatorRunner.Launch`（`:1456`，闸 `:1459`/`:1497`） |
| 协调者 HTTP 族 | 六路由 `/api/cards/{id}/coordinator{,/launch,/rebind,/forget,/wake}` + `/attach`，均 `withLedger(withCoordinator(…))` | `internal/agentd/coordapi.go#registerCoordRoutes`（`:43-50`） |
| 叫机器人入口 | `handleCoordLaunch`：source 只收 coordinate → 小队识别 → 准入 → LaunchForCard | `internal/agentd/coordapi.go#handleCoordLaunch`（`:101`） |
| 跨机唤醒转交（被调端） | `handleCoordWake`：CAS `card.DriverSession == req.Seat` → `automationWakeEvents` 重映射 → `wakeCoordinatorRoundRaw` | `internal/agentd/coordapi.go#handleCoordWake`（`:172`） |
| 项目本机根目录 | `resolveCoordWorkdir`：`s.mgr.ListProjectLocations()` + `workspace.ResolveProject("", card.Project, …)` → `loc.Path`；未登记返回空串 | `internal/agentd/coordapi.go#resolveCoordWorkdir`（`:316`）、`internal/workspace/projectresolve.go#ResolveProject`（`:79`） |
| 项目位置 = 本机 | `ProjectLocation.Path`「该机器上的绝对路径」；登记表是 agentd 本机 store | `internal/proto/proto.go#ProjectLocation`（`:453`）、`internal/orchestration/facade.go#Manager.ListProjectLocations`（`:73`） |
| 本机判据（地址） | `config.IsSelfTarget(listen, target)`；`Server.IsSelfTarget(name)`：空串=本机，登记名经地址判定 | `internal/config/listenclass.go#IsSelfTarget`（`:80`）、`internal/agentd/server.go#Server.IsSelfTarget`（`:519`） |
| 本机判据（机器名） | `scheduling.IsLocalMachine`：空串 / `local` / `本机` / `os.Hostname()` | `internal/scheduling/scheduling.go#IsLocalMachine`（`:182`） |
| 目标客户端选路 | `clientForTarget`：`CanonicalTarget` 归一本机，否则 `pool.For` | `internal/agentd/server.go#clientForTarget`（`:546`） |
| keystone 唤醒 | `Service.Wake`：空座/bind → `Forget` 返回零值（bind 不发起回合） | `internal/keystone/keystone.go#Service.Wake`（`:106`，bind 分支 `:123-126`） |
| 无头简报 | `briefing` 原文「**你是一次性回合**：没有交互窗口」 | `internal/keystone/keystone.go#Service.briefing`（`:334`） |
| 一次性回合 argv | `[bin run --format json -m <model> -s <session> -- <prompt>]` | `internal/hostapi/driver.go#buildArgv`（`:240`） |
| 席位编码 | `cli:<cli>#<session_id>`；`#` 唯一分隔符；来源 `bind\|coordinate` | `internal/proto/seat.go#EncodeSeatIdentity`（`:41`）、`#ParseSeatIdentity`（`:53`）、`#SeatSourceBind`（`:13`） |
| 当前会话出示 | flag → `HANDOFF_SESSION_CLI/ID` → `GROK_SESSION_ID`/`CLAUDE_CODE_SESSION_ID`（二者并存即报错） | `cmd/card_seat.go#currentSeatIdentity`（`:23`） |
| card bind | `currentSeatIdentity` → `st.BindSeat(id, identity, SeatSourceBind, ledger.SeatBearing{})`；flag 仅 `--cli/--session` | `cmd/card_driver.go#cardBindCmd`（`:20`，写面 `:38`，flag `:164-165`） |
| 席位原子写 | `BindSeat` 同事务写席位 + 承载 + `EvDriverSeatBound`；`RebindSeat` CAS 换绑 | `internal/ledger/binding.go#Store.BindSeat`（`:34`）、`#Store.RebindSeat`（`:89`） |
| 承载记录 | `SeatBearing` 只随 coordinate 席位存在；bind 带任何承载字段即 `ErrBadState`（B389 规则 4） | `internal/ledger/binding.go#SeatBearing`（`:196`）、`#validateSeatBearing`（`:211`） |
| 清座 | `clearSeatTx` 同事务清席位三列 + 删承载行 + 递增席位版本 | `internal/ledger/binding.go#Store.clearSeatTx`（`:277`） |
| 唤醒目标解析 | `resolveWakeCard`：事件卡 bind → 空；空座沿 parent 上走，bind 祖先跳过 | `internal/agentd/wakeconsumer.go#resolveWakeCard`（`:492`） |
| 唤醒回合（第二处 bind 跳过） | `wakeCoordinatorRoundID`：bind → `keystone.Forget` + 日志「bind 席位跳过协调者唤醒」返回零值；远端承载走 `transferCoordinatorWake` | `internal/agentd/scheddrain.go#Server.wakeCoordinatorRoundID`（`:436`，bind 分支 `:451-454`，转交判据 `:474`） |
| 跨机转交（发起端） | `clientForTarget(machine)` + `MarkForwarded().CoordinatorWake`，失败本机落 wake_round fail | `internal/agentd/scheddrain.go#Server.transferCoordinatorWake`（`:361`） |
| 消费循环 | 读 `EventsFromAsc` → `automationWakeEvents` → `resolveWakeCard` → 认领 → 退避 → `keystone.Decide` → `wakeCoordinatorRoundRaw` | `internal/agentd/wakeconsumer.go#consumeAutomationEventsOnce`（`:636`） |
| 唤醒事件集 | `automationWakeEvent`：task_mirrored（策略过滤）、decision_*；`needs_human`/`needs_cleared`/`seat_bearing_missing` 恒不唤醒 | `internal/agentd/wakeconsumer.go#automationWakeEvent`（`:216`） |
| 会话消息寻址 | 会话房间人类消息经 `MessageWakeTargets`，命中会话内卡当前 `driver_session` → `WakeMessage{Card}` | `internal/agentd/wakeconsumer.go#roomMessageWakeEvents`（`:330`） |
| card wait 可动作集 | needs_human/needs_cleared/decision_* 恒 true；room_message 判 `kind==user && !by_system`；task_mirrored 经身份闸 + 策略 | `cmd/card_wait.go#cardWaitEventActionable`（`:233`） |
| session wait 命中 | `sessionWaitMatch` 经 `MessageWakeTargets`、member 逐字相等 | `cmd/session.go#sessionWaitMatch`（`:501`） |
| 等人标记 | `MarkNeedsHuman(card, reason, actor)` 落 `EvNeedsHuman{reason}` | `internal/ledger/events.go#Store.MarkNeedsHuman`（`:450`） |
| 插件交付 | `main.go` `//go:embed plugins/opencode-monitor/handoff-monitor.ts` → `cmd.SetPluginContent`；`skill install` 调 `InstallPlugin`、`skill` 调 `PluginStatus` | `main.go:24`、`cmd/skill.go:67/:95`、`internal/skill/plugin.go#PluginRelFile`（`:16`）、`#InstallPlugin`（`:25`）、`#PluginStatus`（`:54`） |
| 薄壳 raw message | 依次试剪贴板、系统浏览器两个处理器，都不认则 Debug 忽略 | `desktop/main.go:139-170` |
| 薄壳协议前缀（样板） | `ExternalBrowserMessagePrefix = "handoff:open-browser:"`；只放 http(s) 同源 | `desktop/internal/shell/external_browser.go:21`、`#HandleExternalBrowserMessage`（`:36`）、`#validateExternalBrowserURL`（`:143`） |
| 前端协议前缀（样板） | `OPEN_BROWSER_MESSAGE_PREFIX = 'handoff:open-browser:'`，经 `webkit.messageHandlers.external.postMessage` 发 | `web/src/app/lib/desktopShell.ts:52` |
| 卡页协调者面板 | 空座只有「▶ 叫机器人」；已绑定有「打开终端」「换绑：叫机器人」 | `web/src/app/cards/CoordinatorPanel.tsx:146-147` |
| 协调者 TS 客户端 | `getCoordinatorStatus`/`launchCoordinator`/`rebindCoordinatorLaunch` 等，路径段 `encodeURIComponent` | `web/src/api/scheduling.ts:245-290` |
| HTTP 错误体 | `writeErr` → `{"error": err.Error()}` | `internal/agentd/ledgerapi.go#writeErr`（`:101`） |

### 1.2 依赖库/外部现实的既成行为（与签名同等承重）

| 行为 | 读数 | 出处 | 状态 |
| --- | --- | --- | --- |
| `claude://code/new?folder=&q=` 只开草稿，q 预填不自动发送；首次文件夹弹信任框 | 台账 15:55、16:25 读数 | `b434-ledger.md` | 已验 |
| `claude://code/continue?session=local_<id>` 能把被回收会话拉起，无弹框 | `warmed successfully in 45ms` | 台账 16:23、16:44 读数 | 已验 |
| q 上限 14,336 字符 | 子代理读 app.asar | 台账 15:4x 调研 | 文档外读数，未逐字节复核 |
| 桌面端会话闲置 900s 回收 | `WarmLifecycle … 900s` | 台账 16:1x 读数 | 已验 |
| mod `$.session.id()` = CLI 会话 id；`$.prompt.submit` 独立回合；mod 随会话进程回收 | 原型 + d.ts | 台账 16:2x、16:28 读数；原型 `register.ts`（只读） | 已验 |
| `CLAUDE_CODE_PLUGIN_DIRS` 接受「绝对路径或 ~ 开头」的插件目录 | CLI 2.1.295 二进制字符串 | 台账 contract 节点读数 | **多目录分隔符未钉**——plan 前必须钉（欠账 §10.6） |
| Wails `app.Browser.OpenURL` 对 `claude://` 自定义 scheme 的行为 | 未读到源码（本机无 Go 模块缓存） | — | **未钉**——plan 前必须钉（欠账 §10.7） |
| launchd 托管的 agentd 进程里 `open claude://…` 能否到达 GUI 会话 | spec 实现决定末条点名唯一未验项 | spec | **未验**——S2 真机验收前置 |

### 1.3 对侧常量查执法（谁真的发出、谁真的消费）

| 常量/机制 | 生产者 | 消费者 | 结论 |
| --- | --- | --- | --- |
| `proto.SeatSourceBind="bind"` | `cmd/card_driver.go:38/:126`（bind、rebind --self） | `keystone.Wake:123`、`resolveWakeCard`、`wakeCoordinatorRoundID:451` | 活跃。本卡**不改**来源词表；桌面端席位仍是 bind |
| `ExternalBrowserMessagePrefix` / `OPEN_BROWSER_MESSAGE_PREFIX` | `desktopShell.ts#requestOpenCurrentPageInBrowser` | `desktop/main.go:164` → `HandleExternalBrowserMessage` | 活跃；两侧各一支字面量测试（`external_browser_test.go:22`、`desktopShell.test.ts:71`）——本卡新前缀照此形态执法 |
| `EvNeedsHuman` | `MarkNeedsHuman`（keystone/agentd/CLI 多处） | `card wait`（可动作）、会话列表待办、`automationWakeEvent`（**不唤醒**） | 活跃；两个消费者对它的「是否叫醒协调者」判定相反——见退回 spec 候选 R1 |
| `[handoff]` 标记 | 原型 mod（只读）；本卡起 `desktopBrief`/`desktopLetter` | 原型 mod 预填代发判定；项目 CLAUDE.md 常设授权文本（S3） | 新常量，唯一定义点 `proto.DesktopWakeMarker` |
| `~/.handoff/desktop-wake` | 本卡起控制面写信 | mod 取信 | 新 wire；目录已因探针存在（空），非事实源 |
| `SeatBearing`（B389） | `BindSeat`(coordinate)/`SetSeatBearing` | `scheddrain.go:460`（读到即按 coordinate 处理） | 活跃；**不得**承载桌面端身份（拍板 D1） |

本表无零使用死常量被当作事实源。

### 1.4 图覆盖债

本机无 `codegraph` 二进制，且无 Go 工具链可现场构建（`go build -o … ./cmd/codegraph` → `command not found: go`，退出 127；`find /` 无 go 二进制）。因此本文全部用 `file#Symbol` 源码锚，未跑 `codegraph sym/who-calls/resolve --doc`；分支视图 diff 未产出（不手写伪造图产物）。欠账见 §9.3、§10。

---

## 2. 架构户口与依赖方向

### 2.1 户口（不新增领域）

| 产物 | 归属 | 依据 |
| --- | --- | --- |
| 拉起材料、简报模板、来信渲染、写信箱、发 continue 深链、唤醒分支、拉起器代开 | `d_gateway`（`internal/agentd/desktopwake.go`，新文件） | spec「写信箱与发深链是 gateway 对本机的出站 I/O，归 d_gateway，不进 d_execution」 |
| 桌面端席位记录、拉起器登记 | `d_ledger`（`internal/ledger/seat_desktop.go`，新文件） | 席位权威在账本（`binding.go` 文件头）；拉起器要跨机可见（拍板 D4） |
| wire 字面量与 DTO | `d_protocol`（`internal/proto/desktop_coordinator.go`，新文件） | proto 是 wire 唯一定义处 |
| CLI flag/子命令 | `d_cli`（`cmd/card_driver.go`、新 `cmd/card_seat_desktop.go`、新 `cmd/launcher.go`） | 沿用 `card bind` / `card seat bearing set` 直读账本形态 |
| 薄壳深链通道 | `desktop/internal/shell/external_deeplink.go`（新文件，desktop 子模块） | 与 `external_browser.go` 同形 |
| 控制台入口与编码 | `d_web_cards`（`CoordinatorPanel.tsx`）+ `web/src/app/lib/claudeDeepLink.ts`（新）+ `desktopShell.ts` + `api/scheduling.ts` | spec 测试缝 2 |
| mod 正式版 | `plugins/claude-desktop-wake/`（新目录，独立交付物，不 import 主模块） | spec 方案 4 |
| mod 安装 | `internal/skill/claudemod.go`（新文件）+ `main.go` embed + `cmd/skill.go` | 与 `plugin.go` 同形 |

### 2.2 依赖方向

本卡**不新增跨域方向、不改预算**（§5）：

- `d_cli → d_ledger`：既有 `entries: ["ledger.Store"]`。`card bind --desktop-session`、`card seat desktop set`、`launcher register/show/unregister` 走 `ledger.Store` 新方法。
- `d_gateway → d_ledger`：既有 `entries: ["ledger.Store", …]`。`SeatDesktopOf`/`DesktopLauncherOf`/`ListDesktopLaunchers` 新方法经 `s.ledger`（`*ledger.Store`）。
- `d_gateway → d_protocol`：既有 `entries: ["proto 实体", "proto（包级函数）", …]`。
- `d_gateway → d_transport`：既有 `entries: ["targetclient.Pool", "client.Client", …]`。跨机席位唤醒复用 `transferCoordinatorWake` 既有边。
- `d_gateway → d_keystone`：**不新增调用**。桌面端席位分支对 `keystone.Wake`/`LaunchForCard` 零调用；消费循环里既有的 `keystone.Decide`（attach 暂缓判定，只读）不变。
- web → gateway：走既有 `/api/cards/{id}/coordinator` 一族（新增 `desktop-launch` 子路径）。
- mod → 主模块：零 import；共享字面量由金样本锁（§6）。

---

## 3. 契约增量：精确签名

### 3.1 wire 字面量（`internal/proto/desktop_coordinator.go`，新增，Ticket 0 已落）

```go
const DesktopWakeMarker   = "[handoff]"               // :17
const DesktopWakeDirRel   = ".handoff/desktop-wake"   // :20  相对 $HOME
const DesktopSessionPrefix = "local_"                  // :24
const (
    ClaudeNewSessionURLPrefix = "claude://code/new?"               // :30
    ClaudeContinueURLPrefix   = "claude://code/continue?session="  // :31
)
const ClaudeNewQMaxChars = 14336                       // :36
const (
    SeatDesktopViaPage     = "page"      // 卡页「用 Claude Code 坐下」
    SeatDesktopViaLauncher = "launcher"  // 拉起器代开
    SeatDesktopViaManual   = "manual"    // 手填（card seat desktop set / 未带 --via）
)
func ValidateDesktopSessionID(id string) error         // :85  前缀 local_ + 余部非空且只含 [A-Za-z0-9-]
```

### 3.2 HTTP DTO（`internal/proto/desktop_coordinator.go`，新增，Ticket 0 已落）

```go
type DesktopLaunchMaterial struct {          // :52
    Card   string `json:"card"`
    Folder string `json:"folder"`           // 本 agentd 登记的项目绝对根目录
    Brief  string `json:"brief"`            // 以 DesktopWakeMarker 开头
}
type DesktopLauncherOpenResp struct {        // :60
    Launcher  string `json:"launcher"`       // 拉起器桌面端会话 id
    Machine   string `json:"machine"`        // 拉起器机器；空串 = 本机
    HandledBy string `json:"handled_by"`
}
// 以下两型服务 POST /api/desktop/deliver（P3=A 协调者已拍板）
type DesktopDeliverReq  struct { CLISession, DesktopSession, Letter string }  // json: cli_session / desktop_session / letter
type DesktopDeliverResp struct { HandledBy string; Written, Opened bool }     // json: handled_by / written / opened
```

TS 镜像（`web/src/api/scheduling.ts:267`、`:274`，Ticket 0 已落）逐字段同名（snake_case）。

### 3.3 桌面端席位记录（`internal/ledger/seat_desktop.go`，新增，Ticket 0 已落空壳）

```go
type SeatDesktop struct { Session, Machine, Via string; BoundAt time.Time }       // :22
func (s *Store) SetSeatDesktop(id, expect string, desk SeatDesktop) error         // :32
func (s *Store) SeatDesktopOf(id string) (SeatDesktop, bool, error)               // :39
```

账本事件（`internal/ledger/types.go:88`，Ticket 0 已落）：

```go
EvSeatDesktopSet = "seat_desktop_set" // 载荷 {identity, desktop_session, machine, via}；actor = 席位身份；结构事件
```

DDL（两方言同步加进 `ddlStatements`，实现节点落）：

```sql
CREATE TABLE IF NOT EXISTS seat_desktops (
    card_id TEXT PRIMARY KEY,
    identity TEXT NOT NULL,          -- 写入时的席位身份（一致性见证，非席位真源）
    desktop_session TEXT NOT NULL,
    machine TEXT NOT NULL,
    via TEXT NOT NULL,
    bound_at <TIMESTAMPTZ|TEXT> NOT NULL);
```

生命周期联动（实现节点落，冻结为条目）：`RebindSeat` 与 `clearSeatTx` 在各自事务内删除该卡 `seat_desktops` 行；`BindSeat` 不写也不删它（空座时本不应有行）。

### 3.4 拉起器登记（`internal/ledger/seat_desktop.go`，新增，Ticket 0 已落空壳）

```go
type DesktopLauncher struct {                                                     // :46
    Project, DesktopSession, CLISession, Machine string
    RegisteredAt time.Time
    Actor string
}
func (s *Store) RegisterDesktopLauncher(l DesktopLauncher) error                  // :56  覆盖式
func (s *Store) DesktopLauncherOf(project string) (DesktopLauncher, bool, error)  // :62
func (s *Store) UnregisterDesktopLauncher(project string) error                   // :68  幂等
func (s *Store) ListDesktopLaunchers() ([]DesktopLauncher, error)                 // :74  按 project 升序
```

```sql
CREATE TABLE IF NOT EXISTS desktop_launchers (
    project TEXT PRIMARY KEY,         -- cards.project 同值域
    desktop_session TEXT NOT NULL,
    cli_session TEXT NOT NULL,
    machine TEXT NOT NULL,
    actor TEXT NOT NULL,
    registered_at <TIMESTAMPTZ|TEXT> NOT NULL);
```

拉起器登记不落卡事件（无卡归属）；登记来源可由 `actor` 追溯。

### 3.5 拉起材料与简报（`internal/agentd/desktopwake.go`，新增，Ticket 0 已落空壳）

```go
var errNoLocalProject = errors.New("项目在本机没有登记位置")                         // :31
func (s *Server) desktopLaunchMaterial(card, via string) (proto.DesktopLaunchMaterial, error)  // :57
type desktopBriefInput struct { Card, Title, BaseBranch, Session, Folder, Via string }         // :43
func desktopBrief(in desktopBriefInput) string                                                 // :64
```

- `Folder` 的唯一来源 = `resolveCoordWorkdir` 同款解析（`s.mgr.ListProjectLocations()` + `workspace.ResolveProject("", card.Project, …)`）；解析失败 → `fmt.Errorf("%w：%s（handoff project add 登记后可用）", errNoLocalProject, card.Project)`。`ListProjectLocations` 必须经 `s.mgr`（`TestB23328GatewayNoDirectStoreTaskProjectCalls` 既有执法面）。
- `Title` = `GetCard(card).Title`；`BaseBranch` = `EffectiveBaseBranch(card)`（空 → 「未设」）；`Session` = `SessionOfCard(card)`（空 → 「未入群」）。

**简报模板**（实现节点照此渲染；`<…>` 为占位）：

```text
[handoff] 你是卡 <卡号> 的协调者，坐在这场 Claude 桌面端会话里。

- 卡号：<卡号>
- 标题：<标题>
- 有效基线分支：<基线|未设>
- 所在会话：<session:n|未入群>
- 项目根目录：<Folder>

请按三步开始：
1. 读 handoff skill。
2. 用桌面端会话管理工具读到你自己的桌面端会话 id（local_ 开头），然后执行：
   handoff card bind <卡号> --desktop-session <你的桌面端会话 id> --via <page|launcher>
3. 之后有事 handoff 会叫醒你，不必挂订阅；推卡按 charter 节点逐节点点火，升级按三档，需要人就 `@user:<群主>`。
```

`<群主>` = 所在会话 `Owner`；未入群时第 3 步末句改为「需要人就在卡上打 needs_human」。

### 3.6 唤醒信箱 wire（新增跨进程契约；P1=A 协调者已拍板）

| 项 | 冻结值 |
| --- | --- |
| 信箱目录 | `$HOME/` + `proto.DesktopWakeDirRel`（写信的 agentd 进程用户的 HOME，即桌面端所在机器、同一 OS 用户）；不存在时以 0o700 创建 |
| 文件名 | `<CLI 会话 id>.<19 位零填充 UnixNano>.md`（同目录平铺，以 CLI 会话 id 为键前缀）；正则 `^<CLI 会话 id>\.[0-9]{19}\.md$` |
| 临时文件 | `.<最终文件名>.tmp`（点开头），写完 `rename` 成最终名 |
| 写入者 | 桌面端所在机器的 agentd（`writeDesktopLetter`） |
| 读取/删除者 | mod：列目录取 `<CLI 会话 id>.*.md`（排除点开头与 `.tmp`），按文件名升序逐封「读全文 → 删除 → `$.prompt.submit`」（先删后提交；重复投递以账本为准） |
| 正文首行前缀 | `proto.DesktopWakeMarker + " "` |
| 不落账 | 写信不落账本事件、不推进 `session_delivery_cursors`、不经 keystone |

spec 勘误：`b434.md` 「唤醒信箱 wire」原文「文件名 = CLI 会话 id」已于本修复轮改为「文件名以 CLI 会话 id 为键（前缀）」，头部修订轨迹记 r2.1 勘误（协调者授权）。

```go
func writeDesktopLetter(home, cliSession, letter string, now time.Time) (string, error)  // desktopwake.go:82
func (s *Server) desktopLetter(card, seat string, evs []keystone.WakeEvent, raws []proto.LedgerEvent) (string, error)  // desktopwake.go:74
```

**来信内容与 `session wait` / `card wait` 同源**（spec `b434.md:65`「命中条 + 引用条 + 未读数 / 工单原文」）。`desktopLetter` 逐条遍历 `raws`（不是只读 `WakeEvent.Summary`——`Summary` 在 `wakeconsumer.go:386` 与 `:236` 被截到 400 字），按事件类型渲染：

| raws 事件 | 段落内容 | 数据来源（与 CLI 投影同源） |
| --- | --- | --- |
| `room_message`（会话房间） | 命中条：seq、会话 id、发言者 actor、**正文全文** | 解码 `proto.RoomMessage`；同 `cmd/session.go#buildSessionWake`（`:534`）的 `Hit` |
| 同上，`ReplyTo>0` 且可解析 | 引用条：seq、actor、**正文全文** | `s.rooms.MessageReference(msg)`（`internal/collab/sessions.go:295`），同 `buildSessionWake` 的 `Referenced` |
| 同上 | 未读数：该席位身份在该会话的未读条数（含命中条） | `s.rooms.ListSessions(seat)` 中 `ID == msg.Room` 的 `Unread`（`internal/collab/sessions.go:33`），同 `buildSessionWake` 的 `Unread` |
| `task_mirrored`（`permission_request`/`question` 等工单） | 工单：seq、task_type、**载荷原文全文** | `mirroredTaskTypeAndPayload(ev)`（`wakeconsumer.go:173`） |
| 其它（`decision_*`、task 终态） | seq、事件类型、载荷原文 | `ev.Payload` |

读引用或未读投影失败 → `desktopLetter` 返回错误，本批不写信（宁可不发信，不发残信），走 §3.7 的「信未落盘」处置。

**来信模板**：

```text
[handoff] 卡 <卡号> 有 <n> 件新事（账本 seq <min>–<max>）

## 会话消息 seq <seq>（<会话 id>，<actor>）
<命中条正文全文>

> 引用 seq <引用 seq>（<引用 actor>）：
> <引用正文全文，逐行加 "> ">

会话 <会话 id> 里你还有 <unread> 条未读（含本条）。

## 工单 seq <seq>（<task_type>）
<载荷原文全文>

## <事件类型> seq <seq>
<载荷原文>

读全文：handoff card show <卡号>；会话消息：handoff session read <会话 id>。处置完不必挂订阅，有事会再叫醒你。
```

拉起器来信 = `DesktopWakeMarker + " 请替我开一场协调者会话。\n\n项目根目录：<Folder>\n\n用桌面端会话管理工具在上述目录新建会话，把下面整段简报作为首条消息发出：\n\n" + Brief(via=launcher)`。

### 3.7 continue 深链、投递与失败路径（`internal/agentd/desktopwake.go`）

```go
type desktopOpener func(url string) error                                                       // :54
func claudeContinueURL(desktopSession string) (string, error)                                   // :89
//   = proto.ClaudeContinueURLPrefix + url.QueryEscape(desktopSession)；先 ValidateDesktopSessionID
func (s *Server) deliverDesktopLetter(ctx context.Context, machine string, req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error)  // :98
func (s *Server) handleDesktopDeliver(w http.ResponseWriter, r *http.Request)                   // :106
type desktopUndeliveredError struct{ machine string; err error }   // :115  信未落盘
func desktopUndelivered(err error) bool                             // :127
type desktopUnopenedError struct{ letterPath string; err error }    // :136  信已落盘、opener 失败
func desktopUnopened(err error) bool                                // :148
func withForwardedWake(ctx context.Context) context.Context         // :158
func isForwardedWake(ctx context.Context) bool                      // :163
func (s *Server) wakeDesktopSeat(ctx context.Context, card, seat string, desk ledger.SeatDesktop,
    evs []keystone.WakeEvent, raws []proto.LedgerEvent, roundID string) (keystone.RoundResult, error)   // :174
const desktopWakeFailReasonPrefix = "桌面端协调者唤醒失败："                                      // :40
```

转交回包增一键（`internal/proto/ledger.go#CoordinatorWakeResp`，r2 Ticket 0 已落，金样本 `desktop_coordinator_test.go#TestB434CoordinatorWakeRespLetterUnopenedWire`）：

```go
type CoordinatorWakeResp struct {
    CoordinatorLaunchResp
    HandledBy      string `json:"handled_by,omitempty"`
    LetterUnopened bool   `json:"letter_unopened,omitempty"` // B434：被转交端信已落盘、opener 失败
}
```

- 本机判据：`scheduling.IsLocalMachine(desk.Machine) || s.IsSelfTarget(desk.Machine)`（与 `scheddrain.go:474` 同式）。
- **本机**：`desktopLetter` → `writeDesktopLetter` → `claudeContinueURL` → opener。
- **异机、本次不是转交来的**：`transferCoordinatorWake(ctx, card, seat, desk.Machine, raws, s.wakeClaimHolder(), roundID)`（沿 B389 转交端点，拍板 D3）；对端 `handleCoordWake` 以 `withForwardedWake(r.Context())` 调 `wakeCoordinatorRoundRaw`，在其本机走同一分支。`transferCoordinatorWake` 收到 2xx 且 `LetterUnopened=true` 时返回 `*desktopUnopenedError`（该键只会在桌面端席位上为 true）；其余失败照旧返回错误，由 `wakeDesktopSeat` 包成 `*desktopUndeliveredError`。
- **异机、本次已是转交来的**（`isForwardedWake(ctx)`）：不再二次转交（一跳封顶），返回 `*desktopUndeliveredError`（原因「席位机器 <machine> 对被转交端也非本机」）；被转交端**不**落 needs_human。
- opener 生产实现：darwin `exec.Command("open", url)`；其它平台返回错误「本平台不支持 Claude 桌面端深链」。opener 注入点为 `Server` 字段（实现节点定名），测试注入 spy。

**单一原则：`wakeDesktopSeat` 从不自己落 needs_human，只返回类型化错误；needs_human 只由认领本批的发起端消费循环写。** 被转交端（`handleCoordWake`）按错误类型回包，不写任何等人标：

| `wakeDesktopSeat` 返回 | 被转交端 `handleCoordWake` 回包 | 发起端消费循环处置 |
| --- | --- | --- |
| `nil`（信落盘 + opener 成功） | 200，`Woke=true` | 照常 complete + 标 seen；清零桌面端失败段 |
| `*desktopUnopenedError`（信已落盘，opener 失败） | **200**，`Woke=false`，`letter_unopened=true` | complete + 标 seen（信已保住事件，重试会产第二封同批信）；按桌面端失败段落 needs_human |
| `*desktopUndeliveredError`（信未落盘：写信/渲染失败、一跳封顶拒转） | 502 `{"error": <原因>}`（`coordapi.go:209-212` 既有形态） | **不**标 seen、**不** complete，认领留在飞挡水位，下轮重试；按桌面端失败段落 needs_human |
| 发起端自己的转交失败（取客户端失败、对端不可达、对端非 2xx） | — | 同上一行 |

**「保住事件」取不标 seen**、不取「标 seen 靠账本重投」，理由：消费循环对已标 seen 的事件没有任何重投机制（`wakeconsumer.go:837-853` 非准入错误终局处置即标 seen + 推水位），标了就永久丢唤醒；「认领留在飞、不标 seen」是 B390 P2 准入满员已在用的可恢复排队态，重试载体（B389 认领挡水位）与去重（`automationSeen`）都现成。代价：失败段内自动化游标终局前缀水位停在该批最小 seq（他卡事件照常处理，靠 `automationSeen` 去重）。

**桌面端失败段计数（独立，不经准入停摆）**：
- 消费循环对 `desktopUndelivered(err)` 走「不收尾」分支，但**不**调 `recordAdmissionStall`——该函数（`wakeconsumer.go:69-90`）累计到 `automationStallEscalateAfter`（`:52`，=3）时会落一条 reason 为「自动化唤醒持续准入失败…协调者小队名额被占满」的 needs_human（`:82-84`），理由错且会成为第二条。现行 `:823-835` 分支内的 `recordAdmissionStall(card)` 调用只留给 `admissionStalled`。
- 桌面端用独立的进程内按卡计数表（实现节点定名，建议 `Server.desktopWakeStall map[string]int`，受 `automationMu` 保护）：`desktopUndelivered` 或 `desktopUnopened` 时计数 +1，**计数从 0 变 1 的那一次**落 `MarkNeedsHuman(card, desktopWakeFailReasonPrefix+<原因>, "agentd")`；`wakeDesktopSeat` 返回 nil 时清零。段 = 两次成功之间的连续失败。
- `MarkNeedsHuman` 本身不幂等（`internal/ledger/events.go:450-460` 每调一次落一条），去重只靠这张表。表在进程内：**agentd 重启清零，重启后同一失败段可能再多落一条 needs_human**——可接受（重启本身罕见，多一条等人标不丢信息），写明不修。

### 3.8 唤醒路径改接（存量缝；P2=A 协调者已拍板）

| 位置 | 现状 | 改后 |
| --- | --- | --- |
| `wakeconsumer.go#resolveWakeCard`（`:492`）事件卡本身（`:511-513`） | bind → `""` | bind 且 `SeatDesktopOf` 命中 → 返回该卡；bind 无记录 → `""`（不变） |
| `wakeconsumer.go#resolveWakeCard` 祖先（`:514-515`） | bind 祖先跳过、继续上走 | 有桌面端记录的 bind 祖先与 coordinate 祖先同等接手（返回该祖先）；无记录的 bind 祖先仍跳过 |
| `scheddrain.go#wakeCoordinatorRoundID`（`:451-454`） | bind → `keystone.Forget` + 跳过 | bind 且 `SeatDesktopOf` 命中 → `return s.wakeDesktopSeat(…)`；bind 无记录 → 原样跳过；coordinate → 原路径 |
| `coordapi.go#handleCoordWake`（`:172`） | `wakeCoordinatorRoundRaw(r.Context(), …)` | 改传 `withForwardedWake(r.Context())`（一跳封顶标记） |
| `wakeconsumer.go#consumeAutomationEventsOnce`（`:823`） | `admissionStalled` → `recordAdmissionStall` + 不收尾 | `admissionStalled` 分支不变；新增并列分支 `desktopUndelivered(err)` → 桌面端失败段计数 + 不收尾（**不**调 `recordAdmissionStall`）；`desktopUnopened(err)` → 桌面端失败段计数 + 终局收尾（complete + 标 seen）；成功 → 清零桌面端失败段 |
| `scheddrain.go#transferCoordinatorWake`（`:361`） | 回包映射 `Woke/SessionID/Rebuilt/Escalated/Output` | 增：`resp.LetterUnopened` → 返回 `*desktopUnopenedError` |
| `coordapi.go#handleCoordWake` 回包（`:209-212`） | 任何错误 → 502 | `desktopUnopened(err)` → 200 + `LetterUnopened=true`、`Woke=false`；其余错误维持 502 |
| `keystone.go#Service.Wake`（`:123`） | bind → no-op | **不变** |
| 唤醒事件集 | `automationWakeEvents` | **不变**（needs_human 是否叫醒见 R1/P5） |

### 3.9 HTTP 面（实现节点落）

| 路由 | 守卫 | 请求 | 响应 |
| --- | --- | --- | --- |
| `GET /api/cards/{id}/coordinator/desktop-launch` | `withLedger`（**不**套 `withCoordinator`） | — | 200 `DesktopLaunchMaterial`（via=page）；404 卡不存在（`ledgerErr`）；409 `{"error": <errNoLocalProject 包装文本>}` |
| `POST /api/cards/{id}/coordinator/desktop-launch` | `withLedger` | `{}` | 200 `DesktopLauncherOpenResp`；409 `{"error":"无拉起器，请在桌面端开一场并登记"}` 且已 `MarkNeedsHuman(card, 同文, "agentd")`；409 无本机位置；502 投递失败（已落 needs_human，前缀同 §3.7） |
| `POST /api/desktop/deliver`（P3=A 协调者已拍板） | `/api` 既有 token 中间件（`internal/agentd/server.go#Server.auth`，`:976`）；不需要账本 | `DesktopDeliverReq` | 200 `DesktopDeliverResp`；400 字段缺失或桌面端会话 id 非法；只在本机写信 + opener，**无论是否带 `X-Handoff-Forwarded`（`forward.go:35`）都从不出站转发**（一跳封顶）；opener 失败 → 200 `{written:true, opened:false}` |

发起端：`deliverDesktopLetter` 对异机拉起器用 `clientForTarget(machine)` 并 `MarkForwarded()` 发出；转交失败 → `MarkNeedsHuman(card, desktopWakeFailReasonPrefix+…)`，代开端点回 502。

```go
var errNoDesktopLauncher = errors.New(desktopNoLauncherReason)                                   // :34
const desktopNoLauncherReason = "无拉起器，请在桌面端开一场并登记"                                  // :37
func (s *Server) openViaDesktopLauncher(ctx context.Context, card string) (proto.DesktopLauncherOpenResp, error)  // :183
```

### 3.10 CLI 面（实现节点落）

```text
handoff card bind <id> [--cli <c> --session <s>] [--desktop-session <local_…> [--machine <名>] [--via page|launcher|manual]]
handoff card seat desktop set <id> --desktop-session <local_…> [--machine <名>] [--via manual]
handoff launcher register --project <名> --desktop-session <local_…> [--machine <名>]
handoff launcher show [--project <名>]        # stdout JSON
handoff launcher unregister --project <名>
```

- `card bind`：先 `BindSeat`（不变），成功后若带 `--desktop-session` 再 `SetSeatDesktop(id, identity, SeatDesktop{…})`；第二步失败时整条命令报错退出非零，但席位保持已坐下（拍板 D2）。
- `--machine` 缺省 = `os.Hostname()`；`--via` 缺省 = `manual`；`--machine`/`--via` 不带 `--desktop-session` → 报错。
- 带 `--desktop-session` 时席位身份的 cli 段必须是 `claude`（桌面端会话只跑 claude），否则报错。
- `card seat desktop set`：expect = 卡上当前 `driver_session`，挂在既有 `cardSeatCmd`（`cmd/card_bearing.go`）下。
- `card rebind --self`（`cmd/card_driver.go:126`）：**不加** `--desktop-session`；`RebindSeat` 同事务删除该卡 `seat_desktops` 行（条 62），换绑后的新席位处于「无桌面端记录的 bind」态（靠 CLI wait，不被信箱唤醒），需要信箱唤醒时由新会话再跑一次 `card seat desktop set <id> --desktop-session <自己的 id>`。取这一支而非给 rebind 加 flag 的理由：rebind 的 HTTP 面（`handleCoordRebind`）明文不接受 self 身份输入，CLI 侧加 flag 会让 self 换绑与 bind 两条命令各维护一份桌面端写入；而 `card seat desktop set` 已是独立 CAS 写面（拍板 D2），复用它零新增接缝。简报模板不涉及 rebind（新会话一律走 bind）。
- `launcher register`：CLI 会话 id 取 `currentSeatIdentity` 解出的 session 段，cli 段必须是 `claude`。
- `handoff status`：新增「拉起器」段，逐项目列 `project / desktop_session / machine / 可达`（可达 = 本机或目标客户端探活成功）；格式归 plan。

### 3.11 薄壳深链通道（`desktop/internal/shell/external_deeplink.go`，新增，Ticket 0 已落空壳）

```go
const ExternalDeepLinkMessagePrefix = "handoff:open-deeplink:"                         // :21
func HandleExternalDeepLinkMessage(log *slog.Logger, message, sourceFrameURL string,
    open func(string) error) bool                                                       // :33
func validateClaudeDeepLink(raw string) (*url.URL, error)                               // :44
```

`desktop/main.go` RawMessageHandler 在剪贴板、系统浏览器之后第三个尝试本处理器，opener 生产注入与 §3.7 同一取舍（Wails `Browser.OpenURL` 若不处理自定义 scheme 则改 `exec.Command("open", url)`，见欠账 §10.7）。

### 3.12 控制台（`web/`）

```ts
// web/src/app/lib/desktopShell.ts
export const OPEN_DEEPLINK_MESSAGE_PREFIX = 'handoff:open-deeplink:'   // :58（Ticket 0 已落）
export function requestOpenDeepLink(url: string): boolean             // :159（空壳）

// web/src/app/lib/claudeDeepLink.ts（新文件，Ticket 0 已落空壳）
export const CLAUDE_Q_MAX_CHARS = 14336                                // :13
export const CLAUDE_NEW_SESSION_URL_PREFIX = 'claude://code/new?'      // :16
export type ClaudeDeepLinkRejectReason = 'folder_not_absolute' | 'brief_empty' | 'brief_too_long' | 'not_implemented'
export type ClaudeDeepLinkResult = { ok: true; url: string } | { ok: false; reason: ClaudeDeepLinkRejectReason }
export function buildClaudeNewSessionURL(folder: string, brief: string): ClaudeDeepLinkResult  // :28
//   成功值 = CLAUDE_NEW_SESSION_URL_PREFIX + 'folder=' + encodeURIComponent(folder) + '&q=' + encodeURIComponent(brief)

// web/src/api/scheduling.ts（Ticket 0 已落）
export interface DesktopLaunchMaterial { card: string; folder: string; brief: string }          // :267
export interface DesktopLauncherOpenResp { launcher: string; machine: string; handled_by: string } // :274
export const getDesktopLaunchMaterial = (cardId: string) => …                                     // :281
export const openViaDesktopLauncher = (cardId: string) => …                                       // :285
```

`CoordinatorPanel.tsx`：`view === 'unbound'` 时在「▶ 叫机器人」旁并列「用 Claude Code 坐下」；材料 409 → 按钮禁用，`title`/说明文字逐字取服务端 `error`；点击 → `buildClaudeNewSessionURL` → 薄壳内 `requestOpenDeepLink`，浏览器内以 `<a href>` 打开；编码拒绝或无桥 → 展开「复制简报」+「到 Claude 桌面端新建会话后粘贴」；首次提示「如弹出信任文件夹，点信任，再按发送」。拉起器代开入口的文案与位置见 P4。

### 3.13 mod 正式版与安装（实现节点落）

- 源码目录：`plugins/claude-desktop-wake/{.claude-plugin/plugin.json, hooks/hooks.json, hooks/register.ts}`；`plugin.json` `name = "handoff-desktop-wake"`。
- 行为（spec 实现决定，逐条冻结于 §4.7）：`session.start` 后 1.5s 首查、此后每 5s；列信箱取 `<$.session.id()>.*.md`（排除点开头与 `.tmp`），按文件名升序逐封「读 → 删 → `$.prompt.submit({text, asUser:true})`」；预填以 `[handoff]` 开头 → 清空输入框并提交。
- 内嵌：`main.go` 以 `//go:embed plugins/claude-desktop-wake` 声明 `embed.FS`（需 `all:` 前缀以收 `.claude-plugin` 点目录——`internal/webui/embed.go:13` 既有教训），经 `cmd.SetClaudeModFS(fs.FS)` 注入。
- 安装（`internal/skill/claudemod.go`，新文件）：

```go
const ClaudeModRelDir = ".handoff/claude-mods/handoff-desktop-wake"
func InstallClaudeMod(mod fs.FS, home string) Site   // 写全目录；确保 ~/.claude/settings.json env.CLAUDE_CODE_PLUGIN_DIRS 含该目录（合并，不删他项）
func ClaudeModStatus(mod fs.FS, home string) Site    // 逐文件哈希比对 + env 是否含该目录
```

`cmd/skill.go` 的 install/status 两处各追加一行，与 `InstallPlugin`/`PluginStatus` 并列；`~/.claude` 不存在 → skipped + Note，不代建。

---

## 4. 原子冻结清单

每条一支可独立判 pass/fail 的接缝断言。「T0」= Ticket 0 已写对应测试，**但本轮未跑**（本机无工具链，§9.2）；其余为实现节点对账条目。r1 修复轮拆开了复合条目并补齐来信内容与异机失败路径，编号整体重排（r0 编号作废）。r2 修复轮改写条 99–111，并在 §4.6 内追加条 165–176（编号接续全清单末尾，不再重排已对账编号）。

### 4.1 拉起材料（S1）

1. `GET /api/cards/{id}/coordinator/desktop-launch` 对项目无本机位置的卡返回 409。
2. 上条 409 的 `error` 文本含卡的 project 名。
3. 上条 409 路径不落任何账本事件。
4. 该路由对存在且项目有本机位置的卡返回 200 `DesktopLaunchMaterial`。
5. `DesktopLaunchMaterial.Folder` 等于 `resolveCoordWorkdir` 同款解析出的 `loc.Path`。
6. `DesktopLaunchMaterial.Brief` 以 `"[handoff] "` 开头。
7. `Brief` 含卡号。
8. `Brief` 含有效基线分支值（或「未设」）。
9. `Brief` 含所在会话 id（或「未入群」）。
10. `Brief` 含 `--desktop-session`。
11. `Brief` 含 `--via page`。
12. `Brief` 不含子串「一次性回合」。
13. 该路由不经 `withCoordinator`：keystone/scheduling 未装配时仍可返回 200。
14. GET 路由不改任何账本或进程内状态。

### 4.2 深链编码（S1，边界型）

15. `buildClaudeNewSessionURL` 成功值以 `claude://code/new?folder=` 开头。
16. folder 以 `encodeURIComponent` 编码。
17. brief 以 `encodeURIComponent` 编码。
18. 成功值不含 `+`（空格编为 `%20`）。
19. 中文、换行、双引号、`&`、`#` 经编码后 `new URL(url).searchParams.get('q')` 逐字还原为 brief。
20. `brief.length > CLAUDE_Q_MAX_CHARS` → `{ok:false, reason:'brief_too_long'}`，不截断。
21. `brief.length === CLAUDE_Q_MAX_CHARS` → 成功。
22. folder 不以 `/` 开头 → `{ok:false, reason:'folder_not_absolute'}`。
23. brief 为空串 → `{ok:false, reason:'brief_empty'}`。
24. TS `CLAUDE_Q_MAX_CHARS === 14336`（T0：`web/src/app/lib/claudeDeepLink.test.ts`）。
25. TS `CLAUDE_NEW_SESSION_URL_PREFIX === 'claude://code/new?'`（T0：同上）。
26. Go `proto.ClaudeNewQMaxChars == 14336`（T0：`internal/proto/desktop_coordinator_test.go#TestB434DesktopWireLiterals`）。
27. Go `proto.ClaudeNewSessionURLPrefix == "claude://code/new?"`（T0：同上）。

### 4.3 薄壳通道（S1）

28. `ExternalDeepLinkMessagePrefix == "handoff:open-deeplink:"`（T0：`desktop/internal/shell/external_deeplink_test.go#TestExternalDeepLinkMessagePrefixLiteral`）。
29. `OPEN_DEEPLINK_MESSAGE_PREFIX === 'handoff:open-deeplink:'`（T0：`web/src/app/lib/desktopShell.test.ts` 新用例）。
30. 非本前缀消息 → `HandleExternalDeepLinkMessage` 返回 false 且不调 opener。
31. `claude://code/new?folder=…&q=…` → 返回 true 且 opener 恰被调用一次、参数逐字等于原 URL。
32. `claude://code/continue?session=local_x` → 放行（opener 被调一次）。
33. `http://` 目标 → 返回 true 且 opener 零调用。
34. `https://` 目标 → 返回 true 且 opener 零调用。
35. `file://` 目标 → 返回 true 且 opener 零调用。
36. `javascript:` 目标 → 返回 true 且 opener 零调用。
37. scheme 为 `claude` 但 host 不是 `code` → 拒绝，opener 零调用。
38. scheme 为 `claude`、host 为 `code` 但 path 不是 `/new`、`/continue` → 拒绝，opener 零调用。
39. 含控制字符（`< 0x20` 或 `0x7f`）的目标 → 拒绝，opener 零调用。
40. 来源 frame 非 http(s) → 拒绝，opener 零调用。
41. 日志不出现完整目标 URL（q 内含简报）。

### 4.4 席位桌面端身份（S1）

42. 不带 `--desktop-session` 的 `card bind` stdout 与 `e020380a` 逐字相同。
43. 不带 `--desktop-session` 的 `card bind` 写入的席位两列与 `e020380a` 相同。
44. 不带 `--desktop-session` 的 `card bind` 落恰一条 `EvDriverSeatBound`、零条 `EvSeatDesktopSet`。
45. 带 `--desktop-session` 的 `card bind` 成功后 `SeatDesktopOf(id)` 返回 ok=true，`Session` 等于给定值。
46. 上条 `Machine` 在未给 `--machine` 时等于 `os.Hostname()`（CLI 可见缺省值，入契约）。
47. 上条 `Via` 在未给 `--via` 时等于 `manual`。
48. 给 `--via page` 时 `Via` 等于 `page`。
49. `SetSeatDesktop` 成功时同事务落恰一条 `EvSeatDesktopSet`。
50. `EvSeatDesktopSet` 载荷键集恰为 {identity, desktop_session, machine, via}。
51. `SetSeatDesktop` 对 coordinate 席位返回错误，且不写行、不落事件。
52. `SetSeatDesktop` 对空座返回错误，且不写行、不落事件。
53. `SetSeatDesktop` 在 `expect` 与卡上席位不符时返回 `ErrCASConflict`，且不写行。
54. `SetSeatDesktop` 对不满足 `ValidateDesktopSessionID` 的 id 返回错误，且不写行。
55. `ValidateDesktopSessionID("local_31eda11a-a6fe-4a5c-a229-fc12d682d1b6") == nil`。
56. `ValidateDesktopSessionID("")` 返回错误。
57. `ValidateDesktopSessionID("local_")` 返回错误。
58. `ValidateDesktopSessionID("31eda11a-a6fe-4a5c-a229-fc12d682d1b6")`（无前缀）返回错误。
59. `ValidateDesktopSessionID` 对含空白、`#`、`/` 的值返回错误。
60. 带 `--desktop-session` 但席位 cli 段非 `claude` → 命令报错，且不写桌面端记录。
61. `--machine` 或 `--via` 不带 `--desktop-session` → 命令报错，且不坐下。
62. `RebindSeat` 成功后该卡 `SeatDesktopOf` 返回 ok=false。
63. `clearSeatTx`（终态关单/人工清座）后该卡 `SeatDesktopOf` 返回 ok=false。
64. 桌面端 bind 席位的 `driver_source` 仍为 `bind`、身份仍为 `cli:claude#<CLAUDE_CODE_SESSION_ID>`。
65. `seat_bearings` 对桌面端 bind 席位无行（B389 规则 4 不变）。
66. `EvSeatDesktopSet` 不唤醒任何人（`automationWakeEvents` 返回空）。

### 4.5 唤醒信箱（S2）

67. 信件最终文件名匹配 `^<CLI 会话 id>\.[0-9]{19}\.md$`。
68. 同一 CLI 会话连续两次写信产生两个不同文件，均可被读回（不覆盖、不合并）。
69. 写信以同目录 `.<名>.tmp` + `rename` 完成：并发读者永不读到半封信。
70. 信箱目录不存在时 `writeDesktopLetter` 以 0o700 创建它。
71. 来信首行以 `"[handoff] "` 开头。
72. 来信含卡号。
73. 来信含本批每个事件的 seq。
74. 会话消息段含命中条正文**全文**（长于 400 字的正文不被截断）。
75. 会话消息段含命中条发言者 actor。
76. 命中条 `ReplyTo` 可解析时，来信含引用条 seq 与引用正文全文，内容等于 `s.rooms.MessageReference(msg)` 的返回。
77. 命中条无 `ReplyTo` 或不可解析时，来信无引用段。
78. 会话消息段含该席位在该会话的未读数，等于 `s.rooms.ListSessions(seat)` 中该会话的 `Unread`。
79. `task_mirrored` 工单（`permission_request`/`question`）段含载荷原文全文。
80. 读引用或未读投影失败 → 不写信、`desktopLetter` 返回错误。
81. 写信不落任何账本事件。
82. 写信不推进 `session_delivery_cursors`。
83. `claudeContinueURL("local_x")` 等于 `"claude://code/continue?session=local_x"`。
84. `claudeContinueURL` 对非法 id 返回错误，且不调 opener。

### 4.6 唤醒分流与失败路径（S2）

85. `resolveWakeCard` 对有桌面端记录的 bind 事件卡返回该卡号。
86. `resolveWakeCard` 对无桌面端记录的 bind 事件卡返回 `""`。
87. `resolveWakeCard` 上走遇有桌面端记录的 bind 祖先时返回该祖先（P2=A）。
88. `resolveWakeCard` 上走遇无桌面端记录的 bind 祖先时跳过继续上走（不变）。
89. 对有桌面端记录、本机的 bind 席位，一批唤醒恰写一封信。
90. 同上，opener 恰调一次。
91. 对有桌面端记录的 bind 席位，`keystone.Wake` 调用次数为 0。
92. 对有桌面端记录的 bind 席位，`keystone.LaunchForCard` 调用次数为 0。
93. 对无桌面端记录的 bind 席位，零写信、零 opener 调用，日志「bind 席位跳过协调者唤醒」仍在。
94. coordinate 席位唤醒路径不变（`TestB3583*`、B389 既有唤醒测试不改仍绿）。
95. 桌面端记录 `Machine` 非本机且 ctx 无转交标记时，恰调一次 `transferCoordinatorWake`，本机零写信、零 opener 调用。
96. `handleCoordWake` 以带转交标记的 ctx 调 `wakeCoordinatorRoundRaw`。
97. 被转交端对 `Machine` 为其本机的桌面端席位写信并调 opener。
98. 被转交端对 `Machine` 对其也非本机的桌面端席位不再出站转交（零 `CoordinatorWake` 请求，一跳封顶）。
99. 上条情形被转交端回 502（`error` 含原因），且被转交端不落 needs_human。
100. 发起端异机转交失败（取客户端失败、对端不可达、对端非 2xx）计入桌面端失败段；段内首次时发起端落 needs_human，reason 以「桌面端协调者唤醒失败：」开头。
101. 发起端异机转交失败的该批 seq 不标 seen。
102. 发起端异机转交失败的该批不 `completeWakeBatch`（认领留在飞，自动化游标不越过该批）。
103. 同一桌面端失败段内（不论失败类型、不论重试多少轮），同一 agentd 进程对该卡恰落一条 needs_human。
104. `wakeDesktopSeat` 成功一次即清零失败段；其后再失败重新落一条 needs_human。
105. 对端恢复后，失败段内保住的事件在下一轮被转交，并出现在对端写出的信里。
106. 本机写信失败（含 `desktopLetter` 失败）计入桌面端失败段；段内首次落 needs_human，前缀同上。
107. 本机写信失败 → opener 零调用。
108. 本机写信失败的该批不标 seen、不 `completeWakeBatch`（同 101/102 处置）。
109. 本机 opener 失败计入桌面端失败段；段内首次落 needs_human，前缀同上。
110. 本机 opener 失败 → 已写的信文件仍在信箱。
111. 本机 opener 失败的该批 seq 标 seen 且 `completeWakeBatch`，下一轮不重投（不产生第二封同批信）。
165. 被转交端信已落盘而 opener 失败时，`handleCoordWake` 回 HTTP 200。
166. 上条回包 `letter_unopened` 为 true。
167. 上条回包 `woke` 为 false。
168. 上条情形被转交端不落 needs_human。
169. 发起端收到 2xx 且 `letter_unopened=true` → 该批标 seen 且 `completeWakeBatch`（不重试，不产生第二封同批信）。
170. 上条情形计入发起端桌面端失败段（段内首次落 needs_human，前缀同上）。
171. 被转交端在任何桌面端失败下都不调 `MarkNeedsHuman`（needs_human 只由认领本批的发起端写）。
172. `wakeDesktopSeat` 自身零 `MarkNeedsHuman` 调用（只返回类型化错误）。
173. 桌面端失败（`desktopUndelivered`/`desktopUnopened`）不调 `recordAdmissionStall`，不改 `automationStall` 计数。
174. 桌面端失败无论重试多少轮，都不产生 reason 含「准入」的 needs_human。
175. `CoordinatorWakeResp.LetterUnopened=false` 时 JSON 无 `letter_unopened` 键（既有转交 wire 不变；T0：`TestB434CoordinatorWakeRespLetterUnopenedWire`，本轮未跑）。
176. `CoordinatorWakeResp.LetterUnopened=true` 时 JSON 含 `"letter_unopened":true`（T0：同上，本轮未跑）。

### 4.7 mod（S2，`claude plugin test`）

112. 信箱有一封本会话的信 → 恰一次 `prompt.submit`，提交文本等于信全文。
113. 上条提交发生时该信文件已不存在（删在提交之前）。
114. 信箱为空 → 零 `prompt.submit`。
115. 信箱有两封本会话的信 → 恰两次 `prompt.submit`。
116. 上条两次提交按文件名升序。
117. 上条每次提交文本各恰等于一封信全文（不合并）。
118. 其它 CLI 会话 id 前缀的信不被本会话读取。
119. 其它 CLI 会话 id 前缀的信不被本会话删除。
120. 点开头或 `.tmp` 结尾的文件不被读取。
121. 输入框预填以 `[handoff]` 开头 → 输入框被清空。
122. 输入框预填以 `[handoff]` 开头 → 恰一次 `prompt.submit`，文本等于原预填。
123. 输入框预填不以 `[handoff]` 开头 → 零提交。
124. 输入框预填不以 `[handoff]` 开头 → 输入框内容不变。
125. 首查发生在 `session.start` 后 1.5s。
126. 首查后查信间隔 5s。
127. mod 源码中的信箱目录与标记字面量与 `proto.DesktopWakeDirRel`/`proto.DesktopWakeMarker` 逐字一致（CDC：mod 用例回放 Go 侧字面量）。

### 4.8 mod 安装（S2）

128. `handoff skill install` 在 `~/.claude` 存在时把 mod 全部文件写到 `~/.handoff/claude-mods/handoff-desktop-wake/`。
129. 安装后 `~/.claude/settings.json` 的 `env.CLAUDE_CODE_PLUGIN_DIRS` 含该目录。
130. 安装保留 `CLAUDE_CODE_PLUGIN_DIRS` 原有的其它目录。
131. 安装保留 `settings.json` 其它键逐字不变。
132. `~/.claude` 不存在 → 该落点报 skipped。
133. `~/.claude` 不存在 → 不创建 `~/.claude`。
134. `handoff skill` 对 mod 文件全部一致且 env 含目录报 in-sync。
135. 任一 mod 文件内容不一致 → 报 stale。
136. env 不含该目录 → 不报 in-sync。
137. 任一 mod 文件缺失 → 报 missing。
138. 二进制内嵌的 mod 含 `.claude-plugin/plugin.json`（embed 不丢点目录）。

### 4.9 拉起器（S3）

139. `launcher register` 后 `DesktopLauncherOf(project)` 返回登记值。
140. 同项目再次 `register` 覆盖前一条（每项目至多一条）。
141. `launcher register` 在 `currentSeatIdentity` 的 cli 段非 `claude` 时报错且不写。
142. `launcher unregister` 幂等。
143. 拉起器登记不落卡事件。
144. `POST …/desktop-launch` 在无拉起器时返回 409，`error` 等于「无拉起器，请在桌面端开一场并登记」。
145. 上条同时 `MarkNeedsHuman(card, "无拉起器，请在桌面端开一场并登记", "agentd")` 恰一次。
146. 上条不写任何信。
147. 上条不调 opener。
148. 有本机拉起器 → 写一封以 `[handoff]` 开头的信到拉起器 CLI 会话的信箱。
149. 上条信含 `Folder`。
150. 上条信含 via=launcher 的简报全文。
151. 有本机拉起器 → 对拉起器桌面端会话 id 调 opener 恰一次（continue）。
152. 拉起请求不写 room_message（是信，不是 `by_system` 结构行）。
153. 拉起器在异机 → 恰一次 `POST /api/desktop/deliver` 到该机，请求带 `X-Handoff-Forwarded`。
154. `/api/desktop/deliver` 无有效 token → 401（`Server.auth` 既有中间件）。
155. `/api/desktop/deliver` 只在本机写信 + opener，不论是否带 `X-Handoff-Forwarded` 都零出站转发。
156. `/api/desktop/deliver` 信落盘后回 `written:true`。
157. `/api/desktop/deliver` opener 失败时回 200 `{written:true, opened:false}`，信留在信箱。
158. 异机拉起器投递失败 → needs_human（前缀「桌面端协调者唤醒失败：」）且代开端点回 502。
159. `handoff status` 列出每个登记拉起器的项目名。
160. 同上列出桌面端会话 id。
161. 同上列出机器。

### 4.10 依赖与图

162. `internal/agentd` 新增代码对 `keystone.Service` 的调用面不扩大（无新 `keystone.` 选择器调用）。
163. `plugins/claude-desktop-wake` 不 import 主模块任何包。
164. `codegraph check` 在本分支视图下无本卡新增违规（欠账：工具链到位后补跑）。

---

## 5. 依赖方向、组装点与预算

- 无新方向、无预算变化：`d_cli→d_ledger`、`d_gateway→d_ledger`、`d_gateway→d_protocol`、`d_gateway→d_transport` 均走既有 entries（§2.2）。
- 组装点仍是 `main.go`、`internal/agentd/server.go`、`cmd/agentd.go`（`codegraph/target.json#assembly`）。本卡在 `main.go` 新增一条 embed 注入（与 `SetPluginContent` 同形），在 `server.go` 新增 opener 装配，均属既有组装点内部。
- `codegraph/target.json`：**不改**。`best.json`：不变。

---

## 6. 可执行冻结与对侧执法点

| 对侧字面量 | 执法点 A | 执法点 B | 锁法 |
| --- | --- | --- | --- |
| `handoff:open-deeplink:` | `desktop/internal/shell/external_deeplink.go:21` | `web/src/app/lib/desktopShell.ts:58` | 两侧各一支字面量测试（T0 已写、本轮未跑：`external_deeplink_test.go`、`desktopShell.test.ts`） |
| `[handoff]` | `internal/proto/desktop_coordinator.go:17` | `plugins/claude-desktop-wake/hooks/register.ts`（实现节点） | Go 金样本（T0，本轮未跑）+ mod 用例逐字回放（条 127） |
| `.handoff/desktop-wake` | `internal/proto/desktop_coordinator.go:20` | mod 源码 / 安装期替换（实现节点） | 同上 |
| `14336` | `internal/proto/desktop_coordinator.go:36` | `web/src/app/lib/claudeDeepLink.ts:13` | Go 金样本（T0，条 26）+ TS 字面量用例（T0：`claudeDeepLink.test.ts`，条 24）；均本轮未跑 |
| `claude://code/new?` | `internal/proto/desktop_coordinator.go:30` | `web/src/app/lib/claudeDeepLink.ts:16` | Go 金样本（T0，条 27）+ TS 字面量用例（T0，条 25）；均本轮未跑 |
| `letter_unopened` 键 | `internal/proto/ledger.go#CoordinatorWakeResp` | 发起端 `transferCoordinatorWake` 回包映射（实现节点） | Go 金样本 `TestB434CoordinatorWakeRespLetterUnopenedWire`（T0，本轮未跑；条 175/176） |
| DTO 形状 | `internal/proto/desktop_coordinator.go:52/:60` | `web/src/api/scheduling.ts:267/:274` | `internal/proto/contract_fixture_test.go` 加四条样本（含两个 Deliver DTO）+ `web/src/api/contract.test.ts` 读 fixture（实现节点；需 `go test -update` 生成，本轮无工具链） |

无哈希、密钥派生、加密编码命中。

---

## 7. 裁决与退回 spec 候选

### 7.1 协调者已拍板（r1 修复轮，2026-10-16）

- **P1 = A**：信箱文件 `<CLI 会话 id>.<19 位零填充 UnixNano>.md` 同目录平铺；mod 按 `<id>.*.md` 取信、按文件名升序逐封提交（§3.6，条 67–68、115–120）。spec `b434.md`「唤醒信箱 wire」同步勘误为「文件名以 CLI 会话 id 为键（前缀）」，头部修订轨迹记 r2.1。
- **P2 = A**：有桌面端记录的 bind 祖先与 coordinate 祖先同等接手子卡唤醒（spec「不再跳过」覆盖 `wakeconsumer.go:511-516` 两处；§3.8，条 85–88）。
- **P3 = A**：新增 `POST /api/desktop/deliver` 一跳转交，鉴权沿 `/api` token 中间件（§3.9，条 153–158）。

### 7.2 待用户裁决（契约不自选）

- **P4 拉起器代开的入口文案与位置**：spec 用户故事 5 写「叫协调者」，实现决定未给。推荐：空座时第三个按钮「让拉起器代开」，仅在拉起器已登记时显示。阻塞 S3 的控制台部分。
- **P5 needs_human 是否叫醒桌面端协调者**（随 R1）：A（推荐，与 B394/契约 §3.5.1 一致）不叫醒，桌面端席位与 coordinate 席位同一事件集；B 叫醒，但须排除协调者自己打的标（防回声）。

### 7.3 退回 spec 候选

- **R1（开）**：spec 用户故事 4「卡上来了工单或 `needs_human`，我被叫醒」与现行唤醒判据冲突——`automationWakeEvent`（`wakeconsumer.go:216`）对 `needs_human` 恒不唤醒（B394 回声防护、契约 §3.5.1），而 spec「唤醒信箱 wire」又说来信与 `card wait` 投影同源（`card wait` 把 needs_human 当可动作）。本契约按现行判据写（§3.8 末行），处置见 P5。
- **R2（已关，r1）**：「文件名 = CLI 会话 id」与缝 4/缝 6 不可兼得——随 P1=A 与 spec r2.1 勘误关闭。

---

## 8. 拍板记录（三重闸门）

**D1 桌面端身份另立 `seat_desktops`，不扩 `seat_bearings`**——难逆转：表结构 + 两处生命周期联动 + B389 规则 4 的测试面；无上下文会惊讶：后人看到两张「席位附属记录」表会想合并；真取舍：被否方案是给 `seat_bearings` 加 `desktop_session` 列并放宽 `validateSeatBearing` 让 bind 带 Machine——否掉理由是 B389 把「承载行只随 coordinate 席位存在」写成了类型注释（`binding.go:190-192`）与写面执法（`validateSeatBearing` 规则 4、`SetSeatBearing` 只收 coordinate），`SeatBearingOf` 的唯一生产读者 `scheddrain.go:460` 读到行即按载体准入处理（今天靠 `:451` 的 bind 早退才不误入）；合并后这条不变式要在每个承载读者处改成「先判来源」，而 `seat_desktops` 另立不动 B389 任何一行。

**D2 桌面端记录由独立 CAS 写面 `SetSeatDesktop` 写入，`card bind` 两步提交**——难逆转：一旦 `BindSeat` 签名加参，`BindSeat`/`RebindSeat` 全部调用方（CLI 两处、agentd 拉起、测试夹具）跟着改；无上下文会惊讶：spec 说「bind 时自报」，后人会想「为什么不同事务」并改成一体；真取舍：被否方案是 `BindSeat(id, identity, source, bearing, desk)` 同事务——否掉理由：两步之间的中间态（已坐下、无桌面端记录）恰是今天的合法 bind 语义（靠 CLI wait，不被信箱唤醒），不产生非法态；且独立写面同时服务「席位 id 先手填」（spec：S2 不依赖 S1）。**反过来写不会有任何测试变红**——这条是本记录存在的主要理由。

**D3 席位跨机唤醒复用 B389 `CoordinatorWake` 转交，信在桌面端所在机渲染与落盘**——难逆转：认领、CAS、wake_round 留痕都挂在这条路径上；无上下文会惊讶：拉起器另有 `/api/desktop/deliver`（P3=A），后人会想统一成一条；真取舍：被否方案是认领机渲染来信、经 deliver 端点只搬运文本——否掉理由：spec 明文「沿 B389 的转交端点」，且被转交端已有 `handleCoordWake` 的席位 CAS（防旧席位收信），deliver 端点没有席位语义。

**D4 拉起器登记存账本，不存 agentd 本机 store**——难逆转：数据落点 + 两方言 DDL；无上下文会惊讶：项目位置在 agentd 本机 store，拉起器却在账本；真取舍：被否方案是挂在 `ProjectLocation` 旁的本机表——否掉理由：拉起请求可能由任一 agentd（手机连的那台）发起，它必须知道拉起器在哪台机器；本机 store 他机读不到。

**D5 桌面端唤醒失败按「信是否已落盘」不对称处置：未落盘不标 seen（认领留在飞重试），已落盘标 seen**——难逆转：动消费循环的终局/排队分类，牵连 B389 认领水位与 B390 准入排队两套既有语义；无上下文会惊讶：同一个 `wakeDesktopSeat` 的两类失败一个标 seen、一个不标，且与 coordinate 席位「非准入错误一律终局」不同，后人极易「统一成标 seen」；真取舍：被否方案是一律标 seen + 靠账本重投——否掉理由：消费循环对已标 seen 事件没有重投机制（`wakeconsumer.go:837-853`），会违反 spec「信不丢」；反向被否方案是一律不标 seen——opener 失败时信已在盒，重试会产第二封同批信。r2 补全跨机一侧：被转交端「信已落盘、opener 失败」以 HTTP 200 + `letter_unopened=true` 回告（而非 502），否则发起端会把它误判为「信未落盘」而每轮重试、对端每轮多写一封同批信；needs_human 只由发起端按独立失败段计数落一条（不经 `recordAdmissionStall`），被转交端从不落——否掉的「两端各自打标」会让同一失败段在 `MarkNeedsHuman` 不幂等（`events.go:450-460`）下重复落标。

审过不立：简报/来信模板文本（产品文案，有条 6–12、71–79 断言会红）；mod 轮询节拍（spec 已定，条 125–126 锁）；`--machine` 缺省 hostname（CLI 可见缺省值，已作为条 46 冻结、有断言，不需拍板记录）；r0 的「桌面端路由只套 `withLedger`」（r1 删除该拍板：条 13 的断言会因反写变红，不满足「反过来写无测试变红」的立记理由，也不难逆转）。

---

## 9. Ticket 0、可执行冻结与直通竖切

### 9.1 Ticket 0 已落（零行为）

- `internal/proto/desktop_coordinator.go`：§3.1 字面量 + §3.2 DTO + `ValidateDesktopSessionID` 空壳 + `ErrDesktopNotImplemented`。（基线已有的 `internal/proto/desktop.go` 是薄壳状态 DTO，未改动。）
- `internal/proto/desktop_coordinator_test.go`：`TestB434DesktopWireLiterals`（字面量金样本）、`TestB434CoordinatorWakeRespLetterUnopenedWire`（r2：`letter_unopened` omitempty 金样本）。
- `internal/proto/ledger.go`：`CoordinatorWakeResp` 增 `LetterUnopened bool json:"letter_unopened,omitempty"`（r2；omitempty，既有 wire 与 `TestCoordinatorWakeWire` 不受影响）。
- `internal/ledger/types.go`：`EvSeatDesktopSet`。
- `internal/ledger/seat_desktop.go`：`SeatDesktop`、`DesktopLauncher` + 六个 Store 方法空壳（`SetSeatDesktop`、`SeatDesktopOf`、`RegisterDesktopLauncher`、`DesktopLauncherOf`、`UnregisterDesktopLauncher`、`ListDesktopLaunchers`，均返回 `ErrDesktopNotImplemented`）。
- `internal/agentd/desktopwake.go`：§3.5/§3.6/§3.7/§3.9 的函数空壳（含 r1 新增 `handleDesktopDeliver`（未注册）、`desktopUndeliveredError`/`desktopUndelivered`、`withForwardedWake`/`isForwardedWake`；`desktopLetter` 改为 `Server` 方法并返回 error；r2 新增 `desktopUnopenedError`/`desktopUnopened`）、两个错误哨兵、两个 reason 字面量、`desktopOpener` 类型。
- `desktop/internal/shell/external_deeplink.go` + `external_deeplink_test.go`：前缀常量、处理器与校验器空壳、前缀字面量测试。
- `web/src/app/lib/desktopShell.ts`（+前缀、`requestOpenDeepLink` 空壳）、`desktopShell.test.ts`（+前缀用例）、`web/src/app/lib/claudeDeepLink.ts`（新）、`web/src/app/lib/claudeDeepLink.test.ts`（r1 新增：条 24/25 字面量用例）、`web/src/api/scheduling.ts`（+两 DTO、两 API 镜像）。

未落（刻意）：DDL、路由注册、CLI flag 与子命令、消费循环分支改接、`plugins/claude-desktop-wake/` 与其 `go:embed`、`internal/skill/claudemod.go`——在无法编译验证的条件下加 embed 目录或注册路由会把编译错误/行为改变带进冻结提交。全部进 §10。

### 9.2 编译与测试（**本轮未能执行**）

本机无 Go 与 Node 工具链（台账原文：`command not found: go`；`go not found / node not found / npx not found`；`find / -type f -name go -perm +111` 零命中；`web/node_modules` 不存在）。因此：

- `go build ./...`、`go vet ./...`：**未跑**。
- `go test ./internal/proto/ -run TestB434DesktopWireLiterals`、`cd desktop && go test ./internal/shell/ -run TestExternalDeepLinkMessagePrefixLiteral`：**未跑**。
- `cd web && npm run typecheck`（`tsc -b`）、`npm run lint`、`npx vitest run src/app/lib/desktopShell.test.ts src/app/lib/claudeDeepLink.test.ts`：**未跑**。

Ticket 0 为纯空壳，已逐文件人工核对导入均被使用、标识符无冲突（`grep` 读数见台账），但**「编译通过」这一法定证据缺失**，作为第一号欠账交协调者认账（§10.1）。

### 9.3 直通竖切（重档法定步骤）——**未落，欠账**

选档依据：spec 定级两问（跨四子系统 + 新增跨进程 wire），三段故事各自可真机验收、合计远超 70 分钟 → 重档；本轮复核无变化。

应落路径（主缝 = 唤醒信箱，spec 测试缝 4/5；库缝形态，夹具直调，不新造对外面）：
`consumeAutomationEventsOnce`（真 SQLite 账本，一张 bind 席位卡 + 写死的 `seat_desktops` 行 + 一条 `decision_opened`）→ `resolveWakeCard` → `wakeCoordinatorRoundID` → `wakeDesktopSeat` → `desktopLetter` → `writeDesktopLetter`（`t.TempDir()` 作 HOME）→ `claudeContinueURL` → opener spy。断言：信箱恰一个文件、opener 恰一次、`keystone.Wake` 零调用。
测试名：`internal/agentd/desktopwake_slice_test.go#TestB434DesktopWakeVerticalSlice`。归属协调者或 contract 执行者，不进任何并行子卡。

### 9.4 图

- `codegraph/target.json`：不改（§5）——随本提交冻结的是「不改」这一声明。
- 分支视图 diff（应为 `codegraph/diffs/cards-B434-charter-1.json`，扁平化命名）：**未产出**，欠账 §10.3。

---

## 10. 本节点欠账（逐条，协调者显式认账）

1. **Ticket 0 编译证据**：在有工具链的机器上跑 `go build ./...`、`go vet ./...`、`(cd desktop && go build ./... && go vet ./...)`、`cd web && npm ci && npm run typecheck && npm run lint`，贴退出码；红即回本节点修骨架。
2. **金样本本轮未跑**：`TestB434DesktopWireLiterals`、`TestB434CoordinatorWakeRespLetterUnopenedWire`、`TestExternalDeepLinkMessagePrefixLiteral`、`desktopShell.test.ts` 新用例、`claudeDeepLink.test.ts`。
3. **图**：工具链到位后 `go build -o <tmp>/codegraph ./cmd/codegraph`（charter/graph），按 `docs/codegraph-scan-recipe.md` 产出 `codegraph/diffs/cards-B434-charter-1.json`（含 §9.1 全部新符号），跑 `codegraph validate --view cards-B434-charter-1` 与 `check`，并 `codegraph resolve --doc docs/superpowers/specs/b434-contract.md`。
4. **直通竖切**：§9.3 整条。
5. **DTO fixture**：`contract_fixture_test.go` 加 `DesktopLaunchMaterial`、`DesktopLauncherOpenResp`、`DesktopDeliverReq`、`DesktopDeliverResp` 样本并 `-update` 生成 `web/src/api/testdata/*.json`；`contract.test.ts` 加读取用例。
6. **外部事实：`CLAUDE_CODE_PLUGIN_DIRS` 多目录分隔符**——**plan 前必须钉**（本节点负责）：从 `claude-code.d.ts` 或 CLI 文档钉死，台账只读到「绝对路径或 ~ 开头」。阻塞条 129–130。
7. **外部事实：Wails `app.Browser.OpenURL` 对 `claude://` 的处理**——**plan 前必须钉**（本节点负责）：读 Wails 源码；不支持则薄壳 opener 改 `exec.Command("open", url)`（darwin）。阻塞条 31–32。
8. **外部事实：mod 拿 HOME 的方式**——**plan 前必须钉**（本节点负责）：原型硬编码 HOME。查 mod API 是否有 homedir；若无，冻结安装期占位符替换（建议 `__HANDOFF_DESKTOP_WAKE_DIR__` → 绝对目录，`ClaudeModStatus` 比对替换后内容）。阻塞条 127–128、134–135。
9. **S1 实现**：§4.1–§4.4 全部条目（材料端点、简报、编码、薄壳通道与 main.go 接线、`card bind` flag、`card seat desktop set`、`seat_desktops` DDL 与生命周期联动、`CoordinatorPanel` 入口）。
10. **S2 实现**：§4.5–§4.8（信箱写入与来信渲染、continue、三处分流改接 + `handleCoordWake` 转交标记 + 消费循环不收尾分支、opener 装配、mod 正式版、安装）。
11. **S3 实现**：§4.9（拉起器表与 CLI、代开端点、`/api/desktop/deliver`、`handoff status` 段、skill 文本：「占座」补「用 Claude Code 坐下」与拉起器登记，「主 agent 回路」补拉起器职责与 CLAUDE.md 常设授权一行）。控制台入口阻塞于 P4。
12. **R1/P5 处置**：用户裁决后在本契约补签（若 P5=B，§3.8 末行与 §4.6 增条目）。
13. **Wave 0 未验一条**：launchd 托管 agentd 内 `open claude://…` 能否到达 GUI 会话（spec 实现决定末条）——S2 开工前真机验。

---

## 11. 移交 plan 附区

（plan 出稿时吸收，并在本区头标注「已由 plan〈文档〉吸收（日期）」销区。）

1. `ListProjectLocations` 在 gateway 生产文件里必须经 `s.mgr`（`b23328_source_guard_test.go` 执法面含 `coordapi.go`；若新路由 handler 写进 `coordapi.go` 即被它覆盖）。建议直接复用 `resolveCoordWorkdir` 的解析逻辑并把它改成返回 `(string, error)`，避免两份解析。
2. `--machine` 缺省 `os.Hostname()`（条 46）对本机判定成立（`IsLocalMachine`），但异机转交走 `clientForTarget(machine)` 要求它是对端 agentd 配置里的 target 名；hostname 与 target 名不一致时转交失败（会落 needs_human，条 100）。plan 决定：`card bind` 时若 hostname 不是任何已知 target 名则提示 `--machine`，或在 status 段显示告警。
3. 未落盘失败段（条 101–105）的重试节拍沿用消费循环 2s；对端长时间不可达时是否加退避由 plan 定（不得改为标 seen）。
4. 队列合成唤醒（`len(raws)==0`）对桌面端异机席位与 coordinate 一样不可转交——沿用 `scheddrain.go` 既有显式失败分支。
5. `handleCoordStatus` 是否加桌面端字段（为 OOS 的「回到那场会话」按钮铺路）——不在本卡冻结面，S1 顺手做则补 DTO 与 fixture。

---

## 12. 本轮法定核对（收尾自检 5 项）

1. 契约增量文档：本文件；每个签名带现状出处（§1.1）或「新增 + Ticket 0 行号」（§3）。✅
2. 目标图：`target.json` 不改（无新方向，§5）——随本提交冻结；分支视图 diff **缺**（无工具链），欠账 §10.3。⚠️
3. Ticket 0 本轮编译通过：**缺**——本机无 Go/Node，命令未能执行（§9.2），欠账 §10.1。❌
4. 可执行冻结：金样本已写（Go 三支：两支字面量 + `letter_unopened` wire；TS 两支：`desktopShell.test.ts` 新用例、`claudeDeepLink.test.ts`）但**本轮未跑**，欠账 §10.2；DTO fixture 欠账 §10.5。无哈希/密钥派生命中。⚠️
5. 三重闸门：§8 命中五条（D1–D5），另记审过不立四条（含 r1 删除的旧 D5）。✅

**缺项没有静默出口**：第 2、3、4 项的缺口已逐条写进 §10，交棒声明须由协调者显式认账后方可进 breakdown。

> **修订记录（r1 修复轮，2026-10-16）**：独立审阅 fail 三阻塞项处置——B1 来信补齐命中条全文/引用条/未读数/工单原文（§3.6，条 74–80）；B2 补异机转交失败 needs_human 与「不标 seen」保事件、被转交端一跳封顶（§3.7–§3.8，条 95–108，拍板 D5）；B3 删除旧 D5（与条 13 矛盾）。协调者拍板 P1/P2/P3=A 落入正文，R2 关闭，spec r2.1 勘误。非阻塞七条：外部事实三条移入欠账 §10.6–§10.8；计数纠错（六个 Store 方法、测试注释引用条号）；复合条目拆开、编号重排（r0 104 条 → r1 164 条）；`claudeDeepLink.test.ts` 落字面量用例；§10.1 加 `npm run lint`；§3.10 写明 rebind 与桌面端记录；§4 头部如实改为「测试已写、本轮未跑」。
>
> **修订记录（r2 修复轮，2026-10-16）**：第二轮审阅 B2 跨机一侧三处收口——B4 被转交端「信已落盘、opener 失败」回 200 + `CoordinatorWakeResp.LetterUnopened=true`（proto 增 omitempty 键 + 金样本），发起端据此标 seen，原移交「opener 失败的错误返回形态」收回冻结（`desktopUnopenedError`，条 109–111、165–170）；B5 被转交端一律不落 needs_human、一跳封顶拒转只回 502 带原因，needs_human 统一由认领本批的发起端写（条 99、171–172）；B6 桌面端失败段用独立进程内计数表、不经 `recordAdmissionStall`，不产生「准入」理由的等人标，agentd 重启清零可能多落一条且写明可接受（§3.7，条 103、173–174）。小错：`desktopOpener` 行号 :53 → :54。条 99–111 改写，追加条 165–176。
