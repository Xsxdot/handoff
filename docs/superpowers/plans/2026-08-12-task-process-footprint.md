# 任务进程足迹（B69 + B70）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentd 认识、能回收、能看见每个任务在机器上占用的进程足迹。

**Architecture:** `internal/prochost` 新增一对孪生原语——`Footprint`（只数）与 `Sweep`（回收），共用同一份成员枚举与身份校验，保证「数出来的」与「会被杀的」永远是同一批。身份校验以**存活锁状态**区分「组长是我们的 shim」与「pgid 被复用的冒名者」。agentd 侧把清扫挂进既有的「executor 已不在」收尾路径，把计数接进 `status` 与新命令 `handoff footprint`。

**Tech Stack:** Go 1.26.1、`golang.org/x/sys/unix`（darwin sysctl；当前是 indirect 依赖，本计划将其提升为 direct，不引入新模块）、cobra（CLI）。

**Spec:** [2026-08-12-task-process-footprint-design.md](../specs/2026-08-12-task-process-footprint-design.md)

## Global Constraints

- **平台原语一律不得 fork**：禁止调用 `ps`/`lsof` 等外部命令。这套代码必须在机器已经 fork 不动时仍可用（spec §3.3）。
- **非 unix 平台一律 no-op**，沿用 `platform_other.go` 既有模式；`GOOS=windows go build ./...` 必须通过。
- **`prochost.Kill` 的契约不得修改**，其既有用例（含 `TestKillSkipsWhenLockFree`）必须保持全绿（spec §3.1）。
- **新增的跨版本协议字段一律 `指针 + omitempty`**：nil 表示「对端没给这个字段」（老 agentd），与「确实是 0」是两回事。这是 `ActiveTask.Watchers`、`StatusResp.Update` 已确立的房规。
- **日志用 `log()`（prochost）/ `m.log`、`log`（agentd）**，禁止 `fmt.Printf`。
- **注释用中文**，新文件写职责与边界，导出方法写参数/返回/注意事项。
- 每个 task 结束时 `gofmt -l .` 必须无输出。

## 与 spec 的两处修正（实现前必读）

读码后发现 spec 两处与代码现实不符，本计划按修正后的版本执行：

**修正一 · 进程枚举原语共用，只有「读上限」独立。** spec §3.3 写「两类能力实现分离，不共用代码」。实际上「按 pgid 枚举组成员」与「按 uid 统计总数」在两个平台上都源于**同一次枚举**（darwin 一次 `KERN_PROC_ALL`、linux 一次 `/proc` 遍历）。拆成两份等于把同一次系统调用做两遍。本计划共用 `enumProcs()`，仅「读上限」（`procLimit()`）独立——那确实是另一回事。

**修正二 · 清扫有两个调用点，不是一个。** spec §3.4 写「无新增调用点，全挂在 `reconcileExecutorGone` 内」。实际上 `RecoverOnStartup`（[watchdog.go:210](../../../internal/agentd/watchdog.go)）对 **`waiting_review` 且 executor 已不在**的任务**刻意不调用** `reconcileExecutorGone`（那条分支只 `kept++` 后 continue，理由是待审核终态不该被追加噪音事件）。而事故当天两个任务最终**正是停在 `waiting_review`**——这恰恰是最需要清扫的形态。

清扫与那条分支的理由不冲突：它不追加事件、不迁移状态，只回收资源。因此本计划抽出一个 `SweepTaskProcs` 助手，从**两处**调用：`reconcileExecutorGone` 内（覆盖三个运行时调用点）与 `RecoverOnStartup` 的 `waiting_review` 保持分支。

---

### Task 1: 平台原语——进程枚举与每 uid 上限

**Files:**
- Create: `internal/prochost/procenum.go`（平台无关类型与文档）
- Create: `internal/prochost/procenum_darwin.go`
- Create: `internal/prochost/procenum_linux.go`
- Create: `internal/prochost/procenum_other.go`
- Test: `internal/prochost/procenum_test.go`
- Modify: `go.mod`（`golang.org/x/sys` 由 indirect 提升为 direct）

