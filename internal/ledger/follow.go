// 账本单流消费：多路 wait 的读侧。推送只是叫醒，真相永远按 seq 查表——
// PG 用 LISTEN card_events 叫醒 + 兜底轮询，SQLite 纯轮询。
package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Follow 从 fromSeq（排他）起持续消费 members() 集合内卡的事件（含
// card_id 为空的项目级事件不在内——多路 wait 只关心子树）。members
// 每轮重新求值：wait 挂起期间新拆/新派发的卡天然进流。onEvent 返回
// 错误即终止。pollInterval 是 SQLite 的轮询间隔与 PG 的兜底间隔；
// 生产用 2*time.Second，阻塞直到 ctx 取消或 onEvent 报错。
func (s *Store) Follow(ctx context.Context, members func() ([]string, error),
	fromSeq int64, pollInterval time.Duration, onEvent func(Event) error) error {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	wake := make(chan struct{}, 1)
	if s.dialect == dialectPG {
		// LISTEN 用独立裸连接：database/sql 连接池拿不到稳定的会话级 LISTEN
		conn, err := pgx.Connect(ctx, s.dsn)
		if err != nil {
			return fmt.Errorf("LISTEN 连接: %w", err)
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(ctx, "LISTEN card_events"); err != nil {
			return fmt.Errorf("LISTEN: %w", err)
		}
		go func() {
			for {
				// 通知只当叫醒铃用，内容不解析——查询以 seq 为准
				if _, err := conn.WaitForNotification(ctx); err != nil {
					return // ctx 取消或连接断：主循环靠兜底轮询继续
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}()
	}
	cursor := fromSeq
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		ids, err := members()
		if err != nil {
			return fmt.Errorf("解析成员集: %w", err)
		}
		evs, err := s.EventsFromAsc(ids, cursor, 500)
		if err != nil {
			return err
		}
		for _, e := range evs {
			if err := onEvent(e); err != nil {
				return err
			}
			cursor = e.Seq
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		case <-wake:
		}
	}
}
