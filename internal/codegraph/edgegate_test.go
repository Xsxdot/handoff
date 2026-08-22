// 本文件测试调用边门控：假边判定的两条判据（跨语言、无 import）与保守放行路径。
// 夹具用 t.TempDir 写真实 Go 源文件、真 go/parser 解析——避免夹具编码不存在的语言语义。
package codegraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeEdgegateFile 在 root 下写一个文件，自动建目录。
func writeEdgegateFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// edgegateRepo 造一个最小多包仓库：
//
//	a 包 import b 并调用之；c 包不 import b（其 _test.go import b，用来验证测试文件不算数）；
//	web/x.tsx 是前端文件。
func edgegateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeEdgegateFile(t, root, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	writeEdgegateFile(t, root, "a/a.go", "package a\n\nimport \"example.com/demo/b\"\n\nfunc A() { b.B() }\n")
	writeEdgegateFile(t, root, "b/b.go", "package b\n\nfunc B() {}\n")
	writeEdgegateFile(t, root, "c/c.go", "package c\n\nfunc C() {}\n")
	writeEdgegateFile(t, root, "c/c_test.go", "package c\n\nimport (\n\t\"testing\"\n\n\t\"example.com/demo/b\"\n)\n\nfunc TestC(t *testing.T) { b.B() }\n")
	writeEdgegateFile(t, root, "web/x.tsx", "export function X() {}\n")
	return root
}

func edgegateNodes() map[string]Node {
	return map[string]Node{
		"n_a": {Name: "A", File: "a/a.go"},
		"n_b": {Name: "B", File: "b/b.go"},
		"n_c": {Name: "C", File: "c/c.go"},
		"n_w": {Name: "X", File: "web/x.tsx"},
	}
}

func TestCheckEdgesFlagsNoImportAndCrossLanguage(t *testing.T) {
	root := edgegateRepo(t)
	issues := CheckEdges(root, edgegateNodes(), []Edge{
		{"n_a", "n_b"}, // 真边：a import 了 b → 不报
		{"n_c", "n_b"}, // 假边：c 的生产代码没 import b（c_test.go 不算）→ no-import
		{"n_w", "n_b"}, // 假边：TSX 调 Go → cross-language
		{"n_b", "n_w"}, // 假边：Go 调 TSX → cross-language
	})
	if len(issues) != 3 {
		t.Fatalf("期望 3 条问题，得到 %d: %+v", len(issues), issues)
	}
	byPair := map[string]string{}
	for _, is := range issues {
		byPair[is.From+"→"+is.To] = is.Reason
	}
	if byPair["n_c→n_b"] != "no-import" {
		t.Errorf("n_c→n_b 应报 no-import，得到 %+v", byPair)
	}
	if byPair["n_w→n_b"] != "cross-language" || byPair["n_b→n_w"] != "cross-language" {
		t.Errorf("跨语言边应报 cross-language，得到 %+v", byPair)
	}
}

func TestCheckEdgesPackageGranularity(t *testing.T) {
	// 包粒度语义：同包另一文件 import 了目标包，边就放行——
	// 方法调用可经由字段类型送达，调用文件不必亲自 import（真实反例：agentd/status.go 经 m.st 调 store）。
	root := edgegateRepo(t)
	writeEdgegateFile(t, root, "c/other.go", "package c\n\nimport \"example.com/demo/b\"\n\nfunc Other() { b.B() }\n")
	issues := CheckEdges(root, edgegateNodes(), []Edge{{"n_c", "n_b"}})
	if len(issues) != 0 {
		t.Fatalf("包内任一生产文件 import 即放行，得到 %+v", issues)
	}
}

func TestCheckEdgesConservativeSkips(t *testing.T) {
	root := edgegateRepo(t)
	// 同包边不判；端点缺失不判（归引用完整性）；目录无生产 .go 保守放行。
	nodes := edgegateNodes()
	nodes["n_a2"] = Node{Name: "A2", File: "a/a2.go"}      // 同包
	nodes["n_ghost"] = Node{Name: "G", File: "ghost/g.go"} // 目录不存在
	issues := CheckEdges(root, nodes, []Edge{
		{"n_a", "n_a2"},
		{"n_missing", "n_b"},
		{"n_ghost", "n_b"},
	})
	if len(issues) != 0 {
		t.Fatalf("保守路径不应报问题，得到 %+v", issues)
	}
}

func TestCheckEdgesNoGoMod(t *testing.T) {
	// 纯前端仓没有 go.mod：Go 侧判据整体停用，跨语言判据保留。
	root := t.TempDir()
	writeEdgegateFile(t, root, "a/a.go", "package a\n\nfunc A() {}\n")
	writeEdgegateFile(t, root, "web/x.tsx", "export function X() {}\n")
	nodes := map[string]Node{
		"n_a": {Name: "A", File: "a/a.go"},
		"n_b": {Name: "B", File: "b/b.go"},
		"n_w": {Name: "X", File: "web/x.tsx"},
	}
	issues := CheckEdges(root, nodes, []Edge{{"n_a", "n_b"}, {"n_w", "n_a"}})
	if len(issues) != 1 || issues[0].Reason != "cross-language" {
		t.Fatalf("无 go.mod 时只报跨语言，得到 %+v", issues)
	}
}

func TestEdgeIssueJSONContract(t *testing.T) {
	// EdgeIssue 的 JSON key 是清洗脚本（B173 Task 3）与外部消费方的 wire 契约，锁死。
	raw, err := json.Marshal(EdgeIssue{From: "x", To: "y", Reason: "no-import", Detail: "d"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"from":"x","to":"y","reason":"no-import","detail":"d"}`
	if string(raw) != want {
		t.Fatalf("JSON 契约漂移: %s != %s", raw, want)
	}
}
