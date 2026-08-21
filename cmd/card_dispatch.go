// card dispatch：按模板拼装 prompt，带上纪律块**角色名**（正文由 agentd 注入），
// 走既有 dispatch 通道；
// 派发即认领（CAS 进「进行中」就是 claim，第二个会话干净失败）；
// task 回链 + 模板版本/纪律角色名快照落事件。
package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/spf13/cobra"
)

var (
	cardDispatchTemplate   string
	cardDispatchTarget     string
	cardDispatchPlan       string
	cardDispatchDiscipline string
	cardDispatchStep       string
)

type dispatchRequest struct {
	prompt string
	branch string
	// 每张卡在目标机上开自己的工作树。共用主工作树时第二张卡会撞
	// 「目标工作目录已被活跃任务占用」409——而看板的前提就是多张卡
	// 同时在飞，共用工作树等于一次只能干一张
	newWorktree bool
	// 审阅轮跑在已有工作分支上：只读、不新开分支。空 = 新开 branch
	existingBranch string
	target         string
	project        string
	executor       string
	// discipline 是本次派发点名的纪律块角色名；空=让 agentd 按 executor 兜底。
	// 只传名字不传正文：正文由 agentd 解析注入，CLI 不再是纪律块的搬运工。
	discipline string
	model      string
	planB64    string
	planName   string
	base       string
}

