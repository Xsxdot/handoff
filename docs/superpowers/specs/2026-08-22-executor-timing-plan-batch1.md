# 实现计划：执行耗时 · 第一批（T1 地基 + T2 claudecode + T3 codex）

日期 2026-08-22 · 子系统 **d_executor（边界型）** · 节点 `charter:plan`
前置：[spec §A](2026-08-22-executor-timing-and-custom-launchers-design.md) · [契约冻结物](2026-08-22-executor-timing-contract.md)（`8ec25bd9`） · [拆解与拍板](2026-08-22-executor-timing-breakdown.md)（`279e2028`）

**拍板依据**：P1=(b) 共用切分器（含 `EndTurn`）· P2=(b) 先做 claudecode+codex 验口径 · P3=(a) 每段刷新 turn 条目 · P4=(b) 命令取两段（本批不涉及，归 T6）。

**读者假设**：对本代码库零上下文。所有需要的代码都在下面，不必自行发挥。

---

## 0. 基线复核（本轮实测，动手前已跑）

| 命令 | 基线结果 |
|---|---|
| `go build ./...` | 退出码 0 |
| `go test ./internal/executor/turn/ ./internal/executor/claudecode/ ./internal/executor/codex/` | 三个包全 `ok` |
| `gofmt -l`（排除 web/node_modules） | 无输出 |

**库行为事实（带出处，非凭记忆）**：

| 事实 | 出处 | 对本计划的约束 |
|---|---|---|
| `claudecode` 的 `emit` **先记日志再取 `emitMu`**，`usage` 类型落进 `default` 分支打 Info「产出未知事件」 | `claudecode/adapter.go:951-967` | 本批会把 usage 事件量放大，**必须补 `case "usage"`**，否则日志刷屏且措辞是错的（见 Task 3 步骤 6） |
| `codex` 的 `emit` **立刻取 `emitMu`**，且用 `select/default` **非阻塞发送，满了 Warn 丢弃** | `codex/adapter.go:593-607` | 绝不可在 `emit` 内部再调 `emit`（自死锁）；且要验证事件量没把通道打满（见 Task 4 验收） |
| `claudecode.evCh` 缓冲 16（阻塞发送）；`codex.evCh` 缓冲 64（丢弃发送） | `claudecode/adapter.go:136`、`codex/adapter.go:211` | 同上 |
| `claudecode` 的回合收尾唯一入口是 `mapResult`（两条分支都置 `turnEnded=true`） | `claudecode/adapter.go:735,762,787` | `EndTurn` 挂在这里，**不挂 emit** |
| `codex` 的回合收尾唯一入口是 `finishTurn`（注释明写「每回合只调一次，各 case 互斥」） | `codex/adapter.go:762` 及 `:618` 注释 | 同上 |
| `HeadTail(s string, head, tail int) (string, bool, int64)` 的预算单位是**字节** | `turn/headtail.go:38` | 契约要求 `Detail` 按 **200 rune** 头尾截断 → 需新增 rune 粒度版本，见 Task 1 |
| `TruncateRunes(s, n)` 只截头、不加标记 | `turn/text.go:31` | 不满足「头尾」要求，不能直接用 |
| `FrameWriter.turn` 是私有字段，进程重启后从文件尾恢复 | `turn/frames.go:63,141` | 段切分器**必须复用它**，不得自建计数器（否则重启后键撞车） |

---

## 1. 任务 DAG

```
Task 1（turn 包两个小件）→ Task 2（Segmenter）→ ┬→ Task 3（claudecode）
                                                └→ Task 4（codex）
                                                        ↓
                                              Task 5（P1 退出闸复核 · 协调者执行）
```

Task 3 与 Task 4 互不依赖，可并行。

---

## Task 1 · `HeadTailRunes` 与 `FrameWriter.Turn()`

**为什么**：两件都是 Segmenter 的前置零件。`Detail` 的截断契约是「200 rune 头尾」，而既有 `HeadTail` 按字节、`TruncateRunes` 只截头——两个都不满足，硬用会在中文命令上切出半个字符。`Turn()` 是「段切分器不得自建回合计数器」这条纪律的唯一实现手段。

**Interfaces**
- Produces：`func HeadTailRunes(s string, head, tail int) string`、`func (w *FrameWriter) Turn() int`
- Consumes：无

### 步骤

**1.1** 在 `internal/executor/turn/headtail.go` 末尾追加：

```go
// HeadTailRunes 按 **rune** 预算做头尾截断，中间以省略标记相连。
//
// 参数：head / tail 为头尾各保留的 rune 数；两者之和 >= 总长时原样返回。
// 返回：截断后的串（未截断时即原串）。
//
// 为什么不复用 HeadTail：那个函数的预算单位是**字节**（headtail.go:38），
// 用在中文命令上会从一个多字节字符中间切开，落进 SQLite 的就是半个字符。
// 帧字段用字节预算是对的（它防的是行长失控），Detail 用 rune 预算也是对的
// （它防的是凭据面扩大），两个单位服务两个目的，不该互相迁就。
func HeadTailRunes(s string, head, tail int) string {
	r := []rune(s)
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if len(r) <= head+tail {
		return s
	}
	return string(r[:head]) + executor.TruncationMarker + string(r[len(r)-tail:])
}
```

> **import 已就位**：`headtail.go:14` 已经 import 了 `internal/executor`（`:47` 在用 `executor.TruncationMarker`），本步骤不需要动 import 块。
>
> 另有一个近亲 `TruncateMarked`（`text.go:18`，按 rune 截断并加标记）——**不能直接用**，它只截头不留尾。而 `headtail.go` 的文件头注释写明了留尾的理由：「报错信息与 stack trace 几乎总在输出尾部，纯头部截断会刚好切掉最有用的那一段」。命令原文同理，尾部往往是决定性的那几个 flag。

**1.2** 在 `internal/executor/turn/frames.go` 的 `NextPart` 之后追加：

```go
// Turn 返回当前回合号（还没开过回合时为 0）。
//
// 存在的唯一理由：段切分器（Segmenter）必须与帧共用同一个回合号。
// **不要在别处自建回合计数器**——FrameWriter 的 turn 在进程重启后从
// frames.jsonl 尾部恢复（见 resumeFrameState），自建的计数器会从 0 重来，
// 于是第二次运行的 "turn/1" 键会覆盖掉第一次运行的真数据，且账面无异常。
func (w *FrameWriter) Turn() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.turn
}
```

