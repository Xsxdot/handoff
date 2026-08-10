# dispatch 基线决议 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `handoff dispatch` 新建的任务分支从**审核者派发时看到的那个提交**开出，而不是任务仓库当时恰好停在的 HEAD；并让这个起点事后可查、分叉不静默。

**Architecture:** 把 `EnsureBaseCommit`（只回答「这个 commit 存不存在」）升级为 `ResolveBaseline`（同时给出「校验结论」与「新分支起点」）——两者出自同一次计算，这是消除 B35 语义断裂的核心。同时订正 `PrepareWorkspace` 的第 1 层校验规则，让自动分支 `handoff/<id8>` 也能带起点。起点与「任务仓库领先多少提交」随 `proto.Task` 落库，dispatch 在 stderr 打一行人读摘要。

**Tech Stack:** Go 1.x（标准库 + `os/exec` 调 git）、SQLite（`internal/store`）、cobra CLI。无新依赖。

**Spec:** [docs/superpowers/specs/2026-08-10-dispatch-baseline-design.md](../specs/2026-08-10-dispatch-baseline-design.md)

## Global Constraints

- 语言：全部注释、日志、错误文案用中文（与全仓一致）。
- 日志：一律 `log()`（workspace.go）或 `m.log`（manager.go）；**禁止** `fmt.Printf`。成功路径也要打日志。
- 新建文件写文件头注释（职责 + 边界）；导出函数写 doc 注释（参数/返回/注意）；非显然分支写「为什么」的行内注释。
- `proto.Task` 是 JSON 线格式契约：**只准新增字段，不准改名/改形状**，key 一律 snake_case 小写。
- `dispatch` 的 **stdout 契约不变**：仍是单行任务 JSON。新增的人读信息一律走 **stderr**。
- git 参数注入面：任何进入 git 命令行的值都不得以 `-` 开头（既有 `PrepareWorkspace` 第 1 层已覆盖，不要削弱）。
- 提交信息结尾附 `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`。

## 相对 spec 的两处决定（spec 未写死，此处定死）

1. **`Baseline.Ahead` 怎么过线到 CLI。** spec §4 要求 dispatch stderr 打出「领先 N 个提交」，但没说 N 怎么从 agentd 传到审核者终端——dispatch 的响应体就是任务 JSON，没有别的通道。本计划的决定：`proto.Task` 同时新增 `BaseCommit` 与 `BaseAhead` 两个字段并双双入库。多存一个 int 的代价极小，换来的是「派发当时任务仓库比起点多几个提交」这条**事后可查的取证记录**——正是 08-10 那次事故里谁都答不上来的那个数字。
2. **既有测试用例 `"base 无 new-branch"` 必须改写。** `workspace_test.go` 的 `TestPrepareWorkspaceMutualExclusionAndInjection` 里有一条 `{Repo: repo, TaskID: "t", Base: "HEAD~1"}` 断言「自动分支带 Base 应拒发」。规则订正后这个组合**变成合法的**，该用例必须替换为新规则下的非法组合（`Branch` + `Base`），否则它会把本次修复本身判为失败。

---

### Task 1: 放行自动分支的起点（规则订正）

这是 spec §6 点名的锚：先让回归测试红，证明洞真实存在，再改规则让它绿。

**Files:**
- Modify: `internal/agentd/workspace.go:120-136`（`WorkspaceReq` 的 doc 与 `Base` 字段注释）
- Modify: `internal/agentd/workspace.go:150-169`（`PrepareWorkspace` doc 的行为表）
- Modify: `internal/agentd/workspace.go:187-189`（第 1 层校验规则）
- Test: `internal/agentd/workspace_test.go`（新增 1 条回归测试 + 改写 1 条既有用例）

**Interfaces:**
- Consumes: 无（本任务不依赖前置任务）
- Produces: `PrepareWorkspace` 在 `WorkspaceReq{Base: <sha>}` 且 `Branch == ""` 时，把 `<sha>` 作为新分支起点传给 git；`Base` 与 `Branch` 同时非空时返回 `ErrBadWorkspaceReq`。

- [ ] **Step 1: 写失败的回归测试**

追加到 `internal/agentd/workspace_test.go` 末尾：

```go
// TestPrepareWorkspaceAutoBranchHonorsBase 是 B35 的回归锚点：自动分支
// handoff/<id8> 必须能从指定起点开出，而不是任务仓库当时的 HEAD。
//
// 为什么这条必须存在：B35 的现场就是「校验的基线是 A、分支实际开在 B」，
// 而这个差异在任何输出里都不留痕迹——只有断言 worktree 的 HEAD 等于起点，
// 才能证明校验结论真的被用上了。
func TestPrepareWorkspaceAutoBranchHonorsBase(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "later.txt", "x") // 主仓 HEAD 前进一格，与 base 拉开距离
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Base: base,
		NewWorktree: true, WorktreesDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("自动分支带 Base 必须放行: %v", err)
	}
	if ws.Branch != "handoff/12345678" {
		t.Fatalf("应是自动分支，得到 %s", ws.Branch)
	}
	if head := gitOut(t, ws.WorkDir, "rev-parse", "HEAD"); head != base {
		t.Fatalf("自动分支起点必须是 Base：head=%s base=%s（B35 根因：起点被静默换成仓库 HEAD）", head, base)
	}
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test ./internal/agentd/ -run TestPrepareWorkspaceAutoBranchHonorsBase -v`
Expected: FAIL，报错含 `base 仅允许与 new-branch 连用`（`ErrBadWorkspaceReq`）。

**如果它意外地通过了，停下来** —— 说明代码已经不是本计划描述的样子，先重读 `workspace.go:187` 再继续。

- [ ] **Step 3: 改规则**

把 `internal/agentd/workspace.go:187-189` 的

