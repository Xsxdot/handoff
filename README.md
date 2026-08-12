# handoff

把实现计划派发给独立 executor 执行、由交互式审核者（Claude Code 会话）通过 `wait`/`reply` 被唤醒并审核修改的两人协作工具：审核者写 plan → `handoff dispatch` 派发给 agentd（本机或远程，Tailscale 内网直连）→ executor 以独立会话跑 `opencode serve` 执行（权限门/提问经 SSE 转成工单唤醒审核者）→ 审核通过 `handoff done` 归档。全程无中心 server、无 hooks、无 MCP：断网不丢事件，审核者会话崩溃可凭两条命令完整恢复现场。

## 架构

```
本机（Mac，随时断网）                     executor 所在机（本机或远程，常在线）
┌─────────────────────┐                ┌──────────────────────────────┐
│ 交互式 Claude Code   │                │ handoff agentd（WS listener） │
│  （审核者）          │   WebSocket    │  ├─ 任务/事件存储（SQLite）    │
│         │           │ ◄────直连────► │  ├─ executor adapter          │
│  handoff wait（后台） │   本机主动拨号  │  │   ├─ opencode serve（shim）  │
│  handoff reply/...   │                │  │   ├─ claude -p（shim）       │
└─────────────────────┘                │  │   ├─ grok agent serve（shim）│
                                        │  │   └─ codex app-server（shim）│
        │                              │  │       ↑ SSE / stream-json /  │
        │                              │  │         ACP / WS JSON-RPC    │
        │                              │   handoff attach（render 流）↑  │
     用户本人（危险操作/需求取舍升级）      └──────────────────────────────┘
```

- **handoff CLI**：`dispatch` / `wait` / `reply` / `continue` / `diff` / `fetch` / `run` / `tasks` / `show` / `attach` / `done` / `stop` / `pull`，只与 agentd 的 HTTP/WS 端口通信，不直接碰 executor 或工作区。
- **handoff agentd**：任务状态机、事件持久化、executor 生命周期（经 shim 以独立会话拉起/续接/回收）、git 工作区操作（建分支、取 diff）。`--target local` 场景由 CLI 直连本机 agentd。
- **executor 挂载**：四种真实执行者各有传输形态——opencode 走 HTTP API + SSE 事件流（`POST /session`、`prompt_async`、`/event` SSE），claude 走 `claude -p --input-format stream-json` 的进程流（权限门经 handoff 内置 stdio MCP server + unix socket），grok 走 `grok agent serve` 的 ACP（JSON-RPC over WebSocket），codex 走 `codex app-server --listen ws://` 的 WS JSON-RPC 2.0（双向）。权限等待一律发生在 executor 会话内部，与审核者是否在线解耦。
- **审核者**：消费事件、决策审批、回答提问、审核 diff、下发修改指令；不持有任何任务状态（状态全在 agentd）。

## 快速开始

前置：Go 1.26+（按 go.mod 声明；低版本可用 `GOTOOLCHAIN=auto` 自动下载）；executor 机安装 `opencode` 并配好模型凭证（`--executor=fake` 可无需任何依赖演示流程）。`--executor=claude` 需要本机装 `claude` 且已登录（`claude -p "hi"` 能出结果即视为就绪）；`--executor=grok` 需要本机装 `grok` 且已登录（`~/.grok/auth.json` 存在，`grok -p "hi"` 能出结果即视为就绪）。

- **codex**（`--executor codex`）：executor 机需安装 codex-cli 并已 `codex login`。
  建议清理 `~/.codex/AGENTS.md`、`~/.codex/hooks.json`、`~/.codex/config.toml` 的
  `[mcp_servers]`——它们会改变 executor 的干活方式（agentd 启动时会 WARN 提示）。
  这是**约定而非代码保证**：`hooks.json` 没有协议级开关可以关掉。
  `config.toml` 里的 `model` / `sandbox_mode` / `approvals_reviewer` /
  `[sandbox_workspace_write]` **不需要清理**——handoff 全部协议级钉死，压得过它们。
  **executor 机需要代理才能连 OpenAI 时，必须给 codex 单独配 `env` 文件**（`config.yaml`
  的 `env:` 段加 `codex: codex.env`，内容为 `https_proxy` / `http_proxy` / `no_proxy`）：
  agentd 从非交互上下文启动，继承不到 shell 里的代理变量。**漏配的症状极具迷惑性**——
  会话建得起来、回合发得出去、`handoff show` 显示 `running`，但模型一个 token 都不产，
  只有 `serve.log` 里刷 `failed to refresh available models`。