**1.3** 在 `internal/executor/turn/headtail_test.go` 追加失败测试并跑红：

```go
func TestHeadTailRunes(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		head, tail int
		want       string
	}{
		{"不足预算原样返回", "abcdef", 3, 3, "abcdef"},
		{"刚好等于预算原样返回", "abcdef", 3, 3, "abcdef"},
		{"英文截断", "abcdefghij", 3, 2, "abc" + executor.TruncationMarker + "ij"},
		// 中文是本函数存在的理由：按字节切会切出半个字符
		{"中文按 rune 切不出乱码", "一二三四五六七八九十", 2, 2, "一二" + executor.TruncationMarker + "九十"},
		{"tail 为 0", "abcdef", 2, 0, "ab" + executor.TruncationMarker},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HeadTailRunes(c.in, c.head, c.tail); got != c.want {
				t.Errorf("HeadTailRunes(%q,%d,%d) = %q，期望 %q", c.in, c.head, c.tail, got, c.want)
			}
		})
	}
}

func TestFrameWriterTurnAccessor(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	if w.Turn() != 0 {
		t.Errorf("还没开回合时应为 0，实得 %d", w.Turn())
	}
	_ = w.BeginTurn("dispatch", "")
	if w.Turn() != 1 {
		t.Errorf("第一回合应为 1，实得 %d", w.Turn())
	}
	_ = w.BeginTurn("send", "")
	if w.Turn() != 2 {
		t.Errorf("第二回合应为 2，实得 %d", w.Turn())
	}
	var nilW *FrameWriter
	if nilW.Turn() != 0 {
		t.Error("nil 接收者应返回 0（全包的 nil 安全约定）")
	}
}
```

**1.4** 跑红 → 实现（1.1/1.2 的代码）→ 跑绿：
```
go test ./internal/executor/turn/ -run 'TestHeadTailRunes|TestFrameWriterTurnAccessor' -v
```

**1.5** `gofmt -l internal/executor/turn/` 无输出 → 提交。

### 测试范围
只跑 `./internal/executor/turn/`。全量测试不属于本 task。

### 日志
本 task **不加日志**：两个都是纯函数/访问器，入口日志会在热路径上刷屏（`Turn()` 每次段事件都调）。这是「成功路径不静默」的正当例外——它们没有失败路径。

### 注释
两个函数的 doc 注释已含在代码块里（参数/返回/为什么）。

### 验收
- `go test ./internal/executor/turn/` 全绿
- 中文用例通过（这是 `HeadTailRunes` 存在的唯一理由，它绿了才算这个 task 有意义）

---

## Task 2 · `turn.Segmenter` 段切分状态机

**为什么**：让「一个回合怎么切成 api/tool/turn 三类条目」这条规则**只存在一处**。四份独立实现的后果不是重复代码，是四种互不可比的口径——而 spec 的整个目的就是让两次派发的数字能对照。

**Interfaces**
- Consumes：`turn.HeadTailRunes`（Task 1）、`proto.TimingEntry` / `proto.TimingKind*`（契约冻结物）
- Produces：
  ```go
  func NewSegmenter(now func() time.Time) *Segmenter
  func (s *Segmenter) BeginTurn(turn int) []proto.TimingEntry
  func (s *Segmenter) ToolStart(part, tool, detail string) []proto.TimingEntry
  func (s *Segmenter) ToolEnd(part string) (time.Duration, []proto.TimingEntry)
  func (s *Segmenter) EndTurn() []proto.TimingEntry
  const DetailRunes = 200
  ```

### 步骤

**2.1** 先写失败测试。新建 `internal/executor/turn/timing_test.go`：