**Interfaces:**
- Consumes: 无（最底层）
- Produces:
  - `type procEntry struct { PID int; PGID int; StartedAt int64 }`（`StartedAt` 为 unix 纳秒）
  - `func enumProcs() ([]procEntry, error)` —— 当前 uid 的全部进程
  - `func procLimit() (int, error)` —— 每 uid 进程数上限
  - `var errNotSupported = errors.New("本平台不支持进程枚举")`

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/procenum_test.go`：

```go
package prochost

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestEnumProcsFindsSelf 验证枚举能找到本进程，且 pgid 与内核一致。
// 这是整套足迹判据的地基：pgid 读错，规则一二三全部失去意义。
func TestEnumProcsFindsSelf(t *testing.T) {
	procs, err := enumProcs()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if !errors.Is(err, errNotSupported) {
			t.Fatalf("非 darwin/linux 应返回 errNotSupported，got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("enumProcs 失败: %v", err)
	}
	self := os.Getpid()
	wantPGID, err := syscall.Getpgid(self)
	if err != nil {
		t.Fatalf("Getpgid 失败: %v", err)
	}
	for _, p := range procs {
		if p.PID != self {
			continue
		}
		if p.PGID != wantPGID {
			t.Fatalf("本进程 pgid 读错：got %d, want %d", p.PGID, wantPGID)
		}
		// 本进程必然启动于「现在」之前、且不早于一年前——粗窗口足以抓出
		// 单位换算错误（秒当纳秒会落到 1970，jiffies 未换算会落到未来）
		now := time.Now().UnixNano()
		if p.StartedAt <= 0 || p.StartedAt > now || p.StartedAt < now-int64(365*24*time.Hour) {
			t.Fatalf("本进程 StartedAt 不合理：%d（now=%d）", p.StartedAt, now)
		}
		return
	}
	t.Fatalf("枚举结果里没有本进程 pid=%d（共 %d 条）", self, len(procs))
}

// TestProcLimitPositive 验证能读到每 uid 上限。
func TestProcLimitPositive(t *testing.T) {
	n, err := procLimit()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if !errors.Is(err, errNotSupported) {
			t.Fatalf("非 darwin/linux 应返回 errNotSupported，got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("procLimit 失败: %v", err)
	}
	if n <= 0 {
		t.Fatalf("上限应为正数，got %d", n)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestEnumProcs|TestProcLimit' -v`
Expected: 编译失败，`undefined: enumProcs`

- [ ] **Step 3: 写平台无关类型与文档**

创建 `internal/prochost/procenum.go`：

```go
// procenum.go —— 进程枚举与上限的平台无关契约。
//
// 职责：
//   - 定义 procEntry：一个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - 声明两个平台原语的契约：enumProcs（当前 uid 的全部进程）、procLimit（每 uid 上限）
//
// 边界：
//   - 只负责「读」，不发任何信号、不做任何判断：谁属于哪个任务是 footprint.go 的事
//   - **实现一律不得 fork**（禁止 ps/lsof）：这套代码要在机器已经 fork 不动的时候
//     仍然可用，否则它会在最需要它的那一刻恰好失灵——2026-08-12 devbox 整机 fork
//     瘫痪时，所有基于 exec 的诊断手段全部失效，正是这条约束的由来
//   - 非 darwin/linux 一律返回 errNotSupported，调用方据此降级，不猜值
package prochost

import "errors"

// errNotSupported 表示本平台没有进程枚举实现。
//
// 为什么要显式区分而不是返回空集：空集意味着「确实一个进程都没有」，
// 与「这个平台我们看不了」是两回事——后者必须让调用方降级为「未知」，
// 而不是渲染出一个 0 让人以为足迹是空的。
var errNotSupported = errors.New("本平台不支持进程枚举")

// procEntry 是一个进程的足迹相关属性。
//
// StartedAt 为 unix 纳秒，两个平台都归一到这个单位——身份校验要把成员的启动
// 时刻与 shim 的启动时刻直接比较，单位不统一这条判据就是错的。
type procEntry struct {
	PID       int
	PGID      int
	StartedAt int64
}
```

- [ ] **Step 4: 写 darwin 实现**

创建 `internal/prochost/procenum_darwin.go`：

```go
//go:build darwin

// procenum_darwin.go —— darwin 的进程枚举实现（sysctl KERN_PROC，不 fork）。
package prochost

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// enumProcs 用 sysctl KERN_PROC_UID 取当前 uid 的全部进程。
//
// 返回：
//   - 每个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - sysctl 失败时返回错误；不返回 errNotSupported（本平台是支持的）
//
// 注意：kinfo_proc 的 p_starttime 是 struct timeval（墙钟），直接换算 unix 纳秒。
func enumProcs() ([]procEntry, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		log().Error("sysctl 枚举进程失败", "uid", os.Getuid(), "cause", err)
		return nil, fmt.Errorf("sysctl kern.proc.uid: %w", err)
	}
	out := make([]procEntry, 0, len(kps))
	for i := range kps {
		st := kps[i].Proc.P_starttime
		out = append(out, procEntry{
			PID:       int(kps[i].Proc.P_pid),
			PGID:      int(kps[i].Eproc.Pgid),
			StartedAt: int64(st.Sec)*int64(time.Second) + int64(st.Usec)*int64(time.Microsecond),
		})
	}
	log().Debug("进程枚举完成", "uid", os.Getuid(), "count", len(out))
	return out, nil
}

// procLimit 读 kern.maxprocperuid（每 uid 进程数上限）。
func procLimit() (int, error) {
	n, err := unix.SysctlUint32("kern.maxprocperuid")
	if err != nil {
		log().Error("读 kern.maxprocperuid 失败", "cause", err)
		return 0, fmt.Errorf("sysctl kern.maxprocperuid: %w", err)
	}
	return int(n), nil
}
```

- [ ] **Step 5: 写 linux 实现**

创建 `internal/prochost/procenum_linux.go`：

```go
//go:build linux

// procenum_linux.go —— linux 的进程枚举实现（读 /proc，不 fork）。
package prochost

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// bootTimeNano 读 /proc/stat 的 btime，返回系统启动时刻（unix 纳秒）。
//
// 为什么需要它：/proc/<pid>/stat 的 starttime 是「自开机以来的时钟嘀嗒数」，
// 必须叠加开机时刻才能变成可与 shim 启动时刻直接比较的绝对时间。
func bootTimeNano() (int64, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, fmt.Errorf("读 /proc/stat: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		sec, perr := strconv.ParseInt(strings.TrimSpace(line[len("btime "):]), 10, 64)
		if perr != nil {
			return 0, fmt.Errorf("解析 btime: %w", perr)
		}
		return sec * int64(time.Second), nil
	}
	return 0, fmt.Errorf("/proc/stat 里没有 btime 行")
}

// clockTick 是 linux 的 USER_HZ。内核对 /proc 恒定按 100 导出，与 CONFIG_HZ 无关。
const clockTick = 100

// enumProcs 遍历 /proc 取当前 uid 的全部进程。
//
// 返回：
//   - 每个进程的 pid / pgid / 启动时刻（unix 纳秒）
//   - /proc 不可读时返回错误；单个进程读失败一律跳过（进程随时会消失，那是常态）
func enumProcs() ([]procEntry, error) {
	boot, err := bootTimeNano()
	if err != nil {
		log().Error("读系统启动时刻失败", "cause", err)
		return nil, err
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		log().Error("读 /proc 失败", "cause", err)
		return nil, fmt.Errorf("读 /proc: %w", err)
	}
	uid := os.Getuid()
	out := make([]procEntry, 0, 256)
	for _, e := range ents {
		pid, cerr := strconv.Atoi(e.Name())
		if cerr != nil {
			continue // 非数字目录不是进程
		}
		fi, serr := os.Stat("/proc/" + e.Name())
		if serr != nil {
			continue // 进程刚消失：常态，不是错误
		}
		sys, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || int(sys.Uid) != uid {
			continue // 只要当前 uid 的
		}
		pgid, start, perr := readStat(pid, boot)
		if perr != nil {
			continue // 同上：读到一半进程没了
		}
		out = append(out, procEntry{PID: pid, PGID: pgid, StartedAt: start})
	}
	log().Debug("进程枚举完成", "uid", uid, "count", len(out))
	return out, nil
}

// readStat 解析 /proc/<pid>/stat，取 pgrp（字段 5）与 starttime（字段 22）。
//
// 注意：字段 2 是 comm，可能含空格与右括号（如 "(my prog)"），因此必须从
// **最后一个** ')' 之后开始切分，不能直接按空格分割整行。
func readStat(pid int, bootNano int64) (pgid int, startedAt int64, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, err
	}
	s := string(b)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, 0, fmt.Errorf("stat 格式异常 pid=%d", pid)
	}
	// idx+2 起是字段 3（state）；fields[0]=state, fields[2]=pgrp, fields[19]=starttime
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("stat 字段不足 pid=%d, got %d", pid, len(fields))
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, fmt.Errorf("解析 pgrp pid=%d: %w", pid, err)
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 starttime pid=%d: %w", pid, err)
	}
	return pgid, bootNano + ticks*int64(time.Second)/clockTick, nil
}

// procLimit 读当前进程的 RLIMIT_NPROC 软上限（每 uid 可创建进程数）。
func procLimit() (int, error) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NPROC, &rl); err != nil {
		log().Error("读 RLIMIT_NPROC 失败", "cause", err)
		return 0, fmt.Errorf("getrlimit RLIMIT_NPROC: %w", err)
	}
	return int(rl.Cur), nil
}
```

- [ ] **Step 6: 写 other 实现**

创建 `internal/prochost/procenum_other.go`：

```go
//go:build !darwin && !linux

// procenum_other.go —— 非 darwin/linux 的空实现。
//
// 一律返回 errNotSupported 而不是空集：调用方必须据此降级为「未知」，
// 而不是渲染出一个 0 让人误以为足迹是空的（见 procenum.go 的 why）。
package prochost

func enumProcs() ([]procEntry, error) { return nil, errNotSupported }

func procLimit() (int, error) { return 0, errNotSupported }
```

- [ ] **Step 7: 提升 x/sys 为直接依赖并运行测试**

Run:
```bash
go mod tidy && go test ./internal/prochost/ -run 'TestEnumProcs|TestProcLimit' -v
```
Expected: PASS（两个用例），且 `go.mod` 中 `golang.org/x/sys` 不再带 `// indirect`

- [ ] **Step 8: 交叉编译自检**

Run:
```bash
GOOS=windows go build ./... && GOOS=linux go build ./... && GOOS=darwin go build ./...
```
Expected: 三条均无输出

- [ ] **Step 9: 加关键节点日志**

本 task 的日志已内联在 Step 4–6 的实现里，逐条核对：
- `enumProcs` 成功路径打 Debug（含 uid 与 count）——枚举是高频调用，Info 会淹没日志
- `enumProcs` / `procLimit` 的每条错误分支打 Error，带 cause 与可定位上下文（uid / sysctl 名）
- linux 侧「单个进程读失败」**刻意不打日志**：进程随时消失是常态，逐条打会在忙碌机器上刷屏

- [ ] **Step 10: 加注释自检**

核对：4 个新文件均有文件头（职责 + 边界，含「不得 fork」的 why）；`enumProcs`/`procLimit`/`readStat`/`bootTimeNano` 均有文档注释；`readStat` 的 comm 含空格陷阱、`clockTick` 恒为 100、`errNotSupported` 为何不返回空集——三处「为什么」注释齐全。

- [ ] **Step 11: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(prochost): 进程枚举与每 uid 上限的平台原语（不 fork）"
```

---

### Task 2: 身份校验判据（纯逻辑）

**Files:**
- Create: `internal/prochost/footprint.go`
- Test: `internal/prochost/footprint_test.go`

**Interfaces:**
- Consumes: `procEntry`（Task 1）、`Handle`（现有，`StartedAt` 字段在 Task 3 加，本 task 先按已有该字段编写）
- Produces:
  - `type Verdict string` 与 `VerdictOK` / `VerdictLeaderReuse` / `VerdictNoCredential`
  - `func classify(h Handle, procs []procEntry, lockHeld bool) (members []int, v Verdict)`

**注意：** 本 task 依赖 `Handle.StartedAt`，而该字段在 Task 3 才加。执行本 task 时**先把字段加上**（`StartedAt int64 \`json:"started_at,omitempty"\``），Task 3 只负责给它赋值与 `Footprint` 封装。

- [ ] **Step 1: 写失败测试**

创建 `internal/prochost/footprint_test.go`：

```go
package prochost

import (
	"sort"
	"testing"
)

// 判据测试的固定基准：shim pid=100，启动于 t0。
const (
	testShimPID = 100
	t0          = int64(1_000_000)
)

func h() Handle { return Handle{PID: testShimPID, StartedAt: t0} }

// TestClassifyLockHeldCountsGroup 锁仍被持有 ⇒ 组长就是我们的 shim，正常计数。
//
// 这条守的是 status 的 per-task 计数：若规则一不看锁状态、一律把
// 「存在 pid==pgid 的活进程」判成复用，所有**运行中**的任务都会被误判，
// per-task 进程数将永远取不到值。
func TestClassifyLockHeldCountsGroup(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0},     // shim 自己（组长）
		{PID: 101, PGID: 100, StartedAt: t0 + 1}, // executor
		{PID: 102, PGID: 100, StartedAt: t0 + 2}, // 孙进程
		{PID: 200, PGID: 200, StartedAt: t0},     // 无关进程
	}
	got, v := classify(h(), procs, true)
	if v != VerdictOK {
		t.Fatalf("锁被持有时应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{100, 101, 102})
}

// TestClassifyLeaderReuseAborts 锁已释放 + 组内有活的 pid==pgid ⇒ pgid 被复用，整组放弃。
func TestClassifyLeaderReuseAborts(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0 + 9999}, // 冒名者：pid 被复用且当了组长
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}
	got, v := classify(h(), procs, false)
	if v != VerdictLeaderReuse {
		t.Fatalf("应判定 leader_reuse，got %s", v)
	}
	if len(got) != 0 {
		t.Fatalf("判定复用时必须返回空集（一个都不能碰），got %v", got)
	}
}

// TestClassifyNoCredential StartedAt 缺失（老 proc.json）⇒ 凭据不全，放弃。
func TestClassifyNoCredential(t *testing.T) {
	procs := []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}
	got, v := classify(Handle{PID: testShimPID, StartedAt: 0}, procs, false)
	if v != VerdictNoCredential {
		t.Fatalf("应判定 no_credential，got %s", v)
	}
	if len(got) != 0 {
		t.Fatalf("凭据不全时必须返回空集，got %v", got)
	}
}

// TestClassifyExcludesMemberStartedBeforeShim 成员启动早于 shim ⇒ 排除（规则三双保险）。
func TestClassifyExcludesMemberStartedBeforeShim(t *testing.T) {
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 - 1}, // 比 shim 还早：不可能是它的后代
	}
	got, v := classify(h(), procs, false)
	if v != VerdictOK {
		t.Fatalf("应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{101})
}

// TestClassifyDeadLeaderNormal 锁已释放、无复用者 ⇒ 正常返回残留后代。
func TestClassifyDeadLeaderNormal(t *testing.T) {
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 + 2},
		{PID: 300, PGID: 300, StartedAt: t0},
	}
	got, v := classify(h(), procs, false)
	if v != VerdictOK {
		t.Fatalf("应为 ok，got %s", v)
	}
	assertMembers(t, got, []int{101, 102})
}

func assertMembers(t *testing.T, got, want []int) {
	t.Helper()
	sort.Ints(got)
	if len(got) != len(want) {
		t.Fatalf("成员数不符：got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("成员不符：got %v, want %v", got, want)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run TestClassify -v`
Expected: 编译失败，`undefined: classify`

- [ ] **Step 3: 写实现**

创建 `internal/prochost/footprint.go`：

```go
// footprint.go —— 任务进程足迹的身份校验与两个孪生原语。
//
// 职责：
//   - classify：给定 Handle 与一次进程快照，判定「哪些进程属于这个任务」
//   - Footprint（只数）/ Sweep（回收）：共用 classify，保证数出来的与被杀的是同一批
//
// 边界：
//   - 不负责枚举进程（那是 procenum_*.go），不负责判定 shim 存活（那是 Alive）
//   - 不修改任何任务状态、不发事件：那是 agentd 的事
//   - **不替代 Kill**：Kill 杀活着的执行者、前提是存活锁被持有；Sweep 收已死执行者
//     的残留、前提是锁已释放。两者风险模型不同，不得互相代劳（见 Sweep 的文档）
package prochost

// Verdict 是一次足迹判定的结论。
//
// 为什么要三态而不是 bool：判不出结论时猜一个值就是制造假阳性，而一条会说谎的
// 诊断比没有更糟——因为你会信它。与 ActiveTask.Live 的三态是同一条纪律。
type Verdict string

const (
	// VerdictOK 身份校验通过，members 可信。
	VerdictOK Verdict = "ok"
	// VerdictLeaderReuse pgid 已被复用（组长位置被无关进程占据），整组放弃。
	VerdictLeaderReuse Verdict = "leader_reuse"
	// VerdictNoCredential 凭据不全（Handle.StartedAt 缺失，多见于升级前写下的
	// proc.json），无法做时间下界校验，放弃。
	VerdictNoCredential Verdict = "no_credential"
)

// classify 判定进程快照中哪些成员属于 h 所代表的任务。
//
// 参数：
//   - h: 任务的进程句柄；h.PID 既是 shim pid 也是进程组 id（Setsid 保证）
//   - procs: 一次进程快照（当前 uid 的全部进程）
//   - lockHeld: h.LockPath 上的存活锁是否仍被持有，即 shim 是否还活着
//
// 返回：
//   - members: 通过校验的成员 pid；判定为放弃时**必然为空**
//   - v: 判定结论
//
// 三条规则（spec §3.2）：
//
// 规则一（组长身份判定，以存活锁为准，一票否决）：
//   - 锁仍被持有 ⇒ 组长就是我们的 shim，pgid 不可能被复用（锁由内核在进程死亡时
//     释放，这个判据本身免疫 pid 复用），正常计数
//   - 锁已释放 ⇒ 组长已死；此时组内若仍有活的 pid==pgid==h.PID，那必然是内核把
//     这个 pid 分配给了新进程且它成了组长，即 pgid 被复用 ⇒ 整组放弃
//
// 规则二（会话封闭性，无需代码）：过了规则一之后，组内成员只可能是我们的后代。
// 依据：shim 调用过 setsid，该进程组属于 shim 独有的会话；setpgid(2) 要求目标
// 进程组与调用者同会话，会话外的进程加不进来。
//
// 规则三（时间下界，双保险）：成员启动时刻必须 ≥ h.StartedAt，否则排除。规则二
// 理论上已经封闭，这条防的是理论之外——代价是漏杀而非误杀。
func classify(h Handle, procs []procEntry, lockHeld bool) (members []int, v Verdict) {
	if h.PID <= 0 || h.StartedAt <= 0 {
		return nil, VerdictNoCredential
	}
	if !lockHeld {
		for _, p := range procs {
			if p.PID == h.PID && p.PGID == h.PID {
				// 组长位置被人占着，而我们的 shim 已经死了 ⇒ pid 被复用
				return nil, VerdictLeaderReuse
			}
		}
	}
	for _, p := range procs {
		if p.PGID != h.PID {
			continue
		}
		if p.StartedAt < h.StartedAt {
			continue // 规则三：比 shim 还早的不可能是它的后代
		}
		members = append(members, p.PID)
	}
	return members, VerdictOK
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/prochost/ -run TestClassify -v`
Expected: PASS（5 个用例）

- [ ] **Step 5: 加关键节点日志**

`classify` 是纯函数、无 I/O、无外部调用，**刻意不打日志**——它每次调用都在 `Footprint`/`Sweep` 内部，由后者在边界上统一记录入参与结论（Task 3、4）。在这里打等于同一件事记两遍，且 `classify` 会被 status 高频调用。

把这条判断写成文件内注释，避免后来者以为是漏了：在 `classify` 文档注释末尾追加一行

```go
// 注意：本函数刻意不打日志——调用方（Footprint/Sweep）在边界上统一记录入参与
// 结论；这里再记一遍等于同一件事写两次，且 status 会高频调用它。
```

- [ ] **Step 6: 加注释自检**

核对：`footprint.go` 有文件头（职责 + 边界，含「不替代 Kill」）；`Verdict` 三个常量各有一行说明；`classify` 文档注释完整覆盖参数/返回/三条规则的 why。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(prochost): 足迹身份校验判据 classify（三规则，锁状态区分组长）"
```

---

### Task 3: `Handle.StartedAt` 落盘与 `Footprint`

**Files:**
- Modify: `internal/prochost/prochost.go`（`Handle` 加字段、`Start` 记录、新增 `Footprint`）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: `classify`（Task 2）、`enumProcs`（Task 1）、`Alive`（现有）
- Produces:
  - `Handle.StartedAt int64`
  - `func Footprint(h Handle) (members []int, v Verdict, err error)`
  - `var enumProcsFn = enumProcs`（测试缝）
  - `var aliveFn`（现有测试缝，复用）

- [ ] **Step 1: 写失败测试**

在 `internal/prochost/footprint_test.go` 追加：

```go
// stubEnum 把进程枚举换成固定快照。
func stubEnum(t *testing.T, procs []procEntry, err error) {
	t.Helper()
	orig := enumProcsFn
	enumProcsFn = func() ([]procEntry, error) { return procs, err }
	t.Cleanup(func() { enumProcsFn = orig })
}

// TestFootprintUsesLockState 验证 Footprint 把存活锁状态正确喂给 classify。
func TestFootprintUsesLockState(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0},
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}
	stubEnum(t, procs, nil)

	stubAlive(t, true) // shim 活着
	got, v, err := Footprint(h())
	if err != nil || v != VerdictOK || len(got) != 2 {
		t.Fatalf("锁被持有：want ok/2 成员，got v=%s members=%v err=%v", v, got, err)
	}

	stubAlive(t, false) // shim 死了，且组长位置有活进程 ⇒ 复用
	_, v, err = Footprint(h())
	if err != nil || v != VerdictLeaderReuse {
		t.Fatalf("锁已释放：want leader_reuse，got v=%s err=%v", v, err)
	}
}

// TestStartRecordsStartedAt 验证 Start 落下的 Handle 带得到启动时刻。
//
// 这条是整个时间下界判据的源头：StartedAt 恒为 0，规则三永远降级为 no_credential，
// 清扫功能等于没上线。
func TestStartRecordsStartedAt(t *testing.T) {
	if !LockSupported() {
		t.Skip("本平台不支持文件锁")
	}
	dir := t.TempDir()
	spec := Spec{
		Argv:     []string{"/bin/sh", "-c", "sleep 5"},
		Dir:      dir,
		Stdout:   filepath.Join(dir, "out.log"),
		Stderr:   filepath.Join(dir, "err.log"),
		LockPath: filepath.Join(dir, "shim.lock"),
		InfoPath: filepath.Join(dir, "proc.json"),
	}
	// selfExe 直接用 /bin/sh 顶替真 shim：本用例只验 StartedAt 有没有被填上，
	// 不验 shim 行为（拿锁、读 spec.json 那些由 shim 自己的用例覆盖）
	hd, err := Start(spec, "/bin/sh", "-c", "sleep 5")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = killGroup(hd.PID) })
	if hd.StartedAt <= 0 {
		t.Fatalf("Start 未记录 StartedAt，got %d", hd.StartedAt)
	}
	if delta := time.Now().UnixNano() - hd.StartedAt; delta < 0 || delta > int64(30*time.Second) {
		t.Fatalf("StartedAt 偏离现在过远：delta=%d ns", delta)
	}
}
```

在文件顶部 import 中补 `"path/filepath"`、`"time"`。

**注意 `stubAlive` 的语义**：它接受一个返回序列，最后一个值会被重复使用（见
`kill_test.go`）。上面两处 `stubAlive(t, true)` / `stubAlive(t, false)` 都是恒定
返回，正是我们要的；同一个用例里连续调两次 `stubAlive` 会各自注册 `t.Cleanup`，
后注册的先还原，行为正确。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run 'TestFootprint|TestStartRecords' -v`
Expected: 编译失败，`undefined: enumProcsFn` / `undefined: Footprint`

