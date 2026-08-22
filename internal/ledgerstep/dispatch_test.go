// 模板派发共用段的回归测试：CLI 与 agentd 都只需替换 Transport，
// 纪律块角色名、prompt 与快照判据在这里统一守住。
package ledgerstep

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestViaTemplateMarksEmptyBaseForTargetResolution 钉住空卡基线的跨机决议边界：
// ledgerstep 不知道目标仓库路径，故只标记「请目标 agentd 解析默认分支」；
// 显式基线仍把原文传下去，不得被这个标记改写。
func TestViaTemplateMarksEmptyBaseForTargetResolution(t *testing.T) {
	t.Run("空基线标记目标侧解析", func(t *testing.T) {
		st, card := dispatchTestCard(t)
		var got DispatchOpts
		d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
			got = opts
			return "T-default-base", nil
		}}
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("ViaTemplate: %v", err)
		}
		if got.Base != "" {
			t.Fatalf("空卡基线不应在 ledgerstep 猜分支名，实得 %q", got.Base)
		}
		if !got.ResolveDefaultBase {
			t.Fatal("空卡基线必须要求目标 agentd 解析项目默认分支")
		}
	})

	t.Run("显式基线保持原语义", func(t *testing.T) {
		st, err := ledger.Open(t.TempDir() + "/ledger.db")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if err := st.EnsureDefaultWorkflows(); err != nil {
			t.Fatal(err)
		}
		if err := st.EnsureDefaultTemplates(); err != nil {
			t.Fatal(err)
		}
		card, err := st.CreateCard(ledger.NewCard{
			Title: "显式基线卡", Project: "demo", Workflow: "bug", BaseBranch: "release", Actor: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		var got DispatchOpts
		d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
			got = opts
			return "T-explicit-base", nil
		}}
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("ViaTemplate: %v", err)
		}
		if got.Base != "release" || got.ResolveDefaultBase {
			t.Fatalf("显式基线必须原样传递且不触发默认解析：base=%q resolve=%v", got.Base, got.ResolveDefaultBase)
		}
	})
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

// TestViaTemplateSecondRoundGetsNumberedBranch 非审阅模板重跑时分支按轮次
// 挂号：首轮无后缀（存量 cards/<卡>-implement 命名不变），第二轮 -2。
// 尾部断言穿序列化边界：挂号分支要落进 dispatched 快照并被 WorkBranch 读回，
// 否则终审会审到第一轮的旧分支。
func TestViaTemplateSecondRoundGetsNumberedBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	var branches []string
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		branches = append(branches, opts.Branch)
		return fmt.Sprintf("T-impl-%d", len(branches)), nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := d.ViaTemplate(context.Background(), card,
			TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
			t.Fatalf("第 %d 轮 ViaTemplate: %v", i+1, err)
		}
	}
	want := []string{"cards/" + card.ID + "-implement", "cards/" + card.ID + "-implement-2"}
	for i := range want {
		if branches[i] != want[i] {
			t.Fatalf("第 %d 轮分支应为 %q，实得 %q", i+1, want[i], branches[i])
		}
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if wb != want[1] {
		t.Fatalf("WorkBranch 应读回最新挂号分支 %q，实得 %q", want[1], wb)
	}
}

func TestBuildPromptThreeSections(t *testing.T) {
	card := ledger.Card{
		ID: "B9.1", Title: "做点什么", AcceptanceCriteria: "测试全绿",
		Attachments: []ledger.Attachment{
			{Kind: "spec", Path: "docs/spec.md"},
			{Kind: "plan", Path: "docs/plan.md"},
		},
	}
	t.Run("全关时只有模板正文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, "")
		if got != "模板正文" {
			t.Fatalf("不该有多余段落:\n%s", got)
		}
	})
	t.Run("带卡上下文", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", true, "")
		for _, want := range []string{
			"模板正文", "## 本卡上下文", "B9.1", "做点什么",
			"feat/x", "合并目标以此为准", "测试全绿",
			"spec: docs/spec.md", "plan: docs/plan.md",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("缺 %q:\n%s", want, got)
			}
		}
	})
	t.Run("带本次补充", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "feat/x", false, "这次只看并发安全")
		if !strings.Contains(got, "## 本次补充") || !strings.Contains(got, "这次只看并发安全") {
			t.Fatalf("补充段没拼进去:\n%s", got)
		}
	})
	t.Run("空基线不写死 main", func(t *testing.T) {
		got := buildPrompt("模板正文", card, "", true, "")
		if strings.Contains(got, "有效基线分支：main") {
			t.Fatalf("基线为空时不得替用户猜一个:\n%s", got)
		}
		if !strings.Contains(got, "有效基线分支：（未设置") {
			t.Fatalf("基线为空时应显式说明未设置:\n%s", got)
		}
	})
	t.Run("无附件不留空标题", func(t *testing.T) {
		bare := ledger.Card{ID: "B9.2", Title: "无附件"}
		got := buildPrompt("模板正文", bare, "feat/x", true, "")
		if strings.Contains(got, "- 附件：") {
			t.Fatalf("没有附件时不该出现附件小节:\n%s", got)
		}
	})
}
