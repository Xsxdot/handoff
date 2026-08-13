# 探针：四家 executor 报不报模型名与 context token 用量

交接文档 [2026-08-12-w4-parallel-handoff.md](2026-08-12-w4-parallel-handoff.md) 的 P4 说得对——
这件事的第一步是探针不是设计。本文是探针结果，不是设计。

**探测机器**：devbox（`sycm@100.73.238.21`），141 个历史任务目录 + 两个在跑的任务。
**探测日期**：2026-08-12。
**方法**：读执行机上真实的会话落盘数据，不是读上游文档、不是凭印象。

---

## 0. 结论先说

| executor | 有效模型名 | context token 用量 | 上下文窗口上限（分母） | 证据强度 |
|---|---|---|---|---|
| **codex** | ✅ `turn_context.model` | ✅ 累计 + 单回合，分项齐全 | ✅ **`model_context_window`** | 真实数据 |
| **claudecode** | ✅ `system/init.model` | ✅ 每条 assistant 消息分项 | ❌ 无 | 真实数据 |
| **opencode** | ✅ `modelID` + `providerID` | ✅ `tokens.total` 直接给 | ❌ 无 | 真实数据（含在跑的任务） |
| **grok** | ✅ `_meta.modelId` | ✅ 回合响应 `_meta.usage`，分项齐全 | ✅ **`totalContextTokens`** | 真实数据（08-13 实抓，见 §4.1；§4 的「没有」已被推翻） |

两条结构性发现，比逐家的字段清单更重要：

**发现一：模型名今天是纯入参，handoff 不知道实际跑的是什么。**
`Task.Model` 是 handoff **发下去**的值，空串表示「用执行者自己的默认」。四家 adapter
（`internal/executor/*/adapter.go`）全部只往下传，没有一家读回执行者**实际**用的模型。
本次派发的任务 `4e3565e1` 就是 `"model":""`，而 opencode 库里记的是 `deepseek-v4-flash`
——这个缺口是实的，而且三家都已经把答案摆在协议里了，是能补上的。

**发现二：分母只有一半的家给。**（08-13 修正：原文是「只有 codex 给」，
grok 也给，见 §4.1；结构性判断不变，只是比例从 1/4 变成 2/4。）
「context token 用量」在原型 TUI 上是个百分比，它需要分子和分母。分子**四家都有**；
分母（模型的上下文窗口大小）**codex 与 grok 在协议里报，claudecode 与 opencode 不报**。
后两家要么由 handoff 自己维护一张「模型 → 窗口大小」的表（会过时、会漏新模型），
要么就只报绝对值不报百分比。

这直接决定了将来那个 brainstorm 的头一个问题：**要百分比还是要绝对值**。选百分比，
就得接受四家里有两家的分母是 handoff 猜的；选绝对值，形态上就与原型不一致。
交接文档预判的「有的 adapter 报不了，就如实缺席」也成立了一半——缺席的不是用量本身，
是分母。

**发现三（08-13 追加）：四家的数据，全都落在 handoff 已经在读的那一帧上。**
codex 在 `thread/start` 的响应顶层与 `thread/tokenUsage/updated`（§1.1），
grok 在 `session/prompt` 的响应 `_meta`（§4.1）——两家都是「解析函数只挑了自己
当时要的字段，其余整块丢掉」。这不是要接新通道，是要多解几个字段。

---

## 1. codex：最全，且唯一给分母

**取证**：`~/.codex/sessions/2026/08/10/rollout-*.jsonl`（codex 自己的 session rollout）。

`event_msg` 下的 `token_count` 子类型，一次会话出现 20 条：

