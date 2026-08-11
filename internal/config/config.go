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
	"regexp"
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
	Listen  string
	Token   string
	DataDir string
	// RepoRoot 是自动登记（B62）的 clone 落点根目录：首次派发到某台机器、而
	// 那台机器上还没有该项目时，agentd 会把仓库 clone 到这里，实际落点为
	// RepoRoot/<登记名>。空=未配置，Load 会补 <DataDir>/repos。
	//
	// 为什么放顶层而不是放进 Target：Target 是在**审核者本地**被读取的
	//（见 cmd/pull.go 的 cfg.Targets[task.Target]），放那儿会让「仓库放哪」
	// 变成审核者的本地状态，换一台审核机接管就得重配。放顶层的语义是
	// 「每台执行机自己决定它的仓库放在哪」。
	// yaml:"repo_root"：strict 解码器（KnownFields）按 tag 匹配键名，不加 tag 时
	// yaml.v3 会把 RepoRoot 映射成 reporoot，与 README/设计文档里的 repo_root 不符。
	RepoRoot     string `yaml:"repo_root"`
	StallTimeout time.Duration
	Targets      map[string]Target
	// Approver 是分级审批链的廉价模型审批者配置。Executor 空=不启用审批链
	//（二期前的现行为：权限请求直接走人工审核者）。
	Approver ApproverConfig
	// Executor 是任务的缺省执行者选择配置。
	Executor ExecutorConfig
	// Terminal 是 dispatch 成功后是否默认弹终端实况的配置。
	Terminal TerminalConfig
	// Sync 是任务结束后自动同步远程任务分支到本地的配置。
	Sync SyncConfig
	// Update 是自动更新配置。Auto 默认 true，Interval 默认 6h。
	Update UpdateConfig
	// Env 是 agent（executor）名 → env 文件名的映射：该 agent 启动时注入该文件里的
	// 环境变量。文件名必须是 <DataDir>/env/ 下的纯文件名（含路径分隔符会被拒绝）。
	// 未配置的 agent 不注入。任务执行者与审批者共用同一份（见 B19 spec §4）。
	Env map[string]string
}

// SyncConfig 描述任务结束（completed/failed）后 wait 是否自动把远程任务分支
// 同步到本地仓库。Auto 默认 true；关闭后仍可用 handoff pull 手动同步。
type SyncConfig struct {
	Auto bool
}

// UpdateConfig 是**已废弃**的自动更新配置。
//
// B59 取消了 agentd 的定时自更新循环：升级改由操作者一条 handoff upgrade
// 触发，二进制由本机下载后推送给远端。这两个字段因此不再有任何效果。
//
// **为什么保留字段而不是删掉**：配置是 KnownFields(true) 严格解析的，未知键
// 让 agentd **启动失败**。v0.1.0 的首次运行会把这两个键写进 config.yaml，
// 直接删字段等于让所有装过 v0.1.0 的机器升级后起不来——正是这个设计要消灭
// 的那类失配的最狠形态（B59 spec D7）。
//
// 取值非默认时由 WarnDeprecated 打一条 Warn：用户把 auto 设成 false 是有
// 意图的，悄悄让它失效等于骗人。
type UpdateConfig struct {
	Auto     bool
	Interval time.Duration
}

// WarnDeprecated 对已废弃且被显式改过的配置打一条 Warn。
//
// 参数：
//   - log: 日志器（agentd 启动时传自己的）
//
// 注意：
//   - 默认值不打。绝大多数机器都是默认值，每次启动打一条无从处置的 Warn，
//     只会让人学会忽略日志——而那是比不打更糟的结果
func (c *Config) WarnDeprecated(log *slog.Logger) {
	if !c.Update.Auto {
		log.Warn("配置 update.auto 已废弃且不再有效果：agentd 不再自动更新，升级请在审核者机器上跑 handoff upgrade --now")
	}
	if c.Update.Interval != 6*time.Hour {
		log.Warn("配置 update.interval 已废弃且不再有效果：agentd 不再定时检查版本",
			"配置值", c.Update.Interval)
	}
}

// ApproverConfig 描述审批链的廉价模型审批者。
//
// 参数语义：
//   - Executor：审批者执行者名（如 opencode/claude/grok）；空=不启用审批链
//   - Model：审批者模型名；空=用执行者自身默认模型
//   - Timeout：单次裁决超时，超时按 escalate 处理（fail-closed）
//   - Blacklist：自定义黑名单正则；命中即跳过审批者直接升级人工审核者
type ApproverConfig struct {
	Executor  string
	Model     string
	Timeout   time.Duration
	Blacklist []string
}

// ExecutorConfig 描述 dispatch 未显式指定执行者时的缺省选择。
type ExecutorConfig struct {
	Default string
	Model   string
}

// TerminalConfig 描述 dispatch 成功后的终端弹窗行为。
//
// Auto 默认 true，仅 darwin 生效（osascript 弹 Terminal.app）；其余平台
// 降级为打印「实况: handoff attach <id>」提示行。
type TerminalConfig struct {
	Auto bool
}

