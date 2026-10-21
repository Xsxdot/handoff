// b453_admission_test.go —— B453 段 3 编制域回归：「限额中」退役与读时归一（Q12/Q19）、
// 载体账号引用（V7）、统一准入判据与 S5 点名文案（V10）。
//
// 职责：经真实账本门面（facadeRegistry）把载体、小队、账号定义与读数行落盘，锁五个准入
// 入口（AdmitCarrier、SelectWhere、AdmitFrozen、AdmitSeatCarrier、admitInto 经 Admit/
// LaunchAdmit）都经同一判据：账号低额度/无额度 → 视同不可用；未探测/探测失败 → 放行。
// 边界：账号读数直接经 accounts.RecordProbe/MarkQuota 构造，不走探测器。
package scheduling_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// newAccountFixture 开临时账本，编制域与账号域共用同一 registry 端口（与组装点一致）。
func newAccountFixture(t *testing.T) (*scheduling.Service, *accounts.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "b453.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	repo := facadeRegistry{f: facade}
	svc := scheduling.New(repo)
	acc := accounts.New(repo)
	svc.SetAccounts(acc)
	return svc, acc, facade
}

func putAccount(t *testing.T, acc *accounts.Service, name, machine string) {
	t.Helper()
	if _, err := acc.Put(accounts.Account{Name: name, Kind: accounts.KindCodex, Machine: machine,
		HomeDir: "/home/" + name, IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80}, 0); err != nil {
		t.Fatalf("登记账号 %s: %v", name, err)
	}
}

// setUsage 写一次成功读数：一个窗口已用 used%，resetIn 后重置（0 = 不给重置时刻）。
func setUsage(t *testing.T, acc *accounts.Service, name string, used float64, resetIn time.Duration) {
	t.Helper()
	now := time.Now()
	w := accounts.Window{Key: "session", UsedPercent: used}
	if resetIn > 0 {
		r := now.Add(resetIn)
		w.ResetsAt = &r
	}
	if err := acc.RecordProbe(name, accounts.ProbeOutcome{StartedAt: now, FinishedAt: now, Writer: "test",
		Reading: &accounts.Reading{ProbedAt: now, Windows: []accounts.Window{w}}}); err != nil {
		t.Fatalf("写账号 %s 读数: %v", name, err)
	}
}

func setProbeFailed(t *testing.T, acc *accounts.Service, name string) {
	t.Helper()
	now := time.Now()
	if err := acc.RecordProbe(name, accounts.ProbeOutcome{StartedAt: now, FinishedAt: now, Writer: "test",
		Failure: &accounts.Problem{Category: "network", Message: "断网"}}); err != nil {
		t.Fatalf("写账号 %s 失败: %v", name, err)
	}
}

// boundCarrier 登记一个绑定账号的在线载体（同机账号）。
func boundCarrier(t *testing.T, svc *scheduling.Service, name, machine, account string) {
	t.Helper()
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: name, Machine: machine, CLI: "codex",
		HomeDir: "/home/" + name, Credential: scheduling.CredentialStandalone, Account: account})
}

var recoverRE = regexp.MustCompile(`预计 \d+h\d+m 后恢复`)

func TestLegacyQuotaStatusReadsAsOnline(t *testing.T) {
	svc, _, facade := newAccountFixture(t)
	body := `{"name":"old","machine":"m1","cli":"opencode","home_dir":"","credential":"standalone","status":"quota","last_error":"rate limited"}`
	if _, err := facade.Put("carrier", "old", 0, []byte(body), "legacy-agentd"); err != nil {
		t.Fatalf("手写存量 quota 载体: %v", err)
	}
	c, err := svc.Carrier("old")
	if err != nil {
		t.Fatalf("读存量载体: %v", err)
	}
	if c.Status != scheduling.StatusOnline {
		t.Fatalf("存量 status=quota 读出 %q，want online（读时归一）", c.Status)
	}
	rows, err := svc.CarrierRows()
	if err != nil || len(rows) != 1 || rows[0].Carrier.Status != scheduling.StatusOnline {
		t.Fatalf("CarrierRows 读出 %+v/%v，want online", rows, err)
	}
	if _, err := svc.AdmitCarrier("old"); err != nil {
		t.Fatalf("存量 quota 载体读作已上线后应可直派: %v", err)
	}
}

