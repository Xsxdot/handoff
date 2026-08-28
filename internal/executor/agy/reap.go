// reap.go —— 无内存运行态时的确定性兜底回收与进程句柄获取。
package agy

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// Reap 在没有内存运行态时按 proc.json 兜底回收 executor 侧资源。
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
func (a *Adapter) ProcHandle(taskID, taskDir string) (prochost.Handle, error) {
	pi, err := readProcInfo(taskDir)
	if err != nil {
		a.log.Error("读取进程句柄失败", "task", taskID, "dir", taskDir, "cause", err)
		return prochost.Handle{}, fmt.Errorf("读取进程句柄: %w", err)
	}
	a.log.Debug("取得进程句柄", "task", taskID, "shim_pid", pi.Handle.PID)
	return pi.Handle, nil
}