```go
	if req.Base != "" && req.NewBranch == "" {
		return Workspace{}, rejectWorkspace("base 仅允许与 new-branch 连用", req)
	}
```

替换为：

```go
	// base 是「新分支的起点」，切一个已存在分支时谈起点没有意义——真正的禁忌
	// 只有这一条。自动分支 handoff/<id8> 同样是新建分支，必须允许带起点：
	// 缺了它，dispatch 校验的基线与新分支的实际起点就成了两码事（B35 根因）。
	if req.Base != "" && req.Branch != "" {
		return Workspace{}, rejectWorkspace("base 与 branch（已存在分支）互斥", req)
	}
```

- [ ] **Step 4: 同步三处注释（否则文档继续教人错的心智模型）**

`internal/agentd/workspace.go:122-124`，把

```go
// 分支维度三态（互斥）：Branch=已存在分支 / NewBranch=新建分支 / 都空=自动
// handoff/<id8>；Base 仅与 NewBranch 连用（空=HEAD）——自动分支不带 Base
// （校验见 PrepareWorkspace 第 1 层，spec §5 与校验一致）。
```

改为

```go
// 分支维度三态（互斥）：Branch=已存在分支 / NewBranch=新建分支 / 都空=自动
// handoff/<id8>；Base 是新分支起点（空=HEAD），与 NewBranch 和自动分支都能
// 连用，只与 Branch 互斥——切已存在分支时没有「起点」这回事。
```

`internal/agentd/workspace.go:132` 的字段注释，把

```go
	Base         string // 新分支起点，仅与 NewBranch 连用（空=HEAD；自动分支不带 Base）
```

改为

```go
	Base         string // 新分支起点（空=HEAD）；与 Branch 互斥，NewBranch/自动分支均可带
```

`internal/agentd/workspace.go:157-158`，把

```go
// 其中 b=指定分支、h=handoff/<id8>、t=Base（仅 N 行有效，空=HEAD；自动分支 A
// 不允许带 Base，见第 1 层校验）、p=WorktreesDir/<id8> 或用户路径。
```

改为

```go
// 其中 b=指定分支、h=handoff/<id8>、t=Base（N 行与 A 行均有效，空=HEAD；B 行
// 切已存在分支，不接受 Base，见第 1 层校验）、p=WorktreesDir/<id8> 或用户路径。
```

同时把行为表第三行（A 行）的 `worktree add -b h <p>` 改为 `worktree add -b h <p> [t]`，与 N 行一致。

- [ ] **Step 5: 改写既有的互斥用例**

`internal/agentd/workspace_test.go` 的 `TestPrepareWorkspaceMutualExclusionAndInjection` 里，把

```go
		"base 无 new-branch":     {Repo: repo, TaskID: "t", Base: "HEAD~1"},
```

替换为

```go
		"base×branch":           {Repo: repo, TaskID: "t", Branch: "a", Base: "HEAD~1"},
```

并把该函数的 doc 注释里的「Base 依赖」改为「Base 与已存在分支互斥」。

- [ ] **Step 6: 跑全包测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS（`TestPrepareWorkspaceAutoBranchHonorsBase` 转绿，`TestPrepareWorkspaceMutualExclusionAndInjection` 与 `TestPrepareWorkspaceNewBranchWithBase` 保持绿）。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "fix(workspace): 自动分支放行 Base 起点，规则订正为 base×branch 互斥"
```

---

### Task 2: `ResolveBaseline` 替换 `EnsureBaseCommit` 并接进 dispatch

**Files:**
- Modify: `internal/agentd/workspace.go:9`（包 doc 里的机制名）
- Modify: `internal/agentd/workspace.go:398-443`（`EnsureBaseCommit` → `ResolveBaseline`）
- Modify: `internal/agentd/workspace.go:445-450` 之后（新增 `headCommit` / `countAhead` 辅助）
- Modify: `internal/agentd/workspace.go:22-35`（import 加 `strconv`）
- Modify: `internal/agentd/manager.go:462-476`（调用点 + 起点优先级 + 分叉 WARN）
- Test: `internal/agentd/workspace_test.go:630-676`（4 条既有 `EnsureBaseCommit*` 测试改写）+ 新增 3 条
- Test: `internal/agentd/manager_test.go`（新增 1 条端到端断言）

**Interfaces:**
- Consumes: Task 1 的 `PrepareWorkspace` —— `WorkspaceReq{Base: <40 位 sha>}` 在 `Branch == ""` 时被当作新分支起点。
- Produces:
  - `type Baseline struct { Start string; Ahead int; Fetched bool }`
  - `func ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error)`
  - `EnsureBaseCommit` **被删除**，不再存在。
  - `Manager.Dispatch` 内的局部量 `start string` / `ahead int`（Task 3 会把它们写进任务记录）。

- [ ] **Step 1: 写失败的测试（改写 4 条既有 + 新增 3 条）**

`internal/agentd/workspace_test.go:630-676` 的四个 `TestEnsureBaseCommit*` 整段替换为下面七个函数：

```go
// TestResolveBaselinePresentSkipsFetch 验证基线已在本地对象库时直接放行且不 fetch。
// 仓库故意配一个不存在的 remote：一旦实现「无条件先 fetch」，git fetch --all
// 会失败并让本用例挂掉——这就是「命中即零网络」的可执行证据。
func TestResolveBaselinePresentSkipsFetch(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "nonexistent.git"))
	bl, err := ResolveBaseline(context.Background(), repo, head)
	if err != nil {
		t.Fatalf("基线已在仓库中必须直接放行（不触发 fetch），实得 %v", err)
	}
	if bl.Start != head || bl.Ahead != 0 || bl.Fetched {
		t.Fatalf("基线即 HEAD 时应 Start=HEAD/Ahead=0/未 fetch，实得 %+v", bl)
	}
}

