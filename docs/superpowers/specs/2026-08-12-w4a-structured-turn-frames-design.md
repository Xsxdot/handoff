# W4a：结构化回合流设计

> 日期：2026-08-12
> 状态：设计已逐节确认
> 上游：`docs/superpowers/specs/2026-08-11-web-console-master-design.md` §5（W4 边界）
> 工作分支：`handoff/web-console`（不合入 `main`）
> 下游：W4b（Web 回合渲染）、W4e（`handoff tui`）都消费本期产出的契约

---

## 0. 一句话

把四个 adapter **已经解析出来、但用完就扔**的回合内容（思维链、工具调用、工具结果）定型、留下、送出去，
让浏览器与 CLI 能忠实复现 executor 的全过程。

---

## 1. 背景与问题

### 1.1 今天丢了什么

`internal/executor/turn/render.go` 的 `AppendRender(path, delta string)` 只接受**纯文本增量**。
四个 adapter 全都只把模型正文喂给它，落成 `render.log`——一坨无结构的字节。
`internal/agentd/render_stream.go` 的文件头注释写得很直白：「不解析内容：render.log 是模型回合文本的原样增量，本文件只做字节搬运」。

而结构化内容**在 adapter 里是完整存在的**：

| executor | 已解析出的结构 | 代码位置 |
|---|---|---|
| Claude Code | `text` / `tool_use` / `tool_result` 块，且按工具名分 `Bash` / `Edit` / `Write` | `internal/executor/claudecode/adapter.go:552,560,597,747,755` |
| opencode | `message.part` 的 `type=text` 与 `type=tool`（带 `state.status`） | `internal/executor/opencode/api.go:441,444` |
| codex | `item/reasoning/textDelta`、`item/reasoning/summaryTextDelta` 等 | `internal/executor/codex/adapter.go:60-61`、`items.go:162` |
| grok | ACP 流增量 | `internal/executor/grok/adapter.go:411,815` |

这些解析结果**被用于权限闸与完成判定，然后丢弃**。思维链更是被**刻意隔离**：

- claude：「thinking_delta 是思考过程，隔离不进 render.log 与回合文本」（`adapter.go:19`、`stream.go:174`）
- opencode：「reasoning/tool 等非 text part 的增量隔离不进」（`adapter.go:25`）

### 1.2 这个隔离必须保留

隔离本身是**对的**：思维链一旦回流进回合文本，`turn.ParseTrailer` 会把模型「我打算输出 `{"ask":...}`」
这样的**思考**当成真的提问，权限闸也会被污染。

因此 W4a 做的**不是拆掉隔离，而是把「隔离」从「丢弃」改成「分流」**：
思维链照旧不进回合文本、不进 trailer 解析、不进权限闸，但落进结构化帧流。

### 1.3 为什么不是终端保真

「打开 executor 的原生 TUI 一比一还原」这条路走不通：四个 executor 全都跑在 headless 模式
（opencode 是 `opencode serve` + SSE，claude 是 stream-json，grok 是 ACP），
**没有任何原生 TUI 进程在跑**，也就没有画面可 attach。
opencode 理论上能另起 TUI 客户端连同一个 session，但那只覆盖四分之一，
且终端画面不可搜索、不可折叠、无法与 Web 的工单审批联动。

结构化回合流是唯一对全部四个 executor 都成立的路线，
而 `internal/executor/turn` 这个包本来就是 executor 无关的——架子是现成的。

---

## 2. 范围

### 2.1 本期做

- `internal/executor/turn` 新增 `FrameWriter`：executor 无关的帧编码与追加写
- 四个 adapter 各自把已有的解析结果映射成帧
- agentd 在写 events 时同步追加 `event` 引用帧
- `GET /api/tasks/{id}/frames`：offset / tail / follow 流式读取
- `handoff frames <task>` CLI
- `proto.Frame` 进契约 fixture

### 2.2 本期不做

- **Web 端渲染**：W4b。本期产出的是数据与端点，不动 `web/`
- **`handoff tui`**：W4e
- **退役 `render.log`**：见 §5，本期并存
- **服务端按类型查询帧**（「给我所有 Bash 调用」）：前端在已加载的帧上过滤足够，
  服务端查询要求索引，而帧是文件——真需要时再说
- **帧的轮转与清理**：帧随任务目录走，与 `render.log` 同待遇

### 2.3 一个事实澄清

