# handoff 项目实例化清单：领域清单机械底稿

> 这是机械扫描底稿，不是领域边界或域类型的最终决定。统计基线为当前分支
> `docs/handoff-domain-inventory` 在 2026-08-21 的工作树。

## 统计口径与实际命令

- Go 范围：`internal/` 下每个含 `.go` 的直接目录作为一个包；`cmd/` 整体作为一个统计单元。
- Web 范围：`web/src` 整体作为一个统计单元；纳入 `.ts`/`.tsx`，测试文件按 `*.test.ts[x]` 与 `web/src/test/` 分开。
- 源码行数与测试行数均由 `wc -l` 得到；源文件数不含测试文件。
- Go 导出符号数：对非测试 Go 文件执行
  `grep -hE '^(type|func|const|var)[[:space:]]+[A-Z][A-Za-z0-9_]*'`，按命中行计数；
  这是“顶层关键字与导出名同一行”的机械口径，不展开括号形式的 `const`/`var` 成员。
- Web 导出符号数：对非测试 `.ts`/`.tsx` 执行
  `grep -hE '(^|[[:space:]])export([[:space:]]|\{|default)'`，按命中行计数；这是按 `export` 行计数，
  不做 TypeScript AST 展开。
- 依赖边来自 `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./internal/... ./cmd .` 的 `.Imports`；
  仅保留 `github.com/Xsxdot/handoff/internal/...`，不计测试专属导入、不计外部依赖。
- 跨包符号引用使用 `rg -o --glob '*.go' '包名\\.[A-Z][A-Za-z0-9_]*'` 后去重；它能发现常规包名限定引用，
  但会漏掉别名、间接赋值、接口实现和方法调用，也可能把同名局部变量的选择器算入候选。
- 升格家族只统计实现源文件：Go 为非 `*_test.go` 的 `.go`，Web 为非测试 `.ts`/`.tsx`；内部包只统计其直接目录，
  `cmd` 与 `web/src` 统计其目录树。前缀是 basename 在第一个 `_` 或 `-` 之前的部分；无分隔符时取完整文件名。

## 1. 包规模表

按非测试源码行数降序。`web/src` 的导出数是 `export` 命中行数，不等同于精确符号 AST 数。

| 包 | 源码行数（不含 `_test.go`） | 测试行数 | 源文件数 | 导出符号数 |
|---|---:|---:|---:|---:|
| `web/src` | 20,483 | 12,116 | 131 | 512 |
| `internal/agentd` | 19,870 | 24,026 | 61 | 62 |
| `cmd` | 7,804 | 6,789 | 46 | 8 |
| `internal/executor/opencode` | 4,385 | 5,927 | 11 | 15 |
| `internal/prochost` | 3,859 | 3,502 | 22 | 41 |
| `internal/ledger` | 3,560 | 2,327 | 16 | 23 |
| `internal/executor/grok` | 3,257 | 3,710 | 13 | 13 |
| `internal/executor/codex` | 3,059 | 2,101 | 14 | 10 |
| `internal/client` | 2,872 | 3,279 | 8 | 18 |
| `internal/executor/claudecode` | 2,585 | 2,588 | 10 | 6 |
| `internal/store` | 2,204 | 2,067 | 5 | 10 |
| `internal/proto` | 1,711 | 942 | 13 | 89 |
| `internal/service` | 1,609 | 1,535 | 6 | 9 |
| `internal/ptyhost` | 1,048 | 559 | 11 | 16 |
| `internal/permgate` | 1,043 | 716 | 6 | 14 |
| `internal/release` | 872 | 938 | 2 | 18 |
| `internal/relay` | 862 | 387 | 6 | 14 |
| `internal/executor/turn` | 815 | 719 | 8 | 19 |
| `internal/ledgerstep` | 795 | 827 | 6 | 14 |
| `internal/initflow` | 721 | 523 | 3 | 20 |
| `internal/config` | 703 | 777 | 2 | 16 |
| `internal/ptyhost/engine` | 638 | 632 | 4 | 2 |
| `internal/discipline` | 503 | 472 | 3 | 13 |
| `internal/executor` | 434 | 74 | 5 | 14 |
| `internal/ptyhost/hostproc` | 424 | 318 | 1 | 3 |
| `internal/envfile` | 414 | 434 | 3 | 11 |
| `internal/executor/fake` | 410 | 75 | 1 | 5 |
| `internal/selfupdate` | 389 | 306 | 3 | 8 |
| `internal/upgrade` | 377 | 280 | 2 | 9 |
| `internal/targetclient` | 374 | 312 | 3 | 4 |
| `internal/ledgermirror` | 318 | 160 | 1 | 5 |
| `internal/pathenv` | 256 | 227 | 1 | 2 |
| `internal/ptyhost/sessdir` | 200 | 177 | 1 | 14 |
| `internal/skill` | 185 | 210 | 2 | 5 |
| `internal/toolchain` | 167 | 213 | 1 | 4 |
| `internal/proxycfg` | 156 | 125 | 1 | 5 |
| `internal/buildinfo` | 144 | 314 | 1 | 1 |
| `internal/ptyhost/wire` | 143 | 121 | 1 | 6 |
| `internal/localsync` | 120 | 145 | 1 | 4 |
| `internal/projectid` | 114 | 52 | 1 | 2 |
| `internal/executor/rawtap` | 102 | 153 | 1 | 3 |
| `internal/logx` | 100 | 59 | 1 | 1 |
| `internal/webui` | 98 | 75 | 3 | 4 |

