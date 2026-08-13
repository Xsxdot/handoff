# B76：`--base <分支名>` 触发 git DWIM 静默改写任务分支 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `dispatch --new-branch X --base <分支名>` 永远开出名为 `X` 的分支，并让「要的分支 ≠ 实到分支」这件事本身在派发当场被发现并拒发。

**Architecture:** 三层，互不替代。① 源头：起点在决议时统一 `rev-parse` 成 40 位 sha 再交给 git，DWIM 从原理上无从发生；② 守卫：`PrepareWorkspace` 建完工作区后核对实际分支 == 请求分支，不符则回滚并拒发——这填的是「从来没人比对过要的和拿到的」这个结构性空白；③ 可见性：`BaseCommit` 契约对齐 + dispatch 回显同时打出分支名、解析后起点、用户输入的 base 原文。

**Tech Stack:** Go 1.26.1；`internal/agentd`（`gitRun` 包装的真实 git 调用，测试不 mock git）；`cmd`（cobra）；日志用包内 `log()` / `m.log`（slog），**禁止 `fmt.Printf` 作日志**。

**Spec:** [2026-08-13-dispatch-base-dwim-design.md](../specs/2026-08-13-dispatch-base-dwim-design.md)

## Global Constraints

- 六闸门全绿方可声明完工：`go build ./...`、`go vet ./...`、`gofmt -l .`（无输出）、`go test ./... -count=1`、`go test -race ./internal/agentd/ ./cmd/`、devbox 真机烟测（Task 4）。
- 测试**不 mock git**，一律在真实 git 仓库上跑（本仓库既有惯例，见 B8）。
- 新增/改动的导出方法必须有 doc 注释；新文件必须有文件头注释（职责 + 边界）；非显然分支必须有中文「为什么」注释。
- 错误文本面向审核者，必须可操作（说清出了什么事、下一步做什么），不得只回显 git 内部措辞。
- `--base` 的用户语义零变化：仍接受分支名、tag、sha。

---

### Task 1: 分支身份守卫 + 克隆仓库测试 fixture

守卫独立于 Task 2 的 sha 解析存在：git 报退出码 0 却干了另一件事，这种可能性不因这次传了 sha 而消失。

**Files:**
- Modify: `internal/agentd/workspace.go`（新增哨兵 `ErrBranchIdentityMismatch`、`verifyBranchIdentity`、`rollbackWorkspace`；在 `PrepareWorkspace` 末尾接入）
- Test: `internal/agentd/workspace_test.go`（新增 `initClonedRepo` fixture + 守卫用例）

**Interfaces:**
- Produces: `ErrBranchIdentityMismatch error`（哨兵，Task 2 的错误映射不涉及它，但 server 层按现有 `ErrBadWorkspaceReq` 之外的未知错误走 500——这是对的，分支身份不符是服务端异常不是用户输入问题）；`initClonedRepo(t *testing.T, baseBranch string) string`（Task 2 复用）

- [ ] **Step 1: 写克隆仓库 fixture**

加到 `internal/agentd/workspace_test.go`：

```go
// initClonedRepo 造「上游仓库 + 克隆」这一对，返回克隆出的仓库路径。
//
// 参数：
//   - baseBranch: 在上游建出的分支名；克隆里它**只以远程跟踪 ref 存在**
//
// 为什么必须是克隆：B76 的触发前提是「base 只有远程跟踪 ref、无本地同名分支」，
// 而 initTestRepo 那种本地 git init 的仓库里本地同名分支总是存在，DWIM 不会
// 发生——这正是这个 bug 一直没被任何测试抓到的原因。
//
// 为什么把 origin 改名成 upstream：registerTestProject 要往仓库里 remote add
// origin，克隆自带的 origin 会让它撞车。改名后远程跟踪 ref 变成
// refs/remotes/upstream/<baseBranch>，DWIM 照样触发（它认的是「在所有 remote 里
// 唯一」，不是「叫 origin」），顺带证明这个缺陷与 remote 叫什么无关。
func initClonedRepo(t *testing.T, baseBranch string) string {
	t.Helper()
	up := initTestRepo(t)
	gitT(t, up, "branch", baseBranch)
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, up, "clone", "-q", up, clone)
	gitT(t, clone, "remote", "rename", "origin", "upstream")
	gitT(t, clone, "config", "user.email", "test@handoff.dev")
	gitT(t, clone, "config", "user.name", "handoff test")
	// 前提自检：克隆里不能有本地同名分支，否则用例测的就不是 B76 的场景了
	if out := gitOut(t, clone, "branch", "--list", baseBranch); out != "" {
		t.Fatalf("fixture 失效：克隆里出现了本地分支 %s（%q），触发前提不成立", baseBranch, out)
	}
	return clone
}
```

