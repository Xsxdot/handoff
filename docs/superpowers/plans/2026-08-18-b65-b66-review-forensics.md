# B65 + B66 审阅取证修复 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff diff` 缺省用任务自己的起点做基准并回显实际基准，让 `handoff run` 的参数原样到达远端不丢引号。

**Architecture:** 两条独立修复共用一条主线——「取证结果必须自带出处」。B65 把 `Task.BaseCommit` 接进 diff/branches 两个接口的缺省基准解析（新函数 `ResolveDiffBase`），并把实际基准与来源经响应回传，CLI 打到 stderr、控制台标在下拉标签上；B66 在客户端逐 token 做 POSIX shell 转义再拼命令行，服务端与协议一字不改。

**Tech Stack:** Go 1.x（`net/http` + `log/slog` + `os/exec`）、cobra、SQLite（`internal/store`）、React + TypeScript + vitest（`web/`）。

**Spec:** [docs/superpowers/specs/2026-08-18-b65-b66-review-forensics-design.md](../specs/2026-08-18-b65-b66-review-forensics-design.md)

## Global Constraints

每个 task 的要求都隐含包含本节全部条款。

1. **服务端 `sh -c` 语义与 `RunCmd` 一行不改。** 进程组回收、10min 超时、输出上限、`runShell()` 解析全部保持原样。B66 的修复完全在客户端。
2. **协议只增字段，不改已有字段的类型、不删字段。** `POST /api/tasks/{id}/run` 的请求体保持 `{"cmd": "<string>"}`。
3. **日志用 `log()`（agentd 包内自由函数）或 `s.log`（`*Server` 方法内），禁止 `fmt.Printf` 作为日志手段。** 注意区分：`cmd/` 层的 `fmt.Fprintln(cmd.OutOrStdout()/ErrOrStderr(), ...)` 是**程序输出**，是既有契约的一部分，**不得**改写成 logger 调用。
4. **注释一律中文。** 新文件必须有文件头注释（职责 + 边界）；导出函数/类型必须有 doc 注释写明参数、返回、注意事项；复杂分支写「为什么」而不是「做了什么」。
5. **`handoff diff` 的 stdout 只能是 diff 正文**，一个字节不多。协调者会把它接进管道分析。所有附加信息走 stderr。
6. **不修改 `~/.claude/skills/handoff/SKILL.md`**——那是用户机器上的私有文件，不在本仓库内，任何情况下不得写入。
7. **不动 `GET /api/projects/{name}/branches`**（另一个同名接口，服务于新建工作树对话框，与任务基准无关）。
8. **每个 task 完成后跑 `gofmt -l .`，必须无输出。** 前端 task 另跑 `npm run lint`（在 `web/` 下）。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/proto/proto.go` | 修改 | 新增 `DiffBaseSource` 类型与三个常量——它们是 HTTP 契约的词汇表，与 `TaskState`/`EventType` 同级同处 |
| `internal/agentd/workspace.go` | 修改 | 新增 `ResolveDiffBase`；订正 `resolveBaseBranch` 已过时的注释 |
| `internal/agentd/workspace_diffbase_test.go` | 新建 | `ResolveDiffBase` 的单元测试（四条路径） |
| `internal/agentd/server.go` | 修改 | 新增 `taskAndRepoOrErr`；`handleTaskDiff`/`handleTaskBranches` 接线并回传来源 |
| `internal/agentd/server_diffbase_test.go` | 新建 | diff/branches 两个接口的端到端断言 |
| `internal/client/client.go` | 修改 | `Diff` 返回值 `string` → `DiffResult` |
| `cmd/diff.go` | 修改 | stderr 基准回显 |
| `cmd/diff_test.go` | 新建 | 回显文案的四态测试 |
| `cmd/shellquote.go` | 新建 | POSIX shell 转义纯函数（`shellQuote` / `shellQuoteJoin`） |
| `cmd/shellquote_test.go` | 新建 | 表格测试 + 真起 `sh` 的往返测试 |
| `cmd/run.go` | 修改 | 用 `shellQuoteJoin` 替换 `strings.Join` |
| `web/src/api/types.ts` | 修改 | `DiffResult`/`BranchesResult` 加可选字段 |
| `web/src/app/task/ReviewSidePanel.tsx` | 修改 | 「自动推导」标签按来源渲染 |
| `web/src/app/task/ReviewSidePanel.test.tsx` | 修改 | 补三态标签测试 |
| `README.md` / `README.zh-CN.md` | 修改 | diff / run 两行的说明订正 |

---

### Task 1: `ResolveDiffBase` —— diff 缺省基准的解析函数

**Files:**
- Modify: `internal/proto/proto.go`（在 `TaskState` 常量组之后新增一组）
- Modify: `internal/agentd/workspace.go`（`resolveBaseBranch` 之前插入，约 `:1021`）
- Test: `internal/agentd/workspace_diffbase_test.go`（新建）

**Interfaces:**
- Produces：`proto.DiffBaseSource` 类型与 `proto.DiffBaseExplicit` / `proto.DiffBaseTaskStart` / `proto.DiffBaseRepoDefault` 三个常量；`agentd.ResolveDiffBase(repo, baseCommit string) (base string, source proto.DiffBaseSource)`。Task 2/3/5 都依赖这几个名字。
- Consumes：既有的 `gitRun(ctx, repo, args...) (stdout, stderr string, err error)`、`resolveBaseBranch(repo string) string`、`log() *slog.Logger`。

- [ ] **Step 1: 在 `internal/proto/proto.go` 加契约词汇表**

放在 `TaskState` 常量组之后（该文件的既有惯例是「类型 + 常量组 + doc 注释」）：

```go
// DiffBaseSource 标识 diff 基准的来源，随 diff / branches 两个接口的响应回传。
//
// 为什么来源要上到线上：真正咬人的不是「基准取错了」，而是「静默取错了」——
// 2026-08-18 审 B129 交付时 diff 缺省吐出一堆无关文件，而输出里没有任何线索
// 指向用了哪个基准，协调者只能靠猜、靠试。把来源一并回传，每次取证就自带出处。
type DiffBaseSource string

