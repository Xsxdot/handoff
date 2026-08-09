# codex adapter 设计（B28）

> 目标：让 `handoff dispatch --executor codex` 与 opencode / claude / grok 对等——五动作
> 全链路可用、分级审批链原样生效、`handoff attach` 看到同形态的终端实况、agentd 重启后
> 能恢复运行态。
>
> 来源：2026-08-09 用户需求「我想把 codex 也加上」；backlog B28。

## 1. 前置 spike（已完成，2026-08-09）

环境：`codex-cli 0.144.1`（macOS 26.5.2 arm64），登录态 `auth_mode = chatgpt`（Plus）。
spike 用一个零依赖的 Python stdio JSON-RPC 客户端驱动 `codex app-server`，跑了四轮真实回合。

### 1.1 已实证的结论

| 假设 | 结论 | 证据 |
|------|------|------|
| 有程序化审批挂载点 | ✅ ServerRequest `item/commandExecution/requestApproval`（server→client 阻塞式请求） | 报文见 §1.2 |
| 权限请求带稳定 id | ✅ `itemId`（形如 `exec-bd1deb0d-…`），与同回合 `item/commandExecution` 事件的 id 一致 | 报文比对 |
| 权限描述可读且不截断 | ✅ `command` 为完整命令行全文，另有 `cwd` 与结构化 `commandActions` | 报文 |
| 应答语义能对上 handoff | ✅ `availableDecisions: ["accept", {acceptWithExecpolicyAmendment…}, "cancel"]` | 报文 |
| 会话可恢复 | ✅ `thread/resume{threadId}`；threadId **等于** sessionId，rollout 落 `<home>/sessions/**.jsonl` | `thread/start` 回执 |
| 有非 stdio 传输 | ✅ `codex app-server --listen ws://IP:PORT`（另支持 `unix://`） | `--help` |
| 任务级配置可协议级下发 | ✅ `thread/start` 直收 `cwd`/`sandbox`/`approvalPolicy`/`approvalsReviewer`/`model`/`baseInstructions`/`developerInstructions`/`config` | 回执按传入值生效 |
| **协议级参数能压过用户 config** | ✅ **关键**：真实 home 下 config 给的基线是 `networkAccess:true`，`turn/start.sandboxPolicy` 显式传 `false` 后，联网命令拿到 `curl: (6) Could not resolve host`；同命令在宿主机直接跑是 `200` | §1.4 |
| codex 自带的模型审批者可被关掉 | ✅ `approvalsReviewer: "user"` 把裁决路由回客户端（用户真实配置是 `auto_review`） | 回执 `approvalsReviewer: "user"` |
| 沙箱拒网会升级成权限门 | ❌ **不会**，命令直接失败、**零工单**，审核者全程不知情 | §1.4 |
| `turn/start` 是同步的 | ❌ **异步**：立即返回 `{turn:{status:"inProgress"}}`，回合终态在 `turn/completed` 通知里 | 首轮据此改写泵循环 |
| workspace-write 沙箱会拦 `/tmp` | ❌ **不拦**：`excludeSlashTmp`/`excludeTmpdirEnvVar` 默认 false | §1.4 |
| 沙箱 `networkAccess=false` 会影响 codex 自身调模型 | ❌ 不影响，回合正常完成（沙箱只约束模型生成的 shell 命令） | 多轮全程 `networkAccess:false` |
| 任务级 `CODEX_HOME` 能隔离用户全局指令 | ✅ 能，但**本设计不采用**，理由见 §1.3 | `instructionSources` 从有到空 |

### 1.2 权限请求实测报文

`approvalPolicy: "untrusted"` 下抓到的报文（`on-request` 下同结构，只是触发条件更窄），
handoff 的 permission 事件即由它翻译：

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
  动作类型与路径。**本期不改判据**（见 §9），但登记为后续可利用的更可靠输入。

### 1.3 为什么用**用户级 home** 而不是任务级 home

任务级 `CODEX_HOME` 实测是能隔离用户全局指令的：