- [ ] **Step 2: 写失败测试**

```go
// TestPrepareWorkspaceRejectsBranchIdentityMismatch 钉住 B76 的守卫：git 报成功
// 但给出的分支不是请求的那个时，必须回滚并拒发，而不是带着错分支继续。
func TestPrepareWorkspaceRejectsBranchIdentityMismatch(t *testing.T) {
	clone := initClonedRepo(t, "shared-base")
	wtDir := filepath.Join(t.TempDir(), "worktrees")

	_, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: clone, TaskID: "abcdefgh-b76", NewWorktree: true, WorktreesDir: wtDir,
		NewBranch: "feat/wanted", Base: "shared-base",
	})
	if !errors.Is(err, ErrBranchIdentityMismatch) {
		t.Fatalf("应按分支身份不符拒发, got: %v", err)
	}
	// 错误文本必须同时点名两个分支——只说「不符」的报错等于没说
	if !strings.Contains(err.Error(), "feat/wanted") || !strings.Contains(err.Error(), "shared-base") {
		t.Fatalf("错误文本应同时含请求分支与实到分支: %v", err)
	}
	// 拒发必须干净：刚建的工作树不能留下
	if _, statErr := os.Stat(filepath.Join(wtDir, "abcdefgh")); !os.IsNotExist(statErr) {
		t.Fatalf("拒发后 managed worktree 应已清理, stat err=%v", statErr)
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestPrepareWorkspaceRejectsBranchIdentityMismatch -count=1 -v`
Expected: FAIL —— 当前 `PrepareWorkspace` 返回 `err == nil`（DWIM 静默成功），断言在第一条 `errors.Is` 上翻红。

- [ ] **Step 4: 加哨兵与守卫函数**

在 `internal/agentd/workspace.go` 的错误哨兵块里加：

```go
	// ErrBranchIdentityMismatch 表示 git 报告成功，但工作区实际所在的分支
	// 不是我们请求的那个（B76：worktree add -b 被 DWIM 顶替）。
	ErrBranchIdentityMismatch = errors.New("工作区分支与请求不符")
```

在 `checkoutInWorktree` 附近加两个辅助：

```go
// verifyBranchIdentity 核对工作区实际所在分支是否就是决议出的分支。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - workDir: 已建好的工作区目录
//   - want: 第 2 层决议出的分支名
//
// 返回：不符或读取失败时返回包 ErrBranchIdentityMismatch 的错误，文本同时
// 含请求分支与实到分支。
//
// 为什么需要这道核对：git 的退出码只说明「命令没报错」，不说明「它做了你要
// 的事」。B76 里 `worktree add -b X <dir> <base>` 在 base 只有远程跟踪 ref 时
// 被 DWIM 顶替成「检出 base」，丢掉 X 且退出码为 0——要的分支与实到分支从来
// 没被比对过，这是结构性空白，不是某一次 git 行为的补丁。
func verifyBranchIdentity(ctx context.Context, workDir, want string) error {
	out, stderr, err := gitRun(ctx, workDir, "branch", "--show-current")
	got := strings.TrimSpace(out)
	if err != nil {
		return fmt.Errorf("%w: 读取工作区 %s 的当前分支失败（请求分支 %s）: %s",
			ErrBranchIdentityMismatch, workDir, want, strings.TrimSpace(stderr))
	}
	if got != want {
		return fmt.Errorf("%w: 请求分支 %s，git 实际给出 %s（工作区 %s）",
			ErrBranchIdentityMismatch, want, got, workDir)
	}
	return nil
}

// rollbackWorkspace 在 PrepareWorkspace 内部失败时回滚已建的工作区。
//
// 为什么不能交给 manager 的补偿 defer：那个 defer 用的是 PrepareWorkspace 的
// **返回值** ws，失败时它是零值，WorkDir 为空，compensateWorkspace 会直接返回。
// 所以 PrepareWorkspace 自己建的东西必须自己收。
func rollbackWorkspace(ctx context.Context, repo string, ws Workspace) {
	if ws.WorkDir == "" {
		return
	}
	if ws.Managed {
		if err := RemoveManagedWorktree(ctx, repo, ws.WorkDir); err != nil {
			log().Error("回滚 managed worktree 失败，需人工清理", "repo", repo,
				"workdir", ws.WorkDir, "cause", err)
			return
		}
		log().Info("已回滚 managed worktree", "repo", repo, "workdir", ws.WorkDir)
		return
	}
	if ws.PrevRef == "" {
		log().Warn("无 PrevRef 可复原，工作区停在当前 ref", "workdir", ws.WorkDir)
		return
	}
	if _, stderr, err := gitRun(ctx, ws.WorkDir, "checkout", ws.PrevRef); err != nil {
		log().Error("回滚切回原 ref 失败，需人工处理", "workdir", ws.WorkDir,
			"prev_ref", ws.PrevRef, "stderr", strings.TrimSpace(stderr), "cause", err)
		return
	}
	log().Info("已回滚至原 ref", "workdir", ws.WorkDir, "prev_ref", ws.PrevRef)
}
```