**`done` 不删任务目录。** 仓库里没有任何 `os.RemoveAll` 碰任务目录
（唯一一处在 `internal/skill/install.go:101`），本地 2026-08-08 的 10 个 `render.log` 至今仍在。
skill 文档里「清任务目录」那句与代码不符，是文档的账。

对本期的意义：**归档后的任务仍能回看完整帧流，不需要额外的保留机制**。

---

## 3. 帧契约

### 3.1 存储位置与格式

`<DataDir>/tasks/<task-id>/frames.jsonl`，**只追加**，每行一个 JSON 帧，行尾 `\n`。

选择文件而非 SQLite 表的理由：帧是**只追加、按序读、随任务归档**的——这正是文件擅长、SQL 不擅长的。
而且 `render_stream.go` 的 offset/tail/follow 机制已经验证过，换成「每行一帧」客户端改动极小；
跨机也免费——W3a 的 `byTask` 透明转发覆盖 `/api/tasks/{id}/...` 全部子路径，`forwardTo` 的 `io.Copy` 支持流式直通。

若并进现有 `events` 表：控制面事件（个位数到几十条）会被数据面帧（千条级）淹没，
`wait` 与工单那条链路要到处加过滤，W3a 的镜像还会把远端每一帧都复制到本机。

### 3.2 帧的字段

```go
// Frame 是一条结构化回合帧。
type Frame struct {
    Seq  int64     `json:"seq"`            // 任务内单调递增，从 1 开始
    TS   time.Time `json:"ts"`             // RFC3339 毫秒
    Turn int       `json:"turn"`           // 回合序号，从 1 开始
    Type FrameType `json:"type"`

    Part string `json:"part,omitempty"`    // part 标识：text/reasoning 靠它拼接，tool_call/tool_result 靠它配对

    Delta string `json:"delta,omitempty"`  // text / reasoning：文本增量

    Tool   string `json:"tool,omitempty"`   // tool_call：工具名
    Input  string `json:"input,omitempty"`  // tool_call：入参（可能截断）
    Output string `json:"output,omitempty"` // tool_result：输出（可能截断）
    Status string `json:"status,omitempty"` // tool_result：ok / error / 上游原文

    Truncated bool  `json:"truncated,omitempty"` // Input/Output 是否被截断
    Bytes     int64 `json:"bytes,omitempty"`     // 截断前的原始字节数

    RefSeq int64  `json:"ref_seq,omitempty"` // event 帧：events 表的 seq
    Event  string `json:"event,omitempty"`   // event 帧：事件类型名（冗余，见下）

    Reason string `json:"reason,omitempty"`  // turn_start：见下
}

type FrameType string

const (
    FrameText       FrameType = "text"
    FrameReasoning  FrameType = "reasoning"
    FrameToolCall   FrameType = "tool_call"
    FrameToolResult FrameType = "tool_result"
    FrameEvent      FrameType = "event"
    FrameTurnStart  FrameType = "turn_start"
)
```

`Reason` **只有两个取值**，与 Adapter 契约一一对应：`dispatch`（`Adapter.Start`）、`send`（`Adapter.Send`）。
不细分「续接指令」与「回答提问」——`Send` 是单一方法，adapter 分不出来，编出来的区分是假的。

**`Seq` 与 `events.seq` 是两套编号**，不要混用。帧 seq 是任务内的、从 1 开始的行号；
events 的 seq 是库级全局自增。`event` 帧用 `RefSeq` 指向后者。

**`Event` 字段是刻意的小冗余**：只存类型名（`permission_request` / `completed` / …），
让前端不查 events 表也知道该画什么形状的卡片。类型名是稳定的，不会漂移。

### 3.3 六种帧

| type | 语义 | 关键字段 | 粒度 |
|---|---|---|---|
| `text` | 模型正文 | `part` `delta` | 增量 |
| `reasoning` | 思维链 | `part` `delta` | 增量 |
| `tool_call` | 工具调用 | `part` `tool` `input` | 一次性完整帧 |
| `tool_result` | 工具结果 | `part` `status` `output` | 一次性完整帧 |
| `event` | 控制面事件引用 | `ref_seq` `event` | 一次性 |
| `turn_start` | 回合边界 | `reason` | 一次性 |

**`part` 的分配规则**：上游有 part / block / item 标识时**原样沿用**（opencode 的 `part.id`、
claude 的 content block 序号、codex 的 item id）；上游没有时由 `FrameWriter` 在**回合内**
按 `p01` `p02` … 递增分配。`part` 只需在**同一回合内**唯一，跨回合可以重复。
`tool_call` 与其 `tool_result` 必须用同一个 `part` 配对——这是前端把结果挂回调用卡片的唯一依据。

