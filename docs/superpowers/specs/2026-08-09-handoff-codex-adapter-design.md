# codex adapter 设计（B28）

> 目标：让 `handoff dispatch --executor codex` 与 opencode / claude / grok 对等——五动作
> 全链路可用、分级审批链原样生效、`handoff attach` 看到同形态的终端实况、agentd 重启后
> 能恢复运行态。
>
> 来源：2026-08-09 用户需求「我想把 codex 也加上」；backlog B28。

## 1. 前置 spike（已完成，2026-08-09）

环境：`codex-cli 0.144.1`（macOS 26.5.2 arm64），登录态 `auth_mode = chatgpt`（Plus）。
spike 用一个零依赖的 Python stdio JSON-RPC 客户端驱动 `codex app-server`，跑了两轮真实回合。

### 1.1 已实证的结论

| 假设 | 结论 | 证据 |
|------|------|------|
| 有程序化审批挂载点 | ✅ ServerRequest `item/commandExecution/requestApproval`（server→client 阻塞式请求） | 报文见 §1.2 |
| 权限请求带稳定 id | ✅ `itemId`（形如 `exec-bd1deb0d-…`），与同回合 `item/commandExecution` 事件的 id 一致 | 报文比对 |
| 权限描述可读且不截断 | ✅ `command` 为完整命令行全文，另有 `cwd` 与结构化 `commandActions` | 报文 |
| 应答语义能对上 handoff | ✅ `availableDecisions: ["accept", {acceptWithExecpolicyAmendment…}, "cancel"]` | 报文 |
| 会话可恢复 | ✅ `thread/resume{threadId}`；threadId **等于** sessionId，rollout 落 `<home>/sessions/**.jsonl` | `thread/start` 回执 |
| 有非 stdio 传输 | ✅ `codex app-server --listen ws://IP:PORT`（另支持 `unix://`） | `--help` |
| 任务级配置可协议级下发 | ✅ `thread/start` 直收 `cwd`/`sandbox`/`approvalPolicy`/`approvalsReviewer`/`model`/`baseInstructions`/`developerInstructions`/`config` | `thread/start` 回执按传入值生效 |
| codex 自带的模型审批者可被关掉 | ✅ `approvalsReviewer: "user"` 把裁决路由回客户端（默认值就是 `user`；用户真实配置是 `auto_review`） | 回执 `approvalsReviewer: "user"` |
| 任务级 `CODEX_HOME` 能隔离用户全局指令 | ✅ `instructionSources` 从 `["~/.codex/AGENTS.md"]` 变成 `[]`，hooks 零触发、MCP server 零启动 | 两轮对比 |
| 任务级 home 能隔离 codex 内置插件/skills | ❌ **不能**，见 §1.3 | 任务 home 里 `plugins/` 28MB、`skills/` 472K |
| `turn/start` 是同步的 | ❌ **异步**：立即返回 `{turn:{status:"inProgress"}}`，回合终态在 `turn/completed` 通知里 | 首轮据此改写泵循环 |
| workspace-write 沙箱会拦 `/tmp` | ❌ **不拦**：`excludeSlashTmp:false`、`excludeTmpdirEnvVar:false` 是默认值 | §1.4 |
| 沙箱 `networkAccess=false` 会影响 codex 自身调模型 | ❌ 不影响，回合正常完成（沙箱只约束模型生成的 shell 命令） | 第二轮全程 `networkAccess:false` |
| 任务级 home 的 `auth.json` 软链会被顶掉（B26 同型） | ⚠️ **本轮未复现，但不构成证伪**，见 §1.5 | 软链存活、真实凭据 mtime 未变 |

### 1.2 权限请求实测报文

`approvalPolicy: "untrusted"` 下，handoff 的 permission 事件即由它翻译：

```json
{"jsonrpc":"2.0","id":0,"method":"item/commandExecution/requestApproval","params":{
  "threadId":"019fe6d6-5f1c-7f83-b516-dd7db8ddd061",
  "turnId":"019fe6d6-5f6a-7b50-905e-17ce236cc5b8",
  "itemId":"exec-bd1deb0d-3dfe-4a27-827d-78ad6413a6c2",
  "startedAtMs":1786284253475,
  "environmentId":"local",
  "command":"/bin/zsh -lc \"echo hello-from-codex > probe.txt && sed -n '1p' probe.txt\"",
  "cwd":"…/scratchpad/repo",
  "commandActions":[{"type":"unknown","command":"echo hello-from-codex > probe.txt && sed -n '1p' probe.txt"}],
  "proposedExecpolicyAmendment":["/bin/zsh","-lc","echo hello-from-codex > probe.txt && sed -n '1p' probe.txt"],
  "availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{…}},"cancel"]}}
```

