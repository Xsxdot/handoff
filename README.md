# handoff

把实现计划派发给独立 executor 执行、由交互式审核者（Claude Code 会话）通过 `wait`/`reply` 被唤醒并审核修改的两人协作工具：审核者写 plan → `handoff dispatch` 派发给 agentd（本机或远程，Tailscale 内网直连）→ executor 在 tmux 内跑 `opencode serve` 执行（权限门/提问经 SSE 转成工单唤醒审核者）→ 审核通过 `handoff done` 归档。全程无中心 server、无 hooks、无 MCP：断网不丢事件，审核者会话崩溃可凭两条命令完整恢复现场。

## 架构

```
本机（Mac，随时断网）                     executor 所在机（本机或远程，常在线）
┌─────────────────────┐                ┌──────────────────────────────┐
│ 交互式 Claude Code   │                │ handoff agentd（WS listener） │
│  （审核者）          │   WebSocket    │  ├─ 任务/事件存储（SQLite）    │
│         │           │ ◄────直连────► │  ├─ executor adapter          │
│  handoff wait（后台） │   本机主动拨号  │  │   ├─ opencode serve（tmux）  │
│  handoff reply/...   │                │  │   └─ claude -p（tmux）       │
└─────────────────────┘                │  │       ↑ SSE 事件 / HTTP API │
        │                              │  桌面终端窗口 tmux attach ↑    │
     用户本人（危险操作/需求取舍升级）      └──────────────────────────────┘
```

- **handoff CLI**：`dispatch` / `wait` / `reply` / `continue` / `diff` / `fetch` / `run` / `tasks` / `show` / `attach` / `done` / `stop` / `pull`，只与 agentd 的 HTTP/WS 端口通信，不直接碰 executor 或工作区。
- **handoff agentd**：任务状态机、事件持久化、executor 生命周期（tmux 内拉起/续接/回收）、git 工作区操作（建分支、取 diff）。`--target local` 场景由 CLI 直连本机 agentd。
- **executor 挂载**：agentd 通过 opencode server 的 HTTP API + SSE 事件流对接（`POST /session`、`prompt_async`、`/event` SSE），或经 `claude -p --input-format stream-json` 的进程流对接（权限门经 handoff 内置 stdio MCP server + unix socket）；权限等待发生在 executor 会话内部，与审核者是否在线解耦。
- **审核者**：消费事件、决策审批、回答提问、审核 diff、下发修改指令；不持有任何任务状态（状态全在 agentd）。

## 快速开始

前置：Go 1.26+（按 go.mod 声明；低版本可用 `GOTOOLCHAIN=auto` 自动下载）；executor 机安装 `opencode` 并配好模型凭证（`--executor=fake` 可无需任何依赖演示流程）。`--executor=claude` 需要本机装 `claude` 且已登录（`claude -p "hi"` 能出结果即视为就绪）。

```bash
go build -o handoff . && sudo mv handoff /usr/local/bin/   # 或直接 go run . <子命令>

# 1. 启动 agentd（executor 机；首次运行自动生成 ~/.handoff/config.yaml，内含随机 Token）
handoff agentd --executor=opencode          # 真实执行（默认）；fake 为脚本演示
handoff agentd --executor=claude            # 用 Claude Code 执行（需本机已登录 claude）

# 2. 本机配对（远程场景）：把 executor 机 ~/.handoff/config.yaml 里的 token 抄到
#    本机同名文件 targets 段：
#       targets:
#         devbox: {addr: "192.168.x.x:7777", token: "<executor 机的 token>", user: "<远程 ssh 用户名>"}
#    user 是远程 attach/pull 的 ssh 用户名：本机用户名与远程一致时可省略，不一致
#    不配它会 Permission denied（attach/pull 无法建立 ssh 连接）。

# 3. 派发一个计划（executor 机侧或经 --target 远程；仓库必须工作区干净）
handoff dispatch --repo /path/to/repo plan.md
handoff dispatch --repo /path/to/repo --prompt "把 README 安装命令改成 brew"   # 无 plan 文件
handoff dispatch --repo /path/to/repo --new-worktree --executor opencode --model cheap/model plan.md
handoff dispatch --repo /path/to/repo --new-worktree --executor claude plan.md              # 用 Claude Code 执行
handoff dispatch --repo /path/to/repo --no-terminal plan.md                    # 派发后不弹终端

# 4. 审核者侧典型循环
handoff wait <task-id> --notify             # 挂后台；事件到达输出单行 JSON 并退出
handoff wait <task-id> --timeout 1h         # 到点报错退出非 0（区别于事件到达的 0）
handoff reply <task-id> --ticket <id> --approve                       # 批权限门
handoff reply <task-id> --ticket <id> --deny --reason "不该装这个"     # 拒权限门
handoff reply <task-id> --ticket <id> --answer "用 pgx 不用 gorm"      # 答提问
handoff wait <task-id>                       # 重新挂 wait，循环往复
```

