// Package agentd 的强制回收入口。
//
// 职责：把「停 executor → 判成败 → 成功才落 failed」这三步收成一个方法，供
// watchdog 的硬上限档调用（B119 §2.3）。
// 边界：不删 worktree（那是 handoff stop 的人工决定，watchdog 自动触发不继承
// 它）；不自己清扫（清扫挂在 transit 的终态分支上，见 manager.go 的 transit）；
// 不做告警去重（边沿状态由调用方 watchdog 持有，见 scanTaskProcs）。
package agentd

import (
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ForceReclaim 强制回收一个失控任务：先停 executor，停掉了才把任务落 failed。
//
// 参数：
//   - taskID: 目标任务
//   - reason: 落 failed 时写进事件的理由，必须含可判断的真实数字（如实际进程数
//     与硬上限），审核者事后要凭它判断回收得对不对
//
// 返回：
//   - nil: executor 已停、任务已落 failed（清扫与工单作废由 transit 的终态收口
//     自动完成）
//   - 非 nil: 没停掉或状态迁移失败，**任务状态保持不变**——调用方应让它留在活跃
//     集里下一轮继续点名重试
//
// 注意：
//   - **顺序不可换**：想清掉一个正在 fork 的任务，必须先让 fork 的源头停下来；
//     杀子进程而留着父进程是打地鼠。这也是 B119 的根因——改前直接对活着的
//     executor 调 Sweep，段①被跳过后什么都没收，却照样宣布「已强制回收」
//   - 不删 managed worktree：1200 进程这种现场最需要留证，删 worktree 是不可逆
//     且外部可见的动作，留给协调者事后 handoff reclaim
func (m *Manager) ForceReclaim(taskID, reason string) error {
	m.log.Warn("强制回收进入", "task", taskID, "reason", reason)
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("强制回收解析执行者失败", "task", taskID, "cause", err)
		return fmt.Errorf("强制回收解析执行者: %w", err)
	}
	if serr := m.stopExecutor(taskID, ad); serr != nil {
		// 没收掉就不能宣布收掉：保持活跃态，让 watchdog 下一轮继续点名重试。
		// stopExecutor 内部已对 ErrStillAlive 发过 orphan_risk 提示。
		m.log.Error("强制回收失败：executor 未停止，任务保持活跃", "task", taskID, "cause", serr)
		return fmt.Errorf("强制回收停止 executor: %w", serr)
	}
	if terr := m.transit(taskID, proto.TaskStateFailed, reason); terr != nil {
		m.log.Error("强制回收落 failed 失败", "task", taskID, "cause", terr)
		return fmt.Errorf("强制回收落 failed: %w", terr)
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeFailed, newFailedPayload(reason, "", ""))
	if aerr != nil {
		m.log.Error("强制回收追加 failed 事件失败", "task", taskID, "cause", aerr)
		return fmt.Errorf("强制回收追加事件: %w", aerr)
	}
	m.hub.Publish(evt)
	m.log.Warn("强制回收完成", "task", taskID, "reason", reason)
	return nil
}
