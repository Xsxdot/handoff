package accounts_test

// validate_test.go —— 账号登记校验、删除与代理打码（B453 段 2，plan V1/V2 + 段 1 裁决 7）。
//
// 职责：锁 S1 全部校验（阈值次序、HOME 形态、kind 词表、名称、代理形态、同机同 HOME 同
// 条目查重点名冲突账号）；删除（被载体引用拒删并列出、无引用连带删读数行、读数行删除
// 失败只告警、CAS 冲突、不存在）；代理 userinfo 密码段打码与「PUT 收到打码值保留原值」。
// 边界：载体引用由组装点经 SetReferrers 注入（段 3 接编制域），这里用桩函数。

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func TestValidateRegistrationTable(t *testing.T) {
	svc := accounts.New(newMem())
	if _, err := svc.Put(newAccount(), 0); err != nil {
		t.Fatal(err)
	}
	mod := func(f func(*accounts.Account)) accounts.Account {
		a := newAccount()
		a.Name = "other"
		f(&a)
		return a
	}
	cases := []struct {
		name    string
		a       accounts.Account
		wantErr string // 空 = 应通过
	}{
		{"预警线等于低额度线", mod(func(a *accounts.Account) { a.WarnPercent, a.LowPercent = 80, 80 }), "阈值"},
		{"低额度线超过 100", mod(func(a *accounts.Account) { a.LowPercent = 101 }), "阈值"},
		{"预警线为 0", mod(func(a *accounts.Account) { a.WarnPercent = 0 }), "阈值"},
		{"HOME 相对路径", mod(func(a *accounts.Account) { a.HomeDir = "root/x" }), "HOME"},
		{"HOME ~user 形态", mod(func(a *accounts.Account) { a.HomeDir = "~bob" }), "HOME"},
		{"未知 kind", mod(func(a *accounts.Account) { a.Kind = "claude" }), "claude"},
		{"空名称", mod(func(a *accounts.Account) { a.Name = "" }), "账号名"},
		{"代理无 scheme", mod(func(a *accounts.Account) { a.Proxy = "127.0.0.1:1080" }), "代理"},
		{"同机同 HOME 同条目（尾斜杠归一）", mod(func(a *accounts.Account) { a.HomeDir = "/root/" }), "og-linux"},
		{"HOME ~ 合法", mod(func(a *accounts.Account) { a.HomeDir = "~" }), ""},
		{"HOME ~/x 合法", mod(func(a *accounts.Account) { a.HomeDir = "~/x" }), ""},
		{"他机同 HOME 合法", mod(func(a *accounts.Account) { a.Machine = "mac-mini" }), ""},
		{"同 HOME 不同 kind 合法", mod(func(a *accounts.Account) { a.Kind, a.Entry = accounts.KindCodex, "tokens" }), ""},
		{"合法代理", mod(func(a *accounts.Account) { a.HomeDir, a.Proxy = "/srv/x", "socks5://u:p@127.0.0.1:1080" }), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := accounts.New(newMem())
			if _, err := s.Put(newAccount(), 0); err != nil {
				t.Fatal(err)
			}
			_, err := s.Put(tc.a, 0)
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
	// 编辑自身（同名）不算重复。
	self := newAccount()
	self.IntervalMinutes = 30
	if _, err := svc.Put(self, 1); err != nil {
		t.Fatalf("编辑自身不应被查重拦下: %v", err)
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
	if _, err := svc.Put(newAccount(), 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordProbe("og-linux", successAt(0, 1, 10)); err != nil {
		t.Fatal(err)
	}
	refs := []string{"c2", "c1"}
	svc.SetReferrers(func(name string) ([]string, error) {
		if name == "og-linux" {
			return refs, nil
		}
		return nil, nil
	})
	view, err := svc.Get("og-linux")
	if err != nil || !reflect.DeepEqual(view.Carriers, []string{"c1", "c2"}) {
		t.Fatalf("读面应列出引用载体（排序）: %+v %v", view.Carriers, err)
	}
	err = svc.Delete("og-linux", 1)
	var ref *accounts.ReferencedError
	if !errors.Is(err, accounts.ErrReferenced) || !errors.As(err, &ref) || !reflect.DeepEqual(ref.Carriers, []string{"c1", "c2"}) {
		t.Fatalf("被引用应拒删并列出载体: %v", err)
	}
	refs = nil
	if err := svc.Delete("og-linux", 99); !errors.Is(err, schedclient.ErrCASConflict) {
		t.Fatalf("版本不符应 CAS 冲突: %v", err)
	}
	if err := svc.Delete("og-linux", 1); err != nil {
		t.Fatalf("无引用应删除: %v", err)
	}
	if _, err := svc.Get("og-linux"); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("定义行应已删: %v", err)
	}
	if _, err := mem.Get(accounts.KindReading, "og-linux"); !errors.Is(err, schedclient.ErrNotFound) {
		t.Fatalf("读数行应连带删除: %v", err)
	}
	if err := svc.Delete("og-linux", 1); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("不存在应 ErrNotFound: %v", err)
	}

	// 读数行删除失败只告警：定义行照删，返回成功；孤儿读数行不出现在列表。
	fmem := failingReadingDelete{newMem()}
	fsvc := accounts.New(fmem)
	if _, err := fsvc.Put(newAccount(), 0); err != nil {
		t.Fatal(err)
	}
	if err := fsvc.RecordProbe("og-linux", successAt(0, 1, 10)); err != nil {
		t.Fatal(err)
	}
	if err := fsvc.Delete("og-linux", 1); err != nil {
		t.Fatalf("读数行删除失败不应让删除失败: %v", err)
	}
	if list, err := fsvc.List(); err != nil || len(list) != 0 {
		t.Fatalf("孤儿读数行不应展示: %+v %v", list, err)
	}
}

func TestProxyMaskAndMaskedRoundTripKeepsStored(t *testing.T) {
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
	mem := newMem()
	svc := accounts.New(mem)
	a := newAccount()
	a.Proxy = "socks5://user:secret@127.0.0.1:1080"
	if _, err := svc.Put(a, 0); err != nil {
		t.Fatal(err)
	}
	// 控制台回填打码值并改间隔：原代理保留。
	edit := a
	edit.Proxy, edit.IntervalMinutes = "socks5://user:***@127.0.0.1:1080", 30
	if _, err := svc.Put(edit, 1); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.Get("og-linux")
	if v.Account.Proxy != "socks5://user:secret@127.0.0.1:1080" || v.Account.IntervalMinutes != 30 {
		t.Fatalf("打码回填应保留原代理: %+v", v.Account)
	}
	// 明确改成新代理 / 清空：照写。
	edit.Proxy = "http://127.0.0.1:9"
	if _, err := svc.Put(edit, 2); err != nil {
		t.Fatal(err)
	}
	edit.Proxy = ""
	if _, err := svc.Put(edit, 3); err != nil {
		t.Fatal(err)
	}
	v, _ = svc.Get("og-linux")
	if v.Account.Proxy != "" {
		t.Fatalf("清空代理应生效: %q", v.Account.Proxy)
	}
}
