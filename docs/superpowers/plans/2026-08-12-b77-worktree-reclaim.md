# B77 `handoff reclaim` 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给终态任务一条可重复执行的 managed worktree 回收入口 `handoff reclaim`，并让既有的清理失败提示指向它。

**Architecture:** 新增 `internal/agentd/reclaim.go` 承载判定与回收（地面真相来自 `git worktree list --porcelain`，不读 `worktree_managed` 字段）、`internal/proto/reclaim.go` 承载传输契约、`cmd/reclaim.go` 承载「无参列、带 id 收」两形态。回收是**纯资源动作**：不改任务状态、不发状态迁移事件、不删分支、不删任务目录。

**Tech Stack:** Go 1.x，cobra CLI，net/http ServeMux，标准库 `os/exec` 调 git，`log/slog`（经 `m.log` / 包内 `log()`）。

## Global Constraints

- **spec 是唯一事实源**：`docs/superpowers/specs/2026-08-12-failed-task-worktree-reclaim-design.md`，与本计划冲突时以 spec 为准。
- **不改任务状态、不追加状态迁移事件**（spec §3.1 第 1 条）。
- **只认终态任务**（`completed` / `failed`），非终态一律 409（spec §3.1 第 2 条）。
- **只认 `Managed=true`**（`WorktreeManaged` 为真且 `WorkDir` 非空）。
- **不删分支、不删任务目录**（spec §2 非目标）。
- **所有 git 调用带超时**：包一层 `context.WithTimeout(ctx, WorkspaceGitTimeout)`，禁止裸 `context.Background()`。
- **不读 `worktree_managed` 判残留**：它删成功从不回写（`store.go:388` 白名单只有 branch/executor_session/plan_summary/done_note）。该字段只用于「这个任务当初是不是 managed 模式」这一个用途。
- **日志一律走 `m.log` / 包内 `log()`**，禁止 `fmt.Printf` 当日志用。
- **中文注释**：新文件写职责与边界头注释，导出符号写参数/返回/注意，非显然分支写「为什么」。
- 四种 409 共用状态码，响应体必须带机器码 `reason`：`not_terminal` / `dirty` / `repo_unreachable` / `not_managed`。CLI 按 `reason` 分派，**不解析中文文案**。

---

### Task 1: 传输契约类型

**Files:**
- Create: `internal/proto/reclaim.go`

**Interfaces:**
- Consumes: 无
- Produces: `proto.WorktreeState`（`WorktreeClean`/`WorktreeDirty`/`WorktreePrunable`/`WorktreeAbsent`/`WorktreeUnknown`）、`proto.DirtyFile{Status,Path string}`、`proto.ReclaimRow`、`proto.ReclaimListResp{Rows []ReclaimRow; Scanned int}`、`proto.ReclaimAction`（`ReclaimRemoved`/`ReclaimPruned`/`ReclaimAlreadyAbsent`）、`proto.ReclaimResp`、`proto.ReclaimReason`（`ReasonNotTerminal`/`ReasonDirty`/`ReasonRepoUnreachable`/`ReasonNotManaged`）、`proto.ReclaimError`

本任务只有类型定义，没有行为，因此不写测试——类型的正确性由后续任务的编译与用例承担。

- [ ] **Step 1: 写类型文件**

```go
// reclaim.go —— handoff reclaim 的传输契约类型。
//
// 职责：
//   - 定义 GET /api/reclaim 的列表响应与 POST /api/tasks/{id}/reclaim 的动作响应
//   - 定义四种 409 拒绝理由的机器码，供 CLI 分派渲染
//
// 边界：
//   - 只描述线上格式，不含任何判定逻辑（判定在 internal/agentd/reclaim.go）
//   - 不描述进程残留（那是 FootprintRow 的事，两者互不覆盖）
package proto

// WorktreeState 是一个终态任务的 managed worktree 当前所处的态。
//
// 注意：Unknown 与 Absent 必须分开。「仓库不可达所以判不出」与「确实没有残留」
// 是两回事，把前者渲染成后者等于用一个假结论把该看的东西藏起来（同 B70 的
// 「不猜 0」纪律）。
type WorktreeState string

const (
	// WorktreeClean 在册且 git status 为空，可直接回收。
	WorktreeClean WorktreeState = "clean"
	// WorktreeDirty 在册但有未提交改动或未跟踪文件，默认拒绝回收。
	WorktreeDirty WorktreeState = "dirty"
	// WorktreePrunable 在册但目录已不存在。它不占磁盘，占的是分支——
	// 照样能让 git push --delete 被拒，因此必须能被看见与回收。
	WorktreePrunable WorktreeState = "prunable"
	// WorktreeAbsent 不在册，无残留。
	WorktreeAbsent WorktreeState = "absent"
	// WorktreeUnknown 仓库不可达或不是 git 仓库，判不出。
	WorktreeUnknown WorktreeState = "unknown"
)

// DirtyFile 是脏工作树里的一个条目，来自 git status --porcelain。
type DirtyFile struct {
	// Status 是 porcelain 的两字符状态码，如 "M " / "??" / "R "。
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ReclaimRow 是 GET /api/reclaim 列表里的一行。
type ReclaimRow struct {
	TaskID  string `json:"task_id"`
	Name    string `json:"name"`
	// State 是任务状态（completed / failed），不是工作树状态。
	State   string        `json:"state"`
	Branch  string        `json:"branch"`
	WorkDir string        `json:"work_dir"`
	// Worktree 是工作树状态，取值见 WorktreeState。
	Worktree WorktreeState `json:"worktree"`
	// DirtyCount 仅在 Worktree=dirty 时有意义。列表只给条数不给清单——
	// 清单可能很长，要看细节走单任务回收的 409 响应。
	DirtyCount int `json:"dirty_count"`
	// Note 是 Worktree=unknown / prunable 时的真因，供人读。
	Note string `json:"note,omitempty"`
}

// ReclaimListResp 是 GET /api/reclaim 的响应。
type ReclaimListResp struct {
	// Rows 只含「仍有残留或判不出」的行；干净收场的任务不入表。
	Rows []ReclaimRow `json:"rows"`
	// Scanned 是本次体检过的终态任务总数，供 CLI 打「共体检 N 个」。
	Scanned int `json:"scanned"`
}

// ReclaimAction 是一次回收实际做了什么。
type ReclaimAction string

const (
	// ReclaimRemoved 走 git worktree remove 删掉了。
	ReclaimRemoved ReclaimAction = "removed"
	// ReclaimPruned 走 git worktree prune 清掉了在册条目（remove 失败后的兜底）。
	ReclaimPruned ReclaimAction = "pruned"
	// ReclaimAlreadyAbsent 本来就不在册，无动作。幂等成功走这条。
	ReclaimAlreadyAbsent ReclaimAction = "already_absent"
)

// ReclaimResp 是 POST /api/tasks/{id}/reclaim 成功时的响应。
type ReclaimResp struct {
	Removed bool          `json:"removed"`
	Action  ReclaimAction `json:"action"`
	WorkDir string        `json:"work_dir"`
	Branch  string        `json:"branch"`
	// Discarded 是 force 强删时被丢弃的条目。留痕用：强删不能悄悄发生。
	Discarded []DirtyFile `json:"discarded,omitempty"`
}

// ReclaimReason 是 409 拒绝的机器码。
//
// 为什么必须有：四种拒绝共用 409 一个状态码，CLI 要分派渲染就只能靠它。
// 靠解析中文文案是不行的——文案是给人看的、会改，机器码是契约、不改。
type ReclaimReason string

const (
	ReasonNotTerminal     ReclaimReason = "not_terminal"
	ReasonDirty           ReclaimReason = "dirty"
	ReasonRepoUnreachable ReclaimReason = "repo_unreachable"
	ReasonNotManaged      ReclaimReason = "not_managed"
)

// ReclaimError 是 409 的响应体。
type ReclaimError struct {
	Error  string        `json:"error"`
	Reason ReclaimReason `json:"reason"`
	// Dirty 仅在 Reason=dirty 时非空，是结构化清单而非预渲染文本——
	// 渲染是 CLI 的事。
	Dirty []DirtyFile `json:"dirty,omitempty"`
}
```

- [ ] **Step 2: 编译验证**

Run: `go build ./...`
Expected: 无输出，退出 0

- [ ] **Step 3: 提交**

```bash
git add internal/proto/reclaim.go
git commit -m "feat(proto): reclaim 传输契约——四态工作树与四种 409 机器码"
```

---

### Task 2: 解析原语与路径规范化

**Files:**
- Create: `internal/agentd/reclaim.go`
- Test: `internal/agentd/reclaim_test.go`

**Interfaces:**
- Consumes: `proto.DirtyFile`（Task 1）
- Produces:
  - `type worktreeEntry struct { Path string; Prunable bool; PruneReason string }`
  - `func parseWorktreeList(out string) map[string]worktreeEntry`（键为 git 报的**原始**路径）
  - `func parsePorcelainStatus(out string) []proto.DirtyFile`
  - `func canonPath(p string) string`
  - `func findEntry(entries map[string]worktreeEntry, workdir string) (worktreeEntry, bool)`

- [ ] **Step 1: 写失败测试**

