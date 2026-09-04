# B329 spec 审查

审查对象：`docs/superpowers/specs/b329.md`（状态：待用户批准；头部自称 **L2**）
对照台账：`docs/superpowers/specs/b329-ledger.md`
对照代码：工作树 `/Users/sycm/.handoff/worktrees/b329-charter`，分支 `cards/B329-charter`，基线 `origin/main` @ `4e633e9a6`。本审查只读工作树文件，不改 spec/代码、不 commit。
审查者：独立 spec 审查人（charter 流；与作者无会话史，一切以亲手读码为准）
日期：2026-09-04

行号按当前工作树，会漂。`codegraph/baseline.json` 核过 `n_cmd_currentSeatIdentity`；`codegraph/best.json` 核过 `k_cmd_fn → d_cli`。who-calls 以 `rg` 为准（图只画出 bind 一条边，见 §7）。本场 grok 的 `GROK_SESSION_ID` / `GROK_AGENT` 采信台账为发现现场，本审查进程未当作磁盘事实复跑。

## 1. 总判

**吸收补丁后再批。** 不能原样批准，也不驳回。

方向对，一条不变式成立：出示函数仍唯一；扩展的是它怎么得到 `(cli, session_id)` 这一对，不是席位字符串、账本写面、HTTP 占用、Wake 或房间书写者门。人在 grok/claude 这场对话里能坐下，没有当前会话时用手填 `--cli/--session` 出示**自己的**一对——与 B307 弃选的 `rebind --to`（把座位指给别人的会话）不是同一件事。弃选站得住。L2 独立验证成立，不抬 L3（见 §3）。接缝不是假缝（见 §4）。

不能原样批准的原因是**来源算法在正文里能读成互不兼容的实现**，其中测试 7 锁住的那支会让 Resume 注入的机器人在继承了宿主 `GROK_SESSION_ID` 的 agentd 下出示失败，破坏 B307/B312「叫机器人后能再出示」这条已经冻结的跨进程身份边界。本卡声明不改 Resume 注入，于是这条冲突规则在 L2 范围内没有配套执法点。

批准前最小补丁见 §6。补的是 spec 正文句子，不是代码。吸收后可以进 plan。

## 2. Findings

### Critical

#### C1. 来源顺序是第一匹配，冲突段和环境源互斥；测试 7 锁住会打穿机器人出示的那支

- **位置**：方案来源顺序 `b329.md:31-38`、冲突段 `b329.md:40`、测试 6–7 `b329.md:88-89`、对话冻结 `b329.md:101-103`；活代码 `cmd/card_seat.go:18-28`（今日只读完整 `HANDOFF_SESSION_*`）、`internal/agentd/server.go:2586-2598`（`resumeTurnRequest` 只追加 `HANDOFF_SESSION_CLI/ID`）、`internal/hostapi/driver.go:226-260`（`buildEnv` 以 `os.Environ()` 为底，只覆盖 `req.Env` 里出现过的键和 `HOME`）；B307 产品句「叫机器人写入的那一对，必须能被该机器人进程里后续的 handoff 再出示」；B312 合同 §3 第 3 款与三重闸门 3。
- **活代码事实**：机器人续接并不擦除父进程环境。`TurnRequest.Env` 只有那两个 `HANDOFF_SESSION_*` 键；`buildEnv` 会把 agentd 进程里其余环境原样带进子进程。因此：若 agentd 从一场 grok 对话拉起（或用户 shell 导出了 `GROK_SESSION_ID`），无头回合里会**同时**有完整 `HANDOFF_SESSION_*`（机器人自己的一对）和非空 `GROK_SESSION_ID`（人的宿主会话）。编码后必然不是同一字符串。本卡正文写「本卡不改这条注入」。
- **三种读法**（可观察行为全不同）：
  1. **环境源第一匹配**：完整 `HANDOFF_*` 即出示注入对，忽略 `GROK_SESSION_ID` / `CLAUDE_CODE_SESSION_ID`。机器人路径保持现网。编号清单 2 的「否则」链支持这一支。
  2. **环境源互斥 fail-closed**：`HANDOFF_*` 与 `GROK_SESSION_ID` 同时完整且编码不一致 → 失败。冲突段第二句 + 测试 7 支持这一支。在读法 1 的环境里，机器人的 `--step` / 协调者 `room send` 会报未出示席位。
  3. **宿主优先、flag 只是填空**：有 `GROK_SESSION_ID` 就坐 grok，命令行另一对被忽略。对话冻结「没有当前会话时才手填」支持这一支；故事 5 与测试 6 反对。
