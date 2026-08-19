<p align="center">
  <img src="docs/assets/handoff-mark.svg" width="128" alt="handoff 项目标志">
</p>

<h1 align="center">handoff</h1>

<p align="center">
  <a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 许可证"></a>
</p>

<p align="center"><strong>把实现计划派发给另一个 AI 执行，你只负责审。</strong></p>

handoff 是一个纯 CLI 的两角色协作工具：你（或任意 coding agent 会话——Claude Code、opencode、grok、codex 都行）扮演**协调者**——写计划、派发任务、裁决权限、审核改动；**executor**（opencode / Claude Code / grok / codex）在独立会话里真正干活，可以在本机，也可以在网络可达的任何一台开发机上（见「连接远程执行机」）。

```
写 plan → handoff dispatch → executor 独立执行
                ↑                    │
        reply 裁决/回答 ←── 权限门/提问 唤醒你
                │                    │
        handoff diff 审核 ←──── 一轮干完
                │
   不满意 continue 续改 / 满意 done 归档
```

**为什么不直接开个终端让 AI 跑？**

- **执行与审核分离**：executor 在独立会话、独立分支（可选独立 worktree）里干活，危险操作被权限门拦下来问你，你批一条它才动一条。
- **断网不丢现场**：所有状态与事件持久化在执行机的 agentd（SQLite），你的会话崩溃、合盖断网、换一台电脑，两条命令就能完整接管现场。
- **远程算力**：笔记本上写计划，派发到常开机的工作站执行；代码经 git 走，改动经 `handoff pull` 回来。
- **无中心依赖**：没有中心 server、没有 hooks、没有 MCP 配置。两台机器 + 一条 WebSocket 直连。

这套流程经过实测检验：**本项目自身除第一期外的全部功能，都是由 Claude Code 作为协调者、经 handoff 派发给 opencode 开发完成的**——你现在看到的代码就是它自己的产物。

## 安装

macOS / Linux（amd64 / arm64）：

```bash
curl -fsSL https://handoff.gosuper.dev/install | bash
```

Windows（amd64 / arm64，PowerShell）：

```powershell
irm https://handoff.gosuper.dev/install.ps1 | iex
```

**Windows 上 handoff 只能当协调者**——派发、审阅、裁决权限、`upgrade` 升远端都可以，
但本机不能当执行机：agentd 依赖的进程承载层在非 unix 平台尚未实现（backlog B37）。
派发目标必须是一台 macOS 或 Linux 执行机；想拿 Windows 机器当执行机，建议装 WSL2，
在里面按 Linux 流程装 handoff 与 executor（协调机连 WSL2 需要把 agentd 端口从 Windows
宿主转发进去，或让 WSL2 直接加入 Tailscale 这类虚拟组网）。另外 `wait --notify` 的
桌面通知只有 macOS 有，Windows 上唤醒通道是 `wait` 的 stdout。

脚本把二进制装到 `~/.local/bin/handoff`（Windows 是 `%LOCALAPPDATA%\Programs\handoff\handoff.exe`），
免 sudo / 免管理员，`HANDOFF_INSTALL_DIR` 可换目录，校验 sha256 后才落盘，可反复重跑。装完确认：

```bash
handoff version
```

首行是形如 `v0.1.0` 的版本号即为 release 构建；显示 `unknown` 说明是本地 `go build` 产物（`handoff upgrade` 不会把它当 release 比对）。

从源码构建需要 Go 1.26+（低版本可 `GOTOOLCHAIN=auto` 自动下载）：

```bash
go build -o handoff . && sudo mv handoff /usr/local/bin/
```

## 快速开始（本机 5 分钟）

**1. 初始化配置。** `handoff init` 探测本机装了哪些 executor，问答式生成 `~/.handoff/config.yaml`（含随机 token）。随时可重跑当改配置用——每一问的默认值就是当前配置值，一路回车即原样保持。

```bash
handoff init
```

**2. 托管 agentd。** agentd 是常驻服务（任务状态机 + executor 生命周期管理），交给 launchd / systemd 托管，崩溃自动拉起、开机自启：

