# 回程 pull 改走 agentd HTTP 面（git bundle）实现计划（B143）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff pull` 与 `wait` 的自动同步经 agentd 的 HTTP 面取 git bundle 拿回任务分支，不再依赖执行机有 ssh、不依赖远端登录 shell 是 POSIX、不依赖 ssh 主机能从 agentd 地址推导出来。

**Architecture:** agentd 新增一个只读端点 `GET /api/tasks/{id}/bundle?have=<sha>`，在**主仓库**里 `git bundle create` 到 OS 临时文件后整包发出（带 `Content-Length`），空区间回 204。客户端把包落到本地临时文件，再把该文件路径当 `RemoteURL` 交给**现有的、一行不改的** `localsync.Fetch`——git 把 bundle 文件当合法 transport。对端 404 说明 agentd 过旧，退回既有 ssh 路径；其它错误如实报错，不回落。

**Tech Stack:** Go 1.x 标准库（`net/http` 的 `ServeMux` 方法路由 + `{id}` 路径参数）、`os/exec` 调 git、`log/slog` 结构化日志、`testing` + `httptest` + 真 git 仓库（`t.TempDir()`）。无新增第三方依赖。

**Spec:** `docs/superpowers/specs/2026-08-18-pull-over-http-bundle-design.md`

## Global Constraints

以下要求对**每个** task 都生效，逐条来自 spec，值原样照抄：

1. **服务端一律在 `task.RepoPath`（主仓库）执行 git，不用 `Workdir()`。** worktree 是主仓的从属工作树，分支对象在主仓库里。（spec §3.1）
2. **空区间的判据是 `git rev-list --count <have>..<branch>` 的输出为 `0`，禁止匹配 `git bundle create` 的 stderr 文案。** 那是英文文案、随 git 版本变。（spec §5）
3. **`localsync` 包一行都不改。** `Fetch` / `Opts` / `Result` / `FetchTimeout` 的签名与行为保持现状。需要「本地是否已有某 commit」这类新逻辑时，写在 `cmd/` 包内。（spec §3.2、§13）
4. **降级只对 404 发生。** 400 / 401 / 500 以及「包拿到了但 `git fetch` 失败」一律如实报错，**不**退回 ssh。（spec §6）
5. **不设人为体积上限**，改为两侧都记录字节数。（spec §8）
6. **所有临时文件落 OS 临时目录（`os.CreateTemp`）并 `defer os.Remove`**，服务端与客户端都不许落进任何 git 仓库——那会弄脏工作区，而干净工作区是 `dispatch` 的前置条件。（spec §8）
7. **日志纪律**：服务端进入打 `task`/`have`/`branch`，完成打 `bytes` 与耗时，204 单独一条 Info 说明是空区间而非失败；客户端必须打**本次走了哪条路**（bundle / ssh 回落），回落时把 404 的事实一并写出。**任何情况下不打 token、不打 bundle 内容。**（spec §9）
8. **不改任何已有函数签名。**（spec §13）
9. 日志一律用包内既有的 logger：agentd 包用 `log()` / `s.log`，client 包用 `c.log()`，cmd 包用 `slog.Default()`。**禁止 `fmt.Printf` 作为日志手段**（`fmt.Fprintln(cmd.OutOrStdout(), …)` 是给人看的命令输出，不算日志，保留现状）。
10. 新建文件必须有文件头注释（职责 + 边界），导出函数必须有 doc 注释（参数、返回、注意事项），非显然的分支必须有「为什么」的中文注释。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/agentd/bundle.go` | 新建 | 在任务主仓库里生成 `<have>..<branch>` 的 bundle 临时文件；把「空区间」「have 不存在」两种**预期形态**与真故障区分成哨兵错误。不碰 HTTP。 |
| `internal/agentd/bundle_test.go` | 新建 | 用 `t.TempDir()` 里的真 git 仓库覆盖薄包/全量/空区间/have 不存在/参数注入。 |
| `internal/agentd/server.go` | 修改 | 新增 `handleTaskBundle`，在 `/api/tasks/{id}/…` 族里注册路由（`s.byTask` 包装）。只做状态码映射与字节搬运。 |
| `internal/agentd/bundle_handler_test.go` | 新建 | handler 层的状态码表驱动测试。 |
| `internal/client/bundle.go` | 新建 | `Client.Bundle` 与 `ErrBundleUnsupported`：把 404 翻成「对端过旧」这一**结论**，其它状态翻成故障。 |
| `internal/client/bundle_test.go` | 新建 | 200/204/404/500 四条分支。 |
| `cmd/pull.go` | 修改 | 降级阶梯：先 bundle，仅 404 退回 ssh。新增本地 `have` 核实与包落盘。 |
| `cmd/pull_test.go` | 新建 | 降级阶梯测试，含**反面断言**：500 时必须报错且不走 ssh。 |
| `internal/agentd/bundle_e2e_test.go` | 新建 | 进程内端到端：真 handler 出包 → 真 fetch 进第二个仓库 → 断言 commit 真的到了。 |
| `README.md` | 修改 | 记录新端点与「回程不再需要执行机有 ssh」。 |

任务边界按「一个新审阅者能独立否决其中一个而放行相邻一个」划：Task 1 是纯 git 逻辑、Task 2 是纯 HTTP 映射、Task 3 是纯客户端翻译、Task 4 是决策链路、Task 5 是链路证明与文档。

---

### Task 1: 服务端 bundle 生成

**Files:**
- Create: `internal/agentd/bundle.go`
- Test: `internal/agentd/bundle_test.go`

**Interfaces:**
- Consumes: `gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)` 与 `gitProbe(...)`（同签名，非零退出记 Debug 不记 Error），均在 `internal/agentd/workspace.go`；`ErrBadBaseBranch`（`workspace.go:69`）；包级 `log() *slog.Logger`。
- Produces: `func BundleRange(ctx context.Context, repo, have, branch string) (string, error)`、`var ErrEmptyRange error`、`var ErrHaveMissing error`。

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/bundle_test.go`：

