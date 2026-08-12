package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMainWorktreeRootMergesLinkedWorktree 验证四种位置都归并到主仓根：
// 主仓根 / 主仓子目录 / linked worktree 根 / linked worktree 子目录。
//
// 为什么必须覆盖子目录：git rev-parse --git-common-dir 在主仓里返回的是
// **相对**路径（根目录 ".git"，子目录 "../.git"），只测根目录会漏掉相对
// 路径的拼接分支。
func TestMainWorktreeRootMergesLinkedWorktree(t *testing.T) {
	ctx := context.Background()
	main := initGitRepo(t)
	sub := filepath.Join(main, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("建子目录: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat/x", wt)
	wtSub := filepath.Join(wt, "internal")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatalf("建 worktree 子目录: %v", err)
	}

	wantMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", main, err)
	}
	for _, dir := range []string{main, sub, wt, wtSub} {
		got, err := MainWorktreeRoot(ctx, dir)
		if err != nil {
			t.Fatalf("MainWorktreeRoot(%s): %v", dir, err)
		}
		gotReal, err := filepath.EvalSymlinks(got)
		if err != nil {
			t.Fatalf("EvalSymlinks(%s): %v", got, err)
		}
		if gotReal != wantMain {
			t.Errorf("MainWorktreeRoot(%s) = %s, want %s", dir, gotReal, wantMain)
		}
	}
}

// TestMainWorktreeRootRejectsNonRepo 验证非 git 目录被拒，且错误可判别、报文含路径。
func TestMainWorktreeRootRejectsNonRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := MainWorktreeRoot(context.Background(), dir)
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("err = %v, want errors.Is(..., ErrRepoUnusable)", err)
	}
}
