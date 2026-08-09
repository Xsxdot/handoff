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

以下未在 spike 中触发过，列入 §6 实现首步必验项，**不得按推断实现后直接声明可用**。

先划清一条界：`codex app-server generate-json-schema` 导出的 **报文形状是权威的**
（§5.4 的枚举纠错即来自它），未实证的是**触发条件与真实取值**——schema 说得清
「字段叫什么、必填哪些」，说不清「什么情况下会发、发出来的 `questions` 长什么样」。

- `item/fileChange/requestApproval` 与 `item/permissions/requestApproval` 的**触发条件**
  （形状已由 schema 定死，见 §5.4）
- `item/tool/requestUserInput`（提问通道，schema 标 EXPERIMENTAL，需 `capabilities.experimentalApi: true`）；
  形状已知：params 必填 `itemId`/`threadId`/`turnId`/`questions[]`（每项 `id`/`header`/`question`，
  可选 `options[]{label,description}`/`isOther`/`isSecret`），应答体
  `{"answers":{"<question id>":{"answers":["…"]}}}`。**待验的是能否触发、以及不带真实
  答案的应答会不会被判工具失败**（grok 那边翻车的正是这一点）
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
| 网络 | `networkAccess: true`（显式钉死） | §2.2 |
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
- **联网操作全程不经过任何人**（见 §2.2）。`curl … | sh`、`npm install`、`pip install` 在 codex
  上零工单直接执行；同样的命令在 claude/grok 上会走 Bash 的黑名单与三级审批链。

（原本还有一条「`/tmp` 写入不叫人」，已由 §2 的 `excludeSlashTmp: true` 显式关掉，不再是差异。）

### 2.2 网络：放开

`networkAccess: true`（显式传，不继承开发机 config），模型生成的 `curl` / `npm install` /
`go mod download` 直接执行。

**这是 2026-08-09 用户的明确决定**，理由是 executor 跑在专用开发机上，网络面本来就敞着，
沙箱内多这一条不构成实质增量风险；而反方向的代价是实的——`networkAccess: false` 时装依赖
会失败，且 §1.4(c) 实证**拒网不产工单**，审核者不会被叫醒、只有模型自己看到命令失败并可能
反复试错绕路，属于「哑失败」。

记下这条决定的完整含义，不含糊：

- 「逃逸沙箱就升级 handoff」这条论证（§2.1）**只覆盖文件系统越界**，不覆盖网络。网络维度上
  codex 与另三个 adapter 的安全形态不同，已列入 §2.1 的明确差异。
- 于是 §6 的 V-3（文件系统越界是否真产工单）成为 §2.1 论证**唯一的实证支点**，权重比原计划更高，
  不许沿用网络那条的结论。

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
结果、**被沙箱拒掉的命令**（这类拒绝不产工单——§1.4c，render.log 是审核者事后唯一能查到的地方）、回合收尾。与 grok 的
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
6. `turn/start`：plan 正文作为 `input: [{type:"text", text: …}]`，**并把四个安全参数每回合
   显式钉一遍**——schema 实证 `turn/start` 同时接受 `sandboxPolicy` / `approvalPolicy` /
   `approvalsReviewer` / `cwd` 的 turn 级覆盖：

   ```json
   {"sandboxPolicy":{"type":"workspaceWrite","networkAccess":true,
                     "excludeSlashTmp":true,"excludeTmpdirEnvVar":true,"writableRoots":[]},
    "approvalPolicy":"on-request","approvalsReviewer":"user","cwd":"<worktree>"}
   ```

   为什么四个都钉而不只钉 `sandboxPolicy`：安全姿态因此**与 thread 的历史状态和恢复
   路径无关**。`thread/start` 时钉过的值会被 `thread/resume` 或任何一次带覆盖的
   `turn/start` 改掉，而恢复路径是最容易漏钉的地方（B18 的教训）；每回合重钉是
   一次固定成本的幂等操作，换来「任何一个回合都不可能跑在开发机 config 的档位上」。

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
| `serverRequest/resolved`（带 `requestId`） | 该 ServerRequest 已被别处了结 → 从挂起表摘掉对应项，不再等裁决 |
| `item/started` / `item/completed` 的 item 本体 | 同时写入 `itemId → item` 索引（§5.4：fileChange 的权限报文没有路径，只能从索引取） |

delta 类通知（`item/agentMessage/delta`、`item/reasoning/*Delta`、`item/commandExecution/outputDelta`）
只喂 render.log，不产 handoff 事件——否则事件表会被刷爆。

### 5.3 Send

`turn/start`（同 threadId 新回合，同样带 `sandboxPolicy`），文本原样透传不加工。

### 5.4 RespondPermission

`codex app-server generate-json-schema` 导出的枚举定死了三条映射（2026-08-09 补查，
纠正了本 spec 初稿的一处错误）：

