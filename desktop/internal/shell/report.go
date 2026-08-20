// 本文件负责按节奏把薄壳快照单向上报给 agentd。
//
// 职责：只负责「按节奏把快照发出去」，不决定快照内容；内容由 main.go 的
// snapshot 闭包从 traySync/traySyncErr 组装。
// 边界：上报失败只退避重试、不向调用方抛错；这条通道坏掉时托盘、控制台加载
// 与同步路仍必须照常工作。
package shell

import (
	"context"
	"log/slog"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// reportInterval 是薄壳状态上报间隔。
//
// 10s 与 agentd 侧 desktopStateTTL（30s）是三倍关系：容得下两次丢包，又不会
// 让退出的薄壳在控制台留下长时间幻影；改一个必须改另一个。
var reportInterval = 10 * time.Second

// ReportDeps 是上报循环的外部依赖。
type ReportDeps struct {
	// Put 把快照 PUT 到 agentd；错误只记日志，循环会继续。
	Put func(context.Context, proto.DesktopState) error
	// Now 取当前时间，供日志与测试注入；nil 使用 time.Now。
	Now func() time.Time
}

// RunReporter 周期性读取并上报当前薄壳快照，直到 ctx 取消。
//
// 参数：
//   - ctx：循环生命周期
//   - log：包级 logger 之外的日志入口，便于调用方统一装配
//   - snapshot：每轮重新组装状态，不能缓存第一次的结果
//   - d：PUT 与时钟依赖
//
// 返回：ctx 取消后返回。上报错误不会通过返回值传播。
// 注意：调用方应在独立 goroutine 中启动，不能挡住打开控制台的路径。
func RunReporter(ctx context.Context, log *slog.Logger, snapshot func() proto.DesktopState, d ReportDeps) {
	if log == nil {
		log = logger
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}

	put := func() {
		if d.Put == nil || snapshot == nil {
			log.Warn("薄壳状态上报依赖不完整")
			return
		}
		st := snapshot()
		if err := d.Put(ctx, st); err != nil {
			log.Warn("薄壳状态上报失败，稍后重试", "at", now(), "cause", err)
			return
		}
		log.Debug("薄壳状态已上报", "at", now(), "app_version", st.AppVersion, "sync_plan", st.SyncPlan)
	}

	// 先立即上报一次，让控制台在首次打开时不必等完整一个周期。
	put()
	ticker := time.NewTicker(reportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			put()
		}
	}
}
