// 职责：graph sym 的单点符号查询——名字决议（含方法尾段匹配）与查询时再锚定。
// 边界：只读仓库文件做锚定校验，不回写图数据；不做邻域遍历（那是 query.go 的活）。
package codegraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SymMatch 是 sym 查询的一条结果卡片：节点全量信息 + 归属 + 再锚定结论。
type SymMatch struct {
	ID     string `json:"id"`
	Domain string `json:"domain,omitempty"` // 所属容器的领域 id；容器缺失或未归域时为空
	// Anchor 是再锚定结论：ok（Line 可用）/ moved（已就近重找，Line 是新行号）/
	// vanished（文件在但符号消失，Line 保留图值仅供参考）/ file_missing（文件不存在）/
	// unscanned（节点未扫描或无文件，不做锚定）。
	Anchor string `json:"anchor"`
	ViewNode
}

// SymResult 是一次 sym 查询的完整输出。多义时 Matches 含全部命中，由调用方自选。
type SymResult struct {
	View    string     `json:"view"`
	Query   string     `json:"query"`
	Matches []SymMatch `json:"matches"`
}

// SymLookup 决议 arg 并对每个命中节点做查询时再锚定。决议优先级：
// 节点 id 精确 > Name 精确 > 方法名尾段精确（"UpsertSpend" 命中 "Store.UpsertSpend"）。
// 多义时全部返回；未命中返回错误，错误文本带近似候选与覆盖债提示——
// 「图未覆盖」必须显式可见，agent 才知道该回落 grep 并记债（总纲 spec 用户故事 3）。
func SymLookup(v *View, repoRoot, arg string) (*SymResult, error) {
	ids := symResolve(v, arg)
	if len(ids) == 0 {
		return nil, fmt.Errorf(
			"符号 %q 不在图中（图未覆盖或名字有误）；近似候选: [%s]。确认图未覆盖时回落 grep，并把该符号记入本节点产出物的「图覆盖债」小节",
			arg, strings.Join(symFuzzy(v, arg), ", "))
	}
	r := &SymResult{View: v.Name, Query: arg}
	for _, id := range ids {
		r.Matches = append(r.Matches, symMatchFor(v, repoRoot, id))
	}
	return r, nil
}

func symMatchFor(v *View, repoRoot, id string) SymMatch {
	n := v.Nodes[id]
	m := SymMatch{ID: id, ViewNode: n}
	if c, ok := v.Containers[n.Container]; ok {
		m.Domain = c.Domain
	}
	if n.Unscanned || n.File == "" {
		m.Anchor = "unscanned"
	} else {
		line, status := ReAnchor(repoRoot, n.Node)
		m.Line = line
		m.Anchor = status
	}
	return m
}

// symResolve 返回决议命中的节点 id 集（有序）。三级优先，高优先级命中即短路：
// 同级多命中不合并跨级——避免「精确名 + 尾段」混在一起让 agent 误读多义。
func symResolve(v *View, arg string) []string {
	if _, ok := v.Nodes[arg]; ok {
		return []string{arg}
	}
	var exact, tail []string
	for id, n := range v.Nodes {
		switch {
		case n.Name == arg:
			exact = append(exact, id)
		case symTail(n.Name) == arg:
			tail = append(tail, id)
		}
	}
	if len(exact) > 0 {
		sort.Strings(exact)
		return exact
	}
	sort.Strings(tail)
	return tail
}

// symTail 取 "Store.UpsertSpend" 的 "UpsertSpend"；无 '.' 时返回原名。
func symTail(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// symFuzzy 返回 contains 近似候选（最多 5 个），形态与 Resolve 的候选一致。
func symFuzzy(v *View, arg string) []string {
	low := strings.ToLower(arg)
	var out []string
	for id, n := range v.Nodes {
		if strings.Contains(strings.ToLower(n.Name), low) {
			out = append(out, id+"("+n.Name+")")
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// ReAnchor 对一个节点做查询时锚定，返回（可用行号, 结论）。
// token 规则与 CheckStale 同源：func 取 Name 尾段，其余取整名；entry 不做 token
// 级校验（注册行长相多样，同 stale.go 的理由），文件在即 ok。
// 窗口 line-1..line+1 内按词边界找到 token 即 ok；否则全文件重找：
// 优先「定义形状」行（去空白后以 func/type/export/interface 开头），
// 无定义形状取首个词边界命中行；全文件无命中 → vanished。
func ReAnchor(repoRoot string, n Node) (int, string) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, n.File))
	if err != nil {
		return n.Line, "file_missing"
	}
	lines := strings.Split(string(raw), "\n")
	if n.Kind == "entry" {
		return n.Line, "ok"
	}
	token := n.Name
	if n.Kind == "func" {
		token = symTail(n.Name)
	}
	if n.Line >= 1 && n.Line <= len(lines) {
		lo, hi := n.Line-2, n.Line+1 // 0 基切片，覆盖 1 基的 line-1..line+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		for _, l := range lines[lo:hi] {
			if symTokenOnLine(l, token) {
				return n.Line, "ok"
			}
		}
	}
	def, any := findTokenLine(lines, token)
	switch {
	case def > 0:
		return def, "moved"
	case any > 0:
		return any, "moved"
	default:
		return n.Line, "vanished"
	}
}

// findTokenLine 返回 token 的定义形状行与任意词边界命中行，行号均为 1 基；
// 定义形状规则与 ReAnchor 的历史行为一致，供图内再锚定和图外文档锚搜索共用。
func findTokenLine(lines []string, token string) (def, any int) {
	for i, l := range lines {
		if !symTokenOnLine(l, token) {
			continue
		}
		if any == 0 {
			any = i + 1
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "func ") || strings.HasPrefix(t, "type ") ||
			strings.HasPrefix(t, "export ") || strings.HasPrefix(t, "interface ") {
			def = i + 1
			break
		}
	}
	return def, any
}

// symTokenOnLine 按词边界判断 token 是否出现在行内——裸 Contains 会把 "Do"
// 误配进 "Done"，再锚定就会锚错行，所以两侧字符必须都不是标识符字符。
func symTokenOnLine(line, token string) bool {
	for start := 0; ; {
		i := strings.Index(line[start:], token)
		if i < 0 {
			return false
		}
		i += start
		before := i == 0 || !isIdentChar(line[i-1])
		after := i+len(token) >= len(line) || !isIdentChar(line[i+len(token)])
		if before && after {
			return true
		}
		start = i + len(token)
	}
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