```go
package agentd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newBundleRepo 建一个真 git 仓库：base 提交在 main，另有一个 feat/x 分支多一个提交。
// 返回仓库路径与 base 提交的完整 sha。
func newBundleRepo(t *testing.T) (repo, baseSHA string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("写 base.txt: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	baseSHA = run("rev-parse", "HEAD")
	run("checkout", "-b", "feat/x")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("写 work.txt: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "work")
	run("checkout", "main")
	return repo, baseSHA
}

// 有 have 时生成薄包：文件存在、非空。
func TestBundleRangeThin(t *testing.T) {
	repo, base := newBundleRepo(t)
	path, err := BundleRange(context.Background(), repo, base, "feat/x")
	if err != nil {
		t.Fatalf("BundleRange: %v", err)
	}
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("生成的包不存在: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("薄包不该是空文件")
	}
}

// 无 have 时生成全量包：也必须成功，且比薄包大（含 base 提交的对象）。
func TestBundleRangeFull(t *testing.T) {
	repo, base := newBundleRepo(t)
	thin, err := BundleRange(context.Background(), repo, base, "feat/x")
	if err != nil {
		t.Fatalf("薄包: %v", err)
	}
	defer os.Remove(thin)
	full, err := BundleRange(context.Background(), repo, "", "feat/x")
	if err != nil {
		t.Fatalf("全量包: %v", err)
	}
	defer os.Remove(full)
	thinFI, _ := os.Stat(thin)
	fullFI, _ := os.Stat(full)
	if fullFI.Size() <= thinFI.Size() {
		t.Errorf("全量包应大于薄包，实得 full=%d thin=%d", fullFI.Size(), thinFI.Size())
	}
}

// 空区间是**预期形态**，必须是 ErrEmptyRange 而不是 git 的失败原文。
// 这条是承重的：不识别它，第二次 pull 就变成一个 500。
func TestBundleRangeEmpty(t *testing.T) {
	repo, _ := newBundleRepo(t)
	head := headSHAForTest(t, repo, "feat/x")
	_, err := BundleRange(context.Background(), repo, head, "feat/x")
	if !errors.Is(err, ErrEmptyRange) {
		t.Fatalf("空区间应返回 ErrEmptyRange，实得 %v", err)
	}
}

// have 在仓库里不存在时响亮失败，不许悄悄退回全量。
func TestBundleRangeHaveMissing(t *testing.T) {
	repo, _ := newBundleRepo(t)
	absent := "0123456789abcdef0123456789abcdef01234567"
	_, err := BundleRange(context.Background(), repo, absent, "feat/x")
	if !errors.Is(err, ErrHaveMissing) {
		t.Fatalf("have 不存在应返回 ErrHaveMissing，实得 %v", err)
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("报文必须带上那个 sha 才能排障，实得 %q", err.Error())
	}
}

// branch / have 以 - 开头会被 git 当成选项：参数注入面，一律拒绝。
func TestBundleRangeRejectsDashPrefix(t *testing.T) {
	repo, base := newBundleRepo(t)
	if _, err := BundleRange(context.Background(), repo, base, "--upload-pack=x"); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("- 前缀分支名应被拒，实得 %v", err)
	}
	if _, err := BundleRange(context.Background(), repo, "--foo", "feat/x"); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("- 前缀 have 应被拒，实得 %v", err)
	}
	if _, err := BundleRange(context.Background(), repo, base, ""); !errors.Is(err, ErrBadBaseBranch) {
		t.Errorf("空分支名应被拒，实得 %v", err)
	}
}

// headSHAForTest 取某分支的完整 sha。
func headSHAForTest(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestBundleRange' -v`
Expected: 编译失败，`undefined: BundleRange` / `undefined: ErrEmptyRange` / `undefined: ErrHaveMissing`

- [ ] **Step 3: 写最小实现**

新建 `internal/agentd/bundle.go`：

```go
// 本文件实现任务分支的 git bundle 生成，供 GET /api/tasks/{id}/bundle 使用。
//
// 职责：
//   - 在任务的**主仓库**里按 <have>..<branch> 生成薄包（have 为空则全量），落 OS 临时文件
//   - 把「空区间」与「have 不存在」这两种预期形态与真故障区分成可判别的哨兵
//
// 边界：
//   - 不碰 HTTP：状态码映射在 server.go 的 handleTaskBundle
//   - 不删临时文件：调用方拿到路径后自己 defer os.Remove
//   - 只读仓库，不建分支、不改任何 ref
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrEmptyRange 表示 <have>..<branch> 区间里没有任何提交。
//
// 为什么必须是可判别的哨兵：`git bundle create` 对空区间是**失败**
//（fatal: Refusing to create empty bundle.），而这个情形天天发生——连着 pull
// 两次、或任务没产生新提交就是。不把它与真故障分开，第二次 pull 就变成一个 500。
var ErrEmptyRange = errors.New("提交区间为空，无需生成 bundle")

// ErrHaveMissing 表示调用方声明的 have 提交在任务仓库中不存在。
//
// 为什么响亮失败而不是悄悄退回全量：客户端只会回传任务记录里的 BaseCommit，
// 它在任务仓库里找不到意味着真的出了异常。退回全量会让「协调者拿到的包比预期
// 大得多」这件事无声发生。
var ErrHaveMissing = errors.New("have 提交在任务仓库中不存在")

// BundleRange 在 repo 里生成 <have>..<branch> 的 git bundle，返回临时文件路径。
//
// 参数：
//   - ctx:    调用方上下文，透传给 git 子进程
//   - repo:   任务的**主仓库**路径（task.RepoPath，不是 Workdir()）——worktree 是
//     主仓的从属工作树，分支对象在主仓库里
//   - have:   协调者已有的基线提交；空串表示生成全量包
//   - branch: 任务分支名
//
// 返回：
//   - path: 生成的临时文件路径。**调用方负责 os.Remove**，本函数不回收
//   - err:  ErrEmptyRange（区间为空，属预期形态）/ ErrHaveMissing（have 不存在）/
//     ErrBadBaseBranch（参数以 - 开头或分支名为空）/ 其余为真故障
//
// 注意：
//   - 临时文件落 OS 临时目录，绝不落进 repo——那会让 dispatch 的干净工作区校验误报
//   - 不设体积上限：一个会拒绝合法全量包的上限，是把能用的路径改成坏的
func BundleRange(ctx context.Context, repo, have, branch string) (string, error) {
	// git 会把以 - 开头的参数解释为选项：这是参数注入面，与 Diff 的 base 同源，
	// 所以复用同一个哨兵（ErrBadBaseBranch），调用方的 400 映射也就统一了
	if branch == "" || strings.HasPrefix(branch, "-") {
		log().Warn("bundle 分支名非法被拒绝", "repo", repo, "branch", branch)
		return "", fmt.Errorf("%w: %q", ErrBadBaseBranch, branch)
	}
	if strings.HasPrefix(have, "-") {
		log().Warn("bundle have 参数非法被拒绝", "repo", repo, "have", have)
		return "", fmt.Errorf("%w: %q", ErrBadBaseBranch, have)
	}
	log().Info("开始生成 bundle", "repo", repo, "branch", branch, "have", have)

	revRange := branch
	if have != "" {
		// 用 gitProbe 而非 gitRun：cat-file -e 的非零退出是**正常分支**（协调者
		// 换了机器、在另一个克隆里 pull，都可能报一个本仓库没有的 sha），
		// 走 gitRun 会在成功路径的日志里留下 ERROR
		if _, _, err := gitProbe(ctx, repo, "cat-file", "-e", have+"^{commit}"); err != nil {
			log().Warn("bundle 的 have 提交在任务仓库中不存在", "repo", repo, "have", have)
			return "", fmt.Errorf("%w: %s", ErrHaveMissing, have)
		}
		revRange = have + ".." + branch
	}

	// 先数提交数再决定要不要造包：空区间对 git bundle create 是失败而非空包，
	// 而空区间是常态。判据用 rev-list --count 的数字，**不匹配 stderr 文案**
	//（那是英文、随 git 版本变，把预期形态的判据建在字符串比较上）
	out, _, err := gitRun(ctx, repo, "rev-list", "--count", revRange)
	if err != nil {
		log().Error("bundle 统计提交数失败", "repo", repo, "range", revRange, "cause", err)
		return "", fmt.Errorf("git rev-list --count %s: %w", revRange, err)
	}
	if strings.TrimSpace(out) == "0" {
		log().Info("bundle 区间为空，无需生成", "repo", repo, "range", revRange)
		return "", ErrEmptyRange
	}

	f, err := os.CreateTemp("", "handoff-bundle-*.bundle")
	if err != nil {
		log().Error("创建 bundle 临时文件失败", "repo", repo, "cause", err)
		return "", fmt.Errorf("创建 bundle 临时文件: %w", err)
	}
	path := f.Name()
	// 立刻关掉自己的句柄：真正写这个文件的是 git 子进程，CreateTemp 在这里只被
	// 用来取一个不冲突的路径
	_ = f.Close()

	if _, stderr, err := gitRun(ctx, repo, "bundle", "create", path, revRange); err != nil {
		_ = os.Remove(path)
		log().Error("生成 bundle 失败", "repo", repo, "range", revRange,
			"stderr", truncateRunes(stderr, 500), "cause", err)
		return "", fmt.Errorf("git bundle create %s: %w", revRange, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		log().Error("bundle 生成后无法读取", "repo", repo, "path", path, "cause", err)
		return "", fmt.Errorf("读取生成的 bundle: %w", err)
	}
	log().Info("bundle 生成完成", "repo", repo, "range", revRange, "bytes", fi.Size())
	return path, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestBundleRange' -v`
