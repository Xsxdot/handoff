# B84 agentd 错误面归因整治 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 agentd 日志里的每条 `level=ERROR` 都对应一次真故障，且每条故障报文指向真因。

**Architecture:** 两个独立组件。① `gitExec` 抽出为 `gitRun` / `gitProbe` 共用执行体，`gitProbe` 用 `probeOutcome` 三态区分「命中 / 预期内未命中 / 探测未能完成」，11 处探测调用点迁移过去；② 新增 `ErrWorkspaceGone` 与两道工作区存在性检查（`taskRepoOrErr` 入口门禁 + `RunCmd` 纵深预检），修掉「工作树已回收却报 `/bin/sh` 不存在」的假归因。两个组件互不依赖，可分别验收。

**Tech Stack:** Go 1.26.1、标准库 `log/slog`、`os/exec`、`net/http/httptest`；无新增第三方依赖。

**Spec:** [docs/superpowers/specs/2026-08-13-agentd-error-attribution-design.md](../specs/2026-08-13-agentd-error-attribution-design.md)

## Global Constraints

- 语言与注释：全部中文注释。新文件必须有文件头注释（职责 + 边界）；导出函数必须有 doc 注释（参数、返回、注意）。
- 日志：一律经 `log()`（即 `slog.Default()`）或 `s.log`，**禁止** `fmt.Printf` / `println`。
- 测试包：本计划所有测试都在 `package agentd`（内部白盒），因为 `gitRun` / `gitProbe` / `taskRepoOrErr` 均未导出。可直接用既有助手：`initGitRepo(t)`、`gitAt(t, dir, args...)`、`gitOut(t, dir, args...)`、`writeAndCommit(t, repo, name, content)`、`newTestManager(t)`、`newWorktree(t, repo, name, branch)`、`seedTerminalTask(t, m, repo, wt, branch, state, managed)`。
- **断言日志的测试禁止 `t.Parallel()`**：`log()` 取的是 `slog.Default()`，捕获需临时替换全局 default，并发会互踩。
- `package agentd` 内部测试**没有** `TestMain`（[hub_test.go](../../../internal/agentd/hub_test.go) 里那个属 `agentd_test` 外部包），所以默认 logger 是 Go 的 stderr handler、Debug 被吞。捕获 helper 必须自带 `Enabled` 恒 true 的 handler，否则 Debug 断言恒失败。
- 每个任务结束前跑 `go test ./internal/agentd/`，全绿才提交。
- 不改 CLI、proto、store。

---

### Task 1: 抽出共用执行体 `gitExec` 与测试注入点

纯重构，行为零变化。先落这一步是因为 Task 2 的 `gitProbe` 和 Task 4 的 fatal 端到端测试都依赖它。

**Files:**
- Modify: `internal/agentd/workspace.go:103-127`（`gitRun` 现址）
- Test: `internal/agentd/workspace_probe_test.go`（新建）

**Interfaces:**
- Produces: `gitExec(ctx context.Context, repo string, args []string) (stdout, stderr string, err error)`
- Produces: `var gitExecFn = gitExec`（测试注入点）
- Consumes: 无

- [ ] **Step 1: 写失败测试**

新建 `internal/agentd/workspace_probe_test.go`：

```go
// workspace_probe_test.go —— gitExec / gitProbe 的执行体与结局分类测试。
//
// 职责：
//   - 验证 gitRun 与 gitProbe 共用同一执行体，且注入点可被测试替换
//   - 验证探测结局三态分类，以及各结局对应的日志级别
//
// 边界：
//   - 不测具体调用点的迁移（那在各自任务的用例里）
//   - 不测 RunCmd / taskRepoOrErr（工作区门禁是另一组件）
package agentd

import (
	"context"
	"errors"
	"testing"
)

// stubGitExec 用假执行体替换 gitExecFn，测试结束自动还原。
func stubGitExec(t *testing.T, fn func(ctx context.Context, repo string, args []string) (string, string, error)) {
	t.Helper()
	prev := gitExecFn
	gitExecFn = fn
	t.Cleanup(func() { gitExecFn = prev })
}

func TestGitRunGoesThroughInjectableExec(t *testing.T) {
	var gotRepo string
	var gotArgs []string
	stubGitExec(t, func(_ context.Context, repo string, args []string) (string, string, error) {
		gotRepo, gotArgs = repo, args
		return "OUT", "ERR", errors.New("boom")
	})
	out, errOut, err := gitRun(context.Background(), "/repo/x", "status", "--porcelain")
	if gotRepo != "/repo/x" {
		t.Fatalf("repo 未透传，实得 %q", gotRepo)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "status" || gotArgs[1] != "--porcelain" {
		t.Fatalf("args 未透传，实得 %v", gotArgs)
	}
	if out != "OUT" || errOut != "ERR" {
		t.Fatalf("输出未透传，实得 out=%q err=%q", out, errOut)
	}
	if err == nil {
		t.Fatal("错误未透传")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestGitRunGoesThroughInjectableExec -v`
Expected: 编译失败，`undefined: gitExecFn`

- [ ] **Step 3: 实现抽取**

把 `internal/agentd/workspace.go` 现有的 `gitRun`（103-127 行）整体替换为：

