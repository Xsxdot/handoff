# opencode 子会话审批归属（B52）设计

## 1. 范围与动机

opencode 的 subagent 发起的权限请求被 handoff 丢弃，任务随后静默挂死：executor 进程活着、看门狗不触发、审核者侧零工单，opencode 在等一个永远不会到来的决策。

丢弃点是 `internal/executor/opencode/adapter.go` 的 `acceptForeign`。它对「会话 id 与本任务不符」的 `permission.asked` 打一条 WARN 后 `return false`，其文档注释给出的理由是：

> 本层无法把子会话映射回任务（没有可用的父子关系端点）

**这个前提是错的**，本次实测已推翻（见 §2）。

影响面（devbox 生产实例历史日志）：28 条 WARN，覆盖 5 个任务（`8f7a4f18` 8 条、`72b500bb`、`8999308e`、`9b36a1bf`、`f7d07ece`）。两种表现：

- **永久挂死**：`f7d07ece` 的时间线是 `00:22:41 最后一条进度事件 → 00:31:31 本 WARN → 零模型活动 → 02:22:47 看门狗判 stalled`。该 WARN 是永久静默前的最后一件事。
- **数十分钟空转**：`8f7a4f18` 的 8 条 WARN 是 **8 个不同的 permID**——模型重发同一条命令，每次重发都被再丢一次；10:09 到 10:46 共 37 分钟零进度，之后它自己绕开了。

准确的描述是「**数十分钟静默空转，有时永久**」，不是「必然致命」。

**本设计的范围**：只修 opencode，改动全部落在 `internal/executor/opencode/` 包内。

**修法约束（用户指定，原文）**：

> 不要把修复做成「取消会话校验」——校验存在的理由是防止跨任务串台，正确解法是把子会话正确归属到父任务，而不是不再校验。

## 2. 实证结论

以下每一条都在 devbox 隔离实例（`/tmp/hfb52`，监听 `127.0.0.1:7893`，独立 DataDir / 二进制 / 探针仓库，未触碰 7777 实例与 `~/.handoff/`）上真机验证，不是代码阅读推论。

### 2.1 缺陷可复现

探针任务 `4e3386dc-f467-426c-aa4d-e3c0fe066c24`，17 秒复现：

```
11:18:09 WARN 收到不属于本任务会话的审批请求，未产出工单
  own_session=ses_0112c6696ffeTEUNsypk6sh5PD
  event_session=ses_0112c44ccffe71cNCE4lYE1aeg
  permission="bash"
  patterns=["curl -s -o /tmp/b52-probe-opencode.txt https://example.com"]
```

挂起工单 0，`render.log` 全空——卡在第一个工具调用上。

### 2.2 父子关系端点存在

`GET /session/{子会话}` 返回：

| 字段 | 值 |
|---|---|
| `parentID` | `ses_0112c6696ffeTEUNsypk6sh5PD` —— **正是本任务的 own_session** |
| `title` | `Run probe curl command (@general subagent)` |
| `directory` | `/private/tmp/hfb52/data/worktrees/4e3386dc` |
| `agent` | `general` |
| `permission` | `[{permission:"task", pattern:"*", action:"deny"}]` |

### 2.3 嵌套深度恒为 1

上表最后一行：opencode **自己**给 subagent 下了 `task: deny`，子 agent 不能再开子 agent。因此归属判定只需比一次 `parentID`，不需要向上遍历。

### 2.4 应答回程可用

`POST /session/ses_0112c44ccffe71cNCE4lYE1aeg/permissions/per_feed3da44001o6mQdhLmCXyiqU`，体 `{"response":"once"}`：

- HTTP 200 `true`
- 挂起权限列表由 1 变 0
- `/tmp/b52-probe-opencode.txt` 真实创建，559 字节 example.com HTML
- 任务走到 `completed`，收尾报告「探针完成：subagent 已起并执行 curl，报告退出码 0」

**整条价值链验通，缺的只有 handoff 这一段接线。**

### 2.5 每任务一个 serve

`proc.go:110` 的 `freePort()`——每个任务起自己的 `opencode serve`，各自随机端口。所以 `/event` 的「全服务器广播」实际是「本任务这一个 serve 的广播」，流上出现的陌生会话本就只可能是本任务自己派生的子会话。

### 2.6 另外三家不需要同样的修复

