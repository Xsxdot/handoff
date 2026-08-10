// controlplane 本机 machine outbox 到 desktop control plane 的投影泵。
//
// 职责：
//   - 从本机 machine cursor 之后顺序消费 durable machine events
//   - 复用 Projector 生成 control events，并触发上层实时广播回调
//   - 启动即补拉，之后由通知与周期兜底继续排空
//
// 边界：
//   - 不生成资源事实；事实已由 owner 事务写入 outbox
//   - 不持有 desktop DTO/Hub；广播由 Projector.OnApplied 注入
package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	localOutboxBatch    = 200
	localOutboxInterval = 250 * time.Millisecond
)

// LocalOutboxReader 是投影泵所需的最小持久化端口。
type LocalOutboxReader interface {
	CurrentCursor(ctx context.Context, machineID string) (int64, error)
	MachineEventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error)
}

// LocalOutboxPump 把 owner 与控制面共用数据库时的本机 outbox 顺序投影。
type LocalOutboxPump struct {
	reader    LocalOutboxReader
	machineID string
	projector *Projector
	log       *slog.Logger
	wake      chan struct{}
	drainMu   sync.Mutex
}

// NewLocalOutboxPump 创建本机 outbox 投影泵。
//
// 参数：
//   - reader: cursor 与 machine event 读取端口
//   - machineID: 本机稳定 Machine ID
//   - projector: 控制面事务投影器；OnApplied 可广播 desktop control event
//   - log: 结构化日志入口
func NewLocalOutboxPump(reader LocalOutboxReader, machineID string, projector *Projector, log *slog.Logger) *LocalOutboxPump {
	if log == nil {
		log = slog.Default()
	}
	return &LocalOutboxPump{
		reader: reader, machineID: machineID, projector: projector, log: log,
		wake: make(chan struct{}, 1),
	}
}

// Notify 提示投影泵尽快排空；重复通知会合并，durable outbox 不会丢事件。
func (p *LocalOutboxPump) Notify() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Drain 同步排空当前 cursor 之后的本机 outbox，返回实际新投影数量。
func (p *LocalOutboxPump) Drain(ctx context.Context) (int, error) {
	p.drainMu.Lock()
	defer p.drainMu.Unlock()
	started := time.Now()
	cursor, err := p.reader.CurrentCursor(ctx, p.machineID)
	if err != nil {
		return 0, fmt.Errorf("读取本机 outbox cursor: %w", err)
	}
	startCursor := cursor
	projected := 0
	for {
		events, err := p.reader.MachineEventsAfter(ctx, p.machineID, cursor, localOutboxBatch)
		if err != nil {
			return projected, fmt.Errorf("读取本机 outbox after=%d: %w", cursor, err)
		}
		for _, event := range events {
			controlEvent, applied, err := p.projector.Apply(ctx, event)
			if err != nil {
				return projected, fmt.Errorf("投影本机事件 seq=%d kind=%s: %w", event.MachineSeq, event.Kind, err)
			}
			cursor = event.MachineSeq
			if applied {
				projected++
				p.log.Debug("本机 outbox 事件已投影", "machine_id", p.machineID,
					"machine_seq", event.MachineSeq, "control_revision", controlEvent.ControlRevision,
					"kind", event.Kind)
			}
		}
		if len(events) < localOutboxBatch {
			break
		}
	}
	if projected > 0 {
		p.log.Info("本机 outbox 排空完成", "machine_id", p.machineID,
			"from_seq", startCursor, "through_seq", cursor, "projected", projected,
			"elapsed_ms", time.Since(started).Milliseconds())
	}
	return projected, nil
}

// Run 在上下文结束前持续投影；启动补拉与周期兜底保证通知丢失也能恢复。
func (p *LocalOutboxPump) Run(ctx context.Context) {
	ticker := time.NewTicker(localOutboxInterval)
	defer ticker.Stop()
	drain := func(reason string) {
		if _, err := p.Drain(ctx); err != nil && ctx.Err() == nil {
			p.log.Error("本机 outbox 投影失败", "machine_id", p.machineID, "reason", reason, "cause", err)
		}
	}
	drain("startup")
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			drain("notify")
		case <-ticker.C:
			drain("periodic")
		}
	}
}