```go
// reclaim_test.go —— worktree 回收的判定与动作测试。
//
// 解析类用例用固定文本（不起 git）；判定与回收类用例在 t.TempDir() 里
// git init + git worktree add 造真实工作树，复用 workspace_test.go 的
// initGitRepo / gitAt / writeAndCommit 助手。
package agentd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorktreeListMarksPrunable(t *testing.T) {
	out := "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n\n" +
		"worktree /repo/wt1\nHEAD abc123\nbranch refs/heads/f1\n\n" +
		"worktree /repo/wt2\nHEAD abc123\nbranch refs/heads/f2\n" +
		"prunable gitdir file points to non-existent location\n\n"
	got := parseWorktreeList(out)
	if len(got) != 3 {
		t.Fatalf("期望 3 个条目，实得 %d：%+v", len(got), got)
	}
	if e := got["/repo/wt2"]; !e.Prunable {
		t.Fatalf("wt2 应判为 prunable，实得 %+v", e)
	}
	if e := got["/repo/wt2"]; e.PruneReason == "" {
		t.Fatalf("prunable 必须带原因，实得空")
	}
	if e := got["/repo/wt1"]; e.Prunable {
		t.Fatalf("wt1 不该被判 prunable，实得 %+v", e)
	}
	if e := got["/repo"]; e.Prunable {
		t.Fatalf("主仓不该被判 prunable，实得 %+v", e)
	}
}

func TestParsePorcelainStatusKeepsStatusCode(t *testing.T) {
	out := " M internal/prochost/fence.go\n?? scratch/probe.log\n"
	got := parsePorcelainStatus(out)
	if len(got) != 2 {
		t.Fatalf("期望 2 项，实得 %d：%+v", len(got), got)
	}
	if got[0].Status != " M" || got[0].Path != "internal/prochost/fence.go" {
		t.Fatalf("第 1 项解析错：%+v", got[0])
	}
	if got[1].Status != "??" || got[1].Path != "scratch/probe.log" {
		t.Fatalf("第 2 项解析错：%+v", got[1])
	}
}

func TestParsePorcelainStatusEmptyIsClean(t *testing.T) {
	if got := parsePorcelainStatus("\n"); len(got) != 0 {
		t.Fatalf("空输出应解析为 0 项，实得 %+v", got)
	}
}

// canonPath 必须能穿透符号链接：macOS 上 /tmp 是 /private/tmp 的链接，
// git 报的是解析后的路径，而任务库里存的可能是未解析的——不归一就永远匹配不上。
func TestCanonPathResolvesSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	if canonPath(link) != canonPath(real) {
		t.Fatalf("链接与目标应归一到同一路径：%s vs %s", canonPath(link), canonPath(real))
	}
}

// 目录已不存在（prunable 态）时仍要能归一：退一步解析父目录再拼回叶子名，
// 否则 prunable 条目永远匹配不上，回收入口对这一态直接失效。
func TestCanonPathResolvesMissingLeafViaParent(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	gone := filepath.Join(link, "gone")
	want := filepath.Join(canonPath(real), "gone")
	if got := canonPath(gone); got != want {
		t.Fatalf("缺失叶子应经父目录归一：实得 %s，期望 %s", got, want)
	}
}

func TestFindEntryMatchesAcrossSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("建符号链接：%v", err)
	}
	entries := map[string]worktreeEntry{real: {Path: real}}
	if _, ok := findEntry(entries, link); !ok {
		t.Fatalf("经符号链接给的 workdir 应能匹配到条目")
	}
	if _, ok := findEntry(entries, filepath.Join(real, "other")); ok {
		t.Fatalf("不同路径不该匹配")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestParseWorktreeList|TestParsePorcelain|TestCanonPath|TestFindEntry' -v`
Expected: FAIL，编译错误 `undefined: parseWorktreeList` 等

- [ ] **Step 3: 写最小实现**

```go
// reclaim.go —— 终态任务 managed worktree 的判定与回收。
//
// 职责：
//   - 从 git worktree list --porcelain 拿地面真相，判定工作树四态
//     （净 / 脏 / 元数据残留 / 不在册），仓库不可达时如实报判不出
//   - 对单个终态任务执行回收（git worktree remove，脏树需显式 force）
//
// 边界：
//   - **纯资源动作**：不改任务状态、不追加状态迁移事件、不发唤醒
//   - 不删任务分支（审核者的工作成果），不删任务目录（失败任务的排查素材）
//   - 不读 worktree_managed 判断「现在还在不在」——该字段删成功从不回写，
//     只用于判断「这个任务当初是不是 managed 模式」
package agentd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// worktreeEntry 是 git worktree list --porcelain 里的一条记录。
type worktreeEntry struct {
	Path        string
	Prunable    bool
	PruneReason string
}

// parseWorktreeList 解析 git worktree list --porcelain 的输出。
//
// 参数：
//   - out: porcelain 原文。记录之间以空行分隔，每条以 "worktree <路径>" 开头，
//     可选属性行含 HEAD / branch / bare / detached / locked / prunable
//
// 返回：
//   - 以 git 报的**原始**路径为键的条目表。路径归一交给 findEntry，
//     解析函数保持纯粹以便用固定文本测试
func parseWorktreeList(out string) map[string]worktreeEntry {
	entries := make(map[string]worktreeEntry)
	var cur worktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries[cur.Path] = cur
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			// 记录之间正常由空行分隔；这里再 flush 一次是防御畸形输出
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.Prunable = true
			cur.PruneReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	flush()
	return entries
}

// parsePorcelainStatus 解析 git status --porcelain 的输出为脏文件清单。
//
// 参数：
//   - out: porcelain 原文，每行形如 "XY 路径"（XY 为两字符状态码）
//
// 返回：
//   - 脏条目清单；输出为空表示工作树干净
//
// 注意：重命名行形如 "R  old -> new"，这里整段留在 Path 里不再拆——
// 审核者要看的是「动了什么」，拆开反而丢失了「从哪来」这条信息
func parsePorcelainStatus(out string) []proto.DirtyFile {
	var files []proto.DirtyFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		files = append(files, proto.DirtyFile{
			Status: line[:2],
			Path:   strings.TrimSpace(line[3:]),
		})
	}
	return files
}

// canonPath 把路径归一到可比较的形态。
//
// 参数：
//   - p: 绝对路径，目录可能已不存在
//
// 返回：
//   - 解析符号链接后的清洁路径
//
// 注意：
//   - 必须穿透符号链接。macOS 上 /tmp 是 /private/tmp 的链接，git 报的是
//     解析后的路径，而任务库里存的可能没解析——不归一就永远匹配不上
//   - 目录已不存在时（prunable 态）EvalSymlinks 会失败，退一步解析父目录
//     再拼回叶子名。不做这层退让，回收入口对 prunable 这一态直接失效
func canonPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// findEntry 在条目表里按归一后的路径查找工作树。
//
// 参数：
//   - entries: parseWorktreeList 的产出（键为 git 原始路径）
//   - workdir: 任务记录里的工作区路径
//
// 返回：
//   - 命中的条目与是否命中
//
// 注意：线性扫描而非直接查表，因为两侧都要经 canonPath 归一才能比较；
// 一个仓库的工作树数量是个位数，这点开销无所谓
func findEntry(entries map[string]worktreeEntry, workdir string) (worktreeEntry, bool) {
	want := canonPath(workdir)
	for p, e := range entries {
		if canonPath(p) == want {
			return e, true
		}
	}
	return worktreeEntry{}, false
}
```

> 注：`context` / `errors` / `fmt` / `store` 这几个 import 在 Task 3、4 才用到。
> 若本任务编译报「imported and not used」，先只留 `path/filepath`、`strings`、
> `proto` 三个，Task 3/4 再按需补回。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestParseWorktreeList|TestParsePorcelain|TestCanonPath|TestFindEntry' -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志**

本任务全是纯函数、无 I/O、无错误分支，按 `instrumenting-code` 的「不适用」条款**不加日志**——
纯解析函数打日志只会在调用方的关键节点日志之外制造噪音。日志在 Task 3/4 的 git 调用与判定出口处加。

在文件头注释中补一句说明，避免后来者以为是漏了：

```go
//   - 本文件的解析类函数（parseWorktreeList / parsePorcelainStatus / canonPath /
//     findEntry）是纯函数，刻意不打日志；可观测性由调用方（classifyWorktree /
//     Reclaim / ReclaimList）在关键节点承担
```

- [ ] **Step 6: 补注释自检**

确认：文件头有职责与边界；四个导出/包级函数都有参数、返回、注意；
`canonPath` 的两层退让、`findEntry` 的线性扫描、`parsePorcelainStatus` 的重命名处理都写了「为什么」。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/reclaim.go internal/agentd/reclaim_test.go
git commit -m "feat(agentd): worktree 册解析与路径归一——穿透符号链接与缺失叶子"
```

---

### Task 3: 四态判定（真 git 仓库）

**Files:**
- Modify: `internal/agentd/reclaim.go`
- Test: `internal/agentd/reclaim_test.go`

**Interfaces:**
- Consumes: `parseWorktreeList` / `parsePorcelainStatus` / `findEntry`（Task 2）、`gitRun`（`workspace.go:103`）、`WorkspaceGitTimeout`（`workspace.go`）
- Produces:
  - `func repoWorktrees(ctx context.Context, repo string) (map[string]worktreeEntry, error)`
  - `func classifyWorktree(ctx context.Context, entries map[string]worktreeEntry, workdir string) (proto.WorktreeState, []proto.DirtyFile, string)`
    返回：状态、脏清单（仅 dirty 非空）、note（unknown/prunable 时的真因）

- [ ] **Step 1: 写失败测试**

```go
// 在 reclaim_test.go 追加。新增 import: "context"、"github.com/xushixin/handoff/internal/proto"

// newWorktree 在 repo 下建一个 managed 风格的工作树并返回其路径。
func newWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(repo), name)
	gitAt(t, repo, "worktree", "add", "-q", dir, "-b", branch)
	return dir
}

func TestClassifyCleanWorktree(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-clean", "f-clean")
	entries, err := repoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("拉工作树册：%v", err)
	}
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeClean {
		t.Fatalf("期望 clean，实得 %s", state)
	}
	if len(dirty) != 0 {
		t.Fatalf("干净树不该有脏清单，实得 %+v", dirty)
	}
}

// 只有未跟踪文件也必须判脏：git worktree remove 正是会因未跟踪文件失败
// （实证 git 2.50.1：fatal: contains modified or untracked files）。
// 判据不与它对齐，就会出现「我说是净的，删的时候被拒了」。
func TestClassifyUntrackedOnlyIsDirty(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-untracked", "f-untracked")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("写未跟踪文件：%v", err)
	}
	entries, err := repoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("拉工作树册：%v", err)
	}
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeDirty {
		t.Fatalf("只有未跟踪文件时也应判 dirty，实得 %s", state)
	}
	if len(dirty) != 1 || dirty[0].Path != "probe.log" {
		t.Fatalf("脏清单应含 probe.log，实得 %+v", dirty)
	}
}

func TestClassifyModifiedIsDirty(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-mod", "f-mod")
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("改已跟踪文件：%v", err)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	state, dirty, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeDirty {
		t.Fatalf("期望 dirty，实得 %s", state)
	}
	if len(dirty) != 1 || dirty[0].Path != "README.md" {
		t.Fatalf("脏清单应含 README.md，实得 %+v", dirty)
	}
}

func TestClassifyPrunableWhenDirGone(t *testing.T) {
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-gone", "f-gone")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删工作树目录：%v", err)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	state, _, note := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreePrunable {
		t.Fatalf("目录已失应判 prunable，实得 %s", state)
	}
	if note == "" {
		t.Fatalf("prunable 必须带原因")
	}
}

