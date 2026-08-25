# B227 实现计划：codex linked worktree 的 git 元数据可写域与 hooks 加固

> 对应 spec：`docs/superpowers/specs/2026-08-25-b227-codex-sandbox-git-metadata.md`
>；原始实验、命令与逐字输出：`docs/superpowers/ledgers/2026-08-25-b227-spec-ledger.md`。
> 目标基线分支：`claude/b227-b62e2f`。本工作树当前分支为
> `cards/B227-charter`，本节点只写计划与台账，不实现源码。

本节点法定产出物是 `docs/superpowers/plans/b227-plan.md`；后续 implement 节点只按本文件
改实现文件，不回写 spec 或本计划。

## 0. 冻结前提与范围

本卡是 L2，两个改动分别留在执行器适配内部与 agentd 工作区内部；不新增
`StartReq` 字段、HTTP/WS 字段、派发请求字段或跨子系统契约。两个改动都必须落地，
因为只开 `.git` 会把共享 hooks 变成 agentd 的宿主提权入口，只加 `core.hooksPath`
又不能解决 codex 在 linked worktree 里写 index/objects/refs 的原始故障。

只允许触及下列文件：

- `internal/executor/turn/gitprobe.go`
- `internal/executor/turn/gitprobe_test.go`
- `internal/executor/codex/adapter.go`
- `internal/executor/codex/export_test.go`
- `internal/executor/codex/discipline_test.go`
- `internal/executor/codex/adapter_sandbox_test.go`（新建）
- `internal/executor/codex/appserver_test.go`
- `internal/agentd/workspace.go`
- `internal/agentd/workspace_test.go`
- `docs/superpowers/ledgers/2026-08-25-b227-spec-ledger.md`

永不触碰：独立 clone 替代 linked worktree、可写根上的精细 allowlist、symlink/挂载
搬运元数据；也不在本卡实现 deny 权限面、`with_additional_permissions` 迁移、并行任务
公共目录互踩收窄或纪律块兜底。它们均已在 spec 的 Out of Scope/roadmap 中冻结。

### 0.1 代码图与现状核对

仓内存在 `codegraph/`，但本机没有 `codegraph` 可执行文件；原始报错是：

```text
/bin/bash: line 1: codegraph: command not found
```

因此本计划按 `codegraph/best.json` 的容器表核对归属，并保留图覆盖债：
`k_codex_fn → d_execution_adapters → d_execution`，`k_agentd_fn → d_orchestration`。
baseline.json 与源码核对出的现状签名为：

```go
func GitTurnStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error)
func sandboxPolicy(taskTmp string) map[string]any
func (a *Adapter) newRunState(taskID, taskDir, repoPath string) *runState
func gitExec(ctx context.Context, repo string, quiet bool, args ...string) (stdout, stderr string, err error)
func PrepareWorkspace(ctx context.Context, req WorkspaceReq) (Workspace, error)
```

### 0.2 已在基线亲自运行的判据

实现者在任何源码改动前重新运行下列命令；命令只覆盖本卡触及的 Go 包，不跑全仓：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/executor/turn ./internal/executor/codex \
  -run 'TestGitTurnStatusDetectsNewCommit|TestSandboxPolicyGrantsTaskTmp|TestTmpEnvPointsGoToolchainAtTaskTmp' -count=1

GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/agentd \
  -run 'TestPrepareWorkspaceNewWorktree$|TestPrepareWorkspaceNewWorktreeAllowsDirtyMainRepo$' -count=1