```go
package turn

import (
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeClock 是注入给 Segmenter 的可控时钟。
//
// 时钟必须可注入，不是测试便利：判据依赖真实时间的测试在 CI 负载下会偶发
// 翻红，而偶发红会被当成噪音忽略，于是这条判据实际上失效了。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)}
}

// pick 从条目集合里挑出某一 Kind 的全部条目，保持顺序。
func pick(es []proto.TimingEntry, k proto.TimingKind) []proto.TimingEntry {
	var out []proto.TimingEntry
	for _, e := range es {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// runSeq 把一串操作喂给 Segmenter，收集全部产出的条目。
type seqStep func(s *Segmenter, c *fakeClock) []proto.TimingEntry

func runSeq(s *Segmenter, c *fakeClock, steps ...seqStep) []proto.TimingEntry {
	var all []proto.TimingEntry
	for _, st := range steps {
		all = append(all, st(s, c)...)
	}
	return all
}

func TestSegmenterSimpleTurn(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)

	all := runSeq(s, c,
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry { return s.BeginTurn(1) },
		// 模型想了 2s 才发出工具调用
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(2 * time.Second)
			return s.ToolStart("t1", "Bash", "go test ./...")
		},
		// 工具跑了 5s
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(5 * time.Second)
			_, es := s.ToolEnd("t1")
			return es
		},
		// 模型又想了 3s 收尾
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(3 * time.Second)
			return s.EndTurn()
		},
	)

	api := pick(all, proto.TimingKindAPI)
	if len(api) != 2 {
		t.Fatalf("应产出 2 个模型段，实得 %d：%+v", len(api), api)
	}
	if api[0].Key != "api/1/0" || api[0].DurMS != 2000 {
		t.Errorf("首个模型段应为 api/1/0 @2000ms，实得 %s @%d", api[0].Key, api[0].DurMS)
	}
	if api[1].Key != "api/1/1" || api[1].DurMS != 3000 {
		t.Errorf("次个模型段应为 api/1/1 @3000ms，实得 %s @%d", api[1].Key, api[1].DurMS)
	}

	tools := pick(all, proto.TimingKindTool)
	if len(tools) != 1 {
		t.Fatalf("应产出 1 个工具段，实得 %d", len(tools))
	}
	if tools[0].Key != "tool/1/t1" || tools[0].DurMS != 5000 || tools[0].OffsetMS != 2000 {
		t.Errorf("工具段应为 tool/1/t1 dur=5000 offset=2000，实得 %s dur=%d offset=%d",
			tools[0].Key, tools[0].DurMS, tools[0].OffsetMS)
	}
	if tools[0].Label != "Bash" || tools[0].Detail != "go test ./..." {
		t.Errorf("工具段应带 Label/Detail，实得 %q / %q", tools[0].Label, tools[0].Detail)
	}

	turns := pick(all, proto.TimingKindTurn)
	last := turns[len(turns)-1]
	if last.Key != "turn/1" || last.DurMS != 10000 {
		t.Errorf("末条回合条目应为 turn/1 @10000ms，实得 %s @%d", last.Key, last.DurMS)
	}
	// 三分法的不变式：api + tool 之和 == 回合墙钟（本例无空档、无并发）
	if api[0].DurMS+api[1].DurMS+tools[0].DurMS != last.DurMS {
		t.Error("无空档无并发时，模型段 + 工具段之和应恰好等于回合墙钟")
	}
}

func TestSegmenterConcurrentTools(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(1 * time.Second)

	// claude 在一条 assistant 消息里并行发两个 tool_use
	_ = s.ToolStart("a", "Bash", "sleep 4")
	_ = s.ToolStart("b", "Read", "x.go")
	c.add(2 * time.Second)
	_, esB := s.ToolEnd("b") // b 先回
	c.add(2 * time.Second)
	_, esA := s.ToolEnd("a") // a 后回
	end := s.EndTurn()

	tools := append(pick(esB, proto.TimingKindTool), pick(esA, proto.TimingKindTool)...)
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具段，实得 %d", len(tools))
	}
	var sum int64
	for _, e := range tools {
		sum += e.DurMS
		if e.OffsetMS != 1000 {
			t.Errorf("%s 的 offset 应为 1000（两者同时开始），实得 %d", e.Key, e.OffsetMS)
		}
	}
	// 这就是 OffsetMS 存在的理由：Σdur=6000，但墙钟跨度只有 4000。
	// 只存 dur 时聚合层会算出 other = 5000-0-6000 = -1000，被 max(0,·) 吞成 0。
	if sum != 6000 {
		t.Errorf("两个工具段之和应为 6000（2000+4000），实得 %d", sum)
	}
	last := pick(end, proto.TimingKindTurn)
	if len(last) == 0 || last[len(last)-1].DurMS != 5000 {
		t.Errorf("回合墙钟应为 5000ms，实得 %+v", last)
	}

	// 并发结束后只算**一个**批次：下一个模型段的键必须是 api/1/1 而不是 api/1/2
	api := pick(end, proto.TimingKindAPI)
	if len(api) != 1 || api[0].Key != "api/1/1" {
		t.Errorf("并发的一批工具只应让批次号 +1，实得 %+v", api)
	}
}

func TestSegmenterUnpairedToolProducesNothing(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(time.Second)
	_ = s.ToolStart("t1", "Bash", "sleep 100")
	// executor 半路死掉，tool_result 永不到达 → 直接收尾
	end := s.EndTurn()

	if got := pick(end, proto.TimingKindTool); len(got) != 0 {
		t.Errorf("没配上结果的工具**不产条目**（而不是产一条 dur=0），实得 %+v", got)
	}
	// 正面锁：确认它没有搬去别的 Kind 里冒充
	if got := pick(end, proto.TimingKindAPI); len(got) != 0 {
		t.Errorf("工具还开着时不该关模型段，实得 %+v", got)
	}
	// ToolEnd 对不认识的 part 必须回 -1（未知），不是 0（很快）
	if d, es := s.ToolEnd("t1"); d != -1 || es != nil {
		t.Errorf("回合已收尾后的迟到结果应回 (-1,nil)，实得 (%v,%+v)", d, es)
	}
}

func TestSegmenterKeysAreReplayStable(t *testing.T) {
	// 幂等键必须从内容派生：同一段序列跑两遍（模拟上游重放 / agentd 重启后
	// 重新喂同一批事件），产出的 Key 集合必须完全相同。用进程内计数器时这条会红。
	keysOf := func() []string {
		c := newTestClock()
		s := NewSegmenter(c.now)
		var all []proto.TimingEntry
		all = append(all, s.BeginTurn(7)...)
		c.add(time.Second)
		all = append(all, s.ToolStart("x", "Bash", "ls")...)
		c.add(time.Second)
		_, es := s.ToolEnd("x")
		all = append(all, es...)
		c.add(time.Second)
		all = append(all, s.ToolStart("y", "Bash", "pwd")...)
		c.add(time.Second)
		_, es = s.ToolEnd("y")
		all = append(all, es...)
		all = append(all, s.EndTurn()...)
		var ks []string
		for _, e := range all {
			ks = append(ks, e.Key)
		}
		return ks
	}
	a, b := keysOf(), keysOf()
	if len(a) != len(b) {
		t.Fatalf("两遍产出的条目数不同：%d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("第 %d 条键不稳定：%q vs %q", i, a[i], b[i])
		}
	}
	// 回合号来自入参而非内部计数器：第 7 回合的键必须带 7
	if a[0] != "turn/7" {
		t.Errorf("回合号应取自入参，实得 %q", a[0])
	}
}

func TestSegmenterDetailTruncatedAtCollector(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	long := ""
	for i := 0; i < DetailRunes*2; i++ {
		long += "中"
	}
	_ = s.ToolStart("t", "Bash", long)
	_, es := s.ToolEnd("t")
	tools := pick(es, proto.TimingKindTool)
	if len(tools) != 1 {
		t.Fatal("应有一个工具段")
	}
	// 截断在采集侧做（契约文档 §3.4：store 明确不做截断，两处都以为对方管了
	// 是这类字段最常见的失守方式）
	if r := []rune(tools[0].Detail); len(r) > DetailRunes+len([]rune("…")) {
		t.Errorf("Detail 应在采集侧截到 %d rune 上下，实得 %d", DetailRunes, len(r))
	}
	if tools[0].Detail == long {
		t.Error("Detail 未被截断——凭据边界失守")
	}
}

func TestSegmenterEndTurnIsIdempotent(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(time.Second)
	if got := s.EndTurn(); len(got) == 0 {
		t.Fatal("首次 EndTurn 应产出条目")
	}
	if got := s.EndTurn(); got != nil {
		t.Errorf("重复 EndTurn 应返回 nil（emit 通道上可能重复触发），实得 %+v", got)
	}
}

func TestSegmenterNilSafe(t *testing.T) {
	// 与 FrameWriter 同款约定：构造失败时 adapter 直接持 nil，调用点不判空
	var s *Segmenter
	if got := s.BeginTurn(1); got != nil {
		t.Error("nil.BeginTurn 应为空操作")
	}
	if got := s.ToolStart("a", "b", "c"); got != nil {
		t.Error("nil.ToolStart 应为空操作")
	}
	if d, es := s.ToolEnd("a"); d != -1 || es != nil {
		t.Error("nil.ToolEnd 应返回 (-1,nil)")
	}
	if got := s.EndTurn(); got != nil {
		t.Error("nil.EndTurn 应为空操作")
	}
}
```

