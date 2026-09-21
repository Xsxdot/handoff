package bind

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// wantBindSurface 是绑定面的全部导出函数签名（壳的交接面，P4=A/P5=A 冻结；
// B386 按 gomobile 真实支持集合修订：列表改计数/按索引取指针）。
// 顺序无关；多一个、少一个、签名漂移都红。
var wantBindSurface = []string{
	"func Close() error",
	"func MachineAt(index int) *Machine",
	"func MachineCount() int",
	"func Origin(machine string) (string, error)",
	"func Pair(bundleJSON string) error",
	"func SessionCookie(machine string) (string, error)",
	"func SwitchMachine(machine string) (string, error)",
}

// TestBindExportedSurfaceIsFrozen 断言绑定面导出函数集合与签名逐字等于冻结面，
// 且不含 Token/Dial（回环门禁承重属性：绑定面不得暴露绕过 cookie 闸的方法）。
func TestBindExportedSurfaceIsFrozen(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv != nil || !fd.Name.IsExported() {
					continue
				}
				var tb bytes.Buffer
				printer.Fprint(&tb, fset, fd.Type)
				got = append(got, "func "+fd.Name.Name+strings.TrimPrefix(tb.String(), "func"))
			}
		}
	}
	sort.Strings(got)
	want := append([]string(nil), wantBindSurface...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("绑定面导出函数集合漂移:\n got=%q\nwant=%q", got, want)
	}
	for _, s := range got {
		low := strings.ToLower(s)
		if strings.Contains(low, "token") || strings.Contains(low, "dial") {
			t.Fatalf("绑定面不得导出 Token/Dial（门禁绕过面）: %s", s)
		}
	}
}

// TestBindProductionHasNoProtocolLogic：绑定层生产文件只许 import 标准库与
// internal/mobilecore；不得 import internal/relay、internal/client、internal/proto
// （协议逻辑零重实现：一切拨号/选路/编解码在核内，壳侧不碰协议）。
func TestBindProductionHasNoProtocolLogic(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"github.com/Xsxdot/handoff/internal/relay",
		"github.com/Xsxdot/handoff/internal/client",
		"github.com/Xsxdot/handoff/internal/proto",
	}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, "`\"")
				for _, bad := range forbidden {
					if p == bad {
						t.Fatalf("%s 属于协议实现层，绑定层不得 import（协议零重实现）: %s", p, filepath.Base(name))
					}
				}
			}
		}
	}
}

// resultTypes 返回绑定面回给壳的结构体类型（跨 gomobile 映射边界的全部类型）。
// B386 后只有本包定义的 Machine——跨包类型会被 gomobile 静默跳过。
func resultTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(Machine{}),
	}
}
