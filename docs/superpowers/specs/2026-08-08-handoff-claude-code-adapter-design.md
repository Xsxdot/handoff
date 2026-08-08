# Claude Code adapter 设计（B2）

> 目标：让 `handoff dispatch --executor claude` 与 opencode 完全对等——五动作全链路可用、
> 分级审批链原样生效、`handoff attach` 看到同样形态的终端实况。
>
> 来源：二期 spec §4.4「新 adapter 范围外单独立项」；backlog B2。

## 1. 前置 spike（已完成，2026-08-08）

设计建立在三条实测结论上（claude 2.1.220，本机 macOS）：

| 假设 | 结论 | 证据 |
|------|------|------|
| `--permission-prompt-tool mcp__<server>__<tool>` 仍可用 | ✅ 可用（未列入 `--help`，SDK 内部在用） | 挂一个极简 stdio MCP server，`rm -rf` 触发时工具被调用 |
| 裁决工具入参含稳定 id | ✅ 入参为 `{tool_name, input, tool_use_id}` | `tool_use_id` 与同回合 `assistant.tool_use.id` 一致 |
| 裁决工具可阻塞、allow/deny 均被尊重 | ✅ | 返回 `{"behavior":"deny","message":...}` 后模型放弃该动作并说明被拒 |
| `-p --input-format stream-json` 进程跨回合存活 | ✅ | 首回合 `result` 后再写第二条 user message，同一 `session_id` 续接成功 |
| 每回合以 `result` 事件收尾 | ✅ | `{"type":"result","subtype":"success","result":"<回合文本>"}` |
| `--setting-sources ''` 会丢失用户 skills | ✅ 会丢 | 空 sources 只剩内置 skill；`user` 才有 backend-go/superpowers 等 |

未验证、留给实现首步的：**任务级 `--settings` 的 `ask`/`deny` 能否压过用户级 `allow`**（见 §5.2）。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| 可视化形态 | 对齐现状：tmux 两窗口（进程窗口 + `tail -f render.log`），不自研 TUI |
| 权限门挂载 | `--permission-prompt-tool` + handoff 内置 stdio MCP server，经 unix socket 桥到 adapter |
| PermissionID | 直接用 Claude 的 `tool_use_id`（天然稳定唯一，满足 executor 包幂等约定） |
| 配置继承 | `--setting-sources user,project` 继承 skills；任务级 `--settings` 用 `deny`/`ask` 收口 |
| 进程宿主 | tmux（与 opencode 同构：agentd 重启/崩溃不带走执行中任务） |
| 指令投递 | 命名管道 `in.fifo`，脚本内 `exec 3<>` 永久持有两端 |
| 通用件 | 抽 `internal/executor/turn` 共享包，opencode adapter 同步改调 |
| tmux 会话名 | 沿用 `handoff-<id8>`，`handoff attach` 零改动 |

## 3. 进程模型

### 3.1 启动脚本

`<taskDir>/run_claude.sh`（0600，taskDir 本身 0700）：

```sh
#!/bin/sh
# 由 agentd 生成：Claude Code 执行者启动脚本。
exec 2>> <taskDir>/claude.log          # stderr 单独落盘，不污染 out.jsonl
exec 3<> <taskDir>/in.fifo             # 永久持有读写两端（why 见下）
claude -p \
  --input-format stream-json --output-format stream-json --verbose \
  --model <model> --session-id <uuid> \
  --setting-sources user,project --settings <taskDir>/settings.json \
  --mcp-config <taskDir>/mcp.json --permission-prompt-tool mcp__handoff__ask \
  <&3 | tee -a <taskDir>/out.jsonl
printf '{"type":"handoff_exit","code":%d}\n' "$?" >> <taskDir>/out.jsonl   # 死亡哨兵
```

- **why `exec 3<>`**：FIFO 的写端每次关闭都会给读端送 EOF，claude 随即退出。脚本自己永久
  持有读写两端后，agentd 可以反复开关写端投递指令而不打断会话。
- **why stderr 不 `2>&1`**：`out.jsonl` 必须是纯 JSON 行流，混入 stderr 会让解析器在
  非 JSON 行上不断报错；诊断信息去 `claude.log`（对应 opencode 的 `serve.log`）。
- **why `--model` 可省**：`model` 为空时省略该参数，由 claude 自己的默认模型决定
  （与二期「model 可空 = 用执行者自身默认」的约定一致）。
- **why `--session-id` 由 agentd 生成**：会话 id 在进程起来之前就已确定，写进 `claude.json`，
  agentd 重启后无需依赖已消费的事件即可恢复；同时它也是 `Result.SessionID` 的来源。