```json
{"type":"token_count","info":{
  "total_token_usage":{"input_tokens":378848,"cached_input_tokens":342016,
    "cache_write_input_tokens":0,"output_tokens":2677,
    "reasoning_output_tokens":1201,"total_tokens":381525},
  "last_token_usage":{"input_tokens":24285,"cached_input_tokens":23296,
    "cache_write_input_tokens":0,"output_tokens":238,
    "reasoning_output_tokens":178,"total_tokens":24523},
  "model_context_window":258400},
 "rate_limits":{"limit_id":"codex","primary":{"used_percent":75.0,
   "window_minutes":10080,"resets_at":1786851343},
   "credits":{"has_credits":false,"balance":"0"},"plan_type":"plus"}}
```

模型名在 `turn_context`：`{"model":"gpt-5.6-sol","summary":"auto"}`。
（`session_meta.model` 是 `null`，别去那儿取。）

顺带一提，`rate_limits` 里连套餐类型和额度重置时间都有——那不是本轮要的东西，
但记一笔，将来若要做「额度快用完了」的提示，数据是现成的。

**未决（08-13 已 settle，见下）**：以上取自 codex **自己**的 rollout 文件，不是
handoff 读的那条线。handoff 的 codex adapter 走的是 app-server 的 `thread/*` +
`item/*` JSON-RPC（见 [adapter.go:60-95](../../../internal/executor/codex/adapter.go:60)），
`token_count` 是 `event_msg` 家族的，**这个协议转不转发它，08-12 没验**。

### 1.1 08-13 补验：app-server **转发**，而且分母也在（codex-cli 0.144.1）

**方法**：不占真实任务，也不改产品代码——本机直接按 adapter 同样的姿势拉起
`codex app-server --listen ws://…`，走 `initialize` → `initialized` →
`thread/start` → `turn/start`，把收到的每一帧原样打出来。输入是一句
「只回一个字：好」，sandbox 用 `read-only`。

**结论一：用量在专门的通知里，带分母。**

```json
{"method":"thread/tokenUsage/updated","params":{
  "threadId":"019ffb3d-…","turnId":"019ffb3d-…",
  "tokenUsage":{
    "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
             "outputTokens":5,"reasoningOutputTokens":0},
    "last":{…同结构…},
    "modelContextWindow":258400}}}
```

字段与 rollout 里的 `token_count` 一一对应，只是 snake_case 换成 camelCase：
`total_token_usage`→`total`、`last_token_usage`→`last`、
**`model_context_window`→`modelContextWindow`**。分子分母都在同一条通知上。

**到达时机可靠**：一个回合内出现一次，**排在 `turn/completed` 之前**
（实测帧序 … `item/completed` → `thread/tokenUsage/updated` →
`account/rateLimits/updated` → `thread/status/changed` → `turn/completed`）。
所以不需要另起轮询，回合终态到手时用量必然已经到了。

**结论二：实际模型名也在这条线上，handoff 现在把它扔了。**

`thread/start` 的**响应**顶层就带（不是 `thread` 子对象里）：

```json
{"id":2,"result":{"thread":{…},"model":"gpt-5.6-sol","modelProvider":"openai",
  "serviceTier":"default","reasoningEffort":"high","sandbox":{…},…}}
```

本次实验**没有**传 `model` 入参（adapter 也只在 `model != ""` 时才传），
回来的 `gpt-5.6-sol` 就是 codex 自己的默认——正是「发现一」要的那个回读值。
而 [adapter.go:306-313](../../../internal/executor/codex/adapter.go:306) 对这个响应
只解 `thread.id`，`model` / `reasoningEffort` 全部丢弃。**补这个缺口不需要新协议
调用，只要多解几个字段。**

**顺带三条**（不在本轮范围，记一笔）：`account/rateLimits/updated` 同样是这条线上
的实时通知（`usedPercent` / `resetsAt` / `planType` / `credits`），不必去读 rollout；
`thread/started` 里有 rollout 文件的绝对 `path`，需要更细的历史时有现成入口；
`turn/completed` 的报文里**没有**任何用量字段，别去那儿找。

