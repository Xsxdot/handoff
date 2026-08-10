# 回收复核与日志可读性（B47 + B41 + B48）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让「进程没死」这个事实先被验证出来、再能一路传到人眼前（B47）；顺带修掉一条测试脚手架竞态（B41）与一处重复日志（B48）。

**Architecture:** `prochost.Kill` 在发完 SIGKILL 后按 `Alive`（文件锁探测）有界退避复核，仍活着则返回新哨兵 `ErrStillAlive`；该哨兵经四个 adapter 的 `Stop` 上抛到 `Manager.stopExecutor`，由它落一条面向人的 progress 事件。B41、B48 各自独立，不与 B47 交叉。

**Tech Stack:** Go 1.26、标准库 `syscall` / flock、slog、`github.com/coder/websocket`（codex 测试脚手架）。

**Spec:** `docs/superpowers/specs/2026-08-10-kill-verification-and-log-hygiene-design.md`（遇到「为什么这么设计」先读它，不要自行重新推导）。

## Global Constraints

- 语言与注释一律中文；新增导出符号必须有 doc 注释（参数/返回/注意），非显然的分支必须有「为什么」型行内注释。
- 日志一律用各包既有的 `log()` / `a.log` / `m.log`，**禁止 `fmt.Printf` 作为日志手段**。错误分支带上下文与 cause，成功路径也要有结论日志。
- 错误分类一律用哨兵 + `errors.Is`；沿途包装必须用 `%w`，**断了链路等于这次改动白做**。
- **不新增平台分支、不碰 `internal/prochost/platform_other.go`**。本次改的全是平台无关逻辑，B37 不因此推进。
- **不改 `reapRetained` 的重试间隔与上限**；本次只是让它第一次被触发。
- **不得 `git push`**，不得合并到 main，不得改 `docs/superpowers/backlog.md`，不得改 `docs/superpowers/plans/` 与 `specs/` 下的文件。
- **不得碰 `~/.handoff/`**，不得启停/重启/覆盖任何监听 7777 的 agentd——它持有正在跑的任务，包括派你干活的这一条。
- 全套闸门（每个 task 末尾跑前四条，Task 5 跑全部）：
  ```
  gofmt -l .
  go build ./...
  go vet ./...
  go test ./... -count=1
  go test -race ./cmd/ ./internal/agentd/ ./internal/store/ ./internal/prochost/ ./internal/executor/codex/ -count=1
  GOOS=windows GOARCH=amd64 go build ./...
  ```
  `-race` 这条比平时多带 `prochost` 与 `codex` 两个包——本次动的正是它们。

## File Structure

| 文件 | 责任 |
|------|------|
| `internal/prochost/prochost.go`（改） | `ErrStillAlive` 哨兵、`Kill` 的复核循环、测试接缝 |
| `internal/prochost/kill_test.go`（新） | 复核三条路径（锁已释放 / 探到已死 / 走满仍活） |
| `internal/executor/{codex,grok,claudecode}/proc.go`（改） | 各加一个 `killProcHost = prochost.Kill` 测试缝（`proc` 是具体类型，否则测不了失败路径） |
| `internal/executor/codex/adapter.go`（改） | `Stop` 把 Kill 错误上抛，去掉自发的 progress emit |
| `internal/executor/grok/adapter.go`（改） | `Stop` 不再丢弃 Kill 错误 |
| 四个 adapter 的 `stop_internal_test.go`（新） | 哨兵穿透断言（opencode 复用既有 `fakeProbe`） |
| `internal/agentd/reconcile.go`（改） | `stopExecutor` 对 `ErrStillAlive` 补人工提示；抽 `notifyOrphanRisk` helper |
| `internal/agentd/reconcile_test.go`（改） | 补提示 / 不补提示 两条断言 |
| `internal/executor/codex/appserver_test.go`（改） | `closeFakeConns` 有界等待登记 |
| `internal/localsync/localsync.go`（改） | 同步失败日志降级为 Debug |

---

### Task 1: `prochost.Kill` 杀完复核

**Files:**
- Modify: `internal/prochost/prochost.go`
- Test: `internal/prochost/kill_test.go`

**Interfaces:**
- Consumes: 既有 `prochost.Alive(h Handle) bool`、`killGroup(pid int) error`、`Handle{PID int; LockPath string}`
- Produces:
  - `prochost.ErrStillAlive`（`error` 哨兵）
  - `Kill` 的新契约：确认进程组退出才返回 nil；走满复核窗口仍存活返回包装 `ErrStillAlive` 的错误
  - 包内测试接缝 `aliveFn` / `killGroupFn` / `killVerifyBackoff`（**仅测试替换**）

- [ ] **Step 1: 写失败的测试**

创建 `internal/prochost/kill_test.go`：

