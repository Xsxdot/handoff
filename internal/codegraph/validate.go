// 本文件实现引用完整性校验：图与 diff 里的一切引用必须落在已定义的对象上。
//
// 职责：Validate（基线自查）、ValidateDiff（diff 相对基线自查）
// 边界：不查 file:line 真实性（stale.go 的事）、不修数据，只报告
package codegraph

import (
	"fmt"
	"sort"
)

// Validate 检查基线的引用完整性，返回问题列表（空 = 干净）。
// 检查项：节点的 container 必须存在；每条边两端必须是已定义节点。
func Validate(g *Graph) []string {
	var issues []string
	for id, n := range g.Nodes {
		if _, ok := g.Containers[n.Container]; !ok {
			issues = append(issues, fmt.Sprintf("节点 %s 引用不存在的容器 %s", id, n.Container))
		}
	}
	for _, e := range g.Edges {
		for _, end := range e {
			if _, ok := g.Nodes[end]; !ok {
				issues = append(issues, fmt.Sprintf("边 %s→%s 引用不存在的节点 %s", e[0], e[1], end))
			}
		}
	}
	for _, e := range g.Implements {
		for _, end := range e {
			if _, ok := g.Nodes[end]; !ok {
				issues = append(issues, fmt.Sprintf("implements 边 %s→%s 引用不存在的节点 %s", e[0], e[1], end))
			}
		}
	}
	for _, p := range g.Projections {
		issues = append(issues, validateProjection(g.Nodes, p, "投影")...)
	}
	issues = append(issues, validateDomains(g)...)
	sort.Strings(issues)
	return issues
}

// validateDomains 检查领域段自洽与容器归属。
// domains 为空时整段跳过——那是旧扫描数据的合法降级路径，不是错误。
func validateDomains(g *Graph) []string {
	if len(g.Domains) == 0 {
		return nil
	}
	var out []string
	hasChild := map[string]bool{}
	for id, d := range g.Domains {
		if d.Parent == "" {
			continue
		}
		if _, ok := g.Domains[d.Parent]; !ok {
			out = append(out, fmt.Sprintf("领域 %s 的 parent %s 不存在", id, d.Parent))
			continue
		}
		hasChild[d.Parent] = true
	}
	// 父链探环：沿 Parent 上溯，重复遇到同一个 id 即成环。
	// 成环会让消费方的路径推导死循环，必须在数据层拦下。
	for id := range g.Domains {
		seen := map[string]bool{id: true}
		for cur := g.Domains[id].Parent; cur != ""; {
			if seen[cur] {
				out = append(out, fmt.Sprintf("领域 %s 的父链存在环", id))
				break
			}
			seen[cur] = true
			d, ok := g.Domains[cur]
			if !ok {
				break // parent 不存在已在上面报过，这里不重复报
			}
			cur = d.Parent
		}
	}
	// 容器归属：必须有 domain、领域必须存在、且必须是叶子。
	// 存在性一律用 ok 判定——拿零值比较会把「存在但字段全空的领域」误判成不存在。
	for cid, c := range g.Containers {
		if c.Domain == "" {
			out = append(out, fmt.Sprintf("容器 %s 未归属领域（domains 非空时每个容器都必须有 domain）", cid))
			continue
		}
		if _, ok := g.Domains[c.Domain]; !ok {
			out = append(out, fmt.Sprintf("容器 %s 引用不存在的领域 %s", cid, c.Domain))
			continue
		}
		if hasChild[c.Domain] {
			out = append(out, fmt.Sprintf("容器 %s 挂在非叶子领域 %s（容器只能挂叶子领域）", cid, c.Domain))
		}
	}
	return out
}

// ValidateDiff 检查 diff 相对基线的引用完整性。
// 检查项：nodesModified/nodesDeleted 引用的节点必须在基线里；
// edgesAdded/edgesDeleted 两端必须在「基线 ∪ nodesAdded」里；
// nodesAdded 的 container 必须存在。
func ValidateDiff(g *Graph, d *Diff) []string {
	var issues []string
	known := func(id string) bool {
		if _, ok := g.Nodes[id]; ok {
			return true
		}
		_, ok := d.NodesAdded[id]
		return ok
	}
	for id, n := range d.NodesAdded {
		if _, ok := g.Containers[n.Container]; !ok {
			issues = append(issues, fmt.Sprintf("新增节点 %s 引用不存在的容器 %s", id, n.Container))
		}
	}
	for id := range d.NodesModified {
		if _, ok := g.Nodes[id]; !ok {
			issues = append(issues, fmt.Sprintf("修改的节点 %s 不在基线里", id))
		}
	}
	for _, id := range d.NodesDeleted {
		if _, ok := g.Nodes[id]; !ok {
			issues = append(issues, fmt.Sprintf("删除的节点 %s 不在基线里", id))
		}
	}
	for _, e := range append(append([]Edge{}, d.EdgesAdded...), d.EdgesDeleted...) {
		for _, end := range e {
			if !known(end) {
				issues = append(issues, fmt.Sprintf("diff 边 %s→%s 引用未知节点 %s", e[0], e[1], end))
			}
		}
	}
	for _, e := range append(append([]Edge{}, d.ImplementsAdded...), d.ImplementsDeleted...) {
		for _, end := range e {
			if !known(end) {
				issues = append(issues, fmt.Sprintf("diff implements 边 %s→%s 引用未知节点 %s", e[0], e[1], end))
			}
		}
	}
	knownNodeMap := make(map[string]Node, len(g.Nodes)+len(d.NodesAdded))
	for id, n := range g.Nodes {
		knownNodeMap[id] = n
	}
	for id, n := range d.NodesAdded {
		knownNodeMap[id] = n
	}
	for id, n := range d.NodesModified {
		if _, ok := knownNodeMap[id]; ok {
			knownNodeMap[id] = n
		}
	}
	for _, p := range append(append([]Projection{}, d.ProjectionsAdded...), d.ProjectionsDeleted...) {
		issues = append(issues, validateProjection(knownNodeMap, p, "diff 投影")...)
	}
	sort.Strings(issues)
	return issues
}

func validateProjection(nodes map[string]Node, p Projection, label string) []string {
	var issues []string
	from, fromOK := nodes[p[0]]
	to, toOK := nodes[p[1]]
	if !fromOK {
		issues = append(issues, fmt.Sprintf("%s %s→%s(%s) 引用不存在的节点 %s", label, p[0], p[1], p[2], p[0]))
	}
	if !toOK {
		issues = append(issues, fmt.Sprintf("%s %s→%s(%s) 引用不存在的节点 %s", label, p[0], p[1], p[2], p[1]))
	}
	if p[2] != "typed" && p[2] != "handroll" && p[2] != "twin" {
		issues = append(issues, fmt.Sprintf("%s %s→%s 的 kind 非法: %q（只认 typed/handroll/twin）", label, p[0], p[1], p[2]))
	}
	if p[2] == "twin" && fromOK && toOK && (from.Kind != "model" || to.Kind != "model") {
		issues = append(issues, fmt.Sprintf("%s %s→%s 的 twin 两端必须都是 model 节点", label, p[0], p[1]))
	}
	return issues
}