- **为什么承重**：B312 把「当前会话只由 `HANDOFF_SESSION_*` 注入」冻成 CLI 与无头回合的跨进程身份边界，目的就是机器人还能出示自己的座位。本卡要给**人开的 grok** 加宿主键，不该把「父进程里碰巧有宿主会话 id」写成与注入对冲突。按测试 7 落地，现网机器人出示在常见「从 grok 里起 agentd」路径上会红；按编号清单落地，测试 7 红。零上下文 plan/implement 必选错一题。用改 `buildEnv` / Resume 去剥 `GROK_SESSION_ID` 来救测试 7，会走进 `d_execution_host`，那是抬 L3，不是本卡声明的范围。
- **建议**：方案里写成**一条**算法，删掉环境源之间的冲突句。见 §6 第 1 条。测试 7 改成机器人回归：完整 `HANDOFF_*` + 不同的 `GROK_SESSION_ID` → 仍出示注入对。

### Important

#### I1. 残缺 `HANDOFF_SESSION_*` 是失败还是落到宿主，没钉

- **位置**：方案「完整的 `HANDOFF_SESSION_CLI` + `HANDOFF_SESSION_ID`」`b329.md:35`；flag「只给其中一个 → 失败，不跟环境变量拼一套」`b329.md:33`；活代码 `card_seat.go:19-22`（今日任一键缺失或空串，整函数失败，没有下一源）。
- **事实**：现网缺一即失败。正文改成「完整的」之后，残缺（只设了 CLI、或 ID 为空串）可以读成：① 与残缺 flag 同构，整次出示失败；② 不算完整注入对，落到 `GROK_SESSION_ID`。② 会让残留的 `HANDOFF_SESSION_CLI=opencode` 加上人的 `GROK_SESSION_ID` 拼成 `cli:grok#…`，把机器人调试残留和环境边角料拼成一套假身份——正是 B307「不从环境边角料推导」要禁的形状，只是拼料从 `USER` 换成了半套注入键。
- **为什么承重**：与 flag 残缺规则同构却不写同一句话，实现会在「保持现网缺一即死」和「放行到 grok」之间飘。
- **建议**：写死残缺 `HANDOFF_*`（缺一或空串）整次失败，文案点名缺哪个键，不跟 `GROK`/`CLAUDE`/flag 拼。`GROK_SESSION_ID` / `CLAUDE_CODE_SESSION_ID` 本身空串当不存在（它们没有配对的物种键）。

#### I2. `GROK_SESSION_ID` 与 `CLAUDE_CODE_SESSION_ID` 同时非空时的行为没钉

- **位置**：来源顺序 `b329.md:36-37`（grok 否则 claude）；冲突段只写了 `HANDOFF_*` 对宿主，没写两种宿主互斥；故事 1–2 `b329.md:60-61`。
- **事实**：从 grok 的 Bash 工具拉起 `claude` 时，子进程可以同时带着 grok 的会话 id 和 Claude Code 注入的 `CLAUDE_CODE_SESSION_ID`（该键是 Claude Code v2.1.132 起给 Bash 子进程的真实键，changelog / GitHub #56879；本仓生产代码零处读取）。编号清单会让人坐成 grok；人以为自己在 claude 里坐下。
- **为什么承重**：两种合法宿主同时在，是「不猜没核过的键」之后仍会遇到的真情况。第一匹配与 fail-closed 坐出来的席位字符串不同，后续 `--step` 对不上。
- **建议**：两种宿主会话 id 同时非空 → 失败，文案要人去掉其中一个或改用手填。不要默默让 grok 赢。

#### I3. flag 只保证 bind 穿过 CLI；其余出示入口可以没挂上仍让测试 8 绿

