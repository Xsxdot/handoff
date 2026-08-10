// 本地工作区完整性校验的测试：porcelain 分类、文件名排版、校验入口。
package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestClassifyLocalDirty 穷举 git status --porcelain 的行形态。
//
// 判别力所在：「已暂存改动」「重命名」「冲突」三行——把它们错分成未跟踪
// （或整行丢弃）的实现会在这里翻红，而只测「工作区改动 + 未跟踪」的用例
// 对那种实现照样绿。
func TestClassifyLocalDirty(t *testing.T) {
	cases := []struct {
		name          string
		porcelain     string
		wantTracked   []string
		wantUntracked []string
	}{
		{"干净", "", nil, nil},
		{"只有未跟踪", "?? scratch.md\n?? tmp.log\n", nil, []string{"scratch.md", "tmp.log"}},
		{"工作区改动", " M cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"已暂存改动", "M  cmd/dispatch.go\n", []string{"cmd/dispatch.go"}, nil},
		{"新增已暂存", "A  cmd/new.go\n", []string{"cmd/new.go"}, nil},
		{"删除", " D README.md\n", []string{"README.md"}, nil},
		{"重命名取新名", "R  old.go -> new.go\n", []string{"new.go"}, nil},
		{"冲突", "UU merge.go\n", []string{"merge.go"}, nil},
		{"混合", " M a.go\n?? b.txt\n", []string{"a.go"}, []string{"b.txt"}},
		{"含空格文件名保留引号", " M \"a b.go\"\n", []string{`"a b.go"`}, nil},
		{"空行忽略", " M a.go\n\n", []string{"a.go"}, nil},
		{"过短行忽略", "X\n M a.go\n", []string{"a.go"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracked, untracked := classifyLocalDirty(c.porcelain)
			if !reflect.DeepEqual(tracked, c.wantTracked) {
				t.Errorf("tracked = %#v, want %#v", tracked, c.wantTracked)
			}
			if !reflect.DeepEqual(untracked, c.wantUntracked) {
				t.Errorf("untracked = %#v, want %#v", untracked, c.wantUntracked)
			}
		})
	}
}

// TestFormatDirtyList 钉死文件名列表的截断规则：超过 5 个只列前 5 个并补计数。
func TestFormatDirtyList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"空", nil, ""},
		{"单个", []string{"a.go"}, "a.go"},
		{"恰好五个", []string{"a", "b", "c", "d", "e"}, "a, b, c, d, e"},
		{"六个截断", []string{"a", "b", "c", "d", "e", "f"}, "a, b, c, d, e ... 另有 1 处"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatDirtyList(c.in); got != c.want {
				t.Errorf("formatDirtyList(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// dirtyTestRepo 建一个带一次提交的临时 git 仓库并 chdir 进去，返回仓库路径。
// t.Chdir 会在用例结束时自动切回，并禁止该用例与其他用例并行。
func dirtyTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")
	t.Chdir(dir)
	return dir
}

// TestCheckLocalWorktreeClean 干净仓库放行且零输出。
func TestCheckLocalWorktreeClean(t *testing.T) {
	dirtyTestRepo(t)
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, false); err != nil {
		t.Fatalf("干净工作区不该报错: %v", err)
	}
	if buf.String() != "" {
		t.Fatalf("干净工作区不该有输出，得到: %q", buf.String())
	}
}

// TestCheckLocalWorktreeTrackedRejects 已跟踪改动必须拒发，且错误里带得出文件名。
func TestCheckLocalWorktreeTrackedRejects(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := checkLocalWorktree(&buf, false)
	if err == nil {
		t.Fatal("已跟踪改动应拒发")
	}
	if !strings.Contains(err.Error(), "tracked.txt") {
		t.Fatalf("错误应列出脏文件名，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-dirty") {
		t.Fatalf("错误应给出 --allow-dirty 出路，得到: %v", err)
	}
}

// TestCheckLocalWorktreeAllowDirtyStillWarns 是本任务判别力最强的一条：
// --allow-dirty 放行，但**必须照打警告并列出文件名**。一个「allowDirty 直接
// return nil」的实现会在这里翻红——而那正是把 --allow-dirty 变成新 B29 的写法。
func TestCheckLocalWorktreeAllowDirtyStillWarns(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, true); err != nil {
		t.Fatalf("--allow-dirty 应放行: %v", err)
	}
	if !strings.Contains(buf.String(), "tracked.txt") {
		t.Fatalf("--allow-dirty 放行时仍须列出被忽略的文件，得到: %q", buf.String())
	}
}

// TestCheckLocalWorktreeUntrackedOnlyWarns 只有未跟踪文件时放行并警告。
func TestCheckLocalWorktreeUntrackedOnlyWarns(t *testing.T) {
	dir := dirtyTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := checkLocalWorktree(&buf, false); err != nil {
		t.Fatalf("只有未跟踪文件应放行: %v", err)
	}
	if !strings.Contains(buf.String(), "scratch.md") {
		t.Fatalf("未跟踪文件应被警告列出，得到: %q", buf.String())
	}
}
