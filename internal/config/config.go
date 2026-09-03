// Package config 负责 handoff 配置的加载、默认值填充与访问令牌生成。
//
// 职责：
//   - 读取 ~/.handoff/config.yaml（或指定路径）并解析为 Config
//   - 首次运行（文件不存在）时生成默认配置与随机 Token 并写盘
//   - 旧文件顶层 update 段先剥再严格解码，避免 KnownFields 拒启动
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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/Xsxdot/handoff/internal/proxycfg"
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
	// Relay 是 executor 出站 relay 配置；nil 表示不启用 relay 出站。
	Relay *RelayConfig `yaml:"relay,omitempty"`
	// RepoRoot 是自动登记（B62）的 clone 落点根目录：首次派发到某台机器、而
	// 那台机器上还没有该项目时，agentd 会把仓库 clone 到这里，实际落点为
	// RepoRoot/<登记名>。空=未配置，Load 会补 <DataDir>/repos。
	//
	// 为什么放顶层而不是放进 Target：Target 是在**协调者本地**被读取的
	//（见 cmd/pull.go 的 cfg.Targets[task.Target]），放那儿会让「仓库放哪」
	// 变成协调者的本地状态，换一台协调者机接管就得重配。放顶层的语义是
	// 「每台执行机自己决定它的仓库放在哪」。
	// yaml:"repo_root"：strict 解码器（KnownFields）按 tag 匹配键名，不加 tag 时
	// yaml.v3 会把 RepoRoot 映射成 reporoot，与 README/设计文档里的 repo_root 不符。
	RepoRoot string `yaml:"repo_root"`
	// PathDirs 是本机额外的可执行文件搜索目录：agentd 启动时按序追加到 PATH 末尾
	// （见 internal/pathenv）。内置已知目录表没覆盖到的安装位置写在这里。
	//
	// 为什么放顶层而不是放进 Executor：它描述的是「**这台机器**上工具装在哪」，
	// 不是执行者的属性——与 RepoRoot 同一个道理。
	//
	// omitempty 是硬要求，不是风格：配置以 KnownFields(true) 严格解析，未知键让
	// agentd **启动失败**。没有 omitempty 时，新版 Save 会把 path_dirs: [] 写进
	// 每一台机器的 config.yaml，而一台还没换版的旧 agentd 读到它就再也起不来了
	//（B59 spec D7 同款，方向相反）。
	PathDirs []string `yaml:"path_dirs,omitempty"`
	// Proxy 是 handoff **自身**出网时使用的代理地址，形如 http://host:port、
	// https://host:port、socks5://host:port、socks5h://host:port。
	// 空 = 不配，沿用 HTTPS_PROXY/HTTP_PROXY/NO_PROXY 环境变量（现行为不变）。
	//
	// 作用范围只有两处：更新链路的 HTTP 出网（查 release、下资产）与 agentd 的
	// git clone/fetch。**不作用于协调者↔agentd 链路**——那是 LAN/loopback 地址，
	// 代理化轻则每次请求多绕一跳，重则 socks5 代理解析不了 100.x.y.z 直接断链，
	// 而这条链路的可达性是 handoff 的命根子。也**不作用于 executor**：executor
	// 的出网归 env 段（B19），两者故障域不交叉——代理挂了只影响升级，不影响任务执行。
	//
	// 为什么放顶层而不是放进 Target：它描述的是「**这台机器**怎么出网」，
	// 与 RepoRoot / PathDirs 同一个道理。
	//
	// omitempty 是硬要求，不是风格：配置以 KnownFields(true) 严格解析，未知键让
	// agentd **启动失败**。没有 omitempty 时，新版 Save 会把 proxy: "" 写进
	// 每一台机器的 config.yaml，而一台还没换版的旧 agentd 读到它就再也起不来了
	//（PathDirs 同款）。
	Proxy string `yaml:"proxy,omitempty"`
	// EnvForward 是要转发进终端会话的环境变量名单（见 internal/ptyhost）。
	//
	// 它解决的是 PathDirs 解决不了的**另一类**问题：SSH_AUTH_SOCK 这类变量由
	// launchd / ssh-agent **按会话注入**，不来自任何 dotfile，因此 login shell
	// 的 rc 链**无法**像恢复 PATH 那样把它恢复出来。agentd 以服务形态托管时，
	// 终端里的 ssh / git push 会因此全部失败。
	//
	// 三态语义（**不要**在 Load 里填默认值）：
	//   nil        → 用内置默认清单 ptyhost.DefaultEnvForward()（当前是 SSH_AUTH_SOCK）
	//   非 nil     → 完全以配置为准
	//   []（显式） → 一个都不转发
	// 一旦 Load 把默认值填进结构体，下一次 Save 就会把 env_forward 落进
	// config.yaml，omitempty 形同虚设，旧 agentd 照样被顶死。
	//
	// omitempty 是硬要求，理由同 PathDirs（B59 spec D7）。
	EnvForward   []string `yaml:"env_forward,omitempty"`
	StallTimeout time.Duration
	Targets      map[string]Target
	// Ledger 中心账本库连接。DSN 空 = 单机回退模式（账本落
	// DataDir/ledger.db 的 SQLite）。omitempty 是硬约束不是风格：
	// 解码是 KnownFields(true)，新键不 omitempty 会让旧版 agentd
	// 读到新版写的配置直接启动失败。
	Ledger LedgerConfig `yaml:"ledger,omitempty"`
	// Approver 是分级审批链的廉价模型审批者配置。Executor 空=不启用审批链
	//（二期前的现行为：权限请求直接走人工协调者）。
	Approver ApproverConfig
	// Executor 是任务的缺省执行者选择配置。
	Executor ExecutorConfig
	// Terminal 是 dispatch 成功后是否弹终端实况的配置（Auto 默认 false，见 TerminalConfig）。
	Terminal TerminalConfig
	// Sync 是任务结束后自动同步远程任务分支到本地的配置。
	Sync SyncConfig
	// Env 是 agent（executor）名 → env 文件名的映射：该 agent 启动时注入该文件里的
	// 环境变量。文件名必须是 <DataDir>/env/ 下的纯文件名（含路径分隔符会被拒绝）。
	// 未配置的 agent 不注入。任务执行者与审批者共用同一份（见 B19 spec §4）。
	Env map[string]string
	// Discipline 是 executor 名 → 纪律块文件名的映射：派发该 executor 的任务时，
	// 把该文件的内容作为「执行纪律」注入首回合 prompt。文件名必须是
	// <DataDir>/discipline/ 下的纯文件名（含路径分隔符会被拒绝）。
	//
	// 三档语义（与 Env 刻意不同的是第三档）：有非空值用该文件；显式空串关闭注入；
	// 未出现该键用内置默认。Env 在未出现该键时是不注入。
	//
	// 为什么第三档不同：env 内容是机器特有的，猜错不如不猜；纪律块内容是 handoff
	// 通用的，不给默认等于让用户退回人工粘贴到 plan 头部（见 B129 spec §2.4）。
	Discipline map[string]string
	// PlatformInvariants 是平台底线恒在层的显式开关。
	//
	// nil 表示未配置，PlatformInvariantsEnabled 将其解释为 true；非 nil 的 false
	// 才是关闭平台不变量的明确机器级选择。使用指针并保留 omitempty，是为了同时
	// 区分旧配置的「没有这个键」与用户明确写入的 false，并避免默认值污染旧配置。
	PlatformInvariants *bool `yaml:"platform_invariants,omitempty"`
	// ProcFence 是 executor 进程围栏配置。默认启用、保留 10%。
	ProcFence ProcFenceConfig `yaml:"proc_fence,omitempty"`
	// Web 是浏览器控制台相关配置。
	Web WebConfig
}

