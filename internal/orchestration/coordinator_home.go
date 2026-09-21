// coordinator_home.go —— 协调者隔离 HOME 的供给、规范化与 shell 单词引用
// （B233.26 自 gateway 归域编排包；SessionRef resolver 因耦合 *Server 留 gateway，
// 见 internal/agentd/coordinator_sessionref.go）。
//
// 职责：
//   - 协调者无头 Launch/Resume 运行前按写入白名单供给隔离 HOME（config.yaml、AGENTS.md、skills/、缺失凭据）；
//   - Launch/Wake 自动化入口将 carrier 登记 HomeDir 规范化为展开后的绝对路径；
//   - attach 定位的 shell 命令拼装提供安全引用（ShellQuote）。
//
// 边界：
//   - 只服务协调者无头 Launch/Resume 与 attach ref，绝不被 WakeHome 调用；
//   - 严禁整树同步或 RemoveAll，不触碰 .local/share/opencode 下除单个表内凭据外的其他文件（尤其 session db）。
package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

type coordinatorHomeSupplier struct {
	currentConfig  func() *config.Config
	userHomeDir    func() (string, error)
	expandHomeDir  func(string) (string, error)
	credentialPath func(string) (string, bool)
	profileFor     func(cli string) (executor.Profile, error)
	loadRules      func(mainHome, cli string) (rules, skills []executor.ProfileFile, err error)
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
	if p.profileFor == nil {
		return "", errors.New("协调者供给缺少 Profile")
	}
	prof, err := p.profileFor(spec.CLI)
	if err != nil {
		return "", err
	}
	if p.loadRules == nil {
		return "", errors.New("协调者供给缺少规则装载函数")
	}
	rules, skills, err := p.loadRules(mainHome, spec.CLI)
	if err != nil {
		return "", err
	}
	rep, err := prof.Prepare(context.Background(), executor.ProfileReq{
		HomeDir:    targetHome,
		Isolated:   true,
		Credential: executor.CredentialMainHomeSync,
		Rules:      rules,
		Skills:     skills,
	})
	if err != nil {
		return "", err
	}
	if !rep.Prepared {
		return "", fmt.Errorf("Profile.Prepare 未成功 prepared=false notes=%v", rep.Notes)
	}
	return targetHome, nil
}

// NewCoordinatorPrepareHome 构造协调者隔离 HOME 的供给函数（coordinatorHomeSupplier
// 的 Prepare 方法值）。B233.18 keystone 构造上移 cmd 组装点后，cmd 无法直接拼装
// 本包未导出类型，字段接线（主 HOME 读取、HOME 展开、凭据相对路径表、Profile
// 闭包）由本构造函数按原 SetupAutomation 形态完成；调用方只注入 Server 持有的
// 三个活依赖（活配置、执行者注册表、规则装载）。返回值供 coordinatorRunner 的
// prepareHome 缝直接消费。providers 允许为 nil（沿用原 nil 守卫：按能力缺口拒绝）。
func NewCoordinatorPrepareHome(currentConfig func() *config.Config, providers *executor.Registry,
	loadRules func(mainHome, cli string) ([]executor.ProfileFile, []executor.ProfileFile, error),
) func(keysclient.SessionSpec) (string, error) {
	supplier := coordinatorHomeSupplier{
		currentConfig:  currentConfig,
		userHomeDir:    os.UserHomeDir,
		expandHomeDir:  hostapi.ExpandHomePath,
		credentialPath: toolchain.CredRelPathFor,
		loadRules:      loadRules,
		profileFor: func(cli string) (executor.Profile, error) {
			base := filepath.Base(cli)
			if providers == nil {
				return nil, executor.UnsupportedError(base, executor.CapProfile)
			}
			prov, err := providers.Get(base)
			if err != nil {
				return nil, err
			}
			if profile, ok := executor.ProfileFromProvider(prov); ok {
				return profile, nil
			}
			return nil, executor.UnsupportedError(base, executor.CapProfile)
		},
	}
	return supplier.Prepare
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

// NormalizeCoordinatorSpec 将 SessionSpec.HomeDir 展开为绝对路径。
// 空值视为主 HOME（~）展开；展开失败或非绝对路径返回包含原串的错误，防止字面 ~ 漏进 keystone。
// B233.26 导出：gateway 侧 scheddrain 的 Launch/Wake 入口仍在消费（归域后跨包调用）。
func NormalizeCoordinatorSpec(spec keysclient.SessionSpec) (keysclient.SessionSpec, error) {
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

// ShellQuote 将字符串安全引用为 POSIX shell 单词：
// 安全字符原样输出，其余字符用单引号包围并把内部 ' 转义为 '\”。
// B233.26 导出：gateway 侧 attachLocator 拼装 attach 命令仍在消费（归域后跨包调用）。
func ShellQuote(s string) string {
	if isSafeShellWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
