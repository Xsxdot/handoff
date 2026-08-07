// watchdog.go —— 任务级卡住看门狗与 agentd 启动恢复。
//
// 职责：
//   - RunWatchdog：周期扫描 running/waiting_answer 任务，最新事件早于 stallTimeout
//     判定卡住，追加 stalled 事件并广播，唤醒审核者裁决（spec §8「任务级超时看门狗」）
//   - RecoverOnStartup：agentd 启动时对 running/waiting_answer 任务逐个探测执行器
//     存活（spec §8「agentd 崩溃后重启恢复」）；不存活 → failed 事件 + 迁移
//     waiting_review 交审核者裁决；存活 → 重建 SSE 订阅继续消费
//
// 边界：
//   - 不做状态机之外的业务决策：stalled 只唤醒不改状态（executor 可能仍在干活，
//     只是没有事件产出），恢复的 failed 迁移固定落 waiting_review，不自动重试
//   - 不直接接触 adapter：重建订阅的具体动作经探活闭包注入（见 RecoverOnStartup
//     的 seam 说明），本文件只负责「探测结果 → 事件/状态」的翻译
//   - tick 间隔是 runWatchdog 的参数（测试注入 10ms），RunWatchdog 固定每分钟一次
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// watchdogTick 是 RunWatchdog 的生产扫描间隔（一分钟）。
//
// 为什么一分钟而不是更短：stalled 的语义是「长时间无事件产出」的告警而非实时
// 监控，触发后还有「只发一次」防刷屏（见 scanStalled），一分钟粒度足够且
// 扫描开销（全表 ListTasks + 每任务 LatestEvent）极低。
const watchdogTick = time.Minute

// stalledPayload 是 stalled 事件的 payload，供审核者快速判断卡了多久、卡在哪个事件后。
type stalledPayload struct {
	LastSeq int64  `json:"last_seq"` // 卡住判定时刻的最新事件 seq（事发锚点）
	Idle    string `json:"idle"`     // 空闲时长（如 "3h2m5s"，秒粒度）
}

// RunWatchdog 启动任务卡住看门狗并持续运行，直到 ctx 取消。
//
// 参数：
//   - ctx: 控制看门狗生命周期；取消时立即退出（调用方负责在进程退出前 cancel，
//     避免 goroutine 泄漏）
//   - st: 持久化存储（任务列表与最新事件的数据源）
//   - hub: 实时路由（stalled 事件广播）
//   - stallTimeout: 判定「卡住」的空闲时长（最新事件距今超过它即触发）
//   - log: 本模块日志入口
//
// 注意：
//   - 扫描间隔固定为 watchdogTick（每分钟）；测试需要注入短间隔时直接调用
//     同包的 runWatchdog 并传入 tick 参数
//   - 每轮扫描对 running/waiting_answer 任务判定；同一任务在 stalled 之后若无
//     新事件不会重复触发（「只发一次」防事件风暴，见 scanStalled 的 why 注释）
func RunWatchdog(ctx context.Context, st *store.Store, hub *Hub, stallTimeout time.Duration, log *slog.Logger) {
	runWatchdog(ctx, st, hub, stallTimeout, watchdogTick, log)
}

// runWatchdog 是看门狗的实现骨架：tick 间隔可注入（生产固定一分钟，
// 测试注入 10ms），其余语义与 RunWatchdog 一致。
func runWatchdog(ctx context.Context, st *store.Store, hub *Hub, stallTimeout time.Duration, tick time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	log.Info("看门狗启动", "tick", tick, "stall_timeout", stallTimeout)
	for {
		select {
		case <-ctx.Done():
			log.Info("看门狗退出", "cause", ctx.Err())
			return
		case <-ticker.C:
			scanStalled(st, hub, stallTimeout, log)
		}
	}
}

// scanStalled 扫描一轮：对 running/waiting_answer 任务判定是否卡住并触发 stalled。
//
// 为什么只扫 running/waiting_answer：pending 尚在启动中（无事件正常）、
// waiting_review 卡住说明审核者还没处理（那是人的节奏，不归看门狗）、
// completed/failed 已终结；只有「executor 应该在干活」的状态才是卡住判定对象。
// waiting_answer 卡住正是「审批挂起过夜」场景——executor 等审核者答复等太久，
// 值得再发一条 stalled 把审核者拽回来。
//
// 每轮扫描独立执行，与其他轮之间无共享状态：stalled 的「只发一次」由
// 「最新事件是否为 stalled」这条持久化事实裁决（见下），不依赖内存记忆，
// 重启后语义依然正确。
func scanStalled(st *store.Store, hub *Hub, stallTimeout time.Duration, log *slog.Logger) {
	tasks, err := st.ListTasks()
	if err != nil {
		log.Error("看门狗扫描任务列表失败", "cause", err)
		return
	}
	checked, fired := 0, 0
	for _, t := range tasks {
		if t.State != proto.TaskStateRunning && t.State != proto.TaskStateWaitingAnswer {
			continue
		}
		checked++
		last, err := st.LatestEvent(t.ID)
		if errors.Is(err, store.ErrNotFound) {
			// 无事件的任务（理论上不存在：dispatch 后必有事件），防御性跳过
			log.Debug("任务无事件，跳过卡住判定", "task", t.ID)
			continue
		}
		if err != nil {
			log.Error("看门狗读取任务最新事件失败", "task", t.ID, "cause", err)
			continue
		}
		idle := time.Since(last.CreatedAt)
		if idle < stallTimeout {
			continue // 新鲜任务：最近有事件产出，不判定卡住
		}
		if last.Type == proto.EventTypeStalled {
			// 「只发一次」防事件风暴：最新事件已是 stalled 说明上次触发后没有
			// 新事件。若每分钟无条件重发，审核者会被 stalled 刷屏淹没；而新事件
			// 一旦到来，最新事件就被顶成别的类型，任务重新具备触发条件——
			// 用「最新事件类型」做裁决，天然只依赖持久化事实，重启也正确
			continue
		}
		evt, err := st.AppendEvent(t.ID, proto.EventTypeStalled, stalledPayload{
			LastSeq: last.Seq, Idle: idle.Round(time.Second).String(),
		})
		if err != nil {
			log.Error("追加 stalled 事件失败", "task", t.ID, "cause", err)
			continue
		}
		hub.Publish(evt)
		fired++
		log.Warn("任务卡住，触发 stalled 事件", "task", t.ID,
			"idle", idle.Round(time.Second).String(), "last_seq", last.Seq)
	}
	log.Debug("看门狗扫描完成", "checked", checked, "fired", fired)
}

