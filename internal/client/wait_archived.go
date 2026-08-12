// wait_archived.go 实现 B67 的依赖任务归档门闩。
//
// 职责：用权威快照与无 cursor 事件流等待真实 archived，维护进程内水位并重连。
// 边界：不读写审核者 cursor，不交付中间事件，不改变远端任务状态。
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

var (
	// ErrDependencyFailed 表示依赖任务在本次等待期间进入 failed。
	ErrDependencyFailed = errors.New("依赖任务已失败")
	// ErrArchivedEventMissing 表示任务已 completed，但 B68 archived 事件缺失。
	ErrArchivedEventMissing = errors.New("任务已归档但 archived 事件缺失")
)

// classifyArchivedSnapshot 在权威快照里寻找真实 archived。
//
// 返回：
//   - 已归档：返回原始 archived 事件与最新水位（供断线重拉）；
//   - 任务 failed：返回 ErrDependencyFailed——等待目标已经不可能达成；
//   - 任务 completed 却找不到 archived：返回 ErrArchivedEventMissing——兼容性/
//     数据错误，绝不合成假事件；
//   - 其余活状态：返回 nil，调用方从最新水位续拉。
//
// 为什么 completed 缺 archived 是错误而不是成功：B68 的 done 路径在归档时
// 追加 archived 事件，completed 却无 archived 意味着对端 agentd 旧版或数据损坏，
// 合成假事件会骗过门闩的调用方，必须显式失败让人升级/查数据。
func classifyArchivedSnapshot(taskID string, snap *AttachInfo) (*proto.Event, int64, error) {
	var fromSeq int64
	if n := len(snap.RecentEvents); n > 0 {
		fromSeq = snap.RecentEvents[n-1].Seq
	}
	if snap.Task.State == proto.TaskStateFailed {
		return nil, fromSeq, fmt.Errorf("%w: task=%s", ErrDependencyFailed, taskID)
	}
	for i := len(snap.RecentEvents) - 1; i >= 0; i-- {
		if snap.RecentEvents[i].Type == proto.EventTypeArchived {
			ev := snap.RecentEvents[i]
			return &ev, fromSeq, nil
		}
	}
	if snap.Task.State == proto.TaskStateCompleted {
		return nil, fromSeq, fmt.Errorf("%w: task=%s", ErrArchivedEventMissing, taskID)
	}
	return nil, fromSeq, nil
}