func TestClassifyAbsentWhenNotRegistered(t *testing.T) {
	repo := initGitRepo(t)
	entries, _ := repoWorktrees(context.Background(), repo)
	state, _, _ := classifyWorktree(context.Background(), entries, filepath.Join(repo, "never-existed"))
	if state != proto.WorktreeAbsent {
		t.Fatalf("未注册路径应判 absent，实得 %s", state)
	}
}

// 仓库不可达必须报 unknown 而不是 absent：把「判不出」渲染成「没有残留」，
// 等于用假结论把该看的东西藏起来（同 B70 的「不猜 0」纪律）。
func TestRepoWorktreesFailsOnNonRepo(t *testing.T) {
	if _, err := repoWorktrees(context.Background(), t.TempDir()); err == nil {
		t.Fatalf("非 git 仓库应返回错误，实得 nil")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestClassify|TestRepoWorktrees' -v`
Expected: FAIL，`undefined: repoWorktrees` / `undefined: classifyWorktree`

- [ ] **Step 3: 写最小实现**

```go
// 追加到 internal/agentd/reclaim.go

// repoWorktrees 拉取一个仓库当前在册的全部工作树。
//
// 参数：
//   - ctx: 上层上下文，内部叠加 WorkspaceGitTimeout
//   - repo: 仓库路径
//
// 返回：
//   - 条目表；仓库不可达或不是 git 仓库时返回错误（调用方据此报「判不出」）
//
// 注意：这是**地面真相的唯一来源**。任务库里的 worktree_managed 只说明
// 「当初建过」，删成功从不回写，用它判「现在还在不在」必然出假阳性
func repoWorktrees(ctx context.Context, repo string) (map[string]worktreeEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := gitRun(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		log().Warn("拉工作树册失败，该仓库下的任务判不出",
			"repo", repo, "stderr", truncateRunes(stderr, 200), "cause", err)
		return nil, fmt.Errorf("git worktree list %s: %s: %w",
			repo, strings.TrimSpace(truncateRunes(stderr, 200)), err)
	}
	entries := parseWorktreeList(out)
	log().Info("工作树册已拉取", "repo", repo, "entries", len(entries))
	return entries, nil
}

// classifyWorktree 判定一个工作区当前所处的态。
//
// 参数：
//   - ctx: 上层上下文，内部叠加 WorkspaceGitTimeout
//   - entries: repoWorktrees 的产出
//   - workdir: 任务记录里的工作区路径
//
// 返回：
//   - 状态；脏清单（仅 dirty 时非空）；note（unknown / prunable 时的真因）
//
// 注意：脏的判据含**未跟踪文件**。这不是保守，而是必须与 git worktree remove
// 自身的拒绝条件对齐——实证 git 2.50.1，只有未跟踪文件时 remove 也会失败
func classifyWorktree(ctx context.Context, entries map[string]worktreeEntry, workdir string) (proto.WorktreeState, []proto.DirtyFile, string) {
	e, ok := findEntry(entries, workdir)
	if !ok {
		log().Info("工作树判定：不在册，无残留", "workdir", workdir)
		return proto.WorktreeAbsent, nil, ""
	}
	if e.Prunable {
		log().Info("工作树判定：元数据残留", "workdir", workdir, "reason", e.PruneReason)
		return proto.WorktreePrunable, nil, e.PruneReason
	}
	sctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	out, stderr, err := gitRun(sctx, workdir, "status", "--porcelain")
	if err != nil {
		note := strings.TrimSpace(truncateRunes(stderr, 200))
		log().Warn("工作树判定：读不到 status，判不出",
			"workdir", workdir, "stderr", note, "cause", err)
		return proto.WorktreeUnknown, nil, note
	}
	files := parsePorcelainStatus(out)
	if len(files) == 0 {
		log().Info("工作树判定：干净，可回收", "workdir", workdir)
		return proto.WorktreeClean, nil, ""
	}
	log().Info("工作树判定：脏，默认拒绝回收", "workdir", workdir, "dirty", len(files))
	return proto.WorktreeDirty, files, ""
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestClassify|TestRepoWorktrees' -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

确认已覆盖：外部调用（两处 `gitRun`）失败均 Warn 带 stderr + cause；
四种判定出口**都有** Info（含 absent 与 clean 这两条「什么都不用做」的成功路径——
静默成功路径正是本 skill 要消灭的东西）。

- [ ] **Step 6: 补注释自检**

确认 `repoWorktrees` 写了「为什么它是地面真相唯一来源」，`classifyWorktree` 写了
「为什么未跟踪也算脏」并注明是实证结论。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/reclaim.go internal/agentd/reclaim_test.go
git commit -m "feat(agentd): 工作树四态判定——净/脏/元数据残留/不在册，判不出如实报"
```

---

### Task 4: 单任务回收

**Files:**
- Modify: `internal/agentd/reclaim.go`
- Test: `internal/agentd/reclaim_test.go`

**Interfaces:**
- Consumes: `repoWorktrees` / `classifyWorktree`（Task 3）、`store.ErrNotFound`、`proto.Task`
- Produces:
  - `var ErrReclaimNotTerminal, ErrReclaimRepoUnreachable, ErrReclaimNotManaged error`
  - `type ErrDirtyWorktree struct { Files []proto.DirtyFile }`，实现 `Error() string`
  - `func (m *Manager) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)`

- [ ] **Step 1: 写失败测试**

```go
// 在 reclaim_test.go 追加。
// newReclaimManager 复用本包既有的测试 Manager 构造方式——实现时照抄
// manager_test.go 里同类用例的建法（store + cfg + log），不要另造一套。

func TestReclaimRemovesCleanWorktree(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r1", "f-r1")
	id := seedTerminalTask(t, m, repo, wt, "f-r1", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("回收干净树应成功，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved || !resp.Removed {
		t.Fatalf("期望 removed，实得 %+v", resp)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("工作树目录应已删除")
	}
}

func TestReclaimRefusesDirtyWithoutForce(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r2", "f-r2")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r2", proto.TaskStateFailed, true)

	_, err := m.Reclaim(context.Background(), id, false)
	var de *ErrDirtyWorktree
	if !errors.As(err, &de) {
		t.Fatalf("脏树无 force 应返回 ErrDirtyWorktree，实得 %v", err)
	}
	if len(de.Files) != 1 || de.Files[0].Path != "probe.log" {
		t.Fatalf("拒绝时必须带脏清单，实得 %+v", de.Files)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("拒绝后工作树必须原样保留：%v", err)
	}
}

func TestReclaimForceRemovesDirtyAndReportsDiscarded(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r3", "f-r3")
	if err := os.WriteFile(filepath.Join(wt, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r3", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, true)
	if err != nil {
		t.Fatalf("force 应删成功，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved {
		t.Fatalf("期望 removed，实得 %s", resp.Action)
	}
	// 强删不能悄悄发生：丢了什么必须留痕
	if len(resp.Discarded) != 1 || resp.Discarded[0].Path != "probe.log" {
		t.Fatalf("强删必须报出被丢弃的条目，实得 %+v", resp.Discarded)
	}
}

func TestReclaimHandlesPrunableEntry(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r4", "f-r4")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删目录：%v", err)
	}
	id := seedTerminalTask(t, m, repo, wt, "f-r4", proto.TaskStateFailed, true)

	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("prunable 条目应可回收，实得 %v", err)
	}
	if resp.Action != proto.ReclaimRemoved && resp.Action != proto.ReclaimPruned {
		t.Fatalf("期望 removed 或 pruned，实得 %s", resp.Action)
	}
	entries, _ := repoWorktrees(context.Background(), repo)
	if _, ok := findEntry(entries, wt); ok {
		t.Fatalf("回收后条目必须从册中消失")
	}
}

// 幂等是「重试入口」的定义：重试第二次会报错的入口，不是重试入口。
func TestReclaimIsIdempotent(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r5", "f-r5")
	id := seedTerminalTask(t, m, repo, wt, "f-r5", proto.TaskStateFailed, true)

	if _, err := m.Reclaim(context.Background(), id, false); err != nil {
		t.Fatalf("首次回收：%v", err)
	}
	resp, err := m.Reclaim(context.Background(), id, false)
	if err != nil {
		t.Fatalf("二次回收必须成功（幂等），实得 %v", err)
	}
	if resp.Action != proto.ReclaimAlreadyAbsent || resp.Removed {
		t.Fatalf("二次回收应报 already_absent 且 removed=false，实得 %+v", resp)
	}
}

func TestReclaimRefusesNonTerminal(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r6", "f-r6")
	id := seedTerminalTask(t, m, repo, wt, "f-r6", proto.TaskStateRunning, true)

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimNotTerminal) {
		t.Fatalf("非终态应拒绝，实得 %v", err)
	}
	if _, serr := os.Stat(wt); serr != nil {
		t.Fatalf("拒绝后工作树必须保留：%v", serr)
	}
}

func TestReclaimRefusesNotManaged(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r7", "f-r7")
	id := seedTerminalTask(t, m, repo, wt, "f-r7", proto.TaskStateFailed, false)

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimNotManaged) {
		t.Fatalf("非 managed 应拒绝，实得 %v", err)
	}
	if _, serr := os.Stat(wt); serr != nil {
		t.Fatalf("拒绝后用户自带工作树必须保留：%v", serr)
	}
}

// 仓库不可达时**绝不能**被当成 already_absent 静默退成功——
// 那会让人以为已经清干净了（同 B64 的「把没上报当成没有」缺陷）。
func TestReclaimRefusesWhenRepoUnreachable(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-r8", "f-r8")
	id := seedTerminalTask(t, m, repo, wt, "f-r8", proto.TaskStateFailed, true)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("删仓库：%v", err)
	}

	_, err := m.Reclaim(context.Background(), id, false)
	if !errors.Is(err, ErrReclaimRepoUnreachable) {
		t.Fatalf("仓库不可达应报 repo_unreachable，实得 %v", err)
	}
}

func TestReclaimNotFound(t *testing.T) {
	m, _ := newReclaimManager(t)
	if _, err := m.Reclaim(context.Background(), "no-such-task", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的任务应返回 ErrNotFound，实得 %v", err)
	}
}
```

测试助手（同文件内实现）：

```go
// newReclaimManager 造一个带真实 git 仓库的测试 Manager。
// 实现时照抄本包既有测试的 Manager 建法（store.Open 到 t.TempDir、
// cfg.DataDir 指向 t.TempDir、log 用 slog.New(slog.NewTextHandler(io.Discard,nil))），
// 不要新造一套构造路径。
func newReclaimManager(t *testing.T) (*Manager, string) {
	t.Helper()
	// ...按本包既有测试的 Manager 构造方式实现，返回 (m, repoPath)
}

// seedTerminalTask 往库里塞一个指定状态的任务，返回任务 ID。
func seedTerminalTask(t *testing.T, m *Manager, repo, workdir, branch string,
	state proto.TaskState, managed bool) string {
	t.Helper()
	// ...CreateTask 落一条含 RepoPath/WorkDir/Branch/WorktreeManaged 的记录，
	// 再按需 transit 到目标状态；实现时复用本包既有测试的建任务助手
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestReclaim -v`
Expected: FAIL，`undefined: ErrReclaimNotTerminal` 等

- [ ] **Step 3: 写最小实现**

```go
// 追加到 internal/agentd/reclaim.go

var (
	// ErrReclaimNotTerminal 表示任务还没到终态，不予回收——删运行中任务的
	// 工作树等于抽它脚下。
	ErrReclaimNotTerminal = errors.New("任务非终态，不回收工作树")
	// ErrReclaimRepoUnreachable 表示仓库不可达或不是 git 仓库，判不出。
	// 单任务回收必须据此拒绝，绝不能降级成「无残留」静默成功。
	ErrReclaimRepoUnreachable = errors.New("仓库不可达，工作树状态判不出")
	// ErrReclaimNotManaged 表示该任务用的是审核者自带的工作树，agentd 无权删。
	ErrReclaimNotManaged = errors.New("工作区不是 agentd 管理的 worktree")
)

// ErrDirtyWorktree 表示工作树有未提交改动或未跟踪文件，未带 force 时拒绝回收。
//
// 为什么是带清单的类型而不是裸哨兵：审核者要决定「这些改动能不能丢」，
// 就必须看见改了什么。只给一句「树是脏的」等于把决定权交出去却不给依据
type ErrDirtyWorktree struct {
	Files []proto.DirtyFile
}

func (e *ErrDirtyWorktree) Error() string {
	return fmt.Sprintf("工作树有 %d 项未提交改动或未跟踪文件", len(e.Files))
}

// Reclaim 回收一个终态任务残留的 managed worktree。
//
// 参数：
//   - ctx: 上层上下文（HTTP 请求）
//   - taskID: 目标任务
//   - force: 为真时对脏工作树也强删（丢弃未提交改动），并在响应里报出丢弃清单
//
// 返回：
//   - 回收结果（removed / pruned / already_absent）
//   - store.ErrNotFound: 任务不存在
//   - ErrReclaimNotTerminal / ErrReclaimNotManaged / ErrReclaimRepoUnreachable
//   - *ErrDirtyWorktree: 脏树且未带 force
//
// 注意：
//   - **纯资源动作**：不改任务状态、不追加事件、不删分支、不删任务目录
//   - 幂等：树已不在则报 already_absent 并成功返回。一条重试第二次会报错的
//     入口不是重试入口
//   - 动手前重读任务快照：failed→running 是合法迁移，列表之后任务可能已被
//     重新派发，终态判定不能停在列表那一刻
func (m *Manager) Reclaim(ctx context.Context, taskID string, force bool) (resp *proto.ReclaimResp, err error) {
	m.log.Info("reclaim 进入", "task", taskID, "force", force)
	defer func() {
		if err != nil {
			m.log.Warn("reclaim 未完成", "task", taskID, "cause", err)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if !cur.State.IsTerminal() {
		return nil, fmt.Errorf("任务 %s 状态 %s，%w", taskID, cur.State, ErrReclaimNotTerminal)
	}
	if !cur.WorktreeManaged || cur.WorkDir == "" {
		return nil, fmt.Errorf("任务 %s：%w", taskID, ErrReclaimNotManaged)
	}

	entries, lerr := repoWorktrees(ctx, cur.RepoPath)
	if lerr != nil {
		return nil, fmt.Errorf("任务 %s 的仓库 %s：%v：%w",
			taskID, cur.RepoPath, lerr, ErrReclaimRepoUnreachable)
	}
	state, dirty, note := classifyWorktree(ctx, entries, cur.WorkDir)
	base := &proto.ReclaimResp{WorkDir: cur.WorkDir, Branch: cur.Branch}

	switch state {
	case proto.WorktreeAbsent:
		m.log.Info("reclaim 完成：本就无残留", "task", taskID, "workdir", cur.WorkDir)
		base.Action = proto.ReclaimAlreadyAbsent
		return base, nil
	case proto.WorktreeUnknown:
		return nil, fmt.Errorf("任务 %s 工作树 %s：%s：%w",
			taskID, cur.WorkDir, note, ErrReclaimRepoUnreachable)
	case proto.WorktreeDirty:
		if !force {
			return nil, &ErrDirtyWorktree{Files: dirty}
		}
		m.log.Warn("reclaim 强删脏工作树", "task", taskID,
			"workdir", cur.WorkDir, "discard", len(dirty))
	}

	rctx, cancel := context.WithTimeout(ctx, WorkspaceGitTimeout)
	defer cancel()
	args := []string{"worktree", "remove", cur.WorkDir}
	if force {
		args = append(args, "--force")
	}
	if _, stderr, rerr := gitRun(rctx, cur.RepoPath, args...); rerr != nil {
		// prunable 兜底：实证 git 2.50.1 上 remove 能直接处理在册但目录已失的
		// 条目，这里只防旧版 git 行为不同。remove 成功是常路，本分支是保险
		if state == proto.WorktreePrunable {
			m.log.Warn("reclaim：prunable 条目 remove 失败，退回 prune",
				"task", taskID, "stderr", truncateRunes(stderr, 200), "cause", rerr)
			if _, pstderr, perr := gitRun(rctx, cur.RepoPath, "worktree", "prune"); perr != nil {
				return nil, fmt.Errorf("git worktree prune %s: %s: %w",
					cur.RepoPath, strings.TrimSpace(truncateRunes(pstderr, 200)), perr)
			}
			after, verr := repoWorktrees(rctx, cur.RepoPath)
			if verr != nil {
				return nil, fmt.Errorf("prune 后复查工作树册：%w", verr)
			}
			if _, still := findEntry(after, cur.WorkDir); still {
				return nil, fmt.Errorf("prune 后条目 %s 仍在册", cur.WorkDir)
			}
			m.log.Info("reclaim 完成：prune 清掉在册条目", "task", taskID, "workdir", cur.WorkDir)
			base.Removed, base.Action = true, proto.ReclaimPruned
			return base, nil
		}
		return nil, fmt.Errorf("git worktree remove %s: %s: %w",
			cur.WorkDir, strings.TrimSpace(truncateRunes(stderr, 200)), rerr)
	}

	m.log.Info("reclaim 完成：工作树已删除", "task", taskID,
		"workdir", cur.WorkDir, "branch", cur.Branch, "discarded", len(dirty))
	base.Removed, base.Action, base.Discarded = true, proto.ReclaimRemoved, dirty
	return base, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestReclaim -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

确认：进入带 taskID/force；四条拒绝路径经 defer 统一 Warn 带 cause；强删单独 Warn 带丢弃条数；
三条成功出口（already_absent / pruned / removed）**都有** Info——尤其 `already_absent`
这条「什么都没做的成功」，静默掉就无法区分「跑过且无残留」与「根本没跑到」。

- [ ] **Step 6: 补注释自检**

确认 `Reclaim` 的 doc 覆盖参数/返回/全部哨兵错误，并写明三条注意（纯资源动作、幂等、
动手前重读快照）；`ErrDirtyWorktree` 写了「为什么带清单」；prunable 兜底分支写了
「为什么是保险而非常路」。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/reclaim.go internal/agentd/reclaim_test.go
git commit -m "feat(agentd): 单任务 worktree 回收——脏树默认拒绝、幂等、prune 兜底"
```

---

### Task 5: 残留列表

**Files:**
- Modify: `internal/agentd/reclaim.go`
- Test: `internal/agentd/reclaim_test.go`

**Interfaces:**
- Consumes: `repoWorktrees` / `classifyWorktree`（Task 3）、`m.st.ListTasks()`
- Produces: `func (m *Manager) ReclaimList() (*proto.ReclaimListResp, error)`

- [ ] **Step 1: 写失败测试**

```go
// 在 reclaim_test.go 追加。

func TestReclaimListShowsResidueOnly(t *testing.T) {
	m, repo := newReclaimManager(t)
	dirtyWT := newWorktree(t, repo, "wt-l1", "f-l1")
	if err := os.WriteFile(filepath.Join(dirtyWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	dirtyID := seedTerminalTask(t, m, repo, dirtyWT, "f-l1", proto.TaskStateFailed, true)
	// 已回收干净的任务：记录还在、worktree_managed 仍是 true，但不该入表
	goneWT := filepath.Join(filepath.Dir(repo), "wt-l2-never")
	seedTerminalTask(t, m, repo, goneWT, "f-l2", proto.TaskStateCompleted, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 2 {
		t.Fatalf("应体检 2 个终态任务，实得 %d", resp.Scanned)
	}
	if len(resp.Rows) != 1 || resp.Rows[0].TaskID != dirtyID {
		t.Fatalf("只有脏树那条该入表，实得 %+v", resp.Rows)
	}
	if resp.Rows[0].Worktree != proto.WorktreeDirty || resp.Rows[0].DirtyCount != 1 {
		t.Fatalf("脏行应带态与条数，实得 %+v", resp.Rows[0])
	}
}

// 非终态任务不入表：它的工作树正被使用，不是残留。
func TestReclaimListSkipsNonTerminal(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-l3", "f-l3")
	seedTerminalTask(t, m, repo, wt, "f-l3", proto.TaskStateRunning, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 0 || len(resp.Rows) != 0 {
		t.Fatalf("非终态不该被体检或入表，实得 %+v", resp)
	}
}

// 一个仓库不可达不能拖垮整张表——列表的核心价值正是在环境已不健康时还能用。
func TestReclaimListDegradesPerRepo(t *testing.T) {
	m, goodRepo := newReclaimManager(t)
	goodWT := newWorktree(t, goodRepo, "wt-l4", "f-l4")
	if err := os.WriteFile(filepath.Join(goodWT, "probe.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("造脏：%v", err)
	}
	goodID := seedTerminalTask(t, m, goodRepo, goodWT, "f-l4", proto.TaskStateFailed, true)

	deadRepo := t.TempDir() // 不是 git 仓库
	deadID := seedTerminalTask(t, m, deadRepo, filepath.Join(deadRepo, "wt"), "f-l5",
		proto.TaskStateFailed, true)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("单仓不可达不该让整张表失败，实得 %v", err)
	}
	var sawGood, sawUnknown bool
	for _, r := range resp.Rows {
		if r.TaskID == goodID && r.Worktree == proto.WorktreeDirty {
			sawGood = true
		}
		if r.TaskID == deadID && r.Worktree == proto.WorktreeUnknown {
			sawUnknown = true
		}
	}
	if !sawGood {
		t.Fatalf("健康仓库的行必须照常返回，实得 %+v", resp.Rows)
	}
	if !sawUnknown {
		t.Fatalf("不可达仓库的行必须标 unknown 而不是消失，实得 %+v", resp.Rows)
	}
}

// 非 managed 的任务不入表：用户自带工作树不是 agentd 的残留。
func TestReclaimListSkipsNotManaged(t *testing.T) {
	m, repo := newReclaimManager(t)
	wt := newWorktree(t, repo, "wt-l6", "f-l6")
	seedTerminalTask(t, m, repo, wt, "f-l6", proto.TaskStateFailed, false)

	resp, err := m.ReclaimList()
	if err != nil {
		t.Fatalf("列表：%v", err)
	}
	if resp.Scanned != 0 || len(resp.Rows) != 0 {
		t.Fatalf("非 managed 不该入表，实得 %+v", resp)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestReclaimList -v`
Expected: FAIL，`undefined: ReclaimList`

- [ ] **Step 3: 写最小实现**

```go
// 追加到 internal/agentd/reclaim.go

// ReclaimList 体检全部终态任务的 managed worktree 残留。
//
// 返回：
//   - 只含「仍有残留或判不出」的行，外加体检总数；查询任务列表失败才返回错误
//
// 注意：
//   - **单个仓库不可达不拖垮整张表**：该行标 unknown 继续走完。列表的核心
//     价值正是在环境已经不健康的时候还能用——这与单任务回收「判不出就拒绝」
//     的处置刻意相反，因为两者的失败代价不同
//   - 按仓库分组只拉一次工作树册：同一仓库下的多个任务共用一次 git 调用
//   - 与 FootprintAll 分工：那个数进程，这个数工作树，互不覆盖
func (m *Manager) ReclaimList() (*proto.ReclaimListResp, error) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("残留体检：查询任务列表失败", "cause", err)
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	m.log.Info("残留体检开始", "tasks", len(tasks))

	ctx := context.Background()
	resp := &proto.ReclaimListResp{Rows: make([]proto.ReclaimRow, 0)}
	// 每个仓库只拉一次册；值为 nil 表示该仓库不可达（判不出）
	cache := make(map[string]map[string]worktreeEntry)
	failed := make(map[string]string)

	for _, t := range tasks {
		if !t.State.IsTerminal() || !t.WorktreeManaged || t.WorkDir == "" {
			continue
		}
		resp.Scanned++
		entries, cached := cache[t.RepoPath]
		if !cached {
			e, lerr := repoWorktrees(ctx, t.RepoPath)
			if lerr != nil {
				failed[t.RepoPath] = strings.TrimSpace(truncateRunes(lerr.Error(), 200))
			}
			cache[t.RepoPath], entries = e, e
		}
		row := proto.ReclaimRow{
			TaskID: t.ID, Name: t.Name, State: string(t.State),
			Branch: t.Branch, WorkDir: t.WorkDir,
		}
		if entries == nil {
			row.Worktree, row.Note = proto.WorktreeUnknown, failed[t.RepoPath]
			resp.Rows = append(resp.Rows, row)
			continue
		}
		state, dirty, note := classifyWorktree(ctx, entries, t.WorkDir)
		if state == proto.WorktreeAbsent {
			continue // 干净收场，不入表
		}
		row.Worktree, row.DirtyCount, row.Note = state, len(dirty), note
		resp.Rows = append(resp.Rows, row)
	}
	m.log.Info("残留体检完成", "scanned", resp.Scanned, "rows", len(resp.Rows),
		"bad_repos", len(failed))
	return resp, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestReclaimList -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

确认：进入带任务总数；查询失败 Error 带 cause；退出带 scanned / rows / bad_repos 三个数
（成功路径不静默，且 `bad_repos>0` 时一眼能看出这张表是有降级的）。

- [ ] **Step 6: 补注释自检**

确认 doc 写清了「为什么列表容忍局部失败而单任务回收不容忍」——这是两处刻意相反的
处置，不写理由后来者一定会把它们「统一」掉。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/reclaim.go internal/agentd/reclaim_test.go
git commit -m "feat(agentd): 残留列表——按仓库分组体检，单仓不可达标 unknown 不拖垮全表"
```

---

### Task 6: HTTP 端点

**Files:**
- Modify: `internal/agentd/server.go:184-197`（路由表）、新增两个 handler
- Test: `internal/agentd/server_test.go`（追加）

**Interfaces:**
- Consumes: `m.ReclaimList()`（Task 5）、`m.Reclaim(ctx,id,force)`（Task 4）、全部哨兵错误
- Produces: `GET /api/reclaim`、`POST /api/tasks/{id}/reclaim`

- [ ] **Step 1: 写失败测试**

```go
// 在 server_test.go 追加。newTestServer 复用本文件既有的建法。

func TestHandleReclaimDirtyReturns409WithReason(t *testing.T) {
	// 构造：终态任务 + 脏 managed worktree
	s, id := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/reclaim",
		strings.NewReader(`{"force":false}`))
	s.mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("脏树应返 409，实得 %d：%s", rec.Code, rec.Body.String())
	}
	var body proto.ReclaimError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体应是 ReclaimError：%v：%s", err, rec.Body.String())
	}
	if body.Reason != proto.ReasonDirty {
		t.Fatalf("reason 应为 dirty，实得 %q", body.Reason)
	}
	if len(body.Dirty) == 0 {
		t.Fatalf("dirty 清单不能为空——CLI 要靠它渲染改动列表")
	}
}

func TestHandleReclaimNonTerminalReturns409NotTerminal(t *testing.T) {
	s, id := newServerWithRunningTask(t)
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/tasks/"+id+"/reclaim", strings.NewReader(`{}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("非终态应返 409，实得 %d", rec.Code)
	}
	var body proto.ReclaimError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Reason != proto.ReasonNotTerminal {
		t.Fatalf("reason 应为 not_terminal，实得 %q", body.Reason)
	}
}

