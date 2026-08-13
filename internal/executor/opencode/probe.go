// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 proc.json，走 Proc.Alive 的既有判据（存活锁 + HTTP 应答），
//     如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收执行者进程、不占 runs 位、不碰 store、不发事件
//   - 不打印 procInfo.Password：凭据值绝不进日志，要打只打非敏感字段
package opencode

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 opencode serve 是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：存活锁被持有且端口有 HTTP 应答，
// 缺一即视为死亡。
//
// 参数：
//   - req: 探测请求（TaskDir 是 proc.json 所在，即 DataDir/tasks/<id>）
//
// 返回：
//   - Alive=true：serve 仍在
//   - Alive=false + Note：已判死，Note 给协调者看
//   - err != nil：探不出结论（proc.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	si, err := readProcInfo(req.TaskDir)
	if err != nil {
		a.log.Info("opencode 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 opencode 恢复凭据: %w", err)
	}
	proc := &Proc{Handle: si.Handle, Port: si.Port, Password: si.Password}
	if proc.Alive() {
		a.log.Info("opencode 探活：serve 存活", "task", req.TaskID,
			"shim_pid", si.Handle.PID, "port", si.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	// Note 是判死后直接呈给协调者的一句话理由，写着一个已经不存在的概念等于误导
	note := fmt.Sprintf("opencode serve 已不在（进程 pid %d，端口 %d）", si.Handle.PID, si.Port)
	a.log.Info("opencode 探活：serve 已不在", "task", req.TaskID,
		"shim_pid", si.Handle.PID, "port", si.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