Expected: 5 个用例全 PASS

- [ ] **Step 5: 关键节点日志自查**

对照 `instrumenting-code` 清单逐条确认（本 task 的日志已写在 Step 3 的代码里，这一步是**核对**不是补写）：

- 进入 `BundleRange` 打 Info，带 `repo` / `branch` / `have`
- 三个拒绝分支（分支名非法、have 非法、have 不存在）各打 Warn 带上下文
- 两个故障分支（rev-list 失败、bundle create 失败）各打 Error 带 `cause`，bundle create 额外带截断的 git stderr 原文
- 空区间打 Info 并说明「无需生成」——**不是**静默返回
- 成功路径打 Info 带 `bytes`——成功不静默
- 全程用 `log()`，无 `fmt.Printf`

不满足的补齐后重跑 Step 4。

- [ ] **Step 6: 注释自查**

- 文件头有「职责 + 边界」，边界明确写了「不碰 HTTP」「不删临时文件」
- `ErrEmptyRange` / `ErrHaveMissing` / `BundleRange` 三处导出符号都有 doc 注释，`BundleRange` 写清参数/返回/注意事项
- 「为什么用 gitProbe 而不是 gitRun」「为什么先关句柄」「为什么不匹配 stderr 文案」三处「为什么」注释在位

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/bundle.go internal/agentd/bundle_test.go
git commit -m "feat(agentd): 新增任务分支 bundle 生成，空区间与 have 缺失单列哨兵"
```

---

### Task 2: bundle HTTP 端点

**Files:**
- Modify: `internal/agentd/server.go`（新增 `handleTaskBundle`；路由注册加在 `api.HandleFunc("GET /api/tasks/{id}/file", …)` 那一族之后）
- Test: `internal/agentd/bundle_handler_test.go`

**Interfaces:**
- Consumes: Task 1 的 `BundleRange(ctx, repo, have, branch) (string, error)`、`ErrEmptyRange`、`ErrHaveMissing`；既有的 `s.taskOrErr(w, taskID) (*proto.Task, bool)`（`server.go:1349`）、`writeJSON(w, status, v)`、`truncateRunes(s string, n int) string`、`s.byTask(h http.HandlerFunc) http.HandlerFunc`、`s.log`；测试辅助 `newTestAgentdEnvWithCfg(t, *config.Config, *slog.Logger) *testAgentdEnv`（`internal/agentd/w3a_testhelpers_test.go:41`，其 `env.ts` 是真 httptest 服务器、`env.st` 是真 SQLite、`env.token` 是 `testToken`）。
- Produces: 路由 `GET /api/tasks/{id}/bundle?have=<sha>`，状态码契约：200（octet-stream + `Content-Length`）/ 204（空区间）/ 400（无分支、have 不存在、参数非法）/ 404（任务不存在）/ 500（git 失败）。

- [ ] **Step 1: 写失败的测试**

新建 `internal/agentd/bundle_handler_test.go`：

> 下面这段辅助（`newBundleEnv` / `getBundle`）已在基线上编译并跑通过一次
> （对 `/api/tasks/{id}/diff` 打真实请求，已知任务 200、未知任务 404）。
> **不要**改成 `httptest.NewRequest` + `srv.Handler().ServeHTTP`：那条路的默认
> Host 是 `example.com`，会被 hostGuard 在鉴权前 403 掉。走 `env.ts.URL` 的真实
> HTTP 客户端没有这个问题，而且拿二进制包体更自然。

```go
package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// newBundleEnv 构造一个带真 git 仓库的测试环境，并入库一个指向它的任务。
//
// 返回：测试环境、任务 ID、仓库路径、base 提交 sha。
//
// 为什么直接 CreateTask 而不是建完再改：store.SetTaskField 的白名单只有
// branch/executor_session/plan_summary/done_note 四项，repo_path 改不了。
func newBundleEnv(t *testing.T, branch string) (env *testAgentdEnv, taskID, repo, baseSHA string) {
	t.Helper()
	env = newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	repo, baseSHA = newBundleRepo(t)
	taskID = "t-bundle"
	if err := env.st.CreateTask(&proto.Task{
		ID: taskID, RepoPath: repo, Branch: branch, State: proto.TaskStateWaitingReview,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return env, taskID, repo, baseSHA
}

// getBundle 打一次 bundle 请求，返回响应与已读完的包体。
func getBundle(t *testing.T, env *testAgentdEnv, taskID, have string) (*http.Response, []byte) {
	t.Helper()
	url := env.ts.URL + "/api/tasks/" + taskID + "/bundle"
	if have != "" {
		url += "?have=" + have
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET bundle: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	return resp, body
}

// 正常薄包：200 + octet-stream + Content-Length 与实际字节数一致。
func TestHandleTaskBundleOK(t *testing.T) {
	env, taskID, _, base := newBundleEnv(t, "feat/x")

	resp, body := getBundle(t, env, taskID, base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码应为 200，实得 %d，体 %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type 应为 application/octet-stream，实得 %q", ct)
	}
	if len(body) == 0 {
		t.Fatal("包体不该为空")
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q 与实际字节数 %d 不符", got, len(body))
	}
}

// 无 have：全量包也必须成功，且比薄包大。
func TestHandleTaskBundleFull(t *testing.T) {
	env, taskID, _, base := newBundleEnv(t, "feat/x")

	respThin, thin := getBundle(t, env, taskID, base)
	if respThin.StatusCode != http.StatusOK {
		t.Fatalf("薄包应为 200，实得 %d", respThin.StatusCode)
	}
	respFull, full := getBundle(t, env, taskID, "")
	if respFull.StatusCode != http.StatusOK {
		t.Fatalf("全量包应为 200，实得 %d", respFull.StatusCode)
	}
	if len(full) <= len(thin) {
		t.Errorf("全量包应大于薄包，实得 full=%d thin=%d", len(full), len(thin))
	}
}

// 空区间：204，且不带包体——这是「本地已是最新」，不是失败。
func TestHandleTaskBundleEmptyRange(t *testing.T) {
	env, taskID, repo, _ := newBundleEnv(t, "feat/x")

	resp, body := getBundle(t, env, taskID, headSHAForTest(t, repo, "feat/x"))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("空区间应为 204，实得 %d，体 %s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Errorf("204 不该有包体，实得 %d 字节", len(body))
	}
}

// have 在任务仓库里不存在：400，报文带上那个 sha。
func TestHandleTaskBundleHaveMissing(t *testing.T) {
	env, taskID, _, _ := newBundleEnv(t, "feat/x")

	absent := "0123456789abcdef0123456789abcdef01234567"
	resp, body := getBundle(t, env, taskID, absent)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("have 不存在应为 400，实得 %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), absent) {
		t.Errorf("400 报文应带上该 sha 才能排障，实得 %s", body)
	}
}

// 任务尚无分支：400，而不是 500。
func TestHandleTaskBundleNoBranch(t *testing.T) {
	env, taskID, _, _ := newBundleEnv(t, "")

	resp, _ := getBundle(t, env, taskID, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("无分支应为 400，实得 %d", resp.StatusCode)
	}
}

// 任务不存在：404（byTask 已有的行为，这里锁住它不被新端点绕开）。
func TestHandleTaskBundleTaskNotFound(t *testing.T) {
	env, _, _, _ := newBundleEnv(t, "feat/x")

	resp, _ := getBundle(t, env, "no-such-task", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("任务不存在应为 404，实得 %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestHandleTaskBundle' -v`
Expected: 全部 FAIL，状态码为 404（路由尚未注册）

- [ ] **Step 3: 写 handler**

在 `internal/agentd/server.go` 里 `handleTaskDiff` 之后插入：

```go
// handleTaskBundle 吐任务分支的 git bundle，供协调者 pull 时不经 ssh 取回改动。
//
// 请求：GET /api/tasks/{id}/bundle?have=<sha>
//   - have 给了：生成 <have>..<branch> 的薄包（常态，通常几百字节）
//   - have 空：  生成全量包（协调者手上没有基线时的罕见退路）
//
// 响应：
//   - 200 application/octet-stream，带 Content-Length
//   - 204 区间为空，本地已是最新（**不是失败**：git bundle create 对空区间会
//     报错，而空区间天天发生——连着 pull 两次就是）
//   - 400 任务无分支 / have 在任务仓库中不存在 / 参数以 - 开头
//   - 404 任务不存在（byTask 已处理）
//   - 500 git 失败
//
// 注意：
//   - 用 task.RepoPath（主仓库）而不是 Workdir()：worktree 是主仓的从属工作树，
//     分支对象在主仓库里。这与 handleTaskDiff 不同，那个要的是工作树状态
//   - 先把包落临时文件再整体发出，**不**把 git 的输出直接流进 ResponseWriter：
//     直接流的话 git 中途失败时响应头早已发出，客户端收到的是一个截断的 200——
//     一次服务端故障被伪装成内容不完整的成功
func (s *Server) handleTaskBundle(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	have := r.URL.Query().Get("have")
	s.log.Info("bundle 请求", "method", r.Method, "path", r.URL.Path, "task", taskID, "have", have)
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return
	}
	if task.Branch == "" {
		s.log.Warn("任务尚无分支，无可打包", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务尚无分支，无可同步"})
		return
	}
	start := time.Now()
	path, err := BundleRange(r.Context(), task.RepoPath, have, task.Branch)
	switch {
	case errors.Is(err, ErrEmptyRange):
		// 空区间是预期形态，Info 说明它不是失败——否则运维看到 204 会去翻错误日志
		s.log.Info("bundle 区间为空，回 204", "task", taskID, "have", have, "branch", task.Branch)
		w.WriteHeader(http.StatusNoContent)
		return
	case errors.Is(err, ErrHaveMissing), errors.Is(err, ErrBadBaseBranch):
		// have 与 branch 都由请求侧决定，属请求问题不是服务故障（与 diff 的
		// ErrBadBaseBranch 同款映射）
		s.log.Warn("bundle 请求参数被拒", "task", taskID, "have", have, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	case err != nil:
		s.log.Error("生成 bundle 失败", "task", taskID, "repo", task.RepoPath,
			"branch", task.Branch, "have", have, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	defer os.Remove(path)

	f, err := os.Open(path)
	if err != nil {
		s.log.Error("打开生成的 bundle 失败", "task", taskID, "path", path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		s.log.Error("读取 bundle 大小失败", "task", taskID, "path", path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	n, err := io.Copy(w, f)
	if err != nil {
		// 头已经发出去了，改不了状态码——只能把事实记下来。客户端会因为
		// 收到的字节数与 Content-Length 不符而失败，这是它该失败的方式
		s.log.Error("发送 bundle 中断", "task", taskID, "sent", n, "total", fi.Size(), "cause", err)
		return
	}
	s.log.Info("bundle 发送完成", "task", taskID, "branch", task.Branch, "have", have,
		"bytes", n, "elapsed_ms", time.Since(start).Milliseconds())
}
```

- [ ] **Step 4: 注册路由**

在 `internal/agentd/server.go` 的 `api.HandleFunc("GET /api/tasks/{id}/file", s.byTask(s.handleTaskFile))` 之后加一行：

```go
	api.HandleFunc("GET /api/tasks/{id}/bundle", s.byTask(s.handleTaskBundle))
```

同时在文件顶部那段路由清单注释里（`//   - GET  /api/tasks/{id}/file         读任务仓库内文件（审阅上下文）` 一行之后）补上：

```go
//   - GET  /api/tasks/{id}/bundle       任务分支的 git bundle（回程 pull，不经 ssh）
```

确认 `server.go` 的 import 块里有 `errors`、`io`、`os`、`strconv`、`time`；缺哪个补哪个。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestHandleTaskBundle' -v`
Expected: 5 个用例全 PASS

- [ ] **Step 6: 关键节点日志自查**

- 请求进入打 Info，带 `task` / `have`（`path` 与 `method` 沿用本包 handler 的既有形态）
- 无分支、参数被拒各打 Warn 带上下文
- 三个 500 分支各打 Error 带 `cause`
- 204 单独一条 Info 明说是空区间而非失败
- 成功打 Info 带 `bytes` 与 `elapsed_ms`
- 发送中断打 Error 带 `sent` / `total`——**这条最容易漏**：`io.Copy` 失败后无法改状态码，日志是唯一痕迹
- 全程 `s.log`，无 `fmt.Printf`；**不打 token、不打包内容**

- [ ] **Step 7: 注释自查**

- `handleTaskBundle` 有完整 doc 注释：请求形态、五个状态码的含义、两条「注意」（用 RepoPath 不用 Workdir、为什么先落盘不流式）
- 路由清单注释已补该端点
- `io.Copy` 失败分支有「头已发出改不了状态码」的为什么注释

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/server.go internal/agentd/bundle_handler_test.go
git commit -m "feat(agentd): 新增 GET /api/tasks/{id}/bundle 端点"
```

---

### Task 3: 客户端 Bundle 方法

**Files:**
- Create: `internal/client/bundle.go`
- Test: `internal/client/bundle_test.go`

**Interfaces:**
- Consumes: `(c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error)`、`(c *Client) httpError(op string, resp *http.Response) error`、`c.log() *slog.Logger`，均在 `internal/client/client.go`。
- Produces: `func (c *Client) Bundle(ctx context.Context, taskID, have string) (rc io.ReadCloser, empty bool, err error)`、`var ErrBundleUnsupported error`。

- [ ] **Step 1: 写失败的测试**

新建 `internal/client/bundle_test.go`：

```go
package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 200：包体原样交给调用方，empty 为 false。
func TestBundleOK(t *testing.T) {
	want := []byte("PACK-fake-bundle-bytes")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("have"); got != "abc123" {
			t.Errorf("have 应透传，实得 %q", got)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	}))
	defer ts.Close()

	rc, empty, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "abc123")
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if empty {
		t.Fatal("200 时 empty 应为 false")
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != string(want) {
		t.Errorf("包体应原样返回，实得 %q", got)
	}
}

// 204：empty 为 true，rc 为 nil，err 为 nil——这是「已是最新」，不是失败。
func TestBundleEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	rc, empty, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
	if err != nil {
		t.Fatalf("204 不该是错误，实得 %v", err)
	}
	if !empty {
		t.Error("204 时 empty 应为 true")
	}
	if rc != nil {
		t.Error("204 时不该返回可读流")
	}
}

// 404：翻成 ErrBundleUnsupported（对端过旧这一**结论**），不是普通错误。
// 承重：cmd 层只对这个哨兵回落 ssh。
func TestBundleUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()

	_, _, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
	if !errors.Is(err, ErrBundleUnsupported) {
		t.Fatalf("404 应翻成 ErrBundleUnsupported，实得 %v", err)
	}
}

// 400 / 500：普通错误，**绝不能**被误判成 ErrBundleUnsupported——否则
// cmd 层会把一次真失败当成「对端过旧」而回落 ssh，把问题藏起来。
func TestBundleOtherStatusIsNotUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		_, _, err := New(ts.URL, "tok").Bundle(context.Background(), "t1", "")
		ts.Close()
		if err == nil {
			t.Errorf("状态码 %d 应返回错误", status)
			continue
		}
		if errors.Is(err, ErrBundleUnsupported) {
			t.Errorf("状态码 %d 不该被当成对端过旧", status)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/client/ -run 'TestBundle' -v`
Expected: 编译失败，`c.Bundle undefined` / `undefined: ErrBundleUnsupported`

- [ ] **Step 3: 写实现**

新建 `internal/client/bundle.go`：

```go
// 本文件是 agentd 的 GET /api/tasks/{id}/bundle 端点的客户端侧封装。
//
// 职责：
//   - 把 HTTP 状态码翻成调用方能分诊的三种结果：拿到包 / 区间为空 / 对端过旧
//
// 边界：
//   - 不落盘、不调 git：把字节流原样交给调用方（cmd/pull.go 负责落临时文件再 fetch）
//   - 不做回落决策：回落与否是 cmd 层的事，本层只提供可判别的哨兵
package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ErrBundleUnsupported 表示对端 agentd 不认识 /api/tasks/{id}/bundle（版本早于该端点引入）。
//
// why（必须是可判别的哨兵）：与 ErrStatusUnsupported 同一条纪律——**404 是结论，
// 其它是故障**。能收到 404 说明 TCP 通、HTTP 正常、Bearer 已经通过。调用方据此
// 退回 ssh 老路；换成对任何错误都回落，就会把一次真失败伪装成「老路也能跑」。
//
// 为什么 404 不会与「任务不存在」混淆：byTask 对不存在的任务也回 404，但
// pull 的第一步 client.Attach(taskID) 成功返回才轮到 Bundle——**任务存在已被
// 上一次请求证明**，所以这里的 404 只能来自路由缺失。实现不去比对响应体文案，
// 那同样是把判据建在字符串上。
var ErrBundleUnsupported = errors.New("对端 agentd 不支持 /api/tasks/{id}/bundle")

// Bundle 取任务分支的 git bundle 字节流。
//
// 参数：
//   - taskID: 完整任务 UUID
//   - have:   协调者本地已有的基线提交；空串请求全量包
//
// 返回：
//   - rc:    bundle 字节流，**调用方负责 Close**；empty 为 true 时是 nil
//   - empty: 对端回了 204，即 have..branch 为空区间，本地已是最新
//   - err:   404 → ErrBundleUnsupported（对端过旧，调用方应回落）；其余为故障
//
// 注意：
//   - 成功路径**不能** defer resp.Body.Close()：Body 就是返回给调用方的 rc
func (c *Client) Bundle(ctx context.Context, taskID, have string) (io.ReadCloser, bool, error) {
	path := "/api/tasks/" + url.PathEscape(taskID) + "/bundle"
	if have != "" {
		path += "?have=" + url.QueryEscape(have)
	}
	c.log().Debug("请求任务 bundle", "task", taskID, "have", have)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, false, fmt.Errorf("bundle 请求: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		c.log().Debug("bundle 响应就绪", "task", taskID, "content_length", resp.ContentLength)
		return resp.Body, false, nil
	case http.StatusNoContent:
		resp.Body.Close()
		c.log().Debug("对端报区间为空，本地已是最新", "task", taskID, "have", have)
		return nil, true, nil
	case http.StatusNotFound:
		// 不走 httpError：它会打 Warn 并造出一个普通错误，而这里的 404 是一条
		// 有用的结论，不是异常（与 Status 的 ErrStatusUnsupported 同款处置）
		resp.Body.Close()
		c.log().Debug("对端 agentd 不支持 bundle 端点，按版本过旧处理", "task", taskID)
		return nil, false, ErrBundleUnsupported
	default:
		defer resp.Body.Close()
		return nil, false, c.httpError("取 bundle", resp)
	}
}
```

> import 块需要 `errors`（`ErrBundleUnsupported` 用），写进去。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/client/ -run 'TestBundle' -v`
Expected: 4 个用例全 PASS

- [ ] **Step 5: 关键节点日志自查**

- 请求前 Debug 带 `task` / `have`（Debug 而非 Info：`wait` 的自动同步是热路径，库层打 Info 会污染 CLI 的 stderr——与 `Status` 的 404 降级到 Debug 同一权衡；**面向人的那一行走哪条路由的日志在 Task 4 的 cmd 层打**）
- 三个非故障分支（200 / 204 / 404）各一条 Debug 说明结论
- 故障分支由 `c.httpError` 统一按状态码分级记录（5xx→Error，4xx→Warn），不重复打
- 无 `fmt.Printf`；**不打 token、不打包内容**（只打 `content_length`）

- [ ] **Step 6: 注释自查**

- 文件头有职责 + 边界（明确「不落盘、不调 git」「不做回落决策」）
- `ErrBundleUnsupported` 的 doc 注释写清「404 是结论、其它是故障」以及**为什么不会与任务不存在混淆**
- `Bundle` 的 doc 注释写清三返回值语义与「成功路径不能 defer Close」这个坑

- [ ] **Step 7: Commit**

```bash
git add internal/client/bundle.go internal/client/bundle_test.go
git commit -m "feat(client): 新增 Bundle 方法，404 翻成 ErrBundleUnsupported"
```

---

### Task 4: pull 的降级阶梯

**Files:**
- Modify: `cmd/pull.go`（改写 `syncTaskBranch`，新增 `syncViaBundle` 与 `hasLocalCommit`）
- Test: `cmd/pull_test.go`

**Interfaces:**
- Consumes: Task 3 的 `client.Bundle(ctx, taskID, have) (io.ReadCloser, bool, error)` 与 `client.ErrBundleUnsupported`；既有的 `localsync.Fetch(ctx, localsync.Opts{LocalRepo, RemoteURL, Branch}) (localsync.Result, error)`、`localsync.Result{Branch string; Commits int; Created bool}`、`loadCLIConfig()`、`sshHostFromTarget(t config.Target) string`、`client.New(addr, token) *client.Client`。
- Produces: `syncTaskBranch` 的签名与返回类型**不变**（`func syncTaskBranch(ctx context.Context, task *proto.Task) (localsync.Result, error)`），`wait` 的调用点因此不用改。

- [ ] **Step 1: 写失败的测试**

新建 `cmd/pull_test.go`：

```go
package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newPullRepo 建一个本地仓库（只有 base 提交），返回路径与 base sha。
func newPullRepo(t *testing.T) (repo, baseSHA string) {
	t.Helper()
	repo = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("写 base.txt: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	return repo, run("rev-parse", "HEAD")
}

// hasLocalCommit：本地有的报 true，本地没有的报 false，非法输入报 false 而不是崩。
func TestHasLocalCommit(t *testing.T) {
	repo, base := newPullRepo(t)
	if !hasLocalCommit(context.Background(), repo, base) {
		t.Error("本地确实有 base，应报 true")
	}
	if hasLocalCommit(context.Background(), repo, "0123456789abcdef0123456789abcdef01234567") {
		t.Error("本地没有的 sha 应报 false")
	}
	if hasLocalCommit(context.Background(), repo, "") {
		t.Error("空 sha 应报 false")
	}
	if hasLocalCommit(context.Background(), repo, "--upload-pack=x") {
		t.Error("- 前缀应报 false，不许当 git 选项送进去")
	}
}

// 承重反面断言：对端回 500 时必须报错，且**不得**回落 ssh。
//
// 少了这条，把「其它错误也回落」写回去照样能过——那会把一次真失败伪装成
// 「老路也能跑」，正是 spec §6 要守住的东西。
// 判据：ssh 回落必然去拨一个 ssh 主机；测试里那个 target 的 Addr 指向本测试
// 的 httptest 服务器，ssh 过去必然失败且报文里带 ssh 的痕迹。所以断言错误里
// **有 500 的痕迹、没有 ssh 的痕迹**。
func TestSyncViaBundleDoesNotFallBackOn500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"git 炸了"}`))
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	_, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if err == nil {
		t.Fatal("500 必须报错")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("错误应带上 500 的事实，实得 %v", err)
	}
	if strings.Contains(err.Error(), "ssh") || strings.Contains(err.Error(), "Host key") {
		t.Errorf("500 不该触发 ssh 回落，实得 %v", err)
	}
}

