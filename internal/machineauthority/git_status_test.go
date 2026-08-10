// machineauthority Git 基础状态测试。
//
// 职责：
//   - 锁定 porcelain v2 -z branch/ordinary/rename/unmerged/untracked 解析
//   - 验证真实仓库 modified/added/deleted/renamed/untracked 与非 Git 空能力
//   - 锁定 Git 2.25 参数数组、只读行为和有界执行入口
//
// 边界：
//   - 不测试 staging/commit/PR；不使用 shell 字符串拼接
package machineauthority

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestGitStatusParsesPorcelainV2Records(t *testing.T) {
	raw := strings.Join([]string{
		"# branch.oid abcdef0123456789\n# branch.head feat/test\n# branch.upstream origin/main\n# branch.ab +3 -2\n",
		"1 .M N... 100644 100644 100644 abc abc tracked.txt", "\x00",
		"1 A. N... 000000 100644 100644 000 def added.txt", "\x00",
		"1 .D N... 100644 100644 000000 abc 000 deleted.txt", "\x00",
		"2 R. N... 100644 100644 100644 abc def R100 renamed.txt", "\x00", "old name.txt", "\x00",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc conflict.txt", "\x00",
		"? untracked.txt", "\x00",
	}, "")
	snapshot, err := parseGitStatusV2("ws", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IsRepository || snapshot.Branch != "feat/test" || snapshot.HeadOID != "abcdef0123456789" || snapshot.Upstream != "origin/main" || snapshot.Ahead != 3 || snapshot.Behind != 2 {
		t.Fatalf("branch snapshot = %+v", snapshot)
	}
	if len(snapshot.Entries) != 6 {
		t.Fatalf("entries = %+v", snapshot.Entries)
	}
	want := []workspaceapi.GitStatusEntry{
		{Path: "tracked.txt", IndexStatus: ".", WorktreeStatus: "M"},
		{Path: "added.txt", IndexStatus: "A", WorktreeStatus: "."},
		{Path: "deleted.txt", IndexStatus: ".", WorktreeStatus: "D"},
		{Path: "renamed.txt", OriginalPath: "old name.txt", IndexStatus: "R", WorktreeStatus: "."},
		{Path: "conflict.txt", IndexStatus: "U", WorktreeStatus: "U"},
		{Path: "untracked.txt", IndexStatus: "?", WorktreeStatus: "?"},
	}
	if !reflect.DeepEqual(snapshot.Entries, want) {
		t.Fatalf("entries = %+v, want %+v", snapshot.Entries, want)
	}
}

func TestGitStatusReadsRealRepositoryWithoutMutation(t *testing.T) {
	dir := gitInit(t)
	writeSearchFile(t, dir, "added.txt", "added\n")
	writeSearchFile(t, dir, "delete.txt", "delete\n")
	writeSearchFile(t, dir, "rename.txt", "rename\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "fixtures")
	writeSearchFile(t, dir, "f.txt", "modified\n")
	writeSearchFile(t, dir, "new.txt", "new\n")
	writeSearchFile(t, dir, "staged.txt", "staged\n")
	runGit(t, dir, "add", "staged.txt")
	if err := os.Remove(filepath.Join(dir, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "mv", "rename.txt", "renamed.txt")
	indexBefore, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir}
	status, err := authority.GitStatus(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if !status.IsRepository || status.Branch != "main" || status.HeadOID == "" || len(status.Entries) < 4 {
		t.Fatalf("status = %+v", status)
	}
	indexAfter, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(indexBefore, indexAfter) {
		t.Fatal("GitStatus 修改了 index")
	}
}

func TestGitStatusNonRepositoryAndGit25Arguments(t *testing.T) {
	authority := NewResourceAuthority(slog.New(slog.NewTextHandler(io.Discard, nil)))
	nonRepo := t.TempDir()
	status, err := authority.GitStatus(context.Background(), workspaceapi.WorkspaceRef{WorkspaceID: "plain", MachineID: "m", RootPath: nonRepo})
	if err != nil || status.IsRepository || status.WorkspaceID != "plain" || status.Entries == nil {
		t.Fatalf("non repo status = %+v, %v", status, err)
	}

	dir := gitInit(t)
	var gotArgs []string
	authority.gitStatusRunner = func(_ context.Context, _ string, args []string) ([]byte, error) {
		gotArgs = append([]string(nil), args...)
		return []byte("# branch.oid abc\n# branch.head main\n"), nil
	}
	_, err = authority.GitStatus(context.Background(), workspaceapi.WorkspaceRef{WorkspaceID: "ws", MachineID: "m", RootPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("git args = %v, want %v", gotArgs, want)
	}
	joined := strings.Join(gotArgs, " ")
	for _, forbidden := range []string{"worktree list -z", "for-each-ref --exclude", "--show-stash"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Git 2.25 不兼容参数: %s", joined)
		}
	}
}

func TestGitStatusRunnerBoundsOutputAndReportsExit(t *testing.T) {
	buffer := &boundedGitBuffer{limit: 4}
	n, err := buffer.Write([]byte("123456"))
	if n != 6 || !errors.Is(err, errGitOutputLimit) || !buffer.exceeded || string(buffer.Bytes()) != "1234" {
		t.Fatalf("bounded buffer = n:%d err:%v exceeded:%t bytes:%q", n, err, buffer.exceeded, buffer.Bytes())
	}

	dir := gitInit(t)
	_, err = runGitStatusCommand(context.Background(), dir, []string{"not-a-command"})
	if err == nil || !strings.Contains(err.Error(), "exit=") || !strings.Contains(err.Error(), "stderr_tail=") {
		t.Fatalf("git failure 未保留有界退出上下文: %v", err)
	}
	if len(err.Error()) > gitStderrTailLimit+512 {
		t.Fatalf("git failure 超出 stderr tail 边界: %d", len(err.Error()))
	}
}
