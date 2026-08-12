# 回合终结信号探针 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 15 次真实派发测出「S3（工具调用被截断）在事件层有没有可判别于 S1（模型自然语言收尾）的信号」，产出可入库回放的原始字节样本与一张逐 executor 判定表。

**Architecture:** 先建一个 env 门控的原始字节旁路 `internal/executor/rawtap`，在四个 adapter 各自唯一的上游读取点各接一行；再在专用沙箱仓库上串行跑 4 场景 × 4 executor（claudecode 无原生提问通道，S4 跳过，共 15 次）；最后把样本入库为 `testdata/*.jsonl` + 回放测试，并按 spec §3.5 的规则回填结论。

**Tech Stack:** Go 1.26 标准库（`os` / `sync` / `path/filepath`），既有的 `handoff dispatch` / `handoff show` CLI，devbox 上的 agentd。

## Global Constraints

- 依据 spec：`docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §5。
- **串行，绝不并行**（spec §5.3）。B73 的整机 fork 瘫痪就是并行 executor 顶穿 `kern.maxprocperuid`。每次派发前查 devbox 进程余量，不足则停下来报告，不硬上。
- **跑在专用沙箱仓库**，每次 `--new-branch --new-worktree`，不碰任何真实项目。
- **探针只观测，不改判定逻辑**：本 plan 不动 `fallbackClassify`、不动 `ParseTrailer`、不动任何 adapter 的分类分支。Task 1/2 加的旁路在解析之前旁写一份字节，不参与任何判断。
- **S3 诱不出来是允许的结果**，如实记「未复现」。spec §3.5 已为「未复现」规定了明确后果（不加证据层），它不是无信息。禁止为了让结论好看而改写场景或反复重试到出现想要的现象。
- 日志一律用各 adapter 已有的 `slog`，**禁止 `fmt.Printf` 充当日志**。
- 中文注释与日志；新文件必须有「职责 / 边界」头注释，导出函数必须有 doc 注释。
- 每个 task 结束时 `gofmt -l .` 无输出、`go vet ./...` 无输出。

### 关于「新增一个环境变量」

spec §4.2 写的「不加 env」约束**只针对 B75 的游标目录**（多一条优先级链就多一个可错面）。本 plan 的 `HANDOFF_RAW_TAP_DIR` 是 **agentd/executor 侧**的诊断开关，缺省关闭、不影响任何判定，两者不冲突。

spec §5.3 要求「每场景归档原始字节流」，但 grok 与 codex 的上游是**进程内的 WebSocket**，opencode 的 SSE 端口由 adapter 随机选，都无法从外部 tee。所以旁路是 spec 这条要求的必要前提，属于对 spec 的补充而非偏离——这里明确记下，供审核者裁。它同时补上了 §2.1 那次现场之所以不可复核的原因：当时没有任何东西把原始字节留下来。

---

### Task 1: `rawtap` 原始字节旁路

**Files:**
- Create: `internal/executor/rawtap/rawtap.go`
- Create: `internal/executor/rawtap/rawtap_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `func Open(executor, taskID string, log *slog.Logger) *Tap` —— 未开启时返回 nil
  - `func (t *Tap) Write(b []byte)` —— nil 接收者安全，写一行原始字节
  - `func (t *Tap) Close()` —— nil 接收者安全
  - 环境变量 `HANDOFF_RAW_TAP_DIR`

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/rawtap/rawtap_test.go`：

```go
package rawtap

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.NewFile(0, os.DevNull), nil))
}

func TestOpenReturnsNilWhenDisabled(t *testing.T) {
	t.Setenv(EnvDir, "")
	if tap := Open("opencode", "task-1", discard()); tap != nil {
		t.Fatal("未设置环境变量时必须完全关闭，返回 nil")
	}
}

func TestNilTapIsSafe(t *testing.T) {
	var tap *Tap
	tap.Write([]byte("x")) // 不得 panic
	tap.Close()
}

func TestWriteAppendsOneLinePerCall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	tap := Open("grok", "task-2", discard())
	if tap == nil {
		t.Fatal("设置了环境变量却没开启")
	}
	tap.Write([]byte(`{"a":1}`))
	tap.Write([]byte("second line\nwith embedded newline"))
	tap.Close()

	b, err := os.ReadFile(filepath.Join(dir, "grok-task-2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("应为 2 行，实际 %d 行: %q", len(lines), string(b))
	}
	if lines[0] != `{"a":1}` {
		t.Fatalf("第一行被改写: %q", lines[0])
	}
	// 内嵌换行必须被转义，否则一条上游消息会在样本里裂成两条，回放时对不上
	if strings.Contains(lines[1], "\nwith") {
		t.Fatalf("内嵌换行未转义: %q", lines[1])
	}
	if !strings.Contains(lines[1], `\n`) {
		t.Fatalf("内嵌换行应转义成 \\n: %q", lines[1])
	}
}

func TestWriteIsConcurrencySafe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	tap := Open("codex", "task-3", discard())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tap.Write([]byte("line"))
		}()
	}
	wg.Wait()
	tap.Close()

	b, err := os.ReadFile(filepath.Join(dir, "codex-task-3.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "\n"); got != 50 {
		t.Fatalf("并发写丢行或串行：应 50 行，实际 %d", got)
	}
}