// PlatformInvariantsEnabled 返回本机是否注入平台不变量恒在层。
//
// 参数：无；接收 nil Config 时按默认启用处理，便于启动早期与测试构造使用。
// 返回：配置缺失或显式 true 时为 true；只有显式 false 时为 false。
// 注意：调用方不要直接解引用 PlatformInvariants，否则旧配置会把默认底线误关掉。
func (c *Config) PlatformInvariantsEnabled() bool {
	if c == nil || c.PlatformInvariants == nil {
		return true
	}
	return *c.PlatformInvariants
}

// LedgerConfig 账本域（任务卡）中心库配置。只描述本机如何连库，
// 不描述库里有什么——schema 归 internal/ledger 管。
type LedgerConfig struct {
	// Enabled 已退休（B229 §2.6）：账本变必需品，恒开。键与字段仅为
	// KnownFields 严格解析不炸存量 config 而保留，值被忽略；加载到该键时
	// Load 会 Warn 一条退休提示。不能用「DSN 非空」当启用信号——单机
	// SQLite 用户恰恰是 DSN 为空的那一类。
	Enabled bool `yaml:"enabled,omitempty"`
	// DSN 形如 postgres://user:pass@host:5432/db。空 = SQLite 回退。
	DSN string `yaml:"dsn,omitempty"`
}

