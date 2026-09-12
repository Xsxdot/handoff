package cmd

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestCLIExecutionCommandsDoNotHandRollHTTP(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	files := []string{"dispatch.go", "wait.go", "reply.go", "continue.go", "stop.go"}
	needles := []string{".HTTPClient()", "http.NewRequest", "client.NewRelay("}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		for _, n := range needles {
			if strings.Contains(text, n) {
				t.Fatalf("%s 生产路径不得手拼 HTTP / 取裸客户端（含 %q）", name, n)
			}
		}
	}
}

// ---- B233.16 T3：执行面不得持聚合依赖（符号级白名单 + 剔除注释/字符串） ----

// executionSurfaceFiles 是「跨机执行消费点」所在的生产文件。工厂所在文件
// （root.go）不在其列：工厂保留聚合是 contract #40 明许的组装形态。
var executionSurfaceFiles = []string{
	"dispatch.go", "wait.go", "reply.go", "continue.go", "stop.go", "card_dispatch.go",
}

// executionFactorySymbols 是唯一放行的符号：聚合只允许出现在工厂里（P4=A）。
var executionFactorySymbols = map[string]bool{
	"newTargetClient":      true,
	"newTargetClientNamed": true,
	"targetClient":         true,
}

// stripCommentsAndStrings 用 go/scanner 重建源码：注释与字符串/字符字面量的内容
// 替换为等长空白（保留换行），其余 token 原样回填到原偏移——这样后续按行做符号级
// 扫描时不会把注释里的示例文本或字符串内容误判成聚合依赖。
func stripCommentsAndStrings(src []byte) []byte {
	out := make([]byte, len(src))
	for i := range out {
		out[i] = ' '
	}
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			continue
		}
		text := lit
		if tok == token.STRING || tok == token.CHAR {
			text = blankPreservingNewlines(lit)
		} else if text == "" {
			text = tok.String()
		}
		copy(out[file.Offset(pos):], text)
	}
	return out
}

// blankPreservingNewlines 把字面量内容替换为等长空白但保留换行，避免把多行
// 原始字符串压成一行而错乱行号。
func blankPreservingNewlines(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\n' && b[i] != '\r' {
			b[i] = ' '
		}
	}
	return string(b)
}

// executionSurfaceViolations 返回非白名单符号里的聚合用法（行号:符号）。
// cleaned 必须已经过 stripCommentsAndStrings。
func executionSurfaceViolations(cleaned []byte) []string {
	funcRe := regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([A-Za-z_]\w*)`)
	var out []string
	current := ""
	for i, line := range strings.Split(string(cleaned), "\n") {
		if m := funcRe.FindStringSubmatch(strings.TrimLeft(line, " \t")); m != nil {
			current = m[1]
		}
		if !strings.Contains(line, "*client.Client") && !strings.Contains(line, "client.New(") {
			continue
		}
		if executionFactorySymbols[current] {
			continue
		}
		sym := current
		if sym == "" {
			sym = "<package>"
		}
		out = append(out, fmt.Sprintf("%d:%s", i+1, sym))
	}
	return out
}

// TestCLIExecutionSurfaceHoldsNarrowDependencies：执行面生产文件不得出现
// 非白名单的 *client.Client / client.New。
func TestCLIExecutionSurfaceHoldsNarrowDependencies(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	dir := filepath.Dir(thisFile)
	var violations []string
	for _, name := range executionSurfaceFiles {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range executionSurfaceViolations(stripCommentsAndStrings(body)) {
			violations = append(violations, name+":"+v)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("执行面文件不得持聚合依赖（白名单仅工厂符号 %v）：%v",
			executionFactorySymbols, violations)
	}
}

// TestCLIExecutionSurfaceGuardHasTeeth 是可变红守卫：人为构造的非白名单聚合用法
// 必须被判红，工厂符号与注释/字符串里的同形文本必须放行。只断言「今日绿」不足以
// 证明守卫有牙（P4=A）。
func TestCLIExecutionSurfaceGuardHasTeeth(t *testing.T) {
	nonFactory := []byte("package cmd\n\nfunc sneaky() *client.Client {\n\treturn client.New(\"a\", \"b\")\n}\n")
	if got := executionSurfaceViolations(stripCommentsAndStrings(nonFactory)); len(got) == 0 {
		t.Fatal("非白名单聚合用法必须被判红（守卫无牙）")
	}

	factory := []byte("package cmd\n\nfunc targetClient(t string) (*client.Client, func(), error) {\n\treturn client.New(\"a\", \"b\"), func() {}, nil\n}\n")
	if got := executionSurfaceViolations(stripCommentsAndStrings(factory)); len(got) != 0 {
		t.Fatalf("工厂符号应被白名单放行，实得 %v", got)
	}

	decoys := []byte("package cmd\n\n// 说明：*client.Client 与 client.New( 只出现在注释里\nvar note = \"*client.Client client.New(\"\n")
	if got := executionSurfaceViolations(stripCommentsAndStrings(decoys)); len(got) != 0 {
		t.Fatalf("注释/字符串里的同形文本不得误报，实得 %v", got)
	}
}
