// handoff card 命令族：任务卡账本的命令面。谁是机器谁是人分得清：
// stdout 只出机器 JSON（一行一对象；list 缺省表格是唯一例外，--json
// 切换），人话走 stderr。状态名用中文原文（与 workflow 定义一致），
// 不设英文别名。
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var cardCmd = &cobra.Command{Use: "card", Short: "任务卡账本（工作项的建/查/流转/合并/拆分）"}

var (
	cardAddProject, cardAddPriority, cardAddParent, cardAddWorkflow, cardAddBase string
	cardListStatus, cardListProject, cardListBase                                string
	cardListBlocked, cardListNeeds, cardListJSON, cardListAll                    bool
	cardMoveExpect                                                               string
	cardUpdateTitle, cardUpdatePriority, cardUpdateAccept                        string
	cardUpdateAttach, cardUpdateDetach                                           string
)

var cardAddCmd = &cobra.Command{
	Use:   "add <标题>",
	Short: "建卡（B 号自动分配；--parent 建子卡）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		card, err := st.CreateCard(ledger.NewCard{
			Title: strings.Join(args, " "), Project: cardAddProject,
			Priority: cardAddPriority, Parent: cardAddParent,
			Workflow: cardAddWorkflow, BaseBranch: cardAddBase, Actor: ledgerActor(),
		})
		if err != nil {
			return fmt.Errorf("建卡: %w", err)
		}
		return printCardJSON(cmd, card)
	},
}

var cardListCmd = &cobra.Command{
	Use:   "list",
	Short: "列卡（含派生标记；缺省表格，--json 一行一对象）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		views, err := st.ListCards(ledger.CardFilter{
			Project: cardListProject, Status: cardListStatus, BaseBranch: cardListBase,
			Blocked: cardListBlocked, Needs: cardListNeeds, IncludeTerminal: cardListAll,
		})
		if err != nil {
			return fmt.Errorf("列卡: %w", err)
		}
		if cardListJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, view := range views {
				if err := enc.Encode(cardViewWire(view)); err != nil {
					return err
				}
			}
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t状态\t优先级\t标题\t标记")
		for _, view := range views {
			var marks []string
			if view.Following != "" {
				marks = append(marks, "跟随 "+view.Following)
			}
			if view.Blocked {
				marks = append(marks, "blocked:"+strings.Join(view.BlockedBy, ","))
			}
			if view.NeedsReason != "" {
				marks = append(marks, "⚑ "+view.NeedsReason)
			}
			if view.OpenDecisions > 0 {
				marks = append(marks, fmt.Sprintf("⚖ %d", view.OpenDecisions))
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", view.ID, view.Status, view.Priority, view.Title, strings.Join(marks, " "))
		}
		return w.Flush()
	},
}

var cardShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "看卡：字段 + 关系 + 挂账 task + 最近事件（单 JSON 对象）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		card, err := st.GetCard(args[0])
		if err != nil {
			return err
		}
		relations, err := st.RelationsOf(card.ID)
		if err != nil {
			return err
		}
		links, err := st.TasksOf(card.ID)
		if err != nil {
			return err
		}
		events, err := st.EventsFromAsc([]string{card.ID}, 0, 200)
		if err != nil {
			return err
		}
		base, err := st.EffectiveBaseBranch(card.ID)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"card": card, "effective_base_branch": base,
			"relations": relations, "tasks": links, "events": events,
		})
	},
}

var cardUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "改卡：--title/--priority/--attach kind:path/--detach path/--accept 判据",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, actor := args[0], ledgerActor()
		if cardUpdateAttach != "" {
			kind, path, ok := strings.Cut(cardUpdateAttach, ":")
			if !ok {
				return fmt.Errorf("--attach 形如 kind:path（如 spec:specs/x.md）")
			}
			if err := st.AttachFile(id, kind, path, actor); err != nil {
				return err
			}
		}
		if cardUpdateDetach != "" {
			if err := st.DetachFile(id, cardUpdateDetach, actor); err != nil {
				return err
			}
		}
		if cardUpdateAccept != "" {
			if err := st.SetAcceptance(id, cardUpdateAccept, actor); err != nil {
				return err
			}
		}
		if cardUpdateTitle != "" || cardUpdatePriority != "" {
			if err := st.UpdateCardMeta(id, cardUpdateTitle, cardUpdatePriority, actor); err != nil {
				return err
			}
		}
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		return printCardJSON(cmd, card)
	},
}

