// handoff discipline 命令族：纪律块正文账本权威副本（B229 缝 2）的人手
// 读写口。职责：读文件、调 internal/ledger 的 PutDiscipline/GetDiscipline/
// ListDisciplineNames 并透传结果；名字与正文的全部校验规则都在库层，
// 本文件不复制任何一条——错误文案即库层原文，可行动性由库保证。
package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var disciplineCmd = &cobra.Command{
	Use:   "discipline",
	Short: "纪律块聚合（不可变版本化，账本权威副本）",
}

var discGetVersion int

var discListCmd = &cobra.Command{
	Use:   "list",
	Short: "列全部纪律块（名 + 最新版本）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		names, err := st.ListDisciplineNames()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t最新版")
		for _, name := range names {
			d, err := st.GetDiscipline(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\n", d.Name, d.Version)
		}
		return w.Flush()
	},
}

var discGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "看纪律块正文（--version 指定版本，缺省最新）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		d, err := st.GetDiscipline(args[0], discGetVersion)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "%s v%d\n", d.Name, d.Version)
		fmt.Fprint(out, d.Body)
		if !strings.HasSuffix(d.Body, "\n") {
			fmt.Fprintln(out)
		}
		return nil
	},
}

var discPutCmd = &cobra.Command{
	Use:   "put <name> <file>",
	Short: "写入纪律块新版本（不改旧版）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return fmt.Errorf("读正文文件: %w", err)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		version, err := st.PutDiscipline(args[0], string(raw))
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "{\"name\":%q,\"version\":%d}\n", args[0], version)
		return nil
	},
}

func init() {
	discGetCmd.Flags().IntVar(&discGetVersion, "version", 0, "指定版本（0=最新）")
	disciplineCmd.AddCommand(discListCmd, discGetCmd, discPutCmd)
	rootCmd.AddCommand(disciplineCmd)
}
