// preflight.go —— agentd 以 codex 为缺省执行者启动时的环境预检。
//
// 职责：
//   - 硬前提（codex 在 PATH、已登录）不满足时给出可行动的错误，早失败早止损
//   - 软污染源（AGENTS.md / hooks.json / mcp_servers）存在时 WARN 并提示清理
//
// 边界：
//   - 不改任何文件：清理是人的决定，agentd 不替用户动他的 ~/.codex
//   - 不检查配置里的 model / sandbox_mode / approvals_reviewer 等项——它们全部
//     被 handoff 协议级压过（spec §1.1 实证），检查它们只会制造噪音
//
// 为什么区分 error 与 WARN：硬前提不满足时任务必然失败，且失败点在回合中途、
// 诊断成本高；软污染源只改变 executor 的干活方式，不影响安全边界（安全档位由
// 代码钉死，spec §1.3），值得提醒但不值得挡住启动。
package codex

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lookPath 是 exec.LookPath 的测试缝：单测替换它模拟「codex 在 PATH 上」，
// 不依赖执行机上真实二进制的安装位置。
var lookPath = exec.LookPath

// Preflight 检查 executor 机的 codex 环境。
//
// 参数：
//   - home: codex home 目录；空串时取 $HOME/.codex
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：
//   - 硬前提不满足时返回带可行动指引的错误；软污染源只打 WARN，返回 nil
func Preflight(home string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("读取用户主目录失败，无法定位 ~/.codex: %w", err)
		}
		home = filepath.Join(h, ".codex")
	}
	log.Info("codex 环境预检", "home", home)

	if _, err := lookPath("codex"); err != nil {
		return fmt.Errorf("executor 机上找不到 codex 可执行文件，请先安装 codex-cli 并确保它在 PATH 上: %w", err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		return fmt.Errorf("codex 未登录（%s/auth.json 不存在），请在 executor 机执行 `codex login`", home)
	}

	// 以下三条只 WARN：会改变 executor 的干活方式，但不影响安全边界
	if _, err := os.Stat(filepath.Join(home, "AGENTS.md")); err == nil {
		log.Warn("codex home 存在全局 AGENTS.md，executor 的干活方式会被它改变，建议移除",
			"path", filepath.Join(home, "AGENTS.md"))
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); err == nil {
		// 没有协议级开关能关掉 hooks，只能靠清理——本方案已知且被接受的软肋
		log.Warn("codex home 存在 hooks.json，且没有协议级开关能关掉它，强烈建议移除",
			"path", filepath.Join(home, "hooks.json"))
	}
	if b, err := os.ReadFile(filepath.Join(home, "config.toml")); err == nil &&
		strings.Contains(string(b), "[mcp_servers") {
		log.Warn("codex config.toml 配了 mcp_servers，executor 会多出一批工具，建议清空",
			"path", filepath.Join(home, "config.toml"))
	}
	log.Info("codex 环境预检通过", "home", home)
	return nil
}
