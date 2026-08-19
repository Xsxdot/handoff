// 本文件实现「基准 + 差异 → 视图」的合并（spec §3.2 的渲染时合并）。
//
// 职责：Merge 产出带 Status 标记的 View；删除的对象保留并标 deleted，
//
//	供消费方画红虚线——直接剔除会让"删了什么"不可见
//
// 边界：不做查询（query.go）；diff 的合法性由 ValidateDiff 把关，
//
//	Merge 对非法引用宽容跳过（渲染路径不因脏数据崩）
package codegraph

// ViewNode 是视图里的节点：Node + 差异状态。
type ViewNode struct {
	Node
	Status string `json:"status,omitempty"` // "" | added | modified | deleted
}

// ViewEdge 是视图里的边。
type ViewEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status,omitempty"`
}

// View 是合并后的图视图。
type View struct {
	Name       string               `json:"view"`
	Containers map[string]Container `json:"containers"`
	Nodes      map[string]ViewNode  `json:"nodes"`
	Edges      []ViewEdge           `json:"edges"`
}

// Merge 把基线与一个 diff 合并成视图。d 为 nil 时返回纯基准视图（Name="baseline"）。
func Merge(g *Graph, d *Diff) *View {
	v := &View{Name: "baseline", Containers: g.Containers,
		Nodes: make(map[string]ViewNode, len(g.Nodes))}
	for id, n := range g.Nodes {
		v.Nodes[id] = ViewNode{Node: n}
	}
	for _, e := range g.Edges {
		v.Edges = append(v.Edges, ViewEdge{From: e[0], To: e[1]})
	}
	if d == nil {
		return v
	}
	v.Name = d.View
	for id, n := range d.NodesAdded {
		v.Nodes[id] = ViewNode{Node: n, Status: "added"}
	}
	for id, n := range d.NodesModified {
		if _, ok := v.Nodes[id]; ok {
			v.Nodes[id] = ViewNode{Node: n, Status: "modified"}
		}
	}
	for _, id := range d.NodesDeleted {
		if vn, ok := v.Nodes[id]; ok {
			vn.Status = "deleted"
			v.Nodes[id] = vn
		}
	}
	del := map[string]bool{}
	for _, e := range d.EdgesDeleted {
		del[e[0]+"\x00"+e[1]] = true
	}
	for i := range v.Edges {
		if del[v.Edges[i].From+"\x00"+v.Edges[i].To] {
			v.Edges[i].Status = "deleted"
		}
	}
	for _, e := range d.EdgesAdded {
		v.Edges = append(v.Edges, ViewEdge{From: e[0], To: e[1], Status: "added"})
	}
	return v
}
