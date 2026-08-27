# B276 spec 审查（B245 / B211 / B256 / B259 / B258 / B261）

审查对象：`docs/superpowers/specs/b276.md`（待用户批准稿）  
对照代码：工作树 `fix/silent-wrong` @ `8ddb060ae`（与 main 同提交，spec-only）  
源卡：B245 / B211 / B256 / B259 / B258 / B261 并入 B276  
用户承重修订：**B259 按「删除 `handoff graph` 命令」审，不按 spec 正文「改 Short」审。**

## Summary

六条里 B245、B256、B211、B261 方向 1 的根因核对成立，推荐改法对准各自的真实缺口（探活失败冒充升级；夹具把 `/tmp` 仓内文件当稳定路径；stub 只打 INFO；写判据不问在飞 task）。B258 是消费侧止损：不拆 HTTP 状态码，靠 skill 改判据，能关掉「见 404 就跳过」这条已记录误操作，但关不掉「工单尚未入库」的竞态（spec 自己也放进 Out of Scope）。

主导残留风险是 **B259**。源 spec 仍写「改 Short、声明子命令仍可用」；用户已改判为直接删命令。按当前正文落地会做错题。按删除落地，又没写执行机查图的替代入口——执行机普遍没有 `codegraph` 二进制，今天靠 `handoff graph` / `go run . graph`；删掉之后平台不变量还在把 `handoff graph` 当唯一合法例外，charter skill 则教 `codegraph` 二进制。这会把「被 deprecated 文案带去找二进制」换成「命令 404 + 纪律还在教这条命令」或「入口删了、执行者无替代」。这是本卡最大的新问题，不写进 spec 不能批。

B245/B256/B211/B261 是治本（各自通道上的错误归因或沉默降级被关掉）。B258 是止损。B259 在用户修订下本可治本，但正文还停在被否决的文案方案。

## Per-bug

### B245 — 治本（三处调用点确实共用一句升级文案；探活失败必须在进闸前分流）

根因核对成立。拒发文案唯一出口是 `internal/discipline/dispatch.go:26` `ErrUnsupportedTarget`（「请先把目标机升级到同批版本再派发」）。`ResolveDispatch`（同文件 `65-68`）在 `targetCap == nil || !*targetCap` 时无条件返回它，**不接收探活错误**。三个生产调用点都是「失败则 cap 保持 nil → 进闸」：

- `cmd/dispatch.go:192-199` `resolveBareDiscipline`：`cli.Status` 失败只 `slog.Warn`，`cap` 仍 nil。
- `cmd/card_dispatch.go:207-221` `resolveCardDispatchDiscipline`：`targetClient` 失败或随后 `Status` 失败同样 cap=nil。
- `internal/agentd/cardstep.go:144-166` `resolveStepDiscipline`：`pool.For` 失败或 `Status` 失败同样 cap=nil；成功路径才 `cap = status.DisciplinesSupported`。

`cardstep.go:137-142` 目标机未定时返回空产物、不进闸，不是这句错话的来源。生产侧 `ResolveDispatch` 调用方只有这三处（其余命中均在测试）。只改裸 `dispatch` 会漏 `card dispatch` 与 `--step`，spec 弃选写对了。

fail-closed 必须保留：探活失败仍拒发，只是不得再说「升级」。`Status` 成功但能力位缺席/false 仍走 `ErrUnsupportedTarget`——存量 `TestBareDispatchRefusesUnsupportedTarget`（`cmd/dispatch_discipline_test.go:136-159`，夹具是 HTTP 200 + `{}` / `false`）和 `TestStartCardStepRejectsUnsupportedTarget`（`internal/agentd/cardstep_discipline_test.go:143-176`）锁的就是这条，**不是**探活失败。接缝 1 把两条分开是对的；禁止共用夹具也是对的。

允许三处各自 `fmt.Errorf`：故事 3 要求「说同样的话」，接缝 1 已点名三处符号并断言含 cause、不含「升级到同批版本」。只要测试真打到三处生产函数，不必抽 helper。

### B211 — 治本（stub 只在启动 INFO；status 人读零标记；指针+omitempty 能把 false 送进 JSON）

