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

另补采样（`--include-partial-messages`）：文本增量走
`stream_event.event.content_block_delta.delta`，其中 `text_delta.text` 是模型正文、
`thinking_delta.thinking` 是思考过程——**只有前者进 render.log 与回合文本**，与 opencode
「reasoning part 不进回合」的隔离一致。回合末 `result.result` 即最后一条 assistant 正文，
正是 trailer 解析的输入。

未验证、留给实现首步的：**任务级 `ask` 相对 `allow`（同文件内 / 跨来源）的优先级**（见 §5.4）。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| 可视化形态 | 对齐现状：tmux 两窗口（进程窗口 + `tail -f render.log`），不自研 TUI |
| 权限门挂载 | `--permission-prompt-tool` + handoff 内置 stdio MCP server，经 unix socket 桥到 adapter |
| PermissionID | 直接用 Claude 的 `tool_use_id`（天然稳定唯一，满足 executor 包幂等约定） |
| 配置继承 | `--setting-sources user,project` 继承 skills；任务级 `--settings` 用 `ask`（危险模式）+ `allow`（其余放行）收口，`deny` 留空（why 见 §5.4） |
| 进程宿主 | tmux（与 opencode 同构：agentd 重启/崩溃不带走执行中任务） |
| 指令投递 | 命名管道 `in.fifo`，脚本内 `exec 3<>` 永久持有两端 |
| 通用件 | `internal/executor/turn` 共享包**已由 B3 会话抽取并合入 main**（`09bc6df`），本 plan 直接 import（见 §6） |
| env 注入 | 透传 `StartReq.Env`（B19，已在 main）到启动脚本的 `export` 行；claude 侧无保留键（策略与凭据走命令行参数，不经环境变量） |
| tmux 会话名 | 沿用 `handoff-<id8>`，`handoff attach` 零改动 |
| 降级路线 | 长驻进程若在真机上不稳，退到「每回合 one-shot `--resume`」（见 §3.4），只动 proc 层 |

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
  --include-partial-messages \
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
- **why `--include-partial-messages`**：不加它 assistant 文本按整块到达（spike 实测），
  `render.log` 的实况会一顿一顿地跳；加了才拿到增量 delta，tmux 窗口 1 的观感才真正对齐
  opencode（opencode 走的就是 part delta）。代价只是多几种可忽略的事件类型。
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

### 3.4 降级路线：每回合 one-shot `--resume`（备选，本期不实现）

设计期评估过的另一条可行架构：不长驻进程，每次 `Send` 用
`tmux respawn-pane` 起一个 `claude -p --resume <session-id>`，回合结束进程自然退出，
会话连续性由 claude 自身的会话持久化保证。

它能一次性消灭本方案里最脆的三个件——fifo 的 `exec 3<>` 生命周期技巧、死亡哨兵、
30s 就绪探测；每回合进程短命且退出码直接可观测，「挂着但其实死了」这类状态根本不存在。

**不选它的原因是一条功能性硬伤**：回合间进程退出会带走执行者起的所有后台进程——
「起 dev server → 下一回合去测它」「后台跑长测试」这类真实开发工作流会静默断掉，
而 handoff 派发的正是真实开发任务。长驻进程（与 opencode serve 同构）天然保住这些。
次要代价是每回合重付 settings/plugins/MCP 冷启动，且实况从流式退化为回合末一次性。

**但它是本方案的天然降级路线**：`session-id`、`out.jsonl`、offset、`perm.sock`、
事件映射全部原样复用，切换只动 proc 层。若长驻形态在真机上暴露不可控问题
（fifo 行为异常、进程被环境回收等），照此退级，不需要重做 adapter。

## 4. 五动作映射

### 4.1 Start

**进程流形态的前置契约（2026-08-09 真机 e2e 实测，三步单变量实验坐实）**：
`claude -p --input-format stream-json` **不会在启动时主动吐 `system/init`**——
它要先收到第一条输入消息才吐 init。因此**必须先投首回合 prompt、再等 init**：
先等它说话、它等你说话，互为死锁（旧顺序下执行者从未真正启动成功过，Step 6
首轮派发即复现：`claude.log` 全空、`out.jsonl` 只有两条 SessionStart hook 事件、
30s 就绪超时）。先写不会阻塞：启动脚本 `exec 3<> in.fifo` 自持读写两端，数据先
躺在管道缓冲里，claude 起来后自然读到（0.5s 早写实测正常）。