// RelayConfig describes the executor's outbound relay registration.
// Credential is only sent during the WSS control exchange; it is separate from
// the E2E key derived from Token.
type RelayConfig struct {
	URL        string `yaml:"url"`
	Credential string `yaml:"credential"`
	Node       string `yaml:"node"`
}

// SyncConfig 描述任务结束（completed/failed）后 wait 是否自动把远程任务分支
// 同步到本地仓库。Auto 默认 true；关闭后仍可用 handoff pull 手动同步。
type SyncConfig struct {
	Auto bool
}

// ApproverConfig 描述审批链的廉价模型审批者。
//
// 参数语义：
//   - Executor：审批者执行者名（如 opencode/claude/grok/agy/codex）；空=不启用审批链
//   - Model：审批者模型名；空=用执行者自身默认模型
//   - Timeout：单次裁决超时，超时按 escalate 处理（fail-closed）
//   - Blacklist：自定义黑名单正则；命中即跳过审批者直接升级人工协调者
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
// Auto 默认 **false**（不弹）；仅当置 true 时才在 darwin 下用 osascript 弹
// Terminal.app 进实况；其余平台无论配置如何都降级为打印
// 「实况: handoff attach <id>」提示行。
//
// 为什么默认不弹：dispatch 的 stdout 有「单行任务 JSON」契约，弹窗与提示行都
// 不该干扰它；且逐次开关由 --no-terminal 承担，默认不弹才不会让老脚本因为
// 多出一行提示而解析错乱。
type TerminalConfig struct {
	Auto bool
}

// WebConfig 是浏览器控制台相关配置。
//
// AllowedHosts 是 Host 白名单的扩展项——回环地址（127.0.0.1 / localhost / ::1）
// 与 Listen 的 host 恒在白名单内，无需重复配置。它为将来的域名/中转场景预留：
// agentd 部署在 handoff.example.com 后面时，不配这一项所有请求都会被 403。
//
// yaml:"allowed_hosts"：strict 解码器（KnownFields）按 tag 匹配键名，
// 不加 tag 时 yaml.v3 会把它映射成 allowedhosts（同 RepoRoot 的处理）。
type WebConfig struct {
	AllowedHosts []string `yaml:"allowed_hosts"`
}

// Target 描述一个可配对远端主机：Addr 为 agentd 地址，Token 为其访问令牌，
// User 为可选的 ssh 用户名（非空时 attach/pull 的 ssh 目标换算为 user@host，
// 空=保持历史行为只用 host）。
type Target struct {
	Addr       string `yaml:"addr,omitempty"`
	Token      string `yaml:"token,omitempty"` // relay 形态下额外用作 E2E PSK 源（HKDF 派生），relay 不可见。
	User       string `yaml:"user,omitempty"`
	Relay      string `yaml:"relay,omitempty"`      // relay WSS URL；与 Addr 互斥。
	Credential string `yaml:"credential,omitempty"` // coordinator 的 CONNECT 凭证。
	Node       string `yaml:"node,omitempty"`       // relay 上的 executor 节点名。
}

