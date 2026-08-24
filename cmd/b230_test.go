package cmd

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestResolveCardDispatchTemplateThreeStates(t *testing.T) {
	old := cardDispatchTemplate
	t.Cleanup(func() { cardDispatchTemplate = old })

	t.Run("zero templates points to template put", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		cardDispatchTemplate = ""
		_, err = resolveCardDispatchTemplate(st)
		if err == nil || !strings.Contains(err.Error(), "先用 template put") {
			t.Fatalf("零模板应指引先建模板: %v", err)
		}
	})

	t.Run("one template is selected", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if _, err := st.PutTemplate("only", ledger.TemplateDef{Executor: "codex", Purpose: ledger.PurposeImplement, Prompt: "x"}); err != nil {
			t.Fatal(err)
		}
		cardDispatchTemplate = ""
		got, err := resolveCardDispatchTemplate(st)
		if err != nil || got != "only" {
			t.Fatalf("唯一模板未自动采用: got=%q err=%v", got, err)
		}
	})

	t.Run("many templates require explicit name", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, name := range []string{"alpha", "beta"} {
			if _, err := st.PutTemplate(name, ledger.TemplateDef{Executor: "codex", Purpose: ledger.PurposeImplement, Prompt: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		cardDispatchTemplate = ""
		_, err = resolveCardDispatchTemplate(st)
		if err == nil || !strings.Contains(err.Error(), "显式指定 --template") ||
			!strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
			t.Fatalf("多模板应列选项并要求显式指定: %v", err)
		}
		cardDispatchTemplate = "beta"
		got, err := resolveCardDispatchTemplate(st)
		if err != nil || got != "beta" {
			t.Fatalf("显式模板应优先: got=%q err=%v", got, err)
		}
	})
}
