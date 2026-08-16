# 探针：四家 executor 报不报**会话级累计用量与花费**（B83）

上一份探针 [2026-08-12-w4-tui-model-token-probe.md](2026-08-12-w4-tui-model-token-probe.md)
解的是「当前 context 占用」（B80）。本文解的是另一个口径：**这个会话一共烧了多少**。
两个口径各有各的帧，混用就是静默错误——这条在上一份的 §4.2 已经踩过一次。

**探测日期**：2026-08-13。
**探测机器**：本机 mac（grok 1.0.3、codex-cli 0.144.1）与 mac-02 / `100.73.238.21`
（claude 2.1.226、opencode）。
**方法**：真实多回合会话实抓，不是读文档、不是凭字段名类推。
每家都刻意跑**三个回合**——单回合分不出「本回合」和「会话累计」，上一份就是栽在这上面。

---

## 0. 结论先说

| executor | 会话累计 tokens | 会话累计花费 | 分母（窗口上限） | handoff 要不要自己加 |
|---|---|---|---|---|
| **claudecode** | ✅ `result.modelUsage.<model>` 白给 | ✅ `total_cost_usd` 白给 | ✅ **`contextWindow`**（推翻旧结论） | **不用** |
| **codex** | ✅ `tokenUsage.total` 白给 | ❌ **不报** | ✅ `modelContextWindow` | 不用（tokens）／花费只能估算 |
| **grok** | ❌ 只给本回合 | ✅ 本回合 `costUsdTicks`，要自己加 | ✅ `totalContextTokens` | **要** |
| **opencode** | ❌ 线上只给单条 message | ✅ 每条 message 的 `cost`，要自己加 | ❌ 无 | **要** |

三条结论比逐家的字段清单更重要：

**发现一：「累计」这件事四家分成两半，且分法与「分母」那件事不同。**
claudecode 与 codex 白给会话累计；grok 与 opencode 只给单次，handoff 必须自己累加。
这与 B80 的分母划分（codex/grok 有、claudecode/opencode 无）**不是同一条线**——
别指望用一个「这家全不全」的标签打发四家。

**发现二：不自报花费的只有 codex 一家。**
B83 那张「模型 → API 牌价」的估算表只服务 codex，其余三家自报。这比最初担心的范围小得多。
但下面 §1 有个坑：grok 的自报**有条件**，认证方式不对就整块缺席。

**发现三（最要紧的一条，见 §2.2）：所谓「白给会话累计」只在进程存活期间成立。**
用同一个 session id `--resume` 之后，claudecode 的累计**从零重新开始**。
handoff 的任务会跨进程恢复，所以四家必须统一成「handoff 自己按幂等键逐次累加」，
一个执行器的累计字段都不能依赖。上表「不用」那一列因此只是「进程内不用」，不是设计结论。

**发现四（推翻旧结论）：claudecode 有分母。**
旧探针 §2 写「没有任何字段告诉你上限是多少」——那是只看了 `assistant` 消息。
`result` 行的 `modelUsage.<model>.contextWindow` 就是分母（实测 `262144`）。
**这条直接影响已经实现完的 B80**，见 §5。

---

## 1. grok：只给本回合，花费要自己加，且自报是有条件的

**做法**：`grokprobe` 起 `grok agent serve`，在**同一个 session** 上串行发三次
`session/prompt`（"只回一个字：甲/乙/丙"），逐回合记 `result._meta.usage`
与 `turn_completed` 通知的 usage。串行是刻意的：并发发 prompt 会让用量归属无从判断。

三个回合的 `turn_completed.usage`（两个来源逐回合**完全一致**）：

| 回合 | numTurns | inputTokens | outputTokens | cachedRead | modelCalls | costUsdTicks |
|---|---|---|---|---|---|---|
| 1 | 1 | 34501 | 63 | 5888 | 1 | 605480000 |
| 2 | 1 | 34585 | 41 | 34432 | 1 | 177680000 |
| 3 | 1 | 34647 | 41 | 34560 | 1 | 177000000 |

**判读**：

- `numTurns` 三回合恒为 **1**——它数的是**本回合内**的模型轮数，不是会话计数器。
  按名字以为它是「第几轮」会错。
- `inputTokens` 34501 → 34585 → 34647，每回合只涨几十。这是 **context 占用**在长
  （对话历史加长），**不是累加**——累加应该是 34501 → 69086 → 103733。