const (
	// DiffBaseExplicit：调用方通过 ?base= 显式指定（CLI 的 --base、控制台下拉选定项）。
	DiffBaseExplicit DiffBaseSource = "explicit"
	// DiffBaseTaskStart：任务分支的实际起点，即 Task.BaseCommit。
	DiffBaseTaskStart DiffBaseSource = "task_base"
	// DiffBaseRepoDefault：仓库默认分支推导（origin/HEAD → main → master）。
	DiffBaseRepoDefault DiffBaseSource = "repo_default"
)
```

- [ ] **Step 2: 写失败的测试**

新建 `internal/agentd/workspace_diffbase_test.go`：

```go
// workspace_diffbase_test.go —— ResolveDiffBase（B65）的解析顺序测试。
//
// 全部用例在 t.TempDir() 里 git init 造真实仓库（复用 workspace_test.go 的
// initGitRepo/gitAt），不触碰真实工作区。
package agentd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 有可达的任务起点时必须用它，而不是仓库默认分支——这正是 B65 的缺陷本体。
func TestResolveDiffBasePrefersTaskStart(t *testing.T) {
	repo := initGitRepo(t)
	head := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))

	base, source := ResolveDiffBase(repo, head)

	if base != head {
		t.Errorf("基准应为任务起点 %s，实得 %q", head, base)
	}
	if source != proto.DiffBaseTaskStart {
		t.Errorf("来源应为 %q，实得 %q", proto.DiffBaseTaskStart, source)
	}
}

// 起点在仓库里不可达（分支删了并 gc、仓库被重 clone）时回落仓库默认分支，
// 而不是把一个取不到的 rev 交给 git diff 去抛错。
func TestResolveDiffBaseFallsBackWhenStartUnreachable(t *testing.T) {
	repo := initGitRepo(t)
	const ghost = "0123456789abcdef0123456789abcdef01234567"

	base, source := ResolveDiffBase(repo, ghost)

	if base != "main" {
		t.Errorf("应回落仓库默认分支 main，实得 %q", base)
	}
	if source != proto.DiffBaseRepoDefault {
		t.Errorf("来源应为 %q，实得 %q", proto.DiffBaseRepoDefault, source)
	}
}

// BaseCommit 为空是正常情况（切已存在分支、或该列后加的老任务），
// 静默回落，不是错误路径。
func TestResolveDiffBaseFallsBackWhenEmpty(t *testing.T) {
	repo := initGitRepo(t)

	base, source := ResolveDiffBase(repo, "")

	if base != "main" || source != proto.DiffBaseRepoDefault {
		t.Fatalf("空起点应回落 main/repo_default，实得 %q/%q", base, source)
	}
}