func TestLegacyDetectQuotaWritesOnlineAndMarksAccount(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	boundCarrier(t, svc, "bound", "m1", "cx")
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "free", Machine: "m1", CLI: "codex",
		Credential: scheduling.CredentialStandalone})

	got, err := svc.ApplyDetect("bound", scheduling.DetectEvidence{Reachable: true, Quota: true,
		Fingerprint: "sha256:aaaaaaaaaaaa"}, "You've hit your usage limit")
	if err != nil {
		t.Fatalf("绑定账号的载体检测撞额度: %v", err)
	}
	if got.Status != scheduling.StatusOnline || got.LastError != "" || got.CredFingerprint != "sha256:aaaaaaaaaaaa" {
		t.Fatalf("绑定账号：载体 = %q/%q/%q，want online/空/指纹写回", got.Status, got.LastError, got.CredFingerprint)
	}
	a, err := acc.Assess("cx", time.Now())
	if err != nil || a.State != accounts.StateOut {
		t.Fatalf("绑定账号应被标无额度，得 %+v/%v", a, err)
	}
	if raw := string(mustBody(t, facade, "carrier", "bound")); strings.Contains(raw, `"quota"`) {
		t.Fatalf("载体 body 不得再写 quota：%s", raw)
	}

	got, err = svc.ApplyDetect("free", scheduling.DetectEvidence{Reachable: true, Quota: true}, "insufficient_quota")
	if err != nil {
		t.Fatalf("未绑定账号的载体检测撞额度: %v", err)
	}
	if got.Status != scheduling.StatusOnline || got.LastError != "insufficient_quota" {
		t.Fatalf("未绑定账号：载体 = %q/%q，want online/额度原因进最近错误", got.Status, got.LastError)
	}
}

func TestAdmitPutCarrierAccountReference(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	putAccount(t, acc, "cx-m1", "m1")
	base := scheduling.Carrier{Name: "c1", Machine: "m2", CLI: "codex", HomeDir: "/h/c1",
		Credential: scheduling.CredentialStandalone}

	other := base
	other.Account = "cx-m1"
	if err := svc.PutCarrier(other, 0); !errors.Is(err, scheduling.ErrInvalid) || !strings.Contains(err.Error(), "cx-m1") {
		t.Fatalf("选他机账号应 ErrInvalid 且点名账号，得 %v", err)
	}
	missing := base
	missing.Account = "ghost"
	if err := svc.PutCarrier(missing, 0); !errors.Is(err, scheduling.ErrInvalid) || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("选不存在账号应 ErrInvalid 且点名账号，得 %v", err)
	}
	if err := svc.PutCarrier(base, 0); err != nil {
		t.Fatalf("不选账号合法: %v", err)
	}

	same := base
	same.Machine, same.Account = "m1", "cx-m1"
	if err := svc.PutCarrier(same, 1); err != nil {
		t.Fatalf("选同机账号: %v", err)
	}
	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true, Fingerprint: "sha256:111111111111"}, ""); err != nil {
		t.Fatalf("检测写指纹: %v", err)
	}
	edit := same
	edit.MaxConcurrency, edit.CredFingerprint = 3, "caller-must-not-set"
	if err := svc.PutCarrier(edit, 3); err != nil {
		t.Fatalf("改并发位: %v", err)
	}
	c, _ := svc.Carrier("c1")
	if c.Account != "cx-m1" || c.CredFingerprint != "sha256:111111111111" {
		t.Fatalf("编辑无关字段后 account/指纹 = %q/%q，want 保留", c.Account, c.CredFingerprint)
	}
	edit.Account = ""
	if err := svc.PutCarrier(edit, 4); err != nil {
		t.Fatalf("取消账号: %v", err)
	}
	c, _ = svc.Carrier("c1")
	if c.Account != "" || c.CredFingerprint != "" {
		t.Fatalf("换账号后 account/指纹 = %q/%q，want 空（旧指纹不再对应所选账号）", c.Account, c.CredFingerprint)
	}
	if raw := string(mustBody(t, facade, "carrier", "c1")); strings.Contains(raw, `"account"`) || strings.Contains(raw, `"cred_fingerprint"`) {
		t.Fatalf("空 account/指纹应以键缺席表达：%s", raw)
	}
}