- **位置**：方案 `b329.md:42`、弃选「flag 只给 bind」`b329.md:56`、故事 3 `b329.md:62`、测试 8 `b329.md:90`；活代码出示入口四处：`cmd/card_driver.go:23`（bind）、`:101`（rebind `--self`）、`cmd/card_node.go:145`（有席位 `--step`）、`cmd/room.go:133`（`kind != user` 的 `room send`）。`card coordinate`（`cmd/card.go:456-471`）与 `rebind --launch`（`card_driver.go:134-143`）不调用出示函数。今日 `cmd/` 下没有 `--cli` / `--session` flag。
- **事实**：测试 8 第一句要求「走出示函数的命令都能把 flag 传到该函数」，第二句立刻降成「至少 `card bind` 穿过真实 CLI 参数边界」。`runLedgerCLI`（`cmd/ledgercli_test.go:33-55`）确实能锁 cobra 边界。只测 bind 的话，rebind/`--step`/`room send` 可以继续零参调用 `currentSeatIdentity()`，普通终端坐下之后下一跳仍报未出示——这正是弃选「flag 只给 bind」的失败模式。
- **为什么承重**：出示函数唯一的产品含义是四个入口同一对。测试预算把三个入口让出去，plan 会只改 bind。
- **建议**：删掉「至少 bind」这半句。四条出示命令都必须把 `--cli/--session` 注册到**该命令自己的** flags（禁止 `PersistentFlags` 挂到 `card` 或 `root`，以免变成弃选的「本机登录」）。`coordinate` 不注册即可（未知 flag 即拒绝）。`rebind` 是同一条命令的两种 mode：`--launch` 加上这两个 flag 必须语义拒绝，不能靠「没注册」。测试：bind / `rebind --self` / 有席位 `--step` / 非 user `room send` 各至少一支真实 CLI 参数；`coordinate` 与 `rebind --launch` 带 flag 失败。

#### I4. 空座 `--step`、无 `--step` 的 dispatch、`kind=user` 的 `room send` 带上身份 flag 时的行为没钉

- **位置**：方案「只挂在走出示函数的命令上：…有席位时的 `card dispatch --step`、协调者 kind 的 `room send`」`b329.md:42`；活代码 `cmd/card_dispatch.go:278-296,358-365`（`--step` 是 `card dispatch` 的普通 flag；无 `--step` 走模板派发，actor 固定 `ledgerActor()`，不调用出示函数）；`cmd/card_node.go:143-155`（空座或两列皆空时**不**调用出示函数，actor 仍是 `ledgerActor()`）；`cmd/room.go:126-138`（仅 `kind != user` 才出示）。
- **事实**：「有席位时」是运行时条件，不是 cobra 注册条件。`--cli/--session` 一旦挂上 `cardDispatchCmd` / `roomSendCmd`，空座 `--step`、裸 `card dispatch`、`--kind user` 在语法上都能带这两个 flag。
- **四种读法**：忽略；拒绝；空座也走出示并把 HTTP actor 从 `cli:user@host` 换成席位字符串（不写座，但事件 actor 变了）；当成坐下（派发占座，B307/B312 已禁）。
- **为什么承重**：空座 `--step` 改 actor 而不占座，会让无人值守派发的审计身份从人尺度变成会话尺度，房间/事件对账全变，且看起来像「我已经出示过了」。忽略则人以为坐下成功。
- **建议**：这三条路径带上 `--cli` 或 `--session` → 失败，不得调用出示函数、不得改 actor、不得占座。空座 `--step` 继续今日的 `ledgerActor()` 审计语义。

#### I5. 现网 `TestCurrentSeatIdentityRequiresInjectedPair` 对宿主键不封闭；「仍绿」与本场 grok 互斥

