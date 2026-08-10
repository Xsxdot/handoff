# 派发入口的一致性与可诊断性（B42 + B43 + B45）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `Manager.Dispatch` 的派发前置补齐三道闸——仓库有效性（B45）、工作目录占用守卫（B42）、新树模式下的主仓脏快照（B43）——让「派发看起来成功了、或失败得看不懂」这三种形态在审核者这一侧就有答案。

**Architecture:** 三处校验统一放进 `Manager.Dispatch` 的前置块，固定顺序 `EnsureRepoUsable → guardWorkdirBusy → ResolveBaseline → PrepareWorkspace`。纯 git 操作（`EnsureRepoUsable` / 脏快照采集）的函数体落在 `internal/agentd/workspace.go`，调用点在 `Dispatch`；占用判定的事实来源是既有任务表，新增 `store.ActiveTasksByWorkDir` 查询，不引入任何新状态。B43 的快照是诊断信息，随任务落库并在 `dispatch` 的 stderr 回显，**不拒发**。

**Tech Stack:** Go（标准库 + `modernc.org/sqlite` + cobra）；测试用 `go test` + `httptest` + `internal/executor/fake`。

**Spec:** [docs/superpowers/specs/2026-08-10-dispatch-entry-consistency-design.md](../specs/2026-08-10-dispatch-entry-consistency-design.md)

## Global Constraints

- 工作分支 `handoff/b42-dispatch-entry`，工作目录即本仓库 checkout；每个 Task 结束时提交一次。
- 日志一律走既有 logger：`internal/agentd` 包内用 `log()`（= `slog.Default()`）或 `m.log`；`internal/store` 包内用 `log()`。**禁止 `fmt.Printf` / `println`**。
- 注释一律中文，新文件写职责+边界，导出函数写参数/返回/注意，非显然分支写「为什么」。
- 报文与日志中的中文文案照抄本计划，不要改写——集成测试按子串断言。
- `stdout` 的「单行任务 JSON」契约不可破（`cmd/dispatch.go:127` 的注释说明了原因）：所有提示走 stderr。
- 每个实现类 Task 的最后一步是**变异检验**：手工打断刚写的实现，确认对应测试转红，再改回。这是 B44 的直接教训（spec §5.4）。
- 全量闸门（Task 6 统一跑，但每个 Task 提交前至少跑本包测试）：`gofmt -l .` 无输出、`go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./cmd/ ./internal/agentd/ ./internal/store/`、`GOOS=windows GOARCH=amd64 go build ./...`。

---

## File Structure

| 文件 | 动作 | 本次承担的职责 |
|------|------|---------------|
| `internal/agentd/workspace.go` | 修改 | 新增 `EnsureRepoUsable`（B45 判据）、`ErrWorkdirBusy` 哨兵、`repoDirtySnapshot` + `maxDirtyFilesListed`（B43 采集）、`Workspace` 两个快照字段；修 `headCommit` 的过期注释 |
| `internal/agentd/manager.go` | 修改 | `Dispatch` 前置块接入两道闸；新增 `guardWorkdirBusy`；`Stop` 的裸终态比较改用 `IsTerminal`；修 `:532` 的过期注释；把快照写进 `proto.Task` |
| `internal/agentd/server.go` | 修改 | `writeDispatchError` 新增 `ErrWorkdirBusy → 409` 一路 + 映射规则文档注释 |
| `internal/proto/proto.go` | 修改 | `TerminalStates` / `TaskState.IsTerminal()`；`Task` 新增 `RepoDirtyCount` / `RepoDirtyFiles` |
| `internal/store/store.go` | 修改 | 读取列清单去重（`taskColumns` + `scanTaskRow`）；新增 `ActiveTasksByWorkDir`；两列 DDL + 迁移 + 读写；修 `:78` 的过期建表注释 |
| `cmd/dispatch.go` | 修改 | 脏快照提示行（stderr） |
| `internal/agentd/workspace_test.go` | 修改 | `EnsureRepoUsable` 的三条单测 |
| `internal/agentd/integration_test.go` | 修改 | B45 两条、B42 四条集成测试 |
| `internal/store/store_test.go` | 修改 | `ActiveTasksByWorkDir` 两条单测 + 快照字段回读 |
| `cmd/dispatch_test.go` | 修改 | 脏快照提示行的两条渲染测试 |

**为什么不新建文件**：三处改动全部落在既有函数的调用链上（`Dispatch` 前置、`writeDispatchError` 映射、`tasks` 表读写），拆出新文件只会让「派发前置」这一段逻辑分散在两个文件里。`workspace.go` 884 行、`store.go` 805 行，都还在可读范围内。

---

## Task 1: B45 —— 仓库有效性校验

**Files:**
- Modify: `internal/agentd/workspace.go`（新增 `EnsureRepoUsable`；改 `headCommit` 注释，约 `:536-548`）
- Modify: `internal/agentd/manager.go`（`Dispatch` 前置块，`ResolveBaseline` 调用之前，约 `:462`）
- Test: `internal/agentd/workspace_test.go`、`internal/agentd/integration_test.go`

**Interfaces:**
- Consumes: 既有 `gitRun(ctx, repo, args...) (stdout, stderr string, err error)`、`ErrRepoUnusable`、`truncateRunes(s string, n int) string`
- Produces: `func EnsureRepoUsable(ctx context.Context, repo string) error`——Task 3 的守卫排在它之后，Task 4/5 不依赖它

- [ ] **Step 1: 写失败测试（单测三条）**

追加到 `internal/agentd/workspace_test.go` 末尾（包 `agentd`，可直接调未导出符号）：

```go
// TestEnsureRepoUsableAcceptsRepo 正常仓库必须放行——守卫不能把好路径也拦下来。
func TestEnsureRepoUsableAcceptsRepo(t *testing.T) {
	repo := initGitRepo(t)
	if err := EnsureRepoUsable(context.Background(), repo); err != nil {
		t.Fatalf("正常仓库 EnsureRepoUsable: %v", err)
	}
}

// TestEnsureRepoUsableRejectsNonGitPath 钉住 B45 的判据：路径存在但不是 git 仓库
// 时必须归入 ErrRepoUnusable，而不是留给后面的 worktree add 扁平成 500。
// 错误文本必须带 git 的原因，只有哨兵等于没说。
func TestEnsureRepoUsableRejectsNonGitPath(t *testing.T) {
	err := EnsureRepoUsable(context.Background(), t.TempDir())
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("非 git 目录 err = %v, want ErrRepoUnusable", err)
	}
	if err.Error() == ErrRepoUnusable.Error() {
		t.Fatalf("错误文本必须带 git 原因，不能只有哨兵: %q", err.Error())
	}
}

// TestEnsureRepoUsableGitMissing 覆盖 spec §3.2 的第二种形态：git 不在 PATH
// （gitRun 返回 exec 错误、stderr 为空）同样归入 ErrRepoUnusable，不能因为
// stderr 空就漏掉分类。
func TestEnsureRepoUsableGitMissing(t *testing.T) {
	repo := initGitRepo(t) // 必须在改 PATH 之前建仓库
	t.Setenv("PATH", "")
	err := EnsureRepoUsable(context.Background(), repo)
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("git 不在 PATH 时 err = %v, want ErrRepoUnusable", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestEnsureRepoUsable -count=1
```

Expected: 编译失败 `undefined: EnsureRepoUsable`。

- [ ] **Step 3: 实现最小版本**

在 `internal/agentd/workspace.go` 的 `ensureCleanWorktree` 函数**之前**插入：

```go
func EnsureRepoUsable(ctx context.Context, repo string) error {
	_, stderr, err := gitRun(ctx, repo, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrRepoUnusable, strings.TrimSpace(stderr), err)
	}
	return nil
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -run TestEnsureRepoUsable -count=1 -v
```

Expected: 三条全 PASS。

- [ ] **Step 5: 写集成测试（两条）**

追加到 `internal/agentd/integration_test.go` 末尾：