- [ ] **Step 3: 加 Handle 字段与测试缝**

修改 `internal/prochost/prochost.go` 的 `Handle`（若 Task 2 已加则跳过字段本身）：

```go
// Handle 是一个已拉起的 shim 的回收凭据。
type Handle struct {
	PID      int    `json:"pid"`
	LockPath string `json:"lock_path"`

	// StartedAt 是 shim 进程的启动时刻（unix 纳秒），足迹身份校验的时间下界。
	//
	// 为什么读内核而不是记墙钟：规则三要把**成员**的启动时刻与它直接比较，而成员
	// 的时刻来自内核（darwin p_starttime / linux /proc starttime）。两边取自同一个
	// 时钟源才可比——记 time.Now() 会引入毫秒级偏差，linux 的 jiffies 精度（10ms）
	// 下足以让紧随其后 fork 的子进程「看起来比父进程还早」，从而被规则三误排除。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出 0 即判
	// VerdictNoCredential 降级为只上报不清扫。老任务不会因为升级就被动手。
	StartedAt int64 `json:"started_at,omitempty"`
}
```

在 `footprint.go` 加测试缝：

```go
// enumProcsFn 是进程枚举的测试缝（包级 var 而非直接调用）：判据测试要喂固定
// 快照，与 aliveFn / killGroupFn 同款路数。
var enumProcsFn = enumProcs
```

- [ ] **Step 4: 让 Start 记录启动时刻**

修改 `internal/prochost/prochost.go` 的 `Start`，在 `spawnDetached` 成功之后、`return` 之前：

```go
	pid, err := spawnDetached(argv, spec.Dir)
	if err != nil {
		log().Error("拉起 shim 失败", "spec", specPath, "cause", err)
		return Handle{}, err
	}
	// 读回内核记录的启动时刻作为身份校验的时间下界（why 见 Handle.StartedAt）。
	// 读不到不阻断拉起：shim 已经在跑了，为一个诊断字段把它杀掉是本末倒置——
	// 代价是这个任务此后只能上报、不能自动清扫，如实降级即可
	startedAt := lookupStartedAt(pid)
	if startedAt <= 0 {
		log().Warn("读不到 shim 启动时刻，该任务将只能上报残留、无法自动清扫",
			"pid", pid, "spec", specPath)
	}
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath,
		"started_at", startedAt)
	return Handle{PID: pid, LockPath: spec.LockPath, StartedAt: startedAt}, nil
```

在 `footprint.go` 加：

```go
// lookupStartedAt 读回某个 pid 的内核启动时刻（unix 纳秒）；读不到返回 0。
//
// 为什么容忍失败：这是诊断凭据不是运行必需品，取不到只降级为「不能自动清扫」，
// 绝不能反过来影响已经拉起的执行者。
func lookupStartedAt(pid int) int64 {
	procs, err := enumProcsFn()
	if err != nil {
		log().Warn("枚举进程失败，无法读取启动时刻", "pid", pid, "cause", err)
		return 0
	}
	for _, p := range procs {
		if p.PID == pid {
			return p.StartedAt
		}
	}
	log().Warn("枚举结果中没有该 pid，无法读取启动时刻", "pid", pid)
	return 0
}
```

- [ ] **Step 5: 写 Footprint**

在 `internal/prochost/footprint.go` 追加：

```go
// Footprint 枚举 h 所代表任务当前占用的进程。
//
// 参数：h 为任务的进程句柄（来自 proc.json）
//
// 返回：
//   - members: 通过身份校验的成员 pid
//   - v: 判定结论；非 VerdictOK 时 members 必然为空
//   - err: 进程枚举失败（平台不支持时为 errNotSupported）
//
// 注意：
//   - **只读，绝不发信号**——它是 Sweep 的孪生只读版本，两者共用 classify
//   - 对**存活中**与**已死亡**的执行者均可调用：判据随存活锁状态自动切换
func Footprint(h Handle) (members []int, v Verdict, err error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("足迹枚举失败", "pid", h.PID, "cause", err)
		return nil, VerdictNoCredential, err
	}
	members, v = classify(h, procs, aliveFn(h))
	log().Debug("足迹判定完成", "pid", h.PID, "verdict", string(v), "members", len(members))
	return members, v, nil
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/prochost/ -v`
Expected: 全部 PASS（含既有用例，特别是 `TestKillSkipsWhenLockFree` 必须仍绿）

- [ ] **Step 7: 加关键节点日志**