func TestOpenFailureDegradesToNil(t *testing.T) {
	// 指到一个「已被文件占位」的路径：MkdirAll 必失败
	dir := t.TempDir()
	occupied := filepath.Join(dir, "occupied")
	if err := os.WriteFile(occupied, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDir, occupied)
	if tap := Open("opencode", "task-4", discard()); tap != nil {
		t.Fatal("目录不可用时必须降级为 nil，不得让诊断开关拖垮执行")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/rawtap/ -v`
Expected: 编译失败，包不存在

- [ ] **Step 3: 写实现**

创建 `internal/executor/rawtap/rawtap.go`：

```go
// Package rawtap 提供上游原始字节的旁路归档。
//
// 职责：
//   - 在 adapter 解析上游消息**之前**，把原始字节按行旁写到一个文件
//   - 由环境变量 HANDOFF_RAW_TAP_DIR 门控，缺省完全关闭（Open 返回 nil）
//
// 边界：
//   - 不解析、不过滤、不判断内容：拿到什么写什么，样本的价值就在于未经加工
//   - 不参与任何回合判定：Write 的返回值是 void，adapter 无从依赖它
//   - 不做轮转与容量上限：这是诊断开关，开着跑一次探针就关，不是常驻设施
//
// 为什么需要它：grok 与 codex 的上游是进程内 WebSocket、opencode 的 SSE 端口
// 由 adapter 随机选，三者都无法从进程外 tee。没有旁路就没有原始样本，
// 而没有原始样本的现场无法从任何一个 clone 复核——B74 的原始现场（2026-08-12）
// 正是因此永久丢失。
package rawtap

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EnvDir 是开启旁路的环境变量名。值为一个目录路径；空或未设置即完全关闭。
const EnvDir = "HANDOFF_RAW_TAP_DIR"

// Tap 是一个任务的原始字节旁路句柄。
//
// 全部方法对 nil 接收者安全：关闭状态下 Open 返回 nil，调用点因此不需要写
// 任何 if——这是「诊断开关绝不能污染主路径」的落地方式。
type Tap struct {
	mu sync.Mutex
	f  *os.File
}

// Open 按环境变量决定是否为某任务开启旁路。
//
// 参数：
//   - executor: 执行者名（opencode/claudecode/grok/codex），作文件名前缀
//   - taskID: 任务 ID，作文件名后缀
//   - log: 用于报告开启与降级
//
// 返回：开启则返回句柄；未开启或开启失败返回 nil（调用方无需区分）
//
// 注意：开启失败不是错误——诊断开关拖垮执行是本末倒置，一律降级为关闭并告警。
func Open(executor, taskID string, log *slog.Logger) *Tap {
	dir := strings.TrimSpace(os.Getenv(EnvDir))
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("原始字节旁路目录不可用，本次不归档", "dir", dir, "cause", err)
		return nil
	}
	p := filepath.Join(dir, fmt.Sprintf("%s-%s.jsonl", executor, taskID))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Warn("原始字节旁路文件打不开，本次不归档", "path", p, "cause", err)
		return nil
	}
	log.Info("原始字节旁路已开启", "executor", executor, "task", taskID, "path", p)
	return &Tap{f: f}
}

// Write 旁写一条上游原始消息，一次调用一行。
//
// 注意：
//   - 内嵌换行会被转义成 \n。上游消息（尤其是被截断的超长工具调用）内部可能
//     带裸换行，不转义的话一条消息在样本里会裂成多条，回放时与真实分帧对不上
//   - 写失败只记一次日志后静默：旁路是观测，观测失败不能影响被观测的东西
func (t *Tap) Write(b []byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f == nil {
		return
	}
	esc := strings.NewReplacer("\\", `\\`, "\n", `\n`, "\r", `\r`).Replace(string(b))
	if _, err := t.f.WriteString(esc + "\n"); err != nil {
		// 只失败一次就关掉：磁盘满时逐条告警会把日志刷爆，而旁路已经废了
		t.f.Close()
		t.f = nil
	}
}

// Close 关闭旁路，幂等，nil 接收者安全。
func (t *Tap) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
}
```

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/rawtap/ -race -count=1 -v`
Expected: 五条全部 PASS（`-race` 必须带上：`TestWriteIsConcurrencySafe` 是本包唯一的并发保证）

- [ ] **Step 5: 加关键节点日志**

- 旁路开启成功：`log.Info("原始字节旁路已开启", "executor", ..., "task", ..., "path", ...)` —— Info 级：开着跑探针时必须能在 agentd.log 里确认它真的开了，否则跑完 15 次才发现没归档。
- 目录不可用 / 文件打不开：`log.Warn` 各一条，带 path 与 cause，然后降级为关闭。
- 写失败：**不打日志**，直接关掉句柄。理由写进注释——逐条告警会把日志刷爆，而此时旁路已经废了。
- `Write` 成功不打日志：它每条上游消息调用一次，打日志等于把整个流复制进 agentd.log。

- [ ] **Step 6: 加注释**

- 包头注释：职责两条 + 边界三条 + **为什么需要它**（三种上游都无法从进程外 tee；B74 原始现场因此丢失）。
- `Tap` 类型：nil 接收者安全的理由（调用点不需要写 if）。
- `Open`：参数/返回 + 「开启失败一律降级」的理由。
- `Write`：**为什么要转义内嵌换行**（回放分帧）+ 写失败为什么静默。
- 常量 `EnvDir`：空即关闭。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/executor/rawtap/ -race -count=1
git add internal/executor/rawtap/
git commit -m "feat(executor): 加 HANDOFF_RAW_TAP_DIR 门控的上游原始字节旁路