// 204：合成「已是最新」的结果，不报错、不 fetch。
func TestSyncViaBundleEmptyRange(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	res, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if err != nil {
		t.Fatalf("204 不该是错误：%v", err)
	}
	if res.Created || res.Commits != 0 {
		t.Errorf("空区间应是「已是最新」，实得 %+v", res)
	}
	if res.Branch != "feat/x" {
		t.Errorf("Branch 应回填为 feat/x，实得 %q", res.Branch)
	}
}

// 404：原样返回 client.ErrBundleUnsupported，由上层决定回落。
// syncViaBundle 自己**不**做回落——职责边界。
func TestSyncViaBundlePropagatesUnsupported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer ts.Close()
	repo, base := newPullRepo(t)

	_, err := syncViaBundle(context.Background(), ts.URL, "tok", "task-1", base, "feat/x", repo)
	if !errorsIsBundleUnsupported(err) {
		t.Fatalf("404 应原样传出 ErrBundleUnsupported，实得 %v", err)
	}
}
```

在同文件末尾加一个小助手（避免测试文件为了一个判断多导入一个包时被 lint 挑剔，写成显式函数更清楚）：

```go
func errorsIsBundleUnsupported(err error) bool {
	return errors.Is(err, client.ErrBundleUnsupported)
}
```

> 相应地测试文件的 import 块需要 `errors` 与 `github.com/Xsxdot/handoff/internal/client`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run 'TestHasLocalCommit|TestSyncViaBundle' -v`
Expected: 编译失败，`undefined: hasLocalCommit` / `undefined: syncViaBundle`

