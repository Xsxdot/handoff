package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newBranchReq / existingBranchReq 是两种模式的请求构造捷径，让用例只写变量部分。
func newBranchReq(branch, base string) proto.CreateWorktreeReq {
	return proto.CreateWorktreeReq{Mode: "new_branch", Branch: branch, Base: base}
}

func existingBranchReq(branch string) proto.CreateWorktreeReq {
	return proto.CreateWorktreeReq{Mode: "existing_branch", Branch: branch}
}

// TestCreateManualWorktreeNewBranch 验证新建分支模式：落点在 manual 子目录下、
// 分支真的被建出来、返回的 Workspace 与项目树同口径（Managed=true、Branch 对得上）。
func TestCreateManualWorktreeNewBranch(t *testing.T) {
	repo := initGitRepo(t)
	dataDir := t.TempDir()
	worktreesDir := filepath.Join(dataDir, "worktrees")

	ws, err := CreateManualWorktree(context.Background(), repo, worktreesDir, newBranchReq("feat/x", "main"))
	if err != nil {
		t.Fatalf("CreateManualWorktree: %v", err)
	}
	wantDir := filepath.Join(worktreesDir, "manual", "feat-x")
	if canonPath(ws.Path) != canonPath(wantDir) {
		t.Fatalf("落点 = %q, want %q", ws.Path, wantDir)
	}
	if ws.Branch != "feat/x" {
		t.Fatalf("分支 = %q, want feat/x", ws.Branch)
	}
	if !ws.Managed {
		t.Fatalf("落在数据区的树 Managed 应为 true")
	}
	if ws.IsMain {
		t.Fatalf("新建树不可能是主工作树")
	}
	if _, statErr := os.Stat(filepath.Join(wantDir, "README.md")); statErr != nil {
		t.Fatalf("新树里应有仓库内容: %v", statErr)
	}
	if out := gitAt(t, repo, "branch", "--list", "feat/x"); !strings.Contains(out, "feat/x") {
		t.Fatalf("分支未建出来: %q", out)
	}
}

// TestCreateManualWorktreeExistingBranch 验证检出已有分支模式。
func TestCreateManualWorktreeExistingBranch(t *testing.T) {
	repo := initGitRepo(t)
	gitAt(t, repo, "branch", "feat/done")
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")

	ws, err := CreateManualWorktree(context.Background(), repo, worktreesDir, existingBranchReq("feat/done"))
	if err != nil {
		t.Fatalf("CreateManualWorktree: %v", err)
	}
	if ws.Branch != "feat/done" {
		t.Fatalf("分支 = %q, want feat/done", ws.Branch)
	}
}

// TestCreateManualWorktreeBaseInferred 验证 base 为空时走 resolveBaseBranch 推导，
// 不是直接拒绝——弹层允许不选基线。
func TestCreateManualWorktreeBaseInferred(t *testing.T) {
	repo := initGitRepo(t)
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")

	if _, err := CreateManualWorktree(context.Background(), repo, worktreesDir, newBranchReq("feat/inferred", "")); err != nil {
		t.Fatalf("base 为空应能推导: %v", err)
	}
}

// TestCreateManualWorktreeRejects 把全部拒绝钉在一处：每条都必须是
// ErrBadWorktreeReq（HTTP 层据此回 400），且报文含出问题的那个值。
func TestCreateManualWorktreeRejects(t *testing.T) {
	cases := []struct {
		name    string
		prep    func(t *testing.T, repo, worktreesDir string)
		req     proto.CreateWorktreeReq
		wantSub string
	}{
		{
			name:    "模式非法",
			prep:    func(*testing.T, string, string) {},
			req:     proto.CreateWorktreeReq{Mode: "whatever", Branch: "feat/a"},
			wantSub: "whatever",
		},
		{
			name:    "分支名空",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("", "main"),
			wantSub: "branch",
		},
		{
			name:    "分支名以横杠开头",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("-rf", "main"),
			wantSub: "-rf",
		},
		{
			name:    "分支名不是合法 ref",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("feat/..x", "main"),
			wantSub: "feat/..x",
		},
		{
			name: "新建模式下分支已存在",
			prep: func(t *testing.T, repo, _ string) {
				gitAt(t, repo, "branch", "feat/dup")
			},
			req:     newBranchReq("feat/dup", "main"),
			wantSub: "feat/dup",
		},
		{
			name:    "检出模式下分支不存在",
			prep:    func(*testing.T, string, string) {},
			req:     existingBranchReq("feat/ghost"),
			wantSub: "feat/ghost",
		},
		{
			name: "分支已被别的工作树占用",
			prep: func(t *testing.T, repo, worktreesDir string) {
				gitAt(t, repo, "branch", "feat/taken")
				if _, err := CreateManualWorktree(context.Background(), repo, worktreesDir, existingBranchReq("feat/taken")); err != nil {
					t.Fatalf("预置第一棵树: %v", err)
				}
			},
			req:     existingBranchReq("feat/taken"),
			wantSub: "feat/taken",
		},
		{
			name: "落点已存在",
			prep: func(t *testing.T, _, worktreesDir string) {
				if err := os.MkdirAll(filepath.Join(worktreesDir, "manual", "feat-occupied"), 0o700); err != nil {
					t.Fatalf("预置落点: %v", err)
				}
			},
			req:     newBranchReq("feat/occupied", "main"),
			wantSub: "feat-occupied",
		},
		{
			name:    "基线不存在",
			prep:    func(*testing.T, string, string) {},
			req:     newBranchReq("feat/badbase", "nonexistent-base"),
			wantSub: "nonexistent-base",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := initGitRepo(t)
			worktreesDir := filepath.Join(t.TempDir(), "worktrees")
			c.prep(t, repo, worktreesDir)

			_, err := CreateManualWorktree(context.Background(), repo, worktreesDir, c.req)
			if err == nil {
				t.Fatalf("应当被拒")
			}
			if !errors.Is(err, ErrBadWorktreeReq) {
				t.Fatalf("错误应可判别为 ErrBadWorktreeReq, got %v", err)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报文应含 %q, got %q", c.wantSub, err.Error())
			}
		})
	}
}

// TestManualWorktreeRoot 钉住落点根：界面回显的就是它，改了等于改契约。
func TestManualWorktreeRoot(t *testing.T) {
	if got := ManualWorktreeRoot("/data/worktrees"); got != filepath.Join("/data/worktrees", "manual") {
		t.Fatalf("ManualWorktreeRoot = %q", got)
	}
}