样例（省略号处为省略的字段）：

```json
{"seq":1,"ts":"2026-08-12T10:30:00.123Z","turn":1,"type":"turn_start","reason":"dispatch"}
{"seq":2,"ts":"2026-08-12T10:30:02.001Z","turn":1,"type":"reasoning","part":"p01","delta":"先看一下测试怎么写的"}
{"seq":3,"ts":"2026-08-12T10:30:04.220Z","turn":1,"type":"text","part":"p02","delta":"我来实现 probeWorkspaces。"}
{"seq":4,"ts":"2026-08-12T10:30:05.010Z","turn":1,"type":"tool_call","part":"p03","tool":"Bash","input":"go test ./..."}
{"seq":5,"ts":"2026-08-12T10:30:31.700Z","turn":1,"type":"tool_result","part":"p03","status":"ok","output":"ok  github.com/…","bytes":142}
{"seq":6,"ts":"2026-08-12T10:31:02.400Z","turn":1,"type":"event","ref_seq":88,"event":"permission_request"}
{"seq":7,"ts":"2026-08-12T10:34:11.900Z","turn":2,"type":"turn_start","reason":"send"}
```

### 3.4 为什么 text/reasoning 走增量而不是快照

opencode 的 `message.part.updated` 携带的是该 part 的**全量文本快照**。
往只追加文件里每次写一份快照是**平方级膨胀**——一个 5000 字的回合会写出几百万字节。

增量则与 `render_stream.go` 的 offset/follow 机制天生契合（都是「追加 + 从偏移续读」），
也才有原生 TUI 那种逐字流出的观感。代价是渲染端要按 `part` 拼接，那是前端一个 reducer 的事。

`tool_call` / `tool_result` 不走增量：工具入参在调用时就是完整的，结果在返回时也是完整的，
拆成增量没有任何好处。

### 3.5 能力差异诚实缺席

某个 executor 没有思维链，就**没有 `reasoning` 帧**。不伪造、不用正文硬凑、不插一条「本执行者不支持思维链」的假帧。
前端看到没有 reasoning 帧就不画那一栏——这与 W3a「不可达是数据不是错误」是同一条纪律的延伸：
**缺席要看得见，但不能靠编造内容来表达**。

---

## 4. 写入路径

### 4.1 FrameWriter

`internal/executor/turn/frames.go` 新增：

```go
// FrameWriter 把结构化回合帧追加进 frames.jsonl。
type FrameWriter struct { /* path, mu, seq, turn */ }

func NewFrameWriter(taskDir string) (*FrameWriter, error)   // 打开/创建，恢复 seq 与 turn
func (w *FrameWriter) BeginTurn(reason string) error        // turn++ 并写 turn_start
func (w *FrameWriter) Text(part, delta string) error
func (w *FrameWriter) Reasoning(part, delta string) error
func (w *FrameWriter) ToolCall(part, tool, input string) error
func (w *FrameWriter) ToolResult(part, status, output string) error
func (w *FrameWriter) EventRef(refSeq int64, eventType string) error
```

`BeginTurn` 的 `reason` 只接受 `dispatch` / `send`（§3.2）。

要点：

- **一把 mutex 保护 seq 分配与写入**：SSE/stream-json 的处理可能多 goroutine，
  seq 必须与写入顺序严格一致，否则续传会错位
- **`NewFrameWriter` 恢复 seq 与 turn**：agentd 重启后 adapter 重建，
  读文件最后一行的 `seq` 与 `turn` 接着写。文件不存在则从 `seq=1, turn=0` 起
- **写失败只 Warn 不中断回合**：与 `AppendRender` 同一纪律——
  可见性是增强能力，不值得为它挂掉任务

### 4.2 回合边界由 Adapter 契约天然界定

`executor.Adapter` 的五动作里，**`Start` 开启第 1 个回合，每次 `Send` 开启下一个回合**。
adapter 在这两处各调一次 `BeginTurn`（`dispatch` / `send`）。不需要新的状态或计数器。

`RespondPermission` **不**开新回合——它是回合内的一次闸门放行，模型继续的是同一轮输出。

### 4.3 那条硬隔离（本期最容易被顺手简化掉的东西）

**`reasoning` 帧只进 frames.jsonl。绝不进 `render.log`，绝不进回合文本，绝不喂 `ParseTrailer`，绝不进权限闸。**

