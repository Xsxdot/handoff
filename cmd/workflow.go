// handoff workflow 命令族：状态机形状聚合的命令面。不可变版本化——
// put 永远产生新版本；migrate 是三处破坏确认之一（批量改卡的呈现）。
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{Use: "workflow", Short: "工作流聚合（状态机形状，不可变版本化）"}

var (
	wfShowVersion     int
	wfPutFile         string
	wfMigrateWorkflow string
	wfMigrateColumn   string
	wfMigrateVersion  int
	wfMigrateYes      bool
)

var wfListCmd = &cobra.Command{
	Use: "list", Short: "列全部工作流（名 + 最新版本 + 状态序列）", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t最新版\t状态序列")
		for _, name := range []string{"feature", "bug", "triage"} { // 出厂三条恒在
			workflow, err := st.GetWorkflow(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\t%v\n", workflow.Name, workflow.Version, workflow.Def.States)
		}
		names, err := st.ListWorkflowNames()
		if err != nil {
			return err
		}
		for _, name := range names {
			if name == "feature" || name == "bug" || name == "triage" {
				continue
			}
			workflow, err := st.GetWorkflow(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\t%v\n", workflow.Name, workflow.Version, workflow.Def.States)
		}
		return w.Flush()
	},
}

var wfShowCmd = &cobra.Command{
	Use: "show <name>", Short: "看工作流定义（--version 指定版本，缺省最新；单 JSON）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		workflow, err := st.GetWorkflow(args[0], wfShowVersion)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(workflow)
	},
}

var wfPutCmd = &cobra.Command{
	Use: "put <name> --file <def.json>", Short: "写入新版本（不改旧版）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(wfPutFile)
		if err != nil {
			return fmt.Errorf("读定义文件: %w", err)
		}
		var def ledger.WorkflowDef
		if err := json.Unmarshal(raw, &def); err != nil {
			return fmt.Errorf("解析定义: %w", err)
		}
		if len(def.States) < 2 {
			return fmt.Errorf("状态序列至少两个状态")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		version, err := st.PutWorkflow(args[0], def)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"name": args[0], "version": version})
	},
}

var wfMigrateCmd = &cobra.Command{
	Use: "migrate <card-id>", Short: "把卡迁到显式工作流和落点列（需确认或 --yes）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(wfMigrateWorkflow) == "" || strings.TrimSpace(wfMigrateColumn) == "" {
			return fmt.Errorf("--workflow 与 --column 必填")
		}
		if wfMigrateVersion < 0 {
			return fmt.Errorf("--version 必须为 0 或正整数")
		}
		if err := confirmDestructive(cmd, wfMigrateYes,
			fmt.Sprintf("把 %s 迁到工作流 %s 的 %s 列", args[0], wfMigrateWorkflow, wfMigrateColumn)); err != nil {
			return err
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.MigrateCardWorkflow(args[0], wfMigrateWorkflow, wfMigrateVersion, wfMigrateColumn, ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	wfShowCmd.Flags().IntVar(&wfShowVersion, "version", 0, "指定版本（0=最新）")
	wfPutCmd.Flags().StringVar(&wfPutFile, "file", "", "定义 JSON 文件（必填）")
	_ = wfPutCmd.MarkFlagRequired("file")
	wfMigrateCmd.Flags().StringVar(&wfMigrateWorkflow, "workflow", "", "目标工作流（必填）")
	wfMigrateCmd.Flags().StringVar(&wfMigrateColumn, "column", "", "目标落点列（必填）")
	wfMigrateCmd.Flags().IntVar(&wfMigrateVersion, "version", 0, "目标版本（0=事务内取最新）")
	wfMigrateCmd.Flags().BoolVar(&wfMigrateYes, "yes", false, "跳过确认")
	_ = wfMigrateCmd.MarkFlagRequired("workflow")
	_ = wfMigrateCmd.MarkFlagRequired("column")
	workflowCmd.AddCommand(wfListCmd, wfShowCmd, wfPutCmd, wfMigrateCmd)
	rootCmd.AddCommand(workflowCmd)
}
