// 模板派发的共用段：取模板 → 算分支与基线 → 拼 prompt → 经注入的 Transport
// 派发 → 回链挂账 → 落 dispatched 快照。
//
// 职责：把「一张卡按某个模板派出去」这件事收口成一处，CLI 与 agentd 共用。
// 边界：
//   - 不认领、不动卡状态——实现类派发在调用前自行 CAS 认领，环节派发不认领
//   - 不做网络——传输经 Transport 注入，本文件不知道对端是 HTTP 还是别的什么
//   - 不解析纪律块——只把角色名传下去，正文由 agentd 解析注入
package ledgerstep

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// DispatchOpts 是 Transport 的完整派发请求。PlanPath 已在 ViaTemplate 中
// 读成 PlanB64/PlanName；其余字段与 agentd 的派发协议一一对应。
type DispatchOpts struct {
	Prompt, Branch, Target, Project, Executor, Model, PlanB64, PlanName, Base, ExistingBranch, Discipline string
	NewWorktree                                                                                           bool
}

// Transport 是注入的派发传输。返回 agentd 生成的 task id；实现不关心传输协议。
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, err error)

// DispatchResult 是模板派发完成后的回显与审计信息。
type DispatchResult struct {
	Card            string `json:"card"`
	Task            string `json:"task"`
	Target          string `json:"target"`
	Branch          string `json:"branch"`
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
}

// Dispatcher 持有模板派发需要的账本、传输和审计 actor。
type Dispatcher struct {
	St        *ledger.Store
	Transport Transport
	Actor     string
}

// TemplateDispatch 描述一次按模板派发：模板、目标机、可选 plan 与纪律角色覆盖。
type TemplateDispatch struct {
	Template           string
	Target             string
	PlanPath           string
	DisciplineOverride string
}

// ViaTemplate 按模板把一张卡派出去。
//
// 参数：c 卡；req 模板名、目标机、可选 plan 路径与纪律块角色名覆盖。
// 返回：派发结果（含 task id、分支、模板版本、纪律块角色名）。
//
// 注意：不含认领语义。实现类派发在调用前自行 CAS 认领；环节派发
// （审阅/合并）刻意不认领——它们是待审阅卡上的动作，认领会把卡拉回进行中。
//
// req.PlanPath 按调用方进程的 CWD 解析。agentd 一侧永远传空串：
// 浏览器里没有 plan 文件，实现类派发也不从界面走。
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error) {
	var zero DispatchResult
	tpl, err := d.St.GetTemplate(req.Template, 0)
	if err != nil {
		return zero, fmt.Errorf("取模板: %w", err)
	}
	target := req.Target
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
	if req.DisciplineOverride != "" {
		disciplineName = req.DisciplineOverride
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
		work, err := d.St.WorkBranch(c.ID)
		if err != nil {
			return zero, fmt.Errorf("审阅轮取工作分支: %w", err)
		}
		// 审阅每轮开一条指向工作分支当前提交的一次性分支。三个约束叠出这个形态：
		// ① 不能复用固定名 cards/<卡>-review——第二轮撞名，判据② 的 3 轮封顶
		//    走不到第二轮；② 不能直接检出工作分支——实现任务的工作树还占着它，
		//    git 不许两个工作树检出同一分支；③ 审阅要看的是工作分支的代码，
		//    所以起点必须是它的当前提交（base=work）
		round, err := d.St.ReviewRounds(c.ID)
		if err != nil {
			return zero, fmt.Errorf("审阅轮取轮次: %w", err)
		}
		branch = fmt.Sprintf("%s/%s-review-%d", tpl.Def.BranchPrefix, c.ID, round+1)
		reviewBase = work
	}
	base, err := d.St.EffectiveBaseBranch(c.ID)
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
	if req.PlanPath != "" {
		content, err := os.ReadFile(req.PlanPath)
		if err != nil {
			return zero, fmt.Errorf("读 plan %s: %w", req.PlanPath, err)
		}
		planB64 = base64.StdEncoding.EncodeToString(content)
		planName = filepath.Base(req.PlanPath)
	}
	taskID, err := d.Transport(ctx, DispatchOpts{
		Prompt: prompt, Branch: branch, Target: target, Project: c.Project,
		Executor: tpl.Def.Executor, Model: model, PlanB64: planB64,
		PlanName: planName, Base: base, NewWorktree: true,
		ExistingBranch: existingBranch, Discipline: disciplineName,
	})
	if err != nil {
		return zero, fmt.Errorf("派发: %w", err)
	}
	slog.Default().Info("模板派发已裁定纪律块角色", "card", c.ID, "template", req.Template,
		"discipline", disciplineName, "overridden", req.DisciplineOverride != "")
	snapshotBranch := branch
	if snapshotBranch == "" {
		snapshotBranch = existingBranch
	}
	if err := d.St.LinkTask(c.ID, target, taskID, tpl.Def.Purpose, d.Actor); err != nil {
		return zero, fmt.Errorf("回链挂账: %w", err)
	}
	if err := d.St.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
		Target: target, TaskID: taskID, Branch: snapshotBranch,
		Purpose: tpl.Def.Purpose, PlanPath: req.PlanPath, Actor: d.Actor,
	}); err != nil {
		return zero, fmt.Errorf("快照落账: %w", err)
	}
	slog.Default().Info("模板派发完成", "card", c.ID, "template", tpl.Name,
		"task", taskID, "target", target, "branch", snapshotBranch, "discipline", disciplineName)
	return DispatchResult{
		Card: c.ID, Task: taskID, Target: target, Branch: snapshotBranch,
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
	}, nil
}
