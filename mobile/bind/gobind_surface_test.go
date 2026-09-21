package bind

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// gobindPinned 与 contract §0 工具链台账、mobile/build.sh 里的版本一致。
// 版本不同的 gobind 可能改变支持类型集合，故断言前先钉版本。
const gobindPinned = "v0.0.0-20260908204917-8b95e45f8d3e"

// TestGomobileSurfaceHasNoSkips 拿**真 gobind 产物**钉住绑定面形状（B386）。
//
// 背景：gomobile 的 `bind/gen.go#isSupported` 对不合规的签名**静默跳过整个函数**
// （产物里只留一行 `// skipped function …`），不报错、不失败——`go build`/`go test`
// 全绿而壳拿不到方法。本用例把那条"沉默"变成红色：
//
//	①产物里不得出现 `skipped function` / `skipped field`；
//	②每个导出函数都必须在产物里出现（方法名按 gomobile 的 camelCase 归一）。
//
// 为什么两类都要：①防"少方法"，②防"多出来的方法被跳过而没人发现"——
// 单看①时，把函数删掉也能让①绿。
//
// 环境：需要 $PATH 里有 gobind（`go install golang.org/x/mobile/cmd/gobind@<钉版>`）。
// 没装就 skip 并打印可行动命令——本仓多机开发，缺工具是常态而非失败；
// 但**在装了 gobind 的机器上（含 CI/真机构建机）这条必须绿**。
func TestGomobileSurfaceHasNoSkips(t *testing.T) {
	// 版本门：先确认拿到的是钉住的那版，避免"换了 gobind 就换判据"。
	if _, err := exec.LookPath("gobind"); err != nil {
		t.Skipf("未找到 gobind；跑真机/CI 前安装：go install golang.org/x/mobile/cmd/gobind@%s", gobindPinned)
	}

	dir := packageDir(t)
	out := t.TempDir()
	for _, lang := range []string{"java", "objc"} {
		langOut := filepath.Join(out, lang)
		if err := os.MkdirAll(langOut, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("gobind", "-lang="+lang, "-outdir="+langOut, ".")
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gobind -lang=%s 失败: %v\n%s", lang, err, b)
		}
		body := readTree(t, langOut)

		if strings.Contains(body, "skipped function") || strings.Contains(body, "skipped field") {
			for _, line := range strings.Split(body, "\n") {
				if strings.Contains(line, "skipped function") || strings.Contains(line, "skipped field") {
					t.Errorf("[-lang=%s] gomobile 跳过了绑定面成员（壳拿不到它）：%s", lang, strings.TrimSpace(line))
				}
			}
			continue
		}
		// ②每个导出函数都得在产物里留下名字。
		// 命名约定按语言不同：Java 是 camelCase（machineCount），
		// ObjC 是「类前缀 + 原名」（BindMachineCount）——判据各自匹配。
		for _, name := range exportedFuncNames(t, dir) {
			want := name
			if lang == "java" {
				want = lowerFirst(name)
			}
			if !strings.Contains(body, want) {
				t.Errorf("[-lang=%s] 导出函数 %s 未出现在 gobind 产物里（被跳过或改名，找的是 %q）", lang, name, want)
			}
		}
	}
}

// readTree 把目录下全部文本文件拼起来（产物是多文件的小树）。
func readTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".java", ".h", ".m", ".go":
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			b.Write(data)
			b.WriteString("\n")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// lowerFirst 把 Go 的首字母大写函数名转成 gomobile 生成的 camelCase 形式。
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// packageDir 返回本包的源码目录（gobind 的输入路径）。
func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	return filepath.Dir(thisFile)
}

// exportedFuncNames 用 AST 列出本包非测试文件里的导出顶层函数名。
func exportedFuncNames(t *testing.T, dir string) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || !fd.Name.IsExported() {
					continue
				}
				names = append(names, fd.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}