- [ ] **Step 5: 在 PrepareWorkspace 末尾接入守卫**

在 `PrepareWorkspace` 里、`log().Info("工作区准备完成", ...)` **之前**插入：

```go
	// 守卫（B76）：三条路径统一在此核对，因为它们都已把结果收敛进 ws
	if verr := verifyBranchIdentity(ctx, ws.WorkDir, ws.Branch); verr != nil {
		log().Error("工作区分支身份核对失败，回滚并拒发", "task", req.TaskID,
			"want", ws.Branch, "workdir", ws.WorkDir, "managed", ws.Managed, "cause", verr)
		rollbackWorkspace(ctx, req.Repo, ws)
		return Workspace{}, verr
	}
	log().Info("工作区分支身份核对通过", "task", req.TaskID, "branch", ws.Branch)
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestPrepareWorkspace -count=1 -v`
Expected: 新用例 PASS，既有 `TestPrepareWorkspace*` 全部仍 PASS（守卫对正常路径是透明的）。

- [ ] **Step 7: 加日志（本任务的日志点自检）**

确认已覆盖：核对通过 Info（分支名）、核对失败 Error（want/got/workdir/managed/cause）、回滚成功 Info、回滚失败 Error（带需人工清理的措辞）、无 PrevRef 可复原 Warn。全部走 `log()`，无 `fmt.Printf`。

- [ ] **Step 8: 加注释（本任务的注释自检）**

确认已覆盖：`ErrBranchIdentityMismatch` 哨兵说明；`verifyBranchIdentity` / `rollbackWorkspace` doc 注释含参数、返回、以及**为什么存在**（守卫的存在理由不在代码表面，尤其 `rollbackWorkspace` 里「manager 的 defer 接不住」这一条）；`initClonedRepo` 说明为什么必须是克隆。

- [ ] **Step 9: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_test.go
git commit -m "feat(workspace): 分支身份守卫——git 报成功不等于给了你要的分支

B76 的结构性修复：要的分支与实到分支从来没被比对过。worktree add -b
在 base 只有远程跟踪 ref 时被 DWIM 顶替、丢掉 -b 且退出码为 0，这条路
上唯一的信号是一句 WARN。核对不符即回滚拒发。

