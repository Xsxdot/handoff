// card dispatch：按模板拼装 prompt + 纪律块，走既有 dispatch 通道；
// 派发即认领（CAS 进「进行中」就是 claim，第二个会话干净失败）；
// task 回链 + 模板版本/纪律 hash 快照落事件。
package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	cardDispatchNode       string
	cardDispatchRepo       string
)

type dispatchRequest struct {
	prompt   string
	branch   string
	target   string
	project  string
	executor string
	model    string
	planB64  string
	planName string
	base     string
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
		Prompt: req.prompt, Target: req.target, NewBranch: req.branch,
		ProjectName: req.project, Executor: req.executor, Model: req.model,
		PlanB64: req.planB64, PlanName: req.planName, Base: req.base,
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

// targetEndpoint 按登记名解析目标机地址与 token。
func targetEndpoint(target string) (addr, token string, err error) {
	cfg := loadCLIConfig()
	tgt, ok := cfg.Targets[target]
	if !ok {
		return "", "", fmt.Errorf("目标机 %s 未登记（handoff init/机器登记先行）", target)
	}
	return "http://" + tgt.Addr, tgt.Token, nil
}

// dispatchResult 模板派发共用段的产出（回显 + 节点入口复用）。
type dispatchResult struct {
	Card            string `json:"card"`
	Task            string `json:"task"`
	Target          string `json:"target"`
	Branch          string `json:"branch"`
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineHash  string `json:"discipline_hash"`
}

// dispatchViaTemplate 模板派发的共用段：取模板 → 纪律块 hash → 拼 prompt
// → 走既有 dispatch 通道 → LinkTask 挂账 → dispatched 快照。
// 不含认领语义：实现类派发在调用前自行 CAS 认领；节点派发也复用此段，
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

	disciplinePath := tpl.Def.DisciplinePath
	if disciplineOverride != "" {
		disciplinePath = disciplineOverride
	}
	discipline, err := os.ReadFile(disciplinePath)
	if err != nil {
		return zero, fmt.Errorf("读纪律块 %s（先把纪律块文件落库，见模板定义）: %w", disciplinePath, err)
	}
	sum := sha256.Sum256(discipline)
	disciplineHash := hex.EncodeToString(sum[:])[:12]

	body := strings.NewReplacer(
		"{{TITLE}}", c.Title,
		"{{CARD}}", c.ID,
		"{{ACCEPT}}", c.AcceptanceCriteria,
	).Replace(tpl.Def.Prompt)
	prompt := string(discipline) + "\n\n---\n\n" + body

	branch := fmt.Sprintf("%s/%s-%s", tpl.Def.BranchPrefix, c.ID, tpl.Def.Purpose)
	base, err := st.EffectiveBaseBranch(c.ID)
	if err != nil {
		return zero, fmt.Errorf("取有效基线: %w", err)
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
		planName: planName, base: base,
	})
	if err != nil {
		return zero, fmt.Errorf("派发: %w", err)
	}
	if err := st.LinkTask(c.ID, target, taskID, tpl.Def.Purpose, actor); err != nil {
		return zero, fmt.Errorf("回链挂账: %w", err)
	}
	if err := st.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineHash: disciplineHash,
		Target: target, TaskID: taskID, Branch: branch, PlanPath: planPath, Actor: actor,
	}); err != nil {
		return zero, fmt.Errorf("快照落账: %w", err)
	}
	return dispatchResult{
		Card: c.ID, Task: taskID, Target: target, Branch: branch,
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineHash: disciplineHash,
	}, nil
}

var cardDispatchCmd = &cobra.Command{
	Use:   "dispatch <id>",
	Short: "按模板派发（派发即认领；--node review|merge 走节点执行器）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, actor := args[0], ledgerActor()
		if cardDispatchNode != "" {
			return runNodeDispatch(cmd, st, id, cardDispatchNode, actor)
		}
		card, err := st.GetCard(id)
		if err != nil {
			return err
		}
		if card.Status == ledger.StatusDoing {
			return fmt.Errorf("卡 %s 已被认领（驱动 %s）", id, card.DriverSession)
		}
		if err := st.MoveCard(id, ledger.StatusDoing, card.Status, actor); err != nil {
			if current, getErr := st.GetCard(id); getErr == nil && current.DriverSession != "" {
				return fmt.Errorf("卡 %s 已被 %s 认领: %w", id, current.DriverSession, err)
			}
			return fmt.Errorf("认领失败（可能被并发抢先）: %w", err)
		}
		if err := st.ClaimDriver(id, actor); err != nil {
			_ = st.MoveCard(id, card.Status, ledger.StatusDoing, actor)
			return fmt.Errorf("认领驱动: %w", err)
		}
		result, err := dispatchViaTemplate(st, card, cardDispatchTemplate, cardDispatchTarget,
			cardDispatchPlan, cardDispatchDiscipline, actor)
		if err != nil {
			_ = st.MoveCard(id, card.Status, ledger.StatusDoing, actor)
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	},
}

func init() {
	cardDispatchCmd.Flags().StringVar(&cardDispatchTemplate, "template", "feature-impl", "派发模板名")
	cardDispatchCmd.Flags().StringVar(&cardDispatchTarget, "target", "", "目标机（覆盖模板）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchPlan, "plan", "", "plan 文件路径（挂派发事件）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchDiscipline, "discipline-override", "", "覆盖纪律块路径（测试/应急）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchNode, "node", "", "节点执行器：review|merge")
	cardDispatchCmd.Flags().StringVar(&cardDispatchRepo, "repo", ".", "本地仓库目录（--node merge 的客观判据与合并在此跑）")
	cardCmd.AddCommand(cardDispatchCmd)
}
