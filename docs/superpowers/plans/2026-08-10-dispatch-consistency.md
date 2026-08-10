# dispatch 前置校验与失败补偿 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让远程派发在本地有未提交改动时拒发并给出可行动指引，让 dispatch 失败时的补偿把自己建的分支和切走的工作树一并复原。

**Architecture:** 两块互不依赖的改动。B29 全在**客户端** `cmd/` —— 只有客户端看得见自己的工作树；抽一个纯字符串分类函数承担判据，CLI 只负责取 `git status --porcelain` 和排版输出。B39 全在 **agentd** `internal/agentd/` —— 给 `Workspace` 补两个字段（本次新建分支的尖端 sha、切走之前的 HEAD），让补偿函数能回答「这分支是不是我建的」和「我该把树切回哪儿」，再按 managed / 非 managed 两种形态分派。

**Tech Stack:** Go 1.26.1、cobra（CLI）、log/slog（agentd 日志）、标准库 `os/exec` 调 git、`testing` 表驱动 + 真实 git 仓库集成测试。

**Spec:** [docs/superpowers/specs/2026-08-10-dispatch-consistency-design.md](../specs/2026-08-10-dispatch-consistency-design.md)

## Global Constraints

- 判据位置不可移动：B29 的检查**必须在客户端** `cmd/dispatch.go`，agentd 看不见客户端的工作树。
- B29 的触发门与基线采集**共用同一个条件**：`targetName != "" && !dispatchNoSyncCheck`，且只在 `localHeadCommit()` 返回非空（cwd 确是 git 仓库）时才查。
- CLI 的所有提示、警告、错误一律走 **stderr**。stdout 的「单行任务 JSON」是上层脚本按行解析的契约，多打一行就会打断它们。
- `--allow-dirty` **不得静默**：放行时必须照打警告并列出文件名。静默的 `--allow-dirty` 就是新的 B29。
- 补偿路径**只在 executor 未接管时生效**：`executorStarted` 守卫保持不变，不得改动。
- 补偿的三条 fail-safe 不可省略：worktree 删除失败 → 不删分支；切回原 ref 失败 → 不删分支；分支尖端与创建时不符 → 不删分支。任何一条拿不准都**保留现场**。
- 不改 `RemoveManagedWorktree` 自身的语义（「只删工作树不删分支」在 Done/Stop 路径上是对的），只在补偿路径**之外**追加删分支动作。
- 日志一律用注入的 `m.log`（slog），**禁止** `fmt.Printf`。CLI 侧无 logger，输出即契约。
- 验收门：`go build ./...` + `go vet ./...` + `gofmt -l .` 无新增 + `go test ./...` 全绿 + `GOOS=windows GOARCH=amd64 go build ./...` 全绿。

## File Structure

| 文件 | 职责 |
|---|---|
| `cmd/dispatch_dirty.go`（新建） | 本地工作区完整性校验：porcelain 分类、文件名排版、校验入口。不碰 cobra、不碰网络。 |
| `cmd/dispatch_dirty_test.go`（新建） | 上述三者的表驱动与 chdir 集成测试。 |
| `cmd/dispatch.go`（修改） | 注册 `--allow-dirty`，在既有基线采集块里调用校验入口。 |
| `cmd/dispatch_test.go`（修改） | 远程派发的 CLI 层断言：脏树零请求、`--allow-dirty` / `--no-sync-check` 放行、本机模式豁免。 |
| `internal/agentd/workspace.go`（修改） | `Workspace` 加 `NewBranchTip` / `PrevRef`；`PrepareWorkspace` 在三种模式下采集它们；新增 `currentRef` / `branchTip` 两个 git 小工具。 |
| `internal/agentd/workspace_test.go`（修改） | 三种模式的字段采集断言，含 detached HEAD。 |
| `internal/agentd/manager.go`（修改） | `compensateManagedWorktree` → `compensateWorkspace`，两种形态分派；新增 `deleteCreatedBranch` 承担三道闸。 |
| `internal/agentd/manager_test.go`（修改） | 七个补偿场景的集成测试。 |
| `README.md` / `skills/handoff/SKILL.md`（修改） | `--allow-dirty` 进 flag 表；本地脏被拒进排障表。 |

---

### Task 1: porcelain 分类函数

判据的全部逻辑集中在一个纯函数里，不碰 git、不碰 I/O，所以能被穷举测试。

**Files:**
- Create: `cmd/dispatch_dirty.go`
- Test: `cmd/dispatch_dirty_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `func classifyLocalDirty(porcelain string) (tracked, untracked []string)` —— 把 `git status --porcelain` 的输出分成「已跟踪文件的改动」与「未跟踪文件」两类，各返回路径列表；干净时两者均为 `nil`。

- [ ] **Step 1: 写失败测试**

创建 `cmd/dispatch_dirty_test.go`：

```go
// 本地工作区完整性校验的测试：porcelain 分类、文件名排版、校验入口。
package cmd

import (
	"reflect"
	"testing"
)

