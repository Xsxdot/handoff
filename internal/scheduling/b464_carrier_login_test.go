// b464_carrier_login_test.go —— B464 段 1 编制域：载体选登录（plan V10）、HOME 镜像（Q3）、
// 存量载体迁移（§5 / V12）、准入改读 载体 → 登录 → 账号。
//
// 职责：经真实账本门面落盘（newAccountFixture），账号与登录由发现归并建出；迁移的「本机」与
// HOME 展开由测试注入（生产由 agentd 组装侧注入 IsSelfMachine 与 hostapi.ExpandHomePath）。
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

// seedLogins 以一次发现归并在 machine 上落下若干 HOME 的 opencode-go 登录。homes 的键是机器无关
// 写法（`~` / `~/.handoff/home/<dir>`），值是该 HOME 的登录（source → 指纹）。
func seedLogins(t *testing.T, acc *accounts.Service, machine, mainAbs string, homes map[string]map[string]string) {
	t.Helper()
	var scanned []accounts.ScannedHome
	for home, logins := range homes {
		abs := mainAbs
		if home != "~" {
			abs = filepath.Join(mainAbs, strings.TrimPrefix(home, "~/"))
		}
		h := accounts.ScannedHome{Home: home, HomeAbs: abs}
		for source, fp := range logins {
			h.Logins = append(h.Logins, accounts.FoundLogin{Kind: accounts.KindOpencodeGo, Source: source, Fingerprint: fp})
		}
		scanned = append(scanned, h)
	}
	if _, err := acc.Reconcile(accounts.Discovery{Machine: machine, At: time.Now(), Homes: scanned}); err != nil {
		t.Fatalf("发现归并: %v", err)
	}
}

func ogLogin(machine, home, source string) string {
	return accounts.LoginID(machine, home, accounts.KindOpencodeGo, "", source)
}

func TestCarrierLoginValidationAndMirror(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	seedLogins(t, acc, "m1", "/home/u", map[string]map[string]string{
		"~":                 {"opencode.db": "sha256:111111111111"},
		"~/.handoff/home/x": {"auth.json": "sha256:222222222222"},
	})
	mainLogin, isoLogin := ogLogin("m1", "~", "opencode.db"), ogLogin("m1", "~/.handoff/home/x", "auth.json")

	for name, tc := range map[string]struct {
		c    scheduling.Carrier
		want string
	}{
		"他机登录":     {scheduling.Carrier{Name: "c", Machine: "m2", CLI: "opencode", Login: mainLogin}, "同机"},
		"kind 不兼容": {scheduling.Carrier{Name: "c", Machine: "m1", CLI: "grok", Login: mainLogin}, "不能用 opencode-go"},
		"登录不存在":    {scheduling.Carrier{Name: "c", Machine: "m1", CLI: "opencode", Login: "login-ghost"}, "login-ghost"},
		"二选一同给":    {scheduling.Carrier{Name: "c", Machine: "m1", CLI: "opencode", Login: mainLogin, NoAccount: true}, "二选一"},
	} {
		if err := svc.PutCarrier(tc.c, 0); !errors.Is(err, scheduling.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：应 ErrInvalid 且含 %q，得 %v", name, tc.want, err)
		}
	}

	// 主 HOME 登录 → ""；请求里的 home_dir 被忽略。
	if err := svc.PutCarrier(scheduling.Carrier{Name: "oc", Machine: "m1", CLI: "opencode", HomeDir: "/ignored",
		Model: "opencode-go/glm", Login: mainLogin}, 0); err != nil {
		t.Fatalf("选主 HOME 登录: %v", err)
	}
	if c, _ := svc.Carrier("oc"); c.HomeDir != "" || c.Login != mainLogin {
		t.Fatalf("主 HOME 登录应镜像为空 HOME：%+v", c)
	}
	// 换成隔离 HOME 登录 → 写登录 home、HOME 变 → pending。
	if err := svc.PutCarrier(scheduling.Carrier{Name: "oc", Machine: "m1", CLI: "opencode", Login: isoLogin}, 1); err != nil {
		t.Fatalf("改选隔离登录: %v", err)
	}
	if c, _ := svc.Carrier("oc"); c.HomeDir != "~/.handoff/home/x" || c.Status != scheduling.StatusPending {
		t.Fatalf("隔离登录应镜像 ~/.handoff/home/x 且回 pending：%+v", c)
	}
	// 无账号 · 主 HOME → ""，键 no_account 只在 true 时出现、login 缺席。
	if err := svc.PutCarrier(scheduling.Carrier{Name: "free", Machine: "m1", CLI: "opencode", HomeDir: "/x", NoAccount: true}, 0); err != nil {
		t.Fatalf("无账号载体: %v", err)
	}
	raw := string(mustBody(t, facade, "carrier", "free"))
	if !strings.Contains(raw, `"no_account":true`) || strings.Contains(raw, `"login"`) || !strings.Contains(raw, `"home_dir":""`) {
		t.Fatalf("无账号载体 body 形状不符：%s", raw)
	}
	raw = string(mustBody(t, facade, "carrier", "oc"))
	for _, legacy := range []string{`"credential"`, `"account"`, `"cred_fingerprint"`} {
		if strings.Contains(raw, legacy) {
			t.Fatalf("新版本不再写 %s：%s", legacy, raw)
		}
	}
}

