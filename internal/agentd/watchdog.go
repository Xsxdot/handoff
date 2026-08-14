// watchdog.go —— 任务级卡住看门狗与 agentd 启动恢复。
//
// 职责：
//   - RunWatchdog：周期扫描 running/waiting_answer 任务，最新事件早于 stallTimeout
//     判定卡住，追加 stalled 事件并广播，唤醒协调者裁决（spec §8「任务级超时看门狗」）
//   - RecoverOnStartup：agentd 启动时对 running/waiting_answer/waiting_review 任务
//     逐个探测执行器存活（spec §8「agentd 崩溃后重启恢复」）；running/waiting_answer
//     不存活 → failed 事件 + 迁移 waiting_review 交协调者裁决；waiting_review 不存活
//     → 保持现状（本就是待审核终态，不追加事件不迁状态）；存活（含 waiting_review）
//     → 重建 SSE 订阅继续消费
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

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// watchdogTick 是 RunWatchdog 的生产扫描间隔（一分钟）。
//
// 为什么一分钟而不是更短：stalled 的语义是「长时间无事件产出」的告警而非实时
// 监控，触发后还有「只发一次」防刷屏（见 scanStalled），一分钟粒度足够且
// 扫描开销（全表 ListTasks + 每任务 LatestEvent）极低。
const watchdogTick = time.Minute

// stalledPayload 是 stalled 事件的 payload，供协调者快速判断卡了多久、卡在哪个事件后。
type stalledPayload struct {
	LastSeq int64  `json:"last_seq"` // 卡住判定时刻的最新事件 seq（事发锚点）；零事件任务无事件可锚，记 0
	Idle    string `json:"idle"`     // 空闲时长（如 "3h2m5s"，秒粒度）
}

// resourcePressurePayload 是高水位事件的载荷：协调者要靠这两个数字判断
// 该不该收敛，只说「压力大」没有任何操作价值。
type resourcePressurePayload struct {
	Used  int `json:"used"`
	Limit int `json:"limit"`
}

// taskProcPressurePayload 是任务级进程越线事件的载荷。
//
// 与 resourcePressurePayload 分开而不是复用：那个是机器级（used/limit 都是 uid
// 维度），这个是任务级，两者叠在一起时审核者要能一眼分清是谁在吃。
type taskProcPressurePayload struct {
	Used   int `json:"used"`
	Budget int `json:"budget"`
}

// taskProcCountFn 是「数某任务名下有几个进程」的测试缝。
// **生产路径恒为 Manager.TaskProcCount**（接线在 cmd/agentd.go），
// 非测试代码不得赋值。
var taskProcCountFn func(taskID string) (int, bool)

// SetTaskProcCounter 注入「数某任务名下进程数」的实现，由 agentd 启动时调用一次。
//
// 参数：fn 为按任务 ID 返回 (进程数, 是否可信) 的实现，生产恒传 Manager.TaskProcCount。
//
// 注意：测试直接赋包级 taskProcCountFn 即可，不需要走本函数。
func SetTaskProcCounter(fn func(taskID string) (int, bool)) {
	taskProcCountFn = fn
}

// RunWatchdog 启动任务卡住看门狗并持续运行，直到 ctx 取消。
//
// 参数：
//   - ctx: 控制看门狗生命周期；取消时立即退出（调用方负责在进程退出前 cancel，
//     避免 goroutine 泄漏）
//   - st: 持久化存储（任务列表与最新事件的数据源）
//   - hub: 实时路由（stalled 事件广播）
//   - stallTimeout: 判定「卡住」的空闲时长（最新事件距今超过它即触发）
//   - budget: 任务级进程数告警线，<=0 表示该档关闭（见 scanTaskProcs）
//   - hardLimit: 任务级进程数硬上限，<=0 表示该档关闭（见 scanTaskProcs）
//   - sweep: 清扫某任务残留进程的入口（接线传 mgr.SweepTaskProcs）
//   - log: 本模块日志入口
//
// 注意：
//   - 扫描间隔固定为 watchdogTick（每分钟）；测试需要注入短间隔时直接调用
//     同包的 runWatchdog 并传入 tick 参数
//   - 每轮扫描对 running/waiting_answer 任务判定；同一任务在 stalled 之后若无
//     活动（新事件或 task.UpdatedAt 前进）不会重复触发，有活动（如协调者 reply）
//     且 executor 仍无事件产出时下一轮会二次触发（「只发一次」按活动裁决，
//     设计见 scanStalled 的函数头 P1-15a）
//   - 每轮除卡住判定外，还判读一次进程余量高水位（见 scanPressure），越线沿
//     给每个活跃任务发一条 resource_pressure 事件唤醒协调者收敛；再按任务点名
//     进程数（见 scanTaskProcs），两档处置——告警线只唤醒，硬上限直接清扫
func RunWatchdog(ctx context.Context, st *store.Store, hub *Hub, stallTimeout time.Duration, budget, hardLimit int, sweep func(string), log *slog.Logger) {
	runWatchdog(ctx, st, hub, stallTimeout, watchdogTick, budget, hardLimit, sweep, log)
}