// TestResolveBaselineAheadCount 验证任务仓库领先基线时数得出提交数——
// 这个数字就是「新分支会丢掉哪些提交」的规模，B35 现场缺的正是它。
func TestResolveBaselineAheadCount(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "a.txt", "1")
	writeAndCommit(t, repo, "b.txt", "2")
	bl, err := ResolveBaseline(context.Background(), repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if bl.Start != base {
		t.Fatalf("Start 必须是入参基线，实得 %s", bl.Start)
	}
	if bl.Ahead != 2 {
		t.Fatalf("任务仓库领先 2 个提交，实得 Ahead=%d", bl.Ahead)
	}
}

// TestResolveBaselineEmptyFallsBackToHead 验证空基线（--no-sync-check / cwd 非仓库）
// 不是「没有起点」而是「起点退回任务仓库 HEAD」：这条路上也必须答得出
// 「这个任务建在哪个提交上」。
func TestResolveBaselineEmptyFallsBackToHead(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	bl, err := ResolveBaseline(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("空基线必须跳过校验，实得 %v", err)
	}
	if bl.Start != head || bl.Ahead != 0 {
		t.Fatalf("空基线应退回仓库 HEAD 且 Ahead=0，实得 %+v（HEAD=%s）", bl, head)
	}
}

// TestResolveBaselineEmptyRepoHasNoStart 验证一个提交都没有的仓库返回空 Start
// 而不是报错：空仓库上 checkout -b 本来就不能带起点，交给 git 默认行为。
func TestResolveBaselineEmptyRepoHasNoStart(t *testing.T) {
	repo := t.TempDir()
	gitAt(t, repo, "init", "-q")
	bl, err := ResolveBaseline(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("空仓库不应报错，实得 %v", err)
	}
	if bl.Start != "" {
		t.Fatalf("空仓库应无起点，实得 %q", bl.Start)
	}
}

// TestResolveBaselineMissingRejects 验证基线缺失且 fetch 补不回来时拒发，
// 且错误里带上基线 sha —— 审核者据此才知道该 push 哪个提交。
func TestResolveBaselineMissingRejects(t *testing.T) {
	repo := initTestRepo(t)
	const absent = "0123456789abcdef0123456789abcdef01234567"
	_, err := ResolveBaseline(context.Background(), repo, absent)
	if !errors.Is(err, ErrBaseCommitMissing) {
		t.Fatalf("基线缺失必须返回 ErrBaseCommitMissing，实得 %v", err)
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("错误文本必须含基线 sha，实得 %q", err.Error())
	}
	if !strings.Contains(err.Error(), "git push") {
		t.Errorf("错误文本必须含 git push 的动作提示，实得 %q", err.Error())
	}
}

// TestResolveBaselineRejectsMalformedSHA 验证非 40 位十六进制一律拒绝：
// 基线值最终会拼进 git 参数，不校验等于开一个注入面。
func TestResolveBaselineRejectsMalformedSHA(t *testing.T) {
	repo := initTestRepo(t)
	for _, bad := range []string{"--upload-pack=evil", "HEAD", "abc123", "0123456789abcdef0123456789abcdef0123456G"} {
		if _, err := ResolveBaseline(context.Background(), repo, bad); !errors.Is(err, ErrBadWorkspaceReq) {
			t.Errorf("基线 %q 必须按参数非法拒绝，实得 %v", bad, err)
		}
	}
}

// TestResolveBaselineStartIsUsableAsBranchStart 是「决议结论真的能当起点用」的
// 闭环断言：ResolveBaseline 给出的 Start 直接喂给 PrepareWorkspace，新 worktree
// 的 HEAD 必须落在它上面。校验与使用之间的连接就是这条测试守着的东西。
func TestResolveBaselineStartIsUsableAsBranchStart(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "drift.txt", "x")
	bl, err := ResolveBaseline(context.Background(), repo, base)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Base: bl.Start,
		NewWorktree: true, WorktreesDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if head := gitOut(t, ws.WorkDir, "rev-parse", "HEAD"); head != bl.Start {
		t.Fatalf("决议起点未被用上：worktree head=%s baseline start=%s", head, bl.Start)
	}
}
```

同时在 `internal/agentd/manager_test.go` 末尾追加：

```go
// TestDispatchAutoBranchStartsAtBaseCommit 是 B35 在 dispatch 全链路上的回归：
// 任务仓库 HEAD 已经前进，但派发时上送的基线是更早那个提交——新 worktree
// 必须落在基线上，不能落在仓库 HEAD 上。
func TestDispatchAutoBranchStartsAtBaseCommit(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	base := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	writeAndCommit(t, repo, "drift.txt", "x") // 仓库 HEAD 前进，模拟执行机落后/超前
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, BaseCommit: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitAt(t, task.Workdir(), "rev-parse", "HEAD"))
	if head != base {
		t.Fatalf("新 worktree 应开在基线 %s 上，实得 %s（B35：校验了基线却从仓库 HEAD 开分支）", base, head)
	}
}
```

- [ ] **Step 2: 跑测试确认它们失败**

Run: `go test ./internal/agentd/ -run 'ResolveBaseline|TestDispatchAutoBranchStartsAtBaseCommit' -count=1`
Expected: 编译失败，`undefined: ResolveBaseline`。这是预期的红。

- [ ] **Step 3: 实现 `Baseline` 与 `ResolveBaseline`**

`internal/agentd/workspace.go` 的 import 块加 `"strconv"`（放在 `"regexp"` 与 `"strings"` 之间，保持字母序）。

把 `internal/agentd/workspace.go:398-443`（`EnsureBaseCommit` 的整段 doc 与函数体）替换为：

```go
// Baseline 是一次基线决议的结果：校验结论与新分支起点出自同一次计算。
//
// 为什么必须是同一个结构而不是分两次算：B35 的根因就是「校验这个 sha 存不存在」
// 与「新分支从哪起」由两段代码各自决定，中间没有任何连接——校验通过了，分支却
// 从任务仓库 HEAD 开出去，两者可以静默地差出几十个提交而不留任何痕迹。
type Baseline struct {
	// Start 是新分支起点（40 位 sha）。任务仓库一个提交都没有时为空，
	// 退回 git 默认行为（空仓库上 checkout -b 本来就不能带起点）。
	Start string
	// Ahead 是任务仓库 HEAD 上有、而 Start 上没有的提交数——这些提交不会进新分支。
	Ahead int
	// Fetched 表示是否为定位 Start 补拉过远端。只用于日志：排障时要能分清
	// 「基线本来就在」与「补拉才拿到」，前者说明两边同步，后者说明执行机落后过。
	Fetched bool
}

