// 本文件实现 handoff agentd 子命令：加载配置、初始化统一日志、打开 SQLite 存储、
// 构建 HTTP/WS 服务并监听。agentd 是本机/配对主机上的长驻服务，是任务的执行入口。
//
// 职责：
//   - 按序完成 bootstrap：config.Load → logx.Setup + slog.SetDefault →
//     pathenv.Apply（PATH 补全，先于一切 fork 子进程）→ store.Open → agentd.NewServer
//   - 对外服务前做启动恢复（RecoverOnStartup）：探活未终结任务的执行器，重建订阅或转 failed
//   - 启动任务卡住看门狗 goroutine（RunWatchdog），长时间无事件产出触发 stalled 唤醒协调者
//   - 监听配置中的 Listen 地址，进程生命周期与 HTTP server 一致
//   - 经 agentd.Shutdown 提供优雅关停：SIGINT/SIGTERM 停收新连接 → 等在途请求
//     → 停看门狗 → 关库 → 放锁；正常关停 exit 0，供进程管理器据此拉起新版
//
// 边界：
//   - 不创建任务/工单：任务生命周期由 manager 驱动（executor 按 --executor 挂载）
//   - 不决定何时停机：信号与进程内触发都汇到 agentd.Shutdown，本文件只接线
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgermirror"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/pathenv"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/toolchain"
	"github.com/spf13/cobra"
)

