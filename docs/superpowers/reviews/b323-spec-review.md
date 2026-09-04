# B323 spec 审查

审查对象：`docs/superpowers/specs/b323.md`（状态：待独立审查后批准；头部自称 L2；bug-batch：审查吸收即批准）
对照台账：`docs/superpowers/specs/b323-ledger.md`
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b323-home`，分支 `cards/B323-charter`，基线 `origin/main` @ `bcc424cbd`（v0.4.1）。本审查只读工作树文件，不改 spec/代码、不 commit。
审查者：独立 spec 审查人（charter 流；与作者无会话史，一切以亲手读码为准）
日期：2026-09-04

行号按当前工作树，会漂。`codegraph/best.json` 核过顶层域与容器归属；`k_agentd_coordinatorRunner` / `attachLocator` **不在** `best.json`（只在 `codegraph/baseline.json`），记图覆盖债。linux-01 / 本机 `du`、真机 401 / Session not found 未独立复跑，采信台账为运行时读数、不当作代码事实。

## 1. 总判

**修订吸收后再批。** 符合头部「bug-batch：审查吸收即批准」——吸收的是下面最小补丁，不是原样进 plan。

方向对，一条不变式成立：叫机器人的无头回合和 attach 命令必须用同一份展开后的隔离 HOME，并且这份 HOME 在拉起前是协调者全套。弃选站得住：不镜像 Output 进房间、不改 WakeHome 搬技能树、不改 PTY env API / 新 wire 字段、不把 Resume 失败兜底拆掉。并入 B324–B327 不是把不相干根因硬收成一类（见 §7）。Out of Scope 覆盖了该推迟的镜像房间 / 人坐下 / inbox / charter 小队 / wait 过滤（见 §8）。L2 独立验证成立，不抬 L3（见 §4）。

不能原样批准的原因不是产品走偏，是三件承重的事正文还能读成互不兼容的实现：

1. **协调者「全套」的写入集 / 禁写集没钉。** 零上下文可以 rsync 整棵 HOME，把 opencode session db 一并覆盖，故事 2 会变成「每次唤醒都重建」。
2. **Locate 内存未命中时 HomeDir 从哪来没钉，接缝 2 的测试用「或」可以跳过这条。** 热路径只改 `attachLocator` 能绿，agentd 重启后 GET `/coordinator` 仍无 HOME。
3. **测试 1 的变异打不中 B327 还活着的那半边。** 成功展开在现行 `RunTurn` 已经绿；「展开失败仍原样返回」需要让 `userHomeDir` 失败才会红。

批准前最小补丁见 §9。补的是 spec 正文句子，不是代码。

## 2. Findings

### Critical

无。不定级错、不新增跨进程 wire、不把执行者 WakeHome 改成全套。会让实现走错题的都收在 Important，吸收进正文即可批。

### Important

#### I1. 「全套」写入哪些路径、禁止碰哪些路径未钉；三种读法里有一种会删掉 session db

- **位置**：方案 2 `b323.md:38-40`、实现决定 `b323.md:73`、测试 3 `b323.md:83`、故事 2 `b323.md:55`；活代码 `internal/hostapi/probe.go:124-130,272-310`（WakeHome 只在目标为空时拷表内凭据）、`internal/toolchain/detect.go:85`（opencode 凭据相对路径 `.local/share/opencode/auth.json`）、`internal/agentd/server.go:2522-2532`（`coordinatorRunner.Launch/Resume` 今日零供给，直通 `RunTurn`）
- **事实**：正文要求占用目录也要补缺、覆盖 first-run 残件、不得删 session db。未列出写入白名单。opencode 会话与凭据同在 `$HOME/.local/share/opencode/` 树下。今日 `copyMainCredential` 只写表内那一个文件、且只建该文件父目录。
- **三种读法**：
  1. 只写 `.handoff/config.yaml` + `.config/opencode/AGENTS.md` + `.config/opencode/skills/`（正确）。
  2. 把主 HOME 的 `.config/opencode` 与 `.local/share/opencode` 整树同步进隔离侧——session db 被主环境或空树覆盖，Resume 必然 Session not found，重建指针回来。
  3. `RemoveAll` 隔离 HOME 再全量铺——更直接删会话。
- **为什么承重**：故事 2 的产品名就是二次唤醒 `rebuilt=false`、房间没有新的「载体已更换」。供给若毁掉会话文件，本卡自己把 B324 再制造一遍。
- **建议**：方案 2 写成白名单 + 禁写集（见 §9）。symlink 还是 copy 仍归 plan（假缝原文可留）。

#### I2. Locate 未命中时 HomeDir 的来源未钉；接缝 2 测的是 locator，洞在它的调用方

- **位置**：问题 3 `b323.md:18`、方案 3 `b323.md:41`、接缝 2 `b323.md:65`、测试 2 `b323.md:82`；活代码 `internal/keystone/keystone.go:195-217`（miss 重建 `SessionRef{CLI, SessionID, Workdir}`，**无 HomeDir**）、`internal/agentd/server.go:2581-2588`（`attachLocator` 拼 `cli + " --session " + id`，根本不读 `ref.HomeDir`）、`internal/agentd/coordapi.go:285-308,316-353`（`handleCoordStatus` / `handleCoordAttach` 调 `keystone.Locate`，自己不读载体）、`internal/agentd/scheddrain.go:260-263,332-335`（Launch/Wake 才 `Carrier.HomeDir`，且经过 `LaunchAdmit`）
- **事实**：B325 是两处，不是一处。热路径：内存 ref 有 `HomeDir`（`launchRound` `keystone.go:236-239` 会写），但 locator 丢弃它。冷路径：agentd 重启后 `sessions` 空，`Locate` 只从席位解析 CLI/session id。席位字符串里没有 HOME。`keystone.Service` 没有 scheduling 端口，不能自己读载体。
- **危险读法**：为了在 `handleCoordStatus` 里拿到 `carrier.HomeDir`，抄 `launchCoordinatorRound` 走 `LaunchAdmit`。GET 状态会占一个协调者名额。
- **测试漏洞**：测试 2 写「`ref.HomeDir=~/...` **或** 内存 miss 后补上的 HomeDir」。只测 locator + 已填好的 ref，变异「拼命令丢掉 HomeDir」能红，miss 重建仍然丢 HOME。包内 **零** 支 `Service.Locate` 测试（`internal/keystone/` 无 Locate 用例）；HTTP 侧 `TestCoordAttachTakeoverMutesWake` 只断言 command 含 session id（`coordapi_test.go:384-386`）。
- **建议**：钉三句：① HomeDir 来自编制域已登记载体，读法与 Launch 相同但 **禁止** `LaunchAdmit`；② 填充发生在 `keystone.Service.Locate` 把 ref 交给 locator **之前**（locator 仍只负责把已有 HomeDir 展开进 command）；③ 测试 2 拆两支，热路径与 miss 各一，禁止用「或」。谁去读载体（gateway 传入 vs keystone 新端口）归 plan，但结果与禁令必须写在 spec。

#### I3. 测试 1 的变异打不中「展开失败仍原样返回」；B327 成功路径在现行代码已经绿

- **位置**：问题 4 `b323.md:19`、红色回路第 1 弹 `b323.md:27`、测试 1 `b323.md:81`、实现决定 `b323.md:72`；活代码 `internal/hostapi/driver.go:232-248`、`internal/hostapi/runturn_test.go:243-263` `TestBuildEnvExpandsTildeHomeDir`
- **事实**：`expandTurnHomeDir` 成功时已经展开；失败才原样返回。`TestBuildEnvExpandsTildeHomeDir` 用 `swapUserHomeDir` 锁成功路径（内部锁在 `buildEnv`，不是 `RunTurn` 缝）。`os.UserHomeDir` 在 macOS/launchd 下通常成功，所以「子进程 HOME 为字面 `~/...`」不是现行成功路径的默认行为。仓库下 `~/` 垃圾目录「真机仍在」是历史残骸，spec 自己已把清理放进 OOS。
- **测试 1 按字面落地**：`HomeDir=~/handoff-home-x` + 临时 Workdir → 现行 `RunTurn` **已经绿**（UserHomeDir 成功则 HOME 绝对、不会建 `~` 条目）。变异「展开失败仍原样返回」**不会**让这支红，除非夹具让 `userHomeDir` 失败。
- **为什么承重**：plan 会把 B327 当成「已有测试、补一下 RunTurn 缝」而留下 fail-open。fail-open 正是字面 `~` 能再次进子进程的唯一活口。
- **建议**：测试 1 改成两支，或把失败夹具写进同一支：① UserHomeDir 成功 → HOME 绝对、Workdir 下无 `~` 条目（把内部锁升到 `RunTurn` 缝，正当）；② `userHomeDir` 返回 error → `RunTurn` **报错**，Workdir 下仍无 `~` 条目，子进程不得带着字面 `HOME=~...` 被拉起。变异「失败原样返回」必须打红第 ② 支。问题陈述把「成功路径仍在写字面 ~」改成「失败仍 fail-open；成功路径已展开，但 SessionRef / 日志 / attach 仍持未展开登记串」。

#### I4. 「本机 agentd 正在用的 config.yaml」有两种文件；token 抄错文件则 401 原样

- **位置**：方案 2 第一点 `b323.md:39`、红色回路第 3 弹 `b323.md:29`、测试 3 `b323.md:83`；活代码 `internal/config/config.go:349-361`（文件不存在 first-run 新 token 写盘）、`623-634`（`DefaultPath` = `UserHomeDir()/.handoff/config.yaml`）、`internal/agentd/server.go:367`（`s.conf()` 是进程活快照，启动时可 `--config` 不是 DefaultPath）
- **事实**：隔离 HOME 里的 `handoff` 认 `$HOME/.handoff/config.yaml`，这条对。agentd 鉴权认的是 **进程内** `s.conf().Token`，不一定等于 DefaultPath 那份文件。
- **两种读法**：(a) 读 DefaultPath 文件抄过去；(b) 把活进程的 token / 绝对 DataDir / 绝对 DSN 写进隔离侧。测试 3「注入的本机配置 token」在夹具里两种都能绿，生产 `--config` 或 DefaultPath 与活快照分叉时只有 (b) 能消 401。
- **建议**：写死隔离侧 token **等于本进程 agentd 正在用的 token**（`s.conf().Token` 或测试注入的同一份）。DataDir / ledger DSN 若原串是相对路径，必须改写成 agentd 实际在用的绝对路径后再落隔离文件，禁止把相对路径抄进另一份 HOME。不必在 spec 写 `s.conf` 这个符号。

#### I5. 协调者 Launch 全套是否包含 CLI 凭据，与 WakeHome「空目录才拷」并置后，空 HOME 直接 coordinate 可以没有 auth.json

- **位置**：问题陈述「协调者全套（账本凭据 + 该 CLI 的规则/skill）」`b323.md:12`、方案 2 `b323.md:38-40`、弃选「检测时搬技能树」`b323.md:46`；活代码 `WakeHome` 只在 `targetEmpty && main_home_sync` 时 `copyMainCredential`（`probe.go:124-130`）。`card coordinate` / `handleCoordLaunch` **不**经过 WakeHome。
- **事实**：正文「账本凭据」读成 handoff `config.yaml` 的 token，不是 opencode `auth.json`。空隔离 HOME 上直接 `card coordinate`：Launch 会写 config + 规则/skill，**不会**走 `copyMainCredential`。占用（例如先跑过一次 first-run，目录已非空）时 WakeHome 也永远不补凭据。
- **为什么承重**：muse 真机 241s 有 output，说明那次 opencode 跑起来了（可能检测曾经供给过）。新载体或 first-run 残件占住的目录，本卡落地后仍可能「token 对了、模型没登录」。这不一定要本卡做凭据供给，但必须二选一写死，不能靠读者猜「全套」包不含 auth.json。
- **建议**：二选一：① 叫机器人路径对 **缺失的** 表内 CLI 凭据做与 WakeHome 同款的单文件拷贝（已存在不覆盖，禁止碰 session db）；② 明文 OOS「CLI 登录凭据仍只由检测按钮 / WakeHome 供给」，故事与真机门改成「已检测 logged_in 的载体」。不要让「全套」和 WakeHome 空目录门各说各话。

#### I6. 「展开收口在 hostapi」与「attach 命令携带展开结果」是两处执法点，实现决定写成了一处

- **位置**：方案 1 `b323.md:37`、方案 3 `b323.md:41`、实现决定 `b323.md:72`；活代码 attach 不经 `RunTurn` / `buildEnv`（`server.go:2581-2588`），PTY 只是把 `init_command` 打进 shell（`internal/ptyhost/types.go:47-52`，web `useWorkbench.ts:119-127` 原样透传）
- **事实**：hostapi 收口盖住无头回合，盖不住 attach。调度域已有未展开前缀先例：`scheduling.RunCommand` 冻成 `HOME=<home_dir> <cli>`，且 **home_dir 用载体已存字符串（可含 ~）**（`internal/scheduling/status.go:63-67`；契约夹具 `CarrierRunCommandResp` 值为 `HOME=~/.handoff/home/mbp-codex codex`）。抄这条到 attach 会让 command 带字面 `~`。bash 对未加引号的赋值会做 tilde 展开，所以「碰巧能用」；这不是本卡要的绝对路径，也不是 Windows/引号路径可移植形态。
- **建议**：实现决定改成：无头回合在进子进程 env 前展开失败即失败；attach 的 command 在 locator 拼串前把 HomeDir 展开成绝对路径，失败即失败。禁止照抄 `RunCommand` 的未展开 `HOME=~/...`。登记面继续允许 `~/.handoff/home/<名>`。

### Minor

#### M1. 问题陈述把 B293 锁写成 `TestWakeHome` / `.config/skills`，活测试名和路径都不是 opencode 真树

`probe_test.go:136-161` 函数名是 `TestWakeHomeSuppliesMainCredentialBeforeTurn`，断言的是隔离侧不得出现 `.config/skills`。opencode 规则树在 `.config/opencode/skills`。现行断言 **挡不住** 把技能写到真路径。测试决定 3 已经写对 `.config/opencode/skills`——以测试决定为准，问题陈述改掉，避免 plan 去保一条假路径。

#### M2. 冷 Resume 日志里的 `home_dir=~/...` 不是子进程 HOME 的证据

`driveTurn` 打的是 `req.HomeDir`（`driver.go:119-120`），不是 `expandTurnHomeDir` 的返回值。`Wake` 经 `overlayResumeRef`（`keystone.go:130,251-264`）确实会把载体登记串盖进 ref，所以日志有 `home_dir` 与「Resume 没带 HOME」是两件事。台账「冷 Resume 日志已有 home_dir 仍 Session not found → 会话不在该 HOME 的 db」作为 **会话落点** 假说成立；把它写成「子进程当时 HOME 就是字面 ~」过满。方案 1「日志里的 home_dir 同样是展开后的」可留，当作新行为，不要当成现状读数。

#### M3. attach command 的引号与 Windows 未写

`HOME=<绝对路径> <cli> --session <id>` 在路径含空格时必须是合法 shell 赋值。PTY `init_command` 按原文+换行打进 shell（`ptyhost/engine`）。合 main 门是本机 macOS，实现机 linux-01，本卡可以不修 Windows `HOME=` 前缀；建议 OOS 补一句「Windows attach 命令形态本期不改」，避免 linux-01 上按 POSIX 写、有人在 plan 里顺手改 PTY env 顶到 L3 门槛。

#### M4. 契约夹具里的 command 仍是无 HOME 的例子，不构成新 wire，也不锁旧形态

`internal/proto/contract_fixture_test.go:176-180` 与 `web/src/api/testdata/CoordinatorStatus.json:7` 为 `opencode --session sess-coord`。字段集合不变，改的是服务端生成的字符串内容。Web `CoordinatorPanel.tsx:1-3` / `Shell.tsx:123-126` 原样透传，不解析 argv。夹具更新归 plan，不算 L3。不要为了对齐夹具去加新 JSON 字段。

#### M5. 图覆盖债

`k_hostapi_Host` → `d_execution_host`（parent `d_execution`），`k_keystone_Service` → `d_keystone`，在 `best.json` 对得上。`k_agentd_coordinatorRunner` 只在 `baseline.json`，`attachLocator` 两边容器表都没有。本卡接缝 2/3 正好落在未入最优图的组装点类型上。不挡批准；plan/implement 勿用 `chain` 冒充这两处的 flow。

## 3. 现状读数逐条对码

| spec 引用 | 实际 | 结论 |
|---|---|---|
| `copyMainCredential` 只拷表内相对路径，空目录才拷；注释「不搬运技能/规则树」 | `probe.go:124-130` 仅 `targetEmpty && main_home_sync`；`272-274` 注释原文；opencode 表项 `toolchain/detect.go:85` = `.local/share/opencode/auth.json` | 成立 |
| `TestWakeHome` 锁隔离侧不得出现 `.config/skills` | 函数实为 `TestWakeHomeSuppliesMainCredentialBeforeTurn` `probe_test.go:159-161`，路径 `.config/skills` | **名称/路径不精确**，见 M1。B293「检测不搬技能」方向成立 |
| `attachLocator.Locate` 拼 `cli + " --session " + id`，不含 HOME | `server.go:2587` `TrimSpace(ref.CLI + " --session " + ref.SessionID)`，不读 `ref.HomeDir` | 成立 |
| `keystone.Locate` miss 重建 ref 无 HomeDir | `keystone.go:211` `SessionRef{CLI, SessionID, Workdir}` | 成立 |
| `expandTurnHomeDir` 失败原样返回 | `driver.go:243-248` | 成立。**成功则展开**；问题陈述第 4 条把失败口说成默认子进程 HOME，过满，见 I3 |
| 冷 Resume 日志已有 `home_dir=~/...` | `overlayResumeRef` `keystone.go:251-264`；日志字段是 `req.HomeDir` `driver.go:119-120` | 日志有登记串 **成立**；不能由此推出 env HOME 未展开，见 M2 |
| `Wake` 会 overlay spec.HomeDir | `keystone.go:128-130` 先席位后 overlay，miss/hit 都走 | 成立。故 B324 不是「Resume 没带 HomeDir 字段」 |
| `config.DefaultPath` 走 `os.UserHomeDir()` | `config.go:623-634` `defaultDataDir` → `UserHomeDir` 成功则 `Join(h, ".handoff")` | 成立。认 `$HOME`（及 UserHomeDir 的其余回退），隔离进程会读隔离侧 `.handoff/config.yaml` |
| first-run 出第二份 token | `config.Load` 文件不存在则 `randToken` 写盘 `config.go:359-361` | 成立。这是 401 的代码机制，真机 401 未本审查复跑 |
| `SessionSpec.Env` / `SessionRef.HomeDir` / `AttachInfo.Command` 已冻结 | `keysclient.go:15-21,26-33,56-60`；HTTP 投影 `proto.CoordinatorAttachInfo` `internal/proto/scheduling.go:113-118`；夹具已有 command 字符串 | 成立。改 command **内容** 不是新字段 |
| 客户端不得拼接 command | 注释 `scheduling.go:114`；web `CoordinatorPanel.tsx:1-3`、`openTerminalWithCommand` `useWorkbench.ts:49-50,119-127` 原样进 `initCommand` | 成立 |
| PTY `init_command` 已是打进 shell 的 | `ptyhost/types.go:47-52`；engine 原文+换行 | 成立 |
| 重建指针只在 `rebuild==true` 时落 | `launchRound` `keystone.go:243-247` | 成立。本卡不改这条是对的 |
| `coordinatorRunner` 调用方是 keystone | 组装 `server.go:2423-2424`；`Launch/Resume` `2522-2532` 直通 `RunTurn`，无供给 | 成立。供给是新建行为 |
| `RunTurn` 调用方含 WakeHome | `probe.go:322-325` `runWake` → `detectTurn` 默认 `RunTurn`；且 WakeHome 先把 `state.path` 展开后再传入 | 成立。故 fail-closed 会作用于检测回合——检测路径的 HomeDir 此时已是绝对路径，`filepath.Clean` 幂等 |
| 载体登记 `~/.handoff/home/<名>` | `scheduling.IsolatedHomeRoot` `status.go:31-33`；`DefaultHomeDir` `55-60` | 成立 |
| 合 main 真机读数（167M、401、Session not found） | 台账 | **未独立核**。代码机制自洽，不当作本审查的磁盘事实 |

`RoundResult.Output` 今日经 `CoordinatorLaunchResp.Output` 回 HTTP，抽屉可展示（`CoordinatorPanel.tsx:63` `setLaunchOutput(result.output)`），**不**经 `roomNarrator` 进房间。房间只有重建时的 Pointer。不镜像 Output 的弃选与现状一致：修的是协调者自己 `room send` 的前置条件，不是 agentd 代写。

## 4. 定级独立验证

独立结论：**L2 成立。** 不是 L1。不要抬 L3。

定级两问套到定稿范围：

| 问 | 本卡 | 答 |
|---|---|---|
| 跨几个子系统契约面？ | 行为落在既有门面：`hostapi.Host.RunTurn`（`d_execution_host` ⊂ `d_execution`）、`keysclient.TerminalLocator` / `coordinatorRunner`（组装点，keystone 消费）、HTTP 仍是 `GET /coordinator` 与 `POST /attach`，`CoordinatorAttachInfo.Command` 字段已在。Web 继续把 command 当不透明字符串。 | 多领域、同一条用户旅程；**没有新的跨子系统契约面** |
| 动不动契约层？ | 不新增 HTTP 路径/字段；不改 `SessionSpec.Env` 形状；不改 PTY env API。Command 从 `opencode --session id` 变成 `HOME=/abs opencode --session id` 是既有「服务端生成的 shell 命令」语义内的内容变化。 | 不动契约层 |

L1 不成立：plan 写出来不会只复述三行（展开失败、供给白名单、Locate miss 补 HOME、WakeHome 负例）；验收含本机真机 attach/resume。

抬 L3 的唯一合法出口 spec 自己已经写了：改终端 tab 的 env API，或给 `AttachInfo` / HTTP 加新字段。审查同意这条门槛。I2 若有人把 HomeDir 持久化进席位 JSON 或新 HTTP 字段，那是实现越界，应停下来问，而不是本卡预先抬级。

`d_keystone` 与 `d_execution` 都是顶层域，但中间是进程内组装点（`SetupAutomation`），不是新 wire。先例 B286：不新增 HTTP → L2。本卡同类。

## 5. 接缝：假缝 / 漏调用方

接缝合法性三款逐条核：

1. **`hostapi.Host.RunTurn`**（调用方 `coordinatorRunner.Launch/Resume`、`WakeHome`/`runWake`）
   - 存量符号，调用方 grep 得到。不是假缝。
   - 注意：WakeHome 传入的 HomeDir 已是 `inspectHome` 展开后的绝对路径；本缝的 `~/` 用例真正打中的是 **coordinatorRunner 直送登记串** 那条。测试必须经 `RunTurn` 公开面，不要只锁 `buildEnv`（现行 `TestBuildEnvExpandsTildeHomeDir` 是内部锁，升缝是对的）。

2. **`keysclient.TerminalLocator.Locate`**（调用方 `keystone.Service.Locate` ← `handleCoordStatus` / `handleCoordAttach`）
   - locator 本身是真缝，生产调用方就是这两处 HTTP。
   - **漏了一层**：B325 的 miss 丢 HomeDir 发生在 **调用方** `keystone.Service.Locate`（`keystone.go:211`），不是 locator。只把接缝预算分给 locator，miss 补 HOME 变成无测试的内部故事。见 I2。这不是假缝，是缝上符号取低了——更高且仍合法的缝是 `keystone.Service.Locate`（调用方两个 handle），locator 作为它的下游。
   - web 不拼接，不必给 `d_web` 占一条缝。`RoomPanel.tsx:324` 的 `selectedRoom.attach.command` 是 **任务** attach，不是协调者 attach，不要误接。

3. **协调者供给挂在 `coordinatorRunner.Launch/Resume`**（调用方 keystone `LaunchForCard` / `Wake`）
   - 新建行为挂既有适配器，调用方真实。WakeHome **不是** 调用方——这是本缝存在的理由（B293 执行者干净 HOME）。
   - 假缝风险：抽出未导出的 `supplyCoordinatorHome` 只测帮手。正文已写挂 Launch/Resume，测试决定 3 的变异是「Launch 不写配置」。执行时必须从 Launch/Resume（或 HTTP launch）进，对照支从同一 `Host` 调 `WakeHome`。
   - `coordinatorRunner` 未入 `best.json`，但不妨碍它是生产适配器。

假缝原文「文案、日志字段名、是否 symlink 还是 copy」合格。I1 的 **路径白名单** 不是假缝，是产品语义，必须从「plan 内部锁」里拿出来（已在 I1）。

## 6. 二解测试（承重句）

| 句子 | 读法 A | 读法 B | 必须以正文消掉 |
|---|---|---|---|
| 「占用目录也要补缺，不得删 session db」 | 只补 config + `.config/opencode/{AGENTS.md,skills/}` | 同步整棵 `.local/share/opencode` 或重建 HOME | **I1** |
| 「Locate 内存未命中时必须带上载体 HomeDir」 | gateway/keystone 只读 `Carrier()` | 抄 Launch 走 `LaunchAdmit`；或只改 locator、miss 不管 | **I2** |
| 测试 2「ref.HomeDir=~ 或 miss 后补上」 | 两支都测 | 只测热路径 ref | **I2** |
| 「展开失败即失败」+ 测试 1 只给成功 `~/` | 另有失败夹具 | 成功路径已绿 = B327 做完，fail-open 留下 | **I3** |
| 「本机 agentd 正在用的 config.yaml」 | 活进程 token + 绝对 DataDir/DSN | 抄 `DefaultPath` 文件 | **I4** |
| 「协调者全套 = 账本凭据 + 规则/skill」 | 只有 handoff config | 含 opencode auth.json | **I5** |
| 「展开收口在 hostapi」 | attach 另展开 | attach 照抄 `RunCommand` 的 `HOME=~/...` | **I6** |
| 「不新增 wire；command 自带 HOME」 | 改字符串内容，字段不变 | 给 AttachInfo 加 `home_dir` / 改 PTY env | 头部 L3 门槛已够；M4 夹具不是反例 |
| 「房间出现协调者自己的非 pointer 叙事」 | token+skill 后协调者 `room send` | agentd 把 `RoundResult.Output` 代写进房间 | 弃选已钉；不要在实现时「保险」地镜像 |

## 7. 并入 B324–B327：不是硬收成一类

两条簇，一条用户旅程，可以收在同一张 L2 卡：

| 源卡 | 根因簇 | 活代码锚点 | 并入是否正当 |
|---|---|---|---|
| B323 | 供给：隔离 HOME 无本机 token → CLI first-run → 401，房间空 | `config.Load` first-run；Launch 零供给 | 主卡 |
| B326 | 供给：凭据拷贝故意不搬规则树；占用不再补 | `copyMainCredential`；`WakeHome` 空目录门 | 与 B323 同一供给簇。检测负例必须留下，否则 B293 回潮 |
| B327 | 路径身份：展开 fail-open | `expandTurnHomeDir` 失败原样返回 | 正当。但成功路径已展开，本卡剩余是 fail-closed + 别把未展开串送进 attach/日志（I3） |
| B325 | 路径身份：attach command 无 HOME；miss 丢 HomeDir | `attachLocator`；`keystone.Locate:211` | 正当。与 RunTurn 不是同一处代码，但是同一不变式的第二只手 |
| B324 | 用户面：冷 Resume Session not found → 重建指针 | `overlayResumeRef` 其实 **已经** 带 HomeDir；会话不在该 db | **不要当成第三处独立展开 bug。** 它是 B327 历史落点 / 未展开 HOME 写会话、再加上 attach 找不到的用户面。热路径 `rebuilt=false` 与「同一 HOME 可续」一致。OOS 不清理存量 `~/` 垃圾 ⇒ 旧会话可能仍要重建一次，这是可接受的一次性，不是漏做 B324 |

不相干、正确排除的：B328 inbox 解码、B329 人坐下出示、B330 charter 绑小队、wait 按席位过滤。空房间在 401 之外还有 inbox 假说；本卡用 token+skill 解释，并用「不镜像 Output」防止用代写掩盖。真机门若 token 已对仍无房间叙事，那是 B328，不要在本卡加镜像。

## 8. Out of Scope

已写、且该推迟的都在：

| 项 | 正文位置 | 审查意见 |
|---|---|---|
| 镜像回合 Output 进房间 | 永不做 `b323.md:89`；弃选 `b323.md:48` | 必要。代写会把 401/无 skill 藏成「房间有字」 |
| B329 人坐下出示 | 本期不做 `b323.md:90` | 另一入口，不是隔离 HOME |
| B328 inbox 解码 | 同上 | 房间空的另一假说；token 修好后若仍空，走 B328 |
| B330 charter 绑 `runner` | 同上 | 小队身份，不是 HOME |
| wait 按席位过滤 | 弃选 `b323.md:50` + 本期不做 | 与本不变式无关 |
| 检测时把执行者 HOME 做成全套 | 永不做 | 保 B293；测试 3 对照支必须留下 |
| 清理已存在的 `~/` 垃圾 | 本期不做 | 正确。人工；本卡只保证不再新写 |
| hostapi 实装 grok/claude/codex 当协调者载体 | 本期不做 | 与 `supportedCLIs` 仅 opencode 一致 |
| 不 reopen B156.3 / B293 / B299 | `b323.md:91` | B299 已把 `SessionRef.HomeDir` 和 overlay 补上；本卡不是 reopen，是补供给与展开失败 / attach |

漏写、建议补进「本期不做」（不挡批准，属 Minor）：

- Windows attach 的 `HOME=` 前缀 / cmd.exe 形态（M3）。
- 把历史 `repos/.../~/.handoff/home/muse` 里的 session 迁回展开后的隔离 HOME（一次重建可接受，不要在本卡做迁移器）。

## 9. 红色回路与变异唯一性

| 回路 | 按正文能红？ | 变异打中唯一？ | 结论 |
|---|---|---|---|
| RunTurn：`~/` + 非空 Workdir，不得出现名为 `~` 的目录；HOME 绝对 | 现行成功路径 **已经绿**（I3） | 「失败原样返回」打不中这支 | 必须加 UserHomeDir 失败夹具，否则 B327 剩口无锁 |
| Locate：command 含 `HOME=/` 与 `--session` | 热路径能红（locator 今日不含 HOME） | 「或 miss」让 miss 无锁 | 拆两支。miss 那支的变异是「重建 ref 不带 HomeDir」 |
| Launch 后隔离 config token = 注入 token；opencode 规则可见；WakeHome 不建 `.config/opencode/skills` | token 缺失能红 | 「Launch 不写配置」打中 token；**打不中** 占用补缺、禁写 session db、覆盖 first-run | 加占用对照：目录里已有 first-run 残件 + 假 session 文件 → token 被覆盖、session 文件仍在、WakeHome 对照仍无技能树 |
| 真机合 main：`card coordinate` 后隔离 `handoff status` 退 0；房间非空无新指针；attach 能进会话 | 机外，正当 | 不替代机内三缝 | 保留。实现机 linux-01 只跑机制测试，合 main 前本机再跑，正文已写对 |

机内三缝方向对，条数对；牙不够，补在 I1–I3，不是再加第四条缝。

## 10. 缺陷族

- **生命周期 / 状态机中断** | agentd 重启后 `sessions` 空，Locate/Resume 还能找到同一 HOME 吗？ | Resume 有 overlay，HOME 来源够。Locate 不够（I2）。供给挂 Resume 则重启后第一次唤醒会补缺，这是优点，但更要禁写 session db（I1），否则重启变成强制重建。存量 `~/` 垃圾里的旧会话 OOS，允许一次 rebuild。
- **静默失败 / 误导报错** | 展开失败仍跑回合 = 字面 `~` 静默落盘。 | 现行 fail-open 就是这一族。改 fail-closed 之后错误必须可行动（含原串与「无法展开 HOME」），不要只 `return err` 成 `hostapi: 回合失败`。attach 展开失败应 400，不能返回不含 HOME 的 command 假装能进 TUI。
- **跨平台假设** | `HOME=` 前缀是 POSIX shell；`expandHomePath` 认 `~/` 与 `~\`。 | 合 main 在 macOS，实现测试在 linux-01，够用。Windows 写进 OOS（M3）。隔离登记串用 `/`（`IsolatedHomeRoot` 字符串拼接），与现行载体法一致。
- **假红 / 假绿测试** | 见 §9。内部 `buildEnv` 锁不能顶 RunTurn 缝；`.config/skills` 不能顶 opencode 真树；Locate 只测热路径会假绿。 |
- **门禁绕过** | 无新 HTTP。供给是本机写隔离 HOME，不经 `/api/host/wake`。不要把供给做成未鉴权新端点。 |
- **序列化边界** | Command 字符串变了，字段没变。穿过 HTTP 的回归：`GET /coordinator` 或 `POST /attach` 的 JSON `command` 含 `HOME=/` 且含 `--session`（热路径一支即可）。夹具更新不是新字段（M4）。 |
- **承重安全属性** | 执行者 WakeHome 仍不得出现 `.config/opencode/skills`——测试 3 对照支必须能红。隔离 HOME 的 token 等于本机 agentd token，避免「看起来配好了其实 401」。不要把主 HOME 的 session db 写进隔离侧。 |

## 11. 批准前最小补丁清单

只改 spec 正文（吸收即批准）。不改代码。

1. **方案 2 写成写入白名单 + 禁写集**：隔离 HOME 内只保证 `.handoff/config.yaml`（token = 本进程 agentd token；DataDir/DSN 相对则改写绝对）、该 CLI 的 `.config/opencode/AGENTS.md` 与 `.config/opencode/skills/`。禁止 `RemoveAll` 整棵 HOME；禁止改写 `.local/share/opencode/` 下除「I5 若选拷 auth.json」以外的条目。占用 = 补缺 + 覆盖 first-run 残件 config，不是重建。
2. **I5 二选一**：叫机器人补缺失的表内 CLI 凭据，或明文凭据仍只走检测。
3. **接缝 2 / 测试 2**：miss 时 HomeDir 来自已登记载体、禁止 LaunchAdmit；填充在交给 locator 之前；热路径与 miss 各一支测试。
4. **测试 1**：增加 `userHomeDir` 失败 → `RunTurn` 报错且 Workdir 下无 `~` 条目。问题陈述把 B327 成功路径改成「已展开，剩余 fail-open」。
5. **实现决定**：展开执法点写两处（进子进程 env 前；拼 attach command 前）。禁止抄 `scheduling.RunCommand` 的未展开 `HOME=~`。
6. **测试 3**：加占用对照（first-run 残件 + 假 session 文件仍在）。WakeHome 对照路径用 `.config/opencode/skills`，与问题陈述对齐。
7. **I4**：隔离 token 对齐活进程，不是「哪份 yaml 方便抄哪份」。

建议顺手（不单独挡批）：M1 测试名/路径、M2 日志字段不是 env 证据、M3 Windows OOS 一句、OOS 不迁历史 `~/` 会话。

吸收后路由：L2 → plan。plan 里不要发明新 HTTP 字段、不要改 PTY env 组装、不要动 WakeHome 的「只拷凭据、空目录才拷」。

## 12. L2/L3 意见（短句）

**维持 L2。** 用户旅程一条，不新增 wire，attach 用命令字符串自带展开后的 `HOME=`，工作台保持原样透传。若实现改到终端 tab 的 env API 或给 `CoordinatorAttachInfo` 加字段，升 L3 并停下来问——这条门槛正文已有，审查确认。I1–I6 是 L2 spec 必须自己写死的产品句，不是合同节点的借口。