```go
// gitExec 是 gitRun 与 gitProbe 共用的执行体：构造命令、双缓冲收集输出、
// 打进入与完成日志，把原始错误原样交回。
//
// 参数：
//   - ctx: 控制本次调用生命周期
//   - repo: git -C 的目标仓库
//   - args: git 子命令及参数
//
// 返回：stdout、stderr 原文，以及 cmd.Run 的原始错误（nil 表示退出码 0）
//
// 注意：本函数**不判定失败的性质、不打失败日志**。同一个非零退出对
// gitRun 是故障、对 gitProbe 是预期内的未命中，归类只能由调用方做。
func gitExec(ctx context.Context, repo string, args []string) (stdout, stderr string, err error) {
	log().Info("git 调用", "repo", repo, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	log().Info("git 调用完成", "repo", repo, "args", args,
		"elapsed_ms", time.Since(start).Milliseconds())
	return outBuf.String(), errBuf.String(), err
}

// gitExecFn 是 gitExec 的可替换入口。
//
// 包级 var 而非直接调用：判据 3 需要构造「fork 失败（EAGAIN）」这种无法用真
// git 稳定复现的场景。与本包既有约定同款（killProcGroup / admissionFn）。
var gitExecFn = gitExec

// gitRun 执行 git -C repo <args...>，返回 stdout 与 stderr。
//
// 语义：本函数的失败是**真故障**——非零退出会打 Error。若某次调用的非零退出
// 是预期内的（拿退出码当判据、且有兜底路径），用 gitProbe，不要用本函数。
//
// 日志：进入/完成由 gitExec 打 Info；失败在此打 Error 带 stderr 原文——
// git 报错原文是排障必需品，不能只留包装后的 error 文本。
func gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	stdout, stderr, err = gitExecFn(ctx, repo, args)
	if err != nil {
		if note := quotaNote(err); note != "" {
			log().Error("git 调用失败（进程配额）", "repo", repo, "args", args,
				"note", note, "cause", err)
			return stdout, stderr, fmt.Errorf("%s: %w", note, err)
		}
		log().Error("git 调用失败", "repo", repo, "args", args,
			"stderr", truncateRunes(stderr, 500), "cause", err)
	}
	return stdout, stderr, err
}
```

- [ ] **Step 4: 跑测试确认通过 + 全量回归**

Run: `go test ./internal/agentd/ -run TestGitRunGoesThroughInjectableExec -v`
Expected: PASS

Run: `go test ./internal/agentd/`
Expected: ok（本任务行为零变化，既有用例必须全绿；有任何红灯说明抽取改变了行为，回到 Step 3）

- [ ] **Step 5: 确认日志覆盖**

本任务不新增行为，日志要求是**保持不变且各归其位**，逐项对照：

- `gitExec` 内保留「git 调用」「git 调用完成」两条 Info（进入 + 退出带耗时）。
- 失败日志留在 `gitRun`，**不要**下沉进 `gitExec`——下沉会让 `gitProbe` 无法避开 Error，整个任务白做。
- 配额分支的 Error 与其 `note` 字段原样保留。

- [ ] **Step 6: 确认注释覆盖**

- 新测试文件有文件头注释（职责 + 边界）。
- `gitExec` doc 注释写明「不判定失败性质、不打失败日志」这条边界。
- `gitExecFn` 注释写明为什么是 var（可测性），并点名与 `killProcGroup` 同款约定。
- `gitRun` doc 注释新增「本函数的失败是真故障，预期内未命中请用 gitProbe」这句选择指引。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_probe_test.go
git commit -m "refactor(agentd): 抽出 gitExec 共用执行体与测试注入点

为 gitProbe 让路：失败的归类只能由调用方做，执行体不表态。
行为零变化。"
```

---

### Task 2: `probeOutcome` 三态与 `gitProbe`

**Files:**
- Modify: `internal/agentd/workspace.go`（紧接 Task 1 的 `gitRun` 之后）
- Test: `internal/agentd/workspace_probe_test.go`（续写）

**Interfaces:**
- Consumes: `gitExecFn`、`quotaNote`、`truncateRunes`（均已存在）
- Produces:
  - `type probeOutcome int`，常量 `probeHit` / `probeMiss` / `probeFatal`，方法 `String() string`
  - `classifyProbe(err error) (probeOutcome, string)`
  - `gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, res probeOutcome, note string)`

- [ ] **Step 1: 写失败测试**

在 `internal/agentd/workspace_probe_test.go` 追加。import 只补这一步真正用到的四个：`"fmt"`、`"log/slog"`、`"sync"`、`"syscall"`（Go 对未使用的 import 是硬报错，`strings` 要到 Task 4 才用得上，别提前加）：

```go
// levelCounter 是按级别计数的 slog handler，供「ERROR 只对应真故障」类断言。
//
// Enabled 恒 true 是刻意的：package agentd 内部测试没有 TestMain 调整过默认
// logger，Debug 默认会被吞掉，不放行就无法断言「降级后仍留了一条 Debug」。
type levelCounter struct {
	mu     sync.Mutex
	counts map[slog.Level]int
	errMsg []string
}

func (h *levelCounter) Enabled(context.Context, slog.Level) bool { return true }

func (h *levelCounter) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.counts[r.Level]++
	if r.Level >= slog.LevelError {
		h.errMsg = append(h.errMsg, r.Message)
	}
	return nil
}

func (h *levelCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelCounter) WithGroup(string) slog.Handler      { return h }

func (h *levelCounter) count(l slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[l]
}

func (h *levelCounter) errors() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.errMsg...)
}