// agentdCmd 启动本地 agentd 服务。
//
// 注意：
//   - logx.Setup 之后必须立即 slog.SetDefault：hub 在 NewServer 构造时捕获 slog.Default()
//     （见 agentd.NewHub），顺序颠倒会让 hub 的日志落在初始默认 logger 上
var agentdCmd = &cobra.Command{
	Use:   "agentd",
	Short: "启动 agentd 服务（HTTP API + WS 事件流）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		p := configPath
		if p == "" {
			p = config.DefaultPath()
		}
		cfg, err := config.Load(p)
		if err != nil {
			return fmt.Errorf("加载配置 %s: %w", p, err)
		}

		logger := logx.Setup("agentd", filepath.Join(cfg.DataDir, "agentd.log"))
		// hub 构造时取 slog.Default()，必须先于 NewServer 生效，才能让 hub/store/config
		// 的全部日志统一走 logx 的「JSON 文件 + stderr 文本」双路输出
		slog.SetDefault(logger)
		if cfg.Relay != nil {
			// 弱 token 经公网 relay 可被离线爆破；relay 出站因此必须
			// fail-closed，不能带着低熵凭证启动。
			if err := validateRelayToken(cfg); err != nil {
				logger.Error("relay requires high-entropy token", "cause", err)
				return fmt.Errorf("relay token 熵校验失败: %w", err)
			}
			logger.Info("relay egress enabled", "url", cfg.Relay.URL, "node", cfg.Relay.Node)
		} else {
			logger.Info("relay egress not configured")
		}

		// 围栏策略必须在任何 executor 被拉起之前注入：Start 算 L 时读的就是
		// 这些包级值，晚一步就会有任务在默认策略下开工
		prochost.SetFencePolicy(cfg.ProcFence.Disabled, cfg.ProcFence.ReserveRatio,
			cfg.ProcFence.TaskHardLimit)
		if supported, reason := prochost.MarkCapability(); supported {
			logger.Info("任务标记归属可用，进程归属不依赖采样时机")
		} else {
			logger.Warn("任务标记归属不可用，进程归属退回其它判据",
				"reason", reason,
				"fallback", prochost.FallbackKind(),
				"note", "Windows 上这是预期形态：回收由 Job Object 进程容器承担")
		}

		// PATH 补全（B7 + B71）：agentd 常由非登录 shell 或进程管理器拉起，
		// 拿到的 PATH 可能只有 /usr/bin:/bin:/usr/sbin:/sbin。必须早于任何
		// fork 子进程的动作，合并结果才能被 executor/审批者/审阅命令继承。
		pathenv.Apply(context.Background(),
			pathenv.Options{IncludeLoginShell: true, ExtraDirs: cfg.PathDirs}, logger)

		// 启动自检（B71）：补全之后立刻报一次四家的解析结果。不报的话，
		// 「opencode 没找到」要等到第一次派发才暴露，那时离根因（PATH）已经很远
		logExecutorDetection(logger, cfg.Executor.Default, toolchain.Detect())

		// systemd KillMode 自检（拆 tmux 后的部署硬要求）：setsid 不脱离 cgroup，
		// KillMode 非 process 时 agentd 重启会连坐执行者。只提示不阻断；非 systemd
		// 环境（macOS 开发机）完全静默。
		agentd.WarnIfKillModeUnsafe(logger)

		// DataDir 首次运行可能不存在（config.Load 只保证配置目录），
		// store.Open 与 taskDir 创建都依赖它，必须先建
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
		}

		// 单实例锁（B34）：一个 DataDir 同时只接纳一个 agentd。位置被三个约束
		// 同时定死，改动前务必读懂：
		//   - 必须在 MkdirAll 之后：首次运行 DataDir 还不存在，锁文件没处放
		//   - 必须在 store.Open 之前：那是「碰数据」的第一步，也是唯一能保证
		//     撞锁时什么都没动过的位置
		//   - 必须在 logx.Setup 之后：否则撞锁失败的日志无处可去
		//
		// 为什么不能指望端口冲突挡住第二个 agentd：ListenAndServe 是本函数
		// **最后**一条语句，在它之前 RecoverOnStartup 已经对在役 agentd 的活
		// 执行器重建了订阅并写入状态迁移；store.Open 开了 WAL 也不拦多进程
		// 打开。破坏发生在撞端口之前，所以锁必须卡在这里。
		lock, err := agentd.AcquireDataDirLock(cfg.DataDir, logger)
		if err != nil {
			// 不再包一层：撞锁时 err 本身就是一段完整的可行动指引，
			// 前面再缀「启动 agentd 失败:」只会把重点冲淡
			return err
		}
		defer lock.Release()

		st, err := store.Open(filepath.Join(cfg.DataDir, "handoff.db"))
		if err != nil {
			return fmt.Errorf("打开存储: %w", err)
		}
		defer st.Close()

		// env 注入（B19）：agent 启动时注入 <DataDir>/env/ 下配置的环境变量文件。
		// 启动预检只 WARN 不阻断——env 文件是数据文件，可能在 agentd 启动后才创建；
		// 但完全不检查会把问题拖到第一次派发才暴露，预检让它在启动日志里就可见。
		// manager 侧自建同款 resolver（NewManager 内），两者无状态、不会发散。
		envRes := envfile.NewResolver(envfile.Dir(cfg.DataDir), envfile.Static(cfg.Env), logger)
		envRes.Preflight()

		// 审批链接线：配置启用了 approver 时构造裁决器；黑名单正则等配置错误
		// 直接启动失败（属配置错误，改配置重启即可，不该带病运行）
		ap, err := agentd.NewApprover(cfg.Approver, envRes, logger)
		if err != nil {
			return fmt.Errorf("初始化审批链: %w", err)
		}
		// 判据网关必须构造，且与审批者是否启用无关：AutoAllow 是第 0 层静态
		// 判据，漏了它，未配置审批者的部署会被工作区内的每次写入淹没
		gate, err := permgate.New(cfg.Approver.Blacklist, logger)
		if err != nil {
			return fmt.Errorf("构造权限判据网关: %w", err)
		}

		// git 出网代理必须在任何 clone/fetch 之前注入。放在 NewServer 之前而不是
		// 之后：自动登记（B62）的 clone 可能在服务起来后的第一个请求就发生
		agentd.SetGitProxy(cfg.Proxy)
		if cfg.Proxy != "" {
			logger.Info("git 出网将使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
		}

		srv := agentd.NewServer(cfg, st, logger)
		srv.SetConfigPath(p)
		if err := srv.ReclaimPtySessions(); err != nil {
			// PTY 是附属能力，扫描失败不应阻断任务派发主服务；broken 条目由日志留给人工处理。
			logger.Error("启动时认领 PTY 会话失败，继续提供服务", "err", err)
		}
		// 支持的执行者都注册：dispatch --executor 可按名选择；opencode/claude/grok/codex
		// 是真实执行，fake 用于演示/测试。Windows 由 adaptersFor 裁剪掉未实现的两家，
		// 缺省由 cfg.Executor.Default 决定（--executor flag 覆盖）
		ads := defaultAdapters(logger)
		if executorFlag != "" {
			if _, ok := ads[executorFlag]; !ok {
				if runtime.GOOS == "windows" {
					return fmt.Errorf("未知 executor %q（Windows 支持 opencode/codex/fake）", executorFlag)
				}
				return fmt.Errorf("未知 executor %q（支持 opencode/claude/grok/codex/fake）", executorFlag)
			}
			// --executor 语义是「覆盖缺省执行者」：只改 cfg 的缺省名，注册表保持
			// 全部可用——老任务按各自 executor 名仍能路由到对应 adapter
			cfg.Executor.Default = executorFlag
		}

		// 缺省执行者是 codex 时做硬预检：codex 复用用户级 ~/.codex（spec §1.3），
		// 未装/未登录会让每个任务都在回合中途失败，诊断成本远高于启动时挡一下。
		// 非缺省时不阻断——注册表保留全部 adapter，一台只跑 opencode 的机器不该
		// 因为没装 codex 就起不来。
		if cfg.Executor.Default == "codex" {
			if err := codex.Preflight("", logger); err != nil {
				return fmt.Errorf("codex 环境预检未通过: %w", err)
			}
		}
		mgr := agentd.NewManager(st, srv.Hub(), ads, cfg, srv.EnvMapping, ap, gate, logger)
		srv.SetManager(mgr)
		// 任务级进程点名（B93 §3.2）：watchdog 的 scanTaskProcs 按任务数进程，
		// 生产计数实现恒为 Manager.TaskProcCount（与 sweep 的 mgr.SweepTaskProcs 同款接线）
		agentd.SetTaskProcCounter(mgr.TaskProcCount)

		// 启动恢复（spec §8）：在对外服务前，把 agentd 崩溃前未终结的任务拉回正轨——
		// 执行器存活的任务经 mgr.ResumeTask 重建 SSE 订阅并重启中介循环，已不在的
		// 任务转 failed/waiting_review 交协调者裁决。探活与「重建订阅」封装在同一个
		// 闭包里（watchdog.go RecoverOnStartup 的 seam 说明），此处即其接线点
		if err := agentd.RecoverOnStartup(st, srv.Hub(), mgr.ResumeTask, mgr.SweepTaskProcs, logger); err != nil {
			return fmt.Errorf("启动恢复: %w", err)
		}
		// 看门狗随停机一起收：以前挂在 context.Background() 上靠进程退出终止，
		// 有了优雅关停之后必须显式取消，否则关停期间它还在扫任务、写事件，
		// 而数据库正要被关掉
		wdCtx, wdCancel := context.WithCancel(context.Background())
		defer wdCancel()
		if cfg.Relay != nil {
			// relay 出站与本地 listen 并存，是附加通道；不改变现有 agentd 路由。
			listener := relay.NewListener(cfg.Relay.URL, cfg.Relay.Credential, cfg.Relay.Node,
				cfg.Token, "", srv.Handler(), logger)
			go listener.RunWithReconnect(wdCtx)
		}
		// relay 隧道预热：探活只有 3s 预算，首次建隧道（WSS + CONNECT + E2E）
		// 装不进去。预热用独立超时提前备好，让探活只花在 /api/status 上。
		go srv.Pool().Warm(wdCtx)
		logger.Info("relay 隧道预热已启动")
		// wdStart 是失配对账扫描的启动时刻护栏：只对本次启动之后的事件判失配
		//（B100 之前的历史 failed+waiting_review 是合法的，见 mismatchVerdict）。
		// 在启动看门狗前取——启动恢复可能已把若干任务迁进终态，取早于它们的时刻
		// 会让这些合法的迁移在首轮就被误判成失配
		wdStart := time.Now()
		go agentd.RunWatchdog(wdCtx, st, srv.Hub(), cfg.StallTimeout,
			cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, mgr.ForceReclaim,
			wdStart, agentd.MismatchScanMinAge, mgr.MismatchTransit(), logger)

		// 恒启动：镜像的机器清单现在来自活快照，启动时没有机器不代表以后没有。
		// 留着 len>0 的闸会让控制台新增的第一台机器永远等不到镜像。
		mirror := agentd.NewMirror(srv.Pool(), st, srv.Hub(), logger)
		go mirror.Run(wdCtx)
		logger.Info("事件镜像已启动", "targets", len(srv.Pool().Names()), "tick", "30s",
			"note", "运行期新增的机器无需重启")

		// 账本域是必需品（B229 §2.6：enabled 开关已退休，配置里的键被忽略）：
		// 恒开库恒挂镜像。dsn 空 = DataDir/ledger.db 单机回退；web 侧靠
		// /api/ledger/health 拿到 enabled:true 后渲染入口。
		stopLedger, err := setupLedger(cfg, srv, wdCtx, logger)
		if err != nil {
			return err
		}
		defer stopLedger()

		// B85：listen 绑单网卡 IP 时追加 loopback 辅助监听，本机 CLI 恒走 127.0.0.1
		//（spec §3.2）。任一地址绑不上都启动失败——辅助监听与主监听同等对待
		listenAddrs := []string{cfg.Listen}
		var listenAux string
		if cls, aux := config.ClassifyListen(cfg.Listen); cls == config.ListenSingle {
			listenAux = aux
			listenAddrs = append(listenAddrs, aux)
		}
		startAttrs := []any{"addr", cfg.Listen, "data_dir", cfg.DataDir, "default_executor", cfg.Executor.Default,
			"proc_fence_disabled", cfg.ProcFence.Disabled,
			"proc_fence_reserve_ratio", cfg.ProcFence.ReserveRatio,
			"proc_fence_task_budget", cfg.ProcFence.TaskBudget,
			"proc_fence_task_hard_limit", cfg.ProcFence.TaskHardLimit}
		// 无辅助监听时不打 listen_aux 字段：两档常规配置的启动日志保持不变
		if listenAux != "" {
			startAttrs = append(startAttrs, "listen_aux", listenAux)
		}
		logger.Info("agentd 服务启动", startAttrs...)

		// 优雅关停：收到 SIGINT/SIGTERM（或进程内 Trigger）后停收新连接、
		// 等在途请求、再按序收尾。返回 nil = exit 0，systemd Restart=always /
		// launchd KeepAlive 据此把新进程拉起来。
		//
		// cleanup 的顺序是有讲究的，不要调换：先停看门狗（它会写库），
		// 再关库，最后放锁（放早了别的 agentd 会在库还开着时进来）。
		// store.Close 与 lock.Release 上面已有 defer，这里不重复调用——
		// defer 在 RunE 返回后仍会执行，顺序是 lock.Release 后于 st.Close，
		// 正是我们要的。
		sd := agentd.NewShutdown(logger)
		// 换版接口靠它退出进程，交接给进程管理器拉起的新二进制
		srv.SetRestart(sd.Trigger)
		// 两侧都要保留：main 侧把 wdCancel 包进了 PTY 感知的优雅关停清理，
		// 本分支要在 Serve 返回后关掉 target 客户端池（含 relay 隧道）。
		err = sd.Serve(newAgentdHTTPServer(cfg.Listen, srv.Handler()),
			srv.GracefulShutdownCleanup(wdCancel), listenAddrs...)
		if closeErr := srv.CloseTargets(); closeErr != nil {
			logger.Warn("关闭 target 客户端池失败", "cause", closeErr)
		}
		return err
	},
}