// IsRelay reports whether this target uses the relay form.
func (t Target) IsRelay() bool { return t.Relay != "" }

// Validate validates a target. Relay and direct targets are mutually exclusive:
// relay targets require Relay, Credential, Node, and Token, while direct targets
// require Addr.
func (t Target) Validate() error {
	if t.IsRelay() {
		if err := validateRelayURL(t.Relay); err != nil {
			return fmt.Errorf("target.relay: %w", err)
		}
		if t.Addr != "" {
			return errors.New("target relay 与 addr 互斥")
		}
		if t.Credential == "" {
			return errors.New("relay target credential 不能为空")
		}
		if t.Node == "" {
			return errors.New("relay target node 不能为空")
		}
		if t.Token == "" {
			return errors.New("relay target token 不能为空")
		}
		return nil
	}
	if t.Addr == "" {
		return errors.New("direct target addr 不能为空")
	}
	return nil
}

// Validate validates the executor relay configuration. ws:// is accepted for
// local tests; production deployments should use wss://.
func (r *RelayConfig) Validate() error {
	if r == nil {
		return nil
	}
	if err := validateRelayURL(r.URL); err != nil {
		return fmt.Errorf("relay.url: %w", err)
	}
	if r.Credential == "" {
		return errors.New("relay.credential 不能为空")
	}
	if r.Node == "" {
		return errors.New("relay.node 不能为空")
	}
	return nil
}

func validateRelayURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL 无法解析: %w", err)
	}
	if (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return fmt.Errorf("URL 必须是 wss:// 或 ws://（当前 %q）", raw)
	}
	return nil
}

// ProcFenceConfig 描述 executor 进程围栏（RLIMIT_NPROC）的策略。
//
// 字段说明：
//   - Disabled: true 时完全不装围栏。逃生开关，正常不该用——2026-08-12 的
//     整机 fork 瘫痪就是无围栏状态下发生的
//   - ReserveRatio: 保留给 agentd/sshd/登录 shell 的名额占系统上限的比例；
//     0 或越界时取默认 0.1。这是「救护车道」的宽度，不是给 executor 的节流
//     旋钮——调小它不增加安全性，只会让 executor 更早撞墙
//
// 注意：yaml tag 必须写全。strict 解码器（KnownFields）按 tag 匹配键名，
// 不加 tag 时 yaml.v3 会把 ReserveRatio 映射成 reserveratio，与 README 里的
// reserve_ratio 对不上（RepoRoot 同款教训）。
type ProcFenceConfig struct {
	Disabled     bool    `yaml:"disabled"`
	ReserveRatio float64 `yaml:"reserve_ratio"`
	// TaskBudget 是**单个任务**名下的进程数告警线，超过即发一次
	// task_proc_pressure 事件唤醒审核者。0 = 关掉这一档。
	//
	// 为什么不是把围栏值调小：RLIMIT_NPROC 的内核判定是「该 uid 当前进程总数
	// 是否超过调用者软限」，不是「这棵进程树的后代数」。给每个 shim 装 300 的
	// 效果是「uid 总数一过 300 所有 shim 一起 fork 失败」，第二个任务会被第一个
	// 饿死——表达不了每任务额度，只能换成 watchdog 按任务点名（B93 spec §2.2）
	TaskBudget int `yaml:"task_budget"`
	// TaskHardLimit 是单个任务的进程数硬上限，超过即强制清扫并落 failed。
	// 0 = 关掉这一档。
	//
	// 两档的分工：TaskBudget 是「叫醒人」，TaskHardLimit 是「不等人了」。
	// 只有一档要么太吵（每次都杀）要么太晚（人没醒机器就没了）。
	TaskHardLimit int `yaml:"task_hard_limit"`
}