// captureLogs 把默认 logger 换成计数 handler，测试结束还原。
// 用了它的测试禁止 t.Parallel()——替换的是进程级全局。
func captureLogs(t *testing.T) *levelCounter {
	t.Helper()
	h := &levelCounter{counts: map[slog.Level]int{}}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func TestClassifyProbeThreeOutcomes(t *testing.T) {
	if got, _ := classifyProbe(nil); got != probeHit {
		t.Fatalf("退出 0 应为 hit，实得 %v", got)
	}
	if got, _ := classifyProbe(errors.New("exit status 1")); got != probeMiss {
		t.Fatalf("普通非零退出应为 miss，实得 %v", got)
	}
	// EAGAIN 是 prochost.ExplainForkFailure 的唯一触发条件，且其 EAGAIN 路径上
	// 四个分支都返回非空 note，因此不依赖本机实际进程数，结果稳定。
	got, note := classifyProbe(fmt.Errorf("fork/exec git: %w", syscall.EAGAIN))
	if got != probeFatal {
		t.Fatalf("fork 失败应为 fatal，实得 %v", got)
	}
	if note == "" {
		t.Fatal("fatal 必须带归因文案，供调用方拼进给人看的报文")
	}
}

func TestGitProbeMissLogsDebugNotError(t *testing.T) {
	cap := captureLogs(t)
	repo := initGitRepo(t)
	_, _, res, _ := gitProbe(context.Background(), repo,
		"rev-parse", "--verify", "--quiet", "refs/heads/no-such-branch")
	if res != probeMiss {
		t.Fatalf("不存在的分支应判 miss，实得 %v", res)
	}
	if n := cap.count(slog.LevelError); n != 0 {
		t.Fatalf("预期内的未命中不该打 ERROR，实得 %d 条：%v", n, cap.errors())
	}
	if n := cap.count(slog.LevelDebug); n == 0 {
		t.Fatal("未命中仍应留一条 Debug——降级不等于静音，排障时要看得见")
	}
}

func TestGitProbeFatalStillLogsError(t *testing.T) {
	cap := captureLogs(t)
	stubGitExec(t, func(context.Context, string, []string) (string, string, error) {
		return "", "", fmt.Errorf("fork/exec git: %w", syscall.EAGAIN)
	})
	_, _, res, note := gitProbe(context.Background(), "/repo/x", "rev-parse", "HEAD")
	if res != probeFatal {
		t.Fatalf("fork 失败应判 fatal，实得 %v", res)
	}
	if note == "" {
		t.Fatal("fatal 应带归因文案")
	}
	if n := cap.count(slog.LevelError); n != 1 {
		t.Fatalf("fork 不出进程是真故障，必须打 ERROR，实得 %d 条", n)
	}
}

func TestGitRunRealFailureStillLogsError(t *testing.T) {
	cap := captureLogs(t)
	repo := initGitRepo(t)
	if _, _, err := gitRun(context.Background(), repo, "checkout", "no-such-ref"); err == nil {
		t.Fatal("checkout 不存在的 ref 应失败")
	}
	// 判据 2 的对照：降级不能把真失败一起静音，否则日志干净了但什么都没记
	if n := cap.count(slog.LevelError); n != 1 {
		t.Fatalf("真失败必须留恰好 1 条 ERROR，实得 %d 条：%v", n, cap.errors())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestClassifyProbe|TestGitProbe' -v`
Expected: 编译失败，`undefined: classifyProbe` / `undefined: gitProbe`

- [ ] **Step 3: 实现**

在 `internal/agentd/workspace.go` 中 `gitRun` 之后插入：

```go
// probeOutcome 是一次探测性 git 调用的三种结局。
//
// 为什么是三态而不是 bool：fork 失败（EAGAIN）与「探测未命中」都表现为非零
// 退出，但结论完全不同——前者是「没能问出答案」，后者是「问到了，答案是没有」。
// 合并成一个 false，会让 resolveCommit 在机器 fork 不出进程时报出「起点不存在」，
// 而起点明明在——那正是本次要消灭的假报文换了个触发条件（见 spec §3.1）。
type probeOutcome int

const (
	probeHit   probeOutcome = iota // 命中：退出码 0
	probeMiss                      // 未命中：非零退出，预期内，调用方按兜底路径继续
	probeFatal                     // 探测未能完成：资源性故障，结论未知，不可当作「没有」
)

// String 让日志与断言里的结局可读。
func (p probeOutcome) String() string {
	switch p {
	case probeHit:
		return "hit"
	case probeMiss:
		return "miss"
	case probeFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// classifyProbe 把一次 git 调用的原始错误翻译成探测结局。
//
// 参数：err 为 gitExec 返回的原始错误，nil 表示退出码 0
//
// 返回：
//   - 结局
//   - 归因文案；仅 probeFatal 时非空，供调用方拼进给人看的报文
func classifyProbe(err error) (probeOutcome, string) {
	if err == nil {
		return probeHit, ""
	}
	// 进程配额/fork 失败经同一个返回值到达，但它不是「未命中」——
	// 机器没资源了，任何基于「没查到」的结论都是编的
	if note := quotaNote(err); note != "" {
		return probeFatal, note
	}
	return probeMiss, ""
}

// gitProbe 执行一次探测性 git 调用：非零退出是预期内的未命中，不是故障。
//
// 与 gitRun 的分工：调用方拿退出码当判据、且未命中时有兜底路径的，用本函数；
// 非零退出确实意味着出事的，用 gitRun。
//
// 参数：
//   - ctx: 控制本次调用生命周期
//   - repo: git -C 的目标仓库
//   - args: git 子命令及参数
//
// 返回：
//   - stdout / stderr 原文
//   - res: 三态结局，调用方判据应写成 res == probeHit
//   - note: 仅 res == probeFatal 时非空的归因文案
//
// 注意：probeFatal 必须与 probeMiss 分开处置。把 fatal 当 miss 会让调用方
// 用「没查到」的口径给出报文，而真因是机器 fork 不出进程。
func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, res probeOutcome, note string) {
	stdout, stderr, err := gitExecFn(ctx, repo, args)
	res, note = classifyProbe(err)
	switch res {
	case probeFatal:
		log().Error("git 探测未能完成（进程配额）", "repo", repo, "args", args,
			"note", note, "cause", err)
	case probeMiss:
		// 降级到 Debug 而不是静音：排障时仍要能看见探测走过哪条路
		log().Debug("git 探测未命中（预期内）", "repo", repo, "args", args,
			"stderr", truncateRunes(stderr, 200), "cause", err)
	}
	return stdout, stderr, res, note
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestClassifyProbe|TestGitProbe|TestGitRunRealFailure' -v`
Expected: 全部 PASS

Run: `go test ./internal/agentd/`
Expected: ok

- [ ] **Step 5: 确认日志覆盖**

- `probeFatal` 打 Error，字段含 `repo` / `args` / `note` / `cause`。
- `probeMiss` 打 Debug，字段含 `repo` / `args` / `stderr` / `cause`——**必须有这条**，降级不是静音。
- `probeHit` 不额外打日志（`gitExec` 已有进入/完成两条 Info，成功路径不静默这条已满足）。

- [ ] **Step 6: 确认注释覆盖**

- `probeOutcome` 类型注释解释「为什么三态而不是 bool」，含 `resolveCommit` 那个具体反例。
- 三个常量各有行尾注释说明含义。
- `classifyProbe`、`gitProbe` 有完整 doc 注释（参数 / 返回 / 注意）。
- `gitProbe` 的 `注意` 段写明 fatal 不可当 miss 处置。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_probe_test.go
git commit -m "feat(agentd): 加 gitProbe 与探测结局三态

预期内的未命中降到 Debug，fork 失败仍打 Error 并单独成态——
把 fatal 当 miss 会让调用方用「没查到」的口径报出假真因。"
```

---

### Task 3: 迁移 7 处「仅判命中」的探测调用点

这 7 处的下游结论对 fatal 与 miss 恰好相同且不误导，因此统一按 `res != probeHit` 处置，`note` 丢弃。

**Files:**
- Modify: `internal/agentd/workspace.go`（`EnsureRepoUsable` 444、`currentRef` 591、`branchTip` 617、`hasCommit` 843、`resolveBaseBranch` 910/914）
- Modify: `internal/agentd/reclaim.go:191`（`classifyWorktree`）
- Test: `internal/agentd/workspace_probe_test.go`（续写）

**Interfaces:**
- Consumes: Task 2 的 `gitProbe`、`probeHit`；Task 2 测试的 `captureLogs`
- Produces: 无新签名（6 个函数签名全部不变）

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/workspace_probe_test.go`：

```go
func TestResolveBaseBranchNoOriginHeadLogsNoError(t *testing.T) {
	cap := captureLogs(t)
	repo := initGitRepo(t) // 只有本地 main，没有 origin/HEAD
	if got := resolveBaseBranch(repo); got != "main" {
		t.Fatalf("应回落到本地 main，实得 %q", got)
	}
	// 这是全仓最密的噪音源：改造前每次不带 --base 的 diff 都打 1~3 条 ERROR
	if n := cap.count(slog.LevelError); n != 0 {
		t.Fatalf("回落到 main 是正常路径，不该有 ERROR，实得 %d 条：%v", n, cap.errors())
	}
}

func TestClassifyWorktreeUnknownKeepsSingleWarn(t *testing.T) {
	cap := captureLogs(t)
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-b84", "f-b84")
	entries, err := repoWorktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("读工作树册：%v", err)
	}
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("删工作树目录：%v", err)
	}
	state, _, _ := classifyWorktree(context.Background(), entries, wt)
	if state != proto.WorktreeUnknown && state != proto.WorktreePrunable {
		t.Fatalf("目录已失应判 unknown 或 prunable，实得 %v", state)
	}
	// 改造前这里是双日志：底层一条 ERROR（噪音）+ 上层一条 Warn（有信息的那条）
	if n := cap.count(slog.LevelError); n != 0 {
		t.Fatalf("判不出是诚实结论，不是故障，不该有 ERROR，实得 %d 条：%v", n, cap.errors())
	}
}
```

补 import：`"os"`、`"github.com/xushixin/handoff/internal/proto"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestResolveBaseBranchNoOriginHead|TestClassifyWorktreeUnknown' -v`
Expected: FAIL，报「不该有 ERROR，实得 N 条」（N ≥ 1）。若 `TestClassifyWorktree` 那条走的是 `prunable` 早返回分支而没打 ERROR，属正常——保留该用例作为回归护栏即可。

- [ ] **Step 3: 逐处迁移**

`internal/agentd/workspace.go` — `EnsureRepoUsable`（现 444）：

```go
	_, stderr, res, _ := gitProbe(ctx, repo, "rev-parse", "--git-dir")
	if res != probeHit {
		log().Warn("dispatch 前置：任务仓库不可用，拒绝派发", "repo", repo,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "res", res)
		return fmt.Errorf("%w: %s", ErrRepoUnusable, strings.TrimSpace(stderr))
	}
```

`currentRef`（现 591）：

```go
	// -q 让 detached 时安静地非零退出，而不是往 stderr 刷错误；
	// detached HEAD 是正常态，未命中走下面的 rev-parse 兜底
	if out, _, res, _ := gitProbe(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); res == probeHit {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}
```

（下一行 `gitRun(ctx, dir, "rev-parse", "HEAD")` **保持不变**——那一步失败确实是取不到 ref 的真故障。）

`branchTip`（现 617）：

```go
	out, stderr, res, _ := gitProbe(ctx, repo, "rev-parse", "refs/heads/"+branch)
	if res != probeHit {
		return "", fmt.Errorf("git rev-parse refs/heads/%s: %s: 探测结局 %v",
			branch, strings.TrimSpace(stderr), res)
	}
	return strings.TrimSpace(out), nil
```

`hasCommit`（现 843）：

```go
func hasCommit(ctx context.Context, repo, sha string) bool {
	// 未命中是常态：对象库里没有就去 fetch，fetch 失败自己会响
	_, _, res, _ := gitProbe(ctx, repo, "cat-file", "-e", sha+"^{commit}")
	return res == probeHit
}
```

`resolveBaseBranch`（现 910、914）：

```go
	if out, _, res, _ := gitProbe(context.Background(), repo,
		"symbolic-ref", "--short", "refs/remotes/origin/HEAD"); res == probeHit && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	for _, cand := range []string{"main", "master"} {
		if _, _, res, _ := gitProbe(context.Background(), repo,
			"rev-parse", "--verify", "--quiet", cand); res == probeHit {
			return cand
		}
	}
```

`internal/agentd/reclaim.go` — `classifyWorktree`（现 191）：

```go
	out, stderr, res, _ := gitProbe(sctx, workdir, "status", "--porcelain")
	if res != probeHit {
		note := strings.TrimSpace(truncateRunes(stderr, 200))
		log().Warn("工作树判定：读不到 status，判不出",
			"workdir", workdir, "stderr", note, "res", res)
		return proto.WorktreeUnknown, nil, note
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestResolveBaseBranchNoOriginHead|TestClassifyWorktreeUnknown' -v`
Expected: PASS

Run: `go test ./internal/agentd/`
Expected: ok。若既有用例断言过错误文本里的 `exit status 1` 等字样，改断言而非改实现——`branchTip` 的错误文本已从 `%w` 包装 err 改为带结局，这是有意的。

- [ ] **Step 5: 确认日志覆盖**

- `EnsureRepoUsable` 与 `classifyWorktree` 的 Warn 保留，并把 `cause` 换成 `res` 字段（结局比一个 `exit status 1` 更有信息）。
- `EnsureRepoUsable` 成功路径的 Info「仓库有效性校验通过」保持不动——成功路径不静默。
- `currentRef` 取不到时的 Warn「采集原 ref 失败，补偿将无法复原工作树」保持不动。
- 迁移后不新增日志点：这 7 处的信息量本来就在上层，底层多嘴才是被修的问题。

- [ ] **Step 6: 确认注释覆盖**

- `currentRef` 的 `-q` 注释扩写成「detached HEAD 是正常态，未命中走 rev-parse 兜底」。
- `hasCommit` 加一行说明未命中触发 fetch、不是故障。
- `EnsureRepoUsable` 原有的判据注释（「用 rev-parse --git-dir 而不是 grep 错误串」）保留不动。
- `branchTip` 的既有注释块保留——它写着「不在此处打日志」，迁移后这句终于成真，不要删。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/reclaim.go internal/agentd/workspace_probe_test.go
git commit -m "fix(agentd): 7 处仅判命中的探测调用点改走 gitProbe

含全仓最密的噪音源 resolveBaseBranch（每次不带 --base 的 diff
打 1~3 条 ERROR），以及 classifyWorktree 的底层噪音+上层 Warn 双日志。
branchTip 的「不在此处打日志」注释至此才名副其实。"
```

---

### Task 4: 迁移 4 处需要区分 fatal 的探测调用点

这 4 处的下游会产出**给人看的拒发理由**，把 fatal 当 miss 会报出「不存在」这种假真因。

**Files:**
- Modify: `internal/agentd/workspace.go`（`PrepareWorkspace` 252、`resolveCommit` 713/722/741）
- Test: `internal/agentd/workspace_probe_test.go`（续写）

**Interfaces:**
- Consumes: Task 2 的 `gitProbe` / `probeHit` / `probeFatal`、`stubGitExec`、既有哨兵 `ErrNoProcHeadroom`（`admission.go`）
- Produces: 无新签名

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/workspace_probe_test.go`：

```go
// TestResolveCommitRemoteOnlyBaseLogsNoError 是 B81 现场的回归护栏：
// 「本地无同名分支、只有 origin/<name>」是远程派发的常态，整条路径成功，
// 日志里不该有 ERROR。
func TestResolveCommitRemoteOnlyBaseLogsNoError(t *testing.T) {
	origin := initGitRepo(t)
	gitAt(t, origin, "branch", "b76-smoke-base")
	parent := t.TempDir()
	gitAt(t, parent, "clone", "-q", origin, "clone")
	work := filepath.Join(parent, "clone")
	// 前提校验：本地确实没有同名分支，只有远程跟踪 ref
	if out := gitOut(t, work, "branch", "--list", "b76-smoke-base"); out != "" {
		t.Fatalf("前提不成立：克隆里出现了本地同名分支 %q", out)
	}

	cap := captureLogs(t)
	sha, err := resolveCommit(context.Background(), work, "b76-smoke-base")
	if err != nil {
		t.Fatalf("只有远程跟踪 ref 时应解析成功：%v", err)
	}
	if len(sha) != 40 {
		t.Fatalf("应返回 40 位 sha，实得 %q", sha)
	}
	if n := cap.count(slog.LevelError); n != 0 {
		t.Fatalf("成功路径不该有 ERROR，实得 %d 条：%v", n, cap.errors())
	}
}

// TestResolveCommitFatalProbeDoesNotClaimMissing 是判据 3 的端到端那半：
// 机器 fork 不出进程时，报文必须指向资源耗尽，而不是「起点不存在」。
func TestResolveCommitFatalProbeDoesNotClaimMissing(t *testing.T) {
	stubGitExec(t, func(context.Context, string, []string) (string, string, error) {
		return "", "", fmt.Errorf("fork/exec git: %w", syscall.EAGAIN)
	})
	_, err := resolveCommit(context.Background(), "/repo/x", "some-base")
	if err == nil {
		t.Fatal("探测未能完成时应拒发")
	}
	if strings.Contains(err.Error(), "不存在") {
		t.Fatalf("fork 耗尽被误报成「起点不存在」，正是本次要消灭的假报文：%v", err)
	}
	if !errors.Is(err, ErrNoProcHeadroom) {
		t.Fatalf("应归到进程余量哨兵，实得 %v", err)
	}
}

// TestPrepareWorkspaceFatalProbeDoesNotClaimBranchMissing 同上，另一处调用点。
func TestPrepareWorkspaceFatalProbeDoesNotClaimBranchMissing(t *testing.T) {
	stubGitExec(t, func(context.Context, string, []string) (string, string, error) {
		return "", "", fmt.Errorf("fork/exec git: %w", syscall.EAGAIN)
	})
	_, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: "/repo/x", TaskID: "t-b84-fatal", Branch: "feature-x",
	})
	if err == nil {
		t.Fatal("探测未能完成时应拒绝")
	}
	if strings.Contains(err.Error(), "不存在") {
		t.Fatalf("fork 耗尽被误报成「分支不存在」：%v", err)
	}
	if !errors.Is(err, ErrNoProcHeadroom) {
		t.Fatalf("应归到进程余量哨兵，实得 %v", err)
	}
}
```

补 import：`"path/filepath"`、`"strings"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run 'TestResolveCommit|TestPrepareWorkspaceFatal' -v`
Expected: 两个 fatal 用例 FAIL（报文里出现「不存在」）；`TestResolveCommitRemoteOnlyBaseLogsNoError` 也 FAIL（实得 ≥1 条 ERROR）——那正是 B81 现场。

- [ ] **Step 3: 实现**

`PrepareWorkspace`（现 251-254）替换为：

```go
	if isExisting {
		// 分支存在性：rev-parse --verify --quiet refs/heads/<name>，非零即不存在
		out, _, res, note := gitProbe(ctx, req.Repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+req.Branch)
		if res == probeFatal {
			// 探测没能完成 ≠ 分支不存在。报「不存在」会让审核者去查一个根本没问题
			// 的分支名，而真因是这台机器 fork 不出进程
			log().Error("分支存在性探测未能完成，拒绝派发",
				"repo", req.Repo, "branch", req.Branch, "note", note)
			return Workspace{}, fmt.Errorf("%w：校验分支 %s 是否存在时，%s",
				ErrNoProcHeadroom, req.Branch, note)
		}
		if res != probeHit || strings.TrimSpace(out) == "" {
			return Workspace{}, rejectWorkspace("分支 "+req.Branch+" 不存在", req)
		}
	}
```

`resolveCommit`（现 713 起）：

```go
	out, stderr, res, note := gitProbe(ctx, repo, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	sha := strings.TrimSpace(out)
	if res == probeHit && sha != "" {
		log().Info("起点已解析为提交号", "repo", repo, "base", rev, "sha", sha)
		return sha, nil
	}
	// 探测未能完成时绝不能继续走「找不到就是不存在」的兜底链：起点可能好端端
	// 在那儿，只是机器 fork 不出进程去问（spec §3.1）
	if res == probeFatal {
		log().Error("起点解析未能完成，拒绝派发", "repo", repo, "base", rev, "note", note)
		return "", fmt.Errorf("%w：解析起点 %s 时，%s", ErrNoProcHeadroom, rev, note)
	}
	// rev-parse 不 DWIM：base 分支只以 refs/remotes/*/<rev> 存在时上面的调用取不到
	// （B76 的触发前提正是这种仓库）。按 git checkout 的 guess 语义补一次「唯一
	// 远程跟踪 ref」匹配，剥到 commit 后与主路径同款校验与落日志。
	matches, _, mres, mnote := gitProbe(ctx, repo, "for-each-ref", "--format=%(refname)", "refs/remotes/*/"+rev)
	if mres == probeFatal {
		log().Error("远程跟踪 ref 枚举未能完成，拒绝派发", "repo", repo, "base", rev, "note", mnote)
		return "", fmt.Errorf("%w：枚举起点 %s 的远程跟踪 ref 时，%s", ErrNoProcHeadroom, rev, mnote)
	}
```

（中间 `cands` 收集与「多于一棵远端」的歧义分支**保持原样不动**。）

`len(cands) == 1` 分支（现 741）：

```go
	if len(cands) == 1 {
		mout, _, cres, cnote := gitProbe(ctx, repo, "rev-parse", "--verify", "--quiet", cands[0]+"^{commit}")
		if cres == probeFatal {
			log().Error("远程跟踪 ref 解析未能完成，拒绝派发",
				"repo", repo, "base", rev, "ref", cands[0], "note", cnote)
			return "", fmt.Errorf("%w：解析起点 %s（%s）时，%s",
				ErrNoProcHeadroom, rev, cands[0], cnote)
		}
		if cres == probeHit && strings.TrimSpace(mout) != "" {
			sha = strings.TrimSpace(mout)
			log().Info("起点已解析为提交号（远程跟踪分支）", "repo", repo, "base", rev, "sha", sha, "ref", cands[0])
			return sha, nil
		}
	}
```

（末尾的 `log().Warn("起点解析失败，拒绝派发", ...)` 与 `ErrBadWorkspaceReq` 返回**保持不变**——走到那里确实是「真的不存在」。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run 'TestResolveCommit|TestPrepareWorkspaceFatal' -v`
Expected: 三个用例全 PASS

Run: `go test ./internal/agentd/`
Expected: ok

- [ ] **Step 5: 确认日志覆盖**

- 四个 fatal 分支各有一条 Error，字段含 `repo` / `base` 或 `branch` / `note`。
- `resolveCommit` 两条成功路径的 Info（主路径与远程跟踪分支）保持不动——成功路径不静默，且这两条正是判据 1 的观察点。
- 末尾「起点解析失败」的 Warn 保持不动。
- 歧义分支的 Warn（多远端同名）保持不动。

- [ ] **Step 6: 确认注释覆盖**

- `PrepareWorkspace` 的 fatal 分支写明「探测没能完成 ≠ 分支不存在」及其后果。
- `resolveCommit` 的 fatal 分支写明「绝不能继续走兜底链」的理由并引 spec §3.1。
- `resolveCommit` 头部既有的大段注释（B76 的 DWIM 陷阱、歧义拒发的理由）**一字不改**。
- 两个 fatal 回归测试的注释写明它们守的是哪条判据。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_probe_test.go
git commit -m "fix(agentd): 派发路径的探测点区分 fatal 与未命中

PrepareWorkspace 与 resolveCommit 的拒发理由会给人看：fork 耗尽时
报「分支/起点不存在」是假真因。改判 ErrNoProcHeadroom 并带归因文案。
附 B81 现场的回归护栏（只有 origin/<name> 时全程零 ERROR）。"
```

---

### Task 5: `ErrWorkspaceGone` 与 `RunCmd` 的 Dir 纵深预检

**Files:**
- Modify: `internal/agentd/workspace.go`（`RunCmd`，现 1070 起；哨兵定义放在文件既有 `Err*` 声明处）
- Test: `internal/agentd/workspace_probe_test.go`（续写）

**Interfaces:**
- Produces: `var ErrWorkspaceGone = errors.New("任务工作区已不存在")`
- Consumes: 无（不依赖 Task 1–4，可与之并行验收）

- [ ] **Step 1: 写失败测试**

追加到 `internal/agentd/workspace_probe_test.go`：

```go
// TestRunCmdWorkspaceGoneReportsRealCause 守的是 B82：工作树被回收后执行命令，
// 报文必须指向工作区，而不是 /bin/sh。
//
// 【本用例唯一的失效方式，改动 RunCmd 时必读】
// 它依赖 RunCmd 内的 setProcGroup 仍然存在。Go 的 os.startProcess 只在
// SysProcAttr == nil 时才预检 Dir；一旦有人去掉进程组设置，Go 会自行给出清楚的
// 「chdir ...」报文，本用例照样 PASS，而线上照样报 /bin/sh。动 setProcGroup 时
// 必须回头确认本用例仍有区分力。
func TestRunCmdWorkspaceGoneReportsRealCause(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatalf("造工作树：%v", err)
	}
	if err := os.RemoveAll(gone); err != nil { // 等价于 stop 的 worktree_removed=true
		t.Fatalf("回收工作树：%v", err)
	}
	_, _, err := RunCmd(context.Background(), gone, "pwd")
	if err == nil {
		t.Fatal("工作区已回收时应报错")
	}
	if !errors.Is(err, ErrWorkspaceGone) {
		t.Fatalf("应归到 ErrWorkspaceGone，实得 %v", err)
	}
	if strings.Contains(err.Error(), "/bin/sh") {
		t.Fatalf("报文仍指向 /bin/sh，归因未修：%v", err)
	}
	if !strings.Contains(err.Error(), gone) {
		t.Fatalf("报文应含工作区路径，否则看的人不知道是哪棵树：%v", err)
	}
}

// TestRunCmdStillWorksWithLiveWorkspace 防预检把正常路径一起拦掉。
func TestRunCmdStillWorksWithLiveWorkspace(t *testing.T) {
	dir := t.TempDir()
	out, code, err := RunCmd(context.Background(), dir, "pwd")
	if err != nil {
		t.Fatalf("工作区存在时应正常执行：%v", err)
	}
	if code != 0 {
		t.Fatalf("pwd 应退 0，实得 %d", code)
	}
	if !strings.Contains(out, filepath.Base(dir)) {
		t.Fatalf("输出应是工作目录，实得 %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestRunCmdWorkspaceGone -v`
Expected: FAIL，`undefined: ErrWorkspaceGone`。把哨兵先加上再跑一次，应看到报文里含 `fork/exec /bin/sh: no such file or directory`——这就是线上那句，确认用例抓的是真现象。

- [ ] **Step 3: 实现**

哨兵（放在 `internal/agentd/workspace.go` 既有 `Err*` 声明附近）：

```go
// ErrWorkspaceGone 表示任务工作区目录已不存在（多因 stop / reclaim 回收了
// managed worktree）。与「仓库不可用」分开：那是仓库本身有问题，这是工作区
// 已被正常回收，两者给调用方的出路不同。
var ErrWorkspaceGone = errors.New("任务工作区已不存在")
```

`RunCmd` 里，在 `checkProcHeadroom` 之后、`exec.CommandContext` 之前插入：

```go
	// Go 的 os.startProcess 只在 SysProcAttr == nil 时才预检 Dir 是否存在
	// （src/os/exec_posix.go：「double-check existence of the directory we want
	// to chdir into. We can make the error clearer this way.」）。而 setProcGroup
	// 为了进程组回收必须设 SysProcAttr，这段好意因此被跳过，chdir 的 ENOENT 会被
	// 归到 argv[0] 头上，报成「fork/exec /bin/sh: no such file or directory」——
	// /bin/sh 当然存在，真因是工作树已被回收。
	//
	// 这里补的正是 Go 放弃的那一步。它不是「上面已经 Stat 过了」的重复：入口门禁
	// 与此处之间存在 TOCTOU 窗口，且 RunCmd 是导出函数，不能依赖调用方先检。
	// 删掉它，B84 的 bug 原样复活。
	if fi, serr := os.Stat(repo); serr != nil || !fi.IsDir() {
		log().Error("run 拒绝：任务工作区不可用", "repo", repo,
			"cmd", truncateRunes(cmdline, 200), "cause", serr)
		return "", -1, fmt.Errorf("%w：工作区 %s", ErrWorkspaceGone, repo)
	}
```

确认 `internal/agentd/workspace.go` 已 import `"os"`（若无则补）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestRunCmd -v`
Expected: 两个用例 PASS

Run: `go test ./internal/agentd/`
Expected: ok

- [ ] **Step 5: 确认日志覆盖**

- 拒绝分支打 Error，字段含 `repo` / `cmd` / `cause`（`cause` 为 `os.Stat` 的原始错误，目录存在但非目录时为 nil，可接受——`repo` 字段已足以定位）。
- `RunCmd` 既有的「run 命令执行」Info 与结束时的输出/退出码日志保持不动。
- 新增分支不静默：任何一次拒绝都在日志里留痕。

- [ ] **Step 6: 确认注释覆盖**

- 预检处的注释必须完整保留三层意思：Go 为什么放弃预检、我们为什么必须补、为什么它不是重复。**这段注释是防它被当成冗余删掉的唯一保险。**
- `ErrWorkspaceGone` 的 doc 注释写明与 `ErrRepoUnusable` 的分工。
- 测试里的「唯一失效方式」注释块必须在场。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/workspace.go internal/agentd/workspace_probe_test.go
git commit -m "fix(agentd): RunCmd 补回 Go 因 SysProcAttr 跳过的 Dir 预检

工作树被回收后执行命令会报「fork/exec /bin/sh: no such file or
directory」——真因是 setProcGroup 设了 SysProcAttr，Go 的 chdir 预检
被跳过，ENOENT 归到了 argv[0] 头上。补检并新增 ErrWorkspaceGone。"
```

---

### Task 6: `taskRepoOrErr` 入口门禁

一处改动同时覆盖 `run` / `diff` / `file` 三个动词。

**Files:**
- Modify: `internal/agentd/server.go:1018-1037`（`taskRepoOrErr`）
- Test: `internal/agentd/workspace_gone_server_test.go`（新建）

**Interfaces:**
- Consumes: Task 5 的 `ErrWorkspaceGone`（仅语义呼应，本任务用 `fs.ErrNotExist` 判定）；既有助手 `newTestManager`、`initGitRepo`、`newWorktree`、`seedTerminalTask`
- Produces: 无新签名（`taskRepoOrErr` 签名不变）

- [ ] **Step 1: 写失败测试**

新建 `internal/agentd/workspace_gone_server_test.go`：

```go
// workspace_gone_server_test.go —— 工作树已回收时三个审阅动词的入口门禁测试。
//
// 职责：
//   - 验证 run / diff / file 在工作区不存在时统一返回 409 且报文一致
//
// 边界：
//   - 不测 RunCmd 内部的纵深预检（那在 workspace_probe_test.go）
//   - 白盒（package agentd）：需要 initGitRepo / newWorktree / seedTerminalTask
package agentd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/proto"
)

// newServerWithReclaimedWorktree 造一个终态任务，其 managed worktree 目录已被删除。
func newServerWithReclaimedWorktree(t *testing.T) (*Server, string) {
	t.Helper()
	m, st, hub, _ := newTestManager(t)
	repo := initGitRepo(t)
	wt := newWorktree(t, repo, "wt-b84-gone", "f-b84-gone")
	id := seedTerminalTask(t, m, repo, wt, "f-b84-gone", proto.TaskStateFailed, true)
	if err := os.RemoveAll(wt); err != nil { // 等价于 stop/reclaim 清理
		t.Fatalf("回收工作树：%v", err)
	}
	srv := &Server{cfg: &config.Config{Token: "test"}, st: st, hub: hub, log: m.log, mgr: m}
	return srv, id
}

func TestReclaimedWorktreeRejectsReviewVerbs(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"run", http.MethodPost, "/run", `{"cmd":"pwd"}`},
		{"diff", http.MethodGet, "/diff", ""},
		{"file", http.MethodGet, "/file?path=README.md", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, id := newServerWithReclaimedWorktree(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, "/api/tasks/"+id+c.path, strings.NewReader(c.body))
			req.Header.Set("Authorization", "Bearer test")
			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("工作树已回收应返 409，实得 %d：%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "已回收") {
				t.Fatalf("报文应说明工作树已回收，实得：%s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "/bin/sh") {
				t.Fatalf("报文仍指向 /bin/sh：%s", rec.Body.String())
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/agentd/ -run TestReclaimedWorktreeRejectsReviewVerbs -v`
Expected: 三个子用例全 FAIL。`run` 那条实得 500 且报文含 `/bin/sh`（线上现象）；`diff` / `file` 实得 500 或 404，报文是 git 的怪话。

- [ ] **Step 3: 实现**

`internal/agentd/server.go` 的 `taskRepoOrErr`，把结尾的 `return workdir, true` 替换为：

```go
	// 工作区可能已被 stop / reclaim 回收。不在此拦住的话，run 会走到 exec 才炸出
	// 与真因无关的「fork/exec /bin/sh」，diff / file 则炸出 git 的怪话——三个动词
	// 共用本入口，判据放这里一处收全（spec §3.3）。
	//
	// 判据是「工作区还在不在」而不是「任务是不是终态」：终态但工作树仍在是合法且
	// 常见的（reclaim 明确保留 dirty 工作树就是为了让人事后进去翻），按终态拦会
	// 恰好掐掉最需要现场勘查的那个场景。
	fi, serr := os.Stat(workdir)
	switch {
	case serr == nil && fi.IsDir():
		return workdir, true
	case errors.Is(serr, fs.ErrNotExist):
		// 409 而不是 404：本函数上一分支已用 404 表达「任务不存在」，两者出路不同
		// （一个是 ID 敲错了，一个是任务还在、去 reclaim 或重新派发）
		s.log.Warn("任务工作树已回收，拒绝审阅动作", "task", taskID, "workdir", workdir)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "任务工作树已回收，无法执行该操作（工作区 " + workdir + " 已不存在）"})
	default:
		// 存在但不是目录、或 Stat 因权限等原因失败：环境异常，不冒充「已回收」
		s.log.Error("读取任务工作区失败", "task", taskID, "workdir", workdir, "cause", serr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "任务工作区不可用"})
	}
	return "", false
```

确认 `internal/agentd/server.go` 已 import `"os"`（`errors` 与 `io/fs` 该文件已在用，见其 1101 行的 `fs.ErrNotExist` 分支）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/agentd/ -run TestReclaimedWorktreeRejectsReviewVerbs -v`
Expected: 三个子用例全 PASS

Run: `go test ./internal/agentd/`
Expected: ok

Run: `go test ./...`
Expected: ok（全仓回归；`taskRepoOrErr` 被多个端点共用，跨包用例可能有依赖）

- [ ] **Step 5: 确认日志覆盖**

- 已回收分支打 Warn（可预期的拒绝，不是故障），字段含 `task` / `workdir`。
- 环境异常分支打 Error 带 `cause`。
- 正常分支不新增日志——各 handler 入口已有「run 请求 / diff 请求」的 Info。

- [ ] **Step 6: 确认注释覆盖**

- 门禁处注释写明两件事：为什么放在这个共用入口（一处收全三个动词），以及为什么判据是工作区存在性而不是任务状态。
- 409 的选择理由写在紧邻分支上（与上方 404 的分工）。
- 新测试文件有文件头注释（职责 + 边界 + 为什么白盒）。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/server.go internal/agentd/workspace_gone_server_test.go
git commit -m "fix(agentd): taskRepoOrErr 加工作区存在性门禁

run/diff/file 共用本入口，工作树被回收时统一返 409 并指向真因。
判据是工作区存在性而非任务状态——终态但工作树仍在是合法场景
（reclaim 保留 dirty 树正是为了事后勘查）。"
```

---

## 收尾检查

全部任务完成后，逐项确认（对照 spec §4 的五条判据）：

| 判据 | 覆盖它的用例 |
|---|---|
| 1 成功路径零 ERROR | `TestResolveCommitRemoteOnlyBaseLogsNoError`、`TestResolveBaseBranchNoOriginHeadLogsNoError`、`TestClassifyWorktreeUnknownKeepsSingleWarn` |
| 2 真失败仍打 ERROR | `TestGitRunRealFailureStillLogsError` |
| 3 fatal 打 ERROR 且不谎称「不存在」 | `TestClassifyProbeThreeOutcomes`、`TestGitProbeFatalStillLogsError`、`TestResolveCommitFatalProbeDoesNotClaimMissing`、`TestPrepareWorkspaceFatalProbeDoesNotClaimBranchMissing` |
| 4 RunCmd 报文指向工作区 | `TestRunCmdWorkspaceGoneReportsRealCause`（+ `TestRunCmdStillWorksWithLiveWorkspace` 防误伤） |
| 5 三动词统一 409 | `TestReclaimedWorktreeRejectsReviewVerbs` 的三个子用例 |

最后跑一次全量：

```bash
go test ./...
```

并确认 spec §2「非目标」四条都没被越界实现：CLI 未改、`reclaim.go:300` 未动、`diff`/`file` 的 git 报错文案未动、状态机未改。