func TestAdmitCarrierSkipsBlockedAccountWithNamedReason(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*testing.T, *accounts.Service)
		blocked bool
		want    []string
	}{
		{"未探测放行", func(*testing.T, *accounts.Service) {}, false, nil},
		{"探测失败放行", func(t *testing.T, a *accounts.Service) { setProbeFailed(t, a, "cx") }, false, nil},
		{"正常放行", func(t *testing.T, a *accounts.Service) { setUsage(t, a, "cx", 30, time.Hour) }, false, nil},
		{"低额度拒", func(t *testing.T, a *accounts.Service) { setUsage(t, a, "cx", 85, 2*time.Hour+41*time.Minute) },
			true, []string{"c1", "账号 cx 低额度"}},
		{"无额度拒", func(t *testing.T, a *accounts.Service) { setUsage(t, a, "cx", 100, 2*time.Hour+41*time.Minute) },
			true, []string{"c1", "账号 cx 无额度"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, acc, _ := newAccountFixture(t)
			putAccount(t, acc, "cx", "m1")
			boundCarrier(t, svc, "c1", "m1", "cx")
			tc.setup(t, acc)
			_, err := svc.AdmitCarrier("c1")
			if !tc.blocked {
				if err != nil {
					t.Fatalf("应放行，得 %v", err)
				}
				return
			}
			if !errors.Is(err, scheduling.ErrNoHealthy) {
				t.Fatalf("应 ErrNoHealthy（调用方 HTTP 映射不变），得 %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("文案缺 %q：%v", w, err)
				}
			}
			if !recoverRE.MatchString(err.Error()) {
				t.Fatalf("文案缺「预计 XhYm 后恢复」：%v", err)
			}
		})
	}
}

func TestSelectWhereAndAdmitSkipBlockedMemberAndNameAll(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	putAccount(t, acc, "acc-a", "m1")
	putAccount(t, acc, "acc-b", "m2")
	boundCarrier(t, svc, "A", "m1", "acc-a")
	boundCarrier(t, svc, "B", "m2", "acc-b")
	if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "A"}, {Carrier: "B"}}}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	setUsage(t, acc, "acc-a", 90, 3*time.Hour)

	for i := 0; i < 3; i++ {
		b, err := svc.SelectWhere(scheduling.IgnitionRequest{Squad: "exec", Actor: "test"}, nil)
		if err != nil || b.Carrier != "B" {
			t.Fatalf("第 %d 次选择 = %+v/%v，want 落 B（A 低额度被跳过）", i+1, b, err)
		}
	}
	b, err := svc.Admit(scheduling.IgnitionRequest{Card: "B453", Squad: "exec", Actor: "test"})
	if err != nil || b.Carrier != "B" {
		t.Fatalf("Admit = %+v/%v，want B", b, err)
	}
	if err := svc.Release("exec", "B"); err != nil {
		t.Fatalf("释放: %v", err)
	}

	if err := acc.MarkQuota("acc-b", accounts.QuotaMark{At: time.Now(), Carrier: "B", Machine: "m2", Category: "quota_exhausted"}); err != nil {
		t.Fatalf("标 acc-b 无额度: %v", err)
	}
	_, selErr := svc.SelectWhere(scheduling.IgnitionRequest{Squad: "exec", Actor: "test"}, nil)
	_, admErr := svc.Admit(scheduling.IgnitionRequest{Card: "B453", Squad: "exec", Actor: "test"})
	for label, err := range map[string]error{"SelectWhere": selErr, "Admit": admErr} {
		if !errors.Is(err, scheduling.ErrNoHealthy) {
			t.Fatalf("%s 全员被跳过应 ErrNoHealthy，得 %v", label, err)
		}
		msg := err.Error()
		for _, w := range []string{"A：账号 acc-a 低额度", "B：账号 acc-b 无额度", "恢复时刻未知"} {
			if !strings.Contains(msg, w) {
				t.Fatalf("%s 文案缺 %q：%s", label, w, msg)
			}
		}
		if !recoverRE.MatchString(msg) {
			t.Fatalf("%s 文案缺 A 的预计恢复：%s", label, msg)
		}
	}
}