| 项 | 真实 `~/.codex` | 任务级 `CODEX_HOME` |
|----|------------------|----------------------|
| 用户 `AGENTS.md` | `instructionSources: ["~/.codex/AGENTS.md"]` | `[]`（已隔离） |
| 用户 `hooks.json` | sessionStart / userPromptSubmit / preToolUse / postToolUse / stop 全触发 | 零触发（已隔离） |
| 用户 MCP server | superdev / node_repl / codex_apps 三个启动 | 零启动（已隔离） |
| codex 内置插件 | 复用 `~/.codex/plugins`（28MB） | **重新下载 28MB**（未隔离） |
| codex 内置 skills | 复用 | **重新铺 472K**（未隔离） |
| sessions rollout | `~/.codex/sessions/**` | 任务 home 内 |

**但本设计不采用任务级 home**，这是 2026-08-09 用户的明确决定，理由是 executor 一般跑在
专用开发机上，把污染源从开发机上卸掉即可。收益是三重的：

1. **凭据零副本**——不需要软链、不需要写回、不需要巡检。B26（grok 令牌刷新顶掉软链、
   用户登录态失效）那一整类问题在 codex 上**架构性不存在**，`internal/authsync` 的抽取
   与那三条防线全部不需要。
2. **每任务省 28MB 与一段开局插件下载延迟**。
3. 实现面显著变小。

代价是 executor 会继承开发机上的 codex 全局环境。**关键在于这个代价不落在安全面上**：
§1.1 已实证协议级参数压过 config，所以沙箱边界与审批档由 handoff 的代码钉死，不靠开发机
配置。留下的是**行为面**代价——`AGENTS.md` 与 `hooks.json` 会改变 executor 的干活方式
（真实 home 那轮实测：模型开局先花两个回合工具调用去读 superpowers 的 `SKILL.md` 和
`codex-tools.md`，才执行用户要求的命令），MCP server 会给它额外工具。

因此这成为一条**部署前置条件**，写进 README（§7）：executor 机的 `~/.codex` 需清理
`AGENTS.md`、`hooks.json`、`config.toml` 里的 `mcp_servers`。这是约定而非代码保证——
`hooks.json` 没有协议级开关可以关掉它，是本方案已知且被接受的软肋。

### 1.4 沙箱的三条实测行为

**(a) 档位差。** 同一条「写工作区外路径」的诉求：

| 档位 | 实测 | 权限工单 |
|------|------|---------|
| `on-request` + workspace-write | 模型跑了 3 条命令（2 次读文件 + `touch /tmp/…`），**全部自动放行**，`/tmp` 文件真的被创建 | **0 张** |
| `untrusted` + workspace-write | 一条 `echo … > probe.txt` 就要批 | **1 张** |

根因：workspace-write 的默认策略是 `{writableRoots: [], excludeTmpdirEnvVar: false, excludeSlashTmp: false}`
——`/tmp` 与 `$TMPDIR` 默认在可写区内，**不算越界**。

**(b) 协议级 `sandboxPolicy` 压得过 config。** 真实 home 下 `thread/start` 回执的基线是
`networkAccess: true`（用户 `[sandbox_workspace_write]` 给的）；`turn/start` 显式传
`{"type":"workspaceWrite","networkAccess":false,"excludeSlashTmp":true,"excludeTmpdirEnvVar":true,"writableRoots":[]}`
后，模型跑 `curl --noproxy '*' https://example.com` 拿到 `curl: (6) Could not resolve host`
（DNS 被掐），而**同一条命令在宿主机直接跑返回 `200`**。

> 首轮探针作废并重跑过一次：第一版命令走了机器上的代理变量，报的是
> `Failed to connect to 127.0.0.1 port 1087`（本地代理没开），在触及沙箱之前就先挂了，
> 证明不了任何事。加 `env -u *_proxy` + `--noproxy '*'` 重跑并补宿主机控制组，才有判别力。

**(c) 拒网不产工单。** 上面那次网络被拒，`approval_count = 0`。沙箱拒网**不会**升级到
handoff 权限门，命令只是直接失败。设计含义见 §2.2。

### 1.5 尚未实证的项

以下未在 spike 中触发过，列入 §6 实现首步必验项，**不得按推断实现后直接声明可用**：