// ResolveBaseline 决议任务的基线：校验审核者本地基线在任务仓库中可用，并给出
// 新分支应当使用的起点与「任务仓库比它多出多少提交」。
//
// 参数：
//   - ctx: 上层上下文；fetch 阶段内部叠加 FetchTimeout
//   - repo: 任务仓库路径
//   - sha: 审核者本地 HEAD 的 40 位十六进制提交号；空=未提供（--no-sync-check
//     或调用方 cwd 不是 git 仓库），此时起点退回任务仓库当前 HEAD
//
// 返回：
//   - Baseline: Start=新分支起点；Ahead=任务仓库 HEAD 领先 Start 的提交数
//   - ErrBadWorkspaceReq: sha 格式非法（会拼进 git 参数，不校验等于开注入面）
//   - ErrBaseCommitMissing: fetch 后仍缺失，错误文本含 sha、fetch stderr 与动作提示
//
// 注意：
//   - 空 sha 也返回一个具体的 Start：让「这个任务建在哪个提交上」在任何路径下
//     都答得出来，包括 --no-sync-check 那条——今天那条路上基线是纯粹的空白
//   - 「命中才不 fetch」是刻意设计：常态下远程并不落后，cat-file 是纯本地对象库
//     查询（微秒级），只有真落后时才付网络代价
//   - fetch 失败（无凭证/网络不通）不单独成一类错误，一并归入 ErrBaseCommitMissing：
//     对调用方而言结论都是「这次派不出去，先解决远程仓库」，stderr 原文已带出根因
func ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error) {
	if sha == "" {
		head := headCommit(ctx, repo)
		log().Info("未提供基线提交，起点退回任务仓库 HEAD", "repo", repo, "start", head)
		return Baseline{Start: head}, nil
	}
	if !baseCommitRe.MatchString(sha) {
		log().Warn("基线提交格式非法，拒绝派发", "repo", repo, "base_commit", truncateRunes(sha, 80))
		return Baseline{}, fmt.Errorf("%w: 基线提交必须是 40 位十六进制，实得 %q", ErrBadWorkspaceReq, truncateRunes(sha, 80))
	}
	fetched := false
	if !hasCommit(ctx, repo, sha) {
		log().Info("基线提交缺失，补拉远端", "repo", repo, "base_commit", sha, "timeout", FetchTimeout)
		fctx, cancel := context.WithTimeout(ctx, FetchTimeout)
		defer cancel()
		_, stderr, ferr := gitRun(fctx, repo, "fetch", "--all", "--prune")
		if ferr != nil {
			log().Error("补拉远端失败", "repo", repo, "base_commit", sha,
				"stderr", truncateRunes(stderr, 500), "cause", ferr)
		}
		fetched = true
		if !hasCommit(ctx, repo, sha) {
			log().Warn("基线提交补拉后仍缺失，拒绝派发", "repo", repo, "base_commit", sha)
			return Baseline{}, fmt.Errorf("%w: %s（任务仓库 %s 落后于本地；fetch 输出：%s）；请先在本地 git push，或用 --no-sync-check 跳过校验",
				ErrBaseCommitMissing, sha, repo, strings.TrimSpace(truncateRunes(stderr, 300)))
		}
		log().Info("补拉远端后基线提交已就位", "repo", repo, "base_commit", sha)
	}
	bl := Baseline{Start: sha, Ahead: countAhead(ctx, repo, sha), Fetched: fetched}
	log().Info("基线决议完成", "repo", repo, "start", bl.Start, "ahead", bl.Ahead, "fetched", bl.Fetched)
	return bl, nil
}

// headCommit 取仓库当前 HEAD 的完整 sha。
//
// 仓库一个提交都没有（或路径不是 git 仓库）时返回空串——空起点交给 git 默认
// 行为，不是错误：真正的仓库问题会在 PrepareWorkspace 的脏检查/建树阶段暴露，
// 在这里把它变成拒发只会给出一个更难懂的报错。
func headCommit(ctx context.Context, repo string) string {
	out, _, err := gitRun(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		log().Info("任务仓库没有 HEAD（空仓库或非 git 仓库），起点留空", "repo", repo)
		return ""
	}
	return strings.TrimSpace(out)
}

