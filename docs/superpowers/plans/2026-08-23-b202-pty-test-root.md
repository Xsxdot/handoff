# B202 实现计划：PTY 测试根目录统一择位

> Spec：`docs/superpowers/specs/2026-08-23-b202-pty-test-root.md`
>
> 卡号：B202；级别：L2；本计划只改测试辅助层，不改 PTY 产品契约与传输实现。

## 目标与冻结边界

三个真实 PTY 测试根目录统一走一份决策：

- `internal/ptyhost/client_test.go:305-316` 的 `shortRoot`；
- `internal/ptyhost/hostproc/hostproc_test.go:81-89` 的 `shortTempDir`；
- `internal/agentd/w3a_testhelpers_test.go:63-75` 的 `ptyRoot` 创建。

择位顺序固定为：

1. `HANDOFF_PTY_TEST_ROOT` 非空时只使用它作为显式覆盖根目录；
2. 未覆盖时先在字面路径 `/tmp` 下用短前缀建唯一目录；
3. `/tmp` 创建失败或实际 socket 路径超限时，在仓库根下的
   `.pty-test-root/` 中用短前缀建唯一目录；
4. 所有候选都不可写或 socket 路径超限时返回带实测字节数与上限的错误，由三个调用点
   `t.Skipf`，不把环境限制伪装成测试失败。

仓库根不从包目录的 `..` 层数推算，而由测试进程运行 `go env GOMOD` 后取其父目录。
`.pty-test-root` 的点号前缀是行为约束，不是命名偏好；真实 `go list ./...` 断言会钉住它
不会进入包枚举。

非范围项：`internal/executor/claudecode/adapter_test.go:117` 的非 PTY Unix socket
仍保持原状。它同样在包目录内使用 `os.MkdirTemp(".", "p")`，有路径长度与包目录污染
风险，但不属于 B202 的 PTY socket 根目录族；在本计划末尾登记 finding，后续另开卡处理。

## 基线取证与依赖事实

本计划写出前在当前基线真实执行过以下命令：

- `go test ./internal/ptyhost/... -count=1`：失败；`internal/ptyhost` 的 10 个 client、
  closeall、survive 用例均在 `shortRoot` 报
  `mkdir /tmp/ph-随机后缀: read-only file system`；`engine`、`hostproc`、`sessdir`、`wire`
  仍为 `ok`。
- `go test ./internal/agentd/ -run 'Test.*Pty|Test.*PTY' -count=1`：`ok`。
- `go test ./internal/agentd/ -count=1`：`ok`。
- `go build ./...`：退出 0，无输出。
- `go vet ./...`：退出 0，无输出。
- `gofmt -l internal/ cmd/ && git diff --check`：无输出。
- `go list ./...`：退出 0；当前列出 `internal/agentd`、`internal/ptyhost`、
  `internal/ptyhost/hostproc` 等普通包，不列出任何点号目录。
- 逐字复扫 `grep -rn 'MkdirTemp(".", ' --include='*_test.go' .` 得到四个文本命中：
  agentd 与 hostproc 的两个实际 PTY 调用、claudecode 的一个实际非 PTY 调用，以及
  client helper 注释中的历史示例。另以调用面确认第三个 PTY 点是
  `client_test.go:305-316` 当前的 /tmp 调用。不得再用“全仓只剩一处”的过期事实覆盖
  这次实测读数。

实现依赖的源码事实，均已在基线核对：

- `internal/ptyhost/sessdir/sessdir.go:35-38` 的实际产品守卫是 `maxSockPath = 100`，
  该值由 macOS 104、Linux 108 的 sockaddr 上限再留余量得到；`sessdir.SockPath`
  在 `:81-82` 拼成“测试根/会话 ID/sock”。
- `internal/ptyhost/sessdir/sessdir.go:90-102` 在 bind 前以 `len(path)`（字节数）检查
  socket 路径并把实测值与上限写进错误；测试根决策必须使用同一 100 字节预算，不能只
  检查根目录本身。
- `internal/ptyhost/client.go:132-134` 的真实客户端 ID 是 `uuid.NewString()`，长度
  36；`internal/ptyhost/hostproc/hostproc.go:153-160` 在监听前再次执行相同的
  `sessdir.CheckSockPath`。计划使用全 36 字节 UUID 作为测试预算占位，hostproc 的
  `s1` 会自然得到更短的实际路径。
- `internal/ptyhost/client_test.go:299-304` 已记录 `t.TempDir()` 在 macOS 上可能过长、
  包目录内临时目录会污染 `./...` 的原因；新 helper 保留并扩展这两条解释。
- `go.mod:1-3` 表明仓库根可由 `go env GOMOD` 确定，不依赖某个包目录的相对层数。

当前没有可用的 `codegraph` CLI；调用面以 `rg` 复核，结果为上述三个 PTY 使用点，另加
一个明确排除的 claudecode 使用点。

## Interfaces

### Task 1 Produces

新增 `internal/ptytestroot/root.go` 导出以下精确接口：

