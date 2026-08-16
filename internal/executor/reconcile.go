// reconcile.go —— 断连窗口会话对账的共享数据契约（B38）。
//
// 职责：
//   - 定义 ReconcileOutcome，供 manager 与各 adapter 共用
//
// 边界：
//   - 只有数据，没有接口：与 resume.go 同规格——对账能力的接口由消费方
//     （manager）定义并做类型断言，这样「不支持对账的 adapter」仍是自然语义，
//     executor.Adapter 的五动作核心契约也不被污染
//   - 无 I/O、无实现
package executor

// ReconcileOutcome 是一次对账的结论，供 CLI 呈现与日志记录。
//
// 字段说明：
//   - TurnEnded: 断连期间回合是否已完结
//   - Emitted: 补发的终态事件数。取值只有 0 或 1——一个断连窗口内至多跨越
//     一个回合边界（新回合只能由经过 agentd 的 Start/Send 发起）
//   - Pending: 重新上报的悬而未决权限请求数。**opencode 恒为 0**：它的消息流
//     里 tool part 只有 callID 没有权限 id，应答端点要求真实 id、伪造即 404，
//     故建出的工单批了也送不回去——宁可不建
//   - Note: 一句话结论，直接给协调者看
type ReconcileOutcome struct {
	TurnEnded bool
	Emitted   int
	Pending   int
	Note      string
}
