package accounts_test

// validate_test.go —— 账号编辑校验、删除与代理打码（B453 段 2；B464 段 1 改为发现建账号）。
//
// 职责：锁人写面（手填新建拒、改名字符规则、阈值/间隔校验、发现字段不可由请求改）；
// 删除（名下登录被载体引用拒删并列出、无引用连带删读数行、读数行删除失败只告警、CAS
// 冲突、不存在、按名字或 id 引用）；代理 userinfo 密码段打码。
// 边界：载体引用由组装点经 SetReferrers 注入（编制域按登录 id 给出），这里用桩函数。

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// TestPutEditValidation 锁 B464 的人写面：expect=0（手填新建）一律拒；改名禁 `:`；
// 阈值/间隔校验同 B453；请求里的发现字段（kind/指纹）被忽略、以现值为准。
func TestPutEditValidation(t *testing.T) {
	svc := accounts.New(newMem())
	acct := seedAccount(t, svc)
	if acct.IntervalMinutes != 15 || acct.WarnPercent != 60 || acct.LowPercent != 80 || acct.CreatedBy != "linux-01" {
		t.Fatalf("发现建出的账号应带缺省阈值与建者：%+v", acct)
	}
	mod := func(f func(*accounts.Account)) accounts.Account {
		a := acct
		f(&a)
		return a
	}
	cases := []struct {
		name    string
		a       accounts.Account
		expect  int
		wantErr string // 空 = 应通过
	}{
		{"手填新建", acct, 0, "由发现产生"},
		{"改名含冒号（段 2 起改名开放，规则见 manage_test.go）", mod(func(a *accounts.Account) { a.Name = "og:x" }), 1, "名字"},
		{"预警线等于低额度线", mod(func(a *accounts.Account) { a.WarnPercent, a.LowPercent = 80, 80 }), 1, "阈值"},
		{"低额度线超过 100", mod(func(a *accounts.Account) { a.LowPercent = 101 }), 1, "阈值"},
		{"预警线为 0", mod(func(a *accounts.Account) { a.WarnPercent = 0 }), 1, "阈值"},
		{"间隔不在可选值", mod(func(a *accounts.Account) { a.IntervalMinutes = 7 }), 1, "间隔"},
		{"改间隔且请求带别的 kind", mod(func(a *accounts.Account) { a.IntervalMinutes, a.Kind = 30, "codex" }), 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Put(tc.a, tc.expect)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过: %v", err)
				}
				return
			}
			if !errors.Is(err, accounts.ErrInvalid) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want ErrInvalid 含 %q, got %v", tc.wantErr, err)
			}
		})
	}
	v, _ := svc.Get(acct.Name) // 名字与 id 都能解析
	if v.Account.IntervalMinutes != 30 || v.Account.Kind != accounts.KindOpencodeGo || v.Account.Fingerprint != acct.Fingerprint {
		t.Fatalf("人写只改阈值/间隔，发现字段以现值为准：%+v", v.Account)
	}
}

// failingReadingDelete 让读数行删除失败，其余透传。
type failingReadingDelete struct{ *memRegistry }

func (f failingReadingDelete) Delete(kind, id string, expect int, actor string) error {
	if kind == accounts.KindReading {
		return errors.New("磁盘坏了")
	}
	return f.memRegistry.Delete(kind, id, expect, actor)
}

func TestDeleteRefusesReferencedAndCascadesReading(t *testing.T) {
	mem := newMem()
	svc := accounts.New(mem)
	seedAccount(t, svc)
	if err := svc.RecordProbe(ogID, successAt(0, 1, 10)); err != nil {
		t.Fatal(err)
	}
	logins, err := svc.Logins()
	if err != nil || len(logins) != 1 {
		t.Fatalf("播种应有一条登录：%+v %v", logins, err)
	}
	refs := []string{"c2", "c1"}
	svc.SetReferrers(func() (map[string][]string, error) {
		return map[string][]string{logins[0].ID: refs, "login-unrelated": {"c9"}}, nil
	})
	view, err := svc.Get(ogID)
	if err != nil || !reflect.DeepEqual(view.Carriers, []string{"c1", "c2"}) ||
		len(view.Logins) != 1 || !reflect.DeepEqual(view.Logins[0].Carriers, []string{"c1", "c2"}) {
		t.Fatalf("读面应在账号与登录上列出引用载体（排序）: %+v %v", view, err)
	}
	err = svc.Delete(ogID, 1)
	var ref *accounts.ReferencedError
	if !errors.Is(err, accounts.ErrReferenced) || !errors.As(err, &ref) || !reflect.DeepEqual(ref.Carriers, []string{"c1", "c2"}) {
		t.Fatalf("被引用应拒删并列出载体: %v", err)
	}
	refs = nil
	if err := svc.Delete(ogID, 99); !errors.Is(err, schedclient.ErrCASConflict) {
		t.Fatalf("版本不符应 CAS 冲突: %v", err)
	}
	if err := svc.Delete("opencode-go-aaaa", 1); err != nil {
		t.Fatalf("无引用应删除（按名字引用）: %v", err)
	}
	if _, err := svc.Get(ogID); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("定义行应已删: %v", err)
	}
	if _, err := mem.Get(accounts.KindReading, ogID); !errors.Is(err, schedclient.ErrNotFound) {
		t.Fatalf("读数行应连带删除: %v", err)
	}
	if err := svc.Delete(ogID, 1); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("不存在应 ErrNotFound: %v", err)
	}

	// 读数行删除失败只告警：定义行照删，返回成功；孤儿读数行不出现在列表。
	fmem := failingReadingDelete{newMem()}
	fsvc := accounts.New(fmem)
	seedAccount(t, fsvc)
	if err := fsvc.RecordProbe(ogID, successAt(0, 1, 10)); err != nil {
		t.Fatal(err)
	}
	if err := fsvc.Delete(ogID, 1); err != nil {
		t.Fatalf("读数行删除失败不应让删除失败: %v", err)
	}
	if list, err := fsvc.List(); err != nil || len(list) != 0 {
		t.Fatalf("孤儿读数行不应展示: %+v %v", list, err)
	}
}

// TestProxyMask 锁代理 userinfo 密码段打码（读面口径唯一定义在 proxycfg.Mask）。账号上的代理
// 已退役（B464：代理挂登录，编辑与打码回填在段 2 的登录代理端点）。
func TestProxyMask(t *testing.T) {
	cases := map[string]string{
		"socks5://user:secret@127.0.0.1:1080": "socks5://user:***@127.0.0.1:1080",
		"http://127.0.0.1:8080":               "http://127.0.0.1:8080",
		"http://onlyuser@127.0.0.1:8080":      "http://onlyuser@127.0.0.1:8080",
		"":                                    "",
	}
	for in, want := range cases {
		if got := accounts.MaskProxy(in); got != want {
			t.Fatalf("MaskProxy(%q) = %q, want %q", in, got, want)
		}
	}
}
