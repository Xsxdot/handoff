// deny.go —— 协调者拒绝时下发给模型的正文渲染。
//
// 职责：
//   - 提供拒绝理由正文的唯一渲染点
//
// 边界：
//   - 不决定「送不送」「怎么送」：同帧送达在 claude adapter，带外注入在 agentd
//
// 为什么单独抽出来：同一段话有两个出口——claude 经 permDecision.Message 与裁决
// 同帧送达，其余 executor 经 manager 的带外注入。两处措辞若各写各的，同一件事
// 在不同 executor 上读起来会像两回事，而这段话正是要让模型改变做法的那段。
package turn

// DenyGuidanceText 渲染「操作被拒 + 理由 + 别再重试」的正文。
//
// 参数：reason 为协调者给出的原因，调用方保证已 trim 且非空
// 返回：可直接下发给模型的正文
//
// 注意：末句「不要重复发起同一请求」不是客套——不给这句，模型被拒后最常见的
// 下一步就是原地再试一次同样的操作，白烧一个回合。
func DenyGuidanceText(reason string) string {
	return "你请求的操作已被协调者拒绝。原因：" + reason +
		"\n请据此调整做法后继续，不要重复发起同一请求。"
}
