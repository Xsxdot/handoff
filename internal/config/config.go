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
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
		if uerr := decodeStrict(b, cfg); uerr != nil {
			log().Error("配置解析失败", "path", path, "cause", uerr)
			return nil, fmt.Errorf("解析配置 %s: %w", path, uerr)
		}
	}
	if verr := cfg.validate(); verr != nil {
		log().Error("配置校验失败", "path", path, "cause", verr)
		return nil, fmt.Errorf("校验配置 %s: %w", path, verr)
	}
	return cfg, nil
}

// validate 校验取值域，把「能解析但必然误动作」的配置挡在启动之前。
//
// 为什么 stalltimeout 必须为正：它是「running 任务多久没动静就算卡住」的阈值。
// 写成 0 或负数时，看门狗会在**每个** running 任务的首个 tick 上判定 stalled，
// 审核者被一批凭空的 stalled 事件叫醒，而任务其实好好的。省略该键走默认值
// （2h）是正常用法，只有显式写了非正值才是配置错误。
func (c *Config) validate() error {
	if c.StallTimeout <= 0 {
		return fmt.Errorf("stalltimeout 必须为正时长（当前 %s）；省略该键即用默认 2h", c.StallTimeout)
	}
	return nil
}

// decodeStrict 用 yaml.Decoder + KnownFields(true) 严格解析配置。
//
// 为什么必须严格（L-1）：yaml.Unmarshal 对未知键静默忽略——旧配置里的
// access_key/secret_key 等已废弃标量会被无声吞掉，而这类键在旧版本中承载
// 鉴权语义：忽略它们会让 agentd 在用户不知情时以无 token 状态启动（安全
// 静默降级）。严格解析让任何键名笔误/废弃键立即报错，错误文本自带未知键名
// （yaml 的 "field <key> not found"）与已知键清单，运维升级时一眼可定位。
// KnownFields 对 targets map 的 value（Target 结构）同样生效，目标条目内的
// 未知键同样被拒绝。
//
// 注意：
//   - 空文件按「无内容」处理（返回 nil、保持默认值），与 yaml.Unmarshal
//     对空输入的 no-op 语义一致——Decode 对空输入返回 io.EOF，不能当错误
func decodeStrict(b []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // 空文件：没有可解析内容，保留默认值
		}
		// 已知键清单与 yaml 报错文本（含未知键名）一起返回；
		// 旧版 access_key/secret_key 等键已不支持，提示直接删除或升级配置
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/stalltimeout/targets{addr,token}）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
	}
	return nil
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
