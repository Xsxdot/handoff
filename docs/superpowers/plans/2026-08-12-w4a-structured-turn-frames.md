# W4a 结构化回合流 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把四个 executor adapter 已经解析出来、但没有结构化去处的回合内容（思维链、工具调用、工具结果）落成 `frames.jsonl`，经 `GET /api/tasks/{id}/frames` 与 `handoff frames` 送出。

**Architecture:** `internal/executor/turn` 新增 executor 无关的 `FrameWriter`（`AppendRender` 的姊妹件），四个 adapter 在**已有的**分流点上各多调一路；agentd 侧靠 `store` 的落库钩子补 `event` 引用帧，并照抄 `render_stream.go` 的 offset/tail/follow 形态开一个 ndjson 端点。全程**只加不改**：`render.log`、events 表、adapter 的控制面判定一律不动。

**Tech Stack:** Go 1.26；标准库（`encoding/json`、`os`、`sync`、`net/http`）；cobra（CLI）；SQLite（既有 store）。**不引入任何新的第三方依赖。**

**Spec:** `docs/superpowers/specs/2026-08-12-w4a-structured-turn-frames-design.md`

---

## Global Constraints

- **只加不改**：不删除、不重构、不"顺手统一"任何既有行为。本计划的每一处改动都是**新增一路输出**。
- **四个 adapter 的 `render.log` 产物必须逐字节不变**。claude / opencode 今天不把思维链写进 render.log，改完照旧不写；**codex 与 grok 今天写**（`codex/items.go:162` 的 `【推理】`、`grok/adapter.go:549-556` 的 `renderBuf`），改完照旧写。**不要借机统一四家行为**。
- **思维链绝不进「回合正文」**——也就是 `turn.ParseTrailer` 的输入。现有隔离判定（claude 的 `textDelta` 对 `thinking_delta` 返回 `("", false)`、opencode 的 `partTypes` 闸、grok 的 `bodyBuf`/`renderBuf` 分股、codex 的「回合正文只从 agentMessage 取」）**一行都不许放松**。若发现隔离与帧写入难以共存，**停下来上报，不要动隔离**。
- **不引入新的第三方依赖**（不动 `go.mod`）。
- **凭据纪律**：`Target.Token`、会话 cookie、auth ticket 明文一律不得进日志。**帧内容本身也绝不进日志**——帧里有模型正文与工具输出，整条打日志会撑爆 agentd.log 并把仓库代码复制进日志。只记类型、长度、序号。
- **`internal/proto/` 的独占约定**：本计划单独派发时，Task 1 由执行者完成，完成后**不得再碰** `internal/proto/` 与 `web/src/api/testdata/`。若与任何前端任务（W4b）**并行**派发，Task 1 改由审核者在派发前落地，执行者从 Task 2 起，发现契约不够用必须**停下上报**，不得自行改结构体、不得跑 `-update`。
- **报错原文不改写**：转发、探测、上游返回的错误一律带原文，不包装成"内部错误"。
- **写帧失败只 Warn 不中断回合**：可见性是增强能力，不值得为它挂掉任务（与 `AppendRender` 同一纪律）。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/proto/frames.go`（新建） | `Frame` 与 `FrameType`：前后端共用的帧线格式 |
| `internal/proto/contract_fixture_test.go`（改） | 加一条 `Frame` 用例 |
| `web/src/api/testdata/Frame.json`（生成） | 契约 fixture |
| `internal/executor/turn/headtail.go`（新建） | 头尾截断纯函数 |
| `internal/executor/turn/frames.go`（新建） | `FrameWriter`：帧编码、seq/turn 维护、追加写 |
| `internal/executor/{claudecode,opencode,grok}/render_golden_test.go` + `testdata/render_golden.txt`（新建） | render 产物的改动前基线，证明零回归 |
| `internal/executor/claudecode/adapter.go`（改） | 在已有分流点多调一路帧 |
| `internal/executor/opencode/adapter.go`（改） | 同上 |
| `internal/executor/codex/adapter.go`（改） | 同上 |
| `internal/executor/grok/adapter.go`（改） | 同上 |
| `internal/store/store.go`（改） | `SetEventHook`：落库后同步回调一次 |
| `internal/agentd/eventframes.go`（新建） | 注册钩子，把事件写成 `event` 引用帧 |
| `internal/agentd/frames_stream.go`（新建） | `GET /api/tasks/{id}/frames` |
| `internal/agentd/server.go`（改） | 挂路由（走 `byTask`） |
| `internal/client/client.go`（改） | `FramesStream` |
| `cmd/frames.go`（新建） | `handoff frames` |

---

## 契约附录（规范性）

Task 1 落地后，以下类型即为**只读契约**。后续任务一律引用它，不得改名、不得加字段。

```go
type FrameType string

const (
	FrameText       FrameType = "text"
	FrameReasoning  FrameType = "reasoning"
	FrameToolCall   FrameType = "tool_call"
	FrameToolResult FrameType = "tool_result"
	FrameEvent      FrameType = "event"
	FrameTurnStart  FrameType = "turn_start"
)

