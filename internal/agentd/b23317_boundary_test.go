package agentd

import (
	"encoding/json"
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

// b23317AssemblyFiles 读仓库 target.json 的 assembly 名单。
func b23317AssemblyFiles(t *testing.T) (repo string, assembly []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	repo = filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	raw, err := os.ReadFile(filepath.Join(repo, "codegraph", "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tgt struct {
		Assembly []string `json:"assembly"`
	}
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	return repo, tgt.Assembly
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

// TestB23317AssemblyFreezeExcludesHandlerFile：锁定本节点冻结的 target.json
// assembly 名单——handler-only 的 internal/agentd/codegraph.go 必须不在名单里
// （缝 1：免检只留开机插线）。server.go 瘦身归 implement，届时由 b23317HandlerDefsIn
// 的当前树断言接管（见 contract 移交 plan）。
func TestB23317AssemblyFreezeExcludesHandlerFile(t *testing.T) {
	_, assembly := b23317AssemblyFiles(t)
	for _, f := range assembly {
		if f == "internal/agentd/codegraph.go" {
			t.Fatalf("codegraph.go 整文件是 handler，必须退出 assembly 免检名单（缝 1）")
		}
	}
}

// b23317FrozenContract 是本节点冻结的有门面方向配额（缝 3）。预算只降不抬：
// 把配额调大来让 red 变绿即违法，本测试是那条纪律的可执行锁。interfaces 也一并
// 锁住，防止删接口假绿。
type b23317FrozenContract struct {
	From, To   string
	Budget     int
	Interfaces []string
}

var b23317FrozenContracts = []b23317FrozenContract{
	{From: "d_gateway", To: "d_orchestration", Budget: 0, Interfaces: []string{"OrchestrationClient"}},
	{From: "d_cli", To: "d_workspace", Budget: 0},
	{From: "d_gateway", To: "d_workspace", Budget: 0},
	{From: "d_orchestration", To: "d_workspace", Budget: 0},
	{From: "d_workspace", To: "d_orchestration", Budget: 0},
	{From: "d_workspace", To: "d_protocol", Budget: 0},
}

// TestB23317FacadeBudgetsStayRatchet 断言六条有门面方向的 legacyBudget 不高于
// 冻结值（只降不抬），且 d_gateway→d_orchestration 的接口声明仍在。
func TestB23317FacadeBudgetsStayRatchet(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	repo := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	raw, err := os.ReadFile(filepath.Join(repo, "codegraph", "target.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tgt struct {
		Contracts []struct {
			From         string   `json:"from"`
			To           string   `json:"to"`
			LegacyBudget int      `json:"legacyBudget"`
			Interfaces   []string `json:"interfaces"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(raw, &tgt); err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, c := range tgt.Contracts {
		idx[c.From+"->"+c.To] = i
	}
	for _, want := range b23317FrozenContracts {
		i, ok := idx[want.From+"->"+want.To]
		if !ok {
			t.Fatalf("冻结方向 %s→%s 的契约条目缺失", want.From, want.To)
		}
		got := tgt.Contracts[i].LegacyBudget
		if got > want.Budget {
			t.Fatalf("%s→%s 配额 %d 高于冻结值 %d——棘轮只降不抬，不许抬配额抹绿",
				want.From, want.To, got, want.Budget)
		}
		for _, iface := range want.Interfaces {
			if !containsStr(tgt.Contracts[i].Interfaces, iface) {
				t.Fatalf("%s→%s 的 interfaces 缺 %q（删接口即假绿）", want.From, want.To, iface)
			}
		}
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
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