- [ ] **Step 3: 改写 pull.go**

把 `cmd/pull.go` 的文件头注释改成：

```go
// 本文件实现 handoff pull 子命令：把远程执行机上的任务分支同步到本地仓库。
//
// 职责：
//   - 查任务拿到 target/仓库路径/分支，取回任务分支的提交并 fetch 到本地同名分支
//   - 决定走哪条路：优先经 agentd 的 HTTP 面取 git bundle，仅在对端过旧（404）时
//     退回 ssh 老路
//
// 边界：
//   - 只 fetch，不 checkout、不合并（合并是协调者的决定）
//   - 本机任务（无 target）无需同步：代码本来就在同一台机器上
//   - 不做 git bundle 的生成：那在 agentd 侧（internal/agentd/bundle.go）
package cmd
```

把 `syncTaskBranch` 整体替换为：

```go
// syncTaskBranch 把任务的远程分支同步到本地 cwd 仓库。
//
// 参数：
//   - task: 任务快照（需要 ID / Target / RepoPath / Branch / BaseCommit 五个字段）
//
// 返回：
//   - 同步结果；任务不是远程任务、缺分支、target 未配置或取回失败时返回错误
//
// 注意：
//   - **降级只对 404 发生**：对端 agentd 过旧（没有 bundle 端点）才退回 ssh；
//     其它错误如实报错。对任何错误都回落会把一次真失败伪装成「老路也能跑」
//   - ssh 老路的远程地址由 sshHostFromTarget(target 配置) 与 task.RepoPath 拼成
//     host:/path。它在 Windows 执行机上不可用（git 的 ssh transport 假定远端登录
//     shell 是 POSIX，cmd.exe 不剥单引号），保留它只为兼容尚未换版的老 agentd
//   - 用 RepoPath 而不是 Workdir()：worktree 是主仓的从属工作树，分支对象在主仓库里
func syncTaskBranch(ctx context.Context, task *proto.Task) (localsync.Result, error) {
	if task.Target == "" {
		return localsync.Result{}, fmt.Errorf("任务 %s 是本机任务，无需同步", task.ID)
	}
	if task.Branch == "" {
		return localsync.Result{}, fmt.Errorf("任务 %s 尚无分支，无可同步", task.ID)
	}
	cfg := loadCLIConfig()
	t, ok := cfg.Targets[task.Target]
	if !ok {
		return localsync.Result{}, fmt.Errorf("target %q 未在配置中定义，无法换算远程地址", task.Target)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return localsync.Result{}, fmt.Errorf("取当前目录: %w", err)
	}

	// have 取任务记录里的基线，但**发出前先在本地核实自己真有**——协调者可能换了
	// 机器接管、或在另一个克隆里执行 pull，不能假设它持有这个提交。核实不过就不带
	// have，服务端给全量包
	have := ""
	if hasLocalCommit(ctx, cwd, task.BaseCommit) {
		have = task.BaseCommit
	}
	// 用 task.Target 对应的配置而不是 --target 标志：任务自己知道它在哪台机器上
	res, err := syncViaBundle(ctx, "http://"+t.Addr, t.Token, task.ID, have, task.Branch, cwd)
	if err == nil {
		slog.Default().Info("回程同步走 agentd HTTP bundle",
			"task", task.ID, "target", task.Target, "branch", task.Branch, "have", have)
		return res, nil
	}
	if !errors.Is(err, client.ErrBundleUnsupported) {
		return localsync.Result{}, err
	}
	// 到这里只可能是 404。把「为什么回落」一并写出来——排障时第一个要问的就是
	// 「这次走的哪条路」，只说「用了 ssh」等于没说
	slog.Default().Info("对端 agentd 无 bundle 端点（404），回程同步回落 ssh 老路",
		"task", task.ID, "target", task.Target, "branch", task.Branch)
	return localsync.Fetch(ctx, localsync.Opts{
		LocalRepo: cwd, RemoteURL: sshHostFromTarget(t) + ":" + task.RepoPath, Branch: task.Branch,
	})
}

// syncViaBundle 经 agentd 的 HTTP 面取 bundle 并 fetch 进本地仓库。
//
// 参数：
//   - addr, token: agentd 端点与 Bearer token
//   - taskID:      完整任务 UUID
//   - have:        本地已核实存在的基线提交；空串请求全量包
//   - branch:      任务分支名
//   - localRepo:   本地仓库路径（fetch 的落点）
//
// 返回：
//   - 同步结果；空区间时返回 Result{Branch: branch}（即「已是最新」）
//   - err 为 client.ErrBundleUnsupported 时表示对端过旧，**由调用方**决定回落；
//     本函数自己不回落
//
// 注意：
//   - bundle 落 OS 临时目录并 defer os.Remove，**绝不能落在 localRepo 里**——
//     那会弄脏协调者的工作区，而干净工作区是 dispatch 的前置条件
//   - 包拿到了但 fetch 失败时如实报错、不回落：包已到手说明 HTTP 这条路是通的，
//     失败在 git 侧（如缺前置对象），换 ssh 重来只会掩盖它
func syncViaBundle(ctx context.Context, addr, token, taskID, have, branch, localRepo string) (localsync.Result, error) {
	rc, empty, err := client.New(addr, token).Bundle(ctx, taskID, have)
	if err != nil {
		return localsync.Result{}, err
	}
	if empty {
		slog.Default().Info("远端无新提交，本地已是最新", "task", taskID, "branch", branch)
		return localsync.Result{Branch: branch}, nil
	}
	defer rc.Close()

	f, err := os.CreateTemp("", "handoff-bundle-*.bundle")
	if err != nil {
		return localsync.Result{}, fmt.Errorf("创建 bundle 临时文件: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	n, copyErr := io.Copy(f, rc)
	closeErr := f.Close()
	if copyErr != nil {
		slog.Default().Error("下载 bundle 失败", "task", taskID, "received", n, "cause", copyErr)
		return localsync.Result{}, fmt.Errorf("下载 bundle（已收 %d 字节）: %w", n, copyErr)
	}
	if closeErr != nil {
		return localsync.Result{}, fmt.Errorf("写入 bundle 临时文件: %w", closeErr)
	}
	slog.Default().Info("bundle 下载完成", "task", taskID, "branch", branch, "bytes", n)

	// git 把 bundle 文件当作一种合法 transport，所以这里把文件路径直接当 RemoteURL
	// 交给现有的 localsync.Fetch——它的文档注释里就写着「也接受本地路径」。
	// 这正是 localsync 一行都不用改的原因
	return localsync.Fetch(ctx, localsync.Opts{LocalRepo: localRepo, RemoteURL: path, Branch: branch})
}

// hasLocalCommit 报告本地仓库里是否已有该提交对象。
//
// 参数：
//   - repo: 本地仓库路径
//   - sha:  完整或缩写的提交号
//
// 返回：true 表示对象存在且是一个 commit；空 sha、以 - 开头的 sha、仓库不可用
// 一律返回 false（不是错误——「没有」是这里的正常答案）。
//
// 为什么拒绝 - 前缀：git 会把它解释为选项，属参数注入面（与 agentd 侧
// ErrBadBaseBranch 同源）。
func hasLocalCommit(ctx context.Context, repo, sha string) bool {
	if repo == "" || sha == "" || strings.HasPrefix(sha, "-") {
		return false
	}
	err := exec.CommandContext(ctx, "git", "-C", repo, "cat-file", "-e", sha+"^{commit}").Run()
	if err != nil {
		slog.Default().Debug("本地无该基线提交，将请求全量包", "repo", repo, "sha", sha)
		return false
	}
	return true
}
```

