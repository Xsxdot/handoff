// coordinator_home.go —— 协调者隔离 HOME 的供给、规范化与 ref 解析。
//
// 职责：
//   - 协调者无头 Launch/Resume 运行前按写入白名单供给隔离 HOME（config.yaml、AGENTS.md、skills/、缺失凭据）；
//   - Launch/Wake 自动化入口将 carrier 登记 HomeDir 规范化为展开后的绝对路径；
//   - attach 定位在 locator 消费 ref 前，由 resolver 补齐已上线载体的 HomeDir 并展开为绝对路径。
//
// 边界：
//   - 只服务协调者无头 Launch/Resume 与 attach ref，绝不被 WakeHome 调用；
//   - 严禁整树同步或 RemoveAll，不触碰 .local/share/opencode 下除单个表内凭据外的其他文件（尤其 session db）。
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

func (p coordinatorHomeSupplier) Prepare(spec keysclient.SessionSpec) (string, error) {
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
	if err := rejectCoordinatorHomeSymlinks(targetHome); err != nil {
		slog.Default().Error("检查协调者隔离 HOME 白名单失败", "cli", spec.CLI,
			"target", targetHome, "cause", err)
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

	slog.Default().Info("准备协调者隔离 HOME", "cli", spec.CLI, "target", targetHome)

	if err := os.MkdirAll(targetHome, 0o700); err != nil {
		return "", fmt.Errorf("创建协调者隔离 HOME %q: %w", targetHome, err)
	}
	projected, err := projectCoordinatorConfig(cfg)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(targetHome, ".handoff", "config.yaml")
	slog.Default().Debug("开始写入协调者隔离配置", "path", configPath)
	if err := config.Save(configPath, &projected); err != nil {
		return "", fmt.Errorf("写协调者隔离配置 %q: %w", configPath, err)
	}
	slog.Default().Info("写入协调者隔离配置完成", "path", configPath)

	if err := copyMissingCoordinatorCredential(mainHome, targetHome, spec.CLI, p.credentialPath); err != nil {
		return "", err
	}
	if err := copyCoordinatorRules(mainHome, targetHome); err != nil {
		return "", err
	}
	return targetHome, nil
}

// projectCoordinatorConfig 复制活配置并把 DataDir、RepoRoot 以及相对 SQLite Ledger DSN
// 转为绝对路径；URL 形式的 DSN（postgres:// / postgresql://）原样保留。
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

// rejectCoordinatorHomeSymlinks 只检查隔离 HOME 根和供给白名单中的路径。
// 不向上遍历文件系统祖先：那会把主机卷别名或其他无关路径误当成隔离 HOME 的风险，
// 而真正需要防护的是即将被 MkdirAll/WriteFile 使用的白名单路径不能跟随链接越界。
func rejectCoordinatorHomeSymlinks(targetHome string) error {
	paths := []string{
		targetHome,
		filepath.Join(targetHome, ".handoff"),
		filepath.Join(targetHome, ".handoff", "config.yaml"),
		filepath.Join(targetHome, ".config"),
		filepath.Join(targetHome, ".config", "opencode"),
		filepath.Join(targetHome, ".config", "opencode", "AGENTS.md"),
		filepath.Join(targetHome, ".config", "opencode", "skills"),
		filepath.Join(targetHome, ".local"),
		filepath.Join(targetHome, ".local", "share"),
		filepath.Join(targetHome, ".local", "share", "opencode"),
		filepath.Join(targetHome, ".local", "share", "opencode", "auth.json"),
		filepath.Join(targetHome, ".grok"),
		filepath.Join(targetHome, ".grok", "auth.json"),
		filepath.Join(targetHome, ".codex"),
		filepath.Join(targetHome, ".codex", "auth.json"),
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("检查协调者隔离 HOME 白名单路径 %q: %w", path, err)
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("协调者隔离 HOME 白名单路径是 symlink %q", path)
		}
	}
	return nil
}

// copyMissingCoordinatorCredential 仅拷贝该 CLI 缺失的单文件登录凭据。
// 为什么不用整树同步：隔离 HOME 不依赖主 HOME 的生命周期，也不会通过 symlink 越出白名单；
// 整树同步会覆盖隔离侧已有的 session 数据库或运行状态。因此仅复制白名单内的缺失凭据。
func copyMissingCoordinatorCredential(mainHome, targetHome, cli string,
	credentialPath func(string) (string, bool)) error {
	if credentialPath == nil {
		return nil
	}
	rel, ok := credentialPath(filepath.Base(cli))
	if !ok || rel == "" || filepath.IsAbs(rel) {
		return nil
	}
	source := filepath.Join(mainHome, rel)
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

// copyCoordinatorRules 供给 AGENTS.md 与 skills 树。
// 为什么不用整树同步：只允许普通文件/目录覆盖同名内容，不删除目标端额外文件，严禁 symlink。
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
	return os.Chmod(destination, info.Mode().Perm())
}