```go
package prochost

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// shrinkBackoff 把复核退避换成微秒级，让复核路径的测试不真的等 1s。
// 用 t.Cleanup 还原，避免影响同包其它用例。
func shrinkBackoff(t *testing.T) {
	t.Helper()
	orig := killVerifyBackoff
	killVerifyBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { killVerifyBackoff = orig })
}

// stubAlive 把存活判定换成脚本化的返回序列（最后一个值会被重复使用）。
func stubAlive(t *testing.T, seq ...bool) {
	t.Helper()
	orig := aliveFn
	i := 0
	aliveFn = func(Handle) bool {
		v := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return v
	}
	t.Cleanup(func() { aliveFn = orig })
}

// stubKillGroup 换掉真实的 SIGKILL，返回一个记录调用次数的指针。
func stubKillGroup(t *testing.T, err error) *int {
	t.Helper()
	orig := killGroupFn
	n := 0
	killGroupFn = func(int) error { n++; return err }
	t.Cleanup(func() { killGroupFn = orig })
	return &n
}

func testHandle(t *testing.T) Handle {
	t.Helper()
	return Handle{PID: 4242, LockPath: filepath.Join(t.TempDir(), "shim.lock")}
}

// TestKillSkipsWhenLockFree 验证锁已释放时直接成功，且**绝不发信号**
// （对已回收的 pid 发信号有误杀被复用 pid 的风险，这是本包的历史教训）。
func TestKillSkipsWhenLockFree(t *testing.T) {
	stubAlive(t, false)
	n := stubKillGroup(t, nil)
	if err := Kill(testHandle(t)); err != nil {
		t.Fatalf("锁已释放时 Kill 应直接成功，got %v", err)
	}
	if *n != 0 {
		t.Fatalf("锁已释放却发了 %d 次信号", *n)
	}
}

// TestKillReturnsNilAfterProcessDies 验证复核探到已死即成功返回。
func TestKillReturnsNilAfterProcessDies(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true, false) // 杀之前活着；第一次复核已死
	stubKillGroup(t, nil)
	if err := Kill(testHandle(t)); err != nil {
		t.Fatalf("复核探到已死时应返回 nil，got %v", err)
	}
}

// TestKillReportsStillAlive 验证走满复核窗口仍存活 → ErrStillAlive。
// 这是 B47 的核心：这个信号以前根本产生不出来。
func TestKillReportsStillAlive(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true) // 恒活
	stubKillGroup(t, nil)
	err := Kill(testHandle(t))
	if !errors.Is(err, ErrStillAlive) {
		t.Fatalf("err = %v, want errors.Is(..., ErrStillAlive)", err)
	}
}

// TestKillPropagatesSignalFailure 验证「信号发送失败」与「进程没死」是两种错误，
// 不能混为一谈——只有后者值得惊动人。
func TestKillPropagatesSignalFailure(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, true)
	sentinel := errors.New("boom")
	stubKillGroup(t, sentinel)
	err := Kill(testHandle(t))
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want 包住 killGroup 的原始错误", err)
	}
	if errors.Is(err, ErrStillAlive) {
		t.Fatal("信号发送失败不应被报成 ErrStillAlive")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/prochost/ -run TestKill -count=1`
Expected: 编译失败，`undefined: killVerifyBackoff` / `undefined: aliveFn` / `undefined: killGroupFn` / `undefined: ErrStillAlive`

- [ ] **Step 3: 写实现**

`internal/prochost/prochost.go` 的 import 块补 `"errors"`。在 `Alive` 之后、`Kill` 之前插入：

```go
// ErrStillAlive 表示已发出 SIGKILL 且复核窗口走完，进程组仍然存活。
//
// 与「信号发送失败」区分开：后者是系统调用出错（可能只是权限或参数问题），
// 前者是进程**真的没死**——只有后一种意味着会留下长期孤儿，值得惊动人。
// agentd 侧靠 errors.Is 认这个哨兵来决定要不要给审核者发提示事件。
var ErrStillAlive = errors.New("进程组仍然存活")

// killVerifyWindow 是复核存活的总时长上限，killVerifyBackoff 的各项之和。
//
// 为什么是 1s 而不是更久：Kill 处在归档/中止的同步路径上，它变慢等于
// handoff done / handoff stop 变慢。1s 足以覆盖 SIGKILL 的正常生效窗口；
// 超过 1s 还活着的本来就该交给人和后台重试，而不是让审核者对着终端干等。
const killVerifyWindow = time.Second

// killVerifyBackoff 是 killGroup 之后逐次复核的等待序列（累计 = killVerifyWindow）。
//
// 为什么要退避而不是固定间隔：SIGKILL 异步生效，绝大多数进程在头几十毫秒内
// 就没了——前密后疏能让常见情况几乎不增加延迟，又不放弃慢死场景的覆盖。
//
// 是变量而非常量：测试要把它换成微秒级，否则每条复核用例都真等 1s。
var killVerifyBackoff = []time.Duration{
	10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond,
	80 * time.Millisecond, 160 * time.Millisecond, 320 * time.Millisecond,
	370 * time.Millisecond,
}

// aliveFn / killGroupFn 是包内测试接缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「持锁但杀不死」的形态，只能靠替换这两个函数驱动复核失败路径。
// **生产路径恒为下面这两个默认值**，任何非测试代码都不得赋值给它们。
var (
	aliveFn     = Alive
	killGroupFn = killGroup
)
```