```go
const SocketPathLimit = 100
const SocketIDForBudget = "00000000-0000-0000-0000-000000000000"

type Source string

const (
	SourceOverride Source = "override"
	SourceTmp      Source = "tmp"
	SourceRepoDot  Source = "repo-dot"
)

type Candidate struct {
	Root   string
	Source Source
}

type Decision struct {
	Root        string
	SocketPath  string
	SocketBytes int
	SocketLimit int
	Source      Source
	Cleanup     func()
}

func Choose(candidates []Candidate, socketID string, socketLimit int) (Decision, error)
func Resolve(socketID string, socketLimit int, logger *slog.Logger) (Decision, error)
```

`Choose` 是无 IO 的纯决策函数：按传入顺序选第一个 socket 路径字节数不超过上限的
候选，所有候选失败时返回错误；它不创建目录、不删除目录、不调用 logger。
`Resolve` 是测试进程使用的 OS 包装：解析仓库根、执行候选目录创建、调用 `Choose`，并
在成功结果的 `Cleanup` 中清掉本次生成的目录。`Resolve` 的每个入口参数、候选创建前后、
错误分支和成功分支都用注入的结构化 `*slog.Logger` 记录。

### Task 1 Consumes

- 标准库 `os.MkdirTemp(dir, pattern) (string, error)`、`os.MkdirAll(path, perm) error`；
- `go env GOMOD` 输出的绝对 `go.mod` 路径；
- `filepath.Join(root, socketID, "sock")` 与 `len([]byte(path))`；
- `HANDOFF_PTY_TEST_ROOT` 环境变量；
- `internal/ptyhost/sessdir` 的 100 字节 socket 路径口径（由调用者传入
  `SocketPathLimit`，不复制产品 socket 逻辑）。

### Task 2 Produces

三个消费点均保留现有本地函数名和签名，只替换函数体：

```go
func shortRoot(t *testing.T) string
func shortTempDir(t *testing.T) string
func newTestAgentdEnvWithCfg(t *testing.T, cfg *config.Config, logger *slog.Logger) *testAgentdEnv
```

它们都调用：

```go
ptytestroot.Resolve(ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, logger)
```

成功时注册 `Decision.Cleanup`；失败时以 `t.Skipf("PTY 测试根目录不可用: %v", err)` 结束
当前测试。除 `internal/ptyhost/client_test.go` 已有的 Unix build constraint 外，不新增
平台判断；agentd 保留现有 Windows 分支，Windows 不创建 PTY 测试根。

## Task 1：新增共享择位 helper 与纯决策测试

### Files

- Create: `internal/ptytestroot/root.go`
- Create: `internal/ptytestroot/root_test.go`

### Step 1：先写失败测试，复核失败形态

先创建下面完整测试文件，再运行：

