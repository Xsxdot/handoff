package approval_test

// b452_artifacts_authority_test.go —— B452 V7 权限两条，经决策权威 Authority.Judge 走一遍：
// 权威按 <DataDir>/tasks/<id> 组 TaskDir，产物目录 <DataDir>/tasks/<id>/artifacts 内的写自动放行
// （无工单），$HOME 下的写升级。Manager 路径与 OpenCode approval.Client 路径共用这一个 Judge。
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
)

func TestB452AuthorityArtifactsWriteAutoAllowHomeEscalates(t *testing.T) {
	const taskID = "b452-artifacts"
	_, a, dataDir := newAuthorityFixture(t, taskID, t.TempDir())
	write := func(p string) executor.AdapterEvent {
		return executor.AdapterEvent{Type: "permission", PermissionID: "p-art", Text: "Write: " + p,
			Perm: &executor.PermRequest{Tool: executor.PermToolWrite, Paths: []string{p}}}
	}
	art := filepath.Join(dataDir, "tasks", taskID, "artifacts", "r.md")
	if v := a.Judge(taskID, write(art)); v.Action != permgate.AutoAllow {
		t.Fatalf("写产物目录应自动放行，得 %s（%s）", v.Action, v.Reason)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到 HOME")
	}
	if v := a.Judge(taskID, write(filepath.Join(home, "x.md"))); v.Action != permgate.Escalate {
		t.Fatalf("写 $HOME/x.md 必须升级人工，得 %s（%s）", v.Action, v.Reason)
	}
}
