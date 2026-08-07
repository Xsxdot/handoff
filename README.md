# handoff

把实现计划派发给独立 executor 执行、由交互式审核者（Claude Code 会话）通过 `wait`/`reply` 被唤醒并审核修改的两人协作工具：审核者写 plan → `handoff dispatch` 派发给 agentd（本机或远程，Tailscale 内网直连）→ executor 在 tmux 内跑 `opencode serve` 执行（权限门/提问经 SSE 转成工单唤醒审核者）→ 审核通过 `handoff done` 归档。全程无中心 server、无 hooks、无 MCP：断网不丢事件，审核者会话崩溃可凭两条命令完整恢复现场。

## 架构

```
本机（Mac，随时断网）                     executor 所在机（本机或远程，常在线）
┌─────────────────────┐                ┌──────────────────────────────┐
│ 交互式 Claude Code   │                │ handoff agentd（WS listener） │
│  （审核者）          │   WebSocket    │  ├─ 任务/事件存储（SQLite）    │
│         │           │ ◄────直连────► │  ├─ executor adapter          │
│  handoff wait（后台） │   本机主动拨号  │  │   └─ opencode serve（tmux）  │
│  handoff reply/...   │                │  │       ↑ SSE 事件 / HTTP API │
└─────────────────────┘                │  桌面终端窗口 tmux attach ↑    │
        │                              └──────────────────────────────┘
     用户本人（危险操作/需求取舍升级）
```

- **handoff CLI**：`dispatch` / `wait` / `reply` / `continue` / `diff` / `fetch` / `run` / `tasks` / `attach` / `done`，只与 agentd 的 HTTP/WS 端口通信，不直接碰 executor 或工作区。
- **handoff agentd**：任务状态机、事件持久化、executor 生命周期（tmux 内拉起/续接/回收）、git 工作区操作（建分支、取 diff）。`--target local` 场景由 CLI 直连本机 agentd。
- **executor 挂载**：agentd 通过 opencode server 的 HTTP API + SSE 事件流对接（`POST /session`、`prompt_async`、`/event` SSE）；权限等待发生在 opencode 会话内部，与审核者是否在线解耦。
- **审核者**：消费事件、决策审批、回答提问、审核 diff、下发修改指令；不持有任何任务状态（状态全在 agentd）。

## 快速开始

前置：Go 1.26+（按 go.mod 声明；低版本可用 `GOTOOLCHAIN=auto` 自动下载）；executor 机安装 `opencode` 并配好模型凭证（`--executor=fake` 可无需任何依赖演示流程）。

```bash
go build -o handoff . && sudo mv handoff /usr/local/bin/   # 或直接 go run . <子命令>

# 1. 启动 agentd（executor 机；首次运行自动生成 ~/.handoff/config.yaml，内含随机 Token）
handoff agentd --executor=opencode          # 真实执行（默认）；fake 为脚本演示

# 2. 本机配对（远程场景）：把 executor 机 ~/.handoff/config.yaml 里的 token 抄到
#    本机同名文件 targets 段：
#       targets:
#         devbox: {addr: "192.168.x.x:7777", token: "<executor 机的 token>"}

# 3. 派发一个计划（executor 机侧或经 --target 远程；仓库必须工作区干净）
handoff dispatch --repo /path/to/repo plan.md

# 4. 审核者侧典型循环
handoff wait <task-id> --notify             # 挂后台；事件到达输出单行 JSON 并退出
handoff reply <task-id> --ticket <id> --approve                       # 批权限门
handoff reply <task-id> --ticket <id> --deny --reason "不该装这个"     # 拒权限门
handoff reply <task-id> --ticket <id> --answer "用 pgx 不用 gorm"      # 答提问
handoff wait <task-id>                       # 重新挂 wait，循环往复
```

事件到达后：`completed`/`failed` 进审核 → `handoff diff <task>` 看改动、必要时 `fetch`/`run` 取证 → 要改就 `handoff continue <task> "<指令>"`（同一会话续接），审过就 `handoff done <task>`。

## 命令速查

