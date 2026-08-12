# 出生登记与点名回收（Plan B / B72）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `Sweep` 能回收 executor 通过 Bash 工具 `setsid` 逃逸出去的后代进程树——靠 shim 在树还活着时周期落盘的后代名册（pid + 启动时刻），executor 死后按名册点名，启动时刻对不上一律不碰。

**Architecture:** 逃逸毁掉的不是「管住」而是「事后凭亲缘认人」：`setsid` 改 pgid/sid，改不了 ppid，更改不了出生事实。所以在**活着的时候记账**——shim 内一个 15s 周期的 goroutine 沿 ppid 链闭包出自己的全部后代，原子写进任务目录的 `roster.json`；`Sweep` 扩成两段，第一段照旧按 pgid 清扫（B69 已有），第二段读名册逐条点名，**pid 存活且 `started_at` 完全一致**才发信号。判据变严不变松：漏杀由 B73 的围栏兜底（只吃预算，不致命），误杀没有兜底（B47 误杀 114 次的教训）。

**Tech Stack:** Go 1.26.1、`golang.org/x/sys/unix`（darwin sysctl KERN_PROC / linux `/proc`）、`log/slog`、标准库 `os`/`encoding/json`。无新依赖。

## Global Constraints

以下逐条抄自 spec `docs/superpowers/specs/2026-08-12-proc-fence-and-registry-design.md`，每个 task 的要求都隐含包含本节。

1. **防线全链路零 fork**（spec §5）。数余量（sysctl）、装围栏（setrlimit）、读进程表（sysctl / `/proc`）、发信号（`syscall.Kill`）——全部进程内系统调用。清扫路径（本 plan 的全部代码）内出现任何 `exec.Command` / `os/exec` 即为实现失败：它会在最需要的时刻，和 2026-08-12 那条 `ps | wc -l` 死在同一个地方。
2. **宁漏勿错**（spec §4.2，B47 红线）。名册里任何一条只要「pid 不存在」或「`started_at` 与当前进程表不完全相等」，一律视为 pid 已易主，**绝不发信号**。不允许「差不多就是它」的近似匹配（不许比较区间、不许只比 pid、不许容差）。
3. **fail-open，不 fail-closed**。名册缺失、损坏、字段不全——一律降级为「本次没有第二段清扫」并打日志，绝不阻断第一段，更不能返回错误让上层把任务判失败。
4. **不猜值**。判不出结论时如实呈现「未知」，不得回退成 0 或编一个像模像样的结论（B69 `Verdict` 三态立下的纪律）。
5. **日志用 slog（包内经 `log()`），禁止 `fmt.Printf`。** 周期循环内的日志降到 Debug，避免刷屏。
6. **六闸门全绿才算完工**：`go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./... -count=1`、`go test -race ./internal/prochost/ ./internal/agentd/`、`GOOS=windows go build ./...`。

## 文件结构

| 文件 | 职责 | 本 plan 的动作 |
|---|---|---|
| `internal/prochost/procenum.go` | 进程枚举的平台无关契约 | 修改：`procEntry` 加 `PPID` |
| `internal/prochost/procenum_darwin.go` | darwin sysctl 实现 | 修改：填 `Eproc.Ppid` |
| `internal/prochost/procenum_linux.go` | linux `/proc` 实现 | 修改：`readStat` 多返回 ppid |
| `internal/prochost/roster.go` | **新增**：后代闭包 + 名册读写 | 创建 |
| `internal/prochost/roster_test.go` | **新增**：闭包与读写的纯逻辑用例 | 创建 |
| `internal/prochost/shim.go` | shim 进程入口 | 修改：起周期落盘 goroutine |
| `internal/prochost/prochost.go` | `Spec`/`Handle`/`Start` | 修改：`Handle.RosterPath` + `Start` 填充 |
| `internal/prochost/footprint.go` | `classify`/`Footprint`/`Sweep` | 修改：Sweep 第二段、Footprint 并入名册 |
| `internal/prochost/footprint_test.go` | 判据用例 | 修改：补第二段的安全用例 |
| `internal/prochost/shim_test.go` | shim 集成用例 | 修改：补名册落盘用例 |
| `docs/superpowers/notes/2026-08-12-proc-birth-registry-smoke.md` | **新增**：真机烟测记录 | 创建 |
| `README.md` / `docs/superpowers/backlog.md` | 文档与总账 | 修改：能力说明与验收回填 |

---

### Task 1: `procEntry` 补 PPID（两个平台原语）

后代闭包要沿 ppid 链走，而现在的 `procEntry` 只有 `PID`/`PGID`/`StartedAt`——**没有 ppid**。这是 Plan B 的地基，先补上。

**Files:**
- Modify: `internal/prochost/procenum.go:28-32`
- Modify: `internal/prochost/procenum_darwin.go:21-38`
- Modify: `internal/prochost/procenum_linux.go:47-111`
- Test: `internal/prochost/procenum_test.go`（追加）

**Interfaces:**
- Consumes: 无（本 plan 的第一个 task）
- Produces: `procEntry{PID, PPID, PGID int; StartedAt int64}`——Task 2 的 `descendantsOf` 靠 `PPID` 建链

- [ ] **Step 1: 写失败用例**

追加到 `internal/prochost/procenum_test.go`：

```go
// 枚举结果里本进程那条的 PPID 必须等于内核认的父进程——这是后代闭包唯一的
// 链接字段，两个平台各写各的解析，用真进程对一次比任何桩都可靠。
func TestEnumProcsFillsPPID(t *testing.T) {
	procs, err := enumProcs()
	if err != nil {
		t.Skipf("本平台不支持进程枚举: %v", err)
	}
	self, ppid := os.Getpid(), os.Getppid()
	for _, p := range procs {
		if p.PID == self {
			if p.PPID != ppid {
				t.Fatalf("本进程 PPID 应为 %d，枚举得到 %d", ppid, p.PPID)
			}
			return
		}
	}
	t.Fatalf("枚举结果里没有本进程 pid=%d", self)
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run TestEnumProcsFillsPPID ./internal/prochost/ -v`
Expected: 编译失败 `p.PPID undefined (type procEntry has no field or method PPID)`

- [ ] **Step 3: 契约里加字段**

`internal/prochost/procenum.go`，`procEntry` 改为：

```go
// procEntry 是一个进程的足迹相关属性。
//
// StartedAt 为 unix 纳秒，两个平台都归一到这个单位——身份校验要把成员的启动
// 时刻与 shim 的启动时刻直接比较，单位不统一这条判据就是错的。
//
// PPID 是出生登记（roster）唯一的链接字段：setsid 改得了 pgid/sid，改不了
// ppid。进程树活着时沿它能闭包出全部后代；树一死 ppid 就断（后代被 reparent
// 给 init/launchd），所以它只在**记账时**可用，不能在清扫时才去追——这正是
// 「出生登记」要在活着的时候落盘的原因。
type procEntry struct {
	PID       int
	PPID      int
	PGID      int
	StartedAt int64
}
```

- [ ] **Step 4: darwin 实现填 PPID**

`internal/prochost/procenum_darwin.go`，`enumProcs` 的循环体改为：

```go
	for i := range kps {
		st := kps[i].Proc.P_starttime
		out = append(out, procEntry{
			PID:       int(kps[i].Proc.P_pid),
			PPID:      int(kps[i].Eproc.Ppid),
			PGID:      int(kps[i].Eproc.Pgid),
			StartedAt: int64(st.Sec)*int64(time.Second) + int64(st.Usec)*int64(time.Microsecond),
		})
	}
```

- [ ] **Step 5: linux 实现填 PPID**

`internal/prochost/procenum_linux.go`，`readStat` 的签名与解析改为（**注意字段序号**：`idx+2` 之后 `fields[0]=state`、`fields[1]=ppid`、`fields[2]=pgrp`、`fields[19]=starttime`）：