grok/codex 的上游是进程内 WebSocket、opencode 的 SSE 端口随机，三者都无法
从进程外 tee——没有旁路就没有原始样本。B74 的原始现场正是因此永久丢失。
缺省完全关闭，nil 接收者安全，写失败静默关句柄：观测失败不影响被观测的东西。"
```

---

### Task 2: 四个 adapter 接入旁路

**Files:**
- Modify: `internal/executor/opencode/api.go:848-864`（SSE 按行读处）
- Modify: `internal/executor/claudecode/stream.go:120-158`（`scanOnce` 的 `ReadBytes` 处）
- Modify: `internal/executor/grok/acp.go:209-215`（`readLoop` 的 `conn.Read` 处）
- Modify: `internal/executor/codex/appserver.go:230-236`（`readLoop` 的 `conn.Read` 处）
- Create: `internal/executor/rawtap/wiring_test.go`

**Interfaces:**
- Consumes: `rawtap.Open` / `(*Tap).Write` / `(*Tap).Close` / `rawtap.EnvDir`（Task 1）
- Produces: 四个 adapter 在开启旁路时把上游每条原始消息写入 `<dir>/<executor>-<taskID>.jsonl`

**接线位置的判据**：每个 adapter 都只有**一个**上游字节入口，全部在解析之前。逐一核实过：

| executor | 唯一入口 | 旁写什么 |
|---|---|---|
| opencode | `api.go` `sc.Scan()` 后的 `line` | 每一行 SSE 原文（含 `data:` 前缀与空行分隔） |
| claudecode | `stream.go` `r.ReadBytes('\n')` 后的 `line` | out.jsonl 的每一整行 |
| grok | `acp.go` `c.conn.Read(ctx)` 后的 `data` | 每一帧 ACP JSON-RPC 报文 |
| codex | `appserver.go` `c.conn.Read(ctx)` 后的 `data` | 每一帧 app-server JSON-RPC 报文 |

opencode 旁写的是**未剥 `data:` 前缀、含空行**的原文，不是 `dispatch` 拼好的事件体——空行就是分帧信号，剥掉它样本就不能回放。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/rawtap/wiring_test.go`。四个 adapter 分居四个包，跨包调用其内部读循环不现实；本测试改为**结构性断言**：确认四个入口处确实调用了 `rawtap`，且没有任何一处在旁写之前做了解析。

```go
package rawtap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wiringSite 是一个必须接入旁路的上游字节入口。
type wiringSite struct {
	file   string // 相对仓库根
	anchor string // 该入口处必须存在的调用
}

// TestAllUpstreamEntrypointsAreTapped 守住「四个 adapter 都接了旁路」这件事。
//
// 为什么用源码断言而不是行为测试：四个读循环各自需要一个真实的上游连接
// （SSE / 文件 tail / 两条 WebSocket）才能跑起来，为它们各造一套桩的成本远高于
// 本测试要防的风险——风险是「新增第五个 executor 时忘了接旁路」，而那正是
// 一条 grep 就能守住的事。
func TestAllUpstreamEntrypointsAreTapped(t *testing.T) {
	sites := []wiringSite{
		{"internal/executor/opencode/api.go", "rawTap.Write(sc.Bytes())"},
		{"internal/executor/claudecode/stream.go", "t.rawTap.Write(line)"},
		{"internal/executor/grok/acp.go", "c.rawTap.Write(data)"},
		{"internal/executor/codex/appserver.go", "c.rawTap.Write(data)"},
	}
	root := repoRoot(t)
	for _, s := range sites {
		b, err := os.ReadFile(filepath.Join(root, s.file))
		if err != nil {
			t.Fatalf("%s: %v", s.file, err)
		}
		if !strings.Contains(string(b), s.anchor) {
			t.Errorf("%s 未接入原始字节旁路，缺 %q", s.file, s.anchor)
		}
	}
}

// repoRoot 从当前包目录向上找到含 go.mod 的目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("未找到仓库根（go.mod）")
		}
		dir = parent
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/executor/rawtap/ -run TestAllUpstream -v`
Expected: FAIL，四条 `未接入原始字节旁路` 全部报出

- [ ] **Step 3: 写实现**

**opencode** —— `internal/executor/opencode/api.go`，在 `API` 结构体加 `rawTap *rawtap.Tap` 字段，在建立 SSE 连接的函数里 `Open`/`defer Close`，读循环内旁写：

```go
	// 原始字节旁路：必须在剥 data: 前缀与聚合之前写，空行也要写——空行就是
	// SSE 的分帧信号，剥掉它样本就不能回放
	rawTap := rawtap.Open("opencode", a.taskID, a.log())
	defer rawTap.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), sseScanBuffer)
	var data []string
	for sc.Scan() {
		rawTap.Write(sc.Bytes())
		line := sc.Text()
		switch {
```

若 `API` 上没有现成的 `taskID`，用该 API 实例已有的会话/任务标识；无标识时传空串，文件名退化为 `opencode-.jsonl`——探针一次只跑一个任务，可接受，但要在注释里写明这个退化。

**claudecode** —— `internal/executor/claudecode/stream.go` 的 `tailer` 加 `rawTap *rawtap.Tap` 字段（由构造 `tailer` 处 `Open`，`tailer` 停止处 `Close`），在推进 offset 之后、`json.Unmarshal` 之前旁写：

```go
		consumed += int64(len(line))
		t.offset.Add(int64(len(line)))

		// 原始字节旁路：在 Unmarshal 之前写，非 JSON 的坏行也要留样
		t.rawTap.Write(line)

		var m streamMsg
		if jerr := json.Unmarshal(line, &m); jerr != nil {
```