`cmd/pull.go` 的 import 块改为：

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/localsync"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -run 'TestHasLocalCommit|TestSyncViaBundle' -v`
Expected: 4 个用例全 PASS

- [ ] **Step 5: 跑 cmd 包全量回归**

Run: `go test ./cmd/`
Expected: ok（`syncTaskBranch` 签名没变，`wait` 的调用点不受影响）

- [ ] **Step 6: 关键节点日志自查**

- **走了哪条路必须可见**：bundle 成功一条 Info、回落 ssh 一条 Info 且带上「404」这个事实
- 空区间一条 Info 说明「已是最新」
- 下载完成一条 Info 带 `bytes`；下载失败一条 Error 带 `received` 与 `cause`
- `hasLocalCommit` 判 false 时一条 Debug 说明「将请求全量包」——它是「包为什么这么大」的唯一线索
- 全程 `slog.Default()`；`fmt.Fprintln(cmd.OutOrStdout(), …)` 是给人看的命令输出，保留，不算日志
- **不打 token、不打包内容**

- [ ] **Step 7: 注释自查**

- 文件头注释已更新，边界写明「不做 bundle 生成」
- `syncTaskBranch` / `syncViaBundle` / `hasLocalCommit` 三处 doc 注释齐备
- 三处「为什么」注释在位：为什么先核实本地 have、为什么只对 404 回落、为什么能把文件路径当 RemoteURL