根因核对成立。`internal/webui/stub.go:44` 默认构建 `Embedded()==false`；`embed.go` 在 `-tags embedweb` 下为 true。启动日志只有 `internal/agentd/server.go:568` `Info("控制台前端", "embedded", webui.Embedded())`。`cmd/status.go#renderStatusWithLookup`（`126-187`）没有这个位。`proto.StatusResp`（`internal/proto/status.go:155-250`）也没有。

能力位填充点在 `handleStatus`（`server.go:694-714`），不在 `Manager.Status`：`PtySupported` / `LaunchersSupported` / `DisciplinesSupported` / `RevealSupported` 都是 handle 层补的。新字段应落在这里，与 `webui.Embedded()` 同层，不要让 Manager 去 import webui。

**三态与 omitempty 不打架。** `encoding/json` 对指针的 omitempty 只省略 **nil**；非 nil 的 `*bool=false` 会编码成 `"k":false`。这与 `PtySupported`（`status.go:193-200`）同一纪律：缺席=对端太老未上报，新 CLI 不得画成 stub；`false` 必须出现在 `--json` 里，人读只在 false 时打一行。若实现成值类型 `bool`+omitempty，false 会被当成空值省略，故事 4 在 JSON 侧静默失败。spec 写了指针，接缝 2 却只锁人读行，没锁「false 时 JSON 键在、nil 时键不在」——见 Issue 4。

字段的 JSON 名 spec 没取（邻居是 `pty_supported` 这种 snake_case）。`--json` 是用户可见通道，名字是产品决定，不能留给 plan 现编。

### B256 — 治本（源卡归因纠正成立；`/tmp` 规则是生产正确行为）

根因核对成立，源卡「macOS 风格 go-build 路径 Linux 不识别」不成立。

`isEphemeralBin`（`cmd/service.go:342-369`）两类都认：路径分量 `HasPrefix("go-build")`，或落在 `os.TempDir()` / `/tmp` / `/var/tmp` 前缀下。`TestResolveServiceBinFallsBackFromGoBuildCache`（`cmd/service_test.go:273-291`）当前 exe 是 `/Users/x/Library/Caches/go-build/...`——Linux 上 **`go-build` 分量同样命中**，exe 会被判临时，回退分支会走。回退候选是 `filepath.Abs("service.go")`。`go test ./cmd` 的 cwd 是 `cmd/`，绝对路径落在仓内 `cmd/service.go`。linux-01 全量在 `/tmp/hbfin` 时，该路径带 `/tmp/` 前缀，被 `isEphemeralBin` 跳过（`398`），函数返回 `407` 那句「当前二进制是 go run / 编译缓存里的临时文件（/Users/x/Library/Caches/go-build/...）」——正是测试 `282` 行吃到的错误。darwin 本机仓不在 `/tmp`，所以绿。

推荐「夹具自己造稳定候选、不动 `/tmp` 生产规则、禁止 Skip」对准根因。`TestIsEphemeralBin`（`253-269`）已有 macOS / `/tmp/go-build*` / `~/.cache/go-build` 三条 true，保持跨平台即可。

夹具例子写「`$HOME` 下本测试专属文件」不稳：`isEphemeralBin` 认的是路径前缀，不认「是不是测试文件」。容器里 `HOME=/tmp` 时，`$HOME/x` 仍会被跳过，事故现场再次变红。接缝 3 应先断言候选 `!isEphemeralBin`，再测回退；失败是 Fatal，不是 Skip。见 Issue 5。

`resolveServiceBinFrom` 还要求 `regularFileExists`（`401-403`），候选必须是普通文件，不能只造一个目录。

### B259 — 按删除命令审（spec 正文仍是被否决的改文案；删了必须给替代入口）

现状读数成立。`cmd/graph.go:17-20`：`graphcli.New("graph")`，Short 前缀 `[deprecated：请改用 codegraph 二进制]`，`rootCmd.AddCommand`。cobra `Deprecated` 字段故意未用。执行机 PATH 上没有 `codegraph` 是普遍事实（`docs/roadmap.md:371-376` B227；charter 刀 0 分发设计把 `handoff graph` 当作「无 Go 有 handoff」通道）。仓内 `go run . graph` 能用，是因为别名还挂在 `main` 的命令树上。

**用户已改判：直接删命令，不是改文案。** spec 方案段（`b276.md:87-95`）、故事 7（`140`）、接缝 4（`149`）、Out of Scope「纪律块/CLAUDE.md 补 `go run . graph` fallback」（`171`）全部还停在旧方案。按字面实现会把用户否决的文案方案做进去。这是 Critical（Issue 1）。