测试 fixture 必须是克隆仓库——本地 git init 的仓库里同名分支总是存在，
恰好绕开触发前提，这是该缺陷长期无人抓住的原因。"
```

---

### Task 2: 起点解析成 sha + `BaseCommit` 契约对齐

**Files:**
- Modify: `internal/agentd/workspace.go`（新增 `resolveCommit`）
- Modify: `internal/agentd/manager.go`（起点决议段调用解析；`BaseCommit: start` 因此变成 sha）
- Test: `internal/agentd/workspace_test.go`（`resolveCommit` 单测）
- Test: `internal/agentd/manager_test.go`（端到端：base 给分支名，任务分支必须是请求的那个）

> 端到端用例放 `manager_test.go` 而不是 `integration_test.go`：后者是 `package agentd_test`（外部测试包），看不见 `resolveCommit`、`initClonedRepo`、`gitOut` 这些包内符号。`manager_test.go` 是 `package agentd`，且已有 `m.Dispatch` 级别的用例可照抄。

**Interfaces:**
- Consumes: Task 1 的 `initClonedRepo`
- Produces: `resolveCommit(ctx context.Context, repo, rev string) (string, error)` —— 成功返回 40 位 sha；失败返回包 `ErrBadWorkspaceReq` 的错误（server 层映射 400）

- [ ] **Step 1: 写 `resolveCommit` 的失败测试**

```go
// TestResolveCommitRemoteOnlyBranch 钉住 B76 的源头修复：base 只有 origin/<name>
// 时也必须解析得出（取远程尖端），否则修复会以「拒发」的形式打断正常派发。
func TestResolveCommitRemoteOnlyBranch(t *testing.T) {
	clone := initClonedRepo(t, "shared-base")
	want := gitOut(t, clone, "rev-parse", "origin/shared-base")

	got, err := resolveCommit(context.Background(), clone, "shared-base")
	if err != nil {
		t.Fatalf("远程跟踪分支简写应可解析: %v", err)
	}
	if got != want {
		t.Fatalf("sha=%q, want %q", got, want)
	}
}

// TestResolveCommitAnnotatedTagPeelsToCommit 钉住 ^{commit} 剥离：annotated tag
// 的裸 rev-parse 给的是 tag 对象，直接拿去开分支会得到非预期的起点。
func TestResolveCommitAnnotatedTagPeelsToCommit(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "tag", "-a", "v1", "-m", "release 1")

	got, err := resolveCommit(context.Background(), repo, "v1")
	if err != nil {
		t.Fatalf("annotated tag 应可解析: %v", err)
	}
	if got != head {
		t.Fatalf("应剥离到 commit: got=%q, want=%q", got, head)
	}
}