func copyCoordinatorTreeIfPresent(source, destination string) error {
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
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("协调者 skills 源含 symlink %q", src)
		}
		if entry.IsDir() {
			if err := copyCoordinatorTreeIfPresent(src, dst); err != nil {
				return err
			}
			continue
		}
		if err := copyCoordinatorFileIfPresent(src, dst, true); err != nil {
			return err
		}
	}
	return nil
}

// normalizeCoordinatorSpec 将 SessionSpec.HomeDir 展开为绝对路径。
// 空值视为主 HOME（~）展开；展开失败或非绝对路径返回包含原串的错误，防止字面 ~ 漏进 keystone。
func normalizeCoordinatorSpec(spec keysclient.SessionSpec) (keysclient.SessionSpec, error) {
	home := spec.HomeDir
	if strings.TrimSpace(home) == "" {
		home = "~"
	}
	expanded, err := hostapi.ExpandHomePath(home)
	if err != nil {
		return spec, fmt.Errorf("展开协调者 HOME %q: %w", home, err)
	}
	if !filepath.IsAbs(expanded) {
		return spec, fmt.Errorf("协调者 HOME 不是绝对路径: %q", expanded)
	}
	spec.HomeDir = expanded
	return spec, nil
}

// coordinatorSessionRefResolver 实现 keystone.SessionRefResolver：
// 在 ref 交给 TerminalLocator 之前，从已登记协调者小队已上线载体补齐空的 HomeDir，
// 并确保展开为绝对路径。
type coordinatorSessionRefResolver struct {
	server        *Server
	expandHomeDir func(string) (string, error)
}

func (r coordinatorSessionRefResolver) ResolveSessionRef(card string, ref keysclient.SessionRef) (keysclient.SessionRef, error) {
	if r.server == nil || r.server.scheduling == nil {
		return ref, errors.New("协调者 attach 无编制域读取端口")
	}
	slog.Default().Info("协调者 SessionRef 解析开始", "card", card, "has_home", ref.HomeDir != "",
		"has_session", ref.SessionID != "")
	if ref.HomeDir == "" {
		squad, err := r.server.resolveCoordinatorSquad()
		if err != nil {
			slog.Default().Error("读取协调者小队失败", "card", card, "cause", err)
			return ref, fmt.Errorf("读取协调者小队以恢复卡 %s HOME: %w", card, err)
		}
		var found bool
		for _, member := range squad.Members {
			carrier, readErr := r.server.scheduling.Carrier(member.Carrier)
			if readErr != nil {
				slog.Default().Error("读取协调者载体失败", "card", card, "carrier", member.Carrier, "cause", readErr)
				return ref, fmt.Errorf("读取协调者载体 %s HOME: %w", member.Carrier, readErr)
			}
			if carrier.Status != scheduling.StatusOnline {
				continue
			}
			ref.HomeDir = carrier.HomeDir
			if strings.TrimSpace(ref.HomeDir) == "" {
				ref.HomeDir = "~"
			}
			found = true
			break
		}
		if !found {
			err := fmt.Errorf("协调者小队 %s 没有已上线载体可恢复 HOME", squad.Name)
			slog.Default().Error("恢复协调者 HOME 失败", "card", card, "squad", squad.Name, "cause", err)
			return ref, err
		}
	}
	expand := r.expandHomeDir
	if expand == nil {
		expand = hostapi.ExpandHomePath
	}
	expanded, err := expand(ref.HomeDir)
	if err != nil {
		slog.Default().Error("展开协调者 attach HOME 失败", "card", card, "home_dir", ref.HomeDir, "cause", err)
		return ref, fmt.Errorf("展开卡 %s 的协调者 attach HOME %q: %w", card, ref.HomeDir, err)
	}
	if !filepath.IsAbs(expanded) {
		err := fmt.Errorf("卡 %s 的协调者 attach HOME 不是绝对路径: %q", card, expanded)
		slog.Default().Error("协调者 attach HOME 非绝对路径", "card", card, "expanded", expanded, "cause", err)
		return ref, err
	}
	ref.HomeDir = expanded
	slog.Default().Info("协调者 SessionRef 解析成功", "card", card, "home_dir", ref.HomeDir)
	return ref, nil
}

// isSafeShellWord 检查字符串是否由合法安全的 shell 字符组成：
// [A-Za-z0-9_+\-.,/:@%]
func isSafeShellWord(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '+' || c == '-' || c == '.' || c == ',' || c == '/' || c == ':' || c == '@' || c == '%' {
			continue
		}
		return false
	}
	return true
}

// shellQuote 将字符串安全引用为 POSIX shell 单词：
// 安全字符原样输出，其余字符用单引号包围并把内部 ' 转义为 '\”。
func shellQuote(s string) string {
	if isSafeShellWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