```bash
handoff service install
handoff service status
```

`init` 选了执行机角色时会顺带问你要不要装，答 y 即就地装好。**不托管的 agentd 重启后不会自己回来**，而且它的 PATH 取决于启动它的那个 shell——「重启后第一次派发报 executor 未安装」多半是这个原因。托管后 Ctrl-C 停不掉它（会被自动拉回），要停用 `handoff service uninstall`。

**只当协调机时不需要本机 agentd**：派发、`wait`、`reply`、`diff`、`attach` 全部直连目标机的 agentd。首次给一个新项目派发时，CLI 会顺带把项目也登记到本机一份（用于 `handoff project ls` 的本机项目树）；本机没有 agentd 时这一跳自动跳过并提示，不影响派发本身。

**3. 派发第一个任务。** 在你的项目目录里（工作区必须干净）：

```bash
handoff dispatch --prompt "把 README 里的安装命令改成 brew"   # 不写 plan 文件的小任务
handoff dispatch --new-worktree plan.md                      # 正式计划，独立 worktree 执行
```

stdout 第一行是任务 JSON，`.id` 就是后续所有命令用的 `<task>`（完整 UUID，不支持短 id）。

**4. 等事件、做裁决。**

```bash
handoff wait <task> --notify              # 阻塞到下一个需要你的事件（macOS 弹通知）
handoff reply <task> --ticket <id> --approve                      # 批权限
handoff reply <task> --ticket <id> --deny --reason "别装全局包"    # 拒权限（务必带理由）
handoff reply <task> --ticket <id> --answer "用 pgx 不用 gorm"     # 答提问
```

**5. 审核收尾。** 收到 `completed` 后任务进入待审核：

```bash
handoff diff <task>                       # git diff + 提交列表
handoff run <task> go test ./...          # 在任务仓库里跑验证命令
handoff continue <task> "重试次数改成 3"    # 不满意：同一会话续改，上下文保留
handoff done <task> --note "已验收"        # 满意：归档并回收 executor
```

想实时看 executor 在干什么，任何时候 `handoff attach <task>`。

> 协调者是 AI 会话时，不需要记这些：安装时已自动装好 handoff skill（Claude Code / opencode / grok / codex 四家都装），AI 会按 skill 里的纪律驱动整个回路。一处能力差异：Claude Code 与 grok 有后台任务唤醒机制，可挂 `wait --follow` 长订阅；opencode 与 codex 没有，skill 会引导它们退回前台逐轮 `wait` 阻塞等待。

## 连接远程执行机

协调机与执行机之间是一条 WebSocket 直连（协调机主动拨号），只要求**协调机能访问到执行机的 agentd 端口**。连通方式按环境选：

- **同一局域网 / 内网**：直接连，`targets` 里写内网 IP 即可。
- **跨网络**：用 Tailscale、WireGuard 这类组网工具把两台机器拉进同一张虚拟内网，`targets` 里写虚拟网卡 IP。
- **云服务器中转连接**：即将推出——届时无法组网的两台机器也能经中转互联。

执行机的 `listen` 分三档：

- **`127.0.0.1:7777`（默认）**：仅本机。本机自用保持默认。
- **单网卡 IP（如 Tailscale 的 `100.x.y.z:7777`）**：只把 agentd 暴露给这一块网卡，安全面比 `0.0.0.0` 小。agentd 会自动追加一个 `127.0.0.1:同端口` 的辅助监听，本机命令始终走 loopback，不随网卡状态起伏。已知限制：该 IP 不在时（组网工具掉线期间重启、开机早于组网工具）agentd 起不来，托管形态下由 launchd/systemd 反复拉起，等 IP 回来自动就绪。
- **`0.0.0.0:7777`**：全网卡，接受任意网卡方向的远程派发。

