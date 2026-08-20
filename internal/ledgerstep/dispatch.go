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
	// ExecutorOverride / ModelOverride 是节点对模板的单字段覆盖；空 = 用模板的。
	ExecutorOverride string
	ModelOverride    string
	// CarryCardContext 为真时把卡上下文段拼进 prompt（来自节点的同名开关）。
	CarryCardContext bool
	// Extra 是本次派发的临时补充说明，可为空。
	Extra string
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
	// 三段拼装要用到有效基线，所以必须排在 base 算完之后。审阅轮的 base 被
	// 换成了工作分支，但卡上下文里要写的是**卡的**基线（合并目标），两者不同，
	// 因此这里重新取一次而不是复用上面的 base。
	cardBase, err := d.St.EffectiveBaseBranch(c.ID)
	if err != nil {
		return zero, fmt.Errorf("取卡上下文基线: %w", err)
	}
	prompt := buildPrompt(body, c, cardBase, req.CarryCardContext, req.Extra)
	model := ""
	if tpl.Def.ModelByTarget != nil {
		model = tpl.Def.ModelByTarget[target]
	}
	executor := tpl.Def.Executor
	if req.ExecutorOverride != "" {
		executor = req.ExecutorOverride
	}
	if req.ModelOverride != "" {
		model = req.ModelOverride
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
	slog.Default().Info("按模板派发",
		"card", c.ID, "template", req.Template, "target", target,
		"executor", executor, "discipline", disciplineName,
		"branch", branch, "base", base,
		"carry_card_context", req.CarryCardContext, "has_extra", strings.TrimSpace(req.Extra) != "",
		"prompt_bytes", len(prompt))
	taskID, err := d.Transport(ctx, DispatchOpts{
		Prompt: prompt, Branch: branch, Target: target, Project: c.Project,
		Executor: executor, Model: model, PlanB64: planB64,
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

// buildPrompt 把 executor 收到的 prompt 按三段拼起来。
//
// 参数：
//   - body:  模板正文（占位符已替换完）
//   - c:     卡
//   - base:  卡的有效基线分支，可为空
//   - carry: 是否拼入卡上下文段（节点的 CarryCardContext 开关）
//   - extra: 本次派发的临时补充说明，可为空
//
// 返回：拼好的 prompt。三段之间用空行分隔，缺席的段不留空标题。
//
// why 分三段而不是全塞进模板：模板是**复用**的（同一份审阅模板给所有卡用），
// 卡上下文是**每张卡不同的事实**，补充说明是**这一次才有的话**。混在一起就
// 只能靠占位符硬塞，而占位符加一个就要改所有模板。
//
// 注意：**这里绝不拼纪律块正文**。纪律块只传名字，正文由 agentd 按 B129 注入；
// 两份纪律同场会让审阅的「只读」被实现块的「完成即 commit」推翻（2026-08-19
// 真机出过一次）。
func buildPrompt(body string, c ledger.Card, base string, carry bool, extra string) string {
	sections := []string{body}
	if carry {
		var b strings.Builder
		b.WriteString("## 本卡上下文\n\n")
		fmt.Fprintf(&b, "- 卡号：%s\n", c.ID)
		fmt.Fprintf(&b, "- 标题：%s\n", c.Title)
		if base != "" {
			// 明写「合并目标以此为准」是这一段的核心用途：合并环节要合到哪条
			// 分支每张卡都不同，节点配置里没有也不该有这个值，它只能从卡带进来。
			fmt.Fprintf(&b, "- 有效基线分支：%s（本卡的合并目标以此为准，不要越过它碰别的分支）\n", base)
		} else {
			b.WriteString("- 有效基线分支：（未设置，需要合并时先向协调者确认，不要自行假定 main）\n")
		}
		if c.AcceptanceCriteria != "" {
			fmt.Fprintf(&b, "- 验收判据：\n%s\n", indentLines(c.AcceptanceCriteria, "  "))
		}
		if len(c.Attachments) > 0 {
			b.WriteString("- 附件（仓内相对路径）：\n")
			for _, att := range c.Attachments {
				fmt.Fprintf(&b, "  - %s: %s\n", att.Kind, att.Path)
			}
		}
		sections = append(sections, strings.TrimRight(b.String(), "\n"))
	}
	if strings.TrimSpace(extra) != "" {
		sections = append(sections, "## 本次补充\n\n"+strings.TrimSpace(extra))
	}
	return strings.Join(sections, "\n\n")
}

// indentLines 给多行文本每行加前缀，让验收判据在 markdown 列表下缩进对齐。
func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
