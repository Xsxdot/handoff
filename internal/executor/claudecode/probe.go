// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读恢复凭据，走 Proc.Alive 的既有判据，如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不占 runs 位、不碰 store、不发事件。
//     Resume 这三件事都做（判死后 Kill 是冷恢复不撞名的前置），正是它不能被
//     status 复用的原因
//   - 不重试、不做抖动吸收：一次探测一个结论。抖动误判的代价由调用方承担，
//     而 status 只读——误判的代价是输出里一行错话，不是一个被错判的任务
package claudecode

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 claude 执行器是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：tmux 会话存在 **且** out.jsonl 不含
// handoff_exit 哨兵，缺一即视为死亡。单看 tmux 会假阳性——窗口 1 的
// tail -f render.log 会一直吊着会话，claude 早死了会话依然在。判据一旦分叉，
// status 说的和实际恢复行为就是两回事。
//
// 参数：
//   - req: 探测请求（TaskDir 是 claude.json 所在，即 DataDir/tasks/<id>）
//
// 返回：
//   - Alive=true：执行器仍在，Note 为空
//   - Alive=false + Note：已判死，Note 是给审核者看的一句话理由
//   - err != nil：探不出结论（恢复凭据缺失/损坏），调用方按 unknown 处理，
//     **不得当成 dead**
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		a.log.Info("claude 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 claude 恢复凭据: %w", err)
	}
	// 只填 Alive 用得到的两个字段：TmuxSession 判会话、TaskDir 定位 out.jsonl
	proc := &Proc{TmuxSession: pi.TmuxSession, TaskDir: req.TaskDir}
	if proc.Alive() {
		a.log.Info("claude 探活：执行器存活", "task", req.TaskID, "tmux", pi.TmuxSession)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("claude 执行器已不在（tmux 会话 %s）", pi.TmuxSession)
	a.log.Info("claude 探活：执行器已不在", "task", req.TaskID, "tmux", pi.TmuxSession)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