// runWatchdog 是看门狗的实现骨架：tick 间隔可注入（生产固定一分钟，
// 测试注入 10ms），其余语义与 RunWatchdog 一致。
func runWatchdog(ctx context.Context, st *store.Store, hub *Hub, stallTimeout time.Duration, tick time.Duration, budget, hardLimit int, sweep func(string), log *slog.Logger) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	log.Info("看门狗启动", "tick", tick, "stall_timeout", stallTimeout)
	pressure := false
	// 任务级进程告警的置位状态跨 tick 存活（与 pressure 同理由：不用包级变量，
	// 那会让两个 agentd 实例互相踩状态）
	taskFired := map[string]bool{}
	for {
		select {
		case <-ctx.Done():
			log.Info("看门狗退出", "cause", ctx.Err())
			return
		case <-ticker.C:
			scanStalled(st, hub, stallTimeout, log)
			pressure = scanPressure(st, hub, pressure, log)
			scanTaskProcs(st, hub, budget, hardLimit, taskFired, sweep, log)
		}
	}
}

// scanStalled 扫描一轮：对 running/waiting_answer 任务判定是否卡住并触发 stalled。
//
// 为什么只扫 running/waiting_answer：pending 尚在启动中（无事件正常）、
// waiting_review 卡住说明协调者还没处理（那是人的节奏，不归看门狗）、
// completed/failed 已终结；只有「executor 应该在干活」的状态才是卡住判定对象。
// waiting_answer 卡住正是「审批挂起过夜」场景——executor 等协调者答复等太久，
// 值得再发一条 stalled 把协调者拽回来。
//
// 活动基线（P1-15 重新设计）：
//   - 最新事件时间；零事件任务（建好后从未产出事件，即「静默挂起」）以
//     task.UpdatedAt 兜底——没有任何事件可锚，任务行是仅有的活动痕迹
//   - 每次触发后以「新 stalled 事件成为最新事件」自然重置基线，无内存记忆
//
// 「只发一次」裁决（P1-15a 重新设计）：最新事件已是 stalled 时，仅当任务在
// 该 stalled 之后出现过活动（task.UpdatedAt 前进）才允许重发。为什么以
// UpdatedAt 变化为二次触发的准绳：reply 回程的 AnswerTicket 会刷新任务的
// updated_at（见 store.AnswerTicket），resumeIfIdle 回迁 running 也会刷新——
// 正是「已 stalled → 协调者回答 → executor 仍然死着」这个最需要二次告警的
// 场景（旧实现永远不再告警）；而普通无活动状态下 updated_at 停在 stalled
// 事件之前，每轮扫描照旧跳过，不产生事件风暴。新 stalled 事件落库时间晚于
// 当时 updated_at，下一轮自然回到「不重发」分支。
//
// 每轮扫描独立执行，与其他轮之间无共享状态：两种裁决都只依赖持久化事实
// （最新事件 + task.UpdatedAt），重启后语义依然正确。
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
		var (
			lastEv   *proto.Event // 最新事件；零事件任务为 nil
			baseline time.Time    // 空闲判定的活动基线
		)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// 零事件任务（P1-15b）：以 task.UpdatedAt 兜底，纳入卡住监控——
			// 旧实现直接跳过，这类「静默挂起」根本不受监控
			baseline = t.UpdatedAt
		case err != nil:
			log.Error("看门狗读取任务最新事件失败", "task", t.ID, "cause", err)
			continue
		default:
			lastEv = last
			baseline = last.CreatedAt
		}
		idle := time.Since(baseline)
		if idle < stallTimeout {
			continue // 新鲜任务：最近有活动（事件产出或状态/字段变更），不判定卡住
		}
		if lastEv != nil && lastEv.Type == proto.EventTypeStalled && !t.UpdatedAt.After(lastEv.CreatedAt) {
			// 「只发一次」防事件风暴：最新事件已是 stalled 且之后无活动
			// （updated_at 未前进），不重发——why 见函数头 P1-15a
			continue
		}
		lastSeq := int64(0)
		if lastEv != nil {
			lastSeq = lastEv.Seq
		}
		evt, err := st.AppendEvent(t.ID, proto.EventTypeStalled, stalledPayload{
			LastSeq: lastSeq, Idle: idle.Round(time.Second).String(),
		})
		if err != nil {
			log.Error("追加 stalled 事件失败", "task", t.ID, "cause", err)
			continue
		}
		hub.Publish(evt)
		fired++
		log.Warn("任务卡住，触发 stalled 事件", "task", t.ID,
			"idle", idle.Round(time.Second).String(), "last_seq", lastSeq)
	}
	log.Debug("看门狗扫描完成", "checked", checked, "fired", fired)
}