**安全红线：把 agentd 暴露到网卡上（后两档）之前，确认这台机器没有直接暴露在公网。** agentd 是明文 HTTP/WS + Bearer token 鉴权、没有 TLS：公网上 token 在传输中可被截获，而拿到 token 就等于能在执行机上派发任意代码执行。家庭/办公内网（NAT 之后）或虚拟组网环境里开 `0.0.0.0` 是预期用法；带公网 IP 的云主机现阶段不要当执行机（或用防火墙把端口收窄到内网/虚拟网段），等云中转连接推出后再上。

## 远程执行机

把活派到另一台机器，三步配好：

**1. 执行机**：装 handoff、装 executor（如 opencode）并配好模型凭证，然后 `handoff init` + `handoff service install`。

**2. 本机配对**：把执行机 `~/.handoff/config.yaml` 里的 token 抄到本机同名文件的 `targets` 段：

```yaml
targets:
  devbox:
    addr: "192.168.x.x:7777"
    token: "<执行机的 token>"
    user: "<远程 ssh 用户名>"    # 与本机用户名一致时可省略；pull 走 ssh 要用它
```

**3. 派发**：

```bash
git push                                  # 远程派发前必须 push——handoff 不传代码，代码经 git 走
handoff dispatch --target devbox --new-worktree plan.md
```

第一次派到某台机器时项目自动登记（必要时自动 clone），落点在执行机配置的 `repo_root` 下，不需要告诉 handoff 代码放哪。派发时以你本地 HEAD 为基线开分支：commit 没 push 会被 400 拒发，本地有未提交改动会被拦下提示（确认无关可 `--allow-dirty`）。

任务结束后远程分支自动同步回本地仓库（`sync.auto`，也可 `handoff pull <task>` 手动）；**只 fetch 不合并**——合并进主线是你的审核决定。

## 命令速查

| 命令 | 用途 | 关键参数 |
|------|------|----------|
| `handoff init` | 探测 executor、交互式生成/更新配置（幂等） | — |
| `handoff service install\|uninstall\|status` | agentd 交给 launchd / systemd 托管 | — |
| `handoff agentd` | 前台启动 agentd（开发/调试用，日常走 service） | `--executor=opencode\|claude\|grok\|codex\|fake`（默认 opencode） |
| `handoff dispatch [plan.md]` | 派发任务（项目由当前目录识别） | `--prompt "<指令>"`（与 plan 文件至少其一）；`--target <机器>`；`--executor`/`--model`/`--name`；`--branch\|--new-branch <b>`；`--base <t>`；`--worktree <路径>\|--new-worktree`；`--allow-dirty`；`--no-sync-check`；`--no-terminal` |
| `handoff wait <task>` | 阻塞等下一个需要你的事件 | `--follow`(持续订阅到任务终结)；`--notify`；`--timeout <时长>`；`--no-sync` |
| `handoff reply <task>` | 回答工单 | `--ticket <id>` + `--approve` / `--deny [--reason]` / `--answer "文本"` 三选一 |
| `handoff diff <task>` | git diff + 提交列表（默认用任务自己的基线提交，没有才退回仓库默认分支） | `--base <基准>` |
| `handoff fetch <task> <文件>` | 读任务仓库内单个文件 | — |
| `handoff run <task> <命令...>` | 在任务仓库执行审阅命令（sh -c，10min 超时；单参数按 shell 命令，多参数逐个 shell 转义） | handoff 自有 flag 必须在任务名**之前** |
| `handoff continue <task> "<指令>"` | 续发修改指令（要求待审核状态，同一会话续接） | — |
| `handoff done <task>` | 归档任务并回收 executor（要求待审核状态） | `--note "<说明>"` |
| `handoff stop <task>` | 主动中止（停 executor、作废工单、任务落 failed） | — |
| `handoff tasks` | 列出全部任务（每行一个 JSON） | — |
| `handoff show <task>` | 任务现场快照（任务 + 待办工单 + 最近事件） | — |
| `handoff attach [task]` | 终端跟随任务实况（无参出选择列表） | `--all`（从头播放）；`--no-follow` |
| `handoff resume <task>` | 恢复卡死任务：重投未送达应答 / 补回丢失的回合终态 | `--force`（对账判不出时强制收口到待审核） |
| `handoff pull <task>` | 远程任务分支同步到本地（只 fetch 不 checkout） | — |
| `handoff project add\|ls\|rm` | 项目登记管理 | `--target <机器>`；add 可 `--path <已有路径>` |
| `handoff reclaim` | 回收终态任务残留的 managed worktree（不删分支） | — |
| `handoff footprint` | 各任务进程占用与本机进程余量 | — |
| `handoff status` | agentd 能不能用、版本、活跃任务与 executor 存活 | `--target <名>`；`--json`；退出码 0=能用 1=够不着 |
| `handoff upgrade` | 巡检/升级本机与全部 target | `--now`；`--target <名>`；`--force`；`--rollback` |
| `handoff version` | 打印版本（首行纯版本号，供脚本比对） | — |
| `handoff skill [install]` | 查看/重装内嵌 AI skill（安装升级时自动同步，日常无需手工） | — |