```go
// root_test.go —— PTY 测试根目录决策的纯函数与真实 go list 回归测试。
//
// 职责：覆盖候选优先级、权限失败、长度失败、覆盖根和点号目录包枚举边界。
// 边界：不启动真实 PTY，不改当前仓库的临时根，只在 t.TempDir 的探针模块中运行 go list。
package ptytestroot

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestChoosePrefersTmp(t *testing.T) {
	tmpRoot := filepath.Join(t.TempDir(), "handoff-pty-tmp")
	dotRoot := filepath.Join(t.TempDir(), ".pty-test-root", "handoff-pty-repo")
	got, err := Choose([]Candidate{
		{Root: tmpRoot, Source: SourceTmp},
		{Root: dotRoot, Source: SourceRepoDot},
	}, SocketIDForBudget, SocketPathLimit)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if got.Root != tmpRoot || got.Source != SourceTmp {
		t.Fatalf("decision = %+v，期望优先 /tmp 候选", got)
	}
	wantPath := filepath.Join(tmpRoot, SocketIDForBudget, "sock")
	if got.SocketPath != wantPath || got.SocketBytes != len([]byte(wantPath)) {
		t.Fatalf("socket = %q/%d，期望 %q/%d", got.SocketPath, got.SocketBytes, wantPath, len([]byte(wantPath)))
	}
}

func TestResolveFallsBackToRepoDotWhenTmpCannotBeCreated(t *testing.T) {
	repo := t.TempDir()
	fs := fileSystem{
		mkdirTemp: func(parent, prefix string) (string, error) {
			if parent == "/tmp" {
				return "", errors.New("read-only /tmp")
			}
			return filepath.Join(parent, prefix+"123"), nil
		},
		mkdirAll: func(string, os.FileMode) error { return nil },
	}
	got, err := resolveFrom(repo, SocketIDForBudget, SocketPathLimit, "", fs, quietLogger())
	if err != nil {
		t.Fatalf("resolveFrom: %v", err)
	}
	if got.Source != SourceRepoDot {
		t.Fatalf("source = %q，期望 repo-dot", got.Source)
	}
	if filepath.Base(filepath.Dir(got.Root)) != ".pty-test-root" {
		t.Fatalf("root = %q，必须位于点号目录下", got.Root)
	}
	if got.SocketBytes > SocketPathLimit {
		t.Fatalf("socket path = %d bytes，超过 %d", got.SocketBytes, SocketPathLimit)
	}
	got.Cleanup()
}

func TestResolveOverrideUsesOnlyConfiguredRoot(t *testing.T) {
	repo := t.TempDir()
	override := filepath.Join(repo, "override")
	fs := fileSystem{
		mkdirTemp: func(string, string) (string, error) {
			return "", errors.New("unexpected MkdirTemp")
		},
		mkdirAll: func(path string, _ os.FileMode) error {
			if path != override {
				t.Fatalf("MkdirAll path = %q，期望覆盖根 %q", path, override)
			}
			return nil
		},
	}
	got, err := resolveFrom(repo, "s1", SocketPathLimit, override, fs, quietLogger())
	if err != nil {
		t.Fatalf("resolveFrom override: %v", err)
	}
	if got.Root != override || got.Source != SourceOverride {
		t.Fatalf("decision = %+v，期望只使用覆盖根", got)
	}
}

func TestChooseRejectsSocketPathOverLimitWithMeasuredBytes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), strings.Repeat("r", SocketPathLimit+20))
	wantPath := filepath.Join(root, "s1", "sock")
	_, err := Choose([]Candidate{{Root: root, Source: SourceTmp}}, "s1", SocketPathLimit)
	if err == nil {
		t.Fatal("超长 socket 路径必须返回错误")
	}
	text := err.Error()
	for _, want := range []string{
		strconv.Itoa(len([]byte(wantPath))),
		strconv.Itoa(SocketPathLimit),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("错误 %q 缺少 %q", text, want)
		}
	}
}

func TestResolveBothCandidatesUnavailableReportsProbeLengths(t *testing.T) {
	repo := t.TempDir()
	fs := fileSystem{
		mkdirTemp: func(string, string) (string, error) {
			return "", errors.New("cannot create candidate")
		},
		mkdirAll: func(string, os.FileMode) error {
			return errors.New("repo root is read-only")
		},
	}
	_, err := resolveFrom(repo, "s1", SocketPathLimit, "", fs, quietLogger())
	if err == nil {
		t.Fatal("两处候选都不可用时必须返回 skip 错误")
	}
	text := err.Error()
	for _, want := range []string{
		strconv.Itoa(len([]byte(filepath.Join("/tmp", "s1", "sock")))),
		strconv.Itoa(len([]byte(filepath.Join(repo, ".pty-test-root", "s1", "sock")))),
		strconv.Itoa(SocketPathLimit),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("错误 %q 缺少实测字段 %q", text, want)
		}
	}
}

func TestRepoDotDirectoryIsSkippedByRealGoList(t *testing.T) {
	repo := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, "go.mod"), "module example.com/b202probe\n\ngo 1.26.1\n")
	write(filepath.Join(repo, "visible", "visible.go"), "package visible\n")
	write(filepath.Join(repo, ".pty-test-root", "hidden", "hidden.go"), "package hidden\n")

	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, out)
	}
	if strings.Contains(string(out), ".pty-test-root") {
		t.Fatalf("go list 收进了点号目录: %s", out)
	}
if !strings.Contains(string(out), "example.com/b202probe/visible") {
		t.Fatalf("go list 未列出普通包: %s", out)
	}
}


### Step 2：写最小实现

创建 internal/ptytestroot/root.go，完整内容如下：

~~~go
// Package ptytestroot 为 Unix socket PTY 测试选择短且不污染 Go 包枚举的根目录。
//
// 职责：按覆盖根、/tmp、仓库根点号目录的顺序创建测试根，并在建 socket 前检查字节长度。
// 边界：只服务测试辅助，不参与 agentd/ptyhost 生产运行时，不创建 socket、不启动进程，
// 也不把非 PTY 测试的 Unix socket 纳入本决策。
package ptytestroot

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SocketPathLimit 必须与 internal/ptyhost/sessdir 的保守 bind 上限保持一致。
// sessdir 以 macOS 104、Linux 108 为基础取 100，给 sockaddr 路径留出余量。
const SocketPathLimit = 100

// SocketIDForBudget 是 UUID 长度的占位 ID；真实 ptyhost 客户端 ID 由 uuid.NewString
// 生成，hostproc 白盒测试使用的 s1 更短。用最大长度预算避免测试根选得过长。
const SocketIDForBudget = "00000000-0000-0000-0000-000000000000"

// Source 是测试根的选择来源。
type Source string

const (
	// SourceOverride 表示使用 HANDOFF_PTY_TEST_ROOT。
	SourceOverride Source = "override"
	// SourceTmp 表示使用 /tmp 下的短临时目录。
	SourceTmp Source = "tmp"
	// SourceRepoDot 表示使用仓库根下 .pty-test-root 的短临时目录。
	SourceRepoDot Source = "repo-dot"
)

// Candidate 是已经可创建、待做 socket 长度判断的候选根。
type Candidate struct {
	Root   string
	Source Source
}

// Decision 是一次成功的测试根决策。
//
// Root 是调用方传给 ptyhost 的会话根；SocketPath/SocketBytes 是用 socketID 预算出的
// 实测值；Cleanup 只清理 Resolve 本次生成的临时目录，调用方必须注册它。
type Decision struct {
	Root        string
	SocketPath  string
	SocketBytes int
	SocketLimit int
	Source      Source
	Cleanup     func()
}

// Choose 按 candidates 顺序选择第一个 socket 路径不超过 socketLimit 的根。
//
// 参数：socketID 是会话 ID 或等长预算占位；socketLimit 是字节上限。
// 返回：成功时返回根、完整 socket 路径和字节数；全部候选失败时返回含每个实测值的错误。
// 注意：Choose 是纯函数，不做文件系统访问，也不负责 Cleanup。
func Choose(candidates []Candidate, socketID string, socketLimit int) (Decision, error) {
	if socketLimit <= 0 {
		return Decision{}, fmt.Errorf("PTY socket 路径上限必须为正数：%d", socketLimit)
	}
	attempts := make([]attempt, 0, len(candidates))
	for _, candidate := range candidates {
		path := socketPath(candidate.Root, socketID)
		bytes := len([]byte(path))
		attempt := attempt{
			source:      candidate.Source,
			root:        candidate.Root,
			socketPath:  path,
			socketBytes: bytes,
			socketLimit: socketLimit,
		}
		if bytes <= socketLimit {
			return Decision{
				Root:        candidate.Root,
				SocketPath:  path,
				SocketBytes: bytes,
				SocketLimit: socketLimit,
				Source:      candidate.Source,
			}, nil
		}
		attempts = append(attempts, attempt)
	}
	return Decision{}, &unavailableError{attempts: attempts}
}

// Resolve 在真实测试进程中执行根目录择位。
//
// 参数：socketID/socketLimit 定义长度预算；logger 接收入口、候选 IO、错误和成功节点，
// 传 nil 时使用 slog.Default。
// 返回：成功时返回可直接交给 PTY Host 的根和清理函数；失败时返回可供 t.Skipf 展示的
// 环境错误，错误文本列出每个候选的实测 socket 字节数与上限。
// 注意：覆盖根是调用方指定的目录，Resolve 不会在 Cleanup 中删除它。
func Resolve(socketID string, socketLimit int, logger *slog.Logger) (Decision, error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("解析 PTY 测试根目录", "socket_id", socketID, "socket_limit", socketLimit,
		"override_set", os.Getenv("HANDOFF_PTY_TEST_ROOT") != "")

	cmd := exec.Command("go", "env", "GOMOD")
	gomod, err := cmd.Output()
	if err != nil {
		logger.Error("解析 Go module 根失败", "command", "go env GOMOD", "err", err)
		return Decision{}, fmt.Errorf("解析 Go module 根（go env GOMOD）失败: %w", err)
	}
	modPath := strings.TrimSpace(string(gomod))
	if modPath == "" {
		logger.Error("go env GOMOD 返回空路径")
		return Decision{}, errors.New("解析 Go module 根失败: go env GOMOD 返回空路径")
	}
	repoRoot := filepath.Dir(filepath.Clean(modPath))
	logger.Debug("Go module 根已解析", "gomod", modPath, "repo_root", repoRoot)
	return resolveFrom(repoRoot, socketID, socketLimit, os.Getenv("HANDOFF_PTY_TEST_ROOT"),
		fileSystem{
			mkdirTemp: os.MkdirTemp,
			mkdirAll:  os.MkdirAll,
		}, logger)
}

type fileSystem struct {
	mkdirTemp func(string, string) (string, error)
	mkdirAll  func(string, os.FileMode) error
}

type attempt struct {
	source      Source
	root        string
	socketPath  string
	socketBytes int
	socketLimit int
	err         error
}

type unavailableError struct {
	attempts []attempt
}

func (e *unavailableError) Error() string {
	var b strings.Builder
	b.WriteString("PTY 测试根目录不可用")
	for _, a := range e.attempts {
		fmt.Fprintf(&b, "；source=%s root=%q 实测 socket=%d 字节，上限=%d",
			a.source, a.root, a.socketBytes, a.socketLimit)
		if a.err != nil {
			fmt.Fprintf(&b, "，原因=%v", a.err)
		}
	}
	return b.String()
}

func resolveFrom(repoRoot, socketID string, socketLimit int, override string, fs fileSystem,
	logger *slog.Logger) (Decision, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if override != "" {
		logger.Info("尝试 HANDOFF_PTY_TEST_ROOT 覆盖根", "root", override)
		probe := makeAttempt(SourceOverride, override, socketID, socketLimit, nil)
		if err := fs.mkdirAll(override, 0o700); err != nil {
			probe.err = fmt.Errorf("创建覆盖根 %s: %w", override, err)
			logger.Warn("PTY 测试覆盖根不可写", "root", override, "socket_bytes", probe.socketBytes,
				"socket_limit", socketLimit, "err", probe.err)
			return Decision{}, &unavailableError{attempts: []attempt{probe}}
		}
		decision, err := Choose([]Candidate{{Root: override, Source: SourceOverride}}, socketID, socketLimit)
		if err != nil {
			logger.Warn("PTY 测试覆盖根路径超限", "root", override, "err", err)
			return Decision{}, err
		}
		decision.Cleanup = func() {}
		logger.Info("PTY 测试根目录已选择", "source", decision.Source, "root", decision.Root,
			"socket", decision.SocketPath, "socket_bytes", decision.SocketBytes)
		return decision, nil
	}

	attempts := make([]attempt, 0, 2)
	logger.Debug("尝试 /tmp PTY 测试根", "parent", "/tmp")
	tmpRoot, err := fs.mkdirTemp("/tmp", "handoff-pty-")
	if err != nil {
		probe := makeAttempt(SourceTmp, "/tmp", socketID, socketLimit, err)
		attempts = append(attempts, probe)
		logger.Warn("/tmp PTY 测试根不可写", "parent", "/tmp", "socket_bytes", probe.socketBytes,
			"socket_limit", socketLimit, "err", err)
	} else {
		decision, chooseErr := Choose([]Candidate{{Root: tmpRoot, Source: SourceTmp}}, socketID, socketLimit)
		if chooseErr == nil {
			decision.Cleanup = generatedCleanup(tmpRoot, "", SourceTmp, logger)
			logger.Info("PTY 测试根目录已选择", "source", decision.Source, "root", decision.Root,
				"socket", decision.SocketPath, "socket_bytes", decision.SocketBytes)
			return decision, nil
		}
		attempts = appendUnavailable(attempts, chooseErr)
		logger.Warn("/tmp PTY 测试根 socket 路径超限", "root", tmpRoot, "err", chooseErr)
		_ = os.RemoveAll(tmpRoot)
	}

	parent := filepath.Join(repoRoot, ".pty-test-root")
	logger.Debug("尝试仓库点号 PTY 测试根", "parent", parent)
	if err := fs.mkdirAll(parent, 0o700); err != nil {
		probe := makeAttempt(SourceRepoDot, parent, socketID, socketLimit, err)
		attempts = append(attempts, probe)
		logger.Warn("仓库点号 PTY 测试父目录不可写", "parent", parent,
			"socket_bytes", probe.socketBytes, "socket_limit", socketLimit, "err", err)
		cleanupEmptyParent(parent, logger)
		return Decision{}, &unavailableError{attempts: attempts}
	}
	repoRootPath, err := fs.mkdirTemp(parent, "handoff-pty-")
	if err != nil {
		probe := makeAttempt(SourceRepoDot, parent, socketID, socketLimit, err)
		attempts = append(attempts, probe)
		logger.Warn("仓库点号 PTY 测试根不可写", "parent", parent,
			"socket_bytes", probe.socketBytes, "socket_limit", socketLimit, "err", err)
		cleanupEmptyParent(parent, logger)
		return Decision{}, &unavailableError{attempts: attempts}
	}
	decision, chooseErr := Choose([]Candidate{{Root: repoRootPath, Source: SourceRepoDot}}, socketID, socketLimit)
	if chooseErr != nil {
		attempts = appendUnavailable(attempts, chooseErr)
		logger.Warn("仓库点号 PTY 测试根 socket 路径超限", "root", repoRootPath, "err", chooseErr)
		_ = os.RemoveAll(repoRootPath)
		cleanupEmptyParent(parent, logger)
		return Decision{}, &unavailableError{attempts: attempts}
	}
	decision.Cleanup = generatedCleanup(repoRootPath, parent, SourceRepoDot, logger)
	logger.Info("PTY 测试根目录已选择", "source", decision.Source, "root", decision.Root,
		"socket", decision.SocketPath, "socket_bytes", decision.SocketBytes)
	return decision, nil
}

func makeAttempt(source Source, root, socketID string, socketLimit int, err error) attempt {
	path := socketPath(root, socketID)
	return attempt{
		source:      source,
		root:        root,
		socketPath:  path,
		socketBytes: len([]byte(path)),
		socketLimit: socketLimit,
		err:         err,
	}
}

func appendUnavailable(dst []attempt, err error) []attempt {
	var unavailable *unavailableError
	if errors.As(err, &unavailable) {
		return append(dst, unavailable.attempts...)
	}
	return dst
}

func socketPath(root, socketID string) string {
	return filepath.Join(root, socketID, "sock")
}

func generatedCleanup(root, parent string, source Source, logger *slog.Logger) func() {
	return func() {
		logger.Info("清理 PTY 测试根目录", "source", source, "root", root)
		if err := os.RemoveAll(root); err != nil {
			logger.Warn("清理 PTY 测试根目录失败", "root", root, "err", err)
		}
		if source == SourceRepoDot && parent != "" {
			cleanupEmptyParent(parent, logger)
		}
	}
}

func cleanupEmptyParent(parent string, logger *slog.Logger) {
	// 只移除空父目录；并发测试仍在使用时保留它，避免互删另一个用例的根。
	if err := os.Remove(parent); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Debug("保留非空 PTY 测试父目录", "parent", parent, "err", err)
	}
}
~~~

为保持每步 2～5 分钟，按以下三个动作应用上面的完整文件代码，每个动作后立即执行
指定命令：

- [ ] 2a：先加入常量、Source/Candidate/Decision、纯函数 Choose 与其注释；运行
  `gofmt -w internal/ptytestroot/root.go` 和
  `go test ./internal/ptytestroot/ -count=1`，预期只剩 Resolve 尚未接入导致的编译错误。
- [ ] 2b：加入 fileSystem、attempt/unavailableError 与 Resolve 的 go env GOMOD 入口；
  再运行同一条 helper 测试命令，预期权限/长度/覆盖根用例通过。
- [ ] 2c：加入 resolveFrom、清理函数与结构化日志；运行 helper 全包测试，预期六个用例
  全部 PASS，且测试目录清理不污染工作树。

实现说明：

- Resolve 的 /tmp 候选使用硬编码 /tmp，不调用 os.TempDir；这是为了不把
  TMPDIR 或 /var/folders/... 的长前缀重新带入 Unix socket 路径。
- /tmp 创建失败时的 attempt 以 /tmp/会话ID/sock 作为可审计 probe，仓库回落目录
  创建失败时以“仓库根/.pty-test-root/会话ID/sock”作为 probe；因此即便没有实际临时目录，
  skip 文案仍总有实测字节数与上限。
- 生成的仓库根临时目录清理后只尝试 os.Remove(parent)，不会递归删除共享父目录；这是
  并发测试之间的边界。覆盖根由配置者拥有，Cleanup 不删除它。
- Choose 和 Resolve 不使用 JSON、YAML、手搭 map 或跨语言 DTO；新增字段只存在于进程
  内的 Decision，无序列化边界。

### Step 3：跑 helper 测试变绿

运行：

~~~text
gofmt -w internal/ptytestroot/root.go internal/ptytestroot/root_test.go
go test ./internal/ptytestroot/ -count=1
~~~

预期：ok github.com/Xsxdot/handoff/internal/ptytestroot；六个断言分别覆盖 /tmp
优先、repo-dot 回落、覆盖根、超长路径、双候选不可用的 skip 文案，以及真实 go list
跳过点号目录。

### Task 1 测试范围声明

本 task 只运行 go test ./internal/ptytestroot/ -count=1。它不运行 PTY 真机测试，也不
运行全量 go test ./...；后者属于协调者在所有实现卡合并后的全局闸门。
+

## Task 2：三个 PTY 测试消费点接入同一决策

### Files

- Modify: internal/ptyhost/client_test.go，仅更新 import 与 shortRoot。
- Modify: internal/ptyhost/hostproc/hostproc_test.go，仅更新 import 与 shortTempDir。
- Modify: internal/agentd/w3a_testhelpers_test.go，仅更新 import 与 Unix PTY 根创建段。
- Do not modify: internal/executor/claudecode/adapter_test.go。

### Step 1：写接入失败测试并先跑红

接入前的基线失败命令已经真实跑过：

~~~text
go test ./internal/ptyhost/... -count=1
~~~

原始失败是 internal/ptyhost/client_test.go:310: mkdir /tmp/ph-随机后缀: read-only file
system，而 internal/ptyhost/hostproc 本身因仍使用包目录暂时为绿。实现后必须反转为
全包绿，并以隐藏目录断言替代“包目录没有某个前缀”的脆弱检查。

### Step 2：最小接入实现

internal/ptyhost/client_test.go 删除 runtime import，加入
github.com/Xsxdot/handoff/internal/ptytestroot，把 shortRoot 整段替换为：

~~~go
// shortRoot 造一个既短又不在包目录内的会话根目录。
//
// 具体择位集中在 ptytestroot：/tmp 适合 Unix socket，codex 沙箱不可写时退到仓库根的
// 点号目录；两处都不可用或完整 socket 路径超限时显式 skip，避免一族底层 bind 假红。
func shortRoot(t *testing.T) string {
	t.Helper()
	decision, err := ptytestroot.Resolve(
		ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, testLog())
	if err != nil {
		t.Skipf("PTY 测试根目录不可用: %v", err)
		return ""
	}
	t.Cleanup(decision.Cleanup)
	return decision.Root
}
~~~

internal/ptyhost/hostproc/hostproc_test.go 加入 log/slog 与
github.com/Xsxdot/handoff/internal/ptytestroot import（已有 io），把 shortTempDir 整段替换为：

~~~go
// shortTempDir 给 hostproc 的 Unix socket 留出路径空间，并复用 ptyhost 客户端的唯一
// 根目录决策。hostproc 用 s1 做白盒会话 ID，但仍按 UUID 最大长度预留空间。
func shortTempDir(t *testing.T) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	decision, err := ptytestroot.Resolve(
		ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, logger)
	if err != nil {
		t.Skipf("PTY 测试根目录不可用: %v", err)
		return ""
	}
	t.Cleanup(decision.Cleanup)
	return decision.Root
}
~~~

internal/agentd/w3a_testhelpers_test.go 加入
github.com/Xsxdot/handoff/internal/ptytestroot import，保留
runtime.GOOS == "windows" 分支，把当前 os.MkdirTemp(".", "at-pty-") 段替换为：

~~~go
	if runtime.GOOS != "windows" {
		decision, err := ptytestroot.Resolve(
			ptytestroot.SocketIDForBudget, ptytestroot.SocketPathLimit, logger)
		if err != nil {
			t.Skipf("PTY 测试根目录不可用: %v", err)
			return nil
		}
		ptyRoot := decision.Root
		srv.ptyRootPath = ptyRoot
		srv.pty = ptyhost.New(ptyRoot, testHandoffExecutable(t), logger)
		t.Cleanup(func() {
			for _, sess := range srv.pty.List() {
				if err := srv.pty.Close(sess.ID); err != nil {
					logger.Warn("清理 PTY 测试会话失败", "session", sess.ID, "err", err)
				}
			}
			decision.Cleanup()
		})
	}
~~~

这段保留 agentd 原有 ptyhost.New 与会话关闭动作，只把根目录来源改为共享 helper；
每条清理错误有结构化上下文，成功 cleanup 由 helper 记录 Info。os import 仍被
testHandoffExecutable 的 MkdirTemp、Chmod 等代码使用，不删除。hostproc 的 io import
仍被既有 EOF 判定使用，不新增 print。

三个文件的 import 结果必须逐字达到下面形态，避免留下已删除的 runtime 或漏掉共享
helper：

~~~go
// internal/ptyhost/client_test.go
import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
	"github.com/Xsxdot/handoff/internal/ptytestroot"
)

// internal/ptyhost/hostproc/hostproc_test.go
import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost/hostproc"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
	"github.com/Xsxdot/handoff/internal/ptyhost/wire"
	"github.com/Xsxdot/handoff/internal/ptytestroot"
)

// internal/agentd/w3a_testhelpers_test.go
import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptytestroot"
	"github.com/Xsxdot/handoff/internal/store"
)
~~~

为保持每步 2～5 分钟，按三个消费点分别接入并即时验证：

- [ ] 2a：先改 client_test.go 的 import 与 shortRoot，运行
  `gofmt -w internal/ptyhost/client_test.go` 和
  `go test ./internal/ptyhost/... -run 'TestClientOpenList|TestClientOpenTimeoutCleansDirectory' -count=1`；
  预期旧 `/tmp` 假红消失，若 helper 尚未完成则保留可定位的编译错误。
- [ ] 2b：再改 hostproc_test.go 的 import 与 shortTempDir，运行
  `gofmt -w internal/ptyhost/hostproc/hostproc_test.go` 和
  `go test ./internal/ptyhost/hostproc/ -count=1`；预期 hostproc 全包绿。
- [ ] 2c：最后改 w3a_testhelpers_test.go 的 PTY 环境段，运行
  `gofmt -w internal/agentd/w3a_testhelpers_test.go` 和
  `go test ./internal/agentd/ -run 'Test.*Pty|Test.*PTY' -count=1`；预期 agentd PTY
  用例全绿，Windows 分支仍只编译不创建 Unix 根。

### Step 3：跑定向 PTY 测试并检查污染

按顺序运行以下命令，逐条保留真实输出：

~~~text
gofmt -w internal/ptyhost/client_test.go internal/ptyhost/hostproc/hostproc_test.go internal/agentd/w3a_testhelpers_test.go
go test ./internal/ptytestroot/ -count=1
go test ./internal/ptyhost/... -count=1
go test ./internal/ptyhost/... -run 'TestClient|TestCloseAll|TestSurvive' -count=3
go test ./internal/agentd/ -run 'Test.*Pty|Test.*PTY' -count=1
go test ./internal/agentd/ -count=1
~~~

验收：

- helper 包、三个 PTY 包级测试与三轮重复测试全部 exit 0；在当前 codex 沙箱里日志应
  显示 /tmp 创建失败后 source=repo-dot，而不是 mkdir /tmp 红掉用例。
- git status --porcelain internal/ptyhost internal/agentd 无输出；测试结束后仓库根
  下不留下 .pty-test-root（并发清理允许父目录短暂存在，但本批测试结束必须为空并被
  os.Remove 清掉）。
- rg -n 'MkdirTemp\("\."' internal/ptyhost internal/agentd --glob '*_test.go'
  无输出；rg -n 'MkdirTemp\("/tmp"' internal/ptyhost internal/agentd --glob '*_test.go'
  无输出。internal/executor/claudecode/adapter_test.go:117 不在这两条扫描范围内，
  且 diff 不得出现该文件。
- helper 测试的真实 go list 子进程证明 .pty-test-root 不进 ./...；PTY 真机
  测试证明三处消费方都能在同一选择策略下启动/收尾。

### Step 4：跑包级静态闸门

~~~text
go build ./...
go vet ./...
gofmt -l internal/ cmd/
git diff --check
git status --short
~~~

预期：build/vet 退出 0；gofmt -l、git diff --check 无输出；git status 只显示
计划实现本身预期的源码与测试文件，不显示测试临时目录。不要在本 task 内运行全量
go test ./...；由协调者在所有卡集成后运行。

### Task 2 测试范围声明

本 task 只运行 internal/ptytestroot、internal/ptyhost/...、internal/agentd/ 与
go build/vet 静态闸门。未改动的 internal/executor/claudecode 不纳入测试范围，也不
以 PTY 卡名义扩大到其它 Unix socket 测试。

## 缺陷族对抗审查与验收结论

实现完成后逐族回答并把结果写入执行 ledger：

1. **环境权限族**：/tmp 只读是否转为 repo-dot；repo-dot 只读是否带 probe 字节数、
   上限和原始错误的 skip；覆盖根失败是否不悄悄退回默认值。答案必须由
   TestResolveFallsBackToRepoDotWhenTmpCannotBeCreated、双失败测试和真实 PTY 沙箱输出
   共同证明。
2. **边界长度族**：长度以完整“测试根/UUID/sock”的 UTF-8 字节计算，<=100 通过、
   >100 skip；长路径测试同时断言错误中的实测数与 100。不能只测 ASCII 根目录长度。
3. **并发/清理族**：每次默认根由 MkdirTemp 唯一化；repo-dot 父目录共享但只做空目录
   os.Remove，不会递归删除另一测试的根；三个 PTY 包重复测试和最终 git status 证明
   无包目录临时物残留。
4. **平台族**：Unix 三处真实 socket 测试走 helper；agentd Windows 分支不创建 PTY
   根；go build ./... 与 go vet ./... 在当前 Linux 基线通过，Windows 行为不因新增
   Unix 路径 API 被导入产品运行时而改变。
5. **可观测性族**：入口参数、go env GOMOD 前后、每个候选 IO 错误、长度失败、成功
   选择、cleanup 及 agentd 会话 cleanup 错误都有 slog 上下文；调用点只把不可用环境
   Skipf，不静默吞错。
6. **范围污染族**：PTY 三个旧 MkdirTemp(".") 消失；claudecode 非 PTY 点保持未改，
   finding 明确转后续卡；全仓复扫结果写 ledger，防止“只改触发红的包”漏点。

## Spec 用户故事归属

- 故事 1（macOS 全量绿、git status 干净）：Task 2 的三个真实消费点、重复 PTY 测试、
  包目录与仓库根清理验收。
- 故事 2（codex 沙箱 /tmp 不可写时绿或可读 skip）：Task 1 的权限失败测试与 Task 2
  的真实 /tmp→repo-dot 回落测试。
- 故事 3（深 worktree 超 socket 上限时显示实测 N/M）：Task 1 的超长路径与双失败
  文案断言、Task 2 的 t.Skipf 消费行为。
- 故事 4（后续只改一处决策）：Task 1 的 internal/ptytestroot 唯一实现，以及 Task 2
  三个调用点只消费 Resolve，不复制择位规则。

## 序列化边界审计

本卡新增的 Decision、Candidate、Attempt 都是测试进程内的 Go 值，不写 JSON/YAML，
不经过 CLI 输出、HTTP、WebSocket 或跨语言边界；因此没有新增手写序列化/投影文件，也不
需要 roundtrip 测试。唯一外部文本边界是 go env GOMOD 与 go list ./... 子进程：
前者由 Resolve 检查非空并记录错误，后者由 TestRepoDotDirectoryIsSkippedByRealGoList
断言隐藏目录不入包枚举。SocketIDForBudget 不是可空业务字段，不存在“缺失”和零值
混淆。

## 类型标注：边界行为清单

实现者和审核者必须逐条看到实际结果：

| 场景 | 真实输入 | 必须观察到的行为 |
|---|---|---|
| 默认优先 | 无覆盖；/tmp 可创建 | SourceTmp，完整 socket 字节数 <=100 |
| 沙箱回落 | 无覆盖；MkdirTemp("/tmp", ...) 返回只读错误 | SourceRepoDot，根在 .pty-test-root，PTY 测试绿 |
| 覆盖根 | HANDOFF_PTY_TEST_ROOT=/path | 只使用该根；不试 /tmp，路径仍需 <=100 |
| 超长根 | 任一候选完整 socket 路径 >100 | 不调用 bind，由 helper 错误并 t.Skipf，文案含实测字节与 100 |
| 两处不可用 | /tmp 和 repo-dot 均创建失败 | 显式 skip，文案列两个 probe 字节数、100 与失败原因 |
| 并发清理 | 多测试共享 .pty-test-root | 各自唯一子根；只删除空父目录，不删邻居 |
| Windows | runtime.GOOS == "windows" 的 agentd 测试 | 不初始化 Unix PTY 根；新增 helper 不改变编译 |
| 非 PTY socket | claudecode adapter_test.go:117 | 文件不变；finding 记录风险，不纳入 B202 验收 |

## 占位符扫描自声明

本计划没有待补项标记、未定义的错误分支、跨任务代称或只描述动作不提供代码的骨架。
两个测试文件与 helper 实现均给出完整代码；既有消费方只按精确函数签名替换，
不依赖未知夹具形态。计划中的“协调者执行”仅指全量 go test ./... 与跨卡审计，不派发
给实现者。

## Commit

实现者完成 Task 1/2 后提交中文提交信息：

~~~text
test(pty): 统一 PTY 测试 socket 根目录择位

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
~~~

本计划节点本身只落盘本文件；实现提交与全量闸门由后续 implement/协调者节点完成。