已内联，核对：`Start` 记录 `started_at` 并在读不到时 Warn（明说后果）；`lookupStartedAt` 两条失败路径各有 Warn 带 pid；`Footprint` 枚举失败 Error、成功 Debug（高频调用不占 Info）。

- [ ] **Step 8: 加注释自检**

核对：`Handle.StartedAt` 有「为什么读内核不记墙钟」与零值语义两段 why；`Footprint` 文档注释含参数/返回/「只读绝不发信号」；`lookupStartedAt` 说明为何容忍失败；`enumProcsFn` 说明为何是包级 var。

- [ ] **Step 9: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(prochost): Handle 记录 shim 启动时刻，新增只读原语 Footprint"
```

---

### Task 4: `Sweep`

**Files:**
- Modify: `internal/prochost/footprint.go`
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: `classify`、`enumProcsFn`、`aliveFn`、`killGroupFn`、`killVerifyBackoff`（均已存在）
- Produces:
  - `func Sweep(h Handle) (killed int, v Verdict, err error)`
  - `var ErrExecutorAlive = errors.New("执行者仍存活，Sweep 不适用")`

- [ ] **Step 1: 写失败测试**

在 `internal/prochost/footprint_test.go` 追加：

```go
// TestSweepRefusesWhenExecutorAlive 锁仍被持有时 Sweep 必须拒绝执行且不发信号。
//
// 杀活着的执行者是 Kill 的职责。两者风险模型不同，互相代劳就会把 Kill 那条
// 「不确认存活就绝不发信号」的纪律绕过去。
func TestSweepRefusesWhenExecutorAlive(t *testing.T) {
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)
	stubAlive(t, true)
	n := stubKillGroup(t, nil)

	_, _, err := Sweep(h())
	if !errors.Is(err, ErrExecutorAlive) {
		t.Fatalf("执行者存活时应返回 ErrExecutorAlive，got %v", err)
	}
	if *n != 0 {
		t.Fatalf("执行者存活却发了 %d 次信号", *n)
	}
}

// TestSweepAbortsOnLeaderReuse pgid 被复用时必须整组放弃且**绝不发信号**。
//
// 这是本次改动最重要的一条：误杀被复用 pgid 的代价是杀掉机器上毫不相干的
// 进程组（B47 现场：旧实现 300 条成功命令误杀 114 次）。
func TestSweepAbortsOnLeaderReuse(t *testing.T) {
	stubEnum(t, []procEntry{
		{PID: 100, PGID: 100, StartedAt: t0 + 9999}, // 冒名组长
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
	}, nil)
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(h())
	if err != nil {
		t.Fatalf("判定复用不该返回错误（那是正常结论），got %v", err)
	}
	if v != VerdictLeaderReuse {
		t.Fatalf("want leader_reuse, got %s", v)
	}
	if killed != 0 || *n != 0 {
		t.Fatalf("判定复用却动了手：killed=%d signals=%d", killed, *n)
	}
}

// TestSweepNoCredentialAborts 凭据不全时放弃且不发信号。
func TestSweepNoCredentialAborts(t *testing.T) {
	stubEnum(t, []procEntry{{PID: 101, PGID: 100, StartedAt: t0 + 1}}, nil)
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(Handle{PID: testShimPID, StartedAt: 0})
	if err != nil || v != VerdictNoCredential || killed != 0 || *n != 0 {
		t.Fatalf("凭据不全应放弃：v=%s killed=%d signals=%d err=%v", v, killed, *n, err)
	}
}

// TestSweepKillsGroupOnce 正常路径：恰好一次组信号，返回成员数。
func TestSweepKillsGroupOnce(t *testing.T) {
	shrinkBackoff(t)
	stubEnum(t, []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 + 2},
	}, nil)
	stubAlive(t, false)
	n := stubKillGroup(t, nil)

	killed, v, err := Sweep(h())
	if err != nil {
		t.Fatalf("正常路径不该报错: %v", err)
	}
	if v != VerdictOK || killed != 2 {
		t.Fatalf("want ok/2, got %s/%d", v, killed)
	}
	if *n != 1 {
		t.Fatalf("应恰好发一次组信号，实发 %d 次", *n)
	}
}

// TestSweepAndFootprintAgree 孪生一致性：同一输入下，两者的成员集合必须完全相同。
//
// 这条钉住整个设计的核心不变式——「数出来的」与「会被杀的」是同一批。
// 两者若各写一份枚举/过滤，status 报 3 个而 Sweep 杀 5 个这种事没人会发现。
func TestSweepAndFootprintAgree(t *testing.T) {
	shrinkBackoff(t)
	procs := []procEntry{
		{PID: 101, PGID: 100, StartedAt: t0 + 1},
		{PID: 102, PGID: 100, StartedAt: t0 - 5}, // 规则三排除
		{PID: 103, PGID: 100, StartedAt: t0 + 3},
		{PID: 200, PGID: 200, StartedAt: t0 + 1}, // 别的组
	}
	stubEnum(t, procs, nil)
	stubAlive(t, false)
	_ = stubKillGroup(t, nil)

	members, v1, err1 := Footprint(h())
	killed, v2, err2 := Sweep(h())
	if err1 != nil || err2 != nil {
		t.Fatalf("不该报错: %v / %v", err1, err2)
	}
	if v1 != v2 {
		t.Fatalf("孪生判定不一致：Footprint=%s Sweep=%s", v1, v2)
	}
	if len(members) != killed {
		t.Fatalf("孪生成员数不一致：Footprint=%d Sweep=%d", len(members), killed)
	}
}
```

文件顶部 import 补 `"errors"`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/prochost/ -run TestSweep -v`
Expected: 编译失败，`undefined: Sweep`

- [ ] **Step 3: 写实现**

在 `internal/prochost/footprint.go` 追加（并在 import 补 `"errors"`、`"fmt"`、`"time"`）：

```go
// ErrExecutorAlive 表示执行者仍然活着，Sweep 不适用。
//
// 调用方靠 errors.Is 判别，禁止按错误文本判——与 ErrLockHeld / ErrStillAlive 同款。
var ErrExecutorAlive = errors.New("执行者仍存活，Sweep 不适用")

// Sweep 回收一个**已死**执行者留下的残留后代。
//
// 参数：h 为任务的进程句柄（来自 proc.json）
//
// 返回：
//   - killed: 发信号时组内通过身份校验的成员数（0 表示没动手）
//   - v: 判定结论；非 VerdictOK 时必然 killed == 0
//   - err: 执行者仍存活（ErrExecutorAlive）、枚举失败、或已发信号但复核仍存活
//     （ErrStillAlive）
//
// 注意：
//   - **前提是执行者已死**。存活锁仍被持有时直接拒绝——杀活着的执行者是 Kill
//     的职责，两者风险模型不同（Kill 以「锁在」为发信号的前提，Sweep 以「锁不在」
//     为前提并逐个校验成员身份），互相代劳会把 Kill 那条纪律绕过去
//   - 判定为放弃（leader_reuse / no_credential）**不是错误**，是正常结论：
//     调用方据 v 决定是否上报人工，不该按 err != nil 判
//   - 与 Kill 一致：发完 SIGKILL 必须复核，复核窗口走完仍存活返回 ErrStillAlive
func Sweep(h Handle) (killed int, v Verdict, err error) {
	if aliveFn(h) {
		log().Warn("执行者仍存活，拒绝清扫", "pid", h.PID, "lock", h.LockPath)
		return 0, VerdictOK, ErrExecutorAlive
	}
	procs, eerr := enumProcsFn()
	if eerr != nil {
		log().Error("清扫前枚举进程失败", "pid", h.PID, "cause", eerr)
		return 0, VerdictNoCredential, eerr
	}
	members, v := classify(h, procs, false)
	if v != VerdictOK {
		log().Warn("清扫放弃", "pid", h.PID, "verdict", string(v))
		return 0, v, nil
	}
	if len(members) == 0 {
		log().Info("无残留可清扫", "pid", h.PID)
		return 0, VerdictOK, nil
	}
	log().Info("回收残留进程组", "pid", h.PID, "members", len(members), "pids", members)
	if kerr := killGroupFn(h.PID); kerr != nil {
		log().Error("回收残留进程组失败", "pid", h.PID, "cause", kerr)
		return 0, VerdictOK, fmt.Errorf("回收进程组 %d: %w", h.PID, kerr)
	}
	// 复核：与 Kill 同款窗口。SIGKILL 异步生效，不复核就是「杀没杀掉我们不知道，
	// 而且假装知道」——B47 修的正是这个
	for i, d := range killVerifyBackoff {
		time.Sleep(d)
		rest, rerr := enumProcsFn()
		if rerr != nil {
			log().Error("复核枚举失败", "pid", h.PID, "cause", rerr)
			break
		}
		if left, _ := classify(h, rest, false); len(left) == 0 {
			log().Info("清扫完成，已确认残留退出", "pid", h.PID,
				"killed", len(members), "probe", i+1)
			return len(members), VerdictOK, nil
		}
	}
	log().Error("已发 SIGKILL 但复核窗口走完仍有残留", "pid", h.PID,
		"window", killVerifyWindow)
	return len(members), VerdictOK,
		fmt.Errorf("%w: pgid=%d，已发 SIGKILL 并复核 %s", ErrStillAlive, h.PID, killVerifyWindow)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/prochost/ -v`
Expected: 全部 PASS（6 个新用例 + 既有用例全绿）

- [ ] **Step 5: 变异检验**

逐条执行，每条改完跑 `go test ./internal/prochost/ -run 'TestSweep|TestClassify|TestFootprint' -v`，确认**指定用例 FAIL**，然后**立即还原**：

| 变异 | 必须 FAIL 的用例 |
|---|---|
| `classify` 里 `if !lockHeld {` 改成 `if true {` | `TestClassifyLockHeldCountsGroup`、`TestFootprintUsesLockState` |
| `classify` 里整个 `leader_reuse` 循环删除 | `TestSweepAbortsOnLeaderReuse`、`TestClassifyLeaderReuseAborts` |
| `classify` 里 `p.StartedAt < h.StartedAt` 改成 `false` | `TestClassifyExcludesMemberStartedBeforeShim`、`TestSweepAndFootprintAgree` |
| `classify` 里 `h.StartedAt <= 0` 判空条件删除 | `TestClassifyNoCredential`、`TestSweepNoCredentialAborts` |
| `Sweep` 里 `if aliveFn(h)` 提前返回删除 | `TestSweepRefusesWhenExecutorAlive` |

全部还原后：

```bash
go test ./internal/prochost/ -count=1 && git diff --exit-code
```
Expected: 测试全绿，`git diff` 无输出

- [ ] **Step 6: 加关键节点日志**

已内联，核对：进入拒绝分支 Warn（带 pid 与 lock）；枚举失败 Error；判定放弃 Warn（带 verdict）；无残留 Info；动手前 Info（带成员数与 pid 列表）；复核成功 Info（带 killed 与探针序号）；复核失败 Error。**成功路径不静默**——这条是 `instrumenting-code` 的硬要求。

- [ ] **Step 7: 加注释自检**

