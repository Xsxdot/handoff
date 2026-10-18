// B439 §4.2 身份/路由拆分：身份值进共享账本与快照，路由值进 Transport/wire；
// 存量 ” 工作分支在自名机器上跳过跨机判据，新命名快照照旧。
package ledgerstep

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// namedResolver 模拟有自名的组装点：省略或 == 自名 → 本机（身份值=自名）。
func namedResolver(self string) func(string) (string, bool) {
	return func(target string) (string, bool) {
		if target == "" || target == self {
			return self, true
		}
		return target, false
	}
}

// TestB439NamedLocalDispatchLandsIdentityRoutesLocal 自名机器本机派发：
// 挂账/快照落自名，wire 走本机短路（空串），模型按路由值取空键。
func TestB439NamedLocalDispatchLandsIdentityRoutesLocal(t *testing.T) {
	st, card := dispatchTestCard(t)
	setTemplateModel(t, st, "", "local-model")
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", ResolveTarget: namedResolver("linux-01"),
		Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			got = opts
			return "T-named-local", "", nil
		}}
	result, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl"})
	if err != nil {
		t.Fatalf("自名本机派发: %v", err)
	}
	if got.Target != "" {
		t.Fatalf("wire target = %q，期望本机短路空串", got.Target)
	}
	if got.Model != "local-model" {
		t.Fatalf("模型应按路由值取空键，实得 %q", got.Model)
	}
	if result.Target != "linux-01" {
		t.Fatalf("result.Target = %q，期望身份值 linux-01", result.Target)
	}
	links, err := st.TasksOf(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Target != "linux-01" {
		t.Fatalf("挂账 target = %+v，期望身份值 linux-01", links)
	}
	wb, err := st.WorkBranch(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wb.Target != "linux-01" {
		t.Fatalf("工作分支 target = %q，期望身份值 linux-01", wb.Target)
	}
}

// TestB439RemoteDispatchLandsNameRoutesName 远端名：身份值=路由值=原名。
func TestB439RemoteDispatchLandsNameRoutesName(t *testing.T) {
	st, card := dispatchTestCard(t)
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", ResolveTarget: namedResolver("linux-01"),
		Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			got = opts
			return "T-remote", "", nil
		}}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("远端派发: %v", err)
	}
	if got.Target != "mac-02" {
		t.Fatalf("wire target = %q，期望 mac-02", got.Target)
	}
	links, err := st.TasksOf(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Target != "mac-02" {
		t.Fatalf("挂账 target = %+v，期望 mac-02", links)
	}
}

// TestB439LegacyEmptyWorkBranchOnNamedMachineSkipsCrossMachine 存量 ” 快照在
// 自名机器上是历史无归属：异机读者不该把它误判成跨机拒接。
func TestB439LegacyEmptyWorkBranchOnNamedMachineSkipsCrossMachine(t *testing.T) {
	st, card := dispatchTestCard(t)
	if _, _, err := st.RecordDispatchAndLinkTask(card.ID, ledger.DispatchSnapshot{
		Target: "", TaskID: "T-legacy", Branch: "cards/" + card.ID + "-implement",
		Purpose: "implement", Actor: "t",
	}); err != nil {
		t.Fatalf("写存量 '' 快照: %v", err)
	}
	var got DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", ResolveTarget: namedResolver("linux-01"),
		Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			got = opts
			return "T-remote", "", nil
		}}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("存量 '' 快照在自名机器上不应被判跨机拒接: %v", err)
	}
	if got.Target != "mac-02" {
		t.Fatalf("wire target = %q，期望 mac-02", got.Target)
	}
}

// TestB439NewNamedWorkBranchCrossMachineRejected 新命名快照切到另一台机器：
// 跨机判据照旧（origin 未发布则拒接）。
func TestB439NewNamedWorkBranchCrossMachineRejected(t *testing.T) {
	st, card := dispatchTestCard(t)
	var calls []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", ResolveTarget: namedResolver("linux-01"),
		Transport: func(ctx context.Context, opts DispatchOpts) (string, string, error) {
			calls = append(calls, opts)
			return "T-remote", "", nil
		}}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("首轮远端派发: %v", err)
	}
	if _, err := d.ViaTemplate(context.Background(), card, TemplateDispatch{Template: "feature-impl", Target: "mac-03"}); err == nil {
		t.Fatal("新命名快照切到另一台机器必须拒接")
	}
	if len(calls) != 1 {
		t.Fatalf("跨机拒接不得再调 Transport，实际 %d", len(calls))
	}
}
