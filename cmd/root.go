// Package cmd 提供 handoff 的 cobra 命令行入口。
//
// 职责：
//   - 定义根命令与全局 flag（--agentd / --target / --config）
//   - 提供 TargetEndpoint 辅助函数，供各子命令换算实际 agentd 端点
//
// 边界：
//   - 不包含具体业务逻辑（dispatch/gate 等子命令由后续任务补充）
//   - 不在此处初始化日志，由各子命令按需调用 logx.Setup
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/config"
)

var (
	agentdURL  string
	targetName string
	configPath string
)

var rootCmd = &cobra.Command{
	Use:   "handoff",
	Short: "handoff：把任务派发到本机/远程 agentd 执行并续接的 CLI",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&agentdURL, "agentd", "http://127.0.0.1:7777", "agentd 服务地址")
	rootCmd.PersistentFlags().StringVar(&targetName, "target", "", "目标主机名（从配置 Targets 中换算 addr/token）")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径（默认 ~/.handoff/config.yaml）")
}

// Execute 运行根命令，错误返回给 main。
func Execute() error {
	return rootCmd.Execute()
}

// TargetEndpoint 根据 --target 换算实际请求的 agentd 端点。
//
// 参数（读取全局 flag）：
//   - --target 为空：直接返回 --agentd 地址，token 为空
//   - --target 非空：从配置 Targets 中查出 addr/token
//
// 返回：
//   - addr: agentd 完整地址（含 http:// 前缀）
//   - token: 访问令牌；未指定 --target 时为空串
//   - err: 配置加载失败或 target 未定义时返回
func TargetEndpoint() (addr, token string, err error) {
	if targetName == "" {
		return agentdURL, "", nil
	}
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return "", "", fmt.Errorf("加载配置 %s: %w", p, err)
	}
	t, ok := cfg.Targets[targetName]
	if !ok {
		return "", "", fmt.Errorf("target %q 未在配置 %s 中定义", targetName, p)
	}
	return "http://" + t.Addr, t.Token, nil
}
