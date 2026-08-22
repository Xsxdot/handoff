// 本文件实现调用边合理性门控：机械排除「按裸符号名撞库」产生的假调用边。
//
// 职责：CheckEdges——两条判据：
//  1. 跨语言（.go ↔ .ts/.tsx）调用边必假：TS 调不到 Go 函数，wire 关联走 projections/twins；
//  2. Go 跨包调用边要求调用方包 import 被调方包（包粒度，排除 _test.go）：
//     没有 import 的跨包调用不可能通过编译，这条边只能是重名误连。
//
// 边界：只判「这条边有没有可能真实」，不判调用语义；不改数据，只报告。
//
//	包粒度而非文件粒度——方法调用可经由字段/参数类型送达，调用文件不必亲自
//	import 被调包（反例：internal/agentd/status.go 经 m.st 调 store.ListTasks）。
//	调用方目录读不到生产 .go 时保守放行——宁漏报不误杀。
//	背景：B173 复查实锤基线 106 条假边（os.ReadFile→agentd.ReadFile 之类），
//	见 docs/superpowers/backlog.md B173。
package codegraph

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// EdgeIssue 一条被门控判定为不可能真实的调用边。
// JSON key 是清洗脚本与外部消费方的 wire 契约，改动需过 TestEdgeIssueJSONContract。
type EdgeIssue struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"` // "cross-language" 或 "no-import"
	Detail string `json:"detail"`
}

// CheckEdges 对调用边做合理性门控，返回问题列表（空 = 干净）。
// repoRoot 是仓库根（读 go.mod 与各包源文件）；nodes/edges 来自基线或「基线+diff」合成。
// 端点缺失的边直接跳过——那归 Validate 的引用完整性负责，这里不重复报。
func CheckEdges(repoRoot string, nodes map[string]Node, edges []Edge) []EdgeIssue {
	module := readModulePath(repoRoot)
	// 包目录（仓库相对、斜杠分隔）→ import 集合；nil 表示目录无生产 .go，保守放行。
	cache := map[string]map[string]bool{}
	var issues []EdgeIssue
	for _, e := range edges {
		from, okFrom := nodes[e[0]]
		to, okTo := nodes[e[1]]
		if !okFrom || !okTo {
			continue
		}
		langFrom, langTo := fileLang(from.File), fileLang(to.File)
		if (langFrom == "go" && langTo == "ts") || (langFrom == "ts" && langTo == "go") {
			issues = append(issues, EdgeIssue{
				From: e[0], To: e[1], Reason: "cross-language",
				Detail: fmt.Sprintf("%s（%s）→ %s（%s）：跨语言调用边必假，wire 关联应走 projections/twins", e[0], from.File, e[1], to.File),
			})
			continue
		}
		if langFrom != "go" || langTo != "go" || module == "" {
			continue // 非 Go-Go 边不判；无 go.mod（纯前端仓）时 Go 侧判据整体停用
		}
		fromDir, toDir := path.Dir(from.File), path.Dir(to.File)
		if fromDir == toDir {
			continue // 同包调用不需要 import
		}
		imps, seen := cache[fromDir]
		if !seen {
			imps = goPkgImports(filepath.Join(repoRoot, filepath.FromSlash(fromDir)))
			cache[fromDir] = imps
		}
		if imps == nil {
			continue
		}
		want := module + "/" + toDir
		if toDir == "." {
			want = module
		}
		if !imps[want] {
			issues = append(issues, EdgeIssue{
				From: e[0], To: e[1], Reason: "no-import",
				Detail: fmt.Sprintf("%s（%s）→ %s（%s）：调用方包未 import %s，跨包调用不可能编译通过", e[0], from.File, e[1], to.File, want),
			})
		}
	}
	return issues
}

// fileLang 按扩展名分类节点文件语言；不认识的返回空串（不参与任何判据）。
func fileLang(file string) string {
	switch {
	case strings.HasSuffix(file, ".go"):
		return "go"
	case strings.HasSuffix(file, ".ts"), strings.HasSuffix(file, ".tsx"):
		return "ts"
	default:
		return ""
	}
}

// readModulePath 读 repoRoot/go.mod 的 module 行；读不到返回空串。
func readModulePath(repoRoot string) string {
	raw, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// goPkgImports 收集 dir 下所有生产 .go 文件（排除 _test.go）的 import 路径并集。
// 目录不存在、不可读或没有可解析的生产 .go 时返回 nil——调用方据此保守放行。
// 用 go/parser 的 ImportsOnly 模式而非正则：注释与字符串里的路径不会误入。
func goPkgImports(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out map[string]bool
	fset := token.NewFileSet()
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			continue // 单文件解析失败不拖垮整包判定
		}
		if out == nil {
			out = map[string]bool{}
		}
		for _, imp := range f.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil {
				out[p] = true
			}
		}
	}
	return out
}
