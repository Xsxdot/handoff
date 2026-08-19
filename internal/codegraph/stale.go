// 本文件实现保鲜检测：节点声称的 file:line 与真实源码对不上即 stale。
//
// 为什么这么设计（spec §7）：过期的图比没有图更糟——agent 信了它就省了验证。
// 节点刻意不存源码正文，file:line 是唯一锚点，所以校验它就是校验图的新鲜度。
//
// 职责：CheckStale——按廉价规则逐节点比对
// 边界：不重扫、不修复，只报告；unscanned 节点跳过（没人声称它是新鲜的）
package codegraph

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StaleNode 描述一个失鲜节点及原因。
type StaleNode struct {
	ID     string `json:"id"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// CheckStale 逐节点做三级廉价校验：文件存在 → 行号在界内 →
// 行窗口（line-1..line+1）里能找到名字 token。entry 只做前两级
// （注册行长相多样，token 检查会假红）；func/model 检查 token：
// func 取 Name 最后一个 '.' 之后的段（"Client.Dispatch" → "Dispatch"），
// model 取整名。文件按缓存读，同文件多节点只读一次。
func CheckStale(repoRoot string, g *Graph) []StaleNode {
	cache := map[string][]string{}
	readLines := func(rel string) ([]string, bool) {
		if ls, ok := cache[rel]; ok {
			return ls, ls != nil
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			cache[rel] = nil
			return nil, false
		}
		ls := strings.Split(string(raw), "\n")
		cache[rel] = ls
		return ls, true
	}
	var out []StaleNode
	for id, n := range g.Nodes {
		if n.Unscanned || n.File == "" {
			continue
		}
		lines, ok := readLines(n.File)
		if !ok {
			out = append(out, StaleNode{ID: id, File: n.File, Line: n.Line, Reason: "文件不存在"})
			continue
		}
		if n.Line < 1 || n.Line > len(lines) {
			out = append(out, StaleNode{ID: id, File: n.File, Line: n.Line, Reason: "行号越界"})
			continue
		}
		if n.Kind == "entry" {
			continue
		}
		token := n.Name
		if i := strings.LastIndex(token, "."); i >= 0 {
			token = token[i+1:]
		}
		lo, hi := n.Line-2, n.Line+1 // 0 基窗口 [line-1-1, line+1)
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if !strings.Contains(strings.Join(lines[lo:hi], "\n"), token) {
			out = append(out, StaleNode{ID: id, File: n.File, Line: n.Line, Reason: "行内容与名字对不上（疑似代码已移动）"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