把 `Kill` 整体替换为：

```go
// Kill 终止 shim 及其全部后代（按进程组发送 SIGKILL），并**复核它是否真的死了**。
//
// 幂等：锁已空闲说明 shim 已死，直接返回 nil——**绝不对该 pid 发任何信号**，
// 因为它可能已被操作系统复用给毫不相干的进程（workspace.go 的历史教训：
// 旧实现 300 条成功命令误杀 114 次）。
//
// 参数：
//   - h: 目标 shim 的 Handle；PID <= 0 视为无进程可杀，直接 nil
//
// 返回：
//   - nil: 已确认进程组退出（或本来就已经死了）
//   - 包装 ErrStillAlive 的错误: 信号发出去了，但复核窗口（killVerifyWindow）
//     走完进程仍存活——调用方应保留运行态、上抛给 agentd 提示人工
//   - 其它错误: 信号发送本身失败
//
// 注意：
//   - 复核判据用 Alive（文件锁）而非 kill(pid, 0)：锁由内核在进程死亡时释放，
//     不存在 pid 复用误判，而 kill(pid,0) 会把「pid 被复用」误报成「还活着」
//   - 本函数在确认死亡前不返回，因此调用方紧随其后的资源清理
//     （如 RemoveManagedWorktree）天然排在进程真死之后，不需要额外同步
func Kill(h Handle) error {
	if h.PID <= 0 {
		return nil
	}
	if !aliveFn(h) {
		log().Info("存活锁已释放，无需回收", "pid", h.PID, "lock", h.LockPath)
		return nil
	}
	log().Info("回收执行者进程组", "pid", h.PID)
	if err := killGroupFn(h.PID); err != nil {
		log().Error("回收执行者进程组失败", "pid", h.PID, "cause", err)
		return fmt.Errorf("回收进程组 %d: %w", h.PID, err)
	}
	for i, d := range killVerifyBackoff {
		time.Sleep(d)
		if !aliveFn(h) {
			log().Info("回收完成，已确认进程组退出", "pid", h.PID, "probe", i+1)
			return nil
		}
	}
	log().Error("已发 SIGKILL 但复核窗口走完仍存活，可能有逃逸出进程组的后代",
		"pid", h.PID, "lock", h.LockPath, "window", killVerifyWindow)
	return fmt.Errorf("%w: pid=%d，已发 SIGKILL 并复核 %s", ErrStillAlive, h.PID, killVerifyWindow)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/prochost/ -count=1 -v`
Expected: 四条新用例全部 PASS，既有用例不受影响

- [ ] **Step 5: 变异检验（必做，不得跳过）**

把 `Kill` 里的复核循环整段注释掉，改成直接 `return nil`（即恢复到改动前的行为）。

Run: `go test ./internal/prochost/ -run TestKillReportsStillAlive -count=1`
Expected: **FAIL**。把失败输出原样记进报告。

若它是**绿的**，说明这条测试没有真正锁住复核行为——**停下来报告这个事实**，不要自己想办法让它红。

恢复后：`go test ./internal/prochost/ -count=1 && git diff --exit-code`（须全绿且工作区干净）。

- [ ] **Step 6: 日志与注释自检**

- 复核成功：Info「回收完成，已确认进程组退出」带 `pid` + 第几次探到（`probe`）——**成功路径必须有结论日志**，否则分不清「确认死了」和「压根没走到复核」
- 复核失败：Error 带 `pid` / `lock` / `window`，并点出最可能的成因（有逃逸出进程组的后代）
- `ErrStillAlive`、`killVerifyWindow`、`killVerifyBackoff`、`aliveFn`/`killGroupFn` 各有「为什么」注释：分别是「与信号发送失败的区别」「为什么是 1s」「为什么退避、为什么是变量」「为什么需要接缝、生产路径不得赋值」
- `Kill` 的 doc 更新为新契约，并写明复核为什么用 `Alive` 而不是 `kill(pid,0)`，以及它给调用方顺带带来的时序保证

