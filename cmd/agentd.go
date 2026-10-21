// 本文件实现 handoff agentd 子命令：加载配置、初始化统一日志、打开 SQLite 存储、
// 构建 HTTP/WS 服务并监听。agentd 是本机/配对主机上的长驻服务，是任务的执行入口。
//
// 职责：
//   - 按序完成 bootstrap：config.Load → logx.Setup + slog.SetDefault →
//     pathenv.Apply（PATH 补全，先于一切 fork 子进程）→ store.Open → agentd.NewServer
//   - 对外服务前做启动恢复（RecoverOnStartup）：探活未终结任务的执行器，重建订阅或转 failed
//   - 启动任务卡住看门狗 goroutine（RunWatchdog），长时间无事件产出触发 stalled 唤醒协调者
//   - 监听配置中的 Listen 地址，进程生命周期与 HTTP server 一致
//   - 经 orchestration.Shutdown 提供优雅关停：SIGINT/SIGTERM 停收新连接 → 等在途请求
//     → 停看门狗 → 关库 → 放锁；正常关停 exit 0，供进程管理器据此拉起新版
//
// 边界：
//   - 不创建任务/工单：任务生命周期由 manager 驱动（executor 按 --executor 挂载）
//   - 不决定何时停机：信号与进程内触发都汇到 orchestration.Shutdown，本文件只接线
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/agy"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgermirror"
	"github.com/Xsxdot/handoff/internal/logx"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/pathenv"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
	"github.com/Xsxdot/handoff/internal/toolchain"
	"github.com/Xsxdot/handoff/internal/workspace"
	"github.com/spf13/cobra"
)