// 形如 git 选项的起点一律不采用：`--output=/tmp/x` 会让 git 把 diff 写到任意
// 路径（参数注入）。与 Diff() 里既有的 "-" 前缀守卫同款，纵深防御。
func TestResolveDiffBaseRejectsDashPrefix(t *testing.T) {
	repo := initGitRepo(t)

	base, source := ResolveDiffBase(repo, "--output=/tmp/pwned")

	if base != "main" || source != proto.DiffBaseRepoDefault {
		t.Fatalf("- 前缀起点必须被拒并回落，实得 %q/%q", base, source)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestResolveDiffBase -count=1`
Expected: FAIL，`undefined: ResolveDiffBase`

- [ ] **Step 4: 实现 `ResolveDiffBase`**

插在 `internal/agentd/workspace.go` 的 `resolveBaseBranch` 定义**之前**：

```go
// ResolveDiffBase 确定 diff 的缺省基准（调用方未用 ?base= 显式指定时）。
//
// 参数：
//   - repo: 任务工作区目录
//   - baseCommit: 任务记录里的 Task.BaseCommit（40 位 sha），可为空
//
// 返回：
//   - base: 可直接交给 Diff() 的 rev；空串表示推导不出，由路由层报 400
//   - source: 该基准的来源，随响应回传给协调者；base 为空时 source 也为空
//
// 注意：
//   - baseCommit 为空是**正常情况**（切已存在分支时没有「起点」这回事，且该列
//     是 B35 之后才加的、老任务不回填），静默回落，不告警
//   - baseCommit 非空但在仓库里不可达时告警并回落，而不是把取不到的 rev 交给
//     git diff——那会变成一个没有归因的 500
func ResolveDiffBase(repo, baseCommit string) (base string, source proto.DiffBaseSource) {
	switch {
	case baseCommit == "":
		// 正常情况，不告警：多数任务切的是已存在分支，本来就没有起点
	case strings.HasPrefix(baseCommit, "-"):
		// 正常路径下 BaseCommit 恒为 40 位十六进制，走到这里说明数据被污染过；
		// 采用它会让 git 把它当选项解释（参数注入），一律拒绝并落到兜底链
		log().Warn("任务起点形如 git 选项，拒绝作为 diff 基准", "repo", repo, "base_commit", baseCommit)
	default:
		// ^{commit} 保证验的是「存在且是一个提交」而不是恰好同名的其他对象；
		// --quiet 让不存在时只用退出码表达，不往 stderr 喷噪音
		if _, _, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", baseCommit+"^{commit}"); err == nil {
			log().Info("diff 基准取任务起点", "repo", repo, "base", baseCommit)
			return baseCommit, proto.DiffBaseTaskStart
		}
		log().Warn("任务起点在仓库中不可达，回落仓库默认分支", "repo", repo, "base_commit", baseCommit)
	}
	if b := resolveBaseBranch(repo); b != "" {
		log().Info("diff 基准取仓库默认分支", "repo", repo, "base", b)
		return b, proto.DiffBaseRepoDefault
	}
	log().Warn("diff 基准推导不出，需要调用方显式指定", "repo", repo)
	return "", ""
}
```

确认 `internal/agentd/workspace.go` 的 import 块已含 `context`、`strings` 与 `github.com/Xsxdot/handoff/internal/proto`；缺哪个补哪个。

- [ ] **Step 5: 订正 `resolveBaseBranch` 的过时注释**

该函数的注释里现在写着「派发时并未记录基准分支名，diff 必须从仓库自身推导出合理默认」——B35 之后 `Task.BaseCommit` 已经记录了起点，这句话已经不成立且会误导下一个读者。改成：

```go
// 为什么保留这条兜底链：任务未必有起点（切已存在分支时没有「起点」这回事），
// 老任务的 BaseCommit 也不回填。ResolveDiffBase 优先用起点，取不到才落到这里。
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestResolveDiffBase -count=1 -v`
Expected: 四条全 PASS

- [ ] **Step 7: 加关键节点日志（自检）**

本 task 的日志已内联在 Step 4，逐条确认都在：
- 取到任务起点 → Info（成功路径不静默）
- 起点形如 git 选项被拒 → Warn 带 `base_commit`
- 起点不可达回落 → Warn 带 `repo` + `base_commit`
- 落到仓库默认分支 → Info 带实际 base
- 彻底推导不出 → Warn
- 用 `log()` 而非 `fmt.Printf`

- [ ] **Step 8: 加注释（自检）**

- `proto.DiffBaseSource` 类型与三个常量各有 doc 注释，类型注释写清「为什么来源要上到线上」
- `ResolveDiffBase` 的 doc 注释含参数、返回、两条注意事项
- 新建测试文件有文件头注释（职责 + 边界）
- `-` 前缀分支与 `^{commit}` 两处有解释「为什么」的行内注释

- [ ] **Step 9: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./internal/proto/ ./internal/agentd/ -count=1`
Expected: `gofmt -l .` 无输出；测试全 ok。若 `internal/proto` 的契约 fixture 测试变红，说明新类型进了 fixture 表——按该测试自己的提示用 `-update` 显式重写并把改动一并提交。

- [ ] **Step 10: Commit**

```bash
git add internal/proto/proto.go internal/agentd/workspace.go internal/agentd/workspace_diffbase_test.go
git commit -m "feat(agentd): ResolveDiffBase——diff 缺省基准优先取任务起点（B65 §2.2）"
```

---

### Task 2: diff 与 branches 两个接口改用任务起点并回传来源

**Files:**
- Modify: `internal/agentd/server.go`（`taskRepoOrErr` `:1342`、`handleTaskDiff` `:1368`、`handleTaskBranches` `:1409`）
- Test: `internal/agentd/server_diffbase_test.go`（新建）

**Interfaces:**
- Consumes：Task 1 的 `ResolveDiffBase` 与 `proto.DiffBase*` 常量。
- Produces：`GET /api/tasks/{id}/diff` 响应 `{"diff","base","base_source"}`；`GET /api/tasks/{id}/branches` 响应 `{"branches","default","default_source"}`。Task 3（CLI）与 Task 5（前端）都消费这两个形状。

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/server_diffbase_test.go`（`package agentd`——要调用未导出的
handler 与 `initGitRepo`/`gitAt`）。**下列脚手架的每个名字都已在仓库里核实过**：
`newTestAgentdEnvWithCfg(t, *config.Config, *slog.Logger) *testAgentdEnv`，其字段为
`srv *Server` / `st *store.Store`；`testToken` 常量定义在 `w3a_testhelpers_test.go`
（`package agentd`，与本文件同包）；`initGitRepo(t)` 造 main 分支 + 一个初始提交，
`gitAt(t, dir, args...) string` 返回命令输出。

> **store 没有 `UpdateTask` 这类整体更新方法**（只有 `UpdateTaskState` /
> `SetTaskField` / `SetTaskUsage`）。因此工作区路径必须在 `CreateTask` 那一刻就
> 设成真实仓库，不要试图事后改任务——**不得为了贴合测试而给 store 新增方法**。

```go
// server_diffbase_test.go —— diff / branches 两个接口的缺省基准与来源回传（B65）。
//
// 边界：只断言基准解析与响应形状，不验证 diff 正文内容（那是 Diff() 自己的测试）。
package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newDiffBaseEnv 造一个「工作区是真实 git 仓库、任务带 BaseCommit」的服务端。
//
// 参数 withStart=false 即模拟「任务没有起点记录」（老任务 / 切已存在分支）。
// 返回：服务端、任务 id、仓库路径、该仓库的 HEAD 提交号。
func newDiffBaseEnv(t *testing.T, withStart bool) (srv *Server, taskID, repo, head string) {
	t.Helper()
	repo = initGitRepo(t)
	head = strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	env := newTestAgentdEnvWithCfg(t,
		&config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID = "t-diffbase"
	base := ""
	if withStart {
		base = head
	}
	// RepoPath 必须在建任务时就落成真实仓库：store 没有整体更新任务的方法
	if err := env.st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: repo, BaseCommit: base,
		State: proto.TaskStateWaitingReview,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return env.srv, taskID, repo, head
}

// getJSON 发一次 GET 到指定 handler 并解析响应体。
func getJSON(t *testing.T, h http.HandlerFunc, taskID, query string, out any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/x"+query, nil)
	req.SetPathValue("id", taskID)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("解析响应 %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code
}

// 不带 base 查询参数时必须用任务起点——B65 的端到端判据。
func TestTaskDiffDefaultsToTaskStart(t *testing.T) {
	srv, taskID, _, head := newDiffBaseEnv(t, true)

	var out struct {
		Base       string               `json:"base"`
		BaseSource proto.DiffBaseSource `json:"base_source"`
	}
	if code := getJSON(t, srv.handleTaskDiff, taskID, "", &out); code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", code)
	}
	if out.Base != head {
		t.Errorf("基准应为任务起点 %s，实得 %q", head, out.Base)
	}
	if out.BaseSource != proto.DiffBaseTaskStart {
		t.Errorf("来源应为 %q，实得 %q", proto.DiffBaseTaskStart, out.BaseSource)
	}
}

// 任务没有起点记录（老任务/切已存在分支）时回落仓库默认分支并如实标注。
func TestTaskDiffFallsBackWhenNoStart(t *testing.T) {
	srv, taskID, _, _ := newDiffBaseEnv(t, false)

	var out struct {
		Base       string               `json:"base"`
		BaseSource proto.DiffBaseSource `json:"base_source"`
	}
	if code := getJSON(t, srv.handleTaskDiff, taskID, "", &out); code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", code)
	}
	if out.Base != "main" || out.BaseSource != proto.DiffBaseRepoDefault {
		t.Fatalf("无起点应回落 main/repo_default，实得 %q/%q", out.Base, out.BaseSource)
	}
}

// 显式给了 base 就用它，且来源标为 explicit——协调者要能区分「我指定的」
// 和「系统替我推的」。
func TestTaskDiffExplicitBaseMarkedExplicit(t *testing.T) {
	srv, taskID, _, _ := newDiffBaseEnv(t, true)

	var out struct {
		Base       string               `json:"base"`
		BaseSource proto.DiffBaseSource `json:"base_source"`
	}
	if code := getJSON(t, srv.handleTaskDiff, taskID, "?base=main", &out); code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", code)
	}
	if out.Base != "main" || out.BaseSource != proto.DiffBaseExplicit {
		t.Fatalf("显式 base 应原样保留并标 explicit，实得 %q/%q", out.Base, out.BaseSource)
	}
}

