// b382_work_branch_dispatch_test.go —— B382 派发侧回归。
//
// 职责：锁住「登记的工作分支被审阅轮取用为基线」（spec §6 接缝 1 调用方），以及
// 「登记分支未发布 origin 时跨机拒发并指路 push」（接缝 3，复用现状 WorkBranchPublished）。
// 缝：Dispatcher.ViaTemplate——审阅轮取工作分支与跨机 origin 闸都在此。
// 边界：不碰真 git（Transport 注入桩）、不启 agentd；生产逻辑零改动，本文件只加锁。
package ledgerstep

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB382ReviewWithoutAnySourcePointsToRegistration 锁接缝 1 断言②的调用点：
// 卡无快照无登记时，审阅轮拒发文案指路登记命令（不是「还没派过实现轮？」）。
func TestB382ReviewWithoutAnySourcePointsToRegistration(t *testing.T) {
	st, card := dispatchTestCard(t)
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(context.Context, DispatchOpts) (string, string, error) {
		return "T-should-not", "", nil
	}}
	_, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Node: "review"})
	if err == nil || !strings.Contains(err.Error(), "work-branch") {
		t.Fatalf("无快照无登记的审阅轮应指路登记，实得 %v", err)
	}
}

// TestB382ReviewUsesRegisteredBranch 锁接缝 1 调用方：卡无快照、有登记，审阅轮
// （目标机为空 ⇒ 不触发跨机闸）应以登记分支为 base，并切一次性审阅分支、走本地起点。
func TestB382ReviewUsesRegisteredBranch(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(_ context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-b382-review", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Node: "review"}); err != nil {
		t.Fatalf("登记后审阅轮应可派发，实得 %v", err)
	}
	if got.Base != "feat/b382-work" {
		t.Fatalf("审阅基线应为登记分支，实得 %q", got.Base)
	}
	if want := "cards/" + card.ID + "-review-1"; got.Branch != want {
		t.Fatalf("审阅分支应为 %q，实得 %q", want, got.Branch)
	}
	if !got.LocalBaseBranch {
		t.Fatal("目标机为空（同机）时登记分支应走本地起点")
	}
}

// TestB382ReviewRegisteredBranchNotPublishedRejects 接缝 3：登记分支未发布 origin、
// 目标机非空（与「未知上一台」不同机）→ 拒发并指路 git push，且不触达 Transport。
func TestB382ReviewRegisteredBranchNotPublishedRejects(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	transportCalls := 0
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(context.Context, DispatchOpts) (string, string, error) {
		transportCalls++
		return "T-should-not", "", nil
	}}
	_, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Target: "mac-02", Node: "review"})
	if err == nil {
		t.Fatal("登记分支未发布 origin 时应拒发")
	}
	for _, want := range []string{"git push origin", "feat/b382-work"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("拒发文案缺 %q：%v", want, err)
		}
	}
	if transportCalls != 0 {
		t.Fatalf("拒发不得触达 Transport，调用次数=%d", transportCalls)
	}
}

// TestB382ReviewRegisteredBranchAfterPublishAllows 接缝 3 正例：登记分支已发布
// origin 后，跨目标机审阅可派发，且不再走本地-only 基线。
func TestB382ReviewRegisteredBranchAfterPublishAllows(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, err := st.RegisterWorkBranch(card.ID, "feat/b382-work", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	if err := st.RecordWorkBranchPublished(card.ID, "feat/b382-work", "", "", "tester"); err != nil {
		t.Fatalf("发布登记分支: %v", err)
	}
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(_ context.Context, opts DispatchOpts) (string, string, error) {
		got = opts
		return "T-b382-review", "", nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "review-generic", Target: "mac-02", Node: "review"}); err != nil {
		t.Fatalf("已发布后跨机审阅应放行，实得 %v", err)
	}
	if got.Base != "feat/b382-work" || got.LocalBaseBranch {
		t.Fatalf("跨机接续：base=%q local=%v，want feat/b382-work / false", got.Base, got.LocalBaseBranch)
	}
}

// TestB382SnapshotStillWinsAtDispatch 回归锁：有快照的卡派发路径不变，登记不改读数。
func TestB382SnapshotStillWinsAtDispatch(t *testing.T) {
	st, card := dispatchTestCard(t)
	if err := st.RecordDispatch(card.ID, ledger.DispatchSnapshot{
		Template: "feature-impl", Target: "mac-02", TaskID: "T-impl",
		Branch: "cards/" + card.ID + "-implement", Purpose: ledger.PurposeImplement, Actor: "test",
	}); err != nil {
		t.Fatalf("落快照: %v", err)
	}
	if _, err := st.RegisterWorkBranch(card.ID, "feat/ignored", "cli:u@h"); err != nil {
		t.Fatalf("登记: %v", err)
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatalf("WorkBranch: %v", err)
	}
	if wb.Branch != "cards/"+card.ID+"-implement" {
		t.Fatalf("快照应胜出，实得 %q", wb.Branch)
	}
}