按删除评估，生产入口（不是历史 ledger / 过期 plan）至少包括：

| 入口 | 现状 | 删了之后若不改 |
|---|---|---|
| `cmd/graph.go` | 挂载别名 | 命令 404 |
| `internal/permgate/selfcmd.go:36-38` `selfCmdNestedReadOnly["graph"]={"resolve":true}` | `handoff graph resolve` 不当自指令 | 死命令仍被放行，执行者以为合法 |
| `internal/permgate/selfcmd_test.go:29-30`、`permgate_test.go:57-58,70-76` | 锁白名单与 fail-closed | 夹具仍教 `handoff graph …` |
| `internal/discipline/platform.go:12` | 平台不变量：**每轮派发注入**，「不要调用 handoff CLI（只读本地图数据的 **handoff graph** 子命令除外）」 | 执行者按铁律去跑 `handoff graph`，cobra unknown command |
| 仓内 `skills/handoff/SKILL.md` | **零处** `handoff graph` / `go run . graph` | 不是这条的真源 |
| charter skill（`charter/skills/{spec,plan,contract,breakdown,recon}`） | 刀 0 已改成 `codegraph …`（`charter/docs/ledgers/2026-08-22-codegraph-extraction-ledger.md:25-26`） | 执行机没有该二进制 → `command not found`，recon 空转（B227 已发生过） |

`TestRepoContractGate`（`cmd/graph_gate_test.go`）走 `codegraph` 库 API，不依赖 CLI 别名，删 `cmd/graph.go` 不会让契约闸挂掉。

**删掉之后查图走哪条路？** spec 没写。执行机有 Go（能 `go test`），没有 `codegraph` 二进制。今天的活路径是 `handoff graph` 与 `go run . graph`（后者只是前者的源码形态）。两者同归于 `cmd/graph.go`。删了之后：

1. `handoff graph` / `go run . graph` → unknown command；
2. charter / AGENTS「有图先查图」→ `codegraph context/sym/…` → PATH 上没有；
3. 平台不变量仍把 `handoff graph` 当唯一例外。

这正是用户点名的新问题：不是「改文案」，是「命令 404、纪律还在教」或「入口删了、无替代」。替代必须写死，且不能是「给各执行机装 codegraph」（永不做）或「补 `go run . graph` fallback」（命令已不存在，这条 Out of Scope 随删除一并作废）。

可行替代（有 Go 的执行机）：`go run github.com/Xsxdot/charter/graph/cmd/codegraph`（handoff `go.mod:7` 已钉 `charter/graph v0.9.0`，与 canonical 二进制同一构造，`charter/graph/cmd/codegraph/main.go` + `graph/cli.New`）。那不是 handoff CLI，平台不变量里的 graph 例外应删掉，改成「查图走 charter/graph 的 `go run` 入口 / 已安装的 `codegraph`，未命中再 grep」。`go run . graph` 不得再出现在活入口。见 Issue 2。

刀 0 契约 `charter/docs/contracts/2026-08-22-codegraph-extraction-contract.md` §4 冻结了「`handoff graph <args>` 与 `codegraph <args>` 同版本等价」；charter `docs/roadmap.md:21` 写明「别名移除时点：deprecated 观察期后另行裁决」。本卡删除就是那次裁决。spec 必须显式宣告，否则实现者会以为只动局部 Short。不因此把整卡抬到 L3——见定级。

活模板 `docs/codegraph-scan-recipe.md:7,98,391` 仍写 `handoff graph validate`，是以后还会派发的扫描 plan，不是过期 ledger。删除命令后应改入口；不挡批准，但 plan 不能漏。

### B258 — 止损（skill 真源路径对；只改 skill 够关掉「见 404 就跳过」，关不掉未注册竞态）

根因核对成立。`handleReply`（`internal/agentd/server.go:993-1013`）三种成因同一 `404` + `{"error":"工单不存在"}`：`GetTicket` `ErrNotFound`（库里没有）、`tk.TaskID != taskID`（不属于该任务）、`AnswerTicket` `ErrNotFound`（已被回答）。协调者从报文分不出。