// TestCarrierLoginMirrorKeepsLegacyHomeString 锁 Q3：登录不变时 home_dir 原串保留（存量载体可能
// 存了绝对路径；改串会让 HOME 判变 → pending、并让排队中的冻结绑定物理比对失败）。
func TestCarrierLoginMirrorKeepsLegacyHomeString(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	seedLogins(t, acc, "m1", "/home/u", map[string]map[string]string{"~/.handoff/home/x": {"auth.json": "sha256:222222222222"}})
	body := `{"name":"old","machine":"m1","cli":"opencode","home_dir":"/home/u/.handoff/home/x","credential":"main_home_sync","status":"online","login":"` +
		ogLogin("m1", "~/.handoff/home/x", "auth.json") + `"}`
	if _, err := facade.Put("carrier", "old", 0, []byte(body), "legacy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.PutCarrier(scheduling.Carrier{Name: "old", Machine: "m1", CLI: "opencode", MaxConcurrency: 2,
		Login: ogLogin("m1", "~/.handoff/home/x", "auth.json")}, 1); err != nil {
		t.Fatalf("编辑并发位: %v", err)
	}
	c, _ := svc.Carrier("old")
	if c.HomeDir != "/home/u/.handoff/home/x" || c.Status != scheduling.StatusOnline || c.MaxConcurrency != 2 {
		t.Fatalf("登录不变时 HOME 原串与状态应保留：%+v", c)
	}
}