**对第 ③ 问的回答**：转发。codex 保持「最全」，不会掉到 grok 那一档——
所以第 ① 问（百分比还是绝对值）的前提不变：分母仍然只有 codex 给。

---

## 2. claudecode：分项齐全，没有分母

**取证**：`~/.handoff/tasks/<id>/out.jsonl`（stream-json 原样落盘，最大的一份 1.6 MB）。

一次会话的行类型分布：`stream_event` 21740、`system` 10091、`assistant` 354、
`user` 285、`tool_progress` 32、`result` 3。

**模型名**在 `system` 的 `init` 子类型上，会话一开始就有：

```json
{"subtype":"init","model":"k3-256k","permissionMode":"default","slash":45}
```

**用量**在每条 `assistant` 消息上：

```json
{"type":"assistant","model":"k3-256k",
 "usage":{"input_tokens":121801,"cache_creation_input_tokens":0,
   "cache_read_input_tokens":0,"output_tokens":0,
   "service_tier":"standard","inference_geo":"not_available"}}
```

`result` 行有整轮累计（`cache_read_input_tokens` 高达 943360），但 `model` 字段是
`null`——要模型名就得从 `init` 或 `assistant` 取，别指望 `result`。

context 占用要自己加：`input_tokens + cache_read_input_tokens + cache_creation_input_tokens`。
**没有任何字段告诉你上限是多少。**

---

## 3. opencode：最好取，也没有分母

**取证**：`~/.local/share/opencode/opencode.db`（SQLite，`message` 表的 `data` 列）。
opencode 已经从文件存储改成了 SQLite，`~/.local/share/opencode/storage/` 是空的——
按旧路径去找会一无所获。

以 mode=ro 只读查询，取自**本次刚派发、正在跑**的任务 `4e3565e1`：

```json
{"role":"assistant","mode":"general","agent":"general",
 "path":{"cwd":"/Users/sycm/.handoff/worktrees/4e3565e1"},
 "cost":0.0000795256,
 "tokens":{"total":15619,"input":196,"output":190,"reasoning":129,
   "cache":{"write":0,"read":15104}},
 "modelID":"deepseek-v4-flash","providerID":"opencode-go"}
```

三家里最省事的一家：**`tokens.total` 就是 context 占用**，不用自己加；
模型名带 provider 前缀信息（`modelID` + `providerID` 两段）；连成本都算好了。

顺带验证了一件与本轮无关但值得记的事：`mode`/`agent` 字段是 `general`，说明
opencode 的 subagent 活动在这一层是**可见的**——将来若要做「当前有几个 subagent 在跑」，
数据也在这儿。

同样**没有上限字段**。要百分比就得靠 `modelID` 去查表。

---

### 3.1 08-13 补验：SSE 线上确认，外加一个界面陷阱

§3 读的是 SQLite 落盘，而 handoff 读的是 SSE——**「库里有」不等于「线上有」**，
这正是 §4 栽的那种坑，所以补验一次。

方法（零成本、只读、不干扰）：mac-02 上有一个 `running` 的 opencode 任务，从它的
`proc.json` 取 port 与 password，`curl -u opencode:<pw> http://127.0.0.1:<port>/event`
旁听广播流——handoff 自己就是这条流的一个订阅者，多一个只读订阅者不改变任何状态。

`message.updated` 的 `properties.info` 是**完整的 message 对象**，与库里那份同形：

```json
{"type":"message.updated","properties":{"sessionID":"ses_…","info":{
  "id":"msg_…","role":"assistant","cost":0.0001408596,
  "tokens":{"total":47071,"input":131,"output":182,"reasoning":294,
            "cache":{"write":0,"read":46464}},
  "modelID":"deepseek-v4-flash","providerID":"opencode-go",
  "time":{"created":1786628040082,"completed":1786628048168},
  "finish":"tool-calls"}}}
```

handoff 现在只解 `info.id` 与 `info.role`
（[opencode/adapter.go:1390](../../../internal/executor/opencode/adapter.go:1390)），
其余整块丢掉——与 codex、grok 同一个形状，**发现三**在三家上都成立。

