// 投递寻址的源码级守卫（B358）：先例 internal/agentd/pointer_gate_test.go。
//
// 扇出禁令要求「唤醒路径必须以寻址命中为必要条件，写不出别的形状」。判据分两层：
//  1. 纯判定入口 room.ResolveDelivery 的签名不得出现「成员集合」类参数——一旦有人
//     给它加一个 members []string 入参，就出现了按成员集合投递的形状，本测试当场红；
//  2. ResolveDelivery 的函数体必须同时引用 Mentions 与 ReplyTo 两个寻址字段——
//     漏掉任一路（例如只按 @ 投递、回复不唤醒）即偏离冻结语义。
//
// 为什么是读源码的 Go 测试而不是 graph check：新增一条真实调用而不写进视图 diff
// 时，闸门眼里它不存在（B156.3 决定性实验，见 pointer_gate_test.go 头注）。只有
// 随 go test 复现的读源码测试才有牙齿。
package room

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveDeliveryShapeHasNoMemberSet 钉住 ResolveDelivery 的签名与寻址字段引用。
func TestResolveDeliveryShapeHasNoMemberSet(t *testing.T) {
	fset := token.NewFileSet()
	path := "delivery.go"
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	var fn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "ResolveDelivery" {
			fn = d
		}
		return true
	})
	if fn == nil {
		t.Fatalf("%s 未找到 ResolveDelivery——扇出禁令的判定入口不存在", path)
	}
	// 判据 1：形参名不得含「成员集合」语义（members/memberSet/memberIDs/recipients）。
	banned := []string{"member", "recipient", "audience", "all"}
	for _, p := range fn.Type.Params.List {
		for _, name := range p.Names {
			lower := strings.ToLower(name.Name)
			for _, bad := range banned {
				if strings.Contains(lower, bad) {
					t.Errorf("ResolveDelivery 形参 %q 含成员集合语义 %q——投递寻址的签名里不许出现成员集合，扇出形状只会从这里长出来", name.Name, bad)
				}
			}
		}
	}
	// 判据 2：函数体必须同时引用 Mentions 与 ReplyTo。
	refs := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "msg" {
				refs[sel.Sel.Name] = true
			}
		}
		return true
	})
	for _, want := range []string{"Mentions", "ReplyTo"} {
		if !refs[want] {
			t.Errorf("ResolveDelivery 未引用寻址字段 %s——两类寻址（显式 @ / 回复原作者）缺一即偏离冻结语义", want)
		}
	}
}

// TestWakePathSourceUsesAddressing 扫描投递相关的非测试源码，钉住「扇出禁令」在
// 仓内的落地：任何按成员集合投递的 helper 都不许出现在 room 包与 collab 门面。
// 判据：room 包与 collab 根包非测试 .go 文件里，不得出现名为
// broadcastToMembers / fanOutToMembers / notifyAllMembers 的函数。
func TestWakePathSourceUsesAddressing(t *testing.T) {
	banned := []string{"broadcastToMembers", "fanOutToMembers", "notifyAllMembers", "wakeAllMembers"}
	for _, dir := range []string{".", ".."} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("读取目录 %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("读取 %s: %v", name, err)
			}
			for _, bad := range banned {
				if strings.Contains(string(body), bad) {
					t.Errorf("%s/%s 出现按成员集合投递的函数 %s——扇出禁令：系统永不按成员集合投递", dir, name, bad)
				}
			}
		}
	}
}
