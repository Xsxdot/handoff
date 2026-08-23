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
	logger.Debug("解析 Go module 根", "command", "go env GOMOD")
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
		decision.Cleanup = func() {
			logger.Info("保留 PTY 测试覆盖根", "source", decision.Source, "root", decision.Root)
		}
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
		removeGeneratedRoot(tmpRoot, SourceTmp, logger)
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
		removeGeneratedRoot(repoRootPath, SourceRepoDot, logger)
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
		removeGeneratedRoot(root, source, logger)
		if source == SourceRepoDot && parent != "" {
			cleanupEmptyParent(parent, logger)
		}
	}
}

func removeGeneratedRoot(root string, source Source, logger *slog.Logger) {
	if err := os.RemoveAll(root); err != nil {
		logger.Warn("清理 PTY 测试根目录失败", "source", source, "root", root, "err", err)
		return
	}
	logger.Debug("PTY 测试根目录清理完成", "source", source, "root", root)
}

func cleanupEmptyParent(parent string, logger *slog.Logger) {
	// 只移除空父目录；并发测试仍在使用时保留它，避免互删另一个用例的根。
	if err := os.Remove(parent); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Debug("保留非空 PTY 测试父目录", "parent", parent, "err", err)
		return
	}
	logger.Debug("PTY 测试父目录已清理或不存在", "parent", parent)
}
