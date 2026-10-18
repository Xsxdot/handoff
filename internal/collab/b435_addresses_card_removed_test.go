// b435_addresses_card_removed_test.go —— B435 C-240 防回潮守卫。
//
// 职责：AST 扫本包非测试源码，断言不再声明 AddressesCard（寻址判定只住匹配函数
// Recipients/Matches；编译已保证无调用方，本守卫防有人把旧判定加回来）。
package collab

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestB435AddressesCardRemoved(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", name, err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "AddressesCard" {
				t.Errorf("%s 仍声明 AddressesCard（C-240：寻址判定只走 Recipients/Matches）", fset.Position(fn.Pos()))
			}
		}
	}
}