skill 真源是 `skills/handoff/SKILL.md`，`main.go:19` `//go:embed`，`handoff skill install` 同步。路径对。无前提的「404 即跳过」至少三处：`178`（cursor：历史 question 补 reply 404，跳过即可）、`254`（`stale`：补 reply 会 404，跳过即可）、`596`（排障表：历史 ticket 补 reply 404 也正常，跳过）。改这三处、改成「先 `show --target` 看 `pending_tickets`」对准已记录的误操作。

`pending_tickets` 与 `reply` 读同一 agentd 工单库（`cmd/show.go` 注释；`TestPermissionImmediateVisible` 锁「事件到达瞬间 pending 已可见」）。权威在任务所在机器上。spec `106` 写了 `handoff show <task> --target <机器>`。若实现把 skill 改成裸 `show` 不带 `--target`，命令打到本机，pending 空，仍然跳过，远程执行者照旧挂到看门狗——和今天「按 404 跳过」同一个结局。接缝 5 必须锁 `--target`。见 Issue 6。

只改 skill、不拆状态码：**不足以**覆盖「工单还没注册」这一成因（`GetTicket` 尚未写入时 show 也是空）。P1-2 已把 permission 的「先 Publish 后建单」收掉，这条竞态变窄，但 question 路径与错 task-id 仍可能 404。spec 把分码放进 Out of Scope 是诚实的。作为止损可接受——前提是 `--target` + pending 重发写进三处正文。故事 8 只覆盖「还在 pending 就重发」，没说「不在 pending 且任务仍 `waiting_answer` 要再 show 一次」；那是分码卡的活，本卡不要求。

并列 404 不一定都要改：`279`（同一 ticket 答两次）、`592`（resume 后 attach 已无挂起项）、`622`（502 后再 reply）是「已消耗」的正确推论，不是「见 404 就跳过」的捷径。接缝不要误伤这些。

### B261 方向 1 — 治本（出声；不注入本轮）。在飞判定与现有镜像终态一致，误报方向对

根因核对成立。`internal/ledgerstep/dispatch.go:165` 派发瞬间读 `c.AcceptanceCriteria` 写进 prompt。`continue` 不重读卡（Out of Scope 方向 2 已承认）。`SetAcceptance`（`internal/ledger/cards.go:566-578`）只 UPDATE 字段 + comment `"更新验收判据"`，不问挂账 task。

生产写判据只有两处：CLI `cmd/card.go:179-182`（`openLedger()` **直写本地 sqlite，不经 HTTP**）和 `handleCardPatch`（`internal/agentd/ledgerapi.go:849-856`）。Web 抽屉 `CardDrawer.tsx:467` 走 PATCH，成功后 `load()` 会刷新 timeline。spec 点名两处是对的；若只改 handle，协调者主路径 `card update --accept` 仍然静默。见 Issue 7。

在飞判定用 `LatestTaskStates.LastType` 与现网镜像终态一致，不是新发明：

- `TaskStateRow` 注释（`internal/ledger/taskstate.go:13`）：`LastType` 空 = 尚无镜像（未知）。
- `mirrorTaskTerminal`（`21-23`）：只有 `archived` / `failed` 是终态；注释写明 `completed` / `turn_failed` 进 `waiting_review`，还会再来事件。
- `LiveMirrorTargets`（`27-28,72-74`）：从未镜像过，或末条不是 archived/failed → 在飞。
- `TestLiveMirrorTargets` / `TestCardStepInFlightInFlightUntilTerminal`（`taskstate_test.go:36-57,159-185`）已锁：空镜像、`completed`、`turn_failed` 在飞；`archived`/`failed` 收口。

`LastType` 来自镜像 payload 的 `task_type`（`AppendMirroredEvent` 把原事件 Type 打进 `{"task_type":...}`，`LatestTaskStates:100-106` 再读出来），不是账本事件名 `task_mirrored`。spec 用 `completed` 当在飞样本是对的。

误报：镜像滞后（远端已 `done`，账本末条还是 `completed`）会警告。spec「空 LastType 宁可误报」同向，可接受。漏报：挂账 task 已 `archived`/`failed` 后再改判据——本来就该无警告，下一轮 `--step` 会读到新文本。多 task 只挂一条 `archived`、另一条空/`completed` 时必须警告；接缝 6 只写「只挂 archived 时没有」不够锁「一归档一在飞」——实现按 `LatestTaskStates` 过滤即可，建议接缝补一条。