**`tmux new-session -d` 的返回不代表脚本已执行（2026-08-09 真机 e2e 第二轮发现，
上一条 init 时序是第一轮）**：`-d` 一创建会话就返回，不等会话内脚本执行到
`exec 3<> in.fifo`；而投 prompt 的 `WriteInput` 以 `O_WRONLY|O_NONBLOCK` 打开
fifo，按 POSIX 读端未就绪时 `open` 直接失败（errno `ENXIO`，macOS 文案
"device not configured"）。prompt 提前到 tmux 返回之后，写入紧跟在返回之后，
撞上读端未开的竞态（真机复现：`open in.fifo: device not configured`）。因此
**投 prompt 前必须先确认 `in.fifo` 已有读者**——`StartProc` 在 `tmuxLaunch`
返回后、返回前以 `O_WRONLY|O_NONBLOCK` 试开探测读端（只探测不写入），超时
5s 报错并带 `claude.log` 尾部。等读端是「进程是否已就位」的语义，必须放在
`tmuxLaunch`（「怎么把脚本跑起来」）之外，桩与生产走同一段等待代码，测试才能
抓到这类竞态。**等读端超时属于「tmuxLaunch 已成功」之后的失败**：此时会话已在
跑而调用方 rollback 依赖 `r.proc`（StartProc 失败返回 nil，拿不到句柄），
`StartProc` 必须自行 `kill-session` 回收，与 init 就绪超时的清理行为保持一致。

Start 步骤：

1. 生成 session uuid，建 `in.fifo`（`mkfifo`）、写 `settings.json` / `mcp.json` / `run_claude.sh`
2. `tmux new-session -d -s handoff-<id8> -c <repoPath> "sh <taskDir>/run_claude.sh"`
3. **确认 `in.fifo` 已有读者**（`StartProc` 内 `waitFIFOReader`，O_NONBLOCK 试开，
   超时 5s 报错带 `claude.log` 尾部）——tmux 返回早于脚本执行，不等必 ENXIO
4. 开渲染窗口（窗口 1）
5. 起 `perm.sock` 监听（见 §5.1）
6. 起 `out.jsonl` tail 循环（此时只读不判就绪）+ 看门狗
7. **往 fifo 投首条 user message**：plan 原文 + prompt 附加指令（拼装逻辑与 opencode 同源）
8. **就绪判定 = 读到 `{"type":"system","subtype":"init"}`**（prompt 已投出的产物）；
   超时 30s 读 `claude.log` 尾部带进错误并 kill 会话清理残留
9. 写 `claude.json`；emit `progress{SessionID}` 作为「会话就绪」信号

启动超时阈值取 30s（大于 opencode serve 的 10s）：claude 冷启动要加载 settings/plugins/MCP
子进程，冷启动明显更慢，10s 会造成假阴性。超时语义是「prompt 已投出、claude 仍未进入
会话」——鉴权失效、settings 非法、MCP 起不来都会卡在这一步。

### 4.2 Events（out.jsonl → AdapterEvent）

| stream-json 消息 | 映射 |
|-----------------|------|
| `system` / `init` | `progress`，携带 `SessionID`；写 `claude.json` |
| `stream_event`（`--include-partial-messages` 产出的 text delta） | 追加 `render.log` 增量（实况流式的来源）；不产生 AdapterEvent |
| `assistant`，content 含 text 块 | 回合文本累积（供收尾分类）；`render.log` 已由 delta 写过则不重复追加；触发 `maybeProgress`（心跳节流同 opencode） |
| `assistant`，content 含 `tool_use` | `render.log` 追加一行动作摘要（`→ Bash: <command 首行>`） |
| `user`，content 含 `tool_result` | `render.log` 追加结果摘要（截断） |
| `result`，`subtype=success` | 回合收尾：取 `result` 文本 → `turn` 包分类 → `question` 或 `result` |
| `result`，`subtype!=success` | `result{OK:false, FailReason=subtype + claude.log 尾部}` |
| `handoff_exit`（脚本写的死亡哨兵） | 进程已退：`code=0` 且本回合已收尾则正常终结；否则 `result{OK:false, FailReason=退出码 + claude.log 尾部}` |
| 其他（`rate_limit_event`、`system/thinking_tokens` 等） | 忽略（只在 debug 日志留痕） |

