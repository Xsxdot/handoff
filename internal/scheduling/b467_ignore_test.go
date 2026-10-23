// b467_ignore_test.go —— B467 段 A 编制域三处：载体保存拒绝（Q6）、存量迁移剔除（Q7）、
// 准入边角态放行（Q5）。
//
// 职责：忽略行归账号域；编制域只读 View.Ignored/Assessment，不直接碰 account_ignore 行。
// 边界：经真实账本门面（newAccountFixture），账号与登录由发现归并建出。
package scheduling_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// ignoreHome 返回 seedLogins 在 machine 的主 HOME 上那条 opencode-go 登录所属账号的 id。
func ignoreAccountID(t *testing.T, acc *accounts.Service, machine, home, source string) string {
	t.Helper()
	l, err := acc.Login(ogLogin(machine, home, source))
	if err != nil {
		t.Fatalf("读登录: %v", err)
	}
	return l.Account
}

func TestPutCarrierRejectsIgnoredAccountLogin(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	seedLogins(t, acc, "m1", "/home/u", map[string]map[string]string{
		"~":                 {"opencode.db": "sha256:111111111111"},
		"~/.handoff/home/x": {"auth.json": "sha256:222222222222"},
	})
	login := ogLogin("m1", "~", "opencode.db")
	accountID := ignoreAccountID(t, acc, "m1", "~", "opencode.db")
	v, err := acc.Ignore(accountID, "m1")
	if err != nil {
		t.Fatalf("忽略: %v", err)
	}
	name := v.Account.Name

	err = svc.PutCarrier(scheduling.Carrier{Name: "c", Machine: "m1", CLI: "opencode", Login: login}, 0)
	if !errors.Is(err, scheduling.ErrInvalid) || !strings.Contains(err.Error(), "账号 "+name+" 已忽略，先在账号页恢复") {
		t.Fatalf("选已忽略账号的登录应 400 类错误并点名账号: %v", err)
	}
	if _, gerr := facade.Get("carrier", "c"); gerr == nil {
		t.Fatal("被拒时不得写载体行")
	}

	// Q6：登录没变、只改并发位的编辑同样被拒（旧二进制在混版本窗口留下的边角态）。
	other := ogLogin("m1", "~/.handoff/home/x", "auth.json")
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "edge", Machine: "m1", CLI: "opencode", Login: other})
	otherID := ignoreAccountID(t, acc, "m1", "~/.handoff/home/x", "auth.json")
	if _, err := acc.Ignore(otherID, "m1"); err != nil {
		t.Fatalf("忽略（模拟旧二进制留下的边角态）: %v", err)
	}
	if err := svc.PutCarrier(scheduling.Carrier{Name: "edge", Machine: "m1", CLI: "opencode",
		Login: other, MaxConcurrency: 2}, 1); !errors.Is(err, scheduling.ErrInvalid) || !strings.Contains(err.Error(), "已忽略") {
		t.Fatalf("未改登录的编辑也应被拒: %v", err)
	}
}

func TestMigrateCarrierLoginsSkipsIgnoredAccount(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	mainAbs := t.TempDir()
	seedLogins(t, acc, "m1", mainAbs, map[string]map[string]string{
		"~":                   {"opencode.db": "sha256:111111111111"},
		"~/.handoff/home/two": {"auth.json": "sha256:333333333333", "opencode.db": "sha256:444444444444"},
	})
	isSelf := func(m string) bool { return m == "m1" }
	expand := func(h string) (string, error) {
		if h == "~" {
			return mainAbs, nil
		}
		if rest, ok := strings.CutPrefix(h, "~/"); ok {
			return filepath.Join(mainAbs, rest), nil
		}
		return filepath.Clean(h), nil
	}
	// 主 HOME 唯一兼容登录属已忽略账号 → 不写、不落「无账号」（Q7）。
	ignoredID := ignoreAccountID(t, acc, "m1", "~", "opencode.db")
	if _, err := acc.Ignore(ignoredID, "m1"); err != nil {
		t.Fatal(err)
	}
	// 同一隔离 HOME 有两条兼容登录：一条属已忽略账号，一条正常 → 选正常那条。
	ignoredTwo := ignoreAccountID(t, acc, "m1", "~/.handoff/home/two", "opencode.db")
	if _, err := acc.Ignore(ignoredTwo, "m1"); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"main": `{"name":"main","machine":"m1","cli":"opencode","home_dir":""}`,
		"two":  `{"name":"two","machine":"m1","cli":"opencode","home_dir":"~/.handoff/home/two"}`,
	} {
		if _, err := facade.Put("carrier", name, 0, []byte(body), "legacy"); err != nil {
			t.Fatal(err)
		}
	}
	logins, err := acc.Logins()
	if err != nil {
		t.Fatal(err)
	}
	results, err := svc.MigrateCarrierLogins(logins, isSelf, expand)
	if err != nil {
		t.Fatalf("迁移: %v", err)
	}
	byName := map[string]scheduling.MigrationResult{}
	for _, r := range results {
		byName[r.Carrier] = r
	}
	if r := byName["main"]; r.Login != "" || r.NoAccount || !strings.Contains(r.Reason, "已忽略") {
		t.Fatalf("主 HOME 只剩已忽略登录应留作未选并说明原因: %+v", r)
	}
	if c, _ := svc.Carrier("main"); c.Login != "" || c.NoAccount {
		t.Fatalf("主 HOME 只剩已忽略登录不得迁为无账号: %+v", c)
	}
	want := accounts.LoginID("m1", "~/.handoff/home/two", accounts.KindOpencodeGo, "", "auth.json")
	if c, _ := svc.Carrier("two"); c.Login != want {
		t.Fatalf("应选同 HOME 里未忽略的那条登录（want %s）: %+v", want, c)
	}
	if r := byName["two"]; r.Rule != scheduling.MigrationRule(1) || !strings.Contains(r.Reason, "恰一条") {
		t.Fatalf("剔除已忽略后恰一条应走规则 1: %+v", r)
	}
}

func TestAdmissionLetsIgnoredAccountFrozenReadingThrough(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	seedLogins(t, acc, "m1", "/home/u", map[string]map[string]string{"~": {"opencode.db": "sha256:111111111111"}})
	login := ogLogin("m1", "~", "opencode.db")
	accountID := ignoreAccountID(t, acc, "m1", "~", "opencode.db")
	if err := acc.RecordProbe(accountID, accounts.ProbeOutcome{StartedAt: time.Now(), FinishedAt: time.Now(),
		Reading: &accounts.Reading{ProbedAt: time.Now(), Windows: []accounts.Window{{Key: "weekly", UsedPercent: 90}}}}); err != nil {
		t.Fatalf("写低额度读数: %v", err)
	}
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "c", Machine: "m1", CLI: "opencode", Login: login})
	if _, err := acc.Ignore(accountID, "m1"); err != nil {
		t.Fatalf("忽略: %v", err)
	}
	if v, err := acc.Get(accountID); err != nil || v.State != accounts.StateLow || v.Ignored == nil {
		t.Fatalf("前置：忽略后读数冻结为低额度: %+v %v", v, err)
	}
	if _, err := svc.AdmitCarrier("c"); err != nil {
		t.Fatalf("所选登录的账号已忽略应按未选账号登录放行（Q5），得 %v", err)
	}
}
