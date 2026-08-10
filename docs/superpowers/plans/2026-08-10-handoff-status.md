# handoff status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `handoff status`，一条命令回答「这个 agentd 能不能用、是什么」——可达性、版本、DataDir、执行者清单、任务计数，以及非终结任务的 executor 是否真活着。

**Architecture:** 新增 `GET /api/status`（走既有 Bearer 鉴权），服务端一次聚合完再返回，CLI 只渲染——因为存活判据在各 adapter 里，CLI 够不着。存活探测走新增的只读可选接口 `prober`，各 adapter 复用自己已有的 `Proc.Alive()`（判据不另写一份，避免与 `Resume` 分叉）。老 agentd 不认新路由会返回 404，CLI 把它直译成一条**成功的**诊断结论。

**Tech Stack:** Go 1.26，cobra（CLI）、`net/http` + Go 1.22 方法路由（服务端）、SQLite（`internal/store`）、`runtime/debug.ReadBuildInfo`（版本来源，无需 ldflags）。

**Spec:** `docs/superpowers/specs/2026-08-10-handoff-status-design.md`（backlog B33）

## Global Constraints

- **探活只读**：`Probe` 绝不 `Kill` 残留会话、不占 `runs` 位、不写 store、不发事件。这三件事 `Resume` 都做，正是它不能被复用的原因。
- **判据不许分叉**：`Probe` 复用各 adapter 已有的 `Proc.Alive()`，不新写一份判据。
- **超时归 `unknown`，绝不归 `dead`**：单次探测 `2 * time.Second`，整体总时限 `10 * time.Second`。假阳性是诊断命令最贵的失败模式。
- **退出码只回答「能不能用」**：`0` = 可达且鉴权通过（含老版 agentd、含探活超时、含版本不一致）；`1` = 够不着。不新增第三个退出码。
- **`TaskCounts` 六个状态的键恒存在**，计数为零也出现；文本渲染才省略零值。
- **JSON 里的任务 ID 始终是完整 UUID**，只有文本渲染用 8 位短 id（`store.GetTask` 是 `WHERE id = ?` 精确匹配，不做前缀查找）。
- **日志用 `slog`（各结构体已持有的 `log` 字段），禁止 `fmt.Printf`**；凭据类值绝不进日志（本特性不碰凭据，但 `opencode` 的 `serveInfo.Password` 在探活路径上出现过，不得打印）。
- 每个实现任务都必须完成「加关键节点日志」与「加注释」两步（文件头职责/边界、导出方法注释、复杂分支的「为什么」）。

## 与 spec 的两处偏离（已核对代码，均为简化）

1. **不再「把判死逻辑抽成纯函数」**（spec §3.3 约束 2 的原措辞）：四个 adapter 都**已经**有只读的 `Proc.Alive()`，且判据正是对的那份（`claudecode/proc.go:449` 的哨兵 + tmux 两条判据）。直接复用比抽取更省，且同样达成「判据不分叉」的目的。
2. **`StartedAt` 在 `NewServer` 内部记录**，而非从 bootstrap 传入（spec §4.3）：`NewServer` 只在 bootstrap 调用一次，语义等价，且避免改动它的签名与全部测试调用点。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/proto/status.go` | 新建 | `BuildInfo` / `ActiveTask` / `StatusResp` 线格式（只有数据）。新开文件而非改 `proto.go`，让 status 的契约集中一处 |
| `internal/buildinfo/buildinfo.go` | 新建 | 读当前二进制的 VCS 戳并归一成 `proto.BuildInfo`；`readBuildInfo` 是测试缝 |
| `internal/executor/probe.go` | 新建 | `ProbeReq` / `ProbeOutcome` 数据契约（只有数据，无接口——与 `resume.go` 同规格） |
| `internal/executor/claudecode/probe.go` | 新建 | claude 的只读 `Probe` |
| `internal/executor/opencode/probe.go` | 新建 | opencode 的只读 `Probe` |
| `internal/executor/grok/probe.go` | 新建 | grok 的只读 `Probe` |
| `internal/executor/codex/probe.go` | 新建 | codex 的只读 `Probe` |
| `internal/agentd/status.go` | 新建 | `Manager.Status()` 聚合 + `prober` 接口断言 + 带时限的逐个探活 |
| `internal/agentd/server.go` | 修改 | `Server` 持有 `startedAt`；注册 `GET /api/status`；`handleStatus` |
| `internal/client/client.go` | 修改 | `Status()` + `ErrStatusUnsupported` 哨兵 |
| `cmd/status.go` | 新建 | CLI：调用、文本/JSON 渲染、404 直译、退出码 |
| `skills/handoff/SKILL.md` | 修改 | 增「确认 agentd 在不在」一节 |
| `README.md` | 修改 | 命令清单补 `status` |

---

## Task 1: 构建标识可读（proto 契约 + buildinfo 包）

**Files:**
- Create: `internal/proto/status.go`
- Create: `internal/buildinfo/buildinfo.go`
- Test: `internal/buildinfo/buildinfo_test.go`

**Interfaces:**
- Consumes: 无（本任务是根）
- Produces: `proto.BuildInfo{Revision, Time string; Modified bool; Go string}`、`proto.ActiveTask`、`proto.StatusResp`、常量 `proto.LiveAlive/LiveDead/LiveUnknown`（值为 `"alive"`/`"dead"`/`"unknown"`）、`buildinfo.Read() (proto.BuildInfo, bool)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/buildinfo/buildinfo_test.go`：

```go
// buildinfo 包测试：VCS 戳解析，以及「测试二进制没有 vcs 戳」这一必须支持的形态。
package buildinfo

import (
	"runtime/debug"
	"testing"
)

// 有 vcs 戳时四个字段都要解析出来，含 modified 的字符串转布尔。
func TestReadParsesVCSSettings(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "8353ef68d711eaf63eeb1287f342f3238204aec8"},
				{Key: "vcs.time", Value: "2026-08-10T01:45:37Z"},
				{Key: "vcs.modified", Value: "true"},
				{Key: "GOARCH", Value: "arm64"},
			},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Revision != "8353ef68d711eaf63eeb1287f342f3238204aec8" {
		t.Fatalf("Revision=%q", got.Revision)
	}
	if got.Time != "2026-08-10T01:45:37Z" {
		t.Fatalf("Time=%q", got.Time)
	}
	if !got.Modified {
		t.Fatal("vcs.modified=true 必须解析为 Modified=true——它意味着这个二进制对不上任何一个提交")
	}
	if got.Go != "go1.26.1" {
		t.Fatalf("Go=%q", got.Go)
	}
}

// go test 编出的测试二进制就是这种形态：有 GoVersion，没有任何 vcs.* 设置。
// Revision 必须是空串（调用方据此显示「版本未知」），不得 panic、不得报错。
func TestReadWithoutVCSStamp(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.26.1",
			Settings:  []debug.BuildSetting{{Key: "GOARCH", Value: "arm64"}},
		}, true
	}
	t.Cleanup(func() { readBuildInfo = old })

	got, ok := Read()
	if !ok {
		t.Fatal("Read 应返回 ok=true")
	}
	if got.Revision != "" {
		t.Fatalf("非 go build 产物的 Revision 应为空，得到 %q", got.Revision)
	}
	if got.Go != "go1.26.1" {
		t.Fatalf("Go=%q", got.Go)
	}
}