// scanPressure 判读一次进程余量，越线沿时给每个活跃任务发一条高水位事件。
//
// 参数：
//   - active: 上一轮的置位状态（越线后为 true）
//   - 其余同 scanStalled
//
// 返回：本轮结束后的置位状态，由调用方持有并回传——**不用包级变量**，
// 那会让两个 agentd 实例（测试里常见）互相踩状态。
//
// 三条语义：
//   - 越线（!active && NearFull）：对每个活跃任务发一次，置位
//   - 已置位且仍在高水位：不重发。事件风暴会把协调者的会话刷爆，
//     反而淹掉真正要处置的工单
//   - 回落到水位线以下：复位，下次越线可再发
//
// 读数未知时**原样返回 active，什么都不做**：把「量不出来」当成「回落了」，
// 会让下一次真越线因为状态错乱而漏报——漏报一次高水位，就等于回到事故当天
// 那个「第一个信号是整机瘫痪」的处境。
func scanPressure(st *store.Store, hub *Hub, active bool, log *slog.Logger) bool {
	a := admissionFn()
	if !a.Known {
		log.Debug("进程余量未知，跳过高水位判读")
		return active
	}
	if !a.NearFull() {
		if active {
			log.Info("进程余量已回落到水位线以下", "used", a.Used, "limit", a.Limit)
		}
		return false
	}
	if active {
		return true // 仍在高水位，已告警过，不重发
	}
	tasks, err := st.ListTasks()
	if err != nil {
		log.Error("高水位告警读取任务列表失败", "cause", err)
		return false // 没发出去就不置位，下一轮重试
	}
	fired := 0
	for _, t := range tasks {
		// 终态任务不需要知道机器压力大——它们已经不会再 fork 任何东西了。
		// 用 IsTerminal 取反而不是枚举活跃态：新增状态时这里自动跟上
		if t.State.IsTerminal() {
			continue
		}
		evt, aerr := st.AppendEvent(t.ID, proto.EventTypeResourcePressure,
			resourcePressurePayload{Used: a.Used, Limit: a.Limit})
		if aerr != nil {
			log.Error("追加高水位事件失败", "task", t.ID, "cause", aerr)
			continue
		}
		hub.Publish(evt)
		fired++
	}
	log.Warn("执行机进程余量达高水位，已告警活跃任务",
		"used", a.Used, "limit", a.Limit, "fired", fired)
	return true
}