**2.2** 跑红：`go test ./internal/executor/turn/ -run TestSegmenter`（应报 `undefined: NewSegmenter`）。

**2.3** 新建 `internal/executor/turn/timing.go`，完整实现：

```go
// timing.go —— 回合内的耗时分段（模型段 / 工具段 / 回合墙钟）。
//
// 职责：
//   - 接收四类信号（回合开始 / 工具开始 / 工具结束 / 回合结束），把一个回合的
//     墙钟切成交替的模型段与工具段，产出 proto.TimingEntry
//   - 幂等键一律从内容派生（回合号 + part / 批次号），不用进程内计数器
//
// 边界：
//   - 不写文件、不发事件：产出的条目交回调用方（adapter）经 AdapterEvent 上报
//   - 不认识任何具体 executor：四家喂进来的是同一组信号，口径因此可比
//   - **不产 other（未归类）条目**：它只在聚合层由差额算出（契约文档 §2.1）
//
// 为什么这四类信号足够：模型段的边界恰好是「回合开始 / 上一批工具全部结束」到
// 「第一个工具开始 / 回合结束」，全部可由这四个信号推出，不需要 adapter 额外
// 告诉我们「模型现在开始想了」——那个时刻在四家协议里根本没有统一的表达。
package turn

import (
	"fmt"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// DetailRunes 是 TimingEntry.Detail 的 rune 上限（契约文档 §3.4 的凭据边界）。
//
// 截断在**采集侧**做：store 侧明确不截（UpsertTiming 的注释写死了这一点）。
// 两处都以为对方管了，是这类字段最常见的失守方式。
const DetailRunes = 200

// openTool 是一次还没收到结果的工具调用。
type openTool struct {
	tool   string
	detail string
	start  time.Time
}

// Segmenter 把一个回合切成模型段与工具段。
//
// 并发安全：全部方法在同一把锁内完成读改写。adapter 的流处理与 Send 可能并发
// 触碰它（BeginTurn 来自 Send，工具信号来自流），与 FrameWriter 同款理由。
//
// nil 安全：全部方法对 nil 接收者是空操作（ToolEnd 返回 -1）。构造失败时
// adapter 直接持 nil，调用点不必到处判空——与 FrameWriter 同款约定。
type Segmenter struct {
	now func() time.Time

	mu   sync.Mutex
	turn int // 当前回合号；0 = 没有开着的回合
	// turnStart 是本回合起点，OffsetMS 与回合墙钟都以它为基准
	turnStart time.Time
	// batches 是本回合内**已完成**的工具批次数，模型段的键靠它派生
	batches int
	// apiStart 是当前模型段的起点；零值 = 当前没有开着的模型段（工具正在跑）
	apiStart time.Time
	open     map[string]*openTool
	live     int // 当前开着的工具数；由 1→0 才算一个批次结束
}

// NewSegmenter 创建段切分器。
//
// 参数：now 是时钟，传 nil 用 time.Now。
//
// 注意：**时钟必须能注入**。判据依赖真实时间的测试在 CI 负载下会偶发翻红，
// 而偶发红会被当噪音忽略，于是这条判据实际上失效了。
func NewSegmenter(now func() time.Time) *Segmenter {
	if now == nil {
		now = time.Now
	}
	return &Segmenter{now: now, open: map[string]*openTool{}}
}

// BeginTurn 开启回合号为 turn 的新回合，并收尾上一个还开着的回合。
//
// 参数：turn 必须取自 FrameWriter.Turn()——**不要自建计数器**，理由见该方法注释。
// 返回：要上报的条目（上一回合的收尾条目 + 本回合的初始 turn 条目）。
func (s *Segmenter) BeginTurn(turn int) []proto.TimingEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.closeTurnLocked()
	now := s.now()
	s.turn, s.turnStart, s.batches = turn, now, 0
	s.apiStart = now
	s.open = map[string]*openTool{}
	s.live = 0
	return append(out, s.turnEntryLocked(now))
}

// ToolStart 记一次工具调用的开始。
//
// 参数：part 是回合内唯一的调用标识（与 tool_call 帧的 Part 同值）；
// tool 是工具名（进 Label）；detail 是命令/入参摘要（进 Detail，本方法负责截断）。
// 返回：要上报的条目。本批工具的第一个 ToolStart 会顺带收掉当前模型段。
//
// 注意：同 part 重复 start 不重复计数——上游流重放时这条是唯一的防线。
func (s *Segmenter) ToolStart(part, tool, detail string) []proto.TimingEntry {
	if s == nil || part == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == 0 {
		return nil // 回合外的信号一律丢弃，不猜回合号
	}
	now := s.now()
	var out []proto.TimingEntry
	if s.live == 0 {
		if e, ok := s.closeAPILocked(now); ok {
			out = append(out, e)
		}
	}
	if _, dup := s.open[part]; !dup {
		s.open[part] = &openTool{
			tool: tool, detail: HeadTailRunes(detail, DetailRunes/2, DetailRunes/2), start: now,
		}
		s.live++
	}
	return append(out, s.turnEntryLocked(now))
}

// ToolEnd 记一次工具调用的结束。
//
// 返回：
//   - dur: 本次调用耗时，直接交给 FrameWriter.ToolResult。**没配上 start 时
//     返回 -1（不知道），不是 0**——0ms 是一次真实可能的极快调用
//   - entries: 要上报的条目；没配上时为 nil（**不产 dur=0 的假条目**）
func (s *Segmenter) ToolEnd(part string) (time.Duration, []proto.TimingEntry) {
	if s == nil {
		return -1, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ot, ok := s.open[part]
	if !ok {
		return -1, nil
	}
	delete(s.open, part)
	s.live--
	now := s.now()
	dur := now.Sub(ot.start)
	out := []proto.TimingEntry{{
		Key:      fmt.Sprintf("tool/%d/%s", s.turn, part),
		Kind:     proto.TimingKindTool,
		Turn:     s.turn,
		DurMS:    dur.Milliseconds(),
		Label:    ot.tool,
		Detail:   ot.detail,
		OffsetMS: ot.start.Sub(s.turnStart).Milliseconds(),
	}}
	// 只有**整批**结束才算一个批次：并发的多个工具共享一个批次号，
	// 否则模型段的键会跳号，而跳号本身不报错、只是账对不上
	if s.live == 0 {
		s.batches++
		s.apiStart = now
	}
	return dur, append(out, s.turnEntryLocked(now))
}

// EndTurn 收尾当前回合：关掉还开着的模型段，刷最后一条 turn 条目。
//
// 幂等：回合已收尾时返回 nil。调用点（adapter 的回合收尾入口）可能被重复触发。
//
// 注意：还开着的工具**不产条目**——没有结束时刻就没有耗时。它造成的缺口由
// 聚合层的 Partial 标出来，这正是 Partial 存在的理由。
func (s *Segmenter) EndTurn() []proto.TimingEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeTurnLocked()
}

// closeTurnLocked 收尾当前回合。调用方必须持锁。
func (s *Segmenter) closeTurnLocked() []proto.TimingEntry {
	if s.turn == 0 {
		return nil
	}
	now := s.now()
	var out []proto.TimingEntry
	if e, ok := s.closeAPILocked(now); ok {
		out = append(out, e)
	}
	out = append(out, s.turnEntryLocked(now))
	s.turn = 0
	return out
}

// closeAPILocked 关掉当前模型段。没有开着的模型段时返回 (零值,false)。
// 调用方必须持锁。
func (s *Segmenter) closeAPILocked(now time.Time) (proto.TimingEntry, bool) {
	if s.apiStart.IsZero() {
		return proto.TimingEntry{}, false
	}
	e := proto.TimingEntry{
		Key:   fmt.Sprintf("api/%d/%d", s.turn, s.batches),
		Kind:  proto.TimingKindAPI,
		Turn:  s.turn,
		DurMS: now.Sub(s.apiStart).Milliseconds(),
	}
	s.apiStart = time.Time{}
	return e, true
}

// turnEntryLocked 造一条当前回合的墙钟条目。调用方必须持锁。
//
// 每次段事件都刷一条（拍板 P3=(a)）：它按同键覆盖，重复上报无害，而代价是
// 「任务跑到一半时也读得到真实总时长」——审核者最想看耗时的时刻，恰恰是
// 「它怎么还没跑完」的那一刻。
func (s *Segmenter) turnEntryLocked(now time.Time) proto.TimingEntry {
	return proto.TimingEntry{
		Key:   fmt.Sprintf("turn/%d", s.turn),
		Kind:  proto.TimingKindTurn,
		Turn:  s.turn,
		DurMS: now.Sub(s.turnStart).Milliseconds(),
	}
}
```