// 读不到构建信息时返回 ok=false，调用方据此降级。
func TestReadUnavailable(t *testing.T) {
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	t.Cleanup(func() { readBuildInfo = old })

	if _, ok := Read(); ok {
		t.Fatal("读不到构建信息时应返回 ok=false")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/buildinfo/ -run TestRead -v`
Expected: FAIL——包不存在（`no Go files in .../internal/buildinfo`）

- [ ] **Step 3: 写 proto 契约**

创建 `internal/proto/status.go`：

```go
// status.go —— handoff status 的响应线格式。
//
// 职责：
//   - 定义 BuildInfo / ActiveTask / StatusResp 三个结构与 Live* 取值常量
//
// 边界：
//   - 只有数据，无行为、无 I/O（与本包其余部分同规格）
//   - 不定义「怎么展示」：文本渲染归 cmd/status.go
package proto

import "time"

// BuildInfo 是一个 handoff 二进制的构建标识，取自 runtime/debug.ReadBuildInfo。
//
// 字段说明：
//   - Revision: vcs.revision；**空串表示不是 go build 产物**（go run / 测试
//     二进制没有 vcs 戳），调用方应显示「版本未知」而不是空
//   - Time: vcs.time
//   - Modified: vcs.modified——true 表示这个二进制是带未提交改动编出来的，
//     它对不上任何一个提交，排障时这是关键信息
//   - Go: 编译所用 Go 版本
type BuildInfo struct {
	Revision string `json:"revision"`
	Time     string `json:"time"`
	Modified bool   `json:"modified"`
	Go       string `json:"go"`
}

// ActiveTask.Live 的三个取值。
//
// 为什么必须有 unknown：探不出结论时猜一个值就是在制造假阳性，而一条会说谎的
// 诊断命令比没有更糟——因为你会信它。
const (
	LiveAlive   = "alive"
	LiveDead    = "dead"
	LiveUnknown = "unknown"
)

// ActiveTask 是一个非终结任务及其 executor 存活结论。
//
// 注意：ID 始终是完整 UUID。文本渲染可以只显示前 8 位（与 tmux 会话命名
// handoff-<id8> 一致，便于人肉对照），但任何拿去当参数的地方都必须用完整 UUID
// ——store.GetTask 是精确匹配，不做前缀查找。
type ActiveTask struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Executor string `json:"executor"`
	RepoPath string `json:"repo_path"`
	Live     string `json:"live"` // LiveAlive / LiveDead / LiveUnknown
	Note     string `json:"note"` // 判死或判不出的一句话理由；alive 时为空
}

// StatusResp 是 GET /api/status 的响应。
//
// 注意：TaskCounts 的六个状态键恒存在，计数为零也出现——缺键与零值对消费方
// 是两回事。
type StatusResp struct {
	Version         BuildInfo      `json:"version"`
	Listen          string         `json:"listen"`
	DataDir         string         `json:"data_dir"`
	StartedAt       time.Time      `json:"started_at"`
	Executors       []string       `json:"executors"`
	DefaultExecutor string         `json:"default_executor"`
	TaskCounts      map[string]int `json:"task_counts"`
	Active          []ActiveTask   `json:"active"`
}
```

- [ ] **Step 4: 写 buildinfo 实现**

创建 `internal/buildinfo/buildinfo.go`：

```go
// Package buildinfo 读取当前二进制的构建标识（VCS revision、构建时刻、是否带
// 未提交改动）。
//
// 职责：
//   - Read：把 runtime/debug.ReadBuildInfo 的结果归一成 proto.BuildInfo
//
// 边界：
//   - 不做版本比较，也不做展示——那是 cmd/status.go 的事
//   - 不打日志：本包无 I/O、无外部调用，在这种纯取值函数里打日志只会制造噪音；
//     版本读取结果由调用方（status 聚合与 CLI 渲染）记录
//   - 单开一个包而不是塞进 cmd 或 agentd：CLI 与 agentd 都要读自己的构建标识，
//     放任何一边都会造成反向依赖
package buildinfo

import (
	"runtime/debug"

	"github.com/xushixin/handoff/internal/proto"
)

// readBuildInfo 是 debug.ReadBuildInfo 的测试缝（与各 adapter 的 tmuxHasSession
// 同手法）。
//
// why（必须可注入）：go test 编出的测试二进制**不带 vcs 戳**——Settings 里只有
// -buildmode / GOARCH / CGO_* 之类，没有 vcs.revision。真实调用在测试里恒返回
// 空 revision，断言无从写起。
var readBuildInfo = debug.ReadBuildInfo

// Read 返回当前二进制的构建标识。
//
// 返回：
//   - ok=false：读不到构建信息（极少见，如用非 go 工具链链接的二进制）
//   - Revision 为空：不是 go build 产物（go run / 测试二进制），调用方应显示
//     「版本未知」而不是空字符串
func Read() (proto.BuildInfo, bool) {
	bi, ok := readBuildInfo()
	if !ok {
		return proto.BuildInfo{}, false
	}
	out := proto.BuildInfo{Go: bi.GoVersion}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Revision = s.Value
		case "vcs.time":
			out.Time = s.Value
		case "vcs.modified":
			// go 把它序列化成字符串 "true"/"false"，不是布尔
			out.Modified = s.Value == "true"
		}
	}
	return out, true
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/buildinfo/ ./internal/proto/ -v`
Expected: PASS（buildinfo 三个用例全绿，proto 既有用例不受影响）

- [ ] **Step 6: 提交**

```bash
git add internal/proto/status.go internal/buildinfo/
git commit -m "feat(status): 加 status 线格式契约与 buildinfo 构建标识读取"
```

---

## Task 2: 探活契约 + claudecode 的只读 Probe

先做 claude，因为它的判据最微妙（tmux 会话在≠进程活着），把契约在最难的那个身上钉死。

**Files:**
- Create: `internal/executor/probe.go`
- Create: `internal/executor/claudecode/probe.go`
- Test: `internal/executor/claudecode/probe_test.go`

**Interfaces:**
- Consumes: 无（`internal/executor` 是底层包，不依赖 Task 1）
- Produces: `executor.ProbeReq{TaskID, TaskDir, SessionID string}`、`executor.ProbeOutcome{Alive bool; Note string}`、`(*claudecode.Adapter).Probe(executor.ProbeReq) (executor.ProbeOutcome, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/claudecode/probe_test.go`：

```go
// claudecode 只读探活测试。
//
// 覆盖三态与那条最容易误判的路径：tmux 会话还在（窗口 1 的 tail -f 吊着）
// 但 claude 已退——必须判死。
package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// tmux 会话在且无死亡哨兵 → 存活。
func TestProbeAlive(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	t.Cleanup(func() { tmuxHasSession = old })

	out, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !out.Alive {
		t.Fatal("tmux 会话在且无哨兵，应判存活")
	}
}

// 关键路径：tmux 会话还在但 out.jsonl 已有 handoff_exit 哨兵 → 必须判死。
// 这正是「manager 层统一 tmux has-session」会给出假阳性的那个反例。
func TestProbeSessionAliveButProcessExited(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, outFileName)
	if err := os.WriteFile(out, []byte(`{"type":"handoff_exit","code":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return true }
	t.Cleanup(func() { tmuxHasSession = old })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("哨兵已出现，即使 tmux 会话还在也必须判死")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由给审核者看")
	}
}

// tmux 会话没了 → 判死。
func TestProbeSessionGone(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	old := tmuxHasSession
	tmuxHasSession = func(string) bool { return false }
	t.Cleanup(func() { tmuxHasSession = old })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("tmux 会话已不存在，进程一定没了")
	}
}

// 恢复凭据缺失 → 返回错误（调用方按 unknown 处理，不得当成 dead）。
func TestProbeUnknownWhenCredentialsMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("凭据缺失必须返回错误，让调用方判 unknown 而不是 dead")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 只读铁律：探活不得回收 tmux 会话。判死路径上 Kill 一次都不能有。
func TestProbeNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	if err := writeProcInfo(dir, &procInfo{TmuxSession: "handoff-abcdef01", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	killed := 0
	oldKill, oldHas := tmuxKill, tmuxHasSession
	tmuxKill = func(string) error { killed++; return nil }
	tmuxHasSession = func(string) bool { return false } // 判死路径
	t.Cleanup(func() { tmuxKill, tmuxHasSession = oldKill, oldHas })

	if _, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if killed != 0 {
		t.Fatalf("探活是只读的，不得回收会话，实际 Kill 了 %d 次", killed)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/claudecode/ -run TestProbe -v`
Expected: FAIL——`a.Probe undefined (type *Adapter has no field or method Probe)`

- [ ] **Step 3: 写探活数据契约**

创建 `internal/executor/probe.go`：

```go
// probe.go —— 只读存活探测的共享数据契约。
//
// 职责：
//   - 定义 ProbeReq / ProbeOutcome，供 manager 与各 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：能力接口由消费方（manager）定义并做类型断言，这样
//     「不支持探活的 adapter 一律按 unknown 处理」是自然语义，executor.Adapter
//     的五动作核心契约也不被污染（与 resume.go 同规格）
//   - 无 I/O、无实现
package executor

// ProbeReq 是一次只读存活探测请求。
//
// 字段说明：
//   - TaskID: 目标任务
//   - TaskDir: DataDir/tasks/<id>，恢复凭据（serve.json / claude.json）在里面
//   - SessionID: 落库的 task.ExecutorSession
type ProbeReq struct {
	TaskID    string
	TaskDir   string
	SessionID string
}

// ProbeOutcome 是一次探测的结论。
//
// 字段说明：
//   - Alive: executor 是否仍在
//   - Note: 一句话理由，直接给审核者看（如「tmux 会话 handoff-1c28505a 不存在」）；
//     Alive=true 时为空
//
// 三态怎么区分：实现方用 error 表达「探不出结论」——err != nil 即 unknown，
// 调用方**不得把它当 dead**。假阳性是诊断命令最贵的失败模式。
type ProbeOutcome struct {
	Alive bool
	Note  string
}
```

- [ ] **Step 4: 写 claudecode 的 Probe**

创建 `internal/executor/claudecode/probe.go`：

```go
// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读恢复凭据，走 Proc.Alive 的既有判据，如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不占 runs 位、不碰 store、不发事件。
//     Resume 这三件事都做（判死后 Kill 是冷恢复不撞名的前置），正是它不能被
//     status 复用的原因
//   - 不重试、不做抖动吸收：一次探测一个结论。抖动误判的代价由调用方承担，
//     而 status 只读——误判的代价是输出里一行错话，不是一个被错判的任务
package claudecode

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 claude 执行器是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：tmux 会话存在 **且** out.jsonl 不含
// handoff_exit 哨兵，缺一即视为死亡。单看 tmux 会假阳性——窗口 1 的
// tail -f render.log 会一直吊着会话，claude 早死了会话依然在。判据一旦分叉，
// status 说的和实际恢复行为就是两回事。
//
// 参数：
//   - req: 探测请求（TaskDir 是 claude.json 所在，即 DataDir/tasks/<id>）
//
// 返回：
//   - Alive=true：执行器仍在，Note 为空
//   - Alive=false + Note：已判死，Note 是给审核者看的一句话理由
//   - err != nil：探不出结论（恢复凭据缺失/损坏），调用方按 unknown 处理，
//     **不得当成 dead**
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		a.log.Info("claude 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 claude 恢复凭据: %w", err)
	}
	// 只填 Alive 用得到的两个字段：TmuxSession 判会话、TaskDir 定位 out.jsonl
	proc := &Proc{TmuxSession: pi.TmuxSession, TaskDir: req.TaskDir}
	if proc.Alive() {
		a.log.Info("claude 探活：执行器存活", "task", req.TaskID, "tmux", pi.TmuxSession)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("claude 执行器已不在（tmux 会话 %s）", pi.TmuxSession)
	a.log.Info("claude 探活：执行器已不在", "task", req.TaskID, "tmux", pi.TmuxSession)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/... -run TestProbe -v`
Expected: PASS（claudecode 五个用例全绿）

- [ ] **Step 6: 跑全量回归，确认没碰坏 Resume**

Run: `go test ./internal/executor/...`
Expected: 全部包 ok

- [ ] **Step 7: 提交**

```bash
git add internal/executor/probe.go internal/executor/claudecode/probe.go internal/executor/claudecode/probe_test.go
git commit -m "feat(status): 加只读探活契约与 claudecode Probe"
```

---

## Task 3: opencode / grok / codex 三家的 Probe

三家形状同构（读凭据 → `Proc.Alive()`），一起做、一起审。

**Files:**
- Create: `internal/executor/opencode/probe.go`
- Create: `internal/executor/grok/probe.go`
- Create: `internal/executor/codex/probe.go`
- Test: `internal/executor/opencode/probe_test.go`、`internal/executor/grok/probe_test.go`、`internal/executor/codex/probe_test.go`

**Interfaces:**
- Consumes: `executor.ProbeReq` / `executor.ProbeOutcome`（Task 2）
- Produces: 三个 `(*Adapter).Probe(executor.ProbeReq) (executor.ProbeOutcome, error)`

- [ ] **Step 1: 写 opencode 的失败测试**

创建 `internal/executor/opencode/probe_test.go`：

```go
// opencode 只读探活测试：判据是 tmux 会话 + HTTP 应答，两者缺一即死。
package opencode

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// tmux 会话没了 → 判死，且不得回收会话。
func TestProbeSessionGoneAndNeverKills(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	// 注意 writeServeInfo 收的是 *Proc（不是 serveInfo）——它内部只取
	// Port/Password/TmuxSession 三个字段序列化
	if err := writeServeInfo(dir, &Proc{Port: 45999, Password: "pw", TmuxSession: "handoff-abcdef01"}); err != nil {
		t.Fatal(err)
	}
	killed := 0
	oldKill, oldHas := tmuxKill, tmuxHasSession
	tmuxKill = func(string) error { killed++; return nil }
	tmuxHasSession = func(string) bool { return false }
	t.Cleanup(func() { tmuxKill, tmuxHasSession = oldKill, oldHas })

	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("tmux 会话已不存在，serve 一定没了")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由")
	}
	if killed != 0 {
		t.Fatalf("探活是只读的，不得回收会话，实际 Kill 了 %d 次", killed)
	}
}

// serve.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("serve.json 缺失必须返回错误，让调用方判 unknown 而不是 dead")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}
```

> **四家的辅助函数名不一致，别跨包照抄**：opencode 是 `readServeInfo(taskDir) (*serveInfo, error)`（小写、另有 `serveInfo` 结构），grok/codex 是 `ReadServeInfo(taskDir) (*Proc, error)`（大写、直接返回 `*Proc`）。下面每家的代码都按各自真实签名写好了。

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/executor/opencode/ -run TestProbe -v`
Expected: FAIL——`a.Probe undefined`

- [ ] **Step 3: 写 opencode 的 Probe**

创建 `internal/executor/opencode/probe.go`：

```go
// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 serve.json，走 Proc.Alive 的既有判据（tmux 会话 + HTTP 应答），
//     如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不占 runs 位、不碰 store、不发事件
//   - 不打印 serveInfo.Password：凭据值绝不进日志，要打只打非敏感字段
package opencode

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 opencode serve 是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：tmux 会话存在且端口有 HTTP 应答，
// 缺一即视为死亡。
//
// 参数：
//   - req: 探测请求（TaskDir 是 serve.json 所在，即 DataDir/tasks/<id>）
//
// 返回：
//   - Alive=true：serve 仍在
//   - Alive=false + Note：已判死，Note 给审核者看
//   - err != nil：探不出结论（serve.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	si, err := readServeInfo(req.TaskDir)
	if err != nil {
		a.log.Info("opencode 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 opencode 恢复凭据: %w", err)
	}
	proc := &Proc{Port: si.Port, Password: si.Password, TmuxSession: si.TmuxSession}
	if proc.Alive() {
		a.log.Info("opencode 探活：serve 存活", "task", req.TaskID,
			"tmux", si.TmuxSession, "port", si.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("opencode serve 已不在（tmux 会话 %s，端口 %d）", si.TmuxSession, si.Port)
	a.log.Info("opencode 探活：serve 已不在", "task", req.TaskID,
		"tmux", si.TmuxSession, "port", si.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
```

- [ ] **Step 4: 跑 opencode 测试确认通过**

Run: `go test ./internal/executor/opencode/ -run TestProbe -v`
Expected: PASS

- [ ] **Step 5: 写 grok 的测试与实现**

创建 `internal/executor/grok/probe_test.go`：

```go
// grok 只读探活测试：判据是端口 HTTP 应答（收到任何响应即算活）。
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// writeTestServeInfo 写一份最小可读的 serve.json，供探活测试构造现场。
// 键名取自 Proc 的 json tag（session / task_dir / port / secret）。
func writeTestServeInfo(t *testing.T, dir string, port int) {
	t.Helper()
	b, err := json.Marshal(&Proc{Session: "handoff-abcdef01", Port: port, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// serve.json 缺失 → 返回错误（调用方判 unknown，不得当 dead）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "sess-1"})
	if err == nil {
		t.Fatal("serve.json 缺失必须返回错误，让调用方判 unknown")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 端口没人听 → 判死，且带理由。
// 用 ReadServeInfo 写一份指向必然无人监听的端口（0 端口不可连）的凭据。
func TestProbeDeadWhenPortClosed(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	writeTestServeInfo(t, dir, 1) // 端口 1：特权端口，本机必然连不上
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("端口无人监听应判死")
	}
	if got.Note == "" {
		t.Fatal("判死必须带一句话理由")
	}
}
```

创建 `internal/executor/grok/probe.go`：

```go
// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 serve.json，走 Proc.Alive 的既有判据（端口 HTTP 应答），
//     如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不动凭据软链、不碰 store、不发事件
//   - 不打印 Proc.Secret / WSURL：两者都含 secret，绝不进日志
package grok

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 grok serve 是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：端口收到任何 HTTP 响应即算活（含 404）。
//
// 返回：
//   - err != nil：探不出结论（serve.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	proc, err := ReadServeInfo(req.TaskDir)
	if err != nil {
		a.log.Info("grok 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 grok 恢复凭据: %w", err)
	}
	if proc.Alive() {
		a.log.Info("grok 探活：serve 存活", "task", req.TaskID, "port", proc.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("grok serve 已不在（端口 %d 无应答）", proc.Port)
	a.log.Info("grok 探活：serve 已不在", "task", req.TaskID, "port", proc.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
```

- [ ] **Step 6: 写 codex 的测试与实现**

创建 `internal/executor/codex/probe_test.go`（与 grok 同形，判据是 TCP 可连）：

```go
// codex 只读探活测试：判据是 app-server 端口 TCP 可连。
package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// serve.json 缺失 → 返回错误（调用方判 unknown）。
func TestProbeUnknownWhenServeInfoMissing(t *testing.T) {
	a := New(nil)
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: t.TempDir(), SessionID: "th-1"})
	if err == nil {
		t.Fatal("serve.json 缺失必须返回错误，让调用方判 unknown")
	}
	if got.Alive {
		t.Fatal("出错时 Alive 必须为 false")
	}
}

// 端口没人听 → 判死。
func TestProbeDeadWhenPortClosed(t *testing.T) {
	a := New(nil)
	dir := t.TempDir()
	b, err := json.Marshal(map[string]any{"port": 1, "session": "handoff-abcdef01"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := a.Probe(executor.ProbeReq{TaskID: "T-1", TaskDir: dir, SessionID: "th-1"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Alive {
		t.Fatal("端口无人监听应判死")
	}
}
```

（codex 的 `Proc` 只有 `session` / `task_dir` / `port` 三个 json 字段——它的 `app-server --listen ws://` 不带鉴权 secret，所以 serve.json 里没有凭据。）

创建 `internal/executor/codex/probe.go`：

```go
// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 serve.json，走 Proc.Alive 的既有判据（TCP 可连），如实返回结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不碰 store、不发事件
//   - 判据弱于 grok 的 HTTP 探活：端口活着不等于协议层活着（见 proc.go 文件头），
//     所以 Note 里如实写「端口可连」，不夸大成「executor 正常」
package codex

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 codex app-server 是否仍存活（manager 的 prober 可选接口）。
//
// 返回：
//   - err != nil：探不出结论（serve.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	proc, err := ReadServeInfo(req.TaskDir)
	if err != nil {
		a.log.Info("codex 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 codex 恢复凭据: %w", err)
	}
	if proc.Alive() {
		a.log.Info("codex 探活：app-server 端口可连", "task", req.TaskID, "port", proc.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("codex app-server 已不在（端口 %d 连不上）", proc.Port)
	a.log.Info("codex 探活：app-server 已不在", "task", req.TaskID, "port", proc.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
```

- [ ] **Step 7: 跑三家测试与全量回归**

Run: `go test ./internal/executor/...`
Expected: 全部包 ok，三家的 `TestProbe*` 全绿

- [ ] **Step 8: 提交**

```bash
git add internal/executor/opencode/probe*.go internal/executor/grok/probe*.go internal/executor/codex/probe*.go
git commit -m "feat(status): opencode/grok/codex 三家只读 Probe"
```

---

## Task 4: 服务端聚合与 GET /api/status

**Files:**
- Create: `internal/agentd/status.go`
- Modify: `internal/agentd/server.go`（`Server` 加 `startedAt` 字段、`NewServer` 记录、`Handler()` 注册路由、新增 `handleStatus`）
- Test: `internal/agentd/status_test.go`

**Interfaces:**
- Consumes: `proto.StatusResp` / `proto.ActiveTask` / `proto.LiveAlive|LiveDead|LiveUnknown`（Task 1）、`buildinfo.Read()`（Task 1）、`executor.ProbeReq` / `executor.ProbeOutcome`（Task 2）
- Produces: `(*Manager).Status() (*proto.StatusResp, error)`；HTTP `GET /api/status` 返回 `proto.StatusResp` 的 JSON

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/status_test.go`：

```go
// GET /api/status 的服务端聚合测试：字段齐全性、探活三态、总时限。
package agentd_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// probeStub 是一个可控探活结论的假 adapter，用来在服务端测试里制造三态现场。
// 它只需实现 executor.Adapter 的五动作 + Probe；五动作全部返回零值即可，
// status 路径不会调到它们。
type probeStub struct {
	alive bool
	note  string
	err   error
	delay time.Duration
}

func (p *probeStub) Start(ctx context.Context, req executor.StartReq) error { return nil }
func (p *probeStub) Events(taskID string) <-chan executor.AdapterEvent      { return nil }
func (p *probeStub) Send(ctx context.Context, taskID, text string) error    { return nil }
func (p *probeStub) RespondPermission(ctx context.Context, taskID, permID, decision string) error {
	return nil
}
func (p *probeStub) Stop(taskID string) error { return nil }

func (p *probeStub) Probe(executor.ProbeReq) (executor.ProbeOutcome, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	if p.err != nil {
		return executor.ProbeOutcome{}, p.err
	}
	return executor.ProbeOutcome{Alive: p.alive, Note: p.note}, nil
}

// 六个状态的计数键必须恒存在，哪怕计数为零——缺键与零值对消费方是两回事。
func TestStatusTaskCountsAlwaysHaveSixKeys(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	got := env.getStatus(t)
	for _, s := range []string{"pending", "running", "waiting_answer",
		"waiting_review", "completed", "failed"} {
		if _, ok := got.TaskCounts[s]; !ok {
			t.Fatalf("task_counts 缺键 %q——缺键与零值对消费方是两回事", s)
		}
	}
}

// 探活为 alive 时 Live=alive、Note 为空。
func TestStatusProbeAlive(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedRunningTask(t, "T-alive")
	got := env.getStatus(t)
	if len(got.Active) != 1 {
		t.Fatalf("活跃任务数=%d，want 1", len(got.Active))
	}
	if got.Active[0].Live != proto.LiveAlive {
		t.Fatalf("Live=%q, want %q", got.Active[0].Live, proto.LiveAlive)
	}
}

// 探活为 dead 时 Live=dead 且 Note 原样带出（审核者靠它判断怎么处置）。
func TestStatusProbeDead(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: false, note: "tmux 会话 handoff-abcdef01 不存在"})
	env.seedRunningTask(t, "T-dead")
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveDead {
		t.Fatalf("Live=%q, want %q", got.Active[0].Live, proto.LiveDead)
	}
	if got.Active[0].Note != "tmux 会话 handoff-abcdef01 不存在" {
		t.Fatalf("Note=%q，判死理由必须原样带出", got.Active[0].Note)
	}
}

// 探针自身失败（err != nil）→ unknown，**绝不能是 dead**。
func TestStatusProbeErrorIsUnknownNotDead(t *testing.T) {
	env := newStatusEnv(t, &probeStub{err: errors.New("凭据损坏")})
	env.seedRunningTask(t, "T-unknown")
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveUnknown {
		t.Fatalf("Live=%q, want %q——探不出结论时猜 dead 就是制造假阳性",
			got.Active[0].Live, proto.LiveUnknown)
	}
}

// 探活超时 → unknown，同样不是 dead。
func TestStatusProbeTimeoutIsUnknown(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true, delay: 3 * time.Second})
	env.seedRunningTask(t, "T-slow")
	start := time.Now()
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveUnknown {
		t.Fatalf("Live=%q, want %q（超时不判死）", got.Active[0].Live, proto.LiveUnknown)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("单次探活超时应在 2s 左右收敛，实际耗时 %v", elapsed)
	}
}

// 终结态任务不出现在 Active 里。
func TestStatusExcludesTerminalTasks(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedTask(t, "T-done", proto.TaskStateCompleted)
	got := env.getStatus(t)
	if len(got.Active) != 0 {
		t.Fatalf("completed 是终结态，不应出现在 active 里，得到 %d 条", len(got.Active))
	}
	if got.TaskCounts["completed"] != 1 {
		t.Fatalf("completed 计数=%d, want 1", got.TaskCounts["completed"])
	}
}

// 无 token → 401（走既有鉴权中间件，回归性断言）。
func TestStatusRequiresAuth(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	resp, err := http.Get(env.ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码=%d, want 401——status 不开匿名口", resp.StatusCode)
	}
}
```

测试辅助（写在同一文件里）：`newStatusEnv` 复用本包既有的 `newTestEnvWithCfg`
构造 store + httptest server，再用 `agentd.NewManager(st, srv.Hub(), map[string]executor.Adapter{"stub": ad}, cfg, nil, nil, logger)` 造 manager 并 `srv.SetManager(mgr)`；`cfg.Executor.Default` 设为 `"stub"`。`seedTask` 用 `st.CreateTask(&proto.Task{ID: id, State: state, Executor: "stub", Name: id, RepoPath: "/repo"})` 落库，`seedRunningTask` 是 `seedTask(id, proto.TaskStateRunning)`。`getStatus` 带 `Authorization: Bearer test-token` 请求 `/api/status` 并解出 `proto.StatusResp`。

实现前先读一遍既有 helper 的确切签名：

```bash
grep -n "func newTestEnvWithCfg" -A 30 internal/agentd/server_test.go
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestStatus -v`
Expected: FAIL——`/api/status` 未注册，返回 404；且 `mgr.Status` 未定义

- [ ] **Step 3: 写聚合实现**

创建 `internal/agentd/status.go`：

```go
// status.go —— GET /api/status 的服务端聚合。
//
// 职责：
//   - Manager.Status：把版本、配置、任务计数、非终结任务的存活结论聚成一个响应
//   - 带时限地逐个探活（prober 可选接口），三态如实返回
//
// 边界：
//   - **只读**：不改任务状态、不发事件、不回收任何 executor 资源。发现失配只
//     报告，修复归 continue/stop 那条既有路径（见 spec §1.4「不兼做恢复」）
//   - 不做周期性探活：本文件只在有人调 status 时才跑，与 Spec A §2.2
//     「不新增周期性探活」不冲突——那条拒绝的是后台定时扫
package agentd

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

const (
	// probeTimeout 是单个任务的探活时限。
	probeTimeout = 2 * time.Second
	// probeTotalTimeout 是一次 status 里全部探活的总时限：活跃任务再多，
	// 这条命令也不能变成慢命令。超出部分一律记 unknown。
	probeTotalTimeout = 10 * time.Second
)

// prober 是「只读探测 executor 是否存活」的可选 adapter 能力
//（四个真实 adapter 均实现，fake 不实现）。
//
// 为什么是可选接口而不是加进 executor.Adapter：不支持探活的 adapter 一律按
// unknown 处理是自然语义，五动作核心契约不该为一个诊断功能扩面
//（与 restorer / reaper / volatilePermitter 同一套路数）。
type prober interface {
	Probe(executor.ProbeReq) (executor.ProbeOutcome, error)
}

// Status 聚合本 agentd 的可用性与身份信息。
//
// 返回：
//   - StatusResp：版本、监听地址、DataDir、执行者清单、六状态计数、活跃任务
//     及其存活结论。StartedAt 由调用方（server 层）填，manager 不持有它
//   - err：只有查询任务列表失败才返回错误；探活失败不是错误，落到单个任务的
//     Live=unknown 上
func (m *Manager) Status() (*proto.StatusResp, error) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("状态聚合：查询任务列表失败", "cause", err)
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	ver, ok := buildinfo.Read()
	if !ok {
		m.log.Warn("状态聚合：读不到构建标识，版本字段留空")
	}
	names := registeredNames(m.ads)
	sort.Strings(names)

	resp := &proto.StatusResp{
		Version:         ver,
		Listen:          m.cfg.Listen,
		DataDir:         m.cfg.DataDir,
		Executors:       names,
		DefaultExecutor: m.cfg.Executor.Default,
		TaskCounts:      map[string]int{},
		Active:          []proto.ActiveTask{},
	}
	// 六个状态的键恒存在：缺键与零值对消费方是两回事
	for _, s := range []proto.TaskState{
		proto.TaskStatePending, proto.TaskStateRunning, proto.TaskStateWaitingAnswer,
		proto.TaskStateWaitingReview, proto.TaskStateCompleted, proto.TaskStateFailed,
	} {
		resp.TaskCounts[string(s)] = 0
	}

	var active []proto.Task
	for _, t := range tasks {
		resp.TaskCounts[string(t.State)]++
		if !isTerminalState(t.State) {
			active = append(active, t)
		}
	}
	resp.Active = m.probeActive(active)
	m.log.Info("状态聚合完成", "tasks", len(tasks), "active", len(active),
		"executors", len(names))
	return resp, nil
}

// isTerminalState 判断状态是否终结（completed / failed）。
func isTerminalState(s proto.TaskState) bool {
	return s == proto.TaskStateCompleted || s == proto.TaskStateFailed
}

// probeActive 对每个非终结任务做一次只读探活，共享一份总时限预算。
func (m *Manager) probeActive(tasks []proto.Task) []proto.ActiveTask {
	out := make([]proto.ActiveTask, 0, len(tasks))
	deadline := time.Now().Add(probeTotalTimeout)
	for _, t := range tasks {
		at := proto.ActiveTask{
			ID: t.ID, Name: t.Name, State: string(t.State),
			Executor: t.Executor, RepoPath: t.RepoPath,
		}
		// 老任务的 Executor 为空，回退缺省——展示上不该出现空执行者
		if at.Executor == "" {
			at.Executor = m.cfg.Executor.Default
		}
		at.Live, at.Note = m.probeOne(t, time.Until(deadline))
		out = append(out, at)
	}
	return out
}

// probeOne 对单个任务做一次只读探活，返回三态之一与一句话理由。
//
// 参数：
//   - budget: 本次探测可用的时限（受总预算约束，上限 probeTimeout）
//
// 注意：
//   - **超时归 unknown，不归 dead**。假阳性是诊断命令最贵的失败模式——一条会
//     说谎的诊断命令比没有更糟，因为你会信它
//   - 探测在 goroutine 里跑、用带缓冲的通道回收结果：超时后那个 goroutine 仍
//     能把结果写进缓冲并正常退出，不会泄漏；底层探针本身也都是有界的
//     （HTTP 客户端带超时、tmux has-session 秒回）
func (m *Manager) probeOne(t proto.Task, budget time.Duration) (live, note string) {
	if budget <= 0 {
		m.log.Warn("状态探活：总时限已用尽，该任务记为未知", "task", t.ID)
		return proto.LiveUnknown, "探活总时限已用尽"
	}
	if budget > probeTimeout {
		budget = probeTimeout
	}
	ad, err := m.adapterFor(t.ID)
	if err != nil {
		m.log.Warn("状态探活：执行者未注册，结论未知", "task", t.ID, "cause", err)
		return proto.LiveUnknown, fmt.Sprintf("执行者未注册：%v", err)
	}
	pr, ok := ad.(prober)
	if !ok {
		m.log.Info("状态探活：该 adapter 不支持探活，结论未知", "task", t.ID)
		return proto.LiveUnknown, "该执行者不支持探活"
	}

	type result struct {
		out executor.ProbeOutcome
		err error
	}
	ch := make(chan result, 1) // 缓冲 1：超时后 goroutine 仍能写入并退出
	go func() {
		o, e := pr.Probe(executor.ProbeReq{
			TaskID:    t.ID,
			TaskDir:   filepath.Join(m.cfg.DataDir, "tasks", t.ID),
			SessionID: t.ExecutorSession,
		})
		ch <- result{o, e}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			m.log.Warn("状态探活：探针失败，结论未知", "task", t.ID, "cause", r.err)
			return proto.LiveUnknown, fmt.Sprintf("探活失败：%v", r.err)
		}
		if r.out.Alive {
			m.log.Info("状态探活：executor 存活", "task", t.ID)
			return proto.LiveAlive, ""
		}
		m.log.Info("状态探活：executor 已不在", "task", t.ID, "note", r.out.Note)
		return proto.LiveDead, r.out.Note
	case <-time.After(budget):
		m.log.Warn("状态探活：超时，结论未知（不判死）", "task", t.ID, "budget", budget)
		return proto.LiveUnknown, "探活超时"
	}
}
```

- [ ] **Step 4: 接线到 server**

修改 `internal/agentd/server.go`：

1. `Server` 结构体加字段（放在 `mgr` 之后）：

```go
	// startedAt 是本 agentd 的启动时刻，status 用它换算 uptime。
	// 在 NewServer 里记录而非从 bootstrap 传入：NewServer 只在 bootstrap 调用
	// 一次，语义等价，且不必改动它的签名与全部测试调用点。
	startedAt time.Time
```

2. `NewServer` 的返回字面量里加 `startedAt: time.Now(),`（并 import `"time"`，若尚未导入）。

3. `Handler()` 的路由表加一行，并在其上方的路由注释里补一条 `GET /api/status  agentd 可用性与身份`：

```go
	mux.HandleFunc("GET /api/status", s.handleStatus)
```

4. 在 `handleListTasks` 之前新增 handler：

```go
// handleStatus 返回本 agentd 的可用性与身份信息（handoff status 的数据源）。
//
// 注意：
//   - manager 未就绪时返回 503：任务计数与探活都要经 manager，此时没有能给的
//     真实答案，宁可明确报「未就绪」也不返回一个半真的响应
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.log.Info("状态查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法回答状态查询")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.Status()
	if err != nil {
		s.log.Error("聚合状态失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	resp.StartedAt = s.startedAt
	s.log.Info("状态查询完成", "active", len(resp.Active), "executors", len(resp.Executors))
	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestStatus -v`
Expected: PASS（七个用例全绿）

- [ ] **Step 6: 跑全量回归**

Run: `go test ./...`
Expected: 全部包 ok

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/status.go internal/agentd/status_test.go internal/agentd/server.go
git commit -m "feat(status): 服务端聚合与 GET /api/status"
```

---

## Task 5: 客户端 Status 与旧版 404 哨兵

**Files:**
- Modify: `internal/client/client.go`
- Test: `internal/client/status_test.go`

**Interfaces:**
- Consumes: `proto.StatusResp`（Task 1）、`GET /api/status`（Task 4）
- Produces: `client.ErrStatusUnsupported`（哨兵错误）、`(*client.Client).Status(ctx context.Context) (*proto.StatusResp, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/client/status_test.go`：

```go
// client.Status 测试：正常解码、老 agentd 的 404 哨兵、其余错误照常报错。
package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xushixin/handoff/internal/client"
)

// 正常 200：字段要能解出来。
func TestStatusDecodes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			t.Errorf("请求路径=%q, want /api/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/d",
			"version":{"revision":"abc123","go":"go1.26.1"},
			"executors":["claude","opencode"],"default_executor":"opencode",
			"task_counts":{"running":1},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	got, err := client.New(ts.URL, "tok").Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Listen != "0.0.0.0:7777" || got.Version.Revision != "abc123" {
		t.Fatalf("解码结果不对: %+v", got)
	}
	if got.DefaultExecutor != "opencode" {
		t.Fatalf("DefaultExecutor=%q", got.DefaultExecutor)
	}
}

// 老 agentd 不认这个路由 → 必须是可判别的哨兵错误，CLI 据此走降级分支并退 0。
func TestStatusOldAgentdReturnsSentinel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	_, err := client.New(ts.URL, "tok").Status(context.Background())
	if !errors.Is(err, client.ErrStatusUnsupported) {
		t.Fatalf("err=%v，404 必须映射成 ErrStatusUnsupported 哨兵", err)
	}
}

// 401 不是哨兵：token 不对是真失败，CLI 要退 1。
func TestStatusUnauthorizedIsNotSentinel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	_, err := client.New(ts.URL, "tok").Status(context.Background())
	if err == nil {
		t.Fatal("401 必须报错")
	}
	if errors.Is(err, client.ErrStatusUnsupported) {
		t.Fatal("401 不是「版本过旧」，不得映射成哨兵")
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/client/ -run TestStatus -v`
Expected: FAIL——`undefined: client.ErrStatusUnsupported`、`Status undefined`

- [ ] **Step 3: 写实现**

在 `internal/client/client.go` 的 `ListTasks` 之前加入（并确认 `errors` 已在 import 列表里）：

```go
// ErrStatusUnsupported 表示对端 agentd 不认识 /api/status（版本早于该端点引入）。
//
// why（必须是可判别的哨兵）：这是唯一一个「HTTP 失败但结论是成功」的分支——
// 能收到 404 说明 TCP 通、HTTP 正常、Bearer 已经通过，三件事都被证明了。
// CLI 据此输出降级结论并退 0，而不是把一台完全能用的机器判成失败。
var ErrStatusUnsupported = errors.New("对端 agentd 不支持 /api/status")

// Status 查询 agentd 的可用性与身份信息（handoff status 的数据源）。
//
// 返回：
//   - StatusResp：版本、监听地址、DataDir、执行者清单、任务计数、活跃任务
//   - ErrStatusUnsupported：对端是老 agentd（404），调用方应走降级输出
//   - 其余错误：连不上、401、5xx 等真失败
func (c *Client) Status(ctx context.Context) (*proto.StatusResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("状态查询请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 不走 httpError：它会打 Warn 日志并造出一个普通错误，而这里的 404
		// 是一条有用的结论，不是异常
		c.log().Info("对端 agentd 不支持 /api/status，按版本过旧处理")
		return nil, ErrStatusUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("状态查询", resp)
	}
	var out proto.StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析状态响应: %w", err)
	}
	return &out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client/ -v`
Expected: PASS（三个 status 用例全绿，既有用例不受影响）

- [ ] **Step 5: 提交**

```bash
git add internal/client/client.go internal/client/status_test.go
git commit -m "feat(status): client.Status 与旧版 404 哨兵"
```

---

## Task 6: CLI 命令与渲染

**Files:**
- Create: `cmd/status.go`
- Test: `cmd/status_test.go`

**Interfaces:**
- Consumes: `client.Status` / `client.ErrStatusUnsupported`（Task 5）、`buildinfo.Read()`（Task 1）、`proto.StatusResp`（Task 1）、`TargetEndpoint()`（既有，`cmd/root.go`）
- Produces: `handoff status [--json]` 子命令

- [ ] **Step 1: 写失败的测试**

创建 `cmd/status_test.go`：

```go
// handoff status 的 CLI 行为测试：正常渲染、老 agentd 降级且退 0、401 退 1。
package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestConfig 写一份最小可用配置，返回路径。
// 字段名按 yaml.v3 对无 tag 结构体的默认规则（全小写字段名）。
func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\ntoken: test-token\ndatadir: " + dir + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runStatus 执行一次 status 命令，返回 stdout 与错误。
func runStatus(t *testing.T, cfgPath, agentdURL string, extra ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	args := append([]string{"status", "--config", cfgPath, "--agentd", agentdURL}, extra...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// 正常 200：关键字段要出现在文本里，且不报错（退出码 0）。
func TestStatusRendersText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"listen":"0.0.0.0:7777","data_dir":"/data",
			"started_at":"2026-08-10T00:00:00Z",
			"version":{"revision":"8353ef68d711eaf63eeb1287f342f3238204aec8","go":"go1.26.1"},
			"executors":["claude","opencode"],"default_executor":"opencode",
			"task_counts":{"running":1,"pending":0,"completed":2},
			"active":[{"id":"1c28505a-1111-2222-3333-444455556666","name":"B19 env 注入",
				"state":"running","executor":"opencode","live":"dead",
				"note":"tmux 会话 handoff-1c28505a 不存在"}]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeTestConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status 应成功，得到错误: %v", err)
	}
	for _, want := range []string{"可用", "8353ef68d711", "/data", "opencode", "running 1", "1c28505a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少 %q：\n%s", want, out)
		}
	}
	// 计数为零的状态不该出现在文本里（JSON 侧才恒有六个键）
	if strings.Contains(out, "pending 0") {
		t.Fatalf("文本渲染应省略零值计数：\n%s", out)
	}
}

// 老 agentd 返回 404：输出降级结论，**且不报错**（退出码 0）。
func TestStatusOldAgentdIsSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeTestConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("老 agentd 照样能派发能审阅，必须退 0，得到错误: %v", err)
	}
	for _, want := range []string{"版本过旧", "Bearer 鉴权通过", "升级远端 agentd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("降级输出缺少 %q：\n%s", want, out)
		}
	}
}

// 401：必须报错（退出码 1）。
func TestStatusUnauthorizedFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)

	if _, err := runStatus(t, writeTestConfig(t), ts.URL); err == nil {
		t.Fatal("401 是真失败，必须返回错误")
	}
}

// --json：顶层 reachable 与退出码同源。
func TestStatusJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"listen":"l","data_dir":"d","task_counts":{},"active":[]}`))
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeTestConfig(t), ts.URL, "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out, `"reachable":true`) {
		t.Fatalf("JSON 输出缺少 reachable:\n%s", out)
	}
}

// --json 遇上老 agentd：reachable=true 且 degraded=true。
func TestStatusJSONDegraded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)

	out, err := runStatus(t, writeTestConfig(t), ts.URL, "--json")
	if err != nil {
		t.Fatalf("status --json: %v", err)
	}
	if !strings.Contains(out, `"degraded":true`) || !strings.Contains(out, `"reachable":true`) {
		t.Fatalf("降级 JSON 应 reachable=true 且 degraded=true:\n%s", out)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./cmd/ -run TestStatus -v`
Expected: FAIL——`unknown command "status" for "handoff"`

- [ ] **Step 3: 写命令与渲染**

创建 `cmd/status.go`：

```go
// 本文件实现 handoff status 子命令：一条命令回答「这个 agentd 能不能用、是什么」。
//
// 职责：
//   - 调 client.Status 取服务端聚合结果，渲染人读文本（默认）或 JSON（--json）
//   - 把老 agentd 的 404 直译成一条**成功的**诊断结论
//   - 退出码只回答「能不能用」：0=可达且鉴权通过，1=够不着
//
// 边界：
//   - 不做探活：判据在各 adapter 里，服务端已经做完，本层只渲染
//   - 不因两边版本不一致而阻断：handoff 没有兼容矩阵，revision 不同不等于
//     不兼容，并列报出交给人判
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// statusJSONOut 对应 --json。
var statusJSONOut bool

// statusJSON 是 --json 的线格式。
//
// reachable 与退出码同源（都回答「能不能用」），脚本读哪个都行；
// degraded=true 表示对端是老 agentd，此时 agentd 字段为 null。
type statusJSON struct {
	Reachable bool              `json:"reachable"`
	Degraded  bool              `json:"degraded"`
	CLI       proto.BuildInfo   `json:"cli"`
	Agentd    *proto.StatusResp `json:"agentd"`
}

// statusCmd 查询 agentd 的可用性与身份。
//
// 使用方式：handoff status [--target <名字>] [--json]
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看 agentd 是否可用及其版本/数据目录/任务概况",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cliVer, _ := buildinfo.Read()
		out := cmd.OutOrStdout()

		st, err := client.New(addr, token).Status(cmd.Context())
		switch {
		case errors.Is(err, client.ErrStatusUnsupported):
			// 老 agentd：能收到 404 已经证明了 TCP 通、HTTP 正常、Bearer 过，
			// 这是一条成功的诊断，不是失败
			if statusJSONOut {
				return json.NewEncoder(out).Encode(statusJSON{
					Reachable: true, Degraded: true, CLI: cliVer})
			}
			renderDegraded(out, addr)
			return nil
		case err != nil:
			return err
		}

		if statusJSONOut {
			return json.NewEncoder(out).Encode(statusJSON{
				Reachable: true, CLI: cliVer, Agentd: st})
		}
		renderStatus(out, addr, cliVer, st)
		return nil
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSONOut, "json", false, "以 JSON 输出（reachable 与退出码同源）")
	rootCmd.AddCommand(statusCmd)
}

// renderDegraded 渲染老 agentd 的降级结论。
//
// 措辞刻意不写成失败：在版本错配这个场景里「远端过旧」正是要的答案。
// 也不写「该端点自 xxx 版本引入」——CLI 无从知道对端版本，编一个引入点就是编造。
func renderDegraded(w io.Writer, addr string) {
	fmt.Fprintf(w, "agentd   %s   可用（版本过旧）\n", addr)
	fmt.Fprintln(w, "已确认   TCP 可达 · HTTP 正常 · Bearer 鉴权通过")
	fmt.Fprintln(w, "限制     该 agentd 不支持 /api/status，详情不可得")
	fmt.Fprintln(w, "处置     升级远端 agentd 后重试")
}

// renderStatus 渲染完整状态。
func renderStatus(w io.Writer, addr string, cli proto.BuildInfo, st *proto.StatusResp) {
	fmt.Fprintf(w, "agentd   %s   可用\n", addr)
	fmt.Fprintf(w, "版本     %s\n", describeBuild(st.Version))
	fmt.Fprintf(w, "本地     %s\n", compareBuild(cli, st.Version))
	fmt.Fprintf(w, "数据     %s   已运行 %s\n", st.DataDir, humanUptime(st.StartedAt))
	fmt.Fprintf(w, "执行者   %s\n", strings.Join(markDefault(st.Executors, st.DefaultExecutor), "  "))
	fmt.Fprintln(w)
	fmt.Fprintf(w, "任务     %s\n", renderCounts(st.TaskCounts))
	if len(st.Active) == 0 {
		return
	}
	fmt.Fprintln(w, "活跃")
	for _, a := range st.Active {
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n",
			short8(a.ID), a.Name, a.State, a.Executor, liveText(a))
	}
}

// describeBuild 把一个构建标识渲染成一行。
//
// Revision 为空表示不是 go build 产物（go run / 测试二进制），如实说明而不是留空。
func describeBuild(b proto.BuildInfo) string {
	if b.Revision == "" {
		return fmt.Sprintf("未知（非 go build 产物）  %s", b.Go)
	}
	s := fmt.Sprintf("%s  %s  %s", short12(b.Revision), b.Time, b.Go)
	if b.Modified {
		// 带未提交改动意味着这个二进制对不上任何一个提交，排障时是关键信息
		s += "  带未提交改动"
	}
	return s
}

// compareBuild 渲染「本地」行：两边 revision 的对照结论。
//
// 不一致**不阻断**：handoff 没有兼容矩阵，revision 不同不等于不兼容，
// 该不该继续交给人判。
func compareBuild(cli, agentd proto.BuildInfo) string {
	if cli.Revision == "" {
		return "本地版本未知（非 go build 产物）"
	}
	s := short12(cli.Revision)
	if cli.Modified {
		s += "  带未提交改动"
	}
	if agentd.Revision == "" {
		return s + "  （对端版本未知，无从对照）"
	}
	if cli.Revision == agentd.Revision {
		return s + "  一致"
	}
	return s + "  与对端不一致（不一定不兼容，请自行判断）"
}

// humanUptime 把启动时刻换算成「已运行多久」。
//
// 零值时刻（老响应或字段缺失）返回「未知」，不显示一个荒谬的天数。
func humanUptime(startedAt time.Time) string {
	if startedAt.IsZero() {
		return "未知"
	}
	d := time.Since(startedAt)
	if d < 0 {
		// 两机时钟不同步时会为负，如实说明而不是显示负数
		return "未知（对端时钟与本机不同步）"
	}
	d = d.Round(time.Minute)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// markDefault 给缺省执行者加标注。
func markDefault(names []string, def string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == def {
			out = append(out, n+"(缺省)")
			continue
		}
		out = append(out, n)
	}
	return out
}

// renderCounts 渲染任务计数，**只列非零的状态**。
//
// why（只列非零）：六个状态里常年有四个是 0，全列会把真正的结论淹掉。
// JSON 侧不做这个省略——人看的要短，机器读的要齐。
func renderCounts(counts map[string]int) string {
	order := []string{"pending", "running", "waiting_answer", "waiting_review", "completed", "failed"}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
		}
	}
	if len(parts) == 0 {
		return "无"
	}
	return strings.Join(parts, " · ")
}

// liveText 把存活结论渲染成一句人话。
func liveText(a proto.ActiveTask) string {
	switch a.Live {
	case proto.LiveAlive:
		return "executor 存活"
	case proto.LiveDead:
		return fmt.Sprintf("executor 已不在（%s）", a.Note)
	default:
		return fmt.Sprintf("存活性未知（%s）", a.Note)
	}
}

// short8 取 id 前 8 位用于展示（与 tmux 会话命名 handoff-<id8> 一致，便于人肉对照）。
//
// 注意：只用于展示。任何拿去当参数的地方都必须用完整 UUID——store.GetTask 是
// 精确匹配，不做前缀查找。
func short8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// short12 取 revision 前 12 位（git 惯用短 hash 长度）。
func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestStatus -v`
Expected: PASS（五个用例全绿）

- [ ] **Step 5: 跑全量回归 + vet + gofmt**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: `gofmt -l` 无输出、vet 无告警、全部包 ok

- [ ] **Step 6: 手工验一次真实输出**

```bash
go build -o /tmp/handoff-status-check . && /tmp/handoff-status-check status --help
```

Expected: 帮助里出现 `status` 与 `--json`；若本机有 agentd 在跑，再跑一次 `/tmp/handoff-status-check status` 看真实渲染。

- [ ] **Step 7: 提交**

```bash
git add cmd/status.go cmd/status_test.go
git commit -m "feat(status): handoff status CLI 与文本/JSON 渲染"
```

---

## Task 7: 文档与 skill 回写

spec §9 的连带动作：让 `status` 成为「想确认远端可用性时最先想到的名字」，文档侧必须跟上。

**Files:**
- Modify: `skills/handoff/SKILL.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: `handoff status` 的实际输出与错误文案（Task 6）
- Produces: 无代码接口

- [ ] **Step 1: 给 SKILL.md 加「确认 agentd 在不在」一节**

在 SKILL.md 的「排障」相关章节之前插入：

```markdown
## 确认 agentd 在不在

    handoff status --target <名字>

**不要 ssh 上去查进程、查端口、查二进制。** 那是在验证零件，而问题是「这个服务
现在能不能用」；零件检查有无数种失败方式（PATH、平台差异、引号嵌套），每一种都
长得像「没有」。

| 输出 | 结论 | 处置 |
|---|---|---|
| 正常一屏（版本/数据/任务） | 能用 | 直接派发 |
| `可用（版本过旧）` | 能用，但远端 agentd 不支持 status | 想看详情就升级远端；不看也不影响派发 |
| `target "x" 未在配置 … 中定义` | 你的本机配置问题，不是远端问题 | 补 target 配置 |
| `dial tcp …: connect: connection refused` | 真的没有 agentd 在跑 | 见下面的红线 |
| `状态码 401` | agentd 在，但 token 对不上 | 同步两边的 token |

退出码：**0 = 能用**（含版本过旧）；**1 = 够不着**。

**红线：查到有 agentd 在跑就复用它，绝不为同一个仓库起第二个 agentd。**
两个 agentd 抢同一个仓库和 worktree，正是状态机最怕的失配——而代码层面目前
没有单实例锁拦你。

活跃任务行末尾的存活结论有三态：`executor 存活` / `executor 已不在（理由）` /
`存活性未知（理由）`。**`未知` 不等于 `已不在`**——探不出结论时不要按「死了」
处置，先看理由。
```

- [ ] **Step 2: README 命令清单补一行**

在 README 的命令清单里加：

```markdown
- `handoff status [--target <名字>] [--json]`——看这个 agentd 能不能用、是什么版本、有哪些活跃任务及其 executor 是否还活着
```

- [ ] **Step 3: 重装 skill 到四个 agent**

Run: `bash skills/install.sh`
Expected: 打印基准副本路径与三条软链，无报错

- [ ] **Step 4: 提交**

```bash
git add skills/handoff/SKILL.md README.md
git commit -m "docs(status): SKILL 增「确认 agentd 在不在」一节，README 补 status"
```

---

## Task 8: backlog 回写

**Files:**
- Modify: `docs/superpowers/backlog.md`

- [ ] **Step 1: 跑全量测试取证**

Run: `go test ./...`
Expected: 全部包 ok。**把实际输出记下来**（包数与结果），验收列要写真实命令与结果，不得编造。

- [ ] **Step 2: 更新 B33 行**

把 B33 从 `🔨 doing` 改为 `✅ done(已验)`（若上一步有失败则为 `✅ done(未验)` 并写明缺什么），`验收` 列填入上一步的真实命令与结果；`原型/流程图` 为 `—`，按 product-backlog 规则自动免除对照。

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B33 完工回写"
```

---

## Self-Review

**1. Spec 覆盖**

| spec 章节 | 落点 |
|---|---|
| §2 与 Spec A 的关系（只读 + 超时判 unknown） | Task 4 `status.go` 文件头与 `probeOne` 注释；Task 2 的 `TestProbeNeverKills` |
| §3.1 判据必须走 adapter | Task 2 / Task 3 各家 `Probe` 复用自己的 `Proc.Alive` |
| §3.2 不复用 `Resume` | Task 2 文件头边界 + `TestProbeNeverKills` |
| §3.3 三态 / 只读 / 判据不分叉 / 超时 | Task 2 契约 + Task 4 `probeOne` |
| §4.1 响应契约 | Task 1 `proto/status.go` |
| §4.2 数据来源与六键恒存在 | Task 4 `Status()` + `TestStatusTaskCountsAlwaysHaveSixKeys` |
| §4.3 `StartedAt` 与版本来源 | Task 4 Step 4（偏离已在开头声明）+ Task 1 `buildinfo` |
| §5.1 文本输出与三种版本行 | Task 6 `renderStatus` / `describeBuild` / `compareBuild` |
| §5.2 `--json` | Task 6 `statusJSON` + 两个 JSON 用例 |
| §5.3 退出码 | Task 6 三个用例（成功/降级退 0，401 退 1） |
| §6 旧版兼容与错误处理表 | Task 5 哨兵 + Task 6 `renderDegraded` |
| §7 文件结构 | 本文 File Structure（两处偏离已声明） |
| §8 测试 | Task 1/2/3/4/5/6 各自的测试步骤；vcs 戳的坑落在 Task 1 |
| §9 连带动作 | Task 7 |

无遗漏。

**2. 占位符扫描**：无 TBD/TODO，每个代码步骤都给了完整代码。写完后回查了全部引用到的既有标识符，四处按实际签名改过：`Manager` 的 `st`/`ads`/`cfg`/`log` 字段与 `registeredNames(m.ads)`、四家 `Adapter` 的 `log` 字段、`Server.log`、`(*Client).log()` 均已确认存在；opencode 的 `writeServeInfo` 收 `*Proc` 而非 `*serveInfo`（测试代码已按真实签名改正）；grok 的测试助手改为直接构造 `&Proc{}` 序列化（键名取自 json tag）；三家的 `serve.json` 常量名（opencode `serveInfoFileName`，grok/codex `serveInfoName`）在计划里不被直接引用，无风险。

**3. 类型一致性**：`proto.BuildInfo` / `proto.ActiveTask` / `proto.StatusResp` 三个结构在 Task 1 定义，Task 4/5/6 全部按此字段名使用；`executor.ProbeReq{TaskID, TaskDir, SessionID}` 与 `executor.ProbeOutcome{Alive, Note}` 在 Task 2 定义，Task 3/4 一致；`prober` 接口方法签名 `Probe(executor.ProbeReq) (executor.ProbeOutcome, error)` 在 Task 2 各 adapter 实现与 Task 4 断言处一致；`client.ErrStatusUnsupported` 在 Task 5 定义、Task 6 消费。
