package accounts_test

// derive_test.go —— 五档推导与「预计恢复」的表驱动回归（B453 段 3，plan Q4/V6）。
//
// 职责：锁优先级「撞额度标记 > 探测失败 > 未探测 > 读数推导」、低额度线按账号阈值、
// 已过重置时刻的窗口按 0% 计、预计恢复取阻挡窗口全部重置的时刻；以及 Assess 读面。

import (
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func ptrTime(t time.Time) *time.Time { return &t }

func TestDeriveFiveStatesWithPriority(t *testing.T) {
	now := at(1000)
	future1, future2, past := ptrTime(now.Add(2*time.Hour)), ptrTime(now.Add(5*time.Hour)), ptrTime(now.Add(-time.Minute))
	reading := func(ws ...accounts.Window) *accounts.Reading {
		return &accounts.Reading{ProbedAt: now, Windows: ws}
	}
	fail := &accounts.Problem{Category: "network", Message: "断网"}
	mark := &accounts.QuotaMark{At: now, Carrier: "mini"}
	a := newAccount() // 预警 60 / 低额度 80
	cases := []struct {
		name    string
		rec     accounts.ReadingRecord
		want    accounts.State
		recover *time.Time
	}{
		{"从无尝试 → 未探测", accounts.ReadingRecord{}, accounts.StateNone, nil},
		{"只有失败 → 探测失败", accounts.ReadingRecord{LastError: fail}, accounts.StateFail, nil},
		{"失败晚于成功 → 探测失败（旧读数 100% 也不挡）", accounts.ReadingRecord{
			LastError: fail, Reading: reading(accounts.Window{Key: "weekly", UsedPercent: 100, ResetsAt: future1})},
			accounts.StateFail, nil},
		{"标记优先于失败 → 无额度、恢复未知", accounts.ReadingRecord{QuotaMark: mark, LastError: fail}, accounts.StateOut, nil},
		{"标记优先于正常读数", accounts.ReadingRecord{QuotaMark: mark, Reading: reading(accounts.Window{Key: "session", UsedPercent: 3})},
			accounts.StateOut, nil},
		{"低于预警 → 正常", accounts.ReadingRecord{Reading: reading(accounts.Window{Key: "session", UsedPercent: 30, ResetsAt: future1})},
			accounts.StateOK, nil},
		{"预警线只改颜色 → 仍正常", accounts.ReadingRecord{Reading: reading(accounts.Window{Key: "session", UsedPercent: 79.9, ResetsAt: future1})},
			accounts.StateOK, nil},
		{"到低额度线 → 低额度，恢复 = 该窗口重置", accounts.ReadingRecord{Reading: reading(
			accounts.Window{Key: "session", UsedPercent: 80, ResetsAt: future1},
			accounts.Window{Key: "weekly", UsedPercent: 20, ResetsAt: future2})},
			accounts.StateLow, future1},
		{"任一窗口 100 → 无额度，恢复 = 阻挡窗口里最晚的重置", accounts.ReadingRecord{Reading: reading(
			accounts.Window{Key: "session", UsedPercent: 100, ResetsAt: future1},
			accounts.Window{Key: "weekly", UsedPercent: 90, ResetsAt: future2})},
			accounts.StateOut, future2},
		{"阻挡窗口缺重置时刻 → 恢复未知", accounts.ReadingRecord{Reading: reading(
			accounts.Window{Key: "session", UsedPercent: 100, ResetsAt: future1},
			accounts.Window{Key: "monthly", UsedPercent: 85})},
			accounts.StateOut, nil},
		{"已过重置时刻的窗口按 0% 计", accounts.ReadingRecord{Reading: reading(
			accounts.Window{Key: "session", UsedPercent: 100, ResetsAt: past})},
			accounts.StateOK, nil},
		{"接口明示不可用 → 无额度、恢复未知", accounts.ReadingRecord{Reading: &accounts.Reading{ProbedAt: now,
			Blocked: &accounts.Problem{Category: "no_subscription", Message: "无订阅"}}},
			accounts.StateOut, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accounts.Derive(a, tc.rec, now); got != tc.want {
				t.Fatalf("Derive = %q，want %q", got, tc.want)
			}
			got := accounts.RecoverAt(a, tc.rec, now)
			switch {
			case tc.recover == nil && got != nil:
				t.Fatalf("RecoverAt = %v，want nil（恢复时刻未知）", *got)
			case tc.recover != nil && (got == nil || !got.Equal(*tc.recover)):
				t.Fatalf("RecoverAt = %v，want %v", got, *tc.recover)
			}
		})
	}
}