// agentdCmd 启动本地 agentd 服务。
//
// 注意：
//   - logx.Setup 之后必须立即 slog.SetDefault：hub 在 NewServer 构造时捕获 slog.Default()
//     （见 orchestration.NewHub），顺序颠倒会让 hub 的日志落在初始默认 logger 上
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
		orchestration.WarnIfKillModeUnsafe(logger)

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
		// 为什么不能指望端口冲突挡住第二个 agentd：绑端口（启动闸）在 store.Open、
		// 审批链与执行器装配之后，store.Open 开了 WAL 也不拦多进程打开；端口
		// 只是第二道闸，碰数据之前的唯一保证是这把锁，所以锁必须卡在这里。
		lock, err := orchestration.AcquireDataDirLock(cfg.DataDir, logger)
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
		// 直接启动失败（属配置错误，改配置重启即可，不该带病运行）。
		// B437 §4.2：共享挂载机（有 DSN 且有机器身份）即使本地 approver 为空也
		// 恒构造 Approver——否则共享链在「本地链为空」的机器上永远走不到（启用门缺口）。
		sharedApproverMachine := cfg.Ledger.DSN != "" && cfgHasLedgerIdentity(cfg)
		var ap *orchestration.Approver
		if sharedApproverMachine {
			ap, err = orchestration.NewApproverForShared(cfg.Approver, envRes, logger)
		} else {
			ap, err = orchestration.NewApprover(cfg.Approver, envRes, logger)
		}
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
		workspace.ConfigureGitNet(workspace.GitNetConfig{
			Argv:   proxycfg.GitArgs(cfg.Proxy),
			Redact: proxycfg.Redact(cfg.Proxy),
		})
		workspace.SetForkFailureNote(prochost.ExplainForkFailure)
		workspace.SetProcHeadroom(orchestration.CheckProcHeadroom)
		if cfg.Proxy != "" {
			logger.Info("git 出网将使用代理", "proxy", proxycfg.Redact(cfg.Proxy))
		}

		srv := agentd.NewServer(cfg, st, logger)
		srv.SetConfigPath(p)
		srv.SetDesktopOpener(agentd.PlatformDesktopOpener(runtime.GOOS))
		previewHub := workspace.NewPreviewHub(logger)
		// B233.19：owner truth 在 internal/workspace；静态 HTTP 服务（gateway 面）
		// 与本机 store 行适配器由本组装点注入。
		previewOwner := workspace.NewPreviewOwner(agentd.NewPreviewStore(st), previewHub,
			workspace.PreviewOwnerDeps{Static: agentd.NewPreviewStaticServer(logger)}, logger)
		previewMirror := agentd.NewPreviewMirror(srv.Pool(), previewOwner, previewHub, srv.IsSelfTarget, logger)
		previewOpener := agentd.NewPreviewOpenService(previewOwner, previewMirror, srv.Pool(), nil, logger)
		srv.SetPreviewOwner(previewOwner)
		srv.SetPreviewMirror(previewMirror)
		srv.SetPreviewOpener(previewOpener)
		if err := srv.ReclaimPtySessions(); err != nil {
			// PTY 是附属能力，扫描失败不应阻断任务派发主服务；broken 条目由日志留给人工处理。
			logger.Error("启动时认领 PTY 会话失败，继续提供服务", "err", err)
		}
		// 支持的执行者都注册：dispatch --executor 可按名选择；opencode/claude/grok/codex/agy
		// 是真实执行，fake 用于演示/测试。Windows 由 adaptersFor 裁剪掉未实现的两家，
		// 缺省由 cfg.Executor.Default 决定（--executor flag 覆盖）
		ads := defaultAdapters(logger)
		if ap != nil {
			if err := bindApproverOneShot(ap, ads, cfg.Approver.Executor, logger); err != nil {
				return fmt.Errorf("绑定审批者 OneShot 失败: %w", err)
			}
		}
		if executorFlag != "" {
			if _, ok := ads[executorFlag]; !ok {
				if runtime.GOOS == "windows" {
					return fmt.Errorf("未知 executor %q（Windows 支持 opencode/claude/codex/agy/fake）", executorFlag)
				}
				return fmt.Errorf("未知 executor %q（支持 opencode/claude/grok/codex/agy/fake）", executorFlag)
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
		srv.SetProviders(orchestration.RegistryFromAds(ads))
		srv.SetRuleLoader(loadCarrierRules)
		mgr := orchestration.NewManager(st, srv.Hub(), ads, cfg, srv.EnvMapping, ap, gate, logger)
		mgr.SetWorkspace(workspace.NewCapability())
		mgr.SetLiveConfig(srv.Conf()) // B233.13 P1：活配置注入移出 SetManager，改组装点显式接线
		srv.SetManager(mgr)
		// B453 S6/S7：回合撞额度的回调（标账号、只读任务换人重跑）实现在控制门面，组装点唯一接线。
		mgr.SetQuotaHandler(srv)
		// 任务级进程点名（B93 §3.2）：watchdog 的 scanTaskProcs 按任务数进程，
		// 生产计数实现恒为 Manager.TaskProcCount（与 sweep 的 mgr.SweepTaskProcs 同款接线）
		orchestration.SetTaskProcCounter(mgr.TaskProcCount)
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
		sd := orchestration.NewShutdown(logger)
		// 换版接口靠它退出进程，交接给进程管理器拉起的新二进制
		srv.SetRestart(sd.Trigger)

		// 启动闸（2026-10-19 mac-mini 事故）：端口在账本挂载**之前**占住，启动期
		// 一切请求回 503 + 当前阶段（账本重试第几次、原因），handoff status 与对端
		// 看到的是「在、未就绪、卡在哪」而不是 connection refused。真实 handler 在
		// 账本挂载与启动恢复都完成后才经 bootGate.Ready 上线——「恢复先于对外服务」
		// 的约束不变，只是把绑端口提前了。单实例仍由上面的 DataDir 锁保证。
		bootGate := agentd.NewBootGate(logger)
		waitServe, err := sd.Begin(newAgentdHTTPServer(cfg.Listen, bootGate), listenAddrs...)
		if err != nil {
			return err
		}
		// 启动期失败的统一出口：先按优雅关停收掉已占的端口，再把原因带出去
		abortBoot := func(cause error) error {
			sd.Trigger("startup-failed")
			_ = waitServe(func() {})
			return cause
		}
		// 停机信号在启动期到达时取消账本重试（bootCtx 只管启动期，不影响 wdCtx 的收尾次序）
		bootCtx, bootCancel := context.WithCancel(context.Background())
		defer bootCancel()
		go func() {
			select {
			case <-sd.Done():
				bootCancel()
			case <-bootCtx.Done():
			}
		}()

		// 恢复前先挂自动化账本：RecoverOnStartup 依赖 Server.autoLedger，账本尚未
		// 装配时必须拒绝启动，而不是让恢复进入一个不完整的服务状态。启动调度占用
		// 对账（B448 r1）在 setupLedger 内的 StartAutomation 入口完成，失败即 abortBoot。
		wdCtx, wdCancel := context.WithCancel(context.Background())
		defer wdCancel()
		stopLedger, err := setupLedger(cfg, srv, st, wdCtx, logger,
			ledgerBoot{ctx: bootCtx, report: bootGate.SetPhase})
		if err != nil {
			if bootCtx.Err() != nil {
				// 启动期收到停机：按优雅关停退出（exit 0），不是启动失败
				return waitServe(func() {})
			}
			return abortBoot(err)
		}
		bootCancel()
		defer stopLedger()

		// B437：把共享审批链读取口 / 远程裁决口 / 本机环解析缝 / 自名判据注入审批者。
		// 注入在账本挂载之后、对外服务之前——运行期每次裁决都能现读当前链。
		srv.SetApproverEnv(envRes)
		if ap != nil {
			ap.SetSelfMachine(srv.IsSelfMachine)
			ap.SetLocalShot(func(name string) (executor.OneShot, error) {
				ad, ok := ads[name]
				if !ok {
					return nil, fmt.Errorf("执行器 %q 未注册", name)
				}
				shot, ok := ad.(executor.OneShot)
				if !ok {
					return nil, fmt.Errorf("执行器 %q 未实现 OneShot", name)
				}
				return shot, nil
			})
			if srv.LedgerMounted() {
				cr := newLedgerChainReader(srv.AutoLedger())
				ap.SetChainReader(cr)
				ap.SetRemoteDecider(srv.ApproverRemoteDecider())
				warnLocalApproverSuperseded(cr, cfg.Approver, logger)
			}
		}

		// 启动恢复（spec §8）：在对外服务前，把 agentd 崩溃前未终结的任务拉回正轨——
		// 执行器存活的任务经 mgr.ResumeTask 重建 SSE 订阅并重启中介循环，已不在的
		// 任务转 failed/waiting_review 交协调者裁决。探活与「重建订阅」封装在同一个
		// 闭包里（watchdog.go RecoverOnStartup 的 seam 说明），此处即其接线点
		// 账本未挂载（S2 挂载门：有 DSN 缺机器身份）时跳过启动恢复对账——
		// RecoverOnStartup 依赖自动化账本门面，未挂载时它必然失败。agentd 照常
		// 提供服务，账本/镜像/卡派发/调度不可用，日志已给可行动指引。
		if !srv.LedgerMounted() {
			logger.Warn("账本未挂载，跳过启动恢复对账",
				"action", "在 config.yaml 设置 name:（直连形态还需 advertise:）后重启")
		} else if err := srv.RecoverOnStartup(mgr.ResumeTask, mgr.SweepTaskProcs, logger); err != nil {
			return abortBoot(fmt.Errorf("启动恢复: %w", err))
		}
		// B452：只读任务 completed 之后、自动归档之前若 agentd 重启，任务会停在 waiting_review
		// 占着名额与工作树——对外服务前补做归档（终态回调已在 SetManager 时接好）。
		mgr.ArchiveCompletedReadonly()
		// B452 S8：产物目录保留期清扫（启动一次，之后每小时）；保留期每轮现读活配置。
		go mgr.RunArtifactsRetention(wdCtx, func() time.Duration { return srv.Conf()().ArtifactsRetention() }, time.Hour)
		if err := srv.StartPreviewServices(wdCtx); err != nil {
			return abortBoot(fmt.Errorf("启动预览服务: %w", err))
		}
		// B438 S8：打洞是 UDP，proxy 设置与 HTTPS_PROXY 都不承载它——配了代理的机器
		// 启动时明说一次（INFO，不是 WARN：这是知会，不是故障）。
		if cfg.P2PProxyNotice() {
			logger.Info(config.P2PProxyNoticeMessage)
		}
		if cfg.Relay != nil {
			// relay 出站与本地 listen 并存，是附加通道；不改变现有 agentd 路由。
			// 打洞应答（B438）：p2p 开时在 relay 信令会话上应答，直连流交同一 handler。
			p2pOpts := targetclient.P2PFromConfig(cfg)
			listener := relay.NewListener(cfg.Relay.URL, cfg.Relay.Credential, cfg.Relay.Node,
				cfg.Token, "", srv.Handler(), logger, relay.WithListenerP2P(p2pOpts))
			logger.Info("relay 出站已启动", "node", cfg.Relay.Node, "p2p", p2pOpts.Enabled, "stun_servers", len(p2pOpts.STUN))
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
		go orchestration.RunWatchdog(wdCtx, st, srv.Hub(), cfg.StallTimeout,
			cfg.ProcFence.TaskBudget, cfg.ProcFence.TaskHardLimit, mgr.ForceReclaim,
			wdStart, orchestration.MismatchScanMinAge, mgr.MismatchTransit(), logger)

		// 恒启动：镜像的机器清单现在来自活快照，启动时没有机器不代表以后没有。
		// 留着 len>0 的闸会让控制台新增的第一台机器永远等不到镜像。
		mirror := workspace.NewMirror(agentd.NewMirrorTaskSource(srv.Pool()), st, srv.Hub(), srv.IsSelfTarget, logger)
		go mirror.Run(wdCtx)
		logger.Info("事件镜像已启动", "targets", len(srv.Pool().Names()), "tick", "30s",
			"note", "运行期新增的机器无需重启")

		// 启动期结束：账本已挂、启动恢复已做，真实 handler 上线（此前一切请求由启动闸回 503）。
		bootGate.Ready(srv.Handler())
		logger.Info("agentd 服务就绪", "addr", cfg.Listen)
		// 两侧都要保留：main 侧把 wdCancel 包进了 PTY 感知的优雅关停清理，
		// 本分支要在 Serve 返回后关掉 target 客户端池（含 relay 隧道）。
		err = waitServe(srv.GracefulShutdownCleanup(wdCancel))
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
		"agy":      agy.New(logger),
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
//   - WriteTimeout 11min（= workspace.RunCmdTimeout + 1min 余量）：响应写入上限，
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
		WriteTimeout:      workspace.RunCmdTimeout + time.Minute,
		IdleTimeout:       120 * time.Second,
	}
}

// executorFlag 覆盖 cfg.Executor.Default：opencode（默认，真实执行）| claude | grok |
// codex | agy | fake（脚本演示）；Windows 上 grok 是否注册取决于符号链接能力探测。
var executorFlag string

func init() {
	rootCmd.AddCommand(agentdCmd)
	agentdCmd.Flags().StringVar(&executorFlag, "executor", "",
		"覆盖默认执行者：opencode（默认）| claude | grok | codex | agy | fake（注册表保留全部，dispatch --executor 仍可按名选择）")
}

// setupLedger 打开账本库并挂载镜像子系统。
//
// B229 §2.6：ledger.enabled 开关退休，账本变必需品——本函数恒执行，
// 不再看任何开关位。dsn 空 = DataDir/ledger.db 单机回退。
//
// 参数：cfg 取 Ledger.DSN 与 DataDir；srv 接收 SetLedger 注入并出镜像
// 所需的 target 客户端池；taskStore 是本机任务事件库，供本机账本源读取；
// ctx 是镜像生命周期（随 agentd 停机取消）；logger 为启动日志入口。
//
// 返回：stop 必须由调用方 defer，且保证先于账本库 Close 执行——订阅
// 回调在写库，Stop 先于 Close 是硬约束（本函数把 Close 一并收进 stop，
// 调用方只需 defer stop() 一个动作）。
// ledgerBoot 是账本挂载的启动期参数（2026-10-19 mac-mini 事故：一次 53300 即放弃账本）。
// 零值即生产缺省：open=ledger.Open/OpenShared，sleep=定时器，report 不播报，
// ctx 取 setupLedger 的 ctx。
type ledgerBoot struct {
	// ctx 取消 = 停机，放弃重试并返回 ctx.Err()
	ctx context.Context
	// open 按 shared 打开账本；测试注入失败序列
	open func(dsn string, shared bool) (*ledger.Store, error)
	// report 把重试状态播报给启动闸（handoff status 据此看出账本未挂载）
	report func(phase string)
	// sleep 退避等待；ctx 取消时立即返回 ctx.Err()
	sleep func(ctx context.Context, d time.Duration) error
}

// 账本挂载退避：首次 1s，倍增，封顶 30s；±20% 抖动，避免全队同时重启后同秒撞库。
const (
	ledgerRetryInitial = time.Second
	ledgerRetryMax     = 30 * time.Second
)

// openLedgerWithRetry 打开账本，瞬时错误（ledger.IsTransientOpenError）退避重试直到
// 成功或停机，配置性错误第一次就返回。日志限频：第 1、2、4、8…次失败打 WARN，
// 其余只经 report 刷新启动闸；重试后成功打一条 INFO 带总次数。
func openLedgerWithRetry(dsn string, shared bool, boot ledgerBoot, logger *slog.Logger) (*ledger.Store, error) {
	open := boot.open
	if open == nil {
		open = func(dsn string, shared bool) (*ledger.Store, error) {
			if shared {
				return ledger.OpenShared(dsn)
			}
			return ledger.Open(dsn)
		}
	}
	sleep := boot.sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	report := boot.report
	if report == nil {
		report = func(string) {}
	}
	ctx := boot.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	backoff := ledgerRetryInitial
	started := time.Now()
	for attempt := 1; ; attempt++ {
		lst, err := open(dsn, shared)
		if err == nil {
			if attempt > 1 {
				// WARN 而非 INFO：与此前的重试 WARN 同级可见，默认日志级别下事故有始有终
				logger.Warn("账本挂载重试成功，瞬时故障已恢复", "attempts", attempt, "elapsed", time.Since(started).Round(time.Second))
			}
			return lst, nil
		}
		if !ledger.IsTransientOpenError(err) {
			logger.Error("打开账本库失败（配置性错误，不重试）", "dsn_configured", shared, "attempt", attempt, "cause", err,
				"action", "检查 config.yaml 的 ledger.dsn（地址/库名/账号/密码）后重启")
			return nil, err
		}
		wait := jitter(backoff)
		reason := ledger.OpenErrorReason(err)
		report(fmt.Sprintf("账本未挂载：共享账本打开失败，第 %d 次重试（%s），约 %s 后再试", attempt, reason, wait.Round(time.Second)))
		if attempt&(attempt-1) == 0 {
			logger.Warn("打开账本库失败（瞬时错误），退避重试；重试期间账本/镜像/卡派发/自动化未挂载，status 报未就绪",
				"dsn_configured", shared, "attempt", attempt, "next_retry", wait.Round(time.Millisecond),
				"elapsed", time.Since(started).Round(time.Second), "cause", err)
		}
		if err := sleep(ctx, wait); err != nil {
			logger.Warn("停机请求到达，放弃账本挂载重试", "attempts", attempt, "cause", err)
			return nil, err
		}
		if backoff *= 2; backoff > ledgerRetryMax {
			backoff = ledgerRetryMax
		}
	}
}

// jitter 返回 d 的 ±20% 随机抖动。
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// sleepCtx 等 d 或 ctx 取消（返回 ctx.Err()）。
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// newQuotaRegistry 组装额度适配注册表（B453，组装点唯一）。凭据相对路径沿用
// toolchain 唯一维护的表；本平台无已知落点时适配器报 unsupported_platform。agy 不读
// 凭据文件（登录态归 CLI 自管），经 PATH 找 agy（启动时 pathenv 已扩 PATH）。
func newQuotaRegistry() *quota.Registry {
	rel := func(cli string) string {
		if p, ok := toolchain.CredRelPathFor(cli); ok {
			return p
		}
		return ""
	}
	return quota.NewRegistry(map[string]quota.Prober{
		quota.KindOpencodeGo: &quota.OpencodeGo{CredRelPath: rel("opencode")},
		quota.KindCodex:      &quota.Codex{CredRelPath: rel("codex")},
		quota.KindGrok:       &quota.Grok{CredRelPath: rel("grok")},
		quota.KindAgy:        &quota.Agy{},
	})
}

func setupLedger(cfg *config.Config, srv *agentd.Server, taskStore *store.Store, ctx context.Context,
	logger *slog.Logger, boot ledgerBoot) (func(), error) {
	ldsn := cfg.Ledger.DSN
	shared := ldsn != ""
	// S2 / §4.5 挂载门：有 DSN 但缺机器身份（无自名，或直连形态缺 advertise:）
	// 时**不打开任何账本**（也不回退本机 SQLite），账本相关子系统（镜像、卡派发、
	// 自登记、调度、自动化、推送）全部不挂载，agentd 照常起。不给一台无名机器留
	// 半共享状态——它会消费共享事件流，正是旧缺陷族的变体。
	if shared && !cfgHasLedgerIdentity(cfg) {
		reason := "有账本 DSN 但缺少可用的机器身份：请在 config.yaml 设置 name:（直连形态还需 advertise:，relay 形态需配 relay: 段）"
		logger.Warn("账本子系统未挂载：缺少机器身份，agentd 照常提供服务（账本/镜像/卡派发/调度不可用）",
			"dsn_configured", true, "name_set", cfg.Name != "",
			"relay_set", cfg.Relay != nil, "advertise_set", cfg.Advertise != "",
			"action", reason)
		return func() {}, nil
	}
	if !shared {
		ldsn = filepath.Join(cfg.DataDir, "ledger.db")
	}
	if boot.ctx == nil {
		boot.ctx = ctx
	}
	lst, err := openLedgerWithRetry(ldsn, shared, boot, logger)
	if err != nil {
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	// B439 S3/S8：自登记（账本打开后、镜像挂载前同步执行）与本地 targets 一次性导入。
	// 撞名（异实例）→ 账本子系统不挂载，agentd 照常起（与 S2 挂载门同款 fail-closed）。
	if shared {
		if err := agentd.SelfRegister(lst, cfg, logger); err != nil {
			logger.Error("机器自登记失败，账本子系统不挂载（agentd 照常提供服务）",
				"cause", err, "action", "改 config.yaml 的 name: 或清理设备表冲突行后重启")
			_ = lst.Close()
			return func() {}, nil
		}
		if err := agentd.ImportLocalTargets(lst, cfg, logger); err != nil {
			logger.Error("本地 targets 导入设备表失败，账本子系统不挂载", "cause", err)
			_ = lst.Close()
			return func() {}, nil
		}
	}
	srv.SetLedger(lst)
	// B439 F5：有 DSN 的机器把池的选路数据源切到设备表；无 DSN 保持活配置（S5）。
	// 必须在镜像/调度/探活之前注入——它们都经池取机器清单。
	if shared {
		srv.SetDeviceSource(agentd.NewDeviceTableSource(lst, logger.With("component", "devices")))
	}
	// B156.3 自动化层装配。B233.18：SetupAutomation 收缩为账本门面 / automation
	// cursor / ptyGate 的纯装配；房间/keystone/hostapi 三域构造上移本组装点
	// （target.json assembly 登记点语义随之由 cmd 承接）——先备 facade，再构造注入。
	srv.SetupAutomation(lst)
	// 房间面（B156.2）：collab 入站门面 + 游标介质，经 SetRooms 注入。
	facade := srv.AutoLedger()
	rooms := collab.New(facade)
	rooms.SetCursorStore(cursor.New(filepath.Join(srv.Conf()().DataDir, "room-cursors.json")))
	// R-K1 (a‴)：项目房间书写者集并入人本人（读活配置 console_user，改配置不必重启）。
	rooms.SetProjectRoomHuman(srv.ConsoleMember)
	srv.SetRooms(rooms)
	// 凭据相对路径表仍由 toolchain 唯一维护；组装点注入给 hostapi，避免
	// hostapi 反向 import maintenance 域或复制三家 CLI 的平台规则。
	hostAPI := hostapi.NewWithCredentialPathFor(toolchain.CredRelPathFor)
	// B453：额度适配注册表组装一次，探测器（读数）与检测链（载体 HOME 凭据指纹）共用
	// 同一份凭据取材，指纹口径不会分叉。
	quotaReg := newQuotaRegistry()
	hostAPI.SetCredentialFingerprint(quotaReg.CredentialFingerprint)
	srv.SetHostAPI(hostAPI)
	// keystone 域（B156.3）：协调者会话承载 + 隔离 HOME 供给 + ref 解析 +
	// 叙事/attach 适配，经 SetKeystone 注入。
	prepareHome := orchestration.NewCoordinatorPrepareHome(srv.Conf(), srv.Providers(), srv.RuleLoader())
	coord := opencode.NewCoordinator(hostAPI, slog.Default())
	runner := agentd.NewCoordinatorRunner(coord, srv.Providers(), prepareHome, rooms.IsPushResidentSession,
		srv.CoordinatorAccountProxy)
	resolver := agentd.NewCoordinatorSessionRefResolver(srv, hostapi.ExpandHomePath)
	ks := keystone.New(runner, keystone.NewRoomNarrator(rooms), facade,
		agentd.NewAttachLocator(hostapi.ExpandHomePath))
	ks.SetSessionRefResolver(resolver)
	srv.SetKeystone(ks)
	// B233.14：编制域具体服务在 cmd 组装点构造（scheduling.New 不再落 gateway）。
	// 注册表由 gateway 经 SchedulingRegistry() 交出台账→Registry 适配；判空拒启动，
	// 不得把 nil 喂给 New 静默建出坏服务。
	reg := srv.SchedulingRegistry()
	if reg == nil {
		_ = lst.Close()
		return nil, fmt.Errorf("编制域装配：SchedulingRegistry 为空（SetupAutomation 未装配账本门面）")
	}
	sched := scheduling.New(reg)
	// 保留控制台改机器清单不必重启 agentd：有 DSN 时判据是设备表（经快照源现读），
	// 无 DSN 时回落活配置 targets:（S5）。
	sched.SetKnownMachines(func(name string) bool {
		if src := srv.DeviceSource(); src != nil {
			_, ok := src.Target(name)
			return ok
		}
		cfg := srv.Conf()()
		if cfg == nil {
			return false
		}
		_, ok := cfg.Targets[name]
		return ok
	})
	// B439 S6：编制域的本机判据收敛到自名（组装点从 config.Name 注入）。无自名
	// 时注入空串 = 保持今天别名词表（S5）。旧别名判据注入 config.IsLegacyLocalAlias
	// （词法唯一定义点在 config）：派发准入（AdmitFrozen）据此拒存量 carrier 的旧
	// 别名 machine，不再经 samePhysicalMachine 的别名语义静默放行。
	sched.SetSelfName(cfg.Name)
	sched.SetLegacyAlias(config.IsLegacyLocalAlias)
	srv.SetScheduling(sched)
	// B453：账号域与编制域共用同一注册表端口（账号定义/读数是 registry 的两个新 kind）。
	acc := accounts.New(reg)
	srv.SetAccounts(acc)
	// 编制域读账号做统一准入/载体引用校验/检测撞额度标记；账号域经编制域查「哪些载体选了
	// 我」做删除拒删（S1）。两向都是同一子系统内的导出调用，接线只在这里。
	sched.SetAccounts(acc)
	acc.SetReferrers(sched.CarriersUsingAccount)
	// 所在机探测器只在挂共享账本的机器上运行（plan Q3）：读数由所在机直写共享账本，
	// 单机 SQLite 账本没有设备表、也没有「他机」可言。账号未填代理时回落活配置 proxy:（Q5）。
	var prober *agentd.AccountProber
	if shared {
		prober = agentd.NewAccountProber(agentd.AccountProberConfig{
			Accounts:   acc,
			Quota:      quotaReg,
			IsSelf:     srv.IsSelfMachine,
			Writer:     cfg.Name,
			ExpandHome: hostapi.ExpandHomePath,
			Proxy: func() string {
				if c := srv.Conf()(); c != nil {
					return c.Proxy
				}
				return ""
			},
			Logger: logger,
		})
		srv.SetAccountProber(prober)
		prober.Start(ctx)
	}
	logger.Info("账号域已挂载", "dsn_configured", shared, "account_prober", prober != nil, "writer", cfg.Name)
	// B156.3 K5：账本打开后才能启动自动化事件流。
	// ctx 随 agentd 停机取消；首轮先重放事件与队列，再进入轮询。
	// B448 r1：StartAutomation 先同步做启动调度占用对账（归还本机遗留名额）再起循环；
	// 对账失败（计数 body 损坏等，带 key/version）中止启动，不带着未知计数进服务。
	if err := srv.StartAutomation(ctx); err != nil {
		logger.Error("自动化编排启动失败：启动调度占用对账未通过", "error_kind", "reconcile", "cause", err)
		_ = lst.Close()
		return nil, fmt.Errorf("启动调度占用对账: %w", err)
	}
	// B432 iOS 推送扇出与自动化循环同生命周期：card_events 侧的旁路挂在
	// 自动化消费循环里，循环不跑推送就不跑，一起起一起停。
	srv.StartPush(ctx)
	// B409.6 脱敏红线：DSN URL/密码不进日志。诊断保留 dsn_configured 布尔
	//（与 TestSetupLedgerFailureDoesNotLogDSNCredentials 同族，Wave 0 先例）。
	logger.Info("自动化编排已挂载", "dsn_configured", shared, "poll", "2s")
	// 恒挂载：机器清单来自 target 客户端池的活配置读取，启动时没有机器
	// 不代表以后没有——留着 len(cfg.Targets)>0 的闸会让控制台新增的第一台
	// 机器永远等不到账本镜像（与任务镜像同一条纪律，B163 ①）。
	// 池必须与任务镜像共用同一个：两个池等于两套 relay 隧道。
	host, _ := os.Hostname()
	// B439：镜像 holder 用自名（不再用 hostname），命名挂账按「名字 == 自名」
	// 路由到本机源。无自名（无 DSN 单机账本）回退 hostname + 地址判据（S5）。
	holder := cfg.Name
	if holder == "" {
		holder = host
	}
	lm := ledgermirror.New(lst, agentd.NewLedgerMirrorMachines(srv.Pool()), ledgermirror.Options{
		Holder:       holder,
		SelfName:     cfg.Name,
		LocalSource:  ledgermirror.NewLocalSource(taskStore, logger.With("source", "local")),
		IsSelfTarget: srv.IsSelfTarget,
	})
	go lm.Run(ctx)
	logger.Info("账本镜像子系统已挂载", "holder", holder, "self_name", cfg.Name,
		"machines", len(srv.Pool().Names()), "dsn_configured", shared)
	return func() {
		// 次序硬约束：先停探测器与镜像（不再写库）再关账本库。
		if prober != nil {
			prober.Stop()
		}
		lm.Stop()
		lst.Close()
	}, nil
}

// cfgHasLedgerIdentity 报告配置是否具备挂载共享账本所需的机器身份（B439 §4.5）：
// 有自名，且（relay 形态 或 直连形态有 advertise:）。缺任一项即 fail-closed，
// 不挂载账本子系统（P1 取甲：缺 advertise 视同缺自名）。
func cfgHasLedgerIdentity(cfg *config.Config) bool {
	if cfg.Name == "" {
		return false
	}
	if cfg.Relay != nil {
		return true
	}
	return cfg.Advertise != ""
}

// bindApproverOneShot 绑定审批者使用的 OneShot 能力。
// 架构法边界：组装点只从同一 ads Bundle 取 OneShot；未实现能力必须启动失败，
// 禁止另起一份按名称构造表。
//
// B376：names 是有序候选列表。逐个从 ads 取；一个都绑不上 → 启动失败（与今天
// 单名未注册相同）；部分缺失 → Warn，Decide 时该名当不可用（记为一次 error
// attempt 后试下一个）。候选名为空=审批链关闭，直接返回。
func bindApproverOneShot(ap *orchestration.Approver, ads map[string]executor.Adapter, names []string, log *slog.Logger) error {
	if ap == nil || len(names) == 0 {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	supported := strings.Join(executor.SupportedHarnesses(), ", ")
	bound := 0
	for _, name := range names {
		if name == "" {
			continue
		}
		ad, ok := ads[name]
		if !ok {
			log.Warn("审批者候选未注册，跳过该候选（Decide 时按不可用处理）",
				"executor", name, "supported", supported)
			continue
		}
		shot, ok := ad.(executor.OneShot)
		if !ok {
			log.Warn("审批者候选未实现 OneShot，跳过该候选",
				"executor", name, "supported", supported)
			continue
		}
		ap.BindOneShotFor(name, shot)
		bound++
		log.Info("审批者候选已绑定 OneShot", "executor", name)
	}
	if bound == 0 {
		err := fmt.Errorf("审批者候选 %v 全部无法绑定 OneShot（支持 OneShot 的执行者: %s）", names, supported)
		log.Error("审批者候选全部无法绑定 OneShot，启动失败", "executors", names, "cause", err)
		return err
	}
	return nil
}

// loadCarrierRules 为协调者供给隔离 HOME 读取主 HOME 的载体规则和技能树。
// 架构法边界：组装点持有五家规则相对路径常量，负责从主 HOME 读取文本，
// 编排层（internal/agentd）零知识接收抽象规则集。
func loadCarrierRules(mainHome, cli string) ([]executor.ProfileFile, []executor.ProfileFile, error) {
	base := filepath.Base(cli)
	var ruleRel, skillsRel string
	switch base {
	case executor.HarnessOpenCode:
		ruleRel, skillsRel = opencode.RulesRelFile, opencode.SkillsRelDir
	case executor.HarnessClaude:
		ruleRel, skillsRel = claudecode.RulesRelFile, claudecode.SkillsRelDir
	case executor.HarnessGrok:
		ruleRel, skillsRel = grok.RulesRelFile, grok.SkillsRelDir
	case executor.HarnessCodex:
		ruleRel, skillsRel = codex.RulesRelFile, codex.SkillsRelDir
	case executor.HarnessAGY:
		ruleRel, skillsRel = agy.RulesRelFile, agy.SkillsRelDir
	default:
		return nil, nil, executor.UnsupportedError(base, executor.CapProfile)
	}
	var rules []executor.ProfileFile
	if b, err := os.ReadFile(filepath.Join(mainHome, ruleRel)); err == nil {
		rules = append(rules, executor.ProfileFile{Name: filepath.Base(ruleRel), Content: string(b)})
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	skills, err := readSkillTree(filepath.Join(mainHome, skillsRel), "")
	return rules, skills, err
}

// readSkillTree 递归读取技能目录树为平铺的 ProfileFile 切片。
func readSkillTree(root, rel string) ([]executor.ProfileFile, error) {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, rel)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []executor.ProfileFile
	for _, e := range entries {
		child := e.Name()
		if rel != "" {
			child = filepath.Join(rel, e.Name())
		}
		p := filepath.Join(root, child)
		info, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("规则源含 symlink %q", p)
		}
		if e.IsDir() {
			sub, err := readSkillTree(root, child)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, executor.ProfileFile{Name: child, Content: string(b)})
	}
	return out, nil
}