- `costUsdTicks` 605480000 → 177680000 → **177000000**，**会下降**。累计不可能下降，
  所以它铁定是本回合的花费。第一回合贵是因为冷启动 + 缓存写入。

**结论：grok 的 `turn_completed.usage` 是本回合的，会话累计要 handoff 自己加。**
grok 自己的文档也是这么说的——`total_cost_usd_ticks` 那段写明按次求和才对得上服务端的
用量导出（`~/.grok/docs/user-guide/14-headless-mode.md`）。

### 1.1 `costUsdTicks` 的单位与三条缺席纪律

同一份文档解决了单位问题，外加三条不遵守就会算出假账单的纪律：

1. **单位：1 USD = 10^10 ticks。** 所以上表是 $0.0605 / $0.0178 / $0.0177。
   用整数 ticks 求和、最后一步才转美元——文档给的理由是浮点求和对不上服务端的账。
2. **缺席不等于免费。** 文档原话是花费缺席意味着 "unreported or incomplete, never free"。
   而且**花费只对 API-key 流量打戳，pool/OAuth 路径经常整块没有**。
   也就是说「grok 自报花费」这条对**部分用户不成立**，handoff 必须能显示「不知道」。
3. **`cost_is_partial` 为真时，grok 自己会把所有花费浮点一并省略**（`total_cost_usd`
   与每个 `modelUsage.*.costUSD`），刻意不让消费者把分项加成一份假的完整账单。
   handoff 累加时要照抄这个语义：任何一次缺花费，整个会话的花费就是「不完整」，
   不能拿已知的部分冒充总额。

### 1.2 顺带：官方文档背书了「同一产品两套口径」

上一份探针 §4.1 的附带发现 1（ACP 的 `inputTokens` 含缓存、headless 的 `input_tokens`
不含）现在有官方原文佐证：文档明说 ACP 的 `_meta.usage.inputTokens` 是完整的 prompt 总和，
只有 headless 投影会把缓存减掉。**按字段名跨口径类推必错**，这条现在是有据可查的事实
而不是我们的观察。

---

## 2. claudecode：三样都白给（且推翻旧结论）

**做法**：`ccprobe/probe.py` 按 `proc.go` 的 `claudeArgv` 原样起
`claude -p --input-format stream-json --output-format stream-json --verbose`，
往 stdin 串行投三条 user message，逐轮抓 `result` 行。
（本机 claude 的 OAuth 已过期，三轮全是 `api_error`；实测在 mac-02 上完成。）

`result` 行**同时给了两个口径**，这是四家里最干净的一家：

| 轮 | `usage.input_tokens` | `usage.cache_read` | `modelUsage.*.inputTokens` | `modelUsage.*.cacheReadInputTokens` | `modelUsage.*.outputTokens` | `total_cost_usd` |
|---|---|---|---|---|---|---|
| 1 | 2776 | 29952 | 2776 | 29952 | 26 | 0.029506 |
| 2 | 265 | 32512 | **3041** | **62464** | **45** | 0.047562 |
| 3 | 54 | 32768 | **3095** | **95232** | **63** | 0.064666 |

- **`usage.*` 是本轮**（54 = 第三轮真实输入）。
- **`modelUsage.<model>.*` 是会话累计**，逐项验算全中：
  2776+265=3041、3041+54=3095；29952+32512=62464、+32768=95232；26+19=45、+18=63。
- **`total_cost_usd` 是会话累计花费**，单调递增，与 `modelUsage.*.costUSD` 同值。
- **`contextWindow: 262144` 就在同一块里**（还有 `maxOutputTokens: 32000`）。

**所以旧探针 §2「claudecode 没有分母」是错的**，成因是只看了 `assistant` 消息、
没看 `result` 行的 `modelUsage`。修正见 §5。

**两个坑**：

1. **assistant 消息会重复推送**。三轮实测收到 **6 条** assistant，两两同值
   （#1=#2、#3=#4、#5=#6）。与 opencode 的重复推送同形（旧探针 §3.1）。
   按「来一条加一次」做累计会翻倍。
2. **认证失败时 `subtype` 仍然是 `success`**。本机那次失败的 result 行是
   `{"is_error": true, ..., "terminal_reason": "api_error", "subtype": "success",
   "result": "Failed to authenticate: OAuth session expired…"}`，usage 全 0。
   只认 `subtype` 会把一次认证失败当成一次零消耗的成功回合记进累计。
   要认的是 `is_error` 与 `terminal_reason`。