- [ ] **Step 7: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/prochost/prochost.go internal/prochost/kill_test.go
git commit -m "fix(b47): prochost.Kill 杀完复核，新增 ErrStillAlive 哨兵"
```

---

### Task 2: 让哨兵穿过四个 adapter

**Files:**
- Modify: `internal/executor/codex/proc.go`、`internal/executor/grok/proc.go`、`internal/executor/claudecode/proc.go`（各加一个 `killProcHost` 测试缝）
- Modify: `internal/executor/codex/adapter.go`（`Stop` 约 :376）
- Modify: `internal/executor/grok/adapter.go`（`Stop` 约 :285）
- Test: `internal/executor/codex/stop_internal_test.go`（新，`package codex`）
- Test: `internal/executor/grok/stop_internal_test.go`（新，`package grok`）
- Test: `internal/executor/claudecode/stop_internal_test.go`（新，`package claudecode`）
- Test: `internal/executor/opencode/stop_internal_test.go`（新，`package opencode`）

**Interfaces:**
- Consumes: `prochost.ErrStillAlive`、`Kill` 的新契约（Task 1 产出）
- Produces: 四个 adapter 的 `Stop` 在 Kill 报 `ErrStillAlive` 时，返回的错误都能被 `errors.Is` 命中；codex/grok/claudecode 三个包新增包内缝 `killProcHost`

- [ ] **Step 1: 先核对四家现状（不写码，只读）**

| adapter | 现状 | 本 task 要做的 |
|---|---|---|
| opencode `adapter.go:526` | `Alive()` 为真则保留运行态 + `go a.reapRetained(r)` + `fmt.Errorf("kill serve: %w", kerr)` | **不改生产代码**，只补防回归测试 |
| claudecode `adapter.go:392` | `return kerr` 裸抛 | **不改 adapter**，只加 `killProcHost` 缝 + 测试 |
| codex `adapter.go:377` | 打 Error + `a.emit` 一条 progress，然后继续 drop 并 `return nil` | 改为上抛 |
| grok `adapter.go:286` | `_ = r.proc.Kill()` 整个丢弃 | 改为上抛 |

先把这四处实际读一遍确认与表一致；不一致就在报告里说明，不要照表硬改。

- [ ] **Step 2: 加 `killProcHost` 测试缝（codex / grok / claudecode 三个包）**

这三个包里 `runState.proc` 是**具体类型** `*Proc`，而 `Proc.Kill()` 直接调 `prochost.Kill`；`prochost` 的接缝是包私有的，从外面够不着。所以三个包各需要一个自己的缝——**沿用本仓库已有的写法**（这三个 `proc.go` 里已经有 `startProcHost = prochost.Start`、`startServe = StartServe` 这类缝，照抄形态即可）。

`internal/executor/codex/proc.go`：把

```go
func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }
```

改为

```go
// killProcHost 是 prochost.Kill 的测试缝：SIGKILL 在类 Unix 上不可拦截，
// 真进程做不出「杀不死」的形态，回收失败路径只能靠替换它来驱动。
var killProcHost = prochost.Kill

func (p *Proc) Kill() error { return killProcHost(p.Handle) }
```

`internal/executor/grok/proc.go` 同样处理（那里也是一行式的 `func (p *Proc) Kill() error { return prochost.Kill(p.Handle) }`）。

`internal/executor/claudecode/proc.go` 的 `Kill` 是多行的，只换最后一行：

```go
// Kill 终止 claude 及其后代（按进程组），幂等。
func (p *Proc) Kill() error {
	if p == nil {
		return nil
	}
	return killProcHost(p.Handle)
}
```

并在该文件已有的缝旁边（`startProcHost` / `startProc` 附近）加上同样的 `var killProcHost = prochost.Kill` 与注释。

opencode **不需要**这个缝：它的 `runState.handle` 已经是 `serveHandle` 接口，测试里现成的 `fakeProbe` 就能注入 Kill 错误。

- [ ] **Step 3: 写失败的测试**

四个包各加一个新文件。**注意包名**：codex / grok 的既有测试多为 `package codex_test` / `package grok_test`（外部包，够不着 `runs`），所以新测试必须是内部包，文件名沿用仓库已有的 `*_internal_test.go` 约定。

`internal/executor/codex/stop_internal_test.go`：

```go
package codex

import (
	"errors"
	"fmt"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
)

// stubStillAlive 把 Kill 换成「已发信号但复核仍存活」的形态。
func stubStillAlive(t *testing.T) {
	t.Helper()
	orig := killProcHost
	killProcHost = func(prochost.Handle) error {
		return fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive)
	}
	t.Cleanup(func() { killProcHost = orig })
}

// TestStopPropagatesStillAlive 验证进程复核失败时，Stop 返回的错误仍能被
// errors.Is 认出 prochost.ErrStillAlive——agentd 侧的人工提示分支全靠它。
// codex 原先在这里就地 emit 一条 progress 然后 return nil，信号到此为止。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := newRunState("t-still-alive", t.TempDir(), t.TempDir())
	r.proc = &Proc{Handle: prochost.Handle{PID: 4242}}
	a.mu.Lock()
	a.runs["t-still-alive"] = r
	a.mu.Unlock()

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
```

`internal/executor/grok/stop_internal_test.go`（grok 没有 `newRunState` 构造函数，`Stop` 只用到 `emitMu` / `cli` / `proc`，直接写字面量）：

```go
package grok

import (
	"errors"
	"fmt"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

func stubStillAlive(t *testing.T) {
	t.Helper()
	orig := killProcHost
	killProcHost = func(prochost.Handle) error {
		return fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive)
	}
	t.Cleanup(func() { killProcHost = orig })
}

// TestStopPropagatesStillAlive 验证 grok 的 Stop 不再把 Kill 错误丢进 `_`。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := &runState{
		taskID: "t-still-alive",
		evCh:   make(chan executor.AdapterEvent, 4),
		proc:   &Proc{Handle: prochost.Handle{PID: 4242}},
	}
	a.mu.Lock()
	a.runs["t-still-alive"] = r
	a.mu.Unlock()

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
```

`internal/executor/claudecode/stop_internal_test.go`（claudecode 有 `a.newRun`，它会**同时登记**运行态并初始化 `stopCh` / `runCtx`，直接用）：

```go
package claudecode

