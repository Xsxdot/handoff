// card dispatch：按模板拼装 prompt，纪律块正文由协调者按角色名从账本组装、
// 派发时随请求下发（B229），走既有 dispatch 通道；
// 派发即认领归属（不动状态列；运行互斥归账本运行锁）；
// task 回链 + 模板版本/纪律角色名快照落事件。
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
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
	cardDispatchExecutor   string
	cardDispatchModel      string
	cardDispatchExtra      string
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
	// discipline 是本次派发点名的纪律块角色名；空=未点名（只注入平台层）。
	// B229：名字仅作审计展示，正文由本进程在认领前经缝 1 组装后随请求下发
	// （disciplineText/DisciplineVersion），执行机收文即用不再自行解析。
	discipline         string
	disciplineText     string
	disciplineVersion  int
	model              string
	planB64            string
	planName           string
	base               string
	resolveDefaultBase bool
	localBaseBranch    bool
}

// dispatchTransport 是派发前逻辑的测试缝。生产路径由
// dispatchTransportWithOpts 走 client.Dispatch；保留这个四参数缝是为了让
// 单测只关心 prompt、分支、目标机与项目四个派发前事实；返回值仍携带目标
// agentd 原样回传的 BaseCommit。
var dispatchTransport = func(prompt, branch, target, project string) (string, string, error) {
	canonical, err := canonicalCLITarget(target)
	if err != nil {
		return "", "", err
	}
	cl, done, err := targetClient(canonical)
	if err != nil {
		return "", "", err
	}
	defer done()
	task, err := cl.Dispatch(context.Background(), client.DispatchOpts{
		Prompt: prompt, NewBranch: branch, Target: canonical, ProjectName: project,
	})
	if err != nil {
		return "", "", err
	}
	return task.ID, task.BaseCommit, nil
}

var dispatchTransportWithOpts = func(req dispatchRequest) (string, string, error) {
	canonical, err := canonicalCLITarget(req.target)
	if err != nil {
		return "", "", err
	}
	cl, done, err := targetClient(canonical)
	if err != nil {
		return "", "", err
	}
	defer done()
	task, err := cl.Dispatch(context.Background(), client.DispatchOpts{
		Prompt: req.prompt, Target: canonical,
		NewBranch: req.branch, Branch: req.existingBranch,
		ProjectName: req.project, Executor: req.executor, Model: req.model,
		Discipline:        req.discipline,
		DisciplineText:    req.disciplineText,
		DisciplineVersion: req.disciplineVersion,
		PlanB64:           req.planB64, PlanName: req.planName, Base: req.base,
		ResolveDefaultBase: req.resolveDefaultBase,
		LocalBaseBranch:    req.localBaseBranch,
		NewWorktree:        req.newWorktree,
	})
	if err != nil {
		return "", "", err
	}
	return task.ID, task.BaseCommit, nil
}

// cliTransport 把 CLI 的派发通道适配成 ledgerstep.Transport。
//
// 保留 dispatchTransportWithOpts 这层间接：它是 cmd 包既有的测试缝
// （swapDispatchTransport / swapDispatchTransportWithOpts），认领与租约的
// 用例还挂在上面。
func cliTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, string, error) {
	return dispatchTransportWithOpts(dispatchRequest{
		prompt: opts.Prompt, branch: opts.Branch, target: opts.Target, project: opts.Project,
		executor: opts.Executor, model: opts.Model, planB64: opts.PlanB64,
		planName: opts.PlanName, base: opts.Base, existingBranch: opts.ExistingBranch,
		discipline: opts.Discipline,
		// B229：ViaTemplate 透传下来的缝 1 产物（Dispatcher 数据字段），原样上 wire。
		disciplineText:     opts.DisciplineText,
		disciplineVersion:  opts.DisciplineVersion,
		newWorktree:        opts.NewWorktree,
		resolveDefaultBase: opts.ResolveDefaultBase,
		localBaseBranch:    opts.LocalBaseBranch,
	})
}