```go
// readStat 解析 /proc/<pid>/stat，取 ppid（字段 4）、pgrp（字段 5）与 starttime（字段 22）。
//
// 注意：字段 2 是 comm，可能含空格与右括号（如 "(my prog)"），因此必须从
// **最后一个** ')' 之后开始切分，不能直接按空格分割整行。
func readStat(pid int, bootNano int64) (ppid, pgid int, startedAt int64, err error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, 0, 0, err
	}
	s := string(b)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, 0, 0, fmt.Errorf("stat 格式异常 pid=%d", pid)
	}
	// idx+2 起是字段 3（state）；fields[0]=state, fields[1]=ppid, fields[2]=pgrp,
	// fields[19]=starttime
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 20 {
		return 0, 0, 0, fmt.Errorf("stat 字段不足 pid=%d, got %d", pid, len(fields))
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 ppid pid=%d: %w", pid, err)
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 pgrp pid=%d: %w", pid, err)
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("解析 starttime pid=%d: %w", pid, err)
	}
	return ppid, pgid, bootNano + ticks*int64(time.Second)/clockTick, nil
}
```

同文件 `enumProcs` 里的调用处改为：

```go
		ppid, pgid, start, perr := readStat(pid, boot)
		if perr != nil {
			continue // 同上：读到一半进程没了
		}
		out = append(out, procEntry{PID: pid, PPID: ppid, PGID: pgid, StartedAt: start})
```

- [ ] **Step 6: 跑用例确认通过**

Run: `go test -run "TestEnumProcs" ./internal/prochost/ -v`
Expected: `--- PASS: TestEnumProcsFindsSelf`、`--- PASS: TestEnumProcsFillsPPID`

- [ ] **Step 7: 加注释自检**

本 task 没有新文件。确认：`procEntry` 的 `PPID` 已带「为什么只能在记账时用」的 why（Step 3 已写）；linux `readStat` 的字段序号注释已同步更新（Step 5 已写）。**不要**给 `PPID: int(kps[i].Eproc.Ppid)` 这种行加注释——那是复述代码。

- [ ] **Step 8: 提交**

```bash
git add internal/prochost/procenum.go internal/prochost/procenum_darwin.go internal/prochost/procenum_linux.go internal/prochost/procenum_test.go
git commit -m "feat(prochost): 进程枚举补 PPID——出生登记唯一的链接字段"
```

---

### Task 2: 后代闭包（纯逻辑）

**Files:**
- Create: `internal/prochost/roster.go`
- Test: `internal/prochost/roster_test.go`

**Interfaces:**
- Consumes: Task 1 的 `procEntry.PPID`
- Produces:
  - `type rosterEntry struct { PID int; StartedAt int64 }`（JSON tag `pid` / `started_at`）
  - `func descendantsOf(root int, procs []procEntry) []rosterEntry`

- [ ] **Step 1: 写失败用例**

创建 `internal/prochost/roster_test.go`（本 task 只用到 `sort`/`testing`/`time`；`os` 与 `path/filepath` 到 Task 3 才用上，现在写进来会因未使用而编译失败）：

```go
package prochost

import (
	"sort"
	"testing"
	"time"
)

// 一条 shim→executor→bash→go 的链，中间那一层 setsid 逃逸（pgid 自成一组）。
// 闭包必须靠 ppid 走到底，pgid 变了不影响。
func TestDescendantsOfFollowsPPIDAcrossSetsid(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 1000},   // shim（root）
		{PID: 101, PPID: 100, PGID: 100, StartedAt: 1100}, // executor，同组
		{PID: 102, PPID: 101, PGID: 102, StartedAt: 1200}, // bash 工具，setsid 逃逸
		{PID: 103, PPID: 102, PGID: 102, StartedAt: 1300}, // go test，逃逸树内
		{PID: 200, PPID: 1, PGID: 200, StartedAt: 1150},   // 无关进程
	}
	got := descendantsOf(100, procs)
	want := []rosterEntry{
		{PID: 101, StartedAt: 1100},
		{PID: 102, StartedAt: 1200},
		{PID: 103, StartedAt: 1300},
	}
	assertRoster(t, got, want)
}

// root 自己绝不能进名册：第二段清扫会对名册里每个 pid 发信号，shim 自己在里面
// 等于自杀，而且第一段的 pgid 清扫本来就覆盖它。
func TestDescendantsOfExcludesRoot(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: 1000},
		{PID: 101, PPID: 100, PGID: 100, StartedAt: 1100},
	}
	for _, e := range descendantsOf(100, procs) {
		if e.PID == 100 {
			t.Fatalf("root 不得出现在名册里: %+v", descendantsOf(100, procs))
		}
	}
}

// 自环/互指不能让闭包死循环——真实进程表里 pid 1 的 ppid 就是 0 或 1，
// 而被 reparent 的进程在快照的两条记录之间也可能出现看起来成环的形态。
func TestDescendantsOfTerminatesOnCycle(t *testing.T) {
	procs := []procEntry{
		{PID: 100, PPID: 100, PGID: 100, StartedAt: 1000}, // 自环
		{PID: 101, PPID: 102, PGID: 101, StartedAt: 1100}, // 与 102 互指
		{PID: 102, PPID: 101, PGID: 102, StartedAt: 1200},
	}
	done := make(chan []rosterEntry, 1)
	go func() { done <- descendantsOf(100, procs) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("自环 root 不该有后代，得到 %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("descendantsOf 未终止，闭包缺少 visited 保护")
	}
}

// 空表 / root 不在表里：返回空名册且不 panic（fail-open 的最基本形态）。
func TestDescendantsOfEmptyInputs(t *testing.T) {
	if got := descendantsOf(100, nil); len(got) != 0 {
		t.Fatalf("空进程表应得空名册，得到 %+v", got)
	}
	procs := []procEntry{{PID: 200, PPID: 1, PGID: 200, StartedAt: 1000}}
	if got := descendantsOf(100, procs); len(got) != 0 {
		t.Fatalf("root 不在表里应得空名册，得到 %+v", got)
	}
}

// assertRoster 按 pid 排序后逐条比对（闭包的遍历顺序不是契约的一部分）。
func assertRoster(t *testing.T, got, want []rosterEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("名册条数不符: got %+v, want %+v", got, want)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].PID < got[j].PID })
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("名册第 %d 条不符: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run TestDescendantsOf ./internal/prochost/ -v`
Expected: 编译失败 `undefined: descendantsOf` / `undefined: rosterEntry`

- [ ] **Step 3: 写实现**

创建 `internal/prochost/roster.go`：

