# B38 探针：grok / codex 的对账能力

> 对应 spec：`2026-08-10-handoff-reconnect-reconciliation-design.md` §7.2 / §7.3 / §7.4
>
> 参照答案：opencode 的对账实现（`internal/executor/opencode/reconcile.go` + `procInfo.WatermarkArmed`）——本探针问的每一个问题，结论直接套用那套接口（`ReconcileOutcome` 四字段、`watermark_armed` 语义、补发与实时事件同形）。

## 目的

spec §7.4 决定范围：**某个 adapter 拿不到需要的信息，它就不实现 `Reconciler`，走 §5 的人工出口，并如实记录为什么拿不到。** 本探针逐条回答 spec §7.2（grok）与 §7.3（codex）的问题清单，给第二份「grok/codex 对账实现计划」提供事实基础。

**纪律**：不接受按协议文档或 schema 名字推断的答案——B28 的 spike 一次推翻四处推断，本仓库对此有明确教训。本探针的答案全部来自两个 adapter 的**既有实现代码**（`internal/executor/grok/`、`internal/executor/codex/`）与 spec 已实证结论，不按 schema 名字猜。**真机探针（抓 grok/codex 真实报文）仍未执行**——两个 adapter 的凭据与 serve 环境当前未就位，本探针的产出是「代码层能确定的」与「必须真机才能确定的」二分清单，第二份实现计划应以本清单为起点、补齐真机项后再动手。

## 已实证的既有结论（代码与 spec 已定，不用重问）

| 事实 | 出处 |
|---|---|
| grok 的 `session/load` **只恢复会话历史，不恢复未决授权请求** | `internal/executor/grok/perm.go:16-18`（spike 实测：WS 断开重连 + session/load 成功，未决权限不重发，工具调用永久卡死）+ `perm.go:98-102`（`PermissionsVolatile`） |
| 因此 grok 权限请求断连后**不可查、不会自行重发** | spec §7.2 问题 4 的一半已答 |
| codex rollout 落在用户级 `~/.codex/sessions/**`，进程重启后 thread 仍在盘上 | `internal/executor/codex/resume.go:12-15` + B28 spec（结构性优势） |
| **grok 回合边界是 `session/prompt` 的响应（stopReason）**，不是会话状态或 idle 事件 | `internal/executor/grok/adapter.go:8`、`:17-18`（「回合边界是 session/prompt 的**响应**而非从 idle 事件推断」）、`:254`（「续接即发新的 session/prompt，回合边界由它的响应标记」）、`:432` `finishTurn` |
| codex adapter **没有**任何「列 thread items / 查会话历史」的主动调用 | `internal/executor/codex/adapter.go` 仅用 `thread/start`（新建）与 `thread/resume`（载入），无查询方法 |
| codex 的 itemIndex 是**内存缓存**，由 `item/*` 通知喂入，非查询接口 | `internal/executor/codex/items.go:94-132`（`itemIndex` 有界索引，`put`/`get` 只服务于通知流） |

---

## 一、grok（ACP over WebSocket）

### 问题 1：`session/load` 是否重放历史 `session/update`？

**怎么问**：起一个 grok 旁挂任务，等它产生至少一条 `session/update`（模型输出一个 step），然后**从 agentd 侧掐断 ACP 连接再重连**（重启 agentd 或直接重拨 WS）。重连成功后，在 `OnNotify`（`internal/executor/grok/acp.go:36`）打点统计：载入之前已发生的 `session/update` 是否**再次**被推送。

**什么算答上了**：
- 若重放：重连后 `OnNotify` 会收到一批**历史** `session/update`（content 与断连前已处理过的一致）。记下「重放了 N 条」。
- 若不重放：重连后只收到 `session/load` 的响应与**新增**事件。

**答案**：**代码层不能确定**，这是必须真机的第一项。`acp.go:36` 的 `OnNotify` 是否收到重放的历史 `session/update` 取决于 grok serve 的 ACP 实现，代码里没有任何缓存/去重逻辑（每次 `OnNotify` 都原样进 `turnAccumulator.feedRaw`，adapter.go:590）。

> **若重放，连带回答一个独立正确性问题**：现有热重连路径（`resume.go` 的 subscribeLoop 重建）**是不是已经在产生重复事件**？判据：重连后把重放的 `session/update` 重新喂给 `turnAccumulator`（`adapter.go:543`），是否会再产出一条 question/result？**代码层判断：会**——`feedRaw`（adapter.go:576）无去重，`agent_message_chunk` 无条件进 `bodyBuf`，重放的正文会重复累积，回合边界（`session/prompt` 响应）到达时 `ParseTrailer` 会从重复文本里再分类一次，很可能产出重复事件。这独立于 B38，若真机确认「重放」，是既有 bug 且必须单独修（对 grok 可能不需要主动查询对账，只需要去重）。

