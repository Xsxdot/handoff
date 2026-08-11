// clicheck.go —— CLI 侧的限流版本检查与提示行。
//
// 边界：
//   - **CLI 永远不自动替换自己**（D13）：CLI 是交互工具，在用户敲命令时不知情地
//     换掉自己不合适，脚本化场景下行为还会突变。这里只打一行提示
//   - 读缓存的失败一律静默：这条路径挂在**每一条** handoff 命令上，
//     一个坏掉的缓存文件让所有命令都吐错误，代价远大于少提示一次更新
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cliCheckInterval 是两次 CLI 版本检查之间的最小间隔。
//
// 24h：CLI 提示的价值是「让人知道有新版了」，天级足够；查得更勤只会
// 增加 GitHub 限流压力，而限流一旦触发，agentd 的自动更新也会跟着失败。
const cliCheckInterval = 24 * time.Hour

// CLICheck 是 CLI 侧版本检查的缓存。
type CLICheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// CLICheckPath 返回缓存文件路径。
func CLICheckPath(dataDir string) string {
	return filepath.Join(dataDir, "update", "cli-check.json")
}

// LoadCLICheck 读缓存。
//
// 返回：
//   - 缓存；**缺失或损坏一律返回 nil，不返回错误**（理由见文件头注释）
func LoadCLICheck(dataDir string) *CLICheck {
	b, err := os.ReadFile(CLICheckPath(dataDir))
	if err != nil {
		return nil
	}
	var c CLICheck
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	return &c
}

// SaveCLICheck 写缓存，自动建目录。
func SaveCLICheck(dataDir string, c *CLICheck) error {
	path := CLICheckPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("建目录 %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// CLICheckStale 判断是否该重新检查。nil（没查过）视为过期。
func CLICheckStale(c *CLICheck, now time.Time) bool {
	if c == nil {
		return true
	}
	return now.Sub(c.CheckedAt) >= cliCheckInterval
}

// NotifyLine 生成提示行；不需要提示时返回空串。
//
// 参数：
//   - c: 缓存（可为 nil）
//   - current: 本进程版本；**空串表示非 release 构建**
//
// 注意：
//   - current 为空时一律不提示。开发时每条命令都被劝「有新版」是纯噪音，
//     而且本地构建本来就不该被劝去装 release
func NotifyLine(c *CLICheck, current string) string {
	if c == nil || c.Latest == "" || current == "" || c.Latest == current {
		return ""
	}
	return fmt.Sprintf("有新版本 %s（当前 %s），运行 handoff upgrade --now 升级", c.Latest, current)
}