核对：`Sweep` 文档注释含参数/返回/三条注意（为何拒绝存活、放弃不是错误、必须复核）；`ErrExecutorAlive` 说明为何靠 `errors.Is`；复核循环内有「为什么必须复核」的 why 注释。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(prochost): 新增 Sweep 回收已死执行者的残留进程组"
```

---

### Task 5: adapter 暴露进程句柄

**Files:**
- Modify: `internal/executor/opencode/reap.go`
- Modify: `internal/executor/claudecode/reap.go`
- Modify: `internal/executor/codex/reap.go`
- Modify: `internal/executor/grok/reap.go`
- Test: `internal/executor/opencode/reap_test.go`、`internal/executor/claudecode/reap_test.go`、`internal/executor/grok/reap_test.go`（各追加两条）
- Test: `internal/executor/codex/reap_test.go`（**新建**——codex 目前没有这个测试文件）

**Interfaces:**
- Consumes: 各 adapter 已有的 `readProcInfo(taskDir)`
- Produces: 四个 adapter 各新增方法
  `func (a *Adapter) ProcHandle(taskID, taskDir string) (prochost.Handle, error)`
  （agentd 侧将以可选接口 `footprinter` 消费，见 Task 6）

- [ ] **Step 1: 写失败测试**

在 `internal/executor/opencode/reap_test.go` 追加（其余三家在各自 reap_test.go 写同构用例，把包名与构造方式按该包既有测试改写）：

```go
// TestProcHandleReadsProcInfo 验证 adapter 能把 proc.json 里的进程句柄交出来。
//
// agentd 侧的清扫与计数都靠它取 Handle——取不到就只能降级为「无凭据」，
// 整个足迹功能对该 adapter 静默失效。
func TestProcHandleReadsProcInfo(t *testing.T) {
	dir := t.TempDir()
	want := prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "shim.lock"), StartedAt: 999}
	if err := writeProcInfo(dir, &procInfo{Handle: want, Port: 1234, Password: "p"}); err != nil {
		t.Fatalf("写 proc.json 失败: %v", err)
	}
	a := New(quietLogger())
	got, err := a.ProcHandle("task-1", dir)
	if err != nil {
		t.Fatalf("ProcHandle 失败: %v", err)
	}
	if got != want {
		t.Fatalf("Handle 不符：got %+v, want %+v", got, want)
	}
}

// TestProcHandleMissingProcInfo proc.json 不存在时必须报错，不能返回零值 Handle。
//
// 零值 Handle 的 PID=0，classify 会判 no_credential 降级——语义上没错，但
// 调用方拿不到「读失败」这个事实，日志里就少了一条能解释「为什么这个任务
// 从来没被清扫过」的线索。
func TestProcHandleMissingProcInfo(t *testing.T) {
	a := New(quietLogger())
	if _, err := a.ProcHandle("task-1", t.TempDir()); err == nil {
		t.Fatal("proc.json 缺失时应返回错误")
	}
}
```

**其余三家的差异**（照抄前先确认，四个包各不相同）：

| 包 | `procInfo` 字段 | 测试里构造 adapter | reap_test.go |
|---|---|---|---|
| opencode | `Handle` `Port` `Password` `LastTurnMsgID` `WatermarkArmed` | `New(quietLogger())` | 已有 |
| claudecode | 见 `proc.go:335` | `New(nil)` | 已有 |
| grok | `Handle` `Port` | `New(quietLogger())` | 已有 |
| codex | `Handle` `Port` | `New(nil)` | **需新建** |

四个包的 `New` 都对 nil logger 兜底（`log = slog.Default()`），因此 `a.log` 恒非 nil，实现里直接用 `a.log.Error` / `a.log.Debug` 即可，无需判空。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/opencode/ -run TestProcHandle -v`
Expected: 编译失败，`a.ProcHandle undefined`

- [ ] **Step 3: 写实现（四家同构）**

在每个 adapter 的 `reap.go` 追加（以 opencode 为例，其余三家把包内 `readProcInfo` 的名字与返回类型按各自实现对齐）：

```go
// ProcHandle 交出该任务的进程句柄（来自任务目录的 proc.json）。
//
// 参数：
//   - taskID: 任务 ID，仅用于日志定位
//   - taskDir: 任务目录（凭据所在）
//
// 返回：
//   - 进程句柄；proc.json 不存在或不可解析时返回错误
//
// 注意：本方法**只读**，不探活、不发信号——存活判定与回收分别是
// prochost.Alive 与 prochost.Sweep 的职责。agentd 以可选接口消费它
// （不实现该方法的 adapter 一律按「无凭据」降级，与 reaper/prober 同款路数）。
func (a *Adapter) ProcHandle(taskID, taskDir string) (prochost.Handle, error) {
	pi, err := readProcInfo(taskDir)
	if err != nil {
		a.log.Error("读取进程句柄失败", "task", taskID, "dir", taskDir, "cause", err)
		return prochost.Handle{}, fmt.Errorf("读取进程句柄: %w", err)
	}
	a.log.Debug("取得进程句柄", "task", taskID, "pid", pi.Handle.PID,
		"has_started_at", pi.Handle.StartedAt > 0)
	return pi.Handle, nil
}
```

（日志字段名跟随该包 `Reap` 既有命名——opencode 用 `"task"` / `"shim_pid"`，照抄即可，别引入第三套叫法。）

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/... -run TestProcHandle -v`
Expected: 四个包各 2 个用例 PASS

- [ ] **Step 5: 加关键节点日志**

已内联：读失败 Error（带 task/dir/cause）；成功 Debug（带 pid 与「凭据是否完整」）。**`has_started_at` 这个字段是刻意的**——升级前的老任务在这里就能被一眼认出来，省掉「为什么这个任务不清扫」的二次排查。

- [ ] **Step 6: 加注释自检**

核对：四家的 `ProcHandle` 均有文档注释（参数/返回/「只读」注意）；说明它是可选接口、不实现即降级。

- [ ] **Step 7: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(executor): 四家 adapter 暴露 ProcHandle 供足迹计数与清扫取凭据"
```

---

### Task 6: agentd 接入清扫（两个调用点）

**Files:**
- Modify: `internal/agentd/reconcile.go`（新增 `footprinter` 接口与 `SweepTaskProcs`，`reconcileExecutorGone` 加清扫）
- Modify: `internal/agentd/watchdog.go`（`RecoverOnStartup` 的 `waiting_review` 分支加清扫）
- Test: `internal/agentd/reconcile_test.go`（追加）

**Interfaces:**
- Consumes: `prochost.Sweep`（Task 4）、adapter 的 `ProcHandle`（Task 5）、`m.adapterFor`（现有）、`m.notifyOrphanRisk`（现有）
- Produces:
  - `type footprinter interface { ProcHandle(taskID, taskDir string) (prochost.Handle, error) }`
  - `func (m *Manager) SweepTaskProcs(taskID string)` —— **必须导出**：`RecoverOnStartup` 的唯一生产调用点在 `cmd/agentd.go:155`（另一个包），与已导出的 `mgr.ResumeTask` 同理
  - `reconcileExecutorGone` 与 `RecoverOnStartup` 均新增一个 `sweep func(taskID string)` 参数

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/reconcile_test.go` 追加：

```go
// TestReconcileExecutorGoneSweepsUnconditionally 验证清扫是无条件后置动作：
// 即使任务状态命中提前返回分支（非 running/waiting_answer），也必须清扫。
//
// 这条守的是事故现场的形态：2026-08-12 两个任务最终都停在 waiting_review，
// 而那正是提前返回会跳过的状态。清扫若跟着提前返回一起被跳过，这个功能
// 在它最该工作的场景里恰好不工作。
func TestReconcileExecutorGoneSweepsUnconditionally(t *testing.T) {
	st, hub, log := newTestDeps(t)
	id := seedTask(t, st, proto.TaskStateWaitingReview)

	swept := 0
	reconcileExecutorGone(st, hub, id, "测试", log, func(string) { swept++ })

	if swept != 1 {
		t.Fatalf("提前返回分支也必须清扫一次，实际 %d 次", swept)
	}
	got, err := st.GetTask(id)
	if err != nil {
		t.Fatalf("读任务失败: %v", err)
	}
	if got.State != proto.TaskStateWaitingReview {
		t.Fatalf("清扫不得改变状态，got %s", got.State)
	}
}

// TestReconcileExecutorGoneSweepsAfterTransit 正常路径：先迁状态、再清扫。
func TestReconcileExecutorGoneSweepsAfterTransit(t *testing.T) {
	st, hub, log := newTestDeps(t)
	id := seedTask(t, st, proto.TaskStateRunning)

	var stateAtSweep proto.TaskState
	reconcileExecutorGone(st, hub, id, "测试", log, func(taskID string) {
		cur, _ := st.GetTask(taskID)
		stateAtSweep = cur.State
	})

	if stateAtSweep != proto.TaskStateWaitingReview {
		t.Fatalf("清扫必须发生在状态迁移之后，清扫时状态为 %s", stateAtSweep)
	}
}
```

（`newTestDeps` / `seedTask` 按 `reconcile_test.go` 既有辅助函数命名复用；若不存在则照该文件既有用例的构造方式内联写出。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestReconcileExecutorGoneSweeps -v`
Expected: 编译失败，`too many arguments in call to reconcileExecutorGone`

- [ ] **Step 3: 给 reconcileExecutorGone 加清扫回调**

修改 `internal/agentd/reconcile.go`：

```go
// reconcileExecutorGone 收尾一个 executor 已不在的任务。
//
// 参数：
//   - sweep: 残留进程清扫回调；**无条件调用**，与任务状态无关（why 见下）
//
// 为什么清扫是无条件后置动作：本函数对非 running/waiting_answer 的状态提前返回，
// 那条分支的理由是「待审核终态与已终结态不需要状态收尾」——它说的是状态，不是
// 资源。executor 已经不在了，残留进程该不该收与任务停在哪个状态无关；已经 done
// 过的任务同样可能有残留（那次 Kill 正因锁已释放而空转）。2026-08-12 事故里两个
// 任务最终都停在 waiting_review，恰好是提前返回会跳过的形态。
//
// 顺序：状态收尾在前、清扫在后。审核者的工作流（任务进 waiting_review）不受
// 清扫成败影响——清扫失败只上报，绝不回头改状态。
func reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string,
	log *slog.Logger, sweep func(taskID string)) proto.TaskState {

	cur, err := st.GetTask(taskID)
	if err != nil {
		log.Error("对账读取任务失败", "task", taskID, "reason", reason, "cause", err)
		return ""
	}
	log.Info("executor 已不在，开始对账", "task", taskID, "state", cur.State, "reason", reason)
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		log.Info("任务无需状态对账，仅清扫残留", "task", taskID, "state", cur.State)
		sweep(taskID) // 无条件：见上方 why
		return cur.State
	}

	voidTicketsWithAudit(st, taskID, reason, log)
	evt, err := st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{FailReason: reason})
	if err != nil {
		log.Error("对账追加 failed 事件失败，不迁移状态", "task", taskID, "cause", err)
		sweep(taskID) // 状态没迁成不代表 executor 还活着，残留照收
		return cur.State
	}
	if err := recoverTransit(st, taskID, cur.State); err != nil {
		log.Error("对账迁移 waiting_review 失败", "task", taskID, "cause", err)
		sweep(taskID)
		return cur.State
	}
	hub.Publish(evt)
	log.Info("对账完成", "task", taskID, "from", cur.State, "to", proto.TaskStateWaitingReview)
	sweep(taskID)
	return proto.TaskStateWaitingReview
}
```

