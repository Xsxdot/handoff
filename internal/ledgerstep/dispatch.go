// 模板派发的共用段：取模板 → 算分支与基线 → 拼 prompt → 经注入的 Transport
// 派发 → 回链挂账 → 落 dispatched 快照。
//
// 职责：把「一张卡按某个模板派出去」这件事收口成一处，CLI 与 agentd 共用。
// 边界：
//   - 不占用协调者席位、不动卡状态——运行互斥由节点编排的 RunLock 负责
//   - 不做网络——传输经 Transport 注入，本文件不知道对端是 HTTP 还是别的什么
//   - 不解析纪律块——B229 起正文由调用方在装配处经缝 1（discipline.ResolveDispatch）
//     解析好，以 Dispatcher 数据字段携带进来；本包只透传与记账
package ledgerstep

import (
	"context"
	"encoding/base64"
	"errors"
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
	// HomeDir 是小队派发载体 HOME 的可空透传值；nil=字段缺席，指向空串=显式空值。
	HomeDir *string
	// B229：DisciplineText 是协调者侧组装好的纪律正文（缝 1 discipline.ResolveDispatch 产物），
	// 随请求下发；执行机收文即用，不再自行解析。DisciplineVersion 是命中的账本
	// 版本号（未点名或临时正文为 0），供快照与回放。Ticket 0 仅声明，接线归实现票。
	DisciplineText    string
	DisciplineVersion int
	// OutputPath 是节点声明路径的确定值，供 transport 审计与测试观察；路径同时已注入 Prompt。
	OutputPath  string
	NewWorktree bool
	// ResolveDefaultBase 标记卡链没有显式基线，需由知道目标仓库路径的 agentd
	// 解析项目默认分支；false 时保持普通派发的 Base/HEAD 语义。
	ResolveDefaultBase bool
	// LocalBaseBranch 标记 Base 是目标机本地的工作分支；目标侧只解析本地 ref，
	// 不得走分支名的远端补拉路径。与 ResolveDefaultBase 互斥。
	LocalBaseBranch bool
}

// Transport 是注入的派发传输。返回 agentd 生成的 task id 与 Task.BaseCommit；
// BaseCommit 必须原样来自目标 agentd，ledgerstep 不在协调者仓库猜测起点。
type Transport func(ctx context.Context, opts DispatchOpts) (taskID string, baseCommit string, err error)

// DispatchResult 是模板派发完成后的回显与审计信息。
type DispatchResult struct {
	Card   string `json:"card"`
	Task   string `json:"task"`
	Target string `json:"target"`
	Branch string `json:"branch"`
	// Base 是本次传给 agentd 的起点分支名；它不是卡的 effective base_branch。
	Base string `json:"base"`
	// BaseCommit 是目标 agentd Task.BaseCommit 的原样回传值。
	BaseCommit      string `json:"base_commit"`
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineName  string `json:"discipline_name"`
	// DisciplineVersion 是本次派发命中的纪律块账本版本（B229）；未点名/临时正文为 0。
	DisciplineVersion int `json:"discipline_version"`
}

// Dispatcher 持有模板派发需要的账本、传输和审计 actor。
type Dispatcher struct {
	St        *ledger.Store
	Transport Transport
	Actor     string
	// HomeDir 是小队绑定载体 HOME 的可空原指针；nil 表示普通派发，指向空串
	// 表示显式不覆盖目标进程 HOME。ledgerstep 不展开、清理或改写该字符串。
	HomeDir *string
	// NormalizeTarget 把调用方已确认的自机登记名归一成空串；nil 等价于恒等
	// 函数。未知目标名不得在此处改写，目标身份判断归组装点负责。
	NormalizeTarget func(target string) string

	// B229 缝 1 产物（数据字段，不是解析函数）：调用方装配时经
	// discipline.ResolveDispatch 解析好的纪律正文与账本版本号。未点名模板的
	// 调用方传纯平台层正文、版本 0。ViaTemplate 原样透传进 DispatchOpts 与
	// dispatched 快照——ledgerstep 不 import discipline：组装点唯一在 d_policy
	// （B229 契约 §4.5），本包零新增 import，解析动作归调用方装配处。
	DisciplineText    string
	DisciplineVersion int
}