**2.4** 跑绿：`go test ./internal/executor/turn/ -run TestSegmenter -v`

**2.5** 跑整包 + 格式化：
```
go test ./internal/executor/turn/ && gofmt -l internal/executor/turn/
```

**2.6** 提交。

### 测试范围
只跑 `./internal/executor/turn/`。

### 日志
本 task **不加日志**，理由与 Task 1 同：段切分器在热路径上（每次工具调用触发 2~3 次），入口日志必然刷屏；它也没有失败路径（所有异常输入都有明确的静默返回语义，且那些语义被测试锁住了）。**失败的可见性由调用方承担**——adapter 上报条目失败时打 Warn（见 Task 3 步骤 5）。

### 注释
文件头（职责/边界/为什么这四类信号够）+ 每个导出方法（参数/返回/注意事项）+ 三处「为什么」（并发批次号、nil 安全约定、每段刷新的代价）已含在代码块里。

### 验收
- 七个测试全绿，其中三条是本 task 的核心判据：
  - `TestSegmenterConcurrentTools`：Σdur=6000 而墙钟=5000 —— 这是 `OffsetMS` 存在理由的可执行证明
  - `TestSegmenterUnpairedToolProducesNothing`：没配上的工具**不产 dur=0 的假条目**
  - `TestSegmenterKeysAreReplayStable`：同序列跑两遍键完全相同 —— 用计数器实现时这条必红
- 缺陷族结论并入：见 §4.1（生命周期）、§4.4（假绿）

---

## Task 3 · claudecode 接线

**为什么**：claudecode 的工具帧已在 `adapter.go:685`（call）与 `:726`（result）产出，本 task 把这两点接上 Segmenter，并把 Ticket 0 留下的 `-1` 换成真耗时。

**打点必须贴着协议事件**：`out.jsonl` 的轮询间隔是 200ms（`stream.go:33`），若按写帧时刻倒推，一次毫秒级的 `Read` 会被算成几百毫秒。

**Interfaces**
- Consumes：`turn.NewSegmenter` / `Segmenter.{BeginTurn,ToolStart,ToolEnd,EndTurn}`（Task 2）、`FrameWriter.Turn()`（Task 1）
- Produces：无对外新签名（全部是包内接线）

### 步骤

**3.1** `internal/executor/claudecode/adapter.go`，在 `runState` 结构体里 `frames` 字段之后加：

```go
	seg          *turn.Segmenter   // 耗时段切分器；与 frames 同款 nil 安全约定
```

**3.2** 在 `newRun` 里 `r.frames = fw` 之后加：

```go
	// 段切分器与帧写入器共用回合号（见 FrameWriter.Turn 注释）。
	// 时钟传 nil 即 time.Now；本包不注入时钟——口径的穷举验证在 turn 包完成，
	// 这里只验「信号有没有喂对」，不验时长算得对不对。
	r.seg = turn.NewSegmenter(nil)
```

**3.3** 两处 `BeginTurn` 之后接线。`adapter.go:200` 附近：

```go
	if err := r.frames.BeginTurn("dispatch", ""); err != nil {
		// ……既有的错误处理原样不动……
	}
	a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))
```

`adapter.go:353` 附近（`BeginTurn("send", text)`）同样加一行。

**3.4** `appendActionSummary`（`adapter.go:668`）里，**在 `r.frames.ToolCall(...)` 之前**插入：

```go
	// 打点必须在写帧**之前**：写帧要过一次头尾截断与文件 IO，把那段时间算进
	// 工具耗时是在给工具记别人的账。
	a.reportTiming(r, r.seg.ToolStart(toolUseID, toolName, timingDetail(toolName, input)))
```