事件到达后：`completed`/`failed` 进审核 → `handoff diff <task>` 看改动、必要时 `fetch`/`run` 取证 → 要改就 `handoff continue <task> "<指令>"`（同一会话续接），审过就 `handoff done <task>`。

## 命令速查

| 命令 | 用途 | 关键参数 |
|------|------|----------|
| `handoff agentd` | 启动 agentd 服务（HTTP + WS） | `--executor=opencode\|claude\|fake`（默认 opencode） |
| `handoff dispatch [plan.md]` | 派发计划任务 | `--repo <仓库路径>`（必须）；`--prompt "<指令>"`（prompt-only 派发，与 plan 文件至少其一）；`--name`/`--executor`/`--model`；`--branch <b>\|--new-branch <b> [--base <t>]`；`--worktree <路径>\|--new-worktree`；`--no-terminal`（派发后不弹终端实况）；`--no-sync-check`（远程派发时跳过基线校验） |
| `handoff wait <task>` | 阻塞等待下一个可动作事件 | `--notify`（macOS 系统通知兜底）；`--timeout <时长>`（如 `1h`，到点报错退出非 0，默认无限等）；`--no-sync`（任务结束时不自动同步远程任务分支） |
| `handoff reply <task>` | 回答一个工单 | `--ticket <id>` + `--approve` / `--deny [--reason]` / `--answer "文本"`（三选一） |
| `handoff tasks` | 列出全部任务（每行一个 JSON） | — |
| `handoff show <task>` | 输出任务现场快照（任务+待办工单+最近事件） | — |
| `handoff attach [task]` | 进入任务 executor 的 tmux 终端实况（无参时任务选择列表，非 TTY 打印建议命令） | `--target <name>` 远程经 ssh 进入 |
| `handoff continue <task> "<指令>"` | 向任务续发修改指令（要求 waiting_review） | — |
| `handoff done <task>` | 归档任务并回收 executor（要求 waiting_review） | — |
| `handoff stop <task>` | 主动中止任务（停 executor、作废挂起工单，任务落 failed） | — |
| `handoff pull <task>` | 把远程任务分支同步到本地仓库（只 fetch，不 checkout） | — |
| `handoff resume <task>` | 恢复卡死任务：重投未送达 executor 的应答 | — |
| `handoff diff <task>` | 输出 git diff + 提交列表（审阅素材） | `--base <分支>`（默认按仓库推导） |
| `handoff fetch <task> <文件>` | 读取仓库内文件（审阅上下文） | — |
| `handoff run <task> <命令...>` | 在任务仓库执行审阅命令（sh -c，10min 超时） | 如 `handoff run T1 go test ./...`；**handoff 自有 flag 必须写在任务名之前**——任务名之后的一切（含 `-v`、`--race`）都原样透传给被执行命令，`handoff run T1 --agentd=... go test` 会把 `--agentd=...` 当成 `go test` 的参数 |

全局参数：`--agentd http://127.0.0.1:7777`（agentd 地址）、`--target <name>`（按配置 Targets 换算地址与 token）、`--config <path>`（配置文件，默认 `~/.handoff/config.yaml`）。

事件类型：`permission_request` / `question`（`wait` 唤醒，凭 `ticket_id` 用 `reply` 回答）、`completed` / `failed`（进审核）、`delivery_failed`（应答没送到 executor，执行 `handoff resume` 重投）、`stalled`（看门狗：长时间无产出）、`progress`（只入库不唤醒）、`approver_decision` / `approver_disabled`（分级审批链审计，只入库不唤醒）。

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

> **claude 执行者的 env 耦合**（2026-08-09 实测）：claude adapter 的任务级 `settings.json`
> 是纯策略文件、**不含任何凭证**——鉴权走上面的 env 注入（B19），与 opencode 一致。但 env
> 文件里若设了 `HOME` 或 `CLAUDE_CONFIG_DIR`，会连带改变 claude 读哪份用户配置
> （`--setting-sources user` 的落点）与凭据落盘位置——这是用户自己的显式配置，不拦，
> 只是需要知道它会一并生效。

## 分级审批链

权限请求的三级分流（spec §3）：**第 0 层静态规则**（taskenv 的 bash 模式表，edit 放行/危险命令 ask）→ **第 1 层廉价模型审批者**（黑名单硬规则 + one-shot CLI 裁决，approve 自动放行 / escalate 升级人工）→ **第 2 层人工审核者**（`reply` 裁决）。第 1 层 fail-closed：裁决失败/超时一律升级；同任务连续失败 3 次停用审批链（`approver_disabled` 事件留痕），后续权限直接升级人工。

## 审核者会话恢复

一个没有任何前文的新审核者会话，仅凭两条命令完整重建现场（覆盖本机会话崩溃、主动关闭重开、换机器接管三种场景）：

```bash
handoff tasks                      # 列出全部任务及状态
handoff show <task>                # plan 摘要 + 事件历史 + 未处理挂起项（pending_tickets）
```

处理完挂起项（未答提问、未批权限、待审核的完成事件）后重新挂 `wait` 即恢复循环。