var cardMoveCmd = &cobra.Command{
	Use:   "move <id> <状态>",
	Short: "状态转移（CAS；--expect 显式钉前值；gate 拒绝会说清缺什么）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.MoveCard(args[0], args[1], cardMoveExpect, ledgerActor()); err != nil {
			return err
		}
		card, err := st.GetCard(args[0])
		if err != nil {
			return err
		}
		return printCardJSON(cmd, card)
	},
}

// printCardJSON stdout 单行卡 JSON（机器契约）。
func printCardJSON(cmd *cobra.Command, card ledger.Card) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(card)
}

// cardViewWire 列表行的线格式（字段名稳定，看板/脚本共用词汇）。
func cardViewWire(view ledger.CardView) map[string]any {
	return map[string]any{
		"id": view.ID, "title": view.Title, "status": view.Status, "priority": view.Priority,
		"project": view.Project, "parent": view.ParentID, "base_branch": view.BaseBranch,
		"following": view.Following, "blocked": view.Blocked, "blocked_by": view.BlockedBy,
		"needs": view.NeedsReason, "open_decisions": view.OpenDecisions,
	}
}

func init() {
	cardAddCmd.Flags().StringVar(&cardAddProject, "project", "", "项目名（必填）")
	cardAddCmd.Flags().StringVar(&cardAddPriority, "priority", "中", "高|中|低")
	cardAddCmd.Flags().StringVar(&cardAddParent, "parent", "", "父卡 id（建子卡）")
	cardAddCmd.Flags().StringVar(&cardAddWorkflow, "workflow", "feature", "工作流名")
	cardAddCmd.Flags().StringVar(&cardAddBase, "base-branch", "", "基线分支（空=继承/主线）")
	_ = cardAddCmd.MarkFlagRequired("project")

	cardListCmd.Flags().StringVar(&cardListProject, "project", "", "按项目过滤")
	cardListCmd.Flags().StringVar(&cardListStatus, "status", "", "按状态过滤")
	cardListCmd.Flags().StringVar(&cardListBase, "base-branch", "", "按有效基线过滤")
	cardListCmd.Flags().BoolVar(&cardListBlocked, "blocked", false, "只列被阻塞的")
	cardListCmd.Flags().BoolVar(&cardListNeeds, "needs", false, "只列需要你的（等人/裁决）")
	cardListCmd.Flags().BoolVar(&cardListJSON, "json", false, "一行一 JSON 对象")
	cardListCmd.Flags().BoolVar(&cardListAll, "all", false, "含已完成/终止")

	cardMoveCmd.Flags().StringVar(&cardMoveExpect, "expect", "", "CAS 前值（脚本场景钉死）")

	cardUpdateCmd.Flags().StringVar(&cardUpdateTitle, "title", "", "改标题")
	cardUpdateCmd.Flags().StringVar(&cardUpdatePriority, "priority", "", "改优先级")
	cardUpdateCmd.Flags().StringVar(&cardUpdateAttach, "attach", "", "挂附件 kind:path")
	cardUpdateCmd.Flags().StringVar(&cardUpdateDetach, "detach", "", "摘附件 path")
	cardUpdateCmd.Flags().StringVar(&cardUpdateAccept, "accept", "", "设验收判据")

	cardCmd.AddCommand(cardAddCmd, cardListCmd, cardShowCmd, cardUpdateCmd, cardMoveCmd)
	rootCmd.AddCommand(cardCmd)
}