import (
	"errors"
	"fmt"
	"testing"

	"github.com/xushixin/handoff/internal/prochost"
)

func stubStillAlive(t *testing.T) {
	t.Helper()
	orig := killProcHost
	killProcHost = func(prochost.Handle) error {
		return fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive)
	}
	t.Cleanup(func() { killProcHost = orig })
}

// TestStopPropagatesStillAlive 是防回归：claudecode 本来就裸抛 kerr，
// 这条钉住它——将来谁想在这里「顺手把错误咽掉」会当场翻红。
func TestStopPropagatesStillAlive(t *testing.T) {
	stubStillAlive(t)
	a := New(nil)
	r := a.newRun("t-still-alive", t.TempDir(), t.TempDir())
	r.proc = &Proc{Handle: prochost.Handle{PID: 4242}}

	err := a.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
}
```

`internal/executor/opencode/stop_internal_test.go`（复用既有的 `newFakeServer` / `startFakeRun` / `fakeProbe`，写法照 `regression_group_a_test.go:313-320`）：

```go
package opencode

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/prochost"
)

// TestStopPropagatesStillAlive 验证 opencode 的保留-重试机制在**新的** Kill
// 语义下真的会被触发：这条路径过去是休眠的（Kill 从不报「还活着」），
// 所以必须实测，不能假定它一直好着。
func TestStopPropagatesStillAlive(t *testing.T) {
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, "t-still-alive", t.TempDir(), t.TempDir())
	ad.reapInterval = 5 * time.Millisecond
	probe := ad.lookup("t-still-alive").handle.(*fakeProbe)
	probe.setKillErr(fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive))

	err := ad.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
	// 保留分支的前提是 handle.Alive() 为真；若 fakeProbe 默认不存活，
	// Stop 会走「kill 失败但 serve 已自灭」分支返回 nil。此时用该包既有的
	// 存活 setter 把它置为存活（**不要**改生产代码去迁就测试）。
	if ad.lookup("t-still-alive") == nil {
		t.Fatal("kill 报仍存活时应保留运行态，交给 reapRetained 后台重试")
	}
}
```

- [ ] **Step 4: 跑测试确认失败**

Run: `go test ./internal/executor/... -run TestStopPropagatesStillAlive -count=1`
Expected: codex / grok 两家 FAIL（错误被吞 / 被丢），claudecode / opencode 两家 PASS（既有行为本就正确）

- [ ] **Step 5: 改 codex**

把 `internal/executor/codex/adapter.go` 的这段：

```go
	if r.proc != nil {
		if err := r.proc.Kill(); err != nil {
			// B20：回收失败要发事件而非静默
			a.log.Error("codex 进程回收失败", "task", taskID, "cause", err)
			a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
				Text: "警告：codex 执行者进程回收失败，可能残留进程: " + err.Error()})
		}
	}
```

替换为：

```go
	if r.proc != nil {
		if err := r.proc.Kill(); err != nil {
			// 回收失败要上抛而不是就地发事件（B47 修正 B20 的做法）：
			// stopExecutor 在调用本方法**之前**已经 noteStopping，事件通道随时会关，
			// 这里 a.emit 能不能落库是个竞态；而 stopExecutor 侧的
			// AppendEvent + Publish 是确定落库的。用可靠的那条替换不可靠的那条。
			// 提前返回也意味着不 drop 运行态——保留才有机会再回收（与 claudecode 同形）。
			a.log.Error("codex 进程回收失败", "task", taskID, "cause", err)
			return fmt.Errorf("kill codex: %w", err)
		}
	}
```

- [ ] **Step 6: 改 grok**

把 `internal/executor/grok/adapter.go` 的：

```go
	if r.proc != nil {
		_ = r.proc.Kill()
	}
```

替换为：

```go
	if r.proc != nil {
		if kerr := r.proc.Kill(); kerr != nil {
			// 不能丢弃：Kill 现在会在「已发 SIGKILL 但复核仍存活」时返回
			// prochost.ErrStillAlive，丢掉它等于让孤儿进程无声无息（B47）
			a.log.Error("grok 进程回收失败", "task", taskID, "cause", kerr)
			return fmt.Errorf("kill grok: %w", kerr)
		}
	}
```

两处若 `fmt` 尚未 import，补上。

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/executor/... -count=1 -v 2>&1 | tail -40`
Expected: 四家的穿透断言全 PASS，各包既有用例不受影响

**特别注意**：codex / grok 改成提前返回后，原先「无论如何都会执行」的 `r.closeEvents()` / `a.drop(taskID)` 在这条路径上不再执行。既有测试若因此翻红，**先判断是测试假设过紧还是真的回归**，在报告里说明结论——不要直接改断言让它绿。

- [ ] **Step 8: 日志与注释自检**

