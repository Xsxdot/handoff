// 协作房间域（B156.2）在账本侧的两个事件写入点：房间消息与消费标记。
// 房间、成员、白名单等规则归 d_collab；账本只承事件流机制——这里不解释
// RoomMessage 的业务字段，只保证「存在性校验 + 同事务 append」的落账纪律。
package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"

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

// consumedMarker 是 message_consumed 事件的载荷 schema（契约 §4 金样键集：
// 恰 message_seq 与 consumer 两键）。actor 列另存 consumer 一份供查重与
// 读侧扫描先按列粗筛；载荷才是权威。collab 侧不得 import 本包，其本地
// 镜像 struct 的等值由两侧字面量测试钉住（TestRoomEventTypeLiteralMatchesLedger
// 同形）。刻意不放 proto：d_protocol 本轮零触碰。
type consumedMarker struct {
	MessageSeq int64  `json:"message_seq"`
	Consumer   string `json:"consumer"`
}

// RecordMessageConsumed 落消息消费标记：同一 mutate 事务内查重后写
// （ClearNeedsHumanFrom 同形，events.go），同 (msgSeq, consumer) 重复消费
// 是幂等 no-op。恰好一次由 mutate 的单写者串行化免费获得（store.go mutate
// 注释）——查重与写入之间的窗口被事务吃掉，这是拍板 5.4「权威在账本事件」
// 的机制兑现。
//
// 语义边界（都有测试钉着）：
//   - cardID 非空时必须指向存在的卡（ErrNotFound），标记挂同一张卡的流上；
//     cardID=""=群级消息的项目级标记。cardID 不参与查重键——seq 全局唯一，
//     传错 cardID 时幂等性优先于报错。
//   - 不校验 msgSeq 是否指向存在的 room_message：目标态「已消费」对不存在
//     消息天然成立（breakdown 岔口六方案甲，未知 seq 同样真落一条标记），
//     静默面由 Pending/Mentions 只列未消费兜底。要改这个选择先回 contract。
//   - consumer 为空直接报错：「谁消费了哪条」没有「谁」无意义。
//
// 返回 nil 含三种情形：首次写入成功、本人重复消费跳过、他人已消费后再写
// 自己的标记（各消费者一条，互不顶替）——前两种全流恰一条本人标记，第三种
// 是新消费者的首次写入。
func (s *Store) RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error {
	if consumer == "" {
		return fmt.Errorf("消费标记必须带 consumer")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return fmt.Errorf("消费标记: 卡 %s: %w", cardID, err)
			}
		}
		// 查重：先按 type+actor 列粗筛出本人的全部标记，再解载荷精确比对
		// message_seq。载荷匹配无法用跨 SQLite(TEXT)/PG(JSONB) 方言的 SQL
		// 表达，Go 解析是唯一可移植路径；单消费者的标记量以「他消费过的
		// 消息数」为界，全扫可承受。
		rows, err := tx.Query(s.q(`SELECT payload FROM card_events WHERE type = ? AND actor = ?`),
			EvMessageConsumed, consumer)
		if err != nil {
			return fmt.Errorf("查消费标记: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return fmt.Errorf("扫消费标记行: %w", err)
			}
			var marker consumedMarker
			if err := json.Unmarshal([]byte(raw), &marker); err != nil {
				continue // 非法载荷不该出现；跳过不中断幂等判定
			}
			if marker.MessageSeq == msgSeq && marker.Consumer == consumer {
				log().Info("消费标记幂等跳过：同参标记已存在",
					"msg_seq", msgSeq, "consumer", consumer, "card", cardID)
				return nil
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历消费标记: %w", err)
		}
		seq, err := s.appendEvent(tx, sink, cardID, EvMessageConsumed, consumer,
			consumedMarker{MessageSeq: msgSeq, Consumer: consumer})
		if err != nil {
			return err
		}
		log().Info("消息消费标记已落账", "msg_seq", msgSeq, "consumer", consumer,
			"card", cardID, "event_seq", seq)
		return nil
	})
}