// scanTaskProcs 按任务点名进程数，两档处置。
//
// 参数：
//   - budget: 告警线，<=0 表示该档关闭
//   - hardLimit: 硬上限，<=0 表示该档关闭
//   - fired: 每任务的告警置位状态，由调用方持有并跨轮传递（**不用包级变量**，
//     那会让两个 agentd 实例互相踩状态——沿用 scanPressure 的同一条理由）
//   - sweep: 清扫某任务残留进程的入口
//
// 三条语义（与 scanPressure 同构）：
//   - 越线且未置位：发一次 task_proc_pressure 并置位
//   - 仍越线且已置位：不重发。事件风暴会把协调者的会话刷爆
//   - 回落到预算以下：复位，下次越线可再发
//
// 硬上限档是本仓库第一次让 agentd 在无人裁决的情况下杀进程，所以：读数不可信
// 一律什么都不做；理由里必须写上 used 与 hardLimit 两个真实数字，让审核者
// 事后能判断杀得对不对。
func scanTaskProcs(st *store.Store, hub *Hub, budget, hardLimit int,
	fired map[string]bool, sweep func(string), log *slog.Logger) {
	// 两档都关 = 完全不启用。这里直接返回而不是往下走到「数了但不处置」——
	// Footprint 每次都要枚举全系统进程表，白数是实打实的开销
	if budget <= 0 && hardLimit <= 0 {
		return
	}
	if taskProcCountFn == nil {
		return
	}
	tasks, err := st.ListTasks()
	if err != nil {
		log.Error("任务进程点名读取任务列表失败", "cause", err)
		return
	}
	for _, t := range tasks {
		// 终态任务已经不会再 fork 任何东西。用 IsTerminal 取反而不是枚举活跃态：
		// 新增状态时这里自动跟上
		if t.State.IsTerminal() {
			continue
		}
		n, ok := taskProcCountFn(t.ID)
		if !ok {
			continue // 数不出来就什么都不做，连置位状态都不动
		}
		if hardLimit > 0 && n > hardLimit {
			log.Error("任务进程数超过硬上限，强制回收", "task", t.ID, "used", n, "hard_limit", hardLimit)
			sweep(t.ID)
			reason := fmt.Sprintf("任务进程数 %d 超过硬上限 %d，已强制回收", n, hardLimit)
			if err := transitFailedWithEvent(st, hub, t.ID, reason, log); err != nil {
				log.Error("强制回收后落 failed 失败", "task", t.ID, "cause", err)
			}
			delete(fired, t.ID)
			continue
		}
		if budget <= 0 {
			continue
		}
		if n <= budget {
			if fired[t.ID] {
				log.Info("任务进程数已回落到预算以下", "task", t.ID, "used", n, "budget", budget)
			}
			delete(fired, t.ID)
			continue
		}
		if fired[t.ID] {
			continue // 仍越线，已告警过，不重发
		}
		evt, aerr := st.AppendEvent(t.ID, proto.EventTypeTaskProcPressure,
			taskProcPressurePayload{Used: n, Budget: budget})
		if aerr != nil {
			log.Error("追加任务进程越线事件失败", "task", t.ID, "cause", aerr)
			continue // 没发出去就不置位，下一轮重试
		}
		// 必须广播：只落库的话审核者要主动 show 才看得见，等于没告警（B91 先例）
		hub.Publish(evt)
		fired[t.ID] = true
		log.Warn("任务进程数超过预算，已告警", "task", t.ID, "used", n, "budget", budget)
	}
}

// transitFailedWithEvent 把任务迁移到 failed 终态、追加带理由的 failed 事件并广播。
//
// 参数：reason 进事件 payload（硬上限分支必须带 used/hardLimit 真实数字，
// 这是不可逆动作，审核者事后要能判断杀得对不对）
//
// 顺序是「先迁状态 → 追加事件 → 广播」：与 handleResult 一致——事件一落库就
// 可被 WS 重放读到，状态必须先就位，否则审核者在一个仍 running 的任务上
// 看到 failed 事件会困惑（handleResult 函数头的同一条理由）。
//
// 为什么不用 reconcileExecutorGone：那个收的是 waiting_review（executor 死了
// 交协调者裁决），本函数是终态 failed——进程失控的强制回收没有「继续」选项。
func transitFailedWithEvent(st *store.Store, hub *Hub, taskID, reason string, log *slog.Logger) error {
	if err := st.UpdateTaskState(taskID, proto.TaskStateFailed); err != nil {
		return err
	}
	// 终态迁移统一作废挂起工单并留痕（B63）：进程失控的强制回收同样适用。
	// 排在 failed 事件之前：LatestEvent 锚定 failed，审核者看事件流不会被
	// 审计噪音挡住终点
	voidTicketsWithAudit(st, taskID, reason, log)
	evt, err := st.AppendEvent(taskID, proto.EventTypeFailed, newFailedPayload(reason, "", ""))
	if err != nil {
		return err
	}
	hub.Publish(evt)
	return nil
}