// countAhead 数任务仓库 HEAD 上有、而 start 上没有的提交数。
//
// 数不出来时返回 0 并打 Warn：这是给人看的提示数字，不该因为数不出来就把
// 整次派发拒掉——起点本身已经校验过了，提示缺失不影响正确性。
func countAhead(ctx context.Context, repo, start string) int {
	out, _, err := gitRun(ctx, repo, "rev-list", "--count", start+"..HEAD")
	if err != nil {
		log().Warn("统计任务仓库领先提交数失败，按 0 处理", "repo", repo, "start", start, "cause", err)
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		log().Warn("领先提交数解析失败，按 0 处理", "repo", repo,
			"out", truncateRunes(out, 80), "cause", err)
		return 0
	}
	return n
}
```

同时把 `internal/agentd/workspace.go:9` 的包 doc 那行

```go
//   - 派发前的远程基线校验：EnsureBaseCommit 保证任务仓库不落后于审核者本地
```

改为

```go
//   - 派发前的基线决议：ResolveBaseline 一次算出「校验结论 + 新分支起点 +
//     任务仓库领先多少提交」，保证校验的东西和用的东西是同一个
```

- [ ] **Step 4: 接进 `Manager.Dispatch`**

把 `internal/agentd/manager.go:462-476` 的

```go
	// 远程基线校验（B4）：放在工作区准备之前——基准不对时后面建的分支全是错的，
	// 且此刻还没有任何落库/建树副作用，拒发是干净的
	if err := EnsureBaseCommit(ctx, req.Repo, req.BaseCommit); err != nil {
		return nil, err
	}
```

替换为

```go
	// 基线决议（B4 校验 + B35 起点）：放在工作区准备之前——基准不对时后面建的
	// 分支全是错的，且此刻还没有任何落库/建树副作用，拒发是干净的
	baseline, err := ResolveBaseline(ctx, req.Repo, req.BaseCommit)
	if err != nil {
		return nil, err
	}
	// 起点优先级：显式 --base > 决议出的基线 > 空（交给 git 默认）。
	// 为什么 Branch 模式要排除：切一个已存在的分支没有「起点」这回事，把基线
	// 硬塞进去会被 PrepareWorkspace 的 base×branch 互斥直接拒掉。
	// 为什么显式 --base 时不报分叉：用户已经明确指定了起点，再警告是噪音。
	start, ahead := req.Base, 0
	if start == "" && req.Branch == "" {
		start, ahead = baseline.Start, baseline.Ahead
		if ahead > 0 {
			m.log.Warn("任务仓库 HEAD 领先基线，新分支不含这些提交",
				"repo", req.Repo, "start", start, "ahead", ahead)
		}
	}
	m.log.Info("基线起点已确定", "repo", req.Repo, "start", start, "ahead", ahead,
		"explicit_base", req.Base != "")
```

再把紧随其后的 `PrepareWorkspace` 调用里的 `Base: req.Base` 改为 `Base: start`：

```go
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{
		Repo: req.Repo, TaskID: taskID,
		Branch: req.Branch, NewBranch: req.NewBranch, Base: start,
		Worktree: req.Worktree, NewWorktree: req.NewWorktree,
		WorktreesDir: filepath.Join(m.cfg.DataDir, "worktrees"),
	})
```

`ahead` 在本任务里的唯一消费者就是上面那条 `m.log.Info`（Task 3 才把它落库），所以不会触发「声明但未使用」的编译错误，无需额外处理。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: PASS（含 7 条 `ResolveBaseline*` 与 `TestDispatchAutoBranchStartsAtBaseCommit`）。

Run: `go build ./... && go vet ./...`
Expected: 无输出（`EnsureBaseCommit` 已无任何引用；若报 undefined，说明还有调用点没改）。

- [ ] **Step 6: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(agentd): EnsureBaseCommit 升级为 ResolveBaseline，校验结论即新分支起点"
```

---

### Task 3: 基线入库（`base_commit` / `base_ahead`）

**Files:**
- Modify: `internal/proto/proto.go:58-81`（`Task` 加两个字段）
- Modify: `internal/store/store.go:72-82`（建表 DDL 加两列）
- Modify: `internal/store/store.go:114-127`（旧库迁移循环加两列）
- Modify: `internal/store/store.go:141-160`（`CreateTask` 的 INSERT）
- Modify: `internal/store/store.go:174-192`（`GetTask` 的 SELECT/Scan）
- Modify: `internal/store/store.go:198-229`（`ListTasks` 的 SELECT/Scan）
- Modify: `internal/agentd/manager.go:508-524`（构造 `proto.Task` 时带上两值）
- Test: `internal/store/store_test.go`（新增 1 条往返测试）
- Test: `internal/agentd/manager_test.go`（新增 2 条：落库断言 + 显式 `--base` 优先级）

**Interfaces:**
- Consumes: Task 2 的 `Manager.Dispatch` 局部量 `start string` / `ahead int`。
- Produces: `proto.Task.BaseCommit string`（json `base_commit`）、`proto.Task.BaseAhead int`（json `base_ahead`）；两者随 `CreateTask` 落库、随 `GetTask`/`ListTasks` 回读。

- [ ] **Step 1: 写失败的测试**

`internal/store/store_test.go` 追加：

```go
// TestCreateTaskPersistsBaseline 验证基线两字段能落库并回读——「这个任务建在
// 哪个提交上、当时仓库比它多几个提交」必须是事后查得到的事实，而不是只在
// 派发那一刻的日志里闪过。
func TestCreateTaskPersistsBaseline(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	const sha = "d64bac4d64bac4d64bac4d64bac4d64bac4d64ba"
	task := &proto.Task{
		ID: "t-base", RepoPath: "/repo", State: proto.TaskStatePending,
		BaseCommit: sha, BaseAhead: 3,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("t-base")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != sha || got.BaseAhead != 3 {
		t.Fatalf("基线字段未持久化: base_commit=%q base_ahead=%d", got.BaseCommit, got.BaseAhead)
	}
	list, err := s.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].BaseCommit != sha || list[0].BaseAhead != 3 {
		t.Fatalf("ListTasks 未带出基线字段: %+v", list)
	}
}
```

`internal/agentd/manager_test.go` 追加：

