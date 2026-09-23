// b400_base_guard_test.go —— B400 首派基线护栏的决策段回归（缝 1：ViaTemplate）。
//
// 职责：用假探针锁住「无显式基线 + 有 spec 附件 + 首个非审阅派发」时的四条决策
// 与两条探针异常；不碰 git（路径在场性由 T3 的 workspace/端到端用例覆盖）。
// 缝：`Dispatcher.ViaTemplate`——spec §6 承重缝的「派发决议段」本体；
// 生产调用方（startCardStep / 队列出队）最终都到达它。
package ledgerstep

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

const b400SpecPath = "docs/superpowers/specs/b400.md"

func b400CardWithSpec(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	st, card := dispatchTestCard(t)
	if _, err := st.AttachFile(card.ID, "spec", b400SpecPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}
	// AttachFile 只改库里的卡；传给 ViaTemplate 的是调用方手里的快照，
	// 必须重读才能带上 Attachments（否则护栏永远看到空附件、假放行）。
	fresh, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("重读卡: %v", err)
	}
	return st, fresh
}

// TestB400FirstDispatchRejectsMissingAttachment 断言①：默认线树缺附件 ⇒ 拒发，
// 文案含分支名、缺失路径与可行动作；且拒发发生在 Transport 之前。
func TestB400FirstDispatchRejectsMissingAttachment(t *testing.T) {
	st, card := b400CardWithSpec(t)
	probeCalled, transportCalled := false, false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(_ context.Context, project, base string, paths []string) (string, []string, error) {
			probeCalled = true
			if project != card.Project || base != "" || len(paths) != 1 || paths[0] != b400SpecPath {
				t.Fatalf("探针入参 = project:%q base:%q paths:%v", project, base, paths)
			}
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) {
			transportCalled = true
			return "T-b400", "", nil
		},
	}
	_, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err == nil {
		t.Fatal("默认线缺附件应拒发，实得 nil")
	}
	if !probeCalled {
		t.Fatal("护栏未调用探针")
	}
	if transportCalled {
		t.Fatal("拒发必须发生在 Transport 之前")
	}
	for _, want := range []string{"main", b400SpecPath, "card update", "--base-branch"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("拒发文案缺 %q：%v", want, err)
		}
	}
}

// TestB400FirstDispatchPassesWhenAttachmentPresent 断言②：附件在场 ⇒ 放行，零假阳性。
func TestB400FirstDispatchPassesWhenAttachmentPresent(t *testing.T) {
	st, card := b400CardWithSpec(t)
	transportCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "main", nil, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) {
			transportCalled = true
			return "T-b400", "", nil
		},
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("附件在场应放行，实得 %v", err)
	}
	if !transportCalled {
		t.Fatal("放行后应到达 Transport")
	}
}

// TestB400LaterNodeSkipsGuard 断言③（回归锁）：已有非审阅派发 ⇒ 后续节点不再查护栏。
func TestB400LaterNodeSkipsGuard(t *testing.T) {
	st, card := b400CardWithSpec(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", TemplateVersion: 1, Target: "mac-02",
		TaskID: "T-prev", Branch: "cards/demo-implement", Purpose: "implement", Actor: "tester",
	}); err != nil {
		t.Fatalf("写先派快照: %v", err)
	}
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("后续节点应放行，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("首个非审阅派发之后不得再查护栏")
	}
}

// TestB400ExplicitBaseSkipsGuard 断言④（豁免通道，防死锁）：卡自有显式基线 ⇒ 跳过，
// 即使探针会报缺附件也不得拒发。
func TestB400ExplicitBaseSkipsGuard(t *testing.T) {
	st, _ := dispatchTestCard(t)
	card, err := st.CreateCard(ledger.NewCard{
		Title: "显式基线卡", Project: "demo", Workflow: "bug", BaseBranch: "cards/demo-work", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建显式基线卡: %v", err)
	}
	if _, err := st.AttachFile(card.ID, "spec", b400SpecPath, "test"); err != nil {
		t.Fatalf("挂 spec: %v", err)
	}
	fresh, err := st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("重读卡: %v", err)
	}
	card = fresh
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "cards/demo-work", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("显式基线应豁免，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("显式基线必须最先短路，探针不得被调用")
	}
}

// TestB400NoAttachmentsPasses 接缝 2（边界）：未挂任何可查附件 ⇒ 放行、不调探针。
func TestB400NoAttachmentsPasses(t *testing.T) {
	st, card := dispatchTestCard(t)
	probeCalled := false
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			probeCalled = true
			return "main", []string{b400SpecPath}, nil
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("无附件可检应放行，实得 %v", err)
	}
	if probeCalled {
		t.Fatal("无可查附件不得调用探针")
	}
}

// TestB400ProbeErrorRejects：探针自身失败（如远端不可达）⇒ fail-closed 拒发并保留 cause。
func TestB400ProbeErrorRejects(t *testing.T) {
	st, card := b400CardWithSpec(t)
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "", nil, errors.New("远端仓库不可达")
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	_, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"})
	if err == nil || !strings.Contains(err.Error(), "远端仓库不可达") {
		t.Fatalf("探针失败应 fail-closed 拒发并保留 cause，实得 %v", err)
	}
}

// TestB400ProbeUnavailableSkips：探针不可用（本机无该项目位置）⇒ 放行并告警（P1 推荐甲）。
func TestB400ProbeUnavailableSkips(t *testing.T) {
	st, card := b400CardWithSpec(t)
	d := &Dispatcher{St: st, Actor: "tester",
		ProbeBaseAttachments: func(context.Context, string, string, []string) (string, []string, error) {
			return "", nil, ErrBaseProbeUnavailable
		},
		Transport: func(context.Context, DispatchOpts) (string, string, error) { return "T-b400", "", nil },
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("探针不可用应放行，实得 %v", err)
	}
}
