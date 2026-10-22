// approver_context.go —— 审批者的任务上下文（B460 S7/S8）。
//
// 职责：
//   - ApproverTaskContext：审批者裁决时看得到的任务形态与三处任务目录
//   - approverPrompt：裁决提示词的唯一组装点——基线模板 + 条件段（上下文段、只读政策段）
//   - Manager.approverTaskContext：两个发起点（Manager consultApprover、OpenCode bindApproval
//     闭包）从任务记录组装上下文
//
// 边界：
//   - 上下文由编排从任务记录取出后**下传**（Decide → RunLocalRing / DecideRemote），审批
//     领域与对端 agentd 都不反查任务（spec 接缝语义）
//   - 零值上下文 = 无上下文：提示词与本卡之前逐字节一致（老发起方、老 wire 都落这里）
//   - wire 形状在 proto.ApproverTaskContext（冻结 F7/F8），本类型是编排内部形状，转换在门面
package orchestration

import (
	"fmt"
	"os"
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
)

// ApproverTaskContext 是审批者裁决时附带的任务上下文。零值 = 无上下文。
//
//   - Mode: 任务形态（readonly / branch / 空=分支形态的旧任务）
//   - Workdir: 工作树
//   - ArtifactsDir: 产物目录（仅当目录存在时填，老任务没有）
//   - TaskTmpDir: 任务临时目录（执行器的 TMPDIR）
type ApproverTaskContext struct {
	Mode         string
	Workdir      string
	ArtifactsDir string
	TaskTmpDir   string
}

// IsZero 报告上下文是否为空（无上下文 → 提示词走基线）。
func (c ApproverTaskContext) IsZero() bool { return c == ApproverTaskContext{} }

// approverPrompt 组装裁决提示词。
//
// 参数：taskSummary 任务摘要；permission 权限原文；nonce 防伪编号；tc 任务上下文
//
// 上下文段与政策段拼在任务摘要之后（即模板「任务背景：%s」之后、「权限请求」之前）。
// 它们作为 Sprintf 的**参数**而非模板进入，路径里的 % 不会打乱填充；approverPromptTemplate
// 本身一字不改（B451 用户逐字批准的那一行在其中）。零值上下文时 taskSummary 原样填入。
func approverPrompt(taskSummary, permission, nonce string, tc ApproverTaskContext) string {
	return fmt.Sprintf(approverPromptTemplate, taskSummary+approverContextSection(tc), permission, nonce, nonce)
}

// approverContextSection 返回以换行开头的上下文段（readonly 再加政策段）；零值返回空串。
//
// 文案见 plan §9（措辞归 plan，增删「应批准 / 必须升级」条目要回 spec 批准）。
func approverContextSection(tc ApproverTaskContext) string {
	if tc.IsZero() {
		return ""
	}
	mode := tc.Mode
	if mode == "" {
		mode = proto.TaskModeBranch
	}
	var b strings.Builder
	b.WriteString("\n任务上下文：")
	// 只解释本任务的实际形态（B465）：两种解释并列时，弱审批模型会把分支任务读成只读而升级
	b.WriteString("\n- 任务形态：" + mode + modeExplanation(mode))
	if tc.Workdir != "" {
		b.WriteString("\n- 工作树：" + tc.Workdir)
	}
	if tc.ArtifactsDir != "" {
		b.WriteString("\n- 产物目录：" + tc.ArtifactsDir + "（环境变量 " + proto.ArtifactsEnvVar + "）")
	}
	if tc.TaskTmpDir != "" {
		b.WriteString("\n- 任务临时目录：" + tc.TaskTmpDir + "（环境变量 TMPDIR）")
	}
	if tc.Mode == proto.TaskModeReadonly {
		b.WriteString("\n" + readonlyApproverPolicy)
	}
	return b.String()
}

// modeExplanation 返回形态行括注；未知形态不加括注（不替审批者猜语义）。
func modeExplanation(mode string) string {
	switch mode {
	case proto.TaskModeReadonly:
		return "（readonly = 只读调查：不改仓库、不提交，结论写成报告交回）"
	case proto.TaskModeBranch:
		return "（分支任务：可在任务工作树内改代码、git commit 到本地任务分支；push 等改变远端仓库的操作不在此列）"
	}
	return ""
}

// readonlyApproverPolicy 是只读任务的裁决政策段（plan §9，S7 放行尺度 A）。
//
// 凭据清单不在这里手写：B460 S10 起与规则层同源，由 credentialListText 从
// permgate.CredentialLocations 渲染——规则层改判 Consult 的每类位置，政策里都点名升级。
var readonlyApproverPolicy = `只读任务的裁决政策：
- 应批准：为调查而读工作树里的代码与文档；在工作树、产物目录、任务临时目录内运行脚本（含 python 与 heredoc）读取、统计、转换这些目录里的数据；把报告、清单写进产物目录或任务临时目录；在这两个目录里建目录。
- 必须升级：读取凭据或密钥（见下方清单，以及任何文件名或环境变量名含 token、secret、credential、password、auth 的文件或变量）；写入上面三个目录之外的任何路径；git push 或任何改变远端仓库的操作；携带凭据、或把本机文件内容发往外部的网络请求。
- 凭据清单（读取或执行其中任何一项即升级）：` + credentialListText() + `
- 拿不准时升级。`

// credentialListText 把 permgate 的凭据位置清单渲染成政策段的一行（以句号结尾）。
//
// 目录项写「下任何文件」，带说明的项把说明括在后面；清单之后固定追加 .env 文件、打印环境
// 变量的命令（同为 permgate 判据）与任务目录里的副本/软链（grok 任务级 GROK_HOME、agy 任务
// HOME 的 .gemini）。
func credentialListText() string {
	var items []string
	for _, l := range permgate.CredentialLocations() {
		item := l.Path
		if strings.HasSuffix(item, "/") {
			item += " 下任何文件"
		}
		if l.Note != "" {
			item += "（" + l.Note + "）"
		}
		items = append(items, item)
	}
	items = append(items, "任何 .env 与 .env.* 文件", permgate.SecretEnvCommandsText,
		"以及任务目录里指向上述文件的副本或软链（例如任务级 GROK_HOME 下的 auth.json、agy 任务 HOME 下的 .gemini 目录）")
	return strings.Join(items, "；") + "。"
}

// approverTaskContext 从任务记录组装审批上下文。
//
// 读任务失败 → Warn + 零值上下文：上下文是裁决的辅助信息，不是前提，与摘要读失败用空串
// 同哲学（裁决照常按无上下文进行）。产物目录仅当存在时填（同 resumeEnvWithArtifacts 判据）。
func (m *Manager) approverTaskContext(taskID string) ApproverTaskContext {
	task, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Warn("审批上下文组装失败：读任务失败，按无上下文裁决", "task", taskID, "cause", err)
		return ApproverTaskContext{}
	}
	return m.approverTaskContextFrom(task)
}

// approverTaskContextFrom 用已读出的任务记录组装上下文（consultApprover 复用同一次读取）。
func (m *Manager) approverTaskContextFrom(task *proto.Task) ApproverTaskContext {
	tc := ApproverTaskContext{
		Mode:       task.Mode,
		Workdir:    task.Workdir(),
		TaskTmpDir: executor.TaskTmpDir(m.cfg.DataDir, task.ID),
	}
	if dir := TaskArtifactsDir(m.cfg.DataDir, task.ID); dirExists(dir) {
		tc.ArtifactsDir = dir
	}
	return tc
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
