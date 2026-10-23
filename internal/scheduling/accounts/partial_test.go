package accounts_test

// partial_test.go —— B464 段 3 的单 HOME 检查语义（plan A12/V13）：Discovery.Partial 只对
// 被扫过的那一个 HOME 下「已失效」结论，其他 HOME 的登录保持原状；同时 NewAccountLogins
// 能回答「这条登录是不是本次新建账号挂上的」。
//
// 边界：内存 registry；扫描与取材由 agentd/accountwork 承接，本测试只锁归并规则。

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestReconcilePartialOnlyMarksScannedHomeGone(t *testing.T) {
	svc := accounts.New(newMem())
	iso := accounts.ScannedHome{Home: "~/.handoff/home/cx", HomeAbs: "/home/u/.handoff/home/cx",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: fpA}}}
	first := reconcile(t, svc, "m1", mainHome(ogFound("opencode.db", fpB)), iso)
	if first.NewAccounts != 2 || len(first.NewAccountLogins) != 2 || len(first.Logins) != 2 {
		t.Fatalf("首轮应建两个账号两条登录并标新账号：%+v", first)
	}
	for _, l := range first.Logins {
		if !first.NewAccountLogins[l.ID] {
			t.Fatalf("新建账号的登录应标进 NewAccountLogins：%+v", first.NewAccountLogins)
		}
	}

	// 只检查 cx：它下面凭据没了 → 它自己变 gone；主 HOME 的登录不在本轮扫描范围，必须保持 ok。
	res, err := svc.Reconcile(accounts.Discovery{Machine: "m1", At: at(60),
		Homes: []accounts.ScannedHome{{Home: iso.Home, HomeAbs: iso.HomeAbs}}, Partial: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Gone != 1 || len(res.NewAccountLogins) != 0 {
		t.Fatalf("单 HOME 检查应只让该 HOME 的登录失效、且不新建账号：%+v", res)
	}
	states := map[string]accounts.LoginState{}
	for _, l := range res.Logins {
		states[l.Home] = l.State
	}
	if states["~/.handoff/home/cx"] != accounts.LoginGone {
		t.Fatalf("被检查 HOME 的登录应已失效：%+v", states)
	}
	if states[accounts.MainHome] != accounts.LoginOK {
		t.Fatalf("其他 HOME 的登录不得被单 HOME 检查标失效：%+v", states)
	}
}