**grok** —— `internal/executor/grok/acp.go` 的 `ACPClient` 加 `rawTap *rawtap.Tap` 字段（`Open` 在建连处，`Close` 在 `readLoop` 的 defer 里），读循环内：

```go
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			exitErr = err
			return
		}
		// 原始字节旁路：在 Unmarshal 之前写，解析失败被跳过的坏帧也要留样——
		// 被截断的工具调用极可能正是解析失败的那一帧
		c.rawTap.Write(data)

		var msg struct {
```

**codex** —— `internal/executor/codex/appserver.go` 的 `Client`，与 grok 完全同形，同一位置加 `c.rawTap.Write(data)` 与同样的注释。

四处的 `Open` 都传各自 executor 名与该连接对应的 taskID，`log` 传各自已有的 logger。

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/... -count=1`
Expected: 结构性断言 PASS；四个 adapter 的既有测试全部保持 PASS（旁路默认关闭，`Open` 返回 nil，`Write` 是空操作，不应改变任何既有行为）

- [ ] **Step 5: 加关键节点日志**

四处均**不加新日志**——`rawtap.Open` 自己已经打了「旁路已开启」的 Info（Task 1），在读循环里再打一条是重复；而读循环内部每帧一条日志会把 agentd.log 刷爆，这正是 `Write` 不打日志的原因。

需要确认的是四处 `Close` 都在 defer 里，进程异常退出时文件仍会被 OS 回收——本条写进注释而非日志。

- [ ] **Step 6: 加注释**

四处各一条 why 注释，且**四条各不相同**（它们防的不是同一件事）：

- opencode：为什么在剥 `data:` 前缀与聚合之前写，为什么空行也要写。
- claudecode：为什么在 `Unmarshal` 之前写（非 JSON 的坏行也要留样）。
- grok / codex：为什么在 `Unmarshal` 之前写——**被截断的工具调用极可能正是解析失败被跳过的那一帧**，这是本探针的核心假设，必须写在代码旁边。
- 四个结构体新字段各一行说明 + `Open`/`Close` 的配对位置。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
git add internal/executor/
git commit -m "feat(executor): 四个 adapter 接入上游原始字节旁路

opencode/claudecode/grok/codex 各自唯一的上游读取点各接一行，一律在解析
之前——被截断的工具调用极可能正是解析失败被跳过的那一帧。默认关闭时
Open 返回 nil、Write 为空操作，既有行为零变化。结构性断言守住「新增
executor 时别忘了接」。"
```

---

### Task 3: 沙箱仓库与四份场景 plan 文本

**Files:**
- Create: `docs/superpowers/probes/2026-08-12-turn-end/S1-natural-finish.md`
- Create: `docs/superpowers/probes/2026-08-12-turn-end/S2-no-commit.md`
- Create: `docs/superpowers/probes/2026-08-12-turn-end/S3-truncated-toolcall.md`
- Create: `docs/superpowers/probes/2026-08-12-turn-end/S4-native-question.md`
- Create: `docs/superpowers/probes/2026-08-12-turn-end/README.md`（矩阵表与逐次记录表，后续 task 往里填）

**Interfaces:**
- Consumes: 无
- Produces: 15 次派发要用的四份 plan 正文；一张待填的结果表

- [ ] **Step 1: 建沙箱仓库并登记**

```bash
mkdir -p ~/workspace/handoff-probe-sandbox && cd ~/workspace/handoff-probe-sandbox && git init && printf '# probe sandbox\n\n用于 handoff 回合终结信号探针，内容无意义，可随时清空。\n' > README.md && printf 'package main\n\nfunc main() {}\n' > main.go && git add -A && git commit -m "chore: 探针沙箱初始化"
```

沙箱必须有 origin（`handoff dispatch` 按 origin 算 project_id）。若无远端，建一个裸仓库当 origin：

```bash
git init --bare ~/workspace/handoff-probe-sandbox.git && cd ~/workspace/handoff-probe-sandbox && git remote add origin ~/workspace/handoff-probe-sandbox.git && git push -u origin HEAD
```

登记到本机与 devbox：

```bash
cd ~/workspace/handoff-probe-sandbox && handoff project add probe-sandbox --target devbox
```

**在 devbox 上也要有这份仓库**（agentd 在那边跑）。若 devbox 上没有，先在 devbox clone 一份再在 devbox 侧 `handoff project add`。

- [ ] **Step 2: 写 S1 场景 plan**

`docs/superpowers/probes/2026-08-12-turn-end/S1-natural-finish.md`：

