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

// ProcHandle 交出该任务的进程句柄（来自任务目录的 proc.json）。
//
// 参数：
//   - taskID: 任务 ID，仅用于日志定位
//   - taskDir: 任务目录（凭据所在）
//
// 返回：
//   - 进程句柄；proc.json 不存在或不可解析时返回错误
//
// 注意：本方法**只读**，不探活、不发信号——存活判定与回收分别是
// prochost.Alive 与 prochost.Sweep 的职责。agentd 以可选接口消费它
// （不实现该方法的 adapter 一律按「无凭据」降级，与 reaper/prober 同款路数）。
func (a *Adapter) ProcHandle(taskID, taskDir string) (prochost.Handle, error) {
	pi, err := readProcInfo(taskDir)
	if err != nil {
		a.log.Error("读取进程句柄失败", "task", taskID, "dir", taskDir, "cause", err)
		return prochost.Handle{}, fmt.Errorf("读取进程句柄: %w", err)
	}
	a.log.Debug("取得进程句柄", "task", taskID, "shim_pid", pi.Handle.PID,
		"has_started_at", pi.Handle.StartedAt > 0)
	return pi.Handle, nil
}