// TestResolveCommitMissingRejects 钉住拒发出口：起点不存在时给可操作的报错，
// 而不是让它一路走到 git 内部措辞（`is not a commit`）才炸。
func TestResolveCommitMissingRejects(t *testing.T) {
	repo := initTestRepo(t)

	_, err := resolveCommit(context.Background(), repo, "no-such-branch")
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("应按 ErrBadWorkspaceReq 拒发, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-branch") || !strings.Contains(err.Error(), "git push") {
		t.Fatalf("错误文本应含起点原文与可操作出路: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestResolveCommit -count=1`
Expected: 编译失败 —— `undefined: resolveCommit`。

- [ ] **Step 3: 实现 `resolveCommit`**

加到 `internal/agentd/workspace.go`（`ResolveBaseline` 附近）：

```go
// resolveCommit 把任意 commit-ish（分支名/tag/sha）解析成 40 位提交号。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径
//   - rev: 待解析的起点原文（用户的 --base 或决议出的基线）
//
// 返回：
//   - 40 位 sha；解析不出时返回包 ErrBadWorkspaceReq 的错误（server 映射 400）
//
// 为什么起点必须以 sha 形态交给 git（B76）：给分支名会触发 DWIM——base 只有
// origin/<name> 时，`worktree add -b X <dir> <base>` 会忽略显式的 -b、开出
// 名为 <base> 的分支，且退出码为 0。传 sha 让 DWIM 从原理上无从发生。
//
// 注意：rev-parse 对「只有远程跟踪 ref 的分支简写」同样解析得出（返回远程
// 尖端），所以 --base 的用户语义零变化。^{commit} 的剥离是必需的：rev 可能
// 是 annotated tag，裸解析给的是 tag 对象而不是提交。
func resolveCommit(ctx context.Context, repo, rev string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	sha := strings.TrimSpace(out)
	if err != nil || sha == "" {
		log().Warn("起点解析失败，拒绝派发", "repo", repo, "base", rev,
			"stderr", strings.TrimSpace(truncateRunes(stderr, 300)))
		return "", fmt.Errorf("%w: 起点 %s 在任务仓库中不存在"+
			"（若它是你本地的分支，先 git push 再派发；或换一个起点）", ErrBadWorkspaceReq, rev)
	}
	log().Info("起点已解析为提交号", "repo", repo, "base", rev, "sha", sha)
	return sha, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestResolveCommit -count=1 -v`
Expected: 三条全 PASS。

- [ ] **Step 5: 写端到端失败测试**

加到 `internal/agentd/manager_test.go`（`compensateOnlyManager` 是个可正常派发的 Manager——名字来自它最早的用途，dataDir 是真临时目录、adapter 是 fake，Dispatch 会成功）：

```go
// TestDispatchBaseBranchNameYieldsRequestedBranch 是 B76 的端到端守门人：
// --base 给分支名（且该分支在任务仓库里只有远程跟踪 ref）时，任务必须开在
// --new-branch 请求的分支上，且 BaseCommit 落库为 40 位 sha。
func TestDispatchBaseBranchNameYieldsRequestedBranch(t *testing.T) {
	clone := initClonedRepo(t, "shared-base")
	wantBase := gitOut(t, clone, "rev-parse", "upstream/shared-base")
	m := compensateOnlyManager(t)
	pid := registerTestProject(t, m, clone)

	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
		NewBranch: "feat/wanted", Base: "shared-base",
	})
	if err != nil {
		t.Fatalf("派发应成功: %v", err)
	}
	if task.Branch != "feat/wanted" {
		t.Fatalf("任务分支应为请求的那个, got %q", task.Branch)
	}
	// BaseCommit 的注释契约是「40 位 sha」，B76 之前它存的是 --base 原文
	if task.BaseCommit != wantBase {
		t.Fatalf("BaseCommit 应为解析后的 sha: got %q, want %q", task.BaseCommit, wantBase)
	}
	// 分支确实建在任务仓库里，且指向 base 的尖端
	if got := gitOut(t, clone, "rev-parse", "refs/heads/feat/wanted"); got != wantBase {
		t.Fatalf("新分支应从解析后的起点开出: branch=%s base=%s", got, wantBase)
	}
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestDispatchBaseBranchNameYieldsRequestedBranch -count=1 -v`
Expected: FAIL —— Task 1 的守卫此刻会把这次派发**拒掉**（DWIM 让实到分支是 `shared-base`），报错含 `工作区分支与请求不符`。这正是守卫在起作用的证据；本步要的是让它变成正常成功。

- [ ] **Step 7: 在起点决议段接入解析**

`internal/agentd/manager.go`，紧接 `m.log.Info("基线起点已确定", ...)` 之后、`checkProcHeadroom` 之前插入：

```go
	// B76：起点必须以 sha 形态交给 git。给分支名会触发 DWIM——base 只有
	// origin/<name> 时 git 会忽略显式的 -b 并开出 base 名字的分支，退出码还是 0。
	// 解析放在这里（而不是 PrepareWorkspace 内部）是因为 start 同时喂给工作区
	// 准备与任务记录的 BaseCommit，一次解析服务两处，两者不可能再分叉。
	if start != "" {
		resolved, rerr := resolveCommit(ctx, repoPath, start)
		if rerr != nil {
			return nil, rerr
		}
		if resolved != start {
			m.log.Info("起点原文已解析为提交号", "repo", repoPath, "base", start, "sha", resolved)
		}
		start = resolved
	}
```

`BaseCommit: start` 那一行不需要改动——它现在拿到的自然是 sha。

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/agentd/ -count=1`
Expected: 新端到端用例 PASS；既有 `TestDispatch*`、`TestResolveBaseline*`、`TestPrepareWorkspace*` 全部仍 PASS。

- [ ] **Step 9: 补原地与用户树两条路径的正向用例**

这两条路径此前在同样输入下是硬失败（`fatal: '<base>' is not a commit and a branch cannot be created from it`），修复后应正常工作。加到 `workspace_test.go`：

```go
// TestPrepareWorkspaceRemoteOnlyBaseAllPaths 钉住三条工作树路径在「base 只有
// origin/<name>」时的一致行为：都应建出请求的分支。原地与用户树此前是硬失败。
func TestPrepareWorkspaceRemoteOnlyBaseAllPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(t *testing.T, clone, base string) WorkspaceReq
	}{
		{"新树", func(t *testing.T, clone, base string) WorkspaceReq {
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-nw", NewWorktree: true,
				WorktreesDir: filepath.Join(t.TempDir(), "w"), NewBranch: "feat/wanted", Base: base}
		}},
		{"原地", func(t *testing.T, clone, base string) WorkspaceReq {
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-ip", NewBranch: "feat/wanted", Base: base}
		}},
		{"用户树", func(t *testing.T, clone, base string) WorkspaceReq {
			wt := filepath.Join(t.TempDir(), "userwt")
			gitT(t, clone, "worktree", "add", "-q", wt)
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-uw", Worktree: wt,
				NewBranch: "feat/wanted", Base: base}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone := initClonedRepo(t, "shared-base")
			// 调用方（manager）已把起点解析成 sha，测试同样喂 sha
			base, err := resolveCommit(context.Background(), clone, "shared-base")
			if err != nil {
				t.Fatalf("解析起点: %v", err)
			}
			ws, err := PrepareWorkspace(context.Background(), tc.mk(t, clone, base))
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			if ws.Branch != "feat/wanted" {
				t.Fatalf("ws.Branch=%q", ws.Branch)
			}
			if cur := gitOut(t, ws.WorkDir, "branch", "--show-current"); cur != "feat/wanted" {
				t.Fatalf("工作区当前分支=%q", cur)
			}
		})
	}
}
```

Run: `go test ./internal/agentd/ -run TestPrepareWorkspaceRemoteOnlyBaseAllPaths -count=1 -v`
Expected: 三个子用例全 PASS。

- [ ] **Step 10: 日志与注释自检**

日志：`resolveCommit` 成功 Info（base 原文 + sha）、失败 Warn 带 git stderr 原文；manager 侧解析后 Info（仅在原文与 sha 不同时打，sha 输入不制造噪音）。注释：`resolveCommit` doc 已含参数/返回/为什么必须传 sha/为什么要 `^{commit}`；manager 插入点的中文注释已说明「为什么解析放这里而不是 PrepareWorkspace 内部」。

- [ ] **Step 11: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/manager.go \
        internal/agentd/workspace_test.go internal/agentd/manager_test.go
git commit -m "fix(dispatch): 起点解析成 sha，从源头消灭 git DWIM

B76 根因：worktree add -b <new> <dir> <base> 在 base 只有 origin/<name>
时被 DWIM 顶替，丢掉显式 -b 且退出码为 0，任务静默开在 base 分支上并
改写它。起点在决议时统一 rev-parse 成 40 位 sha，DWIM 无从发生。

一次解析同时服务工作区准备与任务记录，proto.Task.BaseCommit 由此终于
符合其注释里早已写明的「40 位 sha」契约。原地/用户树两条路径也从难懂
的硬失败转为正常工作。"
```