```markdown
# 探针场景 S1：自然语言收尾（不输出 JSON）

这是一次协议行为观测，不是真实开发任务。请**严格**按下面做，不要发挥。

1. 在仓库根新建文件 `probe-s1.txt`，内容写一行：`S1`
2. `git add probe-s1.txt && git commit -m "probe: S1"`
3. 用**自然语言**说一句「我做完了，已提交」，然后结束本回合。

**硬性约束：本回合的输出里禁止出现任何 JSON、任何花括号 `{`。**
不要输出 `{"branch":...}`，不要输出 `{"ask":...}`，不要复述协议格式。
```

- [ ] **Step 3: 写 S2 场景 plan**

`docs/superpowers/probes/2026-08-12-turn-end/S2-no-commit.md`：

```markdown
# 探针场景 S2：不改不提交，只描述打算

这是一次协议行为观测，不是真实开发任务。请**严格**按下面做，不要发挥。

1. **不要修改任何文件，不要 git add，不要 git commit。**
2. 用自然语言描述一下：如果要给这个仓库加一个 hello world 打印，你打算怎么做。
3. 说完就结束本回合。

**硬性约束：本回合的输出里禁止出现任何 JSON、任何花括号 `{`。**
不要输出 `{"branch":...}`，不要输出 `{"ask":...}`，不要复述协议格式。
```

- [ ] **Step 4: 写 S3 场景 plan**

`docs/superpowers/probes/2026-08-12-turn-end/S3-truncated-toolcall.md`：

```markdown
# 探针场景 S3：超长工具调用参数（求撞输出上限）

这是一次协议行为观测，不是真实开发任务。请**严格**按下面做，不要发挥。

1. 调用一次写文件工具，把内容写到 `probe-s3.txt`。
2. 该工具调用的**内容参数**必须极长：把下面这句话原样重复 **20000 遍**，
   每遍之间用换行分隔，全部作为一次工具调用的参数发出。

   `这是一行用于撑爆单次工具调用参数长度的填充文本，没有任何语义。`

3. **必须一次调用发完，不许分批、不许写脚本生成、不许用 shell 重定向绕开。**
   本场景要观测的就是这一次调用本身，绕开它这次派发就作废。
4. 工具调用发出后，如果还能继续，就结束本回合。

**硬性约束：本回合的输出里禁止出现任何形如 `{"branch":...}` 或 `{"ask":...}` 的协议 JSON。**
```

- [ ] **Step 5: 写 S4 场景 plan**

`docs/superpowers/probes/2026-08-12-turn-end/S4-native-question.md`：

```markdown
# 探针场景 S4：走原生提问通道

这是一次协议行为观测，不是真实开发任务。请**严格**按下面做，不要发挥。

1. **不要修改任何文件，不要提交。**
2. 你需要人来裁决一个三选一。用你自己环境里的**提问工具/提问通道**
   （不是输出 JSON，是调用工具）问出这个问题：

   > 这个仓库的 hello world 应该放在 A) main.go B) 新建 hello.go C) 不加，三选一？

3. 问完就结束本回合，等待回答。

**硬性约束：不要用 `{"ask":...}` 这种 JSON 形式提问，必须走工具通道。
如果你的环境没有提问工具，就直接说「本环境无原生提问通道」并结束回合。**
```

- [ ] **Step 6: 写结果表骨架**

`docs/superpowers/probes/2026-08-12-turn-end/README.md`：

```markdown
# 回合终结信号探针（2026-08-12）

依据 spec `docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §5。
**只回答一个问题：S3（截断）在事件层有没有可判别于 S1 的信号。**

## 前置

- devbox agentd 以 `HANDOFF_RAW_TAP_DIR=~/handoff-probe-raw` 启动（旁路见 `internal/executor/rawtap`）
- 沙箱仓库 `probe-sandbox` 已在本机与 devbox 双侧登记
- **串行**：任何时刻只有一个任务在跑

## 每次派发的动作

```bash
cd ~/workspace/handoff-probe-sandbox && handoff dispatch docs/superpowers/probes/2026-08-12-turn-end/<Sn>.md --target devbox --project probe-sandbox --executor <x> --new-branch probe-<Sn>-<x> --new-worktree --name "probe <Sn> <x>"
```

派发前查 devbox 进程余量（不足则停）：

```bash
ssh devbox 'echo "maxprocperuid=$(sysctl -n kern.maxprocperuid 2>/dev/null || echo n/a) current=$(ps -u $(id -u) | wc -l)"'
```

派发后：

```bash
handoff show <task-id> --target devbox
```

## 结果表（15 行，逐次填）

| # | 场景 | executor | handoff 判成 | 任务落到 | 事件层信号（原始样本里看到什么） | 样本文件 |
|---|------|----------|-------------|---------|--------------------------------|---------|
| 1 | S1 | opencode | | | | |
| 2 | S1 | claudecode | | | | |
| 3 | S1 | grok | | | | |
| 4 | S1 | codex | | | | |
| 5 | S2 | opencode | | | | |
| 6 | S2 | claudecode | | | | |
| 7 | S2 | grok | | | | |
| 8 | S2 | codex | | | | |
| 9 | S3 | opencode | | | | |
| 10 | S3 | claudecode | | | | |
| 11 | S3 | grok | | | | |
| 12 | S3 | codex | | | | |
| 13 | S4 | opencode | | | | |
| 14 | S4 | grok | | | | |
| 15 | S4 | codex | | | | |

**claudecode 无 S4**：`internal/executor/claudecode/` 下无原生提问通道翻译（grep 无 `askedViaTool`）。合计 15 次，不是 16。

## 结论（按 spec §3.5 的规则套用，逐 executor）

| executor | S3 是否复现 | S3 信号能否与 S1 区分 | 处置 |
|---|---|---|---|
| opencode | | | |
| claudecode | | | |
| grok | | | |
| codex | | | |
```

- [ ] **Step 7: 提交**

```bash
git add docs/superpowers/probes/
git commit -m "docs(probe): 回合终结信号探针的四份场景 plan 与结果表骨架

S1 自然语言收尾 / S2 不提交 / S3 超长工具调用参数 / S4 原生提问通道。
claudecode 无原生提问通道，S4 跳过，合计 15 次派发。"
```

---

### Task 4: 跑基线 S1 + S2（8 次派发）

**Files:**
- Modify: `docs/superpowers/probes/2026-08-12-turn-end/README.md`（填第 1–8 行）

**Interfaces:**
- Consumes: Task 2 的旁路、Task 3 的沙箱与场景文本
- Produces: 8 份原始样本（`~/handoff-probe-raw/<executor>-<taskID>.jsonl`）与结果表 1–8 行

**这是本 plan 唯一的对照组。** S3 的信号「能不能与 S1 区分」，判据全在这 8 次跑出来的基线上。

- [ ] **Step 1: 以旁路开启的方式重启 devbox agentd**

先按铁律确认服务托管方：`mcp__superdev__list_services` 查 devbox 上的 agentd 是否被 SuperDev 接管。

- **已接管**：用 `mcp__superdev__restart_service` 重启，并把 `HANDOFF_RAW_TAP_DIR` 加进服务的环境变量（走 `preview_config_change` → `apply_config_change`）。
- **未接管**：按项目既有方式重启，环境变量随启动命令带上。

确认旁路真的开了（Task 1 的 Info 日志）：

```bash
ssh devbox 'grep -c "原始字节旁路已开启" ~/.handoff/agentd.log'
```

- [ ] **Step 2: 逐次派发 S1（4 次，串行）**

按 README 的命令模板，`<Sn>` = `S1-natural-finish`，`<x>` 依次取 `opencode` / `claudecode` / `grok` / `codex`。

**每次派发前**必须先查 devbox 进程余量（README 里的 ssh 命令），余量不足则**停下来报告**，不硬上。
**每次必须等上一次任务落到终态**（`handoff show` 显示 `waiting_review` / `waiting_answer`）**再发下一次**。

每次跑完记录三样进结果表：
- `handoff 判成`：question / result OK / result !OK —— 从 `handoff show <id> --target devbox` 的事件流读
- `任务落到`：waiting_review / waiting_answer
- `样本文件`：`ssh devbox 'ls -la ~/handoff-probe-raw/'` 里对应那个

- [ ] **Step 3: 逐次派发 S2（4 次，串行）**

同 Step 2，`<Sn>` = `S2-no-commit`。

- [ ] **Step 4: 把 8 份样本拉回本机**

```bash
mkdir -p /tmp/probe-raw && rsync -av devbox:~/handoff-probe-raw/ /tmp/probe-raw/
```

- [ ] **Step 5: 逐份读样本，记录事件层信号**

对每份样本，记录**回合最后一条上游消息**的形状进结果表的「事件层信号」列。这是 S3 的对照基准，必须写具体，不能写「正常结束」：

- opencode：最后那条 `session.status` 的 `properties.status.type`，以及此前最后一条 `message.part.updated` 的 part 类型
- claudecode：最后一行的 `type` 与 `subtype`（如 `result` / `success`）
- grok / codex：最后一帧的 `method` 或 `result` 的形状，以及有没有 `error` 帧

- [ ] **Step 6: 填结果表并提交**

```bash
git add docs/superpowers/probes/2026-08-12-turn-end/README.md
git commit -m "docs(probe): S1/S2 基线 8 次派发实测结果"
```

**注意**：本 task 不写代码，因此没有「加日志 / 加注释」两个 step——它们是实现类 task 的要求。本 task 的等价物是 Step 5：**观测必须落成可复核的记录**，写「正常结束」这种无内容的结论等同于没记。

---

### Task 5: 跑 S3 截断场景（4 次派发）

**Files:**
- Modify: `docs/superpowers/probes/2026-08-12-turn-end/README.md`（填第 9–12 行与结论表）

**Interfaces:**
- Consumes: Task 4 的基线记录
- Produces: 4 份 S3 原始样本；逐 executor 的「复现 / 未复现」「可区分 / 不可区分」判定

- [ ] **Step 1: 逐次派发 S3（4 次，串行）**

`<Sn>` = `S3-truncated-toolcall`，`<x>` 依次取四个 executor。同 Task 4 的串行纪律与进程余量检查。

**预判一处已知机制，观测时留意**：opencode 的 SSE scanner 单行上限是 1MB（`internal/executor/opencode/api.go` 的 `sseScanBuffer`）。超长单行会让 `sc.Err()` 非 nil，走「SSE 流读取异常」的 Warn 分支。**若真发生，这本身就是一个可判别于 S1 的信号**，必须原样记下，不要当成探针故障。

- [ ] **Step 2: 判定每个 executor 的 S3 是否复现**

「复现」的判据是**执行者真的发出了那次超长工具调用**，而不是它绕开了（写脚本生成、分批写、改用 shell 重定向）。绕开就是**未复现**，如实记，不重试到出现想要的现象。

允许最多**重试一次**（模型有随机性），第二次仍绕开即定为未复现。重试次数写进结果表。

- [ ] **Step 3: 逐份对照基线，判定信号可区分性**

把 S3 的样本与同一 executor 的 S1 样本并排读，回答一个是非题：

> **只看事件层（不看正文内容），能不能把这次 S3 与那次 S1 分开？**

「能」的例子：多了一帧 `error`；`session.status` 之前有一条 `ToolStatus` 非 completed；连接直接断了；scanner 报了超长行。
「不能」的例子：两边最后几帧完全同形，只有正文长度不同——**正文长度不算信号**，正常长回合也长。

填进结论表的「S3 信号能否与 S1 区分」列。

- [ ] **Step 4: 按 spec §3.5 的规则填「处置」列**

规则原样照抄，不再讨论：

| 探针结果 | 处置 |
|---|---|
| 能与 S1 区分 | 给该 executor 加证据层 |
| 不能区分 | 不加 |
| S3 未复现 | 不加 |

- [ ] **Step 5: 提交**

```bash
git add docs/superpowers/probes/2026-08-12-turn-end/README.md
git commit -m "docs(probe): S3 截断场景 4 次派发实测与逐 executor 判定"
```

---

### Task 6: 跑 S4 原生提问（3 次派发）

**Files:**
- Modify: `docs/superpowers/probes/2026-08-12-turn-end/README.md`（填第 13–15 行）

**Interfaces:**
- Consumes: Task 3 的场景文本
- Produces: 3 份 S4 原始样本；「原生提问通道与 trailer 判定是否一致」的结论

- [ ] **Step 1: 逐次派发 S4（3 次，串行）**

`<Sn>` = `S4-native-question`，`<x>` 依次取 `opencode` / `grok` / `codex`。**claudecode 跳过**（无原生提问通道）。

- [ ] **Step 2: 记录判定一致性**

对每个 executor 记录：
- 执行者有没有真的走工具通道提问（还是退回了「本环境无原生提问通道」）
- handoff 判成了什么、任务落到哪个状态
- 与同 executor 的 trailer 提问路径（历史行为）是否落到同一状态

**已知的对照事实**（spec §2.3）：devbox agentd.log 里 `本回合已通过 question 工具提问` 出现 **0** 次，即 opencode 的原生 question 工具历史上从未被用过。S4 若在 opencode 上成功触发，这条日志应当首次出现——**用它作为「原生通道真的被走了」的判据**，不要只看模型自述：

```bash
ssh devbox 'grep -c "本回合已通过 question 工具提问" ~/.handoff/agentd.log'
```

- [ ] **Step 3: 填表并提交**

```bash
git add docs/superpowers/probes/2026-08-12-turn-end/README.md
git commit -m "docs(probe): S4 原生提问通道 3 次派发实测"
```

---

### Task 7: 样本入库与回放测试

**Files:**
- Create: `internal/executor/opencode/testdata/probe-s1-opencode.jsonl` 等（每个 executor 各自 `testdata/` 下，按 `probe-<场景>-<executor>.jsonl` 命名）
- Create: `internal/executor/opencode/replay_probe_test.go`
- Create: `internal/executor/claudecode/replay_probe_test.go`
- Create: `internal/executor/grok/replay_probe_test.go`
- Create: `internal/executor/codex/replay_probe_test.go`
- Modify: `docs/superpowers/specs/2026-08-12-false-completion-and-cursor-durability-design.md` §3.5（回填实测判定表）

**Interfaces:**
- Consumes: Task 4/5/6 的 15 份原始样本
- Produces: 入库样本 + 四个包各一个回放测试；spec §3.5 的 TBD 被实测结论替换

**为什么必须入库**（既有规矩，`internal/executor/opencode/replay_spike_test.go` 头注释写死了理由）：样本留在本机等于结论无法从任何一个 clone 复核——上游一改协议，没有任何东西会变红。

- [ ] **Step 1: 反转义并归位样本**

`rawtap` 写出的是转义过的行（`\n` / `\r` / `\\`）。入库前反转义回原始分帧，写一个一次性脚本：

```bash
cat > /tmp/unescape.py <<'PY'
import sys, pathlib
src, dst = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
out = []
for line in src.read_text().split("\n"):
    if line == "":
        continue
    out.append(line.replace("\\n", "\n").replace("\\r", "\r").replace("\\\\", "\\"))
dst.write_text("\n".join(out) + "\n")
PY
python3 /tmp/unescape.py /tmp/probe-raw/opencode-<taskID>.jsonl internal/executor/opencode/testdata/probe-s1-opencode.jsonl
```

15 份逐一处理，按 `probe-<场景小写>-<executor>.jsonl` 命名放进对应包的 `testdata/`。

**S3 未复现的 executor 仍然要入库它那次的样本**，文件名后缀改为 `-notreproduced`。未复现本身是结论，样本是它的证据。

- [ ] **Step 2: 写 opencode 的回放测试**

`internal/executor/opencode/replay_probe_test.go`。照抄既有 `replay_spike_test.go` 的形状（样本按原始字节喂进 `streamOnce`，走生产解析路径），断言**当前生产代码对每份样本的分类结果**：

```go
// replay_probe_test.go —— 2026-08-12 回合终结信号探针样本的重放测试。
//
// 职责：
//   - 把 testdata/probe-*.jsonl（探针实跑抓到的 opencode SSE 原始字节）
//     原样回放给 adapter，断言分类结果与探针当时的实测记录一致
//
// 边界：
//   - 只断言「当前代码对这份样本判成什么」，不断言 opencode 应该发什么
//   - 不 mock 解析：走 streamOnce 的生产路径
//
// 为什么必须入库：探针的全部结论（spec §3.5 的判定表）建立在这些样本上。
// 样本留在本机则结论不可从任何 clone 复核，上游改协议时没有任何东西会变红。
package opencode

import "testing"

// probeCase 描述一份探针样本及其实测分类。
type probeCase struct {
	file    string
	wantEvt string // "question" | "result"
	wantOK  bool   // wantEvt == "result" 时有意义
	note    string // 探针记录里那一行的「事件层信号」
}

func TestReplayProbeSamples(t *testing.T) {
	cases := []probeCase{
		// 逐条填入 Task 4/5/6 的实测值。禁止凭想象填——
		// 与结果表对不上的断言比没有断言更糟
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			// 复用 replay_spike_test.go 里已有的回放骨架：起 httptest SSE server
			// 喂样本 → 收集 adapter 事件 → 断言最后一条
			t.Fatal("待 Task 4/5/6 的实测值填入")
		})
	}
}
```

**实现时**：把 `replay_spike_test.go` 里已有的「起 `httptest` SSE server 喂样本 → 收集 adapter 事件」骨架抽成一个可复用的 helper，两个测试共用。`cases` 逐条填**实测值**，不是期望值。

- [ ] **Step 3: 写另外三个包的回放测试**

三个包各一个 `replay_probe_test.go`，同样的文件头注释结构（职责 / 边界 / 为什么必须入库），各自复用本包已有的回放骨架：

- claudecode：`testdata/turn_success.jsonl` 已有的回放路径
- grok：`testdata/updates.jsonl` 已有的回放路径
- codex：本包若无现成回放骨架，按 grok 的形状新写一个（喂 JSON-RPC 帧进 `readLoop` 的处理函数）

- [ ] **Step 4: 运行测试，确认全绿**

Run: `go test ./internal/executor/... -count=1 -v -run Probe`
Expected: 15 条子测试全部 PASS，且每条的断言值来自结果表

- [ ] **Step 5: 回填 spec §3.5**

把 spec §3.5 的决策规则表下方追加一节「实测结论（2026-08-12）」，逐 executor 写「S3 复现 / 未复现」「可区分 / 不可区分」「处置：加 / 不加证据层」，并链到 `docs/superpowers/probes/2026-08-12-turn-end/README.md`。

若结论为「四个 executor 全部不加」，就明确写下这句话，并按 spec §9 把「截断在事件层长什么样」这个空白回填进 `docs/superpowers/backlog.md` 的备注。

- [ ] **Step 6: 关掉旁路**

探针跑完把 devbox agentd 的 `HANDOFF_RAW_TAP_DIR` 撤掉并重启（走 SuperDev 或项目既有方式），确认日志里不再出现「原始字节旁路已开启」。旁路是诊断开关，不是常驻设施（`rawtap` 包头注释已声明这条边界）。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && go test ./... -count=1
git add internal/executor/*/testdata/probe-*.jsonl internal/executor/*/replay_probe_test.go docs/superpowers/specs/ docs/superpowers/backlog.md
git commit -m "test(executor): 探针样本入库与回放测试，回填 spec §3.5 实测判定

15 份原始样本入库四个包的 testdata/，各配回放测试断言当前代码对它们的
分类结果。样本留在本机等于结论不可复核——这是本仓库既有规矩。
spec §3.5 的决策规则不再是待定，逐 executor 有了实测处置。"
```

