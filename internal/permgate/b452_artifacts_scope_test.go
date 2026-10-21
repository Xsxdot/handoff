package permgate

// b452_artifacts_scope_test.go —— B452 S8 风险 ④ 的锁：产物目录 <DataDir>/tasks/<id>/artifacts 落在
// TaskDir 可写范围内（写它不叫醒人），而工作树外、三处范围之外的路径（如 $HOME）仍升级人工。
// 权限门产品代码零改动：这支测试锁的是「产物目录布局 × 既有 Scope 判据」的组合承诺。
import (
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestB452ArtifactsDirWithinTaskDirScope(t *testing.T) {
	g := newTestGate(t)
	dataDir := t.TempDir()
	taskDir := filepath.Join(dataDir, "tasks", "t-b452")
	sc := Scope{Workdir: t.TempDir(), TaskDir: taskDir, TaskTmpDir: filepath.Join(dataDir, "tmp", "t-b452")}
	for _, tool := range []string{executor.PermToolWrite, executor.PermToolEdit} {
		v := g.Judge(Request{Tool: tool, Text: "Write: r.md",
			Paths: []string{filepath.Join(taskDir, "artifacts", "r.md")}}, sc)
		if v.Action != AutoAllow {
			t.Fatalf("%s 产物目录应自动放行，得 %s（%s）", tool, v.Action, v.Reason)
		}
		home := filepath.Join(t.TempDir(), "home", "x.md")
		v = g.Judge(Request{Tool: tool, Text: "Write: x.md", Paths: []string{home}}, sc)
		if v.Action != Escalate {
			t.Fatalf("%s 三处范围之外的路径必须升级，得 %s（%s）", tool, v.Action, v.Reason)
		}
	}
}
