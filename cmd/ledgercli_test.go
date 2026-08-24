// 账本 CLI 测试基座：进程内跑 rootCmd（复用 resetPerRunState 的可重入
// 设计），--config 指向临时目录，账本落该目录 SQLite。
package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// runLedgerCLI 在 dir（DataDir 兼配置目录）下跑一条 handoff 命令。
// 首次调用自动写最小 config.yaml。返回 stdout/stderr 文本与错误。
func runLedgerCLI(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		c := &config.Config{
			Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour,
			// 账本测试基座必须显式开账本：开关默认 false，不开则全族拒绝执行
			Ledger: config.LedgerConfig{Enabled: true},
		}
		if err := config.Save(cfgPath, c); err != nil {
			t.Fatalf("写测试配置: %v", err)
		}
	}
	seedCardCLIForTest(t, dir, args)
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	err := Execute()
	return out.String(), errb.String(), err
}

// seedCardCLIForTest 只写入既有 card 命令测试所需的显式数据。
// 它是测试夹具数据；生产的 openLedger 不做任何 seed。
func seedCardCLIForTest(t *testing.T, dir string, args []string) {
	t.Helper()
	if len(args) == 0 || args[0] != "card" {
		return
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开 CLI 测试账本: %v", err)
	}
	defer st.Close()
	workflowName := "bug"
	for i, arg := range args {
		if arg == "--workflow" && i+1 < len(args) {
			workflowName = args[i+1]
			break
		}
	}
	for name, def := range map[string]ledger.TemplateDef{
		"feature-impl":   {Executor: "opencode", Purpose: ledger.PurposeImplement, BranchPrefix: "cards", Discipline: discipline.NameImplement, Prompt: "实现 {{TITLE}}：{{ACCEPT}}"},
		"review-generic": {Executor: "grok", Purpose: ledger.PurposeReview, BranchPrefix: "cards", Discipline: discipline.NameReview, Prompt: "审阅 {{TITLE}}：{{ACCEPT}}"},
	} {
		if _, err := st.GetTemplate(name, 0); errors.Is(err, ledger.ErrNotFound) {
			if _, err := st.PutTemplate(name, def); err != nil {
				t.Fatalf("写 CLI 测试模板 %s: %v", name, err)
			}
		} else if err != nil {
			t.Fatalf("读 CLI 测试模板 %s: %v", name, err)
		}
	}
	if _, err := st.GetWorkflow(workflowName, 0); errors.Is(err, ledger.ErrNotFound) {
		def := ledger.WorkflowDef{Nodes: []ledger.NodeDef{{Name: ledger.StatusTodo, Next: ledger.StatusDoing},
			{Name: ledger.StatusDoing, Next: ledger.StatusReview, Dispatch: true, Template: "feature-impl"},
			{Name: ledger.StatusReview, Next: ledger.StatusDone, Dispatch: true, Verdict: true, Template: "review-generic", OnFail: ledger.StatusDoing},
			{Name: ledger.StatusDone}}}
		if workflowName == "feature" {
			def = ledger.WorkflowDef{Nodes: []ledger.NodeDef{
				{Name: ledger.StatusTodo, Next: "已出spec"},
				{Name: "已出spec", Next: ledger.StatusDoing, Gate: ledger.Gate{RequireAttachment: "spec"}},
				{Name: ledger.StatusDoing, Next: ledger.StatusReview, Dispatch: true, Template: "feature-impl"},
				{Name: ledger.StatusReview, Next: "待合并", Dispatch: true, Verdict: true, Template: "review-generic", OnFail: ledger.StatusDoing},
				{Name: "待合并", Next: ledger.StatusDone, Gate: ledger.Gate{RequireAcceptance: true}, Dispatch: true, Verdict: true, Template: "review-generic"},
				{Name: ledger.StatusDone},
			}}
		}
		if _, err := st.PutWorkflow(workflowName, def); err != nil {
			t.Fatalf("写 CLI 测试工作流 %s: %v", workflowName, err)
		}
	} else if err != nil {
		t.Fatalf("读 CLI 测试工作流 %s: %v", workflowName, err)
	}
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
	if strings.Contains(out, "feature") || strings.Contains(out, "bug") || strings.Contains(out, "triage") {
		t.Fatalf("新账本不应注入出厂工作流: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.db")); err != nil {
		t.Fatalf("回退 SQLite 未落 DataDir: %v", err)
	}
}

// TestOpenLedgerIgnoresRetiredEnabledFlag 钉住 B229 §26 的退休语义：
// enabled 开关已不存在「关」这一档——显式写 enabled=false 的配置同样
// 必须正常开库（单机回退 DataDir/ledger.db），不再出现「账本未启用」拒绝。
func TestOpenLedgerIgnoresRetiredEnabledFlag(t *testing.T) {
	resetFlags(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	c := &config.Config{
		Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger: config.LedgerConfig{Enabled: false},
	}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	configPath = cfgPath
	st, err := openLedger()
	if err != nil {
		t.Fatalf("enabled 已退休，openLedger 应恒开库: %v", err)
	}
	defer st.Close()
	if _, err := st.ListTemplateNames(); err != nil {
		t.Fatalf("开出的库应可查询: %v", err)
	}
}

// TestLedgerAlwaysOnAfterRetirement 未配 ledger 段时 card 族照常可用，
// DataDir 下正常落 ledger.db——B229 §2.6 退休后账本变必需品，本用例是
// 原「未启用即拒绝且不得自建」语义的翻转钉（TestLedgerDisabledByDefault）。
// 预写一份无 ledger 键的 config.yaml：runLedgerCLI 只在缺配置时才代写，
// 借此把「完全没配过账本」的老机器场景钉住。
func TestLedgerAlwaysOnAfterRetirement(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	c := &config.Config{Listen: "127.0.0.1:0", Token: testToken, DataDir: dir, StallTimeout: 2 * time.Hour}
	if err := config.Save(cfgPath, c); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil || strings.Contains(string(raw), "ledger") {
		t.Fatalf("前置条件失败：测试配置不应含 ledger 键（raw=%s err=%v）", raw, err)
	}
	_, _, execErr := runLedgerCLI(t, dir, "card", "add", "标题", "--project", "demo")
	if execErr != nil {
		t.Fatalf("退休后 card add 应照常执行，实际报错: %v", execErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ledger.db")); statErr != nil {
		t.Fatalf("账本必需后 openLedger 应在 DataDir 落 ledger.db: %v", statErr)
	}
}
