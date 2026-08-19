# 协调者审阅回路契约小修批 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修掉协调者审阅回路上的四条契约缺陷——`diff` 默认基准取错（B65）、`run` 的参数引号被吃掉（B66）、成功路径打 ERROR（B81）、门禁用例偶发翻红（B125）。

**Architecture:** 四条互不耦合，各自一个 task、一个 commit，严格按 Task 1→4 顺序执行。Task 1 是硬前置：它修的是 `go test ./...` 本身的稳定性，后三条的验收都要跑这条命令。全部改动落在既有文件里，不新增包、不改任何线格式（HTTP 请求/响应体只**增字段**，不改既有字段语义）。

**Tech Stack:** Go 1.26.1（module `github.com/Xsxdot/handoff`）、`log/slog`、cobra、React + TypeScript + Vitest（`web/`）。

**上游 spec:** [docs/superpowers/specs/2026-08-18-cli-contract-minor-fixes-design.md](../specs/2026-08-18-cli-contract-minor-fixes-design.md)

## Global Constraints

- **基线分支 `feat/cli-contract-minor-fixes`**（切自 `handoff/web-console@8b1203abd`）。本计划的全部行号以该基线为准；动手前先 `git log -1` 确认基线没变。
- **日志一律用包内 `log()`（`internal/agentd/workspace.go:116`，返回 `slog.Default()`）或 `s.log`（Server 方法内）。禁止 `fmt.Printf` / `println` 作为日志机制。**
- **注释一律中文**，解释「为什么」而不是「做了什么」。新文件写文件头注释（职责 + 边界），导出函数写 doc 注释。
- **不碰 `internal/localsync`**。它自己的 `run`（`internal/localsync/localsync.go:113`）不打日志，不构成 B81 的修复面。
- **不改 `POST /api/tasks/{id}/run` 的线格式**。B66 只改 `cmd/run.go`，服务端一行不动。
- **不碰 `internal/agentd/runshell.go`**。shell 选择是 B37 的交付，与本批无关。
- 每个 task 完成即 commit，提交信息**按各 Task 的 Commit 步骤里给定的原文**写。
- 收工前 `gofmt -l .` 必须无输出（两个 module：仓库根与 `desktop/`；本批只动根 module）。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/agentd/ws_regression_round2_test.go` | 修改 | 新增 `wsDeadline` helper；四处固定期限改走它（Task 1） |
| `internal/agentd/workspace.go` | 修改 | 抽出 `gitExec` 公共体，新增 `gitProbe`；六处探测调用改用它（Task 2）；`resolveBaseBranch` 注释更正（Task 3） |
| `internal/agentd/manualworktree.go` | 修改 | 两处探测调用改 `gitProbe`（Task 2） |
| `internal/agentd/gitprobe_test.go` | 新建 | `gitProbe` 的日志级别与返回值等价性测试（Task 2） |
| `internal/agentd/server.go` | 修改 | 新增 `taskOrErr`；`handleTaskDiff` / `handleTaskBranches` 改用任务基线（Task 3） |
| `internal/agentd/diffbase_test.go` | 新建 | `diffBaseFor` 与两个端点的基线测试（Task 3） |
| `web/src/api/types.ts` | 修改 | `BranchesResult` 增 `task_base`（Task 3） |
| `web/src/app/task/ReviewSidePanel.tsx` | 修改 | 「自动推导」括注三态（Task 3） |
| `web/src/app/task/ReviewSidePanel.test.tsx` | 修改 | 三态文案断言（Task 3） |
| `cmd/run.go` | 修改 | 新增 `shellQuote` / `shellJoin`；`:31` 改用 `shellJoin`（Task 4） |
| `cmd/run_test.go` | 新建 | `shellJoin` 表驱动测试（Task 4） |
| `README.md` / `README.zh-CN.md` / `skills/handoff/SKILL.md` | 修改 | `diff` 默认基准口径（Task 3）、`run` 的参数契约（Task 4） |

---

### Task 1: B125 — WS 用例的等待期限随负载放宽

**Files:**
- Modify: `internal/agentd/ws_regression_round2_test.go:129,160,224,272`（四处固定期限）
- Test: 同一文件（新增 `TestWSDeadlineStaysWithinBounds`）

**Interfaces:**
- Consumes: 无（本 task 不依赖前序）
- Produces: `func wsDeadline(t *testing.T, base time.Duration) time.Duration` —— 包内 test helper，后续 task 不使用

**背景（必读，决定了不许怎么改）：** 全量 `go test ./...` 第一遍报 `ws_regression_round2_test.go:229 等待 seq=21 时读失败: failed to get reader: context deadline exceeded`，耗时 10.01s，正好撞满 `:224` 的 `10*time.Second`。单独 `-run TestWSTruncationWarnsOnRealGap -count=1` 连跑 3 次全过。它等的是「WS 建连 + 重放 5 条 + 一条实时事件」，在整包并行下与其他用例争 goroutine——**是负载函数，不是常数**。所以不许把 10s 改成 20s 了事。

- [ ] **Step 1: 写 `wsDeadline` 的失败测试**

在 `internal/agentd/ws_regression_round2_test.go` 末尾追加：

```go
// TestWSDeadlineStaysWithinBounds 钉住 wsDeadline 的两条不变量（B125）：
// 结果永不低于 base（否则比修改前更容易翻红），也永不超过 base 的 3 倍
// （否则一个真挂住的用例会拖满整个 -timeout，把「哪个用例挂了」变成猜）。
func TestWSDeadlineStaysWithinBounds(t *testing.T) {
	const base = 10 * time.Second
	got := wsDeadline(t, base)
	if got < base {
		t.Errorf("wsDeadline 低于 base：got=%v base=%v", got, base)
	}
	if got > 3*base {
		t.Errorf("wsDeadline 超过 3 倍 base：got=%v base=%v", got, base)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd -run TestWSDeadlineStaysWithinBounds -count=1
```

Expected: 编译失败，`undefined: wsDeadline`

- [ ] **Step 3: 实现 `wsDeadline`**

在同一文件的 helper 区（`tailStr` 附近）追加：

```go
// wsDeadline 返回 WS 用例的等待期限：基准值放宽到 3 倍，并在 -timeout 余额
// 不足时收窄，但绝不低于 base。
//
// 为什么不是写死的 10s（B125）：本文件的用例等的是「建连 + 重放 N 条 + 一条
// 实时事件」，这条链路在整包并行下要与其他用例争 goroutine。实测单独跑 3 次
// 全过、全量第一遍撞满 10.01s——它是负载的函数，写死的数字治不了。
//
// 上限 3 倍 base 的理由：真挂住的用例不该拖满整个 -timeout，否则「哪个用例挂了」
// 只能靠猜。下限 base 的理由：-timeout 很短时若按余额收窄到 base 以下，会比
// 改动前更容易翻红。
//
// 局限：这是**负载缓解，不是根治**。根治要把 WS 用例与重负载用例隔开（分包或
// t.Parallel() 分组）。若本文件的用例仍偶发翻红，按分包处理，不要继续调这个倍数。
func wsDeadline(t *testing.T, base time.Duration) time.Duration {
	t.Helper()
	limit := 3 * base
	if dl, ok := t.Deadline(); ok {
		if quarter := time.Until(dl) / 4; quarter > base && quarter < limit {
			limit = quarter
		}
	}
	return limit
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd -run TestWSDeadlineStaysWithinBounds -count=1 -v
```

Expected: PASS

- [ ] **Step 5: 四处固定期限改走 `wsDeadline`**

把这四行分别替换（**只改这四处；`:234`、`:284`、`:358` 那三处 `3 * time.Second` 的轮询期限不动**——它们等的是日志落盘，轮询已经把「诊断未触发」与「日志没写到」区分开了，是另一类问题）：

- `:129` `ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`
  → `ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))`
- `:160` `ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)`
  → `ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 30*time.Second))`
- `:224` `ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`
  → `ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))`
- `:272` `ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)`
  → `ctx, cancel := context.WithTimeout(context.Background(), wsDeadline(t, 10*time.Second))`

- [ ] **Step 6: 加注释（本 task 的注释步骤）**

在 `TestWSTruncationWarnsOnRealGap`（`:211`）的 doc 注释末尾追加一段：

```go
// B125：本用例的等待期限走 wsDeadline 而非写死的 10s——它等的是建连 + 重放 5 条 +
// 一条实时事件，整包并行时会与其他用例争 goroutine。这是负载缓解不是根治，
// 若仍偶发翻红，按「WS 用例分包」处理，不要继续调倍数。
```

**本 task 没有日志步骤**：改动全部在 `_test.go` 里，不产生运行期行为，没有可打日志的键点。这是**有意的省略**，不是遗漏。

- [ ] **Step 7: 门禁回归（本条的核心判据）**

```bash
go build ./... && go vet ./... && gofmt -l . && for i in 1 2 3 4 5; do echo "=== 第 $i 轮 ==="; go test ./... -count=2 || exit 1; done
```

Expected: 五轮全绿，`gofmt -l .` 无输出。**单次全绿不算数**——原症状就是单次里的第一遍才翻。任一轮翻红就停下，把原始报错原文记进 ledger，不要归因、不要重跑掩盖。

- [ ] **Step 8: Commit**

```bash
git add internal/agentd/ws_regression_round2_test.go
git commit -m "test(agentd): WS 用例的等待期限随负载放宽（B125）