// dispatchTransport 是派发前逻辑的测试缝。生产路径由
// dispatchTransportWithOpts 走 client.Dispatch；保留这个四参数缝是为了让
// 单测只关心 prompt、分支、目标机与项目四个派发前事实。
var dispatchTransport = func(prompt, branch, target, project string) (string, error) {
	cl, done, err := targetClient(target)
	if err != nil {
		return "", err
	}
	defer done()
	task, err := cl.Dispatch(context.Background(), client.DispatchOpts{
		Prompt: prompt, NewBranch: branch, Target: target, ProjectName: project,
	})
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

var dispatchTransportWithOpts = func(req dispatchRequest) (string, error) {
	cl, done, err := targetClient(req.target)
	if err != nil {
		return "", err
	}
	defer done()
	task, err := cl.Dispatch(context.Background(), client.DispatchOpts{
		Prompt: req.prompt, Target: req.target,
		NewBranch: req.branch, Branch: req.existingBranch,
		ProjectName: req.project, Executor: req.executor, Model: req.model,
		Discipline: req.discipline,
		PlanB64:    req.planB64, PlanName: req.planName, Base: req.base,
		NewWorktree: req.newWorktree,
	})
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

// cliTransport 把 CLI 的派发通道适配成 ledgerstep.Transport。
//
// 保留 dispatchTransportWithOpts 这层间接：它是 cmd 包既有的测试缝
// （swapDispatchTransport / swapDispatchTransportWithOpts），认领与租约的
// 用例还挂在上面。
func cliTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, error) {
	return dispatchTransportWithOpts(dispatchRequest{
		prompt: opts.Prompt, branch: opts.Branch, target: opts.Target, project: opts.Project,
		executor: opts.Executor, model: opts.Model, planB64: opts.PlanB64,
		planName: opts.PlanName, base: opts.Base, existingBranch: opts.ExistingBranch,
		discipline: opts.Discipline, newWorktree: opts.NewWorktree,
	})
}

// swapDispatchTransport 替换网络派发段；测试恢复原实现。
func swapDispatchTransport(fn func(prompt, branch, target, project string) (string, error)) func() {
	old := dispatchTransport
	oldWithOpts := dispatchTransportWithOpts
	dispatchTransport = fn
	dispatchTransportWithOpts = func(req dispatchRequest) (string, error) {
		return dispatchTransport(req.prompt, req.branch, req.target, req.project)
	}
	return func() {
		dispatchTransport = old
		dispatchTransportWithOpts = oldWithOpts
	}
}

// swapDispatchTransportWithOpts 只替换携带完整请求的派发段；测试恢复原实现。
//
// 为什么不复用 swapDispatchTransport：那条缝刻意只暴露 prompt/branch/target/project
// 四个标量（见它的注释），纪律块名字这类新字段到不了回调手上。两条缝并存，
// 各测各的关注面，既有用例不必为新字段改回调。
func swapDispatchTransportWithOpts(fn func(dispatchRequest) (string, error)) func() {
	old := dispatchTransportWithOpts
	dispatchTransportWithOpts = fn
	return func() { dispatchTransportWithOpts = old }
}

// targetClient 按登记名取一个可用的 agentd 客户端。
//
// why 不再解析出 (addr, token) 自己 client.New：relay 形态的机器**没有
// addr**，直连构造对它们恒失败（会退化成一个没有 Host 的 URL）。选路判据
// 只允许有一份，在 internal/targetclient，CLI 与 agentd 共用。
//
// 返回的 cleanup 关闭本次可能建立的 relay 隧道，调用方必须调用（直连形态
// 是 no-op）。target 为空视为未指定，直接报错而不是退回本机——环节派发的
// 目标机由 --target 或模板给出，静默换一台机器是更坏的失败。
func targetClient(target string) (*client.Client, func(), error) {
	if target == "" {
		return nil, func() {}, fmt.Errorf("未指定目标机（--target 或模板 target 至少一个）")
	}
	if _, ok := loadCLIConfig().Targets[target]; !ok {
		return nil, func() {}, fmt.Errorf("目标机 %s 未登记（handoff init/机器登记先行）", target)
	}
	return newTargetClientNamed(target)
}

var cardDispatchCmd = &cobra.Command{
	Use:   "dispatch <id>",
	Short: "按模板派发（派发即认领；--step 走工作流节点）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, actor := args[0], ledgerActor()
		if cardDispatchStep != "" {
			return runStepDispatch(cmd, st, id, cardDispatchStep, actor)
		}
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		if card.Status == ledger.StatusDoing {
			return fmt.Errorf("卡 %s 已被认领（驱动 %s）", id, card.DriverSession)
		}
		// 原子认领：转状态与落驱动同事务。分两步写会留出「进行中但驱动
		// 为空」的窗口，并发输家读到它就报不出认领者是谁（判据⑥要会话名）
		if err := st.ClaimCard(id, ledger.StatusDoing, card.Status, ledgerSession()); err != nil {
			// 报文只出一次：库层在「读到时已有驱动」时自带会话名，CAS 竞态
			// 里则没有，这里补读一次；两条路径都只包哨兵不包文案，避免套娃
			if current, getErr := st.GetCard(id); getErr == nil && current.DriverSession != "" &&
				current.DriverSession != ledgerSession() {
				return fmt.Errorf("卡 %s 已被 %s 认领: %w", id, current.DriverSession, ledger.ErrCASConflict)
			}
			return fmt.Errorf("认领失败（可能被并发抢先）: %w", err)
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		dispatcher := &ledgerstep.Dispatcher{St: st, Transport: cliTransport, Actor: actor}
		result, err := dispatcher.ViaTemplate(ctx, card, ledgerstep.TemplateDispatch{
			Template:           cardDispatchTemplate,
			Target:             cardDispatchTarget,
			PlanPath:           cardDispatchPlan,
			DisciplineOverride: cardDispatchDiscipline,
		})
		if err != nil {
			// 回滚要连租约一起退：只退状态会把卡留在「待办但有主」，
			// 驱动身份带 pid，本人换个进程重试都会被自己挡住（见 ReleaseCard）
			_ = st.MoveCard(id, card.Status, ledger.StatusDoing, actor)
			_ = st.ReleaseCard(id, ledgerSession())
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	},
}

func init() {
	cardDispatchCmd.Flags().StringVar(&cardDispatchTemplate, "template", "feature-impl", "派发模板名")
	cardDispatchCmd.Flags().StringVar(&cardDispatchTarget, "target", "", "目标机（覆盖模板）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchPlan, "plan", "", "plan 文件路径（挂派发事件）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchDiscipline, "discipline-override", "", "覆盖模板指定的纪律块角色名（如 review；测试/应急）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchStep, "step", "", "节点名（= 看板列名），从卡钉住的工作流里查；不给则不跑节点")
	cardCmd.AddCommand(cardDispatchCmd)
}