警告文案「从下一轮 `card dispatch --step` 生效」与 continue 不重读一致。HTTP 200 保留对：这是警告不是拒绝。handle 今天回 `{"ok":true}`（`865`）；出声通道是 stderr（CLI）+ 卡评论。Web 没有 stderr，靠评论 + `load()`。不要只改 HTTP body 指望 CLI 用户看见。

把查询放进 `SetAcceptance` 比两处调用方各写一遍更不容易漏；接缝仍应真打 CLI 与 PATCH，不能只测 store。

## Issues

### Issue 1 -- Severity: Critical

- File: `docs/superpowers/specs/b276.md:87-95,140,149,171`；活代码 `cmd/graph.go:17-20`
- Description: 用户已改判 B259 为**删除 `handoff graph` 命令及其代码**。spec 方案、故事 7、接缝 4、Out of Scope 仍是「改 Short，禁止点名 codegraph 二进制，必须写仍可用」。按字面实现会把否决方案做进去，删除面（`cmd/graph.go`、permgate 白名单与测试、平台不变量）一处不动。
- Suggestion: 整段 B259 改成删除。删除面写进方案：`cmd/graph.go` 整文件（`graphcli.New("graph")` + `AddCommand`）；`selfCmdNestedReadOnly["graph"]` 及 `selfcmd_test.go` / `permgate_test.go` 里 `handoff graph ...` 用例（删命令后 `handoff graph resolve` 应 fail-closed，不得再当只读白名单）；平台不变量去掉「handoff graph 除外」。接缝 4 改为：根命令无 `graph` 子命令；`handoff graph --help` / `go run . graph` 为 unknown command；Short 测试作废。历史 ledger / 过期 plan 不要求改。Out of Scope「补 `go run . graph` fallback」删除——命令没了，这条不成立。
- Status: open

### Issue 2 -- Severity: Critical

- File: 用户承重问题（删了之后执行机查图走哪条）；`internal/discipline/platform.go:12`；charter skill 已改 `codegraph …`（`charter/skills/recon/SKILL.md:22`、`spec/SKILL.md:25`）；`go.mod:7` `github.com/Xsxdot/charter/graph v0.9.0`；canonical 入口 `charter/graph/cmd/codegraph/main.go`
- Description: 执行机普遍没有 `codegraph` 二进制，以前靠 `handoff graph` / `go run . graph`。删除后若只清命令、不给替代：平台不变量仍教 `handoff graph`（404）；charter/AGENTS 教 `codegraph`（command not found，B227 空转重演）。这比 deprecated 文案更坏——连能用的别名也没了。永不做「强制安装 codegraph」仍然成立。
- Suggestion: 方案写死替代入口（有 Go 的执行机）：`go run github.com/Xsxdot/charter/graph/cmd/codegraph --repo . <子命令>`（与别名同一 `graph/cli` 构造，吃 go.mod 钉版）。平台不变量改为禁止手调 handoff CLI，查图走上述入口或已安装的 `codegraph`，未命中再 grep。接缝加：组装后的平台层正文不含「handoff graph」作为执行入口；含替代 `go run github.com/Xsxdot/charter/graph/cmd/codegraph`（或 spec 选定的等价命令）。仓内 `skills/handoff/SKILL.md` 本就没有 graph 句，不必为它编一段。charter skill 已用 `codegraph` 二进制名，本卡不改 charter 仓，但 handoff 平台层必须把「没有二进制时怎么跑」说清，否则 recon 节点执行者仍会去找 PATH。
- Status: open

### Issue 3 -- Severity: Important

- File: `docs/superpowers/specs/b276.md:11-15`（L2）；刀 0 契约 `charter/docs/contracts/2026-08-22-codegraph-extraction-contract.md` §4；charter `docs/roadmap.md:21`
- Description: L2 对 B245/B256/B258/B261/B211 站得住：局部文案、夹具、skill、加 omitempty 诊断字段，不改任务/工单状态机。B211 的 `StatusResp` 新字段是既有 HTTP JSON 的加性键，老客户端忽略，不值得为它付 L3 冻结。B259 **删除别名**是刀 0 冻结条款「别名与 canonical 同版本等价」的**另行裁决**（roadmap 原文就是等这次裁决）。不写进 spec，L2 看起来像把跨仓 CLI 契约当局部 Short 来修；写明「本卡即该裁决、别名不再提供」后，仍不必抬 L3——没有新 wire 要冻，只是撤掉一条已 deprecated 的委托。
- Suggestion: 定级理由补一句：B259 删除是 charter 刀 0 §4 别名移除的用户裁决；本仓拆挂载，不在本卡冻新契约。charter roadmap 第 6 条留给 charter 仓销账，不挡本卡批准。
- Status: open