func TestHandleReclaimUnknownTaskReturns404(t *testing.T) {
	s, _ := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/tasks/no-such/reclaim", strings.NewReader(`{}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的任务应返 404，实得 %d", rec.Code)
	}
}

func TestHandleReclaimListReturnsRows(t *testing.T) {
	s, id := newServerWithDirtyWorktree(t)
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/reclaim", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("列表应返 200，实得 %d", rec.Code)
	}
	var body proto.ReclaimListResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解列表响应：%v", err)
	}
	if len(body.Rows) != 1 || body.Rows[0].TaskID != id {
		t.Fatalf("应含那条脏树任务，实得 %+v", body.Rows)
	}
}
```

> 若 `server_test.go` 里没有 `s.mux()` 这样的接缝，实现时按本文件既有用例
> 调用 handler 的方式来（直接调 `s.handleReclaim(rec, req)` 并用
> `req.SetPathValue("id", id)` 注入路径参数）。助手
> `newServerWithDirtyWorktree` / `newServerWithRunningTask` 复用 Task 4/5 的
> `newReclaimManager` + `seedTerminalTask`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestHandleReclaim -v`
Expected: FAIL（404 或 undefined handler）

- [ ] **Step 3: 写最小实现**

路由（`server.go` 路由表内，紧邻 `/api/footprint` 与 `/api/tasks/{id}/stop`）：