// TemplateDispatch 描述一次按模板派发：模板、目标机、可选 plan 与纪律角色覆盖。
type TemplateDispatch struct {
	Template           string
	Target             string
	PlanPath           string
	DisciplineOverride string
	// WriteGate 在 Transport 成功后、每一处账本写入前调用；nil 表示不设闸。
	// 运行节点用它阻止失去运行锁后的挂账与 dispatched 快照写入。
	WriteGate func() bool
	// ExecutorOverride / ModelOverride 是调用方对模板的单字段覆盖；空 = 用模板的。
	// 当 ExecutorOverride 真的改变有效 executor 且 ModelOverride 为空时，
	// ViaTemplate 会清掉下层模型；只显式重述同一个 executor 不改变模型。
	ExecutorOverride string
	ModelOverride    string
	// CarryCardContext 为真时把卡上下文段拼进 prompt（来自节点的同名开关）。
	CarryCardContext bool
	// PurposeOverride 覆盖模板的派发用途；空 = 用模板的。
	// 用途决定分支命名、审阅基线、工作分支归属与轮次挂号四件事，见
	// ledger.NodeOverride.Purpose 的注释。
	PurposeOverride string
	// OmitAcceptance 为真时不把整卡验收判据注入 prompt（来自节点的同名开关）。
	OmitAcceptance bool
	// Extra 是本次派发的临时补充说明，可为空。
	Extra string
	// OutputPath 是本节点声明路径在协调者侧按本轮派发日期渲染后的确定值。
	// 它独立于 CarryCardContext：即使不携带卡上下文，执行者仍必须收到该法定路径。
	OutputPath string
}