```bash
# 安装（macOS / Linux，amd64 / arm64）
curl -fsSL https://handoff.gosuper.dev/install | bash

# 从源码构建（开发时用）
go build -o handoff . && sudo mv handoff /usr/local/bin/   # 或直接 go run . <子命令>
```

安装脚本把二进制装到 `~/.local/bin/handoff`（免 sudo，可用 `HANDOFF_INSTALL_DIR` 换目录），
校验 sha256 后才落盘，可以反复重跑。**不支持 Windows**——agentd 依赖的进程承载层
Windows 实现尚未完成。

装完用 `handoff version` 确认：首行是版本号（形如 `v0.1.0`）说明装的是 release 构建；
显示 `unknown` 说明这是本地 `go build` 的产物，`handoff upgrade` 不会把它当 release
版本比对。

装完先配一次：

```bash
handoff init                    # 探测本机 executor，问答式写出 config.yaml
handoff service install         # 把 agentd 交给 launchd / systemd 托管
handoff service status          # 看托管状态
```

`handoff init` 可以随时重跑当改配置用——每一问的默认值取当前配置的实际值，
一路回车即原样保持。stdin 不是终端时（例如经管道调起）它一问不问，只写默认配置。

**托管之后 agentd 的形态会变**：它由进程管理器拉起，崩溃或退出都会被自动拉回。
Ctrl-C 停不掉它（会被立刻重新拉起），要真正停掉请用 `handoff service uninstall`，
或 `systemctl stop handoff-agentd` / `launchctl bootout gui/$(id -u)/dev.gosuper.handoff.agentd`。
macOS 上 launchd 对重生有约 10 秒节流，重启期间会有约 10 秒的服务空窗——
执行者不受影响（它们在独立会话里），但期间的 `dispatch` / `reply` 会失败。

### 升级与 skill 分发

升级由**操作者触发**，不再有定时自动更新：一条命令看清本机与全部 target 的版本，
一条命令把所有落后的机器升到同一版本。二进制由本机（审核者机器）下载后推送，
**执行机无需出网**——内网机器、跳板机后面的机器也一样能升。

```bash
handoff upgrade                       # 巡检：列出所有机器的版本与结论（默认行为）
handoff upgrade --now                 # 升级所有落后的机器（含本机；本机排最后）
handoff upgrade --now --target devbox # 只升 devbox 这一台
handoff upgrade --now --force         # 越过「有活跃任务」那道闸
handoff upgrade --rollback            # 本机回滚（<二进制>.prev；不支持 --target）
```

巡检输出示例：

```
最新     v0.1.1
本机     二进制 v0.1.0 · agentd v0.1.0   需要升级
devbox   v0.1.0                          需要升级
prod     v0.1.1                          已是最新
aliyun   够不着（dial tcp 10.0.0.5:7777: connect: connection refused）
```

`--now` 会经接口触发 agentd 重启（远端全部处理完才轮到本机——本机重启会打断操作者
正用着的 agentd）。换版后 CLI 轮询 status 确认新版本上线，超时则如实报「已换版但
新进程未上线」并给出回滚命令，绝不报成「升级完成」。

两道闸在下载前逐台预检，agentd 收到推送时再复检一次：

- **活跃任务**：`running` 与 `waiting_answer` 不为 0 时默认拒绝（`waiting_review`
  不计入——它可能挂几天）。`--force` 可越过，报告里会给可复制的 `--force` 命令行。
- **非托管**：agentd 不是被 launchd / systemd 拉起的，换完没人拉起。**`--force`
  也不越过**——处置是先在该机器上 `handoff service install`。

`update.auto` / `update.interval` 两个配置键**已废弃、不再有任何效果**（agentd 不再
定时查版本）。字段保留只是为了让 v0.1.0 写下的旧配置能继续加载；取非默认值时启动
会打一条 Warn 说明。

新版起来后如果有问题，**不会自动回滚**——旧二进制留在 `<路径>.prev`，用
`handoff upgrade --rollback` 人工换回。自动回滚需要「新版启动后自证健康」的握手
协议，而它挡不住的恰恰是「能起来但有逻辑回归」那一类。

CLI 每天最多在后台查一次版本，有新版时在 **stderr** 打一行提示。它**不会**自动
替换自己。