func TestDeriveUsesAccountLowPercent(t *testing.T) {
	now := at(1000)
	rec := accounts.ReadingRecord{Reading: &accounts.Reading{ProbedAt: now, Windows: []accounts.Window{
		{Key: "weekly", UsedPercent: 50, ResetsAt: ptrTime(now.Add(time.Hour))}}}}
	a := newAccount()
	if got := accounts.Derive(a, rec, now); got != accounts.StateOK {
		t.Fatalf("低额度线 80、已用 50 → %q，want ok", got)
	}
	a.WarnPercent, a.LowPercent = 10, 40
	if got := accounts.Derive(a, rec, now); got != accounts.StateLow {
		t.Fatalf("低额度线调到 40、已用 50 → %q，want low（R3 场景）", got)
	}
}

func TestStateBlocksAndLabels(t *testing.T) {
	cases := map[accounts.State]struct {
		blocks bool
		label  string
	}{
		accounts.StateOK: {false, "正常"}, accounts.StateLow: {true, "低额度"}, accounts.StateOut: {true, "无额度"},
		accounts.StateFail: {false, "探测失败"}, accounts.StateNone: {false, "未探测"},
	}
	for st, want := range cases {
		if st.Blocks() != want.blocks || st.Label() != want.label {
			t.Fatalf("%q: Blocks=%v Label=%q，want %v/%q", st, st.Blocks(), st.Label(), want.blocks, want.label)
		}
	}
}

func TestAssessReadsDefinitionAndReading(t *testing.T) {
	repo := newMem()
	svc := accounts.New(repo)
	if _, err := svc.Assess("nope", at(0)); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("不存在的账号应 ErrNotFound，得 %v", err)
	}
	if _, err := svc.Put(newAccount(), 0); err != nil {
		t.Fatalf("登记: %v", err)
	}
	reset := at(0).Add(3 * time.Hour)
	o := successAt(10, 20, 100)
	o.Reading.Windows[0].ResetsAt = &reset
	if err := svc.RecordProbe("og-linux", o); err != nil {
		t.Fatalf("写读数: %v", err)
	}
	got, err := svc.Assess("og-linux", at(30))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Account.Name != "og-linux" || got.State != accounts.StateOut || got.RecoverAt == nil || !got.RecoverAt.Equal(reset) {
		t.Fatalf("Assess = %+v，want og-linux/out/恢复 %v", got, reset)
	}
}

func TestQuotaHitWindows(t *testing.T) {
	now := time.Now()
	r1, r2 := now.Add(time.Hour), now.Add(3*time.Hour)
	a := accounts.Account{LowPercent: 80}
	rec := accounts.ReadingRecord{Reading: &accounts.Reading{Windows: []accounts.Window{
		{Key: "session", UsedPercent: 100, ResetsAt: &r1},
		{Key: "weekly", UsedPercent: 85, ResetsAt: &r2},
		{Key: "monthly", UsedPercent: 10},
	}}}
	hit, at := accounts.QuotaHitWindows(a, rec, now)
	if len(hit) != 2 || at == nil || !at.Equal(r2) {
		t.Fatalf("越线窗口两个、恢复取最晚：%+v %v", hit, at)
	}
	if accounts.WindowLabel(hit[0]) != "5h" || accounts.WindowLabel(accounts.Window{Key: "weekly", Group: "Gemini"}) != "Gemini 周" {
		t.Fatalf("窗口短名不符")
	}
	if hit, at := accounts.QuotaHitWindows(a, accounts.ReadingRecord{}, now); hit != nil || at != nil {
		t.Fatalf("无读数应皆空")
	}
	low := accounts.ReadingRecord{Reading: &accounts.Reading{Windows: []accounts.Window{{Key: "session", UsedPercent: 30, ResetsAt: &r1}}}}
	if hit, at := accounts.QuotaHitWindows(a, low, now); hit != nil || at != nil {
		t.Fatalf("读数未越线应皆空（读数滞后）")
	}
}