```

基线实得：

```text
ok  	github.com/Xsxdot/handoff/internal/executor/turn	0.019s
ok  	github.com/Xsxdot/handoff/internal/executor/codex	0.003s
ok  	github.com/Xsxdot/handoff/internal/agentd	0.148s
```

首次未指定可写模块缓存的合并命令还真实失败过，原始 `read-only file system` 输出已留在
台账 §8；实现者必须使用上面的任务临时缓存路径，不得把该环境失败写成业务回归。

### 0.3 Review 修订记录（2026-08-25）

review 发现 `TestGitCommonDirNormalizesMainAndLinkedWorktree` 直接比较
`filepath.Join(t.TempDir(), ".git")` 与 Git 返回路径，在 macOS `/var` → `/private/var`
符号链接环境下会假红。冻结口径修订为：测试先对 want 与 got 执行
`filepath.Clean` + `filepath.EvalSymlinks` 再比较，并保留主仓库与 linked worktree 的真实
Git 调用；不改 `GitCommonDir` 实现，不以 `t.Skip` 绕过平台差异。

## 1. 工作项接口与接缝清单

### T1：codex 计算并缓存 git 公共目录

允许变更：`internal/executor/turn/gitprobe.go`、`internal/executor/turn/gitprobe_test.go`、
`internal/executor/codex/adapter.go`、`internal/executor/codex/export_test.go`、
`internal/executor/codex/discipline_test.go`、`internal/executor/codex/adapter_sandbox_test.go`、
`internal/executor/codex/appserver_test.go`。

Consumes：

```go
func GitTurnStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error)
func (a *Adapter) newRunState(taskID, taskDir, repoPath string) *runState
func sandboxPolicy(taskTmp string) map[string]any
func (a *Adapter) startTurn(r *runState, text string) error
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error)
```

Produces：

```go
func GitCommonDir(repoPath string) (commonDir string, err error)
func sandboxPolicy(taskTmp, gitCommonDir string) map[string]any
func SandboxPolicyForTest(taskTmpDir, gitCommonDir string) map[string]any
```

以及 `runState.gitCommonDir string`。`gitCommonDir` 是空串时，策略不得追加该根；
`newRunState` 必须在唯一构造点调用一次 `GitCommonDir`，Start 与 Resume 继续共同经过
该构造点。`GitCommonDir` 的返回必须是绝对、Clean 的 common git directory：主仓库的
相对 `.git` 要展开，linked worktree 返回的绝对 common dir 要原样 Clean；非 git 目录
返回错误供运行态以 Debug 记录并静默跳过，不能阻断任务。

### T2：agentd git 收口覆盖 hooks

允许变更：`internal/agentd/workspace.go`、`internal/agentd/workspace_test.go`。

Consumes：

```go
func gitExec(ctx context.Context, repo string, quiet bool, args ...string) (stdout, stderr string, err error)
func PrepareWorkspace(ctx context.Context, req WorkspaceReq) (Workspace, error)
```

Produces：保持上述两个签名完全不变；唯一变化是每次实际执行的 Git argv 在子命令前
插入一个临时空目录的命令行配置：

```text
git -C <repo> -c core.hooksPath=<fresh-empty-dir> <args...>
```

该覆盖必须压过仓库 `.git/config` 中被篡改的 `core.hooksPath`；临时目录每次调用独立、
命令结束后回收。创建临时目录失败要带 `repo`、`args`、错误原因返回并记录 Error；
回收失败只记录带路径的 Warn，不覆盖 Git 原始返回值。

## 2. T1 详细步骤：先锁沙箱接缝，再接入唯一构造点

### T1.1 基线与最小范围

先跑 §0.2 的第一条命令，确认 `turn` 与 `codex` 基线为绿。T1 只跑
`./internal/executor/turn` 与 `./internal/executor/codex`；不跑 `./...`，不跑
`internal/agentd`，因为 T2 才触及 agentd。

### T1.2 写失败测试并跑红

在 `internal/executor/turn/gitprobe_test.go` 的 import 增加 `os`、`path/filepath`，再追加以下
完整测试；它复用本文件已有的
`initRepo(t) (string, string)` 夹具，入口真实执行 git，并通过仓库符号链接复现路径别名；
want 与 got 均先做 `Clean + EvalSymlinks` 归一，锁住主仓库与 linked worktree 的共同
common-dir，并锁住非 git 的错误出口：

```go
func TestGitCommonDirNormalizesMainAndLinkedWorktree(t *testing.T) {
	repo, _ := initRepo(t)
	mainPath := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(repo, mainPath); err != nil {
		t.Fatalf("建立主仓库符号链接: %v", err)
	}
	want := normalizePathForTest(t, filepath.Join(mainPath, ".git"))

	got, err := turn.GitCommonDir(mainPath)
	if err != nil {
		t.Fatalf("主仓库读取 git-common-dir: %v", err)
	}
	if got := normalizePathForTest(t, got); got != want {
		t.Fatalf("主仓库 common-dir = %q，want %q", got, want)
	}

	linked := filepath.Join(t.TempDir(), "linked")
	cmd := exec.Command("git", "-C", mainPath, "worktree", "add", "-q", "-b", "probe", linked)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("建立 linked worktree: %v\n%s", err, out)
	}

	got, err = turn.GitCommonDir(linked)
	if err != nil {
		t.Fatalf("linked worktree 读取 git-common-dir: %v", err)
	}
	if got := normalizePathForTest(t, got); got != want {
		t.Fatalf("linked worktree common-dir = %q，want %q", got, want)
	}
}

