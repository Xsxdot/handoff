// coordinator_home.go —— 协调者无头回合与 attach 所需隔离 HOME 的供给边界。
//
// 职责：为 coordinatorRunner 的 Launch/Resume 和冷 attach 引用提供隔离 HOME
// 供给与解析端口；不被 WakeHome 调用，也不拥有执行者 HOME 的同步策略。
// 边界：只写计划规定的协调者白名单路径，不管理 CLI session db。
package agentd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type coordinatorHomeSupplier struct {
	currentConfig  func() *config.Config
	userHomeDir    func() (string, error)
	expandHomeDir  func(string) (string, error)
	credentialPath func(string) (string, bool)
}

// Prepare materializes the coordinator allowlist into spec.HomeDir and returns its
// absolute path. It never removes existing HOME content, because session databases
// and user-owned files are outside this coordinator supply boundary.
func (p coordinatorHomeSupplier) Prepare(spec keysclient.SessionSpec) (target string, resultErr error) {
	defer func() {
		if resultErr != nil {
			slog.Default().Error("协调者隔离 HOME 供给失败", "cli", spec.CLI,
				"home_dir", spec.HomeDir, "cause", resultErr)
		}
	}()
	slog.Default().Info("协调者隔离 HOME 供给开始", "cli", spec.CLI, "home_dir", spec.HomeDir)
	if strings.TrimSpace(spec.CLI) == "" {
		return "", errors.New("协调者供给缺少 CLI")
	}
	if strings.TrimSpace(spec.HomeDir) == "" {
		return "", errors.New("协调者供给缺少 HomeDir")
	}
	expand := p.expandHomeDir
	if expand == nil {
		expand = hostapi.ExpandHomePath
	}
	targetHome, err := expand(spec.HomeDir)
	if err != nil {
		return "", fmt.Errorf("展开协调者供给 HOME %q: %w", spec.HomeDir, err)
	}
	if !filepath.IsAbs(targetHome) {
		return "", fmt.Errorf("协调者供给 HOME 未展开为绝对路径: %q", targetHome)
	}
	if err := rejectCoordinatorSymlinkPath(targetHome); err != nil {
		return "", err
	}
	if p.userHomeDir == nil {
		return "", errors.New("协调者供给缺少主 HOME 读取函数")
	}
	mainHome, err := p.userHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取主 HOME: %w", err)
	}
	if p.currentConfig == nil {
		return "", errors.New("协调者供给缺少活配置读取函数")
	}
	cfg := p.currentConfig()
	if cfg == nil {
		return "", errors.New("协调者供给缺少 agentd 活配置")
	}
	if err := os.MkdirAll(targetHome, 0o700); err != nil {
		return "", fmt.Errorf("创建协调者隔离 HOME %q: %w", targetHome, err)
	}
	projected, err := projectCoordinatorConfig(cfg)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(targetHome, ".handoff", "config.yaml")
	slog.Default().Info("写协调者隔离配置", "cli", spec.CLI, "target", configPath)
	if err := config.Save(configPath, &projected); err != nil {
		return "", fmt.Errorf("写协调者隔离配置 %q: %w", configPath, err)
	}
	slog.Default().Info("协调者隔离配置已写入", "cli", spec.CLI, "target", configPath)
	if err := copyMissingCoordinatorCredential(mainHome, targetHome, spec.CLI, p.credentialPath); err != nil {
		return "", err
	}
	if err := copyCoordinatorRules(mainHome, targetHome); err != nil {
		return "", err
	}
	slog.Default().Info("协调者隔离 HOME 供给完成", "cli", spec.CLI, "target", targetHome)
	return targetHome, nil
}

// projectCoordinatorConfig copies the live agentd snapshot and makes paths usable
// from the isolated HOME. Relative SQLite DSNs are files, while PostgreSQL URLs
// are connection strings and must remain unchanged.
func projectCoordinatorConfig(cfg *config.Config) (config.Config, error) {
	if cfg == nil {
		return config.Config{}, errors.New("agentd 活配置为空")
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return config.Config{}, errors.New("agentd DataDir 为空")
	}
	projected := *cfg
	var err error
	if projected.DataDir, err = filepath.Abs(projected.DataDir); err != nil {
		return config.Config{}, fmt.Errorf("解析 agentd DataDir %q: %w", cfg.DataDir, err)
	}
	if projected.RepoRoot != "" {
		if projected.RepoRoot, err = filepath.Abs(projected.RepoRoot); err != nil {
			return config.Config{}, fmt.Errorf("解析 agentd RepoRoot %q: %w", cfg.RepoRoot, err)
		}
	}
	if dsn := projected.Ledger.DSN; dsn != "" &&
		!strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		if projected.Ledger.DSN, err = filepath.Abs(dsn); err != nil {
			return config.Config{}, fmt.Errorf("解析 SQLite ledger DSN %q: %w", dsn, err)
		}
	}
	return projected, nil
}