// Load 加载配置：文件不存在时返回带默认值的 Config 并自动生成随机 Token 写盘。
//
// 参数：
//   - path: 配置文件路径
//
// 返回：
//   - 解析后的配置；文件不存在时返回默认配置
//   - 错误信息：读/解析/校验失败时返回。剥 update 后回写失败不返回错误
//
// 注意：
//   - 首次运行生成的 Token 需要人工同步到配对主机的 Targets 中
//   - 旧文件的顶层 update 必须先剥再 KnownFields，否则 v0.1.x 机器升级即砖
//
// 与 Defaults 的分工：Load 在文件不存在时会生成 token 并把默认配置写盘（firstRun），
// 这是给 CLI/agentd 用的；桌面壳的首次引导走 Defaults（见其 doc 注释）。
func Load(path string) (*Config, error) {
	cfg := newDefaultConfig()
	// firstRun 标记首次运行（配置文件不存在）：默认值补全必须在解码之后，
	// 而写盘必须在补全之后，否则默认 repo_root 不会随首次写盘一起落地。
	firstRun := false
	// stripped 表示这次从已有文件剥掉了废弃的顶层 update。必须提到 switch
	// 外面：validate 通过后才回写，校验失败的脏配置不该被我们「修好」落盘。
	stripped := false
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.Token = randToken() // 首次运行：生成 token 并写盘，配对时人工同步到本机 targets
		firstRun = true
		log().Info("首次运行，将生成配置", "path", path)
	case err != nil:
		return nil, fmt.Errorf("读配置 %s: %w", path, err)
	default:
		// 必须先剥顶层 update 再 KnownFields：v0.1.x 写过的旧文件里仍有
		// 这段死配置，直接严格解码会把整个 agentd 卡在启动上。
		var cleaned []byte
		var serr error
		cleaned, stripped, serr = stripDeprecatedTopLevel(b)
		if serr != nil {
			log().Error("配置解析失败", "path", path, "cause", serr)
			return nil, fmt.Errorf("解析配置 %s: %w", path, serr)
		}
		if stripped {
			log().Warn("配置 update 段已废弃，已忽略并将从文件删除", "path", path)
			b = cleaned
		}
		if uerr := decodeStrict(b, cfg); uerr != nil {
			log().Error("配置解析失败", "path", path, "cause", uerr)
			return nil, fmt.Errorf("解析配置 %s: %w", path, uerr)
		}
		// B229 §2.6：ledger.enabled 键退休——字段与键保留（KnownFields 严格解析
		// 不炸存量 config），值已无任何语义。这里按「键是否存在」告警而不是按值：
		// 退休要提醒的是写过这个开关的机器（配了却无效必须可见），没写过的机器
		// 不该被一个自己从未用过的开关天天打扰。指针探测 nil 与显式 false。
		var retiredProbe struct {
			LedgerCfg struct {
				Enabled *bool `yaml:"enabled"`
			} `yaml:"ledger"`
		}
		if perr := yaml.Unmarshal(b, &retiredProbe); perr == nil && retiredProbe.LedgerCfg.Enabled != nil {
			log().Warn("ledger.enabled 已退休：纪律块入库后账本是必需品，该键已忽略", "path", path)
		}
	}
	applyComputedDefaults(cfg)
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
	if !cfg.PlatformInvariantsEnabled() {
		log().Warn("平台不变量已通过显式配置关闭", "path", path, "key", "platform_invariants")
	}
	if cfg.Relay != nil {
		log().Info("relay egress configured", "url", cfg.Relay.URL, "node", cfg.Relay.Node)
	}
	if cfg.Proxy != "" {
		// 只打脱敏值：代理 URL 常含 user:pass@（envfile/resolver.go:64 同款纪律）
		log().Info("已配置出网代理", "proxy", proxycfg.Redact(cfg.Proxy))
	}
	// 剥过 update 就回写一次，磁盘立刻干净。回写失败不得阻断启动：
	// 内存里已经没有这个字段，agentd 能跑；拦下来等于为一次清垃圾
	// 把整台机器卡死在升级后的第一秒。
	if stripped && !firstRun {
		if werr := save(path, cfg); werr != nil {
			log().Error("删除废弃 update 段后回写失败", "path", path, "cause", werr)
		} else {
			log().Info("已从配置文件删除废弃 update 段", "path", path)
		}
	}
	return cfg, nil
}