```bash
# 1. 启动 agentd（executor 机；首次运行自动生成 ~/.handoff/config.yaml，内含随机 Token）
handoff agentd --executor=opencode          # 真实执行（默认）；fake 为脚本演示
handoff agentd --executor=claude            # 用 Claude Code 执行（需本机已登录 claude）
handoff agentd --executor=grok              # 用 grok 执行（需本机已登录 grok）
handoff agentd --executor=codex             # 用 codex 执行（需本机已登录 codex）

# 2. 本机配对（远程场景）：把 executor 机 ~/.handoff/config.yaml 里的 token 抄到
#    本机同名文件 targets 段：
#       targets:
#         devbox: {addr: "192.168.x.x:7777", token: "<executor 机的 token>", user: "<远程 ssh 用户名>"}
#    user 是 pull 用的远程 ssh 用户名：本机用户名与远程一致时可省略，不一致
#    不配它会 Permission denied（pull 无法建立 ssh 连接）。

# 3. 派发一个计划（executor 机侧或经 --target 远程；仓库必须工作区干净）
# 派发的项目由当前目录识别，不需要写路径
handoff dispatch plan.md
handoff dispatch --prompt "把 README 安装命令改成 brew"                        # 无 plan 文件
handoff dispatch --new-worktree --executor opencode --model cheap/model plan.md
handoff dispatch --new-worktree --executor claude plan.md                      # 用 Claude Code 执行
handoff dispatch --target devbox plan.md                                      # 派到开发机（未登记会自动登记）
handoff dispatch --project nova --target devbox plan.md                       # 跨项目：cwd 不是目标项目时
handoff dispatch --no-terminal plan.md                                        # 派发后不弹终端

# 4. 审核者侧典型循环
handoff wait <task-id> --notify             # 一次性：等到下一个可动作事件就退出（派发后等第一个事件适用）
handoff wait --follow <task-id> --timeout 3h  # 持续订阅：每条事件单行输出，任务终结（failed/归档）才退出
handoff reply <task-id> --ticket <id> --approve                       # 批权限门
handoff reply <task-id> --ticket <id> --deny --reason "不该装这个"     # 拒权限门
handoff reply <task-id> --ticket <id> --answer "用 pgx 不用 gorm"      # 答提问
```

`--timeout` 在两种模式下语义不同：一次性模式是「等不到事件的总时长上限」，
`--follow` 模式是「空闲上限」——距上一次收到任何帧（含不唤醒的 progress）的
时长，跨重连累计。**`--follow` 下它必须大于 agentd 的 `stalltimeout`（默认 2h）**，
否则客户端超时会抢在服务端的停滞诊断前面退出；设小了 handoff 会打一条 WARN。

| 退出码 | 含义 |
|--------|------|
| 0 | 一次性：等到事件；`--follow`：任务已终结（failed 或被归档） |
| 124 | 超时（一次性：总时长；`--follow`：空闲） |
| 其他非 0 | 鉴权失败 / 任务不存在 / 连接永久失败 |

事件到达后：`completed`/`failed` 进审核 → `handoff diff <task>` 看改动、必要时 `fetch`/`run` 取证 → 要改就 `handoff continue <task> "<指令>"`（同一会话续接），审过就 `handoff done <task>`。

## 命令速查

