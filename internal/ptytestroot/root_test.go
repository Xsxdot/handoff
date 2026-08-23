// Package ptytestroot 的测试覆盖 PTY 测试根目录决策的纯函数与回归边界。
//
// 职责：覆盖候选优先级、权限失败、长度失败、覆盖根和点号目录包枚举边界。
// 边界：不启动真实 PTY，不改当前仓库的临时根，只在 t.TempDir 的探针模块中运行 go list。
package ptytestroot

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestChoosePrefersTmp(t *testing.T) {
	tmpRoot := filepath.Join(string(filepath.Separator), "tmp", "handoff-pty-tmp")
	dotRoot := filepath.Join(string(filepath.Separator), "repo", ".pty-test-root", "handoff-pty-repo")
	got, err := Choose([]Candidate{
		{Root: tmpRoot, Source: SourceTmp},
		{Root: dotRoot, Source: SourceRepoDot},
	}, SocketIDForBudget, SocketPathLimit)
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if got.Root != tmpRoot || got.Source != SourceTmp {
		t.Fatalf("decision = %+v，期望优先 /tmp 候选", got)
	}
	wantPath := filepath.Join(tmpRoot, SocketIDForBudget, "sock")
	if got.SocketPath != wantPath || got.SocketBytes != len([]byte(wantPath)) {
		t.Fatalf("socket = %q/%d，期望 %q/%d", got.SocketPath, got.SocketBytes, wantPath, len([]byte(wantPath)))
	}
}

func TestResolveFallsBackToRepoDotWhenTmpCannotBeCreated(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repo")
	fs := fileSystem{
		mkdirTemp: func(parent, prefix string) (string, error) {
			if parent == "/tmp" {
				return "", errors.New("read-only /tmp")
			}
			return filepath.Join(parent, prefix+"123"), nil
		},
		mkdirAll: func(string, os.FileMode) error { return nil },
	}
	got, err := resolveFrom(repo, SocketIDForBudget, SocketPathLimit, "", fs, quietLogger())
	if err != nil {
		t.Fatalf("resolveFrom: %v", err)
	}
	if got.Source != SourceRepoDot {
		t.Fatalf("source = %q，期望 repo-dot", got.Source)
	}
	if filepath.Base(filepath.Dir(got.Root)) != ".pty-test-root" {
		t.Fatalf("root = %q，必须位于点号目录下", got.Root)
	}
	if got.SocketBytes > SocketPathLimit {
		t.Fatalf("socket path = %d bytes，超过 %d", got.SocketBytes, SocketPathLimit)
	}
	got.Cleanup()
}

func TestResolveOverrideUsesOnlyConfiguredRoot(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "repo")
	override := filepath.Join(repo, "override")
	fs := fileSystem{
		mkdirTemp: func(string, string) (string, error) {
			return "", errors.New("unexpected MkdirTemp")
		},
		mkdirAll: func(path string, _ os.FileMode) error {
			if path != override {
				t.Fatalf("MkdirAll path = %q，期望覆盖根 %q", path, override)
			}
			return nil
		},
	}
	got, err := resolveFrom(repo, "s1", SocketPathLimit, override, fs, quietLogger())
	if err != nil {
		t.Fatalf("resolveFrom override: %v", err)
	}
	if got.Root != override || got.Source != SourceOverride {
		t.Fatalf("decision = %+v，期望只使用覆盖根", got)
	}
}

func TestChooseRejectsSocketPathOverLimitWithMeasuredBytes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), strings.Repeat("r", SocketPathLimit+20))
	wantPath := filepath.Join(root, "s1", "sock")
	_, err := Choose([]Candidate{{Root: root, Source: SourceTmp}}, "s1", SocketPathLimit)
	if err == nil {
		t.Fatal("超长 socket 路径必须返回错误")
	}
	text := err.Error()
	for _, want := range []string{
		strconv.Itoa(len([]byte(wantPath))),
		strconv.Itoa(SocketPathLimit),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("错误 %q 缺少 %q", text, want)
		}
	}
}

func TestResolveBothCandidatesUnavailableReportsProbeLengths(t *testing.T) {
	repo := t.TempDir()
	fs := fileSystem{
		mkdirTemp: func(string, string) (string, error) {
			return "", errors.New("cannot create candidate")
		},
		mkdirAll: func(string, os.FileMode) error {
			return errors.New("repo root is read-only")
		},
	}
	_, err := resolveFrom(repo, "s1", SocketPathLimit, "", fs, quietLogger())
	if err == nil {
		t.Fatal("两处候选都不可用时必须返回 skip 错误")
	}
	text := err.Error()
	for _, want := range []string{
		strconv.Itoa(len([]byte(filepath.Join("/tmp", "s1", "sock")))),
		strconv.Itoa(len([]byte(filepath.Join(repo, ".pty-test-root", "s1", "sock")))),
		strconv.Itoa(SocketPathLimit),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("错误 %q 缺少实测字段 %q", text, want)
		}
	}
}

func TestRepoDotDirectoryIsSkippedByRealGoList(t *testing.T) {
	repo := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(repo, "go.mod"), "module example.com/b202probe\n\ngo 1.26.1\n")
	write(filepath.Join(repo, "visible", "visible.go"), "package visible\n")
	write(filepath.Join(repo, ".pty-test-root", "hidden", "hidden.go"), "package hidden\n")

	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, out)
	}
	if strings.Contains(string(out), ".pty-test-root") {
		t.Fatalf("go list 收进了点号目录: %s", out)
	}
	if !strings.Contains(string(out), "example.com/b202probe/visible") {
		t.Fatalf("go list 未列出普通包: %s", out)
	}
}
