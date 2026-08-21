// 本文件实现 diff 回灌基线（spec §7）：分支合并回 main 后，把分支视图
// 机械併入 baseline，让基线保鲜成为流程副产物。
//
// 职责：Absorb（纯函数併入）、SaveGraph（原子写盘）
// 边界：不删 diff 文件、不取 git 信息——那是 cmd 层的编排；
//
//	不校验 diff 合法性——调用方先过 ValidateDiff
package codegraph

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// Absorb 返回 g 併入 d 后的新图，入参不变（失败重试无损的前提）。
// 顺序：节点增/改/删 → 删除节点牵连的边一并剔除（否则 Validate 报悬空）→
// 边与 implements 增/删。
func Absorb(g *Graph, d *Diff) *Graph {
	out := &Graph{
		Meta:       g.Meta,
		Domains:    maps.Clone(g.Domains),
		Containers: maps.Clone(g.Containers),
		Nodes:      maps.Clone(g.Nodes),
		Edges:      slices.Clone(g.Edges),
		Implements: slices.Clone(g.Implements),
	}
	for id, n := range d.NodesAdded {
		out.Nodes[id] = n
	}
	for id, n := range d.NodesModified {
		n.SignatureOld = "" // 旧签名是 diff 展示用字段，不进基线
		out.Nodes[id] = n
	}
	dead := make(map[string]bool, len(d.NodesDeleted))
	for _, id := range d.NodesDeleted {
		dead[id] = true
		delete(out.Nodes, id)
	}
	out.Edges = mergeEdges(out.Edges, d.EdgesAdded, d.EdgesDeleted, dead)
	out.Implements = mergeEdges(out.Implements, d.ImplementsAdded, d.ImplementsDeleted, dead)
	return out
}

// mergeEdges 边表併入：加 added、剔 deleted、剔任一端指向已删节点的边，顺带去重。
func mergeEdges(base, added, deleted []Edge, dead map[string]bool) []Edge {
	drop := make(map[Edge]bool, len(deleted))
	for _, e := range deleted {
		drop[e] = true
	}
	seen := make(map[Edge]bool, len(base)+len(added))
	out := make([]Edge, 0, len(base)+len(added))
	for _, e := range append(slices.Clone(base), added...) {
		if drop[e] || dead[e[0]] || dead[e[1]] || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// SaveGraph 原子写 repoRoot/codegraph/baseline.json：同目录临时文件 + rename——
// 写盘半途失败不得留下截断的基线（生命周期中断族）。缩进单空格与既有基线一致。
func SaveGraph(repoRoot string, g *Graph) error {
	raw, err := json.MarshalIndent(g, "", " ")
	if err != nil {
		return fmt.Errorf("编码基线: %w", err)
	}
	dir := filepath.Join(repoRoot, "codegraph")
	tmp, err := os.CreateTemp(dir, "baseline-*.json")
	if err != nil {
		return fmt.Errorf("建临时基线文件: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("写临时基线: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时基线: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "baseline.json")); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("原子替换基线: %w", err)
	}
	return nil
}