func TestAdmitFrozenRejectsBlockedAccount(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	boundCarrier(t, svc, "c1", "m1", "cx")
	frozen := scheduling.Binding{Carrier: "c1", Target: "m1", Executor: "codex", HomeDir: "/home/c1"}
	if _, err := svc.AdmitFrozen(frozen); err != nil {
		t.Fatalf("未探测账号应放行: %v", err)
	}
	if err := svc.Release("", "c1"); err != nil {
		t.Fatalf("释放: %v", err)
	}
	setUsage(t, acc, "cx", 100, time.Hour)
	_, err := svc.AdmitFrozen(frozen)
	if !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "账号 cx 无额度") {
		t.Fatalf("冻结准入应因账号无额度拒，得 %v", err)
	}
}

func TestAdmitSeatCarrierAndLaunchAdmitRejectBlockedAccount(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	boundCarrier(t, svc, "c1", "m1", "cx")
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1"}}}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	setProbeFailed(t, acc, "cx")
	if _, err := svc.AdmitSeatCarrier("coord", "c1"); err != nil {
		t.Fatalf("探测失败应放行: %v", err)
	}
	if err := svc.Release("coord", "c1"); err != nil {
		t.Fatalf("释放: %v", err)
	}
	setUsage(t, acc, "cx", 95, time.Hour)
	if _, err := svc.AdmitSeatCarrier("coord", "c1"); !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "账号 cx 低额度") {
		t.Fatalf("AdmitSeatCarrier 应因账号低额度拒，得 %v", err)
	}
	if _, err := svc.LaunchAdmit("coord"); !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "账号 cx 低额度") {
		t.Fatalf("LaunchAdmit（admitInto）应因账号低额度拒，得 %v", err)
	}
}

func TestAdmitUnknownAccountTreatedAsUnbound(t *testing.T) {
	svc, acc, facade := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	boundCarrier(t, svc, "c1", "m1", "cx")
	e, err := facade.Get("account", "cx")
	if err != nil {
		t.Fatalf("读账号行: %v", err)
	}
	if err := facade.Delete("account", "cx", e.Version, "test"); err != nil {
		t.Fatalf("直删账号行（模拟删除与载体选该账号并发）: %v", err)
	}
	if _, err := svc.AdmitCarrier("c1"); err != nil {
		t.Fatalf("引用不存在账号按未选账号处理，应放行: %v", err)
	}
}

func TestCheckUsableNamesStatusAndAccount(t *testing.T) {
	svc, acc, _ := newAccountFixture(t)
	putAccount(t, acc, "cx", "m1")
	boundCarrier(t, svc, "c1", "m1", "cx")
	c, _ := svc.Carrier("c1")
	if err := svc.CheckUsable(c); err != nil {
		t.Fatalf("可用载体 CheckUsable = %v", err)
	}
	setUsage(t, acc, "cx", 100, 0)
	if err := svc.CheckUsable(c); !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "c1：账号 cx 无额度，恢复时刻未知") {
		t.Fatalf("CheckUsable 应点名账号无额度且恢复未知，得 %v", err)
	}
	c.Status = scheduling.StatusUnreachable
	if err := svc.CheckUsable(c); !errors.Is(err, scheduling.ErrNoHealthy) || !strings.Contains(err.Error(), "c1：载体不可达") {
		t.Fatalf("CheckUsable 应点名载体状态，得 %v", err)
	}
}

// TestAdmitSingleCriterionStructural 是 V10 的结构断言：编制包非测试源码里，「载体是否
// 已上线」的准入比较只允许出现在统一判据 admission.go 一处，防有人在某个入口另写一份。
func TestAdmitSingleCriterionStructural(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列源码: %v", err)
	}
	re := regexp.MustCompile(`Status\s*!=\s*StatusOnline`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "admission.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s: %v", f, err)
		}
		if loc := re.FindIndex(src); loc != nil {
			t.Fatalf("%s 里有直接比较 Status != StatusOnline 的准入代码（应经统一判据 admission.go）", f)
		}
	}
}