### Issue 4 -- Severity: Important

- File: `docs/superpowers/specs/b276.md:66-69,147`；填充点 `internal/agentd/server.go:673-716`；人读 `cmd/status.go:126-162`；三态样板 `internal/proto/status.go:193-200`；契约样本 `internal/proto/contract_fixture_test.go:476-499`
- Description: spec 没给 JSON 字段名。`--json` 是用户可见通道。接缝 2 只锁人读「false 打 stub 行 / true 与缺席不打」，不锁线格式。值类型 `bool`+omitempty 会把 false 省略，看起来像老对端。`statusSample` 今天不填新键，omitempty 下金样本不变红，锁不住新字段。
- Suggestion: spec 定 JSON 名（建议 `web_embedded`，与 `webui.Embedded` 对齐）。接缝 2 加 marshal：`false` → 键存在且为 false；`true` → 键存在且为 true；指针 nil → 键缺席。人读只在 false。填充点写 `handleStatus`（与 PtySupported 同层），并要有一条 GET `/api/status`（或现成 status 测试）看到该键，禁止只测 `renderStatusWithLookup` 的构造体。契约样本补一个非 nil 值（false 更能锁住「不要省略」）。
- Status: open

### Issue 5 -- Severity: Important

- File: `docs/superpowers/specs/b276.md:81,148`；判定 `cmd/service.go:353-367`；现夹具 `cmd/service_test.go:273-291`
- Description: 「例如 `$HOME` 下本测试专属文件」在 `HOME` 落在 `/tmp`（部分 CI/容器）时，候选再次被 `isEphemeralBin` 跳过，事故现场测试又红。`t.TempDir` spec 已禁止，但 `$HOME` 不是「非 tmp」的充分条件。
- Suggestion: 接缝 3 写死：构造候选后先 `!isEphemeralBin(path) && regularFileExists(path)`，不满足则 Fatal（说明为什么路径仍算临时），禁止 Skip。回退成功用例：当前路径含 `go-build`、候选稳定 → 返回候选。跳过用例：候选在 `/tmp`（或 `os.TempDir()`）下必须被跳过。测完删除文件。
- Status: open

### Issue 6 -- Severity: Important

- File: `docs/superpowers/specs/b276.md:104-109,150`；skill `skills/handoff/SKILL.md:178,254,596`；错误归因先例 skill `594`（漏 `--target` 打到本机）
- Description: 权威 `pending_tickets` 在**任务所在机器**上。三处若改成裸 `handoff show <task>`，本机空表会被当成「已消耗/从未存在」而跳过，远程执行者挂死——和今天按 404 跳过同构。spec 正文有 `--target`，接缝 5 没有锁它。
- Suggestion: 接缝 5 断言三处：404 之后的判据是 `handoff show <task> --target <机器>` 的 `pending_tickets`；工单还在 → 重发 reply；不在 → 才跳过。`stale` 段不得让读者先看到「404 跳过」。不要改 `279`/`592`/`622` 那些已消耗的正确推论。
- Status: open

### Issue 7 -- Severity: Important

- File: `docs/superpowers/specs/b276.md:125-128,151`；CLI `cmd/card.go:179-182`；HTTP `internal/agentd/ledgerapi.go:849-865`；`SetAcceptance` `internal/ledger/cards.go:566-578`
- Description: CLI `card update --accept` 直写本地账本，不经 PATCH。只改 `handleCardPatch` 会漏故事 9 的主路径。接缝 6 写了「响应/stderr/卡事件」，容易被做成只测 HTTP。`SetAcceptance` 是两处共同写点，查 `LatestTaskStates` 放这里最不容易漏。
- Suggestion: 方案允许收口在 `SetAcceptance`（写入后查、有在飞则评论带 task-id 与「本轮无效」）。接缝 6 必须真跑 CLI `card update --accept` 看 stderr+卡事件，以及 PATCH 看卡事件；HTTP 可保持 `{"ok":true}`。补一条：同卡一条 `archived` + 一条空/`completed` → 仍警告并列出在飞那条。
- Status: open

