package agentd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// B233.17 缝 2：四个已迁出的后厨包把实现细节封进 internal/<包>/internal/ 后，
// gateway 生产文件（以及 cmd 组装点）只能 import 各自的公开包，不得 import
// 嵌套的里间路径。Go 的 internal 可见性规则本身会在编译期拒绝
// internal/agentd import internal/orchestration/internal/…；本守卫是源码级的
// 机械前置——在编译之前就把这条边界报红，并配变异哨兵证明守卫有牙。
//
// 边界：只查 import 路径，不查 GOFILE / 构建约束；测试文件不参与（包内白盒测试
// 随搬迁，不占接缝名额）。实质实现体是否已搬进嵌套目录归 implement，不在本守卫。
var b23317NestedInternalPrefixes = []string{
	"internal/orchestration/internal/",
	"internal/scheduling/internal/",
	"internal/workspace/internal/",
	"internal/approval/internal/",
}

// b23317NestedImportViolations 返回 src 中对四包嵌套 internal 路径的 import
// 原文（去引号后的路径）。src 必须是单个 Go 文件的源码。
func b23317NestedImportViolations(src []byte) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if err != nil {
		return []string{"parse: " + err.Error()}
	}
	var out []string
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, "`\"")
		for _, pre := range b23317NestedInternalPrefixes {
			if strings.Contains(p, pre) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// b23317ProductionFiles 返回 repo 内需要执法的生产 .go 文件（非 _test.go）。
func b23317ProductionFiles(t *testing.T) (repo string, files []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	repo = filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // 去掉 internal/agentd/<file>
	for _, dir := range []string{"internal/agentd", "cmd"} {
		entries, err := os.ReadDir(filepath.Join(repo, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			files = append(files, filepath.Join(dir, name))
		}
	}
	return repo, files
}

// TestB23317GatewayProductionDoesNotImportNestedInternals：gateway 生产文件与
// cmd 组装点不得 import 四包的嵌套 internal 路径。
func TestB23317GatewayProductionDoesNotImportNestedInternals(t *testing.T) {
	repo, files := b23317ProductionFiles(t)
	var violations []string
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range b23317NestedImportViolations(body) {
			violations = append(violations, rel+": "+v)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("gateway 生产文件不得 import 后厨里间路径（缝 2）：%v", violations)
	}
}

// TestB23317NestedInternalGuardHasTeeth 是可变红哨兵：人为构造的嵌套 internal
// import 必须被判红，公开包 import 与注释里的同形文本必须放行。
func TestB23317NestedInternalGuardHasTeeth(t *testing.T) {
	bad := []byte("package agentd\n\nimport _ \"github.com/Xsxdot/handoff/internal/orchestration/internal/hidden\"\n")
	if got := b23317NestedImportViolations(bad); len(got) == 0 {
		t.Fatal("嵌套 internal import 必须被判红（守卫无牙）")
	}

	good := []byte("package agentd\n\nimport (\n\t\"github.com/Xsxdot/handoff/internal/orchestration\"\n\t\"github.com/Xsxdot/handoff/internal/workspace\"\n)\n")
	if got := b23317NestedImportViolations(good); len(got) != 0 {
		t.Fatalf("公开包 import 应放行，实得 %v", got)
	}

	decoy := []byte("package agentd\n\n// 说明：internal/orchestration/internal/ 只出现在注释里\nvar note = \"internal/workspace/internal/\"\n")
	if got := b23317NestedImportViolations(decoy); len(got) != 0 {
		t.Fatalf("注释/字符串里的同形文本不得误报，实得 %v", got)
	}
}

// ---- B233.17 缝 1：handler 定义不得住在 assembly 名单文件里 ----

// b23317AssemblyFiles 返回缝 1 的 assembly 名单（开机插线文件，免于 handler 定义
// 检查）。名单自 B436 起由本测试持有：原先读自 codegraph/target.json 的 assembly
// 字段，代码图消费侧随 B436 整体删除后，这里写死的即是删除时刻的现值；增减插线
// 文件须在同一提交改这里并写明理由。
func b23317AssemblyFiles(t *testing.T) (repo string, assembly []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	repo = filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return repo, []string{"main.go", "internal/agentd/server.go", "cmd/agentd.go"}
}

// b23317HandlerDefsIn 返回 src 中定义的 Server.handle* 方法名。
func b23317HandlerDefsIn(src []byte) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "b23317.go", src, 0)
	if err != nil {
		return []string{"parse: " + err.Error()}
	}
	var out []string
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
			continue
		}
		if !strings.HasPrefix(fd.Name.Name, "handle") {
			continue
		}
		recv := fd.Recv.List[0].Type
		star, ok := recv.(*ast.StarExpr)
		if !ok {
			continue
		}
		if id, ok := star.X.(*ast.Ident); ok && id.Name == "Server" {
			out = append(out, "Server."+fd.Name.Name)
		}
	}
	return out
}

// TestB23317AssemblyFilesHaveNoHandlerDefs 是缝 1 的实质断言：assembly 名单里的
// 任何 .go 文件都不得定义 Server.handle*（免检只留给开机插线）。handler 搬迁前
// server.go 含 21 个 handle*，本测试必红。
func TestB23317AssemblyFilesHaveNoHandlerDefs(t *testing.T) {
	repo, assembly := b23317AssemblyFiles(t)
	for _, rel := range assembly {
		body, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("读 assembly 文件 %s: %v", rel, err)
		}
		if got := b23317HandlerDefsIn(body); len(got) != 0 {
			t.Fatalf("assembly 文件 %s 不得定义 Server.handle*（缝 1）：%v", rel, got)
		}
	}
}

// TestB23317HandlerGuardHasTeeth 是可变红哨兵：assembly 名单文件里塞回一个
// handle* 定义必须被判红，非 Server 接收者与注释里的同形文本必须放行。
func TestB23317HandlerGuardHasTeeth(t *testing.T) {
	bad := []byte("package agentd\n\nfunc (s *Server) handleSneaky(w http.ResponseWriter, r *http.Request) {}\n")
	if got := b23317HandlerDefsIn(bad); len(got) == 0 {
		t.Fatal("assembly 文件里的 handle* 定义必须被判红（守卫无牙）")
	}

	other := []byte("package agentd\n\nfunc (x *Other) handleSneaky() {}\n")
	if got := b23317HandlerDefsIn(other); len(got) != 0 {
		t.Fatalf("非 Server 接收者应放行，实得 %v", got)
	}

	decoy := []byte("package agentd\n\n// func (s *Server) handleGhost() {}\nvar note = \"handleGhost\"\n")
	if got := b23317HandlerDefsIn(decoy); len(got) != 0 {
		t.Fatalf("注释/字符串里的同形文本不得误报，实得 %v", got)
	}
}
