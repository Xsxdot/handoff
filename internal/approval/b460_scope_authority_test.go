package approval_test

// b460_scope_authority_test.go —— B460 V4 生产 Scope 三条：经 Authority.Judge（真 store 里的
// 任务行、权威自己构造 Scope），证明派生的产物目录进了生产 Scope（防 permgate 单测绿、
// 生产 Scope 里没有 ArtifactsDir 的假绿），且 Cwd/URL 由权威透传给权限门。
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
)

func TestB460AuthorityScopeDerivesArtifactsDir(t *testing.T) {
	const taskID = "b460-scope"
	_, a, dataDir := newAuthorityFixture(t, taskID, t.TempDir())
	art := filepath.Join(dataDir, "tasks", taskID, "artifacts")
	if err := os.MkdirAll(art, 0o700); err != nil {
		t.Fatal(err)
	}
	bash := func(cmd, cwd string) executor.AdapterEvent {
		return executor.AdapterEvent{Type: "permission", PermissionID: "p-b460", Text: "Bash: " + cmd,
			Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: cmd, Cwd: cwd}}
	}
	cases := []struct {
		name  string
		ev    executor.AdapterEvent
		allow bool
	}{
		{"P1 mkdir 产物子目录", bash("mkdir -p "+art+"/sub", ""), true},
		{"P5 产物变量前缀", bash(`mkdir -p "$HANDOFF_ARTIFACTS_DIR/sub"`, ""), true},
		{"N2 HOME 展开", bash(`mkdir -p "$HOME/x"`, ""), false},
		{"P8 Cwd 透传", bash("printf x > r.md", art), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := a.Judge(taskID, tc.ev)
			if (v.Action == permgate.AutoAllow) != tc.allow {
				t.Fatalf("%q 放行=%v 期望 %v（%s %s）", tc.ev.Perm.Command, v.Action == permgate.AutoAllow, tc.allow, v.Action, v.Reason)
			}
		})
	}
	v := a.Judge(taskID, executor.AdapterEvent{Type: "permission", PermissionID: "p-wf",
		Text: "webfetch: https://go.dev/doc/",
		Perm: &executor.PermRequest{Tool: executor.PermToolWebFetch, URL: "https://go.dev/doc/"}})
	if v.Action != permgate.AutoAllow || v.Rule != permgate.RuleWebFetch {
		t.Fatalf("webfetch 经权威应放行 rule=webfetch，得 %s rule=%q", v.Action, v.Rule)
	}
}
