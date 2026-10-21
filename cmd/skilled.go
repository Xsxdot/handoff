// skilled.go —— 熟练工派发的 CLI 侧组装（B452 S1/S2/S5/S7/S8）。
//
// 职责：
//   - dispatch --template：开账本取定义（CLI 侧解析，Q1 甲）、零网络的 flag 互斥校验、
//     把定义与命令行覆盖合成为派发字段（接收者 / 形态 / 亲和 / 提示词）；
//   - 能力位核对（F13）：请求带形态、亲和或 --wait 时，目标 agentd 必须上报 skilled_dispatch_supported；
//   - 终态等待输出（F19–F21）：dispatch --wait 与 wait --final 共用，stdout 只有最终文本与可选的产物行；
//   - 产物路径解析（F20/F23）：同机直接用原路径，跨机下载解到本机临时目录。
//
// 边界：不解释模板正文语义；不改变远端任务状态（超时只结束等待）；不读写审核者 cursor。
package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// remoteMachinePrefix 是受理机转发后回传载体机错误时加的前缀（agentd prefixRemoteError）。
// CLI 据此识别「错误发生在执行机」：此时本机的自动补登记帮不上忙（Q13）。
const remoteMachinePrefix = "执行机 "

// skilledFields 是熟练工派发最终进 wire 的字段（定义 + 命令行覆盖合成后的结果）。
type skilledFields struct {
	receiver, executor, mode, machine, prompt, name string
	// planB64 非空 = --template 带了 plan 文件：模板正文 + 「## 本次任务」+ plan 文件内容（base64）。
	planB64 string
	here    bool
}

// loadDispatchTemplate 开账本取最新版定义。账本由调用方关闭。
//
// 模板不存在时报错点名模板，且此刻零网络（账本是本机文件或共享库，不经 agentd）。
func loadDispatchTemplate(name string) (*ledger.Template, *ledger.Store, error) {
	st, err := openLedger()
	if err != nil {
		return nil, nil, fmt.Errorf("--template 需要读账本: %w", err)
	}
	tpl, err := st.GetTemplate(name, 0)
	if err != nil {
		_ = st.Close()
		slog.Warn("熟练工定义取不到", "template", name, "cause", err)
		return nil, nil, fmt.Errorf("熟练工定义 %q 取不到（用 handoff template list 看有哪些）: %w", name, err)
	}
	return &tpl, st, nil
}

// effectiveAffinity 合成机器亲和：命令行 --here / --machine 覆盖定义的 target。
// 定义 target=here → here；其他非空值 → 机器名；空 → 无亲和。
func effectiveAffinity(tpl *ledger.Template) (machine string, here bool) {
	switch {
	case dispatchHere:
		return "", true
	case dispatchMachine != "":
		return dispatchMachine, false
	case tpl == nil:
		return "", false
	case tpl.Def.Target == ledger.TemplateTargetHere:
		return "", true
	default:
		return tpl.Def.Target, false
	}
}

// checkSkilledDispatchFlags 是 dispatch 的 B452 零网络前置校验（任何请求之前）。
//
// 参数：nArgs 位置参数个数（plan 文件）；tpl 为 nil 表示未带 --template。
func checkSkilledDispatchFlags(cmd *cobra.Command, nArgs int, tpl *ledger.Template) error {
	if cmd.Flags().Changed("timeout") && !dispatchWait {
		return fmt.Errorf("--timeout 只与 --wait 同用（它是等待时限，不是任务时限）")
	}
	if tpl != nil {
		if nArgs == 0 && dispatchPrompt == "" {
			return fmt.Errorf("--template 需要 --prompt 或 plan 文件给出本次任务（会接在模板正文后的「## 本次任务」一节）")
		}
		if tpl.Def.Mode == ledger.TemplateModeReadonly {
			for _, f := range []string{"branch", "new-branch", "worktree", "new-worktree"} {
				if cmd.Flags().Changed(f) {
					return fmt.Errorf("熟练工 %s 是只读形态，不能与 --%s 同用：只读任务在按基线检出的 detached 托管工作树里跑，不建分支、不用自带工作树", tpl.Name, f)
				}
			}
		}
	}
	if _, here := effectiveAffinity(tpl); here && targetName != "" {
		return fmt.Errorf("--here（或定义 target=here）不能与 --target 同用：带 --target 时受理机不是你所在的机器，「here」语义不清；要指定机器用 --machine")
	}
	return nil
}