// copyMissingCoordinatorCredential supplies only the table-selected credential
// when absent. It deliberately does not synchronize the surrounding opencode data
// tree, so session databases remain untouched.
func copyMissingCoordinatorCredential(mainHome, targetHome, cli string,
	credentialPath func(string) (string, bool)) error {
	if credentialPath == nil {
		return nil
	}
	rel, ok := credentialPath(filepath.Base(cli))
	if !ok || rel == "" || filepath.IsAbs(rel) || !safeCoordinatorRelativePath(rel) {
		return nil
	}
	source := filepath.Join(mainHome, rel)
	if err := rejectCoordinatorSymlinkPath(source); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		slog.Default().Warn("协调者主 HOME 缺少表内凭据，跳过供给", "cli", cli, "source", source)
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat 主 HOME 凭据 %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("主 HOME 凭据不是普通文件 %q", source)
	}
	destination := filepath.Join(targetHome, rel)
	if err := rejectCoordinatorSymlinkPath(destination); err != nil {
		return err
	}
	if existing, statErr := os.Lstat(destination); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return fmt.Errorf("隔离凭据目标不是普通文件 %q", destination)
		}
		slog.Default().Info("协调者隔离凭据已存在，保留原文件", "cli", cli, "target", destination,
			"mode", existing.Mode().String())
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查隔离凭据 %q: %w", destination, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("创建隔离凭据父目录 %q: %w", filepath.Dir(destination), err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("读取主 HOME 凭据 %q: %w", source, err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("写隔离凭据 %q: %w", destination, err)
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("设置隔离凭据权限 %q: %w", destination, err)
	}
	slog.Default().Info("协调者缺失凭据已供给", "cli", cli, "target", destination)
	return nil
}

// copyCoordinatorRules copies only the two coordinator rule paths. Keeping this
// allowlist separate from credential copying prevents a broad HOME sync from
// accidentally copying opencode session state.
func copyCoordinatorRules(mainHome, targetHome string) error {
	sourceRoot := filepath.Join(mainHome, ".config", "opencode")
	targetRoot := filepath.Join(targetHome, ".config", "opencode")
	if err := copyCoordinatorFileIfPresent(filepath.Join(sourceRoot, "AGENTS.md"),
		filepath.Join(targetRoot, "AGENTS.md"), true); err != nil {
		return err
	}
	return copyCoordinatorTreeIfPresent(filepath.Join(sourceRoot, "skills"),
		filepath.Join(targetRoot, "skills"))
}

func copyCoordinatorFileIfPresent(source, destination string, overwrite bool) error {
	if err := rejectCoordinatorSymlinkPath(source); err != nil {
		return err
	}
	if err := rejectCoordinatorSymlinkPath(destination); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		slog.Default().Warn("协调者主 HOME 缺少规则文件，跳过供给", "source", source)
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat 协调者规则源 %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("协调者规则源不是普通文件 %q", source)
	}
	if existing, statErr := os.Lstat(destination); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("协调者规则目标是 symlink %q", destination)
		}
		if !overwrite {
			return nil
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查协调者规则目标 %q: %w", destination, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("创建协调者规则父目录 %q: %w", filepath.Dir(destination), err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("读取协调者规则 %q: %w", source, err)
	}
	if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("写协调者规则 %q: %w", destination, err)
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("设置协调者规则权限 %q: %w", destination, err)
	}
	slog.Default().Info("协调者规则文件已供给", "source", source, "target", destination)
	return nil
}

func copyCoordinatorTreeIfPresent(source, destination string) error {
	if err := rejectCoordinatorSymlinkPath(source); err != nil {
		return err
	}
	if err := rejectCoordinatorSymlinkPath(destination); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		slog.Default().Warn("协调者主 HOME 缺少 skills 目录，跳过供给", "source", source)
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat 协调者 skills 源 %q: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("协调者 skills 源不是普通目录 %q", source)
	}
	if existing, statErr := os.Lstat(destination); statErr == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.IsDir() {
			return fmt.Errorf("协调者 skills 目标不是普通目录 %q", destination)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("检查协调者 skills 目标 %q: %w", destination, statErr)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return fmt.Errorf("创建协调者 skills 目标 %q: %w", destination, err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("读取协调者 skills 目录 %q: %w", source, err)
	}
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(destination, entry.Name())
		entryInfo, statErr := os.Lstat(src)
		if statErr != nil {
			return fmt.Errorf("stat 协调者 skills 条目 %q: %w", src, statErr)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("协调者 skills 源含 symlink %q", src)
		}
		if entryInfo.IsDir() {
			if err := copyCoordinatorTreeIfPresent(src, dst); err != nil {
				return err
			}
			continue
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("协调者 skills 源不是普通文件 %q", src)
		}
		if err := copyCoordinatorFileIfPresent(src, dst, true); err != nil {
			return err
		}
	}
	return nil
}

func safeCoordinatorRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// coordinatorLstat and coordinatorEvalSymlinks are path-checking test seams.
// They keep the platform alias case deterministic without weakening the real
// filesystem check used by coordinator HOME supply.
var (
	coordinatorLstat        = os.Lstat
	coordinatorEvalSymlinks = filepath.EvalSymlinks
)

func rejectCoordinatorSymlinkPath(path string) error {
	clean := filepath.Clean(path)
	root := string(filepath.Separator)
	if volume := filepath.VolumeName(clean); volume != "" {
		root = volume + string(filepath.Separator)
	}
	if !filepath.IsAbs(clean) {
		root = "."
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil {
		return fmt.Errorf("检查协调者路径 %q: %w", path, err)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := coordinatorLstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("检查协调者路径 %q: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 && !isCoordinatorSystemVolumeAlias(root, current) {
			return fmt.Errorf("协调者路径含 symlink %q", current)
		}
	}
	return nil
}

// isCoordinatorSystemVolumeAlias allows macOS's immutable /var alias, which
// points at /private/var and is used by t.TempDir. Other symlinks remain denied;
// the exact resolved target keeps this exception from becoming path traversal.
func isCoordinatorSystemVolumeAlias(root, current string) bool {
	if root != string(filepath.Separator) || current != filepath.Join(root, "var") {
		return false
	}
	resolved, err := coordinatorEvalSymlinks(current)
	return err == nil && filepath.Clean(resolved) == filepath.Join(root, "private", "var")
}

type coordinatorSessionRefResolver struct {
	server        *Server
	expandHomeDir func(string) (string, error)
}

// normalizeCoordinatorSpec expands the carrier HOME before keystone persists the
// session reference, keeping Launch and later Resume on the same directory.
func normalizeCoordinatorSpec(spec keysclient.SessionSpec) (keysclient.SessionSpec, error) {
	if strings.TrimSpace(spec.HomeDir) == "" {
		return keysclient.SessionSpec{}, fmt.Errorf("协调者回合缺少 HomeDir: %q", spec.HomeDir)
	}
	expanded, err := hostapi.ExpandHomePath(spec.HomeDir)
	if err != nil {
		return keysclient.SessionSpec{}, fmt.Errorf("展开协调者回合 HOME %q: %w", spec.HomeDir, err)
	}
	if !filepath.IsAbs(expanded) {
		return keysclient.SessionSpec{}, fmt.Errorf("协调者回合 HOME 未展开为绝对路径: %q", expanded)
	}
	spec.HomeDir = expanded
	return spec, nil
}

// ResolveSessionRef fills a cold coordinate reference from the registered online
// carrier without admission. GET status must not consume a launch slot or mutate
// scheduling counters.
func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
	slog.Default().Info("协调者 attach 引用解析开始", "card", card, "has_home", ref.HomeDir != "",
		"has_session", ref.SessionID != "")
	if r.server == nil || r.server.scheduling == nil {
		return ref, errors.New("协调者 attach 无编制域读取端口")
	}
	if ref.HomeDir == "" {
		squad, err := r.server.resolveCoordinatorSquad()
		if err != nil {
			return ref, fmt.Errorf("读取协调者小队以恢复卡 %s HOME: %w", card, err)
		}
		for _, member := range squad.Members {
			carrier, readErr := r.server.scheduling.Carrier(member.Carrier)
			if readErr != nil {
				return ref, fmt.Errorf("读取协调者载体 %s HOME: %w", member.Carrier, readErr)
			}
			if carrier.Status != scheduling.StatusOnline {
				continue
			}
			ref.HomeDir = carrier.HomeDir
			break
		}
		if ref.HomeDir == "" {
			return ref, fmt.Errorf("协调者小队 %s 没有已上线载体可恢复 HOME", squad.Name)
		}
	}
	expand := r.expandHomeDir
	if expand == nil {
		expand = hostapi.ExpandHomePath
	}
	expanded, err := expand(ref.HomeDir)
	if err != nil {
		return ref, fmt.Errorf("展开卡 %s 的协调者 attach HOME %q: %w", card, ref.HomeDir, err)
	}
	if !filepath.IsAbs(expanded) {
		return ref, fmt.Errorf("卡 %s 的协调者 attach HOME 不是绝对路径: %q", card, expanded)
	}
	ref.HomeDir = expanded
	slog.Default().Info("协调者 attach 引用解析完成", "card", card, "has_home", true,
		"has_session", ref.SessionID != "")
	return ref, nil
}