---

### Task 3: dispatch 回显分支名与解析后起点

**Files:**
- Modify: `cmd/dispatch.go`（提出纯函数 `baselineLine`，替换现有回显）
- Test: `cmd/dispatch_test.go`

**Interfaces:**
- Consumes: `proto.Task.Branch` / `.BaseCommit` / `.BaseAhead`（Task 2 保证 `BaseCommit` 是 sha）
- Produces: `baselineLine(task *proto.Task, userBase string) string`

- [ ] **Step 1: 写失败测试**

```go
// TestBaselineLine 钉住派发回显：分支名、解析后起点短号、用户输入的 --base
// 原文三者同行互证。B76 现场里只有一行「基线 worktre」——分支名被按短 sha
// 截成 7 字符，审核者盯着它也看不出分支错了。
func TestBaselineLine(t *testing.T) {
	sha := "e911147aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name, userBase string
		task           proto.Task
		want           string
	}{
		{"给了分支名", "worktree-b69-b70-proc-footprint",
			proto.Task{Branch: "feat/b72-birth-registry", BaseCommit: sha},
			"分支 feat/b72-birth-registry，起点 e911147（worktree-b69-b70-proc-footprint）"},
		{"没给 base", "",
			proto.Task{Branch: "handoff/abcdefgh", BaseCommit: sha},
			"分支 handoff/abcdefgh，起点 e911147"},
		{"base 本来就是 sha", sha,
			proto.Task{Branch: "feat/x", BaseCommit: sha},
			"分支 feat/x，起点 e911147"},
		{"任务仓库领先", "main",
			proto.Task{Branch: "feat/x", BaseCommit: sha, BaseAhead: 3},
			"分支 feat/x，起点 e911147（main）（任务仓库 HEAD 领先 3 个提交，新分支不含它们）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := baselineLine(&tc.task, tc.userBase); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestBaselineLine -count=1`