两点对 handoff 直接有用：

- **`command` 是全文**，不像 B6 时代的 opencode 那样先截断再交安全门——黑名单与廉价模型
  审批者拿到的就是完整命令，`TruncationMarker` 只在超 64KB 防失控上限时才需要。
- **`commandActions` 是结构化的**（`{"type":"read","path":"…"}` / `{"type":"unknown",…}`）。
  claude 与 grok 那边的路径判据（B27）是对展示文本做正则，codex 这里 executor 自己就给出了
  动作类型与路径。**本期不改判据**（见 §9 非目标），但登记为后续可利用的更可靠输入。

### 1.3 任务级 home 隔离掉了什么、没隔离掉什么

对比两轮 `thread/start` 回执与任务 home 落盘：

| 项 | 真实 `~/.codex` | 任务级 `CODEX_HOME` |
|----|------------------|----------------------|
| 用户 `AGENTS.md` | `instructionSources: ["~/.codex/AGENTS.md"]` | `[]`（已隔离） |
| 用户 `hooks.json` | sessionStart / userPromptSubmit / preToolUse / postToolUse / stop 全触发 | 零触发（已隔离） |
| 用户 MCP server | superdev / node_repl / codex_apps 三个启动 | 零启动（已隔离） |
| codex 内置插件 | 复用 `~/.codex/plugins`（28MB） | **重新下载 28MB**（未隔离） |
| codex 内置 skills | 复用 | **重新铺 472K**（未隔离） |
| sessions rollout | `~/.codex/sessions/**` | 任务 home 内（已隔离，resume 依赖它） |
| 生成物 | — | `state_*.sqlite`（+wal/shm）、`logs_*.sqlite`、`memories_*.sqlite`、`goals_*.sqlite`、`cache/`、`models_cache.json`、`shell_snapshots/`、`installation_id` |

隔离的收益是实的：真实 home 那轮，模型开局**先花两个回合工具调用去读 superpowers 的 `SKILL.md`
和 `codex-tools.md`**，才执行用户要求的命令——用户自己的交互式规范会直接改变 executor 的行为。

隔离的代价也是实的：**每个任务 home 约 35MB，其中 28MB 是每次重新下载的插件**。这既是磁盘
开销，也是每个任务开局的一段固定延迟。对策见 §6.1 的必验项 V-3。

另注：codex 会**自己**在任务 home 建 `config.toml` 并写入 `[projects."<cwd>"] trust_level = "trusted"`
——工作区信任提示被自动解决，不需要 handoff 额外处理。这与 §2「handoff 不写任务级 config.toml」
不冲突：那条说的是 handoff 不用 config.toml 承载任务级配置（配置全走 `thread/start` 参数），
codex 自己往里记什么是它的内部状态。

### 1.4 沙箱档位的实测行为差

同一条「写工作区外路径」的诉求，两档结果完全不同：

| 档位 | 实测 | 权限工单 |
|------|------|---------|
| `on-request` + workspace-write | 模型跑了 3 条命令（2 次读文件 + `touch /tmp/…`），**全部自动放行**，`/tmp` 文件真的被创建 | **0 张** |
| `untrusted` + workspace-write | 一条 `echo … > probe.txt` 就要批 | **1 张** |

根因：workspace-write 的默认策略是 `{writableRoots: [], networkAccess: …, excludeTmpdirEnvVar: false, excludeSlashTmp: false}`
——`/tmp` 与 `$TMPDIR` 默认在可写区内，**不算越界**。

`networkAccess` 随 config 变：真实 home（用户配了 `[sandbox_workspace_write]`）下为 `true`，
任务级 home 无对应配置时为 `false`。

### 1.5 凭据：为什么「本轮没复现」不等于「不存在」

任务级 home 的 `auth.json` 软链到 `~/.codex/auth.json`，跑完一个完整回合后：软链仍是软链、
target 未变、`~/.codex/auth.json` 的 mtime 停在 16:28:09 未被动过。