全局参数：`--agentd http://127.0.0.1:7777`（agentd 地址）、`--target <name>`（按配置换算地址与 token）、`--config <path>`（默认 `~/.handoff/config.yaml`）。

## 任务状态与事件

任务状态机：`pending` → `running` → （`waiting_answer` ⇄ `running`）→ `waiting_review` → 归档（`completed`）。**回合以失败收尾也进 `waiting_review`**——executor 会话还在、上下文完整，可以 `continue` 续接重试；只有 `stop` 或 executor 启动失败等才落 `failed`（终态，想继续就重新 dispatch）。**`continue` 和 `done` 都要求 `waiting_review`**，状态不符返回 409。不确定就先 `handoff show`。

`wait` 会被这些事件唤醒：

| 事件 | 含义 | 处置 |
|------|------|------|
| `permission_request` | executor 请求授权 | `reply --approve` / `--deny --reason` |
| `question` | executor 有需求取舍要问 | `reply --answer` |
| `completed` / `failed` | 一轮干完 / 一轮以失败收尾 | 都进审核：`diff` 取证后 `continue` 续改或 `done` 归档 |
| `archived` | 任务被 done 归档（payload 带 note） | 任务真正结束的信号 |
| `delivery_failed` | 裁决落库了但没送到 executor | `handoff resume <task>` 重投 |
| `stalled` | 看门狗：长时间无产出 | `attach`/`show` 判断是长跑还是卡死 |

`progress`、审批链审计事件只入库不唤醒，`show` 的事件历史里可见。

## 配置参考（~/.handoff/config.yaml）

首次运行自动生成（含随机 token）。除 `listen` / `token` 外各段均可省略：

```yaml
listen: "127.0.0.1:7777"      # 监听三档：仅本机（默认）/ 单网卡 IP（自动补 loopback 辅助监听）/ "0.0.0.0:7777" 全网卡（见「连接远程执行机」）
token: "<首次运行自动生成>"     # 访问令牌；配对时抄到协调机的 targets 段

executor:                     # dispatch 未显式指定时的默认执行者
  default: opencode
  model: ""                   # 默认模型（--model 可逐任务覆盖）

approver:                     # 分级审批链：廉价模型先筛一遍权限请求
  executor: opencode          # 空 = 不启用（权限请求直接升级人工）
  model: cheap/model
  timeout: 60s                # 裁决超时按升级处理（fail-closed）
  blacklist:                  # 命中即跳过审批者直接升级人工
    - "kubectl .*delete"

env:                          # executor 启动时注入的环境变量文件（放 ~/.handoff/env/）
  opencode: dev.env           # 值是纯文件名；未配置的 executor 不注入
  codex: codex.env

terminal:
  auto: false                 # true = macOS 派发成功后弹 Terminal 进实况（--no-terminal 逐次关）

sync:
  auto: true                  # 任务结束后自动同步远程任务分支到本地

repo_root: ""                 # 自动登记时项目落地的根目录；空 = <datadir>/repos
path_dirs: ["/opt/tools/bin"] # 额外可执行目录；agentd 已自动合并登录 shell PATH 与常见安装目录，兜不住才加
proxy: ""                     # handoff 自身出网代理；空 = 沿用 HTTPS_PROXY 等环境变量
                              # 支持 http:// https:// socks5:// socks5h://

proc_fence:                   # executor 进程围栏（RLIMIT_NPROC），防失控 fork 拖垮整机
  disabled: false             # 逃生开关，正常别动
  reserve_ratio: 0.1          # 给 agentd/sshd 留的救护车道比例
```