### 行数自洽检查

| 检查项 | `wc -l` 结果 |
|---|---:|
| `internal/` 非测试 Go 合计 | 61,896 |
| `cmd/` 非测试 Go 合计 | 7,804 |
| 本底稿范围（`internal/` + `cmd/`） | 69,700 |
| 自检命令 `find . -name "*.go" ! -name "*_test.go" -not -path "./web/*" \| xargs wc -l` | 71,942 |
| 差异 | 2,242 |
| 差异来源：`desktop/` 非测试 Go | 2,210 |
| 差异来源：根目录 `main.go` | 32 |

`web/src` 不参与上述 Go 自检命令；`desktop/` 与根目录文件也不在本任务指定覆盖范围内。

## 2. 依赖边（只算仓内边）

### 正向表

| 包 | 依赖的本仓包（逗号分隔） | 出度 |
|---|---|---:|
| `internal/agentd` | `internal/buildinfo`, `internal/client`, `internal/config`, `internal/discipline`, `internal/envfile`, `internal/executor`, `internal/executor/turn`, `internal/ledger`, `internal/ledgerstep`, `internal/permgate`, `internal/prochost`, `internal/projectid`, `internal/proto`, `internal/proxycfg`, `internal/ptyhost`, `internal/ptyhost/sessdir`, `internal/release`, `internal/selfupdate`, `internal/store`, `internal/targetclient`, `internal/upgrade`, `internal/webui` | 22 |
| `internal/buildinfo` | `internal/proto` | 1 |
| `internal/client` | `internal/proto`, `internal/relay` | 2 |
| `internal/config` | `internal/proxycfg` | 1 |
| `internal/discipline` | — | 0 |
| `internal/envfile` | — | 0 |
| `internal/executor` | `internal/proto` | 1 |
| `internal/executor/claudecode` | `internal/executor`, `internal/executor/rawtap`, `internal/executor/turn`, `internal/prochost`, `internal/proto` | 5 |
| `internal/executor/codex` | `internal/executor`, `internal/executor/rawtap`, `internal/executor/turn`, `internal/prochost`, `internal/proto` | 5 |
| `internal/executor/fake` | `internal/executor` | 1 |
| `internal/executor/grok` | `internal/executor`, `internal/executor/rawtap`, `internal/executor/turn`, `internal/prochost`, `internal/proto` | 5 |
| `internal/executor/opencode` | `internal/executor`, `internal/executor/rawtap`, `internal/executor/turn`, `internal/prochost`, `internal/proto` | 5 |
| `internal/executor/rawtap` | — | 0 |
| `internal/executor/turn` | `internal/executor`, `internal/proto` | 2 |
| `internal/initflow` | `internal/config`, `internal/toolchain` | 2 |
| `internal/ledger` | `internal/discipline` | 1 |
| `internal/ledgermirror` | `internal/client`, `internal/config`, `internal/ledger`, `internal/proto` | 4 |
| `internal/ledgerstep` | `internal/client`, `internal/ledger`, `internal/proto` | 3 |
| `internal/localsync` | — | 0 |
| `internal/logx` | — | 0 |
| `internal/pathenv` | — | 0 |
| `internal/permgate` | `internal/executor` | 1 |
| `internal/prochost` | — | 0 |
| `internal/projectid` | — | 0 |
| `internal/proto` | — | 0 |
| `internal/proxycfg` | — | 0 |
| `internal/ptyhost` | `internal/prochost`, `internal/ptyhost/sessdir`, `internal/ptyhost/wire` | 3 |
| `internal/ptyhost/engine` | `internal/ptyhost` | 1 |
| `internal/ptyhost/hostproc` | `internal/prochost`, `internal/ptyhost`, `internal/ptyhost/engine`, `internal/ptyhost/sessdir`, `internal/ptyhost/wire` | 5 |
| `internal/ptyhost/sessdir` | `internal/prochost` | 1 |
| `internal/ptyhost/wire` | — | 0 |
| `internal/relay` | — | 0 |
| `internal/release` | — | 0 |
| `internal/selfupdate` | — | 0 |
| `internal/service` | — | 0 |
| `internal/skill` | — | 0 |
| `internal/store` | `internal/projectid`, `internal/proto` | 2 |
| `internal/targetclient` | `internal/client`, `internal/config`, `internal/relay` | 3 |
| `internal/toolchain` | — | 0 |
| `internal/upgrade` | `internal/client`, `internal/proto`, `internal/release` | 3 |
| `internal/webui` | — | 0 |