func normalizePathForTest(t *testing.T, path string) string {
	t.Helper()
	cleaned := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		t.Fatalf("归一化路径 %q: %v", path, err)
	}
	return filepath.Clean(resolved)
}

func TestGitCommonDirRejectsNonGitPath(t *testing.T) {
	if got, err := turn.GitCommonDir(t.TempDir()); err == nil || got != "" {
		t.Fatalf("非 git 目录应返回空路径与错误，got path=%q err=%v", got, err)
	}
}
```

补充同包白盒锁，在新建 `internal/executor/codex/adapter_sandbox_test.go` 写入：

```go
package codex

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewRunStateCachesGitCommonDirAndSkipsNonGit(t *testing.T) {
	repo := initGitCommonDirRepo(t)
	taskDir := t.TempDir()
	a := New(nil)

	r := a.newRunState("task-git", taskDir, repo)
	want := filepath.Clean(filepath.Join(repo, ".git"))
	if r.gitCommonDir != want {
		t.Fatalf("运行态 gitCommonDir = %q，want %q", r.gitCommonDir, want)
	}

	nonGit := t.TempDir()
	r = a.newRunState("task-non-git", t.TempDir(), nonGit)
	if r.gitCommonDir != "" {
		t.Fatalf("非 git 工作目录不得产生可写根，got %q", r.gitCommonDir)
	}
}

func initGitCommonDirRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}
```

这支测试是内部附加锁，不替代下面的声明缝锁：从 `sandboxPolicy` 声明缝本身无法
断言「唯一构造点只算一次」或「非 git 取证错误不抛出」；`newRunState` 是唯一构造点且
不导出，只能用同包白盒测试验证这两个生命周期事实。

在 `internal/executor/codex/discipline_test.go` 用下列完整函数替换现有
`TestSandboxPolicyGrantsTaskTmp`，并在 import 中增加 `encoding/json`：

```go
func TestSandboxPolicyGrantsTaskTmpAndGitCommonDir(t *testing.T) {
	const (
		taskTmp   = "/root/.handoff/tmp/137a7dc9"
		commonDir = "/srv/repos/handoff/.git"
	)
	p := codex.SandboxPolicyForTest(taskTmp, commonDir)

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("序列化 sandboxPolicy: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("反序列化 sandboxPolicy: %v", err)
	}
	roots, ok := wire["writableRoots"].([]any)
	if !ok || len(roots) != 2 || roots[0] != taskTmp || roots[1] != commonDir {
		t.Fatalf("JSON writableRoots = %#v，want [%q %q]", wire["writableRoots"], taskTmp, commonDir)
	}
	if wire["excludeSlashTmp"] != true || wire["excludeTmpdirEnvVar"] != true {
		t.Fatal("两个 exclude 必须保持 true")
	}
	if wire["networkAccess"] != true {
		t.Fatal("networkAccess 必须保持 true")
	}

	emptyRaw, err := json.Marshal(codex.SandboxPolicyForTest(taskTmp, ""))
	if err != nil {
		t.Fatalf("序列化无 common-dir 的 sandboxPolicy: %v", err)
	}
	var emptyWire map[string]any
	if err := json.Unmarshal(emptyRaw, &emptyWire); err != nil {
		t.Fatalf("反序列化无 common-dir 的 sandboxPolicy: %v", err)
	}
	emptyRoots, ok := emptyWire["writableRoots"].([]any)
	if !ok || len(emptyRoots) != 1 || emptyRoots[0] != taskTmp {
		t.Fatalf("无 common-dir 时 writableRoots = %#v，want [%q]", emptyWire["writableRoots"], taskTmp)
	}
}
```

同时在 `export_test.go` 将测试缝改成精确签名：

```go
func SandboxPolicyForTest(taskTmpDir, gitCommonDir string) map[string]any {
	return sandboxPolicy(taskTmpDir, gitCommonDir)
}
```

在 `internal/executor/codex/appserver_test.go` 增加一支真实穿过 `Client.write` 的 JSON-RPC
回归。复用该文件已有的 `startFakeServer`、`wsURL`、`quiet`、`itoa` harness；不另造
WebSocket 夹具。断言逐条写死如下（这是序列化边界的附加锁，不能由上面的 map 单测顶替）：

1. `startFakeServer` 收到一条 `turn/start` 请求，`json.Unmarshal` 成功。
2. `params.sandboxPolicy.writableRoots` 存在且为两个字符串，顺序为
   `/root/.handoff/tmp/137a7dc9`、`/srv/repos/handoff/.git`。
3. `excludeSlashTmp`、`excludeTmpdirEnvVar`、`networkAccess` 在真实 JSON 报文中仍为 `true`。
4. 服务端按请求 id 回 `{"jsonrpc":"2.0","id":<id>,"result":{}}`，客户端
   `cli.Call(context.Background(), "turn/start", map[string]any{
   "sandboxPolicy": codex.SandboxPolicyForTest(taskTmp, commonDir),
   })` 返回 nil error。
5. 任何 JSON 解码失败、字段缺失、类型不符或 Call 返回错误都使测试失败。

这支测试入口是 `Client.Call` 而非内部 map 读取，合法理由是必须验证手搭 map 到
`appserver.go:204` 的 `json.Marshal` 再到服务端消费之间没有字段丢失；从
`sandboxPolicy` 声明缝无法构造这条真实序列化断言。

此时运行 T1 红测命令：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/executor/turn ./internal/executor/codex \
  -run 'TestGitCommonDir|TestNewRunStateCachesGitCommonDirAndSkipsNonGit|TestSandboxPolicyGrantsTaskTmpAndGitCommonDir|TestSandboxPolicySurvivesClientJSONSerialization' -count=1
```

预期为编译/断言失败，因为基线没有 `GitCommonDir`、新 `sandboxPolicy` 签名、缓存字段
和新断言；不得把这次红测的具体编译文本预写成结论，执行者以实跑原文为准。

### T1.3 最小实现：取证、缓存、投影

在 `internal/executor/turn/gitprobe.go` 增加 import `path/filepath`，并在
`GitTurnStatus` 后加入下列完整函数。它只读 git，不写 hooks/config；相对输出按调用
目录展开，绝对化失败保留上下文：

```go
// GitCommonDir 返回 repoPath 所属仓库的共享 git 公共目录。
//
// 参数：repoPath 是主仓库或 linked worktree 的工作目录。
// 返回：绝对、Clean 的 common git directory；repoPath 非 git 仓库、git 不可用、
// 输出为空或路径绝对化失败时返回错误。此函数只读，不改变仓库配置。
func GitCommonDir(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", fmt.Errorf("git rev-parse --git-common-dir returned empty path")
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoPath, common)
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", fmt.Errorf("absolute git-common-dir %q: %w", common, err)
	}
	return filepath.Clean(abs), nil
}
```

在 `adapter.go` 的 `runState` 中加入字段与注释：

```go
	// gitCommonDir 是构造运行态时一次性取证的共享 git 公共目录。
	// 空串表示工作目录非 git 仓库或取证失败；sandboxPolicy 据此不追加该根。
	gitCommonDir string
```

