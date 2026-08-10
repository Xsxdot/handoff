// status.go —— GET /api/status 的服务端聚合。
//
// 职责：
//   - Manager.Status：把版本、配置、任务计数、非终结任务的存活结论聚成一个响应
//   - 带时限地逐个探活（prober 可选接口），三态如实返回
//
// 边界：
//   - **只读**：不改任务状态、不发事件、不回收任何 executor 资源。发现失配只
//     报告，修复归 continue/stop 那条既有路径（见 spec §1.4「不兼做恢复」）
//   - 不做周期性探活：本文件只在有人调 status 时才跑，与 Spec A §2.2
//     「不新增周期性探活」不冲突——那条拒绝的是后台定时扫
package agentd

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/xushixin/handoff/internal/buildinfo"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

const (
	// probeTimeout 是单个任务的探活时限。
	probeTimeout = 2 * time.Second
	// probeTotalTimeout 是一次 status 里全部探活的总时限：活跃任务再多，
	// 这条命令也不能变成慢命令。超出部分一律记 unknown。
	probeTotalTimeout = 10 * time.Second
)

// prober 是「只读探测 executor 是否存活」的可选 adapter 能力
//（四个真实 adapter 均实现，fake 不实现）。
//
// 为什么是可选接口而不是加进 executor.Adapter：不支持探活的 adapter 一律按
// unknown 处理是自然语义，五动作核心契约不该为一个诊断功能扩面
//（与 restorer / reaper / volatilePermitter 同一套路数）。
type prober interface {
	Probe(executor.ProbeReq) (executor.ProbeOutcome, error)
}

// Status 聚合本 agentd 的可用性与身份信息。
//
// 返回：
//   - StatusResp：版本、监听地址、DataDir、执行者清单、六状态计数、活跃任务
//     及其存活结论。StartedAt 由调用方（server 层）填，manager 不持有它
//   - err：只有查询任务列表失败才返回错误；探活失败不是错误，落到单个任务的
//     Live=unknown 上
func (m *Manager) Status() (*proto.StatusResp, error) {
	tasks, err := m.st.ListTasks()
	if err != nil {
		m.log.Error("状态聚合：查询任务列表失败", "cause", err)
		return nil, fmt.Errorf("查询任务列表: %w", err)
	}
	ver, ok := buildinfo.Read()
	if !ok {
		m.log.Warn("状态聚合：读不到构建标识，版本字段留空")
	}
	names := registeredNames(m.ads)
	sort.Strings(names)

	resp := &proto.StatusResp{
		Version:         ver,
		Listen:          m.cfg.Listen,
		DataDir:         m.cfg.DataDir,
		Executors:       names,
		DefaultExecutor: m.cfg.Executor.Default,
		TaskCounts:      map[string]int{},
		Active:          []proto.ActiveTask{},
	}
	// 六个状态的键恒存在：缺键与零值对消费方是两回事
	for _, s := range []proto.TaskState{
		proto.TaskStatePending, proto.TaskStateRunning, proto.TaskStateWaitingAnswer,
		proto.TaskStateWaitingReview, proto.TaskStateCompleted, proto.TaskStateFailed,
	} {
		resp.TaskCounts[string(s)] = 0
	}

	var active []proto.Task
	for _, t := range tasks {
		resp.TaskCounts[string(t.State)]++
		if !isTerminalState(t.State) {
			active = append(active, t)
		}
	}
	resp.Active = m.probeActive(active)
	m.log.Info("状态聚合完成", "tasks", len(tasks), "active", len(active),
		"executors", len(names))
	return resp, nil
}

// isTerminalState 判断状态是否终结（completed / failed）。
func isTerminalState(s proto.TaskState) bool {
	return s == proto.TaskStateCompleted || s == proto.TaskStateFailed
}

// probeActive 对每个非终结任务做一次只读探活，共享一份总时限预算。
func (m *Manager) probeActive(tasks []proto.Task) []proto.ActiveTask {
	out := make([]proto.ActiveTask, 0, len(tasks))
	deadline := time.Now().Add(probeTotalTimeout)
	for _, t := range tasks {
		at := proto.ActiveTask{
			ID: t.ID, Name: t.Name, State: string(t.State),
			Executor: t.Executor, RepoPath: t.RepoPath,
		}
		// 老任务的 Executor 为空，回退缺省——展示上不该出现空执行者
		if at.Executor == "" {
			at.Executor = m.cfg.Executor.Default
		}
		at.Live, at.Note = m.probeOne(t, time.Until(deadline))
		out = append(out, at)
	}
	return out
}

// probeOne 对单个任务做一次只读探活，返回三态之一与一句话理由。
//
// 参数：
//   - budget: 本次探测可用的时限（受总预算约束，上限 probeTimeout）
//
// 注意：
//   - **超时归 unknown，不归 dead**。假阳性是诊断命令最贵的失败模式——一条会
//     说谎的诊断命令比没有更糟，因为你会信它
//   - 探测在 goroutine 里跑、用带缓冲的通道回收结果：超时后那个 goroutine 仍
//     能把结果写进缓冲并正常退出，不会泄漏；底层探针本身也都是有界的
//     （HTTP 客户端带超时、tmux has-session 秒回）
func (m *Manager) probeOne(t proto.Task, budget time.Duration) (live, note string) {
	if budget <= 0 {
		m.log.Warn("状态探活：总时限已用尽，该任务记为未知", "task", t.ID)
		return proto.LiveUnknown, "探活总时限已用尽"
	}
	if budget > probeTimeout {
		budget = probeTimeout
	}
	ad, err := m.adapterFor(t.ID)
	if err != nil {
		m.log.Warn("状态探活：执行者未注册，结论未知", "task", t.ID, "cause", err)
		return proto.LiveUnknown, fmt.Sprintf("执行者未注册：%v", err)
	}
	pr, ok := ad.(prober)
	if !ok {
		m.log.Info("状态探活：该 adapter 不支持探活，结论未知", "task", t.ID)
		return proto.LiveUnknown, "该执行者不支持探活"
	}

	type result struct {
		out executor.ProbeOutcome
		err error
	}
	ch := make(chan result, 1) // 缓冲 1：超时后 goroutine 仍能写入并退出
	go func() {
		o, e := pr.Probe(executor.ProbeReq{
			TaskID:    t.ID,
			TaskDir:   filepath.Join(m.cfg.DataDir, "tasks", t.ID),
			SessionID: t.ExecutorSession,
		})
		ch <- result{o, e}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			m.log.Warn("状态探活：探针失败，结论未知", "task", t.ID, "cause", r.err)
			return proto.LiveUnknown, fmt.Sprintf("探活失败：%v", r.err)
		}
		if r.out.Alive {
			m.log.Info("状态探活：executor 存活", "task", t.ID)
			return proto.LiveAlive, ""
		}
		m.log.Info("状态探活：executor 已不在", "task", t.ID, "note", r.out.Note)
		return proto.LiveDead, r.out.Note
	case <-time.After(budget):
		m.log.Warn("状态探活：超时，结论未知（不判死）", "task", t.ID, "budget", budget)
		return proto.LiveUnknown, "探活超时"
	}
}