**但这不构成证伪。** B26 的结论明写着：grok 那边的触发条件是「任务恰好跨过一次令牌刷新」，
不是「每次任务」——B26 收口时用户重新登录后连跑 5 个 grok 任务软链全好，正是同一个假象。
codex 的 `auth.json` 结构（`tokens.refresh_token` + `last_refresh`）与 grok 同型，且协议里
存在 `account/chatgptAuthTokens/refresh`（reason 枚举只有 `unauthorized`，说明是 401 后的
兜底路径，**不是**常规刷新路径——常规刷新仍由 codex 自己完成）。

结论：**按 B26 同型风险对待**，一开始就上写回机制（§4），不赌。

### 1.6 尚未实证的项

以下未在 spike 中触发过，列入 §6 实现首步必验项，**不得按推断实现后直接声明可用**：

- `item/fileChange/requestApproval` 与 `item/permissions/requestApproval` 的实际触发条件与报文
- `item/tool/requestUserInput`（提问通道，schema 标 EXPERIMENTAL，需 `capabilities.experimentalApi: true`）
- `thread/resume` 跨进程重启续接
- `turn/interrupt` 的实际语义与回收行为
- `ws://` 传输（spike 用的是 stdio；协议语义与传输无关，但握手与断线行为要单独验）

## 2. 核心决策（已确认）

| 决策点 | 结论 | 理由 |
|--------|------|------|
| 传输形态 | `codex app-server --listen ws://127.0.0.1:PORT` | 与 grok ACP over WS 同源，骨架可复用 |
| 进程宿主 | tmux（与另三个同构） | agentd 重启/崩溃不带走执行中任务 |
| 任务级 home | 任务级 `CODEX_HOME` | 隔离用户 AGENTS.md/hooks/MCP，见 §1.3 实测收益 |
| 配置下发 | 协议级（`thread/start` 参数），**不写任务级 config.toml** | codex 与 grok 的关键差异：配置不再绑死 home |
| 沙箱 | `workspace-write` | |
| 审批档 | `approvalPolicy: "on-request"` + `approvalsReviewer: "user"` | 沙箱当筛子，逃逸才升级；见 §3 |
| 网络 | `networkAccess: false`（任务 home 无配置时的默认值，不额外放开） | 模型生成的 `curl`/`npm install` 会被拦并升级人工；codex 自身调模型不受影响 |
| 凭据 | `auth.json` 软链 + 周期巡检写回（B26 机制抽通用件复用） | §4 |
| 会话标识 | `threadId`（== sessionId）落 `task.ExecutorSession` | |

### 2.1 为什么审批档选 `on-request` 而不是 `untrusted`

`untrusted` 与 claude/grok 的「Bash 全部进权限门」行为最一致，但代价是**每条 shell 命令一次
审批往返**——一个真实编码任务几十条命令，就是几十次第 1 层廉价模型调用与延迟叠加。

`on-request` 把路径维度的判据交给 OS 沙箱：工作区内自动放行（等价于 claude/grok 的 allow 兜底），
逃逸沙箱才升级 handoff 三级链（等价于 ask 收窄）。这与 B27 的落地结论同向——B27 探针实证
opencode 的 `external_directory: "ask"` 早就在承担这件事，而工作树内的正常写入**不该**再加一道
「判完还是自动放行」的空门。

明确接受的行为差异（写进 README 与 backlog 备注，不含糊）：

- **工作区内的 `rm -rf` 不再进黑名单**。可承受：工作区是本任务分支的 worktree，删掉的是本任务
  自己的成果，git 可救；而黑名单真正要防的「把宿主机改坏」正是沙箱拦的那一类。
- **`/tmp` 与 `$TMPDIR` 的写入不叫人**（§1.4）。可承受：临时文件是编码任务常规操作，且不致命。

## 3. 任务环境

### 3.1 启动脚本

agentd 在 taskDir 生成 `run_codex.sh`（0600），tmux 窗口 0 执行它：

```sh
#!/bin/sh
# 由 agentd 生成：codex app-server 启动脚本（0600，勿外泄）。
export CODEX_HOME='<taskDir>/codexhome'
# B19 注入的 env 变量在此展开（export KEY='VALUE'，值单引号包裹）
exec codex app-server --listen 'ws://127.0.0.1:<port>' >> '<taskDir>/serve.log' 2>&1
```

与 grok 的 `run_grok.sh` 同形：B19 的 `StartReq.Env` 注入排在业务变量之前、值单引号包裹、
日志只打 key 名不打值。

### 3.2 tmux 布局

