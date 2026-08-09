// 本文件实现 handoff agentd 子命令：加载配置、初始化统一日志、打开 SQLite 存储、
// 构建 HTTP/WS 服务并监听。agentd 是本机/配对主机上的长驻服务，是任务的执行入口。
//
// 职责：
//   - 按序完成 bootstrap：config.Load → logx.Setup + slog.SetDefault →
//     agentd.MergeLoginShellPATH（PATH 补全，先于一切 fork 子进程）→ store.Open → agentd.NewServer
//   - 对外服务前做启动恢复（RecoverOnStartup）：探活未终结任务的执行器，重建订阅或转 failed
//   - 启动任务卡住看门狗 goroutine（RunWatchdog），长时间无事件产出触发 stalled 唤醒审核者
//   - 监听配置中的 Listen 地址，进程生命周期与 HTTP server 一致
//
// 边界：
//   - 不创建任务/工单：任务生命周期由 manager 驱动（executor 按 --executor 挂载）
//   - 优雅关停（signal 处理）不在 MVP 范围，进程退出即断开全部连接
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/envfile"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/claudecode"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/executor/grok"
	"github.com/xushixin/handoff/internal/executor/opencode"
	"github.com/xushixin/handoff/internal/logx"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/store"
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

		// PATH 补全（B7）：agentd 常由非登录 shell 拉起，拿不到 profile 里的
		// PATH——真实踩坑是 executor 在远程机上找不到 go。必须早于任何 fork
		// 子进程的动作，合并结果才能被 executor/审批者/审阅命令继承
		agentd.MergeLoginShellPATH(context.Background(), logger)

		// DataDir 首次运行可能不存在（config.Load 只保证配置目录），
		// store.Open 与 taskDir 创建都依赖它，必须先建
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
		}

		st, err := store.Open(filepath.Join(cfg.DataDir, "handoff.db"))
		if err != nil {
			return fmt.Errorf("打开存储: %w", err)
		}
		defer st.Close()

		// 控制面初始化（桌面 phase2）：确保本机身份、投影配置机器、迁移旧任务、
		// 补投影本机事件。必须在 NewServer 之前完成——任一步失败即拒绝以未就绪
		// 状态提供 desktop /v1 写服务。
		bootstrapStart := time.Now()
		bs := controlplane.NewBootstrapService(st, logger)
		localMachine, err := bs.Initialize(cmd.Context(), cfg)
		if err != nil {
			return fmt.Errorf("控制面初始化: %w", err)
		}
		logger.Info("控制面初始化完成",
			"local_machine_id", localMachine.ID,
			"target_count", len(cfg.Targets),
			"elapsed_ms", time.Since(bootstrapStart).Milliseconds())

		// env 注入（B19）：agent 启动时注入 <DataDir>/env/ 下配置的环境变量文件。
		// 启动预检只 WARN 不阻断——env 文件是数据文件，可能在 agentd 启动后才创建；
		// 但完全不检查会把问题拖到第一次派发才暴露，预检让它在启动日志里就可见。
		// manager 侧自建同款 resolver（NewManager 内），两者无状态、不会发散。
		envRes := envfile.NewResolver(envfile.Dir(cfg.DataDir), cfg.Env, logger)
		envRes.Preflight()

		// 审批链接线：配置启用了 approver 时构造裁决器；黑名单正则等配置错误
		// 直接启动失败（属配置错误，改配置重启即可，不该带病运行）
		ap, err := agentd.NewApprover(cfg.Approver, envRes, logger)
		if err != nil {
			return fmt.Errorf("初始化审批链: %w", err)
		}

		srv := agentd.NewServer(cfg, st, logger)
		// 四个执行者都注册：dispatch --executor 可按名选择；opencode/claude/grok 是
		// 真实执行，fake 用于演示/测试。缺省由 cfg.Executor.Default 决定（--executor flag 覆盖）
		ads := defaultAdapters(logger)
		if executorFlag != "" {
			if _, ok := ads[executorFlag]; !ok {
				return fmt.Errorf("未知 executor %q（支持 opencode/claude/grok/fake）", executorFlag)
			}
			// --executor 语义是「覆盖缺省执行者」：只改 cfg 的缺省名，注册表保持
			// 全部可用——老任务按各自 executor 名仍能路由到对应 adapter
			cfg.Executor.Default = executorFlag
		}
		mgr := agentd.NewManager(st, srv.Hub(), ads, cfg, ap, logger)
		srv.SetManager(mgr)

		// 启动恢复（spec §8）：在对外服务前，把 agentd 崩溃前未终结的任务拉回正轨——
		// 执行器存活的任务经 mgr.ResumeTask 重建 SSE 订阅并重启中介循环，已不在的
		// 任务转 failed/waiting_review 交审核者裁决。探活与「重建订阅」封装在同一个
		// 闭包里（watchdog.go RecoverOnStartup 的 seam 说明），此处即其接线点
		if err := agentd.RecoverOnStartup(st, srv.Hub(), mgr.ResumeTask, logger); err != nil {
			return fmt.Errorf("启动恢复: %w", err)
		}
		// 任务卡住看门狗：周期扫描 running/waiting_answer 任务，长时间无事件产出
		// 触发 stalled 事件唤醒审核者（独立 goroutine，不阻塞 HTTP 服务；
		// MVP 无优雅关停，进程退出即随 ctx 结束）
		go agentd.RunWatchdog(context.Background(), st, srv.Hub(), cfg.StallTimeout, logger)

		// 本机资源权威（桌面 phase2）：启动先做一次完整 Reconcile，再挂 .git
		// watcher 与周期兜底扫描。外部 branch/worktree/task 变化经 durable outbox
		// 推送进桌面左栏。
		reconciler := machineauthority.NewLocalReconciler(st, localMachine, logger)
		if _, err := reconciler.ReconcileAll(cmd.Context(), "startup"); err != nil {
			// 启动 Reconcile 失败不阻断 agentd：任务/API 主路径不依赖资源扫描，
			// 周期兜底仍会重试；记录错误防止静默
			logger.Error("启动 reconcile 失败（周期兜底会重试）", "cause", err)
		}
		stopWatch := reconciler.StartWatch(cmd.Context())
		defer stopWatch()

		// 远端机器同步（桌面 phase2）：为每台配置的远端 agentd 启动 peer 同步
		// worker，把远端 machine events 经 Projector 投影进本机控制面。
		// 机器状态变化投影到 Machine 表；一台坏机器不阻塞其他。
		projector := controlplane.NewProjector(st)
		// 广播进桌面控制流 hub：远端事件投影后桌面 stream 实时收到
		projector.OnApplied = func(ce controlplane.ControlEvent) {
			env, err := (&desktopapi.CatalogAssembler{}).ToControlEvent(ce)
			if err != nil {
				logger.Warn("控制事件广播转换失败", "revision", ce.ControlRevision, "cause", err)
				return
			}
			srv.ControlHub().Publish(env)
		}
		syncManager := peer.NewSyncManager(peer.SyncManagerConfig{
			Machines: syncMachinesFromConfig(cfg),
			CredentialResolver: func(secretRef string) string {
				// secret_ref 形如 config.targets.<name>.token，取 <name> 后查 cfg
				name := strings.TrimPrefix(strings.TrimSuffix(secretRef, ".token"), "config.targets.")
				if t, ok := cfg.Targets[name]; ok {
					return t.Token
				}
				return ""
			},
			Projector: projector,
			OnMachineState: func(machineID string, state peer.SupervisorState) {
				st.SetMachineStatus(context.Background(), machineID,
					controlplane.MachineStatus(string(state)))
			},
			Interval: 30 * time.Second,
			Log:      logger,
		})
		syncManager.Start(context.Background())
		defer syncManager.Stop()

		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir, "default_executor", cfg.Executor.Default)
		return newAgentdHTTPServer(cfg.Listen, srv.Handler()).ListenAndServe()
	},
}

