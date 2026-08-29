package scheduling_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

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
