// resume_killold_internal_test.go —— 冷恢复前必须回收旧 tmux 会话的回归测试。
//
// 职责：
//   - 断言 Resume 判定 serve 已死时，会先 kill 掉旧的同名 tmux 会话
//
// 边界：
//   - 不起 serve 进程、不连网络：探活用一个没人监听的端口，tmux kill 走测试缝
//   - 只验「回收发生了」，重起 serve 本身是 startServe 的职责
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// TestResumeKillsStaleSessionBeforeRestart serve 已死时必须先回收旧 tmux 会话。
//
// 现场（2026-08-09 grok 端到端验收）：杀掉 grok serve 进程后 continue，冷恢复
// 重起 serve 报 "duplicate session: handoff-3949eebd" 而失败，任务回迁
// waiting_review，四级阶梯走到第 3 级又掉下来。原因是 grok 的 tmux 会话由窗口 1
// 的 `tail -f render.log` 吊着——serve 进程死了会话仍在，而冷恢复用的是同一个
// 确定性会话名（handoff-<id8>），必然撞名。claudecode 的 Resume 早有这一步
// （见其 resume.go「先回收旧会话，否则冷恢复重起时撞名」），grok 漏了。
//
// 用 Cold=false 断言：回收发生在「允不允许冷恢复」的判断之前，这样用例不必
// 真的去起一个 grok 进程。
func TestResumeKillsStaleSessionBeforeRestart(t *testing.T) {
	taskDir := t.TempDir()
	// 端口 1 上不会有 serve 在听 → Alive() 判死
	info := Proc{Session: "handoff-deadbeef", TaskDir: taskDir, Port: 1, Secret: "s"}
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("造 serve.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "serve.json"), b, 0o600); err != nil {
		t.Fatalf("写 serve.json: %v", err)
	}

	var killed string
	restore := SwapTmuxKillForTest(func(session string) error { killed = session; return nil })
	defer restore()

	a := New(quietLogger())
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "deadbeef-0000-0000-0000-000000000000", TaskDir: taskDir,
		RepoPath: t.TempDir(), SessionID: "sess-1", Cold: false,
	})
	if err != nil {
		t.Fatalf("Resume 不该报错（判不可恢复不是错误）: %v", err)
	}
	if out.Alive {
		t.Fatal("serve 已死且不允许冷恢复时应判不存活")
	}
	if killed != "handoff-deadbeef" {
		t.Fatalf("必须回收旧 tmux 会话 handoff-deadbeef，实得 %q——不回收的话冷恢复重起必撞名", killed)
	}
}