**`proxy` 段**：给 **handoff 自己**的出网配代理。作用范围只有两处——更新链路
（查 release、下资产）与 agentd 的 `git clone` / `git fetch`。配置它比设
`HTTPS_PROXY` 环境变量更实用的地方在于：agentd 由 launchd / systemd 拉起，
**读不到你终端的 shell env**，而这台机器上本来就有一份 `config.yaml`。

三条边界值得记住：

- **不作用于协调者 ↔ agentd 那条链路**。那是 LAN / 虚拟组网 / loopback 地址，
  代理化只会给它凭空加失败模式。
- **不作用于 executor**。executor 的出网走 `env` 段（下一节），两者故障域不交叉
  ——代理挂了只影响升级，不影响任务执行。
- **SSH 协议的 git remote 吃不到它**。git 的 `http.proxy` 只对 `http(s)://` 的
  remote 生效。如果自动登记要 clone 的仓库是 `git@github.com:...`，改用 HTTPS
  地址即可（`git config --global url."https://github.com/".insteadOf git@github.com:`）。

值写错（scheme 不在支持范围、缺主机）时 agentd **启动就会失败并说明原因**——
这是刻意的：后台更新检查那条路径失败时是静默跳过的，坏代理若不在启动期拦下，
表现就是"什么都不发生"，可以数月无人察觉。

**`env` 段**：给 executor 带上代理、私有 registry 等环境变量。配置里按 executor 名写**纯文件名**（如上例 `opencode: dev.env`，未配置的 executor 不注入），文件本体放**执行机**的 `~/.handoff/env/` 下，dotenv 格式：

```sh
# ~/.handoff/env/dev.env —— 行首 # 是注释；不支持行内注释（URL 里 # 合法）
export HTTPS_PROXY=http://127.0.0.1:7890   # export 前缀可选
GOPROXY=https://goproxy.cn,direct
PATH=${PATH}:/usr/local/go/bin             # ${VAR} 单层展开，单引号内不展开
```

同一份 env 也会注入审批者（`approver`）——否则代理只配半边，审批者连不出去会静默升级人工。文件不存在或语法错会**拒绝派发**并回显完整路径与行号，不会带病启动。

**权限分级**：权限请求走三级分流——静态规则（编辑放行、危险命令必问）→ 廉价模型审批者（配置了 `approver` 时；黑名单命中或裁决失败一律升级）→ 人工协调者 `reply` 裁决。审批者连续失败 3 次自动停用，之后直接升级人工。同一任务内一字不差的同一权限请求，人工批过一次后自动复用（`permission_reuse` 事件留痕），不会反复来问。

写到任务范围外的落点——包括 `echo x > ~/.zshrc` 这类 shell 重定向落点——一律升级人工，不经审批者。

## 各 executor 须知

`--executor` 可选 `opencode`（默认）/ `claude` / `grok` / `codex` / `fake`（无依赖的脚本演示）。就绪判据与注意事项：