// buildSkilledFields 合成派发字段：命令行显式值覆盖定义缺省。tpl 为 nil 时只取命令行。
//
// 本次任务文本的来源（spec 方案「或 plan 文件代替 --prompt」）：
//   - 只有 --prompt：提示词 = 模板正文 + 「## 本次任务」+ --prompt，走 wire 的 prompt；
//   - 有 plan 文件：plan 文件内容即本次任务，组装结果走 plan_b64；--prompt 若同给则原样走 prompt，
//     由 agentd 照裸派发语义拼成「## 附加指令（派发时提供）」接在其后（与不带 --template 时同一规则）。
func buildSkilledFields(tpl *ledger.Template, plan []byte, hasPlan bool) skilledFields {
	f := skilledFields{receiver: dispatchReceiver, executor: dispatchExecutor, prompt: dispatchPrompt, name: dispatchName}
	f.machine, f.here = effectiveAffinity(tpl)
	if tpl == nil {
		return f
	}
	if f.receiver == "" {
		f.receiver = tpl.Def.Receiver
	}
	if f.executor == "" {
		f.executor = tpl.Def.Executor
	}
	if f.name == "" {
		f.name = tpl.Name
	}
	f.mode = tpl.Def.Mode
	if hasPlan {
		f.planB64 = base64.StdEncoding.EncodeToString([]byte(ledgerstep.RenderCardlessPrompt(tpl.Def.Prompt, string(plan))))
		return f
	}
	f.prompt = ledgerstep.RenderCardlessPrompt(tpl.Def.Prompt, dispatchPrompt)
	return f
}

// needsSkilledCapability 报告这次派发是否依赖 B452 能力（F13）。
func needsSkilledCapability(f skilledFields, wait bool) bool {
	return f.mode != "" || f.machine != "" || f.here || wait
}

// statusOnceClient 给 dispatchClient 的 Status 加一次性缓存：缝 1 的纪律解析已经探活过一次，
// 能力位核对复用同一份结果（Q11「复用那次结果」），不多打一轮网络。
type statusOnceClient struct {
	dispatchClient
	status *proto.StatusResp
}

func (c *statusOnceClient) Status(ctx context.Context) (*proto.StatusResp, error) {
	if c.status != nil {
		return c.status, nil
	}
	st, err := c.dispatchClient.Status(ctx)
	if err == nil {
		c.status = st
	}
	return st, err
}

// checkSkilledCapability 核对目标 agentd 的熟练工能力位：缺席（nil）与 false 都按不支持拒发。
//
// why 缺席也拒：老 agentd 会静默忽略 mode / machine / here——只读请求会退化成在主仓原地
// 切分支，亲和会静默失效（Q11）。
func checkSkilledCapability(ctx context.Context, cli *statusOnceClient) error {
	st, err := cli.Status(ctx)
	if err != nil {
		return fmt.Errorf("目标机探活失败：请确认目标机可达、agentd 正在运行且 token 一致：%w", err)
	}
	if st.SkilledDispatchSupported == nil || !*st.SkilledDispatchSupported {
		slog.Warn("目标 agentd 不支持熟练工派发，拒发", "target", targetName,
			"cap_absent", st.SkilledDispatchSupported == nil)
		return fmt.Errorf("目标 agentd 版本过旧，不支持熟练工派发（只读形态 / 机器亲和 / --wait）；请升级 agentd 后重试")
	}
	return nil
}

