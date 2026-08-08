# grok adapter 设计（B3）

> 目标：让 `handoff dispatch --executor grok` 与 opencode 完全对等——五动作全链路可用、
> 分级审批链原样生效、`handoff attach` 看到同样形态的终端实况；并顺带把 grok 登记为
> 可选的审批者执行者。
>
> 来源：二期 spec §4.4「新 adapter 范围外单独立项」；backlog B3。

## 1. 前置 spike（已完成，2026-08-08）

**backlog B3 原记载的前提是错的**，必须先更正：B3 写「缺程序化审批挂载点，与审批链不
契合，优先级低」——那是一期（2026-08-07）依据当时的 grok CLI 形态下的判断。grok 1.0.0
实现了完整的 [Agent Client Protocol (ACP)](https://agentclientprotocol.com)，权限门是
协议内建的客户端方法。**五动作可以做完整的，不需要降级为「预授权模式」。**

设计建立在以下实测结论上（grok 1.0.0 macos-aarch64，本机 macOS）：

| 假设 | 结论 | 证据 |
|------|------|------|
| grok 有程序化审批挂载点 | ✅ ACP `session/request_permission`（agent→client 阻塞式 JSON-RPC 请求） | 见下方报文 |
| 权限请求带稳定 id | ✅ `toolCall.toolCallId`，与同回合 `tool_call` 事件的 id 一致 | 报文比对 |
| 权限描述可读 | ✅ `title`（`Execute \`cmd\``）+ `rawInput.command` | 报文 |
| 应答被尊重 | ✅ `{"outcome":{"outcome":"selected","optionId":"allow-once"\|"reject-once"}}` | 放行则工具执行、拒绝则模型改道 |
| 会话可恢复 | ✅ `agentCapabilities.loadSession: true`，`session/load{sessionId,cwd}` | initialize 响应 |
| 有 serve 形态（非仅 stdio） | ✅ `grok agent serve --bind 127.0.0.1:PORT`，`ws://…/ws?server-key=SECRET` | 端口起来 + ACP 握手通过 |
| serve 上 ACP 完整可用 | ✅ WS 上跑通 initialize → session/new → prompt → **权限请求到达** | `yolo:false` + 权限报文 |
| secret 可不进 argv | ✅ `--secret` 有环境变量等价物 `GROK_AGENT_SECRET` | `--help` |
| 权限模式可被 handoff 强制 | ✅ 任务级 `GROK_HOME` 写 `permission_mode = "default"`，会话 `yolo` 翻 false | 用户真实配置是 `always-approve`，覆盖后权限请求正常到达 |
| `GROK_HOME` 能隔离 `~/.claude` | ❌ **不能**，见 §3.3 | `grok inspect` 仍读真实 HOME 的 settings/skills |
| one-shot 可用（审批者角色） | ✅ `grok -p <prompt>` 输出纯文本 | 返回 `{"decision":"approve"}` 无杂质 |
| one-shot 耗时可接受 | ⚠️ 默认 32.4s，`--effort low` **7.5s** | 审批者默认超时 60s，见 §7 |

权限请求实测报文（handoff 的 permission 事件即由它翻译）：

```json
{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{
  "sessionId":"019fe1f8-...",
  "toolCall":{"toolCallId":"call-8abf27a5-2ff2-43ca-a228-16f9bcd56b75-0",
              "kind":"execute",
              "title":"Execute `echo ws-probe > ws.txt`",
              "rawInput":{"variant":"Bash","command":"echo ws-probe > ws.txt"}},
  "options":[{"optionId":"allow-once","name":"Yes, proceed","kind":"allow_once"},
             {"optionId":"reject-once","name":"No, and tell Grok what to do differently","kind":"reject_once"}]}}
```

| 权限请求会被 grok 超时掉 | ❌ 悬挂 1194s 无任何超时/取消消息，连接与进程存活 | §5.1 实验一 |
| 延迟应答仍被接受 | ✅ 悬挂 90s 后补发 `allow-once`，工具执行、文件生成、`end_turn` 收尾 | §5.1 实验二 |

未验证、留给实现首步的：**WS 断开重连 + `session/load` 后，断线前未决的权限请求是否
会被重发**（见 §5.2）。

## 2. 核心决策（已确认）

| 决策点 | 结论 |
|--------|------|
| 传输形态 | `grok agent serve` + WebSocket ACP（**非** `agent stdio`）——与 opencode serve 同构 |
| 进程宿主 | tmux（与 opencode 同构：agentd 重启/崩溃不带走执行中任务） |
| 可视化形态 | 对齐现状：tmux 两窗口（进程窗口 + `tail -f render.log`），不自研 TUI |
| 权限门挂载 | ACP `session/request_permission` 原生，无需自建桥（对比 claude adapter 需自建 MCP+socket） |
| PermissionID | 直接用 `toolCallId`（天然稳定唯一，满足 executor 包幂等约定） |
| 环境隔离 | 任务级 `GROK_HOME`，**纯净**：只软链 `auth.json`，不继承用户 skills/plugins/MCP/memory |
| 第 0 层分级 | 任务级 `config.toml` 的 `[permission]`，规则表比 opencode 短（grok 内建只读自动放行更准） |
| 存活判据 | HTTP 端口探活（复用 opencode `probeHTTP`），**不用** `tmux has-session` |
| 通用件 | 依赖 `internal/executor/turn` 共享包（本 spec 的 plan 作为 Task 1 前置抽取） |
| tmux 会话名 | 沿用 `handoff-<id8>`，`handoff attach` 零改动 |
| 审批者角色 | `oneshot.go` 登记 grok，编码 `--effort low` |
| 依赖 | **零新增**：`github.com/coder/websocket` 项目已在用 |

### 2.1 为什么选 serve 而不是 stdio

`grok agent stdio` 更简单（无端口、无 secret、无探活），但执行者会绑定 agentd 生命周期
——agentd 重启即杀 grok，且 tmux 里只能 `tail -f render.log` 看回放而非进程实况，看门狗
要改成管道 EOF 判定。这与 opencode 骨架不同构，等于为第二个 adapter 另写一套进程模型。

serve 形态则让 `Proc` / `freePort` / `randomPassword` / `serve.json` / `probeHTTP` /
watchdog / Resume 这套**已在 opencode 上验收过的骨架**整体复用，且执行者独立于 agentd
存活。代价只是多一个端口+secret 的托管面，而 secret 有环境变量入口、不进 argv。

## 3. 任务环境

### 3.1 启动脚本

`<taskDir>/run_grok.sh`（0600，taskDir 本身 0700）：

```sh
#!/bin/sh
# 由 agentd 生成：grok agent serve 启动脚本（0600，含随机 secret，勿外泄）。
exec 2>> <taskDir>/serve.log
export GROK_HOME=<taskDir>/grokhome
export GROK_AGENT_SECRET=<random>
exec grok agent serve --bind 127.0.0.1:<freePort> 2>&1 | tee -a <taskDir>/serve.log
```

- **why secret 走环境变量而非 `--secret`**：tmux 客户端进程的 argv 本机全局可读
  （`/proc/<pid>/cmdline`），这是 opencode 侧 P0-4 划定的安全边界，本 adapter 原样继承。
  同理不用 tmux `-e`：`show-environment` 会把它暴露给任何能连上 tmux server 的本机用户。
- **why `tee -a serve.log`**：serve 所在窗格随命令退出而关闭，`capture-pane` 读不到已关闭
  窗格（P1-8）；serve.log 是死后仍可读的持久诊断副本，启动超时与死亡诊断都从它取尾部。
- **why 首行 `exec 2>>`**：sh 自身的 stderr（如 `grok` 不存在时的 "not found"）同样落盘，
  否则这类报错只进 tmux 窗格、随命令退出一起消失。
- **why 这里可以用 `exec`**（与 claude adapter 相反）：grok 有 HTTP 探活面，不需要脚本
  在进程退出后补写死亡哨兵，因此 sh 可以被替换掉。

### 3.2 tmux 布局

会话名 `handoff-<id8>`（`id8` = 任务 uuid 前 8 字符，与 opencode 同规则）：

- 窗口 0：`sh <taskDir>/run_grok.sh` —— serve 进程（对应 opencode 的 serve 窗口）
- 窗口 1：`tail -f <taskDir>/render.log` —— 模型文本与工具动作增量（与 opencode 同名同义）

窗口 1 沿用 `startRenderTailWindow` 手法：先 touch `render.log` 再开窗口（`tail -f` 对不
存在的文件会立即退出），失败只 Warn 不阻断——它是增强型可见性，不值得为它挂掉任务启动。

`handoff attach` 不需要任何改动。

### 3.3 任务级 GROK_HOME 与第 0 层分级

`GROK_HOME = <taskDir>/grokhome`，纯净：只软链真实 `~/.grok/auth.json`（否则无法登录），
其余全部由 handoff 生成。

`<taskDir>/grokhome/config.toml`（0600）：

```toml
[ui]
permission_mode = "default"        # 压掉用户真实配置的 always-approve

[models]
default = "<model>"                # 空则整节省略，用 grok 自身默认

[permission]
ask = ["Bash(rm *)", "Bash(*sudo*)", "Bash(*git push*)", "Bash(*git reset --hard*)",
       "Bash(*--force*)", "Bash(curl *)", "Bash(wget *)", "WebFetch(*)"]
allow = ["Edit", "Write"]
```

- **why `permission_mode = "default"` 是必需项而非可选项**：用户真实配置是
  `always-approve`，直接沿用等于审批门全废——所有工具调用自动放行，permission 事件永不
  产生。这是任务级 `GROK_HOME` 存在的首要理由。
- **why 规则表比 opencode 短**：grok 内建按 `&&` / `||` / `;` / 管道**分段**识别只读命令
  （`ls`/`cat`/`git status`/`grep`/`rg` 等）并自动放行，且 `ls && rm -rf /` 会被拆开、`rm`
  段仍然拦。opencode 那张以 `"*": "allow"` 收尾的模式表是手工补的等价物，grok 这里删掉
  更准。handoff 只需补 `ask` 危险模式与 `allow` 编辑放行。
- **model 三级优先级**（与 opencode `WriteTaskEnv` 同规则）：任务级 `task.Model` >
  环境变量 `HANDOFF_GROK_MODEL` > 不写（grok 自身默认）。

**已知泄漏（关不掉，写进 README 已知限制）**：grok 无视 `GROK_HOME`，仍从**真实 HOME**
读取 Claude Code 兼容源——`~/.claude/settings.local.json`（本机实测 48 条权限规则）、
`~/.claude/Claude.md`、`~/.claude/skills`（167 个）。缓解与残余风险：

- grok 的规则求值是 **`deny` > `ask` > `allow` 跨源生效**，handoff 写的 `ask` 压得过用户
  个人 allowlist 里的 `allow`，因此上表枚举的危险模式仍然会进审批链，第 0 层分级成立；
- 残余面是「handoff 没枚举、而用户 allow 了」的操作会被静默放行。其上还有 manager 侧
  硬黑名单作为独立兜底（不依赖 executor 侧任何配置），故接受该风险并显式记录。

### 3.4 taskDir 文件契约

| 文件 | 权限 | 用途 | opencode 对应物 |
|------|------|------|----------------|
| `run_grok.sh` | 0600 | 启动脚本 | `run_serve.sh` |
| `grokhome/config.toml` | 0600 | 任务级权限策略与模型 | `OPENCODE_CONFIG` 指向的配置 |
| `grokhome/auth.json` | 软链 | 登录凭据（指向真实 `~/.grok/auth.json`） | — |
| `serve.log` | 0644 | serve stdout/stderr；启动失败与死亡诊断第一手证据 | 同名同义 |
| `render.log` | 0644 | 渲染文本，tmux 窗口 1 的 tail 目标 | 同名同义 |
| `serve.json` | 0600 | 恢复凭据：tmux 会话名 / port / secret / sessionId | 同名同义 |

## 4. 五动作映射

### 4.1 Start

1. 建 `grokhome`、软链 `auth.json`、写 `config.toml` 与 `run_grok.sh`
2. `tmux new-session -d -s handoff-<id8> -c <repoPath> "sh <taskDir>/run_grok.sh"`
3. 开渲染窗口（窗口 1）
4. **就绪判定 = HTTP 端口可连**（阈值 15s，超时读 `serve.log` 尾部带进错误并 kill 会话
   清理残留）。取 15s 而非 opencode 的 10s：grok 冷启动要加载配置与索引，略慢。
5. WS 连 `ws://127.0.0.1:<port>/ws?server-key=<secret>` → `initialize` → `session/new{cwd}`
6. **不等待地**发 `session/prompt`（该请求要跑完一整个回合才响应），内容 = `turn` 包用
   `req.PlanContent` 渲染出的回合纪律模板。注意 `--prompt` 的附加指令在 dispatch 侧就已
   拼进 `PlanContent`（见 `manager.go:471` 的 `StartReq` 组装），adapter 不再二次拼接
7. 写 `serve.json`；emit `progress{SessionID}` 作为「会话就绪」信号

### 4.2 Events（ACP → AdapterEvent）

| ACP 消息 | 映射 |
|---------|------|
| `session/request_permission`（agent→client 请求） | **permission**，`PermissionID = toolCallId`，`Text` = `title` + `rawInput.command`；超限走 `TruncationMarker` |
| `session/update` `agent_message_chunk` | 累积回合正文 → 追加 `render.log` → 触发 `maybeProgress`（节流同 opencode） |
| `session/update` `agent_thought_chunk` | **只进 `render.log`**，不进回合正文 |
| `session/update` `tool_call` / `tool_call_update` | **只进 `render.log`**（`▸ <title>` / 状态变更） |
| `session/prompt` 的**响应**（含 `stopReason`） | 回合收尾，见 §4.2.1 |
| WS 断开 | **不直接终结**：先作废挂起权限、按退避重连，见 §4.2.2 |
| serve 探活判死 / 重连耗尽 | `result{OK:false, FailReason = 原因 + serve.log 尾部（脱敏 secret）}` |
| 其他（`_x.ai/*` 私有通知、`available_commands_update` 等） | 忽略（Debug 留痕，绝不 panic） |

- **why thought 不进回合正文**：`ParseTrailer` 取「最后一个 `{` 开头的行」，推理流里模型
  复述协议样例会污染判定；但它对旁观者有价值，故进 `render.log`。
- **why tool_call 只进 render.log**：grok 给的是结构化 `title`/`status`，渲染出来的旁观
  体验优于 opencode 的 part 混流；但它不是模型对人说的话，不进回合正文。

#### 4.2.1 回合分类

```
stopReason == "end_turn"  → 取本回合累积正文 → turn.ParseTrailer
                             ├─ ask    → question 事件（clampQuestion 截断）
                             ├─ finish → git 取证核对 → result{OK:true, Branch/Commit/Summary}
                             └─ none   → turn 兜底裁决：有新提交→按 finish 收尾；
                                          无新提交→整段回合文本当 question 交审核者
stopReason != "end_turn"  → result{OK:false, FailReason = stopReason + serve.log 尾部}
```

回合结束时若本轮有被拒权限，沿用 opencode 的 `noteRejected` / `takeTurnRejected` /
`rejectedTurnQuestion` 三件套，把「这轮拒了哪几条」拼成 question 交审核者——否则模型被
拒后可能悄悄绕路，人不知情。

**回合边界比 opencode 干净**：这里是 `session/prompt` 的响应，不是从 idle 事件推断，因此
opencode 的 `idleGraceDefault` / `scheduleIdle` / `resolveIdle` / `cancelPendingIdle` 那整套
防抖与竞态处理**本 adapter 不需要**。

#### 4.2.2 WS 断开的单一处置路径

断开不等于任务终结（serve 进程可能好好活着），但 ACP 请求 id 是连接级的，**挂起的权限
一定作废**。处置只有一条路径，`result{OK:false}` 全程**至多 emit 一次**：

```
WS 断开
  → 作废挂起表（此时不 emit，任务未必已死）
  → 按退避重连
     ├─ 重连成功 → session/load → 未决权限能否复原取决于 §5.2 的结论
     │              ├─ grok 会重发 → 新 id 重新登记，CreateTicket 按 id 幂等，审核者无感
     │              └─ 不重发     → 该任务已卡死等应答 → emit result{OK:false}（唯一出口）
     └─ 重连耗尽 → emit result{OK:false}（唯一出口）
```

看门狗的探活判死是**独立**通道：它判死时同样 emit `result{OK:false}`，与上面的出口靠
`closeEvents` 的一次性语义互斥（先到者终结，后到者被丢弃），不会双重终结。

### 4.3 Send

`session/prompt{sessionId, prompt:[{type:"text", text:<原文>}]}`，原文**原样透传不加工**
（契约要求）。发送前检查运行态：任务不在运行中时包装 `executor.ErrTaskNotRunning`。

### 4.4 RespondPermission

adapter 维护挂起表 `pending[toolCallId] = <jsonrpc request id>`：

```
session/request_permission(id=N) → pending[toolCallId]=N → emit permission 事件
  → manager 分级审批链（黑名单 → 审批者 → 审核者），可能数小时后才回
  → RespondPermission(permID, once|reject)
  → 查表 → WS 回 {"jsonrpc":"2.0","id":N,"result":{"outcome":{"outcome":"selected",
                   "optionId":"allow-once"|"reject-once"}}}
```

三条纪律：

1. **PermissionID = `toolCallId`**，不自造 id；manager 侧命名空间化为 `taskID:toolCallId`。
2. **fail-closed**：adapter 绝不自作主张应答。挂起表查不到（进程已死 / 连接已重建）时
   包装 `ErrTaskNotRunning`，交 manager 现有逻辑转 failed 给审核者裁决。
3. **WS 断开即全体作废**：ACP 请求 id 是连接级的，连接消亡则挂起项全部失效。但作废
   **不等于**立刻终结任务——处置走 §4.2.2 的单一路径（先重连，卡死或重连耗尽才 emit
   `result{OK:false}`）。纪律的实质是：绝不静默丢弃，最终一定有一个出口让 manager 知道，
   不能留下一个假装在跑、实则永久静止的任务。

### 4.5 Stop

`session/cancel`（通知）→ 关 WS → `tmux kill-session` → 关闭事件通道。幂等：会话已不存在
视为已清理，不报错（与 opencode `Proc.Kill` 同规则）。Kill 失败时的保留态回收沿用
opencode 的 `reapRetained` 节奏与放弃上限。

### 4.6 Resume（agentd 重启恢复）

读 `serve.json` → **HTTP 端口探活**判存活 → WS 重连 → `session/load{sessionId, cwd}` →
重建事件循环，返回 `alive=true`；探活失败或凭据缺失返回 `alive=false`，manager 按现有
逻辑转 failed 交审核者裁决。

**存活判据必须是端口探活，不能用 `tmux has-session`**：窗口 1 的 `tail -f` 会一直活着，
serve 早死了会话依然存在。这正是 claude adapter 需要自造死亡哨兵的原因；grok 有 HTTP 面
（实测根路径回 404 = HTTP 层活着），直接复用 opencode 的 `probeHTTP` 即可。

看门狗沿用快慢双档：活跃期 200ms、连续 10 次成功且无新事件后降到 2s，连续 3 次失败判死。

## 5. 实现首步必验项

### 5.1 权限悬挂时长（已验，结论：不需要降级机制）

ACP 权限是 agent→client 的**阻塞式**请求，而 handoff 的审核者可能过夜才裁决——若 grok
侧对未应答请求有超时，该工具调用会失败，「人工慢裁决」模型在 grok 上就不成立。

两次实验，结论**分开陈述**（未合并为单次端到端跑通，措辞不越过证据）：

| 实验 | 做法 | 结果 |
|------|------|------|
| 一：超时探测 | 收到权限请求后悬挂 **1194s（≈20min）** 不应答 | grok **未发送任何超时/取消消息**，WS 连接与 serve 进程全程存活 |
| 二：延迟应答 | 悬挂 **90s** 后补发 `allow-once` | **被接受**：工具执行 `exit_code:0`、目标文件真的生成、回合 `stopReason:end_turn` 正常收尾 |

> 实验一原本也带补应答验证，但脚本自身缺陷（两个协程同时 `recv` 触发 `ConcurrencyError`，
> 异常退出把连接关了）使那一半结论作废——补应答虽已发出，grok 没机会处理。故拆成实验二
> 单独验证，并另跑一次干净的 20 分钟复核。

**对设计的影响**：主路径不需要任何超时降级机制（不需要「临近超时先回 `reject-once`、
裁决回来再 `continue` 重新触发」那套）。

**仍不能推断为无限期**：20 分钟不等于 8 小时。跨天场景不依赖单条连接长活——§4.6 的
Resume（端口探活 + `session/load`）与 §4.2.2 的断开处置是独立兜底，即便某天 grok 加了
超时或连接被网络层掐断，任务也不会静默卡死。

### 5.2 重连后未决权限是否重发（未验）

WS 断开重连 + `session/load` 后，断线前未决的 `session/request_permission` 会不会被重发？

- **会重发**：挂起表自然重建，`CreateTicket` 按 id 幂等去重，审核者不被重复唤醒；
- **不重发**：模型会永久卡在等应答。退路是 **Resume 时若发现该任务有未决权限工单，
  直接判 `alive=false` 转 failed 交审核者**——保守，但不会留下永久静止的任务。

写一条回归测试打这个点。不能靠猜（与 claude spec §5.4 同一处理方式）。

## 6. 前置依赖：`internal/executor/turn`

本 spec 的 plan 以**抽取 `turn` 共享包**为 Task 1，单独 commit 合入 main 后再开工 adapter
本体。范围为 B2/B3 两侧设计的并集，详见
[claude spec §6](2026-08-08-handoff-claude-code-adapter-design.md) 的协调注与搬迁表。

实现排序（文书写作不受限，仅约束实现落点）：

```
phase3 B6（改同一片截断代码，其 plan 带固定行引用）
  → turn 抽取落 main（本 spec 的 plan Task 1）
  → B2 / B3 各自 worktree 并行实现
```

并行后 B2/B3 的冲突面只剩 manager 注册表各一行、README 各一行、B3 在 `oneshot.go` 加
一个 case——琐碎合并。

## 7. 接入面（改动清单）

| 位置 | 改动 |
|------|------|
| `internal/executor/turn/` | **Task 1**：抽取（并集范围），opencode 改调，1200 行回归全绿 |
| `internal/executor/grok/` | 新包：`adapter.go` / `acp.go` / `proc.go` / `taskenv.go` |
| `cmd/agentd.go` | 注册表加 `"grok": grok.New(logger)`；未知 executor 错误文本同步 |
| `internal/executor/oneshot.go` | 加 grok 分支（见下）；错误文本同步 |
| `cmd/dispatch.go` | `--executor` 帮助文本补 grok |
| `internal/config/config.go` | 注释里的执行者示例补 grok（无校验逻辑改动） |
| `README.md` | 执行者一节补 grok；已知限制补 §3.3 的 `~/.claude` 泄漏 |
| `cmd/attach.go` | **无需改动**（会话命名与窗口布局一致） |

`oneshot.go` 的 grok 分支：

```go
case "grok":
    if model != "" {
        return []string{"grok", "--effort", "low", "-m", model, "-p", prompt}, nil
    }
    return []string{"grok", "--effort", "low", "-p", prompt}, nil
```

- **why 编码 `--effort low`**：实测同一条裁决 prompt，默认（high effort）32.4s、
  `--effort low` 7.5s。审批者默认超时 60s，high 档等于把预算烧掉一半以上。`OneShotArgs`
  的职责就是「一次性调用形态的唯一登记点」，把「一次性 = 廉价快速」编码进该分支符合定位。
- **why flag 顺序不能动**：`-p <PROMPT>` 是取值参数不是开关，`--effort` 必须在 `-p` 之前，
  否则 grok 报 `a value is required for '--single'`。prompt 仍是末位参数，契约不变。

## 8. 错误处理

| 场景 | 处置 |
|------|------|
| `grok` 未安装 / 不在 PATH | Start 失败，带 `serve.log` 尾部；任务转 failed |
| 15s 内端口未就绪 | Start 失败，带 `serve.log` 尾部；kill 会话清理残留 |
| WS 握手 / `initialize` 失败 | 同上；secret 从不进 argv，日志与 `FailReason` 一律脱敏 |
| WS 中途断开 | 走 §4.2.2 单一路径：作废挂起权限 → 退避重连（参数取自 opencode SSE 退避）→ `session/load`；重连耗尽 → failed |
| 挂起权限遇 WS 断开 | 全体作废；是否终结取决于重连结果，`result{OK:false}` 至多一次（§4.2.2） |
| `session/prompt` 返回非 `end_turn` | failed，`FailReason` 带 `stopReason` |
| 未知 `session/update` 类型 | Debug 跳过，绝不 panic（executor 侧输出不可信） |
| `auth.json` 软链目标不存在 | Start 失败并明示「grok 未登录，先跑 `grok login`」 |
| tmux 未安装 | Start 失败并明示（与 opencode 同） |

## 9. 测试策略

- **单测**
  - ACP 事件映射：用 spike 采到的**真实报文**作 testdata（覆盖 permission / message_chunk /
    thought_chunk / tool_call / prompt 响应各 `stopReason`）
  - 权限挂起表：登记 → 裁决 → 回包；查不到时 `ErrTaskNotRunning`；WS 断开时全体作废
  - 终结唯一性：断开→重连耗尽、断开→重连成功但卡死、看门狗判死三条路径**各自只 emit
    一次** `result{OK:false}`，且三者并发到达时只有先到者生效（§4.2.2）
  - 物料生成：`config.toml` / `run_grok.sh` 内容（含 0600 权限、引号转义、**secret 不在 argv**）
  - 回合分类各分支（ask / finish / none 兜底 / 非 end_turn）
  - Resume：探活成功走 `session/load`；探活失败判 `alive=false`
  - `oneshot.go` 的 grok 分支 argv：`--effort low` 在 `-p` **之前**、prompt 为末位参数
    （顺序错会让 grok 报 `a value is required for '--single'`，见 §7）
- **集成**：假 ACP server（Go 起 WebSocket 服务按脚本回报文）跑完整五动作，不烧 token。
  手法对齐 opencode 的 `proc_script_unix_test`。
- **策略验证**（实现首步）：§5.2 的重连后未决权限是否重发，写回归测试固定结论。
- **真机验收**：本机 dispatch 真任务，走通「权限升级 → 审核者批 → `continue` 改一轮 →
  `done`」，`attach` 确认两窗口都活。

## 10. 非目标

- 不做 grok 的 `--json-schema` 结构化审批者输出（会破坏 `OneShotArgs` 的统一契约，留待
  B9 nonce 防伪落地后再议）
- 不做 leader 模式共享会话（`grok --resume` 让人直接接管同一会话，会与 manager 中介
  循环形成双写）
- 不暴露 reasoning effort 为任务级参数（任务级只走 executor+model 二元组，与二期约定一致）
- 不做 `grok agent stdio` 形态（见 §2.1）
- 不改 `handoff attach` 任何行为
- 不重定义权限文本截断策略（归 B6）
- 不试图封堵 §3.3 的 `~/.claude` 泄漏（grok 侧无此开关，manager 硬黑名单兜底）
