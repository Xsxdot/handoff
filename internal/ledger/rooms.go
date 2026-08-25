// 协作房间域（B156.2）在账本侧的两个事件写入点：房间消息与消费标记。
// 房间、成员、白名单等规则归 d_collab；账本只承事件流机制——这里不解释
// RoomMessage 的业务字段，只保证「存在性校验 + 同事务 append」的落账纪律。
package ledger

import (
	"database/sql"

	"github.com/Xsxdot/handoff/internal/proto"
)

// RecordRoomMessage 落一条房间消息事件。cardID 非空 = 卡会话消息（必须
// 指向存在的卡）；空 = 群级无卡事件（项目群/全员群，天然不进多路 wait）。
// 返回 seq。白名单与书写者执法在 d_collab 的 Send，不在本方法。
func (s *Store) RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error) {
	var seq int64
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return err
			}
		}
		var err error
		seq, err = s.appendEvent(tx, sink, cardID, EvRoomMessage, actor, msg)
		return err
	})
	return seq, err
}

// RecordMessageConsumed 落消息消费标记：同一 mutate 事务内查重后写
// （ClearNeedsHumanFrom 同形），同 (msgSeq, consumer) 重复消费是幂等 no-op。
// Ticket 0 空壳：无可观测行为，实现节点按契约 §4 清单补齐。
func (s *Store) RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error {
	_ = cardID
	_ = msgSeq
	_ = consumer
	return nil
}
