// Package config 负责 handoff 配置的加载、默认值填充与访问令牌生成。
//
// 职责：
//   - 读取 ~/.handoff/config.yaml（或指定路径）并解析为 Config
//   - 首次运行（文件不存在）时生成默认配置与随机 Token 并写盘
//   - 提供 DefaultPath 默认配置路径
//
// 边界：
//   - 不做网络请求，不校验 Target 可达性（由上层调用方负责）
//   - 不依赖 logx，仅使用 slog 默认 logger 输出关键节点日志
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// log 是本包日志入口，运行时取 slog.Default()（与 store 包一致）。
// agentd 在 bootstrap 时 logx.Setup + slog.SetDefault(...)，本包日志即跟随统一格式与级别。
// 为什么不用包级 var 捕获：包级 var 在 package init 时求值，晚于其执行的 slog.SetDefault
// 不会生效；运行时求值才能保证「agentd 先 Setup 后 SetDefault」的接线真正接管本包日志。
func log() *slog.Logger { return slog.Default() }

// Config 是 handoff 的顶层配置。
//
// Listen 为本地 agentd 监听地址；Token 为本机 agentd 的访问令牌；
// DataDir 为数据目录；StallTimeout 为卡住会话的判定超时；
// Targets 为可配对远端主机的地址与令牌表（供 --target 换算）。
type Config struct {
	Listen       string
	Token        string
	DataDir      string
	StallTimeout time.Duration
	Targets      map[string]Target
}

// Target 描述一个可配对远端主机：Addr 为 agentd 地址，Token 为其访问令牌。
type Target struct {
	Addr  string
	Token string
}

// Load 加载配置：文件不存在时返回带默认值的 Config 并自动生成随机 Token 写盘。
//
// 参数：
//   - path: 配置文件路径
//
// 返回：
//   - 解析后的配置；文件不存在时返回默认配置
//   - 错误信息：读/解析/写盘失败时返回
//
// 注意：
//   - 首次运行生成的 Token 需要人工同步到配对主机的 Targets 中
func Load(path string) (*Config, error) {
	cfg := &Config{Listen: "127.0.0.1:7777", DataDir: defaultDataDir(), StallTimeout: 2 * time.Hour}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.Token = randToken() // 首次运行：生成 token 并写盘，配对时人工同步到本机 targets
		log().Info("首次运行，已生成配置", "path", path)
		if werr := save(path, cfg); werr != nil {
			return nil, fmt.Errorf("写默认配置 %s: %w", path, werr)
		}
	case err != nil:
		return nil, fmt.Errorf("读配置 %s: %w", path, err)
	default:
		if uerr := yaml.Unmarshal(b, cfg); uerr != nil {
			return nil, fmt.Errorf("解析配置 %s: %w", path, uerr)
		}
	}
	return cfg, nil
}

// DefaultPath 返回默认配置文件路径（~/.handoff/config.yaml）。
func DefaultPath() string {
	return filepath.Join(defaultDataDir(), "config.yaml")
}

// defaultDataDir 返回默认数据目录；取不到用户主目录时回退到当前目录下的 .handoff。
func defaultDataDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".handoff")
	}
	return ".handoff"
}

// randToken 生成 16 字节（32 位十六进制字符）的加密随机令牌。
//
// 16 字节即 128 位随机熵，对本地配对场景足够：token 仅用于本机 agent 与
// agentd 之间的本地认证，不跨网络传输，穷举 2^128 空间不可行。
// 写盘文件权限为 0600，进一步限制了本地其他用户读取的可能。
func randToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// save 将配置以 YAML 形式写盘，自动创建父目录，文件权限 0600。
func save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置目录 %s: %w", filepath.Dir(path), err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return os.WriteFile(path, b, 0o600)
}