| 命令 | 用途 | 关键参数 |
|------|------|----------|
| `handoff agentd` | 启动 agentd 服务（HTTP + WS） | `--executor=opencode\|fake`（默认 opencode） |
| `handoff dispatch <plan.md>` | 派发计划任务 | `--repo <仓库路径>`（必须）；`--target <name>` |
| `handoff wait <task>` | 阻塞等待下一个可动作事件 | `--notify`（macOS 系统通知兜底） |
| `handoff reply <task>` | 回答一个工单 | `--ticket <id>` + `--approve` / `--deny [--reason]` / `--answer "文本"`（三选一） |
| `handoff tasks` | 列出全部任务（每行一个 JSON） | — |
| `handoff attach <task>` | 输出任务现场快照（任务+待办工单+最近事件） | — |
| `handoff continue <task> "<指令>"` | 向任务续发修改指令（要求 waiting_review） | — |
| `handoff done <task>` | 归档任务并回收 executor（要求 waiting_review） | — |
| `handoff diff <task>` | 输出 git diff + 提交列表（审阅素材） | `--base <分支>`（默认按仓库推导） |
| `handoff fetch <task> <文件>` | 读取仓库内文件（审阅上下文） | — |
| `handoff run <task> <命令...>` | 在任务仓库执行审阅命令（sh -c，10min 超时） | 如 `handoff run T1 go test ./...` |

全局参数：`--agentd http://127.0.0.1:7777`（agentd 地址）、`--target <name>`（按配置 Targets 换算地址与 token）、`--config <path>`（配置文件，默认 `~/.handoff/config.yaml`）。

事件类型：`permission_request` / `question`（`wait` 唤醒，凭 `ticket_id` 用 `reply` 回答）、`completed` / `failed`（进审核）、`stalled`（看门狗：长时间无产出）、`progress`（只入库不唤醒）。

## 审核者会话恢复

一个没有任何前文的新审核者会话，仅凭两条命令完整重建现场（覆盖本机会话崩溃、主动关闭重开、换机器接管三种场景）：

```bash
handoff tasks                      # 列出全部任务及状态
handoff attach <task>              # plan 摘要 + 事件历史 + 未处理挂起项（pending_tickets）
```

处理完挂起项（未答提问、未批权限、待审核的完成事件）后重新挂 `wait` 即恢复循环。

## Troubleshooting

**去哪看日志**

- agentd 日志：`~/.handoff/agentd.log`（JSON 双路输出，stderr 另有文本日志；级别用环境变量 `HANDOFF_LOG_LEVEL=debug` 调低）。
- 任务目录 `~/.handoff/tasks/<task-id>/`：
  - `render.log`：模型回合文本增量（执行实况）；`tmux attach` 也可旁观。
  - `prompt.md` / `opencode.json`：派发给模型的回合制 prompt 与权限配置（edit/bash/webfetch/external_directory 均为 ask）。
  - `serve.json`：serve 连接凭据（端口/密码/tmux 会话名），agentd 重启后凭它重建订阅。
- opencode serve 自身的 stderr：落在 tmux 窗格里（见下），`tmux capture-pane -t handoff-<id8> -p` 可抓取尾部。

**tmux 会话命名规则**：`handoff-<task 前 8 字符>`。`tmux attach -t handoff-<id8>` 直接旁观（甚至介入）executor 实况，`tmux kill-session -t handoff-<id8>` 可人工兜底回收。

**常见问题**

- `wait` 一直不退出 → 看 stderr 日志的「WS 连接断开，等待后重连」：断线退避重连是 `wait` 的常态，重连日志带地址、重连次数与下次退避秒数。
- `dispatch` 报「工作区不干净」→ 任务仓库有未提交/未跟踪改动，提交或 stash 后重试（脏工作区会被污染进任务分支）。
- agentd 重启后任务不丢 → SQLite 落盘 + `RecoverOnStartup` 探活重建 SSE；任务目录 `serve.json` 缺失的任务按「执行器已不在」转 failed 交审核者裁决。
- **SSE 重放风险**：opencode `/event` 在重连时是否重放历史事件尚未经真实样本证实（权限/提问靠 ticket id 幂等去重，但旧 result 重放可能误杀存活的执行器会话）。这是验收级风险，见 `docs/superpowers/e2e-checklist.md` 的 SPIKE-1b 与「水位线应急方案」，上线前必须按清单实测。

## 文档

- 设计文档（架构、协议、错误处理）：`docs/superpowers/specs/2026-08-07-handoff-design.md`
- 真实 opencode e2e 手动验证清单：`docs/superpowers/e2e-checklist.md`