- **回合分类**：先按 `turn` 包的 trailer 协议解析（ask / finish，模型受同一套回合纪律
  prompt 约束）；无 trailer 时退到 `fallbackClassify` 兜底。判为 done 时补 git 取证
  （branch / commit / 是否有新提交），构成 `executor.Result`。两个 executor 共用同一份
  纪律 prompt 与解析器，审核者看到的形态一致。
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

- `permissions.allow`：其余工具放行（对应 opencode 的 `"*": "allow"`，避免 ls/grep 连环唤醒）
- `permissions.ask`：危险模式表，与 `taskenv.bashPermissionRules` 的 ask 项同源
- `permissions.deny`：**留空**

**why deny 留空**（设计期修正）：claude 的 `deny` 是硬拒——命中即不执行，且**不问任何人**。
而 manager 侧黑名单的语义是「命中即无条件**升级审核者**」，人仍有机会批准。把黑名单写进
`deny` 会让 claude 任务的黑名单行为与 opencode 不一致（审核者连看都看不到），等于在
executor 层偷偷改掉了二期定死的审批链语义。黑名单继续由 manager 侧处理，claude 侧只负责
把请求送达。

**实现首步必须先跑探针验证两条优先级**（两条都不能靠猜）：

1. **同文件内**：任务级 `ask` 是否压得过任务级 `allow`——「allow 兜底放行 + ask 收窄危险面」
   这个形状本身是否成立。不成立则整张表要改写成逐工具枚举。
2. **跨来源**：任务级 `ask` 是否压得过**用户级** `allow`——spike 已实测用户个人 allowlist 会
   静默放行本该进审批链的操作（`echo` 直接被放行）。

处置：

- 两条都压得过：按本节走
- 仅第 1 条成立（跨来源压不过）：退到 `--setting-sources project`（丢用户级 skills，保住
  安全门），并把取舍写进 README 已知限制
- 第 1 条不成立：`allow` 改为不写，回到「默认全 ask」——安全但会退化成一期的连环唤醒，
  此时必须靠 manager 侧审批者（廉价模型）吸收噪音，同样记入 README

**实测状态（2026-08-09，devbox，claude 2.1.226）**：两条优先级**均成立**，按本节走：

1. **同文件内**：任务级 `ask` 压得过任务级 `allow`——settings.json 里
   `allow: ["Bash"]` 与 `ask: ["Bash(rm:*)"]` 并存时，`rm` 请求仍触发裁决工具（探针 ASK
   FIRED）。「allow 兜底 + ask 收窄」的形状成立，不需要逐工具枚举。
2. **跨来源**：任务级 `ask` 压得过用户级 `allow`——用户级 settings 的 `allow: ["Bash"]`
   不会静默放行任务级 `ask: ["Bash(rm:*)"]` 命中的请求（ASK FIRED）。个人 allowlist
   无法绕过任务级收窄。

**探针与方法**：`internal/executor/claudecode/probe_live_test.go`
（`HANDOFF_LIVE_CLAUDE=1 go test ./internal/executor/claudecode/ -run Probe -v`）。
探针把鉴权从真实 `~/.claude/settings.json` 的 `env` 段提取后**经进程环境注入**（`ANTHROPIC_*`/
`CLAUDE_*`，凭证不落盘、不打日志），settings.json 因此完全由探针控制——任务级 settings
因此是**纯策略文件，不含任何凭证**。

**鉴权表述（2026-08-09 真机 e2e 二次实测修正）**：**凭证由 claude 自己经
`--setting-sources user` 从真实 `~/.claude/settings.json` 读取，不需要 handoff 的 env 注入
做鉴权**——Step 6 真机派发不带任何凭证 env（env 文件只有 `HANDOFF_ENV_PROBE=ok`），claude
照样跑完整回合。env 注入（B19）是给代理、自定义 `base_url` 这类**额外**环境用的，不是
鉴权的必要条件。探针走 env 注入只是为了构造受控的临时 HOME（探针要把 user settings 换成
测试值，只能把真实 settings 的 env 搬进进程环境再换掉 settings.json），不代表生产路径需要
它。（未单独隔离验证 `--setting-sources ""` 的行为，不写超出证据的断言。）

**两个附带的机制观察**（同为 2026-08-09 实测）：本机 claude 的登录态就存在
`~/.claude/settings.json` 的 `env.ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL` 里（非
keychain、非 `~/.claude.json`）。