同一份探针脚本对四家各跑一轮（先自报有无 subagent 能力，再让子 agent 写一个工作区外的文件，必然触发审批）：

| Executor | subagent 能力 | 子 agent 的审批请求 | 结果 |
|---|---|---|---|
| opencode | `@general subagent` | **丢弃，0 工单** | 卡死 |
| claudecode | `Agent` 工具 | 正常产出工单 | 通过 |
| codex | 子 thread（`019feee5…` → `019feee6…`） | 正常产出工单 | 通过 |
| grok | `spawn_subagent`（`019feee6-066f…`） | 正常产出工单 | 通过 |

隔离实例全生命周期内「不属于本任务会话」的 WARN 只有 1 条，即 opencode 那条。codex / grok 的子 agent 命令批准后真实执行，`/Users/sycm/b52-probe-{codex,grok}.txt` 各 9 字节。

**原因是传输形态，不是运气**：claudecode 走每任务一个 `perm.sock`、grok 走 ACP 私有连接按 `reqID` 应答、codex 走每任务一个 app-server websocket 按 `reqID` 应答——三者的应答通道天然绑死本任务，**根本不需要做「这个请求是不是我的」这个判断**。只有 opencode 是「共享广播 SSE + 按 session id 寻址」，必须自己判，而它判错了。

值得记一笔：codex 的子 agent 同样换了 thread id，它也有嵌套会话身份，只是 adapter 压根不按 thread 过滤，所以什么都没丢。opencode 的问题不是「只有它有嵌套」，而是「只有它做了一个会出错的归属判断」。

### 2.7 工单文案缺 subagent 标注是四家共性

四家的工单文案都不带任何 subagent 标记（claude `Bash: curl …`、codex `运行命令：/bin/zsh -lc '…'`、grok `Execute \`echo …\``），审核者看不出请求来自子 agent 还是主 agent。四家的 provenance 字段也都拿得到：opencode 有 `parentID`、claude 有 `parent_tool_use_id`、codex 有子 threadId、grok 自己就在报告里写出子 agent id。

**本设计只做 opencode 的标注**（认亲时顺手就有 title，零额外成本）。另外三家的标注单独记 B53：它们本来没坏，合进来会把测试面从一家扩到四家，收益只是可读性。

## 3. 设计

### 3.1 改动清单

| 文件 | 改动 |
|---|---|
| `internal/executor/opencode/api.go` | 新增 `GetSession` |
| `internal/executor/opencode/adapter.go` | `runState` 加两张表 + 一把锁；`acceptForeign`、`mapPermissionAsked`、`RespondPermission` |

包外零改动：`executor.AdapterEvent` 不动（认亲失败复用现成的 `progress` 事件，工单标注只是 `Text` 的内容），manager / store / CLI 全不碰。opencode 的会话模型是它自己的实现细节，归属判定就该在它自己的适配层收口——上层不该知道 opencode 有「子会话」这个概念。

### 3.2 API 层

```go
// sessionDetail 是 GET /session/{id} 的响应体形状（只取归属判定与工单标注所需字段）。
type sessionDetail struct {
    ID       string `json:"id"`
    ParentID string `json:"parentID"`
    Title    string `json:"title"`
}

// GetSession 取单个会话的详情，用于把子会话归属回父任务。
func (a *API) GetSession(ctx context.Context, sessionID string) (sessionDetail, error)
```

走现有的 `a.do` + `httpError` 路径。超时用**专门的** `ownershipTimeout = 5 * time.Second`，不复用 `unaryTimeout = 30s`：这个调用在 SSE 事件回调里同步执行，会短暂阻塞本任务的事件流。5 秒可接受——此刻任务本来就在等这个审批，且由 §2.5 阻塞范围只有本任务自己的 serve。

### 3.3 runState

```go
// childSessions 是「已认亲成功的子会话 id → 会话标题」。
// 认亲一次即缓存：一个回合里同一子会话会连发多条事件，每条都发 HTTP 会把网络
// I/O 放进事件热路径。
childSessions map[string]string

// permSession 是 permID → 该权限所属会话 id。
// 应答必须发回请求所在的会话（子会话的权限发给父会话 opencode 不认），而
// RespondPermission 的入参只有 permID。
permSession map[string]string

// sessMu 保护上面两张表。
// 不复用 turnMu：acceptForeign 在 mapEvent 里刻意排在 turnMu.Lock() 之前，
// 就是为了不在持锁时做网络 I/O，这条不能破。
sessMu sync.RWMutex
```

