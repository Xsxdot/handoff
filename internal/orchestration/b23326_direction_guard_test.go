// B233.26 方向守卫：D1 反转后编排域是依赖单向的下沉侧——生产代码不得反向
// import gateway（internal/agentd）。旧守卫（internal/agentd/b23313_managerfactory_test.go
// 的「agentd 生产不得 import orchestration」出站封界）随反转失真退役，执法方向
// 就地反转到本文件；写法沿 b23317_boundary_test.go 的源码级机械前置先例——在
// 编译之前把违规报红（若真出现反向 import，编译期会以 import cycle 拒绝，本守卫
// 是它的前置哨兵），并配变异哨兵证明守卫有牙。
//
// 边界：只查 import 路径，不查 GOFILE / 构建约束；测试文件不参与（包内白盒测试
// 与外部测试包 import agentd 是合法的测试面）。
package orchestration

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// b23326ForbiddenImport 是反转后唯一禁止的方向：编排生产 → gateway 生产。
const b23326ForbiddenImport = "github.com/Xsxdot/handoff/internal/agentd"

// b23326OrchestrationProductionFiles 返回 orchestration 生产 .go 文件（非 _test.go）。
func b23326OrchestrationProductionFiles(t *testing.T) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile) // internal/orchestration/
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files
}

// TestB23326OrchestrationProductionDoesNotImportAgentd：编排生产文件零
// internal/agentd import（D1 反转后的单向实证，`go list -deps` 的源码级前置）。
func TestB23326OrchestrationProductionDoesNotImportAgentd(t *testing.T) {
	var violations []string
	for _, file := range b23326OrchestrationProductionFiles(t) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, "`\""); p == b23326ForbiddenImport {
				violations = append(violations, filepath.Base(file))
			}
		}
	}
	if len(violations) != 0 {
		t.Fatalf("orchestration 生产文件不得反向 import gateway（D1 反转守卫）：%v", violations)
	}
}

// TestB23326DirectionGuardHasTeeth 是可变红哨兵：人为构造的反向 import 必须被判红，
// 同包与合法第三方 import、注释里的同形文本必须放行。
func TestB23326DirectionGuardHasTeeth(t *testing.T) {
	bad := "package orchestration\n\nimport _ \"" + b23326ForbiddenImport + "\"\n"
	fset := token.NewFileSet()
	badFile, err := parser.ParseFile(fset, "bad.go", bad, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, imp := range badFile.Imports {
		if strings.Trim(imp.Path.Value, "`\"") == b23326ForbiddenImport {
			hit = true
		}
	}
	if !hit {
		t.Fatal("反向 import 必须被判红（守卫无牙）")
	}

	// 同包相对引用与第三方 import 放行；注释里的路径文本不参与解析
	good := "package orchestration\n\nimport (\n\t\"github.com/Xsxdot/handoff/internal/workspace\"\n)\n\n// 见 github.com/Xsxdot/handoff/internal/agentd 的说明\nvar _ = workspace.ManualWorktreeRoot\n"
	goodFile, err := parser.ParseFile(fset, "good.go", good, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range goodFile.Imports {
		if strings.Trim(imp.Path.Value, "`\"") == b23326ForbiddenImport {
			t.Fatalf("合法 import 不得误报：%s", imp.Path.Value)
		}
	}
}