- **why 末行死亡哨兵**：`tmux has-session` 对本 adapter **不是**可用的存活判据——窗口 1 的
  `tail -f` 会一直活着，claude 早死了会话依然存在（opencode 靠 HTTP 探活兜住这一点，
  claude 没有这个面）。改由脚本在进程退出后往 `out.jsonl` 追加一行 `handoff_exit` 哨兵：
  它带退出码、随事件流天然送达 adapter，且落在文件里——agentd 重启后重读同样能发现死亡，
  不依赖任何轮询是否恰好在线。因此 `claude` 一行**不能**用 `exec`：`exec` 会让 sh 被替换掉，
  哨兵永远写不出来。

### 3.2 tmux 布局

会话名 `handoff-<id8>`（`id8` = 任务 uuid 前 8 字符，与 opencode 同规则）：

- 窗口 0：`sh <taskDir>/run_claude.sh` —— 原始 stream-json 输出（对应 opencode 的 serve 窗口）
- 窗口 1：`tail -f <taskDir>/render.log` —— 模型文本与工具动作增量（与 opencode 同名同义）

窗口 1 的启动手法沿用 `startRenderTailWindow`：先 touch `render.log` 再开窗口（`tail -f`
对不存在的文件会立即退出），失败只 Warn 不阻断——它是增强型可见性，不值得为它挂掉任务。

`handoff attach` 不需要任何改动：会话命名规则与窗口布局都与 opencode 一致。

### 3.3 taskDir 文件契约

| 文件 | 权限 | 用途 | opencode 对应物 |
|------|------|------|----------------|
| `run_claude.sh` | 0600 | 启动脚本 | `run_serve.sh` |
| `in.fifo` | 0600 | `Send` 投递 stream-json user message | serve HTTP API |
| `out.jsonl` | 0644 | claude stdout 原样，adapter 按 offset 续读 | SSE 事件流 |
| `claude.log` | 0644 | stderr；启动失败与死亡诊断的第一手证据 | `serve.log` |
| `render.log` | 0644 | 渲染文本，tmux 窗口 1 的 tail 目标 | 同名同义 |
| `settings.json` | 0600 | 任务级权限策略 | `OPENCODE_CONFIG` 指向的配置 |
| `mcp.json` | 0600 | 裁决工具挂载（含 socket 路径） | — |
| `perm.sock` | 0600 | 权限裁决 unix socket | — |
| `claude.json` | 0600 | 恢复凭据：tmux 会话名 / session_id / out.jsonl 已消费 offset | `serve.json` |

## 4. 五动作映射

### 4.1 Start

1. 生成 session uuid，建 `in.fifo`（`mkfifo`）、写 `settings.json` / `mcp.json` / `run_claude.sh`
2. `tmux new-session -d -s handoff-<id8> -c <repoPath> "sh <taskDir>/run_claude.sh"`
3. 开渲染窗口（窗口 1）
4. 起 `perm.sock` 监听（见 §5.1）
5. 起 `out.jsonl` tail 循环；**就绪判定 = 读到 `{"type":"system","subtype":"init"}`**
   （超时 30s，超时读 `claude.log` 尾部带进错误并 kill 会话清理残留）
6. 就绪后往 fifo 投首条 user message：plan 原文 + prompt 附加指令（拼装逻辑与 opencode 同源）
7. 写 `claude.json`；emit `progress{SessionID}` 作为「会话就绪」信号

启动超时阈值取 30s（大于 opencode serve 的 10s）：claude 启动要加载 settings/plugins/MCP
子进程，冷启动明显更慢，10s 会造成假阴性。

### 4.2 Events（out.jsonl → AdapterEvent）

| stream-json 消息 | 映射 |
|-----------------|------|
| `system` / `init` | `progress`，携带 `SessionID`；写 `claude.json` |
| `assistant`，content 含 text 块 | 追加 `render.log`；触发 `maybeProgress`（心跳节流同 opencode） |
| `assistant`，content 含 `tool_use` | `render.log` 追加一行动作摘要（`→ Bash: <command 首行>`） |
| `user`，content 含 `tool_result` | `render.log` 追加结果摘要（截断） |
| `result`，`subtype=success` | 回合收尾：取 `result` 文本 → `turn` 包分类 → `question` 或 `result` |
| `result`，`subtype!=success` | `result{OK:false, FailReason=subtype + claude.log 尾部}` |
| `handoff_exit`（脚本写的死亡哨兵） | 进程已退：`code=0` 且本回合已收尾则正常终结；否则 `result{OK:false, FailReason=退出码 + claude.log 尾部}` |
| 其他（`rate_limit_event`、`system/thinking_tokens` 等） | 忽略（只在 debug 日志留痕） |

