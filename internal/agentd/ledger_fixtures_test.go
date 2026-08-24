package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// seedAgentdLedger installs explicit workflow/template fixtures for HTTP tests.
// Production agentd startup leaves the ledger empty; tests that need a flow
// declare the exact data they consume here.
func seedAgentdLedger(t *testing.T, st *ledger.Store) {
	t.Helper()
	for name, def := range map[string]ledger.TemplateDef{
		"feature-impl":   {Executor: "opencode", Purpose: ledger.PurposeImplement, BranchPrefix: "cards", Discipline: discipline.NameImplement, Prompt: "实现 {{TITLE}}：{{ACCEPT}}"},
		"review-generic": {Executor: "grok", Purpose: ledger.PurposeReview, BranchPrefix: "cards", Discipline: discipline.NameReview, Prompt: "审阅 {{TITLE}}：{{ACCEPT}}"},
	} {
		if _, err := st.PutTemplate(name, def); err != nil {
			t.Fatalf("写测试模板 %s: %v", name, err)
		}
	}
	workflows := map[string]ledger.WorkflowDef{
		"bug": {Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: ledger.StatusDoing},
			{Name: ledger.StatusDoing, Next: ledger.StatusReview, Dispatch: true, Template: "feature-impl"},
			{Name: ledger.StatusReview, Next: ledger.StatusDone, Dispatch: true, Verdict: true, Template: "review-generic", OnFail: ledger.StatusDoing},
			{Name: ledger.StatusDone},
		}},
		"feature": {Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "已出spec"},
			{Name: "已出spec", Next: ledger.StatusDoing, Gate: ledger.Gate{RequireAttachment: "spec"}},
			{Name: ledger.StatusDoing, Next: ledger.StatusReview, Dispatch: true, Template: "feature-impl"},
			{Name: ledger.StatusReview, Next: "待合并", Dispatch: true, Verdict: true, Template: "review-generic", OnFail: ledger.StatusDoing},
			{Name: "待合并", Next: ledger.StatusDone, Gate: ledger.Gate{RequireAcceptance: true}, Dispatch: true, Verdict: true, Template: "review-generic"},
			{Name: ledger.StatusDone},
		}},
		"triage": {Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "定性中"}, {Name: "定性中", Next: "已定性"}, {Name: "已定性"},
		}},
		"domain": {Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "拆解"}, {Name: "拆解", Next: "契约冻结"},
			{Name: "契约冻结", Gate: ledger.Gate{RequireAttachment: "contract"}},
		}},
		"attachment-gates": {Nodes: []ledger.NodeDef{
			{Name: ledger.StatusTodo, Next: "单附件"},
			{Name: "单附件", Next: "多附件", Gate: ledger.Gate{RequireAttachment: "spec"}},
			{Name: "多附件", Next: ledger.StatusDone, Gate: ledger.Gate{RequireAttachmentAny: []string{"plan", "contract"}}},
			{Name: ledger.StatusDone},
		}},
	}
	for name, def := range workflows {
		if _, err := st.PutWorkflow(name, def); err != nil {
			t.Fatalf("写测试工作流 %s: %v", name, err)
		}
	}
}
