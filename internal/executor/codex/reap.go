// reap.go —— 运行态丢失时的兜底回收（B20）。
//
// 职责：按 proc.json 拿 prochost.Handle 并 Kill，不留孤儿进程。
// 边界：不删任务目录、不碰 worktree（那是归档与 B15 的职责）；
//
//	**不删 ~/.codex/sessions**——那是 codex 自己的会话历史，删了会破坏
//	用户本人的 `codex resume`（spec §5.5）。
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xushixin/handoff/internal/prochost"
)

// Reap 回收一个任务残留的执行者进程。
//
// 参数：
//   - taskID: 任务 ID（用于日志）
//   - taskDir: 任务目录（用于读 proc.json）
//
// 为什么不再有「确定性命名兜底」：旧实现在 proc.json 缺失时退到 tmux 会话名
// handoff-<id8>，因为会话名可由 taskID 推导。锁+pid 无法从 taskID 推导，
// proc.json 缺失就是真的无据可查——如实报错交审核者，不猜。
//
// 返回：回收失败的错误；进程本就不在时返回 nil（回收是幂等的）
func (a *Adapter) Reap(taskID, taskDir string) error {
	pi, err := readProcInfo(taskDir)
	if err != nil {
		a.log.Error("读恢复凭据失败，无法兜底回收", "task", taskID, "cause", err)
		return fmt.Errorf("兜底回收任务 %s: %w", taskID, err)
	}
	a.log.Info("兜底回收 executor 资源", "task", taskID, "shim_pid", pi.Handle.PID)
	if err := prochost.Kill(pi.Handle); err != nil {
		a.log.Error("兜底回收失败", "task", taskID, "shim_pid", pi.Handle.PID, "cause", err)
		return err
	}
	a.log.Info("兜底回收完成", "task", taskID)
	return nil
}

// readProcInfo 读 proc.json 为松散 Handle 容器（供 Reap 只取 Handle 用）。
func readProcInfo(taskDir string) (*procInfo, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, procInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("读恢复凭据: %w", err)
	}
	var pi procInfo
	if err := json.Unmarshal(b, &pi); err != nil {
		return nil, fmt.Errorf("解析恢复凭据: %w", err)
	}
	if pi.Handle.LockPath == "" {
		return nil, fmt.Errorf("恢复凭据字段不完整（缺 handle.lock_path）")
	}
	return &pi, nil
}