```go
	mux.HandleFunc("GET /api/reclaim", s.handleReclaimList)
	mux.HandleFunc("POST /api/tasks/{id}/reclaim", s.handleReclaim)
```

Handler：

```go
// handleReclaimList 返回全部终态任务的 managed worktree 残留体检结果。
func (s *Server) handleReclaimList(w http.ResponseWriter, r *http.Request) {
	s.log.Info("残留体检请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Error("manager 未就绪，无法体检残留")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.ReclaimList()
	if err != nil {
		s.log.Error("残留体检失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("残留体检请求完成", "rows", len(resp.Rows), "scanned", resp.Scanned)
	writeJSON(w, http.StatusOK, resp)
}

// handleReclaim 回收单个终态任务的 managed worktree。
//
// 状态码：404 任务不存在；409 四种拒绝（reason 区分）；200 成功。
//
// 注意：四种 409 共用状态码，响应体必须带机器码 reason——CLI 靠它分派渲染，
// 解析中文文案是不行的（文案会改，机器码不改）
func (s *Server) handleReclaim(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("回收请求", "method", r.Method, "path", r.URL.Path, "task", taskID)
	if s.mgr == nil {
		s.log.Warn("回收请求到达但 manager 未注入", "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var body struct {
		Force bool `json:"force"`
	}
	// 解码失败按 force=false 处理：强删是破坏性动作，看不懂的输入必须走保守的那边
	_ = json.NewDecoder(r.Body).Decode(&body)

	resp, err := s.mgr.Reclaim(r.Context(), taskID, body.Force)
	if err != nil {
		s.writeReclaimError(w, taskID, err)
		return
	}
	s.log.Info("回收请求完成", "task", taskID, "action", resp.Action, "removed", resp.Removed)
	writeJSON(w, http.StatusOK, resp)
}

// writeReclaimError 把 Reclaim 的错误翻成 HTTP 应答。
//
// 注意：4xx 一律 Warn 不 Error（B11 已定的纪律）——被拒不是 agentd 出故障
func (s *Server) writeReclaimError(w http.ResponseWriter, taskID string, err error) {
	var de *ErrDirtyWorktree
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("回收被拒：任务不存在", "task", taskID)
		writeJSON(w, http.StatusNotFound, proto.ReclaimError{Error: err.Error()})
	case errors.As(err, &de):
		s.log.Warn("回收被拒：工作树脏", "task", taskID, "dirty", len(de.Files))
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonDirty, Dirty: de.Files})
	case errors.Is(err, ErrReclaimNotTerminal):
		s.log.Warn("回收被拒：任务非终态", "task", taskID)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonNotTerminal})
	case errors.Is(err, ErrReclaimNotManaged):
		s.log.Warn("回收被拒：非 managed 工作区", "task", taskID)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonNotManaged})
	case errors.Is(err, ErrReclaimRepoUnreachable):
		s.log.Warn("回收被拒：仓库不可达", "task", taskID, "cause", err)
		writeJSON(w, http.StatusConflict, proto.ReclaimError{
			Error: err.Error(), Reason: proto.ReasonRepoUnreachable})
	default:
		s.log.Error("回收失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, proto.ReclaimError{Error: err.Error()})
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestHandleReclaim -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

确认：两个 handler 都有进入日志（含 method/path/task）与完成日志（含结果字段）；
`writeReclaimError` 的五条拒绝分支全部 Warn 带上下文，只有兜底 500 走 Error。

- [ ] **Step 6: 补注释自检**

确认 `handleReclaim` 写了「为什么 reason 是必需的」，body 解码失败按 false 处理写了「为什么」。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/server.go internal/agentd/server_test.go
git commit -m "feat(agentd): reclaim 两端点——四种 409 带机器码 reason"
```

