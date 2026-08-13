// reap.go —— 无内存运行态时的确定性兜底回收。
//
// 职责：
//   - Reap：按 proc.json 拿 prochost.Handle 并 Kill
//
// 边界：
//   - 不碰任务状态（adapter 不写 store）；回收不掉只返回错误，留不留事件是 manager 的事
package claudecode

import (
	"fmt"

	"github.com/xushixin/handoff/internal/prochost"
)

// Reap 在没有内存运行态时按 proc.json 兜底回收 executor 侧资源。
//
// 回收顺序：读 proc.json 拿 Handle → prochost.Kill（内部先试锁，锁空闲直接成功）。
//
// 为什么不再有「确定性命名兜底」：旧实现在 proc.json 缺失时退到 tmux 会话名
// handoff-<id8>，因为会话名可由 taskID 推导。锁+pid 无法从 taskID 推导，
// proc.json 缺失就是真的无据可查——如实报错交审核者，不猜。
//
// 返回：Handle 对应的进程本就不在时返回 nil——目标是「确保它没了」，
// 不是「确保我杀了它」。
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