type Frame struct {
	Seq       int64     `json:"seq"`
	TS        time.Time `json:"ts"`
	Turn      int       `json:"turn"`
	Type      FrameType `json:"type"`
	Part      string    `json:"part,omitempty"`
	Delta     string    `json:"delta,omitempty"`
	Tool      string    `json:"tool,omitempty"`
	Input     string    `json:"input,omitempty"`
	Output    string    `json:"output,omitempty"`
	Status    string    `json:"status,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
	RefSeq    int64     `json:"ref_seq,omitempty"`
	Event     string    `json:"event,omitempty"`
	Reason    string    `json:"reason,omitempty"`
}
```

`Reason` 只有两个取值：`"dispatch"`（`Adapter.Start`）与 `"send"`（`Adapter.Send`）。

---

## Task 1: 帧的线格式契约

**Files:**
- Create: `internal/proto/frames.go`
- Modify: `internal/proto/contract_fixture_test.go`
- Generate: `web/src/api/testdata/Frame.json`

**Interfaces:**
- Consumes: 无
- Produces: `proto.Frame` 结构体；`proto.FrameType` 及六个常量 `FrameText` / `FrameReasoning` / `FrameToolCall` / `FrameToolResult` / `FrameEvent` / `FrameTurnStart`（全部见上方契约附录）

- [x] **Step 1: 建 `internal/proto/frames.go`**

```go
// frames.go —— 结构化回合帧的线格式。
//
// 职责：
//   - 定义 Frame 与 FrameType：frames.jsonl 每一行、以及
//     GET /api/tasks/{id}/frames 每一行的形状
//
// 边界：
//   - 纯类型定义：不写文件、不做 I/O、不认识任何具体 executor
//   - 不是事件：控制面事件是 Event（events 表），帧只用 RefSeq 指向它
//
// 为什么帧的 Seq 与 Event.Seq 是两套编号：帧 Seq 是**任务内**从 1 开始的行号，
// 由 FrameWriter 维护；Event.Seq 是 SQLite 的**库级**自增主键。混用会让
// 「第 5 帧」和「第 5 号事件」互相冒充。
package proto

import "time"

// FrameType 是帧的类型。
type FrameType string

const (
	// FrameText 是模型正文增量（按 Part 拼接）。
	FrameText FrameType = "text"
	// FrameReasoning 是思维链增量（按 Part 拼接）。绝不进回合正文。
	FrameReasoning FrameType = "reasoning"
	// FrameToolCall 是一次工具调用，一次性完整帧。
	FrameToolCall FrameType = "tool_call"
	// FrameToolResult 是一次工具结果，与 tool_call 靠同一个 Part 配对。
	FrameToolResult FrameType = "tool_result"
	// FrameEvent 是控制面事件的引用（只存指针与类型名，不复制 payload）。
	FrameEvent FrameType = "event"
	// FrameTurnStart 是回合边界。
	FrameTurnStart FrameType = "turn_start"
)

// Frame 是一条结构化回合帧，对应 frames.jsonl 的一行。
//
// 字段按 Type 取用，无关字段一律 omitempty 缺席：
//   - text / reasoning:   Part + Delta
//   - tool_call:          Part + Tool + Input（可能 Truncated，Bytes 为原始长度）
//   - tool_result:        Part + Status + Output（同上）
//   - event:              RefSeq + Event
//   - turn_start:         Reason（"dispatch" 或 "send"）
type Frame struct {
	// Seq 是任务内单调递增的帧号，从 1 开始。与 Event.Seq 无关（见文件头）。
	Seq int64 `json:"seq"`
	// TS 是帧产生时刻。
	TS time.Time `json:"ts"`
	// Turn 是回合序号，从 1 开始。
	Turn int `json:"turn"`
	// Type 决定下面哪些字段有意义。
	Type FrameType `json:"type"`

	// Part 标识帧所属的片段：text/reasoning 靠它拼接，tool_call/tool_result
	// 靠它配对。只需在**同一回合内**唯一，跨回合可以重复。
	Part string `json:"part,omitempty"`

	// Delta 是 text / reasoning 的文本增量（不是快照）。
	Delta string `json:"delta,omitempty"`

	// Tool 是 tool_call 的工具名。
	Tool string `json:"tool,omitempty"`
	// Input 是 tool_call 的入参，可能被头尾截断。
	Input string `json:"input,omitempty"`
	// Output 是 tool_result 的输出，可能被头尾截断。
	Output string `json:"output,omitempty"`
	// Status 是 tool_result 的结局（ok / error / 上游原文）。
	Status string `json:"status,omitempty"`

	// Truncated 报告 Input/Output 是否被截断。
	Truncated bool `json:"truncated,omitempty"`
	// Bytes 是截断前的原始字节数（未截断时为 0）。
	Bytes int64 `json:"bytes,omitempty"`

	// RefSeq 是 event 帧指向的 events 表 seq。
	RefSeq int64 `json:"ref_seq,omitempty"`
	// Event 是 event 帧的事件类型名。刻意的小冗余：让前端不查 events 表
	// 也知道该画什么形状的卡片，类型名是稳定的，不会漂移。
	Event string `json:"event,omitempty"`

	// Reason 是 turn_start 的起因："dispatch"（Adapter.Start）或
	// "send"（Adapter.Send）。不细分"续接"与"回答提问"——Send 是单一方法，
	// adapter 分不出来，编出来的区分是假的。
	Reason string `json:"reason,omitempty"`
}
```

- [x] **Step 2: 在 `contract_fixture_test.go` 的 `cases` 切片末尾加一条**

在 `{"StatusResp", statusSample(now, taskID)},` 之后加：

```go
		{"Frame", frameSample(now)},
```

- [x] **Step 3: 在 `contract_fixture_test.go` 文件末尾加样本函数**

```go
// frameSample 返回 Frame 的代表性样本（被截断的 tool_result）。
//
// 为什么选 tool_result 而不是 text：它是字段最多的一种帧，能同时钉住
// Part/Status/Output/Truncated/Bytes 五个字段的序列化结果；text 帧只有
// Part+Delta，钉不住 omitempty 的边界。
func frameSample(now time.Time) Frame {
	return Frame{
		Seq:       42,
		TS:        now,
		Turn:      2,
		Type:      FrameToolResult,
		Part:      "toolu_01ABCdefGHIjklMNOpqrs",
		Status:    "error",
		Output:    "go: downloading …\n…（已截断）…\nFAIL\texit status 1",
		Truncated: true,
		Bytes:     193422,
	}
}
```

- [x] **Step 4: 生成 fixture 并检查生成物**

```bash
go test ./internal/proto/ -run TestContractFixtures -update
cat web/src/api/testdata/Frame.json
```

Expected: 文件生成，内容形如（`ts` 用 fixture 固定时区 `+08:00`）：

```json
{
  "seq": 42,
  "ts": "2026-08-11T10:30:00+08:00",
  "turn": 2,
  "type": "tool_result",
  "part": "toolu_01ABCdefGHIjklMNOpqrs",
  "output": "go: downloading …\n…（已截断）…\nFAIL\texit status 1",
  "status": "error",
  "truncated": true,
  "bytes": 193422
}
```

关键是确认 `delta` / `tool` / `input` / `ref_seq` / `event` / `reason` 六个键**缺席**——这正是 omitempty 要钉住的。

- [x] **Step 5: 不带 -update 重跑，确认契约稳定**

Run: `go test ./internal/proto/ -run TestContractFixtures -v`
Expected: PASS

- [x] **Step 6: Commit**

```bash
git add internal/proto/frames.go internal/proto/contract_fixture_test.go web/src/api/testdata/Frame.json
git commit -m "feat(proto): Frame 帧线格式契约 + fixture"
```

---

## Task 2: 头尾截断

**Files:**
- Create: `internal/executor/turn/headtail.go`
- Test: `internal/executor/turn/headtail_test.go`

**Interfaces:**
- Consumes: `executor.TruncationMarker`（已存在，值为 `"…（已截断）"`）
- Produces: `func turn.HeadTail(s string, head, tail int) (out string, truncated bool, orig int64)`；常量 `turn.FrameFieldHead = 4 << 10`、`turn.FrameFieldTail = 4 << 10`

- [x] **Step 1: 写失败的测试**

Create `internal/executor/turn/headtail_test.go`:

```go
package turn

import (
	"strings"
	"testing"
)

func TestHeadTailShortStringUntouched(t *testing.T) {
	out, truncated, orig := HeadTail("hello", 10, 10)
	if out != "hello" || truncated || orig != 5 {
		t.Fatalf("短串不该被动: out=%q truncated=%v orig=%d", out, truncated, orig)
	}
}

func TestHeadTailKeepsBothEnds(t *testing.T) {
	s := "HEAD" + strings.Repeat("x", 100) + "TAIL"
	out, truncated, orig := HeadTail(s, 4, 4)
	if !truncated {
		t.Fatal("应当报告已截断")
	}
	if orig != int64(len(s)) {
		t.Fatalf("orig 应为原始字节数 %d，实得 %d", len(s), orig)
	}
	if !strings.HasPrefix(out, "HEAD") {
		t.Fatalf("头部丢了: %q", out)
	}
	// 尾部是关键：报错与 stack trace 通常在尾部
	if !strings.HasSuffix(out, "TAIL") {
		t.Fatalf("尾部丢了: %q", out)
	}
	if !strings.Contains(out, "…（已截断）") {
		t.Fatalf("缺少截断标记: %q", out)
	}
}

// 多字节字符不能被切成半个：切在 UTF-8 码点中间会产生 U+FFFD，
// 前端拿到的就是一串乱码方块。
func TestHeadTailNeverSplitsRune(t *testing.T) {
	s := strings.Repeat("中", 100) // 每个 3 字节
	out, truncated, _ := HeadTail(s, 4, 4)
	if !truncated {
		t.Fatal("应当报告已截断")
	}
	if strings.ContainsRune(out, '�') {
		t.Fatalf("切出了半个字符: %q", out)
	}
}

// 头尾预算合起来已经覆盖全串时不该截断——否则会出现「截断后反而更长」。
func TestHeadTailNoTruncateWhenBudgetCovers(t *testing.T) {
	s := strings.Repeat("a", 20)
	out, truncated, _ := HeadTail(s, 10, 10)
	if truncated || out != s {
		t.Fatalf("预算刚好覆盖时不该截断: out=%q truncated=%v", out, truncated)
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/turn/ -run TestHeadTail -v`
Expected: FAIL，`undefined: HeadTail`

- [x] **Step 3: 写实现**

Create `internal/executor/turn/headtail.go`:

```go
// headtail.go —— 帧字段的头尾截断。
//
// 职责：把超长的工具入参/输出压成「头 + 省略标记 + 尾」，并报告原始长度
// 边界：纯函数，不打日志、不做 I/O；不认识帧结构，只处理字符串
//
// 为什么头尾都留而不是只留头：报错信息与 stack trace 几乎总在输出**尾部**，
// 纯头部截断会刚好切掉最有用的那一段——那正是审核者要看的东西。
package turn

import (
	"strings"
	"unicode/utf8"

	"github.com/xushixin/handoff/internal/executor"
)

const (
	// FrameFieldHead 是帧字段保留的头部字节预算。
	FrameFieldHead = 4 << 10
	// FrameFieldTail 是帧字段保留的尾部字节预算。
	FrameFieldTail = 4 << 10
)

// HeadTail 把 s 压成「头 head 字节 + 截断标记 + 尾 tail 字节」。
//
// 参数：
//   - s:    原始字符串
//   - head: 头部保留的字节预算（按 rune 边界向内收缩，不会切出半个字符）
//   - tail: 尾部保留的字节预算（同上）
//
// 返回：
//   - out:       结果字符串；未截断时与 s 相同
//   - truncated: 是否确实发生了截断
//   - orig:      s 的原始字节数（无论是否截断都返回真实值）
//
// 注意：head+tail 已能覆盖整串时原样返回——否则会出现「截断后比原文还长」
// （多了一个标记）这种荒唐结果。
func HeadTail(s string, head, tail int) (out string, truncated bool, orig int64) {
	orig = int64(len(s))
	if len(s) <= head+tail {
		return s, false, orig
	}
	h := s[:sliceToRuneBoundary(s, head)]
	t := s[len(s)-tailToRuneBoundary(s, tail):]
	var b strings.Builder
	b.WriteString(h)
	b.WriteString(executor.TruncationMarker)
	b.WriteString(t)
	return b.String(), true, orig
}

// sliceToRuneBoundary 返回 <= n 的最大下标，且该下标是一个 rune 的起点。
//
// 为什么要向内收缩而不是直接切：切在 UTF-8 码点中间会产出 U+FFFD 替换字符，
// 前端渲染出来是一串乱码方块，而且 JSON 编码后还会把这个损坏悄悄固化下来。
func sliceToRuneBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// tailToRuneBoundary 返回 <= n 的最大尾部长度，且切点是一个 rune 的起点。
func tailToRuneBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8.RuneStart(s[len(s)-n]) {
		n--
	}
	return n
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/turn/ -run TestHeadTail -v`
Expected: PASS（四个用例全过）

- [x] **Step 5: 加注释自检**

本任务的注释在 Step 3 的代码里已经写全，逐项确认：
- 文件头有职责与边界（含「为什么头尾都留」）
- 三个函数都有导出/内部注释，`HeadTail` 有参数与返回说明
- `sliceToRuneBoundary` 说明了「为什么要向内收缩」而不只是「做了什么」

本任务是纯函数、无 I/O、无错误分支，按 `instrumenting-code` 的适用范围**不加日志**——
调用方（Task 3）会在截断发生时打 Debug。

- [x] **Step 6: Commit**

```bash
git add internal/executor/turn/headtail.go internal/executor/turn/headtail_test.go
git commit -m "feat(turn): 帧字段头尾截断，按 rune 边界收缩不切坏多字节字符"
```

---

## Task 3: FrameWriter

**Files:**
- Create: `internal/executor/turn/frames.go`
- Test: `internal/executor/turn/frames_test.go`

**Interfaces:**
- Consumes: `proto.Frame`、`proto.FrameType` 及六个常量（Task 1）；`turn.HeadTail`、`turn.FrameFieldHead`、`turn.FrameFieldTail`（Task 2）
- Produces:
  - `const turn.FramesFileName = "frames.jsonl"`
  - `func turn.NewFrameWriter(taskDir string, log *slog.Logger) (*turn.FrameWriter, error)`
  - `func (*turn.FrameWriter) BeginTurn(reason string) error`
  - `func (*turn.FrameWriter) NextPart() string`
  - `func (*turn.FrameWriter) Text(part, delta string) error`
  - `func (*turn.FrameWriter) Reasoning(part, delta string) error`
  - `func (*turn.FrameWriter) ToolCall(part, tool, input string) error`
  - `func (*turn.FrameWriter) ToolResult(part, status, output string) error`
  - `func (*turn.FrameWriter) EventRef(refSeq int64, eventType string) error`
  - **全部方法对 nil 接收者安全**（返回 nil，什么都不做）

- [x] **Step 1: 写失败的测试**

Create `internal/executor/turn/frames_test.go`:

```go
package turn

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// readFrames 读出 taskDir 下 frames.jsonl 的全部帧。
func readFrames(t *testing.T, taskDir string) []proto.Frame {
	t.Helper()
	f, err := os.Open(filepath.Join(taskDir, FramesFileName))
	if err != nil {
		t.Fatalf("打开 frames.jsonl: %v", err)
	}
	defer f.Close()
	var out []proto.Frame
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var fr proto.Frame
		if err := json.Unmarshal(sc.Bytes(), &fr); err != nil {
			t.Fatalf("解析帧 %q: %v", sc.Text(), err)
		}
		out = append(out, fr)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("扫描 frames.jsonl: %v", err)
	}
	return out
}

func TestFrameWriterWritesEachType(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFrameWriter(dir, nil)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if err := w.BeginTurn("dispatch"); err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	if err := w.Reasoning("p01", "先看看测试"); err != nil {
		t.Fatalf("Reasoning: %v", err)
	}
	if err := w.Text("p02", "我来实现"); err != nil {
		t.Fatalf("Text: %v", err)
	}
	if err := w.ToolCall("p03", "Bash", "go test ./..."); err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if err := w.ToolResult("p03", "ok", "PASS"); err != nil {
		t.Fatalf("ToolResult: %v", err)
	}
	if err := w.EventRef(88, "permission_request"); err != nil {
		t.Fatalf("EventRef: %v", err)
	}

	frames := readFrames(t, dir)
	if len(frames) != 6 {
		t.Fatalf("应有 6 帧，实得 %d", len(frames))
	}
	wantTypes := []proto.FrameType{
		proto.FrameTurnStart, proto.FrameReasoning, proto.FrameText,
		proto.FrameToolCall, proto.FrameToolResult, proto.FrameEvent,
	}
	for i, want := range wantTypes {
		if frames[i].Type != want {
			t.Errorf("第 %d 帧类型应为 %s，实得 %s", i, want, frames[i].Type)
		}
		if frames[i].Seq != int64(i+1) {
			t.Errorf("第 %d 帧 seq 应为 %d，实得 %d", i, i+1, frames[i].Seq)
		}
		if frames[i].Turn != 1 {
			t.Errorf("第 %d 帧 turn 应为 1，实得 %d", i, frames[i].Turn)
		}
	}
	if frames[0].Reason != "dispatch" {
		t.Errorf("turn_start 的 reason 应为 dispatch，实得 %q", frames[0].Reason)
	}
	if frames[5].RefSeq != 88 || frames[5].Event != "permission_request" {
		t.Errorf("event 帧应带 ref_seq=88 与类型名，实得 %+v", frames[5])
	}
}

func TestFrameWriterTurnIncrements(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	_ = w.Text("p01", "第一轮")
	_ = w.BeginTurn("send")
	_ = w.Text("p01", "第二轮")

	frames := readFrames(t, dir)
	if frames[1].Turn != 1 {
		t.Errorf("第一轮的 text 应在 turn 1，实得 %d", frames[1].Turn)
	}
	if frames[3].Turn != 2 {
		t.Errorf("第二轮的 text 应在 turn 2，实得 %d", frames[3].Turn)
	}
	if frames[2].Reason != "send" {
		t.Errorf("第二个 turn_start 的 reason 应为 send，实得 %q", frames[2].Reason)
	}
}

// agentd 重启后 adapter 会重建 FrameWriter：必须接着上次的 seq/turn 写，
// 从 1 重来会让前端把新帧插到时间线开头。
func TestFrameWriterResumesSeqAndTurn(t *testing.T) {
	dir := t.TempDir()
	w1, _ := NewFrameWriter(dir, nil)
	_ = w1.BeginTurn("dispatch")
	_ = w1.Text("p01", "重启前")
	_ = w1.BeginTurn("send")

	w2, err := NewFrameWriter(dir, nil)
	if err != nil {
		t.Fatalf("重建 FrameWriter: %v", err)
	}
	if err := w2.Text("p01", "重启后"); err != nil {
		t.Fatalf("Text: %v", err)
	}

	frames := readFrames(t, dir)
	last := frames[len(frames)-1]
	if last.Seq != 4 {
		t.Errorf("重建后应接着写 seq 4，实得 %d", last.Seq)
	}
	if last.Turn != 2 {
		t.Errorf("重建后应沿用 turn 2，实得 %d", last.Turn)
	}
}

func TestFrameWriterTruncatesToolFields(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	big := strings.Repeat("x", FrameFieldHead+FrameFieldTail+1000)
	_ = w.ToolResult("p01", "ok", big)

	frames := readFrames(t, dir)
	fr := frames[len(frames)-1]
	if !fr.Truncated {
		t.Error("超长输出应被标记为已截断")
	}
	if fr.Bytes != int64(len(big)) {
		t.Errorf("bytes 应为原始长度 %d，实得 %d", len(big), fr.Bytes)
	}
	if len(fr.Output) >= len(big) {
		t.Errorf("截断后不该还是原长：%d", len(fr.Output))
	}
}

// SSE / stream-json 的处理可能跑在多个 goroutine 上：seq 必须与写入顺序
// 严格一致，否则按 offset 续读会错位。
func TestFrameWriterConcurrentWritesKeepSeqDense(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Text("p01", "x")
		}()
	}
	wg.Wait()

	frames := readFrames(t, dir)
	if len(frames) != n {
		t.Fatalf("应有 %d 帧（行未交错），实得 %d", n, len(frames))
	}
	seen := map[int64]bool{}
	for _, fr := range frames {
		if seen[fr.Seq] {
			t.Fatalf("seq %d 重复", fr.Seq)
		}
		seen[fr.Seq] = true
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("seq %d 缺失（应当连续无洞）", i)
		}
	}
}

// nil 接收者安全：构造失败时 adapter 直接持有 nil，调用点不必到处判空。
func TestFrameWriterNilReceiverIsNoop(t *testing.T) {
	var w *FrameWriter
	if err := w.BeginTurn("dispatch"); err != nil {
		t.Errorf("nil.BeginTurn 应返回 nil，实得 %v", err)
	}
	if err := w.Text("p01", "x"); err != nil {
		t.Errorf("nil.Text 应返回 nil，实得 %v", err)
	}
	if p := w.NextPart(); p != "" {
		t.Errorf("nil.NextPart 应返回空串，实得 %q", p)
	}
}

func TestFrameWriterNextPartIsUniqueWithinTurn(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewFrameWriter(dir, nil)
	_ = w.BeginTurn("dispatch")
	a, b := w.NextPart(), w.NextPart()
	if a == b {
		t.Fatalf("同回合内 NextPart 应互不相同，两次都是 %q", a)
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/turn/ -run TestFrameWriter -v`
Expected: FAIL，`undefined: NewFrameWriter` / `undefined: FramesFileName`

- [x] **Step 3: 写实现**

Create `internal/executor/turn/frames.go`:

```go
// frames.go —— 结构化回合帧落盘到 frames.jsonl。
//
// 职责：
//   - 把回合内容（正文/思维链/工具调用/工具结果/事件引用/回合边界）编码成
//     proto.Frame，逐行追加进任务目录的 frames.jsonl
//   - 维护任务内的帧号 seq 与回合号 turn（进程重启后从文件恢复）
//   - 对工具入参/输出做头尾截断，并如实记录原始长度
//
// 边界：
//   - 不认识任何具体 executor（与 AppendRender 同一层，是它的姊妹件）
//   - 不解释帧内容、不做过滤判定：谁该写 reasoning、谁不该，由 adapter 决定
//   - 不轮转、不清理：frames.jsonl 随任务目录走（done 不删任务目录）
//   - 不碰 render.log：两路输出彼此独立
//
// 为什么每次写都开关文件而不是长持文件句柄：与 AppendRender 完全一致的形态，
// 省掉 Close 的生命周期（adapter 重建、进程重启、任务归档三条路径都要管），
// 而帧的写入频率与 AppendRender 同量级，开销可以忽略。
package turn

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// FramesFileName 是任务目录内帧文件的固定名字。
const FramesFileName = "frames.jsonl"

// frameLineLimit 是单帧编码后的硬上限。
//
// 为什么要有：头尾截断只管 Input/Output 两个字段，将来新增字段时可能悄悄写出
// 巨行把流式读取拖垮。这道闸让那种回归当场变成一条 Warn，而不是线上的卡顿。
const frameLineLimit = 16 << 10

// framesResumeScan 是恢复 seq/turn 时从文件尾部回读的字节数。
// 单帧上限 16KB，回读 64KB 足以覆盖到最后一条完整帧。
const framesResumeScan = 64 << 10

// FrameWriter 把结构化回合帧追加进任务目录的 frames.jsonl。
//
// 并发安全：seq 分配与写入在同一把锁内完成，保证「帧号顺序 == 文件字节顺序」。
// 这不是性能优化的牺牲品——按 offset 续读的客户端依赖这条不变式对齐。
//
// nil 安全：全部方法对 nil 接收者是空操作。构造失败时 adapter 直接持有 nil，
// 调用点不必到处判空——可见性失败不该在正常路径上撒判空代码。
type FrameWriter struct {
	path string
	log  *slog.Logger

	mu       sync.Mutex
	seq      int64
	turn     int
	nextPart int
}

// NewFrameWriter 打开（或准备创建）taskDir 下的 frames.jsonl，并恢复 seq/turn。
//
// 参数：
//   - taskDir: 任务目录（agentd 在 DataDir/tasks/<id> 下创建）
//   - log:     日志入口，可为 nil（测试里常传 nil）
//
// 返回：可用的 FrameWriter；只有 taskDir 不可读时才返回错误。
//
// 注意：文件不存在是正常起点（seq=0, turn=0），不是错误。
func NewFrameWriter(taskDir string, log *slog.Logger) (*FrameWriter, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	w := &FrameWriter{path: filepath.Join(taskDir, FramesFileName), log: log}
	seq, turn, err := resumeFrameState(w.path)
	if err != nil {
		return nil, fmt.Errorf("恢复帧状态 %s: %w", w.path, err)
	}
	w.seq, w.turn = seq, turn
	// 恢复到的位置是「帧流断档」的第一诊断信号：seq 突然回到 0 说明文件被清过
	log.Info("帧写入器就绪", "path", w.path, "resume_seq", seq, "resume_turn", turn)
	return w, nil
}

// BeginTurn 开启新回合：turn 自增、part 计数归零，并写一条 turn_start 帧。
//
// reason 只应是 "dispatch"（Adapter.Start）或 "send"（Adapter.Send）。
func (w *FrameWriter) BeginTurn(reason string) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	w.turn++
	w.nextPart = 0
	turn := w.turn
	w.mu.Unlock()
	w.log.Info("回合开始", "turn", turn, "reason", reason)
	return w.append(proto.Frame{Type: proto.FrameTurnStart, Reason: reason})
}

// NextPart 分配一个回合内唯一的 part 标识（p01、p02…）。
//
// 上游流自带 part / block / item 标识时**优先沿用上游的**，本方法只服务
// 那些没有标识的流——两个来源混用不会撞车，因为 p 前缀是本方法独有的。
func (w *FrameWriter) NextPart() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextPart++
	return fmt.Sprintf("p%02d", w.nextPart)
}

// Text 写一条模型正文增量帧。
func (w *FrameWriter) Text(part, delta string) error {
	if w == nil || delta == "" {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameText, Part: part, Delta: delta})
}

// Reasoning 写一条思维链增量帧。
//
// 注意：本方法只负责落盘。「思维链不能进回合正文」是 adapter 的判定，
// 不在这里——本包不认识回合正文。
func (w *FrameWriter) Reasoning(part, delta string) error {
	if w == nil || delta == "" {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameReasoning, Part: part, Delta: delta})
}

// ToolCall 写一条工具调用帧；input 超长时头尾截断。
func (w *FrameWriter) ToolCall(part, tool, input string) error {
	if w == nil {
		return nil
	}
	out, truncated, orig := HeadTail(input, FrameFieldHead, FrameFieldTail)
	if truncated {
		w.log.Debug("工具入参已截断", "tool", tool, "bytes", orig)
	}
	return w.append(proto.Frame{
		Type: proto.FrameToolCall, Part: part, Tool: tool,
		Input: out, Truncated: truncated, Bytes: truncatedBytes(truncated, orig),
	})
}

// ToolResult 写一条工具结果帧；output 超长时头尾截断。
func (w *FrameWriter) ToolResult(part, status, output string) error {
	if w == nil {
		return nil
	}
	out, truncated, orig := HeadTail(output, FrameFieldHead, FrameFieldTail)
	if truncated {
		w.log.Debug("工具输出已截断", "status", status, "bytes", orig)
	}
	return w.append(proto.Frame{
		Type: proto.FrameToolResult, Part: part, Status: status,
		Output: out, Truncated: truncated, Bytes: truncatedBytes(truncated, orig),
	})
}

// EventRef 写一条控制面事件的引用帧。
//
// 只存 seq 与类型名，不复制 payload：payload 的真相在 events 表，复制一份
// 就有了两份会漂移的真相。
func (w *FrameWriter) EventRef(refSeq int64, eventType string) error {
	if w == nil {
		return nil
	}
	return w.append(proto.Frame{Type: proto.FrameEvent, RefSeq: refSeq, Event: eventType})
}

// truncatedBytes 只在确实截断时返回原始长度，否则返回 0（让 omitempty 生效）。
//
// 为什么不无脑返回 orig：未截断的帧带一个 bytes 字段等于告诉前端「这里发生过
// 截断」，是误导。
func truncatedBytes(truncated bool, orig int64) int64 {
	if truncated {
		return orig
	}
	return 0
}

// append 分配 seq、编码成一行并追加进文件。
//
// 注意：seq 分配与写入必须在同一把锁内——分配完再放锁去写，两个 goroutine
// 就可能以 2、1 的顺序落盘，按 offset 续读的客户端会看到 seq 倒退。
func (w *FrameWriter) append(f proto.Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	f.Seq = w.seq
	f.Turn = w.turn
	f.TS = time.Now()

	line, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("编码帧 seq=%d: %w", f.Seq, err)
	}
	if len(line) > frameLineLimit {
		// 不丢帧也不放行：截断字段兜不住的巨行要能被看见（见 frameLineLimit 注释）
		w.log.Warn("单帧超出行上限，仍照写但请排查字段体量",
			"seq", f.Seq, "type", f.Type, "line_bytes", len(line), "limit", frameLineLimit)
	}
	line = append(line, '\n')

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", w.path, err)
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("写 %s: %w", w.path, err)
	}
	return nil
}

// resumeFrameState 从已有的 frames.jsonl 末尾恢复 seq 与 turn。
//
// 返回 (0, 0, nil) 表示文件不存在或没有可解析的完整帧——都是正常起点。
//
// 为什么只回读尾部而不是整文件：帧文件可以很大（数千帧），而恢复只需要最后
// 一条。回读 framesResumeScan 字节，取其中最后一条能解析的完整行即可。
func resumeFrameState(path string) (seq int64, turn int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	start := fi.Size() - framesResumeScan
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return 0, 0, err
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), frameLineLimit*2)
	for sc.Scan() {
		var fr proto.Frame
		// 回读起点可能落在半行中间，第一行解析失败是预期内的，跳过即可
		if json.Unmarshal(sc.Bytes(), &fr) != nil {
			continue
		}
		if fr.Seq > seq {
			seq, turn = fr.Seq, fr.Turn
		}
	}
	// 扫描出错（如超长行）不当致命：宁可从当前已知的最大 seq 接着写
	return seq, turn, nil
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/turn/ -v`
Expected: PASS（Task 2 与 Task 3 的全部用例，含 `-race`）

再跑一次带竞态检测：

```bash
go test ./internal/executor/turn/ -race -run TestFrameWriter
```

Expected: PASS，无 race 报告

- [x] **Step 5: 加关键节点日志**

Step 3 的实现里已按 `instrumenting-code` 埋点，逐项确认：
- `NewFrameWriter` 成功：Info，带 `path` + `resume_seq` + `resume_turn`（帧流断档的第一诊断信号）
- `BeginTurn`：Info，带 `turn` + `reason`
- 截断发生：Debug，带 `bytes`（高频，**不能** Info）
- 单帧超限：Warn，带 `seq` + `type` + `line_bytes`
- **帧内容本身绝不进日志**：确认没有任何一行日志带 `delta` / `input` / `output` 的值

- [x] **Step 6: 加注释**

确认：文件头有职责/边界，并解释了「为什么每次写都开关文件」；`FrameWriter` 结构体注释说明并发与 nil 语义；每个导出方法有注释；`append` 解释了「为什么 seq 分配与写入必须同锁」；`resumeFrameState` 解释了「为什么只回读尾部」与「第一行解析失败是预期」。

- [x] **Step 7: Commit**

```bash
git add internal/executor/turn/frames.go internal/executor/turn/frames_test.go
git commit -m "feat(turn): FrameWriter——帧编码、seq/turn 恢复、并发安全追加写"
```

---

## Task 4: `render.log` 黄金基线（必须在改 adapter 之前完成）

**Files:**
- Create: `internal/executor/claudecode/render_golden_test.go`
- Create: `internal/executor/claudecode/testdata/render_golden.txt`（生成）
- Create: `internal/executor/opencode/render_golden_test.go`
- Create: `internal/executor/opencode/testdata/render_golden.txt`（生成）
- Create: `internal/executor/grok/render_golden_test.go`
- Create: `internal/executor/grok/testdata/render_golden.txt`（生成）

**Interfaces:**
- Consumes: 三个包各自**已有**的回放素材——`claudecode/testdata/turn_success.jsonl`、`opencode` 的 `startReplay`+`spike5-events.jsonl`、`grok/testdata/updates.jsonl` 与 `NewTurnAccumulatorForTest` / `RenderTextForTest`
- Produces: 三条 `TestRenderGolden` 用例与三份 golden 文件；后续 Task 5/6/8 靠它们证明 render.log 零回归

**这个任务为什么必须排在四个 adapter 之前：** 基线的全部价值在于它是**改动前**的产物。改完再录，录下来的就是改动后的行为，断言恒真，等于没有。

**顺序是不可协商的**：先在**完全未改动**的代码上录 golden 并提交，再动 adapter。执行者若发现自己已经改了某个 adapter 才想起来录基线，**必须把那个 adapter 的改动 stash/revert 掉，重录，再重做改动**。

**codex 没有这一条，是刻意的**：`internal/executor/codex/` 下**没有** testdata 目录，仓库里不存在任何 codex 的真实抓包。手写一份 JSON 冒充抓包比没有更糟——opencode 的 `TestSpikeFixturesAreRealCaptures` 就是专门为防这件事写的。codex 的 render.log 零回归由 Task 7 的既有用例全绿 + Task 12 Step 6 的 `git diff` 检查兜底，并在 Task 12 的自评里如实写明「codex 缺真实抓包，未建 golden」。**不要为了凑齐四个而伪造 fixture。**

- [x] **Step 1: 确认工作区干净、没有任何 adapter 改动**

```bash
git status --porcelain internal/executor/
```

Expected: **无输出**。有输出说明已经改了东西，先处理干净再继续（见上面的顺序纪律）。

- [x] **Step 2: 写 grok 的 golden 用例（三个里最直接的，先做它）**

grok 的 `RenderTextForTest()` 直接返回 render 那一股的全文，不需要落盘。

Create `internal/executor/grok/render_golden_test.go`:

```go
// render_golden_test.go —— grok 的 render 产物黄金基线。
//
// 职责：把 testdata/updates.jsonl 喂进 turnAccumulator，断言 render 那一股的
// 产物与 testdata/render_golden.txt 逐字节相等
//
// 边界：
//   - 不断言「render 里没有思维链」——grok 今天**就是**把思维链写进 render 的
//     （adapter.go 的 renderBuf 显式收「正文 + 推理 + 工具动作」）。拿「不含」
//     去断言会把正确的现状判成失败
//   - 不解释内容，只做字节比对
//
// 为什么是逐字节而不是「包含若干关键字」：W4a 要在这个分流点上多接一路帧，
// 而「多接一路」最容易的失手方式就是顺手把这一股也改了。关键字断言容忍空格、
// 顺序、前后缀的漂移；字节比对不容忍。这正是本文件唯一的作用。
package grok_test

import (
	"bufio"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// updateGolden 用 -update 重录基线。**只有在刻意变更 render 行为时才可以用它**。
var updateGolden = flag.Bool("update", false, "重录 render golden 基线")

func TestRenderGolden(t *testing.T) {
	f, err := os.Open("testdata/updates.jsonl")
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer f.Close()

	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.FeedRawForTest([]byte(line))
		}
	}
	got := h.RenderTextForTest()

	path := filepath.Join("testdata", "render_golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		t.Logf("已重录 golden（%d 字节）", len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden（首次请用 -update 生成）: %v", err)
	}
	if got != string(want) {
		t.Errorf("render 产物与基线不符——W4a 不该改动这一股。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(got), got)
	}
}
```

- [x] **Step 3: 录 grok 基线并确认它能挡住改动**

```bash
go test ./internal/executor/grok/ -run TestRenderGolden -update
go test ./internal/executor/grok/ -run TestRenderGolden -v
```

Expected: 第一条生成 `testdata/render_golden.txt`，第二条 PASS。

**确认这道闸真的有效**（不做这一步就不知道它是不是恒真）：

```bash
cat internal/executor/grok/testdata/render_golden.txt   # 先看一眼内容，确认非空
printf 'x' >> internal/executor/grok/testdata/render_golden.txt
go test ./internal/executor/grok/ -run TestRenderGolden   # 必须 FAIL
git checkout internal/executor/grok/testdata/render_golden.txt
```

Expected: 中间那条 **FAIL**。若它仍然 PASS，说明比对没生效（多半是 golden 为空），回到 Step 2 排查——**一个恒真的基线比没有基线更危险**。

- [x] **Step 4: 写 opencode 的 golden 用例**

opencode 的 `startReplay` 会把 render 真的写进 taskDir 里的文件，所以这里比对的是**真正的 render.log**。

Create `internal/executor/opencode/render_golden_test.go`:

```go
// render_golden_test.go —— opencode 的 render.log 黄金基线。
//
// 职责：用既有的 spike5 抓包回放一整轮，断言落盘的 render.log 与
// testdata/render_golden.txt 逐字节相等
//
// 边界：只比对 render.log 的字节，不断言事件、不断言回合文本（那些各有其测试）
//
// 为什么复用 spike5 而不是 spike3：spike5 是完整一轮（权限 → 应答 → 模型输出
// → idle），render.log 里能同时出现文本与工具动作两类内容；spike3 只到权限就
// 停了，盖不住后半段。
package opencode

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// updateGolden 用 -update 重录基线。只有刻意变更 render 行为时才可以用它。
var updateGolden = flag.Bool("update", false, "重录 render.log golden 基线")

func TestRenderGolden(t *testing.T) {
	// startReplay 内部用 t.TempDir() 建 taskDir，这里需要拿到那个目录，
	// 所以复用它的做法而不是调它本身——见下面 renderGoldenReplay
	taskDir, ch := renderGoldenReplay(t)
	collectReplay(t, ch, 800*time.Millisecond)

	raw, err := os.ReadFile(filepath.Join(taskDir, renderLogFileName))
	if err != nil {
		t.Fatalf("读回放产生的 render.log: %v", err)
	}
	got := string(raw)

	path := filepath.Join("testdata", "render_golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		t.Logf("已重录 golden（%d 字节）", len(raw))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden（首次请用 -update 生成）: %v", err)
	}
	if got != string(want) {
		t.Errorf("render.log 与基线不符——W4a 不该改动它。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(got), got)
	}
}
```

`startReplay` 目前不返回 taskDir。**不要改它的签名**（它有四个既有调用方）——照它的形状另写一个返回 taskDir 的兄弟函数，追加到同一个新文件里：

```go
// renderGoldenReplay 与 startReplay 做同一件事，额外返回 taskDir，
// 好让本测试读到回放落盘的 render.log。
//
// 为什么不改 startReplay 的签名：它有四个既有调用方，为一个测试改公共
// helper 的形状是把成本摊给无关的用例。复制十行比那便宜。
func renderGoldenReplay(t *testing.T) (string, <-chan executor.AdapterEvent) {
	t.Helper()
	ts := replayServer(t, spike5)
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	ad := New(slog.Default())
	ad.idleGrace = 20 * time.Millisecond
	taskID := "render-golden"
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: taskDir,
	}
	if _, err := ad.startRun(t.Context(), req, NewAPI(ts.URL, adapterTestPassword), &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	return taskDir, ad.Events(taskID)
}
```

补上 `log/slog`、`github.com/xushixin/handoff/internal/executor`、`github.com/xushixin/handoff/internal/proto` 三个 import。`renderLogFileName` 是包内已有的常量（`newRun` 里用它拼 renderPath），用它的真名。

- [x] **Step 5: 录 opencode 基线并验证闸有效**

```bash
go test ./internal/executor/opencode/ -run TestRenderGolden -update
go test ./internal/executor/opencode/ -run TestRenderGolden -v
cat internal/executor/opencode/testdata/render_golden.txt
```

Expected: 生成后 PASS，且 golden **非空**。

回放是有超时的异步过程，**基线必须稳定才有意义**——连跑三次确认不飘：

```bash
go test ./internal/executor/opencode/ -run TestRenderGolden -count=3
```

Expected: 三次全 PASS。若出现偶发失败，说明 800ms 的静默窗口不够，把 `collectReplay` 的等待调大到 1.5s 再重录——**带随机失败的基线会被人当噪声忽略，等于没有**。

- [x] **Step 6: 写 claudecode 的 golden 用例**

claude 现有的 `stream_test.go` 只走 tailer，**碰不到 render 那一层**，所以这里要建一个最小回放：把 `turn_success.jsonl` 的每条消息按生产路径喂进映射函数，让它们往真实 taskDir 写 render.log。

Create `internal/executor/claudecode/render_golden_test.go`:

```go
// render_golden_test.go —— claude 的 render.log 黄金基线。
//
// 职责：把 testdata/turn_success.jsonl 按生产路径喂进映射函数，断言落盘的
// render.log 与 testdata/render_golden.txt 逐字节相等
//
// 边界：
//   - 只比对 render.log；事件映射与回合文本各有其测试
//   - 不走 tailer（那是 stream_test.go 的事），直接按行喂映射函数——本测试
//     要盯的是「消息 → render.log」这一段，不是文件尾随
//
// 为什么 claude 需要新建回放而另两家不用：现有的 stream_test.go 只验证 tailer
// 能解析样本，映射层（mapStreamEvent / mapAssistant / mapUserMessage）在测试里
// 从未跑到过 render 落盘——而那正是 W4a 要动的地方。
package claudecode

import (
	"bufio"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden 用 -update 重录基线。只有刻意变更 render 行为时才可以用它。
var updateGolden = flag.Bool("update", false, "重录 render.log golden 基线")

func TestRenderGolden(t *testing.T) {
	src, err := os.Open("testdata/turn_success.jsonl")
	if err != nil {
		t.Fatalf("读 testdata: %v", err)
	}
	defer src.Close()

	taskDir := t.TempDir()
	a := New(slog.Default())
	r := a.newRun("render-golden", taskDir, t.TempDir())

	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m streamMsg
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		// 按 Run 里的真实分派复现：三类消息各自的映射入口
		switch m.Type {
		case "stream_event":
			a.mapStreamEvent(r, m.Event)
		case "assistant":
			a.mapAssistant(r, m.Message)
		case "user":
			a.mapUserMessage(r, m.Message)
		}
	}

	raw, err := os.ReadFile(filepath.Join(taskDir, renderFileName))
	if err != nil {
		// 样本里一条 render 内容都没有时文件不存在——那说明这个基线盖不住
		// 任何东西，是配置问题而不是通过
		t.Fatalf("读 render.log（样本未产生任何 render 内容？）: %v", err)
	}

	path := filepath.Join("testdata", "render_golden.txt")
	if *updateGolden {
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		t.Logf("已重录 golden（%d 字节）", len(raw))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden（首次请用 -update 生成）: %v", err)
	}
	if string(raw) != string(want) {
		t.Errorf("render.log 与基线不符——W4a 不该改动它。\n基线 %d 字节:\n%q\n实得 %d 字节:\n%q",
			len(want), string(want), len(raw), string(raw))
	}
}
```

**这段要按真实代码校准三处**，都是可以直接读出来的，不要猜：

- `streamMsg` 里承载 `stream_event` / `assistant` / `user` 三类载荷的字段名（上面写的 `m.Event` / `m.Message` 是按 `mapStreamEvent(r, ev json.RawMessage)` 的形参推的）——以 `stream.go` 的结构体定义为准。
- `a.newRun(...)` 的真实签名与参数个数——以 `adapter.go` 为准。
- `Run` 循环里对这三类消息的真实分派——照抄它的 `switch`，**本测试的分派必须与生产一致**，否则基线盖的是一条不存在的路径。

- [x] **Step 7: 录 claude 基线并验证闸有效**

```bash
go test ./internal/executor/claudecode/ -run TestRenderGolden -update
go test ./internal/executor/claudecode/ -run TestRenderGolden -v
cat internal/executor/claudecode/testdata/render_golden.txt
```

Expected: PASS，且 golden **非空**。

若 golden 为空或 `render.log` 不存在：说明 `turn_success.jsonl` 这份样本里没有任何会落到 render 的内容（比如只有 result 没有 text_delta）。**这时不要交一个空基线**——空基线恒真。改为在 Task 12 的自评里如实写明「claude 的现有样本不含 render 内容，未建 golden，零回归靠既有用例 + git diff 兜底」，并把本 Step 产生的文件删掉。

- [x] **Step 8: 三个包一起跑一遍**

```bash
go test ./internal/executor/claudecode/ ./internal/executor/opencode/ ./internal/executor/grok/ 2>&1 | tail -20
```

Expected: 全部 PASS（既有用例 + 三条新 golden）

- [x] **Step 9: 加注释自检**

三个文件的头注释都已写明职责、边界与「为什么是逐字节而不是关键字」。额外确认 grok 那份**明确写了**「不断言 render 里没有思维链」及其原因——这是本计划最容易被好心改错的一处。

本任务是纯测试代码，无生产路径、无错误分支需要观测，按 `instrumenting-code` 的适用范围**不加生产日志**；断言失败时打印的基线/实得字节数与内容就是它的可观测性。

- [x] **Step 10: Commit**

```bash
git add internal/executor/claudecode/render_golden_test.go internal/executor/claudecode/testdata/ \
        internal/executor/opencode/render_golden_test.go internal/executor/opencode/testdata/ \
        internal/executor/grok/render_golden_test.go internal/executor/grok/testdata/
git commit -m "test(executor): render 产物黄金基线（改 adapter 之前固化）

在未改动的代码上录 claude/opencode/grok 三家的 render 产物，
后续 W4a 的分流改动靠它证明逐字节零回归。
codex 无真实抓包，刻意不建 golden——伪造 fixture 比没有更糟。"
```

---

## Task 5: claude adapter 分流出帧

**Files:**
- Modify: `internal/executor/claudecode/adapter.go`（`runState` 结构体 ~89-100 行、`newRun` ~115-125 行、`mapStreamEvent` ~520-531 行、`appendActionSummary` ~566-582 行、`mapUserMessage` ~584-610 行、`Start` 与 `Send` 各加一次 `BeginTurn`）
- Modify: `internal/executor/claudecode/stream.go`（`textDelta` ~170-190 行：改成同时返回思维链）
- Test: `internal/executor/claudecode/frames_test.go`（新建）

**Interfaces:**
- Consumes: `turn.NewFrameWriter`、`(*turn.FrameWriter).BeginTurn/Text/Reasoning/ToolCall/ToolResult`（Task 3）
- Produces: 无（adapter 内部改动，不对外暴露新符号）

**背景（读代码前先看这段）：**
claude 的 stream-json 里，思维链是 `content_block_delta` + `delta.type == "thinking_delta"`。今天 `textDelta` 对它返回 `("", false)`，`mapStreamEvent` 直接 `return`——**思维链既不进 render.log 也不进回合正文**。本任务把「丢弃」改成「多写一路帧」，`render.log` 与回合正文的走向**一字不改**。

工具调用与结果的 part 配对用 claude 自己的 id：assistant 的 `tool_use` 块带 `id`，user 的 `tool_result` 块带 `tool_use_id`，两者相等。**用它做 part，不要用 `NextPart()`**。

- [x] **Step 1: 写失败的测试**

Create `internal/executor/claudecode/frames_test.go`:

```go
package claudecode

import (
	"encoding/json"
	"testing"
)

// thinking_delta 必须被识别为思维链而不是正文：这是 claude 侧隔离的根。
func TestSplitDeltaSeparatesThinkingFromText(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"我先想想"}}`)
	text, reasoning := splitDelta(thinking)
	if text != "" {
		t.Errorf("思维链绝不能作为正文返回，实得 %q", text)
	}
	if reasoning != "我先想想" {
		t.Errorf("思维链内容应被取出，实得 %q", reasoning)
	}

	normal := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)
	text, reasoning = splitDelta(normal)
	if text != "你好" {
		t.Errorf("正文应被取出，实得 %q", text)
	}
	if reasoning != "" {
		t.Errorf("正文不该被当成思维链，实得 %q", reasoning)
	}
}