- **回合分类**：`result` 文本经 `turn` 包判定「这是在提问」还是「这一轮干完了」——
  与 opencode 的 `fallbackClassify` 是同一套阈值，判为 done 时补 git 取证
  （branch / commit / 是否有新提交），构成 `executor.Result`。
- **不解析权限**：权限完全走 socket 旁路，`out.jsonl` 里那次 `mcp__handoff__ask` 的
  tool_use 只当普通工具调用渲染，不产生 permission 事件（避免同一请求出两次）。
- **offset 续读**：每消费一行更新 `claude.json.offset`；agentd 重启后从 offset 起读，
  已消费的回合不会重放。

### 4.3 Send

往 `in.fifo` 写一行：

```json
{"type":"user","message":{"role":"user","content":"<原文>"}}
```

原文**原样透传不加工**（契约要求）。写入前检查运行态：任务不在运行中时包装
`executor.ErrTaskNotRunning`。

### 4.4 RespondPermission

按 `permID`（= `tool_use_id`）找到 socket 上挂起的请求并回裁决：

- `once` → `{"behavior":"allow","updatedInput":<原 input>}`
- `reject` → `{"behavior":"deny","message":"<审核者的拒绝理由>"}`

找不到挂起请求（进程已死 / 请求已被重试替换）时包装 `ErrTaskNotRunning`。

### 4.5 Stop

`tmux kill-session` + 关闭 socket 监听 + 停 tail 循环 + 关闭事件通道。幂等：会话已不存在
视为已清理，不报错（与 opencode 的 `Proc.Kill` 同规则）。

### 4.6 Resume（可选接口，agentd 重启恢复）

读 `claude.json` → 判存活：

**存活判据（本 adapter 的关键差异）**：`tmux has-session` **不可用**——窗口 1 的 `tail -f`
会一直活着，claude 早死了会话依然存在。判据是两条，缺一即视为死亡：

1. `out.jsonl` 从头到 `offset` 之后**不含** `handoff_exit` 哨兵（含则进程已退，带退出码）
2. tmux 会话存在（会话都没了，进程一定没了）

恢复动作：

- 活着：从 `offset` 重开 tail、重建 `perm.sock` 监听（MCP 侧会自行重连重登记），返回 `alive=true`
- 死了 / 凭据缺失：返回 `alive=false`，manager 按现有逻辑转 failed 交审核者裁决

**看门狗**：主信号是哨兵（随事件流到达，无需轮询）；兜底再加一路周期
`tmux has-session`，防「会话被外部 kill、哨兵也没写成」的情况。发现死亡时以 `claude.log`
尾部作 `FailReason` emit `result{OK:false}`。

## 5. 权限门

### 5.1 链路

```
claude 需要授权某次工具调用
  → 调 mcp__handoff__ask
  → handoff permission-mcp --sock <taskDir>/perm.sock      （claude 拉起的 stdio 子进程）
  → 连 perm.sock，发 {tool_use_id, tool_name, input}，阻塞等
  → adapter emit AdapterEvent{Type:"permission", PermissionID:tool_use_id, Text:"Bash: rm -rf ..."}
  → manager 现有分级审批链：黑名单 → 审批者（廉价模型）→ 审核者（人）
  → RespondPermission(once|reject) → adapter 回 socket
  → MCP 返回 {"behavior":"allow"} / {"behavior":"deny","message":...} → claude 继续或放弃
```

`handoff permission-mcp` 是 handoff 二进制的一个隐藏子命令（stdio JSON-RPC），只实现
`initialize` / `tools/list` / `tools/call` 三个方法与一个 `ask` 工具。它不读配置、不连
agentd HTTP，唯一的对外面就是 socket 路径——被监管的 executor 拿不到 agentd token。

### 5.2 三条纪律

1. **PermissionID = `tool_use_id`**：Claude 侧稳定唯一，直接满足 `executor` 包的幂等约定，
   不自造 id。manager 侧命名空间化为 `taskID:tool_use_id`。
2. **断线重连**：MCP 进程连不上或连接中途断开时按退避重试，并用**同一 `tool_use_id`**
   重新登记。agentd 重启后 `CreateTicket` 按 id 幂等去重，审核者不会被同一请求唤醒两次。
3. **fail-closed**：socket 无人接时 MCP 一直阻塞（Claude 侧表现为一直等），绝不自作主张
   放行。与「审批者超时按 escalate」同一条纪律。

### 5.3 权限描述文本

`Text` = `<tool_name>: <关键入参>`：Bash 取 `command`，Edit/Write 取 `file_path`，
其余工具取 `input` 的紧凑 JSON。截断走 `turn` 包的截断标记，B6（工单存全文）落地后
自动继承其行为，本 spec 不重复定义截断策略。

