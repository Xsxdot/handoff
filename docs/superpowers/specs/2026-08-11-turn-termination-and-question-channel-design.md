# 回合终结解析与 opencode 提问通路（B48 + B49）设计

## 1. 范围与动机

两条缺陷，共同点是**回合该结束时结束不了、或者结束了却认不出来**——审核者要么白等到 2h 看门狗，要么收到一份被 git 兜底改写过的结论。

| 条目 | 一句话 |
|------|--------|
| B48 | `turn.ParseTrailer` 只认「以 `{` 开头的行」，模型把正文和协议 JSON 写在同一行时整行被跳过，判 none 走 git 兜底 |
| B49 | opencode 原生 `question` 工具阻塞等人作答，而 handoff 从没订阅 `question.asked` 事件，工具永远等不到应答——回合不结束、无 idle、trailer 永不解析，任务挂死到 stall 超时 |

放在一个 spec 里做，是因为两条都落在「回合终结」这条链路上、代码面零重叠（B48 在 `internal/executor/turn/`，B49 在 `internal/executor/opencode/`）、可并行实现与独立验收。

**不在范围内**：claudecode / codex / grok 三家的提问通路。它们各有各的协议（claude 的 stream-json、codex 的 threadId、grok 的 ACP `session/request_permission` 与 `ask_user_question`），本次只接 opencode 这一家。B48 的修复因落在共享包里而四家同时受益，但那是顺带，不是本 spec 要为另三家做真机验收的理由。

## 2. 事实基础：08-11 探针结论

B49 的修法方向此前是三选一（禁用工具 / 侦测 tool part 回填 / 侦测后 abort），因为「opencode 有没有应答通道」当时未知。本次在 devbox（opencode **1.18.16**）上对 `@opencode-ai/sdk` 的生成类型做了静态探针——该类型由服务端 OpenAPI 生成——两个未知数都有了确定答案。

### 2.1 应答协议本来就存在，且与 permission 完全同构

| | permission（handoff 已接通） | question（handoff 未接通） |
|---|---|---|
| SSE 事件 | `permission.asked` | `question.asked` |
| 载荷 | `id` / `sessionID` / `permission` / `metadata` | `id` / `sessionID` / `questions[]` / `tool{messageID,callID}` |
| 应答 | `POST /session/{id}/permissions/{permID}` | `POST /question/{requestID}/reply` |
| 拒绝 | 同上，`response=reject` | `POST /question/{requestID}/reject` |
| 回显事件（须忽略） | `permission.replied` | `question.replied` / `question.rejected` |
| 挂起列表 | — | `GET /question`（跨会话全量） |

`EventPermissionAsked` 与 `EventQuestionAsked` 是 SDK 里 `Event` 联合类型（89 个成员）的并排成员——**同一条 SSE 流**，正是 adapter 已经在消费的那条。

单个问题的结构：

```
QuestionInfo {
  question: string        // 完整问题
  header:   string        // 极短标签（≤30 字符）
  options:  QuestionOption[]   // { label: string, description: string }
  multiple?: boolean      // 是否多选
  custom?:   boolean      // 是否允许自定义答案
}
```

应答体：`{ answers: Array<Array<string>> }`——按问题顺序排列，每项是该问选中的 label 数组。

### 2.2 根因随之改写

B49 在 backlog 上记的是「handoff 的 opencode adapter 没有处理原生 question 工具的通路」。这句话对，但**推论**错了：不是 opencode 缺无人值守通道，而是 handoff 从没订阅它。

`question.asked` 到达 [`adapter.go` 的 `mapEvent`](../../../internal/executor/opencode/adapter.go) 后，因为不在任何 case 里，落进 `default:` 分支被 `Debug("未知 SSE 事件，跳过")` 静默丢掉；`taskScopedEvents` 里也没有它。于是 opencode 侧的工具无限期等待一个永不到来的 reply。

**这条推论修正带来的直接后果**：原方案里「侦测 running 状态的 tool part」那条思路整个作废。不需要去猜 tool part 的状态机，照抄 `mapPermissionAsked` 那条已经在生产里跑的路径即可。

### 2.3 配置层禁用同样可行（但不用）

`Config.tools?: { [key: string]: boolean }` 确实在配置 schema 里，`tools: {"question": false}` 成立。**本次不用**，理由见 §6。