// validateRelayToken keeps the relay startup gate small and directly testable.
// Direct-only agentd configurations intentionally bypass this check.
func validateRelayToken(cfg *config.Config) error {
	if cfg == nil || cfg.Relay == nil {
		return nil
	}
	return relay.CheckTokenEntropy(cfg.Token)
}

// logExecutorDetection 把四家 executor 的探测结果成表写进启动日志，并对
// 「默认执行者没找到」打一条带处置的 WARN。
//
// 参数：
//   - log: 日志入口
//   - defaultExecutor: cfg.Executor.Default
//   - rs: toolchain.Detect() 的结果
//
// 注意：
//   - **不阻断启动**：一台机器不该因为少装一个 executor 就彻底起不来；托管形态下
//     启动失败还会变成崩溃循环。codex 那条硬预检拦的是更窄的判据，两者不冲突
//   - defaultExecutor 是 fake 时不会命中任何一条（fake 不在 Detect 的四家里），
//     于是自然不告警——它是脚本演示执行者，本来就没有对应的二进制
func logExecutorDetection(log *slog.Logger, defaultExecutor string, rs []toolchain.Result) {
	attrs := make([]any, 0, len(rs)*2)
	for _, r := range rs {
		v := r.State.String()
		if r.Path != "" {
			// 路径是排障时唯一有用的信息：它直接回答「补全到底有没有生效」
			v += "  " + r.Path
		}
		attrs = append(attrs, r.Name, v)
	}
	log.Info("executor 探测", attrs...)

	for _, r := range rs {
		if r.Name != defaultExecutor {
			continue
		}
		if r.State == toolchain.StateMissing {
			log.Warn("默认执行者未找到，派发到本机的任务会失败",
				"executor", r.Name,
				"处置", "在本机装上它，或把它所在目录写进 config.yaml 的 path_dirs")
		}
		return
	}
}