- **opencode**：执行机安装 [opencode](https://opencode.ai/go?ref=3AMC8DKNGP) 并配好模型凭证（这是邀请链接：经它注册，你我各得 $5 额度）。
- **claude**（Claude Code）：执行机已登录（`claude -p "hi"` 能出结果）。任务级权限策略是纯策略文件、不含凭证；凭证由 claude 自己从用户配置读。
- **grok**：执行机已登录（`grok -p "hi"` 能出结果）。注意 grok 会读到执行机上的 Claude Code 个人配置（`~/.claude/settings.local.json` 的 allow 规则），handoff 的任务级 ask 能压过其中大部分，但个人 allow 且 handoff 未枚举的操作会被放行；断连时未决权限请求不会重发，任务直接落 failed（可 `continue` 重开）。
- **codex**：执行机已 `codex login`。三点须知：
  - 建议清理 `~/.codex/AGENTS.md`、`~/.codex/hooks.json`、`config.toml` 的 `[mcp_servers]`——它们会改变 executor 行为（agentd 启动时会 WARN）。`config.toml` 里的 `model` / `sandbox_mode` 等**不用**清理，handoff 协议级钉死、压得过它们。
  - 权限模型不同：工作区内操作（含 `rm -rf`）由 OS 沙箱自动放行不进审批，联网操作按配置放行**不经过任何人**——同样的命令在 claude/grok 上会走审批链。
  - 执行机需代理出网时，**必须**经 `env` 段给 codex 配代理文件——agentd 从非交互上下文启动，继承不到 shell 的代理变量。漏配的症状很迷惑：会话建得起来、任务显示 running，但模型一个 token 都不产，只有任务目录 `serve.log` 里刷 `failed to refresh available models`。

## 升级

升级由你触发，没有定时自动更新。默认由**执行机自己**去 GitHub 下载：协调者只查一次
版本、下一份几百字节的 `checksums.txt`，把 tag 与 sha256 下发过去。这样一次多机
升级在协调者与执行机之间只走几十字节，而不是每台机器一份 20MB 的二进制——走云
中转时这个差别是决定性的。

完整性由**协调者下发的** sha256 把关：执行机下完资产比对它，本机代理或镜像被
投毒时会当场被抓住（校验和与资产走两条不同的信任路径）。

执行机确实出不了网时用 `--push` 回退到「本机下载后推送」。对端 agentd 版本过旧
（不认自拉）时会**自动**降级推送，不需要你操心。

```bash
handoff upgrade                       # 巡检：列出所有机器的版本与结论
handoff upgrade --now                 # 升级所有落后的机器（远端先、本机最后）
handoff upgrade --now --target devbox # 只升一台
handoff upgrade --rollback            # 本机回滚到 <二进制>.prev
```

两道安全闸：有活跃任务（`running` / `waiting_answer`）的机器默认拒升（`--force` 可越过）；agentd 非托管的机器拒升且 `--force` 也不越过——先在那台机器 `handoff service install`。升级不会自动回滚，旧二进制保留在 `<路径>.prev`。

三个平台都能自更新，Windows 也不例外——替换是「先把旧的改名成 `.prev`，再把新的移进来」，
这正是 Windows 允许对一个正在运行的 exe 做的操作。

CLI 每天最多后台查一次新版本，有更新在 stderr 提示一行，不会自动替换自己。

## 会话恢复

协调者不持有任何状态（状态全在执行机的 agentd），所以会话崩溃、断网、换机器接管都是同一套动作：

```bash
handoff tasks                      # 列出全部任务及状态
handoff show <task>                # 现场快照：状态 + 未处理工单 + 事件历史
```

清完 `pending_tickets` 后按状态办：`running` 继续等事件，`waiting_review` 进审核。

## Troubleshooting

日志都在执行机上：agentd 主日志 `~/.handoff/agentd.log`（`HANDOFF_LOG_LEVEL=debug` 调低级别）；任务目录 `~/.handoff/tasks/<task-id>/` 下有 `render.log`（模型实况，`attach` 读的就是它）、`serve.log`（executor 输出，事后取证以它为准）等。

| 症状 | 原因与处置 |
|------|-----------|
| 命令报 404「任务不存在」 | 传了短 id。所有命令要完整 UUID，`handoff tasks` 里取 |
| `continue` / `done` 报 409 | 任务不在待审核状态。`handoff show` 看真实状态 |
| `reply` 返回 502 / 收到 `delivery_failed` | 裁决落库了但 executor 没收到。`handoff resume <task>` 幂等重投 |
| 任务冻死在 `running` 但 `attach` 可见模型早干完了 | 断连窗口丢了回合终态。`handoff resume <task>` 对账补回；判不出再加 `--force` 收口（保住会话，与 `stop` 杀会话不同） |
| `wait` 一直不返回 | 通常只是还没有事件（正常）；stderr 的断线重连日志也是常态。无人值守加 `--timeout` |
| `wait` 立刻报错退出 | 401 = 两边 token 不一致；1008 = task-id 不存在。修好再挂，重试不会自己好 |
| 远程任务的命令报「task not found」 | 漏了 `--target`，命令打到了本机 agentd。看 stderr 的 `addr=` 确认 |
| `dispatch` 报「工作区不干净」 | 任务仓库有未提交改动，提交或 stash（脏改动会污染进任务分支） |
| `dispatch` 报 400「基线提交在任务仓库中不存在」 | 本地 commit 没 push。`git push` 后重试 |
| 派发报 executor `not found in $PATH`，但你终端里明明能跑 | agentd 的 PATH 与你终端不同。它启动时会自动补全（登录 shell + 常见安装目录），日志搜「已补全 PATH」；还兜不住就把目录写进 `path_dirs` 重启 agentd |
| executor 报 `resource temporarily unavailable` | 撞上进程围栏（防失控 fork 的保护），不是代码 bug。收敛并行即可，报错细节在任务目录 `shim.log` |
| agentd 起不来，报数据目录被锁 | 已有一个 agentd 在跑。复用它；升级要先停旧再起新 |
| `upgrade --now` 报失败，或卡着不动 | 默认走执行机自拉，原因在对端。`handoff status --target <名字>` 看 `update.pull_state`：`stage` 是到哪一步、`error` 是原文（代理连不上 / sha256 不符 / 自检没过）。执行机出不了网就用 `handoff upgrade --now --push` |

**macOS：从 Releases 页面下载后提示「无法打开，因为无法验证开发者」**

发布的 darwin 资产已做 Developer ID 签名与公证，但裸命令行工具无法内嵌公证票据
（Apple 的 stapler 只支持 .app / .dmg / .pkg），首次运行需要联网让系统去 Apple
校验。断网时会被拦下。处置：联网后重试，或手动摘掉隔离标记：

```bash
xattr -d com.apple.quarantine ~/.local/bin/handoff
```

用 `curl | bash` 安装不会遇到这个问题——curl 不打隔离标记。

**Windows：提示「Windows 已保护你的电脑」**

Windows 二进制未做 Authenticode 签名（需另购 OV/EV 证书），SmartScreen 会提示
未知发布者。处置：点「更多信息」→「仍要运行」。可先用 `checksums.txt` 核对
下载物的 sha256 再运行。

## 卸载

```bash
handoff service uninstall
rm ~/.local/bin/handoff
rm -rf ~/.handoff        # 含配置、任务数据与日志，确认不要了再删
```

## 即将推出

- **云服务器中转连接**：无法内网组网的协调机与执行机经云端中转互联。
- **桌面端**：图形界面查看与操作任务，不再只有 CLI。

## 文档

- 设计文档（架构、协议、错误处理）：[docs/superpowers/specs/2026-08-07-handoff-design.md](docs/superpowers/specs/2026-08-07-handoff-design.md)
- 给 AI 协调者的使用 skill：[skills/handoff/SKILL.md](skills/handoff/SKILL.md)——skill 内嵌在二进制里，随安装与升级自动同步到本机各 agent，版本不会漂移
- systemd 手工部署模板：[deploy/handoff-agentd.service](deploy/handoff-agentd.service)（注意模板中 `KillMode=process` 是硬要求：它保证重启 agentd 不杀正在跑的 executor）
- 参与贡献（本地怎么跑、提交前要过哪几道门）：[CONTRIBUTING.md](CONTRIBUTING.md)
- 安全策略与威胁模型（**漏洞请走私密通道，不要开公开 issue**）：[SECURITY.md](SECURITY.md)

## 友情链接

- [Linux Do](https://linux.do) —— 真诚、友善、团结、专业的开发者社区
- [opencode](https://opencode.ai/go?ref=3AMC8DKNGP) —— handoff 的默认 executor；这是邀请链接，经它注册你我各得 $5 额度

## License

[Apache License 2.0](LICENSE)