- `item/fileChange/requestApproval` 与 `item/permissions/requestApproval` 的触发条件与报文
- `item/tool/requestUserInput`（提问通道，schema 标 EXPERIMENTAL，需 `capabilities.experimentalApi: true`）
- `thread/resume` 跨进程重启续接
- `turn/interrupt` 的实际语义与回收行为
- `ws://` 传输（spike 用的是 stdio；协议语义与传输无关，但握手与断线行为要单独验）
- 并发任务共用 `~/.codex` 的 sessions 与 `state_*.sqlite` 是否有锁竞争

## 2. 核心决策（已确认）

| 决策点 | 结论 | 理由 |
|--------|------|------|
| 传输形态 | `codex app-server --listen ws://127.0.0.1:PORT` | 与 grok ACP over WS 同源，骨架可复用 |
| 进程宿主 | tmux（与另三个同构） | agentd 重启/崩溃不带走执行中任务 |
| home | **用户级 `~/.codex`**，不设 `CODEX_HOME` | §1.3；凭据零副本，B26 型问题不存在 |
| 配置下发 | 全部协议级（`thread/start` / `turn/start` 参数），**不碰任何 config 文件** | §1.1 实证协议压过 config |
| 沙箱 | `workspace-write`，且每回合显式传 `sandboxPolicy` 钉死 | 不让开发机 config 影响安全判据 |
| 审批档 | `approvalPolicy: "on-request"` + `approvalsReviewer: "user"` | §2.1 |
| 网络 | `networkAccess: false`（显式钉死） | §2.2 |
| `/tmp` | `excludeSlashTmp: true` + `excludeTmpdirEnvVar: true`（显式钉死） | §2.1 第二条差异随之消失 |
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

（原本还有一条「`/tmp` 写入不叫人」，已由 §2 的 `excludeSlashTmp: true` 显式关掉，不再是差异。）

### 2.2 网络：拒掉，并且接受审核者看不到

`networkAccess: false` 钉死后，模型生成的 `curl` / `npm install` / `go mod download` 会被沙箱
拒掉。**但 §1.4(c) 实证拒网不产工单**——审核者不会被叫醒，只有 executor 自己看到命令失败。

这是本设计里最不舒服的一条，明确记下取舍：

- 选 `false`：装依赖会失败，模型可能反复试错甚至绕路；审核者事后才从 render.log 看到。
- 选 `true`：`curl | sh` 这类操作全程无人知晓，且与「沙箱当筛子」的整个论证冲突。

选 `false`，理由是失败是响的、放行是哑的——前者审核者迟早会在 diff 或 render.log 里看到并
`continue` 指示，后者是静默的安全洞。**若真机跑一段时间后发现装依赖失败频繁**，正确的补法不是
翻成 `true`，而是给「网络访问失败」加一条 progress 事件让审核者当场知道（登记在 §9，不在本期）。

## 3. 任务环境

### 3.1 启动脚本

agentd 在 taskDir 生成 `run_codex.sh`（0600），tmux 窗口 0 执行它：

```sh
#!/bin/sh
# 由 agentd 生成：codex app-server 启动脚本（0600，勿外泄）。
# 注意：不设 CODEX_HOME——本设计刻意复用用户级 ~/.codex（见 spec §1.3）。
# B19 注入的 env 变量在此展开（export KEY='VALUE'，值单引号包裹）
exec codex app-server --listen 'ws://127.0.0.1:<port>' >> '<taskDir>/serve.log' 2>&1
```

与 grok 的 `run_grok.sh` 同形：B19 的 `StartReq.Env` 注入排在最前、值单引号包裹、
日志只打 key 名不打值。

### 3.2 tmux 布局

沿用 claude/grok 的两窗口形态，`handoff attach` 看到的东西保持一致：

- 窗口 0：`run_codex.sh`（app-server 本体）
- 窗口 1：`tail -f <taskDir>/render.log`（adapter 把事件流渲染成人读文本）

`render.log` 至少要落：模型推理摘要、工具动作（命令 + cwd）、`【模型提问】` 段、权限升级与裁决
结果、**被沙箱拒掉的命令**（§2.2 的补偿：审核者事后至少能查到）、回合收尾。与 grok 的
render.log 对齐，审核者跨 executor 看到同一种东西。

### 3.3 部署前置条件