Expected: 编译失败 —— `undefined: baselineLine`。

- [ ] **Step 3: 实现 `baselineLine` 并替换回显**

`cmd/dispatch.go`，在 `shortSHA` 旁边加：

```go
// looksLikeSHA 判断字符串是否是提交号形态（全十六进制且不短于 7 位）。
// 用途单一：决定回显里要不要把用户输入的 --base 原文再打一遍——原文就是
// sha 时再打一遍是纯噪音。
func looksLikeSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// baselineLine 拼派发后回显给审核者的那一行（分支 + 起点 [+ base 原文] [+ 领先提示]）。
//
// 参数：
//   - task: 派发应答里的任务；BaseCommit 由 agentd 保证是 40 位 sha
//   - userBase: 用户在命令行给的 --base 原文（空=没给）
//
// 为什么要同时打三样：B76 的现场里回显只有「基线 worktre」——BaseCommit 当时
// 存的是分支名，又被 shortSHA 按短 sha 截成 7 字符，于是「任务开错了分支」这件
// 事在派发那一刻毫无痕迹，一路静默到收工 pull 才炸。三个信息同行互证，任何一
// 项不符当场看得见。
func baselineLine(task *proto.Task, userBase string) string {
	line := fmt.Sprintf("分支 %s，起点 %s", task.Branch, shortSHA(task.BaseCommit))
	if userBase != "" && !looksLikeSHA(userBase) {
		line += "（" + userBase + "）"
	}
	if task.BaseAhead > 0 {
		line += fmt.Sprintf("（任务仓库 HEAD 领先 %d 个提交，新分支不含它们）", task.BaseAhead)
	}
	return line
}
```

把 RunE 里那段回显替换成：

```go
		if task.BaseCommit != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), baselineLine(task, dispatchBase))
		}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./cmd/ -count=1`
Expected: `TestBaselineLine` 四个子用例全 PASS，`cmd` 包其余用例仍 PASS。

- [ ] **Step 5: 注释自检**

`baselineLine` 与 `looksLikeSHA` 均有 doc 注释；`baselineLine` 写清了「为什么三样同打」这个**理由**（B76 现场），不是复述代码。此处不加日志：这是面向用户的 stderr 输出，不是可观测性日志；stdout 的单行任务 JSON 契约不得破坏。

- [ ] **Step 6: 提交**

```bash
git add cmd/dispatch.go cmd/dispatch_test.go
git commit -m "feat(dispatch): 回显分支名与解析后起点，让派发当场可验

B76 现场里回显只有一行「基线 worktre」——分支名被按短 sha 截成 7 字符，
分支开错这件事在派发那一刻毫无痕迹。改为分支名、起点短号、用户输入的
--base 原文三者同行互证。"
```

---

### Task 4: 变异检验、六闸门与真机烟测

测试没咬住根因是本条最需要防的失败模式——这个缺陷此前之所以逃过全部测试，就是因为 fixture 恰好绕开了触发前提。

**Files:** 无生产代码改动（仅在验证不通过时回到 Task 1–3 修）

- [ ] **Step 1: 变异检验之一——摘掉 sha 解析**

临时把 Task 2 Step 7 插入的解析块注释掉。
Run: `go test ./internal/agentd/ -run 'TestDispatchBaseBranchNameYieldsRequestedBranch|TestPrepareWorkspaceRemoteOnlyBaseAllPaths' -count=1`
Expected: **必须翻红**。不翻红说明 fixture 没造出「base 只有 origin/<name>」这个前提（多半退化成本地分支存在），测试没咬住根因——回到 Task 1 Step 1 修 fixture。
恢复代码后重跑确认转绿。

- [ ] **Step 2: 变异检验之二——摘掉守卫**

