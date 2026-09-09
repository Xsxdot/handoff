package ledger

import "errors"

var errDispatchRoundUnwired = errors.New("ledger: dispatch round not wired")

// RecordDispatchRound 为一次已成功创建远端 task、但本地落账未完成的派发保留轮次事实。
// 该事实不属于 card_events 或 card_tasks 事务；Ticket 0 只声明可编译接缝，持久化接线归实现票。
func (s *Store) RecordDispatchRound(cardID, purpose string) error {
	return errDispatchRoundUnwired
}