沿用 claude/grok 的两窗口形态，`handoff attach` 看到的东西保持一致：

- 窗口 0：`run_codex.sh`（app-server 本体）
- 窗口 1：`tail -f <taskDir>/render.log`（adapter 把事件流渲染成人读文本）

`render.log` 至少要落：模型推理摘要、工具动作（命令 + cwd）、`【模型提问】` 段、权限升级与裁决
结果、回合收尾。与 grok 的 render.log 对齐，审核者跨 executor 看到同一种东西。

### 3.3 任务级 CODEX_HOME 的构成

```
<taskDir>/codexhome/
  auth.json      -> 软链到 ~/.codex/auth.json（见 §4）
  （其余由 codex 自行生成：sessions/、state_*.sqlite、plugins/、skills/、cache/ …）
```

**不写任务级 `config.toml`**——所有任务级配置走 `thread/start` 参数。这是 codex 相对 grok 的
结构性优势：grok 那边「config.toml 就是 home」，逼得配置隔离必须连带 home 隔离；codex 这里
两件事解耦，任务 home 只承担「隔离用户全局指令 + 装 sessions」这一件事。

## 4. 凭据归属（B26 同型）

把 `internal/executor/grok/authsync.go` 抽为 **`internal/authsync`** 通用件，grok 与 codex 共用：

- **周期巡检**：定时检查任务 home 里的 auth 文件是否从软链变成了普通文件
- **收编写回**：变成普通文件说明 executor 刷新过令牌，把新凭据写回真实 home
- **软链复位**：写回成功后用**原子 rename** 把软链复位（不是先删后建）

B26 已踩平并有变异检查兜底的三条防线原样搬过去，一条都不能丢：

1. **「严格更晚」才写回**——防倒灌（用旧凭据覆盖新凭据）
2. **写回失败时错误复位软链**——防任务目录里留下孤儿凭据副本
3. **原子 rename，无破链窗口**——`Symlink` 失败会让任务永久失去凭据，与「下轮再试恢复」矛盾

抽取时的参数化面：auth 文件名（grok `auth.json` / codex `auth.json`）、真实 home 路径、
「更晚」的判据字段（grok 用 `expires_at`，**codex 用什么字段要在 V-4 里确认**——`auth.json`
里可见 `last_refresh` 与 `tokens.*`，不能想当然）。

## 5. 五动作映射

### 5.1 Start

1. 建任务 home、铺 auth 软链、生成 `run_codex.sh`
2. tmux 拉起窗口 0，等端口就绪（沿用 claude adapter 的「等读端就绪」教训：进程起来 ≠ 端口可连）
3. WS 连上 → `initialize`（`capabilities.experimentalApi: true`）→ `initialized` 通知
4. `thread/start`：`cwd`=worktree、`sandbox`=workspace-write、`approvalPolicy`=on-request、
   `approvalsReviewer`=user、`model`=dispatch 的 `--model`（未指定则不传）、
   `developerInstructions`=handoff 的收尾协议（产 commit + summary 的约定）
5. 拿到 `threadId` 立即 emit 一条带 `SessionID` 的 progress 事件（会话就绪信号，见 executor 契约）
6. `turn/start`：plan 正文作为 `input: [{type:"text", text: …}]`

### 5.2 Events

`turn/start` 是异步的（§1.1），事件循环泵 ServerNotification：

| codex | handoff |
|-------|---------|
| `item/started` / `item/completed`（commandExecution、agentMessage、reasoning） | progress（渲染进 render.log） |
| `turn/completed` | 回合终态 → 走 `internal/executor/turn` 的回合分类 |
| `thread/status/changed` | progress |
| `account/rateLimits/updated` | progress（带 `usedPercent`，配额可观测） |
| ServerRequest `item/*/requestApproval` | permission（`PermissionID` = `itemId`） |
| ServerRequest `item/tool/requestUserInput` | question（**必须应答**，否则回合挂死——grok 的教训） |

delta 类通知（`item/agentMessage/delta`、`item/reasoning/*Delta`、`item/commandExecution/outputDelta`）
只喂 render.log，不产 handoff 事件——否则事件表会被刷爆。

### 5.3 Send

`turn/start`（同 threadId 新回合），文本原样透传不加工。

### 5.4 RespondPermission