把 `sandboxPolicy` 整段替换为：

```go
// sandboxPolicy 返回每回合显式下发的沙箱策略。
//
// taskTmp 是任务隔离的工具链临时目录；gitCommonDir 是 linked worktree 所需的
// 共享 git 公共目录。两者按固定顺序进入 writableRoots，空值不占位；其余安全姿态
// 保持历史值。真正的 gitCommonDir 只在 newRunState 中取一次并缓存，避免每回合重跑 git。
func sandboxPolicy(taskTmp, gitCommonDir string) map[string]any {
	roots := []any{}
	for _, root := range []string{taskTmp, gitCommonDir} {
		if root != "" {
			roots = append(roots, root)
		}
	}
	return map[string]any{
		"type":                "workspaceWrite",
		"networkAccess":       true,
		"excludeSlashTmp":     true,
		"excludeTmpdirEnvVar": true,
		"writableRoots":       roots,
	}
}
```

把 `newRunState` 的开头改成下面的完整形状；`repoPath == ""` 时不调用 git。成功路径
记录 task/repo/common-dir，非 git/取证失败只 Debug 并继续，符合 spec 的静默跳过：

```go
// newRunState 建一条运行态；git 公共目录在此唯一构造点取证并缓存，Start 与 Resume 共用。
func (a *Adapter) newRunState(taskID, taskDir, repoPath string) *runState {
	gitCommonDir := ""
	if repoPath != "" {
		a.log.Debug("读取 git 公共目录", "task", taskID, "repo", repoPath)
		var err error
		gitCommonDir, err = turn.GitCommonDir(repoPath)
		if err != nil {
			a.log.Debug("git 公共目录不可用，跳过追加可写根", "task", taskID,
				"repo", repoPath, "cause", err)
			gitCommonDir = ""
		} else {
			a.log.Info("git 公共目录已准备为沙箱可写根", "task", taskID,
				"repo", repoPath, "common_dir", gitCommonDir)
		}
	}
	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath, gitCommonDir: gitCommonDir,
		evCh:      make(chan executor.AdapterEvent, 64),
		permTable: newPermTable(),
		items:     newItemIndex(itemIndexCap),
		// lastProgress 必须从创建时刻起算，避免第一次 flushRender 因零值 time.Time
		// 误产一条进度事件。
		lastProgress: time.Now(),
	}
	fw, err := turn.WriterFor(taskDir, a.log)
	if err != nil {
		a.log.Warn("创建帧写入器失败，本任务无结构化帧", "task", taskID, "cause", err)
	}
	r.frames = fw
	r.seg = turn.NewSegmenter(nil)
	return r
}
```

在 `startTurn` 的唯一策略投影处把：

```go
"sandboxPolicy": sandboxPolicy(taskTmpDir(r.taskDir)),
```

替换为：

```go
"sandboxPolicy": sandboxPolicy(taskTmpDir(r.taskDir), r.gitCommonDir),
```

不要给 `executor.StartReq` 或 `executor.ResumeReq` 增加字段；恢复路径已经调用同一个
`newRunState`，因此不再另算、不漏算。

### T1.4 注释、日志与绿测

确认新函数头写职责/边界，导出 `GitCommonDir` 写参数/返回/只读注意事项，
`runState.gitCommonDir` 解释空值语义，`sandboxPolicy` 解释两根的顺序与缓存原因。
确认读取前、成功后、失败后均有结构化 slog；禁用 `print`/`fmt.Println`。

先 `gofmt -w` 仅处理 T1 文件，再运行：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/executor/turn ./internal/executor/codex
```

判绿必须是两包各输出 `ok`；随后只跑 T1 新增/既有相关测试：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/executor/turn ./internal/executor/codex \
  -run 'TestGitCommonDir|TestNewRunStateCachesGitCommonDirAndSkipsNonGit|TestSandboxPolicyGrantsTaskTmpAndGitCommonDir|TestSandboxPolicySurvivesClientJSONSerialization|TestTmpEnvPointsGoToolchainAtTaskTmp' -count=1
```

