// 本文件实现 handoff agentd 子命令：加载配置、初始化统一日志、打开 SQLite 存储、
// 构建 HTTP/WS 服务并监听。agentd 是本机/配对主机上的长驻服务，是任务的执行入口。
//
// 职责：
//   - 按序完成 bootstrap：config.Load → logx.Setup + slog.SetDefault → store.Open → agentd.NewServer
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
	"time"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/executor/opencode"
	"github.com/xushixin/handoff/internal/logx"
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

		srv := agentd.NewServer(cfg, st, logger)
		var ad executor.Adapter
		switch executorFlag {
		case "opencode":
			ad = opencode.New(logger)
		case "fake":
			ad = fake.New(nil)
		default:
			return fmt.Errorf("未知 executor %q（支持 opencode/fake）", executorFlag)
		}
		mgr := agentd.NewManager(st, srv.Hub(), ad, cfg, logger)
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
		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir)
		return newAgentdHTTPServer(cfg.Listen, srv.Handler()).ListenAndServe()
	},
}

// newAgentdHTTPServer 构造 agentd 的 HTTP 服务监听（独立成函数以便测试断言超时配置）。
//
// 各超时值的 why：
//   - ReadHeaderTimeout 10s：请求头读取上限——防 slowloris（慢速请求头占满连接
//     goroutine）；配合 IdleTimeout 保证半死连接被回收
//   - ReadTimeout 30s：请求体读取上限（reply/fetch 等请求体都很小，30s 充足）
//   - WriteTimeout 60s：响应写入上限。对 hijacked 连接（WS 事件流）**不生效**：
//     net/http 在 Hijack 时清除连接上的全部截止时间（server.go hijackLocked 里
//     rwc.SetDeadline(time.Time{})，实测 Go 1.26 行为），coder/websocket 的
//     Accept 走 hijack——长连接不受该值约束，60s 只作用于普通 HTTP 响应
//   - IdleTimeout 120s：keep-alive 空闲连接回收，防连接池被死连接占满
func newAgentdHTTPServer(listen string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// executorFlag 选择 executor 实现：opencode（默认，真实执行）| fake（脚本演示）。
var executorFlag string

func init() {
	rootCmd.AddCommand(agentdCmd)
	agentdCmd.Flags().StringVar(&executorFlag, "executor", "opencode",
		"executor 实现：opencode（默认）| fake")
}
