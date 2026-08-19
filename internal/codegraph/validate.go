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
	sort.Strings(issues)
	return issues
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
	sort.Strings(issues)
	return issues
}
