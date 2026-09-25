// failure.go —— 回合失败的封闭分类词表。
//
// 职责：给「一个回合为什么失败」提供跨进程可判别的精确取值，让消费方按字段
// 而不是按人读文案分支（B100：客户端若靠 fail_reason 散文判断，供应商改一句
// 文案就会静默改变行为）。
//
// 边界：纯类型包，不含生产/消费逻辑；词表是封闭的——新增分类必须改这里，
// 未知值由消费方一律 fail-closed。
package proto

// FailureClass 是一个回合失败的封闭分类。
//
// 空值（零值）表示「未分类」：旧 adapter、旧 agentd 或历史事件没有这个字段，
// 其语义是「不可自动重试」，不是「零文本」。
type FailureClass string

const (
	// FailureClassZeroText 表示回合明确以零文本收尾（供应商流中断等），且
	// executor 进程仍在线，可由节点自动续接一次同一 task。
	//
	// 只有 adapter 的明确零文本分支可以产出它；不得从最终字符串、事件顺序
	// 或 git 状态反推（spec §8）。
	FailureClassZeroText FailureClass = "zero_text"
)