### Issue 8 -- Severity: Minor

- File: `docs/codegraph-scan-recipe.md:7,98,391`（活扫描 plan 模板）；`docs/superpowers/specs/b276.md` 未列
- Description: 扫描配方仍把 `handoff graph validate` 当收尾命令。删除别名后，以后派出去的扫描 plan 会 404。不是过期 ledger，是活模板。不挡批准。
- Suggestion: plan/实现把配方里的执行入口改成与 B259 同一替代命令。历史 ledgers 不动。
- Status: open

### Issue 9 -- Severity: Minor

- File: `cmd/dispatch_discipline_test.go:136-159`；`internal/agentd/cardstep_discipline_test.go:143-176`
- Description: 存量测试断言拒发文案含「升级」，夹具是 Status **成功** + 能力位缺席/false。探活失败若被误接到同一套测试上，会把「仍应升级」和「不得升级」搅在一起。spec 接缝 1 已禁止共用夹具；点名这两条存量测试保持「成功探活 + 缺席 → 仍含升级」即可。
- Suggestion: 实现时给 Status 返回 error（或假目标机关闭）各加一条；不要改上述两条的「升级」断言。
- Status: open

## Defect-family answers

- **生命周期 / 状态机中断**：B245 探活失败仍拒发，不 fail-open，不改任务状态机。B261 HTTP 200 + 评论，不 409，不打断在飞轮次。B258 不改工单状态码。B259 删的是只读 CLI 别名，不碰任务。无新的中途挂死路径；B259 的风险是执行者查图 404，属工具入口，不是状态机。
- **静默失败 / 误导报错**：B245 正是误导报错（网络说成升级），改法对准。B211 把沉默的 stub 变成 WARN + status 行。B261 把沉默的判据写入变成警告。B258 若漏 `--target`，会从一种静默跳过换成另一种（Issue 6）。B259 按原文改 Short 会留下「去找二进制」；按删除不写替代，会换成命令 404（Issue 1–2）。
- **假红 / 假绿测试**：B256 `$HOME` 在 `/tmp` 下会假红（Issue 5）。B211 只测人读、不 marshal，值类型 bool 会假绿（Issue 4）。B245 用能力位缺席夹具冒充探活失败会假绿。B259 接缝 4 若仍锁 Short「仍可用」，删除实现必红——那是好事，前提是先改接缝。B261 只测 PATCH 会让 CLI 路径假绿。
- **序列化边界**：B211 `*bool` + omitempty：nil 省略，`false` 必须出现。与 PtySupported 同。契约样本不填新键则金文件不锁它（Issue 4）。B261 不改 JSON 契约。B258 不改 404 体。
- **门禁绕过**：B259 删命令后，若不拆 `selfCmdNestedReadOnly["graph"]`，`handoff graph resolve` 仍不当自指令，执行者会发出一条必失败的「合法」命令。应改为未知子命令 fail-closed。平台不变量是派发期铁律，不是 permgate；两处都要改。

## Spec approval recommendation

**修订后再批。**

B245 / B256 / B211 / B258 / B261 方向可以对着当前源码落地；B256 对源卡归因的纠正是对的。不能批的原因是 B259：正文还在写被用户否决的「改文案」，且即使用户修订的「删除命令」替换进去，替代查图入口也还是空的。批准前必须补进 spec 正文（不是代码）的最小改动：

1. **B259 改成删除命令**（Issue 1）：删除面含 `cmd/graph.go`、permgate 白名单与测试、平台不变量例外；接缝 4 改为命令不存在；作废「补 `go run . graph`」。
2. **B259 写死执行机查图替代入口**（Issue 2）：`go run github.com/Xsxdot/charter/graph/cmd/codegraph`（或等价、且不依赖 PATH 上的 `codegraph` 二进制）；平台层组装产物不得再把 `handoff graph` 当执行入口。
3. **B211 JSON 名 + false 必须出现在线上**（Issue 4）。
4. **B256 候选先断言非 ephemeral**（Issue 5）。
5. **B258 skill 判据带 `--target`**（Issue 6）。
6. **B261 接缝真打 CLI，且一归档一在飞仍警告**（Issue 7）。

Issue 3（L2 下宣告刀 0 别名裁决）写进定级理由即可，不挡实现。Issue 8、9 可在 plan 消化。