因为复用用户级 home，executor 机需要满足（README 明写，agentd 启动预检尽量检查并 WARN）：

- 装了 `codex` 且已登录（`~/.codex/auth.json` 存在）
- `~/.codex/AGENTS.md` 已移除（否则会改变 executor 的干活方式）
- `~/.codex/hooks.json` 已移除（**没有协议级开关能关掉它**，只能靠清理）
- `~/.codex/config.toml` 里的 `[mcp_servers]` 已清空（否则 executor 会多出一批工具）

`config.toml` 里的其余项（`model`、`approvals_reviewer`、`sandbox_mode`、
`[sandbox_workspace_write]`）**不需要清理**——handoff 全部协议级钉死，实测压得过它们。

## 4. 凭据

复用用户级 `~/.codex`，凭据只有一份权威副本，executor 与用户本人共用同一份登录态。
令牌刷新由 codex 自己完成、写回同一个文件，**不存在 B26 那类「任务目录里困住一份新凭据」
的窗口**。本节无需任何机制。

协议里存在 `account/chatgptAuthTokens/refresh`（ServerRequest，reason 枚举只有 `unauthorized`），
那是 401 后请客户端补令牌的兜底路径。handoff **不实现**它——按最保守路径处理：收到即回错误，
让 codex 走它自己的刷新逻辑；真出现 401 就让任务失败并回显真因，交审核者裁决。

## 5. 五动作映射

### 5.1 Start

1. 生成 `run_codex.sh`
2. tmux 拉起窗口 0，等端口就绪（沿用 claude adapter 的教训：进程起来 ≠ 端口可连）
3. WS 连上 → `initialize`（`capabilities.experimentalApi: true`）→ `initialized` 通知
4. `thread/start`：`cwd`=worktree、`sandbox`=workspace-write、`approvalPolicy`=on-request、
   `approvalsReviewer`=user、`model`=dispatch 的 `--model`（未指定则不传）、
   `developerInstructions`=handoff 的收尾协议（产 commit + summary 的约定）
5. 拿到 `threadId` 立即 emit 一条带 `SessionID` 的 progress 事件（会话就绪信号，见 executor 契约）
6. `turn/start`：plan 正文作为 `input: [{type:"text", text: …}]`，**并显式传 `sandboxPolicy`**：
   `{"type":"workspaceWrite","networkAccess":false,"excludeSlashTmp":true,"excludeTmpdirEnvVar":true,"writableRoots":[]}`
   ——每个回合都要传（`turn/start` 级参数，不是 thread 级）

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
| ServerRequest `account/chatgptAuthTokens/refresh` | 回错误，不实现（§4） |

delta 类通知（`item/agentMessage/delta`、`item/reasoning/*Delta`、`item/commandExecution/outputDelta`）
只喂 render.log，不产 handoff 事件——否则事件表会被刷爆。

### 5.3 Send

`turn/start`（同 threadId 新回合，同样带 `sandboxPolicy`），文本原样透传不加工。

### 5.4 RespondPermission

应答对应的 ServerRequest：`once → {"decision":"accept"}`，`reject → {"decision":"cancel"}`。
**不使用** `acceptForSession` 与 `acceptWithExecpolicyAmendment`——两者都是「以后同类不再问」，
等价于 B23 明确否掉的「批准一次后同样命令自动放行」，是实打实的安全削弱。

### 5.5 Stop

`turn/interrupt` → 关 WS → 收 tmux 会话。**没有任务级 home 要删**（本设计的简化红利之一）；
managed worktree 的清理沿用现有逻辑（B15）。按 B20 的教训：运行态丢失时也要能按会话名
`handoff-<id8>` 兜底回收，且回收失败要发事件而非静默。

sessions rollout 落在 `~/.codex/sessions/**`，归档时**不删**——它是 codex 自己的会话历史，
删了会破坏用户本人的 `codex resume`。

### 5.6 Resume（agentd 重启恢复）

按 B18：启动恢复时按 tmux 会话名探活，活着就重连 WS 并 `thread/resume{threadId, cwd}`，
状态不改。`threadId` 来自 `task.ExecutorSession`。因为 rollout 在用户级 home，agentd 重启、
甚至 app-server 进程重启后 thread 都还在盘上——这比任务级 home 的方案更结实。