```go
// TestDispatchRecordsBaseline 验证派发落库的基线就是实际用的起点，且领先数
// 被如实记下——这正是 08-10 事故复盘时谁都答不上来的那个数字。
func TestDispatchRecordsBaseline(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	base := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	writeAndCommit(t, repo, "one.txt", "1")
	writeAndCommit(t, repo, "two.txt", "2")
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, BaseCommit: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != base {
		t.Fatalf("落库基线应是实际起点 %s，实得 %q", base, got.BaseCommit)
	}
	if got.BaseAhead != 2 {
		t.Fatalf("任务仓库领先 2 个提交，落库实得 %d", got.BaseAhead)
	}
}
```

再追加一条覆盖「显式 `--base` 压过基线」的用例：

```go
// TestDispatchExplicitBaseWinsOverBaseline 验证起点优先级：显式 --base 压过
// 决议出的基线，且此时不记领先数——用户已经明确指定了起点，「你丢了 N 个
// 提交」这句话对他毫无意义，是噪音不是信息。
func TestDispatchExplicitBaseWinsOverBaseline(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	explicit := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	baseline := writeAndCommit(t, repo, "mid.txt", "m") // 基线比 explicit 新
	writeAndCommit(t, repo, "tip.txt", "t")            // 仓库 HEAD 再前进一格
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
		BaseCommit: baseline, Base: explicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitAt(t, task.Workdir(), "rev-parse", "HEAD"))
	if head != explicit {
		t.Fatalf("显式 --base 应压过基线：worktree head=%s explicit=%s baseline=%s", head, explicit, baseline)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != explicit {
		t.Fatalf("落库基线应是实际用的起点 %s，实得 %q", explicit, got.BaseCommit)
	}
	if got.BaseAhead != 0 {
		t.Fatalf("显式起点不该报领先数，实得 %d", got.BaseAhead)
	}
}
```

- [ ] **Step 2: 跑测试确认它们失败**

Run: `go test ./internal/store/ ./internal/agentd/ -run 'Baseline' -count=1`
Expected: 编译失败，`task.BaseCommit undefined (type proto.Task has no field or method BaseCommit)`。

- [ ] **Step 3: 加 `proto.Task` 字段**

在 `internal/proto/proto.go` 的 `Task` 结构里，`WorktreeManaged` 之后追加：

```go
	// BaseCommit 是本任务新分支的**实际起点**（40 位 sha）；空=切已存在分支
	// （没有起点这回事）或老任务（该列后加，不回填、不编造）。
	// 它回答的是「这个任务建在哪个提交上」——B35 之前这个问题无处可问。
	BaseCommit string `json:"base_commit"`
	// BaseAhead 是派发当时任务仓库 HEAD 领先 BaseCommit 的提交数：这些提交
	// 不在任务分支里。0 表示起点就是仓库 HEAD，或该数字当时没能算出来。
	BaseAhead int `json:"base_ahead"`
```

- [ ] **Step 4: 加 store 的列与读写**

`internal/store/store.go:72-82` 的建表 DDL，把最后一行

```go
  worktree_managed INTEGER NOT NULL DEFAULT 0)`,
```

改为

```go
  worktree_managed INTEGER NOT NULL DEFAULT 0,
  -- 基线两列（B35）：base_commit=任务新分支的实际起点；base_ahead=派发当时
  -- 任务仓库 HEAD 领先该起点的提交数（这些提交不在任务分支里）。
  base_commit TEXT NOT NULL DEFAULT '', base_ahead INTEGER NOT NULL DEFAULT 0)`,
```

`internal/store/store.go:114-120` 的迁移 map，在 `"worktree_managed"` 之后加两项：

```go
		"base_commit":      "TEXT NOT NULL DEFAULT ''",
		"base_ahead":       "INTEGER NOT NULL DEFAULT 0",
```

并把该循环上方注释的首行「迁移：为旧库补二期 tasks 列（name/executor/model/work_dir/worktree_managed）。」改为「迁移：为旧库补 tasks 增量列（二期 name/executor/model/work_dir/worktree_managed + B35 base_commit/base_ahead）。」

`CreateTask` 的 INSERT 改为：

```go
	_, err := s.db.ExecContext(context.Background(), `
INSERT INTO tasks (id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, base_commit, base_ahead)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Target, t.RepoPath, t.Branch, t.PlanPath, t.PlanSummary,
		t.ExecutorSession, t.State, fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt),
		t.Name, t.Executor, t.Model, t.WorkDir, boolToInt(t.WorktreeManaged),
		t.BaseCommit, t.BaseAhead)
```

`GetTask` 的查询与 Scan 改为：

```go
	err := s.db.QueryRowContext(context.Background(), `
SELECT id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, base_commit, base_ahead
FROM tasks WHERE id = ?`, id).
		Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
			&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt,
			&task.Name, &task.Executor, &task.Model, &task.WorkDir, &worktreeManaged,
			&task.BaseCommit, &task.BaseAhead)
```

`ListTasks` 的查询与 Scan 同样改为：

```go
	rows, err := s.db.QueryContext(context.Background(), `
SELECT id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, base_commit, base_ahead
FROM tasks ORDER BY created_at DESC`)
```

```go
		if err := rows.Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
			&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt,
			&task.Name, &task.Executor, &task.Model, &task.WorkDir, &worktreeManaged,
			&task.BaseCommit, &task.BaseAhead); err != nil {
			return nil, fmt.Errorf("读取任务行: %w", err)
		}
```

- [ ] **Step 5: 让 manager 把值写进任务**

`internal/agentd/manager.go` 构造 `task = &proto.Task{...}` 时，在 `WorktreeManaged: ws.Managed,` 之后追加：

```go
			// 基线随创建期一并入库（此刻已由 ResolveBaseline 决议完毕），
			// 不走 SetTaskField——那个白名单只服务「创建时还不知道」的字段
			BaseCommit: start,
			BaseAhead:  ahead,
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/store/ ./internal/agentd/ ./internal/proto/ -count=1`
Expected: PASS。

- [ ] **Step 7: 验证旧库能平滑升级**

```bash
go test ./internal/store/ -run TestTaskLifecycle -count=1 -v
```
Expected: PASS。迁移循环容忍 `duplicate column`，新建库走 DDL、旧库走 ALTER，两条路都必须绿。

- [ ] **Step 8: 提交**

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go internal/agentd/manager.go internal/agentd/manager_test.go
git commit -m "feat(store): 任务记录基线起点与领先提交数（base_commit/base_ahead）"
```