- codex / grok 两处新分支各有 Error 日志，带 `task` + cause
- 两处各有「为什么」注释：codex 讲清「为什么把 emit 换成上抛」（竞态 vs 确定落库），grok 讲清「为什么不能再丢弃」
- 不要在这两处再写「B20」字样而不说明——B47 是对 B20 做法的**修正**，注释要让下一个读到的人知道这是有意为之
- 三处 `killProcHost` 各带注释说明「为什么需要这个缝」（SIGKILL 不可拦截，失败路径只能靠替换驱动），与同文件里 `startProcHost` 的注释风格一致

- [ ] **Step 9: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/executor/
git commit -m "fix(b47): codex/grok 的 Stop 不再吞掉 Kill 错误，哨兵可穿透"
```

---

### Task 3: `stopExecutor` 把信号送到人眼前

**Files:**
- Modify: `internal/agentd/reconcile.go`
- Test: `internal/agentd/reconcile_test.go`（追加，紧挨既有的两条 `TestStopExecutor*`）

**Interfaces:**
- Consumes: `prochost.ErrStillAlive`（Task 1）、四个 adapter 的穿透（Task 2）
- Produces: `(*Manager).notifyOrphanRisk(taskID, text string)`（包内 helper）

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/reconcile_test.go` 里追加两条。**两条都要**——只测「会发」会让「只对这一种错误发」这个决定失去保护。

沿用本文件既有的脚手架（`chanAdapter` / `newTestManagerWithAds` / `mustCreateTask` / `st.EventsFromAsc`，见 `TestStopExecutorEmitsEventWhenReapFails`），只新增一个可注入 Stop 错误的假 adapter：

```go
// stopErrAdapter 是 Stop 错误可注入的测试 adapter。
// 刻意**不实现** reaper：本组两条用例走的是「Stop 失败且非 ErrTaskNotRunning」
// 这条分支，兜底回收压根不该被触及。
type stopErrAdapter struct {
	chanAdapter
	stopErr error
}

func (a *stopErrAdapter) Stop(string) error { return a.stopErr }

// TestStopExecutorNotifiesOnStillAlive 验证 Stop 报「进程仍存活」时，审核者
// 能在事件流里看到人工提示——B47 的全部意义就在这一条。改动前这里只有一行
// Error 日志进 agentd.log，审核者的终端上什么都不会出现。
func TestStopExecutorNotifiesOnStillAlive(t *testing.T) {
	ad := &stopErrAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		stopErr:     fmt.Errorf("kill codex: %w", prochost.ErrStillAlive),
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	const taskID = "abcdef12-3456-7890-abcd-ef1234567890"
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor(taskID, ad)

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "handoff stop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("进程仍存活应产出指向 handoff stop 的 progress 事件，实际事件: %v", evs)
	}
}

// TestStopExecutorStaysQuietOnOtherErrors 验证其它 Stop 失败**不**发事件：
// 全发等于把审核者淹了，那样这条提示就没人看了。
func TestStopExecutorStaysQuietOnOtherErrors(t *testing.T) {
	ad := &stopErrAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		stopErr:     errors.New("上下文已取消"),
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	const taskID = "abcdef12-3456-7890-abcd-ef1234567891"
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview})
	m.stopExecutor(taskID, ad)

	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress {
			t.Fatalf("非 ErrStillAlive 的失败不应发提示事件，got %s", string(e.Payload))
		}
	}
}
```

补 import：`"github.com/xushixin/handoff/internal/prochost"`（`errors` / `fmt` / `strings` / `executor` / `proto` 本文件已有）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestStopExecutor -count=1`
Expected: 第一条 FAIL（没有任何 progress 事件），第二条 PASS

- [ ] **Step 3: 抽出 helper 并接上判定**

在 `internal/agentd/reconcile.go` 里新增：

```go
// notifyOrphanRisk 追加一条「executor 可能残留」的 progress 事件并广播。
//
// 参数：
//   - taskID: 目标任务
//   - text: 面向审核者的正文；给的必须是「下一步做什么」而不是「出了什么错」
//
// 注意：
//   - 追加失败只记日志、不返回错误：调用方全都处在归档/中止的收尾路径上，
//     那件事本身已经达成，不该因为发不出提示而中断
func (m *Manager) notifyOrphanRisk(taskID, text string) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if err != nil {
		m.log.Error("追加 executor 残留提示事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
	m.log.Info("已向审核者发出 executor 残留提示", "task", taskID)
}
```

把 `stopExecutor` 里这段：

```go
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：保持既有语义（只记日志），
		// 兜底回收对它无意义——真去 kill 进程反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		return
	}
```

改为：

```go
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：兜底回收对它无意义——
		// 真去 kill 进程反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		// 唯独「已发 SIGKILL 但复核仍存活」要惊动人：这是唯一一种不提示就会
		// 留下长期孤儿的失败（B20 现场存活了 11.5 小时，正是因为完全静默）。
		// 其余 Stop 失败五花八门（ctx 取消、内部状态不一致），全发事件等于
		// 把审核者淹了，那样这条提示就没人看了。
		if errors.Is(err, prochost.ErrStillAlive) {
			m.notifyOrphanRisk(taskID, fmt.Sprintf(
				"executor 进程可能残留（已发 SIGKILL 但复核仍存活），"+
					"请先 handoff status 确认，再 handoff stop %s 回收（原因：%v）", taskID, err))
		}
		return
	}