---

### Task 7: client 方法与 404 消歧

**Files:**
- Modify: `internal/client/client.go`
- Test: `internal/client/client_test.go`（追加）

**Interfaces:**
- Consumes: `proto.ReclaimListResp` / `proto.ReclaimResp` / `proto.ReclaimError`（Task 1）
- Produces:
  - `var ErrReclaimUnsupported error`
  - `type ReclaimRejected struct { Reason proto.ReclaimReason; Msg string; Dirty []proto.DirtyFile }`
  - `func (c *Client) ReclaimList(ctx context.Context) (*proto.ReclaimListResp, error)`
  - `func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)`

- [ ] **Step 1: 写失败测试**

```go
// 在 client_test.go 追加。

// 老 agentd：两个端点都 404 → 判定为不支持，调用方据此降级退 0。
func TestReclaimOnOldAgentdReportsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // 老 agentd：两条路由都不存在
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	if !errors.Is(err, ErrReclaimUnsupported) {
		t.Fatalf("两端点皆 404 应判为不支持，实得 %v", err)
	}
}

// 新 agentd + 不存在的任务：动作 404 但列表 200 → 任务是真不存在。
// 这两条走同一个 HTTP 码，用例分不开就等于没修。
func TestReclaimUnknownTaskIsNotMistakenForUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/reclaim" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"rows":[],"scanned":0}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"任务 abc 不存在"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	if err == nil {
		t.Fatalf("应报错")
	}
	if errors.Is(err, ErrReclaimUnsupported) {
		t.Fatalf("列表可用时不得判成「不支持」，实得 %v", err)
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误应透传服务端真因，实得 %v", err)
	}
}

func TestReclaimDirtyRejectionCarriesStructuredList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"工作树有 2 项未提交改动或未跟踪文件",
"reason":"dirty","dirty":[{"status":" M","path":"a.go"},{"status":"??","path":"b.log"}]}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "tok").Reclaim(context.Background(), "abc", false)
	var rej *ReclaimRejected
	if !errors.As(err, &rej) {
		t.Fatalf("409 应解成 ReclaimRejected，实得 %v", err)
	}
	if rej.Reason != proto.ReasonDirty || len(rej.Dirty) != 2 {
		t.Fatalf("拒绝详情解析错：%+v", rej)
	}
}

func TestReclaimListUnsupportedOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "tok").ReclaimList(context.Background()); !errors.Is(err, ErrReclaimUnsupported) {
		t.Fatalf("列表 404 应判为不支持，实得 %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run TestReclaim -v`
Expected: FAIL，`undefined: ErrReclaimUnsupported`

- [ ] **Step 3: 写最小实现**

```go
// 追加到 internal/client/client.go

// ErrReclaimUnsupported 表示对端 agentd 太旧，没有 worktree 回收端点。
//
// 与 ErrStatusUnsupported / ErrFootprintUnsupported 分开：处置建议不同
// （这条说「升级后才能远程回收，眼下只能上机器 git worktree remove」）
var ErrReclaimUnsupported = errors.New("对端 agentd 不支持 worktree 回收")

// ReclaimRejected 是一次被拒的回收，带机器码与（脏树时的）改动清单。
//
// 为什么不做成一堆哨兵：四种拒绝共用 409，调用方要的是「哪一种 + 细节」，
// 一个带 Reason 字段的类型比四个哨兵加一次类型断言更直白
type ReclaimRejected struct {
	Reason proto.ReclaimReason
	Msg    string
	Dirty  []proto.DirtyFile
}

func (e *ReclaimRejected) Error() string { return e.Msg }

// ReclaimList 拉取对端全部终态任务的 worktree 残留体检结果。
//
// 返回：
//   - 体检结果；404（对端过旧）返回 ErrReclaimUnsupported
//   - 请求失败或响应非法时返回错误
func (c *Client) ReclaimList(ctx context.Context) (*proto.ReclaimListResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/reclaim", nil)
	if err != nil {
		return nil, fmt.Errorf("残留体检请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 与 Status/Footprint 的 404 同款：这是预期结论不是异常，用 Debug
		c.log().Debug("对端 agentd 不支持 /api/reclaim，按版本过旧处理")
		return nil, ErrReclaimUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("残留体检", resp)
	}
	var out proto.ReclaimListResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析残留体检响应: %w", err)
	}
	return &out, nil
}

// Reclaim 回收指定终态任务残留的 managed worktree。
//
// 参数：
//   - taskID: 目标任务
//   - force: 对脏工作树强删（丢弃未提交改动）
//
// 返回：
//   - 回收结果
//   - ErrReclaimUnsupported: 对端过旧
//   - *ReclaimRejected: 被拒（409），带机器码与改动清单
//   - 其余错误：连不上、401、5xx、任务不存在
//
// 注意（404 消歧）：老 agentd 没有这条路由，POST 打过去也是 404——与「任务
// 不存在」撞码。照直翻译会对着一台好机器报「任务不存在」，把人引向完全错误
// 的方向。因此收到 404 时补打一次 GET /api/reclaim：它也 404 才是老 agentd，
// 它 200 说明任务是真不存在。只在错误路径上多一次往返，换一个不靠猜的结论
func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error) {
	// c.do 自己会 json.Marshal(body)——这里必须传**值**，不能传 io.Reader。
	// 传 bytes.NewReader 会被二次序列化成 {}（*bytes.Reader 没有导出字段），
	// force 永远到不了服务端，而且两侧都不报错，只是 --force 静默失效
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/reclaim",
		map[string]bool{"force": force})
	if err != nil {
		return nil, fmt.Errorf("回收 worktree 请求: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if _, lerr := c.ReclaimList(ctx); errors.Is(lerr, ErrReclaimUnsupported) {
			c.log().Debug("对端两条 reclaim 路由皆 404，按版本过旧处理", "task", taskID)
			return nil, ErrReclaimUnsupported
		}
		return nil, c.httpError("回收 worktree", resp)
	}
	if resp.StatusCode == http.StatusConflict {
		var re proto.ReclaimError
		if derr := json.NewDecoder(resp.Body).Decode(&re); derr != nil {
			return nil, c.httpError("回收 worktree", resp)
		}
		c.log().Warn("回收被拒", "task", taskID, "reason", re.Reason, "dirty", len(re.Dirty))
		return nil, &ReclaimRejected{Reason: re.Reason, Msg: re.Error, Dirty: re.Dirty}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("回收 worktree", resp)
	}
	var out proto.ReclaimResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析回收响应: %w", err)
	}
	return &out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client/ -run TestReclaim -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

确认：404 降级判定与 409 被拒都有日志（前者 Debug——它是预期结论，Info 会污染诊断
命令的 stderr；后者 Warn 带 reason 与脏项数）。

- [ ] **Step 6: 补注释自检**

确认 `Reclaim` 的 doc 把 404 消歧的**为什么**写全了（撞码 → 会误导 → 补探测），
`ReclaimRejected` 写了「为什么不做成四个哨兵」。

- [ ] **Step 7: 提交**

```bash
git add internal/client/client.go internal/client/client_test.go
git commit -m "feat(client): reclaim 两方法——404 补探测消歧，409 解成结构化拒绝"
```

---

### Task 8: CLI 命令

**Files:**
- Create: `cmd/reclaim.go`
- Test: `cmd/reclaim_test.go`

**Interfaces:**
- Consumes: `client.ReclaimList` / `client.Reclaim` / `client.ErrReclaimUnsupported` / `client.ReclaimRejected`（Task 7）、`TargetEndpoint()`、`short8()`（`cmd/status.go:310`）
- Produces: `handoff reclaim [task] [--target x] [--force] [--json]`

- [ ] **Step 1: 写失败测试**

```go
// reclaim_test.go —— handoff reclaim 的渲染与退出语义测试。
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
)

func TestRenderReclaimListShowsAllStates(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimList(&buf, &proto.ReclaimListResp{
		Scanned: 40,
		Rows: []proto.ReclaimRow{
			{TaskID: "2c58bbb7-0000-0000-0000-000000000000", Name: "b73 围栏 r2",
				State: "failed", WorkDir: "/w/2c58bbb7",
				Worktree: proto.WorktreeDirty, DirtyCount: 4},
			{TaskID: "ef012345-0000-0000-0000-000000000000", Name: "b69 足迹",
				State: "failed", WorkDir: "/w/ef012345",
				Worktree: proto.WorktreePrunable, Note: "gitdir file points to non-existent location"},
			{TaskID: "9a8b7c6d-0000-0000-0000-000000000000", Name: "b52 子会话",
				State: "failed", WorkDir: "/w/9a8b7c6d",
				Worktree: proto.WorktreeUnknown, Note: "仓库不可达"},
		},
	})
	out := buf.String()
	for _, want := range []string{"共体检 40", "2c58bbb7", "脏", "4", "元数据残留", "判不出"} {
		if !strings.Contains(out, want) {
			t.Fatalf("列表输出应含 %q，实得：\n%s", want, out)
		}
	}
}

func TestRenderReclaimListEmptyIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimList(&buf, &proto.ReclaimListResp{Scanned: 40})
	out := buf.String()
	if !strings.Contains(out, "无") || !strings.Contains(out, "40") {
		t.Fatalf("无残留应一行收口并报体检数，实得：%s", out)
	}
}

func TestRenderReclaimResultKeepsBranchNotice(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimResult(&buf, "2c58bbb7", &proto.ReclaimResp{
		Removed: true, Action: proto.ReclaimRemoved,
		WorkDir: "/w/2c58bbb7", Branch: "feat/b73-proc-fence-r2",
	})
	out := buf.String()
	// 「分支保留」必须每次都说：删了树没删分支是本命令最容易被误解的地方
	if !strings.Contains(out, "分支") || !strings.Contains(out, "feat/b73-proc-fence-r2") {
		t.Fatalf("成功输出必须点名分支被保留，实得：%s", out)
	}
}