### 5.4 任务级策略（settings.json）

`--setting-sources user,project` 继承执行机的 skills 与项目约定；任务级 `--settings` 负责收口：

- `permissions.deny`：黑名单硬拒（与 manager 侧黑名单同源，不另起一张表）
- `permissions.ask`：危险 bash 模式表，与 `taskenv.bashPermissionRules` 同源

**实现首步必须先验证「任务级 `ask`/`deny` 是否压得过用户级 `allow`」**（写一条回归测试打这个点）：

- 压得过：按本节走
- 压不过：退到 `--setting-sources project`（丢用户级 skills，保住安全门），并把这个取舍
  写进 README 的已知限制

这个不能靠猜——spike 已实测到用户个人 allowlist 会静默放行本该进审批链的操作（`echo` 直接放行）。

## 6. 共享包重构（`internal/executor/turn`）

两个 adapter 同构的四件事搬进新包，opencode adapter 改调：

| 搬迁项 | 现位置 |
|--------|--------|
| 回合文本分类（question vs done）+ `clampQuestion` | `opencode/adapter.go: fallbackClassify / clampQuestion` |
| render.log 增量落盘 | `opencode/adapter.go: appendRender` |
| git 回合取证（branch / commit / hasNew） | `opencode/adapter.go: gitTurnStatus` |
| 截断标记（`TruncationMarker` 的使用面） | `opencode` 的 `truncateMarked` |

验收硬指标：opencode adapter 那 1200 行回归测试**全绿**。不绿就是抽错了边界，回退重来。

## 7. 接入面（改动清单）

| 位置 | 改动 |
|------|------|
| `internal/executor/claudecode/` | 新包：adapter.go / proc.go / stream.go / perm.go / taskenv.go |
| `internal/executor/turn/` | 新包：见 §6 |
| `internal/executor/opencode/` | 改调 `turn` 包 |
| `cmd/permission_mcp.go` | 新隐藏子命令 `handoff permission-mcp` |
| `internal/agentd/manager.go` | adapter 注册表加 `"claude"` |
| `internal/executor/oneshot.go` | 无需改动（claude 已登记） |
| `cmd/attach.go` | 无需改动（会话命名一致） |
| `README.md` | 执行者一节补 claude；已知限制补 §5.4 的结论 |

## 8. 错误处理

| 场景 | 处置 |
|------|------|
| `claude` 未安装 / 不在 PATH | Start 失败，错误带 `claude.log` 尾部；任务转 failed |
| 30s 内未读到 `init` | Start 失败，带 `claude.log` 尾部；kill 会话清理残留 |
| `out.jsonl` 出现非 JSON 行 | 跳过并 Warn（不中断解析循环）；连续 N 行非法则视为流损坏，转 failed |
| fifo 写入失败（进程已退） | 包装 `ErrTaskNotRunning` |
| MCP 子进程被 claude 杀掉 | 挂起请求随连接断开消失；重试由 claude 的下一次工具调用自然覆盖 |
| agentd 重启时有挂起权限 | MCP 重连重登记（同 `tool_use_id`），ticket 幂等，不重复唤醒 |
| tmux 未安装 | Start 失败并明示（与 opencode 同） |

## 9. 测试策略

- **单测**
  - 事件映射：用 spike 采到的**真实 `out.jsonl`** 作 testdata（覆盖 init / text / tool_use /
    tool_result / result success / result error）
  - 权限 socket 协议：登记 → 裁决 → 回包；重连重登记幂等
  - fifo 投递：原文透传、进程已退时的 `ErrTaskNotRunning`
  - `settings.json` / `mcp.json` / `run_claude.sh` 生成内容（含 0600 权限与引号转义）
  - offset 续读恢复：中途重建 tail 不重放已消费回合
  - 存活判据：写入 `handoff_exit` 哨兵后 `Resume` 必须判死——**并显式覆盖
    「tmux 会话还在（窗口 1 的 tail 撑着）但 claude 已退」这一路**，这是本 adapter
    最容易误判为存活的场景
- **集成**：假 claude 脚本（对齐 opencode 的 `proc_script_unix_test` 手法）跑完整五动作，
  不烧任何 token
- **策略验证**：§5.4 的 `ask`/`deny` vs 用户 `allow` 优先级回归测试（实现第一步）
- **真机验收**：本机 dispatch 真任务，走通「权限升级 → 审核者批 → `continue` 改一轮 → `done`」，
  并 `attach` 确认两个窗口都活

## 10. 非目标

- 不自研 TUI（用户已确认对齐现状即可）
- 不做 grok adapter（B3 单独立项）
- 不改 `handoff attach` 的任何行为
- 不重新定义权限文本截断策略（归 B6）