| 命令 | 用途 | 关键参数 |
|------|------|----------|
| `handoff agentd` | 启动 agentd 服务（HTTP + WS） | `--executor=opencode\|claude\|grok\|codex\|fake`（默认 opencode） |
| `handoff dispatch [plan.md]` | 派发计划任务（项目由当前目录自动识别） | `--project <名字>`（cwd 不是目标项目时指定）；`--prompt "<指令>"`（prompt-only 派发，与 plan 文件至少其一）；`--name`/`--executor`/`--model`；`--branch <b>\|--new-branch <b>`；`--base <t>`；`--worktree <路径>\|--new-worktree`；`--no-terminal`；`--no-sync-check`；`--allow-dirty` |
| `handoff project add [名字]` | 把当前项目登记到本机（`--target` 时一并登记到那台开发机） | `--target <机器>`（一起登记；那台机器上没有时自动 clone）；`--path <该机器上已有的路径>`（仅与 `--target` 连用） |
| `handoff project ls` | 列出机器上的项目位置（含实际状态） | `--target <机器>` |
| `handoff project rm <名字>` | 注销一条项目位置（只删登记，不删磁盘上的代码） | `--target <机器>` |
| `handoff wait <task>` | 阻塞等待任务的下一个可动作事件（`--follow` 时持续订阅，任务终结才退出） | `--notify`（macOS 系统通知兜底）；`--timeout <时长>`（一次性=总时长上限，`--follow`=空闲上限，默认无限等）；`--follow`（持续订阅，事件单行输出）；`--no-sync` |
| `handoff reply <task>` | 回答一个工单 | `--ticket <id>` + `--approve` / `--deny [--reason]` / `--answer "文本"`（三选一） |
| `handoff tasks` | 列出全部任务（每行一个 JSON） | — |
| `handoff show <task>` | 输出任务现场快照（任务+待办工单+最近事件） | — |
| `handoff attach [task]` | 在终端跟随任务实况（render 流；无参时任务选择列表，非 TTY 打印建议命令） | `--all`（从头播放全部实况）；`--no-follow`（放完当前内容即退出） |
| `handoff continue <task> "<指令>"` | 向任务续发修改指令（要求 waiting_review） | — |
| `handoff done <task>` | 归档任务并回收 executor（要求 waiting_review） | — |
| `handoff stop <task>` | 主动中止任务（停 executor、作废挂起工单，任务落 failed） | — |
| `handoff status [--target <名字>]` | 看这个 agentd 能不能用、是什么版本、有哪些活跃任务及其 executor 是否还活着 | `--json`（reachable 与退出码同源；老 agentd 显示 degraded） |
| `handoff version` | 打印本二进制的版本标识（首行为纯版本号，供脚本比对） | — |
| `handoff init` | 探测本机 executor 并交互式生成/更新配置（幂等，可重跑） | — |
| `handoff service install\|uninstall\|status` | 把 agentd 交给 launchd / systemd 托管 | — |
| `handoff upgrade [--check\|--now\|--force\|--target <名>\|--rollback]` | 巡检 / 升级本机与全部 target，或回滚本机 | `--now`（执行升级）；`--target <名>`（只升那一台）；`--force`（越过活跃任务闸，不越过非托管闸）；`--rollback`（本机回滚，不支持 `--target`） |
| `handoff skill [install]` | 报告 / 重装内嵌 skill 在本机各 agent 的安装状态 | 安装与升级会自动调用，正常不需要手工跑 |
| `handoff pull <task>` | 把远程任务分支同步到本地仓库（只 fetch，不 checkout） | — |
| `handoff resume <task>` | 恢复卡死任务：重投未送达的应答，或对账补回断连窗口丢失的回合终态 | `--force`（对账判不出时仍强制收口到待审核，保住 executor 会话） |
| `handoff diff <task>` | 输出 git diff + 提交列表（审阅素材） | `--base <分支>`（默认按仓库推导） |
| `handoff fetch <task> <文件>` | 读取仓库内文件（审阅上下文） | — |
| `handoff run <task> <命令...>` | 在任务仓库执行审阅命令（sh -c，10min 超时） | 如 `handoff run T1 go test ./...`；**handoff 自有 flag 必须写在任务名之前**——任务名之后的一切（含 `-v`、`--race`）都原样透传给被执行命令，`handoff run T1 --agentd=... go test` 会把 `--agentd=...` 当成 `go test` 的参数 |

全局参数：`--agentd http://127.0.0.1:7777`（agentd 地址）、`--target <name>`（按配置 Targets 换算地址与 token）、`--config <path>`（配置文件，默认 `~/.handoff/config.yaml`）。

事件类型：`permission_request` / `question`（`wait` 唤醒，凭 `ticket_id` 用 `reply` 回答）、`completed` / `failed`（进审核）、`delivery_failed`（应答没送到 executor，执行 `handoff resume` 重投）、`stalled`（看门狗：长时间无产出）、`progress`（只入库不唤醒）、`approver_decision` / `approver_disabled`（分级审批链审计，只入库不唤醒）。

### 浏览器控制台

```bash
handoff console                 # 打开系统浏览器（自动换一次性 ticket）
handoff console --print-url     # 只打印兑换 URL，不打开浏览器
handoff sessions                # 列出已建立的浏览器会话
handoff sessions revoke <id>    # 吊销一个会话（手机丢失时用它）
```

**机制**：`console` 用主令牌向 agentd 换一张 **60 秒、一次性**的 ticket，
浏览器打开该 URL 后 agentd 原子消费它，下发一个 httpOnly cookie 会话（默认 30 天，
滑动续期），此后 `/api` 与 `/ws` 全部路由都用这个 cookie。

**长期凭据永远不进 URL**——URL 里只有那张一次性 ticket。

**Host 白名单**：agentd 只接受 Host 为 `127.0.0.1` / `localhost` / `::1` /
配置的 `listen` 地址的请求。放到域名后面时必须配：

```yaml
web:
  allowed_hosts:
    - handoff.example.com
```

不配的表现是**全部请求 403**，agentd 日志里有 `Host 不在白名单`。

**桌面壳接线契约**（壳内零凭据逻辑）：

1. 探测本机 agentd 是否在监听；
2. 执行 `handoff console --print-url`，**stdout 恰好一行，就是 URL**；
3. `loadURL(那一行)`。

壳不读 `config.yaml`、不碰主令牌、不实现任何鉴权代码。会话过期时页面返回 401，
壳重跑第 2、3 步即可，用户无感。

## 配置（~/.handoff/config.yaml）