### 问题 2：ACP 有无「查会话历史 / 当前状态」的调用

**怎么问**：在 grok serve 存活时，用 ACP 客户端逐条尝试这些方法，看哪个返回非错误：
- `session/load`（既有，见 `perm.go:16` 注释——已实证它存在且能恢复历史）
- `session/get` / `session/status` / `session/list`（按协议文档猜测的方法名，**必须实测**，不许按名字推断）
- 无 `session/update` 之外的任何响应式查询

**什么算答上了**：列出每个方法名 → 实测响应（成功的方法返回的报文结构）或 `-32601 method not found`。**grok 的对账水位载体**（对应 opencode 的消息 id）据此定：若 `session/load` 返回的内容里带可去重的消息/step 序号，用它；否则看问题 1 的「是否重放」。

**答案**：**代码层能确定的部分**：adapter 只在 `session/new`（建会话）与 `session/prompt`（发指令）之外**没有调用任何查询方法**；`session/load` 只出现在 `perm.go` 注释（spike 实证存在）而**当前代码里没有任何 `cli.Call("session/load", …)` 调用**——恢复路径靠的是「续接即发 session/prompt」（adapter.go:254），不载入历史。因此「当前代码未用 session/load」是确定的，但「session/load 返回体里有没有可去重的序号」必须真机抓包才能答。**若问题 1 答「重放」**，grok 对账退化为「重放去重」，水位载体不再是必需的。

### 问题 3：未决的 `session/request_permission` 断连后是否可查

**怎么问**：造一个 `session/request_permission` 挂起（manager 不应答），断开 ACP 重连，`session/load` 之后：
- 直接答：**不可查**。`perm.go:16-18` 已实证「重连后未决权限不重发」。补一步验证「有没有别的方法能枚举挂起的权限请求」（问题 2 里列出的查询方法若存在，看它带不带未决权限信息）。

**什么算答上了**：确认不可查（沿用既有结论），或发现某个查询方法能带回未决权限 id → 那条路可做重新上报（对应 spec §1.5 目标 3）。

**答案**：**已实证不可查**（`perm.go:16-18`）；代码里也没有任何「枚举未决权限」的调用。维持「不可查」。

### grok 的水位载体（对应 opencode 的消息 id）

**怎么问**：`session/update` 的 params 里有没有稳定可排序的标识（step 序号 / 消息 id / 时间戳）？读 `turnAccumulator`（`adapter.go:543`）喂给它的原始报文，找可做去重键的字段。

**什么算答上了**：给出字段路径 + 是否单调递增。**注意**：若问题 1 的答案是「`session/load` 重放历史 update」，grok 可能**不需要主动查询**，对账退化为「重放去重」（见问题 1 的连带问题）。

**答案**：**代码层能确定的形态**：`session/update` 的 params 结构为 `update.sessionUpdate`（枚举 kind：`agent_message_chunk` / `agent_thought_chunk` / `tool_call` / …）+ `update.content.text` + `update.status`（adapter.go:587-600）。**没有看到稳定的消息 id / step 序号字段**——`agent_message_chunk` 只有连续文本，无单调序号。若真机确认无序号，grok 的水位载体只能取「已消费到的 `session/update` 计数」或「回合起始时间戳」，可靠性需真机评估。

---

## 二、codex（app-server WebSocket）

### 问题 1：app-server 有无「列 thread items」的方法

**怎么问**：codex app-server 存活时，用 WS JSON-RPC 客户端逐条尝试（方法名按协议文档列出，**必须实测**）：
- `thread/list` / `thread/listItems` / `thread/getItems` / `item/list`
- 从既有报文逆向：`parseItemNotification`（`items.go:81`）处理的是 `item/*` 通知，看有没有对应的「主动查询」方法。

**什么算答上了**：列出每个方法名 → 实测响应或 `method not found`。有能列 items 的方法 → codex 可以像 opencode 一样主动查会话尾部，对账形态与 opencode 同构（查尾部 → 比水位 → 补发）。

**答案**：**代码层能确定的部分**：adapter 只用 `thread/start`（新建）与 `thread/resume`（载入既有 thread，resume.go 复用），**没有任何「列 items」的主动调用**；itemIndex（items.go:94）是纯内存缓存、由通知喂入、进程重启即清空。**「app-server 协议层到底有没有 list-items 方法」必须真机抓包**——代码没调用不代表协议没有，但按 spec §7.4 的纪律，未实证即视为不可用，codex 对账不能依赖它。

### 问题 2：rollout 能否直接读盘取回最后一条 assistant item 与其完结状态

**怎么问**：codex 任务执行中，`~/.codex/sessions/**` 下找到该 thread 的 rollout 文件，直接读盘：
- 有没有「最后一条 assistant item」及其**完结状态**（completion / error）？
- 结构是 JSONL 还是目录树？逐条 item 是否可排序？