// defaultAdapters 返回 agentd 的 executor 注册表（name → Adapter）。
//
// 抽成函数而非内联字面量：注册表是 dispatch --executor 路由的唯一真相，
// 漏注册的症状是「派发时报未注册」而不是编译错误，值得一条断言守着
// （见 agentd_test.go 的 TestAdapterRegistryHasAlwaysAvailableExecutors）。
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return adaptersFor(runtime.GOOS, logger)
}

// claude 从 B128 起在所有平台注册：Windows 的输入通道（inputch_windows.go）
// 与裁决 socket（AF_UNIX，Windows 原生支持）都已落地。
// adaptersFor 按平台能力构造执行器注册表。
//
// 参数：goos 为目标平台；logger 用于播报不注册的理由
//
// 返回：可用执行器的注册表。
//
// 注意：
//   - **不注册必须有明确理由并打日志**：静默缺席会让用户以为是配置问题，
//     而 dispatch 在门口被拒时只知道「没这个执行器」
//   - grok 走**运行期能力探测**而不是按平台写死：它卡的是符号链接权限，
//     而那是部署形态决定的（管理员 / 开发者模式），同一个 Windows 上装法
//     不同结论就不同。写死等于把一台其实可用的机器判成不可用
func adaptersFor(goos string, logger *slog.Logger) map[string]executor.Adapter {
	return adaptersForWithProbe(goos, logger, os.TempDir())
}

