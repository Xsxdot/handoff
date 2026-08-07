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
	"strings"

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

// TargetEndpoint 根据 --target / --agentd / --config 换算实际请求的 agentd 端点与令牌。
//
// 参数（读取全局 flag）：
//   - --target 为空（本机模式）：token 一律取本地配置 cfg.Token（服务端无条件要求
//     Bearer，无 token 的本机调用必然 401）；地址取 --agentd（显式指定时优先）或
//     cfg.Listen（与 agentd 实际监听一致），cfg.Token 为空时返回错误
//   - --target 非空：从配置 Targets 中查出 addr/token（远程配对）
//
// 返回：
//   - addr: agentd 完整地址（含 http:// 前缀）
//   - token: 访问令牌
//   - err: 配置加载失败、target 未定义或本机 token 为空时返回
func TargetEndpoint() (addr, token string, err error) {
	p := configPath
	if p == "" {
		p = config.DefaultPath()
	}
	cfg, err := config.Load(p)
	if err != nil {
		return "", "", fmt.Errorf("加载配置 %s: %w", p, err)
	}
	if targetName == "" {
		// 本机模式：token 必须来自本地配置——agentd 的 Bearer 鉴权无条件生效，
		// 且 config.Load 保证配置文件存在时 token 一定已生成（首次运行即写盘）
		if cfg.Token == "" {
			return "", "", fmt.Errorf("配置 %s 未设置 token，无法认证本机 agentd", p)
		}
		// 地址优先级：显式 --agentd 优先（用户指明了别的端点）；未显式指定时
		// 用配置 Listen，与 agentd 实际监听保持一致，避免默认值与配置漂移
		if rootCmd.PersistentFlags().Changed("agentd") {
			addr = agentdURL
		} else {
			addr = cfg.Listen
			if !strings.Contains(addr, "://") {
				addr = "http://" + addr
			}
		}
		return addr, cfg.Token, nil
	}
	t, ok := cfg.Targets[targetName]
	if !ok {
		return "", "", fmt.Errorf("target %q 未在配置 %s 中定义", targetName, p)
	}
	return "http://" + t.Addr, t.Token, nil
}
