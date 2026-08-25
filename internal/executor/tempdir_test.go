package executor_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestTaskTmpDirGoldenVectors(t *testing.T) {
	tests := []struct {
		name    string
		dataDir string
		taskID  string
		want    string
	}{
		{
			name:    "default data root and UUID",
			dataDir: "/root/.handoff",
			taskID:  "137a7dc9-df89-4c1c-891e-ebe106c68b37",
			want:    "/root/.handoff/tmp/137a7dc9",
		},
		{
			name:    "short ID is unchanged",
			dataDir: "/var/lib/handoff",
			taskID:  "T1",
			want:    "/var/lib/handoff/tmp/T1",
		},
		{
			name:    "empty ID is represented without padding",
			dataDir: "/var/lib/handoff",
			taskID:  "",
			want:    "/var/lib/handoff/tmp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executor.TaskTmpDir(tt.dataDir, tt.taskID); got != tt.want {
				t.Fatalf("TaskTmpDir(%q, %q) = %q, want %q", tt.dataDir, tt.taskID, got, tt.want)
			}
		})
	}
}