- [ ] **Step 8: Commit**

```bash
git add cmd/pull.go cmd/pull_test.go
git commit -m "feat(pull): 回程同步优先走 HTTP bundle，仅 404 回落 ssh"
```

---

### Task 5: 端到端证明与文档

**Files:**
- Create: `internal/agentd/bundle_e2e_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: Task 1 的 `BundleRange` 与测试辅助 `newBundleRepo` / `headSHAForTest`；Task 2 的 `handleTaskBundle` 路由与测试辅助 `newBundleEnv` / `getBundle`。
- Produces: 无新导出符号。

- [ ] **Step 1: 写失败的端到端测试**

新建 `internal/agentd/bundle_e2e_test.go`：

```go
package agentd

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 端到端：真 handler 出包 → 把包 fetch 进第二个仓库 → 断言那个 commit 真的到了。
//
// 这条是承重的：状态码对了只说明 HTTP 层对了，证明不了这条链路真能搬运 git 对象。
// 不需要网络，全在 t.TempDir() 里。
func TestBundleEndToEndCarriesCommit(t *testing.T) {
	env, taskID, remote, base := newBundleEnv(t, "feat/x")
	wantSHA := headSHAForTest(t, remote, "feat/x")

	// 协调者侧：一个只有 main 的本地仓库（模拟「我有基线、没有任务分支」）。
	//
	// **`--no-local` 是承重的**：同机克隆走 git 的 local 优化，会硬链接整个对象库，
	// 于是 `--single-branch` 只限制了 refs、feat/x 的提交照样在本地——前置条件当场
	// 不成立。这条在基线上实测过：不加 --no-local 时 cat-file -e 报 YES。
	local := t.TempDir()
	gitClone(t, remote, local)
	if hasCommitInDir(t, local, wantSHA) {
		t.Fatal("前置条件不成立：本地此时不该已有 feat/x 的提交")
	}

	// 取包
	resp, body := getBundle(t, env, taskID, base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("取包应为 200，实得 %d，体 %s", resp.StatusCode, body)
	}
	// 包落到独立的临时目录，**不能**落进 local——那会弄脏被 fetch 的仓库
	bundlePath := filepath.Join(t.TempDir(), "task.bundle")
	if err := os.WriteFile(bundlePath, body, 0o644); err != nil {
		t.Fatalf("落盘 bundle: %v", err)
	}

	// 把包当 transport fetch 进本地仓库
	gitInDir(t, local, "fetch", bundlePath, "feat/x:feat/x")

	if !hasCommitInDir(t, local, wantSHA) {
		t.Fatalf("commit %s 应已被 bundle 搬到本地", wantSHA)
	}
	if got := strings.TrimSpace(gitInDir(t, local, "rev-parse", "feat/x")); got != wantSHA {
		t.Errorf("本地 feat/x 应指向 %s，实得 %s", wantSHA, got)
	}
}

// gitClone 把 remote 只克隆 main 分支到 dst（dst 须已存在且为空）。
//
// --no-local 强制走 git 传输协议而非硬链接对象库，见调用点的注释。
func gitClone(t *testing.T, remote, dst string) {
	t.Helper()
	c := exec.Command("git", "clone", "--quiet", "--no-local",
		"--branch", "main", "--single-branch", remote, dst)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
}