```

并把下面 `Reap` 失败那处的 `AppendEvent` + `Publish` 也改为调用 `m.notifyOrphanRisk(taskID, fmt.Sprintf(...))`，**保留它原有的文案**（两条提示的成因不同，措辞不该被统一掉）。

补 import：`"github.com/xushixin/handoff/internal/prochost"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 两条新用例都 PASS，既有用例不受影响

- [ ] **Step 5: 变异检验（必做）**

把 Step 3 里新加的 `if errors.Is(err, prochost.ErrStillAlive) { ... }` 整块注释掉。

Run: `go test ./internal/agentd/ -run TestStopExecutorNotifiesOnStillAlive -count=1`
Expected: **FAIL**。原样记进报告。恢复后 `go test ./... -count=1 && git diff --exit-code`。

- [ ] **Step 6: 日志与注释自检**

- `notifyOrphanRisk` 成功路径 Info、失败路径 Error，都带 `task`
- `notifyOrphanRisk` 有 doc（含「为什么追加失败不返回错误」）
- 新判定分支有「为什么只对这一种错误发事件」的注释，并保留原注释里「兜底回收对它无意义」那半句——那是另一个独立的 why，别一起删掉
- 事件文案给的是**下一步做什么**（`handoff status` → `handoff stop`），不是一句「出错了」

- [ ] **Step 7: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/agentd/
git commit -m "fix(b47): stopExecutor 对进程仍存活补人工提示事件"
```

---

### Task 4: B41 修测试脚手架竞态

**Files:**
- Modify: `internal/executor/codex/appserver_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `closeFakeConns(t *testing.T, srv *httptest.Server)`（签名变更）

- [ ] **Step 1: 先证伪（必做，顺序不能调换）**

在 `startFakeServer` 的 handler 里，`registerFakeConn(srv, conn)` **之前**注入：

```go
		time.Sleep(200 * time.Millisecond) // 临时：放大 registerFakeConn 的登记窗口
```

Run: `go test ./internal/executor/codex/ -run TestPendingCallsFailWhenConnectionDies -count=3 -v`
Expected: **稳定 FAIL**，且失败信息是「挂起请求永久悬挂」。把输出原样记进报告。

**若不翻红，说明 spec §3.1 的诊断错了——停下来报告这个事实，不许绕过去直接改。** 诊断错了就说明真正的根因还没找到，改了也只是把 flake 藏起来。

- [ ] **Step 2: 改 `closeFakeConns`（保留注入的延迟）**

把 `closeFakeConns` 替换为：

```go
// closeFakeConns 用**异常状态码**关闭一个假服务端的全部连接，模拟服务端非预期死亡。
//
// 为什么不用 StatusNormalClosure：本辅助函数服务的测试验的是「连接意外死掉时
// 挂起请求必须以错误终结、OnClosed 必须收到非 nil err」——正常关闭会把它退化
// 成一个优雅停机测试，两条路径（主动 Close 传 nil / 被动断线传 err）就分不开了。
//
// 为什么要先等登记完成：codex.Dial 在**客户端**握手完成即返回，而
// registerFakeConn 跑在**服务端 handler goroutine** 里、位于 websocket.Accept
// 之后，两者没有任何同步。测试 goroutine 若先跑到这里，登记表还是空的——
// 一条连接都关不掉，被测的挂起请求于是不会以错误终结，最后撞上用例自己的
// 3s 超时，报成「挂起请求永久悬挂」。这正是 B41 那个偶发 flake 的成因。
//
// 为什么等不到要 t.Fatal 而不是静默返回：「一条连接都没建立」意味着测试前提
// 已经被破坏，此时继续跑，后面的断言验的就不是它以为在验的东西。
func closeFakeConns(t *testing.T, srv *httptest.Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		fakeConnsMu.Lock()
		conns := append([]*websocket.Conn(nil), fakeConns[srv]...)
		fakeConnsMu.Unlock()
		if len(conns) > 0 {
			for _, c := range conns {
				_ = c.Close(websocket.StatusInternalError, "fake server died")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("等待 2s 仍无已登记连接：服务端 handler 未执行到 registerFakeConn，测试前提被破坏")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

更新唯一的调用点（`appserver_test.go:334`）为 `closeFakeConns(t, srv)`。

- [ ] **Step 3: 确认修法有效（延迟仍在）**

Run: `go test ./internal/executor/codex/ -run TestPendingCallsFailWhenConnectionDies -count=3 -v`
Expected: **PASS**。原样记进报告——「注入延迟仍在却已转绿」是这次修法有效的直接证据。

- [ ] **Step 4: 移除注入的延迟并复跑**

删掉 Step 1 注入的 `time.Sleep`。

Run: `go test -race ./internal/executor/codex/ -count=20`
Expected: 全绿。flake 单跑一次不算数，`-count=20` 是本条的验收线。

- [ ] **Step 5: 注释自检**

- `closeFakeConns` 的 doc 讲清竞态成因（客户端握手 vs 服务端 handler 无同步）与「为什么等不到要 Fatal」
- 不要在代码里留任何 Step 1 注入的临时代码；用 `git diff` 确认

- [ ] **Step 6: 跑闸门并提交**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
git add internal/executor/codex/appserver_test.go
git commit -m "fix(b41): closeFakeConns 等待连接登记，消除 codex 测试脚手架竞态"
```