**什么算答上了**：给出文件路径模式、item 结构里的关键字段（id / role / text / completed 标志）。能读盘取回「最后一条 assistant 消息 + 完结状态」→ 对账可直接读盘（比 HTTP/WS 查询更稳，进程死了也不影响）；**codex 的水位载体**（对应 opencode 的消息 id）用盘上 item 的 id。

**答案**：**代码层能确定的部分**：`resume.go:12-15` 明写「rollout 落在用户级 `~/.codex/sessions/**`，agentd 重启、甚至 app-server 进程重启后 thread 都还在盘上，冷恢复不依赖任务目录里的会话数据」——**读盘取回是结构性可行的前提已实证**。但「rollout 的文件结构、最后一条 assistant item 的完结状态字段」**必须真机读盘确认**（B28 spec 只记了「rollout 在用户级目录、进程重启后 thread 仍在盘上」这一条结构性优势，没记文件内格式）。这是 codex 对账最有希望的一条路：**读盘**，比 opencode 的 HTTP 查询更稳（进程死了也不影响）。

### 问题 3：未决的 `requestApproval` 断连后是否可查

**怎么问**：造一个 `item/*/requestApproval` 挂起，断开 WS 重连后：
- 问题 1 的查询方法（若存在）能否带回未决审批？
- 读盘（问题 2）能否看到挂起的审批条目？

**什么算答上了**：确认不可查（沿用 `perm.go` 同款 spike 逻辑实测）或发现可查路径。

**答案**：**代码层不能确定**——依赖问题 1/2 的答案。`requestApproval` 是服务端发来的**请求**（adapter.go:47-49，`OnServerRequest` 处理），连接断开后它是否被重发/可枚举，代码里没有线索。**与 grok 同款风险**：若断开即作废（opencode/grok 都实证过此形态），codex 权限请求同样不可查、不可重新上报。

### codex 的水位载体

同 grok 一节：查 items 或读盘后，取可稳定排序的 item id 做水位。

**答案**：**倾向读盘**：`~/.codex/sessions/**` 的 rollout 是唯一有实证支撑的持久化载体（resume.go:12-15），对账读盘取最后一条 item 的 id 作水位；前提是问题 2 真机确认文件内格式。若问题 1 真机发现 list-items 方法，则与 opencode 同构走查询。

---

## 三、结论（每个 adapter 一个明确判定）

| adapter | 能实现对账？ | 理由（代码层判定 + 真机前置项） |
|---|---|---|
| grok | **暂不能**（待真机两项） | 回合边界靠 `session/prompt` 响应而非会话状态；`session/update` 无稳定消息 id；未决权限已实证不可查。对账所需「取回水位之后的会话内容」在代码层找不到载体（无主动查询、无持久化会话副本）。**前置真机**：①`session/load` 是否重放 `session/update`（若重放→只需去重，且这是既有 bug）②重放时能否从 update 流取到可去重序号 |
| codex | **有条件能**（读盘路径） | rollout 在盘上（resume.go:12-15 已实证）是唯一有实证支撑的载体——对账读 `~/.codex/sessions/**` 取最后一条 item，形态与 opencode 同构（读尾部 → 比水位 → 补发）。**前置真机**：①rollout 文件内格式（最后一条 assistant item 的 id 与完结状态字段）②未决 approval 断连后是否可查/可读盘 |

**不能实现的一方**：照 spec §7.4 与 backlog 里 opencode 权限那条的写法（「已实证不可行」是范本），如实记录原因，且**不塞一个永远返回「没事」的空实现**。opencode 权限那条的范本形态是：**「这条 spike 结论写在代码注释里而非 backlog 里，本 spec 的 brainstorm 阶段没读到」——降级告警的注释里往往埋着已经做过的探针结论**。本探针同样把「grok 回合边界靠 session/prompt 响应」「codex rollout 在盘上」这两个从代码注释读出的结论显式记下，避免第二份计划重蹈覆辙。

## 四、真机前置项清单（第二份实现计划的起点）

**grok：**
1. `session/load` 是否重放历史 `session/update`（连带判定现有热重连是否已在产生重复事件）
2. `session/load` 返回体是否带可去重的消息/step 序号
3. 重放时未决 `session/request_permission` 是否随 `session/load` 重新出现（预期：不，`perm.go:16-18` 已实证）

**codex：**
4. `~/.codex/sessions/**` 的 rollout 文件格式：最后一条 assistant item 的 id、role、text、完结状态字段路径
5. app-server 有无 list-items 主动查询方法（`thread/listItems` 等，逐条实测）
6. 未决 `requestApproval` 断连后是否可查/可读盘

**探针执行日志**：（待真机执行时逐条记录：命令输出、报文、task id）