现有的隔离判定（`claudecode/stream.go:174` 的 `thinking_delta` 过滤、opencode 的非 text part 隔离）
**一行都不许放松**。改的只是「丢弃」变成「分流」：原本 `return "", false` 的地方，
改成先调一次 `w.Reasoning(part, delta)` 再 `return "", false`。

实现时若发现某个 adapter 的隔离与帧写入难以共存，**停下来上报，不要放松隔离**。

### 4.4 event 帧由 agentd 写

agentd 在 `AppendEvent` 成功后同步调 `EventRef(seq, type)`。
与 adapter 同进程，写序可控，因此**单一 append 顺序即真实顺序**——
这正是不做「双流按时间戳归并」的理由：adapter 与 agentd 虽同进程但写入路径不同，
时间戳归并会真的乱序，而单流 append 永不会。

`progress` / `approver_decision` / `approver_disabled` 这三类**不唤醒 `wait`** 的事件同样写引用帧——
它们不打扰审核者，但属于「全过程」的一部分，应当出现在时间线上。

---

## 5. `render.log` 并存

`render.log` 原样保留，`AppendRender` 一行不改。text 帧与 render.log 双写。

理由是交付节奏：`render.log` 今天有两个消费者——`handoff attach` 的第二窗口
（在执行机上直接 `tail -f` **文件**，不走 HTTP）和 W2 的 `web/src/app/task/RenderPanel.tsx`
（走 `GET /api/tasks/{id}/render?tail=65536&follow=1` 的 text/plain 流）。
并存让 **W4a 能独立上线、零回归**，W4b 从容切换。

代价是纯文本写两遍。文本体量很小（本地样本最大 2.7KB），且 `render.log` 是帧流的严格子集、单向派生，
不构成「两份真相」的风险。

**退役 `render.log` 留到 W4b 落地后单独决定**，不在本期范围。

---

## 6. 体量控制

`tool_call.Input` 与 `tool_result.Output` 超过阈值即截断：**头 4KB + 尾 4KB，中间省略**，
帧里带 `Truncated: true` 与截断前的 `Bytes`。

**头尾都留是关键**：报错与 stack trace 通常在尾部，纯头部截断会刚好切掉最有用的那段。

省略处用 `executor.TruncationMarker`（`…（已截断）`）标记，与现有截断纪律一致。

单行帧另设硬上限（16KB），任何情况下不得超过——防止未来新增字段时无声地写出巨行把流式读取拖垮。

要看全文有更好的工具：`handoff diff` / `fetch` / `run`。原生 TUI 本来也是折叠显示的。

**不做旁路 blob 存储**：为「偶尔想看全文」加一套 blob 文件、一个新端点、一套生命周期管理，
是典型的 YAGNI。真有需求时再单独加，帧里已经有 `Bytes` 记录了原始长度，届时不用改契约。

---

## 7. 传输

### 7.1 端点

```
GET /api/tasks/{id}/frames?offset=<n>&tail=<n>&follow=1
```

形态**完全照抄 `render_stream.go`**：

- 参数优先级：显式 `offset` > `tail` > 默认回溯。**`offset` 与 `tail` 的单位都是字节**
  （不是行数、不是帧数），与 `render` 端点完全一致；默认回溯量取 `renderDefaultTail` 同量级的 4KB
- 响应 200 + `Content-Type: application/x-ndjson`
- 响应头 `X-Handoff-Frames-Size` 为响应开始时的文件大小（对齐 `X-Handoff-Render-Size`）
- `follow=1` 时到达文件尾不关闭，1s 轮询探测增长，20s 心跳保活
- 文件不存在时返回 200 空内容而非 404——任务刚 dispatch、模型还没吐第一帧是正常状态
- 客户端断开时 `r.Context()` 取消，函数返回，不留 goroutine

轮询而非 fsnotify 的理由与 `render_stream.go` 相同，不重复论证。

### 7.2 半行

字节 offset 可能落在一行中间。两侧都要处理：

- **服务端**：只在完整行边界切——从 offset 读到的最后一个不完整行**不发送**，
  下次轮询补齐后再发。这样客户端收到的永远是完整行
- **客户端**：仍缓冲一层不完整行兜底。服务端保证是契约，客户端缓冲是防御

`tail=<n>` 回溯时同理：从回溯点向后找到第一个 `\n`，从它之后开始。

### 7.3 跨机

