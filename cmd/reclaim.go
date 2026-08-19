// reclaim.go —— handoff reclaim 子命令：回收终态任务残留的 managed worktree。
//
// 职责：
//   - 无参：列出仍占着 managed worktree 的终态任务（净/脏/元数据残留/判不出）
//   - 带任务 id：回收那一个；脏树默认拒绝并报出改动清单，--force 才强删
//
// 边界：
//   - 不删任务分支（协调者的工作成果），每次成功输出都明说这一点
//   - 不删任务目录（失败任务的排查素材还在里面）
//   - 不改任务状态：回收前后 handoff show 看到的状态一致
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	reclaimForce bool
	reclaimJSON  bool
)

// reclaimCmd 列出或回收终态任务残留的 managed worktree。
//
// 使用方式：
//
//	handoff reclaim [--target <名字>] [--json]           列
//	handoff reclaim <task> [--target <名字>] [--force]   收
var reclaimCmd = &cobra.Command{
	Use:   "reclaim [task]",
	Short: "回收终态任务残留的 managed worktree（不删分支）",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cl, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		addr := "http://relay"
		if targetName == "" {
			addr, _, _ = TargetEndpoint()
		}
		if len(args) == 0 {
			return runReclaimList(cmd, cl, addr)
		}
		return runReclaimOne(cmd, cl, args[0], addr)
	},
}

func init() {
	reclaimCmd.Flags().BoolVar(&reclaimForce, "force", false,
		"对有未提交改动的工作树也强删（丢弃这些改动）")
	reclaimCmd.Flags().BoolVar(&reclaimJSON, "json", false, "以 JSON 输出（仅列表形态）")
	rootCmd.AddCommand(reclaimCmd)
}

// runReclaimList 列出残留。
//
// 注意：**恒退 0**。这是一份报告，「有残留」是它的正常结论而非失败；
// 只有拿不到列表（连不上、401、5xx）才退非零
func runReclaimList(cmd *cobra.Command, cl *client.Client, addr string) error {
	out := cmd.OutOrStdout()
	resp, err := cl.ReclaimList(cmd.Context())
	switch {
	case errors.Is(err, client.ErrReclaimUnsupported):
		// 与 status/footprint 同款：404 是一条成功的诊断结论，不是失败
		fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
		fmt.Fprintln(out, "限制     该 agentd 不支持 /api/reclaim，残留不可得")
		fmt.Fprintln(out, "处置     升级远端 agentd 后重试")
		return nil
	case err != nil:
		return err
	}
	if reclaimJSON {
		return json.NewEncoder(out).Encode(resp)
	}
	renderReclaimList(out, resp)
	return nil
}

// runReclaimOne 回收单个任务的工作树。
//
// 注意：脏树被拒时把清单渲染到 stdout，只让 cobra 往 stderr 打一行短因由——
// 详情给人看、单行给脚本看，两边都不被对方淹没
func runReclaimOne(cmd *cobra.Command, cl *client.Client, taskID, addr string) error {
	out := cmd.OutOrStdout()
	resp, err := cl.Reclaim(cmd.Context(), taskID, reclaimForce)
	var rej *client.ReclaimRejected
	switch {
	case errors.Is(err, client.ErrReclaimUnsupported):
		fmt.Fprintf(out, "agentd   %s   可用（版本过旧）\n", addr)
		fmt.Fprintln(out, "限制     该 agentd 不支持 worktree 回收")
		fmt.Fprintln(out, "处置     升级远端 agentd，或上机器手动 git worktree remove")
		return nil
	case errors.As(err, &rej):
		if rej.Reason == proto.ReasonDirty {
			renderDirtyRejection(out, taskID, "", rej.Dirty)
			return errors.New("未回收：工作树有未提交改动")
		}
		return err
	case err != nil:
		return err
	}
	renderReclaimResult(out, taskID, resp)
	return nil
}