// adaptersForWithProbe 是 adaptersFor 的可测形态：探测目录由调用方给。
func adaptersForWithProbe(goos string, logger *slog.Logger, probeDir string) map[string]executor.Adapter {
	ads := map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"codex":    codex.New(logger),
		"claude":   claudecode.New(logger),
		"fake":     fake.New(nil),
	}
	if supported, reason := grok.SymlinkCapability(probeDir); supported {
		ads["grok"] = grok.New(logger)
	} else {
		logger.Warn("不注册 grok：本机不具备创建符号链接的能力",
			"reason", reason,
			"note", "grok 用软链让 auth 文件只有一份权威副本，改成复制会让用户那份与任务那份静默漂移")
	}
	_ = goos // 平台不再直接决定注册面，保留参数是为了不改调用方与既有测试
	return ads
}

// newAgentdHTTPServer 构造 agentd 的 HTTP 服务监听（独立成函数以便测试断言超时配置）。
//
// 各超时值的 why：
//   - ReadHeaderTimeout 10s：请求头读取上限——防 slowloris（慢速请求头占满连接
//     goroutine）；配合 IdleTimeout 保证半死连接被回收
//   - ReadTimeout 30s：请求体读取上限（reply/fetch 等请求体都很小，30s 充足）。
//     只作用于请求头/体的读取，不约束 handler 执行时长，无需随 run 上限放大
//   - WriteTimeout 11min（= agentd.RunCmdTimeout + 1min 余量）：响应写入上限，
//     **必须** ≥ run 路由的执行上限——handleTaskRun 在 handler 内同步执行 RunCmd
//     （最长 RunCmdTimeout=10min），net/http 的 WriteTimeout 在 handler 执行前设下
//     deadline、响应写完前不重置，若小于命令执行上限，跑测试/lint 的审阅命令
//     （经常超 60s）会在 60s 时被掐断连接，RunCmd 随 r.Context() 取消被提前杀掉，
//     退出码 124 的文档化契约永远无法兑现。dispatch 链路的病态时长（StartServe≤10s
//     叠加 CreateSession 30s 与 PromptAsync 30s ≈ 70s）也在该上限内。对 hijacked 连接
//     （WS 事件流）**不生效**：net/http 在 Hijack 时清除连接上的全部截止时间
//     （server.go hijackLocked 里 rwc.SetDeadline(time.Time{})，实测 Go 1.26 行为），
//     coder/websocket 的 Accept 走 hijack——长连接不受该值约束
//   - IdleTimeout 120s：keep-alive 空闲连接回收，防连接池被死连接占满
func newAgentdHTTPServer(listen string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      agentd.RunCmdTimeout + time.Minute,
		IdleTimeout:       120 * time.Second,
	}
}