// branches 的 default 必须与 diff 的缺省基准同源——两边分叉正是本次要消灭的东西。
func TestTaskBranchesDefaultMatchesDiffBase(t *testing.T) {
	srv, taskID, _, head := newDiffBaseEnv(t, true)

	var out struct {
		Default       string               `json:"default"`
		DefaultSource proto.DiffBaseSource `json:"default_source"`
	}
	if code := getJSON(t, srv.handleTaskBranches, taskID, "", &out); code != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d", code)
	}
	if out.Default != head || out.DefaultSource != proto.DiffBaseTaskStart {
		t.Fatalf("branches 的 default 应与 diff 同源（%s/task_base），实得 %q/%q",
			head, out.Default, out.DefaultSource)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestTaskDiff|TestTaskBranches' -count=1`
Expected: FAIL（`base_source` 字段不存在，断言落空）

- [ ] **Step 3: 新增 `taskAndRepoOrErr`，把 `taskRepoOrErr` 降级为瘦包装**

替换 `internal/agentd/server.go:1342` 处的 `taskRepoOrErr`：

```go
// taskAndRepoOrErr 读取路径中的任务，返回任务体与其工作区目录。
//
// 任务不存在（404）或没有工作区路径（400）时响应已写好，ok=false，调用方直接 return。
//
// 为什么要把任务体也返回：diff 与 branches 的缺省基准要用 Task.BaseCommit（B65）。
// 此前 taskRepoOrErr 把读出来的任务直接丢掉，这两处只能退回按仓库默认分支猜，
// 于是 diff 缺省吐出的是「任务分支相对 main」而非「任务分支相对自己的起点」。
func (s *Server) taskAndRepoOrErr(w http.ResponseWriter, taskID string) (*proto.Task, string, bool) {
	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		} else {
			s.log.Error("读取任务失败", "task", taskID, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		}
		return nil, "", false
	}
	workdir := task.Workdir()
	if workdir == "" {
		s.log.Warn("任务缺少工作区路径", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务没有工作区路径"})
		return nil, "", false
	}
	return task, workdir, true
}

// taskRepoOrErr 是 taskAndRepoOrErr 的瘦包装，供不需要任务体的调用点使用
//（fetch / run）。行为与错误响应完全一致。
func (s *Server) taskRepoOrErr(w http.ResponseWriter, taskID string) (repo string, ok bool) {
	_, repo, ok = s.taskAndRepoOrErr(w, taskID)
	return repo, ok
}
```

`file`（`:1431`）与 `run`（`:1492`）两个调用点**保持不变**，继续用 `taskRepoOrErr`。

- [ ] **Step 4: `handleTaskDiff` 接线并回传来源**

```go
	task, repo, ok := s.taskAndRepoOrErr(w, taskID)
	if !ok {
		return
	}
	base := r.URL.Query().Get("base")
	source := proto.DiffBaseExplicit
	if base == "" {
		base, source = ResolveDiffBase(repo, task.BaseCommit)
	}
	if base == "" {
		s.log.Warn("无法确定基准分支", "task", taskID, "repo", repo)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法确定基准分支，请用 base 参数指定"})
		return
	}
```

`Diff(repo, base)` 及其两个错误分支保持原样。成功分支替换为：

```go
	s.log.Info("diff 完成", "task", taskID, "base", base, "base_source", source, "bytes", len(diff))
	writeJSON(w, http.StatusOK, map[string]string{
		"diff": diff, "base": base, "base_source": string(source),
	})
```

同步订正该函数的 doc 注释里那句「缺省时按仓库默认分支推导（resolveBaseBranch）」，改成描述四级顺序。

- [ ] **Step 5: `handleTaskBranches` 接线**

`repo, ok := s.taskRepoOrErr(...)` 改为 `task, repo, ok := s.taskAndRepoOrErr(...)`，末尾两行替换为：

```go
	def, source := ResolveDiffBase(repo, task.BaseCommit)
	s.log.Info("branches 完成", "task", taskID, "count", len(branches),
		"default", def, "default_source", source)
	writeJSON(w, http.StatusOK, map[string]any{
		"branches": branches, "default": def, "default_source": string(source),
	})
```

并订正该函数 doc 注释里「default 为空表示推导不出」那句——现在 `default` 可能是 sha 而非分支名，要写明这一点。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestTaskDiff|TestTaskBranches' -count=1 -v`
Expected: 三条全 PASS

- [ ] **Step 7: 加关键节点日志（自检）**

- `diff 完成` Info 带 `base` + `base_source` + 字节数（此前**成功路径完全无日志**，静默成功正是排障靠猜的根源）
- `branches 完成` Info 补上 `default` + `default_source`
- 错误分支的既有 Warn/Error 一条不删、不降级

- [ ] **Step 8: 加注释（自检）**

- `taskAndRepoOrErr` 的 doc 注释写清「为什么要返回任务体」（B65 的因果）
- `taskRepoOrErr` 注明自己是瘦包装及适用调用点
- `handleTaskDiff` / `handleTaskBranches` 的 doc 注释与新行为一致（四级顺序、default 可能是 sha）
- 新建测试文件有文件头注释

- [ ] **Step 9: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./internal/agentd/ -count=1`
Expected: `gofmt -l .` 无输出；全 ok

- [ ] **Step 10: Commit**

```bash
git add internal/agentd/server.go internal/agentd/server_diffbase_test.go
git commit -m "feat(agentd): diff/branches 缺省基准取任务起点并回传来源（B65 §2.3-2.4）"
```

---

### Task 3: CLI 基准回显

**Files:**
- Modify: `internal/client/client.go`（`Diff` 方法）
- Modify: `cmd/diff.go`
- Modify: `README.md:283`、`README.zh-CN.md:169`
- Test: `cmd/diff_test.go`（新建）

**Interfaces:**
- Consumes：Task 2 的响应字段 `base` / `base_source`；`proto.DiffBase*` 常量。
- Produces：`client.DiffResult{Diff, Base string; BaseSource proto.DiffBaseSource}`；`client.Diff` 返回值由 `string` 变为 `DiffResult`（全仓唯一调用点是 `cmd/diff.go`）。

- [ ] **Step 1: 写失败的测试**

新建 `cmd/diff_test.go`：

```go
// diff_test.go —— handoff diff 的 stderr 基准回显文案（B65 §2.5）。
//
// 边界：只测文案映射这一个纯函数；HTTP 交互由 internal/client 自己的测试覆盖。
package cmd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestBaseLine(t *testing.T) {
	const sha = "a1a83f34db2ce70eba4261f44562eee7694e27ec"
	cases := []struct {
		name string
		in   client.DiffResult
		want string
	}{
		{"任务起点截断到 8 位", client.DiffResult{Base: sha, BaseSource: proto.DiffBaseTaskStart},
			"基准 a1a83f34（任务起点）"},
		{"仓库默认分支不写原因", client.DiffResult{Base: "main", BaseSource: proto.DiffBaseRepoDefault},
			"基准 main（仓库默认分支；未用任务起点）"},
		{"显式指定", client.DiffResult{Base: "dev", BaseSource: proto.DiffBaseExplicit},
			"基准 dev（显式指定）"},
		{"老 agentd 不返回字段时不打印", client.DiffResult{}, ""},
		{"来源未知时只报基准不编造", client.DiffResult{Base: "main", BaseSource: "future"},
			"基准 main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := baseLine(c.in); got != c.want {
				t.Errorf("baseLine() = %q，期望 %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestBaseLine -count=1`
Expected: FAIL，`undefined: baseLine`

- [ ] **Step 3: 改 `client.Diff` 的返回值**

在 `internal/client/client.go` 的 `Diff` 方法上方新增类型，并改写方法：

```go
// DiffResult 是 diff 的结果：diff 正文，以及本次实际使用的基准与其来源。
//
// 注意：
//   - Base/BaseSource 可能为空——老版本 agentd 不返回这两个字段。空即「不知道」，
//     调用方据此省略回显即可，**不要拿本地信息去编一个基准出来**
type DiffResult struct {
	Diff       string
	Base       string
	BaseSource proto.DiffBaseSource
}

// Diff 取任务分支相对基准的审阅素材（git diff + 提交列表）。
//
// 参数 base 为空时由 agentd 推导缺省基准（优先任务起点），实际用了哪个经
// 返回值的 Base/BaseSource 回传——协调者需要知道素材是相对什么算出来的。
func (c *Client) Diff(ctx context.Context, taskID, base string) (DiffResult, error) {
```

方法体内三处 `return "", ...` 改为 `return DiffResult{}, ...`；解析块改为：

```go
	var out struct {
		Diff       string               `json:"diff"`
		Base       string               `json:"base"`
		BaseSource proto.DiffBaseSource `json:"base_source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return DiffResult{}, fmt.Errorf("解析 diff 响应: %w", err)
	}
	return DiffResult{Diff: out.Diff, Base: out.Base, BaseSource: out.BaseSource}, nil
```

- [ ] **Step 4: 实现 `baseLine` 并接进 `cmd/diff.go`**

```go
// baseLine 把 diff 响应里的基准与来源折成一行 stderr 回显；空串表示不打印。
//
// 为什么 repo_default 的文案不写原因：落到这一级有两种可能——任务本来就没有
// 起点记录，或起点在仓库里不可达——CLI 无从分辨，精确原因在 agentd 日志里。
// 宁可少说一句，也不能替它编一个理由。
func baseLine(r client.DiffResult) string {
	if r.Base == "" {
		// 老 agentd 不返回这两个字段。不知道就不说。
		return ""
	}
	switch r.BaseSource {
	case proto.DiffBaseTaskStart:
		short := r.Base
		if len(short) > 8 {
			short = short[:8]
		}
		return fmt.Sprintf("基准 %s（任务起点）", short)
	case proto.DiffBaseRepoDefault:
		return fmt.Sprintf("基准 %s（仓库默认分支；未用任务起点）", r.Base)
	case proto.DiffBaseExplicit:
		return fmt.Sprintf("基准 %s（显式指定）", r.Base)
	default:
		// 未来新增的来源值：报事实，不猜语义
		return fmt.Sprintf("基准 %s", r.Base)
	}
}
```

`RunE` 内替换为：

```go
		res, err := client.New(addr, token).Diff(cmd.Context(), args[0], diffBase)
		if err != nil {
			return err
		}
		// 基准回显走 stderr：stdout 是「纯 diff 正文」的既有契约，协调者常把它
		// 接进管道分析，多打一行头就多一处要剥。与 dispatch 的「基线 <短号>」同款。
		if line := baseLine(res); line != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), line)
		}
		fmt.Fprintln(cmd.OutOrStdout(), res.Diff)
```

并订正 `cmd/diff.go` 文件头注释的「边界」一条——现在缺省是任务起点，且会回显。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -run TestBaseLine -count=1 -v`
Expected: 五条子用例全 PASS

- [ ] **Step 6: 改两份 README 的 diff 行**

`README.md:283`：

```
| `handoff diff <task>` | git diff + commit list (review material); defaults to the task's own start commit, and prints which base it used to stderr | `--base <branch-or-commit>` |
```

`README.zh-CN.md:169`：

```
| `handoff diff <task>` | git diff + 提交列表（审阅素材）；缺省以任务自己的起点为基准，并把实际用的基准打到 stderr | `--base <分支或提交>` |
```

- [ ] **Step 7: 加关键节点日志（自检）**

`cmd/` 层没有 logger，输出即产物。确认：**没有把 `fmt.Fprintln` 改写成 logger**（Global Constraint 3），且 stdout 只有 diff 正文（Global Constraint 5）。

- [ ] **Step 8: 加注释（自检）**

- `client.DiffResult` 类型与 `Diff` 方法的 doc 注释写清「空即不知道，不要编造」
- `baseLine` 的 doc 注释写清「为什么不写 repo_default 的原因」
- stderr 与 stdout 分流处有行内注释说明既有契约
- `cmd/diff.go` 与 `cmd/diff_test.go` 的文件头注释与新行为一致

- [ ] **Step 9: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./cmd/ ./internal/client/ -count=1`
Expected: `gofmt -l .` 无输出；全 ok

- [ ] **Step 10: Commit**

```bash
git add internal/client/client.go cmd/diff.go cmd/diff_test.go README.md README.zh-CN.md
git commit -m "feat(cli): handoff diff 把实际使用的基准回显到 stderr（B65 §2.5）"
```

---

### Task 4: `shellQuote` + run 接线

**Files:**
- Create: `cmd/shellquote.go`
- Test: `cmd/shellquote_test.go`（新建）
- Modify: `cmd/run.go:31`
- Modify: `README.md:285`、`README.zh-CN.md:171`

**Interfaces:**
- Produces：`shellQuote(arg string) string`、`shellQuoteJoin(args []string) string`（包内私有，仅 `cmd/run.go` 消费）。
- Consumes：无。本 task 与 Task 1-3 完全独立，可任意顺序实现。

- [ ] **Step 1: 写失败的测试**

新建 `cmd/shellquote_test.go`。**下面的用例集与期望值是 2026-08-18 真机跑 `sh` 实测过的（20/20 通过），不是推演**：

```go
// shellquote_test.go —— POSIX shell 转义的正确性（B66）。
//
// 职责：
//   - 表格测试钉住转义产物的形状（跨平台都跑）
//   - 往返测试**真的起一个 sh** 把转义串解析回去，断言 argv 逐字还原
//
// 边界：
//   - 往返测试在 Windows 上跳过（sh 需 Git for Windows，未必存在）；
//     表格测试仍然跑，转义逻辑本身与平台无关
package cmd

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo", "'foo'"},
		{"foo bar", "'foo bar'"},
		{"", "''"},                      // 空串必须产出 ''，否则 join 后整个参数消失
		{"it's", `'it'\''s'`},           // 单引号是唯一需要处理的字符
		{"$HOME", "'$HOME'"},            // 单引号内 $ 是字面量
		{"a && b", "'a && b'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestShellQuoteJoin(t *testing.T) {
	got := shellQuoteJoin([]string{"grep", "-rn", "foo bar", "."})
	want := `'grep' '-rn' 'foo bar' '.'`
	if got != want {
		t.Errorf("shellQuoteJoin() = %q，期望 %q", got, want)
	}
	if got := shellQuoteJoin(nil); got != "" {
		t.Errorf("空参数应产出空串，实得 %q", got)
	}
}

// roundTripCases 是必须能原样穿过 sh 的参数集合。
//
// 为什么要有这一组：本条修复的判据不是「转义串长什么样」——那等于把实现抄进
// 测试——而是「参数穿过一层 sh 之后还是不是原来那串字节」。
var roundTripCases = []string{
	"foo bar", "it's", `he said "hi"`, "$HOME", "`whoami`", "a && b",
	"line1\nline2", "", "中文 参数", "--race", "-n", "*.go", `a\b`,
	"'", "''", "\t tab", "$(rm -rf /)", "~", "!", "#comment",
}

// 单个参数穿过 sh 之后必须逐字节相同。
func TestShellQuoteRoundTripThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 sh 需 Git for Windows，未必存在；转义逻辑由表格测试覆盖")
	}
	for _, arg := range roundTripCases {
		t.Run(arg, func(t *testing.T) {
			// printf 的第一个操作数是格式串，%s 已占位，因此 -n 之类的参数
			// 不会被当成选项解析（2026-08-18 实测确认）
			line := shellQuoteJoin([]string{"printf", "%s", arg})
			out, err := exec.Command("sh", "-c", line).Output()
			if err != nil {
				t.Fatalf("sh -c %q 失败: %v", line, err)
			}
			if string(out) != arg {
				t.Errorf("往返后 = %q，期望 %q（命令行 %s）", string(out), arg, line)
			}
		})
	}
}