// renderReclaimList 渲染残留列表。
//
// 注意：判不出的行**永远显示**。「没有残留」与「判不出」是两回事，把后者
// 按前者藏起来，等于用一个假结论把该看的东西盖住（同 renderFootprint 的规矩）
func renderReclaimList(w io.Writer, resp *proto.ReclaimListResp) {
	if len(resp.Rows) == 0 {
		fmt.Fprintf(w, "残留     无（共体检 %d 个终态任务）\n", resp.Scanned)
		return
	}
	fmt.Fprintf(w, "残留     %d 个终态任务仍占着 managed worktree（共体检 %d 个）\n",
		len(resp.Rows), resp.Scanned)
	for _, r := range resp.Rows {
		fmt.Fprintf(w, "  %s  %s  %s  %s  %s\n",
			short8(r.TaskID), r.Name, r.State, worktreeLabel(r), r.WorkDir)
	}
}

// worktreeLabel 把工作树状态渲染成人读标签。
func worktreeLabel(r proto.ReclaimRow) string {
	switch r.Worktree {
	case proto.WorktreeClean:
		return "净"
	case proto.WorktreeDirty:
		return fmt.Sprintf("脏（%d 项改动）", r.DirtyCount)
	case proto.WorktreePrunable:
		return "元数据残留（目录已不存在）"
	case proto.WorktreeUnknown:
		return "⚠ 判不出：" + r.Note
	default:
		return string(r.Worktree)
	}
}

// renderReclaimResult 渲染一次回收的结果。
func renderReclaimResult(w io.Writer, taskID string, resp *proto.ReclaimResp) {
	if resp.Action == proto.ReclaimAlreadyAbsent {
		fmt.Fprintf(w, "无残留   %s 的 managed worktree 已不在，无需回收\n", short8(taskID))
		return
	}
	fmt.Fprintf(w, "已回收   %s 的 managed worktree\n", short8(taskID))
	fmt.Fprintf(w, "工作树   %s（%s）\n", resp.WorkDir, actionLabel(resp.Action))
	if n := len(resp.Discarded); n > 0 {
		// 强删不能悄悄发生：丢了什么必须打出来
		fmt.Fprintf(w, "已丢弃   %d 项未提交改动\n", n)
		for _, f := range resp.Discarded {
			fmt.Fprintf(w, "         %s  %s\n", f.Status, f.Path)
		}
	}
	if resp.Branch != "" {
		fmt.Fprintf(w, "提示     任务分支 %s 保留——reclaim 不删分支\n", resp.Branch)
	}
}

// actionLabel 把回收动作渲染成人读标签。
func actionLabel(a proto.ReclaimAction) string {
	if a == proto.ReclaimPruned {
		return "在册条目已清理"
	}
	return "已删除"
}

// renderDirtyRejection 渲染脏树拒绝的详情。
func renderDirtyRejection(w io.Writer, taskID, workdir string, files []proto.DirtyFile) {
	fmt.Fprintln(w, "拒绝     工作树有未提交改动，未回收")
	if workdir != "" {
		fmt.Fprintf(w, "工作树   %s\n", workdir)
	}
	for i, f := range files {
		label := "改动    "
		if i > 0 {
			label = "        "
		}
		fmt.Fprintf(w, "%s %s  %s\n", label, f.Status, f.Path)
	}
	fmt.Fprintf(w, "         （共 %d 项）\n", len(files))
	fmt.Fprintf(w, "处置     确认可丢弃后重跑：handoff reclaim %s%s --force\n", taskID, targetArg())
}

// targetArg 把当前的 --target 还原成可粘贴的命令片段（本机模式返回空串）。
//
// 处置建议里必须带上它：远程任务只存在于那台机器的 agentd 上，漏掉 --target
// 的命令会打到本机，同样以 404 收场——那就又变成一条「照着敲必然失败」的建议。
func targetArg() string {
	if targetName == "" {
		return ""
	}
	return " --target " + targetName
}