// Target 描述一个可配对远端主机：Addr 为 agentd 地址，Token 为其访问令牌，
// User 为可选的 ssh 用户名（非空时 attach/pull 的 ssh 目标换算为 user@host，
// 空=保持历史行为只用 host）。
type Target struct {
	Addr  string
	Token string
	User  string
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
	// 初始字面量预置默认值，yaml 覆盖式解码：配置里没写的键保持默认
	//（如 approver.timeout=60s、executor.default=opencode、terminal.auto=true），
	// 写了的键覆盖——而非「只读显式配置，其余为空」导致默认值丢失。
	cfg := &Config{
		Listen: "127.0.0.1:7777", DataDir: defaultDataDir(), StallTimeout: 2 * time.Hour,
		Approver: ApproverConfig{Timeout: 60 * time.Second},
		Executor: ExecutorConfig{Default: "opencode"},
		Terminal: TerminalConfig{Auto: true},
		Sync:     SyncConfig{Auto: true},
		Update:   UpdateConfig{Auto: true, Interval: 6 * time.Hour},
		Targets:  map[string]Target{},
		Env:      map[string]string{},
	}
	// firstRun 标记首次运行（配置文件不存在）：默认值补全必须在解码之后，
	// 而写盘必须在补全之后，否则默认 repo_root 不会随首次写盘一起落地。
	firstRun := false
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.Token = randToken() // 首次运行：生成 token 并写盘，配对时人工同步到本机 targets
		firstRun = true
		log().Info("首次运行，将生成配置", "path", path)
	case err != nil:
		return nil, fmt.Errorf("读配置 %s: %w", path, err)
	default:
		if uerr := decodeStrict(b, cfg); uerr != nil {
			log().Error("配置解析失败", "path", path, "cause", uerr)
			return nil, fmt.Errorf("解析配置 %s: %w", path, uerr)
		}
	}
	// repo_root 的默认值必须在解码之后补，不能预置在初始字面量里：
	// 它派生自 DataDir，而 DataDir 本身可能被配置文件改写。
	//
	// 为什么必须有默认值：自动登记（B62）把 clone 变成首次派发的主路径，
	// repo_root 为空时 agentd 会直接拒绝 clone，新开发机上第一次派发必然失败。
	// 落盘之后就固定，此后改 datadir 不会静默改克隆落点。
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = filepath.Join(cfg.DataDir, "repos")
		log().Info("repo_root 未配置，采用默认落点", "repo_root", cfg.RepoRoot)
	}
	if firstRun {
		if werr := save(path, cfg); werr != nil {
			return nil, fmt.Errorf("写默认配置 %s: %w", path, werr)
		}
		log().Info("首次运行，已生成配置", "path", path)
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
	// approver 相关的取值域校验只在审批链启用时生效（Executor 非空）：
	// 未启用时写不写这些键都不影响行为，写错也不该拦启动。
	if c.Approver.Executor != "" {
		if c.Approver.Timeout <= 0 {
			return fmt.Errorf("approver.timeout 必须为正时长（当前 %s）", c.Approver.Timeout)
		}
		for i, r := range c.Approver.Blacklist {
			// 黑名单是正则，必须在启动期编译校验：运行期才 panic 会让
			// 任务权限处理在仲裁中途崩溃，而启动期报错只需改配置重启。
			if _, err := regexp.Compile(r); err != nil {
				return fmt.Errorf("approver.blacklist[%d] 非法正则 %q: %w", i, r, err)
			}
		}
	}
	// update.interval 只在启用自动更新时校验：没启用的东西写错不该拦启动，
	// 与 approver 那组的处置保持一致。
	//
	// 为什么非正值必须拦：0 会让更新循环的 ticker 每个 tick 都立刻到期，
	// 退化成忙轮询，几秒钟打满 GitHub 匿名限流（60 次/小时），此后所有
	// 版本检查一起失败——症状是「自动更新莫名其妙不工作了」，根因却在
	// 一行配置上。省略该键走默认 6h 是正常用法。
	if c.Update.Auto && c.Update.Interval <= 0 {
		return fmt.Errorf("update.interval 必须为正时长（当前 %s）；省略该键即用默认 6h", c.Update.Interval)
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
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/repo_root/stalltimeout/targets{addr,user,token}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/update{auto,interval}/env{<agent>: <文件名>}）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
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

// Save 把配置以 YAML 写盘，自动创建父目录，文件权限 0600。
//
// 参数：
//   - path: 目标路径
//   - cfg: 要写入的配置
//
// 返回：
//   - 错误信息：建目录、序列化或写盘失败时返回
//
// 注意：
//   - 0600 是硬要求：配置里含 token，组内可读就等于把令牌给了同机其他账号
func Save(path string, cfg *Config) error { return save(path, cfg) }