---

### Task 4: dispatch 打基线摘要，并订正 SKILL.md

**Files:**
- Modify: `cmd/dispatch.go:100-118`（收到任务后打 stderr 摘要行）
- Modify: `cmd/dispatch.go:1-13`（文件头职责补一条）
- Test: `cmd/dispatch_test.go:16-50`（`runDispatch` helper 改造）+ 新增 2 条测试
- Modify: `skills/handoff/SKILL.md:93-112`（去程一节）
- Modify: `skills/handoff/SKILL.md:244`、`skills/handoff/SKILL.md:269`（排障表与红旗表两行）

**Interfaces:**
- Consumes: Task 3 的 `proto.Task.BaseCommit` / `proto.Task.BaseAhead`（`client.Dispatch` 返回 `*proto.Task`，字段自动过线，无需改 client）。
- Produces: 无（终点任务）。

- [ ] **Step 1: 改造测试 helper 让它能看见 stderr**

`cmd/dispatch_test.go` 的 `runDispatch` 目前把 stderr 丢掉（`_ = errBuf`），且假服务端的任务 JSON 写死。改为：

```go
// dispatchTestTaskJSON 是假 agentd 返回的任务 JSON。测试可临时改写它来构造
// 不同的任务形态（如带基线字段），t.Cleanup 里复原。
var dispatchTestTaskJSON = `{"id":"task-abc123","state":"running"}`

// runDispatch 以给定 flags 执行 dispatch（指向 fake agentd），返回 stdout、stderr 与错误。
func runDispatch(t *testing.T, extraArgs ...string) (string, string, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, dispatchTestTaskJSON)
	}))
	t.Cleanup(ts.Close)
	addr := strings.TrimPrefix(ts.URL, "http://")
	cfgPath := writeTestConfig(t, "listen: \""+addr+"\"\ntoken: \""+testToken+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	agentdURL = "http://127.0.0.1:7777"
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false
	t.Cleanup(func() { dispatchNoTerminal = false })

	args := append([]string{"dispatch", "--repo", t.TempDir(), "--prompt", "x"}, extraArgs...)
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
	return out.String(), errBuf.String(), err
}
```

三个既有调用点跟着改（只是多接一个返回值）：

- `TestDispatchOpensTerminalByDefault`：`if _, err := runDispatch(t); err != nil {` → `if _, _, err := runDispatch(t); err != nil {`
- `TestDispatchNoTerminalFlagSuppresses`：`out, err := runDispatch(t, "--no-terminal")` → `out, _, err := runDispatch(t, "--no-terminal")`
- `TestDispatchTerminalFailureDoesNotFailCommand`：`out, err := runDispatch(t)` → `out, _, err := runDispatch(t)`

- [ ] **Step 2: 写失败的测试**

`cmd/dispatch_test.go` 追加：

```go
// TestDispatchPrintsBaselineToStderr 验证派发后 stderr 打出基线短号，且 stdout
// 契约不受影响（仍是单行任务 JSON——上层脚本按行解析，多一行就全乱）。
func TestDispatchPrintsBaselineToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","base_commit":"d64bac4d64bac4d64bac4d64bac4d64bac4d64ba","base_ahead":0}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	out, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "基线 d64bac4") {
		t.Fatalf("stderr 应含基线短号，得到 %q", errOut)
	}
	if strings.Contains(errOut, "领先") {
		t.Fatalf("未分叉时不该提领先提交数，得到 %q", errOut)
	}
	if strings.Contains(out, "基线") {
		t.Fatalf("stdout 必须只有任务 JSON（脚本按行解析），得到 %q", out)
	}
}

// TestDispatchPrintsDivergenceToStderr 验证任务仓库领先基线时把丢掉的提交数
// 说出来——B35 的现场就是这个差异毫无痕迹，审核者甚至反过来怀疑执行者搞错了。
func TestDispatchPrintsDivergenceToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","base_commit":"d64bac4d64bac4d64bac4d64bac4d64bac4d64ba","base_ahead":3}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "领先 3 个提交") {
		t.Fatalf("stderr 应说明任务仓库领先几个提交，得到 %q", errOut)
	}
}

// TestDispatchNoBaselineNoLine 验证没有基线（切已存在分支/老 agentd）时不打
// 空洞的一行：宁可不说，也不要打一个「基线 」误导人。
func TestDispatchNoBaselineNoLine(t *testing.T) {
	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errOut, "基线") {
		t.Fatalf("无基线时不应打基线行，得到 %q", errOut)
	}
}
```

- [ ] **Step 3: 跑测试确认它们失败**

Run: `go test ./cmd/ -run 'TestDispatchPrintsBaseline|TestDispatchPrintsDivergence|TestDispatchNoBaselineNoLine' -count=1`
Expected: FAIL —— `stderr 应含基线短号，得到 ""`（第三条会先通过，那是对的：它守的是「不多嘴」）。

- [ ] **Step 4: 实现 stderr 摘要行**

`cmd/dispatch.go` 里，在 `if err != nil { return err }`（Dispatch 返回处）之后、`json.Marshal(task)` 之前插入：

