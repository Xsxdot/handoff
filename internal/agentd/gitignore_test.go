// markIgnored 的测试：真起一个 git 仓库，判据必须与 git 自己一致。
package agentd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newIgnoreRepo 建一个带 .gitignore 的临时仓库，返回仓库路径。
func newIgnoreRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("环境没有可用的 git，跳过：%v %s", err, out)
	}
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("建目录: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("写 %s: %v", rel, err)
		}
	}
	// node_modules/ 与 *.out 被忽略；!keep.out 覆盖否定规则——自己解析 .gitignore
	// 的实现最容易在这一条上出错，所以必须钉住
	write(".gitignore", "node_modules/\n*.out\n!keep.out\n")
	write("main.go", "package main\n")
	write("coverage.out", "x\n")
	write("keep.out", "x\n")
	write("node_modules/pkg/index.js", "x\n")
	write("internal/a.go", "package internal\n")
	write("internal/tmp.out", "x\n")
	return dir
}

// TestMarkIgnoredFollowsGit 断言：根层的忽略判定与 git 一致（含否定规则与目录）。
func TestMarkIgnoredFollowsGit(t *testing.T) {
	repo := newIgnoreRepo(t)
	entries := []proto.DirEntry{
		{Name: "internal", IsDir: true},
		{Name: "node_modules", IsDir: true},
		{Name: ".gitignore"},
		{Name: "coverage.out"},
		{Name: "keep.out"},
		{Name: "main.go"},
	}
	markIgnored(context.Background(), repo, "", entries)

	want := map[string]bool{
		"internal": false, "node_modules": true, ".gitignore": false,
		"coverage.out": true, "keep.out": false, "main.go": false,
	}
	for _, e := range entries {
		if e.Ignored != want[e.Name] {
			t.Errorf("%s 的 ignored = %v，期望 %v", e.Name, e.Ignored, want[e.Name])
		}
	}
}

// TestMarkIgnoredInSubdir 断言：子层的路径按仓库根拼接，不会把 rel 丢掉
// （丢掉 rel 会让 internal/tmp.out 被当成根下的 tmp.out——恰好也命中 *.out，
// 所以这条用例必须同时验证一个**在子层不命中**的文件）。
func TestMarkIgnoredInSubdir(t *testing.T) {
	repo := newIgnoreRepo(t)
	entries := []proto.DirEntry{{Name: "a.go"}, {Name: "tmp.out"}}
	markIgnored(context.Background(), repo, "internal", entries)
	if entries[0].Ignored {
		t.Error("internal/a.go 被标为忽略")
	}
	if !entries[1].Ignored {
		t.Error("internal/tmp.out 没被标为忽略")
	}
}

// TestMarkIgnoredNonRepoFailsOpen 断言：目录不是 git 仓库时不报错、不标记
// （fail open 是本函数的核心纪律：宁可少标，不可标错）。
func TestMarkIgnoredNonRepoFailsOpen(t *testing.T) {
	dir := t.TempDir()
	entries := []proto.DirEntry{{Name: "a.txt"}}
	markIgnored(context.Background(), dir, "", entries)
	if entries[0].Ignored {
		t.Error("非 git 目录里的条目被标成了忽略")
	}
}

// TestMarkIgnoredEmptyEntries 断言：空列表直接返回，不起子进程。
func TestMarkIgnoredEmptyEntries(t *testing.T) {
	markIgnored(context.Background(), t.TempDir(), "", nil)
}