- **位置**：测试 2 `b329.md:84`、测试 4 `b329.md:86`、台账末行；活代码 `cmd/card_driver_test.go:17-28`（只 `Setenv` 两个 `HANDOFF_SESSION_*` 和 `USER`，不碰 `GROK_SESSION_ID` / `CLAUDE_CODE_SESSION_ID`）。
- **事实**：Go 的 `t.Setenv` 不清除未点名的键。本卡落地后，若测试进程里有 `GROK_SESSION_ID`（本场 grok 的发现现场正是如此）：① 成功支在 C1 读法 2 下会因冲突失败；② 负例清空 `HANDOFF_SESSION_ID` 后会落到 grok 身份而不是报错，测试期望 error。CI（无宿主键）与 grok 里跑 `go test` 会分叉。
- **为什么承重**：spec 自己把「该负例仍须绿」写成门。不写隔离，门在发现现场就是假的。
- **建议**：凡出示测试必须显式清空未在本支声明的 `HANDOFF_SESSION_*`、`GROK_SESSION_ID`、`CLAUDE_CODE_SESSION_ID`（以及本卡不认的 `CLAUDE_CODE_REMOTE_SESSION_ID`）。测试 2 的负例改成「无任何来源 + `USER` 仍不得回退」，不要依赖「进程里碰巧没有 grok」。

### Minor

#### M1. B312 引用写成「§3.3」，合同第 3 节是 1–5 款不是 3.3

- **位置**：现状读数 `b329.md:22`（「B312 合同 §3.3」）。活文档 `docs/superpowers/specs/b312-contract.md:126-131`：Resume 注入是第 3 节第 3 款。改成「§3 第 3 款」以免 plan 去找不存在的小节。

#### M2. 方案写「协调者 kind」，现状和测试写「非 user」；活代码是 `kind != user`

- **位置**：`b329.md:20,42,79` vs `cmd/room.go:131-138`。非 user 含 `escalation` / `deviation` / `closing` / `reply` / `relay` / `pointer`（`internal/proto/rooms.go:12-20`）。本卡 OOS 不改书写者门。正文统一成与活代码相同的「`kind != user`」，避免有人把 `relay` 从出示里拿掉。

#### M3. 图 who-calls 覆盖不足；现状读数的四处调用方以 `rg` 为准是对的

- `codegraph/baseline.json` 只有 `n_cmd_cardBindCmd_RunE → n_cmd_currentSeatIdentity`。`n_cmd_roomSendCmd_RunE` 的出边是 `openRoomService` / `Send`，没有出示函数。`n_cmdrunStepDispatch` 签名仍是旧的 `func runStepDispatch(..., st *ledger.Store, id, node, actor string)`（`baseline.json:103468-103476`），活代码是 `runStepDispatch(cmd, id, node)` 并在函数内自己出示。台账「图调用方四处」不成立；spec 正文按源码列四处，成立。记图覆盖债，不挡批准。

#### M4. `GROK_SESSION_ID` 不是本仓生产键，公开 grok 环境变量表未列此名

- 本卡把它当宿主键是发现现场的产品决定，可以。实现决定已写不要求 `GROK_AGENT`。建议在实现决定补一句：键名以本场 grok 真机为准；若产品改名，本卡范围不跟改，走手填。不必为此抬级。

#### M5. 工作台现状是「没有坐下按钮」，不是「坐下按钮不可用」

- `web/src/app/cards/CoordinatorPanel.tsx:146-147`：空座只有「叫机器人」，有人是「换绑：叫机器人」；无坐下、无换绑给我。CardDrawer 测试里的「坐下」是来源展示词（`CardDrawer.test.tsx:104-115`）。B312 §6.2 允许「不展示」或「展示但不可用」。故事 7「网页坐下仍不可用；没有填一对的表单」两种现状都满足。建议故事 7 改成「工作台继续不提供坐下/换绑给我，也不加填一对表单」，避免实现者去画一个禁用按钮。

## 3. 定级意见

**维持 L2。** 头部自称 L2，审查同意。

定级两问：

