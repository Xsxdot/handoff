// Service.Pointer 路由缺席的源码级守卫（B156.2 C6，契约 §4「HTTP/CLI 路由表中
// 不存在可达 Pointer 的入口」与 :404「Pointer 调用方在仓内只有控制面组件」的
// 执行机制）。判据 (a)：全仓非测试 .go 文件里对 collab.Service.Pointer 的引用
// 只允许出现在白名单 pointerWhitelist 内；白名单外出现第二处即红。
//
// 为什么是读源码的 Go 测试而不是 graph check / TestRepoContractGate：
// cmd/graph_gate_test.go:36 的 codegraph.Check 四入参全部来自 JSON，不解析源码；
// 新增一条真实调用而不写进视图 diff，闸门眼里它不存在（B156.3 决定性实验：
// internal/ledgerstep 放真调 Service.Pointer，build/vet/graph check/TestRepoContractGate
// 四项与基线一个数都不差）。只有随 go test 复现的读源码测试才有牙齿。
//
// 判定机制（按接收者形状，协调者复核定案）：go/ast 收集全部 SelectorExpr 且
// Sel.Name=="Pointer"；排除 X 为标识符 unsafe 的（unsafe.Pointer( 类型转换，恰 6 处：
// internal/prochost/taskmark_darwin.go 1、platform_windows.go 5）与 X 为标识符 atomic
// 的（atomic.Pointer[ 泛型类型实例化，另 1 处：server.go:93）——其余都是候选，按
// 所在承载形态与白名单比对。**对第三方类型上的同名 Pointer 方法本测试是过包含的**
// ——过包含是红线的安全方向，判据要求的出口「新的正当引用显式加进白名单并写明
// 理由」正是它的退出通道。
//
// 归属逻辑覆盖三种承载形态（协调者核实的 C7 实况：派发指针行真实调用在包级 var
// 闭包里）：
//  1. FuncDecl（普通函数/方法）→ 函数名 / 「类型.方法名」；
//  2. 包级 var/const 声明里的函数字面量 → 用那个变量名（如 cmd/card_dispatch.go
//     的 roomPointer）；
//  3. 函数内部的匿名闭包 → 归属到最内层的 FuncDecl（外层函数名）——近似，只要求
//     报得出可指认的名字。
//
// 三类 span 统一记录起止位置，命中时取最内层（跨度最小）那个。
//
// 白名单是显式列表（文件+函数+理由）。将来再出现正当引用，必须在同一提交把该
// 引用加进白名单并写明理由，不许放宽断言——只堵不留出口会诱发绕过。
package agentd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pointerWhitelist 是 collab.Service.Pointer 在仓内非测试 .go 文件的全部合法引用处。
type pointerWhitelistEntry struct {
	file string // 相对仓库根的路径
	fn   string // 所在函数（方法按「类型.方法名」，包级按函数名/承载 var 名）
	why  string
}

var pointerWhitelist = []pointerWhitelistEntry{
	{"internal/agentd/server.go", "roomNarrator.Say",
		"组装点适配器（target.json assembly 登记点，server.go）：B156.3 叙事换绑轮把协调者叙事经此落卡房间（Service.Pointer）；不在 HTTP 路由可达图上。"},
	{"cmd/card_dispatch.go", "roomPointer",
		"CLI 裸派发的派发指针行经测试缝 roomPointer（包级 var 闭包承载，归属实测名）落账（岔口八范围），不在 HTTP 路由可达图上。"},
}

// TestPointerRouteAbsentFromSource 契约 §4 判据 (a)：全仓非测试 .go 文件里每个
// 非 unsafe/atomic 前缀的 Pointer 选择器调用都必须落在白名单内；白名单每条至少
// 有一个命中（防白名单膨胀成无牙条款）。
func TestPointerRouteAbsentFromSource(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	type hit struct{ file, fn string }
	var hits []hit
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "web" ||
				d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil // 解析失败不阻断其它文件（该文件编译错误由 build 兜底）
		}
		// 收集函数声明与包级 var/const 声明区间，用于把候选定位到所在承载形态
		// （覆盖 FuncDecl / 包级 var 闭包 / 函数内匿名闭包三种形态）。
		type span struct {
			name          string
			start, endPos token.Pos
		}
		var spans []span
		ast.Inspect(f, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				spans = append(spans, span{pointerFnName(d), d.Pos(), d.End()})
			case *ast.GenDecl:
				if d.Tok != token.VAR && d.Tok != token.CONST {
					return true
				}
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						spans = append(spans, span{vs.Names[0].Name, d.Pos(), d.End()})
					}
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Pointer" {
				return true
			}
			// 排除 unsafe.Pointer(（类型转换，恰 6 处）与 atomic.Pointer[（泛型
			// 类型实例化，server.go:93）——X 均为裸标识符，且都是类型构造不是方法调用。
			if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "unsafe" || id.Name == "atomic") {
				return true
			}
			pos := fset.Position(sel.Pos())
			// 取最内层承载形态：命中同一位置的多个 span 里挑跨度最小者。
			best := ""
			bestLen := 0
			for _, sp := range spans {
				p := fset.Position(sp.start).Offset
				e := fset.Position(sp.endPos).Offset
				if pos.Offset >= p && pos.Offset <= e {
					l := e - p
					if best == "" || l < bestLen {
						best = sp.name
						bestLen = l
					}
				}
			}
			hits = append(hits, hit{rel, best})
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(hits) == 0 {
		t.Fatal("守卫未命中任何 Pointer 选择器：白名单可能已空转，需人工核查（基线应至少命中 server.go 的 roomNarrator.Say）")
	}
	for _, h := range hits {
		ok := false
		for _, w := range pointerWhitelist {
			if h.file == w.file && h.fn == w.fn {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("白名单外 Pointer 引用: %s %s——若属正当引用，显式加进 pointerWhitelist 并写明理由，不许放宽断言", h.file, h.fn)
		}
	}
	for _, w := range pointerWhitelist {
		found := false
		for _, h := range hits {
			if h.file == w.file && h.fn == w.fn {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("白名单条目无对应引用，可能已失效: %s %s（%s）", w.file, w.fn, w.why)
		}
	}
}

// pointerFnName 从 FuncDecl 推导函数名：方法返回「类型.方法名」（指针接收者去 *），
// 包级返回函数名。白名单定位用。
func pointerFnName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		typ := fd.Recv.List[0].Type
		name := ""
		if st, ok := typ.(*ast.StarExpr); ok {
			if id, ok := st.X.(*ast.Ident); ok {
				name = id.Name
			}
		} else if id, ok := typ.(*ast.Ident); ok {
			name = id.Name
		}
		return name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// repoRoot 定位仓库根（测试运行于 internal/agentd，向上两级应见 go.mod）。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(dir))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("无法定位仓库根: %v", err)
	}
	return root
}