**陷阱：同一条消息会被推多次，且新消息的 tokens 是全 0。** 实测序列是
「有 tokens 无 `time.completed`」→「有 tokens 有 `time.completed`」→
**下一条新消息，tokens 全 0**。天真的「取最后一条 message 的 tokens」会让界面
在每条新 assistant 消息开头闪回 0。取数必须带条件（`tokens.total > 0`，
或 `time.completed` 存在）。

**算术**：`total 47071 = input 131 + output 182 + reasoning 294 + cache.read 46464`。
所以 `tokens.total` **不是** context 占用（它含 output 与 reasoning），
context 占用是 `input + cache.read + cache.write = 46595`。§3 说「`tokens.total`
直接就是 context 占用」是不准的，以本节为准。

---

## 4. ~~grok：会话协议里没有，用量走的是 OTel~~ —— **08-13 推翻，见 §4.1**

> **本节结论是错的。** grok 的 ACP 线上有完整用量，而且分母也有。
> 下面的原文保留不删，因为它示范了一种具体的错误方式：**三条反面证据全是真的，
> 但它们证明的是「我没找到」，不是「不存在」**。第 1 条说 wire 不落盘——那正是
> 「所以要抓活的」，不是「所以没有」；第 2 条列的四个 `session/update` 变体确实
> 不带用量——但用量根本不在 `session/update` 上；第 3 条的 OTel 文档说的是**导出
> 给组织的指标通道**，与「协议里报不报」是两件事，共存不矛盾。
> 反面证据只有在穷举过通道之后才成立，而当时没有穷举，只是读了落盘的东西。

这是唯一一家给不出正面证据的。三条独立的反面证据：

1. **没有 wire 落盘可查。** 16 个 grok 任务的 `serve.log` 最大只有 1557 字节，
   全是启动日志；ACP 的 JSON-RPC 帧不落盘。
2. **handoff 收到的 `session/update` 变体里没有用量。**
   [grok/adapter.go:586-660](../../../internal/executor/grok/adapter.go:586) 分流的四种：
   `agent_message_chunk` / `agent_thought_chunk` / `tool_call` / `tool_call_update`。
   ACP 标准的其余变体（plan、current_mode_update 等）也都不带 token 计数。
3. **grok 自己的文档把用量指向 OpenTelemetry。**
   任务级 home 里带着 grok 的用户文档，`docs/user-guide/24-monitoring-usage.md` 写明
   token 用量是导出到组织的 OTel collector 的**指标**（`grok_code.token.usage`，
   按 `type` = input/output/reasoning/cache_read 分标签），不是会话协议的一部分。

也就是说：grok 的数据存在，但在一条 handoff 根本没接的通道上。让 handoff 去起一个
OTel collector 只为读自己任务的 token 数，代价与收益完全不成比例。

**所以「有的 adapter 报不了，就如实缺席」这条预判，落在 grok 头上。**
这与仓库既有的 B69/B70 纪律一致：指针 + `omitempty`，nil 表示「取不到」，
永远不猜 0。用 0 冒充「grok 没用 token」是在编造。

### 4.1 08-13 补验：**grok 是四家里唯一分子分母都在同一次回合里给全的**（grok 1.0.3）

起因是 grok 自己被问到「handoff 怎么监控 executor 用量」，它答「用量在 ACP 的
`_x.ai/session/update` 的 `turn_completed.usage` 里，只是 handoff 的 `feedRaw`
只认 `session/update`、私有通知一概忽略」。本机实抓验证。

**做法**：按 `grok/proc.go` 的 `grokServeArgv` 原样起
`grok agent serve --bind 127.0.0.1:<port>`（`GROK_AGENT_SECRET` 走 env），
按 `grok/adapter.go` 的 `openSession` 原样握手 `initialize` → `session/new` →
`session/prompt`，把每一帧原样打出来。**不设 `GROK_HOME`**——任务级 home 里没有
登录态，`session/new` 会直接 `Authentication required`。55 帧。