临时把 Task 1 Step 5 插入的守卫块注释掉。
Run: `go test ./internal/agentd/ -run TestPrepareWorkspaceRejectsBranchIdentityMismatch -count=1`
Expected: **必须翻红**。
恢复代码后重跑确认转绿。

- [ ] **Step 3: 六闸门**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```
Expected: `gofmt -l .` 无输出；`go test` 全包 ok、0 FAIL。

```bash
go test -race ./internal/agentd/ ./cmd/ -count=1
```
Expected: ok，无 race 报告。

- [ ] **Step 4: devbox 真机烟测**

在本地仓库造一条分支并推上去，然后重放 B76 原案：

```bash
git push -u origin HEAD:refs/heads/b76-smoke-base
handoff dispatch --target devbox --new-worktree \
  --new-branch feat/b76-smoke --base b76-smoke-base \
  --executor fake --prompt "什么都不用做"
```

逐项确认（四条全过才算通过）：

1. dispatch 的 stderr 打出 `分支 feat/b76-smoke，起点 <7位短号>（b76-smoke-base）`
2. `ssh sycm@100.73.238.21 'git -C /Users/sycm/workspace/handoff rev-parse --verify refs/heads/feat/b76-smoke'` 返回 sha，且等于 `b76-smoke-base` 的尖端
3. 应答 JSON 的 `.base_commit` 是 40 位 sha（不再是分支名）
4. 任务工作树的当前分支是 `feat/b76-smoke`（`handoff run <task> git branch --show-current`）

收工：`handoff done <task>`，并删掉烟测分支（本地与 origin）。

- [ ] **Step 5: 完工前的 instrumenting-code 清单自检**

- [ ] 每个错误分支都有带上下文与 cause 的日志
- [ ] 每次外部调用（git）前后有日志 —— `gitRun` 已统一覆盖
- [ ] 成功路径有出口日志（分支身份核对通过、起点解析成功）
- [ ] 无 `fmt.Printf` / `print` 充当日志（`cmd` 里对 stderr 的 `Fprintln` 是用户可见输出，不是日志）
- [ ] 新增导出符号有 doc 注释；非显然分支有「为什么」注释

- [ ] **Step 6: 回填 backlog 并提交**

把 B76 行改为 `✅ done(已验)`，`验收` 列写入：六闸门实跑结果（命令 + 包数/FAIL 数）、两条变异检验的翻红确认、devbox 真机四项确认；`原型/流程图` 为 `—`，自动免除对照。

```bash
git add docs/superpowers/backlog.md
git commit -m "docs(backlog): B76 验收回填——六闸门、两条变异检验、devbox 真机四项"
```

---

## Self-Review

**Spec 覆盖**：§3.1 起点解析 → Task 2 Step 3/7；§3.2 守卫（含回滚归属与 `recordNewBranchTip` 分工）→ Task 1 Step 4/5；§3.3 `BaseCommit` 契约 → Task 2 Step 7 + 端到端断言，回显 → Task 3；§4.1 克隆 fixture → Task 1 Step 1；§4.2 七条断言 → Task 1 Step 2、Task 2 Step 1/5/9、Task 3 Step 1；§4.3 变异检验 → Task 4 Step 1/2；§4.4 真机烟测 → Task 4 Step 4；§4.5 日志与注释自检 → 各任务的自检步 + Task 4 Step 5。§5 影响与兼容、§6 已知残留（B80）无需实现任务。

**类型一致性**：`resolveCommit(ctx, repo, rev) (string, error)` 在 Task 2 Step 3 定义，Task 2 Step 9 与 manager 调用点签名一致；`verifyBranchIdentity(ctx, workDir, want) error` 与 `rollbackWorkspace(ctx, repo, ws)` 在 Task 1 Step 4 定义、Step 5 调用一致；`baselineLine(*proto.Task, string) string` 在 Task 3 Step 1 使用、Step 3 定义一致；`initClonedRepo(t, baseBranch) string` 在 Task 1 定义，Task 2 三处复用一致。

**已知的执行期注意点**：Task 2 Step 6 的端到端用例在修复前的失败形态是「被 Task 1 的守卫拒发」而非「静默拿到错分支」——因为 Task 1 先落地。这是预期的，不要据此认为测试写错了。
