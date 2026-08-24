package ledger

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
)

// seedTestTemplates 为派发组装测试写入显式测试数据。生产 Store.Open 不安装模板。
// 夹具只验证模板能存、能取、能组装，不替已退场的出厂方法论正文作证。
func seedTestTemplates(t *testing.T, s *Store) error {
	t.Helper()
	defs := map[string]TemplateDef{
		"feature-impl": {
			Executor: "opencode", Purpose: "implement", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     "实现 {{TITLE}}：{{ACCEPT}}",
		},
		"review-generic": {
			Executor: "grok", Purpose: "review", BranchPrefix: "cards",
			Discipline: discipline.NameReview,
			Prompt:     "审阅 {{TITLE}}：{{ACCEPT}}",
		},
		"domain-breakdown": {
			Executor: "codex", Purpose: "breakdown", BranchPrefix: "cards",
			Discipline: discipline.NameSpecDraft,
			Prompt:     "拆解 {{TITLE}}：{{ACCEPT}}",
		},
		"domain-ticket0": {
			Executor: "codex", Purpose: "ticket0", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     "冻结 {{TITLE}}：{{ACCEPT}}",
		},
		"domain-integration": {
			Executor: "codex", Purpose: "integration", BranchPrefix: "cards",
			Discipline: discipline.NameImplement,
			Prompt:     "集成 {{TITLE}}：{{ACCEPT}}",
		},
	}
	for name, def := range defs {
		_, err := s.GetTemplate(name, 0)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutTemplate(name, def); err != nil {
			return err
		}
	}
	return nil
}

// seedTestWorkflows installs explicit workflow fixtures used by legacy domain
// operation tests. The fixture shape mirrors the old tests' required gates and
// dispatch nodes but is not production data.
func seedTestWorkflows(t *testing.T, s *Store) error {
	t.Helper()
	if err := seedTestTemplates(t, s); err != nil {
		return err
	}
	defs := map[string]WorkflowDef{
		"feature": {Nodes: []NodeDef{
			{Name: StatusTodo, Next: "已出spec"},
			{Name: "已出spec", Next: StatusDoing, Gate: Gate{RequireAttachment: "spec"}},
			{Name: StatusDoing, Next: StatusReview, Dispatch: true, Template: "feature-impl", CarryCardContext: true},
			{Name: StatusReview, Next: "待合并", OnFail: StatusDoing, Dispatch: true, Verdict: true, Template: "review-generic", CarryCardContext: true, MaxRounds: 3},
			{Name: "待合并", Next: StatusDone, Gate: Gate{RequireAcceptance: true}, Dispatch: true, Verdict: true, Template: "review-generic", CarryCardContext: true, MaxRounds: 1, Override: NodeOverride{Discipline: discipline.NameFinishing}, HumanBases: []string{"main"}},
			{Name: StatusDone},
		}},
		"bug": {Nodes: []NodeDef{
			{Name: StatusTodo, Next: StatusDoing},
			{Name: StatusDoing, Next: StatusReview, Dispatch: true, Template: "feature-impl", CarryCardContext: true},
			{Name: StatusReview, Next: StatusDone, OnFail: StatusDoing, Dispatch: true, Verdict: true, Template: "review-generic", CarryCardContext: true, MaxRounds: 3},
			{Name: StatusDone},
		}},
		"triage": {Nodes: []NodeDef{
			{Name: StatusTodo, Next: "定性中"},
			{Name: "定性中", Next: "已定性"},
			{Name: "已定性"},
		}},
		"domain": {Nodes: []NodeDef{
			{Name: StatusTodo, Next: "拆解"},
			{Name: "拆解", Next: "契约冻结", Dispatch: true, Template: "domain-breakdown", CarryCardContext: true},
			{Name: "契约冻结", Next: "域实现", Gate: Gate{RequireAttachment: "contract"}, Dispatch: true, Verdict: true, Template: "domain-ticket0", CarryCardContext: true, MaxRounds: 2},
			{Name: "域实现", Next: "集成"},
			{Name: "集成", Next: "终审", OnFail: "域实现", Gate: Gate{RequireChildrenDone: true}, Dispatch: true, Verdict: true, Template: "domain-integration", CarryCardContext: true, MaxRounds: 2},
			{Name: "终审", Next: StatusDone, Gate: Gate{RequireAcceptance: true}, Dispatch: true, Verdict: true, Template: "review-generic", CarryCardContext: true, MaxRounds: 1, Override: NodeOverride{Discipline: discipline.NameFinishing}, HumanBases: []string{"main"}},
			{Name: StatusDone},
		}},
	}
	for name, def := range defs {
		current, err := s.getWorkflowStored(name, 0)
		if err == nil {
			if len(current.Def.Nodes) > 0 {
				continue
			}
			if len(current.Def.States) > 0 {
				if _, err := s.PutWorkflow(name, def); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutWorkflow(name, def); err != nil {
			return err
		}
	}
	return nil
}