## 6. 共享包重构（`internal/executor/turn`）——✅ 已由 B3 会话完成并合入 main

> **状态（2026-08-09 核实）**：抽取已落 main（`09bc6df`，merge `282c932`），opencode
> 已改调，本 plan 直接 import。实际导出面：`RenderPrompt` / `ParseTrailer` / `Trailer` /
> `AppendRender` / `GitTurnStatus` / `TruncateMarked` / `TruncateRunes` / `TailRunes` /
> `ClampQuestion` / `QuestionTextLimit`（**注意后两个命名与下表原计划不同**，plan Task 2
> Step 1 已按实际改写）。
>
> 原协调注（2026-08-08，B2/B3 两会话对齐）：抽取由 grok adapter（B3）会话作为前置任务
> 先行完成、单独 commit 合入 main；实现排序 `phase3 B6 → turn 抽取落 main → B2 / B3 并行`。
> 并行后 B2/B3 的冲突面只剩 manager 注册表各一行、README 各一行、B3 在 oneshot.go
> 加 case——琐碎合并。

搬迁范围（B2/B3 两侧设计的**并集**，opencode adapter 同步改调）：

| 搬迁项 | 现位置 |
|--------|--------|
| 回合纪律 prompt 模板与渲染 | `opencode/taskenv.go: promptTemplate / promptTmpl` |
| trailer 协议解析（ask / finish） | `opencode/taskenv.go: ParseTrailer / Trailer` |
| 回合文本分类（question vs done）+ `clampQuestion` | `opencode/adapter.go: fallbackClassify / clampQuestion` |
| render.log 增量落盘 | `opencode/adapter.go: appendRender` |
| git 回合取证（branch / commit / hasNew） | `opencode/adapter.go: gitTurnStatus` |
| 截断工具（`TruncationMarker` 的使用面） | `opencode` 的 `truncateMarked` / `truncateRunes` / `tailRunes` |

why（promptTemplate 与 ParseTrailer 必须同包，B2 原表未列全）：教模型协议的 prompt 与
解析协议的代码是同一契约的两半，分居两处必然出现「改纪律只改一半」的漂移——两个
executor 的审核者会看到不一样的东西。§4.1「拼装逻辑与 opencode 同源」已隐含依赖它。

验收硬指标：opencode adapter 那 1200 行回归测试**全绿**。不绿就是抽错了边界，回退重来。

## 7. 接入面（改动清单）

| 位置 | 改动 |
|------|------|
| `internal/executor/claudecode/` | 新包：adapter.go / proc.go / stream.go / perm.go / taskenv.go |
| `internal/executor/turn/` | **前置已落 main**（B3 会话完成，见 §6 协调注），本 plan 直接 import |
| `internal/executor/opencode/` | 前置抽取中已改调 `turn` 包，本 plan 无需再动 |
| `cmd/permission_mcp.go` | 新隐藏子命令 `handoff permission-mcp` |
| `internal/agentd/manager.go` | adapter 注册表加 `"claude"` |
| `internal/executor/oneshot.go` | 无需改动（claude 已登记） |
| `cmd/attach.go` | 无需改动（会话命名一致） |
| `README.md` | 执行者一节补 claude；已知限制补 §5.4 的结论；配置 `env` 段注明对 claude 同样生效 |
| env 注入（B19） | **无新增接线**：resolver 按 executor 名查 `config.Env["claude"]`，注册表加 `"claude"` 即生效；adapter 侧只需把 `StartReq.Env` 透传进启动脚本的 `export` 行（排在 claude 命令前、值单引号包裹），与 opencode `writeServeScript` 同构 |

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
- 不抽 `internal/executor/turn` 共享包（移交 B3 会话前置完成，见 §6 协调注）
- 不改 `handoff attach` 的任何行为
- 不重新定义权限文本截断策略（归 B6）
- 不实现进程死亡后的自动复活

## 11. 未来选项（记录，不在本期）

- **进程死亡后 `--resume` 原地复活**：claude 有自持久化会话，进程崩了理论上可以
  `--resume <session-id>` 接着跑而不是转 failed。本期**故意保守**——复活会重放
  未完成回合、可能产生重复副作用，且与 opencode 的「死了就交审核者裁决」行为不一致。
  真机跑出足够多的崩溃样本、确认崩溃点集中且可判定后再考虑。
- **降级到 one-shot `--resume` 形态**：见 §3.4，触发条件与代价已写明。