同步修改方法薄包装：

```go
// reconcileExecutorGone 是包级同名函数的方法薄包装（省去调用点重复传 st/hub/log）。
func (m *Manager) reconcileExecutorGone(taskID, reason string) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID, reason, m.log, m.SweepTaskProcs)
}
```

- [ ] **Step 4: 写 footprinter 接口与 SweepTaskProcs**

在 `internal/agentd/reconcile.go` 追加：

```go
// footprinter 是「交出任务进程句柄」的可选 adapter 能力（四个真实 adapter 均实现，
// fake 不实现）。
//
// 为什么是可选接口而不是加进 executor.Adapter：不支持的 adapter 一律按「无凭据」
// 降级是自然语义，五动作核心契约不该为一个诊断/回收功能扩面——与 reaper /
// prober / restorer / volatilePermitter 同一套路数。
type footprinter interface {
	ProcHandle(taskID, taskDir string) (prochost.Handle, error)
}

// SweepTaskProcs 清扫一个任务的残留进程，best-effort。
//
// 参数：taskID 为目标任务
//
// 注意：
//   - 无返回值：调用方全都处在收尾路径上，清扫成败不该反过来影响那件事
//   - 只有「确实有残留但我们没敢动」才发事件提示人工；成功与无残留只进日志。
//     这是尊重 stopExecutor 已经想清楚过的事——「其余失败五花八门，全发事件
//     等于把审核者淹了，那样这条提示就没人看了」
//   - 导出是因为 RecoverOnStartup 的接线点在 cmd/agentd.go（与 ResumeTask 同理），
//     不是给外部当通用 API 用
func (m *Manager) SweepTaskProcs(taskID string) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("清扫解析执行者失败", "task", taskID, "cause", err)
		return
	}
	fp, ok := ad.(footprinter)
	if !ok {
		m.log.Debug("adapter 不支持进程句柄，跳过清扫", "task", taskID)
		return
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	h, err := fp.ProcHandle(taskID, taskDir)
	if err != nil {
		m.log.Error("清扫取进程句柄失败", "task", taskID, "dir", taskDir, "cause", err)
		return
	}
	killed, verdict, err := prochost.Sweep(h)
	switch {
	case errors.Is(err, prochost.ErrExecutorAlive):
		// 竞态：判死与清扫之间 executor 又被认为活着。不是错误，交给正常路径
		m.log.Info("清扫时执行者仍存活，交由常规回收路径", "task", taskID, "pid", h.PID)
	case err != nil:
		m.log.Error("清扫失败", "task", taskID, "pid", h.PID, "cause", err)
		m.notifyOrphanRisk(taskID, fmt.Sprintf(
			"残留进程清扫失败（pid=%d，原因：%v），请先 handoff footprint 确认再人工处理", h.PID, err))
	case verdict != prochost.VerdictOK:
		m.log.Warn("清扫放弃", "task", taskID, "pid", h.PID, "verdict", string(verdict))
		m.notifyOrphanRisk(taskID, fmt.Sprintf(
			"残留进程未清扫（判定：%s），请先 handoff footprint 确认再人工处理", verdict))
	case killed > 0:
		m.log.Info("残留进程已清扫", "task", taskID, "pid", h.PID, "killed", killed)
	default:
		m.log.Info("无残留进程", "task", taskID, "pid", h.PID)
	}
}
```

import 补 `"errors"`、`"fmt"`、`"path/filepath"`、`"github.com/xushixin/handoff/internal/prochost"`。

- [ ] **Step 5: 接入 RecoverOnStartup 的 waiting_review 分支**

修改 `internal/agentd/watchdog.go` 的 `RecoverOnStartup`：签名加 `sweep func(taskID string)` 参数，两处改动——

```go
		if t.State == proto.TaskStateWaitingReview {
			// waiting_review 本来就是待审核终态：executor 不在不追加事件、不迁移
			// 状态——审核者裁决（continue 重派 / done 归档）才是它该走的路。
			//
			// 但**残留进程照收**：那条理由说的是状态与事件噪音，不是资源。
			// 2026-08-12 事故里两个任务最终正停在这个状态，若跟着一起跳过，
			// 清扫会在最该工作的场景里恰好不工作
			kept++
			log.Info("waiting_review 任务 executor 已不在，保持现状等审核者裁决", "task", t.ID, "alive", false)
			sweep(t.ID)
			continue
		}
		failed++
		log.Info("执行器已不在，任务转 waiting_review 交审核者", "task", t.ID, "alive", false, "state", t.State)
		reconcileExecutorGone(st, hub, t.ID, "agentd 重启后执行器已不在", log, sweep)
```

同步修改 `RecoverOnStartup` 的全部调用点：

- 生产调用点只有一处——`cmd/agentd.go:155`，改为
  `agentd.RecoverOnStartup(st, srv.Hub(), mgr.ResumeTask, mgr.SweepTaskProcs, logger)`
- 测试调用点四处（`internal/agentd/watchdog_test.go` 的 282 / 327 / 394 / 428 行附近），
  传 `func(string) {}`

`RecoverOnStartup` 的新签名：

```go
func RecoverOnStartup(st *store.Store, hub *Hub, probe func(taskID string) bool,
	sweep func(taskID string), log *slog.Logger) error
```

`sweep` 紧跟 `probe`：两者是同款注入缝（避免 watchdog 直接接触 adapter），并排放读起来才是一回事。

- [ ] **Step 6: 修复其余调用点并运行全量测试**

Run:
```bash
grep -rn "reconcileExecutorGone(" --include="*.go" . | grep -v "func "
go build ./... && go test ./internal/agentd/ -v 2>&1 | tail -20
```
Expected: 编译通过；`internal/agentd` 全绿（含两个新用例）

- [ ] **Step 7: 加关键节点日志**

已内联，核对：`SweepTaskProcs` 五条分支（解析失败/不支持/取凭据失败/清扫失败/放弃/成功/无残留）各有一条带 task 与 pid 的日志；提前返回分支的 Info 文案由「任务无需对账，跳过」改为「任务无需状态对账，仅清扫残留」——**旧文案会让读日志的人以为什么都没做**。

- [ ] **Step 8: 加注释自检**

核对：`footprinter` 说明为何是可选接口；`SweepTaskProcs` 文档注释含参数与两条注意（无返回值的 why、上报节制的 why）；`reconcileExecutorGone` 文档注释新增「为什么清扫无条件」整段 why；`watchdog.go` 的 `waiting_review` 分支有「残留照收」的 why。

- [ ] **Step 9: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(agentd): executor 已不在时无条件清扫残留进程组（两个调用点）"
```

---

### Task 7: `status` 显示 per-task 进程数与全局占用

**Files:**
- Modify: `internal/proto/status.go`（`ActiveTask.Procs`、`StatusResp.Proc`、`ProcUsage`）
- Modify: `internal/agentd/status.go`（填充）
- Modify: `internal/prochost/footprint.go`（导出 `UIDUsage`）
- Modify: `cmd/status.go`（渲染）
- Test: `internal/agentd/status_test.go`、`cmd/status_test.go`（各追加）

**Interfaces:**
- Consumes: `prochost.Footprint`（Task 3）、`footprinter`（Task 6）
- Produces:
  - `func prochost.UIDUsage() (used, limit int, err error)`
  - `type proto.ProcUsage struct { Used int; Limit int }`
  - `proto.StatusResp.Proc *ProcUsage`、`proto.ActiveTask.Procs *int`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/status_test.go` 追加：

```go
// TestStatusFillsProcsForActiveTasks 验证活跃任务带上进程数。
func TestStatusFillsProcsForActiveTasks(t *testing.T) {
	m := newTestManager(t) // 按本文件既有构造方式
	id := seedRunningTask(t, m)

	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	for _, a := range resp.Active {
		if a.ID != id {
			continue
		}
		if a.Procs == nil {
			t.Fatal("活跃任务应带 Procs（取不到时也该是指针指向的确定值或 nil，见下）")
		}
		return
	}
	t.Fatalf("响应里没有任务 %s", id)
}

// TestStatusProcsNilWhenUnsupported adapter 不支持时 Procs 必须是 nil，不能填 0。
//
// nil 表示「没这个信息」，0 表示「确实没有进程」。填 0 就是制造假阳性——
// 与 Watchers / Live 三态是同一条纪律。
func TestStatusProcsNilWhenUnsupported(t *testing.T) {
	m := newTestManagerWithFakeExecutor(t) // fake 不实现 footprinter
	seedRunningTask(t, m)

	resp, err := m.Status()
	if err != nil {
		t.Fatalf("Status 失败: %v", err)
	}
	for _, a := range resp.Active {
		if a.Procs != nil {
			t.Fatalf("不支持的 adapter 应留 nil，got %d", *a.Procs)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestStatusProcs -v`
Expected: 编译失败，`a.Procs undefined`

- [ ] **Step 3: 加协议字段**

修改 `internal/proto/status.go`：

```go
// ProcUsage 是本机当前 uid 的进程占用与上限。
//
// 为什么两个数必须一起给：只看 Used 不知道离墙还有多远，只看 Limit 没有意义。
// 2026-08-12 devbox 整机 fork 瘫痪时，346/2666 这两个数并排才说明得了问题。
type ProcUsage struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}
```

在 `ActiveTask` 追加：

```go
	// Procs 是该任务当前占用的进程数。
	//
	// 为什么是指针：nil 表示**取不到这个信息**（老 agentd、adapter 不支持、
	// 平台不支持、或 pgid 判定为复用/凭据不全），与「确实是 0 个进程」是两回事。
	// 猜一个 0 就是制造假阳性——与 Watchers、Live 三态同一条纪律。
	Procs *int `json:"procs,omitempty"`
```

在 `StatusResp` 追加：

```go
	// Proc 是本机 uid 级的进程占用与上限。指针 + omitempty：老 agentd 不发这个
	// 字段，消费方拿到 nil 应当什么都不显示，而不是显示一个「0/0」的假状态。
	Proc *ProcUsage `json:"proc,omitempty"`
```

- [ ] **Step 4: 导出 UIDUsage**

在 `internal/prochost/footprint.go` 追加：