```go
// TestDispatchNewWorktreeRepoUnusable400 覆盖 B45 报告里的那半：managed 路径
// （--new-worktree）上仓库不可用，旧行为一路走到 worktree add 失败、落 500
// 「派发任务失败」，真因只在 agentd.log 里。现在必须是 400 + 可读原因。
func TestDispatchNewWorktreeRepoUnusable400(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: t.TempDir(), PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err == nil {
		t.Fatal("非 git 路径 + --new-worktree 应被拒绝")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("应为 400 + 可读原因, got: %v", err)
	}
}

// TestDispatchRepoUnusableNotMisdiagnosed 是 B45 动机场景（远程派发）的守门人：
// 带 base_commit 时，非 git 路径旧行为会被 ResolveBaseline 误诊成
// ErrBaseCommitMissing 的 400「任务仓库落后于本地；请先在本地 git push」——
// 一个自信的错答案，比沉默更糟。
func TestDispatchRepoUnusableNotMisdiagnosed(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: t.TempDir(), PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
		BaseCommit: strings.Repeat("a", 40),
	})
	if err == nil {
		t.Fatal("非 git 路径应被拒绝")
	}
	if strings.Contains(err.Error(), "git push") || strings.Contains(err.Error(), "落后") {
		t.Fatalf("非 git 仓库不该被误诊为基线缺失: %v", err)
	}
	if !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("应归入仓库不可用, got: %v", err)
	}
}
```

- [ ] **Step 6: 跑集成测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestDispatchNewWorktreeRepoUnusable400|TestDispatchRepoUnusableNotMisdiagnosed' -count=1
```

Expected: 两条都 FAIL——第一条报 500、第二条报文含「请先在本地 git push」。

- [ ] **Step 7: 在 Dispatch 前置块接入**

`internal/agentd/manager.go`，在 `// 基线决议（B4 校验 + B35 起点）` 注释块**之前**插入：

```go
	// 派发前置 1（B45）：仓库有效性。必须排在 ResolveBaseline 之前——对一个非
	// git 路径，ResolveBaseline 会把它误诊成 ErrBaseCommitMissing（「任务仓库落后
	// 于本地，请先 git push」），那是个比沉默更糟的答案；managed 路径上则一路
	// 走到 worktree add 才失败，扁平成 500
	if err := EnsureRepoUsable(ctx, req.Repo); err != nil {
		return nil, err
	}
```

- [ ] **Step 8: 跑测试确认通过**

```bash
go test ./internal/agentd/ -count=1
```

Expected: 全 PASS。特别确认 `TestDispatchRepoUnusable400` 与 `TestDispatchUnknownError500` 仍绿——后者用「关掉 store」构造未知错误、仓库是正常 git 仓库，不受本次改动影响（spec §5.2 提到的冲突未发生，无需收窄）。

- [ ] **Step 9: 加关键节点日志**

在 `EnsureRepoUsable` 里补两条（`gitRun` 自身已记录 git 调用与失败 stderr，这里补的是**判定结论**）：

```go
func EnsureRepoUsable(ctx context.Context, repo string) error {
	_, stderr, err := gitRun(ctx, repo, "rev-parse", "--git-dir")
	if err != nil {
		log().Warn("dispatch 前置：任务仓库不可用，拒绝派发", "repo", repo,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return fmt.Errorf("%w: %s: %v", ErrRepoUnusable, strings.TrimSpace(stderr), err)
	}
	log().Info("dispatch 前置：仓库有效性校验通过", "repo", repo)
	return nil
}
```

成功路径也打：否则日志里分不清「校验过了」与「这段代码根本没跑到」。

- [ ] **Step 10: 加注释**

其一，`EnsureRepoUsable` 的文档注释（放在函数上方）：

```go
// EnsureRepoUsable 校验 repo 确实是一个可用的 git 仓库。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径
//
// 返回：
//   - nil：是可用的 git 仓库
//   - ErrRepoUnusable：路径不存在 / 不是 git 仓库 / git 不在 PATH / 权限不足，
//     错误文本带 git stderr 原文（server 层据此给 400，见 writeDispatchError）
//
// 注意：
//   - 由 Dispatch 在 ResolveBaseline 之前调用。放在那里而不是建树前，是因为
//     ResolveBaseline 对非 git 仓库会误报成 ErrBaseCommitMissing（「落后于本地，
//     请先 push」），那是个比沉默更糟的答案
//   - 判据用 rev-parse --git-dir 而不是 grep worktree add 的错误串：前者是显式
//     判据，后者依赖 git 的文案不变
//   - ensureCleanWorktree 里原有的 ErrRepoUnusable 包装保留——它仍是 git status
//     因其他原因失败时的兜底
```

其二，改掉 `headCommit` 上方那句已被本次修复推翻的注释（原文「真正的仓库问题会在 PrepareWorkspace 的脏检查/建树阶段暴露」——它正是本缺口的来源，留着就是骗后人的线索）：

```go
// headCommit 取仓库当前 HEAD 的完整 sha。
//
// 返回空串只对应「仓库一个提交都没有」：仓库有效性已由 Dispatch 前置的
// EnsureRepoUsable 保证（B45），走到这里时路径一定是可用的 git 仓库。
// 空起点交给 git 默认行为，不是错误。
```

- [ ] **Step 11: 变异检验**

把 `EnsureRepoUsable` 的返回改成恒 `nil`（保留 git 调用），跑：

```bash
go test ./internal/agentd/ -run 'TestEnsureRepoUsable|TestDispatchNewWorktreeRepoUnusable400|TestDispatchRepoUnusableNotMisdiagnosed' -count=1
```

Expected: 至少三条转红。确认后改回，重跑确认全绿。