二期新增三段（均可省略，用默认值）：

```yaml
approver:                     # 分级审批链的廉价模型审批者
  executor: opencode          # 空=不启用审批链（权限请求直接升级人工审核者）
  model: cheap/model          # 审批者模型；空=用执行者自身默认
  timeout: 60s                # 单次裁决超时，超时按 escalate（fail-closed）
  blacklist:                  # 自定义黑名单正则；命中即跳过审批者直接升级
    - "kubectl .*delete"
executor:                     # dispatch 未显式指定执行者时的缺省
  default: opencode
  model: ""                   # 缺省模型（dispatch --model 可逐任务覆盖）
terminal:                     # dispatch 成功后的终端弹窗
  auto: true                  # darwin 下 osascript 弹 Terminal.app 进实况
sync:                         # 任务结束（completed/failed）后自动同步远程任务分支到本地
  auto: true                  # 关闭后仍可用 handoff pull 手动同步
env:                          # agent 启动时注入的环境变量文件（放 ~/.handoff/env/ 下）
  opencode: dev.env           # 值是纯文件名；未配置的 agent 不注入
  claude: work.env            # 对 claude 执行者同样生效（鉴权/代理等走同一套注入）
repo_root: ""                 # 项目落点根目录；留空则取 <datadir>/repos（首次生成配置时写入本文件）
web:                          # 浏览器控制台 Host 白名单
  allowed_hosts:              # 放行域名（回环地址恒在白名单，无需配置）
    - handoff.example.com
```

`env` 段让 agent 启动时带上代理、私有 registry、额外 PATH 等环境变量。文件放执行机的
`~/.handoff/env/` 下，格式是 dotenv（`KEY=VALUE`，`#` 开头整行注释，`export` 前缀可选，
值支持 `${VAR}` 单层展开，单引号内不展开）：

```sh
export HTTPS_PROXY=http://127.0.0.1:7890
GOPROXY=https://goproxy.cn,direct
PATH=${PATH}:/usr/local/go/bin
```

同一份 env 也会注入审批者（`approver.executor`）—— 否则代理只配半边，审批者连不出去会
静默升级人工审核者。文件不存在或语法错时**拒绝派发**并回显完整路径与行号，不会带病启动。
不支持行内注释（`#` 只在行首生效，因为 URL 里 `#` 合法）。

`repo_root` 是**执行机顶层配置**：它是**自动登记时项目落地的根目录**——首次派发到一台新开发机，
agentd 会在这里落地项目（config 里留空时默认 `<datadir>/repos`，首次生成配置时写回本文件）。
项目登记是「哪台执行机」的属性，放顶层的语义是「每台执行机自己决定项目放在哪」。自动登记在目标机上的
落点有三种结局：

- 落点不存在 → agentd clone 一份再登记
- 落点已存在且就是本项目 → **直接登记，不重复 clone**（project rm 之后再派发能自动恢复登记，靠的就是这条）
- 落点已存在但不是 git 仓库、或属于另一个项目 → 拒绝并要求人工处置；**agentd 不会自动删除或改名**

因此**想改落点，不能靠「删掉登记再派发」**：那会重新认领老目录（repo_root/名字 还在），落点根本没变。
要换位置请显式 `handoff project add --target <机器> --path <新位置>`，或先把老目录挪走/删掉。

> **claude 执行者的 env 耦合**（2026-08-09 实测）：claude adapter 的任务级 `settings.json`
> 是纯策略文件、**不含任何凭证**——**凭证由 claude 自己经 `--setting-sources user` 从真实
> `~/.claude/settings.json` 读取**（真机派发不带任何凭证 env 即跑通），env 注入（B19）是给
> 代理、自定义 `base_url` 这类**额外**环境用的，不是鉴权的必要条件。env 文件里若设了 `HOME`
> 或 `CLAUDE_CONFIG_DIR`，会连带改变 claude 读哪份用户配置（`--setting-sources user` 的落点）
> 与凭据落盘位置——这是用户自己的显式配置，不拦，只是需要知道它会一并生效。

## 分级审批链

权限请求的三级分流（spec §3）：**第 0 层静态规则**（taskenv 的 bash 模式表，edit 放行/危险命令 ask）→ **第 1 层廉价模型审批者**（黑名单硬规则 + one-shot CLI 裁决，approve 自动放行 / escalate 升级人工）→ **第 2 层人工审核者**（`reply` 裁决）。第 1 层 fail-closed：裁决失败/超时一律升级；同任务连续失败 3 次停用审批链（`approver_disabled` 事件留痕），后续权限直接升级人工。

## 审核者会话恢复