### 反向表

反向表把 `cmd` 作为一个仓内消费者计入；`root (main)` 只导入 `cmd`，不直接导入 `internal` 包。

| 包 | 被哪些本仓包依赖 | 入度 |
|---|---|---:|
| `internal/agentd` | `cmd` | 1 |
| `internal/buildinfo` | `internal/agentd`, `cmd` | 2 |
| `internal/client` | `internal/agentd`, `internal/ledgermirror`, `internal/ledgerstep`, `internal/targetclient`, `internal/upgrade`, `cmd` | 6 |
| `internal/config` | `internal/agentd`, `internal/initflow`, `internal/ledgermirror`, `internal/targetclient`, `cmd` | 5 |
| `internal/discipline` | `internal/agentd`, `internal/ledger` | 2 |
| `internal/envfile` | `internal/agentd`, `cmd` | 2 |
| `internal/executor` | `internal/agentd`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/fake`, `internal/executor/grok`, `internal/executor/opencode`, `internal/executor/turn`, `internal/permgate`, `cmd` | 9 |
| `internal/executor/claudecode` | `cmd` | 1 |
| `internal/executor/codex` | `cmd` | 1 |
| `internal/executor/fake` | `cmd` | 1 |
| `internal/executor/grok` | `cmd` | 1 |
| `internal/executor/opencode` | `cmd` | 1 |
| `internal/executor/rawtap` | `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode` | 4 |
| `internal/executor/turn` | `internal/agentd`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode` | 5 |
| `internal/initflow` | `cmd` | 1 |
| `internal/ledger` | `internal/agentd`, `internal/ledgermirror`, `internal/ledgerstep`, `cmd` | 4 |
| `internal/ledgermirror` | `cmd` | 1 |
| `internal/ledgerstep` | `internal/agentd`, `cmd` | 2 |
| `internal/localsync` | `cmd` | 1 |
| `internal/logx` | `cmd` | 1 |
| `internal/pathenv` | `cmd` | 1 |
| `internal/permgate` | `internal/agentd`, `cmd` | 2 |
| `internal/prochost` | `internal/agentd`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode`, `internal/ptyhost`, `internal/ptyhost/hostproc`, `internal/ptyhost/sessdir`, `cmd` | 9 |
| `internal/projectid` | `internal/agentd`, `internal/store`, `cmd` | 3 |
| `internal/proto` | `internal/agentd`, `internal/buildinfo`, `internal/client`, `internal/executor`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode`, `internal/executor/turn`, `internal/ledgermirror`, `internal/ledgerstep`, `internal/store`, `internal/upgrade`, `cmd` | 14 |
| `internal/proxycfg` | `internal/agentd`, `internal/config`, `cmd` | 3 |
| `internal/ptyhost` | `internal/agentd`, `internal/ptyhost/engine`, `internal/ptyhost/hostproc`, `cmd` | 4 |
| `internal/ptyhost/engine` | `internal/ptyhost/hostproc` | 1 |
| `internal/ptyhost/hostproc` | `cmd` | 1 |
| `internal/ptyhost/sessdir` | `internal/agentd`, `internal/ptyhost`, `internal/ptyhost/hostproc` | 3 |
| `internal/ptyhost/wire` | `internal/ptyhost`, `internal/ptyhost/hostproc` | 2 |
| `internal/relay` | `internal/client`, `internal/targetclient`, `cmd` | 3 |
| `internal/release` | `internal/agentd`, `internal/upgrade`, `cmd` | 3 |
| `internal/selfupdate` | `internal/agentd`, `cmd` | 2 |
| `internal/service` | `cmd` | 1 |
| `internal/skill` | `cmd` | 1 |
| `internal/store` | `internal/agentd`, `cmd` | 2 |
| `internal/targetclient` | `internal/agentd`, `cmd` | 2 |
| `internal/toolchain` | `internal/initflow`, `cmd` | 2 |
| `internal/upgrade` | `internal/agentd`, `cmd` | 2 |
| `internal/webui` | `internal/agentd` | 1 |