- [ ] **Step 12: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/ -count=1
```

```bash
git add internal/agentd/workspace.go internal/agentd/manager.go internal/agentd/workspace_test.go internal/agentd/integration_test.go && git commit -m "fix(b45): 派发前校验仓库有效性，非 git 路径不再扁平成 500/误诊为基线缺失"
```

---

## Task 2: B42 前半 —— 终态谓词与按工作目录查活跃任务

**Files:**
- Modify: `internal/proto/proto.go`（状态常量块之后）
- Modify: `internal/agentd/manager.go`（`Stop` 里的裸终态比较，约 `:923`）
- Modify: `internal/store/store.go`（`Open` 的建表注释约 `:78`；`GetTask` / `ListTasks` 改用共享列清单；新增 `ActiveTasksByWorkDir`）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: 既有 `proto.TaskState` 常量、`store.Store.db`、`parseTime`
- Produces:
  - `var proto.TerminalStates []proto.TaskState`
  - `func (s proto.TaskState) IsTerminal() bool`
  - `func (s *store.Store) ActiveTasksByWorkDir(workDir string) ([]proto.Task, error)`——Task 3 的守卫直接调它
  - `const taskColumns string` / `func scanTaskRow(sc rowScanner) (proto.Task, error)`——Task 4 加列时只改这两处

- [ ] **Step 1: 写失败测试**

追加到 `internal/store/store_test.go` 末尾：

```go
// newTaskAt 造一条指定状态与工作目录的任务（直插，不走状态机——本测试要的就是
// 六个状态各来一条）。
func newTaskAt(t *testing.T, s *store.Store, id, workDir, repoPath string, st proto.TaskState) {
	t.Helper()
	now := time.Now().UTC()
	if err := s.CreateTask(&proto.Task{
		ID: id, RepoPath: repoPath, WorkDir: workDir, State: st,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask(%s): %v", id, err)
	}
}

// TestActiveTasksByWorkDirOnlyNonTerminal 钉住占用判定的语义：四个非终态算占用，
// completed/failed 不算。waiting_review 必须在内——审核期间 diff/fetch/run/continue
// 都依赖那棵树的 HEAD，被切走就全看错东西（spec §3.3）。
func TestActiveTasksByWorkDirOnlyNonTerminal(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const wd = "/work/repo"
	for i, st := range []proto.TaskState{
		proto.TaskStatePending, proto.TaskStateRunning, proto.TaskStateWaitingAnswer,
		proto.TaskStateWaitingReview, proto.TaskStateCompleted, proto.TaskStateFailed,
	} {
		newTaskAt(t, s, fmt.Sprintf("task-%d", i), wd, wd, st)
	}
	// 另一个目录上的活跃任务不该被捞进来
	newTaskAt(t, s, "task-other", "/work/other", "/work/other", proto.TaskStateRunning)

	got, err := s.ActiveTasksByWorkDir(wd)
	if err != nil {
		t.Fatalf("ActiveTasksByWorkDir: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("活跃任务数 = %d, want 4: %+v", len(got), got)
	}
	for _, task := range got {
		if task.State.IsTerminal() {
			t.Fatalf("终态任务不该算占用: %s(%s)", task.ID, task.State)
		}
		if task.WorkDir != wd {
			t.Fatalf("捞到了别的目录的任务: %s(%s)", task.ID, task.WorkDir)
		}
	}

	// 空 workDir 刻意不查：managed 模式每任务一棵新树，不需要这个判据
	empty, err := s.ActiveTasksByWorkDir("")
	if err != nil {
		t.Fatalf("ActiveTasksByWorkDir(\"\"): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("空 workDir 应返回空切片, got %d 条", len(empty))
	}
}

// TestActiveTasksByWorkDirLegacyEmptyWorkDir 是旧库兜底分支的守门人：早期原地
// 模式的 work_dir 存空串（由 proto.Task.Workdir() 回退到 repo_path），这类历史行
// 同样占着仓库，必须被查到。新派发的任务不会产生这种行，只能直插构造。
func TestActiveTasksByWorkDirLegacyEmptyWorkDir(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const repo = "/legacy/repo"
	newTaskAt(t, s, "legacy-1", "", repo, proto.TaskStateRunning)

	got, err := s.ActiveTasksByWorkDir(repo)
	if err != nil {
		t.Fatalf("ActiveTasksByWorkDir: %v", err)
	}
	if len(got) != 1 || got[0].ID != "legacy-1" {
		t.Fatalf("历史空 work_dir 行应被查到, got %+v", got)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/store/ -run TestActiveTasksByWorkDir -count=1
```

Expected: 编译失败 `s.ActiveTasksByWorkDir undefined` / `task.State.IsTerminal undefined`。

- [ ] **Step 3: 实现 proto 侧终态谓词**

`internal/proto/proto.go`，在 `TaskState` 常量块之后插入：

```go
// TerminalStates 是任务的两个终态：到此不再有 executor 持有工作区。
// 存储层按它生成「非终态」查询条件，避免与状态机定义漂移。
var TerminalStates = []TaskState{TaskStateCompleted, TaskStateFailed}

// IsTerminal 报告该状态是否为终态（completed / failed）。
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed
}
```

- [ ] **Step 4: 实现 store 侧查询（含读取列去重）**

`internal/store/store.go`。其一，在 `CreateTask` 之前插入共享的列清单与扫描器：

```go
// taskColumns 是 tasks 表的完整读取列清单：GetTask / ListTasks /
// ActiveTasksByWorkDir 共用同一份。为什么要共用：这份清单原先在两处各抄一遍，
// 每加一列就得同步四个位置（DDL/迁移/写/读×N），漏一处的表现是运行期
// Scan 列数不匹配——集中到一处后加列只改这里与 scanTaskRow。
const taskColumns = `id, target, repo_path, branch, plan_path, plan_summary, executor_session, state, created_at, updated_at,
  name, executor, model, work_dir, worktree_managed, base_commit, base_ahead`

// rowScanner 抽象 *sql.Row 与 *sql.Rows 的公共 Scan 能力，让单行与多行查询
// 共用同一个扫描函数。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTaskRow 按 taskColumns 的顺序把一行扫成 proto.Task（时间与 bool 就地还原）。
//
// 返回：扫描失败时原样返回错误（含 sql.ErrNoRows，由调用方翻译成 ErrNotFound）
func scanTaskRow(sc rowScanner) (proto.Task, error) {
	var (
		task            proto.Task
		createdAt       string
		updatedAt       string
		worktreeManaged int
	)
	if err := sc.Scan(&task.ID, &task.Target, &task.RepoPath, &task.Branch, &task.PlanPath,
		&task.PlanSummary, &task.ExecutorSession, &task.State, &createdAt, &updatedAt,
		&task.Name, &task.Executor, &task.Model, &task.WorkDir, &worktreeManaged,
		&task.BaseCommit, &task.BaseAhead); err != nil {
		return proto.Task{}, err
	}
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	task.WorktreeManaged = worktreeManaged != 0
	return task, nil
}
```

其二，`GetTask` 的函数体整体替换为：

```go
func (s *Store) GetTask(id string) (*proto.Task, error) {
	row := s.db.QueryRowContext(context.Background(),
		`SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
	task, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取任务 %s: %w", id, err)
	}
	return &task, nil
}
```

其三，`ListTasks` 的函数体整体替换为：

```go
func (s *Store) ListTasks() ([]proto.Task, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+taskColumns+` FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	defer rows.Close()
	var tasks []proto.Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取任务行: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务列表: %w", err)
	}
	return tasks, nil
}
```

其四，在 `ListTasks` 之后新增：

```go
func (s *Store) ActiveTasksByWorkDir(workDir string) ([]proto.Task, error) {
	if workDir == "" {
		return nil, nil
	}
	placeholders := make([]string, len(proto.TerminalStates))
	args := []any{workDir, workDir}
	for i, st := range proto.TerminalStates {
		placeholders[i] = "?"
		args = append(args, string(st))
	}
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT `+taskColumns+` FROM tasks
WHERE (work_dir = ? OR (work_dir = '' AND repo_path = ?))
  AND state NOT IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("查询工作目录 %s 的活跃任务: %w", workDir, err)
	}
	defer rows.Close()
	var tasks []proto.Task
	for rows.Next() {
		task, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("读取活跃任务行: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活跃任务: %w", err)
	}
	return tasks, nil
}
```

`strings` 已在 `store.go` 的 import 里（迁移逻辑用它判 duplicate column），不需要新增 import。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/store/ ./internal/proto/ -count=1
```

Expected: 全 PASS（含既有的 `TestTaskLifecycle` ——列清单重构不能改变回读行为）。

- [ ] **Step 6: 把 Stop 的裸终态比较换成谓词**

`internal/agentd/manager.go` 的 `Stop` 里：

```go
	if cur.State.IsTerminal() {
```

替换原来的 `if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {`。这是我们正在改的这段逻辑里的既有毛刺——占用判定和它必须共用同一个「什么叫终态」的定义，两处各写一遍迟早漂移。

```bash
go test ./internal/agentd/ -count=1
```

Expected: 全 PASS。

- [ ] **Step 7: 加关键节点日志**

`ActiveTasksByWorkDir` 只在命中时打一条（查询本身是热路径上的常规操作，命中才是需要解释的事实）：

```go
	if len(tasks) > 0 {
		log().Info("工作目录上存在活跃任务", "workdir", workDir, "count", len(tasks))
	}
```

放在 `rows.Err()` 检查之后、`return tasks, nil` 之前。查询失败的日志不在这里打——错误已带上下文返回，由调用方 `guardWorkdirBusy`（Task 3）打 Error，避免同一件事两处刷屏。

- [ ] **Step 8: 加注释**

其一，`ActiveTasksByWorkDir` 的文档注释：

```go
// ActiveTasksByWorkDir 返回工作目录为 workDir 的全部非终态任务。
//
// 参数：
//   - workDir: 工作目录绝对路径（原地模式即仓库路径）；空串返回空切片
//
// 返回：
//   - 非终态任务切片（可能为空），按创建时间倒序
//   - 查询失败返回错误（调用方按「查不出来就保守拒发」处置）
//
// 注意：
//   - 终态清单取自 proto.TerminalStates，避免与状态机定义漂移
//   - 空 workDir 直接返回空切片：不查是刻意的，managed 模式每任务一棵新树，
//     天然不冲突，不需要这个判据
//   - WHERE 里对空 work_dir 的兜底是给**旧库历史行**的：早期原地模式的
//     work_dir 存空串（proto.Task.Workdir() 的回退就是为它们写的），那些任务
//     同样占着仓库。新派发的任务 work_dir 一定是满的
```

其二，改掉 `store.go` 建表 DDL 里那条已过期的列注释——原文说「work_dir=工作区目录（空=原地模式…）」，而 `PrepareWorkspace` 的原地分支现在写的是 `WorkDir: req.Repo`：

```go
  -- work_dir=工作区目录（原地模式=仓库路径，worktree 模式=工作树路径；
  --   旧库里原地模式曾存空串，读取时由 proto.Task.Workdir() 回退到 repo_path）；
```

- [ ] **Step 9: 变异检验**

把 `ActiveTasksByWorkDir` 的 `NOT IN` 改成 `IN`，跑：

```bash
go test ./internal/store/ -run TestActiveTasksByWorkDir -count=1
```

Expected: `TestActiveTasksByWorkDirOnlyNonTerminal` 转红（捞回 2 条终态）。改回；再把 `WHERE` 里的 `OR (work_dir = '' AND repo_path = ?)` 删掉，确认 `TestActiveTasksByWorkDirLegacyEmptyWorkDir` 转红。改回，重跑确认全绿。

- [ ] **Step 10: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/store/ ./internal/proto/ ./internal/agentd/ -count=1
```

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go internal/agentd/manager.go && git commit -m "feat(b42): 终态谓词与按工作目录查活跃任务（store 读取列清单收敛为一处）"
```

---

## Task 3: B42 后半 —— 工作目录占用守卫接入派发

**Files:**
- Modify: `internal/agentd/workspace.go`（错误哨兵块，约 `:51-63`）
- Modify: `internal/agentd/manager.go`（`Dispatch` 前置块；新增 `guardWorkdirBusy`；修 `:532` 过期注释）
- Modify: `internal/agentd/server.go`（`writeDispatchError` 约 `:498-544`）
- Test: `internal/agentd/integration_test.go`

**Interfaces:**
- Consumes: Task 2 的 `store.ActiveTasksByWorkDir`、Task 1 的 `EnsureRepoUsable`（守卫排在它之后）
- Produces: `var ErrWorkdirBusy error`、`func (m *Manager) guardWorkdirBusy(workDir string) error`

- [ ] **Step 1: 写失败测试（四条集成）**

追加到 `internal/agentd/integration_test.go` 末尾：

```go
// TestDispatchWorkdirBusyWhileRunning409 覆盖 B42 的主场景：任务 A 原地占着仓库，
// 同仓库再派 B 必须被 409 拒绝且点名 A。旧行为是放行——A 一提交完
// git status 就干净了，脏检查这道「保护」恰好在最危险的时刻消失，B 的
// checkout -b 直接把共享 HEAD 切走，A 的下一次提交落到 B 的分支上。
func TestDispatchWorkdirBusyWhileRunning409(t *testing.T) {
	env := newIntegEnv(t, nil) // 空脚本：A 起来后停在 running
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	a := env.dispatchPlan(t, "第一个任务")

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md", Target: "local",
	})
	if err == nil {
		t.Fatal("同一仓库的第二个原地任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Fatalf("占用冲突应为 409, got: %v", err)
	}
	if !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("报文必须点名占用者 %s, got: %v", a.ID, err)
	}
	if !strings.Contains(err.Error(), "--new-worktree") {
		t.Fatalf("报文必须给出出路, got: %v", err)
	}

	// stop 让 A 落 failed（终态）→ 目录释放
	if _, err := env.cli.Stop(context.Background(), a.ID); err != nil {
		t.Fatalf("Stop(A): %v", err)
	}
	b := env.dispatchPlan(t, "第二个任务")
	if b.State != proto.TaskStateRunning {
		t.Fatalf("释放后 dispatch state=%s, want running", b.State)
	}
}

// TestDispatchWorkdirBusyWhileWaitingReview 钉住 spec §3.3 里最容易被质疑的一条：
// waiting_review 也算占用。审核期间要跑 diff/fetch/run/continue，HEAD 被切走这些
// 全会看错东西，continue 回去更是在别人的分支上干活。代价是必须先 done 掉。
func TestDispatchWorkdirBusyWhileWaitingReview(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{{Finish: executor.Result{OK: true, Summary: "干完了"}}})
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	a := env.dispatchPlan(t, "第一个任务")

	if ev := env.waitAction(t, a.ID); ev.Type != proto.EventTypeCompleted {
		t.Fatalf("首个事件 type=%s, want completed", ev.Type)
	}
	eventually(t, 2*time.Second, "A 进入 waiting_review", func() bool {
		cur, err := env.st.GetTask(a.ID)
		return err == nil && cur.State == proto.TaskStateWaitingReview
	})

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md", Target: "local",
	})
	if err == nil {
		t.Fatal("waiting_review 的任务仍占着工作树，第二个任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "waiting_review") {
		t.Fatalf("报文应为 409 并说明占用者状态, got: %v", err)
	}

	if err := env.cli.Done(context.Background(), a.ID); err != nil {
		t.Fatalf("Done(A): %v", err)
	}
	b := env.dispatchPlan(t, "第二个任务")
	if b.State != proto.TaskStateRunning {
		t.Fatalf("done 后 dispatch state=%s, want running", b.State)
	}
}

// TestDispatchTwoNewWorktreesNotBlocked 防误伤：managed 树每任务一棵，天然不冲突，
// 守卫不该挡住本来就安全的路径——挡住了等于把并行派发这个核心能力废掉。
func TestDispatchTwoNewWorktreesNotBlocked(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	for i := 0; i < 2; i++ {
		task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
			Repo: env.repo, PlanB64: plan, PlanName: "plan.md",
			Target: "local", NewWorktree: true,
		})
		if err != nil {
			t.Fatalf("第 %d 个 --new-worktree 派发失败: %v", i+1, err)
		}
		if task.State != proto.TaskStateRunning {
			t.Fatalf("第 %d 个任务 state=%s, want running", i+1, task.State)
		}
	}
}

// TestDispatchUserWorktreeBusy 覆盖第三种模式：两个任务指同一棵用户自带
// worktree，第二个被拒。判定键是 WorkDir，一条规则覆盖三种模式。
func TestDispatchUserWorktreeBusy(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, env.repo, "worktree", "add", "-b", "wt-branch", wt)

	first, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md", Target: "local", Worktree: wt,
	})
	if err != nil {
		t.Fatalf("首个用户树派发: %v", err)
	}
	_, err = env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md", Target: "local", Worktree: wt,
	})
	if err == nil {
		t.Fatal("同一棵用户 worktree 的第二个任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("应为 409 且点名占用者 %s, got: %v", first.ID, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestDispatchWorkdirBusy|TestDispatchUserWorktreeBusy|TestDispatchTwoNewWorktreesNotBlocked' -count=1
```

Expected: 三条 busy 用例 FAIL（第二次派发居然成功了），`TestDispatchTwoNewWorktreesNotBlocked` PASS（它断言的是现状不被破坏）。

- [ ] **Step 3: 新增错误哨兵**

`internal/agentd/workspace.go` 的 `var (...)` 错误块里，`ErrBadWorkspaceReq` 之后加：

```go
	ErrWorkdirBusy = errors.New("目标工作目录已被活跃任务占用")
```

- [ ] **Step 4: 实现守卫并接入 Dispatch**

`internal/agentd/manager.go`，在 `Dispatch` 之后（`compensateWorkspace` 之前）新增：

```go
func (m *Manager) guardWorkdirBusy(workDir string) error {
	if workDir == "" {
		return nil
	}
	busy, err := m.st.ActiveTasksByWorkDir(workDir)
	if err != nil {
		return fmt.Errorf("查询工作目录占用: %w", err)
	}
	if len(busy) == 0 {
		return nil
	}
	holder := busy[0]
	return fmt.Errorf("%w: %s 正被任务 %s（%s, %s）占用；先 handoff done/stop 它，或改用 --new-worktree 在独立工作树上开工",
		ErrWorkdirBusy, workDir, holder.ID, holder.Name, holder.State)
}
```

在 `Dispatch` 里，Task 1 插入的 `EnsureRepoUsable` 块**之后**、`// 基线决议` 之前插入：

```go
	// 派发前置 2（B42）：工作目录占用守卫。managed 树每任务一棵，天然不冲突，
	// 不必查；另两种模式的目标目录在派发前就已知，Dispatch 自己算得出来。
	// 排在 ResolveBaseline 之前：后者在基线缺失时会做一次 git fetch（网络代价），
	// 一个注定要被拒的派发不该先付这笔钱
	occupied := ""
	if !req.NewWorktree {
		occupied = req.Repo
		if req.Worktree != "" {
			occupied = req.Worktree
		}
	}
	if err := m.guardWorkdirBusy(occupied); err != nil {
		return nil, err
	}
```

- [ ] **Step 5: 加 409 映射**

`internal/agentd/server.go` 的 `writeDispatchError`，在 `case errors.Is(err, ErrDirtyWorktree):` **之后**插入：

```go
	case errors.Is(err, ErrWorkdirBusy):
		s.log.Warn("dispatch 被拒：目标工作目录被占用", "repo", repo, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
```

- [ ] **Step 6: 跑测试确认通过**

```bash
go test ./internal/agentd/ -count=1
```

Expected: 全 PASS。若既有用例因「同一仓库连派两个原地任务」而转红，那不是回归——是这些用例本来就在演示被修掉的危险行为，把它们改成 `--new-worktree` 或先 `Stop` 掉前一个任务，并在用例注释里写明改动原因。

- [ ] **Step 7: 加关键节点日志**

`guardWorkdirBusy` 三条分支各一条（这是拒发决策点，日志缺了就只剩一个 409 报文，排障时无从判断守卫到底跑没跑）：

```go
func (m *Manager) guardWorkdirBusy(workDir string) error {
	if workDir == "" {
		m.log.Info("工作目录占用检查跳过（managed 模式，每任务一棵新树）")
		return nil
	}
	busy, err := m.st.ActiveTasksByWorkDir(workDir)
	if err != nil {
		m.log.Error("查询工作目录占用失败，保守拒发", "workdir", workDir, "cause", err)
		return fmt.Errorf("查询工作目录占用: %w", err)
	}
	if len(busy) == 0 {
		m.log.Info("工作目录占用检查通过", "workdir", workDir)
		return nil
	}
	holder := busy[0]
	m.log.Warn("dispatch 被拒：目标工作目录已被活跃任务占用", "workdir", workDir,
		"holder", holder.ID, "holder_state", holder.State, "holders", len(busy))
	return fmt.Errorf("%w: %s 正被任务 %s（%s, %s）占用；先 handoff done/stop 它，或改用 --new-worktree 在独立工作树上开工",
		ErrWorkdirBusy, workDir, holder.ID, holder.Name, holder.State)
}
```

- [ ] **Step 8: 加注释**

其一，`guardWorkdirBusy` 的文档注释：

```go
// guardWorkdirBusy 拒绝把任务派到已被活跃任务占用的工作目录（B42）。
//
// 参数：
//   - workDir: 目标工作目录；空串=managed 模式（每任务一棵新树），直接放行
//
// 返回：
//   - nil：无人占用，或本次是 managed 模式
//   - ErrWorkdirBusy：已有非终态任务占着这个目录，错误文本点名占用者与两条出路
//     （server 层据此给 409，与「工作区不干净」同为状态冲突）
//   - 其他错误：查询任务表失败
//
// 注意：
//   - 查询失败按拒发处理：放行的代价是两个 executor 抢同一棵工作树、互相切走
//     HEAD 且全程无报错，比多拒一次派发严重得多
//   - 只报第一个占用者：报文是给人看的行动指引，列出全部只会让它更难读；
//     日志里带了 holders 总数
//   - git 自己已经挡住了分支级冲突（worktree add 遇到已被检出的分支会失败），
//     这道守卫补的是唯一的洞：被共享的主工作树
```

其二，`writeDispatchError` 上方的映射规则文档注释里，`ErrDirtyWorktree` 那条之后补一行：

```go
//   - ErrWorkdirBusy → 409：目标工作目录已被一个非终态任务占用（含 waiting_review），
//     与 ErrDirtyWorktree 同为状态冲突而非请求错误——报文点名占用任务并给出
//     两条出路（done/stop 它，或改用 --new-worktree）
```

其三，改掉 `manager.go` 里那条已过期的字段注释（原文「WorkDir 原地模式存空串」；`PrepareWorkspace` 的原地分支现在写的是 `WorkDir: req.Repo`）：

```go
		// 二期字段创建时即已知，随 CreateTask 写入（WorkDir 三种模式都是满的：
		// 原地=仓库路径、用户树/managed=工作树路径；proto.Task.Workdir() 的
		// 空串回退只服务旧库历史行）
```

- [ ] **Step 9: 变异检验**

把 `guardWorkdirBusy` 的 `if len(busy) == 0` 改成 `if true`，跑：

```bash
go test ./internal/agentd/ -run 'TestDispatchWorkdirBusy|TestDispatchUserWorktreeBusy' -count=1
```

Expected: 三条转红。改回；再把 `occupied` 的计算里 `if !req.NewWorktree` 改成 `if true`，确认 `TestDispatchTwoNewWorktreesNotBlocked` 转红（守卫误伤了 managed 路径）。改回，重跑确认全绿。

- [ ] **Step 10: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/agentd/ -count=1
```

```bash
git add internal/agentd/workspace.go internal/agentd/manager.go internal/agentd/server.go internal/agentd/integration_test.go && git commit -m "feat(b42): 派发前加工作目录占用守卫，冲突返回 409 并点名占用任务"
```

---

## Task 4: B43 前半 —— 新树模式下的主仓脏快照（采集与落库）

**Files:**
- Modify: `internal/proto/proto.go`（`Task` 结构体，`BaseAhead` 之后）
- Modify: `internal/store/store.go`（DDL 约 `:72-84`、迁移列表约 `:117`、`taskColumns`、`scanTaskRow`、`CreateTask`）
- Modify: `internal/agentd/workspace.go`（`Workspace` 结构体；新增 `maxDirtyFilesListed` 与 `repoDirtySnapshot`；managed 分支采集）
- Modify: `internal/agentd/manager.go`（`proto.Task` 构造）
- Test: `internal/store/store_test.go`、`internal/agentd/integration_test.go`

**Interfaces:**
- Consumes: Task 2 的 `taskColumns` / `scanTaskRow`
- Produces:
  - `proto.Task.RepoDirtyCount int` / `proto.Task.RepoDirtyFiles string`（JSON key `repo_dirty_count` / `repo_dirty_files`）——Task 5 的 CLI 读这两个字段
  - `Workspace.RepoDirtyCount int` / `Workspace.RepoDirtyFiles string`
  - `const maxDirtyFilesListed = 5`

- [ ] **Step 1: 写失败测试（store 回读 + 两条集成）**

追加到 `internal/store/store_test.go`：

```go
// TestTaskDirtySnapshotRoundTrip 钉住两个新列的读写：条数与文件串各存各的，
// 封顶截断发生在服务端，条数不能因为封顶而丢失。
func TestTaskDirtySnapshotRoundTrip(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now().UTC()
	if err := s.CreateTask(&proto.Task{
		ID: "dirty-1", RepoPath: "/repo", State: proto.TaskStatePending,
		CreatedAt: now, UpdatedAt: now,
		RepoDirtyCount: 9, RepoDirtyFiles: "a.go, b.go 等 9 处",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, err := s.GetTask("dirty-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.RepoDirtyCount != 9 || got.RepoDirtyFiles != "a.go, b.go 等 9 处" {
		t.Fatalf("脏快照回读不一致: count=%d files=%q", got.RepoDirtyCount, got.RepoDirtyFiles)
	}
}
```

追加到 `internal/agentd/integration_test.go`：

```go
// TestDispatchNewWorktreeCarriesDirtySnapshot 覆盖 B43：--new-worktree 免脏检查是
// 对的（新树天然干净），但主仓的未提交改动不在基线里、executor 在新树里看不到
// 它们——派发照常成功，但任务必须带上快照，否则这件事在任何输出里都不留痕迹。
// 造 9 个脏文件验证封顶：只列 5 个，条数仍是 9。
func TestDispatchNewWorktreeCarriesDirtySnapshot(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("dirty-%d.txt", i)
		if err := os.WriteFile(filepath.Join(env.repo, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("写脏文件 %s: %v", name, err)
		}
	}

	task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("主仓脏不该阻塞 --new-worktree 派发: %v", err)
	}
	if task.RepoDirtyCount != 9 {
		t.Fatalf("RepoDirtyCount = %d, want 9", task.RepoDirtyCount)
	}
	if strings.Count(task.RepoDirtyFiles, "dirty-") != 5 {
		t.Fatalf("文件串应封顶 5 个, got %q", task.RepoDirtyFiles)
	}
	if !strings.Contains(task.RepoDirtyFiles, "等 9 处") {
		t.Fatalf("截断后必须仍说得出总数, got %q", task.RepoDirtyFiles)
	}
}

// TestDispatchNewWorktreeCleanRepoNoSnapshot 主仓干净时两个字段为零值——
// 不能打一条「有 0 处未提交改动」的空提示。
func TestDispatchNewWorktreeCleanRepoNoSnapshot(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		Repo: env.repo, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if task.RepoDirtyCount != 0 || task.RepoDirtyFiles != "" {
		t.Fatalf("干净仓库不该有快照: count=%d files=%q", task.RepoDirtyCount, task.RepoDirtyFiles)
	}
}
```

`integration_test.go` 需要新增 import `"fmt"`（若尚未引入）。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/store/ ./internal/agentd/ -run 'TestTaskDirtySnapshotRoundTrip|TestDispatchNewWorktreeCarriesDirtySnapshot|TestDispatchNewWorktreeCleanRepoNoSnapshot' -count=1
```

Expected: 编译失败 `unknown field RepoDirtyCount in struct literal`。

- [ ] **Step 3: 加 proto 字段**

`internal/proto/proto.go` 的 `Task` 结构体，`BaseAhead` 之后：

```go
	// RepoDirtyCount 是派发当时任务仓库未提交改动的**总数**（含未跟踪文件）；
	// 0=干净，或本次不是 managed（--new-worktree）模式。这些改动不在新工作树
	// 里，executor 看不到它们。
	RepoDirtyCount int `json:"repo_dirty_count"`
	// RepoDirtyFiles 是上述改动的文件名展示串（逗号分隔，封顶 5 个，超出补
	// 「等 N 处」）；服务端截断后的展示用字段，与 PlanSummary 同形，不供程序消费
	//（要精确条数请读 RepoDirtyCount）。
	RepoDirtyFiles string `json:"repo_dirty_files"`
```

- [ ] **Step 4: 加 store 两列**

`internal/store/store.go` 四处：

其一，`CREATE TABLE tasks` 的末尾（`base_ahead INTEGER NOT NULL DEFAULT 0` 之后，注意补逗号）：

```go
  -- B43 两列：repo_dirty_count=派发当时任务仓库未提交改动总数（仅 managed 模式采集）；
  -- repo_dirty_files=其文件名展示串（封顶 5 个）。这些改动不在新工作树里。
  repo_dirty_count INTEGER NOT NULL DEFAULT 0, repo_dirty_files TEXT NOT NULL DEFAULT '')`,
```

其二，增量列迁移 map 里补两条：

```go
		"repo_dirty_count": "INTEGER NOT NULL DEFAULT 0",
		"repo_dirty_files": "TEXT NOT NULL DEFAULT ''",
```

其三，`taskColumns` 末尾追加 `, repo_dirty_count, repo_dirty_files`。

其四，`scanTaskRow` 的 `Scan` 参数末尾追加 `&task.RepoDirtyCount, &task.RepoDirtyFiles`；`CreateTask` 的 INSERT 列清单、`VALUES` 占位符与实参三处同步追加 `repo_dirty_count, repo_dirty_files` / `?, ?` / `t.RepoDirtyCount, t.RepoDirtyFiles`。

- [ ] **Step 5: 实现脏快照采集**

`internal/agentd/workspace.go`。其一，`Workspace` 结构体末尾追加：

```go
	// RepoDirtyCount / RepoDirtyFiles 是 managed 模式下派发当时**主仓库**的脏快照
	// （语义见 proto.Task 同名字段）；非 managed 模式恒为零值——那两条路径的脏
	// 工作区已被 ensureCleanWorktree 拒发，不存在「有改动却看不见」的情形。
	RepoDirtyCount int
	RepoDirtyFiles string
```

其二，在 `ensureCleanWorktree` 之后新增：

```go
const maxDirtyFilesListed = 5

func repoDirtySnapshot(ctx context.Context, repo string) (count int, files string) {
	out, _, err := gitRun(ctx, repo, "status", "--porcelain")
	if err != nil {
		return 0, ""
	}
	var names []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// porcelain v1 行格式为 XY<空格>路径；重命名是 "R  旧名 -> 新名"，取新名
		name := strings.TrimSpace(line)
		if len(line) > 3 {
			name = strings.TrimSpace(line[3:])
		}
		if i := strings.LastIndex(name, " -> "); i >= 0 {
			name = name[i+4:]
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return 0, ""
	}
	shown := names
	if len(names) > maxDirtyFilesListed {
		shown = names[:maxDirtyFilesListed]
	}
	files = strings.Join(shown, ", ")
	if len(names) > len(shown) {
		files += fmt.Sprintf(" 等 %d 处", len(names))
	}
	return len(names), files
}
```

其三，`PrepareWorkspace` 的 `case req.NewWorktree:` 分支开头（`workDir := ...` 之前）插入采集，并把结果带进 `ws`：

```go
		// B43：新树免脏检查是对的（新树天然干净），但主仓的未提交改动不在基线里，
		// executor 在新树里看不到它们。建树之前采一次快照，供 dispatch 回显
		dirtyCount, dirtyFiles := repoDirtySnapshot(ctx, req.Repo)
```

```go
		ws = Workspace{Branch: branch, WorkDir: workDir, Managed: true,
			RepoDirtyCount: dirtyCount, RepoDirtyFiles: dirtyFiles}
```

- [ ] **Step 6: 落到任务上**

`internal/agentd/manager.go` 的 `task = &proto.Task{...}` 字面量，`BaseAhead: ahead,` 之后：

```go
		// B43：新树不含主仓这些未提交改动，随任务落库供 CLI 回显（不阻断派发）
		RepoDirtyCount: ws.RepoDirtyCount,
		RepoDirtyFiles: ws.RepoDirtyFiles,
```

- [ ] **Step 7: 跑测试确认通过**

```bash
go test ./internal/store/ ./internal/agentd/ -count=1
```

Expected: 全 PASS。

- [ ] **Step 8: 加关键节点日志**

`repoDirtySnapshot` 的失败分支与命中分支各一条（采集失败不阻断派发，所以**必须**留日志，否则「提示为什么没出现」无从查起）：

```go
	if err != nil {
		log().Warn("采集任务仓库脏快照失败，提示留空（不阻断派发）", "repo", repo, "cause", err)
		return 0, ""
	}
```

在 `return len(names), files` 之前：

```go
	// 服务端日志带完整未截断列表：展示串封顶 5 个是给人读的，排障要看全的
	log().Warn("任务仓库有未提交改动，新工作树不含它们",
		"repo", repo, "count", len(names), "files", strings.Join(names, ", "))
```

干净时（`len(names) == 0`）不打日志：那是常态，没有需要解释的事实。

- [ ] **Step 9: 加注释**

`repoDirtySnapshot` 与 `maxDirtyFilesListed` 的注释：

```go
// maxDirtyFilesListed 是脏快照展示串里最多列出的文件数。
// 封顶而不是全列：这是给人读的一行提示，几十个文件名会把它撑成一屏；
// 真实条数由 RepoDirtyCount 单独承载，不会因为封顶而丢失。
const maxDirtyFilesListed = 5

// repoDirtySnapshot 采集仓库未提交改动的快照（条数 + 文件名展示串）。
//
// 参数：
//   - ctx: 控制本次 git 调用的生命周期
//   - repo: 任务仓库路径（managed 模式下即主仓库）
//
// 返回：
//   - count: 未提交改动总数（含未跟踪文件）；干净或采集失败时为 0
//   - files: 逗号分隔的文件名串，最多 maxDirtyFilesListed 个，超出补「等 N 处」
//
// 注意：
//   - 采集失败不返回错误：这是诊断信息，不该挡住主流程（与 currentRef 同款约定），
//     失败只打 Warn 并返回零值
//   - 不区分已跟踪/未跟踪：B29 分它们是因为处置不同（拒发 vs 警告），这里两者
//     对新工作树同样不可见、处置完全一样，分了只是噪音
```

- [ ] **Step 10: 变异检验**

把 `maxDirtyFilesListed` 改成 `50`，跑：

```bash
go test ./internal/agentd/ -run TestDispatchNewWorktreeCarriesDirtySnapshot -count=1
```

Expected: 转红（列了 9 个文件名、没有「等 9 处」）。改回；再把 `repoDirtySnapshot` 的返回改成恒 `0, ""`，确认同一条转红。改回，重跑确认全绿。

- [ ] **Step 11: 提交**

```bash
gofmt -l . && go vet ./... && go test ./internal/store/ ./internal/agentd/ -count=1
```

```bash
git add internal/proto/proto.go internal/store/store.go internal/store/store_test.go internal/agentd/workspace.go internal/agentd/manager.go internal/agentd/integration_test.go && git commit -m "feat(b43): managed 模式采集主仓脏快照并随任务落库"
```

---

## Task 5: B43 后半 —— dispatch 回显脏快照

**Files:**
- Modify: `cmd/dispatch.go`（基线摘要块之后，约 `:131-137`；顺带补文件头「职责」一条）
- Test: `cmd/dispatch_test.go`

**Interfaces:**
- Consumes: Task 4 的 `proto.Task.RepoDirtyCount` / `RepoDirtyFiles`
- Produces: 无（终端输出）

- [ ] **Step 1: 写失败测试（两条）**

追加到 `cmd/dispatch_test.go` 末尾：

```go
// TestDispatchPrintsDirtySnapshotToStderr 验证 B43 的回显：执行机仓库有未提交
// 改动时 stderr 说出来（远程派发时审核者根本看不到那台机器的工作区），且
// stdout 仍是单行任务 JSON——上层脚本按行解析，多一行就全乱。
func TestDispatchPrintsDirtySnapshotToStderr(t *testing.T) {
	old := dispatchTestTaskJSON
	dispatchTestTaskJSON = `{"id":"task-abc123","state":"running","repo_dirty_count":3,"repo_dirty_files":"a.go, b.go, c.go"}`
	t.Cleanup(func() { dispatchTestTaskJSON = old })

	out, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(errOut, "3 处未提交改动") || !strings.Contains(errOut, "a.go, b.go, c.go") {
		t.Fatalf("stderr 应含脏改动条数与文件名，得到 %q", errOut)
	}
	if strings.Contains(out, "未提交改动") {
		t.Fatalf("stdout 必须只有任务 JSON（脚本按行解析），得到 %q", out)
	}
}

// TestDispatchNoDirtySnapshotNoLine 验证干净时不打空洞的一行：
// 「有 0 处未提交改动」比不说更糟。
func TestDispatchNoDirtySnapshotNoLine(t *testing.T) {
	_, errOut, err := runDispatch(t, "--no-terminal")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errOut, "未提交改动") {
		t.Fatalf("干净时不应打提示行，得到 %q", errOut)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run 'TestDispatchPrintsDirtySnapshotToStderr|TestDispatchNoDirtySnapshotNoLine' -count=1
```

Expected: 第一条 FAIL（stderr 里没有那行），第二条 PASS。

- [ ] **Step 3: 实现**

`cmd/dispatch.go`，在基线摘要那个 `if task.BaseCommit != "" { ... }` 块之后插入：

```go
			if task.RepoDirtyCount > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"提示: 执行机仓库有 %d 处未提交改动，新工作树不含它们：%s\n",
					task.RepoDirtyCount, task.RepoDirtyFiles)
			}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd/ -count=1
```

Expected: 全 PASS（既有的基线渲染三条不受影响——新提示行是独立条件）。

- [ ] **Step 5: 加关键节点日志**

本步骤**不加日志**，并在此记录理由：`cmd` 包是 CLI 前台进程，它的「日志」就是给人看的 stderr 输出——这一行本身即是可观测性产物，再叠一层结构化日志只会重复。派发链路上真正需要被 `tail_logs` 捞到的那条 WARN（含完整未截断文件列表）在服务端，已由 Task 4 Step 8 加好。

- [ ] **Step 6: 加注释**

其一，提示行上方的「为什么」：

```go
			// B43：新工作树不含执行机仓库里未提交的改动，而审核者看不到那台机器的
			// 工作区——不说，executor 就会在一份没有那些改动的代码上开工而无人知晓。
			// 与基线行同走 stderr（stdout 的单行任务 JSON 契约不能破，见上方注释）
```

其二，`cmd/dispatch.go` 文件头「职责」列表里，基线摘要那条之后补一条：

```go
//   - 派发成功后在 stderr 提示执行机仓库的未提交改动（managed 工作树不含它们）
```

- [ ] **Step 7: 变异检验**

把 `> 0` 改成 `> 100`，跑：

```bash
go test ./cmd/ -run TestDispatchPrintsDirtySnapshot -count=1
```

Expected: 转红。改回，重跑确认全绿。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go test ./cmd/ -count=1
```

```bash
git add cmd/dispatch.go cmd/dispatch_test.go && git commit -m "feat(b43): dispatch 在 stderr 回显执行机仓库的未提交改动"
```

---

## Task 6: 全量闸门与 devbox 真机验收

**Files:**
- Modify: 无（除非闸门暴露问题）
- Verify: spec §6 的全部条目

**Interfaces:**
- Consumes: Task 1–5 的全部产出
- Produces: 验收证据（贴进最终汇报，供 backlog 的「验收」列引用）

- [ ] **Step 1: 跑全量闸门**

```bash
gofmt -l . && go build ./... && go vet ./... && go test ./... -count=1
```

Expected: `gofmt -l .` 无输出，其余全绿。

- [ ] **Step 2: 跑竞态与跨平台构建**

```bash
go test -race ./cmd/ ./internal/agentd/ ./internal/store/ -count=1
```

```bash
GOOS=windows GOARCH=amd64 go build ./...
```

Expected: 均无输出/全绿。

- [ ] **Step 3: 推分支，在 devbox 上构建验收用二进制**

真机验收在 devbox（`sycm@100.73.238.21`）上做。**红线：不覆盖那台机器上正在跑的 agentd**——起第二个实例，独立端口 + 独立 DataDir + 独立仓库（DataDir 文件锁会挡住共用同一份数据目录的第二个实例，这三样必须同时独立）。

```bash
git push -u origin handoff/b42-dispatch-entry
```

```bash
ssh sycm@100.73.238.21 'set -e; rm -rf ~/handoff-b42-src; git clone -b handoff/b42-dispatch-entry https://github.com/xushixin/handoff.git ~/handoff-b42-src; cd ~/handoff-b42-src; go build -o ~/handoff-b42-src/handoff-b42 .'
```

若 clone 地址不可用（私有仓库/无凭据），改用 `rsync -a --exclude .git ./ sycm@100.73.238.21:~/handoff-b42-src/` 把工作副本推上去再构建。

- [ ] **Step 4: 起第二个 agentd（新端口 + 新 DataDir + 新仓库）**

```bash
ssh sycm@100.73.238.21 'set -e; mkdir -p ~/.handoff-b42; printf "listen: \"127.0.0.1:7787\"\ntoken: \"b42-verify\"\ndatadir: \"%s/.handoff-b42\"\nexecutor:\n  default: fake\n" "$HOME" > ~/.handoff-b42/config.yaml; rm -rf ~/b42-repo; git init -q ~/b42-repo; cd ~/b42-repo; git config user.email v@handoff.dev; git config user.name verify; echo hi > README.md; git add .; git commit -qm init'
```

```bash
ssh sycm@100.73.238.21 'cd ~; nohup ~/handoff-b42-src/handoff-b42 agentd --config ~/.handoff-b42/config.yaml > ~/.handoff-b42/agentd.log 2>&1 & sleep 2; ~/handoff-b42-src/handoff-b42 status --config ~/.handoff-b42/config.yaml'
```

Expected: `status` 打出一屏正常输出（版本/数据/任务）。若报 `connection refused`，看 `~/.handoff-b42/agentd.log` 的最后几行——**不要**去 ssh 查进程查端口（那是在验证零件）。

- [ ] **Step 5: 验收 1 —— 占用守卫（B42）**

```bash
ssh sycm@100.73.238.21 'cd ~/b42-repo; H="$HOME/handoff-b42-src/handoff-b42 --config $HOME/.handoff-b42/config.yaml"; A=$($H dispatch --repo ~/b42-repo --executor fake --prompt "占位任务" --no-terminal | head -1 | python3 -c "import sys,json;print(json.load(sys.stdin)[\"id\"])"); echo "A=$A"; $H dispatch --repo ~/b42-repo --executor fake --prompt "第二个" --no-terminal; echo "退出码=$?"'
```

Expected: 第二条 dispatch 失败，报文含 `409`、任务 A 的完整 id、`--new-worktree`。把 `A=` 那一行的 id 记下来给下一步。

```bash
ssh sycm@100.73.238.21 'H="$HOME/handoff-b42-src/handoff-b42 --config $HOME/.handoff-b42/config.yaml"; A=<上一步的 A>; $H stop $A; cd ~/b42-repo; $H dispatch --repo ~/b42-repo --executor fake --prompt "释放后重派" --no-terminal | head -1'
```

Expected: 释放后派发成功，输出一行任务 JSON（`"state":"running"`）。

- [ ] **Step 6: 验收 2 —— 仓库不可用（B45）**

```bash
ssh sycm@100.73.238.21 'H="$HOME/handoff-b42-src/handoff-b42 --config $HOME/.handoff-b42/config.yaml"; mkdir -p ~/b42-notrepo; cd ~/b42-repo; $H dispatch --repo ~/b42-notrepo --executor fake --new-worktree --prompt "非 git 路径" --no-terminal; echo "退出码=$?"'
```

Expected: 报文含 `400` 与「不可用」+ git 原文，**不含**「派发任务失败」的扁平 500，也**不含**「请先在本地 git push」的误诊。

- [ ] **Step 7: 验收 3 —— 脏快照提示（B43）**

```bash
ssh sycm@100.73.238.21 'H="$HOME/handoff-b42-src/handoff-b42 --config $HOME/.handoff-b42/config.yaml"; cd ~/b42-repo; echo x > d1.txt; echo y > d2.txt; $H dispatch --repo ~/b42-repo --executor fake --new-worktree --prompt "带脏快照" --no-terminal 2>&1 >/dev/null'
```

Expected: stderr 出现 `提示: 执行机仓库有 2 处未提交改动，新工作树不含它们：d1.txt, d2.txt`。

- [ ] **Step 8: 收摊**

```bash
ssh sycm@100.73.238.21 'H="$HOME/handoff-b42-src/handoff-b42 --config $HOME/.handoff-b42/config.yaml"; for id in $($H tasks | python3 -c "import sys,json;[print(json.loads(l)[\"id\"]) for l in sys.stdin if l.strip()]"); do $H stop $id 2>/dev/null; done; pkill -f "handoff-b42 agentd" || true'
```

保留 `~/.handoff-b42/agentd.log` 作为验收证据，不要删——原 agentd 与其数据目录（`~/.handoff`）全程未被触碰，这一点在汇报里明说。

- [ ] **Step 9: 汇报**

把三条真机结果的原文（报文/提示行）与 Step 1–2 的闸门输出一并汇报，并说明 backlog 的 B42/B43/B45 三行可以从 `📋 specced` 推到 `✅ done(已验)`、`验收` 列写什么。**backlog 的实际改动不在本计划内**——它由 `product-backlog` 在收尾时单独更新，与代码改动分开提交。

---

## Self-Review

**1. Spec 覆盖检查**

| Spec 条目 | 落在哪 |
|---|---|
| §3.1 前置顺序 `EnsureRepoUsable → guardWorkdirBusy → ResolveBaseline → PrepareWorkspace` | Task 1 Step 7 + Task 3 Step 4（两处插入点前后相邻，顺序即代码顺序） |
| §3.2 `EnsureRepoUsable` + 保留 `ensureCleanWorktree` 的包装 + 改 `headCommit` 注释 | Task 1 Step 3/10 |
| §3.3 `TerminalStates` / `IsTerminal` / `manager.go:923` 换谓词 | Task 2 Step 3/6 |
| §3.3 `ActiveTasksByWorkDir` + 空串兜底 + 两条过期注释 | Task 2 Step 4/8、Task 3 Step 8 其三 |
| §3.3 `ErrWorkdirBusy` + `occupied` 计算 + 拒发报文 + 409 映射与文档注释 | Task 3 Step 3/4/5/8 |
| §3.4 两个 proto 字段 + DDL + 迁移 + 读写 + 采集点 + 不阻断 + 服务端完整 WARN | Task 4 全部 |
| §3.4 CLI stderr 渲染 | Task 5 |
| §4 错误分类总表 | Task 1（400）+ Task 3（409）+ Task 4（200 带快照）共同实现；`ErrDirtyWorktree` / `ErrBaseCommitMissing` 两行是既有行为，未改动 |
| §5.1 五条占用测试 | Task 2 Step 1（两条 store）+ Task 3 Step 1（四条集成，含防误伤与用户树） |
| §5.2 三条 B45 测试 + `integration_test.go:528` 的冲突排查 | Task 1 Step 1/5/8 |
| §5.3 三条 B43 测试 | Task 4 Step 1（两条）+ Task 5 Step 1（两条 CLI） |
| §5.4 变异检验 | 每个实现 Task 的倒数第二步 |
| §6 验收（闸门 + 三条真机） | Task 6 |
| §7 明确不做 | 计划里没有任何一步碰它们 |

**2. 占位符扫描**：无 TBD/TODO；每个代码步骤都给了可直接落盘的代码；每个测试步骤都给了完整测试函数体；Task 6 Step 5 的 `A=<上一步的 A>` 是运行期才知道的任务 id（前一条命令已 echo 出来），不是待填的设计决策。

**3. 类型一致性**：`EnsureRepoUsable(ctx, repo) error`、`guardWorkdirBusy(workDir) error`、`ActiveTasksByWorkDir(workDir) ([]proto.Task, error)`、`repoDirtySnapshot(ctx, repo) (int, string)`、`Workspace.RepoDirtyCount/RepoDirtyFiles`、`proto.Task.RepoDirtyCount/RepoDirtyFiles`、`TerminalStates` / `IsTerminal()` —— Task 2 定义的 `taskColumns` / `scanTaskRow` 在 Task 4 Step 4 被同名引用；Task 4 定义的两个 proto 字段在 Task 5 Step 3 被同名引用。核对无漂移。