// readonlyDirtyNotice 只读形态不拒脏树（它只读已提交的东西），但必须当场说清楚：
// 本地未提交改动对调查不可见。读不到工作区状态时静默（与 checkLocalWorktree 同款降级）。
func readonlyDirtyNotice(errOut io.Writer, sha string) {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return
	}
	tracked, _ := classifyLocalDirty(string(out))
	if len(tracked) == 0 {
		return
	}
	fmt.Fprintf(errOut, "提示: 只读调查基于已提交的 %s，本地未提交改动不可见（%d 处：%s）\n",
		shortSHA(sha), len(tracked), formatDirtyList(tracked))
}

// remoteMachineOf 识别「执行机 <M>：…」前缀的转发错误，返回机器名。
func remoteMachineOf(err error) (string, bool) {
	text, ok := client.ServerErrorText(err)
	if !ok || !strings.HasPrefix(text, remoteMachinePrefix) {
		return "", false
	}
	rest := strings.TrimPrefix(text, remoteMachinePrefix)
	i := strings.Index(rest, "：")
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}

// wrapDispatchError 把派发错误换成对外的退出码与可行动文案：422 → 退出码 4（F21）；
// 执行机侧「项目未登记」→ 点名去哪台机器登记（Q13，不自动补登记）。
func wrapDispatchError(err error) error {
	if err == nil {
		return nil
	}
	if code, ok := client.StatusCode(err); ok && code == 422 {
		slog.Warn("派发被机器亲和拒绝", "cause", err)
		return &exitCodeError{code: ExitAffinityRefused, err: err}
	}
	if m, ok := remoteMachineOf(err); ok && strings.Contains(err.Error(), projectNotRegisteredMarker) {
		return fmt.Errorf("%w\n提示: 项目在执行机 %s 上未登记——到那台机器的项目目录里执行 handoff project add（跨机自动补登记暂不支持）", err, m)
	}
	return err
}

// finalWaitClient 是终态等待与产物解析的消费面（接口定义在使用方）。
type finalWaitClient interface {
	WaitFinal(ctx context.Context, taskID, machine string) (*client.FinalOutcome, error)
	AttachOn(ctx context.Context, taskID, machine string) (*client.AttachInfo, error)
	Artifacts(ctx context.Context, taskID, machine string) (io.ReadCloser, error)
}

