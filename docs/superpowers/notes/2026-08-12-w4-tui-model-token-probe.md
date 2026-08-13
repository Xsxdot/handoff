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
| **grok** | ❓ 未取到证据 | ❌ 会话协议里没有 | ❌ 无 | 反面证据（见 §4） |

两条结构性发现，比逐家的字段清单更重要：

**发现一：模型名今天是纯入参，handoff 不知道实际跑的是什么。**
`Task.Model` 是 handoff **发下去**的值，空串表示「用执行者自己的默认」。四家 adapter
（`internal/executor/*/adapter.go`）全部只往下传，没有一家读回执行者**实际**用的模型。
本次派发的任务 `4e3565e1` 就是 `"model":""`，而 opencode 库里记的是 `deepseek-v4-flash`
——这个缺口是实的，而且三家都已经把答案摆在协议里了，是能补上的。

**发现二：分母只有 codex 给。**
「context token 用量」在原型 TUI 上是个百分比，它需要分子和分母。分子三家都有；
分母（模型的上下文窗口大小）**只有 codex 在协议里报**。claude 与 opencode 要么由
handoff 自己维护一张「模型 → 窗口大小」的表（会过时、会漏新模型），要么就只报绝对值不报百分比。

这直接决定了将来那个 brainstorm 的头一个问题：**要百分比还是要绝对值**。选百分比，
就得接受三家里有两家的分母是 handoff 猜的；选绝对值，形态上就与原型不一致。
交接文档预判的「有的 adapter 报不了，就如实缺席」也成立了一半——缺席的不是用量本身，
是分母。

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

**未决**：以上取自 codex **自己**的 rollout 文件，不是 handoff 读的那条线。
handoff 的 codex adapter 走的是 app-server 的 `thread/*` + `item/*` JSON-RPC
（见 [adapter.go:60-95](../../../internal/executor/codex/adapter.go:60)），
`token_count` 是 `event_msg` 家族的，**这个协议转不转发它，本次没验**。

要settle 它只需一次实验：拿 codex 派一个最小任务，把 adapter 收到的所有通知方法名
打一遍 debug 日志，看有没有携带 usage 的帧。没做是因为它要占一个真实任务，
而这个问题属于将来那个 brainstorm 的范围，不属于探针。

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

## 4. grok：会话协议里没有，用量走的是 OTel

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

---

## 5. 给将来那个 brainstorm 的三个问题

探针到此为止，下面是设计问题，**不在本文回答**：

1. **百分比还是绝对值？** 分母只有 codex 给。要百分比，claude 与 opencode 的分母
   得由 handoff 维护一张模型表——那张表会过时、会漏新模型，且漏了就是**静默错误**
   （百分比照常显示，只是错的）。绝对值不会错，但与原型形态不一致。
2. **累计口径还是当前口径？** claude 的 `assistant.usage` 是**每条消息**的，
   codex 的 `total_token_usage` 是**整个会话累计**的，opencode 的 `tokens.total` 是
   **该条消息的**。三家口径不同，硬凑成一个数之前得先定义清楚 TUI 顶栏那个数
   到底是什么意思。
3. **codex 的 app-server 转不转发 `token_count`？** §1 的未决项，一次最小实验可settle。
   若不转发，codex 从「最全」掉到「和 grok 一样报不了」——这会反过来改变第 1 问的答案。

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