并在文件末尾加辅助函数：

```go
// timingDetail 从工具入参里取出进 TimingEntry.Detail 的摘要。
//
// Bash 取完整命令原文（聚合层要按命令首词分桶，摘要里没有命令就分不了桶）；
// 其余工具取紧凑 JSON。截断由 Segmenter 负责，本函数不截。
//
// 为什么不复用 appendActionSummary 里那行 render.log 摘要：那行走的是
// firstLine，会切掉多行命令的后续行——而多行命令的首行往往只是 `set -e`。
func timingDetail(toolName string, input json.RawMessage) string {
	if toolName == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			return in.Command
		}
	}
	return compactJSON(input)
}
```

**3.5** `mapUserMessage`（`adapter.go:726` 所在函数）里，把：

```go
		if err := r.frames.ToolResult(block.ToolUseID, status, full, -1); err != nil {
```

替换为：

```go
		// 先取耗时再写帧：dur 来自 Segmenter 里记的 tool_call 时刻，
		// 没配上时是 -1（不知道），帧上就不带 dur_ms
		dur, entries := r.seg.ToolEnd(block.ToolUseID)
		if err := r.frames.ToolResult(block.ToolUseID, status, full, dur); err != nil {
```

并在该 `if err != nil { ... }` 块之后加：

```go
		a.reportTiming(r, entries)
```

（同时删掉那行 `// TODO(contract Ticket 0): ...` 注释。）

**3.6** `mapResult`（`adapter.go:735`）的**第一行**插入回合收尾：

```go
func (a *Adapter) mapResult(r *runState, m streamMsg) {
	// 回合收尾在最前面：本函数有两条出口（异常分支提前 return），
	// 放在开头是唯一能同时覆盖两条的位置。EndTurn 幂等，重复触发无害。
	a.reportTiming(r, r.seg.EndTurn())
	// ……既有代码原样不动……
```

**3.7** 新增上报辅助函数（放在 `emit` 附近）：

```go
// reportTiming 把段切分器产出的条目逐条经 usage 事件上报。
//
// 为什么走 usage 而不是新事件类型：Usage（当前占用）、Spend（累计消耗）与
// Timing（耗时）是同一次模型调用结束时的三样产物；拆成两个事件，两者之间
// 就能插进一次 agentd 重启（契约文档 §6.3 的拍板记录）。
//
// entries 为空是常态（不是错误），静默返回。
func (a *Adapter) reportTiming(r *runState, entries []proto.TimingEntry) {
	for i := range entries {
		e := entries[i]
		if !a.emit(r, executor.AdapterEvent{Type: "usage", Timing: &e}) {
			// 通道已关或已 stop：剩下的条目也送不出去，不必逐条重试刷日志
			a.log.Debug("耗时条目未能上报（事件通道已终止）",
				"task", r.taskID, "key", e.Key, "remaining", len(entries)-i)
			return
		}
	}
}
```

**3.8** **补 `emit` 的 `usage` 分支**（`adapter.go:951` 的 switch）。当前 `usage` 落进 `default` 打 Info「产出未知事件」——本批把 usage 事件量放大数十倍，这个分支会刷屏，而且措辞本来就是错的（usage 不是未知事件）。在 `case "result":` 之后加：

```go
	case "usage":
		// 不打 Info：用量/耗时事件频率高（一个回合几十到几百条），逐条打入口
		// 日志就是刷屏。落库结果的日志在 manager 的 handleUsage/handleSpend/
		// handleTiming 里打（那里是 Debug，且只在真落库时打）。
```

**3.9** 加回归测试。在 `internal/executor/claudecode/adapter_test.go`（或新建 `timing_test.go`）里：

```go
// TestClaudeToolTimingPaired 钉住「工具调用的两端都喂给了段切分器」。
//
// 它不验时长算得对不对（那是 turn 包的事），只验**信号有没有喂对**：
// 一次配对的 tool_use/tool_result 必须产出 tool 条目，且帧上带 dur_ms。
func TestClaudeToolTimingPaired(t *testing.T) {
	// 用既有的 turn_success.jsonl 夹具驱动一个回合，收集 AdapterEvent，
	// 断言：
	//   1. 至少一条 ev.Timing != nil 且 Kind == proto.TimingKindTool
	//   2. 该条目的 Label == "Bash"，Detail 含 "go test"
	//   3. frames.jsonl 里那条 tool_result 帧的 dur_ms > 0
	//   4. 反面：tool_call 帧的 dur_ms == 0（耗时只落在结果侧）
	//   5. 至少一条 Kind == proto.TimingKindTurn（回合墙钟有刷出来）
}
```

> **实现提示**：本包既有测试（`adapter_test.go`）已有驱动夹具跑完一个回合并收集事件的形态，照它写，不要另起一套 harness。

**3.10** 跑测试 + 格式化 + 提交：
```
go test ./internal/executor/claudecode/ && gofmt -l internal/executor/claudecode/
```

### 测试范围
只跑 `./internal/executor/claudecode/`（如果改动触及 `turn` 包则一并跑 `./internal/executor/turn/`，本 task 不应触及）。

### 日志
- `reportTiming` 的失败路径带 Warn/Debug + 上下文（task、key、剩余条数）——已含在 3.7
- `emit` 的 `usage` 分支**刻意不打 Info**，理由写在注释里（3.8）——这是「成功路径不静默」的正当例外：落库结果由 manager 侧的 Debug 承担

### 注释
`timingDetail`、`reportTiming` 的 doc 注释（参数/返回/为什么）已含；三处「为什么」（打点在写帧前、EndTurn 放函数首行、usage 不打 Info）已写进代码块。

### 验收
- `go test ./internal/executor/claudecode/` 全绿
- 3.9 的五条断言全过，其中第 4 条是反面断言，**必须与第 3 条成对存在**（`tool_result` 有 / `tool_call` 无）——单独的反面断言在字段搬家后照样绿
- 真机项见 §5

---

## Task 4 · codex 接线

**为什么**：与 Task 3 同，落点在 `codex/adapter.go:1128`（call）与 `:1137`（result）。codex 走 appserver 通知，事件到达路径与 claudecode 的文件轮询完全不同——**这正是「四家口径可比」的压力测试点**。

**Interfaces**：同 Task 3。

### 步骤

**4.1** `runState` 加 `seg *turn.Segmenter`；构造处（`adapter.go:211` 附近的 `newRun`）加 `r.seg = turn.NewSegmenter(nil)`。