// executorFlag 覆盖 cfg.Executor.Default：opencode（默认，真实执行）| claude | grok |
// codex | fake（脚本演示）；Windows 上 grok 是否注册取决于符号链接能力探测。
var executorFlag string

func init() {
	rootCmd.AddCommand(agentdCmd)
	agentdCmd.Flags().StringVar(&executorFlag, "executor", "",
		"覆盖默认执行者：opencode（默认）| claude | grok | codex | fake（注册表保留全部，dispatch --executor 仍可按名选择）")
}

// setupLedger 打开账本库并挂载镜像子系统。
//
// B229 §2.6：ledger.enabled 开关退休，账本变必需品——本函数恒执行，
// 不再看任何开关位。dsn 空 = DataDir/ledger.db 单机回退。
//
// 参数：cfg 取 Ledger.DSN 与 DataDir；srv 接收 SetLedger 注入并出镜像
// 所需的 target 客户端池；ctx 是镜像生命周期（随 agentd 停机取消）；
// logger 为启动日志入口。
//
// 返回：stop 必须由调用方 defer，且保证先于账本库 Close 执行——订阅
// 回调在写库，Stop 先于 Close 是硬约束（本函数把 Close 一并收进 stop，
// 调用方只需 defer stop() 一个动作）。
func setupLedger(cfg *config.Config, srv *agentd.Server, ctx context.Context,
	logger *slog.Logger) (func(), error) {
	ldsn := cfg.Ledger.DSN
	if ldsn == "" {
		ldsn = filepath.Join(cfg.DataDir, "ledger.db")
	}
	lst, err := ledger.Open(ldsn)
	if err != nil {
		logger.Error("打开账本库失败", "dsn", ldsn, "cause", err)
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	srv.SetLedger(lst)
	// 恒挂载：机器清单来自 target 客户端池的活配置读取，启动时没有机器
	// 不代表以后没有——留着 len(cfg.Targets)>0 的闸会让控制台新增的第一台
	// 机器永远等不到账本镜像（与任务镜像同一条纪律，B163 ①）。
	// 池必须与任务镜像共用同一个：两个池等于两套 relay 隧道。
	host, _ := os.Hostname()
	lm := ledgermirror.New(lst, srv.Pool(), ledgermirror.Options{Holder: host})
	go lm.Run(ctx)
	logger.Info("账本镜像子系统已挂载", "holder", host,
		"machines", len(srv.Pool().Names()), "dsn", ldsn)
	return func() {
		// 次序硬约束：先停镜像（不再写库）再关账本库。
		lm.Stop()
		lst.Close()
	}, nil
}