// textDelta 是既有隔离判定的入口，语义必须原封不动：thinking 一律 false。
func TestTextDeltaBehaviourUnchanged(t *testing.T) {
	thinking := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"我先想想"}}`)
	if got, ok := textDelta(thinking); ok || got != "" {
		t.Fatalf("textDelta 对 thinking_delta 必须返回 (\"\", false)，实得 (%q, %v)", got, ok)
	}
	normal := json.RawMessage(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`)
	if got, ok := textDelta(normal); !ok || got != "你好" {
		t.Fatalf("textDelta 对 text_delta 必须返回 (\"你好\", true)，实得 (%q, %v)", got, ok)
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/claudecode/ -run "TestSplitDelta|TestTextDeltaBehaviour" -v`
Expected: `TestSplitDeltaSeparatesThinkingFromText` FAIL（`undefined: splitDelta`）；`TestTextDeltaBehaviour` PASS（既有行为，本用例是防回归的护栏）

- [x] **Step 3: 在 `stream.go` 加 `splitDelta`，`textDelta` 保持不变**

在 `stream.go` 的 `textDelta` 函数**之后**追加（不要改 `textDelta` 本身）：

```go
// splitDelta 从 stream_event 里同时取出正文增量与思维链增量。
//
// 返回：
//   - text:      delta.type == "text_delta" 时的正文，否则空串
//   - reasoning: delta.type == "thinking_delta" 时的思维链，否则空串
//
// 两者互斥，至多一个非空。
//
// 为什么另开一个函数而不是改 textDelta：textDelta 是既有隔离判定的入口
// （mapStreamEvent 靠它把思维链挡在 render.log 与回合正文之外）。改它的返回
// 语义等于动隔离，而本期的纪律是隔离一行不许放松——多一个函数是最便宜的守法方式。
func splitDelta(ev json.RawMessage) (text, reasoning string) {
	var e struct {
		Type  string `json:"type"`
		Delta struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(ev, &e); err != nil {
		return "", ""
	}
	if e.Type != "content_block_delta" {
		return "", ""
	}
	switch e.Delta.Type {
	case "text_delta":
		return e.Delta.Text, ""
	case "thinking_delta":
		return "", e.Delta.Thinking
	}
	return "", ""
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/claudecode/ -run "TestSplitDelta|TestTextDeltaBehaviour" -v`
Expected: 两个都 PASS

- [x] **Step 5: 给 `runState` 加 frames 字段并在 `newRun` 构造**

在 `runState` 结构体里 `renderPath string` 那一行**之后**加：

```go
	frames       *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
```

在 `newRun` 里 `renderPath: filepath.Join(taskDir, renderFileName),` 之后，函数 return 之前加：

```go
	// 帧写入器构造失败不该挡住任务：可见性是增强能力。持 nil 继续，
	// FrameWriter 的方法对 nil 接收者是空操作，调用点不必判空。
	fw, err := turn.NewFrameWriter(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
```

（若 `newRun` 现在是单个复合字面量 `return &runState{...}` 的形式，改成先赋给局部变量 `r`，再补上面两句，最后 `return r`。）

- [x] **Step 6: 在 `mapStreamEvent` 里多写一路帧**

把 `mapStreamEvent` 整个替换为：

```go
// mapStreamEvent 处理流式增量：text_delta 追加 render.log（实况流式来源），
// 不产生 AdapterEvent（spec §4.2）；thinking_delta 被 textDelta 过滤掉，
// 但会单独落一条 reasoning 帧（W4a：隔离从「丢弃」改成「分流」）。
//
// 隔离不变式：thinking 内容只走 r.frames，绝不进 render.log、绝不进 turnBuf，
// 因此绝不喂 turn.ParseTrailer、绝不进权限闸。
func (a *Adapter) mapStreamEvent(r *runState, ev json.RawMessage) {
	text, reasoning := splitDelta(ev)
	if reasoning != "" {
		if err := r.frames.Reasoning(r.textPart, reasoning); err != nil {
			a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
		return
	}
	if text == "" {
		return
	}
	if err := turn.AppendRender(r.renderPath, text); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
	if err := r.frames.Text(r.textPart, text); err != nil {
		a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
}
```

在 `runState` 里再加一个字段（紧跟 `frames`）：

```go
	textPart     string // 本回合正文/思维链的 part 标识，BeginTurn 后由 NextPart 分配
```

- [x] **Step 7: 在 `appendActionSummary` 与 `mapUserMessage` 里带上 tool id**

`appendActionSummary` 现在的签名是 `(r *runState, toolName string, input json.RawMessage)`。改成带 id：

```go
// appendActionSummary 往 render.log 追加一行工具动作摘要（render 流的旁观内容），
// 并落一条 tool_call 帧。
//
// toolUseID 用作帧的 part：user 消息里的 tool_result 带同值的 tool_use_id，
// 两条帧因此天然配对，不需要本地再维护一张映射表。
func (a *Adapter) appendActionSummary(r *runState, toolUseID, toolName string, input json.RawMessage) {
	line := "→ " + toolName
	if toolName == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) == nil && in.Command != "" {
			line += ": " + firstLine(in.Command)
		}
	} else {
		line += ": " + compactJSON(input)
	}
	if err := turn.AppendRender(r.renderPath, "\n"+line+"\n"); err != nil {
		a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
	}
	// 帧里存**完整入参**（只受头尾截断约束），不是 render.log 的行摘要——
	// 行摘要的 firstLine 会切掉多行命令的后续行，那正是审核者要看的
	if err := r.frames.ToolCall(toolUseID, toolName, string(input)); err != nil {
		a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
}
```

在 `mapAssistant` 里，`Content` 的匿名结构体加一个 `ID` 字段，并把调用改成传 id：

```go
	var m struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
```

```go
		case "tool_use":
			a.appendActionSummary(r, block.ID, block.Name, block.Input)
```

`mapUserMessage` 的匿名结构体加 `ToolUseID` 与 `IsError`，并在写 render.log 之后落 tool_result 帧：

```go
// mapUserMessage 处理 user 消息：tool_result 块往 render.log 追加结果摘要，
// 并落一条 tool_result 帧（part 取 tool_use_id，与 tool_call 帧配对）。
func (a *Adapter) mapUserMessage(r *runState, msg json.RawMessage) {
	var m struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		a.log.Debug("user 消息解析失败，跳过", "task", r.taskID, "cause", err)
		return
	}
	for _, block := range m.Content {
		if block.Type != "tool_result" {
			continue
		}
		summary := compactJSON(block.Content)
		if s, ok := jsonString(block.Content); ok {
			summary = firstLine(s)
		}
		line := "↩ " + turn.TruncateRunes(summary, 200)
		if err := turn.AppendRender(r.renderPath, "\n"+line+"\n"); err != nil {
			a.log.Warn("追加 render.log 失败", "task", r.taskID, "cause", err)
		}
		// 帧里存完整结果（只受头尾截断约束），不是 render.log 的 200 字行摘要
		full := compactJSON(block.Content)
		if s, ok := jsonString(block.Content); ok {
			full = s
		}
		status := "ok"
		if block.IsError {
			status = "error"
		}
		if err := r.frames.ToolResult(block.ToolUseID, status, full); err != nil {
			a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
	}
}
```

- [x] **Step 8: 在 `Start` 与 `Send` 里各开一个回合**

在 `Start` 中 `r := a.newRun(req.Task.ID, req.TaskDir, req.Task.Workdir())` 之后加：

```go
	if err := r.frames.BeginTurn("dispatch"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

在 `Send` 方法里，取到该任务的 `runState`（沿用该方法现有的取法）之后、真正把文本发给 executor 之前加：

```go
	if err := r.frames.BeginTurn("send"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

- [x] **Step 9: 跑全包测试，确认 render.log 零回归**

```bash
go build ./... && go test ./internal/executor/claudecode/ -v 2>&1 | tail -30
```

Expected: 全部 PASS。**任何一条既有用例变红都意味着 render.log 或回合正文的行为被改动了**——那是 Global Constraints 的红线，回到 Step 5 检查，不要改测试去迁就实现。

其中 Task 4 录的 `TestRenderGolden` 是本任务最硬的那道闸，单独再看一眼：

```bash
go test ./internal/executor/claudecode/ -run TestRenderGolden -v
```

Expected: PASS。它变红 = render.log 产物变了。**绝对不要用 `-update` 把它重录过去**——那正是这道闸要拦的事。回去改实现。

- [x] **Step 10: 加关键节点日志自检**

Step 5-8 的代码里每一处写帧都带了错误分支的 Warn，逐项确认：

- `newRun` 构造帧写入器失败：Warn，带 `task` + `cause`，且**不中断**任务
- 每一处 `r.frames.Xxx(...)` 的返回值都被检查并 Warn，带 `task` + `cause`
- **没有新增任何成功路径的高频日志**：`mapStreamEvent` 每个 delta 都会走一遍，在那里打 Info 会把 agentd.log 刷爆。回合级的 Info 由 `FrameWriter.BeginTurn` 打，adapter 里不要重复
- **帧内容绝不进日志**：确认没有任何一行日志把 `text` / `reasoning` / `input` / `block.Content` 当实参

```bash
grep -n 'a\.log\.' internal/executor/claudecode/adapter.go | grep -i 'text\|reasoning\|input\|content\|delta'
```

Expected: 无输出（有输出说明帧内容或正文进了日志）

- [x] **Step 11: 加注释自检**

确认：`splitDelta` 有「为什么另开一个函数」的 why 注释；`mapStreamEvent` 的注释写明隔离不变式；`appendActionSummary` 说明了「帧存完整入参而非行摘要」的原因；`runState` 两个新字段都有行内说明。

- [x] **Step 12: Commit**

```bash
git add internal/executor/claudecode/
git commit -m "feat(claude): 回合帧分流——thinking 落 reasoning 帧，tool_use/tool_result 按 id 配对"
```

---

## Task 6: opencode adapter 分流出帧

**Files:**
- Modify: `internal/executor/opencode/adapter.go`（`runState` ~179 行、`newRun` ~254 行、`flushPending` ~1456-1470 行、`mapPartDelta` 的 `default:` 分支 ~1519-1521 行、`setPartText` ~1724-1750 行、`Start` 与 `Send`）
- Test: `internal/executor/opencode/frames_test.go`（新建）

**Interfaces:**
- Consumes: `turn.NewFrameWriter`、`(*turn.FrameWriter).BeginTurn/Text/Reasoning`（Task 3）
- Produces: 无

**背景：**
opencode 的 part 类型由 `message.part.updated` 揭晓并记进 `r.partTypes[key]`（值为 `"text"` / `"reasoning"` / `"tool"` / `"step-start"`）。`mapPartDelta` 里有三个分支：

- `case "text"` — 落地进回合与 render.log（**不动**）
- `case ""` — 类型未知，暂存进 `pendingDelta`（**不动**）
- `default:` — 已知非 text（reasoning/tool），今天是**空分支直接丢弃**

本任务只在 `default:` 分支与 `flushPending` 的丢弃路径上多写一路 reasoning 帧。`partKey(messageID, partID)` 天然就是稳定的 part 标识，**直接拿来当帧的 part，不要用 `NextPart()`**。

- [x] **Step 1: 写失败的测试**

Create `internal/executor/opencode/frames_test.go`:

```go
package opencode

import (
	"strings"
	"testing"
)

// reasoning part 的增量必须被认成思维链，且 part 标识沿用 opencode 自己的
// messageID:partID——它跨事件稳定，比本地自增的 p01 更能对齐上游。
func TestReasoningPartRoutedAsReasoning(t *testing.T) {
	key := partKey("msg_1", "prt_9")
	if !strings.Contains(key, "msg_1") || !strings.Contains(key, "prt_9") {
		t.Fatalf("partKey 应同时含 messageID 与 partID，实得 %q", key)
	}
	if got := frameKind("reasoning"); got != kindReasoning {
		t.Errorf("reasoning part 应归为思维链，实得 %v", got)
	}
	if got := frameKind("tool"); got != kindSkip {
		t.Errorf("tool part 的文本增量不产帧，实得 %v", got)
	}
	if got := frameKind("step-start"); got != kindSkip {
		t.Errorf("step-start 不产帧，实得 %v", got)
	}
	if got := frameKind("text"); got != kindText {
		t.Errorf("text part 应归为正文，实得 %v", got)
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestReasoningPartRouted -v`
Expected: FAIL，`undefined: frameKind`

- [x] **Step 3: 加 `frameKind` 分类函数**

在 `adapter.go` 里 `flushPending` 函数**之前**加：

```go
// partFrameKind 是 part 类型到帧类型的归类结果。
type partFrameKind int

const (
	kindSkip      partFrameKind = iota // 不产帧
	kindText                           // 产 text 帧
	kindReasoning                      // 产 reasoning 帧
)

// frameKind 把 opencode 的 part 类型归类成帧类型。
//
// 为什么 tool 归到 kindSkip：tool part 的**文本增量**是工具入参的流式拼装，
// 不是给人读的内容；工具调用本身由 mapToolPart 以完整的 tool_call 帧上报，
// 在这里再产一路会出现同一次调用两种形态。
//
// 未知类型一律 kindSkip：opencode 上游加了新 part 类型时，宁可少一种帧，
// 也不要把它猜成正文——猜错就是把非正文内容当模型输出展示。
func frameKind(partType string) partFrameKind {
	switch partType {
	case "text":
		return kindText
	case "reasoning":
		return kindReasoning
	default:
		return kindSkip
	}
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestReasoningPartRouted -v`
Expected: PASS

- [x] **Step 5: 给 `runState` 加 frames 字段并在 `newRun` 构造**

在 `runState` 里 `renderPath  string` 之后加：

```go
	frames      *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
```

在 `newRun` 的 `renderPath: filepath.Join(taskDir, renderLogFileName),` 之后、return 之前加：

```go
	// 构造失败不挡任务：FrameWriter 的方法对 nil 接收者是空操作
	fw, err := turn.NewFrameWriter(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
```

- [x] **Step 6: 在 `mapPartDelta` 的 `default:` 分支落 reasoning 帧**

把该分支替换为：

```go
	default:
		// 已知非 text part（reasoning/tool）的增量：不累积、不进 render.log
		// （隔离一行不改）。W4a 在此**额外**分流出一路结构化帧：
		// reasoning 落 reasoning 帧，tool 与未知类型不产帧（见 frameKind）
		if frameKind(r.partTypes[key]) == kindReasoning {
			if err := r.frames.Reasoning(key, pd.Delta); err != nil {
				a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
		}
	}
```

- [x] **Step 7: 在 `flushPending` 的丢弃路径落 reasoning 帧**

`flushPending` 现在的签名是 `(r *runState, key string, isText bool)`。类型揭晓时它只拿到一个 bool，分不出 reasoning 与 tool——改成接收真实类型：

```go
// flushPending 在 part 类型揭晓时处置它的暂存增量：是文本就落地进回合，
// 不是就整段丢弃（reasoning/tool 的增量绝不能进回合与 render.log）。
//
// W4a：丢弃前先按真实类型分流出一路帧——reasoning 的暂存增量落 reasoning 帧，
// 其余照旧整段丢弃。回合与 render.log 的走向一字不改。
//
// 为什么参数从 isText bool 换成 partType string：bool 分不出 reasoning 与
// tool，而这两者的帧处置不同（前者产帧、后者不产）。
func (a *Adapter) flushPending(r *runState, key, partType string) {
	buf, ok := r.pendingDelta[key]
	if !ok {
		return
	}
	delete(r.pendingDelta, key)
	r.pendingBytes -= len(buf)
	switch frameKind(partType) {
	case kindText:
		a.setPartText(r, key, r.partSeen[key]+buf)
		return
	case kindReasoning:
		if err := r.frames.Reasoning(key, buf); err != nil {
			a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
	}
	a.log.Debug("暂存增量所属 part 非文本，不进回合", "task", r.taskID,
		"type", partType, "bytes", len(buf))
}
```

调用点在 `mapPartUpdated` 里，改成传类型。注意 **`isText` 的既有语义带 `!r.userMsgs[p.MessageID]` 这一项，不能丢**——user 消息里的 text part 不算模型正文：

```go
	// 类型揭晓：把该 part 暂存的增量按真实类型落地或丢弃（A-5）
	// 注意：user 消息里的 text part 不是模型正文，按非文本处置（沿用 isText 的既有语义）
	pendingType := p.Type
	if p.Type == "text" && r.userMsgs[p.MessageID] {
		pendingType = "user-text"
	}
	a.flushPending(r, key, pendingType)
```

- [x] **Step 8: 在 `setPartText` 里落 text 帧**

`setPartText` 已经在算「相对已见文本的增量」并写 render.log。在它写 render.log 成功的那一路后面追加：

```go
	// 与 render.log 同源同增量：帧流与实况流对同一段文本给出一致的切分
	if err := r.frames.Text(key, delta); err != nil {
		a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
```

（`delta` 是该函数里已算好的增量变量；若局部变量名不同，用它实际的名字。若该函数存在「快照被修订、非追加」的覆盖分支，那一路**不产帧**——帧流是只追加的，无法表达"改写历史"，与文件头注释里 render.log 的处置一致。）

- [x] **Step 9: 在 `Start` 与 `Send` 里各开一个回合**

`Start` 里取到 `r` 之后：

```go
	if err := r.frames.BeginTurn("dispatch"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
```

`Send` 里取到该任务的 `runState` 之后、发出提示词之前：

```go
	if err := r.frames.BeginTurn("send"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
```

opencode **不调 `NextPart()`**：它的 `partKey` 就是稳定的 part 标识。

- [x] **Step 10: 跑全包测试**

```bash
go build ./... && go test ./internal/executor/opencode/ 2>&1 | tail -30
```

Expected: 全部 PASS。特别关注两条：

- `regression_group_a_test.go` 断言回合文本与截断行为，**变红即说明隔离被动了**
- Task 4 录的 `TestRenderGolden` 断言 render.log 逐字节不变，**变红即说明 render.log 被动了**。不要用 `-update` 重录过去——那正是这道闸要拦的事

- [x] **Step 11: 加关键节点日志自检**

逐项确认：

- `newRun` 构造帧写入器失败：Warn，带 `task` + `cause`，不中断任务
- 每一处 `r.frames.Xxx(...)` 的返回值都被检查并 Warn
- `flushPending` 丢弃非文本增量时保留了原有的 Debug（带 `type` + `bytes`），**没有把 `buf` 内容打进去**
- 没有新增成功路径的高频日志（`mapPartDelta` 每个增量走一遍）

```bash
grep -n 'a\.log\.' internal/executor/opencode/adapter.go | grep -i 'delta\|buf\|text\|content'
```

Expected: 只允许出现打**长度**（`len(buf)`）的那一条，不得有打内容的

- [x] **Step 12: 加注释自检**

确认：`frameKind` 有「为什么 tool 归 kindSkip」与「未知类型为什么不猜」的 why；`flushPending` 说明了「为什么参数从 bool 换成 string」；`mapPartUpdated` 的调用点说明了 `user-text` 这个哨兵值的由来；`default:` 分支保留了原注释并补上 W4a 的增量说明。

- [x] **Step 13: Commit**

```bash
git add internal/executor/opencode/
git commit -m "feat(opencode): 回合帧分流——reasoning part 落帧，partKey 直接做 part 标识"
```

---

## Task 7: codex adapter 分流出帧

**Files:**
- Modify: `internal/executor/codex/adapter.go`（`runState`、`newRun`、`OnNotify` 的 `deltaNotifications` 分支 ~680-683 行与 item 分支 ~684-700 行、`Start` 与 `Send`）
- Test: `internal/executor/codex/frames_test.go`（新建）

**Interfaces:**
- Consumes: `turn.NewFrameWriter`、`(*turn.FrameWriter).BeginTurn/Text/Reasoning/ToolCall/ToolResult/NextPart`（Task 3）
- Produces: 无

**背景（与前两个 adapter 不同，务必读）：**
codex **今天就把思维链写进 render.log**——`item/reasoning/textDelta` 与 `item/reasoning/summaryTextDelta` 都在 `deltaNotifications` 里直喂 `appendRenderDelta`，`renderLine` 还给 reasoning item 渲染 `【推理】`。

**这是既有行为，本任务一字不改。** 帧是**额外**的一路。回合正文的隔离在别处：`appendBody` 只在 `it.Type == "agentMessage"` 时调用——那条判定同样一字不改。

- [x] **Step 1: 写失败的测试**

Create `internal/executor/codex/frames_test.go`:

```go
package codex

import "testing"

// 通知方法名到帧类型的归类：reasoning 的两种 delta 都算思维链，
// agentMessage 的 delta 算正文，命令输出的 delta 不产帧（它属于 tool_result）。
func TestDeltaFrameKind(t *testing.T) {
	cases := map[string]deltaKind{
		"item/agentMessage/delta":           deltaKindText,
		"item/reasoning/textDelta":          deltaKindReasoning,
		"item/reasoning/summaryTextDelta":   deltaKindReasoning,
		"item/commandExecution/outputDelta": deltaKindNone,
		"item/somethingNew/delta":           deltaKindNone,
	}
	for method, want := range cases {
		if got := deltaFrameKind(method); got != want {
			t.Errorf("%s 应归为 %v，实得 %v", method, want, got)
		}
	}
}

// 既有不变式：deltaNotifications 的成员资格不能变——它决定哪些通知
// 只喂 render.log 而不产 handoff 事件，改动会把事件库刷爆。
func TestDeltaNotificationsMembershipUnchanged(t *testing.T) {
	want := []string{
		"item/agentMessage/delta",
		"item/reasoning/textDelta",
		"item/reasoning/summaryTextDelta",
		"item/commandExecution/outputDelta",
	}
	if len(deltaNotifications) != len(want) {
		t.Fatalf("deltaNotifications 数量应为 %d，实得 %d", len(want), len(deltaNotifications))
	}
	for _, m := range want {
		if !deltaNotifications[m] {
			t.Errorf("%s 应在 deltaNotifications 里", m)
		}
	}
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/codex/ -run "TestDeltaFrameKind|TestDeltaNotifications" -v`
Expected: `TestDeltaFrameKind` FAIL（`undefined: deltaFrameKind`）；`TestDeltaNotificationsMembershipUnchanged` PASS

- [x] **Step 3: 加 `deltaFrameKind`**

在 `adapter.go` 的 `deltaNotifications` 变量声明**之后**加：

```go
// deltaKind 是增量通知的帧归类。
//
// 常量带 Kind 中缀是被迫的：本包已有一个 deltaText **函数**
// （adapter.go:727，从 params 里取增量文本），Go 不允许同名。
type deltaKind int

const (
	deltaKindNone      deltaKind = iota // 不产帧
	deltaKindText                       // 产 text 帧
	deltaKindReasoning                  // 产 reasoning 帧
)

// deltaFrameKind 把增量通知的方法名归类成帧类型。
//
// 为什么 commandExecution/outputDelta 归 deltaNone：它是命令的流式输出，
// 属于工具结果；完整结果由 commandExecution item 的 completed 通知以一条
// tool_result 帧上报，在这里再产一路会把同一段输出写两遍。
//
// 未知方法一律 deltaNone：codex 上游加了新的 delta 通知时，宁可少一种帧，
// 也不要把它猜成正文。
func deltaFrameKind(method string) deltaKind {
	switch method {
	case "item/agentMessage/delta":
		return deltaKindText
	case "item/reasoning/textDelta", "item/reasoning/summaryTextDelta":
		return deltaKindReasoning
	default:
		return deltaKindNone
	}
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/codex/ -run "TestDeltaFrameKind|TestDeltaNotifications" -v`
Expected: 两个都 PASS

- [x] **Step 5: 给 `runState` 加 frames 与 part 字段，并在 `newRun` 构造**

`runState` 加两个字段：

```go
	frames   *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	textPart string            // 本回合正文/思维链的 part 标识
```

`newRun`（或 codex 里等价的 runState 构造处）加：

```go
	// 构造失败不挡任务：FrameWriter 的方法对 nil 接收者是空操作
	fw, err := turn.NewFrameWriter(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
```

- [x] **Step 6: 在 `OnNotify` 的两个分支里多写一路帧**

`deltaNotifications` 分支改为（**`appendRenderDelta` 那一行原样保留**）：

```go
	case deltaNotifications[method]:
		text := deltaText(params) // 既有函数：从 params 取增量文本
		r.appendRenderDelta(text) // 既有行为：一字不改（codex 的 render.log 本来就含思维链）
		switch deltaFrameKind(method) {
		case deltaKindText:
			if err := r.frames.Text(r.textPart, text); err != nil {
				a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
		case deltaKindReasoning:
			if err := r.frames.Reasoning(r.textPart, text); err != nil {
				a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
			}
		}
		return
```

item 分支在 `r.appendRenderDelta(it.renderLine())` **之后**加：

```go
		// 工具类 item 落一条 tool_call / tool_result 帧。part 取 item id：
		// started 与 completed 两条通知带同一个 id，帧因此天然配对
		a.appendItemFrame(r, method, it)
```

并在文件末尾加：

```go
// appendItemFrame 把 item 通知落成 tool_call / tool_result 帧。
//
// 归类：
//   - commandExecution / fileChange 的 started → tool_call
//   - 同类 item 的 completed → tool_result（status 由 ExitCode 判定）
//   - agentMessage / reasoning 不在此处产帧（它们走 delta 通知那一路）
//
// 为什么 part 取 it.ID：started 与 completed 是同一个 item 的两次通知，
// id 相同，前端据此把结果挂回调用卡片，不需要本地维护映射表。
func (a *Adapter) appendItemFrame(r *runState, method string, it *threadItem) {
	if it.Type != "commandExecution" && it.Type != "fileChange" {
		return
	}
	if method == ntfItemStarted {
		input := it.Command
		if it.Type == "fileChange" {
			input = it.renderLine() // 文件变更没有命令串，用路径清单当入参
		}
		if err := r.frames.ToolCall(it.ID, it.Type, input); err != nil {
			a.log.Warn("写 tool_call 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
		return
	}
	status := "ok"
	if it.ExitCode != nil && *it.ExitCode != 0 {
		status = "error"
	}
	if err := r.frames.ToolResult(it.ID, status, it.renderLine()); err != nil {
		a.log.Warn("写 tool_result 帧失败，不影响回合", "task", r.taskID, "cause", err)
	}
}
```

（若 `threadItem` 没有 `ID` 字段，用 `parseItemNotification` 已经取出的那个 id 字段名——`items.go` 里 `itemIndex.get(id)` 按 id 查，说明该字段存在，用它实际的名字。）

- [x] **Step 7: 在 `Start` 与 `Send` 里各开一个回合**

`Start` 里取到 `r` 之后：

```go
	if err := r.frames.BeginTurn("dispatch"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

`Send` 里对应位置：

```go
	if err := r.frames.BeginTurn("send"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

- [x] **Step 8: 跑全包测试**

```bash
go build ./... && go test ./internal/executor/codex/ 2>&1 | tail -30
```

Expected: 全部 PASS。既有用例变红 = render.log 或回合正文被动了，回到 Step 6 检查。

**codex 没有 golden 基线**（Task 4 说明了原因：仓库里没有 codex 的真实抓包，伪造一份比没有更糟）。所以这里的零回归只靠既有用例 + Task 12 Step 6 的 `git diff` 检查兜底，比另外三家弱。改 `OnNotify` 时格外小心 `r.appendRenderDelta(text)` 那一行——**它一个字都不能动**。

- [x] **Step 9: 加关键节点日志自检**

逐项确认：

- `newRun` 构造帧写入器失败：Warn，带 `task` + `cause`，不中断任务
- 每一处 `r.frames.Xxx(...)` 的返回值都被检查并 Warn
- 没有新增成功路径的高频日志（`OnNotify` 的 delta 分支每个增量走一遍）
- **帧内容绝不进日志**：codex 的 `it.renderLine()` 会带上命令原文与文件路径，它只能进帧、不能进日志

```bash
grep -n 'a\.log\.\|r\.log\.' internal/executor/codex/adapter.go | grep -i 'text\|renderLine\|command\|delta'
```

Expected: 无输出

- [x] **Step 10: 加注释自检**

确认：`deltaFrameKind` 有「为什么 outputDelta 不产帧」与「未知方法不猜」的 why；`OnNotify` 的 `appendRenderDelta` 那一行带「既有行为：一字不改」的说明；`appendItemFrame` 说明了「为什么 part 取 it.ID」。

- [x] **Step 11: Commit**

```bash
git add internal/executor/codex/
git commit -m "feat(codex): 回合帧分流——reasoning delta 落帧，command/fileChange item 按 id 配对"
```

---

## Task 8: grok adapter 分流出帧

**Files:**
- Modify: `internal/executor/grok/adapter.go`（`runState`、`newRun`、`turnAccumulator.feedRaw` 的 `agent_thought_chunk` 与 `agent_message_chunk` 分支 ~600-610 行、`Start` 与 `Send`）
- Test: `internal/executor/grok/frames_test.go`（新建）

**Interfaces:**
- Consumes: `turn.NewFrameWriter`、`(*turn.FrameWriter).BeginTurn/Text/Reasoning/NextPart`（Task 3）
- Produces: 无

**背景：**
grok 的 `turnAccumulator` 已经把 session/update 分成两股：`bodyBuf` 只收 `agent_message_chunk`（回合正文，喂 `ParseTrailer`），`renderBuf` 收「正文 + 推理 + 工具动作」。**思维链今天进 render.log，这是既有行为，不改。**

`turnAccumulator` 是个纯累积器，不持有 adapter 也不持有 FrameWriter。**不要把 FrameWriter 塞进它**——那会让一个纯数据结构带上 I/O。改在 `feedRaw` 的调用方分流。

- [x] **Step 1: 写失败的测试**

Create `internal/executor/grok/frames_test.go`:

```go
package grok

import "testing"

// sessionUpdate 类型到帧类型的归类。
func TestUpdateFrameKind(t *testing.T) {
	cases := map[string]updateKind{
		"agent_message_chunk": updateText,
		"agent_thought_chunk": updateReasoning,
		"tool_call":           updateNone,
		"tool_call_update":    updateNone,
		"something_new":       updateNone,
	}
	for u, want := range cases {
		if got := updateFrameKind(u); got != want {
			t.Errorf("%s 应归为 %v，实得 %v", u, want, got)
		}
	}
}

// 既有不变式：思维链绝不进 bodyBuf（bodyBuf 是 ParseTrailer 的输入）。
func TestThoughtNeverEntersTurnText(t *testing.T) {
	acc := newTurnAccumulator()
	acc.feedRaw([]byte(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"我打算输出 {\"ask\":\"x\"}"}}`))
	if got := acc.turnText(); got != "" {
		t.Fatalf("思维链绝不能进回合正文，实得 %q", got)
	}
	// 但它照旧进 render 那一股（grok 的既有行为）
	if got := acc.takeRender(); got == "" {
		t.Fatal("思维链应照旧进 render 股（既有行为不该被改动）")
	}
}
```

（`feedRaw` 的实参形态以该方法的真实签名为准：若它接的是已解析的结构体而非裸字节，按真实签名构造等价输入，断言不变。）

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run "TestUpdateFrameKind|TestThoughtNever" -v`
Expected: `TestUpdateFrameKind` FAIL（`undefined: updateFrameKind`）；`TestThoughtNeverEntersTurnText` PASS

- [x] **Step 3: 加 `updateFrameKind`**

在 `adapter.go` 的 `turnAccumulator` 类型声明**之前**加：

```go
// updateKind 是 ACP sessionUpdate 的帧归类。
type updateKind int

const (
	updateNone      updateKind = iota // 不产帧
	updateText                        // 产 text 帧
	updateReasoning                   // 产 reasoning 帧
)

// updateFrameKind 把 ACP 的 sessionUpdate 类型归类成帧类型。
//
// 为什么 tool_call / tool_call_update 归 updateNone：grok 的工具动作今天只有
// 一行人读摘要（toolLine，带 200 截断），拿它当 tool_call 帧的 input 会把
// 「命令尾部可能藏着危险片段」这个已知问题（见 adapter.go 的 toolLine 注释）
// 复制进帧流。W4a 不为 grok 造工具帧——诚实缺席好过失真在场（spec §3.5）。
//
// 未知类型一律 updateNone。
func updateFrameKind(sessionUpdate string) updateKind {
	switch sessionUpdate {
	case "agent_message_chunk":
		return updateText
	case "agent_thought_chunk":
		return updateReasoning
	default:
		return updateNone
	}
}
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run "TestUpdateFrameKind|TestThoughtNever" -v`
Expected: 两个都 PASS

- [x] **Step 5: 给 `runState` 加 frames 与 part 字段并构造**

```go
	frames   *turn.FrameWriter // 结构化回合帧；构造失败时为 nil，方法对 nil 安全
	textPart string            // 本回合正文/思维链的 part 标识
```

`newRun` 里加：

```go
	// 构造失败不挡任务：FrameWriter 的方法对 nil 接收者是空操作
	fw, err := turn.NewFrameWriter(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
```

- [x] **Step 6: 在 `feedRaw` 的调用方分流出帧**

找到 `feedRaw` 的调用点（adapter 处理 session/update 通知的那一处）。在调用 `feedRaw` **之后**加：

```go
	// W4a：turnAccumulator 是纯累积器，不该带 I/O——帧在它的调用方分流。
	// bodyBuf / renderBuf 的两股走向一字不改，这里只是多一路输出
	switch updateFrameKind(su.SessionUpdate) {
	case updateText:
		if err := r.frames.Text(r.textPart, su.Content.Text); err != nil {
			a.log.Warn("写 text 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
	case updateReasoning:
		if err := r.frames.Reasoning(r.textPart, su.Content.Text); err != nil {
			a.log.Warn("写 reasoning 帧失败，不影响回合", "task", r.taskID, "cause", err)
		}
	}
```

（`su` 与 `su.Content.Text` 用调用点已有的变量与字段名；若调用点只有裸字节，先按该文件既有的解析方式取出 `sessionUpdate` 与文本，再照上面分流。）

- [x] **Step 7: 在 `Start` 与 `Send` 里各开一个回合**

`Start` 里取到 `r` 之后：

```go
	if err := r.frames.BeginTurn("dispatch"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", req.Task.ID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

`Send` 里对应位置：

```go
	if err := r.frames.BeginTurn("send"); err != nil {
		a.log.Warn("写 turn_start 帧失败，不影响回合", "task", taskID, "cause", err)
	}
	r.textPart = r.frames.NextPart()
```

- [x] **Step 8: 跑全包测试**

```bash
go build ./... && go test ./internal/executor/grok/ 2>&1 | tail -30
```

Expected: 全部 PASS。其中两条是本任务的硬闸：

- `TestMapUpdateRoutesByKind` 断言思维链不进回合正文（隔离）
- Task 4 录的 `TestRenderGolden` 断言 render 产物逐字节不变

后者变红时**不要 `-update` 重录**。grok 的 render 里本来就有思维链，那是既有行为——你要做的是让帧走另一路，不是把这一路改干净。

- [x] **Step 9: 加关键节点日志自检**

逐项确认：

- `newRun` 构造帧写入器失败：Warn，带 `task` + `cause`，不中断任务
- 每一处 `r.frames.Xxx(...)` 的返回值都被检查并 Warn
- 没有新增成功路径的高频日志（session/update 每条走一遍）
- **帧内容绝不进日志**：`su.Content.Text` 是模型正文或思维链，只能进帧

```bash
grep -n 'a\.log\.' internal/executor/grok/adapter.go | grep -i 'text\|content\|thought'
```

Expected: 无输出

- [x] **Step 10: 加注释自检**

确认：`updateFrameKind` 有「为什么 grok 不产工具帧」的 why（并引用 spec §3.5 的诚实缺席原则）；分流点注释说明了「为什么不把 FrameWriter 塞进 turnAccumulator」。

- [x] **Step 11: Commit**

```bash
git add internal/executor/grok/
git commit -m "feat(grok): 回合帧分流——thought 落 reasoning 帧，工具帧诚实缺席"
```

---

## Task 9: 事件引用帧（store 落库钩子）

**Files:**
- Modify: `internal/store/store.go`（`Store` 结构体加字段、`AppendEvent` 尾部回调、新增 `SetEventHook`）
- Create: `internal/agentd/eventframes.go`
- Modify: `internal/agentd/server.go`（装配期注册钩子）
- Test: `internal/store/eventhook_test.go`（新建）、`internal/agentd/eventframes_test.go`（新建）

**Interfaces:**
- Consumes: `turn.NewFrameWriter`、`(*turn.FrameWriter).EventRef`（Task 3）
- Produces:
  - `func (*store.Store) SetEventHook(fn func(proto.Event))`
  - `func (*agentd.Server) registerEventFrameHook()`

**为什么用钩子而不是改 20 个调用点：** `AppendEvent` 在 agentd 里有 20 个调用点（manager.go 17、reconcile.go 2、watchdog.go 1）。逐点补一行既啰嗦，又留下「以后新增调用点忘了补」的失效模式。钩子是一个注册点覆盖全部现有与未来的调用点。

- [x] **Step 1: 写失败的测试（store 侧）**

Create `internal/store/eventhook_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func TestEventHookFiresAfterInsert(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	var got []proto.Event
	st.SetEventHook(func(e proto.Event) { got = append(got, e) })

	evt, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{"text": "跑起来了"})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("钩子应被调用 1 次，实得 %d", len(got))
	}
	if got[0].Seq != evt.Seq || got[0].Type != proto.EventTypeProgress {
		t.Errorf("钩子收到的事件应与返回值一致：%+v vs %+v", got[0], evt)
	}
}

// 钩子 panic 不能把一次事件落库拖垮：事件已经进库了，
// 让一个可见性副作用去回滚它是本末倒置。
func TestEventHookPanicDoesNotBreakAppend(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	st.SetEventHook(func(proto.Event) { panic("钩子炸了") })

	evt, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{"text": "x"})
	if err != nil {
		t.Fatalf("钩子 panic 不该让 AppendEvent 失败：%v", err)
	}
	if evt.Seq == 0 {
		t.Error("事件应当已落库并带上 seq")
	}
}

// 未注册钩子时一切照旧。
func TestAppendEventWithoutHook(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()
	if _, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{}); err != nil {
		t.Fatalf("无钩子时 AppendEvent 应正常：%v", err)
	}
}
```

（`Open` 的真实签名以 `store` 包为准；若它需要更多参数，按包内既有测试的建库方式构造。）

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run TestEventHook -v`
Expected: FAIL，`st.SetEventHook undefined`

- [x] **Step 3: 在 store 里加钩子**

`Store` 结构体加字段：

```go
	// eventHook 在事件落库成功后被同步调用一次，用于派生只读副作用
	// （目前是写 frames.jsonl 的 event 引用帧）。见 SetEventHook 的边界约定。
	eventHookMu sync.RWMutex
	eventHook   func(proto.Event)
```

（若 `store.go` 尚未 import `sync`，补上。）

新增方法：

```go
// SetEventHook 注册「事件落库后」的回调。传 nil 可取消。
//
// 调用时机：INSERT 成功、proto.Event 组装完成之后，AppendEvent 返回之前，
// **同步**调用。同步是刻意的——它保证「事件入库顺序 == 钩子观察顺序」，
// 派生出的帧流才能与事件流对齐。
//
// 边界（违反会死锁或自我递归）：
//   - **钩子内不得回调本 Store 的任何方法**。只允许做不回到数据库的动作，
//     比如往文件追加一行。
//   - 钩子不得长时间阻塞：它跑在 AppendEvent 的调用栈上，会拖慢事件落库。
//   - 钩子 panic 由本方法内部 recover：一个可见性副作用不该让已经成功的
//     事件落库变成失败。
func (s *Store) SetEventHook(fn func(proto.Event)) {
	s.eventHookMu.Lock()
	defer s.eventHookMu.Unlock()
	s.eventHook = fn
}

// fireEventHook 调用已注册的钩子，并把 panic 收在这里。
func (s *Store) fireEventHook(e proto.Event) {
	s.eventHookMu.RLock()
	fn := s.eventHook
	s.eventHookMu.RUnlock()
	if fn == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			// 事件已经落库了，这里只能记账——把它升级成错误会让
			// 一个派生副作用回过头来否定一次成功的写入
			s.log.Error("事件钩子 panic，已忽略", "seq", e.Seq, "type", e.Type, "panic", rec)
		}
	}()
	fn(e)
}
```

（`s.log` 用 `Store` 里已有的日志字段名；若 `Store` 没有日志字段，改用 `slog.Default().Error(...)`。）

在 `AppendEvent` 的 return 之前改成：

```go
	evt := proto.Event{Seq: seq, TaskID: taskID, Type: typ,
		Payload: json.RawMessage(b), CreatedAt: parseTime(now)}
	// 同步触发钩子：保证「入库顺序 == 观察顺序」（见 SetEventHook）
	s.fireEventHook(evt)
	return evt, nil
```

- [x] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ -run "TestEventHook|TestAppendEventWithoutHook" -v`
Expected: 三个用例全 PASS

- [x] **Step 5: 写 agentd 侧的失败测试**

Create `internal/agentd/eventframes_test.go`:

```go
package agentd

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/proto"
)

func TestEventFrameHookWritesRefFrame(t *testing.T) {
	dataDir := t.TempDir()
	taskID := "3704f368-8109-4943-b6c2-97e7943f577e"
	if err := os.MkdirAll(filepath.Join(dataDir, "tasks", taskID), 0o755); err != nil {
		t.Fatalf("建任务目录: %v", err)
	}

	hook := eventFrameHook(dataDir, testLogger(t))
	hook(proto.Event{Seq: 88, TaskID: taskID, Type: proto.EventTypePermissionRequest})

	f, err := os.Open(filepath.Join(dataDir, "tasks", taskID, turn.FramesFileName))
	if err != nil {
		t.Fatalf("打开 frames.jsonl: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("应至少写出一帧")
	}
	var fr proto.Frame
	if err := json.Unmarshal(sc.Bytes(), &fr); err != nil {
		t.Fatalf("解析帧: %v", err)
	}
	if fr.Type != proto.FrameEvent {
		t.Errorf("类型应为 event，实得 %s", fr.Type)
	}
	if fr.RefSeq != 88 {
		t.Errorf("ref_seq 应为 88，实得 %d", fr.RefSeq)
	}
	if string(fr.Event) != string(proto.EventTypePermissionRequest) {
		t.Errorf("event 名应为 permission_request，实得 %q", fr.Event)
	}
}

// 任务目录不存在（事件属于一个已清理的任务）不该 panic 也不该报错——
// 钩子是尽力而为的可见性副作用。
func TestEventFrameHookToleratesMissingTaskDir(t *testing.T) {
	hook := eventFrameHook(t.TempDir(), testLogger(t))
	hook(proto.Event{Seq: 1, TaskID: "no-such-task", Type: proto.EventTypeProgress})
	// 不 panic 即通过
}
```

（`testLogger(t)` 用 `agentd` 包内既有的测试日志构造方式；若没有，用 `slog.New(slog.NewTextHandler(io.Discard, nil))`。）

- [x] **Step 6: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestEventFrameHook -v`
Expected: FAIL，`undefined: eventFrameHook`

- [x] **Step 7: 写 `internal/agentd/eventframes.go`**

```go
// eventframes.go —— 把控制面事件派生成 frames.jsonl 里的 event 引用帧。
//
// 职责：
//   - 提供一个 store.SetEventHook 用的回调：事件落库后往该任务的
//     frames.jsonl 追加一条 event 帧（只存 seq 与类型名）
//
// 边界：
//   - 只写文件，**绝不回调 store**（会自我递归 / 争锁，见 SetEventHook 的约定）
//   - 不复制 payload：payload 的真相在 events 表，复制一份就有两份会漂移的真相
//   - 尽力而为：任务目录不在、写失败，都只 Warn，绝不影响已经成功的事件落库
//
// 为什么 event 帧要存在：帧流要能表达「模型说了这句 → 请求了权限 → 继续」的
// **顺序**。事件与帧由同一进程写、走同一个 append 序，因此单流顺序即真实顺序；
// 若让前端拿事件流和帧流按时间戳归并，两条不同写入路径的时间戳会真的乱序。
package agentd

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/proto"
)

// eventFrameHook 返回一个「事件落库后写 event 引用帧」的回调。
//
// 参数：
//   - dataDir: agentd 数据目录（任务目录在 dataDir/tasks/<id> 下）
//   - log:     日志入口
//
// 注意：任务目录不存在时静默跳过——事件可能属于一个目录已被清掉的任务，
// 那不是错误，只是没有帧文件可写。
func eventFrameHook(dataDir string, log *slog.Logger) func(proto.Event) {
	return func(e proto.Event) {
		taskDir := filepath.Join(dataDir, "tasks", e.TaskID)
		if _, err := os.Stat(taskDir); err != nil {
			return // 目录不在：没有帧文件可写，不是错误
		}
		w, err := turn.NewFrameWriter(taskDir, log)
		if err != nil {
			log.Warn("事件帧：创建帧写入器失败", "task", e.TaskID, "seq", e.Seq, "cause", err)
			return
		}
		if err := w.EventRef(e.Seq, string(e.Type)); err != nil {
			log.Warn("事件帧：写入失败", "task", e.TaskID, "seq", e.Seq,
				"type", e.Type, "cause", err)
			return
		}
		log.Debug("事件帧已写入", "task", e.TaskID, "seq", e.Seq, "type", e.Type)
	}
}

// registerEventFrameHook 在装配期把事件帧钩子挂到 store 上。
//
// 为什么是一个注册点而不是改 20 个 AppendEvent 调用点：调用点散落在
// manager.go / reconcile.go / watchdog.go，逐点补一行既啰嗦，又留下
// 「以后新增调用点忘了补」的失效模式。钩子自动覆盖现有与未来的全部调用点。
func (s *Server) registerEventFrameHook() {
	s.st.SetEventHook(eventFrameHook(s.cfg.DataDir, s.log))
	s.log.Info("事件帧钩子已注册", "datadir", s.cfg.DataDir)
}
```

- [x] **Step 8: 在 Server 装配期调用**

在 `server.go` 里 `New`（或等价的 Server 构造函数）返回之前加：

```go
	// 事件落库即派生一条 event 引用帧，让帧流能表达控制面事件的时序
	s.registerEventFrameHook()
```

- [x] **Step 9: 跑测试确认通过**

```bash
go build ./... && go test ./internal/store/ ./internal/agentd/ 2>&1 | tail -20
```

Expected: 全部 PASS

- [x] **Step 10: 加关键节点日志自检**

确认：钩子注册时 Info（带 datadir）；写帧失败 Warn（带 task + seq + type + cause）；写成功 Debug（高频，不能 Info）；`fireEventHook` 的 panic 走 Error。**确认没有任何一行日志带事件 payload**——payload 里可能有命令原文与仓库路径。

- [x] **Step 11: 加注释自检**

确认：

- `eventframes.go` 文件头有职责与边界，并写明「为什么 event 帧要存在」（帧流要表达顺序，两条写入路径的时间戳归并会真的乱序）
- `eventFrameHook` 有参数说明与「任务目录不存在时静默跳过」的原因
- `registerEventFrameHook` 有「为什么是一个注册点而不是改 20 个调用点」的 why
- `SetEventHook` 的边界约定写全了三条（不得回调 Store、不得阻塞、panic 内部 recover），这是**会死锁**的约定，必须写在方法注释里而不是散在别处
- `fireEventHook` 解释了「为什么 panic 只记账不升级成错误」

- [x] **Step 12: Commit**

```bash
git add internal/store/ internal/agentd/eventframes.go internal/agentd/eventframes_test.go internal/agentd/server.go
git commit -m "feat(store,agentd): 事件落库钩子 + event 引用帧，一个注册点覆盖 20 个调用点"
```

---

## Task 10: `GET /api/tasks/{id}/frames`

**Files:**
- Create: `internal/agentd/frames_stream.go`
- Modify: `internal/agentd/server.go`（路由表注释 + `mux.HandleFunc`）
- Test: `internal/agentd/frames_stream_test.go`（新建）

**Interfaces:**
- Consumes: `turn.FramesFileName`（Task 3）；`(*Server).byTask`、`renderStartOffset`、`copyFrom`、`renderPollInterval`、`renderHeartbeat`（均已存在）
- Produces: `func (*Server) handleTaskFrames(w http.ResponseWriter, r *http.Request)`；响应头 `X-Handoff-Frames-Size`

**形态照抄 `render_stream.go`**：同样的 offset/tail/follow 语义、同样的 1s 轮询与 20s 心跳、同样的「文件不存在返回 200 空内容」。**唯一的实质差异是行边界**：ndjson 的消费方按行解析，服务端必须只在完整行边界切。

- [x] **Step 1: 写失败的测试**

Create `internal/agentd/frames_stream_test.go`:

```go
package agentd

import (
	"strings"
	"testing"
)

// 服务端只发完整行：offset 落在半行中间时，把不完整的头部丢掉，
// 从下一个完整行开始——否则客户端第一行永远解析失败。
func TestAlignToLineStart(t *testing.T) {
	buf := []byte(`{"seq":1}` + "\n" + `{"seq":2}` + "\n")
	// 从第 3 字节开始：落在第一行中间
	if got := string(alignToLineStart(buf)); got != `{"seq":2}`+"\n" {
		t.Errorf("应跳过残缺首行，实得 %q", got)
	}
	// 恰好落在行首：一字不丢
	if got := string(alignToLineStart([]byte(`{"seq":1}` + "\n"))); got != `{"seq":1}`+"\n" {
		t.Errorf("行首起点不该丢内容，实得 %q", got)
	}
}

// 尾部不完整的行不发送：等它补齐了下一轮再发。
func TestTrimIncompleteTail(t *testing.T) {
	complete, held := trimIncompleteTail([]byte(`{"seq":1}` + "\n" + `{"seq":2`))
	if string(complete) != `{"seq":1}`+"\n" {
		t.Errorf("应只发完整行，实得 %q", complete)
	}
	if held != len(`{"seq":2`) {
		t.Errorf("残缺尾部应被扣住 %d 字节，实得 %d", len(`{"seq":2`), held)
	}

	complete, held = trimIncompleteTail([]byte(`{"seq":1}` + "\n"))
	if string(complete) != `{"seq":1}`+"\n" || held != 0 {
		t.Errorf("全是完整行时不该扣住任何字节：%q held=%d", complete, held)
	}

	// 一行都没写完：一个字节都不发
	complete, held = trimIncompleteTail([]byte(`{"seq`))
	if len(complete) != 0 || held != len(`{"seq`) {
		t.Errorf("无完整行时应全部扣住：%q held=%d", complete, held)
	}
}

func TestAlignToLineStartNoNewline(t *testing.T) {
	// 整段都没有换行：没有可用的完整行起点，全丢
	if got := alignToLineStart([]byte(`{"seq":1`)); len(got) != 0 {
		t.Errorf("无换行时应返回空，实得 %q", string(got))
	}
}

func TestFramesOffsetParamsReuseRenderSemantics(t *testing.T) {
	// frames 与 render 共用 renderStartOffset：单位都是字节，
	// 优先级都是 offset > tail > 默认回溯。这里只钉住「确实复用了」
	if framesDefaultTail != renderDefaultTail {
		t.Errorf("frames 的默认回溯量应与 render 一致（%d），实得 %d",
			renderDefaultTail, framesDefaultTail)
	}
	_ = strings.TrimSpace("") // 保持 import 有用
}
```

- [x] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run "TestAlignToLineStart|TestTrimIncompleteTail|TestFramesOffset" -v`
Expected: FAIL，`undefined: alignToLineStart` 等

- [x] **Step 3: 写 `internal/agentd/frames_stream.go`**

```go
// frames_stream.go —— 结构化回合帧（frames.jsonl）的流式读取接口。
//
// 职责：
//   - 按 offset / tail 截取 frames.jsonl 并写出；follow=1 时持续追送增量
//   - **只在完整行边界切**：ndjson 的消费方按行解析，半行会让它解析失败
//   - 通过响应头告知客户端当前文件大小，供断线续传对齐
//
// 边界：
//   - 不解析帧内容：本文件只认换行符，不认 JSON 里有什么
//   - 不做轮转/清理：frames.jsonl 随任务目录走
//   - 不是事件流：控制面事件走 /ws/events，本接口服务「回合过程的完整复现」
//
// 与 render_stream.go 的关系：形态刻意照抄（同样的参数语义、轮询间隔、心跳、
// 文件不存在返回 200 空内容），唯一的实质差异是行边界对齐。两者共用
// renderStartOffset / copyFrom / renderPollInterval / renderHeartbeat，
// 避免两份会漂移的偏移语义。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xushixin/handoff/internal/executor/turn"
)

// framesDefaultTail 是不带参数时从尾部回溯的字节数。
// 与 render 保持一致：两个接口的「默认看多少」不该无缘无故不同。
const framesDefaultTail = renderDefaultTail

// handleTaskFrames 流式输出任务的 frames.jsonl。
//
// 查询参数（语义与 /render 完全一致，单位都是**字节**）：
//   - offset: 起始字节偏移；与 tail 互斥，两者都不给时按 framesDefaultTail 回溯
//   - tail:   从文件尾部回溯的字节数
//   - follow: 1 表示到达文件尾后不关闭连接，持续追送增量
//
// 响应：200 + application/x-ndjson 流；响应头 X-Handoff-Frames-Size 为响应
// 开始时的文件大小。
//
// 注意：
//   - frames.jsonl 尚不存在时返回 200 空内容而非 404——任务刚 dispatch、
//     模型还没产出第一帧是正常状态（与 /render 同一处置）
//   - 客户端断开时 r.Context() 被取消，本函数随即返回，不留 goroutine
func (s *Server) handleTaskFrames(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.taskRepoOrErr(w, taskID); !ok {
		return // taskRepoOrErr 已写 404
	}
	framesPath := filepath.Join(s.cfg.DataDir, "tasks", taskID, turn.FramesFileName)

	size := renderSize(framesPath)
	offset, err := renderStartOffset(r, size)
	if err != nil {
		s.log.Warn("frames 请求参数非法", "task", taskID, "cause", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1"

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Handoff-Frames-Size", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	s.log.Info("frames 流开始", "task", taskID, "offset", offset, "size", size, "follow", follow)
	sent, err := streamFrames(r.Context(), w, flusher, framesPath, offset, follow)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Error("frames 流中断", "task", taskID, "sent", sent, "cause", err)
		return
	}
	s.log.Info("frames 流结束", "task", taskID, "sent", sent)
}

// streamFrames 从 offset 起把 path 的内容按**完整行**写到 w；follow 为真时持续追送。
//
// 返回：已发送字节数与终止原因（客户端断开时返回 ctx.Err()）。
//
// 与 streamRender 的差异只有一处：这里维护一个「已读到但还不完整」的尾部，
// 不把它发出去，下一轮补齐后再连同后续内容一起发。
func streamFrames(ctx context.Context, w io.Writer, flusher http.Flusher,
	path string, offset int64, follow bool) (int64, error) {
	var sent int64
	first := true
	lastBeat := time.Now()
	for {
		chunk, err := readFrom(path, offset)
		if err != nil {
			return sent, err
		}
		if first && len(chunk) > 0 {
			// 首块的起点可能落在半行中间（offset 由 tail 回溯算出）：
			// 跳到下一个完整行的开头，客户端第一行才解析得动
			aligned := alignToLineStart(chunk)
			offset += int64(len(chunk) - len(aligned))
			chunk = aligned
			first = false
		}
		complete, held := trimIncompleteTail(chunk)
		if len(complete) > 0 {
			n, werr := w.Write(complete)
			sent += int64(n)
			offset += int64(n)
			if werr != nil {
				return sent, werr
			}
			lastBeat = time.Now()
			if flusher != nil {
				flusher.Flush()
			}
		}
		_ = held // 被扣住的残缺尾部不推进 offset，下一轮重读
		if !follow {
			return sent, nil
		}
		// 心跳：ndjson 里一个空行不是合法帧，但按行解析的客户端会跳过空行，
		// 用它保活比自造一种「心跳帧」干净——心跳不该混进数据模型
		if len(complete) == 0 && time.Since(lastBeat) >= renderHeartbeat {
			if _, err := w.Write([]byte("\n")); err != nil {
				return sent, err
			}
			if flusher != nil {
				flusher.Flush()
			}
			lastBeat = time.Now()
		}
		select {
		case <-ctx.Done():
			return sent, ctx.Err()
		case <-time.After(renderPollInterval):
		}
	}
}

// readFrom 读出 path 从 offset 起的全部剩余内容。
//
// 文件不存在返回 (nil, nil)：follow 模式下这是「还没开始产出」的正常状态
// （与 copyFrom 同一处置，只是这里要拿到字节而不是直接搬运）。
func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// alignToLineStart 丢掉开头那个不完整的行，返回从第一个完整行开始的切片。
//
// 整段都没有换行时返回空切片：一个完整行都没有，什么也发不了。
func alignToLineStart(b []byte) []byte {
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return nil
	}
	return b[i+1:]
}

// trimIncompleteTail 把 b 切成「完整行部分」与「被扣住的残缺尾部长度」。
//
// 为什么服务端要保证这件事，而客户端还要再缓冲一层：服务端保证是契约
// （任何客户端都能按行解析），客户端缓冲是防御（代理与中间设备可能在任意
// 字节处切包）。两层都要有。
func trimIncompleteTail(b []byte) (complete []byte, held int) {
	i := bytes.LastIndexByte(b, '\n')
	if i < 0 {
		return nil, len(b)
	}
	return b[:i+1], len(b) - (i + 1)
}
```

- [x] **Step 4: 挂路由**

在 `server.go` 的路由表注释里，`GET /api/tasks/{id}/render` 那行之后加一行：

```go
//   - GET  /api/tasks/{id}/frames      结构化回合帧（frames.jsonl）流式读取（W4b/TUI 数据源）
```

在 `mux.HandleFunc("GET /api/tasks/{id}/render", s.byTask(s.handleTaskRender))` 之后加：

```go
	mux.HandleFunc("GET /api/tasks/{id}/frames", s.byTask(s.handleTaskFrames))
```

**必须走 `byTask`**：跨机读远端任务的帧靠它转发，`forwardTo` 用 `io.Copy` 直通，天然支持流式。

- [x] **Step 5: 跑测试确认通过**

```bash
go build ./... && go test ./internal/agentd/ -run "TestAlignToLineStart|TestTrimIncompleteTail|TestFramesOffset" -v
```

Expected: 全部 PASS

- [x] **Step 6: 加一个端到端的 handler 测试**

追加到 `frames_stream_test.go`：

```go
// 文件不存在时返回 200 空内容而非 404——任务刚 dispatch 是正常状态。
func TestHandleTaskFramesMissingFileReturns200(t *testing.T) {
	srv, taskID := newTestServerWithTask(t)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/frames", nil)
	req.SetPathValue("id", taskID)
	rec := httptest.NewRecorder()

	srv.handleTaskFrames(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("无帧文件时响应体应为空，实得 %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type 应为 application/x-ndjson，实得 %q", ct)
	}
	if rec.Header().Get("X-Handoff-Frames-Size") != "0" {
		t.Errorf("空文件的 size 头应为 0，实得 %q", rec.Header().Get("X-Handoff-Frames-Size"))
	}
}

// offset 落在半行中间时，客户端收到的第一行必须是完整可解析的。
func TestHandleTaskFramesAlignsHalfLineOffset(t *testing.T) {
	srv, taskID := newTestServerWithTask(t)
	dir := filepath.Join(srv.cfg.DataDir, "tasks", taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建任务目录: %v", err)
	}
	content := `{"seq":1,"type":"text"}` + "\n" + `{"seq":2,"type":"text"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, turn.FramesFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("写帧文件: %v", err)
	}

	// offset=5 落在第一行中间
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/frames?offset=5", nil)
	req.SetPathValue("id", taskID)
	rec := httptest.NewRecorder()

	srv.handleTaskFrames(rec, req)

	got := rec.Body.String()
	if got != `{"seq":2,"type":"text"}`+"\n" {
		t.Fatalf("应从下一个完整行开始，实得 %q", got)
	}
	var fr proto.Frame
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &fr); err != nil {
		t.Fatalf("客户端应能直接解析第一行，实得解析失败: %v", err)
	}
}
```

`newTestServerWithTask(t)` 用 `agentd` 包内既有的测试 Server 构造方式（W3a 的 `w3a_testhelpers_test.go` 里有同类 helper，优先复用；没有合适的就照它的写法新建一个，返回 `*Server` 与一个已入库任务的 id）。补上 `net/http/httptest`、`encoding/json`、`os`、`path/filepath`、`strings` 等 import。

- [x] **Step 7: 跑全包测试**

```bash
go test ./internal/agentd/ 2>&1 | tail -20
```

Expected: 全部 PASS

- [x] **Step 8: 加关键节点日志自检**

确认：流开始 Info（task + offset + size + follow）；流结束 Info（task + sent）；中断且非 `context.Canceled` 时 Error（带 cause）；参数非法 Warn。**确认没有任何一行日志打印帧内容**。

- [x] **Step 9: 加注释自检**

确认：文件头写清与 `render_stream.go` 的关系与唯一差异；`streamFrames` 解释了「与 streamRender 的差异只有一处」；`trimIncompleteTail` 解释了「为什么服务端保证了客户端还要再缓冲一层」；心跳那处解释了「为什么用空行而不是自造心跳帧」。

- [x] **Step 10: Commit**

```bash
git add internal/agentd/frames_stream.go internal/agentd/frames_stream_test.go internal/agentd/server.go
git commit -m "feat(agentd): GET /api/tasks/{id}/frames——照抄 render 流形态，只在完整行边界切"
```

---

## Task 11: `handoff frames` CLI

**Files:**
- Modify: `internal/client/client.go`（新增 `FramesStream`）
- Create: `cmd/frames.go`
- Test: `cmd/frames_test.go`（新建）

**Interfaces:**
- Consumes: `(*Server).handleTaskFrames` 的线契约（Task 10）；`client.New`、`c.doStream`、`c.httpError`（已存在）
- Produces: `func (*client.Client) FramesStream(ctx context.Context, taskID string, offset, tail int64, follow bool) (io.ReadCloser, int64, error)`；`handoff frames` 子命令

- [x] **Step 1: 在 client 里加 `FramesStream`**

在 `client.go` 的 `RenderStream` 方法**之后**加：

```go
// FramesStream 打开任务的结构化回合帧（frames.jsonl）流式读取。
//
// 参数：
//   - taskID: 目标任务
//   - offset: 起始字节偏移；>0 时优先于 tail（用于断线续传）
//   - tail:   从尾部回溯的字节数（offset<=0 时生效；两者都为 0 时由服务端取默认值）
//   - follow: 是否在到达文件尾后继续等待增量
//
// 返回：
//   - 流（调用方负责 Close，每行一个 proto.Frame 的 JSON）、响应开始时的文件
//     字节数、错误
//
// 注意：
//   - 与 RenderStream 一样**不设读超时**：follow 模式下长时间无输出是正常的
//   - 服务端保证只在完整行边界切，但调用方仍应按行缓冲——中间设备可能在
//     任意字节处切包
func (c *Client) FramesStream(ctx context.Context, taskID string,
	offset, tail int64, follow bool) (io.ReadCloser, int64, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	} else if tail > 0 {
		q.Set("tail", strconv.FormatInt(tail, 10))
	}
	if follow {
		q.Set("follow", "1")
	}
	path := "/api/tasks/" + taskID + "/frames"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := c.doStream(ctx, http.MethodGet, path)
	if err != nil {
		return nil, 0, fmt.Errorf("frames 流请求: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, 0, c.httpError("frames 流", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Handoff-Frames-Size"), 10, 64)
	return resp.Body, size, nil
}
```

- [x] **Step 2: 写 CLI 的失败测试**

Create `cmd/frames_test.go`:

```go
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

// frames 每行原样输出一帧 JSON：它是 TUI 与脚本的数据源，不做人类友好格式化。
func TestFramesCmdEmitsRawJSONLines(t *testing.T) {
	body := `{"seq":1,"type":"text","delta":"你好"}` + "\n" +
		`{"seq":2,"type":"tool_call","tool":"Bash"}` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/frames") {
			t.Errorf("应请求 /frames，实得 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Handoff-Frames-Size", "999")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runFrames(t.Context(), srv.URL, "", "task-1", 0, 0, false, &out); err != nil {
		t.Fatalf("runFrames: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("应输出 2 行，实得 %d：%q", len(lines), out.String())
	}
	var fr proto.Frame
	if err := json.Unmarshal([]byte(lines[0]), &fr); err != nil {
		t.Fatalf("每行都应是可解析的帧 JSON：%v", err)
	}
	if fr.Seq != 1 || fr.Delta != "你好" {
		t.Errorf("首帧内容应原样透传，实得 %+v", fr)
	}
}

// 空行是服务端的心跳，不是帧：不能原样喷给消费方。
func TestFramesCmdSkipsHeartbeatBlankLines(t *testing.T) {
	body := "\n" + `{"seq":1,"type":"text"}` + "\n" + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runFrames(t.Context(), srv.URL, "", "task-1", 0, 0, false, &out); err != nil {
		t.Fatalf("runFrames: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != `{"seq":1,"type":"text"}` {
		t.Errorf("心跳空行应被跳过，实得 %q", out.String())
	}
}
```

- [x] **Step 3: 跑测试确认失败**

Run: `go test ./cmd/ -run TestFramesCmd -v`
Expected: FAIL，`undefined: runFrames`

- [x] **Step 4: 写 `cmd/frames.go`**

```go
// 本文件实现 handoff frames 子命令：读任务的结构化回合帧。
//
// 职责：
//   - 调 GET /api/tasks/{id}/frames，把 ndjson 流每行原样打到 stdout
//
// 边界：
//   - **不做人类友好格式化**：本命令是 handoff tui（W4e）与脚本的数据源，
//     人要看好看的有 Web 控制台。与 handoff tasks 的「一行一个 JSON」同风格
//   - 不解析帧语义：只做行搬运与心跳过滤
//   - 任务 id 是完整 UUID 精确匹配，没有前缀补全（与全部子命令一致）
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	framesFollow bool
	framesOffset int64
	framesTail   int64
)

// framesCmd 读任务的结构化回合帧。
var framesCmd = &cobra.Command{
	Use:   "frames <task>",
	Short: "读任务的结构化回合帧（每行一个 JSON 帧）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		return runFrames(cmd.Context(), addr, token, args[0],
			framesOffset, framesTail, framesFollow, cmd.OutOrStdout())
	},
}

// runFrames 打开帧流并把每一行原样写到 out。
//
// 参数：
//   - addr/token: agentd 端点与令牌（token 只进请求头，绝不进日志或输出）
//   - taskID:     完整 UUID
//   - offset/tail/follow: 与 GET /api/tasks/{id}/frames 的同名参数一致（字节）
//
// 注意：
//   - 空行是服务端的心跳保活，跳过不输出——它不是帧，喷给消费方会让
//     按行解析的脚本收到一条解析失败
//   - follow 模式下本函数直到 ctx 取消（Ctrl+C）或服务端断流才返回
func runFrames(ctx context.Context, addr, token, taskID string,
	offset, tail int64, follow bool, out io.Writer) error {
	rc, size, err := client.New(addr, token).FramesStream(ctx, taskID, offset, tail, follow)
	if err != nil {
		return err
	}
	defer rc.Close()

	sc := bufio.NewScanner(rc)
	// 单帧上限 16KB，给到 1MB 足够宽裕，同时挡住异常巨行把内存吃穿
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue // 心跳空行（见函数注释）
		}
		if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
			return fmt.Errorf("输出帧: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		// ctx 取消（Ctrl+C）走到这里是正常收尾，交给调用方按 ctx 判定
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("读帧流（文件当前 %d 字节）: %w", size, err)
	}
	return nil
}

func init() {
	framesCmd.Flags().BoolVar(&framesFollow, "follow", false, "到达文件尾后继续等待增量")
	framesCmd.Flags().Int64Var(&framesOffset, "offset", 0, "起始字节偏移（优先于 --tail）")
	framesCmd.Flags().Int64Var(&framesTail, "tail", 0, "从尾部回溯的字节数")
	rootCmd.AddCommand(framesCmd)
}
```

- [x] **Step 5: 跑测试确认通过**

```bash
go build ./... && go test ./cmd/ -run TestFramesCmd -v
```

Expected: 两个用例都 PASS

- [x] **Step 6: 跑全量测试**

```bash
go test ./internal/... ./cmd/... 2>&1 | tail -30
```

Expected: 全部 PASS

- [x] **Step 7: 加关键节点日志自检**

CLI 的「日志」就是它的输出与错误。确认：错误信息带上下文（`读帧流（文件当前 N 字节）: ...`）；`token` 不出现在任何输出或错误里；Ctrl+C 是正常收尾而不是报错。

- [x] **Step 8: 加注释自检**

确认：文件头有职责与边界（含「为什么不做人类友好格式化」）；`runFrames` 有参数说明与「为什么跳过空行」；`FramesStream` 有参数、返回与「为什么不设读超时」。

- [x] **Step 9: Commit**

```bash
git add internal/client/client.go cmd/frames.go cmd/frames_test.go
git commit -m "feat(cli): handoff frames——每行一个原始 JSON 帧，W4e 的数据源"
```

---

## Task 12: 收口自检

**Files:**
- 无新增；只做验证与必要的修补

- [x] **Step 1: 全量构建与测试**

```bash
go build ./... && go vet ./... && go test ./internal/... ./cmd/... 2>&1 | tail -40
```

Expected: 构建干净、vet 无输出、测试全绿

- [x] **Step 2: 格式检查**

```bash
gofmt -l internal cmd
```

Expected: 输出里**不得出现本计划新建或修改的任何文件**。

已知的 6 个历史遗留（继承自 main，**不属于本次改动，不要顺手改**）：
`internal/agentd/integration_test.go`、`internal/agentd/projectresolve.go`、`internal/agentd/server.go`、`internal/projectid/projectid.go`、`cmd/dispatch.go`、`cmd/project.go`。

若 `server.go` 之外还有别的文件出现，那就是本次引入的，格式化掉。

- [x] **Step 3: 竞态检测（帧写入是本期唯一的新并发点）**

```bash
go test ./internal/executor/... -race 2>&1 | tail -20
```

Expected: PASS，无 race 报告

- [x] **Step 4: 契约核对**

```bash
go test ./internal/proto/ -run TestContractFixtures -v
```

Expected: PASS（不带 `-update`）。若变红说明有人动了 `proto.Frame` 却没同步 fixture——**停下来看差异，不要直接 `-update` 盖过去**。

- [x] **Step 5: 逐条核对 Global Constraints**

```bash
# 1. 没有引入新依赖
git diff --stat main -- go.mod go.sum
# 期望：无输出

# 2. 没有碰 web/（W4a 是纯后端）
git diff --stat main -- web/ | grep -v "testdata/Frame.json"
# 期望：除 Frame.json 外无输出

# 3. 帧内容没进日志：搜可疑的日志实参
grep -rn 'log\.\(Info\|Warn\|Error\|Debug\)' internal/executor/turn/frames.go internal/agentd/eventframes.go internal/agentd/frames_stream.go | grep -i 'delta\|input\|output\|payload'
# 期望：无输出
```

- [x] **Step 6: 核对 render.log 零回归**

先跑三条黄金基线（Task 4 在改动前录的，这是最强的那道证明）：

```bash
go test ./internal/executor/claudecode/ ./internal/executor/opencode/ ./internal/executor/grok/ -run TestRenderGolden -v
```

Expected: 三条全 PASS。**任何一条曾被 `-update` 重录过，这一步就失去意义**——若你在执行过程中重录过，必须在自评里如实写明是哪一条、为什么。

codex 没有基线（Task 4 说明了原因），它只有下面这条兜底。

再确认本计划全程没有删改任何一行既有的 render 写入：

```bash
git diff main -- internal/executor/ | grep '^-' | grep 'AppendRender\|appendRenderDelta\|renderBuf\|bodyBuf'
```

Expected: **无输出**。有输出即说明 render.log 或回合正文的既有写入被删改过——那是 Global Constraints 的红线，必须回滚该处改动。

- [x] **Step 7: 手工冒烟（本机，不派发）**

```bash
go build -o /tmp/w4a-handoff . && /tmp/w4a-handoff frames --help
```

Expected: 打印用法，三个 flag（`--follow` / `--offset` / `--tail`）都在。

- [x] **Step 8: 写一份自评并提交**

在最后一个提交的消息里写清：

- 完成了哪几个 Task
- 哪些地方与本计划的写法不同、为什么（尤其是各 adapter 里"用它实际的名字"那些处，写清最终用的是什么）
- 剩下什么没做、什么地方拿不准

```bash
git add -A
git commit -m "chore(w4a): 收口自检——构建/vet/测试/race/契约/格式全过

<在此写自评：完成项、与计划的偏差及原因、遗留与不确定处>"
```

---

## 自评（写计划时的已知不确定处）

以下四处是写计划时**没有百分之百确认**的，执行者遇到时按注明的方式处理，不要硬套：

1. **各 adapter 的 `Send` 方法里 `runState` 的取法**。四个 adapter 拿 runState 的方式不完全一样（有的从 map 取、有的走 `a.runs`）。计划里统一写成"取到该任务的 runState 之后"，用该方法**实际的**取法即可。

2. **codex `threadItem` 的 id 字段名**。`items.go` 的 `itemIndex.get(id)` 按 id 查，说明字段存在，但计划里写的 `it.ID` 未必是它的真名。用真名。

3. **grok `feedRaw` 调用点的变量与字段名**。计划里写的 `su.SessionUpdate` / `su.Content.Text` 是按 ACP 的通用形状推的，用调用点实际的名字。

4. **opencode `setPartText` 里增量变量的名字**。计划里叫 `delta`，用它实际的名字；并注意该函数若有"快照被修订、非追加"的覆盖分支，**那一路不产帧**（帧流只追加，表达不了改写历史）。

5. **claude golden 回放里的三处形状**（Task 4 Step 6 已列明）：`streamMsg` 承载三类载荷的字段名、`newRun` 的真实签名、`Run` 循环里的真实分派。都能直接读出来，照读的写，别照计划里推的写。

6. **`turn_success.jsonl` 到底会不会产出 render 内容**——没有验证过。会，就有 claude 的基线；不会，按 Task 4 Step 7 的指示删掉空基线并在自评里写明，**不要交一个恒真的空基线**。

另外两处**刻意不做**，不是遗漏：

- **不给 grok 造工具帧**：grok 的工具信息今天只有一行带 200 截断的人读摘要，拿它当 `tool_call.input` 会把「命令尾部可能藏危险片段」这个已知问题复制进帧流。诚实缺席好过失真在场（spec §3.5）。
- **不做 `DELETE /api/tasks/{id}/frames` 之类的清理端点**：帧随任务目录走，`done` 不删任务目录，没有需要单独清理的场景（spec §2.2）。