// RecoverOnStartup 在 agentd 启动时恢复未终结任务（spec §8 的 agentd 重启恢复）：
// 对全部 running/waiting_answer 任务调用 probe 探测执行器存活——
//   - 不存活：追加 failed 事件（原因固定为「agentd 重启后执行器已不在」）并迁移
//     waiting_review，交审核者裁决（失败现场留在事件里，审核者凭 tasks/attach
//     可见）；事件照常广播，启动期无人订阅则由客户端凭 seq cursor 补拉
//   - 存活：重建 SSE 订阅继续消费——重建动作由 probe 闭包内部完成（见 seam 说明），
//     本函数只记录结论日志
//
// 参数：
//   - st: 持久化存储
//   - hub: 实时路由（failed 事件广播）
//   - probe: 探活闭包 func(taskID) bool；返回 true 表示执行器存活且（对支持恢复
//     的 adapter）事件流已重建。这是本函数保持「不带 adapter 引用」签名的接口
//     缝隙（seam）：存活的「重建订阅 + 重启中介循环」动作必须封装在闭包内部，
//     接线见 cmd/agentd.go（mgr.ResumeTask）与 Manager.ResumeTask 的 doc
//   - log: 本模块日志入口
//
// 返回：
//   - 任务列表读取失败时返回错误（恢复不可靠，让 agentd 启动失败暴露问题）；
//     单个任务的恢复失败（事件追加/状态迁移失败）只记录日志，不中断整体恢复
//
// 注意：
//   - 必须在 HTTP 服务开始前调用（cmd/agentd.go 的 bootstrap 顺序保证）
//   - waiting_answer 任务迁移 waiting_review 需经 running 两跳（waiting_answer→
//     waiting_review 直接迁移不在 6 状态迁移表中，见 recoverTransit）
func RecoverOnStartup(st *store.Store, hub *Hub, probe func(taskID string) bool, log *slog.Logger) error {
	tasks, err := st.ListTasks()
	if err != nil {
		return fmt.Errorf("启动恢复读取任务列表: %w", err)
	}
	recovered, failed := 0, 0
	for _, t := range tasks {
		if t.State != proto.TaskStateRunning && t.State != proto.TaskStateWaitingAnswer {
			continue // 其余状态不需要恢复：pending 未启动、终态已结束、waiting_review 是人的节奏
		}
		if probe(t.ID) {
			recovered++
			log.Info("执行器存活，重建订阅继续消费", "task", t.ID, "alive", true, "state", t.State)
			continue
		}
		failed++
		log.Info("执行器已不在，任务转 waiting_review 交审核者", "task", t.ID, "alive", false, "state", t.State)
		evt, err := st.AppendEvent(t.ID, proto.EventTypeFailed, failedPayload{FailReason: "agentd 重启后执行器已不在"})
		if err != nil {
			log.Error("追加恢复失败事件失败", "task", t.ID, "cause", err)
			continue
		}
		if err := recoverTransit(st, t.ID, t.State); err != nil {
			log.Error("恢复失败任务迁移 waiting_review 失败", "task", t.ID, "cause", err)
			continue
		}
		hub.Publish(evt)
	}
	log.Info("启动恢复完成", "recovered", recovered, "failed", failed)
	return nil
}

// recoverTransit 把恢复失败的任务迁移到 waiting_review。
//
// why（两跳）：状态机只有 6 状态，迁移表中 waiting_answer 只允许去 running/failed，
// 直接迁 waiting_review 会被 ErrBadTransit 拒绝；经 running 中转的两跳
// （waiting_answer→running→waiting_review）均合法，与 manager.transitToReviewRetry
// 的竞态兜底路径同构。running 任务则直跳。首跳失败（ErrBadTransit）时继续尝试
// 第二跳：失败意味着状态已被并发变更，以最新快照为准补跳即可收敛。
func recoverTransit(st *store.Store, taskID string, cur proto.TaskState) error {
	if cur == proto.TaskStateWaitingAnswer {
		if err := st.UpdateTaskState(taskID, proto.TaskStateRunning); err != nil && !errors.Is(err, store.ErrBadTransit) {
			return err
		}
	}
	return st.UpdateTaskState(taskID, proto.TaskStateWaitingReview)
}