应答对应的 ServerRequest：`once → {"decision":"accept"}`，`reject → {"decision":"cancel"}`。
**不使用** `acceptForSession` 与 `acceptWithExecpolicyAmendment`——两者都是「以后同类不再问」，
等价于 B23 明确否掉的「批准一次后同样命令自动放行」，是实打实的安全削弱。

### 5.5 Stop

`turn/interrupt` → 关 WS → 收 tmux 会话 → 删任务 home（35MB，见 §1.3）。
按 B20 的教训：运行态丢失时也要能按会话名 `handoff-<id8>` 兜底回收，且回收失败要发事件而非静默。

### 5.6 Resume（agentd 重启恢复）

按 B18：启动恢复时按 tmux 会话名探活，活着就重连 WS 并 `thread/resume{threadId, cwd}`，
状态不改。`threadId` 来自 `task.ExecutorSession`，rollout 在任务 home 里（所以任务 home
**不能在 Stop 之前删**）。

## 6. 实现首步必验项

这些**在实现的第一步就验**，验完把结论回写本 spec；未验之前不得声明对应能力可用。

| id | 待验 | 为什么必须先验 |
|----|------|---------------|
| V-1 | `item/tool/requestUserInput` 能否触发、报文形态、应答形态 | grok 那边提问通道连翻两次车（应答形态错 → 被判工具失败；兜底重复上报 → 一次提问两张工单）。codex 这条还标着 EXPERIMENTAL |
| V-2 | `thread/resume` 跨 app-server 进程重启能否续接同一会话 | B18 是审核者亲身撞上的缺陷，不能再赌 |
| V-3 | 能否把真实 home 的 `plugins/`、`skills/` 以只读软链复用 | 决定每个任务是省下 28MB 与一段开局延迟，还是照付 |
| V-4 | codex `auth.json` 里「哪个字段能判定更晚」 | §4 第 1 条防线（防倒灌）的判据，不能想当然 |
| V-5 | `on-request` 档下逃逸沙箱的写入是否真产生 `requestApproval`、文件是否真的没被创建 | §2.1 整个论证都建立在这条上；B27 探针的做法是「拒绝后 `ls` 确认文件不存在」 |
| V-6 | `ws://` 传输的握手与断线行为 | spike 走的是 stdio；grok 的 §4.2.2「WS 断开的单一处置路径」教训要对照 |

## 7. 接入面（改动清单）

- **新增 `internal/executor/codex/`**：`adapter.go` / `proc.go`（tmux + 端口就绪）/ `appserver.go`
  （WS JSON-RPC）/ `perm.go` / `taskenv.go` / `resume.go` / `testdata/`
- **新增 `internal/authsync/`**：从 `internal/executor/grok/authsync.go` 抽取，grok 改为调用它
  （**grok 的行为必须零变化**，抽取后原有单测与变异检查全部照跑）
- **`internal/executor/turn`**：复用，不改
- **agentd**：executor 选择加 `codex` 分支；`--executor=codex` 的启动预检（`codex` 在 PATH、
  `~/.codex/auth.json` 存在）
- **CLI**：`dispatch --executor codex`
- **README**：架构图第三个 executor 旁补 codex；快速开始的 `agentd --executor` 与 `dispatch --executor`
  示例各补一行；前置条件补「需本机装 `codex` 且已登录」
- **backlog**：新增 B28，并在备注里明记 §2.1 的两条行为差异

## 8. 错误处理

- 启动失败（codex 不在 PATH、未登录、端口占用）→ 回显可行动真因（B16 的教训：不许扁平化成
  「派发任务失败」），任务落 failed，**任务 home 与 managed worktree 都要删**（B15）
- WS 断开 → 单一处置路径，对齐 grok §4.2.2，不搞多条分支
- `Send` / `RespondPermission` / `Stop` 遇到运行态不存在 → 包装 `executor.ErrTaskNotRunning`
  哨兵（禁止靠错误文本判别）
- 未决权限在重连后是否重发：**按最保守路径实现**（假设不重发），与 grok 同

## 9. 非目标

- **不改 B23/B27 的权限判据**：`commandActions` 的结构化路径是更可靠的输入，但改判据是跨三个
  adapter 的事，单独立项
- **不做 codex 的 execpolicy `.rules` / granular 审批档**：`on-request` 已满足本期目标
- **不把 codex 登记为审批者执行者**（分级审批链第 1 层）：与本期目标无关
- **不做 API key 计费换轨**：与 B26 同一处置——只进文档不进代码
- **不改 `wait` 的事件游标语义**（B22）：与本 adapter 无关
