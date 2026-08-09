// controlplane Projector：machine event → 控制面全局投影。
//
// 职责：
//   - Apply：把一条 machine event 投影进控制面（幂等记录 → 更新投影 →
//     追加 ControlEvent → 更新 last_machine_seq）
//   - 本机与远端事件共用同一入口（spec §8.3：桌面 handler 不得为 local 分支
//     直接查原始表）
//
// 边界：
//   - 投影与 control_event 同事务（由 Repository.ApplyMachineEvent 承担）
//   - 重复事件返回 applied=false 且不分配新 revision
//   - 本类型只做编排，不触碰数据库细节
package controlplane

import (
	"context"
	"fmt"
)

// Projector 把机器事件投影为控制面全局状态。
type Projector struct {
	repo Repository
	// OnApplied 在事件成功投影（applied=true）后回调产生的 ControlEvent。
	// 供上层（agentd）把事件广播进 control stream hub；nil=不广播。
	OnApplied func(ControlEvent)
}

// NewProjector 创建投影器。
//
// 参数：
//   - repo: 实现 ApplyMachineEvent 的持久化端口（通常是 *store.Store）
func NewProjector(repo Repository) *Projector {
	return &Projector{repo: repo}
}

// Apply 投影一条 machine event。
//
// 返回：
//   - ControlEvent：投影产生的控制事件；重复事件返回零值
//   - applied：true=新投影并分配 revision；false=重复被幂等忽略
//   - err：数据库错误
//
// 语义（spec §8.3）：幂等记录 machine event → 更新 Workspace/GitRef/
// TaskSummary/Operation 投影 → 追加 ControlEvent → 更新 last_machine_seq，
// 全部在同一事务内。
func (p *Projector) Apply(ctx context.Context, ev MachineEvent) (ControlEvent, bool, error) {
	if ev.MachineID == "" || ev.EventID == "" {
		return ControlEvent{}, false, fmt.Errorf("machine event 缺 machine_id 或 event_id")
	}
	ce, applied, err := p.repo.ApplyMachineEvent(ctx, ev)
	if err != nil {
		return ControlEvent{}, false, fmt.Errorf("投影 machine event %s/%s: %w", ev.MachineID, ev.EventID, err)
	}
	if applied && p.OnApplied != nil {
		p.OnApplied(ce)
	}
	return ce, applied, nil
}