一个没有任何前文的新审核者会话，仅凭两条命令完整重建现场（覆盖本机会话崩溃、主动关闭重开、换机器接管三种场景）：

```bash
handoff tasks                      # 列出全部任务及状态
handoff show <task>                # plan 摘要 + 事件历史 + 未处理挂起项（pending_tickets）
```

处理完挂起项（未答提问、未批权限、待审核的完成事件）后按 state 决定：`running` → follow 订阅继续收新事件；`waiting_review` → 进审核。

> 快照查看是 `handoff show <task>`；`handoff attach [task]` 是在终端跟随任务实况（render 流），无参时在任务列表里选择。二期起两者分离——一期 attach 的语义更名给 show。

## 部署到 systemd

unit 模板见 `deploy/handoff-agentd.service`。安装：

```bash
sudo cp deploy/handoff-agentd.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now handoff-agentd
```

模板里的 `User` 与 `ExecStart` 路径请改成你自己的。

**`KillMode=process` 是硬要求，不是可选优化**。执行者进程由 agentd 经 shim 以独立会话（setsid）拉起，目的是让它活过 agentd 的重启与升级；但 setsid **改不了 cgroup 归属**——cgroup 由 fork 继承。systemd 默认的 `KillMode=control-group` 会在 `systemctl restart` 时向整个 cgroup 发信号，shim 与执行者一并被杀，正在跑的任务全部中断。设为 `process` 后 systemd 只终止 agentd 主进程，执行者继续跑；agentd 重启后靠存活锁探测重新接管。

**没有设 `KillMode=process` 时，agentd 会在启动日志里 WARN**（只提示不阻断，因为用户可能有意让重启即清场）。

## Troubleshooting

**去哪看日志**

- agentd 日志：`~/.handoff/agentd.log`（JSON 双路输出，stderr 另有文本日志；级别用环境变量 `HANDOFF_LOG_LEVEL=debug` 调低）。
- 任务目录 `~/.handoff/tasks/<task-id>/`：
  - `render.log`：模型回合文本增量（执行实况）；`handoff attach` 从它流式读取。
  - `prompt.md` / `opencode.json`：派发给模型的回合制 prompt 与权限配置（edit/bash/webfetch/external_directory 均为 ask）。
  - `proc.json`：执行者连接凭据（shim Handle / 端口 / session_id），agentd 重启后凭它探活与重建订阅。
  - `spec.json`：拉起 shim 的启动描述，**权限 0600 且含完整 env**（可能含凭据，走 env 而非 argv，避免出现在 `ps` 输出里）；任务归档后随任务目录一并清理。
  - `proc.lock`：shim 的存活锁（`prochost.Alive` 的唯一判据；内核在进程死亡时自动释放）。
- 执行者输出落盘 `<taskDir>/serve.log`（或 claude 的 `out.jsonl`/`claude.log`）：serve 退出后仍可读，事后取证一律以落盘文件为准。

**执行者进程的承载与回收**：每个任务由 agentd 经 `prochost.Start` 拉起一个极轻的 shim 进程（`handoff _shim`），shim 持有 `proc.lock`、承载真正执行者并负责收尸（补写退出哨兵）。agentd 重启/崩溃不影响执行者——恢复后凭 `proc.json` 探活重连。回收统一走 `handoff stop`（按进程组 Kill）或任务自然结束；人工兜底可 `kill -9 <shim pid>`（pid 见 `proc.json` 的 `handle.pid`）。

claude 与 grok 与 codex 的承载方式与 opencode 同构：执行者进程由各自 adapter 经 prochost 拉起，实况统一经 `handoff attach` 观看。诊断文件按 executor 对应：claude 是 `out.jsonl`（stdout 事件流）与 `claude.log`（stderr）与 `proc.json`（Handle / session_id / 已消费 offset）；grok 是 `serve.log` 与 `proc.json`（Handle / 端口 / session_id）；codex 是 `serve.log` 与 `proc.json`（Handle / 端口 / threadId）。

> **已知限制（2026-08-09 探针实测）**：claude 执行者的任务级 `settings.json` 采用「`allow` 兜底 + `ask` 收窄」的静态分级，探针确认同文件内任务级 `ask` 压得过 `allow`、且跨来源压得过用户级 `allow`（个人 allowlist 无法绕过任务级收窄），详见 spec §5.4。执行机 claude 的登录态（`ANTHROPIC_API_KEY` 等）存在于 `~/.claude/settings.json` 的 `env` 段，由 claude 自己读取，handoff 不复制这份配置（见上文「claude 执行者的 env 耦合」）；若执行机 claude 未登录，任务会启动失败并转交审核者。

**常见问题**

