package accounts_test

// discover_test.go —— 发现归并的承重回归（B464 段 1，plan V1/V5 段 1 子集）。
//
// 职责：锁「同身份归并到同一账号」在两机并发下成立（N=50 轮交错，单线程下随机 id 方案
// 也会全绿，只有负载能把它和确定性 id 区分开）；撞名加后缀；同 HOME 两来源不互认；
// 扫不到 → 已失效、再现 → 正常、换身份 → 改挂账号；出错的 HOME 不标已失效；
// 只在内容变化时写登录行；discovery_run 每轮写一次。
// 边界：内存 registry（与账本适配器同一 CAS 契约）；扫描与取材由 agentd/accountwork 承接。

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

const (
	fpA = "sha256:aaaa11112222"
	fpB = "sha256:bbbb33334444"
)

func ogFound(source, fp string) accounts.FoundLogin {
	return accounts.FoundLogin{Kind: accounts.KindOpencodeGo, Source: source, Fingerprint: fp}
}

func mainHome(found ...accounts.FoundLogin) accounts.ScannedHome {
	return accounts.ScannedHome{Home: "~", HomeAbs: "/home/u", Logins: found}
}

func reconcile(t *testing.T, svc *accounts.Service, machine string, homes ...accounts.ScannedHome) accounts.ReconcileResult {
	t.Helper()
	res, err := svc.Reconcile(accounts.Discovery{Machine: machine, At: base, Homes: homes})
	if err != nil {
		t.Fatalf("Reconcile(%s): %v", machine, err)
	}
	return res
}

// TestReconcileDedupeTwoMachinesConcurrent 是 V1 承重：两台「机器」对同一身份并发各跑 50 轮
// 归并 → 恰一个账号、两条登录。
func TestReconcileDedupeTwoMachinesConcurrent(t *testing.T) {
	const n = 50
	mem := newMem()
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for _, machine := range []string{"mac-mini", "linux-01"} {
		wg.Add(1)
		go func(machine string) {
			defer wg.Done()
			svc := accounts.New(mem) // 两机各自的服务实例，只共享账本
			for i := 0; i < n; i++ {
				if _, err := svc.Reconcile(accounts.Discovery{Machine: machine, At: at(i),
					Homes: []accounts.ScannedHome{mainHome(ogFound("opencode.db", fpA))}}); err != nil {
					errs <- fmt.Errorf("%s 第 %d 轮: %w", machine, i, err)
					return
				}
			}
		}(machine)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	views, err := accounts.New(mem).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("同身份两机并发发现应恰一个账号，得 %d：%+v", len(views), views)
	}
	v := views[0]
	if v.Account.ID != "opencode-go:aaaa11112222" || v.Account.Name != "opencode-go-aaaa" || v.Account.Fingerprint != fpA {
		t.Fatalf("账号 id/名字/指纹 = %q/%q/%q", v.Account.ID, v.Account.Name, v.Account.Fingerprint)
	}
	if len(v.Logins) != 2 || v.Logins[0].Login.Machine == v.Logins[1].Login.Machine {
		t.Fatalf("应两条登录（各机一条），得 %+v", v.Logins)
	}
	for _, lv := range v.Logins {
		if lv.Login.State != accounts.LoginOK || lv.Login.Home != "~" || lv.Login.Source != "opencode.db" {
			t.Fatalf("登录行形状不对：%+v", lv.Login)
		}
		if lv.Version != 1 {
			t.Fatalf("内容未变的重扫不得重写登录行，%s 版本 = %d", lv.Login.ID, lv.Version)
		}
	}
	if v.Version != 1 {
		t.Fatalf("账号行只应被建一次，版本 = %d", v.Version)
	}
}