```go
// UIDUsage 报告当前 uid 的进程占用与上限。
//
// 返回：
//   - used: 当前 uid 的进程数；limit: 每 uid 上限
//   - err: 平台不支持（errNotSupported）或读取失败
//
// 注意：与 Footprint 共用同一次进程枚举的实现，但回答的是不同问题——
// Footprint 问「这个任务占了多少」，本函数问「这台机器还剩多少」。
// 调用方拿到 err 时必须如实呈现为「未知」，不得回退成 0。
func UIDUsage() (used, limit int, err error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("统计 uid 进程占用失败", "cause", err)
		return 0, 0, err
	}
	limit, err = procLimit()
	if err != nil {
		log().Error("读取进程数上限失败", "cause", err)
		return len(procs), 0, err
	}
	log().Debug("uid 进程占用", "used", len(procs), "limit", limit)
	return len(procs), limit, nil
}
```

- [ ] **Step 5: 在 Status 里填充**

修改 `internal/agentd/status.go`，在既有探活循环内为每个活跃任务补 `Procs`，并在响应组装处补 `Proc`：

```go
	// 全局占用：失败只 Warn 并留 nil——status 是诊断命令，宁可少一个数
	// 也不能给一个编出来的数
	if used, limit, err := prochost.UIDUsage(); err != nil {
		m.log.Warn("状态聚合：读不到进程占用，该字段留空", "cause", err)
	} else {
		resp.Proc = &proto.ProcUsage{Used: used, Limit: limit}
	}
```

per-task（在填充 `ActiveTask` 的位置）：

```go
	// per-task 足迹：只读、失败留 nil。它复用本函数既有的时限纪律——
	// status 不能因为多了一个诊断字段就变成慢命令
	if fp, ok := ad.(footprinter); ok {
		taskDir := filepath.Join(m.cfg.DataDir, "tasks", t.ID)
		if h, herr := fp.ProcHandle(t.ID, taskDir); herr == nil {
			if members, v, ferr := prochost.Footprint(h); ferr == nil && v == prochost.VerdictOK {
				n := len(members)
				at.Procs = &n
			} else {
				m.log.Debug("状态聚合：足迹判定不可用，该任务 procs 留空",
					"task", t.ID, "verdict", string(v), "cause", ferr)
			}
		}
	}
```

- [ ] **Step 6: 渲染**

修改 `cmd/status.go` 的 `renderStatus`——在「任务」行（现 121 行）之后插入：

```go
	// nil 表示对端没给（老 agentd / 平台不支持），整行不打印。
	// 打一行「0/0」比不打更糟：它看起来像个结论，实际是我们不知道
	if st.Proc != nil {
		fmt.Fprintf(w, "进程     %d/%d（本机 uid 已用/上限）\n", st.Proc.Used, st.Proc.Limit)
	}
```

在活跃任务的 `line` 组装处（现 127 行）之后、`unattended` 判断之前插入：

```go
		// 同上：nil 不追加。这里刻意放在存活结论之后——先回答「活没活」，
		// 再回答「占了多少」，后者是前者的补充而不是替代
		if a.Procs != nil {
			line += fmt.Sprintf("  %d 进程", *a.Procs)
		}
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/agentd/ ./cmd/ ./internal/proto/ -v 2>&1 | tail -20`
Expected: 全绿

- [ ] **Step 8: 加关键节点日志**

已内联，核对：全局占用读失败 Warn 并明说「该字段留空」；per-task 判定不可用 Debug（带 verdict 与 cause）——用 Debug 是因为它每次 status 都可能触发，Info 会刷屏。成功路径由 `UIDUsage` 内的 Debug 覆盖。

- [ ] **Step 9: 加注释自检**

核对：`ProcUsage` 有「为什么两个数必须一起给」；`ActiveTask.Procs` 与 `StatusResp.Proc` 各有「为什么是指针」；`UIDUsage` 有文档注释与「与 Footprint 的分工」注意；status.go 两处填充各有一行 why（为何失败留 nil、为何复用时限纪律）。

- [ ] **Step 10: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(status): 显示每任务进程数与本机 uid 进程占用/上限"
```

---

### Task 8: `handoff footprint` 命令

**Files:**
- Create: `cmd/footprint.go`
- Create: `cmd/footprint_test.go`
- Modify: `internal/proto/status.go`（`FootprintRow` / `FootprintResp`）
- Modify: `internal/agentd/server.go`（`GET /api/footprint`）
- Modify: `internal/agentd/status.go`（`Manager.FootprintAll`）
- Modify: `internal/client/client.go`（`Footprint` 方法）
- Test: `internal/agentd/status_test.go`（追加）

**Interfaces:**
- Consumes: `prochost.Footprint`、`prochost.UIDUsage`、`footprinter`
- Produces:
  - `type proto.FootprintRow struct { TaskID, Name, State string; Procs int; Verdict string }`
  - `type proto.FootprintResp struct { Rows []FootprintRow; Usage *ProcUsage }`
  - `func (m *Manager) FootprintAll() (*proto.FootprintResp, error)`
  - `func (c *Client) Footprint(ctx context.Context) (*proto.FootprintResp, error)`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/status_test.go` 追加：

```go
// TestFootprintAllCoversArchivedTasks 验证体检覆盖已归档任务。
//
// 这是这条命令存在的理由：Done 只删 worktree、不删任务目录，历史任务的
// proc.json 都还在。若只扫活跃任务，它与 status 就没有区别了。
func TestFootprintAllCoversArchivedTasks(t *testing.T) {
	m := newTestManager(t)
	archived := seedTaskWithState(t, m, proto.TaskStateCompleted)

	resp, err := m.FootprintAll()
	if err != nil {
		t.Fatalf("FootprintAll 失败: %v", err)
	}
	for _, r := range resp.Rows {
		if r.TaskID == archived {
			return
		}
	}
	t.Fatalf("体检结果里没有已归档任务 %s（共 %d 行）", archived, len(resp.Rows))
}

// TestFootprintAllReportsVerdict 验证判定结论如实带出，不被抹成 0。
func TestFootprintAllReportsVerdict(t *testing.T) {
	m := newTestManager(t)
	seedTaskWithState(t, m, proto.TaskStateCompleted)

	resp, err := m.FootprintAll()
	if err != nil {
		t.Fatalf("FootprintAll 失败: %v", err)
	}
	if len(resp.Rows) == 0 {
		t.Fatal("应至少有一行")
	}
	if resp.Rows[0].Verdict == "" {
		t.Fatal("Verdict 不得为空——判不出结论也要如实说，不能只给一个 0")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agentd/ -run TestFootprintAll -v`
Expected: 编译失败，`m.FootprintAll undefined`

- [ ] **Step 3: 加协议类型**

在 `internal/proto/status.go` 追加：

```go
// FootprintRow 是一个任务的进程足迹体检结果。
//
// 注意：Verdict 恒非空。判不出结论时给 leader_reuse / no_credential，而不是
// 把 Procs 抹成 0 了事——「没有残留」与「我们不敢下结论」是两回事，后者需要
// 人工看一眼，前者不需要。
type FootprintRow struct {
	TaskID  string `json:"task_id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Procs   int    `json:"procs"`
	Verdict string `json:"verdict"`
}

// FootprintResp 是 GET /api/footprint 的响应：全部任务（含已归档）的足迹体检。
type FootprintResp struct {
	Rows  []FootprintRow `json:"rows"`
	Usage *ProcUsage     `json:"usage,omitempty"`
}
```

- [ ] **Step 4: 写 Manager.FootprintAll**

在 `internal/agentd/status.go` 追加：

```go
// FootprintAll 体检全部任务（含已归档）的进程足迹。
//
// 返回：
//   - 每个任务一行（含判定结论）与本机 uid 占用；查询任务列表失败才返回错误
//
// 注意：
//   - **只读，绝不发信号**：本方法只数不杀。数出来之后要不要动手是人的决定
//   - 与 status 分开的理由：本方法遍历全部历史任务目录，天然是慢命令；
//     status 有「不能变成慢命令」的硬纪律，两者不能合并
//   - 已归档任务同样体检：Done 只删 worktree、不删任务目录，凭据都还在
func (m *Manager) FootprintAll() (*proto.FootprintResp, error) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("足迹体检：查询任务列表失败", "cause", err)
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	m.log.Info("足迹体检开始", "tasks", len(tasks))
	resp := &proto.FootprintResp{Rows: make([]proto.FootprintRow, 0, len(tasks))}
	scanned, withProcs := 0, 0
	for _, t := range tasks {
		ad, aerr := m.adapterFor(t.ID)
		if aerr != nil {
			continue
		}
		fp, ok := ad.(footprinter)
		if !ok {
			continue
		}
		h, herr := fp.ProcHandle(t.ID, filepath.Join(m.cfg.DataDir, "tasks", t.ID))
		if herr != nil {
			continue // 无凭据（多为从未启动或已清理）：不是异常，不入表
		}
		scanned++
		members, v, ferr := prochost.Footprint(h)
		if ferr != nil {
			m.log.Warn("足迹体检：枚举失败", "task", t.ID, "cause", ferr)
			continue
		}
		if len(members) > 0 {
			withProcs++
		}
		resp.Rows = append(resp.Rows, proto.FootprintRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State),
			Procs: len(members), Verdict: string(v),
		})
	}
	if used, limit, uerr := prochost.UIDUsage(); uerr == nil {
		resp.Usage = &proto.ProcUsage{Used: used, Limit: limit}
	} else {
		m.log.Warn("足迹体检：读不到进程占用，该字段留空", "cause", uerr)
	}
	m.log.Info("足迹体检完成", "scanned", scanned, "with_procs", withProcs, "rows", len(resp.Rows))
	return resp, nil
}
```

- [ ] **Step 5: 加 HTTP 端点与客户端方法**

`internal/agentd/server.go`——路由表注释（现 166 行附近）加一行 `//   - GET  /api/footprint                 全任务进程足迹体检`，注册（现 183 行附近）加：

```go
	mux.HandleFunc("GET /api/footprint", s.handleFootprint)
```

handler 照 `handleStatus` 同构：

```go
// handleFootprint 返回全部任务（含已归档）的进程足迹体检。
//
// 注意：这是慢接口——它遍历全部历史任务目录逐个枚举进程。与 /api/status
// 分开正是为了不把那条「必须快」的诊断路径拖下水。
func (s *Server) handleFootprint(w http.ResponseWriter, r *http.Request) {
	s.log.Info("足迹体检请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法体检足迹")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.FootprintAll()
	if err != nil {
		s.log.Error("足迹体检失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("足迹体检请求完成", "rows", len(resp.Rows))
	writeJSON(w, http.StatusOK, resp)
}
```

`internal/client/client.go` 照 `Status` 同构：

```go
// Footprint 拉取对端全部任务的进程足迹体检结果。
//
// 返回：
//   - 体检结果；404（对端 agentd 过旧、没有这个端点）返回 ErrFootprintUnsupported
//   - 请求失败或响应非法时返回错误
//
// 注意：这是慢命令——对端要遍历全部历史任务目录，调用方应给足超时。
func (c *Client) Footprint(ctx context.Context) (*proto.FootprintResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/footprint", nil)
	if err != nil {
		return nil, fmt.Errorf("足迹体检请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 与 Status 的 404 同款处理：这是**预期结论**不是异常，用 Debug 而非
		// Info——调用方会把它渲染成人读的一句话，库层再打 Info 就是重复
		c.log().Debug("对端 agentd 不支持 /api/footprint，按版本过旧处理")
		return nil, ErrFootprintUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("足迹体检", resp)
	}
	var out proto.FootprintResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析足迹体检响应: %w", err)
	}
	return &out, nil
}
```