## 6. 实现首步必验项

这些**在实现的第一步就验**，验完把结论回写本 spec；未验之前不得声明对应能力可用。

| id | 待验 | 为什么必须先验 |
|----|------|---------------|
| V-1 | `item/tool/requestUserInput` 能否触发、报文形态、应答形态 | grok 那边提问通道连翻两次车（应答形态错 → 被判工具失败；兜底重复上报 → 一次提问两张工单）。codex 这条还标着 EXPERIMENTAL |
| V-2 | `thread/resume` 跨 app-server 进程重启能否续接同一会话 | B18 是审核者亲身撞上的缺陷，不能再赌 |
| V-3 | `on-request` + 显式 `sandboxPolicy` 下，**文件系统**越界写是否真产生 `requestApproval`、拒绝后文件是否真的没被创建 | §2.1 整个论证压在这条上；网络那条已实证**不产工单**（§1.4c），文件系统这条不能想当然沿用。B27 探针的做法是「拒绝后 `ls` 确认文件不存在」 |
| V-4 | 并发多个 codex 任务共用 `~/.codex`（sessions + `state_*.sqlite`）是否有锁竞争或串扰 | 用户级 home 方案独有的新风险，任务级 home 时不存在 |
| V-5 | `ws://` 传输的握手与断线行为 | spike 走的是 stdio；grok 的「WS 断开的单一处置路径」教训要对照 |
| V-6 | 清理过 `AGENTS.md`/`hooks.json`/`mcp_servers` 的 home 上，executor 行为是否干净（不再绕路读 skill） | §1.3 的部署前置条件是否真的够，要在开发机上实测一次 |

## 7. 接入面（改动清单）

- **新增 `internal/executor/codex/`**：`adapter.go` / `proc.go`（tmux + 端口就绪）/ `appserver.go`
  （WS JSON-RPC）/ `perm.go` / `taskenv.go` / `resume.go` / `testdata/`
- **`internal/executor/turn`**：复用，不改
- **`internal/executor/grok`**：不动（本设计不再抽 `internal/authsync`）
- **agentd**：executor 选择加 `codex` 分支；`--executor=codex` 的启动预检（`codex` 在 PATH、
  `~/.codex/auth.json` 存在；`AGENTS.md`/`hooks.json`/`mcp_servers` 存在时打 WARN 并提示清理）
- **CLI**：`dispatch --executor codex`
- **README**：架构图第三个 executor 旁补 codex；`agentd --executor` 与 `dispatch --executor`
  示例各补一行；前置条件补 §3.3 的四条（含「executor 机的 `~/.codex` 需清理」）
- **backlog**：新增 B28，备注里明记 §2.1 的行为差异与 §2.2 的网络取舍

## 8. 错误处理

- 启动失败（codex 不在 PATH、未登录、端口占用）→ 回显可行动真因（B16 的教训：不许扁平化成
  「派发任务失败」），任务落 failed，managed worktree 要删（B15）
- WS 断开 → 单一处置路径，对齐 grok 的做法，不搞多条分支
- `Send` / `RespondPermission` / `Stop` 遇到运行态不存在 → 包装 `executor.ErrTaskNotRunning`
  哨兵（禁止靠错误文本判别）
- 未决权限在重连后是否重发：**按最保守路径实现**（假设不重发），与 grok 同
- 401（`account/chatgptAuthTokens/refresh` 到达）→ 任务失败并回显「codex 登录态失效，请在
  executor 机重新 `codex login`」

## 9. 非目标

- **不改 B23/B27 的权限判据**：`commandActions` 的结构化路径是更可靠的输入，但改判据是跨三个
  adapter 的事，单独立项
- **不给「沙箱拒网」补 progress 事件**：§2.2 记了这个补法，但要等真机跑一段时间确认频率再做
- **不做 codex 的 execpolicy `.rules` / granular 审批档**：`on-request` 已满足本期目标
- **不把 codex 登记为审批者执行者**（分级审批链第 1 层）：与本期目标无关
- **不做任务级 `CODEX_HOME`**：§1.3 已定，若将来要在共享机器上跑，再单独立项
- **不改 `wait` 的事件游标语义**（B22）：与本 adapter 无关