// TestReconcileAutoNameCollisionAndSourcesDoNotMerge：不同身份同基干名 → 后缀；同 HOME 两来源
// （API key 与 OAuth）身份不同 → 两个账号。
func TestReconcileAutoNameCollisionAndSourcesDoNotMerge(t *testing.T) {
	svc := accounts.New(newMem())
	res := reconcile(t, svc, "m1", mainHome(
		ogFound("auth.json", "sha256:abcd11111111"),
		ogFound("opencode.db", "sha256:abcd22222222"),
	), accounts.ScannedHome{Home: "~/.handoff/home/x", HomeAbs: "/home/u/.handoff/home/x", Logins: []accounts.FoundLogin{
		{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:eeee11111111", Label: "u@example.com"},
		{Kind: accounts.KindOpencodeGo, Source: "auth.json", Fingerprint: "sha256:eeee22222222", Label: "u@example.com"},
	}})
	if res.NewAccounts != 4 || res.Added != 4 || len(res.Logins) != 4 {
		t.Fatalf("应新建 4 账号 4 登录：%+v", res)
	}
	views, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, v := range views {
		names[v.Account.ID] = v.Account.Name
	}
	want := map[string]string{
		"opencode-go:abcd11111111": "opencode-go-abcd",
		"opencode-go:abcd22222222": "opencode-go-abcd-2",
		"opencode-go:eeee11111111": "u@example.com",
		"opencode-go:eeee22222222": "u@example.com-2",
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("自动名 = %v，want %v", names, want)
	}
}

// TestReconcileBackfillsAccountLabelWithoutRenaming 锁 I1：先无 label 建账号，后来的来源带上
// 邮箱 → 只补 label，不改自动名；label 不变的重扫不再写（版本不动）。
func TestReconcileBackfillsAccountLabelWithoutRenaming(t *testing.T) {
	svc := accounts.New(newMem())
	first := reconcile(t, svc, "m1", mainHome(ogFound("opencode.db", fpA)))
	if first.NewAccounts != 1 {
		t.Fatalf("应新建一个账号：%+v", first)
	}
	views, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Account.Name != "opencode-go-aaaa" || views[0].Account.Label != "" {
		t.Fatalf("首轮自动名/label 不对：%+v", views)
	}
	if views[0].Version != 1 {
		t.Fatalf("首轮账号版本 = %d", views[0].Version)
	}
	reconcile(t, svc, "m1", mainHome(accounts.FoundLogin{Kind: accounts.KindOpencodeGo,
		Source: "opencode.db", Fingerprint: fpA, Label: "u@example.com"}))
	views, err = svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("补 label 不得建新账号：%+v", views)
	}
	if views[0].Account.Name != "opencode-go-aaaa" || views[0].Account.Label != "u@example.com" {
		t.Fatalf("应只补 label、名字不变：%+v", views[0].Account)
	}
	if views[0].Version != 2 {
		t.Fatalf("补 label 应写一次账号行，版本 = %d", views[0].Version)
	}
	reconcile(t, svc, "m1", mainHome(accounts.FoundLogin{Kind: accounts.KindOpencodeGo,
		Source: "opencode.db", Fingerprint: fpA, Label: "u@example.com"}))
	views, err = svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Version != 2 {
		t.Fatalf("label 未变的重扫不得再写，版本 = %d", views[0].Version)
	}
}

// TestReconcileLoginStateTransitions 锁段 1 的两态迁移：扫不到 → 已失效（不删行）、再现 →
// 正常、同槽位换身份 → 改挂账号；出错的 HOME 不标已失效；discovery_run 每轮写。
func TestReconcileLoginStateTransitions(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	first := reconcile(t, svc, "m1", mainHome(ogFound("opencode.db", fpA)))
	id := first.Logins[0].ID

	gone := reconcile(t, svc, "m1", mainHome())
	if gone.Gone != 1 || gone.Logins[0].State != accounts.LoginGone {
		t.Fatalf("扫不到应标已失效：%+v", gone)
	}
	if l, err := svc.Login(id); err != nil || l.State != accounts.LoginGone {
		t.Fatalf("已失效登录行应保留：%+v %v", l, err)
	}
	again := reconcile(t, svc, "m1", mainHome())
	if again.Gone != 0 || again.Changed != 0 {
		t.Fatalf("已失效的再扫不应重复计数/写：%+v", again)
	}

	back := reconcile(t, svc, "m1", mainHome(ogFound("opencode.db", fpA)))
	if back.Changed != 1 || back.Logins[0].State != accounts.LoginOK {
		t.Fatalf("再现应回正常：%+v", back)
	}

	swapped := reconcile(t, svc, "m1", mainHome(ogFound("opencode.db", fpB)))
	l, _ := svc.Login(id)
	if swapped.Changed != 1 || swapped.NewAccounts != 1 || l.Account != "opencode-go:bbbb33334444" || l.Fingerprint != fpB {
		t.Fatalf("同槽位换身份应改挂新账号：%+v / %+v", swapped, l)
	}
	if v, err := svc.Get("opencode-go:aaaa11112222"); err != nil || len(v.Logins) != 0 {
		t.Fatalf("旧账号应不再有该登录：%+v %v", v.Logins, err)
	}

	errored := accounts.ScannedHome{Home: "~", HomeAbs: "/home/u", Errors: []string{"auth.json 形状不对"}}
	if res := reconcile(t, svc, "m1", errored); res.Gone != 0 {
		t.Fatalf("扫描出错的 HOME 不得标已失效：%+v", res)
	}
	if res, err := svc.Reconcile(accounts.Discovery{Machine: "m1", At: base, Errors: []string{"列不出 ~/.handoff/home"}}); err != nil || res.Gone != 0 {
		t.Fatalf("整体扫描不完整时，未扫到的 HOME 不得标已失效：%+v %v", res, err)
	}
	runs, err := svc.DiscoveryRuns()
	if err != nil || len(runs) != 1 || runs[0].Machine != "m1" || len(runs[0].Errors) != 1 {
		t.Fatalf("discovery_run 应每机一行并带本轮错误：%+v %v", runs, err)
	}

	other := reconcile(t, svc, "m2")
	if other.Gone != 0 {
		t.Fatalf("他机的发现不得动本机登录：%+v", other)
	}
}