- `wait` 报错退出 → 先看报错内容：token 未同步（401，`~/.handoff/config.yaml` 与 agentd 的 token 需一致）或任务不存在（1008 policy violation，`handoff tasks` 核对 task-id）会**立即**报错退出、不会无限重试；确认 token 与 task-id 无误后再挂。
- `wait` 一直不退出 → 大概率只是「还没有事件」（正常）：看 stderr 日志的「WS 连接断开，等待后重连」，断线退避重连是 `wait` 的常态，重连日志带地址、重连次数与下次退避秒数；无人值守时可加 `--timeout` 兜底。
- 收到 `delivery_failed` 事件，或 `reply` 返回 502 → 裁决已落库但没送到 executor（executor 半死、调用超时）。此时工单已被消耗、`attach` 看不到挂起项，`reply` 会 404、`continue`/`done` 会 409——执行 `handoff resume <task>` 重投：executor 还在就继续执行，确已不在则任务转交审核（之后可 `continue` 重派或 `done` 归档）。该命令幂等，已送达的应答不会重复投递。
- 任务冻死在 `running`、`attach` 能看到模型已经干完 → **agentd 与 executor 断连窗口内回合已完结、终态事件永久丢失（B38）**。`handoff resume <task>` 会做会话对账，把丢失的回合终态补回事件流，任务按补回的事件自然迁移（`result`→`waiting_review`、`question`→`waiting_answer`），此后可正常 `continue`/`reply`。对账判不出（executor 不支持对账 / 回合确实还在忙 / 查询失败）时加 `--force`：把任务强制收口到 `waiting_review` 并留下「人工强制、未经 executor 确认」的事件——它**保住 executor 会话**，与 `handoff stop`（杀会话、落 failed）根本不同。
- `dispatch` 报「工作区不干净」→ 任务仓库有未提交/未跟踪改动，提交或 stash 后重试（脏工作区会被污染进任务分支）。
- agentd 重启后任务不丢 → SQLite 落盘 + `RecoverOnStartup` 探活重建 SSE；任务目录 `serve.json` 缺失的任务按「执行器已不在」转 failed 交审核者裁决。
- **grok 执行者会读到你的 Claude Code 个人配置**：grok 无视 `GROK_HOME`，仍从真实 HOME 读 `~/.claude/settings.local.json` 的权限规则与 `~/.claude/skills`。handoff 写入任务级 `ask` 规则可以压过其中的 `allow`（grok 的求值是 `deny` > `ask` > `allow` 跨源生效），危险模式表仍然有效；但「handoff 没枚举、而你个人 allow 了」的操作会被静默放行。agentd 侧的硬黑名单是独立兜底。
- **grok 任务断连即失败**：ACP 的权限是随连接存续的阻塞请求，连接断开后未决的授权请求不会被重发。handoff 选择立刻转 failed 交审核者（可 `continue` 重开一轮），而不是假装恢复成功留下一个静止的任务。
- **codex 复用你执行机的全局 codex 环境**：`~/.codex/AGENTS.md`、`~/.codex/hooks.json`、`config.toml` 的 `[mcp_servers]` 都会改变 executor 的干活方式（`hooks.json` 没有协议级开关能关掉，只能清理——agentd 以 codex 为缺省执行者启动时会对这三样打 WARN）。安全档位由 handoff 协议级钉死（`on-request` + OS 沙箱 + 每回合重钉），不依赖开发机干净；详见下方「codex 与其它 executor 的差异」。
- **codex 任务断连即失败**：权限请求随 WS 连接存续，连接断开后未决请求不会被重发。handoff 选择立刻转 failed 交审核者，与 grok 同策略。
- **SSE 重放风险**：opencode `/event` 在重连时是否重放历史事件尚未经真实样本证实（权限/提问靠 ticket id 幂等去重，但旧 result 重放可能误杀存活的执行器会话）。这是验收级风险，见 `docs/superpowers/e2e-checklist.md` 的 SPIKE-1b 与「水位线应急方案」，上线前必须按清单实测。

## 执行者差异

`--executor` 可选 `opencode`（默认）/ `claude` / `grok` / `codex` / `fake`。fake 为脚本演示，其余四个都是真实执行。

**claude 与 opencode 的差异**

- **传输形态是进程流**：`claude -p --input-format stream-json --output-format stream-json` 在 tmux 里长驻，指令经 `in.fifo` 写入、事件按 offset 增量读 `out.jsonl`。opencode 走 HTTP + SSE。
- **权限门经 MCP 桥接**：claude 的 `--permission-prompt-tool` 指向 handoff 内置的 stdio MCP server（`handoff permission-mcp` 子命令），它再经 unix socket 把裁决请求转给 agentd。opencode 的权限直接来自 SSE 事件。
- **任务级策略是静态分级**：`<taskDir>/settings.json` 用「`allow` 兜底 + `ask` 收窄」，是**纯策略文件、不含任何凭证**（鉴权走 env 注入）；详见下方已知限制。
- **模型来源**：`dispatch --model` 经 `claude --model <名>` 透传（为空则用 claude 自身默认）；鉴权则完全由执行机的登录态提供，handoff 只经 env 注入传递，不复制凭证。