**4.2** 两处 `BeginTurn`（`adapter.go:316`、`:512`）之后加 `a.reportTiming(r, r.seg.BeginTurn(r.frames.Turn()))`。

**4.3** `appendItemFrame`（`adapter.go:1119`）的 `ntfItemStarted` 分支里，**在 `r.frames.ToolCall(...)` 之前**加：

```go
		a.reportTiming(r, r.seg.ToolStart(it.ID, it.Type, input))
```

（`input` 就是该分支已经算好的那个变量：`commandExecution` 取 `it.Command`，`fileChange` 取 `it.renderLine()`。**不另造 detail 提取函数**——codex 的入参形态已经是纯文本。）

**4.4** 同函数的 completed 分支，把：

```go
	// TODO(contract Ticket 0): 耗时打点归 implement 节点；-1 = 不知道，帧上不带 dur_ms。
	if err := r.frames.ToolResult(it.ID, status, it.renderLine(), -1); err != nil {
```

替换为：

```go
	dur, entries := r.seg.ToolEnd(it.ID)
	if err := r.frames.ToolResult(it.ID, status, it.renderLine(), dur); err != nil {
```

并在该 `if` 块后加 `a.reportTiming(r, entries)`。

**4.5** `finishTurn`（`adapter.go:762`）的**第一行**（`a.flushRender(r)` 之前）插入：

```go
	// 回合收尾在最前面：本函数按 status 分支且 failed 分支提前 return，
	// 放开头是唯一能覆盖全部分支的位置。EndTurn 幂等。
	a.reportTiming(r, r.seg.EndTurn())
```

**4.6** 加 `reportTiming`。**注意与 claudecode 的一处关键差异**：codex 的 `emit` **第一行就取 `emitMu`**（`adapter.go:594`），所以 `reportTiming` **绝不能从 `emit` 内部调用**——本计划把它挂在 `finishTurn` / `appendItemFrame` 上，都在 `emit` 外部，安全。函数体同 Task 3 的 3.7（把 `a.log` 的措辞换成 codex 的口径）。

**4.7** codex 的 `emit` 不按事件类型打 Info（它只在通道异常时打日志），**无需 3.8 那样的分支补丁**。

**4.8** 加回归测试，断言项同 3.9，夹具取 codex 的 item 通知序列（照 `items_test.go` / `frames_test.go` 的既有形态）。**外加一条与 claudecode 的口径对照**：

```go
// TestCodexTimingShapeMatchesClaude 钉住「同形状的回合，两家产出的条目种类与
// 数量相同」。
//
// 它不比时长（那当然不同），只比结构：一次「模型输出 → 一个工具 → 模型输出」
// 的回合，两家都应产出 2 个 api 条目 + 1 个 tool 条目 + ≥1 个 turn 条目。
// 这条一旦红，说明共用切分器在两家上算出的段结构不同构 —— 此时**退回 P1 的
// 选项 (a)**，不要在 codex 侧打补丁把它掰成一样。
```

**4.9** **验证事件通道没被打满**（codex 特有，见 §0 的库行为事实）：跑本包测试后，确认日志里没有 `事件通道满，丢弃事件`。若出现，说明每回合的条目量超出 64 缓冲的承受，处置是**调大缓冲**而不是减少打点——减少打点等于把账记漏。

**4.10** 跑测试 + 格式化 + 提交：
```
go test ./internal/executor/codex/ && gofmt -l internal/executor/codex/
```

### 测试范围
只跑 `./internal/executor/codex/`。

### 日志 / 注释
同 Task 3（`reportTiming` 的 doc 与失败路径日志、三处「为什么」）。

### 验收
- `go test ./internal/executor/codex/` 全绿
- 4.8 的口径对照测试绿
- 4.9 的通道日志检查通过
- 真机项见 §5

---

## Task 5 · P1 退出闸复核 —— **本 task 由协调者执行，不派发**

**为什么**：拍板记录的执法条款：T2、T3 完成后立刻复核「共用切分器里有没有长出 per-provider 分支」。长出了就当场退回 P1 的选项 (a)（各 adapter 自打），**不许拖到 T4/T5**——拖到那时四家都已按 (b) 写完，退回的成本从「改一处」变成「返工四处」。

**判据（行为化，可机械执行）**：

```
grep -nE "claude|codex|grok|opencode" internal/executor/turn/timing.go
```

**期望：无输出。** 有输出即闸门触发，当场停下来重新拍 P1。

第二条判据（人工，但有明确形状）：`Segmenter` 的导出方法数与 Task 2 定义的一致（`BeginTurn` / `ToolStart` / `ToolEnd` / `EndTurn` 四个）。多出任何一个「给某家用的」方法，同样算闸门触发。

**为什么这个 task 不能派发**：它是一次**裁决**（要不要退回 P1），不是一次实现。执行者的纪律块禁止它自行改变计划方向，把裁决派出去等于没裁。

---

## 2. spec 覆盖对账（自审三查之一）

| spec §A 条款 | 本批覆盖 | 落点 |
|---|---|---|
| A.3 三分法：模型段 | ✅ | Task 2 `closeAPILocked` |
| A.3 三分法：工具段 | ✅ | Task 2 `ToolEnd` |
| A.3 三分法：未归类 | — | **本批不做**，归 T6（聚合层算差额，adapter 永不上报） |
| A.3 并发工具如实并列 | ✅ | Task 2 的 `OffsetMS` + `TestSegmenterConcurrentTools` |
| A.4 故事 1（工具卡耗时） | 部分 | 本批产出 `Frame.dur_ms`；渲染归 T7 |
| A.4 故事 2/3（任务级面板） | — | 归 T6 + T7 |
| A.4 故事 4（CLI） | — | 零改动，归真机验证 |
| A.4 故事 5（grok 也有） | — | 归 T4/T5（第二批） |
| A.4 故事 6（历史任务显示「—」） | — | 归 T6 |
| A.6 打点贴协议事件、不按写帧时刻倒推 | ✅ | Task 3 步骤 3.4、Task 4 步骤 4.3（都在写帧**之前**） |
| A.6 时钟可注入 | ✅ | Task 2 `NewSegmenter(now)` |

**本批未覆盖的 spec 条款全部有明确归属，无遗漏。**

## 3. 占位符扫描（自审三查之二）

全文无 TBD、无「加适当的错误处理」、无「同 Task N」。