**锁序固定 `turnMu → sessMu`**（`mapPermissionAsked` 在 `turnMu` 下取 `sessMu`），`acceptForeign` 与 `RespondPermission` 只取 `sessMu`，无环。

**绝不持 `sessMu` 跨 HTTP 调用**：RLock 查缓存 → 解锁 → 发请求 → Lock 写入。同一个新子会话的两条事件挤在一起会发两次 `GET /session`，无害且幂等，不加 singleflight。

### 3.4 入向：认亲

`acceptForeign` 的 `permission.asked` 分支改为：

1. `event_session` 为空串 → **不发 HTTP**，直接判定失败走 §3.6（拿空 id 去 `GET /session/` 只会换来一个 404，白白阻塞 5 秒）
2. 命中 `childSessions` → 接受（Debug 日志，热路径不刷屏）
3. 未命中 → `GET /session/{event_session}`
   - `parentID == r.session` → 认亲成功，缓存 `title`，接受
   - 其余 → 判定失败，走 §3.6

**不向上递归**：由 §2.3，嵌套深度恒为 1。真出现二级说明 opencode 的 `task: deny` 不变量被打破了，那是断言失败，该让人看见，不是在事件热路径上做无界遍历。

**只缓存正结果，不缓存负结果**：一次网络抖动导致的判定失败若被缓存，这个子会话就被永久拉黑，后续每一条审批都丢。负结果不入表，下一条事件重新判。

### 3.5 出向：应答

`mapPermissionAsked` 记 `permSession[permID] = 事件所属会话`；来自子会话时给 `Text` 加前缀 `[子 agent: <title>] `，例如：

```
[子 agent: Run probe curl command (@general subagent)] bash: curl -s -o /tmp/b52-probe-opencode.txt https://example.com
```

两条边界：

- `title` 为空时降级为 `[子 agent] `，不留 `[子 agent: ] ` 这种空冒号
- 前缀在**现有空描述兜底之后**加：先按现有逻辑拼描述（拼不出时用「opencode 未提供权限描述（id …）」兜底），再在最前面加前缀。顺序反了会让兜底分支判空失败

`RespondPermission` 两级回退：

1. `permSession[permID]` 命中 → 发往该会话
2. 未命中 → Warn 后退回 `r.session`（当前行为）

只做两级。曾考虑第三级「`GET /permission` 查挂起列表反查 sessionID」，用于 agentd 恰好在某个子 agent 审批挂起时重启的场景（`permSession` 是内存表，重启即丢）——**不做**：该场景罕见，且失败是响的（POST 到父会话拿 4xx，审核者当场看到 reply 报错），不是静默的。真被咬到再加。

### 3.6 错误处理

认亲的四种失败，全部 fail-closed 且**不静默**——Warn + 一条 `progress` 事件（审核者在 `handoff show` 的事件历史里可见）后丢弃：

| 情况 | 理由 |
|---|---|
| `GET /session` 网络失败 / 非 2xx / 超时 | **不重试**：上游天然有重试，模型被拒后会重发同一条命令（`8f7a4f18` 实测 8 次重试、8 个不同 permID）。在 SSE 热路径上再叠一层重试只会把阻塞翻倍 |
| `parentID` 为空 | 顶层会话，确实不是我们的 |
| `parentID` 非空但 ≠ own_session | 嵌套深度 >1，§2.3 的不变量被打破。Warn 里必须点名这一点 |
| `permission.asked` 缺 `sessionID` | 保持现状丢弃：`/event` 上一条无归属的审批门不能 fail-open |

**非 `permission.asked` 的子会话事件继续丢弃**（保持现状）。回合记账（`lastAssistantMsgID` 水位、`pendingDelta`、`mapIdle` 分类）整套围绕**单一会话的消息序列**构建，交错第二个会话的消息会直接污染水位与空闲判定。子 agent 干了什么仍然看得到——四家探针都证明主 agent 会在自己的收尾正文里如实转述。

**已知限制**：子 agent 运行期间 `render.log` 没有新输出，审核者在实况窗口里看不到子 agent 的过程，只能看到工单和主 agent 事后的转述。

### 3.7 日志与注释