func TestMigrateCarrierLoginsRules(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	mainAbs := t.TempDir()
	seedLogins(t, acc, "m1", mainAbs, map[string]map[string]string{
		"~":                    {"opencode.db": "sha256:111111111111"},
		"~/.handoff/home/x":    {"auth.json": "sha256:222222222222"},
		"~/.handoff/home/both": {"auth.json": "sha256:333333333333", "opencode.db": "sha256:444444444444"},
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
	legacy := map[string]string{
		"r1-main":     `{"name":"r1-main","machine":"m1","cli":"opencode","home_dir":"","credential":"standalone"}`,
		"r1-abs":      `{"name":"r1-abs","machine":"m1","cli":"opencode","home_dir":"` + filepath.Join(mainAbs, ".handoff/home/x") + `","credential":"main_home_sync"}`,
		"r2-ambig":    `{"name":"r2-ambig","machine":"m1","cli":"opencode","home_dir":"~/.handoff/home/both","model":"opencode-go/glm"}`,
		"r3-grok":     `{"name":"r3-grok","machine":"m1","cli":"grok","home_dir":""}`,
		"r4-nowhere":  `{"name":"r4-nowhere","machine":"m1","cli":"opencode","home_dir":"~/.handoff/home/none"}`,
		"chosen":      `{"name":"chosen","machine":"m1","cli":"opencode","home_dir":"","no_account":true}`,
		"other-machn": `{"name":"other-machn","machine":"m2","cli":"opencode","home_dir":""}`,
	}
	for name, body := range legacy {
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
	check := func(name string, rule scheduling.MigrationRule, login string, noAccount bool) {
		t.Helper()
		r := byName[name]
		c, _ := svc.Carrier(name)
		if r.Rule != rule || c.Login != login || c.NoAccount != noAccount {
			t.Fatalf("%s：规则 %d login=%q no_account=%v，得 %+v / 载体 %+v", name, rule, login, noAccount, r, c)
		}
	}
	check("r1-main", 1, ogLogin("m1", "~", "opencode.db"), false)
	check("r1-abs", 1, ogLogin("m1", "~/.handoff/home/x", "auth.json"), false)
	check("r2-ambig", 2, "", false) // 两条 opencode-go 登录，模型前缀消歧不了
	check("r3-grok", 3, "", true)   // 主 HOME 无 grok 登录 → 无账号 · 主 HOME
	check("r4-nowhere", 4, "", false)
	if _, touched := byName["chosen"]; touched {
		t.Fatal("已有选择的载体不得被迁移处理")
	}
	if _, touched := byName["other-machn"]; touched {
		t.Fatal("他机载体不得被本机迁移处理")
	}
	if c, _ := svc.Carrier("r1-abs"); c.HomeDir != filepath.Join(mainAbs, ".handoff/home/x") {
		t.Fatalf("迁移不改 home_dir 串：%q", c.HomeDir)
	}
	// 幂等：再跑一轮，已迁移的不再处理，未选的结论不变。
	again, err := svc.MigrateCarrierLogins(logins, isSelf, expand)
	if err != nil || len(again) != 2 {
		t.Fatalf("第二轮只应再看两个未选载体：%+v %v", again, err)
	}
}

func TestMigrateCarrierLoginsModelPrefixAndGone(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	mainAbs := t.TempDir()
	if _, err := acc.Reconcile(accounts.Discovery{Machine: "m1", At: time.Now(), Homes: []accounts.ScannedHome{{
		Home: "~", HomeAbs: mainAbs, Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:111111111111"},
			{Kind: accounts.KindAPIKey, Provider: "deepseek", Source: "auth.json", Fingerprint: "sha256:555555555555"},
		}}}}); err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]string{"by-og": "opencode-go/kimi", "by-provider": "deepseek/chat", "free": "opencode/x-free"} {
		body := `{"name":"` + name + `","machine":"m1","cli":"opencode","home_dir":"","model":"` + model + `"}`
		if _, err := facade.Put("carrier", name, 0, []byte(body), "legacy"); err != nil {
			t.Fatal(err)
		}
	}
	logins, _ := acc.Logins()
	expand := func(string) (string, error) { return mainAbs, nil }
	if _, err := svc.MigrateCarrierLogins(logins, func(string) bool { return true }, expand); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Carrier("by-og"); c.Login != ogLogin("m1", "~", "opencode.db") {
		t.Fatalf("opencode-go/ 前缀应选 opencode-go 登录：%+v", c)
	}
	if c, _ := svc.Carrier("by-provider"); c.Login != accounts.LoginID("m1", "~", accounts.KindAPIKey, "deepseek", "auth.json") {
		t.Fatalf("<provider>/ 前缀应选该 provider 的 key 登录：%+v", c)
	}
	if c, _ := svc.Carrier("free"); c.LoginChosen() {
		t.Fatalf("免费模型前缀不唯一，应留作未选（不落规则 3：主 HOME 有兼容登录）：%+v", c)
	}

	// 已失效登录不参与匹配：主 HOME 只剩失效登录 → 规则 3。
	if _, err := acc.Reconcile(accounts.Discovery{Machine: "m1", At: time.Now(), Homes: []accounts.ScannedHome{{Home: "~", HomeAbs: mainAbs}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Put("carrier", "late", 0, []byte(`{"name":"late","machine":"m1","cli":"opencode","home_dir":""}`), "legacy"); err != nil {
		t.Fatal(err)
	}
	logins, _ = acc.Logins()
	if _, err := svc.MigrateCarrierLogins(logins, func(string) bool { return true }, expand); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Carrier("late"); !c.NoAccount {
		t.Fatalf("主 HOME 只有已失效登录应迁为无账号：%+v", c)
	}
}

// TestAdmissionReadsLoginThenAccount 锁准入改读 载体 → 登录 → 账号：账号无额度挡；登录行被删
// （与载体选择并发）按未选放行。
func TestAdmissionReadsLoginThenAccount(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	seedLogins(t, acc, "m1", "/home/u", map[string]map[string]string{"~": {"opencode.db": "sha256:111111111111"}})
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "oc", Machine: "m1", CLI: "opencode", Login: ogLogin("m1", "~", "opencode.db")})
	if err := acc.MarkQuota("opencode-go:111111111111", accounts.QuotaMark{At: time.Now(), Carrier: "oc", Category: "quota_exhausted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdmitCarrier("oc"); !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "账号 opencode-go-1111 无额度") {
		t.Fatalf("所选登录的账号无额度应拒并点名账号名，得 %v", err)
	}
	rec, err := facade.Get(accounts.KindLogin, ogLogin("m1", "~", "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := facade.Delete(accounts.KindLogin, rec.ID, rec.Version, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdmitCarrier("oc"); err != nil {
		t.Fatalf("登录行不存在按未选放行，得 %v", err)
	}
	refs, err := svc.LoginReferrers()
	if err != nil || len(refs[ogLogin("m1", "~", "opencode.db")]) != 1 {
		t.Fatalf("登录引用查询：%+v %v", refs, err)
	}
}
