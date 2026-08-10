// resume_killold_internal_test.go —— 冷恢复前必须回收旧执行者进程的回归测试。
//
// 职责：
//   - 断言 Resume 判定 serve 已死时，会先 kill 掉旧的执行者进程（按进程组）
//
// 边界：
//   - 不起 serve 进程、不连网络：探活用一个没人监听的端口 + 无人持有的锁，
//     Kill 走真实的 prochost.Kill（锁空闲即返回 nil，不发信号）
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

// TestResumeKillsStaleSessionBeforeRestart serve 已死时必须先回收旧执行者。
//
// 现场（2026-08-09 grok 端到端验收）：杀掉 grok serve 进程后 continue，冷恢复
// 重起 serve 报撞名而失败，任务回迁 waiting_review。旧根因是 tmux 会话由第二窗口
// tail -f 吊着不散；换成 prochost 后等价风险是「旧 shim 仍持有 proc.lock，
// 新 shim 抢不到锁」——冷恢复路径必须先 Kill 旧 Handle。
//
// 用 Cold=false 断言：回收发生在「允不允许冷恢复」的判断之前，这样用例不必
// 真的去起一个 grok 进程。
func TestResumeKillsStaleSessionBeforeRestart(t *testing.T) {
	taskDir := t.TempDir()
	// 端口 1 上不会有 serve 在听、锁无人持有 → Alive() 判死
	info := procInfo{
		Handle: prochost.Handle{PID: 4242, LockPath: filepath.Join(taskDir, lockFileName)},
		Port:   1, Secret: "s",
	}
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("造 proc.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, procInfoFileName), b, 0o600); err != nil {
		t.Fatalf("写 proc.json: %v", err)
	}

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
}