---

## Self-Review

**1. Spec coverage**

| spec 条目 | 落在哪 |
|---|---|
| §5.1 只回答「S3 有无可判别信号」 | Task 5 Step 3 的是非题 |
| §5.2 四 executor × 四场景矩阵 | Task 3 场景文本 + Task 4/5/6 派发 |
| §5.2 claudecode 无 S4，合计 15 次 | Task 3 README + Task 6 Step 1 |
| §5.2 每次量三样（信号 / 判定 / 状态） | Task 4 Step 2 与 Step 5、Task 5、Task 6 |
| §5.3 串行 + 进程余量检查 | Global Constraints + Task 4 Step 2 |
| §5.3 专用沙箱、每次新分支新 worktree | Task 3 Step 1 + README 命令模板 |
| §5.3 原始字节归档入 testdata + 回放测试 | Task 1/2（旁路）+ Task 7 |
| §5.3 「未复现」是允许结果 | Task 5 Step 2（含「绕开即未复现」「最多重试一次」） |
| §5.3 只观测不改判定逻辑 | Global Constraints；Task 1/2 只旁写不参与判断 |
| §3.5 决策规则套用 | Task 5 Step 4（规则原样照抄） |
| §3.5 结论回填 | Task 7 Step 5 |
| §8 验收 #8（15 次全有样本，S3 结论明确） | Task 7 Step 1（含未复现也入库） |