| handoff | commandExecution / fileChange | 依据 |
|---------|------------------------------|------|
| `once` | `{"decision":"accept"}` | |
| `reject` | `{"decision":"decline"}` | schema：`decline` = "The agent will continue the turn"；`cancel` = "The turn will also be immediately interrupted" |

**初稿写的 `cancel` 是错的**，必须是 `decline`。handoff 的 reject 语义来自 grok 的
`reject-once`：拒掉这一次、回合继续跑，被拒清单在回合收尾时一并交代给审核者
（`finishTurn` 的 `takeRejected`）。用 `cancel` 会把整个回合掐掉，adapter 随即把它
判成失败——审核者点一次「拒绝」等于杀掉任务，与另三个 adapter 的行为不对等。

**不使用** `acceptForSession` / `acceptWithExecpolicyAmendment` / `applyNetworkPolicyAmendment`
——三者都是「以后同类不再问」，等价于 B23 明确否掉的「批准一次后同样命令自动放行」，
是实打实的安全削弱。

**`item/permissions/requestApproval` 一律 fail-closed。** 它的应答体不是 decision 枚举，
而是一份 `GrantedPermissionProfile` + `scope`（默认 `turn`）——语义是「模型申请把沙箱
放宽一截」，与 `acceptForSession` 同类。handoff 固定回一份空 profile（不授予任何额外
权限），同时把这次申请写进 render.log 并产一条 progress 让审核者知情。**不做成可批准的
权限门**：能被批准的「放宽沙箱」正是 §2.1 安全论证赖以成立的那道边界。

**`item/fileChange/requestApproval` 的报文里没有路径。** schema 的必填字段只有
`itemId`/`threadId`/`turnId`/`startedAtMs`（另有可选 `grantRoot`/`reason`），
路径在同 `itemId` 的 `item/started` 通知的 `item.changes[].path` 里。因此 adapter
必须维护 `itemId → 最近一次 ThreadItem` 的索引，权限事件的 `PermRequest.Paths`
从索引里取；索引里查不到就**不伪造结构**（`Perm` 置 nil），交 manager fail-closed
升级人工——这是 `executor.PermRequest` 的既定边界。

### 5.5 Stop

`turn/interrupt` → 关 WS → 收 tmux 会话。**没有任务级 home 要删**（本设计的简化红利之一）；
managed worktree 的清理沿用现有逻辑（B15）。按 B20 的教训：运行态丢失时也要能按会话名
`handoff-<id8>` 兜底回收，且回收失败要发事件而非静默。

sessions rollout 落在 `~/.codex/sessions/**`，归档时**不删**——它是 codex 自己的会话历史，
删了会破坏用户本人的 `codex resume`。

### 5.6 Resume（agentd 重启恢复）

按 B18：启动恢复时按 tmux 会话名探活，活着就重连 WS 并 `thread/resume`，状态不改。
`threadId` 来自 `task.ExecutorSession`。`thread/resume` 的参数**必须把 `cwd` /
`approvalPolicy` / `approvalsReviewer` 一起重传**（schema 实证它接受这三个覆盖）——
恢复后的第一个 `turn/start` 也会再钉一遍（§5.1 步骤 6），两层都钉是因为恢复路径正是
最容易让安全档位悄悄退回开发机 config 的地方。因为 rollout 在用户级 home，agentd 重启、
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

### 6.1 真机验收结果（2026-08-10，审核者在 devbox 执行）

真机环境：devbox，**codex-cli 0.146.0**（spec 与实现计划都是照 0.144.1 写的），handoff b28 二进制，任务 `da2f1906`（主链路 + V-2 + V-1）与 `a6a84d65`/`93db5460`/`0f9de0c1`（V-4）。

**先记一个部署前提，它差点让整轮验收得出错误结论**：devbox 连 OpenAI 端点要走本机 sing-box HTTP 代理（`api.openai.com` 直连会解析到 `69.63.176.59` 然后 TCP 握手超时，`chatgpt.com/backend-api` 同为 `http_code=000`；同一时刻 `github.com` 返回 200）。grok / claude 早就靠 `~/.handoff/env/*.env` 注入代理，而 **`config.yaml` 的 `env:` 段里没有 codex 条目**，于是首轮 e2e（任务 `5d6c6a3d`）表现为「会话建得起来、回合发得出去、模型一个 token 都不产」，serve.log 只刷 `failed to refresh available models`。补上 `codex: codex.env`（同一份代理变量）并重启 agentd 后，同一个 plan **90 秒跑完**。**结论：codex 的部署前置条件必须包含「按需配 `env/codex.env`」，这条要写进 README。**

