// 本文件负责回答两个问题：agentd 在哪（地址与主令牌），以及这台机器配没配过 handoff。
//
// 边界：只读配置，**不碰网络**——「配置里写着某地址」和「那地址上真的有 agentd」
// 是两件事，后者是 lifecycle.go 的职责。也不写配置：首次引导（W5b-2）才写。
package shell

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/config"
)

// Endpoint 是连接本机 agentd 所需的最小信息。
type Endpoint struct {
	// Addr 是 agentd 的监听地址，形如 127.0.0.1:7777（不含 scheme）。
	Addr string
	// Token 是主令牌。**只在进程内使用**，不得写日志、不得传给前端。
	Token string
}

// ConfigState 表示这台机器配没配过 handoff。
type ConfigState int

const (
	// StateUnconfigured：没配过，调用方应走首次引导。
	StateUnconfigured ConfigState = iota
	// StateConfigured：配过，可以拿 Endpoint 去握手。
	StateConfigured
)

// String 让日志里的 state 是人能读的词而不是 0/1。
func (s ConfigState) String() string {
	switch s {
	case StateUnconfigured:
		return "unconfigured"
	case StateConfigured:
		return "configured"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Resolve 读取配置并判断这台机器配没配过 handoff。
//
// 参数：
//   - path: 配置文件路径。传空则用 config.DefaultPath()（~/.handoff/config.yaml）
//
// 返回：
//   - Endpoint：仅在 StateConfigured 时有意义
//   - ConfigState
//   - error：**只在配置文件存在却读不动/解析不了时返回**。文件不存在不是错误
//
// 注意：
//   - **不要用 config.Load 的 error 判断「配没配过」**。它在文件不存在时返回
//     默认配置且 err==nil，照 error 判断会把全新机器误判为已配置，
//     然后拿着空令牌去握手，症状是一个难以归因的 401。
func Resolve(path string) (Endpoint, ConfigState, error) {
	if path == "" {
		path = config.DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			slog.Info("未找到配置文件，判为未配置", "path", path)
			return Endpoint{}, StateUnconfigured, nil
		}
		return Endpoint{}, StateUnconfigured, fmt.Errorf("检查配置文件 %s: %w", path, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return Endpoint{}, StateUnconfigured, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if cfg.Token == "" {
		slog.Info("配置文件存在但主令牌为空，判为未配置", "path", path)
		return Endpoint{}, StateUnconfigured, nil
	}
	// 注意日志里只出地址不出令牌
	slog.Info("已定位 agentd 配置", "path", path, "addr", cfg.Listen)
	return Endpoint{Addr: cfg.Listen, Token: cfg.Token}, StateConfigured, nil
}