### 2.4 仍未实证的一条

`custom: true` 时能否把自由文本直接放进 `answers` 透传，目前只有类型层面的推断（`QuestionAnswer = Array<string>`，是字符串数组而非枚举），没有运行时证据。处置见 §5.4——不设阻塞探针，由代码在运行时降级。

## 3. B48：ParseTrailer 两级提取

### 3.1 现状与失败形态

[`turn/protocol.go:87`](../../../internal/executor/turn/protocol.go) 的规则是「逐行扫描，取最后一个 `TrimSpace(line)` 以 `{` 开头的行，整行 `json.Unmarshal`」。

08-10 B38 真机验收中 agentd.log 出现 `turn_tail="g.{\"branch\":...}"`——模型把一个残留字符和协议 JSON 写在了同一行。该行行首不是 `{`，被整行跳过；更早的行里也没有协议 JSON，于是判 none，走 `fallbackClassify` 的 git 兜底。兜底那条路会用 git 实况的 branch/commit 覆盖模型自己报的值，并把回合末 200 字符当 summary——结论没错但来源被悄悄换掉了，且日志里留下一条「executor 不守纪律」的误判。

### 3.2 设计：主路径 + 回退，只增不减

`ParseTrailer` 改为两级：

**主路径（新增）**——取最后一个非空行，定位该行第一个 `{`，用 `json.Decoder` 从该偏移解码**一个** JSON 值。`Decoder.Decode` 读满第一个完整值即停，不要求整行都是 JSON，因此前缀正文（`g.{"branch":...}`）与后缀正文（`{"ask":"q"} 好的`）一并覆盖。

**回退（现有规则原样保留）**——主路径没解出可用协议字段时，仍走「取最后一个以 `{` 开头的行」。模型写完 trailer 又追一行说明时最后一行没有 `{`，靠这条兜住。

判据不变：`ask` 非空 → `"ask"`；`branch`/`commit`/`summary` 任一非空 → `"finish"`；否则 `"none"`。宽容解码（不设 `DisallowUnknownFields`）不变。纯函数、不打日志、不 panic 的既有约束不变。

### 3.3 为什么只放宽最后一行

放宽提取必然扩大误吞面：正文里出现含 `ask`/`branch`/`commit`/`summary` 字段的 JSON 就可能被当成本回合结论。这不是假想风险——模型复述协议格式是真实发生过的，grok adapter 为此已经把推理流和文本流分成两股（[`grok/adapter.go:551`](../../../internal/executor/grok/adapter.go)）。

把放宽限制在**最后一个非空行**，与收尾纪律「作为本回合最后一行」对齐，误吞窗口收窄到几乎为零：正文中间无论写什么 JSON 都不受影响，只有模型在真正的末行上把正文和协议混排时才触发新逻辑。

代价是明确的：模型写完 trailer 又追加了一整行正文时，主路径在那行上解不出协议，退回旧规则；若更早的行也不是以 `{` 开头，仍然漏判。**接受**——这个形态没有真机证据，而为它放宽全部行会把误吞面扩大到整段文本。

### 3.4 影响面

`ParseTrailer` 被四个 adapter 共用（[`opencode/adapter.go:1438`](../../../internal/executor/opencode/adapter.go)、[`opencode/reconcile.go:234`](../../../internal/executor/opencode/reconcile.go)、[`grok/adapter.go:463`](../../../internal/executor/grok/adapter.go)、[`codex/adapter.go:591`](../../../internal/executor/codex/adapter.go)、[`claudecode/adapter.go:630`](../../../internal/executor/claudecode/adapter.go)）。改一处四家同时受益，也意味着四家同时承担误吞风险——这正是把放宽限制在末行的另一个理由。

## 4. B49：接通 opencode question 协议

### 4.1 事件入口

`question.asked` 加入 `taskScopedEvents`（它会直接产出面向审核者的工单，必须归属到会话）。`mapEvent` 的 switch 加三个 case：

- `question.asked` → `mapQuestionAsked`
- `question.replied` / `question.rejected` → Debug 忽略

回显事件必须忽略，理由与 `permission.replied` 那条注释所记的完全一致：把应答回显当成新请求，审核者的答复会被当作再次提问，流程死循环。

