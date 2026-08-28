// probe.go —— 只读存活探测。
package agy

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/executor"
)

// Probe 只读探测 agy 执行器是否仍存活。
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		a.log.Info("agy 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读恢复凭据: %w", err)
	}
	proc := &Proc{Handle: pi.Handle, TaskDir: req.TaskDir}
	if proc.Alive() {
		a.log.Info("agy 探活：执行器存活", "task", req.TaskID, "shim_pid", pi.Handle.PID)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("agy 执行器已不在（进程 pid %d）", pi.Handle.PID)
	a.log.Info("agy 探活：执行器已不在", "task", req.TaskID, "shim_pid", pi.Handle.PID)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