**grok 与 opencode 的差异**

- **grok 的传输形态是 ACP**：`grok agent serve` 在 tmux 里长驻，handoff 经 WebSocket 跑 Agent Client Protocol（JSON-RPC 双向）。opencode 走 HTTP + SSE。
- **grok 的模型来源**：任务级模型（`dispatch --model`）写入任务级 `GROK_HOME` 的 `config.toml` 的 `[models].default`；为空则用 grok 自身默认。opencode 由 `~/.config/opencode/` 提供模型配置。
- **grok 的回合边界**：是 `session/prompt` 的响应（`stopReason`），而非 opencode 从 idle 事件推断——handoff 不需要那套 idle 去抖与竞态处理。
- **grok 的权限门**：`session/request_permission` 是阻塞式 JSON-RPC 请求，应答必须带原请求 id 回发（handoff 用挂起表暂存 toolCallId→id）。
- **grok 的推理流**（`agent_thought_chunk`）与工具调用只进 `render.log`、不进回合正文——避免污染 `{"ask":…}` trailer 的解析。
- **任务环境**：`<taskDir>/grokhome/` 里是任务级 `config.toml`（钉死 `permission_mode=default`，用户真实配置的 always-approve 不会带进来）；`auth.json` 软链指向真实 `~/.grok/auth.json`——grok 刷新时会把软链替换成普通文件，看门狗每 30 秒巡检一次，按账号键比 `expires_at` 把严格更新的条目原子写回权威副本并复位软链。

**codex 与其它 executor 的差异**

- **传输形态是 app-server WS**：`codex app-server --listen ws://127.0.0.1:<port>` 在 tmux 里长驻，handoff 经 WebSocket 跑 JSON-RPC 2.0 双向协议（`thread/start`、`turn/start`、`item/*` 通知）。`turn/start` 是**异步**的（立即返回 `inProgress`，终态在 `turn/completed` 通知里），回合边界由通知驱动。
- **进程形态与探活**：`serve.json` 只有 tmux 会话 / 端口 / threadId，**没有 secret**（`--listen ws://` 不带鉴权）；存活判据是 TCP 连通（纯 WS 无 HTTP 面可探），真正健康信号是 WS 连接自身的死亡。
- **权限行为差异（选 `on-request` 让 OS 沙箱当筛子）**：工作区内的操作（含 `rm -rf`）由沙箱自动放行、**不进黑名单**（删的是本任务分支自己的成果，git 可救）；只有沙箱边界判「逃逸」的请求才升级审批。**联网操作全程不经过任何人**——`networkAccess` 按用户决定设为 true，`curl … | sh`、`npm install` 零工单直接执行；同样的命令在 claude/grok 上会走 Bash 黑名单与三级审批链。
- **凭据形态（与用户本人共用同一份 `~/.codex` 登录态）**：令牌刷新由 codex 自己完成，不存在 B26 那类「任务目录里困住一份新凭据」的窗口；代价是 executor 继承开发机的 codex 全局环境（见上文已知限制）。
- **配置隔离不靠 home**：codex adapter **不做任务级 home、不写任何 config 文件**——安全档位全部协议级下发且每回合重钉（`sandboxPolicy` / `approvalPolicy` / `approvalsReviewer` / `cwd`），与 thread 的历史状态和恢复路径无关。
- **恢复能力更强**：rollout 落在用户级 `~/.codex/sessions/**`，agentd 重启、甚至 app-server 进程重启后 thread 都还在盘上；冷恢复比 grok 少一段「修凭据软链」。

## 文档

- 设计文档（架构、协议、错误处理）：`docs/superpowers/specs/2026-08-07-handoff-design.md`
- 真实 opencode e2e 手动验证清单：`docs/superpowers/e2e-checklist.md`
- **给 AI 审核者的使用 skill**：`skills/handoff/SKILL.md`——审核者回路（dispatch → wait → reply → diff → continue/done）、状态机硬约束与排障。skill **内嵌在二进制里**，版本与二进制一致、不可能漂移：一行安装装完自动同步，`handoff upgrade --now` 换版后也会自动同步，日常不需要手工管。要查状态或重装用 `handoff skill` / `handoff skill install`（开发时用 `go run . skill install`）。