// 多参数时 argv 边界不能被吞掉——这正是 B66 的现场（'foo bar' 被拆成两个参数）。
func TestShellQuoteJoinPreservesArgvBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上 sh 需 Git for Windows，未必存在")
	}
	line := shellQuoteJoin([]string{"sh", "-c", `printf "%s|" "$@"`, "sh", "a b", "c'd", "e&&f", ""})
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("sh -c %q 失败: %v", line, err)
	}
	if want := "a b|c'd|e&&f||"; string(out) != want {
		t.Errorf("argv 往返 = %q，期望 %q", string(out), want)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestShellQuote' -count=1`
Expected: FAIL，`undefined: shellQuote`

- [ ] **Step 3: 实现 `cmd/shellquote.go`**

```go
// 本文件实现把命令行参数安全地拼成一条 POSIX shell 命令行。
//
// 职责：
//   - 把已经被本地 shell 拆好的 argv 逐个转义再拼接，使远端 sh -c 解析回来
//     得到逐字节相同的 argv
//
// 边界：
//   - 只做 POSIX 单引号转义，不解析、不校验、不改写命令语义
//   - 不处理 cmd.exe / PowerShell 方言：agentd 侧恒用 POSIX sh（Windows 上
//     也是找 Git 的 sh.exe，找不到就硬失败，见 internal/agentd/runshell.go），
//     因此这里的转义在所有平台上都是对的
package cmd

import "strings"

// shellQuote 把一个参数包装成 POSIX shell 的单引号字面量。
//
// 单引号内除 ' 以外的一切字符都是字面量——空格、$、反引号、反斜杠、换行、
// 通配符全都不会被 shell 解释——所以只需处理 ' 自身：闭合当前引号、插入一个
// 反斜杠转义的 '、再重新开引号，即 '\''。
//
// 空串产出 ''：不加引号的话它在 join 之后会整个消失，参数个数就变了。
func shellQuote(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// shellQuoteJoin 把一组参数逐个转义后用空格连接成可交给 sh -c 的命令行。
//
// 为什么必须逐个转义再拼，而不是直接 strings.Join(args, " ")：本地 shell 已经
// 把用户敲的命令拆成词并剥掉了引号，直接重拼等于让远端 sh 再拆一遍——`foo bar`
// 会变成两个参数。命令因此穿过了两层 shell，却只有第一层的引号被消费掉（B66）。
//
// 参数为空切片时返回空串（调用方自行判空，服务端也会拒绝空命令）。
func shellQuoteJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestShellQuote' -count=1 -v`
Expected: 全 PASS，其中 `TestShellQuoteRoundTripThroughSh` 有 20 条子用例

- [ ] **Step 5: 接进 `cmd/run.go`**

`cmd/run.go:31` 的

```go
		stdout, code, err := client.New(addr, token).Run(cmd.Context(), args[0], strings.Join(args[1:], " "))
```

改为

```go
		stdout, code, err := client.New(addr, token).Run(cmd.Context(), args[0], shellQuoteJoin(args[1:]))
```

`strings` 若因此不再被使用，从 import 块删掉。

同时在文件头注释的「职责」里把「把命令原文透传给 agentd 执行」改写成说明转义这一步：

```go
//   - 把每个参数逐个做 POSIX shell 转义后拼成命令行交给 agentd 执行（sh -c，
//     10min 超时）——本地 shell 已经剥掉一层引号，不转义就会在远端被拆第二次
```

- [ ] **Step 6: 跑测试确认没打破既有行为**

Run: `go test ./cmd/ -count=1`
Expected: 全 ok（`cmd` 包下已有 31 个测试文件，任何 run 相关的既有断言都必须仍然绿）

- [ ] **Step 7: 改两份 README 的 run 行**

`README.md:285` 的说明列追加一句：

```
| `handoff run <task> <command...>` | Run a review command inside the task repo (sh -c, 10min timeout). Arguments are shell-quoted per token, so quotes and spaces survive; for `&&`/pipes write `handoff run <task> sh -c 'a && b'` | handoff's own flags must come **before** the task id; everything after it is passed through verbatim |
```

`README.zh-CN.md:171` 同理：

```
| `handoff run <task> <命令...>` | 在任务仓库执行审阅命令（sh -c，10min 超时）。参数逐 token 做 shell 转义，引号与空格不丢；要写 `&&`/管道请用 `handoff run <task> sh -c 'a && b'` | handoff 自有 flag 必须在任务名**之前**，任务名之后的一切原样透传给命令 |
```

**两份都要在 run 那一行下方或就近补一句已知脚坑**（内容见 spec §3.3）：直接敲
`handoff run <task> go build ./... && go vet ./...` 时 `&&` 会被**本地** shell 吃掉，
`go vet` 在协调者本机跑；这是 shell 的行为，不是 handoff 的缺陷。

- [ ] **Step 8: 加关键节点日志（自检）**

`shellQuote`/`shellQuoteJoin` 是无 I/O、无分支失败的纯函数，按 instrumenting-code 的
豁免条款不需要日志。**服务端 `RunCmd` 里既有的 `run 命令执行` Info 一行不改**——
它记的 `cmdline` 现在是转义后的形态，正是排障时想看的东西。确认没有引入
`fmt.Printf` 作为日志手段。

- [ ] **Step 9: 加注释（自检）**

- `cmd/shellquote.go` 有文件头注释（职责 + 边界），边界里写明「不处理 cmd.exe/PowerShell 方言」及其依据
- 两个函数各有 doc 注释；`shellQuoteJoin` 的注释写清「为什么不能直接 Join」（B66 的因果）
- `cmd/run.go` 的文件头注释与新行为一致
- 测试文件有文件头注释，`roundTripCases` 上方写明「判据是往返而非形状」

- [ ] **Step 10: 全量回归 + 格式**

Run: `go build ./... && go vet ./... && gofmt -l . && go test ./cmd/ -count=1`
Expected: `gofmt -l .` 无输出；全 ok

- [ ] **Step 11: Commit**

```bash
git add cmd/shellquote.go cmd/shellquote_test.go cmd/run.go README.md README.zh-CN.md
git commit -m "fix(cli): handoff run 逐 token shell 转义，引号与空格不再丢失（B66）"
```

---

### Task 5: 控制台基准标签对齐

**Files:**
- Modify: `web/src/api/types.ts`（`DiffResult` `:350`、`BranchesResult` `:365`）
- Modify: `web/src/app/task/ReviewSidePanel.tsx`（`DiffSection`，`:89` 那一行）
- Test: `web/src/app/task/ReviewSidePanel.test.tsx`

**Interfaces:**
- Consumes：Task 2 的 `GET /api/tasks/{id}/branches` 响应新增的 `default_source`。
- Produces：无下游。

- [ ] **Step 1: 写失败的测试**

在 `web/src/app/task/ReviewSidePanel.test.tsx` 里补三条。注意既有 `beforeEach` 的
mock 是 `{ branches: ['main', 'dev'], default: 'main' }`（无 `default_source`），
它本身就是「老 agentd」这一态，保留不动：

```tsx
  it('基准下拉：任务起点显示短号并标注来源', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({
      branches: ['main', 'dev'],
      default: 'a1a83f34db2ce70eba4261f44562eee7694e27ec',
      default_source: 'task_base',
    })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    expect(await screen.findByRole('option', { name: '自动推导（a1a83f34 · 任务起点）' })).toBeInTheDocument()
  })

  it('基准下拉：回落仓库默认分支时如实标注', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({
      branches: ['main', 'dev'],
      default: 'main',
      default_source: 'repo_default',
    })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    expect(await screen.findByRole('option', { name: '自动推导（main · 仓库默认）' })).toBeInTheDocument()
  })

  it('基准下拉：老 agentd 无 default_source 时按仓库默认展示', async () => {
    // beforeEach 的默认 mock 就是这一态
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    expect(await screen.findByRole('option', { name: '自动推导（main · 仓库默认）' })).toBeInTheDocument()
  })
```

- [ ] **Step 2: 跑测试确认失败**

Run（在 `web/` 下）：`npm test -- ReviewSidePanel`
Expected: 三条新用例 FAIL（当前渲染的是 `自动推导（main）`）

- [ ] **Step 3: 扩 TS 类型**

`web/src/api/types.ts` 的 `DiffResult`：

```ts
// DiffResult 是 GET /api/tasks/{id}/diff 的响应。
//
// base/base_source 是老 agentd 不返回的可选字段：缺席即「不知道用了什么基准」，
// 展示层据此省略标注，不要编造。
export interface DiffResult {
  diff: string
  base?: string
  base_source?: string
}
```

`BranchesResult`：

```ts
// BranchesResult 是 GET /api/tasks/{id}/branches 的响应：本地分支名 + 缺省基准。
//
// default **可能是 sha 而不是分支名**——它回答的是「不选的话 diff 实际会用什么」，
// 而缺省会优先用任务自己的起点（B65）。它不必出现在 branches 列表里，因为它只是
// 「自动推导」那一项的标签。空串 = 推导不出，下拉退化为仅「自动推导」。
export interface BranchesResult {
  branches: string[]
  default: string
  // default_source ∈ 'task_base' | 'repo_default'；缺席 = 老 agentd。
  default_source?: string
}
```

- [ ] **Step 4: 实现标签渲染**

在 `ReviewSidePanel.tsx` 的 `DiffSection` 之外（模块级）新增：

```tsx
// autoBaseLabel 生成基准下拉里「自动推导」那一项的标签。
//
// 为什么要标来源：不标的话，控制台显示的默认基准与 diff 实际用的基准可以静默
// 分叉——这正是 B65 在 CLI 侧咬人的那个形态，不能在前端重演一次。
function autoBaseLabel(b: BranchesResult | null): string {
  if (!b?.default) return '自动推导'
  if (b.default_source === 'task_base') {
    return `自动推导（${b.default.slice(0, 8)} · 任务起点）`
  }
  return `自动推导（${b.default} · 仓库默认）`
}
```

`:89` 那一行替换为：

```tsx
          <option value="">{autoBaseLabel(branches)}</option>
```

- [ ] **Step 5: 跑测试确认通过**

Run（在 `web/` 下）：`npm test -- ReviewSidePanel`
Expected: 全 PASS（含既有的「分支接口失败退化」用例——它 mock 的是 `null`，走 `!b?.default` 分支返回光秃秃的 `自动推导`，正则 `/自动推导/` 仍然匹配）

- [ ] **Step 6: 加关键节点日志（自检）**

前端展示层无日志设施，本 task 无日志要求。确认没有引入 `console.log`。

- [ ] **Step 7: 加注释（自检）**

- `DiffResult` / `BranchesResult` 的可选字段都写明「缺席 = 老 agentd，不要编造」
- `BranchesResult.default` 写明「可能是 sha 而非分支名」及其原因
- `autoBaseLabel` 的注释写清「为什么要标来源」

- [ ] **Step 8: 全量回归**

Run（在 `web/` 下）：`npm run lint && npm test -- --run`
Expected: lint 无错；前端测试全绿

- [ ] **Step 9: Commit**

```bash
git add web/src/api/types.ts web/src/app/task/ReviewSidePanel.tsx web/src/app/task/ReviewSidePanel.test.tsx
git commit -m "feat(web): 审阅栏基准下拉标注来源，与 CLI 缺省保持同源（B65 §4）"
```

---

### Task 6: 整分支终审

**Files:** 无新增，只读审阅 + 必要时修补。

- [ ] **Step 1: 取整分支 diff**

```bash
BASE=$(git merge-base HEAD handoff/web-console)
git diff "$BASE".. --stat
git log --oneline "$BASE"..
```

- [ ] **Step 2: 逐条核对 Global Constraints**

对着 diff 逐项确认，任一不过就修：

1. `internal/agentd/workspace.go` 的 `RunCmd` 与 `internal/agentd/runshell.go` **零改动**
2. `POST /api/tasks/{id}/run` 的请求体形状未变；diff/branches 只增字段
3. 全 diff 内无新增 `fmt.Printf`；`cmd/` 层的 `fmt.Fprintln` 未被改写成 logger
4. 新建文件（`cmd/shellquote.go` 及三个新测试文件）都有文件头注释；新增导出标识符（`proto.DiffBaseSource` 及三常量、`agentd.ResolveDiffBase`、`client.DiffResult`）都有 doc 注释
5. `cmd/diff.go` 的 stdout 只有 `res.Diff`
6. 全 diff 内无 `~/.claude/` 路径的写入
7. `GET /api/projects/{name}/branches` 相关代码零改动
8. `gofmt -l .` 无输出

- [ ] **Step 3: 全量门**

```bash
go build ./...
go vet ./...
gofmt -l .
go test ./... -count=1
```

再单独跑一次竞态：

```bash
go test -race ./internal/agentd/ ./cmd/ ./internal/client/ -count=1
```

前端：

```bash
cd web && npm run lint && npm test -- --run
```

- [ ] **Step 4: 终审发现项一次性修完**

有发现项就一次性全部修掉，再做一次只看修复 diff 的范围复审。不搞逐项修，也没有第二轮修复波。

- [ ] **Step 5: Commit（仅在有修补时）**

```bash
git add -A
git commit -m "chore: B65+B66 整分支终审修补"
```

---

## 真机验收 —— **由协调者本地执行，不随本计划派发**

以下三条需要**驱动 handoff 自身**（起 agentd、派任务、调 `handoff` CLI），与派发给
执行者的纪律块「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」
直接冲突。执行者**不要尝试执行这一节**，也不要因为没做而报 BLOCKED——它不属于
你的交付范围。

1. 对一个 `--new-worktree --new-branch` 派出的任务执行 `handoff diff <task>`（不带
   `--base`）：stdout 只含该任务分支的改动；stderr 打出 `基准 <短号>（任务起点）`
2. `handoff run <task> grep -rn 'foo bar' .` —— 带空格的模式不被拆成两个参数
3. `handoff run <task> sh -c 'go build ./... && go vet ./...'` —— 两条命令都在远端
   跑完，退出码正确

## Self-Review 记录

- **Spec 覆盖**：spec §2.1/2.2 → Task 1；§2.3/2.4 → Task 2；§2.5 → Task 3；§3 → Task 4；
  §4 → Task 5；§5 契约汇总散落在 Task 2/3/5 的接口块；§6.1 Go 测试 → Task 1/2/3/4；
  §6.2 前端测试 → Task 5；§6.3 真机验收 → 独立成节并标注不派发；§7 边界 → Global
  Constraints 1/2/6/7 + Task 4 Step 7 的脚坑说明；§8 README → Task 3 Step 6 与
  Task 4 Step 7，用户全局文件与记忆明确不在本计划范围内。
- **占位符扫描**：无 TBD / TODO / 「类似 Task N」；每个代码步骤都给了可直接落地的代码块。
- **类型一致性**：`proto.DiffBaseSource` 在 Task 1 定义，Task 2（`string(source)` 入
  JSON）、Task 3（`client.DiffResult.BaseSource` 字段与 `baseLine` 的 switch）、
  Task 5（TS 侧对应 `default_source?: string`）三处引用一致；`ResolveDiffBase` 的
  签名 `(repo, baseCommit string) (string, proto.DiffBaseSource)` 在 Task 1/2 两处一致。
- **脚手架真名已全部核实**（2026-08-18 在本分支实测）：`newTestAgentdEnvWithCfg`
  返回 `*testAgentdEnv{srv *Server, st *store.Store, ...}`；`testToken` 定义在
  `w3a_testhelpers_test.go`（`package agentd`）；`initGitRepo`/`gitAt` 在
  `workspace_test.go`（同包）。**`store` 没有 `UpdateTask` 这类整体更新方法**——
  初稿写过，核实后已改为在 `CreateTask` 时直接落 `RepoPath`。实现者若发现本计划
  仍有对不上的名字，以仓库实际为准，**不得新增方法去迁就计划**。
- **`shellQuote` 的往返用例是实测结果**：2026-08-18 在本机真起 `sh` 跑过
  20/20 单参数往返 + 多参数 argv 边界（`a b|c'"'"'d|e&&f||`），不是推演。