- **入度为 0 的包：无**（按上述统计范围把 `cmd` 作为消费者计入）。

## 3. 对外暴露面

源码行数前 8 个统计单元如下。Go 符号名是跨包限定引用扫描得到的去重候选；`cmd` 的限定引用另受
局部变量同名影响，因此只保留能与其顶层导出声明对应的名字。`web/src` 不是 Go 包，单列其构建/模块消费事实。

### `web/src`（20,483 行）

- 被谁用：没有仓内 Go 包直接 import `web/src`；`internal/webui` 嵌入的是构建产物 `web/dist`，不是源码目录；前端构建链消费 `web/src`。
- 跨模块实际引用的代表性导出：`AppRoutes`, `Shell`, `cn`, `connectPty`, `connectEvents`, `wsCloseReason`, `fetchCards`, `fetchCardDetail`, `createCard`, `runCardStep`, `fetchFlows`, `fetchMachines`, `fetchTasks`, `ApiError`, `BoardPage`, `CardsPage`, `ProjectTree`, `WorkbenchPage`, `usePoll`, `useMachines`, `FileTree`, `TerminalTab`, `Task`, `Event`, `ProjectNode`, `CardView`, `NodeDef`。
- 对外承诺：提供浏览器控制台的页面、组件、状态 hooks、API 客户端与共享 TypeScript 契约类型。
- 口径限制：本条使用 `export`/相对路径 `import` 的文本事实；没有把前端目录强行解释成 Go 的包级 API。

### `internal/agentd`（19,870 行）

- 被谁用：`cmd`。
- 被跨包实际引用的导出符号：`AcquireDataDirLock`, `MainWorktreeRoot`, `MismatchScanMinAge`, `NewApprover`, `NewManager`, `NewMirror`, `NewServer`, `NewShutdown`, `RecoverOnStartup`, `RunCmdTimeout`, `RunWatchdog`, `SetGitProxy`, `SetTaskProcCounter`, `WarnIfKillModeUnsafe`。
- 对外承诺：提供 agentd 服务端、任务/项目/工作区/机器管理、监控恢复、HTTP/WebSocket 与更新相关的服务能力。

### `cmd`（7,804 行）

- 被谁用：`root (main)`。
- 被跨包实际引用的导出符号：`Execute`, `ExitCode`, `SetSkillContent`。
- 对外承诺：提供 handoff CLI 的命令注册、执行入口、退出码转换与内嵌 skill 注入入口。

### `internal/executor/opencode`（4,385 行）

- 被谁用：`cmd`。
- 被跨包实际引用的导出符号：`New`。
- 对外承诺：提供 OpenCode executor 的适配器构造入口。