// runFinalWait 等任务最后一回合的终态并按 F19–F21 输出：
//   - 完成：stdout = final_text 原文（不以换行结尾时补一个）；产物非空再加一行 `artifacts: <本机绝对路径>`；退出 0；
//   - 非完成：stdout 空，退出码 3，错误文案含任务 id 与原因；
//   - 超时：退出码 124，任务照跑，文案给出接着等的命令；
//   - 有待答工单（B460 F1/F2）：stdout = 工单块（见 writePendingTickets），退出码 5，任务照跑。
//
// timeout<=0 表示不设时限。
func runFinalWait(cmd *cobra.Command, cli finalWaitClient, taskID, machine string, timeout time.Duration) error {
	ctx := cmd.Context()
	waitCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	slog.Info("等待任务终态", "task", taskID, "machine", machine, "timeout", timeout.String())
	errOut := cmd.ErrOrStderr()
	out, err := cli.WaitFinal(waitCtx, taskID, machine)
	resume := resumeCommand(taskID, machine)
	if err != nil {
		if timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("等待任务终态超时，任务照跑", "task", taskID, "machine", machine, "timeout", timeout.String())
			return &exitCodeError{code: ExitTimeout, err: fmt.Errorf(
				"等待任务 %s 终态超时（%s），任务照跑、未被停止；接着等：%s", taskID, timeout, resume)}
		}
		return err
	}
	if len(out.Pending) > 0 {
		ids := make([]string, 0, len(out.Pending))
		for _, tk := range out.Pending {
			ids = append(ids, tk.ID)
		}
		slog.Info("任务有待答工单，以退出码 5 交回人", "task", taskID, "machine", machine, "tickets", ids)
		if err := writePendingTickets(cmd.OutOrStdout(), taskID, resume, out.Pending); err != nil {
			return fmt.Errorf("写出待答工单: %w", err)
		}
		return &exitCodeError{code: ExitPendingTicket, err: fmt.Errorf(
			"任务 %s 有 %d 张待答工单（%s），答复后续等：%s", taskID, len(ids), strings.Join(ids, ", "), resume)}
	}
	if !out.Completed {
		slog.Warn("任务以非完成告终", "task", taskID, "machine", machine, "type", out.Event.Type, "reason", out.Reason)
		return &exitCodeError{code: ExitTaskFailed, err: fmt.Errorf(
			"任务 %s 未完成（%s）：%s", taskID, out.Event.Type, out.Reason)}
	}
	// 产物先解析、后输出：下载失败时最终文本照样输出，再以退出码 1 报产物故障。
	artifactsPath, aerr := localArtifactsPath(ctx, cli, taskID, machine, out.Task.ArtifactsDir)
	w := cmd.OutOrStdout()
	if out.HasFinalText {
		text := out.FinalText
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if _, err := io.WriteString(w, text); err != nil {
			return fmt.Errorf("写出最终文本: %w", err)
		}
	} else {
		fmt.Fprintf(errOut, "提示: 任务 %s 已完成，但执行器没有给出最终文本（老版本 adapter）；用 handoff show %s 看过程\n", taskID, taskID)
	}
	if aerr != nil {
		retry := "handoff artifacts " + taskID
		if machine != "" {
			retry += " --machine " + machine
		}
		return fmt.Errorf("任务 %s 已完成（最终文本已输出），但产物取回失败：%w；重试：%s", taskID, aerr, retry)
	}
	if artifactsPath != "" {
		if _, err := fmt.Fprintln(w, "artifacts: "+artifactsPath); err != nil {
			return fmt.Errorf("写出产物行: %w", err)
		}
	}
	slog.Info("任务终态已输出", "task", taskID, "machine", machine, "artifacts", artifactsPath)
	return nil
}

// artifactsClient 是产物解析的最小消费面。
type artifactsClient interface {
	Artifacts(ctx context.Context, taskID, machine string) (io.ReadCloser, error)
}

// localArtifactsPath 把任务产物变成本机可读路径；无产物返回空串。
//
// 同机判据（Q8）：本机能 Stat 到 artifactsDir 且是目录 → 直接用原路径（路径含完整任务 UUID，
// 他机不可能恰有同名目录）；否则经控制门面下载，解到 os.MkdirTemp 的新目录。
func localArtifactsPath(ctx context.Context, cli artifactsClient, taskID, machine, artifactsDir string) (string, error) {
	// 版本偏斜：执行机不报 artifacts_dir（B452 之前的 agentd）即视为无产物，不尝试下载。
	// 老执行机没有产物接口，下载只会换来一个误导性的 404/「版本过旧」。
	if artifactsDir == "" {
		slog.Info("任务未报告产物目录，按无产物处理", "task", taskID, "machine", machine)
		return "", nil
	}
	if fi, err := os.Stat(artifactsDir); err == nil && fi.IsDir() {
		entries, err := os.ReadDir(artifactsDir)
		if err != nil {
			return "", fmt.Errorf("读本机产物目录 %s: %w", artifactsDir, err)
		}
		if len(entries) == 0 {
			return "", nil
		}
		slog.Info("产物目录本机可读，直接使用原路径", "task", taskID, "dir", artifactsDir)
		return artifactsDir, nil
	}
	rc, err := cli.Artifacts(ctx, taskID, machine)
	if err != nil {
		return "", err
	}
	if rc == nil {
		return "", nil
	}
	defer rc.Close()
	dest, err := os.MkdirTemp("", "handoff-artifacts-"+id8(taskID)+"-")
	if err != nil {
		return "", fmt.Errorf("建本机产物临时目录: %w", err)
	}
	if err := client.ExtractArtifacts(rc, dest); err != nil {
		slog.Error("产物包解包失败，已整体丢弃", "task", taskID, "machine", machine, "cause", err)
		return "", err
	}
	slog.Info("产物已下载到本机", "task", taskID, "machine", machine, "dir", dest)
	return dest, nil
}

