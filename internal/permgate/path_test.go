package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInScopeUsesTaskTmpAsThirdRoot locks the executor-owned task scratch area
// into the same path gate as worktree and TaskDir.
func TestInScopeUsesTaskTmpAsThirdRoot(t *testing.T) {
	root := t.TempDir()
	scope := Scope{
		Workdir:    filepath.Join(root, "work"),
		TaskDir:    filepath.Join(root, "tasks", "task-1"),
		TaskTmpDir: filepath.Join(root, "tmp", "abcd1234"),
	}
	for _, dir := range []string{scope.Workdir, scope.TaskDir, scope.TaskTmpDir, filepath.Join(root, "tmp", "shared")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name     string
		path     string
		wantIn   bool
		wantBase string
	}{
		{"worktree", filepath.Join(scope.Workdir, "out.txt"), true, scope.Workdir},
		{"task-dir", filepath.Join(scope.TaskDir, "log.txt"), true, scope.TaskDir},
		{"task-tmp", filepath.Join(scope.TaskTmpDir, "out.txt"), true, scope.TaskTmpDir},
		{"shared-tmp", filepath.Join(root, "tmp", "shared", "out.txt"), false, ""},
		{"prefix-sibling", filepath.Join(root, "tmp", "abcd1234-sibling", "out.txt"), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIn, gotBase, err := InScope(tc.path, scope)
			if err != nil {
				t.Fatalf("InScope(%q): %v", tc.path, err)
			}
			if gotIn != tc.wantIn || gotBase != tc.wantBase {
				t.Fatalf("InScope(%q) = (%v, %q), want (%v, %q)", tc.path, gotIn, gotBase, tc.wantIn, tc.wantBase)
			}
		})
	}
}

// TestInScopeAccepts 范围内的路径都应判为 in。
func TestInScopeAccepts(t *testing.T) {
	work := t.TempDir()
	task := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: task}
	cases := []string{
		filepath.Join(work, "main.go"),
		filepath.Join(work, "internal", "a", "b.go"), // 目录尚不存在
		"main.go", // 相对路径按 Workdir 解析
		"./internal/x.go",
		filepath.Join(task, "notes.md"),
	}
	for _, p := range cases {
		in, base, err := InScope(p, sc)
		if err != nil {
			t.Fatalf("InScope(%q) 报错: %v", p, err)
		}
		if !in {
			t.Errorf("应判为范围内: %q", p)
			continue
		}
		if base == "" {
			t.Errorf("范围内必须回报命中的基准目录: %q", p)
		}
	}
}

// TestInScopeRejectsPrefixTrap 锁死「不得用字符串前缀」这条约束。
//
// /repo-evil 以 /repo 开头，strings.HasPrefix 会把它误判成仓库内部。
func TestInScopeRejectsPrefixTrap(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "repo")
	evil := filepath.Join(root, "repo-evil")
	for _, d := range []string{work, evil} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("建目录 %s: %v", d, err)
		}
	}
	in, _, err := InScope(filepath.Join(evil, "x.go"), Scope{Workdir: work})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("repo-evil 不是 repo 的子目录，必须判为越界（前缀匹配的经典陷阱）")
	}
}

// TestInScopeRejectsSymlinkEscape 锁死软链逃逸。
//
// 仓库里放一个指向仓库外的软链，经它写出去必须判越界，否则
// `ln -s ~ /repo/link` 之后写 /repo/link/.ssh/authorized_keys 就绕过了。
func TestInScopeRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{work, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("建目录 %s: %v", d, err)
		}
	}
	link := filepath.Join(work, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("建软链: %v", err)
	}
	in, _, err := InScope(filepath.Join(link, "pwned"), Scope{Workdir: work})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("经软链写到仓库外必须判越界")
	}
}

// TestInScopeRejectsOutside 常见的宿主机敏感路径必须判越界。
func TestInScopeRejectsOutside(t *testing.T) {
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir()}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("取 home: %v", err)
	}
	cases := []string{
		filepath.Join(home, ".ssh", "authorized_keys"),
		filepath.Join(home, ".zshrc"),
		"/etc/hosts",
		filepath.Join(work, "..", "escape.go"), // 相对回退
	}
	for _, p := range cases {
		in, _, err := InScope(p, sc)
		if err != nil {
			t.Fatalf("InScope(%q) 报错: %v", p, err)
		}
		if in {
			t.Errorf("必须判为越界: %q", p)
		}
	}
}

// TestInScopeEmptyBaseIgnored TaskDir 为空时不参与判定，不得因此把任何
// 路径判成范围内。
func TestInScopeEmptyBaseIgnored(t *testing.T) {
	work := t.TempDir()
	in, _, err := InScope("/etc/hosts", Scope{Workdir: work, TaskDir: ""})
	if err != nil {
		t.Fatalf("InScope 报错: %v", err)
	}
	if in {
		t.Fatal("TaskDir 为空时不得放宽判定")
	}
}