### 2.2 补验：`--resume` 之后累计归零——「会话累计」其实是「进程累计」

上面三轮是**同一个进程**。handoff 的任务不是这样跑的：agentd 重启、executor 崩溃、
任务恢复，都会起一个新进程并 `--resume <session_id>` 接回原会话。所以必须再问一次：
新进程里的 `modelUsage` 带不带前一个进程的量？

**做法**：拿上面那个会话的 id 重跑同一个探针，只把 `--session-id` 换成 `--resume`。

| 轮 | `usage.input_tokens` | `usage.cache_read` | `modelUsage.*.inputTokens` | `modelUsage.*.cacheRead` | `modelUsage.*.output` | `total_cost_usd` |
|---|---|---|---|---|---|---|
| 1 | 98 | 32768 | 98 | 32768 | 14 | 0.017224 |
| 2 | 138 | 32768 | 236 | 65536 | 28 | 0.034648 |
| 3 | 178 | 32768 | 414 | 98304 | 42 | 0.052272 |

前一个进程收尾时是 in=**3095** / cacheRd=**95232** / out=**63** / cost=**$0.064666**。
新进程第一轮是 in=**98** / cacheRd=32768 / out=14 / cost=**$0.017224**——**一点都没带过来**。

会话内容本身确实恢复了（第一轮 `cache_read` 就有 32768，`input_tokens` 只有 98，
说明历史上下文在缓存里），**归零的只是用量计数器**。

**所以 §0 那张表的「要不要自己加」一列，正确读法是「进程内要不要自己加」。**
跨进程看，四家全都要 handoff 自己累加：

| executor | 逐次量取哪里 | 幂等键 |
|---|---|---|
| claudecode | `assistant.message.usage`（本轮） | assistant message 的 `id` |
| codex | `tokenUsage.last`（本次调用） | `params.turnId` |
| grok | `response_completed` / `turn_completed` 的本回合 usage | `_meta.promptId`（实测三回合各不相同） |
| opencode | `message.updated` 的 `info.tokens` | `info.id` |

统一成这一套的附带好处是**不用再关心哪家给不给累计**——四家一个算法，
agentd 重启、executor 换进程、任务 resume 都不影响正确性，代价只是必须落库且幂等。

**codex 的 `thread/resume` 未验**（跑一次要花额度改探针）。但它不影响设计：
上面这套一律取「本次调用」的量，`tokenUsage.total` 延不延续都用不到它。
真要用 `total` 才需要先验这一条。

---

## 3. codex：tokens 白给累计，花费一个字都不报

取自上一份 §1.1 那次 app-server 实验的落盘（零成本复查，没有再花额度）：

```json
{"method":"thread/tokenUsage/updated","params":{"tokenUsage":{
  "total":{"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
           "outputTokens":5,"reasoningOutputTokens":0},
  "last": {"totalTokens":24673,"inputTokens":24668,"cachedInputTokens":9984,
           "outputTokens":5,"reasoningOutputTokens":0},
  "modelContextWindow":258400}}}
```

`total` 就是会话累计（那次只跑一个回合，所以 `total == last`；跨回合累计由 codex 自己的
rollout 文件佐证——旧探针 §1 里 `total_token_usage.input_tokens` 是 378848，
而 `last_token_usage` 是 24285）。

**整个 `tokenUsage` 块里没有任何花费字段。** 翻遍这一帧也没有 `cost` / `usd` / `price`。
所以 **codex 是 B83 那张牌价估算表唯一的服务对象**，其余三家自报（grok 有条件自报，见 §1.1）。

`cachedInputTokens` 是 `inputTokens` 的**子集**（旧探针 §1 已定），累计口径下同样成立——
把它当加项会重复计数。

---

## 4. opencode：库里有会话汇总，线上只见单条

**库里有**。`~/.local/share/opencode/opencode.db` 的 `session` 表带一整排汇总列：
`cost`、`tokens_input`、`tokens_output`、`tokens_reasoning`、`tokens_cache_read`、
`tokens_cache_write`。取一个真实会话核对，与逐条 message 求和**一分不差**：

```
session ses_0046bbc80ffeda  cost=0.004912 input=29339 output=3821 reasoning=10910 cacheRead=568448
逐条 assistant 求和(19 条)   cost=0.004912 input=29339 output=3821 reasoning=10910 cacheRead=568448
```