// gitInDir 在 dir 里跑 git，失败即 t.Fatal，返回 stdout+stderr。
func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// hasCommitInDir 报告 dir 里是否已有该 commit 对象。
func hasCommitInDir(t *testing.T, dir, sha string) bool {
	t.Helper()
	return exec.Command("git", "-C", dir, "cat-file", "-e", sha+"^{commit}").Run() == nil
}
```

- [ ] **Step 2: 跑测试确认它先失败在正确的地方**

Run: `go test ./internal/agentd/ -run 'TestBundleEndToEndCarriesCommit' -v`
Expected: 如果 Task 1/2 已完成，这条应当**直接通过**——它是链路证明而非驱动新代码。若失败，失败点必须在 `fetch` 或最终断言（说明链路真有问题），而不是在编译或 404（那说明前两个 task 没做完，回头修）。

- [ ] **Step 3: 跑整包回归**

Run: `go test ./internal/agentd/ ./internal/client/ ./cmd/`
Expected: 三个包全 ok

- [ ] **Step 4: 更新 README**

在 `README.md` 里 `handoff pull <task>` 相关的说明处（第 265 行附近那段 “**fetch only, no merge**”）之后补一段：

```markdown
`pull` 经 agentd 的 HTTP 面取一个 git bundle 再 fetch 到本地——**不需要执行机上有
ssh，也不需要远端登录 shell 是 POSIX**。对端 agentd 太旧（没有
`GET /api/tasks/{id}/bundle`，返回 404）时自动退回旧的 ssh 路径；此时执行机仍需
可 ssh 且登录 shell 为 POSIX（Windows 的 cmd.exe 不满足）。客户端日志会写明本次
走的是哪条路。
```

并在 `README.md` 的 API 端点清单里（如有）加一行 `GET /api/tasks/{id}/bundle`。若 README 没有端点清单，跳过这半句——**不要为此新造一个章节**。

- [ ] **Step 5: 跑 gofmt 与 vet**

Run:
```bash
gofmt -l . && go vet ./...
```
Expected: `gofmt -l` 无输出（有输出就 `gofmt -w` 那些文件并重跑），`go vet` 无告警。

> 这一步不是形式主义：executor 的 ledger 历史上漏过 gofmt，「测试全绿」不等于「格式干净」。

- [ ] **Step 6: 跑全仓测试**

Run: `go test ./...`
Expected: 全部 ok。有失败就贴原始报错原文，不要替它归因。

- [ ] **Step 7: Commit**

```bash
git add internal/agentd/bundle_e2e_test.go README.md
git commit -m "test(agentd): bundle 端到端证明链路能搬运 commit；README 记录新回程路径"
```

---

## 审核者专属：真机验收（**不要执行这一节**）

> **给执行者**：这一节由协调者在本地执行，**不属于你的任务范围**。它需要驱动
> handoff 自身（派发任务、调 handoff CLI、操作 ssh 隧道），与执行纪律块「不要派发、
> 不要调用 handoff CLI」直接冲突。跳过它，把上面 5 个 task 做完即可。

留在协调者本地的验收清单（spec §11）：

1. 向 win-b37 派一个会产生提交的任务 → `completed`
2. `handoff pull --target win-b37 <完整 id>` → 分支真的落到本地，`git log <分支>` 见到那个 commit
3. 再 pull 一次 → 报「已是最新」而不是 500
4. 任务结束时 `wait` 的自动同步不再打「自动同步跳过: …… Host key verification failed」
5. **对照组**：mac-02 仍是老 agentd，pull 应仍走 ssh 且成功，客户端日志明确写出走的是回落

---

## 自审记录

**1. spec 覆盖**

| spec 章节 | 落在哪 |
|---|---|
| §3.1 端点形态、落临时文件、用 RepoPath | Task 1 Step 3、Task 2 Step 3/4 |
| §3.2 localsync 一行不改、客户端四步流程 | 全局约束 3；Task 4 Step 3 的 `syncViaBundle` |
| §3.3 承重前提 | Task 1 与 Task 5 的测试就是这些前提的回归锁 |
| §4 基线协商（have 取 BaseCommit、发出前本地核实、服务端反向校验 400） | Task 4 的 `hasLocalCommit`；Task 1 的 `ErrHaveMissing`；Task 2 的 400 映射 |
| §5 空区间 204、判据用 rev-list --count | 全局约束 2；Task 1 Step 3；Task 2 的 204 分支 |
| §6 降级阶梯 + 404 歧义排除 | Task 3 的 `ErrBundleUnsupported`；Task 4 的 `errors.Is` 分支与反面断言 |
| §7 保留 ssh 路径 | Task 4 保留了 `localsync.Fetch` + `sshHostFromTarget` 分支 |
| §8 不设上限、两侧记字节数、临时文件落 OS 临时目录 | 全局约束 5/6；Task 1、Task 2、Task 4 各自的 `bytes` 日志 |
| §9 日志纪律 | 全局约束 7；每个 task 的「关键节点日志自查」步 |
| §10 测试策略四条 | Task 1/2（第 1 条）、Task 3（第 2 条）、Task 4（第 3 条，承重）、Task 5（第 4 条，承重）|
| §11 真机验收 | 「审核者专属」一节，明确不派发 |
| §12 已知边界 | 不需要代码；§12.3 的遗留由 Task 4 的注释记下 |
| §13 影响面（不改已有签名）| 全局约束 8；Task 4 的 Interfaces 明写 `syncTaskBranch` 签名不变 |

无缺口。

**2. 占位符扫描**

无 TBD / TODO / 「类似 Task N」/「加上适当的错误处理」。每个代码步都给了可直接落盘的实际代码。两处「按现状调整」是有意为之且给了 `grep` 命令（Task 2 Step 1 的测试辅助名、Task 5 Step 4 的 README 端点清单），不是让实现者自由发挥。

**3. 类型一致性**

- `BundleRange(ctx, repo, have, branch) (string, error)` —— Task 1 定义，Task 2 调用，参数序一致
- `ErrEmptyRange` / `ErrHaveMissing` / `ErrBadBaseBranch` —— Task 1 产出，Task 2 `errors.Is` 消费
- `Client.Bundle(ctx, taskID, have) (io.ReadCloser, bool, error)` —— Task 3 定义，Task 4 调用，返回序 `(rc, empty, err)` 一致
- `client.ErrBundleUnsupported` —— Task 3 产出，Task 4 与其测试消费
- `syncViaBundle(ctx, addr, token, taskID, have, branch, localRepo)` —— Task 4 定义，其测试按同序调用（7 参）
- `hasLocalCommit(ctx, repo, sha) bool` —— Task 4 定义与测试一致
- `newBundleRepo(t) (repo, baseSHA string)` / `headSHAForTest(t, repo, ref)` / `newBundleEnv(t, branch) (*testAgentdEnv, taskID, repo, baseSHA string)` / `getBundle(t, env, taskID, have) (*http.Response, []byte)` —— 在 agentd 包内跨三个测试文件共用，定义各只有一处（前两个在 `bundle_test.go`，后两个在 `bundle_handler_test.go`），Task 5 只消费不重定义。**这四个辅助连同 Task 2 的六个用例已在基线上编译通过一次**（用 stub 版 `BundleRange`/`handleTaskBundle` 走 `go vet ./internal/agentd/`），签名不是推断出来的
- `localsync.Result{Branch, Commits, Created}` —— 只读不改，字段名与现状一致