W3a 的 `byTask` 把本端点包进去即可，无新增机制。
`forwardTo` 用 `io.Copy` 直通，天然支持流式；客户端断开时 `r.Context()` 取消，上游连接随之断开。

---

## 8. CLI

```
handoff frames <task> [--follow] [--offset N] [--tail N] [--target <机器>]
```

每行原样输出一帧 JSON，不做人类友好格式化——它是 W4e（`handoff tui`）的数据源与脚本消费面，
人要看好看的有 Web 与将来的 TUI。与 `handoff tasks` 的「一行一个 JSON」输出风格一致。

任务 id 仍是**完整 UUID 精确匹配**，无前缀补全。

---

## 9. 日志纪律

按 `instrumenting-code`：

- `NewFrameWriter`：Info，`task` + 恢复到的 `seq` / `turn`（帧流断档的第一诊断信号）
- `BeginTurn`：Info，`task` + `turn` + `reason`
- 帧写入失败：Warn，`task` + `type` + cause，**不中断回合**
- 截断发生：Debug，`task` + `type` + `bytes`（高频，不能 Info）
- frames 流开始/结束：Info，`task` + `offset` + `size` + `follow` + 已发字节（对齐 render 流）
- frames 流中断（非 `context.Canceled`）：Error + cause

**帧内容本身绝不进日志**：帧里有模型正文与工具输出，整条打进日志会把 agentd.log 撑爆，
也可能把仓库代码复制进日志。只记类型、长度、序号。

token、cookie、ticket 明文一律不得进日志（既有纪律，此处重申）。

---

## 10. 测试

### 10.1 `turn` 包

- 帧编码：每种 type 的 JSON 形状与 `omitempty` 行为
- 头尾截断：恰好在阈值、远超阈值、多字节字符边界（不能切出半个 UTF-8 字符）
- seq/turn 恢复：给一个已有 frames.jsonl，断言 `NewFrameWriter` 接着写而不是从 1 重来
- 并发写：多 goroutine 同时写，断言 seq 连续无重复、行不交错

### 10.2 每个 adapter 一组 golden 回放测试

**这是防上游漂移的唯一有效手段。** 喂一段真实抓下来的原生流（录制成 testdata），
断言产出的帧序列逐帧相等。四个 adapter 各一组。

同一组测试必须断言**思维链没有泄漏**：产出的 `text` 帧里不含任何 reasoning 内容，
`render.log` 里也不含。这条是 §4.3 硬隔离的自动化守卫。

### 10.3 agentd

- offset / tail / follow 三种取法
- 半行边界：构造一个 offset 落在行中间的请求，断言收到的第一行是完整的
- 文件不存在返回 200 空内容
- 客户端断开不留 goroutine

### 10.4 契约

`proto.Frame` 进 `internal/proto/contract_fixture_test.go`，
生成 `web/src/api/testdata/Frame.json`，W4b 据此写解析。

**按并行纪律，`internal/proto/` 由审核者独占**——W4a 的执行者发现契约不够用必须停下上报。

---

## 11. 完成判据

1. 一个真实任务跑完后，`frames.jsonl` 里能看到完整的「思考 → 调工具 → 工具结果 → 请求权限 → 继续 → 完成」时间线
2. `handoff frames <task> --follow` 能实时跟上正在跑的任务
3. `handoff frames <task> --target devbox` 能看远端任务的帧（走 W3a 转发）
4. 四个 executor 各跑一次，各自的帧序列符合其能力（没有思维链的就是没有 reasoning 帧，不伪造）
5. **`render.log` 与 `handoff attach` 行为逐字节不变**，W2 的 `RenderPanel` 零回归
6. `go build ./...` / `go test ./...` 全绿

---

## 12. 已知取舍

| 取舍 | 代价 | 为什么接受 |
|---|---|---|
| 文件而非 SQL | 不能服务端按类型查询 | 帧是只追加按序读的；查询需求可在前端已加载的帧上满足 |
| 增量而非快照 | 渲染端要按 part 拼接 | 快照写进只追加文件是平方级膨胀；增量才有流式观感 |
| 头尾截断 | 看不到超长输出全文 | 原生 TUI 也折叠；全文有 diff/fetch/run |
| 与 render.log 并存 | 文本双写 | 换来 W4a 独立上线、零回归 |
| `event` 帧存类型名冗余 | 一处小冗余 | 前端不查 events 也能画对卡片；类型名稳定 |
| 不做 blob 旁路 | 全文不可展开 | YAGNI；`Bytes` 已留下扩展余地，届时不用改契约 |