> 快照查看是 `handoff show <task>`；`handoff attach [task]` 是进入 executor 终端实况（tmux），无参时在任务列表里选择。二期起两者分离——一期 attach 的语义更名给 show。

## Troubleshooting

**去哪看日志**

- agentd 日志：`~/.handoff/agentd.log`（JSON 双路输出，stderr 另有文本日志；级别用环境变量 `HANDOFF_LOG_LEVEL=debug` 调低）。
- 任务目录 `~/.handoff/tasks/<task-id>/`：
  - `render.log`：模型回合文本增量（执行实况）；`tmux attach` 后第二窗口即 `tail -f` 该文件。
  - `prompt.md` / `opencode.json`：派发给模型的回合制 prompt 与权限配置（edit/bash/webfetch/external_directory 均为 ask）。
  - `serve.json`：serve 连接凭据（端口/密码/tmux 会话名），agentd 重启后凭它重建订阅。
  - `run_serve.sh`：拉起 serve 的启动脚本，**权限 0600 且含明文 serve 密码**（密码走脚本而非 argv，避免出现在 `ps` 输出里）；任务归档后随任务目录一并清理。
- opencode serve 自身的输出：`tee` 落盘 `<taskDir>/serve.log`。tmux 第一窗格实时可见；serve 退出后该窗格关闭，但**会话不会随之销毁**——第二窗口的 `tail -f render.log` 仍吊着会话，adapter 检测到 serve 死亡时会主动 `kill-session` 回收（见 subscribeLoop）。事后取证一律以 `serve.log` 为准。

**tmux 会话命名规则**：`handoff-<task 前 8 字符>`。`tmux attach -t handoff-<id8>` 直接旁观（甚至介入）executor 实况，`tmux kill-session -t handoff-<id8>` 可人工兜底回收。

claude 任务的 tmux 布局与 opencode 同构：窗口 0 是 `claude -p` 的 stream-json 原始输出，窗口 1 是 `tail -f render.log`（模型正文实况）；`handoff attach` 一套命令覆盖两个 executor。claude 任务的诊断文件对应 `claude.log`（stderr，对应 serve.log）与 `claude.json`（恢复凭据：tmux 会话 / session_id / out.jsonl 已消费 offset，对应 serve.json）。

> **已知限制（2026-08-09 探针实测）**：claude 执行者的任务级 `settings.json` 采用「`allow` 兜底 + `ask` 收窄」的静态分级，探针确认同文件内任务级 `ask` 压得过 `allow`、且跨来源压得过用户级 `allow`（个人 allowlist 无法绕过任务级收窄），详见 spec §5.4。执行机 claude 的登录态（`ANTHROPIC_API_KEY` 等）存在于 `~/.claude/settings.json` 的 `env` 段——handoff 不复制这份配置，鉴权一律走 `env` 注入（见上文「claude 执行者的 env 耦合」）；若执行机 claude 未登录，任务会启动失败并转交审核者。

**常见问题**

- `wait` 报错退出 → 先看报错内容：token 未同步（401，`~/.handoff/config.yaml` 与 agentd 的 token 需一致）或任务不存在（1008 policy violation，`handoff tasks` 核对 task-id）会**立即**报错退出、不会无限重试；确认 token 与 task-id 无误后再挂。
- `wait` 一直不退出 → 大概率只是「还没有事件」（正常）：看 stderr 日志的「WS 连接断开，等待后重连」，断线退避重连是 `wait` 的常态，重连日志带地址、重连次数与下次退避秒数；无人值守时可加 `--timeout` 兜底。
- 收到 `delivery_failed` 事件，或 `reply` 返回 502 → 裁决已落库但没送到 executor（executor 半死、调用超时）。此时工单已被消耗、`attach` 看不到挂起项，`reply` 会 404、`continue`/`done` 会 409——执行 `handoff resume <task>` 重投：executor 还在就继续执行，确已不在则任务转交审核（之后可 `continue` 重派或 `done` 归档）。该命令幂等，已送达的应答不会重复投递。
- `dispatch` 报「工作区不干净」→ 任务仓库有未提交/未跟踪改动，提交或 stash 后重试（脏工作区会被污染进任务分支）。
- agentd 重启后任务不丢 → SQLite 落盘 + `RecoverOnStartup` 探活重建 SSE；任务目录 `serve.json` 缺失的任务按「执行器已不在」转 failed 交审核者裁决。
- **SSE 重放风险**：opencode `/event` 在重连时是否重放历史事件尚未经真实样本证实（权限/提问靠 ticket id 幂等去重，但旧 result 重放可能误杀存活的执行器会话）。这是验收级风险，见 `docs/superpowers/e2e-checklist.md` 的 SPIKE-1b 与「水位线应急方案」，上线前必须按清单实测。

## 文档

- 设计文档（架构、协议、错误处理）：`docs/superpowers/specs/2026-08-07-handoff-design.md`
- 真实 opencode e2e 手动验证清单：`docs/superpowers/e2e-checklist.md`
