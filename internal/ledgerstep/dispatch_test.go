// 模板派发共用段的回归测试：CLI 与 agentd 都只需替换 Transport，
// 纪律块角色名、prompt 与快照判据在这里统一守住。
package ledgerstep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func dispatchTestCard(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.EnsureDefaultWorkflows(); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureDefaultTemplates(); err != nil {
		t.Fatal(err)
	}
	card, err := st.CreateCard(ledger.NewCard{Title: "要派的卡", Project: "demo", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return st, card
}

// TestViaTemplateSendsDisciplineName 模板的角色名要随派发请求上送，
// 而不是被 CLI 读成正文拼进 prompt。
func TestViaTemplateSendsDisciplineName(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		got = opts
		return "T-fake-1", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Discipline != "implement" {
		t.Fatalf("请求里应带角色名 implement，实得 %q", got.Discipline)
	}
}

// TestViaTemplateNoDisciplineInPrompt prompt 里不许再出现纪律块正文。
// 这是本次重构的核心判据：两份纪律块同时在场时，审阅那次的「只读，不写」
// 会被实现块的「每个 task 完成即 commit」推翻。
func TestViaTemplateNoDisciplineInPrompt(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		got = opts
		return "T-fake-2", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	for _, mark := range []string{"# 审阅纪律", "# 执行纪律", "只读，不写", "每个 task 完成即 commit"} {
		if strings.Contains(got.Prompt, mark) {
			t.Fatalf("prompt 里不该再有纪律块正文，命中 %q：\n%s", mark, got.Prompt)
		}
	}
	if !strings.Contains(got.Prompt, "要派的卡") {
		t.Fatalf("模板正文应还在：\n%s", got.Prompt)
	}
}

// TestViaTemplateOverrideReplacesName --discipline-override 改的是名字，
// 不再是文件路径。
func TestViaTemplateOverrideReplacesName(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		got = opts
		return "T-fake-3", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02", DisciplineOverride: "review"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	if got.Discipline != "review" {
		t.Fatalf("override 应替换名字，实得 %q", got.Discipline)
	}
}

// TestViaTemplateSnapshotRecordsDisciplineName 派发事件快照要答得出
// 「这次用的哪块纪律」——正文不再经过 CLI，指纹算不出来了，
// 但那个问题本身没消失，答案换成名字。
func TestViaTemplateSnapshotRecordsDisciplineName(t *testing.T) {
	st, card := dispatchTestCard(t)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		return "T-fake-4", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("ViaTemplate: %v", err)
	}
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	show := string(raw)
	if !strings.Contains(show, `"discipline_name":"implement"`) {
		t.Fatalf("快照应记下角色名: %q", show)
	}
}
