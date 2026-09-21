package scheduling_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestOccupancyCarrierKeyGolden(t *testing.T) {
	if got := scheduling.OccupancyCarrierKey("muse"); got != "carrier/muse" {
		t.Fatalf("OccupancyCarrierKey(muse) = %q, want carrier/muse", got)
	}
	if got := scheduling.OccupancyCarrierKey("c1"); got != "carrier/c1" {
		t.Fatalf("OccupancyCarrierKey(c1) = %q, want carrier/c1", got)
	}
	if got := scheduling.OccupancyCarrierKey(""); got != "" {
		t.Fatalf("空载体名必须得到空键，实得 %q", got)
	}
}

func TestOccupancyMemberKeyGolden(t *testing.T) {
	if got := scheduling.OccupancyMemberKey("exec", "muse"); got != "squad/exec/muse" {
		t.Fatalf("OccupancyMemberKey(exec,muse) = %q, want squad/exec/muse", got)
	}
	if got := scheduling.OccupancyMemberKey("sq1", "c1"); got != "squad/sq1/c1" {
		t.Fatalf("OccupancyMemberKey(sq1,c1) = %q, want squad/sq1/c1", got)
	}
	if got := scheduling.OccupancyMemberKey("", "muse"); got != "" {
		t.Fatalf("空小队不得写出成员键，实得 %q", got)
	}
	if got := scheduling.OccupancyMemberKey("exec", ""); got != "" {
		t.Fatalf("空载体不得写出成员键，实得 %q", got)
	}
}

func TestOccupancyKeysCarrierOnly(t *testing.T) {
	member, carrier := scheduling.OccupancyKeys("", "muse")
	if member != "" {
		t.Fatalf("载体直派成员键必须空，实得 %q", member)
	}
	if carrier != "carrier/muse" {
		t.Fatalf("载体直派载体键 = %q, want carrier/muse", carrier)
	}
}

func TestOccupancyKeysSquadAndCarrier(t *testing.T) {
	member, carrier := scheduling.OccupancyKeys("exec", "muse")
	if member != "squad/exec/muse" {
		t.Fatalf("成员键 = %q, want squad/exec/muse", member)
	}
	if carrier != "carrier/muse" {
		t.Fatalf("载体键 = %q, want carrier/muse", carrier)
	}
}
