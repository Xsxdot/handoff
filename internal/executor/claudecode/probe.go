// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读恢复凭据，走 Proc.Alive 的既有判据，如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收执行者进程、不占 runs 位、不碰 store、不发事件。
//     Resume 这三件事都做（判死后 Kill 是冷恢复不撞名的前置），正是它不能被
//     status 复用的原因
//   - 不重试、不做抖动吸收：一次探测一个结论。抖动误判的代价由调用方承担，
//     而 status 只读——误判的代价是输出里一行错话，不是一个被错判的任务
package claudecode

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/executor"
)

// Probe 只读探测 claude 执行器是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：存活锁被持有 **且** out.jsonl 无
// handoff_exit 哨兵，缺一即视为死亡。判据一旦分叉，status 说的和实际恢复行为
// 就是两回事。
//
// 参数：
//   - req: 探测请求（TaskDir 是 proc.json 所在，即 DataDir/tasks/<id>）
//
// 返回：
//   - Alive=true：执行器仍在，Note 为空
//   - Alive=false + Note：已判死，Note 是给协调者看的一句话理由
//   - err != nil：探不出结论（恢复凭据缺失/损坏），调用方按 unknown 处理，
//     **不得当成 dead**
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		a.log.Info("claude 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读恢复凭据: %w", err)
	}
	// 只填 Alive 用得到的字段：Handle 判存活锁、TaskDir 定位 out.jsonl
	proc := &Proc{Handle: pi.Handle, TaskDir: req.TaskDir}
	if proc.Alive() {
		a.log.Info("claude 探活：执行器存活", "task", req.TaskID, "shim_pid", pi.Handle.PID)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	// Note 是判死后直接呈给协调者的一句话理由，写着一个已经不存在的概念等于误导
	note := fmt.Sprintf("claude 执行器已不在（进程 pid %d）", pi.Handle.PID)
	a.log.Info("claude 探活：执行器已不在", "task", req.TaskID, "shim_pid", pi.Handle.PID)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