**已知的对 spec 的补充**：`rawtap` 旁路（Task 1/2）在 spec §5 里没有对应条目。理由与裁决点已写在 Global Constraints 的「关于新增一个环境变量」小节——三种上游都无法从进程外 tee，没有旁路就没有 §5.3 要求的原始样本。这是明说的补充，不是静默扩张。

**2. Placeholder scan**

Task 7 Step 2 的 `cases` 是**刻意留空**的：它的内容必须是 Task 4/5/6 的实测值，此刻写任何具体值都是编造，而编造的断言比没有断言更糟。这一点在代码注释与步骤正文里各写了一次，且用 `t.Fatal("待 Task 4/5/6 的实测值填入")` 保证漏填时测试变红而不是假绿。**这是唯一的空位，且它是被守住的。** 其余步骤无 TBD、无「同 Task N」。

Task 4/5/6 是执行类 task（跑真机派发），没有代码步骤，因此不含「加日志 / 加注释」step——`instrumenting-code` 的要求针对实现类 task。Task 4 Step 6 下方已明确写出这条豁免的理由与等价物（观测必须落成可复核的记录）。实现类 task 是 Task 1、2、7，三者都有日志与注释步骤（Task 7 的注释要求写在 Step 2/3 的文件头注释里，日志不适用——它只有测试代码）。

**3. Type consistency**

`rawtap.Open` / `(*Tap).Write` / `(*Tap).Close` / `rawtap.EnvDir` 在 Task 1 定义，Task 2 的四处接线与 Task 2 测试的四个 anchor 字符串（`rawTap.Write(sc.Bytes())` / `t.rawTap.Write(line)` / `c.rawTap.Write(data)` ×2）逐字一致。样本命名 `probe-<场景>-<executor>.jsonl` 在 Task 7 Step 1 与文件清单一致。