TestWSTruncationWarnsOnRealGap 在全量并行下撞满硬编码的 10s：它等的是
建连 + 重放 5 条 + 一条实时事件，是负载的函数不是常数。加 wsDeadline
helper，放宽到 3 倍 base 并在 -timeout 余额不足时收窄，下限保住 base。

这是负载缓解不是根治，用例注释里写明了根治方向是 WS 用例分包。"
```

---

### Task 2: B81 — 探测型 git 调用不再走 ERROR 通道

**Files:**
- Modify: `internal/agentd/workspace.go:131-155`（抽 `gitExec`，加 `gitProbe`）、`:325`、`:517`、`:833`、`:861`、`:963`、`:1034`
- Modify: `internal/agentd/manualworktree.go:67`、`:127`
- Create: `internal/agentd/gitprobe_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error)` —— 与 `gitRun` **完全同签名同返回语义**，只是失败记 Debug 不记 Error。Task 3 不直接使用它。

**背景：** `--base <分支名>` 的远程派发在**成功路径**上打 `level=ERROR msg="git 调用失败" args="[rev-parse --verify --quiet <name>^{commit}]" cause="exit status 1"`。远程执行机只 `fetch` 出 `origin/<name>`、从不建本地分支，所以这一步 exit=1 是预期内的未命中，随后 `for-each-ref refs/remotes/*/<name>` 命中、整条路径成功。按 `level=ERROR` 过滤日志会捞出正常路径。

- [ ] **Step 1: 写失败测试**

新建 `internal/agentd/gitprobe_test.go`：

```go
// gitprobe_test.go —— gitProbe 的两条约定：探测未命中不记 ERROR，且返回值与
// gitRun 逐字段等价。
//
// 边界：本文件不测 gitRun 的成功路径（既有测试已覆盖），只测「失败时的日志级别」
// 与「两者返回值一致」这两件 gitProbe 独有的事。
package agentd

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs 把 slog 默认 logger 换成写进 buffer 的 Debug 级 handler，
// 返回读取函数；测试结束自动还原。
//
// 为什么改默认 logger：workspace.go 的 log() 取的是 slog.Default()（运行时取值，
// 不是依赖注入），这是本包既有的约定，测试只能从这里切进去。
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf.String
}

// TestGitProbeMissDoesNotLogError 钉住 B81：探测未命中记 DEBUG 不记 ERROR。
func TestGitProbeMissDoesNotLogError(t *testing.T) {
	repo := newTestRepo(t)
	logs := captureLogs(t)

	_, _, err := gitProbe(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支")
	if err == nil {
		t.Fatal("探测不存在的分支应返回非 nil error")
	}
	out := logs()
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("探测未命中不该产生 ERROR；日志：%s", out)
	}
	if !strings.Contains(out, "level=DEBUG") {
		t.Errorf("探测未命中应留一条 DEBUG 供排障；日志：%s", out)
	}
}

// TestGitRunMissStillLogsError 反向钉住：真故障通道没被一起降级。
func TestGitRunMissStillLogsError(t *testing.T) {
	repo := newTestRepo(t)
	logs := captureLogs(t)

	if _, _, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支"); err == nil {
		t.Fatal("应返回非 nil error")
	}
	if out := logs(); !strings.Contains(out, "level=ERROR") {
		t.Errorf("gitRun 的失败仍应是 ERROR；日志：%s", out)
	}
}

// TestGitProbeReturnsSameAsGitRun 钉住两者返回值逐字段等价——gitProbe 只改日志
// 级别，调用方仍按 err != nil 判未命中，不得有任何语义差异。
func TestGitProbeReturnsSameAsGitRun(t *testing.T) {
	repo := newTestRepo(t)
	args := []string{"rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支"}

	runOut, runErrText, runErr := gitRun(context.Background(), repo, args...)
	probeOut, probeErrText, probeErr := gitProbe(context.Background(), repo, args...)

	if runOut != probeOut {
		t.Errorf("stdout 不一致：gitRun=%q gitProbe=%q", runOut, probeOut)
	}
	if runErrText != probeErrText {
		t.Errorf("stderr 不一致：gitRun=%q gitProbe=%q", runErrText, probeErrText)
	}
	if (runErr == nil) != (probeErr == nil) {
		t.Errorf("error 有无不一致：gitRun=%v gitProbe=%v", runErr, probeErr)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd -run 'TestGitProbe|TestGitRunMiss' -count=1
```

Expected: 编译失败，`undefined: gitProbe`

- [ ] **Step 3: 抽出 `gitExec` 并实现 `gitProbe`**

把 `internal/agentd/workspace.go:131` 的 `gitRun` 整体替换为下面三个函数（`gitRun` 的**签名与行为一字不变**，只是内部改调 `gitExec`）：

```go
// gitExec 是 gitRun / gitProbe 的公共体：执行 git -C repo <args...>。
//
// quiet 只影响**失败时的日志级别**：false 记 Error（真故障），true 记 Debug
// （预期内的探测未命中）。返回值语义与 quiet 无关。
func gitExec(ctx context.Context, repo string, quiet bool, args ...string) (stdout, stderr string, err error) {
	log().Info("git 调用", "repo", repo, "args", args)
	start := time.Now()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	log().Info("git 调用完成", "repo", repo, "args", args,
		"elapsed_ms", time.Since(start).Milliseconds())
	if err != nil {
		// 进程配额那条分支**不受 quiet 影响**：EAGAIN / fork failed 无论出现在
		// 哪种调用里都是真故障（B73 的资源耗尽归因就靠它），一律 Error。
		if note := quotaNote(err); note != "" {
			log().Error("git 调用失败（进程配额）", "repo", repo, "args", args,
				"note", note, "cause", err)
			return outBuf.String(), errBuf.String(), fmt.Errorf("%s: %w", note, err)
		}
		if quiet {
			log().Debug("git 探测未命中（预期内）", "repo", repo, "args", args,
				"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
		} else {
			log().Error("git 调用失败", "repo", repo, "args", args,
				"stderr", truncateRunes(errBuf.String(), 500), "cause", err)
		}
	}
	return outBuf.String(), errBuf.String(), err
}

// gitRun 执行 git -C repo <args...>，返回 stdout 与 stderr。
//
// 日志：调用前 Info（repo、args）、调用后 Info（耗时）；失败 Error 带 stderr
// 原文——git 报错原文是排障必需品，不能只留包装后的 error 文本。
//
// 用于**失败即故障**的调用（clone / fetch / worktree add / diff / log 等）。
// 失败是预期内结果的探测型调用请用 gitProbe。
func gitRun(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	return gitExec(ctx, repo, false, args...)
}

// gitProbe 与 gitRun 相同，但把**非零退出**当成预期内的探测结果而非故障：
// 失败记 Debug 不记 Error。返回值语义完全不变（调用方仍按 err != nil 判未命中）。
//
// 为什么需要它（B81）：探测型调用（rev-parse --verify --quiet、cat-file -e）的
// 非零退出是**正常分支**——远程执行机只 fetch 出 origin/<name>、从不建本地分支，
// 所以「本地同名分支不存在」是常态。经 gitRun 打成 ERROR 后，成功路径的日志里
// 躺着 ERROR，与真故障无法区分；按 level=ERROR 过滤日志会捞出正常路径。
//
// 边界：只用于「失败是预期内结果」的调用。会真正出事的 git 调用仍走 gitRun。
// 进程配额失败无论走哪个入口都仍是 Error。
func gitProbe(ctx context.Context, repo string, args ...string) (stdout, stderr string, err error) {
	return gitExec(ctx, repo, true, args...)
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd -run 'TestGitProbe|TestGitRunMiss' -count=1 -v
```

Expected: 三个用例全 PASS

- [ ] **Step 5: 八处探测调用改用 `gitProbe`**

逐处把 `gitRun(` 改成 `gitProbe(`，**只改这八处**：

| 文件:行 | 调用 |
|---|---|
| `internal/agentd/workspace.go:325` | `rev-parse --verify --quiet refs/heads/<branch>` |
| `internal/agentd/workspace.go:517` | `rev-parse --git-dir` |
| `internal/agentd/workspace.go:833` | `rev-parse --verify --quiet <rev>^{commit}` |
| `internal/agentd/workspace.go:861` | `rev-parse --verify --quiet <cand>^{commit}` |
| `internal/agentd/workspace.go:963` | `cat-file -e <sha>^{commit}` |
| `internal/agentd/workspace.go:1034` | `rev-parse --verify --quiet <main\|master>` |
| `internal/agentd/manualworktree.go:67` | `rev-parse --verify --quiet refs/heads/<branch>` |
| `internal/agentd/manualworktree.go:127` | `rev-parse --verify --quiet <base>` |

**不要改 `resolveBaseBranch` 里的 `symbolic-ref --short refs/remotes/origin/HEAD`（`:1030` 附近）**——它同样是探测，但改它会让本 task 的 diff 超出上表；留给后续条目。（**这是有意的取舍，记进 ledger。**）

- [ ] **Step 6: 加日志（本 task 的日志步骤）**

日志改动已含在 Step 3 的 `gitExec` 里，逐条对照确认：
- 进入键点：`log().Info("git 调用", ...)` 带 repo 与 args —— 保持不变
- 外部调用后：`log().Info("git 调用完成", ...)` 带耗时 —— 保持不变
- 错误分支：真故障 `Error` 带 stderr 原文 + cause；探测未命中 `Debug` 带同样的上下文 —— **新增的这条 Debug 必须带 stderr 与 cause**，否则排障时探测路径变成黑箱
- 进程配额分支：无论 quiet 与否都 `Error` 带 note + cause —— 保持不变

- [ ] **Step 7: 加注释（本 task 的注释步骤）**

- 新文件 `gitprobe_test.go` 的文件头注释（职责 + 边界）—— 已含在 Step 1
- `gitExec` / `gitRun` / `gitProbe` 三个 doc 注释 —— 已含在 Step 3
- 在 `internal/agentd/workspace.go:1034` 那处（`resolveBaseBranch` 的兜底循环）加一行 inline：

```go
// 这里的未命中是正常分支（仓库可能既没有 main 也没有 master），走 gitProbe
// 才不会在成功路径上留 ERROR。
```

- [ ] **Step 8: 全量回归**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```

Expected: 全绿，`gofmt -l .` 无输出

- [ ] **Step 9: Commit**

```bash
git add internal/agentd/workspace.go internal/agentd/manualworktree.go internal/agentd/gitprobe_test.go
git commit -m "fix(agentd): 探测型 git 调用不再走 ERROR 通道（B81）

rev-parse --verify --quiet / cat-file -e 这类调用的非零退出是预期内的
未命中——远程执行机只 fetch 出 origin/<name>、从不建本地分支，所以
「本地同名分支不存在」是常态。经 gitRun 打成 ERROR 后，成功路径的日志里
躺着 ERROR，按 level=ERROR 过滤会捞出正常路径。

抽 gitExec 公共体，加 gitProbe（失败记 Debug），八处探测调用改用它。
返回值语义一字未变；进程配额失败无论走哪个入口仍是 ERROR（B73 的归因靠它）。"
```

---

### Task 3: B65 — `diff` 缺省基准优先用任务自己的 `base_commit`

**Files:**
- Modify: `internal/agentd/server.go:1342`（`taskRepoOrErr` 拆出 `taskOrErr`）、`:1373-1381`（`handleTaskDiff`）、`:1406-1421`（`handleTaskBranches`）
- Modify: `internal/agentd/workspace.go:1025` 附近（`resolveBaseBranch` 的过时注释）
- Create: `internal/agentd/diffbase_test.go`
- Modify: `web/src/api/types.ts:365-369`、`web/src/app/task/ReviewSidePanel.tsx:89`、`web/src/app/task/ReviewSidePanel.test.tsx`
- Modify: `README.md:285`、`README.zh-CN.md:171`、`skills/handoff/SKILL.md`（diff 段）

**Interfaces:**
- Consumes: 无（不依赖 Task 1/2 的产出）
- Produces:
  - `func diffBaseFor(task *proto.Task, repo string) string` —— 任务 diff 的缺省基准 rev
  - `func (s *Server) taskOrErr(w http.ResponseWriter, taskID string) (*proto.Task, bool)` —— 读任务并做 404/400 门禁，`taskRepoOrErr` 变成它的薄包装
  - `GET /api/tasks/{id}/branches` 响应新增 `task_base` 字段（string，任务的 `BaseCommit`，可为空串）

**背景：** W2 任务的 `handoff diff` 默认吐 26611 行（含 `main` 与特性分支之间的全部历史），真实改动只有 3274 行。`server.go:1379` 在 `base` 缺省时调 `resolveBaseBranch`，而任务记录里本来就有 `BaseCommit`（`proto.go:235`）。`resolveBaseBranch` 的注释还写着「派发时并未记录基准分支名」——那是 B35 之前的事实。

**`BaseCommit` 为空是有语义的**，不是「缺字段」：proto 注释写明「空=切已存在分支（没有起点这回事）或老任务」。所以退回推导链是**正当的正常分支**。

**为什么必须同时改 `handleTaskBranches` 与前端**：`handleTaskBranches` 的 `default` 喂着前端 `ReviewSidePanel.tsx:89` 的 `自动推导（{default}）`。只改 `handleTaskDiff` 的话，控制台会显示「自动推导（main）」而 diff 实际用 `BaseCommit`——一个当场可见的谎。

- [ ] **Step 1: 写后端失败测试**

新建 `internal/agentd/diffbase_test.go`：

```go
// diffbase_test.go —— diff 缺省基准的取值规则（B65）：任务有 BaseCommit 就用它，
// 没有才退回按仓库推导。
//
// 边界：本文件不测 Diff() 本身的输出格式（既有行为未变），只测「缺省基准取谁」
// 以及两个端点是否一致地采用了同一个取值。
package agentd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// TestDiffBaseForPrefersTaskBaseCommit 钉住优先级：BaseCommit 非空即用它。
func TestDiffBaseForPrefersTaskBaseCommit(t *testing.T) {
	repo := newTestRepo(t)
	task := &proto.Task{ID: "t1", BaseCommit: "0123456789abcdef0123456789abcdef01234567"}
	if got := diffBaseFor(task, repo); got != task.BaseCommit {
		t.Errorf("应优先用任务基线：got=%q want=%q", got, task.BaseCommit)
	}
}

// TestDiffBaseForFallsBackWhenNoBaseCommit 钉住退回：BaseCommit 为空（切已存在
// 分支或老任务）时按仓库推导，退回是正常分支不是兜底。
func TestDiffBaseForFallsBackWhenNoBaseCommit(t *testing.T) {
	repo := newTestRepo(t) // newTestRepo 建的是 main 分支
	task := &proto.Task{ID: "t1"}
	if got := diffBaseFor(task, repo); got != "main" {
		t.Errorf("应退回推导链：got=%q want=%q", got, "main")
	}
}

// TestBranchesEndpointReportsTaskBase 钉住端点一致性：branches 必须把 diff 实际
// 会用的任务基线报出来，否则前端「自动推导（…）」会显示与实际不符的值。
func TestBranchesEndpointReportsTaskBase(t *testing.T) {
	const token = "diffbase-token"
	const sha = "0123456789abcdef0123456789abcdef01234567"
	repo := newTestRepo(t)

	st, err := store.Open(t.TempDir() + "/diffbase.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{
		ID: "t1", Target: "local", State: proto.TaskStatePending,
		WorkDir: repo, BaseCommit: sha, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	srv := NewServer(&config.Config{Token: token, DataDir: t.TempDir()}, st, discardLogger())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks/t1/branches", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 branches: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	var body struct {
		Branches []string `json:"branches"`
		Default  string   `json:"default"`
		TaskBase string   `json:"task_base"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body.TaskBase != sha {
		t.Errorf("task_base 不对：got=%q want=%q", body.TaskBase, sha)
	}
	if !strings.Contains(strings.Join(body.Branches, ","), "main") {
		t.Errorf("分支列表应含 main：%v", body.Branches)
	}
}
```

**`discardLogger()` 直接用包内既有的那个**（`internal/agentd/watchdog_test.go:32`），
**不要重复定义**——同包内重复声明是编译错误。本文件不需要 `io` / `log/slog` 的 import。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd -run 'TestDiffBaseFor|TestBranchesEndpointReportsTaskBase' -count=1
```

Expected: 编译失败，`undefined: diffBaseFor`

- [ ] **Step 3: 实现后端**

**3a.** 在 `internal/agentd/server.go` 的 `taskRepoOrErr`（`:1342`）**之前**插入 `taskOrErr`，并把 `taskRepoOrErr` 改写成它的薄包装（**保持 `taskRepoOrErr` 的签名与行为不变**——它另有三个调用点：`frames_stream.go:53`、`render_stream.go:54`、`server.go:1492`）：

```go
// taskOrErr 读取路径中的任务并做门禁：任务不存在写 404、缺工作区路径写 400，
// 两种情况都返回 ok=false（调用方直接 return 即可）。
//
// 为什么独立于 taskRepoOrErr：diff / branches 两个端点除了工作区路径还要读
// 任务的 BaseCommit（B65），而另外三个调用点只关心路径。拆开后既不动它们的
// 签名，也不必让它们承担一个用不到的返回值。
func (s *Server) taskOrErr(w http.ResponseWriter, taskID string) (*proto.Task, bool) {
	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
		} else {
			s.log.Error("读取任务失败", "task", taskID, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		}
		return nil, false
	}
	if task.Workdir() == "" {
		s.log.Warn("任务缺少工作区路径", "task", taskID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "任务没有工作区路径"})
		return nil, false
	}
	return task, true
}

// taskRepoOrErr 读取路径中的任务并返回其工作区目录；任务不存在（404）或没有
// 工作区路径（400）时已写好响应并返回 ok=false。
func (s *Server) taskRepoOrErr(w http.ResponseWriter, taskID string) (repo string, ok bool) {
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return "", false
	}
	return task.Workdir(), true
}
```

**3b.** 在 `internal/agentd/workspace.go` 的 `resolveBaseBranch` 之前加 `diffBaseFor`：

```go
// diffBaseFor 返回任务 diff 的缺省基准 rev：任务自己的 BaseCommit 优先，
// 为空才按仓库推导。
//
// 为什么优先任务基线（B65）：按仓库默认分支推导会把「默认分支与任务分支之间的
// 全部历史」也算进 diff——实测一个真实任务默认吐 26611 行而真实改动只有 3274 行，
// 审核者第一眼拿到的素材被淹掉。任务记录里本来就有它建在哪个提交上（B35 起）。
//
// BaseCommit 为空**不是缺字段**：proto 注释写明「空=切已存在分支（没有起点这回事）
// 或老任务」，所以退回推导链是正常分支，不是兜底。
//
// 返回空串表示两条路都取不到（非 git 仓库、既无 main 也无 master），
// 由调用方报 400 并提示显式指定 base。
func diffBaseFor(task *proto.Task, repo string) string {
	if task != nil && task.BaseCommit != "" {
		return task.BaseCommit
	}
	return resolveBaseBranch(repo)
}
```

（`workspace.go` 需要 import `internal/proto`；若已 import 则跳过。）

**3c.** 更正 `resolveBaseBranch` doc 注释里那句过时的话：把

```
// 为什么需要这个兜底链：任务仓库的分支名不可预知（main/master/dev 皆可能），
// 派发时并未记录基准分支名，diff 必须从仓库自身推导出合理默认。
```

改成

```
// 为什么需要这个推导链：任务仓库的分支名不可预知（main/master/dev 皆可能）。
//
// 注意（B65 更正）：「派发时并未记录基准分支名」这句原文已经过时——B35 起任务
// 记录里有 BaseCommit。缺省基准现在由 diffBaseFor 决定，本函数只负责
// **没有任务基线时**的推导，不再是 diff 的唯一入口。
```

**3d.** `handleTaskDiff`（`:1373` 起）改用任务基线：

```go
	task, ok := s.taskOrErr(w, taskID)
	if !ok {
		return
	}
	repo := task.Workdir()
	base := r.URL.Query().Get("base")
	if base == "" {
		base = diffBaseFor(task, repo)
	}
```

**3e.** `handleTaskBranches`（`:1406` 起）同样改用 `taskOrErr`，并在响应里增字段：

```go
	writeJSON(w, http.StatusOK, map[string]any{
		"branches":  branches,
		"default":   resolveBaseBranch(repo),
		"task_base": task.BaseCommit,
	})
```

**`default` 的语义不变**（仍是推导结果），新增的 `task_base` 表示 diff 实际会用的任务基线——两个字段分别对应两件事，前端才能分辨。

- [ ] **Step 4: 跑后端测试确认通过**

```bash
go test ./internal/agentd -run 'TestDiffBaseFor|TestBranchesEndpointReportsTaskBase' -count=1 -v
```

Expected: 三个用例全 PASS

- [ ] **Step 5: 写前端失败测试**

在 `web/src/app/task/ReviewSidePanel.test.tsx` 的 `describe` 内追加三个用例：

```tsx
  it('自动推导括注：有任务基线时显示前 8 位 sha', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({
      branches: ['main'], default: 'main', task_base: '0123456789abcdef0123456789abcdef01234567',
    })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导（任务基线 01234567）' })).toBeInTheDocument()
  })
  it('自动推导括注：无任务基线时退回分支名', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main'], default: 'main', task_base: '' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导（main）' })).toBeInTheDocument()
  })
  it('自动推导括注：两者皆空时不带括注', async () => {
    vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: [], default: '', task_base: '' })
    render(<ReviewSidePanel taskId="t1" onClose={() => {}} />)
    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.getByRole('option', { name: '自动推导' })).toBeInTheDocument()
  })
```

同时把文件顶部 `beforeEach` 里的既有 mock 补上新字段，避免类型报错：

```tsx
  vi.mocked(fetchTaskBranches).mockResolvedValue({ branches: ['main', 'dev'], default: 'main', task_base: '' })
```

- [ ] **Step 6: 跑前端测试确认失败**

```bash
cd web && npx vitest run src/app/task/ReviewSidePanel.test.tsx
```

Expected: 类型/断言失败——`task_base` 不在 `BranchesResult` 上，且括注文案不匹配

- [ ] **Step 7: 实现前端**

**7a.** `web/src/api/types.ts:365-369`：

```ts
// BranchesResult 是 GET /api/tasks/{id}/branches 的响应：本地分支名 + 推导默认
// + 任务自己的基线。
//
// default 为空串 = 按仓库推导不出。
// task_base 为空串 = 该任务没有记录基线（切已存在分支或老任务）。
// 两者都非空时 diff 实际用的是 task_base——B65 之后这两个字段不是一回事，
// 展示时别把 default 当成「diff 会用的基准」。
export interface BranchesResult {
  branches: string[]
  default: string
  task_base: string
}
```

**字段名用下划线 `task_base`，不要驼峰**：`fetchTaskBranches`（`web/src/api/client.ts:191`）是 `request<BranchesResult>(...)` 直接透传 JSON，全仓没有 camel 化层；`types.ts` 既有字段（`repo_path`、`work_dir`、`created_at` 等）也都与线格式同名。**不要为这一个字段新造映射。**

**7b.** `web/src/app/task/ReviewSidePanel.tsx:89` 那行替换为：

```tsx
          <option value="">自动推导{autoBaseHint(branches)}</option>
```

并在组件文件内（`ReviewSidePanel` 定义之前）加纯函数：

```tsx
// autoBaseHint 返回「自动推导」项的括注文本。
//
// 为什么要分三态（B65）：diff 的缺省基准优先用任务自己的 base_commit，
// 只有它为空才按仓库推导。若这里恒显示推导出的分支名，控制台会在有任务基线时
// 显示一个 diff 根本没用的值——一个当场可见的谎。
function autoBaseHint(branches: BranchesResult | null): string {
  if (!branches) return ''
  if (branches.task_base) return `（任务基线 ${branches.task_base.slice(0, 8)}）`
  if (branches.default) return `（${branches.default}）`
  return ''
}
```

（记得 import `BranchesResult` 类型。）

- [ ] **Step 8: 跑前端测试确认通过**

```bash
cd web && npx vitest run src/app/task/ReviewSidePanel.test.tsx && npm run typecheck && npm run lint
```

Expected: 用例全 PASS，typecheck 与 lint 零错误

- [ ] **Step 9: 加日志（本 task 的日志步骤）**

在 `handleTaskDiff` 里，`base` 确定之后、调用 `Diff` 之前补一条 Info，让「这次 diff 用的是谁」在日志里可查：

```go
	s.log.Info("diff 基准已确定", "task", taskID, "base", base,
		"from_task_base", r.URL.Query().Get("base") == "" && task.BaseCommit != "")
```

对照 instrumenting-code 的键点清单确认：
- 进入键点：`s.log.Info("diff 请求", ...)` 已有 —— 不动
- 状态/取值变更：**新增的这条**（基准取自任务还是推导）—— 这是 B65 的整个行为差异所在，不打就没法在真机上验证它生效了
- 错误分支：`无法确定基准分支` 的 Warn、`ErrBadBaseBranch` 的分支 —— 已有，不动
- 成功退出：**`handleTaskDiff` 目前没有成功日志**（`server.go:1399` 直接 `writeJSON` 就返回），
  补一条在 `writeJSON` 之前：

```go
	s.log.Info("diff 完成", "task", taskID, "base", base, "bytes", len(diff))
```

  静默的成功路径分不出「跑了且没差异」与「压根没跑到」，这正是 instrumenting-code
  点名的头号症状

- [ ] **Step 10: 加注释（本 task 的注释步骤）**

- 新文件 `diffbase_test.go` 文件头注释 —— 已含在 Step 1
- `taskOrErr` / `diffBaseFor` / `autoBaseHint` 三个 doc 注释 —— 已含在 Step 3、Step 7
- `resolveBaseBranch` 的过时注释更正 —— 已含在 Step 3c
- `BranchesResult` 的字段语义注释 —— 已含在 Step 7a

- [ ] **Step 11: 同步文档口径**

三处都要改，**改的是「默认基准是什么」这句话**，不要顺手重写整段：

- `README.md:285` 的 `handoff diff` 行：默认基准描述改为「defaults to the task's own base commit, falling back to the repo's default branch」
- `README.zh-CN.md:171` 对应行：改为「默认用任务自己的基线提交，没有才退回仓库默认分支」
- `skills/handoff/SKILL.md` 的 diff 段：同上口径，并加一句「所以默认 diff 就是这个任务的改动，不再含 base 分支与任务分支之间的历史」
- `cmd/diff.go:7` 的文件头注释与 `:41` 的 flag 说明（`"基准分支（默认按仓库默认分支推导）"`）→ 改为 `"基准（默认用任务基线提交，没有才按仓库默认分支推导）"`

- [ ] **Step 12: 全量回归**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1 && cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

Expected: 全绿

- [ ] **Step 13: Commit**

```bash
git add internal/agentd/server.go internal/agentd/workspace.go internal/agentd/diffbase_test.go cmd/diff.go web/src/api/types.ts web/src/app/task/ReviewSidePanel.tsx web/src/app/task/ReviewSidePanel.test.tsx README.md README.zh-CN.md skills/handoff/SKILL.md
git commit -m "fix(agentd,web): diff 缺省基准优先用任务自己的 base_commit（B65）

按仓库默认分支推导会把「默认分支与任务分支之间的全部历史」算进 diff——
实测一个真实任务默认吐 26611 行而真实改动只有 3274 行。任务记录里本来
就有它建在哪个提交上（B35 起），resolveBaseBranch 注释里「派发时并未
记录基准分支名」那句已经过时。

加 diffBaseFor 决定缺省基准；拆出 taskOrErr 让 diff/branches 两个端点
能读到任务本身（taskRepoOrErr 签名不变，另外三个调用点不动）。

branches 端点增 task_base 字段、前端「自动推导」括注改三态——只改 diff
的话控制台会显示一个 diff 根本没用的基准，是当场可见的谎。"
```

---

### Task 4: B66 — `run` 的多参数形态逐个 shell 转义

**Files:**
- Modify: `cmd/run.go:31`（`strings.Join` → `shellJoin`），新增 `shellQuote` / `shellJoin`
- Create: `cmd/run_test.go`
- Modify: `README.md:285`、`README.zh-CN.md:171`、`skills/handoff/SKILL.md:242-249`

**Interfaces:**
- Consumes: 无
- Produces: `func shellJoin(args []string) string` —— 包 `cmd` 内的纯函数，不导出

**背景：** `handoff run T1 grep -rn 'foo bar' .` 在远端跑成了另一个命令，且**静默**。链路穿两层 shell，只有第一层的引号被消费：本地 shell 剥掉引号 → `cmd/run.go:31` 的 `strings.Join(args[1:], " ")` 重拼 → `RunCmd`（`workspace.go:1518`）再 `sh -c` 解析一次。

**为什么必须按参数个数分档（这是本 task 最容易做错的地方）：** `args[1:]` 有两种来源——

- **单参数**：`handoff run T1 "cd sub && go test ./..."` ——用户给的就是一条 shell 命令原文，本来就指望 `sh -c` 解析它。
- **多参数**：`handoff run T1 grep -rn 'foo bar' .` ——用户给的是 argv，本地 shell 已完成分词与去引号。

**若对单参数也转义**，`sh -c "'cd sub && go test ./...'"` 会把整条命令当成一个带空格的命令名，报 command not found——**把一个今天能用的用法改坏**。

- [ ] **Step 1: 写失败测试**

新建 `cmd/run_test.go`：

```go
// run_test.go —— handoff run 的参数拼接契约（B66）：单参数按 shell 原文透传，
// 多参数逐个 POSIX 单引号转义。
//
// 边界：本文件只测拼接这一步的字符串输出，不起 agentd、不发请求——远端执行
// 语义由真机走查覆盖（归审核者，见 spec §7）。
package cmd

import "testing"

// TestShellJoin 钉住两档行为与转义规则。
//
// 单参数那一行是**回归防线**：对它也转义会把 `handoff run T1 "cd x && go test"`
// 改坏（整条命令被当成一个带空格的命令名）。
func TestShellJoin(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"单参数按原文透传", []string{"go test ./... && go vet ./..."}, "go test ./... && go vet ./..."},
		{"多参数安全字符不加引号", []string{"go", "test", "./..."}, "go test ./..."},
		{"含空格的参数加单引号", []string{"grep", "-rn", "foo bar", "."}, "grep -rn 'foo bar' ."},
		{"内嵌单引号按 '\\'' 拆开", []string{"echo", "it's"}, `echo 'it'\''s'`},
		{"元字符必须被引住", []string{"ls", "*.go"}, "ls '*.go'"},
		{"空串参数保留为空引号", []string{"echo", ""}, "echo ''"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shellJoin(c.args); got != c.want {
				t.Errorf("shellJoin(%q) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd -run TestShellJoin -count=1
```

Expected: 编译失败，`undefined: shellJoin`

- [ ] **Step 3: 实现**

在 `cmd/run.go` 的 `runCmd` 定义**之前**加：

```go
// shellSafe 是无需引号即可安全交给 sh 的字符集：字母数字与一组不参与 shell
// 解释的标点。取值与 POSIX shell 的元字符集互补——不在这里的一律引住。
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote 把一个参数转成可安全交给 sh 的形态：安全字符原样返回，
// 其余用单引号包住，内嵌单引号按 POSIX 惯例拆成 '\'' 三段。
func shellQuote(s string) string {
	if s != "" && shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellJoin 把 argv 拼成一条可安全交给远端 sh -c 的命令行。
//
// 两档行为：
//   - **只有一个参数**：原样返回。用户给的就是一条 shell 命令原文
//     （`handoff run T1 "cd sub && go test ./..."`），本来就指望 sh -c 解析它。
//     对它转义会把整条命令当成一个带空格的命令名，报 command not found——
//     这是一条**回归防线**，改动前请先读 cmd/run_test.go 的第一个用例。
//   - **多个参数**：逐个 shellQuote 后以空格连接。用户给的是 argv，本地 shell
//     已完成分词与去引号。
//
// 为什么需要它（B66）：命令要穿两层 shell。本地 shell 已消费掉一层引号，
// 直接 strings.Join 重拼后远端 sh -c 会按新的词边界再解析一次——
// `grep -rn 'foo bar' .` 到远端就成了三个参数。静默失真，不报错，
// 而审阅取证正是「读了就信」的场景。
func shellJoin(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}
```

（补 `regexp` import；`strings` 已在。）

把 `cmd/run.go:31` 的

```go
		stdout, code, err := client.New(addr, token).Run(cmd.Context(), args[0], strings.Join(args[1:], " "))
```

改成

```go
		cmdline := shellJoin(args[1:])
		stdout, code, err := client.New(addr, token).Run(cmd.Context(), args[0], cmdline)
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd -run TestShellJoin -count=1 -v
```

Expected: 六个子用例全 PASS

- [ ] **Step 5: 加日志（本 task 的日志步骤）**

`cmd/` 是 CLI 层，本仓约定这里不接 `slog`（输出即用户界面）。**但拼接结果必须对用户可见**，否则「敲的和跑的不一样」这类问题仍然只能靠猜。在 `RunE` 里、发请求之前加一行到 stderr（不污染 stdout 的输出契约）：

```go
		fmt.Fprintf(cmd.ErrOrStderr(), "远端将执行: %s\n", cmdline)
```

这是本 task 的可观测性交付：多参数被转义成什么、单参数是否原样透传，用户当场看得见。**不要改成打到 stdout**——`handoff run` 的 stdout 是命令输出原文，审阅者会直接读它。

- [ ] **Step 6: 加注释（本 task 的注释步骤）**

- 新文件 `cmd/run_test.go` 文件头注释 —— 已含在 Step 1
- `shellSafe` / `shellQuote` / `shellJoin` 三处注释 —— 已含在 Step 3
- 更新 `cmd/run.go` 的文件头注释：把「把命令原文透传给 agentd 执行」改为

```go
//   - 按参数个数分档拼接命令行（单参数=shell 原文透传，多参数=逐个转义），
//     交给 agentd 执行（sh -c，10min 超时），合并输出原文打印；
//     非零退出码以错误返回（cobra 打印到 stderr），输出已先行打印
```

- [ ] **Step 7: 同步文档契约**

**这条契约必须落进文档，不能只活在代码里**——它决定了用户该怎么敲命令：

- `skills/handoff/SKILL.md:242-249` 的 `handoff run` 段，在既有的「参数顺序有坑」之后追加：

```markdown
**`handoff run` 的参数按个数分两档**：

- **只给一个参数** = 一条 shell 命令原文，原样交给远端 `sh -c` 解析：
  `handoff run T1 "cd web && npm test"`
- **给多个参数** = argv，逐个做 shell 转义后再拼接。你敲的引号、空格、元字符
  原样到达远端：`handoff run T1 grep -rn 'foo bar' .`

B66 之前多参数形态是直接空格重拼的，`'foo bar'` 到远端会变成两个参数——静默失真，
不报错。
```

- `README.md:285` 与 `README.zh-CN.md:171` 的 `handoff run` 行，把「everything after it is passed through verbatim」/「任务名之后的一切原样透传给命令」补成两档说明（一句话即可，细节留给 SKILL.md）

- [ ] **Step 8: 全量回归**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1
```

Expected: 全绿，`gofmt -l .` 无输出

- [ ] **Step 9: Commit**

```bash
git add cmd/run.go cmd/run_test.go README.md README.zh-CN.md skills/handoff/SKILL.md
git commit -m "fix(cmd): run 的多参数形态逐个 shell 转义（B66）

命令穿两层 shell，只有第一层的引号被消费：本地 shell 剥掉引号后
strings.Join 重拼，远端 sh -c 再按新词边界解析一次，
\`grep -rn 'foo bar' .\` 就成了三个参数。静默失真，而审阅取证正是
「读了就信」的场景。

按参数个数分两档：单参数按 shell 原文透传（对它转义会把
\`handoff run T1 \"cd x && go test\"\` 改坏），多参数逐个 POSIX 单引号
转义。服务端一行未动，线格式不变。

拼接结果打到 stderr，用户当场能看见远端将执行什么。"
```

---

## 终审（全部 task 完成后做一次）

- [ ] **整分支终审**：`git diff <分支起点>..HEAD` 通读，对照 spec §8 逐条注入变异靶子，断言测试翻红：

| 靶子 | 应翻红 |
|---|---|
| `gitExec` 的 quiet 分支改回 `Error` | `TestGitProbeMissDoesNotLogError` |
| `gitExec` 的进程配额分支也受 quiet 影响 | 无自动用例 → **手工确认代码里那条分支在 quiet 之外**，记进 ledger |
| `diffBaseFor` 去掉 `BaseCommit` 优先 | `TestDiffBaseForPrefersTaskBaseCommit` |
| `handleTaskBranches` 不返回 `task_base` | `TestBranchesEndpointReportsTaskBase` + 前端三态用例 |
| `shellJoin` 对单参数也转义 | `TestShellJoin/单参数按原文透传` |
| `shellJoin` 多参数退回 `strings.Join` | `TestShellJoin/含空格的参数加单引号`、`.../内嵌单引号按 '\'' 拆开` |

**B125 没有变异靶子**——它修的是稳定性不是行为。**如实记「本条无变异靶子」，不要编一个。**

- [ ] **最终全量**：

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1 && go test -race ./internal/agentd/ ./cmd/ && cd web && npx vitest run && npm run typecheck && npm run lint && npm run build
```

- [ ] **ledger 落盘**：`docs/superpowers/ledgers/ledger-cli-contract-minor-fixes.md`，每 task 完成、每轮修复各一行，含 commit 范围。

---

## 不做清单（越界即为 plan 违规）

- **B60 的任何实现**——它已由 `1da5dd357` 修掉（`internal/client/client.go:121` 的 `isDeliverable` 已过滤 `approver_decision`）。本批不碰 `internal/client/`。
- **argv 上送的线格式变更**——`POST /api/tasks/{id}/run` 的请求体保持 `{cmdline}`，不要改成 `{argv}`。老 agentd 收不到新字段，跨版本兼容要单独设计。
- **`internal/localsync/localsync.go`**——它自己的 `run` 不打日志，没有 B81 的噪音。
- **`internal/agentd/runshell.go` 与 `RunCmd`**——shell 选择是 B37 的交付，本批服务端一行不动。
- **WS 用例的分包隔离**——B125 的根治方案，本批只做负载缓解。
- **`resolveBaseBranch` 的推导链顺序**——`origin/HEAD → main → master` 不动，B65 只是在它前面加一层优先级。

## 归审核者、不派发的验收项

以下四项要起 agentd、派任务、调 `handoff` CLI，与执行纪律块的「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」直接冲突。**执行者不要跑它们，也不要为它们写结论**：

1. B81 真机：带 `--base <远程分支名>` 的远程派发，该时间窗内 `grep 'level=ERROR' agentd.log` 为空
2. B65 真机：对 `BaseCommit` ≠ 默认分支的任务跑 `handoff diff`，行数对齐本地 `git diff <base_commit>...HEAD`
3. B66 真机：`handoff run <task> grep -rn 'foo bar' .` 与本地同命令输出一致；`handoff run <task> "cd <子目录> && ls"` 仍正常
4. B60 复验：审批链放行时 `wait --follow` 不退出；重放路径同样不因 `approver_decision` 唤醒
5. **backlog 落账**（spec §10）：B125 / B81(main 线) / B65 / B66 转 `✅ done` 并填验收栏，
   B60 按复验结果处置。**行加在 `handoff/web-console` 上，不要在 `main` 上加**——`main`
   的 B114–B119 六行至今是孤儿，已经因此发生过一次改号（`e20fedd3c`）。
   执行者不要改 `docs/superpowers/backlog.md`。