| id | 结论 |
|----|------|
| V-1 | **触发不了，且原因明确**：显式要求模型调用原生提问工具后，它回「`request_user_input` 工具在当前 Default mode 不可用」。所以 `item/tool/requestUserInput` 在当前协议档位下**根本不会到达 handoff**，Task 7 那段代码是防御性的、暂时走不到。**提问通道本身不受影响**——handoff 侧的问题中继全程由 trailer / 兜底路径承担，本轮实测产出 5 张 ask 工单，`reply --answer` 每次都被模型正确续接 |
| V-2 | **已验，上下文完整**。先让模型记住口令 `pineapple-4417` 并收尾，再 `tmux kill-session` 杀掉 app-server 进程，然后 `continue`：日志走完整条冷恢复阶梯（`app-server 已不在，进入冷恢复 old_port=55863` → `冷恢复新 app-server 就绪 new_port=58150` → `codex 任务已恢复 mode=cold`），`thread/resume` 载回原 thread `019fe749-…`，模型**准确答出 `pineapple-4417`** |
| V-3 | **两半都已验，§2.1 的论证成立**。①越界写**真的产工单**：`tee /Users/sycm/handoff-e2e-probe.txt` 触发 `requestApproval`，`once → accept` 后文件**真的被创建**（17 字节，内容正确）；②拒绝后**文件真的没被动**：`rm -rf /Users/sycm/handoff-e2e-deny.txt` 命中黑名单升级人工，`reply --deny` 后 serve.log 留下 `exec_command failed … Rejected("rejected by user")`，文件原样健在。顺带实证了被拒清单优先的兜底——adapter 产出「本回合有权限请求被拒。被拒清单：…」的 ask 工单 |
| V-4 | **已验，无锁竞争**。三个 codex 任务并发跑完（各自 managed worktree + 各自 app-server，共用同一份 `~/.codex`），全部 `completed`，四个任务的 serve.log 里 `database is locked` / `SQLITE_BUSY` 命中**均为 0** |
| V-5 | **已验**。`ws://127.0.0.1:<port>` 的 URL 形态与 spec 一致，启动横幅另暴露 `/readyz` 与 `/healthz`（`Proc.Alive()` 用 TCP 探活保守可行，日后要更强判据这两个 HTTP 面是现成的）；app-server **209ms** 就绪；断线行为也已验——杀掉进程后读循环以 `failed to get reader: failed to read frame header: EOF` 终结，`onClosed` 把真因连同 serve.log 尾部一起报出，随后的 `continue` 正常触发冷恢复 |
| V-6 | **未验**。要验「清理过 `AGENTS.md`/`hooks.json`/`mcp_servers` 的 home 上 executor 是否干净」就得动用户本人的 `~/.codex`（且该目录有 `config.toml.superdev-bak`，疑似被 superdev 管理），审核者不擅自改。**但污染本身已被实证，两条都直接**：①MCP——serve.log 持续报 `rmcp::transport::worker … chatgpt.com/backend-api/ps/mcp`，说明用户 `config.toml` 里的 `[mcp_servers.superdev]` 确实被 executor 继承；②hooks——同机对照实验（直接 `codex exec`，非 app-server 路径）的输出头两行就是 `hook: SessionStart` / `hook: SessionStart Failed`，`~/.codex/hooks.json` 真的在跑。Preflight 那三条 WARN 是有的放矢的。**仍未验的是「清理之后是否真的干净」**，那需要动用户的 home |

同轮验到的其它事实：

- **五动作全链路通**：`dispatch --executor codex` → `running` → 权限门（审批链自动裁决 + 黑名单升级人工）→ `completed` → `diff`（`probe.txt` 一行，提交 `dd26207`）→ `continue` 同会话续接（thread id 全程不变）→ `done`（app-server 进程、tmux 会话、managed worktree 三样都清干净，实测零残留）。
- **`Stop` 有一次没杀死 app-server**：任务 `5d6c6a3d`（当时卡在连不上网的状态）`handoff stop` 返回成功、agentd 也打了 `codex tmux 会话已回收`，但 `codex app-server` 进程存活下来、26 分钟后仍 LISTEN 在原端口 64038，只能手工 `kill`。后续四个任务走 `done` 全部干净，所以复现条件疑似与「codex 正卡在子进程/网络超时」有关。`Proc.Kill` 只做 `tmux kill-session`（与 grok 同形），杀完不复核进程是否真的死了——已记入 backlog。
- **兜底提问会在空转回合上打转**：模型做完事会正常输出 HANDOFF_STATUS（本轮多次产出 `completed`），但纯对话回合（如只回一句「已收尾。」）没有 trailer、也没有新提交，就被兜底当成提问上报，需要审核者再答一轮。行为安全（不会静默结束），但 UX 上会多绕一圈。

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
- **不给联网操作加任何门**：§2.2 已定放开；若将来要收，正确的做法是给 `commandActions` 加网络
  维度判据（与 B23/B27 的判据改造同批），不是把 `networkAccess` 翻回 false 制造哑失败
- **不做 codex 的 execpolicy `.rules` / granular 审批档**：`on-request` 已满足本期目标
- **不把 codex 登记为审批者执行者**（分级审批链第 1 层）：与本期目标无关
- **不做任务级 `CODEX_HOME`**：§1.3 已定，若将来要在共享机器上跑，再单独立项
- **不改 `wait` 的事件游标语义**（B22）：与本 adapter 无关
