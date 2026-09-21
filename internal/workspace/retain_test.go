package workspace_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/workspace"
)

func TestMayRecycle(t *testing.T) {
	tests := []struct {
		name string
		in   workspace.RecycleInput
		want workspace.Decision
	}{
		{
			name: "user tree never recycled on done",
			in:   workspace.RecycleInput{Managed: false, Terminal: true, State: "completed", Trigger: workspace.TriggerDone},
			want: workspace.RetainKeep,
		},
		{
			name: "user tree never recycled on stop",
			in:   workspace.RecycleInput{Managed: false, Terminal: true, State: "failed", Trigger: workspace.TriggerStop},
			want: workspace.RetainKeep,
		},
		{
			name: "manual tree never recycled",
			in:   workspace.RecycleInput{Managed: true, Manual: true, Terminal: true, Trigger: workspace.TriggerExplicit},
			want: workspace.RetainKeep,
		},
		{
			name: "waiting_review not recycled by explicit gc",
			in:   workspace.RecycleInput{Managed: true, Terminal: false, State: "waiting_review", Trigger: workspace.TriggerExplicit},
			want: workspace.RetainKeep,
		},
		{
			name: "waiting_review not recycled by done mis-fire",
			in:   workspace.RecycleInput{Managed: true, Terminal: false, State: "waiting_review", Trigger: workspace.TriggerDone},
			want: workspace.RetainKeep,
		},
		{
			name: "stop retains failed managed tree",
			in:   workspace.RecycleInput{Managed: true, Terminal: true, State: "failed", Trigger: workspace.TriggerStop},
			want: workspace.RetainKeep,
		},
		{
			name: "running not recycled",
			in:   workspace.RecycleInput{Managed: true, Terminal: false, State: "running", Trigger: workspace.TriggerExplicit},
			want: workspace.RetainKeep,
		},
		{
			name: "done recycles archived managed tree",
			in:   workspace.RecycleInput{Managed: true, Terminal: true, State: "completed", Trigger: workspace.TriggerDone},
			want: workspace.RetainRecycle,
		},
		{
			name: "explicit reclaim recycles failed managed tree",
			in:   workspace.RecycleInput{Managed: true, Terminal: true, State: "failed", Trigger: workspace.TriggerExplicit},
			want: workspace.RetainRecycle,
		},
		{
			name: "compensate recycles unused managed tree",
			in:   workspace.RecycleInput{Managed: true, Terminal: false, State: "", Trigger: workspace.TriggerCompensate},
			want: workspace.RetainRecycle,
		},
		{
			name: "compensate does not recycle user tree",
			in:   workspace.RecycleInput{Managed: false, Trigger: workspace.TriggerCompensate},
			want: workspace.RetainKeep,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspace.MayRecycle(tt.in); got != tt.want {
				t.Fatalf("MayRecycle(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