日志全部走 `a.log`（无一处 `fmt.Printf`）：

| 点位 | 级别 | 字段 |
|---|---|---|
| 缓存命中 | Debug | `task` / `child` |
| 认亲开始 | Info | `task` / `event_session` |
| 认亲成功 | Info | `task` / `child` / `parent` / `title` / `elapsed_ms` |
| 四种失败 | Warn | 各自独立文案 + `cause`，能直接 grep 区分 |
| `RespondPermission` | Info | 现有字段加 `session` 与「来自映射表 / 退回父会话」标记 |

注释重点：**`acceptForeign` 的文档注释必须重写**。它现在写着「本层无法把子会话映射回任务（没有可用的父子关系端点）」——这句话是本缺陷的根源，且已被 §2.2 推翻。新注释要把三条实测结论钉进去：`GET /session/{id}` 返回 `parentID`；子会话被 opencode 自己下了 `task: deny` 所以深度为 1；每任务一个 serve 所以流上的陌生会话本就只可能是自己的子会话。写清楚，下一个人才不会把假设改回去。

另需注释：`sessionDetail` 为何只取三个字段；`sessMu` 为何不复用 `turnMu`；为何不向上递归；为何不缓存负结果；为何非 permission 的子会话事件仍丢弃。

## 4. 测试策略

现成基建：`adapter_test.go:134` 的 `newFakeServer` 是可脚本化 SSE 的假 opencode，`startFakeRun` 起一轮。

**当前 `acceptForeign` 零测试覆盖**（全仓 grep 无命中）——这就是它能带着一个错误前提上线的原因。

单元测试六例，全部先红后绿：

| # | 用例 | 断言 |
|---|---|---|
| 1 | 子会话 `permission.asked`，假服务器 `GET /session/{child}` 返回 `parentID == own` | 产出工单，`Text` 带 `[子 agent: …] ` 前缀 |
| 2 | 同一子会话连发两条事件 | `GET /session` 只被调用一次（假服务器计数） |
| 3 | `GET /session` 返回 500 | 无工单，收到一条 `progress` 事件 |
| 4 | 首次 500、第二次 200 | 第二次成功产出工单（证明负结果未被缓存） |
| 5 | `parentID` 指向第三方会话 | 无工单 + `progress` |
| 6 | 对子会话 permID 调 `RespondPermission` | POST 打到 `/session/{child}/permissions/{permID}`，不是父会话 |

## 5. 验收闸门

**回归**：`go test ./internal/executor/opencode/...` 全绿。`regression_group_a`、`replay_spike`、`reconcile_internal` 是历史踩坑的固化，改会话过滤路径必须证明没碰坏它们。

**真机 e2e**：devbox 隔离实例 `/tmp/hfb52`（7893，独立 DataDir / 二进制 / 探针仓库；不触碰 7777 与 `~/.handoff/`），用 §2.1 那份已复现过缺陷的探针脚本重跑 opencode。四条标准缺一不可：

1. 挂起工单数 **1**（当前 0）
2. 工单文案带 `[子 agent: Run probe curl command (@general subagent)]`
3. 批准后 `/tmp/b52-probe-opencode.txt` 真实创建，任务走到 `completed`
4. 本次验收运行期间，agentd.log 新增的「不属于本任务会话」WARN 数 **0**（复现那次留下 1 条，按时间戳区分，不要拿累计数当依据）

## 6. 不做的事

| 不做 | 理由 |
|---|---|
| 取消会话校验 | 用户明确约束（§1）。校验防的是跨任务串台，正确解法是把子会话归属回父任务 |
| 向上递归找祖先 | §2.3 深度恒为 1；出现二级是断言失败，该报警不该兜底 |
| `GET /permission` 反查会话（应答第三级回退） | 场景罕见且失败是响的。见 §3.5 |
| 接受子会话的非审批事件 | 会污染回合记账水位。见 §3.6 |
| claude / codex / grok 的 subagent 标注 | 它们本来没坏（§2.6）。单独记 B53，见 §2.7 |
| 把「一进程一任务」不变量单开 backlog 行 | 实测证明三家确实是私有通道，无跨任务串台风险。README「执行者差异」补一段话即可 |

## 7. Windows 影响

无。改动只涉及 HTTP 调用与内存表，不含路径、信号、进程或 unix socket 语义。