// newDefaultConfig 构造出厂默认配置（不落盘）。
//
// 初始字面量预置默认值，yaml 覆盖式解码：配置里没写的键保持默认
// （如 approver.timeout=60s、executor.default=opencode、terminal.auto=false），
// 写了的键覆盖——而非「只读显式配置，其余为空」导致默认值丢失。
func newDefaultConfig() *Config {
	return &Config{
		Listen: "127.0.0.1:7777", DataDir: defaultDataDir(), StallTimeout: 2 * time.Hour,
		Approver: ApproverConfig{Timeout: 60 * time.Second},
		Executor: ExecutorConfig{Default: "opencode"},
		Terminal: TerminalConfig{Auto: false},
		Sync:     SyncConfig{Auto: true},
		ProcFence: ProcFenceConfig{
			// 默认值放初始字面量而不是兜底：ReserveRatio 的兜底是「越界就取默认」，
			// 但 TaskBudget/TaskHardLimit 的 0 是「关掉这一档」的合法表达，用同样的
			// 兜底会把显式写的 0 改回默认值。初始字面量配合覆盖式解码：省略时保持
			// 默认（400/1200），显式写 0 覆盖为 0。
			ReserveRatio: 0.1, TaskBudget: 400, TaskHardLimit: 1200,
		},
		Targets:    map[string]Target{},
		Env:        map[string]string{},
		Discipline: map[string]string{},
	}
}

// applyComputedDefaults 补算依赖实际值才能定的默认项，必须在解码之后调用：
// repo_root 派生自 DataDir 与 ProcFence 的越界兜底都依赖已解析的配置内容。
func applyComputedDefaults(cfg *Config) {
	// repo_root 的默认值必须在解码之后补，不能预置在初始字面量里：
	// 它派生自 DataDir，而 DataDir 本身可能被配置文件改写。
	//
	// 为什么必须有默认值：自动登记（B62）把 clone 变成首次派发的主路径，
	// repo_root 为空时 agentd 会直接拒绝 clone，新开发机上第一次派发必然失败。
	// 落盘之后就固定，此后改 datadir 不会静默改克隆落点。
	if cfg.RepoRoot == "" {
		cfg.RepoRoot = filepath.Join(cfg.DataDir, "repos")
		log().Debug("repo_root 未配置，采用默认落点", "repo_root", cfg.RepoRoot)
	}
	// 保留比缺省 0.1：不写配置的用户也应该被围栏保护，默认必须在安全侧
	if cfg.ProcFence.ReserveRatio <= 0 || cfg.ProcFence.ReserveRatio >= 1 {
		cfg.ProcFence.ReserveRatio = 0.1
	}
	// 负数是配置写错，归零 = 关掉这一档；0 本身是合法的「关掉」，原样保留。
	// 注意与 ReserveRatio 的兜底不同：那个用「越界就取默认」是因为 0 对它无意义，
	// 而 TaskBudget/TaskHardLimit 的 0 是「不启用该档」的显式表达，不能改回默认。
	if cfg.ProcFence.TaskBudget < 0 {
		cfg.ProcFence.TaskBudget = 0
	}
	if cfg.ProcFence.TaskHardLimit < 0 {
		cfg.ProcFence.TaskHardLimit = 0
	}
	// 硬上限低于告警线是自相矛盾的配置（还没告警就先杀了），抬到告警线。
	// 只在两档都启用时校正——有一档是 0 说明用户刻意只要另一档
	if cfg.ProcFence.TaskBudget > 0 && cfg.ProcFence.TaskHardLimit > 0 &&
		cfg.ProcFence.TaskHardLimit < cfg.ProcFence.TaskBudget {
		cfg.ProcFence.TaskHardLimit = cfg.ProcFence.TaskBudget
	}
}

