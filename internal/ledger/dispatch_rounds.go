// 本文件负责记录远端任务已经创建但本地派发落账失败的耗费轮次。
// 边界：只操作 card_dispatch_rounds，不执行远端 HTTP，不生成事件或 TaskLink。
package ledger

import (
	"database/sql"
	"fmt"
)

// RecordDispatchRound 记录一次未形成 card_tasks 挂账的耗费轮次。
// 参数 cardID 和 purpose 必须非空；返回值保留事务、卡不存在或 SQL 错误。
// 注意：该记录是独立计数，不向事件流广播，也不替代成功挂账。
func (s *Store) RecordDispatchRound(cardID, purpose string) error {
	log().Debug("派发耗费轮次落账进入", "card", cardID, "purpose", purpose)
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if cardID == "" {
			return fmt.Errorf("派发耗费轮次: 卡号不能为空")
		}
		if purpose == "" {
			return fmt.Errorf("派发耗费轮次: purpose 不能为空")
		}
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("派发耗费轮次: 卡 %s: %w", cardID, err)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_dispatch_rounds
			(card_id, purpose, created_at) VALUES (?, ?, ?)`),
			cardID, purpose, s.tval(s.timeNow())); err != nil {
			return fmt.Errorf("写派发耗费轮次: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("派发耗费轮次落账失败", "card", cardID, "purpose", purpose, "cause", err)
		return err
	}
	log().Info("派发耗费轮次落账完成", "card", cardID, "purpose", purpose)
	return nil
}
