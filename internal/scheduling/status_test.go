package scheduling_test

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestPutCarrierLifecyclePreservesOrResetsStatus(t *testing.T) {
	svc, _ := newRowsFixture(t)
	carrier := scheduling.Carrier{Name: "c1", Machine: "m1", CLI: "opencode",
		HomeDir: "~/.handoff/home/c1", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline, LastError: "caller must not set state"}
	if err := svc.PutCarrier(carrier, 0); err != nil {
		t.Fatalf("新建载体: %v", err)
	}
	got, err := svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读新建载体: %v", err)
	}
	if got.Status != scheduling.StatusPending || got.LastError != "" {
		t.Fatalf("新建状态 = %q/%q，want pending/empty", got.Status, got.LastError)
	}

	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("检测上线: %v", err)
	}
	got, err = svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读上线载体: %v", err)
	}
	if got.Status != scheduling.StatusOnline || got.LastError != "" {
		t.Fatalf("上线状态 = %q/%q，want online/empty", got.Status, got.LastError)
	}

	if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{NeedLogin: true, Reachable: true}, "需要登录"); err != nil {
		t.Fatalf("检测需登录: %v", err)
	}
	got, err = svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读需登录载体: %v", err)
	}
	if got.Status != scheduling.StatusPending || got.LastError != "需要登录" {
		t.Fatalf("需登录状态 = %q/%q，want pending/需要登录", got.Status, got.LastError)
	}

	carrier.Status = scheduling.StatusOnline
	carrier.LastError = "input ignored"
	if err := svc.PutCarrier(carrier, 3); err != nil {
		t.Fatalf("HOME 未变更新: %v", err)
	}
	got, err = svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读 HOME 未变载体: %v", err)
	}
	if got.Status != scheduling.StatusPending || got.LastError != "需要登录" {
		t.Fatalf("HOME 未变不应覆盖旧状态 = %q/%q", got.Status, got.LastError)
	}

	carrier.HomeDir = "~/.handoff/home/c1-new"
	if err := svc.PutCarrier(carrier, 4); err != nil {
		t.Fatalf("HOME 变化更新: %v", err)
	}
	got, err = svc.Carrier("c1")
	if err != nil {
		t.Fatalf("读 HOME 变化载体: %v", err)
	}
	if got.Status != scheduling.StatusPending || got.LastError != "" {
		t.Fatalf("HOME 变化应重置状态 = %q/%q，want pending/empty", got.Status, got.LastError)
	}
}

func TestApplyDetectUsesPriorityAndPreviousReachability(t *testing.T) {
	cases := []struct {
		name     string
		previous scheduling.CarrierStatus
		evidence scheduling.DetectEvidence
		detail   string
		want     scheduling.CarrierStatus
		wantErr  string
	}{
		{"pending unreachable", scheduling.StatusPending, scheduling.DetectEvidence{}, "offline", scheduling.StatusPending, "offline"},
		{"online unreachable", scheduling.StatusOnline, scheduling.DetectEvidence{}, "offline", scheduling.StatusUnreachable, "offline"},
		{"quota unreachable", scheduling.StatusQuota, scheduling.DetectEvidence{}, "offline", scheduling.StatusUnreachable, "offline"},
		{"unreachable remains", scheduling.StatusUnreachable, scheduling.DetectEvidence{}, "offline", scheduling.StatusUnreachable, "offline"},
		{"quota beats login", scheduling.StatusPending, scheduling.DetectEvidence{Reachable: true, Quota: true, NeedLogin: true}, "quota", scheduling.StatusQuota, "quota"},
		{"login", scheduling.StatusPending, scheduling.DetectEvidence{Reachable: true, NeedLogin: true}, "login", scheduling.StatusPending, "login"},
		{"online clears detail", scheduling.StatusPending, scheduling.DetectEvidence{Reachable: true}, "stale", scheduling.StatusOnline, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newRowsFixture(t)
			carrier := scheduling.Carrier{Name: "c1", Machine: "m1", CLI: "opencode",
				Credential: scheduling.CredentialStandalone}
			if err := svc.PutCarrier(carrier, 0); err != nil {
				t.Fatalf("登记载体: %v", err)
			}
			if tc.previous != scheduling.StatusPending {
				if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
					t.Fatalf("建立前置 online: %v", err)
				}
				if tc.previous == scheduling.StatusQuota {
					if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{Reachable: true, Quota: true}, ""); err != nil {
						t.Fatalf("建立前置 quota: %v", err)
					}
				}
				if tc.previous == scheduling.StatusUnreachable {
					if _, err := svc.ApplyDetect("c1", scheduling.DetectEvidence{}, ""); err != nil {
						t.Fatalf("建立前置 unreachable: %v", err)
					}
				}
			}
			got, err := svc.ApplyDetect("c1", tc.evidence, tc.detail)
			if err != nil {
				t.Fatalf("ApplyDetect: %v", err)
			}
			if got.Status != tc.want || got.LastError != tc.wantErr {
				t.Fatalf("结果 = %q/%q，want %q/%q", got.Status, got.LastError, tc.want, tc.wantErr)
			}
		})
	}
}

