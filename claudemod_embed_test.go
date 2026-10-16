// claudemod_embed_test.go —— 锁 B434 契约条 138（内嵌不丢点目录）、条 127（mod 字面量与 Go 侧逐字一致）、
// 条 163（mod 不 import 主模块）。三条都只能在 main 包看到真实的 go:embed 结果，故落在这里。
package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB434ClaudeModEmbedIsExactlyTheMod 锁条 138：二进制内嵌的 mod 恰是三件，含 .claude-plugin/plugin.json，
// 不含测试文件与引擎写的 types。
func TestB434ClaudeModEmbedIsExactlyTheMod(t *testing.T) {
	var got []string
	err := fs.WalkDir(claudeModFS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts"}
	if !slices.Equal(got, want) {
		t.Fatalf("内嵌 mod 文件 = %q，期望 %q", got, want)
	}
	b, err := fs.ReadFile(claudeModFS(), ".claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil || manifest.Name != "handoff-desktop-wake" {
		t.Fatalf("plugin.json name = %q（err=%v），契约 §3.13 冻结为 handoff-desktop-wake", manifest.Name, err)
	}
}

// TestB434ClaudeModEmbedMatchesRepoDir 锁契约 §3.13 r8 维护提醒：go:embed 显式列文件，往 mod 目录加文件
// 却忘了同步列表时这里变红。仓内目录去掉 tests/（plugin test 用例）与 .claude-plugin/types/（引擎写入）
// 后，文件集合与内嵌恰好相同，且逐字节相等。
func TestB434ClaudeModEmbedMatchesRepoDir(t *testing.T) {
	root := filepath.FromSlash(claudeModDir)
	onDisk := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "tests" || rel == ".claude-plugin/types" {
				return fs.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		onDisk[rel] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	embedded := map[string][]byte{}
	err = fs.WalkDir(claudeModFS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(claudeModFS(), p)
		embedded[p] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, b := range onDisk {
		e, ok := embedded[rel]
		if !ok {
			t.Fatalf("仓内 %s/%s 未内嵌：往 mod 加文件须同步 main.go 的 go:embed 列表", claudeModDir, rel)
		}
		if !bytes.Equal(e, b) {
			t.Fatalf("内嵌 %s 与仓内文件不一致", rel)
		}
	}
	for rel := range embedded {
		if _, ok := onDisk[rel]; !ok {
			t.Fatalf("内嵌 %s 在仓内目录（去 tests/、types/）中不存在", rel)
		}
	}
}

// TestB434ClaudeModLiteralsMatchProto 锁条 127：mod 以 $.env.get('HOME') 取 HOME，信箱目录与标记字面量
// 与 proto 逐字一致，且不含硬编码绝对 HOME 与安装期占位符。
func TestB434ClaudeModLiteralsMatchProto(t *testing.T) {
	b, err := fs.ReadFile(claudeModFS(), "hooks/register.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"'" + proto.DesktopWakeMarker + "'",
		"'" + proto.DesktopWakeDirRel + "'",
		"$.env.get('HOME')",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("register.ts 缺少 %s", want)
		}
	}
	for _, banned := range []string{"/Users/", "/home/", "{{", "__HOME__", "@HOME@"} {
		if strings.Contains(src, banned) {
			t.Fatalf("register.ts 含 %q：mod 不得硬编码 HOME 或留安装期占位符（契约 §10.8）", banned)
		}
	}
}

// TestB434ClaudeModImportsNothingFromMainModule 锁条 163：mod 源码只 import 'claude-code'（类型）。
func TestB434ClaudeModImportsNothingFromMainModule(t *testing.T) {
	b, err := fs.ReadFile(claudeModFS(), "hooks/register.ts")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^\s*import\b[^'"]*['"]([^'"]+)['"]`)
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		if m[1] != "claude-code" {
			t.Fatalf("register.ts import 了 %q：mod 是独立交付物，只能 import 'claude-code'", m[1])
		}
	}
	if strings.Contains(string(b), "github.com/Xsxdot/handoff") {
		t.Fatal("register.ts 引用了主模块路径")
	}
}
