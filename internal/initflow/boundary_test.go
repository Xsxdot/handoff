package initflow_test

import (
	"go/build"
	"strings"
	"testing"
)

// initflow 是 CLI 与桌面壳共用的问答逻辑。它一旦沾上 TUI 库或 cobra，
// 桌面壳 import 它就会把整套终端 UI（乃至整个 CLI）链进来——那正是
// spec §4.4.2 否掉「就地导出让薄壳 import cmd」的理由。
// 这道门把该结论钉死在测试里，而不是留在注释里靠人记得。
func TestInitflowHasNoUILayerDeps(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("解析本包依赖失败：%v", err)
	}
	banned := []string{"charmbracelet/huh", "charmbracelet/bubbletea", "spf13/cobra", "mattn/go-isatty"}
	for _, imp := range pkg.Imports {
		for _, b := range banned {
			if strings.Contains(imp, b) {
				t.Errorf("initflow 不得依赖 UI 层：%s", imp)
			}
		}
	}
}