```go
// roster.go —— 出生登记：后代名册的闭包、落盘与读取。
//
// 职责：
//   - 在进程树**还活着**的时候，沿 ppid 链闭包出 shim 的全部后代
//   - 把名册（pid + 启动时刻）原子落盘，供 executor 死后点名回收
//   - 读回名册，容忍缺失与损坏
//
// 边界：
//   - 不发任何信号、不做存活判定——点名与回收是 footprint.go 的 Sweep 的事
//   - 不做增量维护：每次快照都是全量重算。最后一次快照 ≈ executor 死亡时刻的
//     存活者，早退的短命进程自然不在里面，无需追踪它们的死亡
//   - 不碰 proc.json（那是 adapter 独占的文件），名册是独立文件
package prochost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RosterFileName 是后代名册的文件名（与 proc.json 同目录）。
const RosterFileName = "roster.json"

// rosterEntry 是名册里的一条：一个后代进程的 pid 与它的出生时刻。
//
// 为什么必须带 StartedAt：pid 会被内核复用。清扫发生在 executor 死后，名册
// 落盘与点名之间隔着不确定的时间，期间该 pid 完全可能已经属于另一个无关进程
// （极端情况下是 agentd 或用户的登录 shell）。出生时刻是这条记录的身份凭据，
// 对不上就是另一个进程——B47 误杀 114 次的教训，这里宁漏勿错。
type rosterEntry struct {
	PID       int   `json:"pid"`
	StartedAt int64 `json:"started_at"`
}

// rosterPath 由 proc.json 的路径推出名册路径（同目录，固定文件名）。
//
// 为什么不让调用方各自拼：shim 写、Start 记、Sweep 读，三处必须完全一致，
// 拼错一个字符的表现是「名册永远为空」——一个不报错、只是悄悄不干活的故障。
func rosterPath(infoPath string) string {
	if infoPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(infoPath), RosterFileName)
}

// descendantsOf 从进程快照里闭包出 root 的全部后代（不含 root 自己）。
//
// 参数：
//   - root: 起点 pid（生产里是 shim 自己）
//   - procs: 一次进程快照（当前 uid 的全部进程）
//
// 返回：后代的 pid 与启动时刻；root 不在快照里或没有后代时返回空切片
//
// 为什么按 ppid 而不是 pgid：executor 经 Bash 工具拉起的子进程会 setsid 自成
// 会话与进程组（2026-08-12 devbox 实证：`33365 92657 33365 (zsh)`，父进程是
// opencode serve 但 pgid 是它自己），pgid 判据看不到它们。ppid 不受 setsid 影响。
//
// 注意：
//   - **本函数只在树活着时有意义**。executor 一死，后代被 reparent 给
//     init/launchd，ppid 链当场断在最需要它的地方——所以名册必须在活着的时候
//     周期落盘，而不是清扫时现算
//   - visited 集合是必需的：真实快照里 pid 1 的 ppid 是 0 或 1（自环），且快照
//     是非原子的，两条记录之间可能出现看起来成环的形态。没有它会死循环
func descendantsOf(root int, procs []procEntry) []rosterEntry {
	if root <= 0 || len(procs) == 0 {
		return nil
	}
	// 先按 ppid 建反向索引，避免每一层都全表扫描（进程表可达数千条）
	children := make(map[int][]procEntry, len(procs))
	for _, p := range procs {
		if p.PID == p.PPID {
			continue // 自环：pid 1 的常见形态，不可能是别人的后代链的一环
		}
		children[p.PPID] = append(children[p.PPID], p)
	}
	visited := map[int]bool{root: true}
	queue := []int{root}
	out := make([]rosterEntry, 0, 8)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			if visited[c.PID] {
				continue
			}
			visited[c.PID] = true
			out = append(out, rosterEntry{PID: c.PID, StartedAt: c.StartedAt})
			queue = append(queue, c.PID)
		}
	}
	return out
}
```

- [ ] **Step 4: 跑用例确认通过**

Run: `go test -run TestDescendantsOf ./internal/prochost/ -v`
Expected: 四条全 PASS

- [ ] **Step 5: 加关键节点日志**

本 task 的两个函数**刻意不打日志**，且这个决定要写进注释（否则下一个人会以为是漏了）。在 `descendantsOf` 的注释「注意」段末尾补一条：

```go
//   - 本函数刻意不打日志：它每 15s 被调用一次、且是纯函数，日志放在调用方
//     （shim 的周期落盘）边界上记一次入参与结论即可，这里再记等于同一件事
//     写两遍并按周期刷屏
```

- [ ] **Step 6: 加注释自检**

对照 `instrumenting-code`：新文件 `roster.go` 已有文件头（职责 + 边界，Step 3 已写）；`rosterEntry`/`rosterPath`/`descendantsOf` 三个声明都有注释且写的是 why 不是 what；`visited`、自环跳过两处非显然分支已有中文「为什么」。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/roster.go internal/prochost/roster_test.go
git commit -m "feat(prochost): 后代闭包——沿 ppid 链穿透 setsid 逃逸"
```

---

### Task 3: 名册原子落盘与读取

**Files:**
- Modify: `internal/prochost/roster.go`
- Test: `internal/prochost/roster_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 的 `rosterEntry`、`rosterPath`
- Produces:
  - `func writeRoster(path string, entries []rosterEntry) error`
  - `func readRoster(path string) ([]rosterEntry, error)`——名册不存在时返回 `(nil, nil)`，**不是错误**

- [ ] **Step 1: 写失败用例**

先把 `internal/prochost/roster_test.go` 的 import 块补成（本 task 开始用文件系统）：

```go
import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)
```

再追加用例：

```go
// 写—读往返：落盘的内容必须原样读回来。
func TestWriteReadRosterRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), RosterFileName)
	want := []rosterEntry{{PID: 101, StartedAt: 1100}, {PID: 102, StartedAt: 1200}}
	if err := writeRoster(path, want); err != nil {
		t.Fatalf("写名册: %v", err)
	}
	got, err := readRoster(path)
	if err != nil {
		t.Fatalf("读名册: %v", err)
	}
	assertRoster(t, got, want)
}

// 名册不存在**不是错误**：任务刚起来还没到第一次落盘、或这是升级前建的老任务，
// 都是正常形态。Sweep 靠 (nil, nil) 安静跳过第二段，不能因此把清扫判成失败。
func TestReadRosterMissingIsNotError(t *testing.T) {
	got, err := readRoster(filepath.Join(t.TempDir(), RosterFileName))
	if err != nil {
		t.Fatalf("名册缺失不该报错，得到 %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("名册缺失应得空名册，得到 %+v", got)
	}
}

// 名册损坏必须**报错**而不是当成空名册：空名册意味着「确实没有后代」，
// 损坏意味着「有后代但我读不出来」，两者对调用方是不同的决定（后者要打日志
// 让人看见），这与 errNotSupported 不能退化成空集是同一条纪律。
func TestReadRosterCorruptReportsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), RosterFileName)
	if err := os.WriteFile(path, []byte("{不是 json"), 0o600); err != nil {
		t.Fatalf("造损坏文件: %v", err)
	}
	if _, err := readRoster(path); err == nil {
		t.Fatal("损坏的名册必须报错，不得静默当成空名册")
	}
}

// 落盘必须原子：读者任何时刻看到的要么是上一版完整名册，要么是新版完整名册，
// 不存在读到半截的窗口。这里断言临时文件没有残留，且权限是 0600（名册暴露
// 本机进程结构，与 spec.json 同级别对待）。
func TestWriteRosterIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, RosterFileName)
	if err := writeRoster(path, []rosterEntry{{PID: 101, StartedAt: 1100}}); err != nil {
		t.Fatalf("写名册: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录: %v", err)
	}
	if len(ents) != 1 || ents[0].Name() != RosterFileName {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("目录里应只剩名册本身（临时文件必须已 rename 掉），实得 %v", names)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("名册权限应为 0600，实得 %v", fi.Mode().Perm())
	}
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run "TestWriteRoster|TestReadRoster|TestWriteReadRoster" ./internal/prochost/ -v`
Expected: 编译失败 `undefined: writeRoster` / `undefined: readRoster`

- [ ] **Step 3: 写实现**

追加到 `internal/prochost/roster.go`：

```go
// writeRoster 把名册原子写到 path（临时文件 + rename）。
//
// 参数：
//   - path: 名册路径（rosterPath 的结果）
//   - entries: 本次快照的全部后代；空切片是合法输入（表示这一刻没有后代）
//
// 返回：临时文件写失败或 rename 失败时返回错误
//
// 为什么必须原子：读者是另一个进程（agentd 的 Sweep），它随时可能在 shim
// 正在写的瞬间读。直接覆盖写会让读者拿到半截 JSON——而半截 JSON 解析失败会
// 被当成「名册损坏」，于是一次正常的周期写入就变成了一条错误日志。
//
// 为什么临时文件放同目录：rename 只有在同一文件系统内才是原子的。
func writeRoster(path string, entries []rosterEntry) error {
	if path == "" {
		return fmt.Errorf("名册路径为空")
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("序列化名册: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写名册临时文件 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // 尽力而为：留着它下次会被覆盖，删不掉也不影响正确性
		return fmt.Errorf("落盘名册 %s: %w", path, err)
	}
	return nil
}

// readRoster 读回名册。
//
// 参数：path 为名册路径；空串等同于「没有名册」
//
// 返回：
//   - entries: 名册内容；没有名册时为 nil
//   - err: 文件存在但读不动或解析失败
//
// 注意：**文件不存在返回 (nil, nil) 而不是错误**。三种正常形态都会走到这里：
// 任务刚起来还没到第一次落盘、升级前建的老任务、adapter 不带 InfoPath。
// 把它们当错误会让 Sweep 每次都记一条假故障，真故障就淹没了。
// 但**解析失败必须报错**：那是「有名册却读不出来」，与「没有名册」是两回事。
func readRoster(path string) ([]rosterEntry, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读名册 %s: %w", path, err)
	}
	var entries []rosterEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("解析名册 %s: %w", path, err)
	}
	return entries, nil
}
```