1. **跨几个子系统契约面？** 定稿范围是 `cmd/card_seat.go#currentSeatIdentity` 及其四个 CLI 调用方怎么得到这一对。`codegraph/best.json`：`k_cmd_fn` 归顶层域 `d_cli`。baseline 域名 `d_coordination_cli` 是旧词，最优树已归 `d_cli`。席位编码（`internal/proto/seat.go#EncodeSeatIdentity`）、`Store.BindSeat` / `RebindSeat`、HTTP `POST .../coordinator/rebind`（活代码 `coordapi.go:165-180` 拒绝 `mode=self` 与非空 `identity`）、Wake、房间 `VerifyWriter`、Web 抽屉，正文都声明不动。无新 HTTP、无新 wire 字段。
2. **动不动契约层？** B312 §2.1 第 2 款与 §3 第 1 款是**出示来源规则**，不是账本/HTTP/编码骨架。本卡在 implement 回写 B312 文档以避免两份真相，属于冻结物触碰后的回写，不另开 contract 节点——前提是 C1 的补丁**不要**改 `resumeTurnRequest` / `buildEnv`。一旦为了救环境源冲突去剥宿主环境变量，改动跨进 `d_execution_host`（以及 agentd 组装点），那就是跨子系统契约面，应停下来抬 L3。

不是 L1：来源优先级、冲突、flag 挂哪些命令，plan 增量不是零。

不抬 L3 的举证：跨子系统契约面要有新的或被改写的 **wire / 账本写面 / 编码语法 / Resume 注入键**。本卡四者都冻住。允许人在 CLI 上出示自己的一对，不改变「谁能写座位」——`BindSeat` 仍然只信调用方递过来的规范字符串；HTTP 仍然不接受 `identity`。与 `--to` 的差别是入口语义（出示自己 vs 指定别人），不是新缝。

## 4. 接缝合法性

一条缝：`cmd/card_seat.go#currentSeatIdentity`。

| 调用方 | 活代码 | 图 |
|---|---|---|
| `card bind` | `cmd/card_driver.go:23` | 有边 `n_cmd_cardBindCmd_RunE → n_cmd_currentSeatIdentity` |
| `card rebind --self` | `cmd/card_driver.go:101` | 无边（图覆盖债） |
| 有席位 `card dispatch --step` | `cmd/card_node.go:145`（`DriverSession != "" \|\| DriverSource != ""`） | `n_cmdrunStepDispatch` 签名过期，无出示边 |
| `kind != user` 的 `room send` | `cmd/room.go:133` | `n_cmd_roomSendCmd_RunE` 只连 `openRoomService` / `Send` |
| 测试 | `cmd/card_driver_test.go:20,25` | 测试节点通常不入生产调用图 |

不是假缝：函数已导出给生产命令，不是为了走满分支新抽的纯函数。spec 写「不抽无调用方的纯函数占缝」合法。若 implement 另抽 `resolveSeatSources` 只给单测用、四个命令不走它，那才是假缝，plan 审查时按这条退回。

漏调用方：生产四处 spec 都点到了。无第五处生产调用（全仓 `rg currentSeatIdentity` 仅上述 + 测试）。`card coordinate`、`rebind --launch`、空座 `--step`、模板 `card dispatch`、`kind=user` 的 `room send`、`takeover`/`release` 今日都不走出示函数——本卡也不该让它们默默走。I3/I4 补的是 flag 挂载，不是新缝。

HTTP / Web 不是本卡接缝：`handleCoordRebind` 拒绝 self/identity；CoordinatorPanel 无坐下表单。正文 OOS 成立。

## 5. 二解测试

| 陈述 | 读法 A | 读法 B | 承重？ |
|---|---|---|---|
| 来源顺序 1 然后 2，再加上冲突段 | flag 覆盖环境 | 两者都在必须编码一致；环境源之间也互斥（测试 7） | **是**（C1）。必须收成一条算法 |
| 「没有当前会话时才手填」 | 有宿主则忽略 flag | 有宿主则 flag 必须一致，否则失败 | **是**。故事 5 锁后者；冻结句要改到与故事 5 同构 |
| 「完整的 HANDOFF_*」 | 残缺则落到 GROK | 残缺则整次失败 | **是**（I1） |
| GROK 与 CLAUDE 同时非空 | grok 优先 | fail-closed | **是**（I2） |
| 「至少 bind 穿过 CLI」 | 只测 bind，其余入口 unit 调 helper | 四个入口都要真 flag | **是**（I3） |
| flag 挂在 `card dispatch` / `room send` 上 | 空座/`user`/无 `--step` 时忽略 | 拒绝 | **是**（I4） |
| `--cli` 的值 | 任意能过 `EncodeSeatIdentity` 的物种名 | 只允许 grok/claude/… 白名单 | 否。正文已说走现网编码规则，非法字符失败 |
| `currentSeatIdentity` 签名是否加参 | 零参 + 包级 cobra 变量 | `(cli, session string)` | 否。用户看不见；plan 可定，只要四个入口同一函数 |
| 手填能否写成别人的一对 | 能（与 export `HANDOFF_*` 同安全模型） | 要证明「这是我」 | 否。现网已是出示即信；本卡不升级为密码学身份 |
| 工作台「坐下仍不可用」 | 不画按钮 | 画禁用按钮 | 否（M5）。建议写死不画，免得白做 |