func TestAdmissionRequiresOnlineCarrierAndSeparatesNoHealthyFromNoSlot(t *testing.T) {
	svc, _ := newRowsFixture(t)
	for _, name := range []string{"pending", "quota", "unreachable", "online"} {
		if err := svc.PutCarrier(scheduling.Carrier{Name: name, Machine: name + "-machine", CLI: "opencode",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1}, 0); err != nil {
			t.Fatalf("登记 %s: %v", name, err)
		}
	}
	if _, err := svc.ApplyDetect("quota", scheduling.DetectEvidence{Reachable: true, Quota: true}, "quota"); err != nil {
		t.Fatalf("设置 quota: %v", err)
	}
	if _, err := svc.ApplyDetect("unreachable", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("设置 unreachable 前置: %v", err)
	}
	if _, err := svc.ApplyDetect("unreachable", scheduling.DetectEvidence{}, "offline"); err != nil {
		t.Fatalf("设置 unreachable: %v", err)
	}
	if _, err := svc.ApplyDetect("online", scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("设置 online: %v", err)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "exec", Role: scheduling.RoleExecutor,
		Members: []string{"pending", "quota", "unreachable", "online"}}, 0); err != nil {
		t.Fatalf("登记执行者小队: %v", err)
	}
	got, err := svc.Admit(scheduling.IgnitionRequest{Card: "B293", Squad: "exec", Actor: "test"})
	if err != nil || got.Carrier != "online" {
		t.Fatalf("准入 = %+v/%v，want online/nil", got, err)
	}
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "B294", Squad: "exec", Actor: "test"}); !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("online 满员应 ErrNoSlot，得 %v", err)
	}

	if err := svc.PutSquad(scheduling.Squad{Name: "none", Role: scheduling.RoleExecutor,
		Members: []string{"pending", "quota", "unreachable"}}, 0); err != nil {
		t.Fatalf("登记全非 online 小队: %v", err)
	}
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "B295", Squad: "none", Actor: "test"}); !errors.Is(err, scheduling.ErrNoHealthy) {
		t.Fatalf("全非 online 应 ErrNoHealthy，得 %v", err)
	}

	if err := svc.Release("exec", "online"); err != nil {
		t.Fatalf("释放执行者准入: %v", err)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []string{"online"}}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	if got, err := svc.LaunchAdmit("coord"); err != nil || got.Carrier != "online" {
		t.Fatalf("协调者准入 = %+v/%v，want online/nil", got, err)
	}
}

func TestCarrierStatusLabels(t *testing.T) {
	cases := []struct {
		status scheduling.CarrierStatus
		label  string
	}{
		{scheduling.StatusPending, "未上线"},
		{scheduling.StatusOnline, "已上线"},
		{scheduling.StatusQuota, "限额中"},
		{scheduling.StatusUnreachable, "不可达"},
	}
	for _, c := range cases {
		if got := c.status.Label(); got != c.label {
			t.Fatalf("status %q Label = %q, want %q", c.status, got, c.label)
		}
	}
}

func TestDefaultHomeDir(t *testing.T) {
	if got := scheduling.DefaultHomeDir("mbp-opencode"); got != "~/.handoff/home/mbp-opencode" {
		t.Fatalf("DefaultHomeDir = %q", got)
	}
	if got := scheduling.DefaultHomeDir("  "); got != "" {
		t.Fatalf("空白名字应得空串, got %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	got := scheduling.RunCommand(scheduling.Carrier{
		HomeDir: "~/.handoff/home/x", CLI: "codex",
	})
	if got != "HOME=~/.handoff/home/x codex" {
		t.Fatalf("RunCommand = %q", got)
	}
}
