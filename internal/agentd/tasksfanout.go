// 本文件实现任务列表的跨机汇总：GET /api/tasks?scope=all。
//
// 职责：
//   - 把本机任务与 mirror_tasks 里的远端快照合并成 §5.3 的信封
//   - 给每台机器留一行 MachineStatus（快照的 fetched_at 即数据新旧）
//
// 边界：
//   - **不现场扇出**：看板 2.5s 轮询打的是本机，快慢与远端可达性解耦。
//     远端部分取自镜像快照（§6.3），刷新由镜像的「事件即门铃 + 30s 慢对账」
//     负责——这是本设计里「看板不被远端抖动波及」的兑现处
//   - 不改无参数时的响应形状：裸数组 []TaskView，与 W2 契约逐字节一致
package agentd

import (
	"context"
	"sort"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// tasksAll 汇总本机与全部镜像任务为 §5.3 信封。
//
// 返回值恒有效：本机列表读不出来时照常返回信封（镜像部分仍可用）；
// 机器应答情况逐台进 Machines，不因单台缺失让整个响应失败。
func (s *Server) tasksAll(ctx context.Context) proto.TasksResp {
	tasks, err := s.st.ListTasks()
	if err != nil {
		s.log.Error("任务汇总：查询本机任务失败", "cause", err)
		tasks = []proto.Task{}
	}
	idx := s.projectIndex()
	views := make([]proto.TaskView, 0, len(tasks))
	for _, t := range tasks {
		t.ProjectID = idx.projectIDOf(t.RepoPath) // 读时 join，不落库
		views = append(views, proto.TaskView{Task: t, Watchers: s.hub.Watchers(t.ID)})
	}

	mirrors, err := s.st.ListMirrorTasks()
	if err != nil {
		s.log.Error("任务汇总：查询镜像快照失败", "cause", err)
		mirrors = []store.MirrorTask{}
	}
	// target → 该机最新一条快照的时刻（机器应答行的数据新旧）
	fetched := map[string]time.Time{}
	for _, mt := range mirrors {
		t := mt.Task
		t.Machine = mt.Target // 汇总方盖章：这条任务是从哪个 target 拉来的
		views = append(views, proto.TaskView{Task: t, Watchers: s.hub.Watchers(t.ID)})
		if prev, ok := fetched[mt.Target]; !ok || mt.FetchedAt.After(prev) {
			fetched[mt.Target] = mt.FetchedAt
		}
	}

	machines := []proto.MachineStatus{{Name: "", Ok: true, FetchedAt: time.Now().UTC()}}
	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := proto.MachineStatus{Name: name}
		if ts, ok := fetched[name]; ok {
			st.Ok = true
			st.FetchedAt = ts
		} else {
			// 「没有快照」不等于「不可达」：可能是它上面根本没有任务，或镜像
			// 还没跑到它。报文必须把两种可能都说出来，不能武断写成「不可达」
			st.Ok = false
			st.Error = "尚无该机器的镜像快照（可能从未可达，或它上面没有任务）"
		}
		machines = append(machines, st)
	}

	s.log.Info("任务汇总完成", "local", len(tasks), "mirrored", len(mirrors),
		"machines", len(machines))
	return proto.TasksResp{Machines: machines, Tasks: views}
}
