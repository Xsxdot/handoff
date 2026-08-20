// card dispatch：按模板拼装 prompt，带上纪律块**角色名**（正文由 agentd 注入），
// 走既有 dispatch 通道；
// 派发即认领（CAS 进「进行中」就是 claim，第二个会话干净失败）；
// task 回链 + 模板版本/纪律角色名快照落事件。
package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var (
	cardDispatchTemplate   string
	cardDispatchTarget     string
	cardDispatchPlan       string
	cardDispatchDiscipline string
	cardDispatchStep       string
	cardDispatchRepo       string
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
	addr, token, err := targetEndpoint(target)
	if err != nil {
		return "", err
	}
	task, err := client.New(addr, token).Dispatch(context.Background(), client.DispatchOpts{
		Prompt: prompt, NewBranch: branch, Target: target, ProjectName: project,
	})
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

var dispatchTransportWithOpts = func(req dispatchRequest) (string, error) {
	addr, token, err := targetEndpoint(req.target)
	if err != nil {
		return "", err
	}
	task, err := client.New(addr, token).Dispatch(context.Background(), client.DispatchOpts{
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

// targetEndpoint 按登记名解析目标机地址与 token。
func targetEndpoint(target string) (addr, token string, err error) {
	cfg := loadCLIConfig()
	tgt, ok := cfg.Targets[target]
	if !ok {
		return "", "", fmt.Errorf("目标机 %s 未登记（handoff init/机器登记先行）", target)
	}
	return "http://" + tgt.Addr, tgt.Token, nil
}

// dispatchResult 模板派发共用段的产出（回显 + 环节入口复用）。
type dispatchResult struct {
	Card            string `json:"card"`
	Task            string `json:"task"`
	Target          string `json:"target"`
	Branch          string `json:"branch"`
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
}

// dispatchViaTemplate 模板派发的共用段：取模板 → 决定纪律块名字 → 拼 prompt
// → 走既有 dispatch 通道 → LinkTask 挂账 → dispatched 快照。
// 不含认领语义：实现类派发在调用前自行 CAS 认领；环节派发也复用此段，
// 因而不会把待审阅卡拉回进行中。
func dispatchViaTemplate(st *ledger.Store, c ledger.Card,
	tplName, targetFlag, planPath, disciplineOverride, actor string) (dispatchResult, error) {
	var zero dispatchResult
	tpl, err := st.GetTemplate(tplName, 0)
	if err != nil {
		return zero, fmt.Errorf("取模板: %w", err)
	}
	target := targetFlag
	if target == "" {
		target = tpl.Def.Target
	}
	if target == "" {
		return zero, fmt.Errorf("目标机未定：--target 或模板 target 至少一个")
	}

	// 纪律块只传名字，正文由 agentd 解析注入。CLI 曾在这里读文件并拼进 prompt，
	// 而 agentd 又会按 executor 注入一份——两份同时在场，审阅那次的「只读，不写」
	// 被实现块的「每个 task 完成即 commit」直接推翻（2026-08-19 真机实测过一次）。
	disciplineName := tpl.Def.Discipline
	if disciplineOverride != "" {
		disciplineName = disciplineOverride
	}

	body := strings.NewReplacer(
		"{{TITLE}}", c.Title,
		"{{CARD}}", c.ID,
		"{{ACCEPT}}", c.AcceptanceCriteria,
	).Replace(tpl.Def.Prompt)
	prompt := body

	reviewBase := ""
	// 审阅轮跑在卡的工作分支上：审阅是只读的，开自己的分支既没意义，又会
	// 让同一张卡的第二轮撞上第一轮的同名分支（判据② 的 3 轮封顶因此走不到
	// 第二轮——2026-08-19 真机实测 fatal: a branch named ... already exists）
	branch := fmt.Sprintf("%s/%s-%s", tpl.Def.BranchPrefix, c.ID, tpl.Def.Purpose)
	existingBranch := ""
	if tpl.Def.Purpose == ledger.PurposeReview {
		work, err := st.WorkBranch(c.ID)
		if err != nil {
			return zero, fmt.Errorf("审阅轮取工作分支: %w", err)
		}
		// 审阅每轮开一条指向工作分支当前提交的一次性分支。三个约束叠出这个形态：
		// ① 不能复用固定名 cards/<卡>-review——第二轮撞名，判据② 的 3 轮封顶
		//    走不到第二轮；② 不能直接检出工作分支——实现任务的工作树还占着它，
		//    git 不许两个工作树检出同一分支；③ 审阅要看的是工作分支的代码，
		//    所以起点必须是它的当前提交（base=work）
		round, err := st.ReviewRounds(c.ID)
		if err != nil {
			return zero, fmt.Errorf("审阅轮取轮次: %w", err)
		}
		branch = fmt.Sprintf("%s/%s-review-%d", tpl.Def.BranchPrefix, c.ID, round+1)
		reviewBase = work
	}
	base, err := st.EffectiveBaseBranch(c.ID)
	if err != nil {
		return zero, fmt.Errorf("取有效基线: %w", err)
	}
	if reviewBase != "" {
		base = reviewBase // 审阅分支从工作分支的当前提交开，不是从基线开
	}
	model := ""
	if tpl.Def.ModelByTarget != nil {
		model = tpl.Def.ModelByTarget[target]
	}
	var planB64, planName string
	if planPath != "" {
		content, err := os.ReadFile(planPath)
		if err != nil {
			return zero, fmt.Errorf("读 plan %s: %w", planPath, err)
		}
		planB64 = base64.StdEncoding.EncodeToString(content)
		planName = filepath.Base(planPath)
	}
	taskID, err := dispatchTransportWithOpts(dispatchRequest{
		prompt: prompt, branch: branch, target: target, project: c.Project,
		executor: tpl.Def.Executor, model: model, planB64: planB64,
		planName: planName, base: base, newWorktree: true,
		existingBranch: existingBranch, discipline: disciplineName,
	})
	if err != nil {
		return zero, fmt.Errorf("派发: %w", err)
	}
	slog.Default().Info("模板派发已裁定纪律块角色", "card", c.ID, "template", tplName,
		"discipline", disciplineName, "overridden", disciplineOverride != "")
	snapshotBranch := branch
	if snapshotBranch == "" {
		snapshotBranch = existingBranch
	}
	if err := st.LinkTask(c.ID, target, taskID, tpl.Def.Purpose, actor); err != nil {
		return zero, fmt.Errorf("回链挂账: %w", err)
	}
	if err := st.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
		Target: target, TaskID: taskID, Branch: snapshotBranch,
		Purpose: tpl.Def.Purpose, PlanPath: planPath, Actor: actor,
	}); err != nil {
		return zero, fmt.Errorf("快照落账: %w", err)
	}
	return dispatchResult{
		Card: c.ID, Task: taskID, Target: target, Branch: snapshotBranch,
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
	}, nil
}

var cardDispatchCmd = &cobra.Command{
	Use:   "dispatch <id>",
	Short: "按模板派发（派发即认领；--step review|merge 走自动环节）",
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
		result, err := dispatchViaTemplate(st, card, cardDispatchTemplate, cardDispatchTarget,
			cardDispatchPlan, cardDispatchDiscipline, actor)
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
	cardDispatchCmd.Flags().StringVar(&cardDispatchStep, "step", "", "自动环节：review|merge")
	cardDispatchCmd.Flags().StringVar(&cardDispatchRepo, "repo", ".", "本地仓库目录（--step merge 的客观判据与合并在此跑）")
	cardCmd.AddCommand(cardDispatchCmd)
}
