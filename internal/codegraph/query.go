// 本文件实现视图上的邻域查询：多源 BFS，下游为正距离、上游为负距离。
//
// 职责：Resolve（id/名字 → 节点 id）、Neighborhood（chain/who-calls/并集共用核心）
// 边界：不做布局（消费方的事）；deleted 节点/边不参与遍历（它们只是渲染残影）
package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// ResultNode 是查询结果里的节点：id + 与焦点的 BFS 距离（下游正、上游负）。
type ResultNode struct {
	ID   string `json:"id"`
	Dist int    `json:"dist"`
	ViewNode
}

// Result 是一次邻域查询的完整结果。
// UnscannedEntries/Warning 让消费方能区分「查询无结果」与「根本没扫」——
// 这是 spec §6 的硬要求，agent 拿掉这个信息会写出漏影响面的 plan。
type Result struct {
	View             string       `json:"view"`
	Foci             []string     `json:"foci"`
	Nodes            []ResultNode `json:"nodes"`
	Edges            []ViewEdge   `json:"edges"`
	UnscannedEntries int          `json:"unscannedEntries"`
	Warning          string       `json:"warning,omitempty"`
}

// Resolve 把命令行参数解析成节点 id：先按 id 精确匹配，再按 name 精确匹配。
// name 多义或未命中时报错并列出近似候选（contains，最多 5 个），方便 agent 自纠。
func Resolve(v *View, arg string) (string, error) {
	if _, ok := v.Nodes[arg]; ok {
		return arg, nil
	}
	var exact, fuzzy []string
	for id, n := range v.Nodes {
		if n.Name == arg {
			exact = append(exact, id)
		} else if strings.Contains(strings.ToLower(n.Name), strings.ToLower(arg)) {
			fuzzy = append(fuzzy, id+"("+n.Name+")")
		}
	}
	sort.Strings(exact)
	sort.Strings(fuzzy)
	switch len(exact) {
	case 1:
		return exact[0], nil
	case 0:
		if len(fuzzy) > 5 {
			fuzzy = fuzzy[:5]
		}
		return "", fmt.Errorf("节点 %q 不在图中；近似候选: %s", arg, strings.Join(fuzzy, ", "))
	default:
		return "", fmt.Errorf("名字 %q 多义，请用节点 id: %s", arg, strings.Join(exact, ", "))
	}
}

// Neighborhood 从焦点集合做多源 BFS。down/up 是两个方向各自的最大深度：
// 0 = 该方向不查，-1 = 不限。deleted 节点与边不参与遍历。
func Neighborhood(v *View, foci []string, down, up int) (*Result, error) {
	adj, radj := map[string][]string{}, map[string][]string{}
	for _, e := range v.Edges {
		if e.Status == "deleted" ||
			v.Nodes[e.From].Status == "deleted" || v.Nodes[e.To].Status == "deleted" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		radj[e.To] = append(radj[e.To], e.From)
	}
	dist := map[string]int{}
	for _, f := range foci {
		n, ok := v.Nodes[f]
		if !ok {
			return nil, fmt.Errorf("焦点 %s 不在视图中", f)
		}
		if n.Status == "deleted" {
			return nil, fmt.Errorf("焦点 %s 在该视图中已被删除", f)
		}
		dist[f] = 0
	}
	bfs := func(next map[string][]string, step, limit int) {
		frontier := append([]string{}, foci...)
		for len(frontier) > 0 {
			var nx []string
			for _, id := range frontier {
				d := dist[id] + step
				if limit >= 0 && abs(d) > limit {
					continue
				}
				for _, t := range next[id] {
					if _, seen := dist[t]; !seen {
						dist[t] = d
						nx = append(nx, t)
					}
				}
			}
			frontier = nx
		}
	}
	if down != 0 {
		bfs(adj, 1, down)
	}
	if up != 0 {
		bfs(radj, -1, up)
	}

	r := &Result{View: v.Name, Foci: append([]string{}, foci...)}
	for id, d := range dist {
		r.Nodes = append(r.Nodes, ResultNode{ID: id, Dist: d, ViewNode: v.Nodes[id]})
	}
	sort.Slice(r.Nodes, func(i, j int) bool {
		if r.Nodes[i].Dist != r.Nodes[j].Dist {
			return r.Nodes[i].Dist < r.Nodes[j].Dist
		}
		return r.Nodes[i].ID < r.Nodes[j].ID
	})
	for _, e := range v.Edges {
		if _, a := dist[e.From]; a {
			if _, b := dist[e.To]; b {
				r.Edges = append(r.Edges, e)
			}
		}
	}
	for _, n := range v.Nodes {
		if n.Kind == "entry" && n.Unscanned && n.Status != "deleted" {
			r.UnscannedEntries++
		}
	}
	if r.UnscannedEntries > 0 {
		r.Warning = fmt.Sprintf("基线仍有 %d 个未扫描入口：查询结果为空不等于没有调用方", r.UnscannedEntries)
	}
	return r, nil
}

// abs 是 int 绝对值（标准库到 1.21 仍无泛型版，自备）。
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