**核对 grok 自己的说法**：主张成立，**位置说错了**。

| grok 的说法 | 实测 |
|---|---|
| 用量在 ACP 线上 | ✅ 成立 |
| 字段 `inputTokens`/`outputTokens`/`cachedReadTokens`/`reasoningTokens`/`costUsdTicks` | ✅ 一字不差 |
| 方法名 `_x.ai/session/update` | ❌ 实际是 **`_x.ai/session_notification`**，`update.sessionUpdate = "turn_completed"` |
| `feedRaw` 只认 `session/update`、私有通知被忽略 | ✅ 成立，代码注释就写着 `// _x.ai/* 等私有通知一概忽略`（[grok/adapter.go:644](../../../internal/executor/grok/adapter.go:644)） |
| `failed` 事件已带 `ProcUsage`，是「快照挂终态」的先例 | ❌ 张冠李戴。`proto.ProcUsage` 是本机 uid 的**进程数**占用（`Used`/`Limit`），与 token 无关 |

**但真正的发现是：根本不用碰私有通知。** 同一份 usage 也挂在
**`session/prompt` 的响应**上——而 handoff 的 `awaitTurn` 已经在读这个响应了
（取 `stopReason` 当回合边界），只是没解 `_meta`：

```json
{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn","_meta":{
  "sessionId":"019ffb4e-…","promptId":"a95e4bff-…",
  "modelId":"grok-4.6",
  "inputTokens":34502,"outputTokens":56,"totalTokens":34558,
  "cachedReadTokens":5888,"reasoningTokens":51,
  "usage":{"inputTokens":34502,"outputTokens":56,"totalTokens":34558,
    "cachedReadTokens":5888,"cacheCreationTokens":0,"reasoningTokens":51,
    "modelCalls":1,"apiDurationMs":3943,"costUsdTicks":605080000,
    "modelUsage":{"grok-4.6-build":{…同结构…}},"numTurns":1}}}}
```

这和 §1.1 的 codex 是**同一个形状的结论**：数据早就在 handoff 已经读的那一帧里，
缺的只是多解几个字段。

**分母也有**，在私有通知 `_x.ai/models/update` 上（会话建立后即到，回合前）：

```json
{"method":"_x.ai/models/update","params":{"currentModelId":"grok-4.6",
  "availableModels":[{"modelId":"grok-4.6","name":"Grok 4.6",
    "_meta":{"totalContextTokens":500000,…}}]}}
```

这条走 `OnNotify`（[acp.go:255](../../../internal/executor/grok/acp.go:255) 的
`case msg.Method != ""`），adapter 层拿得到——被丢掉的是 `feedRaw` 那一层，不是
连接层。加一个 case 即可，不需要新协议调用。

**算术**：`totalTokens 34558 = inputTokens 34502 + outputTokens 56`，所以
`cachedReadTokens 5888` 是 `inputTokens` 的**子集**，与 codex 同规矩，相加会重复计数。
当前 context 占用取 `inputTokens`，分母取 `totalContextTokens` →
`34.5k / 500k (7%)`。

**三个附带发现**：

1. 同一条线上有**两套命名**。回合中还会来一条
   `_x.ai/session_notification` + `sessionUpdate: "response_completed"`，它的 usage 是
   snake_case（`input_tokens` / `cache_read_input_tokens` / `reasoning_tokens`），
   数值也不同（`input_tokens: 28614`，是单次模型调用；`turn_completed` 的 34502 是整回合）。
   **取 `turn_completed` / `_meta` 那份**，别按名字模糊匹配。
2. 每条 `session/update` 的 `_meta.totalTokens` 带回合内实时快照（本次 23636）。
   要「回合中途就刷新用量」的话通道现成，但那是另一个口径，先不用。
