// 本文件实现 graph resolve：校验代码图与文档中的 file#Symbol 符号锚。
//
// 职责：图内节点再锚定、图外源码词边界搜索、Markdown 锚点提取与去重。
// 边界：只读视图和仓库文件，不修改文档或 codegraph 数据。
package codegraph

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AnchorResult 是一次 file#Symbol 锚点检查结果。
type AnchorResult struct {
	Ref    string `json:"ref"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Anchor string `json:"anchor"`
	NodeID string `json:"nodeId,omitempty"`
}

// ResolveAnchor 解析 ref（形如 path/file.go#Symbol），优先使用图内节点，
// 图外则在整文件中按词边界搜索。图外命中统一记为 moved，因为没有原始图行号可比较。
func ResolveAnchor(v *View, repoRoot, ref string) (*AnchorResult, error) {
	file, symbol, ok := strings.Cut(ref, "#")
	if !ok || file == "" || symbol == "" {
		return nil, fmt.Errorf("锚点 %q 格式非法，应为 file#Symbol", ref)
	}
	r := &AnchorResult{Ref: ref, File: file}
	if id, ok := resolveGraphAnchor(v, file, symbol); ok {
		n := v.Nodes[id]
		r.Line, r.Anchor = ReAnchor(repoRoot, n.Node)
		r.NodeID = id
		return r, nil
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(file)))
	if err != nil {
		if os.IsNotExist(err) {
			r.Anchor = "file_missing"
			return r, nil
		}
		return nil, fmt.Errorf("读取锚点文件 %s: %w", file, err)
	}
	def, any := findTokenLine(strings.Split(string(raw), "\n"), symbol)
	switch {
	case def > 0:
		r.Line, r.Anchor = def, "moved"
	case any > 0:
		r.Line, r.Anchor = any, "moved"
	default:
		r.Anchor = "vanished"
	}
	return r, nil
}

func resolveGraphAnchor(v *View, file, symbol string) (string, bool) {
	var exact, tail []string
	for id, n := range v.Nodes {
		if n.Status == "deleted" || n.File != file {
			continue
		}
		if n.Name == symbol {
			exact = append(exact, id)
		} else if symTail(n.Name) == symbol {
			tail = append(tail, id)
		}
	}
	if len(exact) > 0 {
		sort.Strings(exact)
		return exact[0], true
	}
	if len(tail) > 0 {
		sort.Strings(tail)
		return tail[0], true
	}
	return "", false
}

var docAnchorPattern = regexp.MustCompile("`([\\w./-]+\\.[A-Za-z]+)#([A-Za-z_][A-Za-z0-9_.]*)`")

// CheckDocAnchors 提取 Markdown 反引号内的 file#Symbol 引用，去重后逐条解析。
func CheckDocAnchors(v *View, repoRoot, docPath string) ([]AnchorResult, error) {
	path := docPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文档 %s: %w", docPath, err)
	}
	var out []AnchorResult
	seen := map[string]bool{}
	for _, match := range docAnchorPattern.FindAllStringSubmatch(string(raw), -1) {
		ref := match[1] + "#" + match[2]
		if seen[ref] {
			continue
		}
		seen[ref] = true
		result, err := ResolveAnchor(v, repoRoot, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, *result)
	}
	return out, nil
}
