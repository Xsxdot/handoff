// permission_recover.go —— 断连/重启后重新发现挂起权限（B395）。
//
// 职责：
//   - rediscoverPendingPermissions：查 GET /permission，把本任务会话的未决权限
//     重放成与实时路径同形的 permission 事件（经 mapPermissionAsked），使 executor
//     不再等一个永远收不到应答的权限门
//
// 边界：
//   - 不写 store、不改任务状态：重放的事件经既有 evCh 交 manager，幂等由 manager
//     的工单去重（approval.Authority.IsReplay，authority.go:113）承担
//   - 不做权限判定、不新造描述逻辑：结构化载荷、描述组合、子会话标注、暂停窗口
//     全部复用 mapPermissionAsked（adapter.go:1542），避免与实时路径漂移
//   - 端点缺失（旧版 404）只降级告警，不打断恢复
package opencode

import (
	"context"
	"encoding/json"
)

// permissionAskedProps 把一条挂起权限还原成 permission.asked 的 properties 形状，
// 供 mapPermissionAsked 原样消费（单一解析入口，避免两套字段名漂移）。
//
// 为什么走 JSON 往返而不是直接构造 AdapterEvent：mapPermissionAsked 已承载描述
// 组合、空描述兜底、子会话标注、permSession/permText/permTiming 登记与
// PauseWaiting 全套语义；再造一条「恢复专用」事件构造会与实时路径漂移。且
// testdata/perm_bash.json 的真实形态正是这个形状——往返本身就是可回归的序列化边界。
//
// 注意：Metadata 为空（服务端未给 metadata）时不写该键，mapPermissionAsked 解析时
// 得零值结构——与「字段缺席」同义，不会 panic。patterns 为空时写成 null/[]，两者
// 都被解析成空切片，语义相同。
func permissionAskedProps(p PendingPermission) json.RawMessage {
	m := map[string]any{
		"id":         p.ID,
		"sessionID":  p.SessionID,
		"permission": p.Permission,
		"patterns":   p.Patterns,
		"tool":       map[string]any{"messageID": p.Tool.MessageID, "callID": p.Tool.CallID},
	}
	if len(p.Metadata) > 0 {
		m["metadata"] = p.Metadata
	}
	b, _ := json.Marshal(m)
	return b
}

// rediscoverPendingPermissions 在断连重连/agentd 重启后重新发现本任务挂起的权限并重放。
//
// 为什么必须有这一步（B395 根因）：/event 无重放语义，断连间隙服务端产出的
// permission.asked 永久丢失（adapter.go:1111-1117 的 onReconnect 告警）。而 opencode
// 侧那个工具还在阻塞等应答——不重新发现，任务就是一个谁也叫不醒的孤儿：回合不产
// 事件、不 idle、无终态，只能在 2h 后由 stall 看门狗唤醒人工（真机：10:29:09 起
// 20 分钟零帧产出，最终 resume --force 收口）。提问有 rediscoverPendingQuestions
// （resume.go:213）兜这条，权限此前没有。
//
// 注意：
//   - 本函数在 goroutine 里跑，不阻塞重连/重启返回；失败只记日志（恢复不了还有
//     stall 看门狗与人工兜底，不该让恢复本身失败）
//   - GET /permission 返回该 serve 上全部会话的挂起权限。每个任务一个 serve
//     （proc.go 的 freePort），流上的陌生会话只可能是本任务派生的子会话——归属
//     判定复用实时路径的 acceptForeign（adapter.go:1431），不另立一套规则
//   - 只处理未决权限：已应答的不会出现在 GET /permission 里，故不会重复唤醒；
//     即便服务端同时经 SSE 重放同一 request，也由 manager 的工单去重吸收
func (a *Adapter) rediscoverPendingPermissions(ctx context.Context, taskID string) {
	r := a.lookup(taskID)
	if r == nil || r.api == nil {
		a.log.Debug("重新发现挂起权限跳过：该任务无运行态", "task", taskID)
		return
	}
	pending, err := r.api.ListPendingPermissions(ctx)
	if err != nil {
		// 旧版 opencode 无 GET /permission（404）属预期降级：保留人工兜底告警，
		// 不让恢复本身失败
		a.log.Warn("重新发现挂起权限失败，若任务卡在等待决策请 handoff attach 查看或 handoff resume --force 收口",
			"task", taskID, "cause", err)
		return
	}
	n := 0
	for _, p := range pending {
		if p.ID == "" || p.SessionID == "" {
			// 缺 id/会话无法归属，跳过；与 mapPermissionAsked 的守卫同义（不静默丢整批）
			a.log.Warn("恢复时跳过缺 id/会话的挂起权限", "task", taskID, "perm", p.ID)
			continue
		}
		props := permissionAskedProps(p)
		if p.SessionID != r.session &&
			!a.acceptForeign(r, sseEvent{Type: "permission.asked", Properties: props}, p.SessionID) {
			a.log.Warn("恢复时跳过非本任务的挂起权限", "task", taskID,
				"perm", p.ID, "session", p.SessionID, "own_session", r.session)
			continue
		}
		// 与 mapEvent 的 switch 契约一致：mapPermissionAsked 必须在 turnMu 下执行
		r.turnMu.Lock()
		a.mapPermissionAsked(r, props)
		r.turnMu.Unlock()
		n++
	}
	a.log.Info("恢复后重新发现挂起权限", "task", taskID, "count", n, "total", len(pending))
}