// WaitArchived 等待任务出现真实的 archived 事件（B67 依赖门闩）。
//
// 参数：
//   - ctx: 总等待时限。它不会被任何中间帧或重连重置，到点即返回
//     context.DeadlineExceeded。
//   - taskID: 任务 id（必须是完整 UUID）
//
// 返回：
//   - *proto.Event: 原始 archived 事件（与 B68 落库一致，不合成）
//   - ErrDependencyFailed: 任务 failed（快照阶段或事件流阶段）
//   - ErrArchivedEventMissing: 任务 completed 但 archived 事件缺失
//   - 永久错误（4xx/1008）与 ctx 取消
//
// 边界：不读写 ~/.handoff/cursor-<task>，水位只留在进程内；等待期间不交付
// question/permission_request/completed 等中间事件，也不触发任何远端状态变更。
func (c *Client) WaitArchived(ctx context.Context, taskID string) (*proto.Event, error) {
	backoff := c.wsInitialBackoff
	c.log().Debug("等待归档开始", "addr", c.baseURL, "task", taskID)

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			c.log().Debug("等待归档结束：上下文取消", "task", taskID, "cause", err)
			return nil, err
		}

		c.log().Debug("等待归档读取权威快照", "task", taskID, "attempt", attempt)
		snap, err := c.Attach(ctx, taskID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if isPermanent(err) {
				c.log().Error("等待归档失败：快照永久错误",
					"task", taskID, "attempt", attempt, "cause", err)
				return nil, err
			}
			c.log().Debug("等待归档快照暂时失败，退避重试",
				"task", taskID, "attempt", attempt, "backoff", backoff, "cause", err)
			if err := waitArchivedRetry(ctx, &backoff,
				c.wsInitialBackoff, c.wsMaxBackoff, false); err != nil {
				return nil, err
			}
			continue
		}
		terminal, fromSeq, err := classifyArchivedSnapshot(taskID, snap)
		if err != nil {
			c.log().Error("等待归档结束：快照终态失败",
				"task", taskID, "from_seq", fromSeq, "cause", err)
			return nil, err
		}
		if terminal != nil {
			c.log().Debug("等待归档完成：快照已有 archived",
				"task", taskID, "seq", terminal.Seq)
			return terminal, nil
		}

		var archived *proto.Event
		var failed *proto.Event
		started := time.Now()
		streamErr := c.StreamEventsOnce(ctx, taskID, fromSeq, func(ev proto.Event) error {
			// 只在内存推进水位：绝不调用 writeCursor。一旦落到磁盘，
			// 审核者 cursor 会被本门闩吃掉，人手敲的 wait 就再也收不到事件。
			fromSeq = ev.Seq
			switch ev.Type {
			case proto.EventTypeArchived:
				copy := ev
				archived = &copy
				return errStopStream
			case proto.EventTypeFailed:
				copy := ev
				failed = &copy
				return errStopStream
			default:
				return nil
			}
		})
		lived := time.Since(started)

		if archived != nil {
			c.log().Debug("等待归档完成：收到 archived",
				"task", taskID, "seq", archived.Seq)
			return archived, nil
		}
		if failed != nil {
			err := fmt.Errorf("%w: task=%s seq=%d payload=%s",
				ErrDependencyFailed, taskID, failed.Seq, string(failed.Payload))
			c.log().Error("等待归档结束：依赖任务失败",
				"task", taskID, "seq", failed.Seq, "cause", err)
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(streamErr, errArchived) {
			// 正常 close 只是线索，不是成功证据：对端也可能因为别的原因正常收尾。
			// 立即回查权威快照，只有真实 archived 事件才允许门闩放行。
			c.log().Debug("等待归档连接正常关闭，回查归档事件",
				"task", taskID, "from_seq", fromSeq)
			continue
		}
		if streamErr == nil {
			err := fmt.Errorf("归档事件流无终态却正常结束: task=%s from_seq=%d", taskID, fromSeq)
			c.log().Error("等待归档协议异常", "task", taskID, "cause", err)
			return nil, err
		}
		if isPermanent(streamErr) {
			c.log().Error("等待归档失败：事件流永久错误",
				"task", taskID, "from_seq", fromSeq, "cause", streamErr)
			return nil, streamErr
		}
		c.log().Debug("等待归档事件流暂时断开，退避重连",
			"task", taskID, "attempt", attempt, "from_seq", fromSeq,
			"backoff", backoff, "cause", streamErr)
		if err := waitArchivedRetry(ctx, &backoff,
			c.wsInitialBackoff, c.wsMaxBackoff,
			lived >= c.wsStableAfter); err != nil {
			return nil, err
		}
	}
}

// waitArchivedRetry 实现门闩的确定性退避：健康连接先把 backoff 复位为 initial，
// ctx 可取消等待，非健康失败后倍增并封顶。
//
// healthy 参数为什么能复位：连接活够 wsStableAfter 说明对端与网络都稳定，
// 上次的累积退避已无意义，重新从 initial 起步（与 WaitEvent 同语义）。
func waitArchivedRetry(ctx context.Context, backoff *time.Duration,
	initial, max time.Duration, healthy bool) error {
	if healthy {
		*backoff = initial
	}
	delay := *backoff
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if !healthy {
		next := delay * 2
		if next > max {
			next = max
		}
		*backoff = next
	}
	return nil
}