```go
			// 基线摘要走 stderr：stdout 是「单行任务 JSON」的既有契约，上层脚本
			// 按行解析，多打一行就会把它们全部打断。为什么必须打：B35 的现场里
			// 分支开在了三批改动之前，而这件事在任何输出里都不留痕迹——审核者
			// 甚至反过来怀疑是执行者找错了目录
			if task.BaseCommit != "" {
				line := "基线 " + shortSHA(task.BaseCommit)
				if task.BaseAhead > 0 {
					line += fmt.Sprintf("（任务仓库 HEAD 领先 %d 个提交，新分支不含它们）", task.BaseAhead)
				}
				fmt.Fprintln(cmd.ErrOrStderr(), line)
			}
```

并在 `localHeadCommit` 之后追加辅助函数：

```go
// shortSHA 取提交号前 7 位（git 惯例的短号）；不足 7 位原样返回。
// 摘要行给人读，40 位全量 sha 会把有用信息挤出视线——完整值在任务 JSON 的
// base_commit 里，需要精确比对时从那里取。
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
```

`cmd/dispatch.go` 文件头职责列表里，在「远程派发时采集本地 HEAD 作基线随请求上送（--no-sync-check 可关）」之后追加一行：

```go
//   - 派发成功后在 stderr 打一行基线摘要（起点短号 + 任务仓库领先的提交数）
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -count=1`
Expected: PASS。

- [ ] **Step 6: 订正 SKILL.md 的去程一节**

`skills/handoff/SKILL.md` 里「**fetch ≠ 分支到位。**」那一整条（约 101 行）现在是**错的**——它教的正是被本次修复消灭掉的行为。把它替换为：

```markdown
- **新分支的起点是你派发时的本地 HEAD，不是执行机仓库的 HEAD。** agentd 收到基线后，既拿它做存在性校验，也拿它做新分支的起点——两件事出自同一次决议，不会再分叉（B35 之前会：校验的是你的基线，开分支用的是执行机 HEAD，中间可以差出几十个提交而毫无痕迹）。派发成功后 stderr 会打一行 `基线 <短号>`；执行机仓库比这个起点新时还会补上「领先 N 个提交，新分支不含它们」。
- **`--no-sync-check` 关掉的不止是校验。** 它同时关掉起点决议——没有基线可用时，新分支的起点退回执行机仓库当前的 HEAD（很可能是旧的）。只在 cwd 和 `--repo` 根本不是同一个仓库时用它。
```

「稳妥的远程派发姿势，把起点钉死」那段（约 105-110 行）里的 `--base "$(git rev-parse HEAD)"` 已不再必要，改为：

````markdown
稳妥的远程派发姿势：

```bash
git push                                                  # 缺这步必被拒
handoff dispatch --target devbox --repo /remote/path \
  --new-worktree --new-branch feat/x plan.md              # 起点自动取你当前的 HEAD
```

`--base` 仍然可用，用于**刻意**从别处开分支（比如从某个 tag 或更早的提交起）；给了它就以它为准，也不会再提示分叉。
````

紧随其后那段 `--no-sync-check` 的说明（约 112 行）已被上面的要点覆盖，删掉整段避免与新要点重复。

- [ ] **Step 7: 订正排障表与红旗表**

`skills/handoff/SKILL.md` 排障表里这一行（约 244 行）：

```markdown
| 远程派发成功，但 executor 基于旧代码开工 | 两种：改动只 commit 没 push（校验静默通过）；或 `--new-branch` 没给 `--base`，从远程旧 HEAD 起 | `--base "$(git rev-parse HEAD)"` 钉死起点，派发前先 `git push` |
```

替换为：

```markdown
| 远程派发成功，但 executor 基于旧代码开工 | 改动只 commit 没 push——校验拿 HEAD 比，HEAD 不含未提交改动，会静默通过 | 派发前先 `git push`。起点本身不用管：新分支自动落在你派发时的 HEAD 上，stderr 的「基线」行就是实际起点 |
```

红旗表里这一行（约 269 行）：

```markdown
| 「agentd 已经 fetch 过了，远程分支就是最新的」 | fetch 只落对象，不动分支。不带 `--base` 的新分支照样从远程旧 HEAD 起。 |
```

替换为：

```markdown
| 「stderr 说领先 3 个提交，应该问题不大」 | 那 3 个提交不在任务分支里。执行者会找不到刚加的文件、目录、backlog 行——先想清楚它们是不是这次任务要用的东西。 |
```

- [ ] **Step 8: 全量验证**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```
Expected: build/vet 无输出；`gofmt -l .` 只列出历史遗留的 `internal/executor/grok/askquestion_internal_test.go`（本次不引入新的）；`go test ./...` 全部 `ok`。

- [ ] **Step 9: 提交**

```bash
git add cmd/dispatch.go cmd/dispatch_test.go skills/handoff/SKILL.md
git commit -m "feat(dispatch): stderr 打基线摘要行，SKILL.md 订正起点心智模型"
```

---

## 落地后的验收清单

实现全部完成、声明完工之前逐项确认（`instrumenting-code` 自检 + 本仓最终审阅清单）：

- [ ] `ResolveBaseline` 的每个错误分支都带上下文（repo + sha + cause）
- [ ] `git fetch` 这个外部调用前后都有日志，失败打 Error 带 stderr
- [ ] 成功路径不静默：「基线决议完成」「基线起点已确定」两条 Info 必须存在
- [ ] 全程无 `fmt.Printf` 作为日志手段（`cmd/dispatch.go` 里的 `fmt.Fprintln` 是**面向用户的输出**，不是日志，合规）
- [ ] `Baseline` 类型与 `ResolveBaseline`/`headCommit`/`countAhead` 都有注释，非显然分支写了「为什么」
- [ ] `proto.Task` 新字段的注释说明了「空代表什么」（老任务不回填、不编造）
- [ ] 无跨层调用：store 只被 manager 调，cmd 只经 client 拿数据
- [ ] `skills/handoff/SKILL.md` 里再无任何一处仍在教「新分支从远程 HEAD 起」