**唯一的例外是 Task 3.9 / 4.8 的测试体**——它们给的是断言清单与实现提示，不是完整代码。**这是刻意的**：这两个测试必须复用本包既有的夹具驱动 harness（`claudecode/adapter_test.go` 与 `codex/items_test.go` 各有一套，形态不同），凭空写一份新 harness 才是错的。断言项已逐条列全，无发挥空间。

## 4. 四项检查

### 4.1 缺陷族对抗审查

**生命周期 / 状态机中断**：打点中途 agentd 重启 → 留下开着的工具，`ToolEnd` 永不到达 → **不产条目**（Task 2 已测），该回合 `Σtool` 偏小、聚合层 `Partial=true`。回合号从 `FrameWriter.Turn()` 取，重启后从文件恢复，**键不会撞车**（Task 1 的注释与 Task 2 的重放测试共同守住）。孤儿资源：无——Segmenter 是内存态，随 runState 走。
→ 并入 Task 2 验收。**未验证项**：重启后 `frames.jsonl` 恢复出的 turn 号是否真的延续 → §5 第 3 条。

**静默失败 / 误导报错**：三条错误路径——(i) `reportTiming` 送不出去 → Debug + 提前返回，不逐条刷屏；(ii) `ToolEnd` 没配上 → 返回 `-1` 而非 `0`，帧上不带 `dur_ms`（可区分「没报」与「很快」）；(iii) 写帧失败 → 沿用既有 Warn，不影响回合。**不存在「报成功但没做」的窗口**：条目要么产出要么不产出，没有中间态。
→ 并入 Task 2/3 验收。

**跨平台假设**：**无，因为**本批只用 `time.Now`/`time.Duration`、`sync.Mutex`、字符串处理与 `fmt.Sprintf`，全部无平台差异。不碰路径、进程组、权限模型、webview。

**假红 / 假绿测试**：三处高风险，各有处置——
1. 计时竞态 → 时钟注入（Task 2），adapter 侧的测试**刻意不验时长**（3.9 注释已写明），只验信号喂没喂对；
2. 反面断言（`tool_call` 无 `dur_ms`、没配上的工具不产条目）→ **每条都配了正面断言**（`tool_result` 有 `dur_ms=1500`、配上的工具产条目），成对存在；
3. **夹具编码不存在的世界** → 这是本批最大的假绿风险：`turn_success.jsonl` 与 codex 的 item 序列都是**手写**的。处置：§5 第 1 条真机项，把真实事件序列与夹具比对。

**门禁绕过**：**无，因为**本批没有新增任何用户可触发的写路径或执行路径。条目由 adapter 单向产出，全程无用户输入参与。

**webview / 平台差异（项目第六族）**：**无，因为**本批零前端改动。

### 4.2 序列化边界设问

新字段从产生到消费的每一处投影，本批涉及的段：

| 环节 | 手写投影？ | 断言 |
|---|---|---|
| `Segmenter` → `proto.TimingEntry` | 否（结构体直造） | Task 2 逐字段断言 |
| `TimingEntry` → `AdapterEvent.Timing` | 否（取址） | Task 3.9 / 4.8 断言 `ev.Timing != nil` |
| `dur` → `Frame.DurMS` → `frames.jsonl` | **是**（`durMS()` 折算 + JSON tag） | **穿过真实序列化边界的回归测试已存在**：契约冻结时加的 `frames_test.go` 断言是从磁盘读回 `frames.jsonl` 再比对的（`readFrames`），不是比内存对象。Task 3.9 的第 3/4 条断言沿用同一条路 |
| 「字段缺失」vs「值为零」 | — | `dur < 0` → 不带 `dur_ms`；契约已接受「旧帧的 0 与真实 0ms 不可区分」这个歧义并写明理由（0ms 调用在实测中不存在） |

**本批不触及** manager 的手搭 map 风险区（那 15 处在 `internal/agentd`，本批零改动）。该风险归 T6/T7。

### 4.3 上下文预算检查

| Task | 有界文件集 | 圈得出？ |
|---|---|---|
| 1 | `internal/executor/turn/{headtail,frames}.go` + 对应测试 | ✅ |
| 2 | `internal/executor/turn/timing{,_test}.go`（新建） | ✅ |
| 3 | `internal/executor/claudecode/adapter.go` + 一个测试文件 | ✅ |
| 4 | `internal/executor/codex/adapter.go` + 一个测试文件 | ✅ |
| 5 | 一条 grep | ✅ |

架构法第三条三条判据在[拆解稿 §1.1](2026-08-22-executor-timing-breakdown.md) 已逐条回答，**均不命中，无需竖切卡**。

### 4.4 类型标注

**d_executor 是边界型子系统**，接缝对面是真实 executor 进程与其协议。因此本批的机内测试只验**契约形状与信号接线**，行为验收写成显式真机清单（§5）。Task 2 是唯一的例外——它是纯状态机，对面是自有代码，机内可闭环。

---

## 5. 真机清单（归协调者执行，不派发）

1. **夹具与真实序列同构**（承 4.1 假绿第 3 条）——各跑一个 claudecode 与 codex 的真实回合（含至少一次 Bash 工具调用），抓 `out.jsonl` / appserver 通知的真实序列，与本批测试夹具比对形状。**不同构即夹具在验证一个编出来的世界。**
2. **两家的段结构真同构**（承 P1 与 Task 4.8）——同一份简单 plan 分别派给 claudecode 与 codex，比对产出的条目**种类与数量**。数量不同即 P1 退出闸触发。
3. **agentd 重启后回合号延续**（承 4.1 生命周期）——跑到一半重启 agentd，`grep '"type":"turn_start"' frames.jsonl | tail -3` 确认 turn 号不从 1 重来。
4. **codex 事件通道未被打满**（承 §0 的库行为事实 + Task 4.9）——真实回合跑完后 `grep '事件通道满' agentd 日志`，**期望无输出**。

---

## 6. 派发前自审

- **Task 5 已标注「本 task 由协调者执行，不派发」**——它是一次裁决不是一次实现。
- **§5 真机清单全部归协调者**——其中第 2、3 条需要驱动 handoff 自身（派发任务、重启 agentd），与执行者纪律的「不派发、不调 handoff CLI、不起 executor 进程」直接冲突，**派出去等于没验**。
- Task 1~4 均不涉及 handoff 自身的驱动，可派发。