// ViaTemplate 按模板把一张卡派出去。
//
// 参数：c 卡；req 模板名、目标机、可选 plan 路径与纪律块角色名覆盖。
// 返回：派发结果（含 task id、起点分支、agentd Task.BaseCommit、模板版本、纪律块角色名）。
//
// 注意：不含协调者席位语义。实现类与环节派发都只负责派任务；运行互斥由
// 节点编排的 RunLock 负责，席位由 bind/coordinate/rebind 命令管理。
//
// req.PlanPath 按调用方进程的 CWD 解析。agentd 一侧永远传空串：
// 浏览器里没有 plan 文件，实现类派发也不从界面走。
func (d *Dispatcher) ViaTemplate(ctx context.Context, c ledger.Card, req TemplateDispatch) (DispatchResult, error) {
	var zero DispatchResult
	tpl, err := d.St.GetTemplate(req.Template, 0)
	if err != nil {
		slog.Default().Warn("取模板失败", "card", c.ID, "template", req.Template, "cause", err)
		return zero, fmt.Errorf("取模板: %w", err)
	}
	// 有效目标机与纪律角色名：请求覆盖 > 模板缺省。两个调用方装配处的缝 1
	// 解析必须与这里完全同序（共用 PreflightDiscipline → disciplineAndTarget），
	// 各抄一份迟早漂移——探错目标机的拒发闸比没有闸更危险。
	target := req.Target
	if target == "" {
		target = tpl.Def.Target
	}
	rawTarget := target
	if d.NormalizeTarget != nil {
		target = d.NormalizeTarget(target)
	}
	disciplineName := tpl.Def.Discipline
	if req.DisciplineOverride != "" {
		disciplineName = req.DisciplineOverride
	}
	slog.Default().Info("模板派发目标已归一", "card", c.ID, "template", req.Template,
		"raw_target", rawTarget, "target", target)

	// 有效用途：节点覆盖优先于模板。下面**所有**按用途裁决的地方都读它，
	// 不再直接读取模板用途字段——漏掉任何一处都会让节点只对了一半（例如分支
	// 名对了但快照里记的还是模板用途，WorkBranch 于是把审阅分支当成工作分支）。
	templateDef := tpl.Def
	purpose := templateDef.Purpose
	if req.PurposeOverride != "" {
		purpose = req.PurposeOverride
	}

	// WorkBranch 同时返回分支和上次目标机；这两个值必须来自同一条非审阅
	// dispatched 快照，才能可靠判断本次是否仍在持有该私有工作分支的机器上。
	// 跨机时在 Transport 之前拒绝，避免静默退回卡基线并丢掉上一轮产出。
	workInfo, workErr := d.St.WorkBranch(c.ID)
	hasWorkBranch := workErr == nil
	if workErr != nil && !errors.Is(workErr, ledger.ErrNotFound) {
		slog.Default().Error("读取卡工作分支失败", "card", c.ID, "target", target,
			"previous_target", "", "branch", "", "cause", workErr)
		return zero, fmt.Errorf("取卡工作分支: %w", workErr)
	}
	previousTarget := ""
	if hasWorkBranch {
		previousTarget = workInfo.Target
		if d.NormalizeTarget != nil {
			previousTarget = d.NormalizeTarget(previousTarget)
		}
	}
	originReady := false
	if hasWorkBranch && previousTarget != target {
		published, pubErr := d.St.WorkBranchPublished(c.ID, workInfo.Branch)
		if pubErr != nil {
			slog.Default().Error("读取工作分支 origin 发布失败", "card", c.ID,
				"branch", workInfo.Branch, "cause", pubErr)
			return zero, fmt.Errorf("取工作分支 origin 发布: %w", pubErr)
		}
		if !published {
			slog.Default().Warn("工作分支跨目标机且未发布 origin，拒绝接续", "card", c.ID,
				"branch", workInfo.Branch, "previous_target", previousTarget, "target", target,
				"cause", "目标机身份不一致且 origin 未见该分支")
			return zero, fmt.Errorf("工作分支只存在于创建它的那台机器：上次目标机 %q，本次目标机 %q；请先在上一台 git push origin %s（失败则 needs_human）。日常路径不使用 --base",
				previousTarget, target, workInfo.Branch)
		}
		originReady = true
		slog.Default().Info("工作分支已在 origin，跨机接续", "card", c.ID,
			"branch", workInfo.Branch, "previous_target", previousTarget, "target", target)
	}

	// 判据被收起时不留空冒号：模板正文里「验收判据：{{ACCEPT}}」后面跟一片
	// 空白，比说明白更让执行者困惑。替换值已经是完整句——模板 {{ACCEPT}} 后面
	// 不要再跟「这是整卡的最终验收判据」：那句是 B182 的失败缓解，omit 时会
	// 跟本句并置成病句（B197），且没挡住 plan 节点写实现。
	acceptance := c.AcceptanceCriteria
	if req.OmitAcceptance {
		acceptance = "（本节点不注入整卡验收判据——那是实现级的最终判据；本节点的产出物与 pass 依据以纪律块为准）"
	}
	body := strings.NewReplacer(
		"{{TITLE}}", c.Title,
		"{{CARD}}", c.ID,
		"{{ACCEPT}}", acceptance,
	).Replace(tpl.Def.Prompt)

	// 审阅轮跑在卡的工作分支上：审阅是只读的，开自己的分支既没意义，又会
	// 让同一张卡的第二轮撞上第一轮的同名分支（判据② 的 3 轮封顶因此走不到
	// 第二轮——2026-08-19 真机实测 fatal: a branch named ... already exists）
	branch := fmt.Sprintf("%s/%s-%s", tpl.Def.BranchPrefix, c.ID, purpose)
	existingBranch := ""
	if purpose == ledger.PurposeReview {
		if !hasWorkBranch {
			return zero, fmt.Errorf("审阅轮取工作分支: %w", workErr)
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
	} else {
		// 非审阅节点的重跑同样会撞分支名——同一张卡第二次从同 purpose 模板
		// 派发时，目标机上第一轮分支还在，git 拒绝创建同名分支（与审阅轮
		// 2026-08-19 真机实测同一形态）。解法与审阅一致：按「同 purpose 已派
		// 几次」挂号。首轮保持无后缀，存量卡的分支命名不变。
		rounds, err := d.St.PurposeRounds(c.ID, purpose)
		if err != nil {
			return zero, fmt.Errorf("取 %s 轮次: %w", purpose, err)
		}
		if rounds > 0 {
			branch = fmt.Sprintf("%s-%d", branch, rounds+1)
			slog.Default().Info("重跑轮分支挂号", "card", c.ID,
				"purpose", purpose, "round", rounds+1, "branch", branch)
		}
	}
	base, err := d.St.EffectiveBaseBranch(c.ID)
	if err != nil {
		slog.Default().Warn("取有效基线失败", "card", c.ID, "target", target,
			"previous_target", previousTarget, "branch", "", "cause", err)
		return zero, fmt.Errorf("取有效基线: %w", err)
	}
	localBaseBranch := false
	if hasWorkBranch {
		base = workInfo.Branch // 后续节点从工作分支起，不是从卡合并基线起
		if !originReady {
			localBaseBranch = true
		}
	}
	resolveDefaultBase := base == ""
	// 三段拼装要用到有效基线，所以必须排在 base 算完之后。审阅轮的 base 被
	// 换成了工作分支，但卡上下文里要写的是**卡的**基线（合并目标），两者不同，
	// 因此这里重新取一次而不是复用上面的 base。
	cardBase, err := d.St.EffectiveBaseBranch(c.ID)
	if err != nil {
		slog.Default().Warn("取卡上下文基线失败", "card", c.ID, "target", target,
			"previous_target", previousTarget, "branch", branch, "cause", err)
		return zero, fmt.Errorf("取卡上下文基线: %w", err)
	}
	prompt := buildPrompt(body, c, cardBase, req.CarryCardContext, req.OmitAcceptance, req.Extra, req.OutputPath)
	model := ""
	if tpl.Def.ModelByTarget != nil {
		model = tpl.Def.ModelByTarget[target]
	}
	executor := tpl.Def.Executor
	if req.ExecutorOverride != "" && req.ExecutorOverride != tpl.Def.Executor {
		executor = req.ExecutorOverride
		// 模型是执行器的同层伴随覆盖。换执行器时不能把模板声明的模型
		// 带给新执行器；空值交给新执行器自身的默认模型。这里按“有效
		// executor 是否变化”判定，与 agentd Manager.resolveModel 的边界
		// 注释同源：显式写出默认 executor 仍照常套用配置模型。
		model = ""
	} else if req.ExecutorOverride != "" {
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
		"executor", executor, "model", model, "discipline", disciplineName,
		"discipline_version", d.DisciplineVersion,
		"discipline_bytes", len(d.DisciplineText),
		"purpose", purpose, "purpose_overridden", req.PurposeOverride != "",
		"omit_acceptance", req.OmitAcceptance,
		"branch", branch, "base", base,
		"resolve_default_base", resolveDefaultBase,
		"carry_card_context", req.CarryCardContext, "has_extra", strings.TrimSpace(req.Extra) != "",
		"output_path", req.OutputPath,
		"prompt_bytes", len(prompt))
	slog.Default().Info("派发前已确定节点产出路径", "card", c.ID, "template", req.Template,
		"target", target, "output_path", req.OutputPath)
	taskID, baseCommit, err := d.Transport(ctx, DispatchOpts{
		Prompt: prompt, Branch: branch, Target: target, Project: c.Project,
		Executor: executor, Model: model, PlanB64: planB64,
		HomeDir:    d.HomeDir,
		OutputPath: req.OutputPath,
		PlanName:   planName, Base: base, NewWorktree: true,
		ExistingBranch: existingBranch, Discipline: disciplineName,
		// B229：调用方经缝 1 解析好的正文与版本，原样下发（§3.1 未点名也带平台层）。
		DisciplineText:     d.DisciplineText,
		DisciplineVersion:  d.DisciplineVersion,
		ResolveDefaultBase: resolveDefaultBase,
		LocalBaseBranch:    localBaseBranch,
	})
	if err != nil {
		slog.Default().Warn("模板派发传输失败", "card", c.ID, "target", target,
			"branch", branch, "base", base, "cause", err)
		return zero, fmt.Errorf("派发: %w", err)
	}
	slog.Default().Info("模板派发传输已返回", "card", c.ID, "target", target,
		"task", taskID, "base", base, "base_commit", baseCommit)
	slog.Default().Info("模板派发已裁定纪律块角色", "card", c.ID, "template", req.Template,
		"discipline", disciplineName, "overridden", req.DisciplineOverride != "")
	snapshotBranch := branch
	if snapshotBranch == "" {
		snapshotBranch = existingBranch
	}
	if req.WriteGate != nil && !req.WriteGate() {
		err := fmt.Errorf("挂账被拒：%w", ErrWriteGateClosed)
		slog.Default().Warn("失去写权，停止派发挂账", "card", c.ID, "target", target, "task", taskID, "cause", err)
		return zero, err
	}
	if err := d.St.LinkTask(c.ID, target, taskID, purpose, d.Actor); err != nil {
		slog.Default().Warn("模板派发回链挂账失败", "card", c.ID, "target", target,
			"task", taskID, "cause", err)
		return zero, fmt.Errorf("回链挂账: %w", err)
	}
	if req.WriteGate != nil && !req.WriteGate() {
		err := fmt.Errorf("快照落账被拒：%w", ErrWriteGateClosed)
		slog.Default().Warn("失去写权，停止派发快照落账", "card", c.ID, "target", target, "task", taskID, "cause", err)
		return zero, err
	}
	if err := d.St.RecordDispatch(c.ID, ledger.DispatchSnapshot{
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
		DisciplineVersion: d.DisciplineVersion,
		Target:            target, TaskID: taskID, Branch: snapshotBranch,
		Base: base, BaseCommit: baseCommit,
		Executor: executor, Model: model,
		Purpose: purpose, PlanPath: req.PlanPath, Actor: d.Actor,
	}); err != nil {
		slog.Default().Warn("模板派发快照落账失败", "card", c.ID, "target", target,
			"task", taskID, "base", base, "base_commit", baseCommit, "cause", err)
		return zero, fmt.Errorf("快照落账: %w", err)
	}
	slog.Default().Info("模板派发完成", "card", c.ID, "template", tpl.Name,
		"task", taskID, "target", target, "executor", executor, "model", model,
		"branch", snapshotBranch, "discipline", disciplineName)
	return DispatchResult{
		Card: c.ID, Task: taskID, Target: target, Branch: snapshotBranch,
		Base: base, BaseCommit: baseCommit,
		Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineName: disciplineName,
		DisciplineVersion: d.DisciplineVersion,
	}, nil
}

// disciplineAndTarget 返回一次模板派发的有效（纪律角色名，目标机）：
// 请求覆盖优先，模板缺省兜底。ViaTemplate 与 PreflightDiscipline 必须共用
// 这一份实现——调用方的缝 1 解析若与派发时的裁决漂移，探活会打错机器、
// 正文会配错名字，拒发闸反而变成事故源。
func disciplineAndTarget(def ledger.TemplateDef, overrideName, reqTarget string) (name, target string) {
	name = def.Discipline
	if overrideName != "" {
		name = overrideName
	}
	target = reqTarget
	if target == "" {
		target = def.Target
	}
	return name, target
}

// PreflightDiscipline 以与 ViaTemplate 完全相同的裁决顺序算出一次模板派发的
// 有效（纪律角色名，目标机），供调用方在装配 Dispatcher 前完成缝 1 解析：
// 名字经账本 lookup 取正文、目标机探 Status 拿能力位，一并交给
// discipline.ResolveDispatch 产出正文三元组，再填进 Dispatcher 数据字段。
//
// 参数：st 账本；templateName 模板名；overrideName 调用方角色覆盖（空=不覆盖）；
// reqTarget 调用方目标机覆盖（空=用模板的）。
// 返回：有效角色名与目标机。target 为空表示「模板与请求都没定目标机」——这是
// 纯计算结果不是错误，如何处置（提前拒绝 / 放给 ViaTemplate 的既有失败路径）
// 由调用方按自己的同步语义决定；模板取不到时返回错误。
//
// 为什么导出：有效值依赖「请求覆盖 > 模板缺省」这套回退顺序，本包内
// ViaTemplate 与这里是唯一两处消费点且共用同一私有实现；调用方绕开它
// 自行推导就会与实际派发漂移。
func PreflightDiscipline(st *ledger.Store, templateName, overrideName, reqTarget string) (name, target string, err error) {
	tpl, err := st.GetTemplate(templateName, 0)
	if err != nil {
		return "", "", fmt.Errorf("取模板: %w", err)
	}
	name, target = disciplineAndTarget(tpl.Def, overrideName, reqTarget)
	return name, target, nil
}

// buildPrompt 把 executor 收到的 prompt 按三段拼起来。
//
// 参数：
//   - body:  模板正文（占位符已替换完）
//   - c:     卡
//   - base:  卡的有效基线分支，可为空
//   - carry: 是否拼入卡上下文段（节点的 CarryCardContext 开关）
//   - omitAccept: 是否**不**注入整卡验收判据（节点的 OmitAcceptance 开关）
//   - extra: 本次派发的临时补充说明，可为空
//   - outputPath: 本节点声明路径；独立于 carry，即使不带卡上下文也必须注入。
//     这是机器精确匹配键；日期前缀只作为历史文件提示，不是合法变体。
//
// 返回：拼好的 prompt。三段之间用空行分隔，缺席的段不留空标题。
//
// why 分三段而不是全塞进模板：模板是**复用**的（同一份审阅模板给所有卡用），
// 卡上下文是**每张卡不同的事实**，补充说明是**这一次才有的话**。混在一起就
// 只能靠占位符硬塞，而占位符加一个就要改所有模板。
//
// 注意：**这里绝不拼纪律块正文**。正文由调用方经缝 1 解析后走 DispatchOpts
// 的 DisciplineText 独立通道下发；两份纪律同场会让审阅的「只读」被实现块的
// 「完成即 commit」推翻（2026-08-19 真机出过一次）。
func buildPrompt(body string, c ledger.Card, base string, carry, omitAccept bool, extra, outputPath string) string {
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
		// 判据有两个注入通道（模板的 {{ACCEPT}} 与这一段），开关必须同时管住
		// 两个——只堵一个等于没堵，charter 流的节点两个通道都开着。
		if c.AcceptanceCriteria != "" && !omitAccept {
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
	if strings.TrimSpace(outputPath) != "" {
		sections = append(sections,
			"## 本节点产出物\n\n- 法定路径："+outputPath+
				"\n- 请把本节点产出物写到该路径，不要另起文件名。"+
				"不要加日期前缀；带 YYYY-MM-DD- 的是历史文件，不是本节点法定产出。")
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