// swapDispatchTransport 替换网络派发段；测试恢复原实现。
func swapDispatchTransport(fn func(prompt, branch, target, project string) (string, string, error)) func() {
	old := dispatchTransport
	oldWithOpts := dispatchTransportWithOpts
	dispatchTransport = fn
	dispatchTransportWithOpts = func(req dispatchRequest) (string, string, error) {
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
func swapDispatchTransportWithOpts(fn func(dispatchRequest) (string, string, error)) func() {
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
// canonicalCLITarget 把已登记且指向本机的 target 归一为空串。
//
// 空串本身就是本机；未知名称保留原名并返回可行动错误，不增加“本机”或
// “localhost”魔法别名。
func canonicalCLITarget(target string) (string, error) {
	if target == "" {
		return "", nil
	}
	cfg, err := config.Load(effectiveConfigPath())
	if err != nil {
		return "", fmt.Errorf("加载配置: %w", err)
	}
	t, ok := cfg.Targets[target]
	if !ok {
		return target, fmt.Errorf("目标机 %s 未登记（handoff init/机器登记先行）", target)
	}
	if config.IsSelfTarget(cfg.Listen, t) {
		return "", nil
	}
	return target, nil
}

// targetClient 按规范目标取得一个可用的 agentd 客户端。
//
// 返回的 cleanup 关闭本次可能建立的 relay 隧道，调用方必须调用（直连形态
// 是 no-op）。target 为空或登记到本机的 loopback 地址都走 LocalEndpoint；
// 未登记名称仍原样拒绝。
func targetClient(target string) (*client.Client, func(), error) {
	canonical, err := canonicalCLITarget(target)
	if err != nil {
		return nil, func() {}, err
	}
	if canonical == "" {
		addr, token, err := LocalEndpoint()
		if err != nil {
			return nil, func() {}, err
		}
		slog.Info("CLI 采用本机客户端", "target", target, "canonical_target", canonical)
		return client.New(addr, token), func() {}, nil
	}
	slog.Info("CLI 采用远端客户端", "target", canonical, "canonical_target", canonical)
	return newTargetClientNamed(canonical)
}

// resolveCardDispatchTemplate 按账本解析裸 card dispatch 的模板。
//
// 参数：st 已打开的账本；全局 cardDispatchTemplate 是显式 --template 值。
// 返回：显式模板原样返回；无显式值时唯一模板自动返回，零/多模板返回可行动错误。
// 注意：仅供不带 --step 的裸 dispatch 使用；工作流节点模板由节点定义决定。
func resolveCardDispatchTemplate(st *ledger.Store) (string, error) {
	if strings.TrimSpace(cardDispatchTemplate) != "" {
		slog.Info("采用显式派发模板", "template", cardDispatchTemplate)
		return cardDispatchTemplate, nil
	}
	slog.Info("解析缺省派发模板")
	names, err := st.ListTemplateNames()
	if err != nil {
		slog.Warn("列派发模板失败", "cause", err)
		return "", fmt.Errorf("解析缺省派发模板: 列模板失败: %w", err)
	}
	switch len(names) {
	case 0:
		err := fmt.Errorf("派发缺少模板：账本中没有模板，请先用 template put 建立一条模板")
		slog.Warn("裸派发被拒：账本没有模板")
		return "", err
	case 1:
		slog.Info("裸派发采用唯一模板", "template", names[0])
		return names[0], nil
	default:
		err := fmt.Errorf("派发缺少模板：账本中有多条模板，请显式指定 --template（可选：%s）", strings.Join(names, "、"))
		slog.Warn("裸派发被拒：缺省模板有歧义", "templates", names)
		return "", err
	}
}

// resolveCardDispatchDiscipline 是 CLI 模板派发的缝 1 收口（B229 契约 §2.2）：
// 绑账本 lookup 与目标机能力位探活各一次，经 discipline.ResolveDispatch 产出
// 随派发下发的正文三元组。探活失败保留网络/认证 cause 并停止派发；只有探活成功
// 但能力位缺席=nil 或 false 才产生「升级」拒发文案。未点名模板的产物是纯平台层
// 正文、版本 0（§3.1 拒发闸覆盖一切带正文派发）。
func resolveCardDispatchDiscipline(ctx context.Context, st *ledger.Store, name, target string) (discipline.ResolvedDiscipline, error) {
	lookup := func(n string) (int, string, error) {
		d, err := st.GetDiscipline(n, 0)
		if err != nil {
			return 0, "", err
		}
		return d.Version, d.Body, nil
	}
	var cap *bool
	cl, done, err := targetClient(target)
	if err != nil {
		slog.Error("模板派发前取得目标机客户端失败", "target", target, "cause", err)
		return discipline.ResolvedDiscipline{}, fmt.Errorf(
			"目标机探活失败：请确认目标机可达、agentd 正在运行且 token 一致：%w", err)
	}
	defer done()
	status, err := cl.Status(ctx)
	if err != nil {
		slog.Error("模板派发前目标机探活失败", "target", target, "cause", err)
		return discipline.ResolvedDiscipline{}, fmt.Errorf(
			"目标机探活失败：请确认目标机可达、agentd 正在运行且 token 一致：%w", err)
	}
	cap = status.DisciplinesSupported
	res, err := discipline.ResolveDispatch(lookup, discipline.DisciplineRef{Name: name},
		loadCLIConfig().PlatformInvariantsEnabled(), cap)
	if err != nil {
		return discipline.ResolvedDiscipline{}, err
	}
	slog.Info("模板派发纪律正文已就绪", "target", target,
		"discipline", name, "version", res.Version, "bytes", len(res.Text))
	return res, nil
}

var cardDispatchCmd = &cobra.Command{
	Use:   "dispatch <id>",
	Short: "按模板派发（派发即认领；--step 走工作流节点）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if cardDispatchStep != "" {
			return runStepDispatch(cmd, id, cardDispatchStep)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		templateName, err := resolveCardDispatchTemplate(st)
		if err != nil {
			return err
		}
		actor := ledgerActor()
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		if card.DriverSession != "" && card.DriverSession != actor {
			return fmt.Errorf("卡 %s 已由 %s 认领: %w", id, card.DriverSession, ledger.ErrCASConflict)
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// B229 缝 1：解析在认领之前完成——拒发发生在任何状态迁移之前，零半状态。
		// 有效（角色名，目标机）与 ViaTemplate 同序（共用 PreflightDiscipline）。
		discName, discTarget, err := ledgerstep.PreflightDiscipline(st, templateName, cardDispatchDiscipline, cardDispatchTarget)
		if err != nil {
			return err
		}
		discTarget, err = canonicalCLITarget(discTarget)
		if err != nil {
			return err
		}
		var resolved discipline.ResolvedDiscipline
		resolved, err = resolveCardDispatchDiscipline(ctx, st, discName, discTarget)
		if err != nil {
			slog.Warn("裸卡派发被拒发闸拦下", "card", id, "discipline", discName,
				"target", discTarget, "cause", err)
			return err
		}
		// B239 认领只写归属、不动状态列（运行互斥归账本运行锁）；
		// B229 的拒发闸在它之前完成，所以拒发时零半状态这条仍然成立。
		if err := st.ClaimCard(id, actor); err != nil {
			return fmt.Errorf("认领失败: %w", err)
		}
		dispatcher := &ledgerstep.Dispatcher{
			St: st, Transport: cliTransport, Actor: actor,
			DisciplineText:    resolved.Text,
			DisciplineVersion: resolved.Version,
			NormalizeTarget: func(target string) string {
				canonical, err := canonicalCLITarget(target)
				if err != nil {
					slog.Warn("CLI 派发目标归一失败，保留原值", "target", target, "cause", err)
					return target
				}
				return canonical
			},
		}
		result, err := dispatcher.ViaTemplate(ctx, card, ledgerstep.TemplateDispatch{
			Template:           templateName,
			Target:             cardDispatchTarget,
			PlanPath:           cardDispatchPlan,
			DisciplineOverride: cardDispatchDiscipline,
			ExecutorOverride:   cardDispatchExecutor,
			ModelOverride:      cardDispatchModel,
			Extra:              cardDispatchExtra,
		})
		if err != nil {
			// 回滚只退归属；没有状态转移需要回退，归属不带 pid，
			// 同一人换个进程也能自己清掉。
			_ = st.ReleaseCard(id, actor)
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	},
}

func init() {
	cardDispatchCmd.Flags().StringVar(&cardDispatchTemplate, "template", "", "派发模板名（缺省时账本仅有一条模板才自动采用）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchTarget, "target", "", "目标机（覆盖模板）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchPlan, "plan", "", "plan 文件路径（挂派发事件）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchExtra, "extra", "", "本次派发的一次性补充说明（进 prompt 的「本次补充」小节；不落卡，不影响后续轮次）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchDiscipline, "discipline-override", "", "覆盖模板指定的纪律块角色名（如 review；测试/应急）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchStep, "step", "", "节点名（= 看板列名），从卡钉住的工作流里查；不给则不跑节点")
	cardDispatchCmd.Flags().StringVar(&cardDispatchExecutor, "executor", "", "一次性覆盖模板/节点的执行器")
	cardDispatchCmd.Flags().StringVar(&cardDispatchModel, "model", "", "一次性覆盖模型；空 = 交给执行器自身默认")
	cardCmd.AddCommand(cardDispatchCmd)
}