3. `_meta.modelId = "grok-4.6"` 也在同一帧，实际模型名一并解决——
   §5 第 3 问「模型名要不要和用量拆开推进」现在四家全部落在同一批帧里，拆的理由更弱了。

**结论修正**：grok 从「如实缺席」改为**四家里唯一分子分母都在同一次回合里给全的**
（codex 的分母在 `thread/tokenUsage/updated`，也全，但那是独立通知）。
真正没有分母的只剩 claudecode 与 opencode 两家。

---

## 5. 给将来那个 brainstorm 的三个问题

探针到此为止，下面是设计问题，**不在本文回答**：

1. **百分比还是绝对值？**（08-13：分母 codex 与 grok 都给）要百分比，claude 与 opencode 的分母
   得由 handoff 维护一张模型表——那张表会过时、会漏新模型，且漏了就是**静默错误**
   （百分比照常显示，只是错的）。绝对值不会错，但与原型形态不一致。
2. **累计口径还是当前口径？** claude 的 `assistant.usage` 是**每条消息**的，
   codex 的 `total_token_usage` 是**整个会话累计**的，opencode 的 `tokens.total` 是
   **该条消息的**。三家口径不同，硬凑成一个数之前得先定义清楚 TUI 顶栏那个数
   到底是什么意思。
3. ~~**codex 的 app-server 转不转发 `token_count`？**~~ **08-13 已答：转发**，见 §1.1。
   通知是 `thread/tokenUsage/updated`，分子分母俱全，且排在 `turn/completed` 之前。
   codex 保持「最全」，第 1 问的前提不变。**新冒出来的问题**：实际模型名就在
   `thread/start` 的响应顶层、handoff 现在丢弃它——那么「模型名」这一半是不是应该
   和用量拆成两件事各自推进？回读模型名三家都能做、成本低、没有口径分歧；
   用量则卡在第 1、2 问上。

---

## 6. 复现命令

都是只读的。opencode 那条用 `mode=ro` 是刻意的：库在被活着的 opencode 进程写。

```bash
# codex
ssh sycm@100.73.238.21 'f=$(ls -S ~/.codex/sessions/*/*/*/*.jsonl | head -1); grep token_count "$f" | tail -1 | jq -c .payload'

# claudecode
ssh sycm@100.73.238.21 'cd ~/.handoff/tasks && f=$(ls -S */out.jsonl | head -1); jq -rc "select(.type==\"system\" and .subtype==\"init\") | {model}" "$f"'

# opencode
ssh sycm@100.73.238.21 'sqlite3 "file:$HOME/.local/share/opencode/opencode.db?mode=ro" "select data from message where data like '"'"'%\"total\":%'"'"' order by rowid desc limit 1;"'
```

§1.1 的 app-server 实验不是只读的（它跑一个真实回合、花额度），复现方式：本机起
`codex app-server --listen ws://127.0.0.1:<port>`，用任一 WebSocket 客户端按
`initialize` → `initialized`（通知）→ `thread/start` → `turn/start` 发一遍，
参数照抄 [adapter.go:279-340](../../../internal/executor/codex/adapter.go:279)，
sandbox 换成 `read-only` 即可。要看的是 `thread/start` 的响应顶层与
`thread/tokenUsage/updated` 这一帧。

§4.1 的 grok 实验同理（也花额度）：本机起
`grok agent serve --bind 127.0.0.1:<port>`，`GROK_AGENT_SECRET` 走 env，
连 `ws://127.0.0.1:<port>/ws?server-key=<secret>`，按
[grok/adapter.go:225-245](../../../internal/executor/grok/adapter.go:225) 发
`initialize` → `session/new` → `session/prompt`。**不要设 `GROK_HOME`**——
任务级 home 没有登录态，`session/new` 会返回 `Authentication required`。
要看的是 `session/prompt` 响应的 `result._meta` 与 `_x.ai/models/update` 这一帧。