// TestClassifyLocalDirty 穷举 git status --porcelain 的行形态。
//
// 判别力所在：「已暂存改动」「重命名」「冲突」三行——把它们错分成未跟踪
// （或整行丢弃）的实现会在这里翻红，而只测「工作区改动 + 未跟踪」的用例
// 对那种实现照样绿。
func TestClassifyLocalDirty(t *testing.T) {
	cases := []struct {
		name          string
		porcelain     string
		wantTracked   []string
		wantUntracked []string
	}{
		{"干净", "", nil, nil},
		{"只有未跟踪", "?? scratch.md\n?? tmp.log\n", nil, []string{"scratch.md", "tmp.log"}},
		{"工作区改动", " M cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"已暂存改动", "M  cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"新增已暂存", "A  cmd/new.go\n", []string{"cmd/new.go"}, nil},
		{"删除", " D README.md\n", []string{"README.md"}, nil},
		{"重命名取新名", "R  old.go -> new.go\n", []string{"new.go"}, nil},
		{"冲突", "UU merge.go\n", []string{"merge.go"}, nil},
		{"混合", " M a.go\n?? b.txt\n", []string{"a.go"}, []string{"b.txt"}},
		{"含空格文件名保留引号", " M \"a b.go\"\n", []string{`"a b.go"`}, nil},
		{"空行忽略", " M a.go\n\n", []string{"a.go"}, nil},
		{"过短行忽略", "X\n M a.go\n", []string{"a.go"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracked, untracked := classifyLocalDirty(c.porcelain)
			if !reflect.DeepEqual(tracked, c.wantTracked) {
				t.Errorf("tracked = %#v, want %#v", tracked, c.wantTracked)
			}
			if !reflect.DeepEqual(untracked, c.wantUntracked) {
				t.Errorf("untracked = %#v, want %#v", untracked, c.wantUntracked)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestClassifyLocalDirty`
Expected: 编译失败，`undefined: classifyLocalDirty`

- [ ] **Step 3: 写最小实现**

创建 `cmd/dispatch_dirty.go`：

```go
// 本文件实现 dispatch 的本地工作区完整性校验（backlog B29）。
//
// 职责：
//   - 把 git status --porcelain 的输出分成「已跟踪改动」与「未跟踪文件」两类
//   - 已跟踪改动拒发（--allow-dirty 可放行），未跟踪只警告
//   - 全部提示走调用方给的 stderr writer
//
// 边界：
//   - 只看当前工作目录（cwd）这一棵树；agentd 侧任务仓库的脏检查是另一回事，
//     由 internal/agentd 的 ensureCleanWorktree 负责，两者互不替代
//   - 不发起任何网络请求：拒发必须发生在 HTTP 请求之前
//   - 不解释 git 的退出码：status 本身失败时降级放行，不把派发挡死
package cmd

import "strings"

// classifyLocalDirty 把 git status --porcelain 的输出分成「已跟踪改动」与
// 「未跟踪文件」两类。
//
// 参数：
//   - porcelain: git status --porcelain 的原始 stdout
//
// 返回：
//   - tracked: 已跟踪文件的改动路径（含已暂存的 M /A ，它们同样没进 commit）
//   - untracked: 未跟踪文件路径（?? 开头）
//   - 两者在无对应条目时为 nil
func classifyLocalDirty(porcelain string) (tracked, untracked []string) {
	for _, line := range strings.Split(porcelain, "\n") {
		// porcelain v1 每行形如 "XY PATH"：两位状态码 + 一个空格 + 路径。
		// 短于 4 的行（空行、意外输出）没有可用路径，跳过而不是当成空文件名
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// 重命名/拷贝形如 "R  old -> new"：审核者关心改动落在哪个新路径上
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = path[i+len(" -> "):]
		}
		if strings.HasPrefix(line, "??") {
			untracked = append(untracked, path)
			continue
		}
		tracked = append(tracked, path)
	}
	return tracked, untracked
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run TestClassifyLocalDirty -v`
Expected: 12 个子用例全 PASS

- [ ] **Step 5: 提交**

```bash
git add cmd/dispatch_dirty.go cmd/dispatch_dirty_test.go
git commit -m "feat(dispatch): porcelain 分类函数，区分已跟踪改动与未跟踪文件"
```

---

### Task 2: 校验入口与输出契约

把分类结果变成人能照做的输出，并决定拒不拒。

**Files:**
- Modify: `cmd/dispatch_dirty.go`（追加 `dirtyListLimit`、`formatDirtyList`、`checkLocalWorktree`）
- Test: `cmd/dispatch_dirty_test.go`（追加两组测试）

**Interfaces:**
- Consumes: `classifyLocalDirty(porcelain string) (tracked, untracked []string)`（Task 1）
- Produces:
  - `func formatDirtyList(files []string) string` —— 逗号连接，超过 5 个截断并补 `... 另有 N 处`
  - `func checkLocalWorktree(errOut io.Writer, allowDirty bool) error` —— 在 cwd 跑 `git status --porcelain` 并处置；拒发时返回非 nil error，放行时返回 nil（警告已写入 `errOut`）

- [ ] **Step 1: 写失败测试**

在 `cmd/dispatch_dirty_test.go` 末尾追加。注意 import 需补 `bytes`、`os/exec`、`path/filepath`、`os`、`strings`：

```go
// TestFormatDirtyList 钉死文件名列表的截断规则：超过 5 个只列前 5 个并补计数。
func TestFormatDirtyList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"空", nil, ""},
		{"单个", []string{"a.go"}, "a.go"},
		{"恰好五个", []string{"a", "b", "c", "d", "e"}, "a, b, c, d, e"},
		{"六个截断", []string{"a", "b", "c", "d", "e", "f"}, "a, b, c, d, e ... 另有 1 处"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatDirtyList(c.in); got != c.want {
				t.Errorf("formatDirtyList(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// dirtyTestRepo 建一个带一次提交的临时 git 仓库并 chdir 进去，返回仓库路径。
// t.Chdir 会在用例结束时自动切回，并禁止该用例与其他用例并行。
func dirtyTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	t.Chdir(dir)
	return dir
}

// TestCheckLocalWorktreeClean 干净仓库放行且零输出。
func TestCheckLocalWorktreeClean(t *testing.T) {
	dirtyTestRepo(t)
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, false); err != nil {
		t.Fatalf("干净工作区不该报错: %v", err)
	}
	if buf.String() != "" {
		t.Fatalf("干净工作区不该有输出，得到: %q", buf.String())
	}
}

// TestCheckLocalWorktreeTrackedRejects 已跟踪改动必须拒发，且错误里带得出文件名。
func TestCheckLocalWorktreeTrackedRejects(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := checkLocalWorktree(&buf, false)
	if err == nil {
		t.Fatal("已跟踪改动应拒发")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Fatalf("错误应列出脏文件名，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-dirty") {
		t.Fatalf("错误应给出 --allow-dirty 出路，得到: %v", err)
	}
}

// TestCheckLocalWorktreeAllowDirtyStillWarns 是本任务判别力最强的一条：
// --allow-dirty 放行，但**必须照打警告并列出文件名**。一个「allowDirty 直接
// return nil」的实现会在这里翻红——而那正是把 --allow-dirty 变成新 B29 的写法。
func TestCheckLocalWorktreeAllowDirtyStillWarns(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, true); err != nil {
		t.Fatalf("--allow-dirty 应放行: %v", err)
	}
	if !strings.Contains(buf.String(), "tracked.txt") {
		t.Fatalf("--allow-dirty 放行时仍须列出被忽略的文件，得到: %q", buf.String())
	}
}

// TestCheckLocalWorktreeUntrackedOnlyWarns 只有未跟踪文件时放行并警告。
func TestCheckLocalWorktreeUntrackedOnlyWarns(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, false); err != nil {
		t.Fatalf("只有未跟踪文件应放行: %v", err)
	}
	if !strings.Contains(buf.String(), "scratch.md") {
		t.Fatalf("未跟踪文件应被警告列出，得到: %q", buf.String())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestFormatDirtyList|TestCheckLocalWorktree'`
Expected: 编译失败，`undefined: formatDirtyList`、`undefined: checkLocalWorktree`

- [ ] **Step 3: 写最小实现**

在 `cmd/dispatch_dirty.go` 的 import 补 `fmt`、`io`、`os/exec`，并追加：

```go
// dirtyListLimit 是提示里最多列出的文件数。列全了会把有用信息挤出视线，
// 而审核者只需要「哪一类文件脏了」就够决定下一步。
const dirtyListLimit = 5

// formatDirtyList 把文件名列表拼成一行给人读；超过 dirtyListLimit 截断并补计数。
//
// 参数：files 为路径列表，可为空
// 返回：逗号连接的单行文本；files 为空时返回空串
func formatDirtyList(files []string) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) <= dirtyListLimit {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s ... 另有 %d 处",
		strings.Join(files[:dirtyListLimit], ", "), len(files)-dirtyListLimit)
}

// checkLocalWorktree 校验当前工作目录是否有「不会随基线送到 executor」的改动。
//
// 参数：
//   - errOut: 提示与警告的输出目标（调用方传 cmd.ErrOrStderr()）
//   - allowDirty: 为真时已跟踪改动只警告不拒发（--allow-dirty）
//
// 返回：
//   - 已跟踪文件有未提交改动且 allowDirty 为假 → 返回可行动的错误（调用方据此中止派发）
//   - 其余一律返回 nil；未跟踪文件与被放行的已跟踪改动都已写入 errOut
//
// 注意：
//   - 必须在发起 HTTP 请求之前调用——拒发的价值就在于不产生任何远端副作用
//   - git status 自身失败时降级放行：调用点已确认 cwd 是 git 仓库（HEAD 解析成功），
//     走到这里的失败属异常情形，不该因此把派发挡死
func checkLocalWorktree(errOut io.Writer, allowDirty bool) error {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		fmt.Fprintln(errOut, "提示: 读取本地工作区状态失败，已跳过完整性校验:", err)
		return nil
	}
	tracked, untracked := classifyLocalDirty(string(out))
	if len(untracked) > 0 {
		fmt.Fprintf(errOut, "提示: 本地有 %d 个未跟踪文件不会被派发（executor 看不到）：%s\n",
			len(untracked), formatDirtyList(untracked))
	}
	if len(tracked) == 0 {
		return nil
	}
	// 放行也必须留痕：静默的 --allow-dirty 就是新的 B29
	if allowDirty {
		fmt.Fprintf(errOut, "警告: 本地有 %d 处未提交的已跟踪改动，--allow-dirty 已放行（executor 看不到它们）：%s\n",
			len(tracked), formatDirtyList(tracked))
		return nil
	}
	return fmt.Errorf("本地工作区有 %d 处未提交的已跟踪改动，executor 看不到它们：%s\n"+
		"远程派发会基于不含这些改动的基线开工。请先 git commit 或 git stash；"+
		"确要照现状派发，加 --allow-dirty",
		len(tracked), formatDirtyList(tracked))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestFormatDirtyList|TestCheckLocalWorktree' -v`
Expected: 全 PASS

- [ ] **Step 5: 提交**

```bash
git add cmd/dispatch_dirty.go cmd/dispatch_dirty_test.go
git commit -m "feat(dispatch): 本地工作区校验入口，已跟踪改动拒发、未跟踪警告"
```

---

### Task 3: 接进 dispatch 命令

**Files:**
- Modify: `cmd/dispatch.go`（`dispatchAllowDirty` 变量、flag 注册、基线采集块内调用）
- Test: `cmd/dispatch_test.go`（追加 CLI 层断言）

**Interfaces:**
- Consumes: `checkLocalWorktree(errOut io.Writer, allowDirty bool) error`（Task 2）
- Produces: CLI flag `--allow-dirty`；远程派发在本地已跟踪脏时**不发起 HTTP 请求**即返回错误

- [ ] **Step 1: 写失败测试**

在 `cmd/dispatch_test.go` 末尾追加。核心判别力在**请求计数**——只断言「返回错误」的话，一个「先发请求再报错」的实现照样绿，而那正好丢掉了拒发的全部价值。需在 import 补 `os`、`os/exec`、`path/filepath`、`sync/atomic`（`bytes`/`fmt`/`net/http`/`httptest`/`strings` 已有）：

```go
// dirtyCwd 在 t.TempDir() 里造一个「已跟踪文件被改过」的仓库并 chdir 进去，
// 返回仓库路径。t.Chdir 在用例结束时自动切回。
func dirtyCwd(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	// 改一个已跟踪文件且不提交——这就是 B29 会静默丢掉的那类改动
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	return repo
}

// runRemoteDispatch 以远程模式（--target e2e）执行 dispatch，返回 stdout、
// 假 agentd 收到的请求数与错误。
//
// 不复用 runDispatch：那个 helper 恒置 targetName = ""（本机模式），走不进
// 远程派发的门；这里必须自己搭一份带 targets 的配置。
func runRemoteDispatch(t *testing.T, repo string, extraArgs ...string) (string, int32, error) {
	t.Helper()
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, dispatchTestTaskJSON)
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \"127.0.0.1:7777\"\ntoken: \""+testToken+"\"\n"+
		"targets:\n  e2e:\n    addr: \""+addr+"\"\n    token: \""+testToken+"\"\n")

	// resetFlags 已负责在用例结束时复原 agentdURL/targetName/configPath 与
	// --agentd 的 Changed 标记
	resetFlags(t)
	configPath = cfgPath
	targetName = "e2e"
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() {
		dispatchNoTerminal = false
		dispatchAllowDirty = false
		dispatchNoSyncCheck = false
	})

	args := append([]string{"dispatch", "--repo", repo, "--prompt", "x", "--no-terminal"}, extraArgs...)
	rootCmd.SetArgs(args)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	var out, errBuf bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := Execute()
	return out.String(), hits.Load(), err
}

// TestDispatchRemoteDirtyDoesNotSendRequest 验证远程派发在本地已跟踪脏时
// **一个 HTTP 请求都不发**就返回错误。
//
// 判别力：请求计数。只断言 err != nil 的话，「先发请求再报错」的实现照样绿，
// 而那会在远端建出分支和 worktree——正是 B39 现场的成因。
func TestDispatchRemoteDirtyDoesNotSendRequest(t *testing.T) {
	repo := dirtyCwd(t)
	_, hits, err := runRemoteDispatch(t, repo)
	if err == nil {
		t.Fatal("本地已跟踪脏时远程派发应被拒")
	}
	if !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("错误应列出脏文件，得到: %v", err)
	}
	if hits != 0 {
		t.Fatalf("拒发时不该发起任何 HTTP 请求，实际 %d 次", hits)
	}
}

// TestDispatchRemoteDirtyAllowDirty 验证 --allow-dirty 让同一场景放行到底。
func TestDispatchRemoteDirtyAllowDirty(t *testing.T) {
	repo := dirtyCwd(t)
	out, hits, err := runRemoteDispatch(t, repo, "--allow-dirty")
	if err != nil {
		t.Fatalf("--allow-dirty 应放行: %v", err)
	}
	if hits != 1 {
		t.Fatalf("放行后应正常发起一次派发请求，实际 %d 次", hits)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("stdout 应仍是单行任务 JSON，得到: %q", out)
	}
}

// TestDispatchNoSyncCheckSkipsDirty 验证 --no-sync-check 把整块基线逻辑
// （含本地工作区校验）一并关掉——它关的是「根本不看 cwd」，语义上必须覆盖新检查。
func TestDispatchNoSyncCheckSkipsDirty(t *testing.T) {
	repo := dirtyCwd(t)
	_, hits, err := runRemoteDispatch(t, repo, "--no-sync-check")
	if err != nil {
		t.Fatalf("--no-sync-check 应跳过本地校验: %v", err)
	}
	if hits != 1 {
		t.Fatalf("应正常发起一次派发请求，实际 %d 次", hits)
	}
}

// TestDispatchLocalDirtyNotChecked 验证**本机派发**（无 --target）完全不查本地工作区。
//
// 为什么本机模式必须豁免：cwd 与 --repo 可以是两个毫不相干的仓库，查 cwd 是查错了
// 对象。这也正是既有代码不在本机模式采基线的原因，新检查必须共用同一道门。
func TestDispatchLocalDirtyNotChecked(t *testing.T) {
	dirtyCwd(t)
	out, _, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("本机派发不该因 cwd 脏而失败: %v", err)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("本机派发应正常返回任务 JSON，得到: %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestDispatchRemoteDirty|TestDispatchNoSyncCheckSkipsDirty|TestDispatchLocalDirtyNotChecked'`
Expected: 编译失败（`undefined: dispatchAllowDirty`、`unknown flag: --allow-dirty`）；补上 flag 后 `TestDispatchRemoteDirtyDoesNotSendRequest` FAIL 于「本地已跟踪脏时远程派发应被拒」，其余三条 PASS（它们是防误伤锚：新检查不得波及本机模式、`--no-sync-check` 与 `--allow-dirty` 放行路径）

- [ ] **Step 3: 写最小实现**

`cmd/dispatch.go` 三处改动。

其一，变量块（现有 `dispatchNoSyncCheck` 之后）追加：

```go
	dispatchAllowDirty  bool
```

其二，把既有的基线采集块替换为（`else if` 保证只在 cwd 确是 git 仓库时才查 status）：

```go
		baseCommit := ""
		if targetName != "" && !dispatchNoSyncCheck {
			baseCommit = localHeadCommit()
			if baseCommit == "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"提示: 当前目录不是 git 仓库，已跳过远程基线校验（远程仓库可能落后于你的本地代码）")
			} else if err := checkLocalWorktree(cmd.ErrOrStderr(), dispatchAllowDirty); err != nil {
				// B29：基线只带得走已提交的东西。这里拒发是为了不在远端留下
				// 任何副作用（分支、worktree、任务记录），所以必须在发请求之前
				return err
			}
		}
```

其三，flag 注册（现有 `--no-sync-check` 之后）追加：

```go
	dispatchCmd.Flags().BoolVar(&dispatchAllowDirty, "allow-dirty", false,
		"本地工作区有未提交的已跟踪改动时仍照常派发（executor 看不到这些改动）")
```

同时更新文件头注释的职责段，把「远程派发时采集本地 HEAD 作基线」一条改写为：

```go
//   - 远程派发时采集本地 HEAD 作基线随请求上送，并校验本地工作区完整性
//     （已跟踪改动拒发、未跟踪警告；--no-sync-check 关掉整块，--allow-dirty 只关拒发）
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -v`
Expected: 新用例 PASS，`cmd` 包既有用例全部仍 PASS（尤其 `runDispatch` 系列——它们 `targetName = ""`，走不进新加的门）

- [ ] **Step 5: 补充成功路径的输出验证**

Run: `go test ./cmd/ -run TestDispatch -v`
Expected: 全 PASS。确认 stdout 仍只有单行任务 JSON（新增输出全在 stderr）。

- [ ] **Step 6: 提交**

```bash
git add cmd/dispatch.go cmd/dispatch_test.go
git commit -m "feat(dispatch): 远程派发前校验本地工作区，拒发不发起请求"
```

---

### Task 4: Workspace 记住「分支是我建的」和「树从哪来」

补偿要做正确的事，先得有据可依。这两个字段就是那个依据。

**Files:**
- Modify: `internal/agentd/workspace.go`（`Workspace` 结构、`PrepareWorkspace` 三处分支、新增两个 git 小工具）
- Test: `internal/agentd/workspace_test.go`

**Interfaces:**
- Consumes: 既有 `gitRun(ctx, repo, args...) (stdout, stderr string, err error)`
- Produces:
  - `Workspace.NewBranchTip string` —— 本次新建分支时的尖端 sha；空串表示分支不是本次新建的
  - `Workspace.PrevRef string` —— 非 managed 模式 `checkout` 之前的 HEAD（分支名，detached 时为 sha）；managed 模式恒为空
  - `func currentRef(ctx context.Context, dir string) string`
  - `func branchTip(ctx context.Context, repo, branch string) string`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/workspace_test.go` 末尾追加（复用文件内已有的 `initTestRepo`、`gitT`、`gitOut`、`writeAndCommit` 助手）：

```go
// TestPrepareWorkspaceRecordsNewBranchTip 验证三种工作树模式下，新建分支时
// 都记下了它的尖端 sha，而切已存在分支时该字段为空。
//
// 判别力：最后一条（--branch 已存在分支 → NewBranchTip 必须为空）。缺了它，
// 一个「无条件记 tip」的实现会让补偿把用户自己的分支删掉。
func TestPrepareWorkspaceRecordsNewBranchTip(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")

	// 新工作树 + 自动分支
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "aaaaaaaa-0000-0000-0000-000000000000",
		NewWorktree: true, WorktreesDir: filepath.Join(t.TempDir(), "wt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.NewBranchTip != head {
		t.Errorf("新建分支应记下尖端 %s，得到 %q", head, ws.NewBranchTip)
	}
	if ws.PrevRef != "" {
		t.Errorf("managed 模式 PrevRef 应为空，得到 %q", ws.PrevRef)
	}

	// 新工作树 + 已存在分支
	gitT(t, repo, "branch", "mine")
	ws2, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "bbbbbbbb-0000-0000-0000-000000000000",
		Branch: "mine", NewWorktree: true, WorktreesDir: filepath.Join(t.TempDir(), "wt2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws2.NewBranchTip != "" {
		t.Errorf("切已存在分支时 NewBranchTip 必须为空，得到 %q", ws2.NewBranchTip)
	}
}

// TestPrepareWorkspaceRecordsPrevRefInPlace 验证原地模式记下了切走之前的分支名。
func TestPrepareWorkspaceRecordsPrevRefInPlace(t *testing.T) {
	repo := initTestRepo(t)
	before := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "cccccccc-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != before {
		t.Errorf("原地模式应记下原分支 %s，得到 %q", before, ws.PrevRef)
	}
	if ws.NewBranchTip == "" {
		t.Error("原地模式自动分支也是新建分支，NewBranchTip 不应为空")
	}
}

// TestPrepareWorkspaceRecordsPrevRefDetached 验证 detached HEAD 起步时，
// PrevRef 退回 commit sha——它同样能直接喂给 git checkout 复原。
//
// 判别力：一个只用 symbolic-ref 的实现在这里会记下空串，补偿就无从复原。
func TestPrepareWorkspaceRecordsPrevRefDetached(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "--detach", "-q", head)
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "dddddddd-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != head {
		t.Errorf("detached 起步应记下 commit sha %s，得到 %q", head, ws.PrevRef)
	}
}
```

若该文件尚未 import `strings` / `filepath` / `context`，一并补上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestPrepareWorkspaceRecords`
Expected: 编译失败，`ws.NewBranchTip undefined`、`ws.PrevRef undefined`

- [ ] **Step 3: 写最小实现**

`internal/agentd/workspace.go` 四处改动。

其一，`Workspace` 结构（原第 141-145 行）改为：

```go
type Workspace struct {
	Branch  string
	WorkDir string // executor cwd 与审阅命令目录；原地模式 = Repo
	Managed bool   // WorkDir 是 agentd 创建的 worktree（done 时代删）

	// NewBranchTip 是本次 dispatch 新建分支时的尖端 sha；空串表示分支不是本次
	// 新建的（--branch <已存在分支> 模式）。补偿删分支前用它复核「自创建以来
	// 没动过」。
	//
	// 为什么用 sha 而不是 BranchCreated bool：一个 bool 加一个 sha 能构造出
	// 「声称建了分支却说不出它当时指向哪」这种非法状态，用单字段就构造不出来。
	NewBranchTip string
	// PrevRef 是非 managed 模式下 checkout 之前的 HEAD：正常在分支上时为分支名，
	// detached 时为 commit sha，两者都能直接喂给 git checkout 复原。managed 模式
	// 恒为空（新工作树没有「之前」）。空串表示采集失败，补偿据此放弃复原而非乱切。
	PrevRef string
}
```

其二，新工作树分支（`case req.NewWorktree:` 内，`gitRun` 成功之后）把赋值改为：

```go
		ws = Workspace{Branch: branch, WorkDir: workDir, Managed: true}
		if !isExisting {
			ws.NewBranchTip = branchTip(ctx, req.Repo, branch)
		}
```

其三，用户树分支（`case req.Worktree != "":`）——`PrevRef` 必须在 `checkoutInWorktree` **之前**采：

```go
		prev := currentRef(ctx, req.Worktree)
		if err := checkoutInWorktree(ctx, req.Worktree, branch, req.Base, isExisting); err != nil {
			return Workspace{}, err
		}
		ws = Workspace{Branch: branch, WorkDir: req.Worktree, Managed: false, PrevRef: prev}
		if !isExisting {
			ws.NewBranchTip = branchTip(ctx, req.Repo, branch)
		}
```

其四，原地分支（`default:`）——同样在 `checkout` 之前采：

```go
		prev := currentRef(ctx, req.Repo)
		var args []string
		if isExisting {
			args = []string{"checkout", branch}
		} else {
			args = []string{"checkout", "-b", branch}
			if req.Base != "" {
				args = append(args, req.Base)
			}
		}
		if _, stderr, err := gitRun(ctx, req.Repo, args...); err != nil {
			return Workspace{}, fmt.Errorf("git %v: %s: %w", args, strings.TrimSpace(stderr), err)
		}
		ws = Workspace{Branch: branch, WorkDir: req.Repo, Managed: false, PrevRef: prev}
		if !isExisting {
			ws.NewBranchTip = branchTip(ctx, req.Repo, branch)
		}
```

其五，在 `worktreeBelongsToRepo` 之后追加两个小工具：

```go
// currentRef 取工作树当前 HEAD 的**可复原引用**：正常在分支上时返回分支名，
// detached 时返回 commit sha。两种形态都能直接喂给 git checkout。
//
// 参数：dir 为工作树路径（原地模式即主仓库）
//
// 返回：引用字符串；取不到时返回空串
//
// 注意：
//   - 返回空串**不是错误**，调用方按「不知道该切回哪儿」处置。采集失败不该
//     挡住派发，但也绝不能拿一个猜测值去 checkout——乱切比不切更糟
func currentRef(ctx context.Context, dir string) string {
	// -q 让 detached 时安静地非零退出，而不是往 stderr 刷错误
	if out, _, err := gitRun(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}
	out, _, err := gitRun(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		log().Warn("采集原 ref 失败，补偿将无法复原工作树", "dir", dir, "cause", err)
		return ""
	}
	return strings.TrimSpace(out)
}

// branchTip 取分支当前尖端 sha。
//
// 参数：repo 为主仓库路径，branch 为分支名
//
// 返回：40 位 sha；取不到时返回空串（调用方据此保守处置——补偿侧「取不到」
// 与「对不上」同样不删分支）
func branchTip(ctx context.Context, repo, branch string) string {
	out, _, err := gitRun(ctx, repo, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		log().Warn("取分支尖端失败", "repo", repo, "branch", branch, "cause", err)
		return ""
	}
	return strings.TrimSpace(out)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestPrepareWorkspace -v`
Expected: 新增三条与既有 `TestPrepareWorkspace*` 全 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "feat(workspace): 记录新建分支尖端与切走前的 HEAD，供补偿复原"
```

---

### Task 5: 补偿路径复原分支与工作树

**Files:**
- Modify: `internal/agentd/manager.go`（`compensateManagedWorktree` → `compensateWorkspace`，新增 `deleteCreatedBranch`，以及第 505 行 defer 内的调用点）
- Test: `internal/agentd/manager_test.go`

**Interfaces:**
- Consumes: `Workspace.NewBranchTip` / `Workspace.PrevRef`、`branchTip(ctx, repo, branch) string`（Task 4）、既有 `RemoveManagedWorktree(ctx, repo, workdir) error`、`gitRun`
- Produces: `func (m *Manager) compensateWorkspace(ctx context.Context, repo string, ws Workspace)`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/manager_test.go` 末尾追加。注入失败的手法照抄文件内既有的 `TestDispatchFailedAfterWorkspaceCleansManagedWorktree`：在 DataDir 下预置一个名为 `tasks` 的**文件**，让 `MkdirAll(DataDir/tasks/<id>)` 失败——精确命中「工作区已建、executor 未接管」的窗口：

```go
// compensateFixture 造一个「PrepareWorkspace 之后必失败」的 Manager：
// DataDir 下预置名为 tasks 的普通文件，使 MkdirAll(DataDir/tasks/<id>) 失败。
func compensateFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "tasks"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: dataDir, Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return m, dataDir
}

// branchExists 报告 repo 里是否存在名为 branch 的本地分支。
func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	c := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return c.Run() == nil
}

// TestCompensateDeletesCreatedBranch 验证 managed 模式补偿把**本次新建的**分支
// 一并删掉——这正是 B39 的原始诉求：修好环境后用同一分支名重试必须能成功。
func TestCompensateDeletesCreatedBranch(t *testing.T) {
	repo := initTestRepo(t)
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, NewBranch: "e2e/retry",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if branchExists(t, repo, "e2e/retry") {
		t.Fatal("本次新建的分支应被补偿删除，否则同名重试会撞 already exists")
	}
}

// TestCompensateKeepsExistingBranch 验证 --branch <已存在分支> 模式下补偿
// **不删**分支。判别力：一个无脑删分支的实现会在这里删掉用户自己的分支。
func TestCompensateKeepsExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "mine")
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, Branch: "mine",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if !branchExists(t, repo, "mine") {
		t.Fatal("已存在分支不是本次新建的，补偿绝不能删")
	}
}

// TestCompensateInPlaceRestoresPrevRef 验证原地模式（当前缺省）补偿把主仓
// 切回原分支并删掉新建分支。
//
// 判别力：这是 brainstorm 中扩大范围的那一半——旧实现 `if !ws.Managed { return }`
// 在这里直接早退，主仓会停在空分支上。
func TestCompensateInPlaceRestoresPrevRef(t *testing.T) {
	repo := initTestRepo(t)
	before := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewBranch: "e2e/inplace",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	after := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if after != before {
		t.Fatalf("原地模式补偿应把主仓切回 %s，实际停在 %s", before, after)
	}
	if branchExists(t, repo, "e2e/inplace") {
		t.Fatal("原地模式下本次新建的分支同样应被删除")
	}
}

// TestCompensateInPlaceRestoresDetached 验证 detached HEAD 起步时补偿切回原 commit。
func TestCompensateInPlaceRestoresDetached(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "--detach", "-q", head)
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewBranch: "e2e/detached",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if got := gitOut(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("detached 起步应切回 %s，实际 %s", head, got)
	}
	if branchExists(t, repo, "e2e/detached") {
		t.Fatal("新建分支应被删除")
	}
}
```

再追加三条**白盒**用例：它们要构造的失败点（worktree 删不掉、分支尖端被动过）无法从
`Dispatch` 外部注入，直接驱动 `compensateWorkspace` 才是确定性的做法（本文件已是
`package agentd`，白盒驱动与既有 `TestTransitToReview*` 同一路子）：

```go
// compensateOnlyManager 造一个只用来调 compensateWorkspace 的 Manager——
// 这三条用例不经过 Dispatch，store/hub 只需能构造出来。
func compensateOnlyManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	return NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestCompensateKeepsBranchWhenWorktreeRemoveFails 验证 worktree 删不掉时
// **绝不删分支**：分支还被那棵树 checkout 着，且失败现场要留给人排查。
//
// 注入方式：给一个根本没在 git 里注册过的 WorkDir，worktree remove 必然失败。
func TestCompensateKeepsBranchWhenWorktreeRemoveFails(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "e2e/stuck")
	tip := gitOut(t, repo, "rev-parse", "refs/heads/e2e/stuck")
	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, Workspace{
		Branch: "e2e/stuck", WorkDir: filepath.Join(t.TempDir(), "not-a-worktree"),
		Managed: true, NewBranchTip: tip,
	})
	if !branchExists(t, repo, "e2e/stuck") {
		t.Fatal("worktree 删除失败时分支必须保留")
	}
}

// TestCompensateKeepsBranchWhenTipMoved 验证分支尖端与创建时不符（疑似已有
// 提交）时保留分支。删分支不可逆，宁可留残留也不能删错。
func TestCompensateKeepsBranchWhenTipMoved(t *testing.T) {
	repo := initTestRepo(t)
	orig := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	staleTip := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "-q", "-b", "e2e/moved")
	writeAndCommit(t, repo, "extra.txt", "x\n") // 尖端前移，与 staleTip 不再相等
	gitT(t, repo, "checkout", "-q", orig)
	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, Workspace{
		Branch: "e2e/moved", WorkDir: repo, Managed: false,
		NewBranchTip: staleTip, PrevRef: orig,
	})
	if !branchExists(t, repo, "e2e/moved") {
		t.Fatal("尖端与创建时不符的分支必须保留，日志记 WARN 即可")
	}
}

// TestCompensateUserWorktreeRestores 验证用户自带 worktree 模式：树切回原分支、
// 新建分支删掉。这是 spec §6 表里第六行，也是「非 managed 不止原地一种」的证据。
func TestCompensateUserWorktreeRestores(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "userwt")
	gitT(t, repo, "worktree", "add", "-q", "-b", "userbase", wt)

	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "eeeeeeee-0000-0000-0000-000000000000",
		NewBranch: "e2e/userwt", Worktree: wt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != "userbase" {
		t.Fatalf("用户树的 PrevRef 应为 userbase，得到 %q", ws.PrevRef)
	}

	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, ws)

	if got := gitOut(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "userbase" {
		t.Fatalf("用户树应被切回 userbase，实际停在 %s", got)
	}
	if branchExists(t, repo, "e2e/userwt") {
		t.Fatal("用户树模式下本次新建的分支同样应被删除")
	}
}
```

`manager_test.go` 已 import `strings`/`os`/`filepath`/`io`/`log/slog`/`context`；本任务需补 `os/exec`（`branchExists` 用）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestCompensate -v`
Expected：
- FAIL：`TestCompensateDeletesCreatedBranch`、`TestCompensateInPlaceRestoresPrevRef`、`TestCompensateInPlaceRestoresDetached`、`TestCompensateUserWorktreeRestores`（分支仍在 / 树停在新分支上）
- 编译期 FAIL 也可接受（`compensateWorkspace` 尚未存在）——先按编译错误落地函数签名再看断言
- PASS：`TestCompensateKeepsExistingBranch`、`TestCompensateKeepsBranchWhenWorktreeRemoveFails`、`TestCompensateKeepsBranchWhenTipMoved`（旧实现本来就不删分支，这三条是防回归锚，绿→绿）

- [ ] **Step 3: 写最小实现**

`internal/agentd/manager.go` 两处改动。

其一，defer 内的调用点（原第 505 行）改名：

```go
	defer func() {
		if err != nil && !executorStarted {
			m.compensateWorkspace(ctx, req.Repo, ws)
		}
	}()
```

其二，把 `compensateManagedWorktree` 整个替换为：

```go
// compensateWorkspace 在 dispatch 后续步骤失败时复原已准备好的工作区。
//
// why：PrepareWorkspace 成功意味着磁盘上已经有了工作树、且分支已经建好/切好。
// 若随后 executor 接管前的任何步骤失败（MkdirAll/WriteFile/CreateTask/
// SetTaskField/adapter.Start），任务要么没落库、要么落 failed——两者都没有 done
// 清理路径（done 只认 waiting_review），痕迹会永久留在用户的仓库里：managed
// 模式留下孤儿 worktree 与挡路的空分支（同名重试直接撞 already exists，B39），
// 非 managed 模式更直接——用户的工作树就停在那个空分支上。
//
// 参数：
//   - ctx: 控制补偿期间的 git 调用
//   - repo: 主仓库路径
//   - ws: PrepareWorkspace 的产出
//
// 注意：
//   - 由 Dispatch 的 defer 统一调用，且只在 executor 未接管时（见 executorStarted
//     注释）；executor 接管后删工作树是把运行中的任务脚下抽空
//   - 全程只记日志，**不覆盖也不替换原始派发错误**——审核者要看的是任务为什么
//     没派出去，补偿成败是次要信息
//   - 三条 fail-safe：worktree 删除失败 / 切回原 ref 失败 / 分支尖端对不上，
//     任一命中都保留现场不再往下做。宁可留残留，不可误删
func (m *Manager) compensateWorkspace(ctx context.Context, repo string, ws Workspace) {
	// 空值守卫：现有调用点把 defer 注册在 PrepareWorkspace 成功之后，理论上到不了
	// 这里，但补偿函数本身不该依赖调用点的注册位置
	if ws.WorkDir == "" {
		return
	}
	m.log.Warn("dispatch 后续失败，补偿复原工作区", "repo", repo, "workdir", ws.WorkDir,
		"managed", ws.Managed, "branch", ws.Branch, "prev_ref", ws.PrevRef)

	if ws.Managed {
		if err := RemoveManagedWorktree(ctx, repo, ws.WorkDir); err != nil {
			// 工作树还在，分支被它 checkout 着，git 也会拒绝删除；且失败现场要留给人排查
			m.log.Error("补偿清理 managed worktree 失败，保留分支待查",
				"repo", repo, "workdir", ws.WorkDir, "branch", ws.Branch, "cause", err)
			return
		}
	} else {
		if ws.PrevRef == "" {
			m.log.Warn("补偿无法复原：未记录原 ref，工作树仍停在任务分支上",
				"workdir", ws.WorkDir, "branch", ws.Branch,
				"manual", "git -C "+ws.WorkDir+" checkout <你原来的分支>")
			return
		}
		if _, stderr, err := gitRun(ctx, ws.WorkDir, "checkout", ws.PrevRef); err != nil {
			m.log.Error("补偿切回原 ref 失败，工作树仍停在任务分支上",
				"workdir", ws.WorkDir, "prev_ref", ws.PrevRef,
				"stderr", truncateRunes(stderr, 300), "cause", err)
			return
		}
		m.log.Info("补偿已切回原 ref", "workdir", ws.WorkDir, "prev_ref", ws.PrevRef)
	}
	m.deleteCreatedBranch(ctx, repo, ws)
}

// deleteCreatedBranch 删除本次 dispatch 新建的分支（补偿路径专用）。
//
// why 这件事不放进 RemoveManagedWorktree：那个函数服务的是 Done/Stop 归档场景，
// 「只删工作树不删分支」在那里完全正确——分支上是任务成果。补偿场景的要求正好
// 相反：分支是几秒前刚建的，executorStarted 守卫保证零提交，留着只会挡路。
// 同一个函数满足不了相反的两组要求，所以在补偿侧单独承担。
//
// 参数：ctx 控制 git 调用；repo 主仓库路径；ws 为 PrepareWorkspace 的产出
//
// 注意：
//   - 调用前必须已经确保分支不再被任何工作树 checkout（managed 已删树 /
//     非 managed 已切回原 ref），否则 git 会拒绝
//   - NewBranchTip 为空 = 分支不是本次新建的，是用户的东西，一律不动
func (m *Manager) deleteCreatedBranch(ctx context.Context, repo string, ws Workspace) {
	if ws.NewBranchTip == "" {
		return
	}
	m.log.Info("补偿删除本次新建的分支", "repo", repo, "branch", ws.Branch, "tip", ws.NewBranchTip)
	// 复核尖端：executorStarted 守卫理论上保证零提交，但删分支不可逆，
	// 宁可留个残留也不能删错。branchTip 取不到时返回空串，同样落进这一支
	if cur := branchTip(ctx, repo, ws.Branch); cur != ws.NewBranchTip {
		m.log.Warn("分支尖端与创建时不符，疑似已有提交，保留待查",
			"branch", ws.Branch, "expect", ws.NewBranchTip, "actual", cur)
		return
	}
	// 用 -D 而非 -d：分支起点可能领先仓库当前 HEAD，-d 会因「未合并」误拒；
	// 而「自创建以来零提交」已由上一步实证，-D 在这里是确定性而非暴力
	if _, stderr, err := gitRun(ctx, repo, "branch", "-D", ws.Branch); err != nil {
		m.log.Error("补偿删除分支失败", "repo", repo, "branch", ws.Branch,
			"stderr", truncateRunes(stderr, 300), "cause", err)
		return
	}
	m.log.Info("补偿删除分支完成", "repo", repo, "branch", ws.Branch)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestCompensate -v`
Expected: 七条全 PASS（对应 spec §6 的七个补偿场景）

- [ ] **Step 5: 跑全包回归**

Run: `go test ./internal/agentd/ -count=1`
Expected: 全 PASS，尤其既有的 `TestDispatchFailedAfterWorkspaceCleansManagedWorktree` 与 `TestDispatchStartFailureCleansManagedWorktree` 仍绿

- [ ] **Step 6: 提交**

```bash
git add internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "fix(dispatch): 补偿删除本次新建的分支并把非 managed 工作树切回原处"
```

---

### Task 6: 文档同步与全量验收

**Files:**
- Modify: `README.md`（dispatch 的 flag 表一行）
- Modify: `skills/handoff/SKILL.md`（排障表一行）

**Interfaces:**
- Consumes: `--allow-dirty`（Task 3）
- Produces: 无代码接口

- [ ] **Step 1: 更新 README 的 flag 表**

`README.md` 第 85 行 dispatch 那一行，在 `` `--no-sync-check`（远程派发时跳过基线校验） `` 之后追加：

```
；`--allow-dirty`（本地工作区有未提交的已跟踪改动时仍照常派发）
```

- [ ] **Step 2: 更新 SKILL.md 排障表**

`skills/handoff/SKILL.md` 第 271 行那条「`dispatch` 报「工作区不干净」」之后，新增一行（注意与既有那条区分：一条是**执行机上**的仓库，新增这条是**你本地**的仓库）：

```markdown
| `dispatch` 报「本地工作区有 N 处未提交的已跟踪改动」 | **你本地**（不是执行机）有改动没提交，远程派发的基线不含它们，executor 会基于旧代码开工 | `git commit` 或 `git stash` 后重试；确认这些改动与本次任务无关时加 `--allow-dirty`（放行仍会打印被忽略的文件） |
```

- [ ] **Step 3: 全量验收**

依次运行，四条都必须干净：

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```

Expected: build/vet 无输出；`gofmt -l .` 只剩既有的 `internal/executor/grok/askquestion_internal_test.go`（历史遗留，非本次引入），无新增；`go test` 全绿（20 包）

- [ ] **Step 4: Windows 交叉编译门禁**

```bash
GOOS=windows GOARCH=amd64 go build ./...
```

Expected: 无输出（B36 立的门禁，本次改动不得破坏）

- [ ] **Step 5: 竞态复核**

```bash
go test -race ./cmd/ ./internal/agentd/ -count=1
```

Expected: 全绿，无 race 报告

- [ ] **Step 6: 提交**

```bash
git add README.md skills/handoff/SKILL.md
git commit -m "docs: --allow-dirty 进 flag 表，本地脏被拒进排障表"
```

---

## 真机验收（合并前必做）

代码门禁全绿**不等于**修好了。以下两条在 devbox 上实测，结果写回 backlog B29/B39 的验收列：

1. **B29 闭环**：本地故意改一个已跟踪文件不提交 → `handoff --target devbox dispatch ...` 被拒，错误里列出该文件名；加 `--allow-dirty` 后放行，且 stderr 有列出该文件的警告；提交后再派发无任何提示。
2. **B39 闭环**：让 agentd 在 PrepareWorkspace 之后失败（把 opencode 从 agentd 的 PATH 里摘掉即可，这正是 B39 原始现场的成因），`dispatch --new-branch e2e/b39 --new-worktree` → 确认 worktree 与分支**都没了**，随后**用同一分支名 `e2e/b39` 重试并成功**。最后这一步是 B39 的原始诉求，也是唯一能证明修复有效的断言——只看日志说「已删除」不算。

## 回归锚

Task 5 的 `TestCompensateInPlaceRestoresPrevRef` 必须抄回旧实现（把 `compensateWorkspace` 换回 `if !ws.Managed { return }` 的早退版本）单独复跑一次，确认**变红**。红→绿证据写入 backlog 验收列，不采信执行者自述。