子会话归属直接复用 B52 建好的 `acceptForeign`——opencode 的 subagent 会话同样会调 question 工具，B52 已经确立了「子会话事件归属到父任务」的裁决，不需要为 question 另起一套。

### 4.2 `mapQuestionAsked`

解析 `{id, sessionID, questions[], tool{messageID,callID}}`，然后：

1. **按 requestID 去重**。runState 维护已上报的 requestID 集合；SSE 重连重放同一事件时不产重复工单。`AdapterEvent` 的 `question` 类型没有 `PermissionID` 那样的幂等 id，manager 无法自行去重，所以去重必须在 adapter 侧做。
2. **渲染工单文本**。多问按「问题 N」分段，每问列出 `N.M label — description`，并在该问末尾标注多选/可自定义。渲染结果经 `turn.ClampQuestion` 截断后作为 `AdapterEvent.Text`。
3. **emit `question` 事件**。
4. **不 `clearTurn`、不 `advanceWatermark`。** 这是与 trailer-ask 路径的根本差别：工具阻塞时回合并没有结束，没有 idle，答复之后是**同一个回合**继续。清缓冲会把该回合已累积的文本丢掉，推进水位会让后续对账错位。
5. **存 pending**：requestID + 问题结构（各问的 label 表、`multiple`、`custom`）。

**描述下限**：`questions` 为空、或全部问题都没有 label 时，照 `mapPermissionAsked` 的先例 `Warn` 并按「未说明的提问」把原始载荷交人工，不静默丢弃。看不懂的请求交给人，是这个 adapter 已经确立的纪律。

requestID 只存在 runState 里，不进 `AdapterEvent` 契约——工具阻塞保证同一任务至多一个挂起请求，因此不需要像 permission 那样把 id 透到 manager。

### 4.3 `Send` 分流

Adapter 五动作里，审核者的答复统一走 `Send(ctx, taskID, text)`，现在一律发成新一轮 `prompt_async`。改为按「本任务有没有挂起的 question request」分流：

- **有**：解析答复 → `POST /question/{requestID}/reply` → 清 pending → **不**发 prompt（回合本来就在跑，再发一条 prompt 会开出第二个回合）
- **无**：原样 `prompt_async`（现有行为，不变）

这不改 Adapter 契约，只加 runState 字段。

**答复解析（分级）**——对每一问依序取一个 token，逐级尝试：

1. 编号 `N.M`（单问时允许省略成 `M`）→ 该问的第 M 个 label
2. label 原文，`TrimSpace` + 大小写归一后精确匹配
3. 都不中且该问 `custom: true` → 原文作为自定义答案透传
4. 都不中且 `custom: false` → **不发 reply**，重发同一张工单并带明确提示（"请填 1-3 或选项原文"）

`multiple: true` 时一问内允许逗号分隔多个 token，`answers[i]` 成为多元素数组。答案个数与问题个数不匹配时同样走第 4 条的重问路径。

重问而非猜测：猜错一个选项的代价是模型按错误前提继续干活，而重问的代价只是审核者多按一次——错误方向必须选后者。这与 B6 确立的「误升级好过漏放行」是同一取舍。

### 4.4 回合级去重

接通工具通道后模型有两条提问路：原生 `question` 工具、trailer 的 `{"ask":...}`。两条都保留（工具通道带选项、体验更好；trailer 是模型不调工具时的兵），但必须防止同一回合出两张工单。

runState 加取走式标记 `askedViaTool`：`mapQuestionAsked` 置位；`mapIdle` 分类得到 `kind == "ask"` 时若标记已置位（**取走**，读后即清），抑制该 trailer 工单、只记 Debug。

这是 B3 grok adapter 已经踩过并修好的同一个坑：模型通过工具通道问过之后，回合结束时又输出一段叙述被兜底当成第二个问题上报，一次提问给审核者两张工单。语义也一致——兜底通道存在的目的是「保证回合不静默结束」，本回合已通过工具给过问题时该诉求已经满足。

取走式而非常驻：标记的生命周期是一个回合，跨回合残留会让下一回合的真 trailer 提问被误抑制。

### 4.5 生命周期

| 时机 | 行为 |
|---|---|
| `Stop` 时有挂起请求 | 先 `POST /question/{id}/reject` 解阻塞，再杀进程；reject 失败只 `Warn` 不阻断（进程随即消失） |
| agentd 重启恢复 | `GET /question` 拉全量挂起请求，按 `sessionID` 过滤出本任务的，重新 emit 工单（去重集合是新的，所以会补发） |
| 任务已终结但请求仍挂起 | reject 兜底 |