// Defaults 返回一份「出厂默认 + 随机 token」的配置，**不落盘**。
//
// 与 Load 的分工：Load 在文件不存在时会生成 token 并把默认配置写盘（firstRun），
// 这是给 CLI/agentd 用的。桌面壳的首次引导不能调 Load——向导问答中途崩溃、
// 被杀或取消时，磁盘上绝不允许出现会让 Resolve 判为「已配置」的 config.yaml
// （SIGKILL 实测原样复现过这个坑，且回滚法依赖进程还活着，封不死）。Defaults
// 只构造内存里的配置，落盘由调用方在问答成功后一次性 config.Save。
func Defaults() *Config {
	cfg := newDefaultConfig()
	cfg.Token = randToken()
	applyComputedDefaults(cfg)
	if verr := cfg.validate(); verr != nil {
		// 出厂默认必然合法；真走到这里说明默认字面量写错了
		panic("config: 出厂默认校验失败: " + verr.Error())
	}
	return cfg
}

// validate 校验取值域，把「能解析但必然误动作」的配置挡在启动之前。
//
// 为什么 stalltimeout 必须为正：它是「running 任务多久没动静就算卡住」的阈值。
// 写成 0 或负数时，看门狗会在**每个** running 任务的首个 tick 上判定 stalled，
// 协调者被一批凭空的 stalled 事件叫醒，而任务其实好好的。省略该键走默认值
// （2h）是正常用法，只有显式写了非正值才是配置错误。
func (c *Config) validate() error {
	if c.StallTimeout <= 0 {
		return fmt.Errorf("stalltimeout 必须为正时长（当前 %s）；省略该键即用默认 2h", c.StallTimeout)
	}
	// 坏代理必须在启动期硬拒。运行期容错的后果是：后台更新检查那条路径的纪律
	// 是「任何一步失败都静默跳过」（它挂在每条命令上，自己不能成为故障源），
	// 于是一个拼错的代理表现为**什么都不发生**，可以存在数月而无人察觉。
	// 与 approver.blacklist 的正则在启动期编译校验是同一条纪律。
	if err := proxycfg.Validate(c.Proxy); err != nil {
		return err
	}
	if err := c.Relay.Validate(); err != nil {
		return fmt.Errorf("relay 配置校验失败: %w", err)
	}
	for name, target := range c.Targets {
		if err := target.Validate(); err != nil {
			return fmt.Errorf("target %q 校验失败: %w", name, err)
		}
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
		return fmt.Errorf("配置包含未知字段（支持: listen/token/datadir/repo_root/path_dirs/proxy/env_forward/stalltimeout/relay{url,credential,node}/targets{addr,user,token,relay,credential,node}/ledger{enabled,dsn}/approver{executor,model,timeout,blacklist}/executor{default,model}/terminal{auto}/sync{auto}/proc_fence/env{<agent>: <文件名>}/discipline{<executor>: <文件名>}/platform_invariants）: %w；旧版 access_key/secret_key 等键已废弃，请删除未知键或升级配置", err)
	}
	return nil
}

// stripDeprecatedTopLevel 删掉已废弃的顶层键，返回剥过的 yaml 和是否剥到了东西。
//
// 为什么在 KnownFields 之前做：v0.1.x 写过 update 段的机器升级后，直接严格
// 解码会拒启动。剥掉再解码，旧文件能起，其它未知键仍硬拒。只剥顶层，
// 不走进 targets / env，避免误伤嵌套里碰巧叫 update 的键。
func stripDeprecatedTopLevel(b []byte) (out []byte, stripped bool, err error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return b, false, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, false, err
	}
	// yaml.Unmarshal 通常得到 DocumentNode，真正的 mapping 在 Content[0]。
	mapping := &root
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return b, false, nil
		}
		mapping = root.Content[0]
	}
	if mapping.Kind != yaml.MappingNode {
		return b, false, nil
	}
	stripped = removeMapKey(mapping, "update")
	if !stripped {
		return b, false, nil
	}
	out, err = yaml.Marshal(&root)
	return out, true, err
}

// removeMapKey 从 MappingNode 顶层切掉名为 key 的那一对（Content 是
// key/value 交错）。只动这一层，不递归。
func removeMapKey(n *yaml.Node, key string) bool {
	if n == nil || n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return true
		}
	}
	return false
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
