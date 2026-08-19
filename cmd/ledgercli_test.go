// 账本 CLI 测试基座：进程内跑 rootCmd（复用 resetPerRunState 的可重入
// 设计），--config 指向临时目录，账本落该目录 SQLite。
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Xsxdot/handoff/internal/config"
)

// runLedgerCLI 在 dir（DataDir 兼配置目录）下跑一条 handoff 命令。
// 首次调用自动写最小 config.yaml。返回 stdout/stderr 文本与错误。
func runLedgerCLI(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		c := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour}
		if err := config.Save(cfgPath, c); err != nil {
			t.Fatalf("写测试配置: %v", err)
		}
	}
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	err := Execute()
	return out.String(), errb.String(), err
}

// resetAllFlags 递归把命令树上所有 flag 恢复默认值。cobra 的 flag 绑定
// 在包级变量上，跨 Execute() 持久——上一个测试设过的 --parent/--json
// 会静默污染下一个测试（repo 既有做法是逐个 t.Cleanup 手工复位，账本
// 命令族 flag 太多，统一在基座里回默认值）。
func resetAllFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range c.Commands() {
		resetAllFlags(sub)
	}
}

func TestOpenLedgerFallbackSQLite(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "workflow", "list")
	if err != nil {
		t.Fatalf("workflow list: %v", err)
	}
	if !strings.Contains(out, "feature") || !strings.Contains(out, "bug") {
		t.Fatalf("默认工作流缺失: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.db")); err != nil {
		t.Fatalf("回退 SQLite 未落 DataDir: %v", err)
	}
}