**线上未见**。handoff 读的是 SSE `/event` 广播，不是这个库。旧探针 §3.1 已确认
`message.updated` 推的是**完整的单条 message 对象**（含该条的 `cost` 与 `tokens`），
没有会话级汇总。本轮想再抓一次活的，但当时那个 opencode 已经空闲，
40 秒只收到 `server.connected` + `server.heartbeat`——**没有反证，也没有正证**。

**处置**：按「线上只有单条」设计，即 handoff 自己按 `info.id` 幂等累加。
这是安全的一侧：真有 session 级事件时改成直接读更省事，反过来则是漏账。
`session` 表的存在只说明**这个数字是可算的、且算法就是逐条相加**——
它给了我们一份可对账的真值，不是一条可用的通道。

---

## 5. 对 B80 的影响：claudecode 的分母是个现成的缺口

B80（`feat/b80-executor-model-usage`，7 个 commit 已完成）里，claudecode 的实现写着：

```go
// claudecode 的协议里没有任何字段给窗口上限，所以 ContextWindow 恒为 nil，
// 界面据此只显绝对值——不去猜、不查表。
```

**这句注释现在是错的**，`result.modelUsage.<model>.contextWindow = 262144` 就在协议里。
成因是 B80 的 spec 基于旧探针，而旧探针只看了 `assistant` 消息——**不是执行者做错了**。

补它要多解一种行：现在解的是 `assistant`（每轮的占用），分母在 `result`（回合结束才有）。
代价是**第一个回合结束前 claudecode 没有分母**，界面从绝对值切成百分比要晚一个回合。
这可以接受——总比永远显示不了百分比好。

---

## 6. 对 B83 的设计约束（不在本文回答，但探针已经把边界钉死）

1. **累加必须落库且幂等，而且是四家都要（§2.2）。** B80 的去重是**内存态**
   （`Manager.lastUsage`），agentd 重启后首帧必写一次——对「当前占用」无害（覆盖同值），
   对「累计」就是重复计数。幂等键：opencode 用 `info.id`，grok 用 `_meta.promptId`
   （本轮实测三个回合的 `promptId` 各不相同，可用）。
2. **花费的缺席是一等状态，不是 0。** 三种缺席各不相同：codex 从不报（→ 估算）、
   grok 按认证方式可能整块不报（→ 显示未知）、grok 的 `cost_is_partial`（→ 显示不完整）。
   全部塞进「$0.00」就是把三种「不知道」压成一个错误的「免费」。
3. **「输入 / 缓存输入」并排显示前必须归一化。** 累计口径下四家依旧不同：
   codex 的 `cachedInputTokens` 是 `inputTokens` 的子集；claudecode 的
   `cacheReadInputTokens` 与 `inputTokens` 平行相加；grok 的 ACP 口径含缓存、
   headless 口径不含；opencode 的 `cache.read` 与 `input` 平行相加。
   不归一化就照排，同一个「输入」标签在四家下是四个意思，而且不报错。
4. **重复推送是两家的共性**（claudecode 的 assistant、opencode 的 message.updated），
   累计逻辑必须按 id 幂等，不能按「收到即加」。

---

## 7. 复现命令

只读的两条（零成本）：

```bash
# opencode：会话汇总列 vs 逐条求和
ssh sycm@100.73.238.21 'sqlite3 -header -column "file:$HOME/.local/share/opencode/opencode.db?mode=ro" "select substr(id,1,18) id, round(cost,6) cost, tokens_input, tokens_output, tokens_cache_read from session order by rowid desc limit 3;"'

# grok：花费单位与缺席语义的官方原文
grep -n -A6 'total_cost_usd_ticks' ~/.grok/docs/user-guide/14-headless-mode.md
```

花额度的两条（各跑三个真实回合，探针源码在会话 scratchpad 的
`grokprobe/main.go` 与 `ccprobe/probe.py`，不入库）：

```bash
# grok：同一 session 串行三回合，比 turn_completed.usage 是否累加
cd <空目录> && PROBE_PORT=47421 ./grokprobe

# claudecode：stdin 串行投三条 user message，比 result 的两个口径
ssh sycm@100.73.238.21 'cd /tmp/ccprobe && PROBE_CWD=/tmp/ccprobe/emptycwd python3 probe.py'
```

两个探针都必须用**空的 cwd**：executor 会扫工作目录建上下文，指到大仓库上会让
`input_tokens` 混进一堆与本次测量无关的量，三个回合的差值就没法读了。
