# Handoff 设计文档

日期：2026-08-07
状态：已与用户确认

## 1. 问题与目标

用户使用 Claude Code（交互式会话，下称「审核者」）通过 writing-plans 产出实现计划后，希望把计划交给另一个 agent（下称「executor」）执行，executor 可能在本机，也可能在远程开发机上。执行期间 executor 会产生权限请求和提问；执行完成后审核者审核结果，所有修改（不分简单复杂）写成指令回发给 executor 继续执行，直到审核通过。

核心难点：

1. **唤醒**：审核者是一个交互式 Claude Code 会话，如何在 executor 需要它（提问/审批/完成）时被可靠唤醒；
2. **断网**：本机（Mac 笔记本）随上下班断网，远程 executor 必须能等待、事件不丢；
3. **可见**：用户希望能直接打开终端看到 executor 的实时运行画面；
4. **恢复**：审核者会话崩溃/被关闭后，新会话必须能完整拉回现场继续。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| 审核者形态 | 用户正在使用的交互式 Claude Code / Claude Desktop 会话 |
| 唤醒机制 | `handoff wait` 阻塞 CLI 挂为后台进程，事件到达即退出，harness 自动唤醒审核者 |
| 网络拓扑 | 无中心 server；agentd 在 executor 所在机器监听 WebSocket，本机 CLI 主动拨号（Tailscale 内网直连），断线指数退避重连 |
| 远程/本机 | 远程是可选项；agentd 位置透明，`--target local` 时在本机跑，架构同构 |
| executor 挂载方式 | 全 CLI，不用 MCP：权限走 PreToolUse hook + `handoff gate`，提问走 Bash 调 `handoff ask` |
| 审核与修改 | 审核者不直接碰工作区文件；所有修改写成指令回发，executor `--resume` 续原会话执行 |
| 上下文交接 | executor 每轮收尾强制 commit，完成事件携带分支名 + commit hash；审核素材 = `handoff diff` 传回的 git diff |
| 审批分级 | 审核者全权处理常规权限与技术提问；危险操作与需求取舍升级给用户本人 |
| Executor 支持 | adapter 抽象；MVP 只做 Claude Code，opencode / grok CLI 留接口 |
| 技术栈 | Go 单二进制 `handoff`，两端复用 |

## 3. 架构总览

```
本机（Mac，随时断网）                     executor 所在机（本机或远程，常在线）
┌─────────────────────┐                ┌──────────────────────────────┐
│ 交互式 Claude Code   │                │ handoff agentd（WS listener） │
│  （审核者）          │   WebSocket    │  ├─ 任务/事件存储（SQLite）    │
│         │           │ ◄────直连────► │  ├─ executor adapter          │
│  handoff wait（后台） │   本机主动拨号  │  │   └─ claude -p（tmux 内）   │
│  handoff reply/...   │                │  └─ hooks → handoff gate/ask  │
└─────────────────────┘                │  桌面终端窗口 tmux attach ↑    │
        │                              └──────────────────────────────┘
     用户本人（危险操作/需求取舍升级）
```

组件职责与边界：

- **handoff CLI（本机侧子命令）**：`dispatch` / `wait` / `reply` / `continue` / `diff` / `fetch` / `run` / `tasks` / `attach` / `done`。只与 agentd 的 WS 端口通信，不直接操作 executor 或工作区。
- **handoff agentd**：任务状态机、事件持久化队列、executor 生命周期管理（tmux 内拉起/续接/回收）、git 工作区操作（建分支、取 diff）。监听 WS；`--target local` 场景由 CLI 自动拉起本机 agentd。
- **executor 侧挂载（同一二进制的子命令）**：`gate`（PreToolUse hook 调用，阻塞等审批）、`ask`（executor 经 Bash 调用，阻塞等回答）。二者只与本机 agentd 通信（localhost），阻塞发生在 agentd 内部排队，与审核者是否在线解耦。
- **审核者**：消费事件、决策审批、回答提问、审核 diff、下发修改指令；不持有任何任务状态（状态全在 agentd）。

## 4. 任务生命周期

1. **分发**：审核者执行 `handoff dispatch --target <name> --repo <path> plan.md`。agentd 建任务分支（或 worktree），在 tmux session 中启动 executor，可选在远程桌面弹终端窗口 attach 显示实况；用户也可随时 `ssh -t <host> tmux attach -t <task>` 查看。
2. **执行**：Claude Code adapter 以 `claude -p` 启动，prompt 为 executing-plans 语义 + plan 内容 + 提问纪律（见 §7），`--output-format stream-json` 流出事件；hooks 配置见 §6。
3. **事件与唤醒**：审核者挂 `handoff wait <task>`（后台进程）。事件到达 → wait 以 JSON payload 退出 → 审核者被唤醒处理 → `handoff reply` 回传结果 → 重新挂 `wait`。事件类型：
   - `permission_request`：常规操作（项目内读写、跑测试、装依赖）直接批；危险操作（rm -rf、force push、生产环境、系统级改动）升级用户，用户不在场则挂起，executor 阻塞等待；
   - `question`：技术类直接答；需求取舍类升级用户；
   - `completed`：携带分支名、commit hash、执行摘要，进入审核；
   - `failed` / `crashed`：携带日志尾部，审核者诊断后决定重试指令或升级用户；
   - `progress`（可选心跳）：只入库不唤醒。
