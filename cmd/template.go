// handoff template 命令族：派发配方的查询与不可变版本写入。
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "派发模板聚合（不可变版本化）",
}

var (
	tplShowVersion int
	tplPutFile     string
)

var tplListCmd = &cobra.Command{
	Use:   "list",
	Short: "列全部派发模板（名 + 最新版本）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		names, err := st.ListTemplateNames()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t最新版")
		for _, name := range names {
			tpl, err := st.GetTemplate(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\n", tpl.Name, tpl.Version)
		}
		return w.Flush()
	},
}

var tplShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "看派发模板定义（--version 指定版本，缺省最新；单 JSON）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		tpl, err := st.GetTemplate(args[0], tplShowVersion)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(tpl)
	},
}

var tplPutCmd = &cobra.Command{
	Use:   "put <name> --file <def.json>",
	Short: "写入派发模板新版本（不改旧版）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(tplPutFile)
		if err != nil {
			return fmt.Errorf("读定义文件: %w", err)
		}
		var def ledger.TemplateDef
		if err := json.Unmarshal(raw, &def); err != nil {
			return fmt.Errorf("解析定义: %w", err)
		}
		if def.Executor == "" || def.Prompt == "" || def.DisciplinePath == "" {
			return fmt.Errorf("executor/prompt/discipline_path 三者必填")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		version, err := st.PutTemplate(args[0], def)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"name": args[0], "version": version})
	},
}

func init() {
	tplShowCmd.Flags().IntVar(&tplShowVersion, "version", 0, "指定版本（0=最新）")
	tplPutCmd.Flags().StringVar(&tplPutFile, "file", "", "定义 JSON 文件（必填）")
	_ = tplPutCmd.MarkFlagRequired("file")
	templateCmd.AddCommand(tplListCmd, tplShowCmd, tplPutCmd)
	rootCmd.AddCommand(templateCmd)
}