## 6. 批准前最小补丁

只改 spec 正文。建议直接替换方案里「来源顺序 + 冲突」两段，不要两段并存。

1. **来源算法（替换 `b329.md:31-40`）**写成四步，禁止第三种读法：
   - 先看 flag：`--cli` 与 `--session` 都非空 → 编码为 flag 对；只非空一个 → 失败，不跟任何环境变量拼。
   - 再看注入：`HANDOFF_SESSION_CLI` 与 `HANDOFF_SESSION_ID` 都非空 → 编码为注入对。缺一或空串 → 整次出示失败（不落到宿主）。
   - 否则看宿主：仅当注入对完全不存在（两个键都未设或都空）时，非空 `GROK_SESSION_ID` → `cli:grok#…`；否则非空 `CLAUDE_CODE_SESSION_ID` → `cli:claude#…`。两个宿主 id 同时非空 → 失败。
   - 仍没有 → 失败；文案同时提到到 grok/claude 对话里重试，以及 `--cli` 与 `--session`。
   - **冲突只剩一句**：flag 对若存在，且注入/宿主按上面规则也会得出一对，两者编码后不一致 → 失败；一致则通过。完整注入对存在时**忽略**宿主键（机器人回归）。禁止环境源互斥失败。禁止用改 Resume/`buildEnv` 来「清理」宿主键。
2. **测试 7** 改为：完整 `HANDOFF_*` + 不同的 `GROK_SESSION_ID` → 出示注入对（变异：按冲突把这次打红的实现必须红）。另加：flag 对与注入对不一致 → 失败；flag 对与仅有的 GROK 对不一致 → 失败。
3. **测试 8** 删「至少 bind」。四条出示命令各一支真实 CLI flag；`coordinate` / `rebind --launch` 带 flag 失败。写明 flag 注册在子命令本地，不进 `PersistentFlags`。
4. **补负例**：空座 `--step`、无 `--step` 的 `card dispatch`、`--kind user` 的 `room send`，只要带了 `--cli` 或 `--session` → 失败；席位与 actor 不变。
5. **测试隔离**：出示相关测试必须清空未声明的 `HANDOFF_SESSION_*` / `GROK_SESSION_ID` / `CLAUDE_CODE_SESSION_ID` / `CLAUDE_CODE_REMOTE_SESSION_ID`。把这句话写进测试决定，这样测试 2「仍绿」在 grok 里也成立。
6. **冻结句**（`b329.md:101-103`）改成与算法同构：有当前注入或宿主会话时，手填必须与之编码一致；没有当前会话才允许只靠 `--cli/--session` 出示自己的一对。不是 `rebind --to`。
7. **B312 回写范围**保持原文，但把「§3.3」改成「§3 第 1 款（出示来源）与第 3 款（Resume 注入键不变）」。§2.1 第 2 款按原文改「禁 `--to` 式接班者，允许出示自己的 `--cli/--session`」。编码语法、`BindSeat`/`RebindSeat`、Resume 注入键、Web 不能坐下、HTTP 换绑不接受 `identity`，继续不动。
8. （建议顺手）故事 7 改成「工作台继续不提供坐下/换绑给我，也不加填一对表单」；方案/测试里的「协调者 kind」改成「`kind != user`」。不挡批准。

M1/M3/M4 记账即可。

## 7. 跑过的命令与读过但未成 finding 的地方

读过（亲手打开或 `rg`，不是转述 spec）：