---

### Task 5: B48 日志降级、全套闸门与真机烟测

**Files:**
- Modify: `internal/localsync/localsync.go`

**Interfaces:**
- Consumes: 无
- Produces: 无（终结 task）

- [ ] **Step 1: 降级重复日志**

把 `internal/localsync/localsync.go` 里这段：

```go
	if _, stderr, ferr := run(ctx, o.LocalRepo, "fetch", o.RemoteURL, refspec); ferr != nil {
		log().Error("本地同步失败", "local", o.LocalRepo, "remote", o.RemoteURL, "branch", o.Branch,
			"stderr", strings.TrimSpace(stderr), "cause", ferr)
```

改为：

```go
	if _, stderr, ferr := run(ctx, o.LocalRepo, "fetch", o.RemoteURL, refspec); ferr != nil {
		// 降级为 Debug（B48）：这段 git 原文已经原样进了下面的返回值，
		// cmd/wait.go 会把它打给人看。库层再 Error 一遍，审核者就会在 stderr 上
		// 紧挨着看到同一段报错的两份副本，真正的排障信息被自己的副本淹没。
		// 与仓库既有纪律一致（internal/store 全层不打日志，靠 %w 带上下文、
		// 由调用方带业务上下文记录）。降级而非删除：agentd 侧若将来复用本库，
		// Debug 仍留得住线索。
		log().Debug("本地同步失败", "local", o.LocalRepo, "remote", o.RemoteURL, "branch", o.Branch,
			"stderr", strings.TrimSpace(stderr), "cause", ferr)
```

**返回值那行一个字都不要动**——`cmd/wait.go` 那份人可读输出的内容全靠它。

- [ ] **Step 2: 跑全套六条闸门**

逐条跑，把实际输出记进报告：

```bash
gofmt -l .
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./cmd/ ./internal/agentd/ ./internal/store/ ./internal/prochost/ ./internal/executor/codex/ -count=1
GOOS=windows GOARCH=amd64 go build ./...
```

再补一条 B41 的专项：

```bash
go test -race ./internal/executor/codex/ -count=20
```

- [ ] **Step 3: 真机烟测（隔离实例）**

本次改的是**回收路径**，它跑在每一次 `handoff done` / `handoff stop` 上。单测用的是桩，所以必须真跑一次确认没把正常路径改坏。

**红线**：不得启停/覆盖监听 7777 的 agentd，不得碰 `~/.handoff/`（含 `config.yaml`）。验收实例必须用**独立端口 + 独立 DataDir + 独立编译的二进制 + 独立仓库副本**，配置自己写一份、每条命令带 `--config <你自己的路径>`；清理时**只按验收二进制的完整路径精确匹配**来 kill，**绝不 `pkill -f agentd`**。

依次验证并记录实际输出：

1. 起隔离实例，派一个最小任务（随便写个一句话的 plan），等它进 `waiting_review`
2. `handoff done` → 必须成功归档；查该实例的 agentd 日志，应能看到 **Task 1 新增的**「回收完成，已确认进程组退出」这条 Info（**这是复核真的跑了、且走的是成功路径的直接证据**）
3. 再派一个任务，中途 `handoff stop` → 必须成功中止，日志同样应有那条复核成功日志
4. 确认两次都**没有**出现「executor 进程可能残留」提示事件（正常路径不该惊动人）
5. 清理隔离实例，确认 7777 那个 agentd 全程未被触碰（`handoff status --target ...` 或直接看进程仍在）

- [ ] **Step 4: 提交**

```bash
git add internal/localsync/localsync.go
git commit -m "fix(b48): 同步失败的 git 原文只由呈现层打印一次"
```

---

## 完工报告要包含

1. 五个 task 各自的 commit sha 与一句话说明。
2. **三处证据**，各自贴原文：
   - Task 1 Step 5 变异检验：注入后 FAIL、恢复后 PASS + `git diff --exit-code`
   - Task 3 Step 5 变异检验：同上
   - Task 4 Step 1/3 证伪流程：注入延迟后**稳定 FAIL** 的原文、修法生效后（延迟仍在）**转绿**的原文
3. 六条闸门 + `-count=20` 专项各自的实际输出。
4. 真机烟测五项的实际输出，**特别是第 2 步那条「已确认进程组退出」日志的原文**。
5. Task 2 Step 1 的四家现状核对结果（与计划里的表是否一致），以及 Step 6 里若有既有测试翻红的判断结论。
6. 任何偏离计划的地方及原因。**没有偏离就明确写「无偏离」**，不要含糊带过。