func TestRenderReclaimResultAlreadyAbsent(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimResult(&buf, "2c58bbb7", &proto.ReclaimResp{
		Removed: false, Action: proto.ReclaimAlreadyAbsent, WorkDir: "/w/2c58bbb7",
	})
	if !strings.Contains(buf.String(), "无残留") {
		t.Fatalf("幂等成功应明说无残留，实得：%s", buf.String())
	}
}

func TestRenderDirtyRejectionListsFilesAndForceHint(t *testing.T) {
	var buf bytes.Buffer
	renderDirtyRejection(&buf, "2c58bbb7", "/w/2c58bbb7", []proto.DirtyFile{
		{Status: " M", Path: "internal/prochost/fence.go"},
		{Status: "??", Path: "scratch/probe.log"},
	})
	out := buf.String()
	for _, want := range []string{"internal/prochost/fence.go", "scratch/probe.log",
		"共 2 项", "handoff reclaim 2c58bbb7 --force"} {
		if !strings.Contains(out, want) {
			t.Fatalf("拒绝输出应含 %q，实得：\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestRenderReclaim|TestRenderDirty' -v`
Expected: FAIL，`undefined: renderReclaimList`

- [ ] **Step 3: 写最小实现**

```go
// reclaim.go —— handoff reclaim 子命令：回收终态任务残留的 managed worktree。
//
// 职责：
//   - 无参：列出仍占着 managed worktree 的终态任务（净/脏/元数据残留/判不出）
//   - 带任务 id：回收那一个；脏树默认拒绝并报出改动清单，--force 才强删
//
// 边界：
//   - 不删任务分支（审核者的工作成果），每次成功输出都明说这一点
//   - 不删任务目录（失败任务的排查素材还在里面）
//   - 不改任务状态：回收前后 handoff show 看到的状态一致
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

var (
	reclaimForce bool
	reclaimJSON  bool
)

// reclaimCmd 列出或回收终态任务残留的 managed worktree。
//
// 使用方式：
//
//	handoff reclaim [--target <名字>] [--json]           列
//	handoff reclaim <task> [--target <名字>] [--force]   收
var reclaimCmd = &cobra.Command{
	Use:   "reclaim [task]",
	Short: "回收终态任务残留的 managed worktree（不删分支）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		cl := client.New(addr, token)
		if len(args) == 0 {
			return runReclaimList(cmd, cl, addr)
		}
		return runReclaimOne(cmd, cl, args[0], addr)
	},
}

func init() {
	reclaimCmd.Flags().BoolVar(&reclaimForce, "force", false,
		"对有未提交改动的工作树也强删（丢弃这些改动）")
	reclaimCmd.Flags().BoolVar(&reclaimJSON, "json", false, "以 JSON 输出（仅列表形态）")
	rootCmd.AddCommand(reclaimCmd)
}

// runReclaimList 列出残留。
//
// 注意：**恒退 0**。这是一份报告，「有残留」是它的正常结论而非失败；
// 只有拿不到列表（连不上、401、5xx）才退非零
func runReclaimList(cmd *cobra.Command, cl *client.Client, addr string) error {
	out := cmd.OutOrStdout()
	resp, err := cl.ReclaimList(cmd.Context())
	switch {
	case errors.Is(err, client.ErrReclaimUnsupported):
		// 与 status/footprint 同款：404 是一条成功的诊断结论，不是失败
		fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
		fmt.Fprintln(out, "限制     该 agentd 不支持 /api/reclaim，残留不可得")
		fmt.Fprintln(out, "处置     升级远端 agentd 后重试")
		return nil
	case err != nil:
		return err
	}
	if reclaimJSON {
		return json.NewEncoder(out).Encode(resp)
	}
	renderReclaimList(out, resp)
	return nil
}

// runReclaimOne 回收单个任务的工作树。
//
// 注意：脏树被拒时把清单渲染到 stdout，只让 cobra 往 stderr 打一行短因由——
// 详情给人看、单行给脚本看，两边都不被对方淹没
func runReclaimOne(cmd *cobra.Command, cl *client.Client, taskID, addr string) error {
	out := cmd.OutOrStdout()
	resp, err := cl.Reclaim(cmd.Context(), taskID, reclaimForce)
	var rej *client.ReclaimRejected
	switch {
	case errors.Is(err, client.ErrReclaimUnsupported):
		fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
		fmt.Fprintln(out, "限制     该 agentd 不支持 worktree 回收")
		fmt.Fprintln(out, "处置     升级远端 agentd，或上机器手动 git worktree remove")
		return nil
	case errors.As(err, &rej):
		if rej.Reason == proto.ReasonDirty {
			renderDirtyRejection(out, taskID, "", rej.Dirty)
			return errors.New("未回收：工作树有未提交改动")
		}
		return err
	case err != nil:
		return err
	}
	renderReclaimResult(out, taskID, resp)
	return nil
}

// renderReclaimList 渲染残留列表。
//
// 注意：判不出的行**永远显示**。「没有残留」与「判不出」是两回事，把后者
// 按前者藏起来，等于用一个假结论把该看的东西盖住（同 renderFootprint 的规矩）
func renderReclaimList(w io.Writer, resp *proto.ReclaimListResp) {
	if len(resp.Rows) == 0 {
		fmt.Fprintf(w, "残留     无（共体检 %d 个终态任务）\n", resp.Scanned)
		return
	}
	fmt.Fprintf(w, "残留     %d 个终态任务仍占着 managed worktree（共体检 %d 个）\n",
		len(resp.Rows), resp.Scanned)
	for _, r := range resp.Rows {
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n",
			short8(r.TaskID), r.Name, r.State, worktreeLabel(r), r.WorkDir)
	}
}

// worktreeLabel 把工作树状态渲染成人读标签。
func worktreeLabel(r proto.ReclaimRow) string {
	switch r.Worktree {
	case proto.WorktreeClean:
		return "净"
	case proto.WorktreeDirty:
		return fmt.Sprintf("脏（%d 项改动）", r.DirtyCount)
	case proto.WorktreePrunable:
		return "元数据残留（目录已不存在）"
	case proto.WorktreeUnknown:
		return "⚠ 判不出：" + r.Note
	default:
		return string(r.Worktree)
	}
}

// renderReclaimResult 渲染一次回收的结果。
func renderReclaimResult(w io.Writer, taskID string, resp *proto.ReclaimResp) {
	if resp.Action == proto.ReclaimAlreadyAbsent {
		fmt.Fprintf(w, "无残留   %s 的 managed worktree 已不在，无需回收\n", short8(taskID))
		return
	}
	fmt.Fprintf(w, "已回收   %s 的 managed worktree\n", short8(taskID))
	fmt.Fprintf(w, "工作树   %s（%s）\n", resp.WorkDir, actionLabel(resp.Action))
	if n := len(resp.Discarded); n > 0 {
		// 强删不能悄悄发生：丢了什么必须打出来
		fmt.Fprintf(w, "已丢弃   %d 项未提交改动\n", n)
		for _, f := range resp.Discarded {
			fmt.Fprintf(w, "         %s  %s\n", f.Status, f.Path)
		}
	}
	if resp.Branch != "" {
		fmt.Fprintf(w, "提示     任务分支 %s 保留——reclaim 不删分支\n", resp.Branch)
	}
}

// actionLabel 把回收动作渲染成人读标签。
func actionLabel(a proto.ReclaimAction) string {
	if a == proto.ReclaimPruned {
		return "在册条目已清理"
	}
	return "已删除"
}

// renderDirtyRejection 渲染脏树拒绝的详情。
func renderDirtyRejection(w io.Writer, taskID, workdir string, files []proto.DirtyFile) {
	fmt.Fprintln(w, "拒绝     工作树有未提交改动，未回收")
	if workdir != "" {
		fmt.Fprintf(w, "工作树   %s\n", workdir)
	}
	for i, f := range files {
		label := "改动    "
		if i > 0 {
			label = "        "
		}
		fmt.Fprintf(w, "%s %s  %s\n", label, f.Status, f.Path)
	}
	fmt.Fprintf(w, "         （共 %d 项）\n", len(files))
	fmt.Fprintf(w, "处置     确认可丢弃后重跑：handoff reclaim %s --force\n", short8(taskID))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestRenderReclaim|TestRenderDirty' -v`
Expected: 全部 PASS

- [ ] **Step 5: 补日志自检**

CLI 层的「日志」就是它打给人的输出，本任务已在四条路径（列表 / 成功 / 幂等 / 拒绝）
上各有明确输出，无静默路径。确认**没有**用 `fmt.Printf` 直写 os.Stdout——
一律经 `cmd.OutOrStdout()`，否则测试拿不到输出。

- [ ] **Step 6: 补注释自检**

确认文件头写了职责与三条边界；`runReclaimList` 写了「为什么恒退 0」；
`runReclaimOne` 写了「为什么详情走 stdout、短因由走 stderr」；
`renderReclaimList` 写了「为什么判不出的行永远显示」。

- [ ] **Step 7: 提交**

```bash
git add cmd/reclaim.go cmd/reclaim_test.go
git commit -m "feat(cmd): handoff reclaim——无参列、带 id 收，脏树报清单给出 --force 出路"
```

---

### Task 9: 让清理失败当场带上出路

**Files:**
- Modify: `internal/agentd/manager.go:1102`（Done）、`internal/agentd/manager.go:1191`（Stop）、`internal/agentd/manager.go:763-775`（compensateWorkspace 签名与日志）、`internal/agentd/manager.go:610`（调用点）
- Modify: `internal/agentd/reclaim.go`（新增 `worktreeCleanupHint` / `shortTaskID`）
- Test: `internal/agentd/manager_test.go`（追加用例；:1584/:1603/:1631 三处调用补实参）

**Interfaces:**
- Consumes: 无新增
- Produces: 无新增导出符号；只改三处提示文案

`2c58bbb7` 无声漏掉的直接原因就是这条提示没给出真出路——手动 `git worktree remove`
撞的是同一堵 git 墙。**入口存在但没人知道，与入口不存在等价。**

- [ ] **Step 1: 写失败测试**

```go
// 在 manager_test.go 追加。

// 清理失败的提示必须指向可执行的出路，而不是一条会撞同一堵墙的手工命令。
func TestWorktreeCleanupFailureHintPointsToReclaim(t *testing.T) {
	got := worktreeCleanupHint("abcdef12-0000-0000-0000-000000000000",
		errors.New("contains modified or untracked files"))
	if !strings.Contains(got, "handoff reclaim abcdef12") {
		t.Fatalf("提示必须给出 reclaim 命令，实得：%s", got)
	}
	if strings.Contains(got, "git worktree remove") {
		t.Fatalf("不该再引导手动 git worktree remove——它撞的是同一堵墙：%s", got)
	}
	if !strings.Contains(got, "contains modified or untracked files") {
		t.Fatalf("提示必须带真因，实得：%s", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestWorktreeCleanupHint -v`
Expected: FAIL，`undefined: worktreeCleanupHint`

- [ ] **Step 3: 写最小实现**

在 `internal/agentd/reclaim.go` 追加：

```go
// worktreeCleanupHint 构造「清理失败」提示文案。
//
// 参数：
//   - taskID: 任务 ID（提示里只取前 8 位，与 CLI 的接受形态一致）
//   - cause: 清理失败的真因
//
// 返回：
//   - 带真因与可执行出路的一句话
//
// 注意：刻意不再提「请手动 git worktree remove」。清理失败最常见的原因就是
// 工作树脏而 remove 不带 --force，手工重跑同一条命令撞的是同一堵墙——
// B77 的 2c58bbb7 正是这么无声漏掉的
func worktreeCleanupHint(taskID string, cause error) string {
	return fmt.Sprintf("worktree 清理失败：%v，可重试：handoff reclaim %s",
		cause, shortTaskID(taskID))
}

// shortTaskID 取任务 ID 前 8 位（不足 8 位则原样返回）。
func shortTaskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
```

三处改动：

```go
// manager.go Done 内（原 :1102）
				Text: worktreeCleanupHint(taskID, werr),

// manager.go Stop 内（原 :1191）
				Text: worktreeCleanupHint(taskID, werr),

// manager.go compensateWorkspace 内（原 :775）——它只有日志、没有事件，
// 加一个 retry 字段把同一条出路带上
			m.log.Error("补偿清理 managed worktree 失败，保留分支待查",
				"repo", repo, "workdir", ws.WorkDir, "branch", ws.Branch,
				"retry", "handoff reclaim "+shortTaskID(taskID), "cause", err)
```

`Workspace`（`workspace.go:157`）**没有** `TaskID` 字段——它在 `WorkspaceReq` 上。
因此给 `compensateWorkspace` 加一个 taskID 形参，而不是给 `Workspace` 加字段
（那个结构体是「工作区结果」，塞任务身份会把两个概念揉在一起）：

```go
// compensateWorkspace 在 dispatch 后续步骤失败时复原已准备好的工作区。
//
// 参数：
//   - taskID: 用于在清理失败时给出可重试的 handoff reclaim 命令
func (m *Manager) compensateWorkspace(ctx context.Context, taskID, repo string, ws Workspace) {
```

调用点 `manager.go:610` 在 Dispatch 的 defer 里，taskID 是现成的：

```go
			m.compensateWorkspace(ctx, taskID, repoPath, ws)
```

`manager_test.go` 里三处调用（:1584 / :1603 / :1631）一并补上 taskID 实参。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestWorktreeCleanupHint|TestStop|TestDone' -v`
Expected: 全部 PASS（既有 Stop/Done 用例若断言过旧文案，一并更新为新文案）

- [ ] **Step 5: 补日志自检**

确认三处都带了真因与出路；`compensateWorkspace` 那条是 Error 级（补偿失败要留痕）。

- [ ] **Step 6: 补注释自检**

确认 `worktreeCleanupHint` 的 doc 写清了「为什么不再提手动 remove」——
不写这条理由，后来者很可能好心把手工命令加回去。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/reclaim.go internal/agentd/manager_test.go
git commit -m "fix(agentd): 清理失败提示改指 handoff reclaim，不再引导撞同一堵墙的手工命令"
```

---

### Task 10: 六闸门、变异检验与真机烟测

**Files:**
- Create: `docs/superpowers/notes/2026-08-12-worktree-reclaim-smoke.md`
- Modify: `docs/superpowers/backlog.md`（B77 行回填验收）

**Interfaces:**
- Consumes: 前九个 task 的全部产出
- Produces: 烟测记录 + backlog 验收证据

- [ ] **Step 1: 六闸门**

依次运行并把**实际输出**记进烟测文档：

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```
```bash
go test -race ./internal/agentd/ ./internal/client/ ./cmd/
```
```bash
GOOS=windows go build ./...
```

Expected: `gofmt -l .` 无输出；`go test ./...` 全包 ok；`-race` 三包 ok；交叉编译无输出。

- [ ] **Step 2: 变异检验（六条，逐条改码→指定用例 FAIL→还原→`git diff --exit-code` 干净）**

| # | 变异 | 必须 FAIL 的用例 |
|---|---|---|
| 1 | `classifyWorktree` 里脏判据改成只看已跟踪改动（过滤掉 `??` 开头的行） | `TestClassifyUntrackedOnlyIsDirty` |
| 2 | `Reclaim` 的 `if !force { return ... }` 改成无条件放行 | `TestReclaimRefusesDirtyWithoutForce` |
| 3 | `Reclaim` 里 `WorktreeUnknown` 分支改成走 `already_absent` | `TestReclaimRefusesWhenRepoUnreachable` |
| 4 | `ReclaimList` 里 `entries == nil` 分支改成 `continue`（判不出的行直接丢掉） | `TestReclaimListDegradesPerRepo` |
| 5 | `canonPath` 删掉父目录退让那一段 | `TestCanonPathResolvesMissingLeafViaParent` |
| 6 | `client.Reclaim` 的 404 分支直接返回 `ErrReclaimUnsupported`（不补探测） | `TestReclaimUnknownTaskIsNotMistakenForUnsupported` |

每条按：改码 → 跑该用例确认 FAIL → `git checkout -- <file>` 还原 → `git diff --exit-code`
确认工作树干净。**若某条变异的预期 FAIL 没出现，那是用例的缺陷，当场补用例**
（B72 的变异 4 就是这么发现 fixture 问题的），并在烟测文档里如实记录。

- [ ] **Step 3: 真机烟测（devbox）**

1. 编隔离二进制与隔离实例（独立端口 / 独立 DataDir，**不动生产 7777 与 `~/.handoff`**），
   照 B73 烟测文档 §1 的参数表建法。
2. 跑 `handoff reclaim`（无参），把输出与 devbox 上实际的
   `git worktree list --porcelain` 逐条对账。
   **这一步顺带结掉「那 15 个 failed 到底漏没漏」的悬案，并验证 spec §1.2 的脏树推断**——
   把对账结果（真残留几个、其中脏的几个、只是字段没回写的几个）写进烟测文档。
3. 挑一个净的回收，验 `action=removed`、目录消失、分支仍在。
4. 挑一个脏的回收，验 409 拒绝、改动清单正确、**工作树原样保留**。
5. `handoff reclaim 2c58bbb7 --force`，验强删成功、丢弃清单被打出来，
   随后 `git push --delete feat/b73-proc-fence-r2` **放行**。
6. 对同一个 id 再跑一次 `handoff reclaim`，验 `already_absent` 且退 0（幂等）。
7. 对一个 running 任务跑 `handoff reclaim`，验 409 `not_terminal` 且工作树保留。

- [ ] **Step 4: 写烟测记录**

按 `notes/2026-08-12-proc-fence-smoke.md` 的体例写
`docs/superpowers/notes/2026-08-12-worktree-reclaim-smoke.md`：结论速览、隔离实例参数表、
六闸门实际输出、七步真机验证的实际命令与输出、**以及第 2 步的对账结论**。
烟测中若照出真实缺陷，当场修掉并在文档里如实记录（B73 的 8.1/8.2 就是这么来的）。

- [ ] **Step 5: 回填 backlog**

把 B77 行改为 `✅ done(已验)`，`验收` 列写六闸门结果 + 变异检验逐条 + 真机七步的关键实测，
并注明「无原型/流程图，自动免除对照」。

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/notes/2026-08-12-worktree-reclaim-smoke.md docs/superpowers/backlog.md
git commit -m "docs: worktree 回收真机烟测记录与 B77 验收回填"
```

---

## 自审记录

**spec 覆盖**：§3.1 边界四条 → Task 4（终态/managed/幂等）+ 全局约束（不改状态）；
§3.2 地面真相 → Task 3 `repoWorktrees`；§3.3 prunable → Task 3 判定 + Task 4 兜底；
§3.4 判不出 → Task 3 + Task 5 + Task 4（两处相反处置各有用例）；§3.5 判定表 → Task 3/4；
§4 CLI 契约 → Task 8；§5 端点与 reason → Task 6；§5.1 404 消歧 → Task 7；
§6 降级接线 → Task 9；§7 错误处理纪律 → 全局约束 + Task 4/5 的 doc；
§8 验证策略 → Task 10。**无遗漏**。

**类型一致性**：`proto.WorktreeState` / `proto.ReclaimAction` / `proto.ReclaimReason`
三组常量在 Task 1 定义，Task 3–8 全程使用同名标识符；`ErrDirtyWorktree`（agentd 侧，
带 `Files`）与 `ReclaimRejected`（client 侧，带 `Reason`/`Msg`/`Dirty`）分属两层，
不混用；`canonPath` / `findEntry` / `worktreeCleanupHint` / `shortTaskID` 四个包级函数
只在 `internal/agentd` 内使用。

**已核实的既有设施**（计划中的引用均已对过真实代码）：
`short8`（`cmd/status.go:310`）、`truncateRunes`（`internal/agentd/server.go:1424`，同包可用）、
`gitRun(ctx, repo, args...) (stdout, stderr string, err error)`（`workspace.go:103`）、
`WorkspaceGitTimeout = 2 * time.Minute`（`workspace.go:84`）、`log()`（`workspace.go:88`）、
`Store.ListTasks() ([]proto.Task, error)`（`store.go:263`）、`store.ErrNotFound`（`store.go:39`）、
`TargetEndpoint()`（`cmd/root.go:161`）、`client.New(addr, token)`（`client.go:176`）、
`TaskState.IsTerminal()`（`proto.go:34`）、`proto.Task` 的 `RepoPath`/`WorkDir`/
`WorktreeManaged`/`Branch`/`Name`/`State` 字段（`proto.go:101-123`）。

**已知需实现时对齐的接缝**（不是占位符，是本仓库既有设施的复用点）：
`newReclaimManager` / `seedTerminalTask` / `newServerWithDirtyWorktree` 三个测试助手
要照抄本包既有测试的构造方式（`manager_test.go:1561` 的 `compensateOnlyManager` 是最近的样板）。
