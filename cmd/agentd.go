// 本文件实现 handoff agentd 子命令：加载配置、初始化统一日志、打开 SQLite 存储、
// 构建 HTTP/WS 服务并监听。agentd 是本机/配对主机上的长驻服务，是任务的执行入口。
//
// 职责：
//   - 按序完成 bootstrap：config.Load → logx.Setup + slog.SetDefault → store.Open → agentd.NewServer
//   - 监听配置中的 Listen 地址，进程生命周期与 HTTP server 一致
//
// 边界：
//   - 不创建任务/工单，不启动 executor（manager 由后续任务实现）
//   - 优雅关停（signal 处理）不在 MVP 范围，进程退出即断开全部连接
package cmd

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor/fake"
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
		// TODO(handoff): Task 11 换成 opencode adapter（--executor 开关），当前仅 fake 可用
		mgr := agentd.NewManager(st, srv.Hub(), fake.New(nil), cfg, logger)
		srv.SetManager(mgr)
		logger.Info("agentd 服务启动", "addr", cfg.Listen, "data_dir", cfg.DataDir)
		return http.ListenAndServe(cfg.Listen, srv.Handler())
	},
}

func init() {
	rootCmd.AddCommand(agentdCmd)
}