// RecoverOnStartup 在 agentd 启动时恢复未终结任务（spec §8 的 agentd 重启恢复）：
// 对全部 running/waiting_answer/waiting_review 任务调用 probe 探测执行器存活——
//   - running/waiting_answer 不存活：追加 failed 事件（原因固定为「agentd 重启后
//     执行器已不在」）并迁移 waiting_review，交协调者裁决（失败现场留在事件里，
//     协调者凭 tasks/attach 可见）；该任务的挂起工单一并作废（P1-16，见
//     VoidPendingTickets 的语义），事件照常广播，启动期无人订阅则由客户端凭
//     seq cursor 补拉
//   - waiting_review 不存活：保持现状即可——它本来就是待审核终态，等待协调者
//     裁决（continue 重派 / done 归档）是既有的终态语义，追加 failed 事件或再迁
//     状态只会产生噪音，**不**复用 running/waiting_answer 的 failed 迁移路径
//   - 存活（含 waiting_review）：重建 SSE 订阅继续消费——重建动作由 probe 闭包
//     内部完成（见 seam 说明），本函数只记录结论日志；waiting_review 存活时
//     同样重建，续接依赖的会话上下文（opencode serve 进程与 SSE 会话）才不至于
//     随 agentd 进程消亡而丢失，但**不改任务状态**（它该留在 waiting_review 等人）
//
// 参数：
//   - st: 持久化存储
//   - hub: 实时路由（failed 事件广播）
//   - probe: 探活闭包 func(taskID) bool；返回 true 表示执行器存活且（对支持恢复
//     的 adapter）事件流已重建。这是本函数保持「不带 adapter 引用」签名的接口
//     缝隙（seam）：存活的「重建订阅 + 重启中介循环」动作必须封装在闭包内部，
//     接线见 cmd/agentd.go（mgr.ResumeTask）与 Manager.ResumeTask 的 doc
//   - sweep: 残留进程清扫闭包 func(taskID)；**无条件调用**——executor 已不在时
//     残留进程该不该收与任务停在哪个状态无关（waiting_review 同样照收，见下）。
//     与 probe 是同款注入缝（避免 watchdog 直接接触 adapter），并排放读起来才
//     是一回事；接线见 cmd/agentd.go（mgr.SweepTaskProcs）
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
//   - 探活本身无副作用：waiting_review 任务无论 probe 结果如何都不改状态，
//     状态由协调者动作（continue/done）驱动
func RecoverOnStartup(st *store.Store, hub *Hub, probe func(taskID string) bool,
	sweep func(taskID string), log *slog.Logger) error {
	tasks, err := st.ListTasks()
	if err != nil {
		return fmt.Errorf("启动恢复读取任务列表: %w", err)
	}
	recovered, failed, kept := 0, 0, 0
	for _, t := range tasks {
		if t.State != proto.TaskStateRunning && t.State != proto.TaskStateWaitingAnswer && t.State != proto.TaskStateWaitingReview {
			continue // 其余状态不需要恢复：pending 未启动、终态已结束
		}
		if probe(t.ID) {
			recovered++
			log.Info("执行器存活，重建订阅继续消费", "task", t.ID, "alive", true, "state", t.State)
			continue
		}
		if t.State == proto.TaskStateWaitingReview {
			// waiting_review 本来就是待审核终态：executor 不在不追加事件、不迁移
			// 状态——协调者裁决（continue 重派 / done 归档）才是它该走的路。
			//
			// 但**残留进程照收**：那条理由说的是状态与事件噪音，不是资源。
			// 2026-08-12 事故里两个任务最终正停在这个状态，若跟着一起跳过，
			// 清扫会在最该工作的场景里恰好不工作
			kept++
			log.Info("waiting_review 任务 executor 已不在，保持现状等协调者裁决", "task", t.ID, "alive", false)
			sweep(t.ID)
			continue
		}
		failed++
		log.Info("执行器已不在，任务转 waiting_review 交协调者", "task", t.ID, "alive", false, "state", t.State)
		reconcileExecutorGone(st, hub, t.ID, "agentd 重启后执行器已不在", log, sweep)
	}
	log.Info("启动恢复完成", "recovered", recovered, "failed", failed, "waiting_review_kept", kept)
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