重启恢复这条与 B18/B20/B24 确立的运行态重建纪律一致：**agentd 重启后，一切「executor 那边还等着人」的状态都必须被重新发现并重新唤醒审核者**，否则任务成为孤儿。`GET /question` 正是为此存在的端点。

## 5. 测试策略

### 5.1 turn 包单测（B48）

表驱动，六组：

| 输入形态 | 期望 |
|---|---|
| `g.{"branch":"x","commit":"y"}` | finish，取到 x/y（B48 现场） |
| `{"ask":"q"} 好的` | ask，取到 q（后缀正文） |
| `前缀 {"ask":"q"} 后缀` | ask，取到 q |
| 末行是正文、更早行是 `{"ask":...}` | ask（回退路径不回归） |
| 末行含 `{` 但不是合法 JSON（如 `见 {} 占位`） | none，且不 panic |
| 末行是合法 JSON 但无协议字段 | none |

### 5.2 opencode 包单测（B49）

- `mapQuestionAsked`：requestID 去重、多问渲染格式、`questions` 为空时的降级上报、**不清空回合缓冲**（断言 `clearTurn` 未发生——这条最容易在实现时漏掉）
- `Send` 分流：编号命中 / label 命中 / custom 透传 / 都不中重问，各一条；`multiple` 多选一条；答数不匹配一条
- 回合级去重：工具问过后同回合的 trailer ask 被抑制；下一回合的 trailer ask **不**被抑制（取走语义）
- 生命周期：`Stop` 时有挂起请求则调用 reject；恢复时按 sessionID 过滤补发

### 5.3 真机 e2e（devbox）

1. 触发模型调用 `question` 工具 → 工单带编号选项到达审核者 → `reply --answer "1.2"` → 模型在**同一回合**续接（不是新回合）→ 任务走完
2. B49 死锁形态不再复现：任务不再卡到 2h stall
3. B48：造一个 `g.{"branch":...}` 形态，确认 agentd.log 里不再出现「回合未输出协议 trailer，走 git 兜底」
4. agentd 重启：挂起 question 存活 → 重启 → 工单被补发

### 5.4 `custom` 透传：运行时降级，不设阻塞探针

§2.4 那条推断（`custom: true` 时自由文本能否直接进 `answers`）不设前置探针，改为**让代码自己承受两种结果**：

§4.3 第 3 级照常发 reply；服务端若以 4xx 拒绝（说明它校验 label 白名单），把该错误识别为「自定义答案不被接受」，落回第 4 级重问路径，工单提示改为「该问不接受自定义答案，请填 1-3 或选项原文」。

为什么不做阻塞探针（写计划时相对初稿的修订）：

1. 构造一个 `custom: true` 的挂起请求必须先让模型真的调用 `question` 工具，探针成本接近一次完整 e2e，却只换一个布尔值；
2. 探针结论会随 opencode 版本失效，而降级分支是长期有效的——**服务端拒绝自定义答案**这件事在生产里同样可能发生（不同版本、不同工具配置），代码本来就该扛得住；
3. 阻塞探针会让实现停在第一个 task 上等真机窗口。

真机 e2e（§5.3）仍会揭示实际走的是哪一条分支，届时把结论回填进本节。

## 6. 不做（YAGNI）

| 不做 | 理由 |
|---|---|
| 禁用 `question` 工具（`tools: {question: false}`） | 已验证可用，留作应急开关。接通通道后它是冗余的，且禁用会丢掉 options/multiple 这些结构化信息，审核者只能读一段纯文本 |
| 多张工单的「部分已答」中间态 | 需要 agentd 重启 / stop / 超时各定义一遍行为，状态机面明显变大；一张工单答完全部问题已经够用 |
| 改 Adapter 五动作契约 | requestID 留在 adapter 内即可，工具阻塞保证至多一个挂起请求 |
| claude / codex / grok 三家的提问通路 | 各有各的协议，探针实测均正常产出工单，没坏 |
| 侦测 running 状态的 tool part | §2.2 已说明这条思路作废——不需要猜工具状态，事件通道是一等的 |