// modeLabel 是形态的人读名：空串即分支形态（F1）。
func modeLabel(mode string) string {
	if mode == "" {
		return ledger.TemplateModeBranch
	}
	return mode
}

// resumeCommand 是续等命令串：超时（124）与待答工单（5）共用同一构造。
func resumeCommand(taskID, machine string) string {
	resume := "handoff wait " + taskID + " --final"
	if machine != "" {
		resume += " --machine " + machine
	}
	return resume
}

// pendingTicketRequest 是工单请求体里退出码 5 要用的字段（生产方形状见
// orchestration.ticketRequest / approval.ticketRequest：kind=gate|ask）。
type pendingTicketRequest struct {
	Kind       string `json:"kind"`
	Permission string `json:"permission"`
	Question   string `json:"question"`
	Reason     string `json:"reason"`
}

// writePendingTickets 按 F2（B460 冻结）写出待答工单块：每张一块、块间空一行，最后一行续等命令。
//
//	ticket: <工单ID>
//	kind: permission | question
//	reason: <升级理由>            ← 仅请求体带 reason 时
//	--- 全文 ---
//	<请求体 permission / question 原文，不截断>
//	--- 全文完 ---
//	reply: handoff reply <任务ID> --ticket <工单ID> --approve          ← permission 两行
//	reply: handoff reply <任务ID> --ticket <工单ID> --deny --reason "<原因>"
//	reply: handoff reply <任务ID> --ticket <工单ID> --answer "<回答>"  ← question 一行
//
//	resume: handoff wait <任务ID> --final[ --machine <M>]
//
// 全文取工单记录（F3），不取事件 payload（那里截断到 200 rune）。请求体解不开时全文退回
// 请求原文，类型按工单 kind 判——宁可多打原文也不吞掉要人看的内容。
func writePendingTickets(w io.Writer, taskID, resume string, tickets []proto.Ticket) error {
	var b strings.Builder
	for i, tk := range tickets {
		if i > 0 {
			b.WriteString("\n")
		}
		var req pendingTicketRequest
		full := string(tk.Request)
		kindRaw := tk.Kind
		if err := json.Unmarshal(tk.Request, &req); err != nil {
			slog.Warn("待答工单请求体解析失败，全文退回原文", "task", taskID, "ticket", tk.ID, "cause", err)
		} else {
			if req.Kind != "" {
				kindRaw = req.Kind
			}
			switch {
			case kindRaw == "ask" && req.Question != "":
				full = req.Question
			case req.Permission != "":
				full = req.Permission
			}
		}
		kind := "permission"
		if kindRaw == "ask" {
			kind = "question"
		}
		b.WriteString("ticket: " + tk.ID + "\n")
		b.WriteString("kind: " + kind + "\n")
		if req.Reason != "" {
			b.WriteString("reason: " + req.Reason + "\n")
		}
		b.WriteString("--- 全文 ---\n" + full + "\n--- 全文完 ---\n")
		prefix := "reply: handoff reply " + taskID + " --ticket " + tk.ID
		if kind == "question" {
			b.WriteString(prefix + " --answer \"<回答>\"\n")
		} else {
			b.WriteString(prefix + " --approve\n")
			b.WriteString(prefix + " --deny --reason \"<原因>\"\n")
		}
	}
	b.WriteString("\nresume: " + resume + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}
