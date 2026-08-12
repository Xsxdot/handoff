// 本文件实现项目树的跨机汇总：GET /api/projects/tree?scope=all。
//
// 职责：
//   - 对每台 target 现场取它的**单机**树，按 project_id 与本机的树合并
//   - 给每台机器的 location 盖上 machine 名
//   - 无论成败，每台机器都在响应的 machines 里留一行
//
// 边界：
//   - 现场扇出（不读缓存）：项目登记是低频操作，实时性换简单。任务列表的
//     scope=all 走的是镜像快照（见 tasksfanout），两者刻意不同
//   - 一跳封顶：扇出请求带 X-Handoff-Forwarded，对端不再扇出
//   - 单台失败不影响整体：整体恒 200
package agentd

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// treeFanoutBudget 是整轮扇出的总预算（§5.2：短于任何调用方超时）。
const treeFanoutBudget = 3 * time.Second

// buildTreeAll 汇总本机与全部 target 的项目树。
//
// 返回值恒有效：单台失败记进 Machines 那一行，不影响其余机器与本机的数据。
func (s *Server) buildTreeAll(ctx context.Context) proto.ProjectTreeResp {
	out, err := s.buildLocalTree(ctx)
	if err != nil {
		// 连本机的树都读不出来：仍然返回可用信封，让 UI 看到本机 ok=false
		out = proto.ProjectTreeResp{Projects: []proto.ProjectNode{}, Unowned: []string{}}
		out.Machines = []proto.MachineStatus{{Name: "", Ok: false,
			FetchedAt: time.Now().UTC(), Error: err.Error()}}
	} else {
		out.Machines = []proto.MachineStatus{{Name: "", Ok: true, FetchedAt: time.Now().UTC()}}
	}

	names := make([]string, 0, len(s.cfg.Targets))
	for name := range s.cfg.Targets {
		names = append(names, name)
	}
	sort.Strings(names)

	fanCtx, cancel := context.WithTimeout(ctx, treeFanoutBudget)
	defer cancel()

	type result struct {
		status proto.MachineStatus
		tree   *proto.ProjectTreeResp
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			t := s.cfg.Targets[name]
			st := proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC()}
			tree, err := client.New(t.Addr, t.Token).MarkForwarded().ProjectTree(fanCtx)
			if err != nil {
				s.log.Warn("项目树扇出失败", "machine", name, "addr", t.Addr, "cause", err)
				st.Error = err.Error()
				results[i] = result{status: st}
				return
			}
			st.Ok = true
			results[i] = result{status: st, tree: tree}
		}(i, name)
	}
	wg.Wait()

	for _, r := range results {
		out.Machines = append(out.Machines, r.status)
		if r.tree == nil {
			continue
		}
		mergeTree(&out, r.status.Name, *r.tree)
	}
	s.log.Info("项目树汇总完成", "machines", len(out.Machines),
		"projects", len(out.Projects))
	return out
}

// mergeTree 把一台远程机器的单机树并进汇总结果。
//
// 合并键是 project_id——它是同一个纯函数从同一个 origin 算出的，跨机天然相等，
// **不需要任何协商**。同一项目在不同机器上的 location 因此排在一起。
func mergeTree(dst *proto.ProjectTreeResp, machine string, src proto.ProjectTreeResp) {
	idx := map[string]int{}
	for i, p := range dst.Projects {
		idx[p.ProjectID] = i
	}
	for _, p := range src.Projects {
		for i := range p.Locations {
			// 远端答的是它的单机树，machine 恒空串；由**汇总方**盖章为 target 名
			p.Locations[i].Machine = machine
		}
		if i, ok := idx[p.ProjectID]; ok {
			dst.Projects[i].Locations = append(dst.Projects[i].Locations, p.Locations...)
			continue
		}
		idx[p.ProjectID] = len(dst.Projects)
		dst.Projects = append(dst.Projects, p)
	}
	dst.Unowned = append(dst.Unowned, src.Unowned...)
}