并在 `ErrStatusUnsupported` 旁加：

```go
// ErrFootprintUnsupported 表示对端 agentd 太旧，没有 /api/footprint。
//
// 与 ErrStatusUnsupported 分开而不复用：调用方要给出的处置建议不同
//（那条说「升级后才能看状态」，这条说「升级后才能看进程足迹，眼下只能上机器 ps」）
var ErrFootprintUnsupported = errors.New("对端 agentd 不支持足迹体检")
```

- [ ] **Step 6: 写 CLI**

创建 `cmd/footprint.go`：

```go
// footprint.go —— `handoff footprint` 命令：体检全部任务的进程足迹。
//
// 职责：
//   - 拉取对端全部任务（含已归档）的进程占用与判定结论并渲染
//
// 边界：
//   - **只数不杀**：本命令不回收任何进程。清扫由 agentd 在 executor 判死时
//     自动完成（见 spec §3.4），本命令只负责让人看见
//   - 不改任何任务状态、不发事件
package cmd
```

命令体（照 `cmd/status.go` 的 `TargetEndpoint` + `client.New` + `--json` 三段式）：

```go
var (
	footprintJSONOut bool
	footprintAll     bool
)

// footprintCmd 体检全部任务的进程足迹。
//
// 使用方式：handoff footprint [--target <名字>] [--all] [--json]
var footprintCmd = &cobra.Command{
	Use:   "footprint",
	Short: "查看各任务占用的进程数与本机进程余量（只数不杀）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		fp, err := client.New(addr, token).Footprint(cmd.Context())
		switch {
		case errors.Is(err, client.ErrFootprintUnsupported):
			// 与 status 同款：404 是一条成功的诊断结论，不是失败
			fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
			fmt.Fprintln(out, "限制     该 agentd 不支持 /api/footprint，足迹不可得")
			fmt.Fprintln(out, "处置     升级远端 agentd 后重试")
			return nil
		case err != nil:
			return err
		}
		if footprintJSONOut {
			return json.NewEncoder(out).Encode(fp)
		}
		renderFootprint(out, fp)
		return nil
	},
}

func init() {
	footprintCmd.Flags().BoolVar(&footprintJSONOut, "json", false, "以 JSON 输出")
	footprintCmd.Flags().BoolVar(&footprintAll, "all", false, "显示全部任务（默认只显示有残留或判不出结论的）")
	rootCmd.AddCommand(footprintCmd)
}

// renderFootprint 渲染体检结果。
//
// 默认过滤规则：只显示 Procs > 0（确有残留）或 Verdict != ok（我们不敢下结论）
// 的行。**后者必须显示，哪怕 Procs 是 0**——「没有残留」与「判不出」是两回事，
// 把判不出的行按 0 过滤掉，等于用一个假结论把该看的东西藏起来。
func renderFootprint(w io.Writer, fp *proto.FootprintResp) {
	if fp.Usage != nil {
		fmt.Fprintf(w, "进程     %d/%d（本机 uid 已用/上限）\n", fp.Usage.Used, fp.Usage.Limit)
	} else {
		// nil 不能渲染成 0/0：见 proto.ProcUsage 的 why
		fmt.Fprintln(w, "进程     未知（对端未提供）")
	}
	shown := 0
	for _, r := range fp.Rows {
		if !footprintAll && r.Procs == 0 && r.Verdict == string(prochost.VerdictOK) {
			continue
		}
		shown++
		line := fmt.Sprintf("  %s  %s  %s  %d 进程", short8(r.TaskID), r.Name, r.State, r.Procs)
		if r.Verdict != string(prochost.VerdictOK) {
			line += "  ⚠ " + r.Verdict
		}
		fmt.Fprintln(w, line)
	}
	if shown == 0 {
		fmt.Fprintln(w, "足迹     无残留（共体检 "+strconv.Itoa(len(fp.Rows))+" 个任务）")
	}
}
```

`short8` 复用 `cmd/status.go` 已有的同名函数（同包）。

- [ ] **Step 7: 写 CLI 测试**

创建 `cmd/footprint_test.go`（`runFootprint` 照 `cmd/status_test.go` 的 `runStatus` 同构：`writeStatusConfig(t)` 造配置、`rootCmd.SetArgs` + `SetOut`、`t.Cleanup` 里复位标志位）：

```go
// footprintBody 是三行体检结果：确有残留 / 判不出结论但 0 进程 / 干净。
const footprintBody = `{"usage":{"used":346,"limit":2666},"rows":[
	{"task_id":"aaaaaaaa-1111-2222-3333-444455556666","name":"有残留","state":"waiting_review","procs":7,"verdict":"ok"},
	{"task_id":"bbbbbbbb-1111-2222-3333-444455556666","name":"判不出","state":"completed","procs":0,"verdict":"leader_reuse"},
	{"task_id":"cccccccc-1111-2222-3333-444455556666","name":"干净","state":"completed","procs":0,"verdict":"ok"}]}`

// TestFootprintShowsResidueAndUnverdicted 默认过滤：有残留的与判不出的都要显示，
// 干净的不显示。
//
// **判不出的那行是这条用例的重点**：它 procs=0，一个只按 procs>0 过滤的实现
// 会把它藏起来——那正是「用一个假结论盖住该看的东西」。
func TestFootprintShowsResidueAndUnverdicted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("footprint 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"346/2666", "aaaaaaaa", "7 进程", "bbbbbbbb", "leader_reuse"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "cccccccc") {
		t.Fatalf("干净任务不该默认显示：\n%s", out)
	}
}

// TestFootprintAllShowsEverything --all 时三行都在。
func TestFootprintAllShowsEverything(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(footprintBody))
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, true)
	if err != nil {
		t.Fatalf("footprint --all 应成功: %v", err)
	}
	for _, want := range []string{"aaaaaaaa", "bbbbbbbb", "cccccccc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--all 输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestFootprintDegradesOn404 老 agentd 返回 404：输出降级结论，**且不报错**。
func TestFootprintDegradesOn404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	out, err := runFootprint(t, writeStatusConfig(t), ts.URL, false)
	if err != nil {
		t.Fatalf("404 是一条成功的诊断结论，不该报错: %v", err)
	}
	if !strings.Contains(out, "版本过旧") {
		t.Fatalf("应输出降级结论：\n%s", out)
	}
}
```

- [ ] **Step 8: 运行全量测试**

Run:
```bash
go build ./... && go test ./... -count=1 2>&1 | tail -30
```
Expected: 全部包 ok

- [ ] **Step 9: 加关键节点日志**

已内联，核对：`FootprintAll` 进入打 Info（带任务数）、退出打 Info（带 scanned / with_procs / rows）——**成功路径不静默**；枚举失败与占用读取失败各有 Warn 带 cause。CLI 侧不打日志（它是前台命令，输出即反馈）。

- [ ] **Step 10: 加注释自检**

核对：`cmd/footprint.go` 有文件头（职责 + 「只数不杀」边界）；`FootprintAll` 文档注释含返回与三条注意；`FootprintRow.Verdict` 有「为何恒非空」；`Client.Footprint` 有「这是慢命令」提示。

- [ ] **Step 11: 提交**

```bash
gofmt -l . && go vet ./... && git add -A && git commit -m "feat(cli): 新增 handoff footprint——全任务进程足迹只读体检"
```

---

### Task 9: 真机烟测与收尾自检

**Files:**
- Create: `docs/superpowers/notes/2026-08-12-footprint-smoke.md`（烟测记录）

**Interfaces:**
- Consumes: 前 8 个 task 的全部产出
- Produces: 烟测证据（供 backlog 验收列引用）

- [ ] **Step 1: 六闸门**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1 && go test -race ./internal/prochost/ ./internal/agentd/ && GOOS=windows go build ./...
```
Expected: 全部通过，`gofmt -l .` 无输出

- [ ] **Step 2: 起隔离实例**

按 B47 先例（**不占 devbox**，其上有他人任务在跑）：

```bash
go build -o /tmp/handoff-fp ./
mkdir -p /tmp/handoff-fp-data
cp -r /path/to/repo /tmp/handoff-fp-repo
```

用独立端口（7891）+ 独立 DataDir（`/tmp/handoff-fp-data`）+ 自带 config 起 agentd。**起之前先记下 7777 那个 agentd 的 pid，收尾时确认它没被碰过。**

- [ ] **Step 3: 复现当前缺陷（修复前的对照）**

在隔离实例上派一个任务，等 executor 起来后：

```bash
# 记下 shim pgid 与组内成员
/tmp/handoff-fp footprint --target local-fp
# 直接杀掉 executor 本身（绕开 done/stop，模拟自然死亡）
kill -9 <executor_pid>
```

预期（修复后）：agentd 日志出现 `残留进程已清扫 task=… killed=N`，`footprint` 中该任务的 Procs 归零。

**若在实现前先跑这一步**，预期是残留进程继续存活、日志无任何清扫记录——那是当前缺陷的复现证据，记进烟测文档。

- [ ] **Step 4: 验证不误杀**

确认隔离实例之外的进程未受影响：

```bash
ps -p <7777_agentd_pid> -o pid,lstart,command
```
Expected: pid 与启动时刻前后一致

- [ ] **Step 5: 写烟测记录**

创建 `docs/superpowers/notes/2026-08-12-footprint-smoke.md`，记录：隔离实例参数、修复前后的 `footprint` 输出对照、agentd 日志中的清扫行、7777 agentd pid 前后一致的证据。

- [ ] **Step 6: 按 instrumenting-code 收尾自检**

逐条核对（任一不过就回去补）：

- [ ] 每个错误分支都带上下文与 cause
- [ ] 每个外部调用（sysctl / /proc / kill）前后有日志
- [ ] **成功路径不静默**：`Sweep` 成功、`FootprintAll` 完成、`Start` 记录 StartedAt 均有 Info
- [ ] 无 `fmt.Printf` / `print` 作为日志机制
- [ ] 8 个新文件均有文件头（职责 + 边界）
- [ ] 全部新增导出方法有文档注释（参数/返回/注意）
- [ ] 非显然分支有「为什么」注释（锁状态区分、时间下界、无条件清扫、指针字段语义）

- [ ] **Step 7: 更新 backlog**

把 B69、B70 从 `💡 idea` 推进到 `✅ done(已验)`，`验收` 列填：六闸门结果 + 五处变异检验的复现记录 + 烟测文档链接。

**注意**：backlog 的 B69/B70 两行与「待验证的空白」条目当前在**主检出**里未提交（建工作树之前改的），合并前需要处理这个分叉。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && git add -A && git commit -m "docs: B69/B70 真机烟测记录与 backlog 验收"
```