4. **审核**：审核者 `handoff diff <task>` 取 diff，必要时 `handoff fetch <file>` 取上下文、`handoff run <cmd>` 远程跑测试。需要修改则 `handoff continue <task> "<指令>"`，agentd 以 `claude -p --resume <executor-session>` 续接原会话执行，修完必须再 commit，回到步骤 3。
5. **收尾**：审核通过后 `handoff done <task>`，按任务配置决定是否 push；任务状态置为 completed 归档。

## 5. 协议与数据

- **存储（agentd 侧 SQLite）**：
  - `tasks`：id、target、repo、branch、plan 摘要、executor session id、状态机（pending / running / waiting_review / waiting_answer / completed / failed）；
  - `events`：自增 seq、task id、类型、payload JSON、acked 标记;
  - `answers`：ticket id、事件 seq、回答内容——供 `gate`/`ask` 断线重等（幂等）。
- **WS 协议**：事件下发 + 客户端 ack + 重连时携带 `resume_from_seq` 补发未 ack 事件，保证不丢不重。
- **安全**：Tailscale 内网为边界 + 首次配对生成 shared token，不做用户体系。
- **断线语义**：本机离线时事件只入库不投递；executor 阻塞在 `gate`/`ask` 上原地等待；本机重连后先补发积压事件。

## 6. Executor 挂载细节（全 CLI，零 MCP）

- **权限门**：executor 的 settings 配 PreToolUse hook → hook 脚本调 `handoff gate --task <id> --payload -`，阻塞至 agentd 收到审核者决定，输出映射为 `permissionDecision: allow/deny`。Hook `timeout` 字段显式配大（24h 级）。
- **提问**：executor 被 prompt 约束「所有提问必须执行 `handoff ask '<问题>'`」，该命令先落 ticket 再阻塞，答案回来打印到 stdout。executor settings 调大 `BASH_MAX_TIMEOUT_MS`；若仍被掐断，executor 以 `handoff ask --wait <ticket>` 重新等待，幂等不丢。
- **Adapter 接口**（为 opencode / grok 预留）：`Start(task, plan)` / `Events() <-chan Event` / `Gate(req) Decision` / `Ask(q) Answer` / `Continue(instructions)`。grok 若缺程序化审批挂载点，降级为「预授权模式」（仅白名单自动放行，不支持中途审批）。

## 7. 会话恢复（验收标准级要求）

一个**没有任何前文的全新审核者会话**，仅凭两条命令即可完整重建现场：

- `handoff tasks`：列出全部任务及状态；
- `handoff attach <task>`：返回 plan 摘要、事件历史、**未处理挂起项**（未答提问、未批权限、待审核的完成事件）。

处理完挂起项后重新挂 `wait` 即恢复循环。此流程同时覆盖「本机会话崩溃」「用户主动关闭后重开」「换一台机器接管审核」三种场景。兜底：`wait --notify` 在事件到达时发 macOS 系统通知，提醒用户会话已不在时重新拉起。

## 8. 错误处理

- executor 进程崩溃 → adapter 捕获退出码，`crashed` 事件携带 stream-json 尾部与 stderr；
- agentd 崩溃 → SQLite 状态落盘可恢复，systemd 托管自启；重启后对 running 状态任务做存活探测（tmux session 是否存在），不存在则标记 crashed；
- 任务级超时看门狗：长时间无事件产出（默认 2h，可配）→ `stalled` 事件唤醒审核者裁决；
- `gate`/`ask` 进程被意外杀死 → ticket 幂等重等（§6）。

## 9. 测试策略

- 协议层 / 事件队列 / 状态机：单元测试；
- 全流程集成测试：**fake executor adapter**（脚本化产出权限请求、提问、完成事件），不消耗 token；
- 真实 Claude Code e2e：手动验证清单（含 hook 长阻塞 spike，见 §10）。

## 10. 风险与 Spike

| # | 风险 | 缓解 |
|---|------|------|
| 1 | PreToolUse hook 长阻塞（小时级）在实际 claude 版本上是否被额外机制掐断 | 实现前 spike 验证；已有 fallback（ticket 幂等重等） |
| 2 | headless 模式下 executor 不守提问纪律、自作主张 | prompt 强约束 + 审核环节兜底；必要时 PreToolUse 拦截高危自主行为 |
| 3 | 本机会话关闭导致唤醒链断裂 | `wait --notify` 系统通知兜底；`claude --resume` 全自动接管留 v2 |

## 11. 范围外（YAGNI）

- 中心协调 server、多用户体系；
- opencode / grok adapter 的实现（仅留接口）；
- Web 面板 / GUI；
- executor 并发多任务调度（v1 串行，一个 target 同时跑多个任务不做保证）。
