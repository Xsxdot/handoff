// 账本命令族的公共底座。职责：解析账本库位置并打开、CLI 侧 actor 标识、
// 破坏性动作确认。边界：本文件不含任何具体动词逻辑。
//
// 为什么 CLI 直连账本库而不经本机 agentd HTTP（有意偏离既有惯例）：
// 账本凭据本来就在协调机 config 里，CLI 与 agentd 是对等消费者；账本
// 操作不应依赖本机 agentd 存活；执行域必须走 HTTP 是因为 task 数据在
// 远端机器的 SQLite——账本（中心库/本机回退）没有这个约束。
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// openLedger 按配置打开账本库：ledger.dsn 非空连中心库，空则回退
// DataDir/ledger.db（单机模式）。每次打开幂等 seed 默认工作流。
// 调用方负责 Close。
func openLedger() (*ledger.Store, error) {
	cfg := loadCLIConfig()
	dsn := cfg.Ledger.DSN
	if dsn == "" {
		dsn = filepath.Join(cfg.DataDir, "ledger.db")
	}
	st, err := ledger.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	if err := st.EnsureDefaultWorkflows(); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("seed 默认工作流: %w", err)
	}
	return st, nil
}

// ledgerActor CLI 写账时的 actor 标识：cli:<user>@<host>。事件流取证用，
// 不做鉴权（账本凭据即权限边界）。
func ledgerActor() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("cli:%s@%s", user, host)
}

// confirmDestructive 三处破坏性动作（close/merge/workflow migrate）的
// 二次确认：--yes 跳过；非 TTY 且无 --yes 直接拒绝（脚本必须显式表态，
// 不许静默走破坏路径）。
func confirmDestructive(cmd *cobra.Command, yes bool, msg string) error {
	if yes {
		return nil
	}
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isatty.IsTerminal(f.Fd()) {
		return fmt.Errorf("%s：非交互环境需 --yes 显式确认", msg)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s (y/N) ", msg)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("%s：已取消", msg)
	}
}
