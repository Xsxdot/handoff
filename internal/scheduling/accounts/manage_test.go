package accounts_test

// manage_test.go —— 账号管理：改名、删除连带、B453 手填旧行（B464 段 2，plan Q17/Q20、V7）。
//
// 职责：锁改名规则（唯一、禁 `:` 与 `/`、空 = 不改，名字冲突 → ErrNameTaken）；改名后
// 引用不断（登录行、读数行、准入都按账号 id）；删除连带删名下全部登录行（含他机）与读数行，
// 被引用拒删；B453 手填旧行（无指纹）不展示、可按 id（= 旧名）删除。
// 边界：内存 registry；HTTP 映射（409/400）由 agentd 的账号 API 测试承接。

import (
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestRenameRulesAndReferencesSurvive(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	acct := seedAccount(t, svc)
	reconcileAt(t, svc, "m9", base, accounts.ScannedHome{Home: "~", HomeAbs: "/home/x",
		Logins: []accounts.FoundLogin{{Kind: accounts.KindCodex, Source: "auth.json", Fingerprint: fpB, Label: "taken@example.com"}}})
	if err := svc.RecordProbe(acct.ID, successAt(0, 1, 90)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, to string
		want     error // nil = 应通过
	}{
		{"含冒号", "og:main", accounts.ErrInvalid},
		{"含斜杠", "og/main", accounts.ErrInvalid},
		{"与他人重名", "taken@example.com", accounts.ErrNameTaken},
		{"合法改名", "og-main", nil},
	}
	version := 1
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := acct
			a.Name = tc.to
			v, err := svc.Put(a, version)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("want %v，得 %v", tc.want, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("应通过：%v", err)
			}
			version = v
		})
	}
	if errors.Is(accounts.ErrNameTaken, accounts.ErrInvalid) {
		t.Fatal("名字冲突要能与 400 类校验错误区分（gateway 回 409）")
	}
	v, err := svc.Get("og-main")
	if err != nil || v.Account.ID != acct.ID || len(v.Logins) != 1 || v.Record.Reading == nil {
		t.Fatalf("改名后按新名读到同一账号、登录与读数不断：%+v %v", v, err)
	}
	if _, err := svc.Get(acct.Name); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("旧名不再解析：%v", err)
	}
	if as, err := svc.Assess(acct.ID, time.Now()); err != nil || as.State != accounts.StateLow || as.Account.Name != "og-main" {
		t.Fatalf("准入按 id 读、名字随改：%+v %v", as, err)
	}
	same := v.Account
	if _, err := svc.Put(same, v.Version); err != nil {
		t.Fatalf("名字不变（与自己同名）不算冲突：%v", err)
	}
	// 下一轮发现不改回自动名
	seedAccount(t, svc)
	if v, _ := svc.Get(acct.ID); v.Account.Name != "og-main" {
		t.Fatalf("发现不得改写人起的名字：%q", v.Account.Name)
	}
}

func TestDeleteCascadesAllLoginsAndReading(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	seedAccount(t, svc)
	reconcileAt(t, svc, "mac-mini", base, mainHome(ogFound("opencode.db", "sha256:aaaa11112222")))
	other := reconcileAt(t, svc, "mac-mini", base, mainHome(ogFound("opencode.db", "sha256:aaaa11112222"),
		ogFound("auth.json", fpB)))
	if len(other.Logins) != 2 {
		t.Fatalf("夹具：mac-mini 两条登录：%+v", other.Logins)
	}
	if err := svc.RecordProbe(ogID, successAt(0, 1, 10)); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.Get(ogID)
	if len(v.Logins) != 2 {
		t.Fatalf("夹具：账号两机各一条登录：%+v", v.Logins)
	}
	if err := svc.Delete(ogID, v.Version); err != nil {
		t.Fatalf("删除：%v", err)
	}
	logins, _ := svc.Logins()
	if len(logins) != 1 || logins[0].Account == ogID {
		t.Fatalf("名下全部登录（含他机）应连带删除，他人登录保留：%+v", logins)
	}
	if _, err := mem.Get(accounts.KindReading, ogID); !errors.Is(err, schedclient.ErrNotFound) {
		t.Fatalf("读数行应连带删除：%v", err)
	}
	// 下一轮发现以同一 id 重建、名字回到自动名（删除对话框写明）
	seedAccount(t, svc)
	if v, err := svc.Get(ogID); err != nil || v.Account.Name != "opencode-go-aaaa" {
		t.Fatalf("删除后再发现应以同一 id 重建：%+v %v", v.Account, err)
	}
}

// TestLegacyB453RowHiddenButDeletable 锁 Q20：B453 手填旧行（无指纹，id = 名字）不展示，
// DELETE 按 id 照常可删。
func TestLegacyB453RowHiddenButDeletable(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	body := []byte(`{"name":"og-old","kind":"opencode-go","machine":"m1","home_dir":"~","entry":"opencode-go"}`)
	if _, err := mem.Put(accounts.KindAccount, "og-old", 0, body, "b453"); err != nil {
		t.Fatal(err)
	}
	if list, err := svc.List(); err != nil || len(list) != 0 {
		t.Fatalf("旧行不展示：%+v %v", list, err)
	}
	if err := svc.Delete("og-old", 99); !errors.Is(err, schedclient.ErrCASConflict) {
		t.Fatalf("旧行删除同样按 CAS：%v", err)
	}
	if err := svc.Delete("og-old", 1); err != nil {
		t.Fatalf("旧行应可删：%v", err)
	}
	if _, err := mem.Get(accounts.KindAccount, "og-old"); !errors.Is(err, schedclient.ErrNotFound) {
		t.Fatalf("旧行应已删：%v", err)
	}
	if err := svc.Delete("og-old", 1); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("再删应 ErrNotFound：%v", err)
	}
}