// defaultAdapters 返回 agentd 的 executor 注册表（name → Adapter）。
//
// 抽成函数而非内联字面量：注册表是 dispatch --executor 路由的唯一真相，
// 漏注册的症状是「派发时报未注册」而不是编译错误，值得一条断言守着
// （见 agentd_test.go 的 TestAdapterRegistryHasAllExecutors）。
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"claude":   claudecode.New(logger),
		"grok":     grok.New(logger),
		"fake":     fake.New(nil),
	}
}

// configuredMachinesFromConfig 把 config.Targets 投影为控制面 ConfiguredMachine。
//
// 为什么只传 secret_ref 引用不传 token：token 值只由运行时 credential resolver
// 按 config.targets.<name>.token 从 config 读取，领域层与 DB 永不接触 token。
func configuredMachinesFromConfig(cfg *config.Config) ([]controlplane.ConfiguredMachine, error) {
	var out []controlplane.ConfiguredMachine
	for name, t := range cfg.Targets {
		if t.Addr == "" {
			continue
		}
		displayName := t.DisplayName
		if displayName == "" {
			displayName = name
		}
		out = append(out, controlplane.ConfiguredMachine{
			ConfigKey:   name,
			DisplayName: displayName,
			Kind:        controlplane.MachineKindRemote,
			Endpoint:    t.Addr,
			SecretRef:   "config.targets." + name + ".token",
		})
	}
	return out, nil
}

// syncMachinesFromConfig 把 config.Targets 投影为 peer 同步机器列表。
func syncMachinesFromConfig(cfg *config.Config) []peer.SyncMachine {
	var out []peer.SyncMachine
	for name, t := range cfg.Targets {
		if t.Addr == "" {
			continue
		}
		out = append(out, peer.SyncMachine{
			MachineID: name,
			Endpoint:  t.Addr,
			SecretRef: "config.targets." + name + ".token",
		})
	}
	return out
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

// executorFlag 覆盖 cfg.Executor.Default：opencode（默认，真实执行）| claude | grok | fake（脚本演示）。
var executorFlag string

func init() {
	rootCmd.AddCommand(agentdCmd)
	agentdCmd.Flags().StringVar(&executorFlag, "executor", "",
		"覆盖缺省执行者：opencode（默认）| claude | grok | fake（注册表保留全部，dispatch --executor 仍可按名选择）")
}
