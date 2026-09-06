package executor_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestPermFingerprintGoldenVectors(t *testing.T) {
	tests := []struct {
		name string
		ev   executor.AdapterEvent
		want string
	}{
		{
			name: "command domain echo hello",
			ev: executor.AdapterEvent{
				Text: "bash: echo hello",
				Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "echo hello"},
			},
			want: "7d0130b080382e74c685d247932d8e5379402827c2715a5e89f876e38f0d97b6",
		},
		{
			name: "path domain sorted a.txt b.txt",
			ev: executor.AdapterEvent{
				Text: "edit: files",
				Perm: &executor.PermRequest{Tool: executor.PermToolEdit, Paths: []string{"b.txt", "a.txt"}},
			},
			want: "59234912cd739e05080f36c692d91730511dd142d7026e42d18a47eb78a43b87",
		},
		{
			name: "path domain write /tmp/x",
			ev: executor.AdapterEvent{
				Text: "write: /tmp/x",
				Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{"/tmp/x"}},
			},
			want: "256b330da811d87cde7f209883a1fd3f294971aae0df939dbeb487a1c3e51f24",
		},
		{
			name: "text domain bash rm",
			ev:   executor.AdapterEvent{Text: "Bash: rm -rf /"},
			want: "d6517b968a2866ccc16958f7aa2be184c410936552c5e04975708717b9e04b4c",
		},
		{
			name: "text domain empty",
			ev:   executor.AdapterEvent{},
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executor.PermFingerprint(tt.ev); got != tt.want {
				t.Fatalf("PermFingerprint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPermFingerprintCommandDomainIgnoresTwinKind(t *testing.T) {
	cmd := "rm -rf /tmp/x"
	extDir := executor.AdapterEvent{
		Text: "external_directory: " + cmd,
		Perm: &executor.PermRequest{Tool: "external_directory", Command: cmd, Paths: []string{"/tmp"}},
	}
	bash := executor.AdapterEvent{
		Text: "bash: " + cmd,
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd},
	}
	if executor.PermFingerprint(extDir) != executor.PermFingerprint(bash) {
		t.Fatal("same command must share fingerprint across twin permission kinds")
	}
}

func TestReuseFingerprintSaltsVersion(t *testing.T) {
	perm := executor.PermFingerprint(executor.AdapterEvent{
		Text: "bash: echo hello",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "echo hello"},
	})
	a := executor.ReuseFingerprint("ver-1", perm)
	b := executor.ReuseFingerprint("ver-2", perm)
	c := executor.ReuseFingerprint("ver-1", perm)
	if a == perm {
		t.Fatal("加盐后不得等于裸 PermFingerprint")
	}
	if a == b {
		t.Fatal("不同 Version 必须不同复用键")
	}
	if a != c {
		t.Fatal("同一 Version+指纹必须稳定")
	}
	if len(a) != 64 {
		t.Fatalf("hex sha256 长度=%d", len(a))
	}
}