- spec / 台账：`docs/superpowers/specs/b329.md`、`b329-ledger.md`
- 上游：`docs/superpowers/specs/2026-09-03-b307-bind-current-session-design.md`（故事 5、出示函数唯一、弃选 `--to`）；`docs/superpowers/specs/b312-contract.md` §2.1、§3、§5.1、§6.1–6.2、§11 闸门 3、冻结清单 38
- 出示与调用方：`cmd/card_seat.go`、`cmd/card_driver.go`、`cmd/card_node.go`（`runStepDispatch`）、`cmd/room.go`、`cmd/card.go`（`cardCoordinateCmd`）、`cmd/card_dispatch.go`（`--step` 与模板派发分叉）
- 编码与账本：`internal/proto/seat.go`、`internal/ledger/binding.go`（`BindSeat`/`RebindSeat` 签名未在本卡改）
- 注入与子进程环境：`internal/agentd/server.go#resumeTurnRequest`、`internal/hostapi/hostapi.go` `TurnRequest.Env` 注释、`internal/hostapi/driver.go#buildEnv`
- HTTP / Web：`internal/agentd/coordapi.go#handleCoordRebind`、`internal/proto/scheduling.go#CoordinatorRebindReq`、`web/src/app/cards/CoordinatorPanel.tsx`、`web/src/app/cards/CardDrawer.tsx` 席位展示、`web/src/api/scheduling.ts` 无 bind client
- 测试：`cmd/card_driver_test.go#TestCurrentSeatIdentityRequiresInjectedPair`、`TestCardBindUsesCurrentSeat`、`cmd/card_node_test.go`（无席位不匹配用例）、`cmd/room_test.go`（只锁 user kind + `ledgerActor`）、`cmd/ledgercli_test.go#runLedgerCLI`
- 图：`codegraph/baseline.json` `n_cmd_currentSeatIdentity`（约 145018、23590 行）、`n_cmdrunStepDispatch` 过期签名、`n_cmd_roomSendCmd_RunE` 出边；`codegraph/best.json` `k_cmd_fn → d_cli`、`d_cli` 顶层域；`codegraph/target.json` `d_cli → d_ledger` / `d_cli → d_protocol`（B312 注记：CLI 只经 proto 编码 helper）
- skill 现状：`skills/handoff/SKILL.md:720` 排障仍写「到 grok/claude 这场对话里再按」——正文已把 skill 改文案放到实现、不在本节点改，不记 finding
- 定级法条：`/Users/sycm/.handoff/repos/charter/skills/spec/SKILL.md` 定级两问；`architecture-law/SKILL.md` 第一条

读过未成 finding：

- `EncodeSeatIdentity` 拒空段、首尾空白、`#`、cli 内 `:`——spec「仍然走现网编码」成立。
- bind 走本地 `Store.BindSeat`、不经新 HTTP 字段——`card_driver.go:34` 与 spec 现状读数成立。
- `rebind --self` 本地 CAS + `CoordinatorForget`；`--launch` 只 POST `{mode:launch}`——与「coordinate / rebind --launch 不得用这一对指定机器人」相容。
- 今日 `cmd/` 无 `--session` flag，不存在撞名。
- 空座 `--step` 仍用 `ledgerActor()`、不写席位——B312 冻结清单 57，本卡不应顺手改掉（I4 就是防这个）。
- Web 无填一对表单、无 bind API——故事 7 / OOS 成立。
- 不认 `CLAUDE_CODE_REMOTE_SESSION_ID` 与 Claude Code 云会话键分工一致，OOS 成立。
- B307 故事 1「作为正在聊需求的 grok 坐下」在现网确实坏：`currentSeatIdentity` 不读 `GROK_SESSION_ID`。本卡修它，主语正确。
- 头部「不 reopen B156.3 / B307 / B312」与「实现时改 B312 文档」可以并存：改的是出示来源句，不是重开卡。

本审查未跑 `go test`、未跑 `codegraph` CLI（用仓内 JSON + `rg`）、未在本进程复跑 `handoff card bind --help`（活代码 `cardBindCmd` 无身份 flag 已核）。未改任何文件以外的本审查稿。