### `internal/prochost`（3,859 行）

- 被谁用：`internal/agentd`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode`, `internal/ptyhost`, `internal/ptyhost/hostproc`, `internal/ptyhost/sessdir`, `cmd`。
- 被跨包实际引用的导出符号：`AcquireLock`, `Admission`, `Alive`, `CheckAdmission`, `CountGroup`, `CreateInputChannel`, `ErrExecutorAlive`, `ErrLockHeld`, `ErrNotSupported`, `ErrStillAlive`, `ExplainForkFailure`, `FallbackKind`, `Footprint`, `Handle`, `IsLocked`, `Kill`, `Lock`, `LockSupported`, `MarkCapability`, `ProcessCredential`, `ProcessCredentialForPID`, `PtyhostCredentials`, `ResolveMarkRoot`, `RunShim`, `SentinelPrefix`, `SetFencePolicy`, `SetPtyhostCredentialProvider`, `Spec`, `Start`, `Sweep`, `UIDUsage`, `VerdictOK`, `WaitInputReader`, `WriteInputChannel`。
- 对外承诺：提供宿主进程启动/存活/终止、输入通道、锁、资源足迹、准入围栏与平台凭据能力。

### `internal/ledger`（3,560 行）

- 被谁用：`internal/agentd`, `internal/ledgermirror`, `internal/ledgerstep`, `cmd`。
- 被跨包实际引用的导出符号：`AddComment`, `AllTaskLinks`, `AnswerDecision`, `AttachFile`, `Attachment`, `Card`, `CardBrief`, `CardFilter`, `CardView`, `ChildrenOf`, `ClearNeedsHuman`, `CloseShelved`, `CreateCard`, `DecisionsOf`, `DetachFile`, `DispatchSnapshot`, `EffectiveBaseBranch`, `ErrBadMerge`, `ErrBadState`, `ErrCASConflict`, `ErrCycle`, `ErrGateBlocked`, `ErrNotFound`, `EvAcceptanceRecorded`, `EvComment`, `EvNeedsHuman`, `EvReviewVerdict`, `EvStatusMoved`, `EvTaskMirrored`, `Event`, `EventsFromAsc`, `GetCard`, `GetTemplate`, `GetWorkflow`, `LatestTaskStates`, `ListCards`, `ListDecisions`, `ListTemplateNames`, `ListWorkflowNames`, `MirrorHealth`, `MirroredEvent`, `MoveCard`, `NeedsOf`, `NewCard`, `NodeDef`, `Open`, `OpenTicketCounts`, `PurposeReview`, `PutWorkflow`, `RecordAcceptance`, `RelBlocks`, `RelationsOf`, `SetAcceptance`, `StatusClosed`, `StatusDoing`, `StatusDone`, `Store`, `TaskLink`, `TemplateDef`, `UpdateCardMeta`, `WorkflowDef`。
- 对外承诺：提供卡片、事件、关系、决策、模板/工作流及 SQLite/PostgreSQL 存储访问能力。

### `internal/executor/grok`（3,257 行）

- 被谁用：`cmd`。
- 被跨包实际引用的导出符号：`New`, `SymlinkCapability`。
- 对外承诺：提供 Grok executor 适配器构造与本机能力探测入口。

### `internal/executor/codex`（3,059 行）

- 被谁用：`cmd`。
- 被跨包实际引用的导出符号：`New`, `Preflight`。
- 对外承诺：提供 Codex executor 适配器构造与执行前检查入口。

## 4. 候选大领域分组（建议，非决定）

以下是按依赖边聚类的**建议**，不是最终领域清单，也不替协调者决定逻辑域/边界域。

| 候选大领域（建议） | 包 | 依赖聚类理由 |
|---|---|---|
| 契约、卡片与持久化 | `internal/proto`, `internal/buildinfo`, `internal/projectid`, `internal/store`, `internal/ledger`, `internal/ledgerstep`, `internal/ledgermirror`, `internal/discipline` | 共享协议类型、项目标识、账本存储、卡片事件/关系与工作流推进；`ledgerstep`/`ledgermirror` 都直接落在 `ledger` 上。 |
| Executor 适配 | `internal/executor`, `internal/executor/claudecode`, `internal/executor/codex`, `internal/executor/grok`, `internal/executor/opencode`, `internal/executor/fake`, `internal/executor/rawtap`, `internal/executor/turn` | 适配器共同依赖 executor 契约、turn、rawtap 与 proto，且四个真实 provider 具有相似的进程/会话接缝。 |
| 宿主进程与 PTY | `internal/prochost`, `internal/ptyhost`, `internal/ptyhost/engine`, `internal/ptyhost/hostproc`, `internal/ptyhost/sessdir`, `internal/ptyhost/wire` | 形成从进程围栏到 PTY engine、hostproc、会话目录和 wire 的仓内依赖链，并共同面对 OS/进程现实。 |
| Agentd 控制面与远端连接 | `internal/agentd`, `internal/client`, `internal/targetclient`, `internal/config`, `internal/proxycfg`, `internal/relay`, `internal/release`, `internal/upgrade`, `internal/selfupdate`, `internal/webui` | `agentd` 是高出度汇合点，客户端/目标端/relay/config/update/webui 都参与服务编排或远端控制面。 |
| CLI、初始化与本机集成 | `cmd`, `internal/initflow`, `internal/toolchain`, `internal/localsync`, `internal/logx`, `internal/pathenv`, `internal/permgate`, `internal/envfile`, `internal/service`, `internal/skill` | `cmd` 直接汇聚这些入口、初始化、环境、权限、服务和 skill 相关包，主要由 CLI 消费。 |
| Web 控制台 | `web/src` | 前端页面/组件/hooks/API 客户端集中在此；`internal/webui` 只负责构建产物嵌入，源码与 Go 包之间没有直接 import 边。 |

### 拿不准的边界

- `internal/prochost` 既像 Executor 适配的共同边界，也像 PTY/宿主进程大领域，因为两类消费者都直接依赖它。
- `internal/ptyhost` 既像宿主进程大领域，也像 `agentd` 控制面的一部分，因为 `agentd` 直接调用 PTY 能力。
- `internal/ledgerstep` 既像账本/工作流的一部分，也像 `agentd` 调配层的一部分，因为它同时依赖 `ledger`、`client` 与 `proto`。
- `internal/config` 既像控制面配置，也像 CLI/初始化的一部分，因为它被 `agentd`、`targetclient`、`initflow` 与 `cmd` 使用。
- `internal/permgate` 既像本机安全边界，也像 CLI/控制面基础设施，因为它同时被 `agentd` 与 `cmd` 使用。
- `internal/proto` 既像独立契约域，也像账本、Executor、agentd 与 Web 之间的共享契约层，因为其入度为 14 且自身无仓内依赖。
- `web/src` 既像独立前端域，也像 `internal/agentd`/`internal/proto` 的表现层，因为前端 API 类型与 agentd HTTP 端点的对应关系不在 Go import 图中体现。

## 5. 两条红线的命中情况

### 域尺寸红线：源码行数 > 20,000

| 包/统计单元 | 非测试源码行数 | 命中 |
|---|---:|---|
| `web/src` | 20,483 | 是（按本任务覆盖单元计） |

- `internal/agentd` 为 19,870 行，严格按 `> 20,000` 不命中。
- 其余本任务覆盖单元也未超过 20,000 行。

### 升格判据：文件名前缀家族 ≥ 5 个实现源文件

| 包 | 前缀 | 文件数 | 文件列表 |
|---|---|---:|---|
| `cmd` | `card` | 5 | `card_dispatch.go`, `card_minb.go`, `card_records.go`, `card_node.go`, `card_wait.go` |

- 本表明确排除测试文件；因此 `cmd/card_*_test.go` 不会把家族重复计入。
- 前缀取 basename 在第一个 `_` 或 `-` 之前；本表中的 `card_dispatch.go`、`card_minb.go`、`card_records.go`、`card_node.go`、`card_wait.go` 均归入 `card`。
- 对所有 `internal` 直接包目录、`cmd` 目录树和 `web/src` 目录树执行同一规则后，除 `cmd/card` 外没有达到 5 个实现源文件的家族。