## 3. T2 详细步骤：在 gitExec 单点堵住 hooks 逃逸

### T2.1 基线与最小范围

先跑 §0.2 的第二条命令，确认 `PrepareWorkspace` 的两个已有行为为绿。T2 只跑
`./internal/agentd`；允许该包全量测试，因为 `gitExec` 是该包所有 git 调用的公共
收口，不能只跑一个调用方来证明没有破坏其他读写路径。

### T2.2 写对照测试并跑红

在 `internal/agentd/workspace_test.go` 追加下列完整用例。它先用**未覆盖**的原始
`gitT` 建 control worktree，证明同一仓库的 post-checkout hook 确实会运行；再设置
仓库 `core.hooksPath` 指向该恶意 hooks 目录，经过真实 `PrepareWorkspace`/`gitExec`
建 target worktree，证明命令行覆盖压过配置且 marker 不出现：

```go
func TestPrepareWorkspaceDoesNotRunRepositoryHook(t *testing.T) {
	repo := initTestRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("B227_HOOK_MARKER", marker)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	hook := filepath.Join(hooksDir, "post-checkout")
	script := "#!/bin/sh\nprintf 'hook-ran\\n' >> \"$B227_HOOK_MARKER\"\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatalf("写 post-checkout hook: %v", err)
	}
	gitT(t, repo, "config", "core.hooksPath", hooksDir)

	control := filepath.Join(t.TempDir(), "control")
	gitT(t, repo, "worktree", "add", "-q", "-b", "hook-control", control)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("对照组 hook 应运行并创建 marker: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("清理对照 marker: %v", err)
	}
	gitT(t, repo, "worktree", "remove", "--force", control)

	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "hook-target-0000", NewWorktree: true,
		WorktreesDir: filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("带 hooks 覆盖的 PrepareWorkspace: %v", err)
	}
	if !ws.Managed {
		t.Fatalf("target 必须是 managed worktree: %+v", ws)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target worktree 不得运行恶意 hook，stat err=%v", err)
	}
}
```