- [ ] **Step 4: 跑用例确认通过**

Run: `go test -run "Roster" ./internal/prochost/ -v`
Expected: Task 2 与本 task 的用例全 PASS

- [ ] **Step 5: 加关键节点日志**

这两个函数同样不打日志——它们是被周期调用的 I/O 原语，日志在调用方边界记。把这个决定写进 `writeRoster` 注释末尾：

```go
// 注意：本函数不打日志。它每 15s 被调用一次，成功路径打日志就是按周期刷屏；
// 失败由调用方（shim 的周期落盘）统一记一条 Warn 并继续——名册写不出去只
// 意味着这一轮没有第二段清扫，不值得中断任务。
```

- [ ] **Step 6: 加注释自检**

确认 `writeRoster` 有「为什么必须原子」「为什么临时文件放同目录」；`readRoster` 有「为什么缺失不是错误、损坏是错误」；两者都有参数/返回说明。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/roster.go internal/prochost/roster_test.go
git commit -m "feat(prochost): 名册原子落盘与读取——缺失非错误，损坏是错误"
```

---

### Task 4: shim 内周期落盘

**Files:**
- Modify: `internal/prochost/shim.go:124-149`
- Test: `internal/prochost/shim_test.go`（追加）

**Interfaces:**
- Consumes: Task 2/3 的 `descendantsOf`、`writeRoster`、`rosterPath`
- Produces: shim 运行期间任务目录下持续更新的 `roster.json`

- [ ] **Step 1: 写失败用例**

追加到 `internal/prochost/shim_test.go`：

```go
// shim 必须在拉起 executor 后**立即**落一次名册，而不是等第一个周期到点。
// 短命任务（几秒就结束）在等待周期的窗口里死掉的话，名册永远是空的，
// 第二段清扫就等于不存在——这正是逃逸残留最容易发生的场景（编译、跑测试）。
func TestShimWritesRosterImmediately(t *testing.T) {
	dir := t.TempDir()
	// 让 executor 活得久一点，好让我们在它还活着时读到名册
	spec := baseSpec(dir, "sleep 5")
	specPath := writeSpec(t, dir, spec)

	t.Setenv(helperEnv, "shimentry")
	t.Setenv("PROCHOST_TEST_SPEC", specPath)
	pid, err := spawnDetached(
		[]string{os.Args[0], "-test.run", "^TestHelperShimEntry$", "-test.v=false"},
		dir, nil)
	if err != nil {
		t.Fatalf("拉起 shim: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	path := rosterPath(spec.InfoPath)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		entries, rerr := readRoster(path)
		if rerr == nil && len(entries) > 0 {
			// 名册里必须有真实存活的 pid，且带非零出生时刻
			for _, e := range entries {
				if e.PID <= 0 || e.StartedAt <= 0 {
					t.Fatalf("名册条目字段不全: %+v", e)
				}
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("10s 内没等到非空名册（path=%s）", path)
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run TestShimWritesRosterImmediately ./internal/prochost/ -v`
Expected: FAIL `10s 内没等到非空名册`

- [ ] **Step 3: 写实现**

`internal/prochost/shim.go`：先在文件的 var 区加采样间隔（放在 `SentinelPrefix` 常量附近）：

```go
// rosterInterval 是后代名册的采样间隔。
//
// 为什么是 15s：名册的陈旧度上界就是它——间隔内出生并在下次快照前逃逸的进程
// 会漏记（由 B73 的围栏兜底，只吃预算不致命）。再密下去每次都要全量枚举进程表
// （数千条）且对回收成功率没有实质提升：真正堆积的是长命的编译/测试进程，
// 它们活得远比 15s 长。
//
// 是变量而非常量：测试要把它调到毫秒级，否则每条周期用例都真等 15s。
var rosterInterval = 15 * time.Second
```

然后在 `RunShim` 里，`l.Info("执行者进程已启动", ...)` 之后、`code := 0` 之前插入：

```go
	// 出生登记：趁进程树还活着，周期把后代名册落盘。executor 一死后代就被
	// reparent 给 init/launchd，ppid 链当场断——名册是那之后唯一还能凭出生
	// 事实认人的东西（why 见 roster.go 的 descendantsOf）
	stopRoster := make(chan struct{})
	rosterDone := make(chan struct{})
	go func() {
		defer close(rosterDone)
		snapshotRoster(l, spec.InfoPath)
		tk := time.NewTicker(rosterInterval)
		defer tk.Stop()
		for {
			select {
			case <-stopRoster:
				return
			case <-tk.C:
				snapshotRoster(l, spec.InfoPath)
			}
		}
	}()
```

在 `cmd.Wait()` 之后、写哨兵之前，停掉它：

```go
	// executor 已退出，停止采样。最后一次快照留在盘上，它 ≈ 死亡时刻的存活者，
	// 正是第二段清扫要点名的那批
	close(stopRoster)
	<-rosterDone
```

在文件末尾新增采样函数：

```go
// snapshotRoster 采一次后代名册并落盘。
//
// 参数：
//   - l: 已带 lock 字段的日志入口
//   - infoPath: adapter 的 proc.json 路径，名册与它同目录
//
// 注意：**任何一步失败都只打日志、不中断 shim**。名册写不出去只意味着这一轮
// 没有第二段清扫（残留由围栏兜底），为它杀掉正在干活的 executor 是本末倒置。
func snapshotRoster(l *slog.Logger, infoPath string) {
	path := rosterPath(infoPath)
	if path == "" {
		l.Warn("无 info_path，无法落盘后代名册，本任务不做出生登记")
		return
	}
	procs, err := enumProcsFn()
	if err != nil {
		l.Warn("枚举进程失败，本轮跳过出生登记", "cause", err)
		return
	}
	entries := descendantsOf(os.Getpid(), procs)
	if err := writeRoster(path, entries); err != nil {
		l.Warn("落盘后代名册失败，本轮跳过出生登记", "path", path, "cause", err)
		return
	}
	// Debug 级：这是每 15s 一次的周期日志，Info 会把任务日志刷满
	l.Debug("后代名册已更新", "path", path, "count", len(entries))
}
```

`shim.go` 的 import 需要补 `log/slog`（`snapshotRoster` 的参数类型）。

- [ ] **Step 4: 跑用例确认通过**

Run: `go test -run "TestShim" ./internal/prochost/ -v`
Expected: `--- PASS: TestShimWritesRosterImmediately`，且 `TestShimLogsLandInTaskDirShimLog` 等既有用例不回归

- [ ] **Step 5: 加关键节点日志**

对照 `instrumenting-code` 逐条确认（本 task 是唯一有周期循环的地方，格外要看）：
- 进入关键操作：`snapshotRoster` 的三条失败分支各有 Warn 且带 cause/path 上下文 ✓
- 成功路径不静默：有 Debug 级的「后代名册已更新」带 count ✓
- 循环内降级到 Debug：✓（周期日志用 Debug，不是 Info）
- 无 `fmt.Printf`：✓

**补一条**：goroutine 退出时记一次收尾，否则「名册停在哪一刻」在日志里无迹可寻。在 `close(stopRoster); <-rosterDone` 之后加：

```go
	l.Info("出生登记已停止", "roster", rosterPath(spec.InfoPath))
```

- [ ] **Step 6: 加注释自检**

确认：`rosterInterval` 有「为什么是 15s」与「为什么是变量」；goroutine 起点有「为什么要趁活着记」；停止处有「为什么最后一次快照就是要点名的那批」；`snapshotRoster` 有参数说明与「失败不中断」的 why。

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/shim.go internal/prochost/shim_test.go
git commit -m "feat(prochost): shim 周期落盘后代名册，起手立即采一次"
```

---

### Task 5: `Handle.RosterPath` 接线

`Sweep(h Handle)` 手上只有 `PID`/`LockPath`/`StartedAt`，找不到名册。照 `StartedAt` 的先例给 `Handle` 加一个 `omitempty` 字段，由 `Start` 填，升级前的 `proc.json` 读出空串即安静跳过第二段。

**Files:**
- Modify: `internal/prochost/prochost.go:74-88`（`Handle`）、`:233-277`（`Start`）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: Task 3 的 `rosterPath`
- Produces: `Handle{PID, LockPath, StartedAt, RosterPath}`——Task 6 的 `Sweep` 靠 `RosterPath` 找名册

- [ ] **Step 1: 写失败用例**

追加到 `internal/prochost/footprint_test.go`。写法照抄同文件里 `TestStartRecordsStartedAt` 的路数——**用 `/bin/sh` 顶替真 shim 作为 selfExe**，因为本用例只验 Handle 字段有没有被填上，不验 shim 行为：

```go
// Start 必须把名册路径记进 Handle：Sweep 在 agentd 进程里跑，它只有 proc.json
// 反序列化出来的 Handle，没有 spec，推不出任务目录。这个字段是两个进程之间
// 唯一的交接点，漏填的表现是「第二段清扫永远静默地不干活」。
func TestStartRecordsRosterPath(t *testing.T) {
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
	hd, err := Start(spec, "/bin/sh", "-c", "sleep 5")
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(func() { _ = killGroup(hd.PID) })
	want := filepath.Join(dir, RosterFileName)
	if hd.RosterPath != want {
		t.Fatalf("Handle.RosterPath 应为 %s，实得 %q", want, hd.RosterPath)
	}
}

// 升级前写下的 proc.json 没有 roster_path 字段，读出来是空串。这必须是一条
// 安静的降级路径（只做第一段清扫），不是错误——老任务不该因为升级就被动手。
func TestHandleWithoutRosterPathDecodesEmpty(t *testing.T) {
	var h Handle
	if err := json.Unmarshal([]byte(`{"pid":100,"lock_path":"/tmp/x.lock","started_at":1000}`), &h); err != nil {
		t.Fatalf("解析老 proc.json: %v", err)
	}
	if h.RosterPath != "" {
		t.Fatalf("老 proc.json 应解出空 RosterPath，实得 %q", h.RosterPath)
	}
}
```

`footprint_test.go` 的 import 块要补 `encoding/json`（现有块是 `errors`/`path/filepath`/`sort`/`testing`/`time`）。

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run "TestStartRecordsRosterPath|TestHandleWithoutRosterPath" ./internal/prochost/ -v`
Expected: 编译失败 `h.RosterPath undefined`

- [ ] **Step 3: 给 Handle 加字段**

`internal/prochost/prochost.go`，在 `Handle` 的 `StartedAt` 之后追加：

```go
	// RosterPath 是后代名册（roster.json）的路径，第二段清扫的入口。
	//
	// 为什么要记在 Handle 里而不是让 Sweep 自己推：Sweep 跑在 agentd 进程里，
	// 手上只有 proc.json 反序列化出来的 Handle——它没有 spec，也就没有
	// InfoPath，推不出任务目录。这个字段是两个进程之间唯一的交接点。
	//
	// omitempty + 零值语义：升级前写下的 proc.json 没有这个字段，读出空串即
	// 跳过第二段清扫（只做 pgid 那段），与 StartedAt 缺失时降级为只上报是
	// 同一条纪律——老任务不会因为升级就被动手。
	RosterPath string `json:"roster_path,omitempty"`
```

- [ ] **Step 4: `Start` 填充**

`internal/prochost/prochost.go` 的 `Start` 末尾，返回语句改为：

```go
	roster := rosterPath(spec.InfoPath)
	log().Info("shim 已拉起", "pid", pid, "bin", spec.Argv[0], "spec", specPath,
		"started_at", startedAt, "roster", roster)
	return Handle{
		PID:        pid,
		LockPath:   spec.LockPath,
		StartedAt:  startedAt,
		RosterPath: roster,
	}, nil
```

- [ ] **Step 5: 跑用例确认通过**

Run: `go test ./internal/prochost/ -count=1`
Expected: `ok`

- [ ] **Step 6: 加关键节点日志**

`Start` 的「shim 已拉起」那条已补上 `roster` 字段（Step 4）——这是关键节点的入参记录，排障时能一眼看出名册该在哪。无新增分支，无需其它日志。

- [ ] **Step 7: 加注释自检**

确认 `RosterPath` 字段注释写清了「为什么要记在 Handle 里」与「空串怎么降级」，而不是「名册路径」这种复述。

- [ ] **Step 8: 提交**

```bash
git add internal/prochost/prochost.go internal/prochost/footprint_test.go
git commit -m "feat(prochost): Handle 记名册路径，老 proc.json 空串降级"
```

---

### Task 6: `Sweep` 第二段——点名回收

**Files:**
- Modify: `internal/prochost/footprint.go:173-216`（`Sweep`）
- Modify: `internal/prochost/prochost.go:141-165`（新增 `killProcFn` 接缝）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: Task 3 的 `readRoster`、Task 5 的 `Handle.RosterPath`
- Produces:
  - `var killProcFn = killProc`（单 pid 发信号的测试接缝）
  - `func rosterKill(h Handle, procs []procEntry) (killed int)`
  - `Sweep` 的 `killed` 返回值改为「两段之和」

- [ ] **Step 1: 写失败用例**

追加到 `internal/prochost/footprint_test.go`：

```go
// stubKillProc 记录单 pid 信号的调用序列，供第二段清扫的用例断言「杀了谁」。
func stubKillProc(t *testing.T) *[]int {
	t.Helper()
	var got []int
	orig := killProcFn
	killProcFn = func(pid int) error { got = append(got, pid); return nil }
	t.Cleanup(func() { killProcFn = orig })
	return &got
}

// 名册里的 pid 存活且出生时刻完全一致 —— 点名回收。
func TestSweepKillsRosterMembers(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	// **必须同时 stub 组信号**：不 stub 就会对 pgid=100 发真 SIGKILL；而且这里
	// 断言它调用 0 次本身就是判据——第二段绝不能退化成按组杀
	groupN := stubKillGroup(t, nil)
	// 第一段：组内只有 501 且它自成一组，shim 的组是空的 → 无成员；
	// 第二段：501 在表里且出生时刻吻合 → 点名回收
	stubEnum(t, []procEntry{{PID: 501, PPID: 1, PGID: 501, StartedAt: 5100}}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 1 {
		t.Fatalf("应回收 1 个名册成员，实得 %d", n)
	}
	if len(*killed) != 1 || (*killed)[0] != 501 {
		t.Fatalf("应对 pid 501 单独发信号，实得 %v", *killed)
	}
	if *groupN != 0 {
		t.Fatalf("点名回收必须逐个发信号，不得按组杀，实得组信号 %d 次", *groupN)
	}
}

// **B47 红线**：pid 还在，但出生时刻对不上 —— pid 已易主，绝不发信号。
// 这条用例是整个 Plan B 最重要的一条：判据松掉的代价是杀掉用户的无关进程。
func TestSweepSkipsRosterMemberWithMismatchedBirth(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	// pid 501 还在，但它的出生时刻是 9999——这是内核把 501 分给了别的进程
	stubEnum(t, []procEntry{{PID: 501, PPID: 1, PGID: 501, StartedAt: 9999}}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	if n != 0 {
		t.Fatalf("出生时刻不符不得回收，实得 killed=%d", n)
	}
	if len(*killed) != 0 {
		t.Fatalf("出生时刻不符时绝不能发信号，实得 %v", *killed)
	}
}

// 名册里的 pid 已经不在进程表里 —— 早就退了，无需动作，也不算失败。
func TestSweepSkipsRosterMemberAlreadyGone(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	shrinkBackoff(t)
	stubAlive(t, false)
	killed := stubKillProc(t)
	stubEnum(t, []procEntry{}, nil)
	h := Handle{PID: 100, StartedAt: t0, RosterPath: roster}
	n, _, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 0 || len(*killed) != 0 {
		t.Fatalf("已退出的成员不该有任何动作，实得 killed=%d, signals=%v", n, *killed)
	}
}

// 没有名册（老任务/刚起来）：第一段照常，第二段安静跳过，不报错。
func TestSweepWithoutRosterStillDoesGroupPhase(t *testing.T) {
	shrinkBackoff(t)
	stubAlive(t, false)
	stubKillGroup(t, nil) // 第一段会真的发组信号，必须 stub
	stubEnum(t,
		[]procEntry{{PID: 101, PPID: 100, PGID: 100, StartedAt: t0 + 1}}, nil,
		[]procEntry{})
	h := Handle{PID: 100, StartedAt: t0} // RosterPath 为空
	n, v, err := Sweep(h)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if v != VerdictOK || n != 1 {
		t.Fatalf("无名册时第一段仍应正常，实得 killed=%d verdict=%s", n, v)
	}
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run "TestSweepKillsRoster|TestSweepSkipsRoster|TestSweepWithoutRoster" ./internal/prochost/ -v`
Expected: 编译失败 `undefined: killProcFn`

- [ ] **Step 3: 加单 pid 发信号的原语与接缝**

`internal/prochost/prochost.go`，在 `aliveFn / killGroupFn` 的 var 块里追加 `killProcFn`，并在同文件（或 `platform_unix.go` / `platform_other.go`，与 `killGroup` 放一起，保持平台切分一致）实现 `killProc`：

```go
// killProc 对**单个 pid** 发 SIGKILL（不是进程组）。
//
// 为什么不能复用 killGroup：第二段清扫的对象是 setsid 逃逸出去的进程，它们
// 各自成组，组里往往还有它们自己的无关兄弟；按组发信号会把没经过身份校验的
// 进程一起带走——那正是 B47 误杀的形态。名册逐条校验、逐条发信号，一条一条来。
func killProc(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("非法 pid %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}
```

接缝：

```go
	// killProcFn 是单 pid 发信号的测试接缝：名册点名的安全用例要断言
	// 「哪些 pid 被发了信号」，真进程做不出「出生时刻不符」这种形态
	killProcFn = killProc
```

- [ ] **Step 4: 写第二段实现**

`internal/prochost/footprint.go`，新增：

```go
// rosterKill 执行第二段清扫：按出生名册点名回收 setsid 逃逸的后代。
//
// 参数：
//   - h: 任务句柄，用 h.RosterPath 找名册
//   - procs: 一次进程快照（与第一段共用，避免重复枚举）
//
// 返回：killed 为**实际发出信号**的成员数
//
// 判据（spec §4.2，一条都不能松）：名册里的一条只有在「pid 出现在当前进程表」
// **且**「StartedAt 与表里那条完全相等」时才发信号。任一不符即视为 pid 已易主，
// 绝不发信号——漏杀只是让残留多活一会儿（由 B73 的围栏兜住，只吃预算），
// 误杀则可能打掉用户的登录 shell 或 agentd 自己（B47 误杀 114 次的教训）。
//
// 注意：
//   - 名册缺失（RosterPath 为空、文件不在）是**正常形态**，安静返回 0
//   - 名册损坏只打日志并返回 0，不影响已经完成的第一段
//   - 逐条发信号而不是按组：逃逸者各自成组，按组会带走未经校验的兄弟进程
func rosterKill(h Handle, procs []procEntry) (killed int) {
	entries, err := readRoster(h.RosterPath)
	if err != nil {
		log().Warn("读后代名册失败，跳过点名回收", "pid", h.PID,
			"roster", h.RosterPath, "cause", err)
		return 0
	}
	if len(entries) == 0 {
		log().Debug("无后代名册或名册为空，跳过点名回收", "pid", h.PID, "roster", h.RosterPath)
		return 0
	}
	live := make(map[int]int64, len(procs))
	for _, p := range procs {
		live[p.PID] = p.StartedAt
	}
	var skipped int
	for _, e := range entries {
		started, ok := live[e.PID]
		if !ok {
			continue // 早就退了：常态，不是异常
		}
		if started != e.StartedAt {
			// pid 已易主。这条日志必须是 Warn 并带两个时刻：它是「我们差点杀错」
			// 的唯一现场记录，出现频率高本身就是个值得追的信号
			log().Warn("名册成员 pid 已易主，拒绝发信号", "pid", e.PID,
				"roster_started_at", e.StartedAt, "actual_started_at", started)
			skipped++
			continue
		}
		if kerr := killProcFn(e.PID); kerr != nil {
			log().Error("回收名册成员失败", "pid", e.PID, "cause", kerr)
			continue
		}
		killed++
	}
	log().Info("点名回收完成", "pid", h.PID, "roster_total", len(entries),
		"killed", killed, "skipped_reused", skipped)
	return killed
}
```

`Sweep` 内部改动（保持签名不变）：

1. 第一段判定为放弃（`v != VerdictOK`）时**仍然要跑第二段**——`leader_reuse` 说的是「shim 的 pgid 被复用了」，与名册成员的身份无关，名册每条自己带凭据。把原来的 `return 0, v, nil` 改为先跑 `rosterKill` 再返回：

```go
	members, v := classify(h, procs, false)
	if v != VerdictOK {
		// 第一段放弃 ≠ 第二段也得放弃：leader_reuse/no_credential 说的是 shim
		// 自己的凭据出了问题，而名册里每条成员都自带 pid+出生时刻的凭据，
		// 两者的信任来源是独立的
		log().Warn("组清扫放弃，仍尝试点名回收", "pid", h.PID, "verdict", string(v))
		return rosterKill(h, procs), v, nil
	}
```

2. 「无残留可清扫」分支同样要跑第二段：

```go
	if len(members) == 0 {
		log().Info("组内无残留，转入点名回收", "pid", h.PID)
		return rosterKill(h, procs), VerdictOK, nil
	}
```

3. 正常路径：组清扫成功复核后，加上第二段的战果：

```go
		if left, _ := classify(h, rest, false); len(left) == 0 {
			n := rosterKill(h, rest)
			log().Info("清扫完成，已确认残留退出", "pid", h.PID,
				"group_killed", len(members), "roster_killed", n, "probe", i+1)
			return len(members) + n, VerdictOK, nil
		}
```

4. 复核窗口走完仍有残留的失败分支保持不变（不跑第二段：组都没杀干净，先把这个问题交给人）。

- [ ] **Step 5: 跑用例确认通过**

Run: `go test ./internal/prochost/ -count=1 -v 2>&1 | grep -E "^(--- (PASS|FAIL)|ok|FAIL)" | head -40`
Expected: 新增四条全 PASS，既有 `TestSweep*` 无回归

- [ ] **Step 6: 加关键节点日志**

对照 `instrumenting-code`：
- 每个错误分支带上下文 ✓（读名册失败带 roster 路径与 cause；发信号失败带 pid 与 cause）
- 成功路径不静默 ✓（「点名回收完成」带 roster_total/killed/skipped_reused 三个数）
- **最关键的一条**：「pid 已易主，拒绝发信号」是 Warn 且带名册记的与实际读到的两个时刻——这是安全判据真正生效的唯一现场证据，也是烟测第 ④ 条要 grep 的那行
- 无 `fmt.Printf` ✓

- [ ] **Step 7: 加注释自检**

确认：`killProc` 有「为什么不能复用 killGroup」；`rosterKill` 有参数/返回/判据/三条注意；`Sweep` 里「第一段放弃仍跑第二段」这个反直觉的决定有 why（Step 4 已写）。

同时更新 `Sweep` 的函数注释——它现在的「注意」里写着「判据只覆盖与 shim 同进程组的成员」，这句已经**不再成立**，必须改写，否则它会变成一条骗人的注释：

```go
//   - **两段判据**：第一段按 pgid 回收「shim + executor 本体」这一层；第二段按
//     出生名册点名回收 executor 经 Bash 工具 setsid 逃逸出去的后代（B72）。
//     第二段依赖 shim 生前落盘的 roster.json，缺失时自动降级为只做第一段——
//     升级前建的任务、或 shim 还没来得及落第一次名册就死了的任务，都是这个形态
```

- [ ] **Step 8: 提交**

```bash
git add internal/prochost/footprint.go internal/prochost/prochost.go internal/prochost/footprint_test.go
git commit -m "feat(prochost): Sweep 第二段点名回收——出生时刻对不上绝不发信号"
```

---

### Task 7: `Footprint` 并入名册（口径一致）

**这一步超出 spec §4.2 的字面范围（那里只说扩展 Sweep），是自觉补的。** 理由：不补的话 `handoff footprint` / `handoff status` 会报「这个任务占 1 个进程」，而 `Sweep` 转头杀掉 6 个——**同一个任务的两个数字互相矛盾**，而其中小的那个恰好漏掉了这个 feature 存在的全部理由。B70 立的规矩是「宣称什么就得是什么」，两段回收上线后，可见性也得跟着变成两段。

**Files:**
- Modify: `internal/prochost/footprint.go:136-145`（`Footprint`）
- Test: `internal/prochost/footprint_test.go`（追加）

**Interfaces:**
- Consumes: Task 3 的 `readRoster`、Task 6 的匹配规则
- Produces: `func rosterMembers(h Handle, procs []procEntry) []int`；`Footprint` 的 `members` 变为两段并集

- [ ] **Step 1: 写失败用例**

```go
// Footprint 报的数必须和 Sweep 会动手的范围一致：只报 pgid 那层、却杀两层，
// 等于让 handoff footprint 的输出骗人（B70：宣称什么就得是什么）。
func TestFootprintIncludesRosterMembers(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true) // executor 活着，Footprint 对活任务也要能用
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},        // shim
		{PID: 101, PPID: 100, PGID: 100, StartedAt: t0 + 1},  // executor，同组
		{PID: 501, PPID: 101, PGID: 501, StartedAt: 5100},    // 逃逸后代，在名册里
	}, nil)
	h := Handle{PID: 100, LockPath: filepath.Join(dir, "x.lock"), StartedAt: t0, RosterPath: roster}
	members, v, err := Footprint(h)
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	if v != VerdictOK {
		t.Fatalf("verdict 应为 ok，实得 %s", v)
	}
	assertMembers(t, members, []int{100, 101, 501})
}

// 与 Sweep 同一条红线：出生时刻对不上的名册成员**不计入**足迹。
// 数字上多算一个只是难看，但它会让审核者以为残留还在、去追一个不存在的东西。
func TestFootprintExcludesReusedRosterPID(t *testing.T) {
	dir := t.TempDir()
	roster := filepath.Join(dir, RosterFileName)
	if err := writeRoster(roster, []rosterEntry{{PID: 501, StartedAt: 5100}}); err != nil {
		t.Fatalf("造名册: %v", err)
	}
	stubAlive(t, true)
	stubEnum(t, []procEntry{
		{PID: 100, PPID: 1, PGID: 100, StartedAt: t0},
		{PID: 501, PPID: 1, PGID: 501, StartedAt: 9999}, // pid 易主
	}, nil)
	h := Handle{PID: 100, LockPath: filepath.Join(dir, "x.lock"), StartedAt: t0, RosterPath: roster}
	members, _, err := Footprint(h)
	if err != nil {
		t.Fatalf("Footprint: %v", err)
	}
	assertMembers(t, members, []int{100})
}
```

- [ ] **Step 2: 跑用例确认失败**

Run: `go test -run "TestFootprintIncludesRoster|TestFootprintExcludesReused" ./internal/prochost/ -v`
Expected: FAIL，`members` 里没有 501

- [ ] **Step 3: 写实现**

`internal/prochost/footprint.go` 新增（判据与 `rosterKill` 共用同一条规则，但只读不发信号）：

```go
// rosterMembers 按出生名册筛出仍然存活且身份吻合的后代 pid（只读，不发信号）。
//
// 参数：h 为任务句柄（用 h.RosterPath）；procs 为一次进程快照
//
// 返回：通过「pid 在表 + StartedAt 完全相等」校验的 pid；名册缺失/损坏时为空
//
// 注意：它与 rosterKill 是同一条判据的只读孪生版，两者必须同时改——一个报数、
// 一个动手，判据分叉就意味着「报的和杀的不是同一批」。
func rosterMembers(h Handle, procs []procEntry) []int {
	entries, err := readRoster(h.RosterPath)
	if err != nil {
		log().Warn("读后代名册失败，足迹不含逃逸后代", "pid", h.PID,
			"roster", h.RosterPath, "cause", err)
		return nil
	}
	if len(entries) == 0 {
		return nil
	}
	live := make(map[int]int64, len(procs))
	for _, p := range procs {
		live[p.PID] = p.StartedAt
	}
	var out []int
	for _, e := range entries {
		if started, ok := live[e.PID]; ok && started == e.StartedAt {
			out = append(out, e.PID)
		}
	}
	return out
}
```

`Footprint` 改为并集（**去重**：逃逸后代理论上不会同时出现在 pgid 组里，但名册是异步快照，不能假设两边不相交）：

```go
func Footprint(h Handle) (members []int, v Verdict, err error) {
	procs, err := enumProcsFn()
	if err != nil {
		log().Error("足迹枚举失败", "pid", h.PID, "cause", err)
		return nil, VerdictNoCredential, err
	}
	members, v = classify(h, procs, aliveFn(h))
	if v == VerdictOK {
		// 第二段：名册里仍然存活且身份吻合的逃逸后代。判定放弃时不并入——
		// 那时 members 必须为空是 classify 的契约，不能被这里破坏
		seen := make(map[int]bool, len(members))
		for _, p := range members {
			seen[p] = true
		}
		for _, p := range rosterMembers(h, procs) {
			if !seen[p] {
				members = append(members, p)
				seen[p] = true
			}
		}
	}
	log().Debug("足迹判定完成", "pid", h.PID, "verdict", string(v), "members", len(members))
	return members, v, nil
}
```

- [ ] **Step 4: 跑用例确认通过**

Run: `go test ./internal/prochost/ ./internal/agentd/ -count=1`
Expected: 两个包都 `ok`（`agentd` 侧的 status 用例会消费 `Footprint`，一并确认无回归）

- [ ] **Step 5: 加关键节点日志**

`rosterMembers` 只在读名册失败时打一条 Warn（它被 `handoff status` 高频调用，成功路径打日志会刷屏——这个理由要写进注释）。`Footprint` 末尾既有的 Debug 已包含合并后的 `members` 数，无需新增。

在 `rosterMembers` 注释末尾补：

```go
//   - 成功路径刻意不打日志：Footprint 被 handoff status 按任务高频调用，
//     每次记一行会把 agentd.log 淹掉。失败才记，且只记一次
```

- [ ] **Step 6: 加注释自检**

确认 `rosterMembers` 有「与 rosterKill 必须同时改」的警告；`Footprint` 里「判定放弃时不并入」的分支有 why。

**同时更新 B69 留下的两处过时注释**——它们现在会误导人：
- `classify` 的「判据覆盖边界」段：保留（`classify` 本身确实只管 pgid），但末尾补一句「逃逸后代由 `rosterMembers` 覆盖，见 B72 出生登记」
- `Footprint` 注释的「注意」段：补一条「members 是两段并集：pgid 组 + 出生名册里仍存活且身份吻合的逃逸后代」

- [ ] **Step 7: 提交**

```bash
git add internal/prochost/footprint.go internal/prochost/footprint_test.go
git commit -m "feat(prochost): Footprint 并入名册成员，报的数与杀的范围对齐"
```

---

### Task 8: 变异检验

用例存在 ≠ 用例有效（B47 纪律）。逐条：改码 → 跑指定用例确认 **FAIL** → 还原 → `git diff --exit-code` 确认干净。**六条一条都不能跳，也不能只在报告里声称做过。**

**Files:** 无（全部改完即还原）

- [ ] **Step 1: 变异一——名册判据去掉出生时刻比对**

`rosterKill` 里 `if started != e.StartedAt` 那段整体删除（只要 pid 在表里就杀）。

Run: `go test -run TestSweepSkipsRosterMemberWithMismatchedBirth ./internal/prochost/ -v`
Expected: **FAIL**（`出生时刻不符时绝不能发信号，实得 [501]`）

还原后：`git diff --exit-code`（无输出）

- [ ] **Step 2: 变异二——第二段按组发信号**

`rosterKill` 里 `killProcFn(e.PID)` 改为 `killGroupFn(e.PID)`。

Run: `go test -run TestSweepKillsRosterMembers ./internal/prochost/ -v`
Expected: **FAIL**（`应对 pid 501 单独发信号`——`stubKillProc` 记录为空，因为走的是 `killGroupFn`）

还原后：`git diff --exit-code`

- [ ] **Step 3: 变异三——闭包把 root 也算进后代**

`descendantsOf` 的 `visited := map[int]bool{root: true}` 改为 `visited := map[int]bool{}`，并在入队前把 root 自己也 append 进 out。

Run: `go test -run TestDescendantsOfExcludesRoot ./internal/prochost/ -v`
Expected: **FAIL**（`root 不得出现在名册里`）

还原后：`git diff --exit-code`

- [ ] **Step 4: 变异四——去掉闭包的 visited 保护**

`descendantsOf` 里 `if visited[c.PID] { continue }` 与两处 `visited[...] = true` 全部删除。

Run: `go test -run TestDescendantsOfTerminatesOnCycle -timeout 30s ./internal/prochost/ -v`
Expected: **FAIL**（`descendantsOf 未终止，闭包缺少 visited 保护`，或整包 `-timeout` 触发 panic）

还原后：`git diff --exit-code`

- [ ] **Step 5: 变异五——名册只在周期到点时落盘**

`RunShim` 的 goroutine 里，把 `snapshotRoster(l, spec.InfoPath)` 那次**立即调用**删掉（只留 ticker 分支）。

Run: `go test -run TestShimWritesRosterImmediately ./internal/prochost/ -v`
Expected: **FAIL**（`10s 内没等到非空名册`——`rosterInterval` 是 15s，10s 内不会有第一次落盘）

还原后：`git diff --exit-code`

- [ ] **Step 6: 变异六——名册缺失当成错误**

`readRoster` 里 `if os.IsNotExist(err) { return nil, nil }` 删掉（让它落到下面的 `return nil, fmt.Errorf(...)`）。

Run: `go test -run TestReadRosterMissingIsNotError ./internal/prochost/ -v`
Expected: **FAIL**（`名册缺失不该报错`）

只有这一条会 FAIL，这是预期的，不要顺手去「加强」别的用例来陪绑：
`TestSweepWithoutRosterStillDoesGroupPhase` 用的是**空 RosterPath**，走的是 `readRoster` 开头 `path == ""` 的短路分支，根本到不了 `os.IsNotExist`；即便到得了，`rosterKill` 出错也只返回 0，第一段的 1 照样返回，用例仍会 PASS。**这正是 fail-open 该有的形态**——名册出任何问题都不该影响第一段。把「缺失非错误」这条判据钉死的责任就在 `TestReadRosterMissingIsNotError` 一条身上，它是这条变异唯一的探针。

还原后：`git diff --exit-code`

- [ ] **Step 7: 确认工作区干净并记录**

```bash
git status --porcelain
```
Expected: 无输出。六条变异的 FAIL 用例名逐条记进完工报告。

---

### Task 9: 真机烟测 + 文档回填

spec §6 第 ④ 条：**roster 点名回收一个 setsid 逃逸的构造进程，且 pid 易主场景不发信号**。烟测进程在宿主 shell 构造，**不派发真任务**（B73 烟测已实证：真派发路径的窗口在机器噪声下不可复现，且这台机器刚瘫过，省着用）。

**Files:**
- Create: `docs/superpowers/notes/2026-08-12-proc-birth-registry-smoke.md`
- Modify: `README.md`、`docs/superpowers/backlog.md`

- [ ] **Step 1: 六闸门,贴实际输出**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
go test -race ./internal/prochost/ ./internal/agentd/
GOOS=windows go build ./...
```
六条命令的**实际输出**逐条记进烟测文档,不得只写「全绿」。

- [ ] **Step 2: 构造逃逸进程并验证点名回收**

在宿主 shell 里(隔离目录,不碰任何在跑的 agentd):

```bash
D=$(mktemp -d); go build -o "$D/handoff-b72" .
# spec 指向一个会 setsid 逃逸的子进程：sh 起一个 setsid 的 sleep，自己立刻退出，
# 于是 sleep 既不在 shim 的进程组里，也在 shim 死后成为孤儿——正是要回收的形态
cat > "$D/spec.json" <<EOF
{"argv":["/bin/sh","-c","setsid /bin/sleep 300 & sleep 30"],"dir":"$D","env":[],
 "stdout":"$D/serve.log","stderr":"$D/serve.log","lock_path":"$D/proc.lock",
 "info_path":"$D/proc.json","sentinel":false}
EOF
"$D/handoff-b72" _shim --spec "$D/spec.json" 2> "$D/shim.log" &
sleep 3
cat "$D/roster.json"     # 必须含 setsid 出去的那个 sleep 的 pid + started_at
```

要验证并记录的四条：
1. `roster.json` 里**确实有** setsid 逃逸的那个 `sleep` 的 pid（证明 ppid 闭包穿透了 setsid）
2. 杀掉 shim 后，逃逸的 `sleep` 仍然存活（证明 pgid 判据确实收不到它——**这是本 feature 的存在理由，必须当场证明一次，不能只引用 B72 的历史结论**）
3. 用一个最小 Go 程序（或 `handoff` 的既有入口）调 `Sweep`，确认该 `sleep` 被回收，且 `shim.log`/`agentd.log` 里有「点名回收完成 … killed=1」
4. **pid 易主场景**：手工把 `roster.json` 里某条的 `started_at` 改成一个错的值，再跑一次 `Sweep`，确认该 pid **没有**被杀，且日志里有 `名册成员 pid 已易主，拒绝发信号` 并带两个时刻

- [ ] **Step 3: 收尾并证明无附带损害**

```bash
pkill -f "handoff-b72 _shim" || true
rm -rf "$D"
ps -u $(whoami) -o command | grep -c handoff-b72   # 期望 0
```
若烟测在有生产 agentd 的机器上做，另需记录该 agentd 的 pid 前后一致。

- [ ] **Step 4: 写烟测记录**

`docs/superpowers/notes/2026-08-12-proc-birth-registry-smoke.md`，结构照 B73 的 `2026-08-12-proc-fence-smoke.md`：环境参数、六闸门实际输出、四条验证的命令与原文输出、无误伤与清理、**计划偏差/缺陷照实直说**。

- [ ] **Step 5: README 补能力说明**

在 `README.md` 讲进程回收/足迹的那一节补一段：两段回收的边界——第一段按进程组收 shim + executor 本体，第二段按出生名册收 executor 经 Bash 工具 `setsid` 逃逸的后代；名册每 15s 采样一次，**采样间隔内出生并逃逸的进程可能漏记**（由进程围栏兜底，只吃预算不致命）；出生时刻对不上的一律不碰（宁漏勿错）。

- [ ] **Step 6: backlog 回填**

`docs/superpowers/backlog.md` 的 B72 行：`🔨 doing` → `✅ done(已验)`，`验收` 列填六闸门实际结果 + 六条变异的 FAIL 用例名 + 烟测四条的结论；`原型/流程图` 为 `—`，自动免除对照。**若任何一条没验成，如实写 `done(未验)` 或把未验项列出来，不许含糊带过。**

- [ ] **Step 7: 提交**

```bash
git add docs/superpowers/notes/2026-08-12-proc-birth-registry-smoke.md README.md docs/superpowers/backlog.md
git commit -m "docs: 出生登记真机烟测记录与 B72 验收回填"
```

---

## 完工报告要包含

1. **六闸门的实际输出**（不是「全绿」两个字）。
2. **六条变异检验**各自的 FAIL 用例名与还原确认（`git diff --exit-code` 的结果）。
3. **烟测四条验证**的命令与原文输出，特别是第 2 条（逃逸进程在 shim 死后仍存活）与第 4 条（pid 易主拒绝发信号的日志原文）。
4. **Task 7 是超出 spec §4.2 的自觉扩展**，说明你是否认同这个判断；不认同就说理由。
5. 任何你认为计划写错了、或不得不偏离的地方**和原因**——计划写错了就直说，别硬凑。B73 那轮真机烟测照出两条计划缺陷和两条代码缺陷，那是最有价值的产出。
6. **已知局限如实列出**（spec §4.3）：采样间隔内的漏记窗口 ≤15s；名册陈旧度 ≤ 一个采样周期，且陈旧只会变成漏杀不会变成误杀。