此时运行 T2 红测命令：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/agentd -run TestPrepareWorkspaceDoesNotRunRepositoryHook -count=1
```

基线下对照组应先证明 hook 可运行，target 断言应失败；执行者必须贴实际失败原文，
不能根据台账 §2 代写红测输出。

### T2.3 最小实现：gitExec 每次命令行隔离 hooks

在 `internal/agentd/workspace.go` 只替换 `gitExec` 函数体为下列完整代码；保留现有
`gitRun`/`gitProbe` 签名与 quiet 日志语义。`-c` 放在 git 子命令之前，使用绝对临时
目录；每次调用独立目录，避免并行命令共享可写 hooks 面：

```go
// gitExec 是 gitRun / gitProbe 的公共体：执行 git -C repo <args...>，并在命令行层
// 强制使用本次调用的空 hooks 目录。这样即使仓库配置被执行者改写，agentd 自己的 git
// 也不会执行仓库 hooks；临时目录在单次调用结束后回收。
func gitExec(ctx context.Context, repo string, quiet bool, args ...string) (stdout, stderr string, err error) {
	log().Info("git 调用", "repo", repo, "args", args)
	start := time.Now()
	hooksPath, err := os.MkdirTemp("", "handoff-empty-hooks-")
	if err != nil {
		log().Error("创建 git hooks 隔离目录失败", "repo", repo, "args", args, "cause", err)
		return "", "", fmt.Errorf("创建 git hooks 隔离目录: %w", err)
	}
	log().Debug("git hooks 已隔离", "repo", repo, "args", args, "hooks_path", hooksPath)
	defer func() {
		if removeErr := os.RemoveAll(hooksPath); removeErr != nil {
			log().Warn("回收 git hooks 隔离目录失败", "repo", repo, "args", args,
				"hooks_path", hooksPath, "cause", removeErr)
		}
	}()

	cmdArgs := append([]string{"-C", repo, "-c", "core.hooksPath=" + hooksPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	log().Info("git 调用完成", "repo", repo, "args", args,
		"elapsed_ms", time.Since(start).Milliseconds())
	if err != nil {
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
```

不在 `gitRun`、`gitProbe`、`PrepareWorkspace` 或每个调用点重复插入 `-c`；单点收口
是 spec 的必要实现决定。不要把 hooks 目录放进仓库或任务工作树，不要读取并修改
`.git/config`，不要用 shell 拼接命令。

### T2.4 注释、日志与绿测

确认 `gitExec` 的函数头说明命令行覆盖的安全边界、临时目录生命周期与不改变返回值的
回收失败处理；确认入口带 repo/args，外部调用前后有 logs，创建失败与 Git 失败均带
上下文，成功完成有 elapsed 日志；禁用 `print`。

先 `gofmt -w internal/agentd/workspace.go internal/agentd/workspace_test.go`，再运行：

```bash
GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/agentd -run TestPrepareWorkspaceDoesNotRunRepositoryHook -count=1

GOMODCACHE=/root/.handoff/tmp/02772ff3/gomodcache \
  go test ./internal/agentd
```

判据是：对照组 marker 存在；目标组 marker 不存在；全包输出 `ok`。全包测试必须在
T2 完成后一次跑完，不能由 T1 或任何单个内部测试代替。

## 4. 协调者执行的 Linux 真机验收（不派发）

以下步骤需要 Linux Landlock 的真实 codex 进程，macOS/seatbelt 上的策略 map 断言不能
替代。**本 task 由协调者执行，不派发给子执行者；本节点不调用 handoff CLI。**

协调者把实现提交合入有效基线后，在 Linux 执行机上按既有协调流程启动一个
`codex --new-worktree` 任务，并通过该任务的 `--extra`/计划正文要求执行者按顺序执行：

```text
git fetch --no-tags origin
git commit --allow-empty -m "b227 sandbox probe"
git rev-parse --git-common-dir
git status --short --branch
```

协调者必须从真实任务事件与最终输出取证：

1. `git fetch` 与 `git commit` 均真实返回成功，最终 HEAD 能读到该空提交；
2. 该轮没有因这两条 git 元数据写操作产生 permission ticket，也没有
   `Read-only file system`/`Operation not permitted`；
3. 任务仍运行在 linked worktree，`git rev-parse --git-common-dir` 指向主仓 `.git`；
4. 同轮的 `networkAccess=true`、`excludeSlashTmp=true`、`excludeTmpdirEnvVar=true`
   由 T1 的 JSON 断言保留，Linux 真机只补「沙箱真的放行 git 写」这一行为事实。

协调者把 Linux 命令、任务 id、事件原文、退出结果追加到台账；真机未跑到或任一条失败
时，裁决必须写「未验证」/原始错误，不能以本地 Go 测试绿代替 pass。

## 5. 验收栏：行为、缺陷族与接缝双向覆盖

### 5.1 行为验收

- 主仓库与 linked worktree 的 `git-common-dir` 都归一成同一个绝对 common dir；非 git
  工作目录不报错、不追加根、不阻断 Start/Resume。
- 每个运行态只在 `newRunState` 取证一次；Start 与 Resume 均使用缓存字段，
  `startTurn` 下发的 `writableRoots` 顺序固定为任务 tmp、common dir；原有 network 与
  两个 exclude 保持 true。
- `sandboxPolicy` 的 map 穿过 JSON-RPC `Client.write` 后字段存在、类型正确、空 common
  dir 不被编码成空占位。
- 每次 agentd git 调用均使用独立空 hooks 目录的命令行覆盖；被篡改的仓库配置不能使
  post-checkout 运行；临时目录回收失败不会篡改 git 返回值。
- Linux 真机清单按 §4 四条逐条通过；这项行为不能由 macOS 单元测试宣称通过。

### 5.2 defect-families 对抗审查

- 路径拓扑：主仓库相对 `.git`、linked worktree 绝对 common dir、非 git/空输出三形态
  均有断言；不使用 worktree 私有目录冒充 common dir。
- 配置/安全逃逸：测试先证明未覆盖时恶意 hook 会运行，再证明命令行 `-c` 压过被篡改
  `.git/config`；不靠仓库配置自觉清空 hooks。
- 序列化边界：`adapter.go#sandboxPolicy` 的手搭 map 与 `appserver.go#Client.write`
  的 `json.Marshal` 都列入文件清单，真实 WebSocket harness 逐字段断言；可空 common
  dir 与非空路径分别断言，区分字段省略与空值。
- 错误/可观测性：Git common-dir 探测失败只 Debug 并保留空根；临时 hooks 目录创建失败
  Error 返回；Git 原始 stderr 继续进入失败日志/返回；成功调用有完成耗时日志。
- 并发/清理：hooks 路径按调用 `MkdirTemp`，不共享可写目录；defer 回收；既有
  `gitRun`/`gitProbe` quiet 与 quota 分支保持原语义。
- 回归：turn/codex 定向测试、agentd 新 hooks 测试、agentd 全包测试全部通过；不得
  用计数型新增行数/文件数作为验收替代。

### 5.3 接缝覆盖（双向）

声明缝清单只有两条：

1. `internal/executor/codex/adapter.go#sandboxPolicy`：由
   `internal/executor/codex/discipline_test.go#TestSandboxPolicyGrantsTaskTmpAndGitCommonDir`
   锁住；真实 JSON 传输附加锁由 `internal/executor/codex/appserver_test.go` 的
   `Client.Call` 入口穿过 `Client.write`。
2. `internal/agentd/workspace.go#gitExec`：由
   `internal/agentd/workspace_test.go#TestPrepareWorkspaceDoesNotRunRepositoryHook`
   经 `PrepareWorkspace` → `gitRun`/`gitProbe` → `gitExec` 锁住，且测试含 raw-git 对照组。

测试 → 缝：T1 map 测试入口直接调用 `SandboxPolicyForTest`，其实现只转发到
`sandboxPolicy`；JSON 测试入口 `Client.Call` 穿过 `Client.write`；T2 入口是声明缝的
生产调用链 `PrepareWorkspace`，不是直接调用内部 helper。内部 `newRunState` 测试仅作
附加生命周期锁，理由已在 T1.2 明示，不能顶替缝级测试。

缝 → 测试：两条声明缝各至少有一支缝级断言；不得删除任一测试或把它降格成纯 helper
表驱动测试。

## 6. spec 故事归属与提交前自审

| 用户故事 | 具体归属 |
|---|---|
| 协调者要求 fetch/核对基线时 codex 不再因只读 git 元数据失败 | T1.3 的缓存/策略接入 + §4 Linux 真机 |
| codex 记台账、提交不再反复提权 | T1.2 JSON/策略断言 + §4 无 git permission ticket |
| new-worktree、自带 worktree、原地模式行为一致 | T1.3 唯一构造点 + T2 单点 hooks 收口 + §4 linked worktree 真机 |
| 恶意 hook 不被下一次 agentd 派发执行 | T2.2 对照/目标回归 + T2.3 命令行覆盖 |

提交前按顺序执行：

1. `gofmt -d` 检查 T1/T2 文件无格式差异。
2. 运行 T1 两包全量与 T2 agentd 全包测试，记录原始输出到台账。
3. 运行占位符扫描并排除扫描命令自身文本；结果必须为空。本计划不得有占位符或改变
   测试入口的未声明退路。
4. `git diff --check`；确认实现只在允许文件集内，未改 spec、contract、go.mod、git 配置。
5. 计划实现完成后，执行者在当前分支 `git add` 计划要求的实现文件与台账并 commit，
   不 push、不切分支；协调者另行完成 §4 真机收口。

占位符扫描声明：`appserver_test.go` 的序列化测试复用既有
`startFakeServer`/`wsURL`/`quiet`/`itoa` harness，因 harness 形态已固定且本计划已把
五条逐条可判 pass/fail 的断言列全，使用纪律块允许的「复用既有夹具」例外；其它测试
均给出完整代码块。
