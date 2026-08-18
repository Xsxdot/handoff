// 工作区探测测试：porcelain 解析的四种形态 + 目录失效的降级。
package agentd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseWorktreePorcelainShapes 覆盖主工作树/linked/detached/managed 四种形态。
func TestParseWorktreePorcelainShapes(t *testing.T) {
	out := strings.Join([]string{
		"worktree /home/dev/handoff",
		"HEAD 482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6",
		"branch refs/heads/main",
		"",
		"worktree /home/dev/.handoff/worktrees/w1",
		"HEAD 9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6482aab1",
		"branch refs/heads/handoff/w1",
		"",
		"worktree /home/dev/scratch",
		"HEAD aa11bb22cc33dd44ee55ff66aa77bb88cc99dd00",
		"detached",
		"",
	}, "\n")
	ws := parseWorktreePorcelain(out, "/home/dev/.handoff/worktrees")
	if len(ws) != 3 {
		t.Fatalf("工作树数 = %d，期望 3：%+v", len(ws), ws)
	}
	if !ws[0].IsMain || ws[0].Branch != "main" || ws[0].Head != "482aab1" || ws[0].Managed {
		t.Errorf("主工作树解析错：%+v", ws[0])
	}
	if ws[1].IsMain || !ws[1].Managed || ws[1].Branch != "handoff/w1" {
		t.Errorf("managed 工作树解析错：%+v", ws[1])
	}
	// detached 时 branch 为空串，head 仍在——UI 靠 head 显示，不能两个都空
	if ws[2].Branch != "" || ws[2].Head != "aa11bb2" || ws[2].Managed {
		t.Errorf("detached 工作树解析错：%+v", ws[2])
	}
}

// TestProbeWorkspacesRealRepo 对真实仓库探测：主工作树 + 一个 linked worktree。
func TestProbeWorkspacesRealRepo(t *testing.T) {
	repo := initGitRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitAt(t, repo, "worktree", "add", "-b", "side", linked)

	ws, probeErr := probeWorkspaces(context.Background(), repo, filepath.Dir(linked))
	if probeErr != "" {
		t.Fatalf("探测不该失败：%s", probeErr)
	}
	if len(ws) != 2 {
		t.Fatalf("工作树数 = %d，期望 2：%+v", len(ws), ws)
	}
	if !ws[0].IsMain {
		t.Errorf("第一条应是主工作树：%+v", ws[0])
	}
	if !ws[1].Managed {
		t.Errorf("linked 落在 managedRoot 下，应判定为 managed：%+v", ws[1])
	}
}

// TestProbeWorkspacesBadDirDegrades 是本任务最重要的一条：目录失效不炸树。
//
// 项目树必须能展示「登记还在、目录已失效」，整棵树 500 会让用户连哪个项目
// 坏了都看不见。
func TestProbeWorkspacesBadDirDegrades(t *testing.T) {
	ws, probeErr := probeWorkspaces(context.Background(), filepath.Join(t.TempDir(), "gone"), "")
	if probeErr == "" {
		t.Fatal("目录不存在时必须给出 probe_error")
	}
	if ws == nil {
		t.Fatal("失败时也必须返回空切片而非 nil：JSON 要序列化成 [] 不是 null")
	}
	if len(ws) != 0 {
		t.Fatalf("失败时不该有工作树：%+v", ws)
	}
}

// TestWorkspaceCreatedAt 验证链接工作树取得到创建时间、主工作树如实留零值。
//
// 为什么要建真仓库：本函数读的是 git worktree add 写下的
// .git/worktrees/<名>/gitdir，用手工造的目录结构测等于在测自己写的假数据。
//
// 为什么主工作树期望零值而不是某个时间：`.git` 目录的 mtime 是「最后一次在
// 里面增删条目」而不是创建时间（实测一个 08-07 建的仓库报出 08-18）。排序把
// 主工作树钉在第一位、不参与比较，这个值没有消费者——如实说不知道，好过报
// 一个自信的错值。详见 workspaceCreatedAt 的注释。
func TestWorkspaceCreatedAt(t *testing.T) {
	main := initGitRepo(t)
	linked := filepath.Join(t.TempDir(), "wt")
	gitAt(t, main, "worktree", "add", "-b", "feat", linked)

	ws, probeErr := probeWorkspaces(context.Background(), main, "")
	if probeErr != "" {
		t.Fatalf("探测失败: %s", probeErr)
	}
	if len(ws) != 2 {
		t.Fatalf("期望 2 个工作树，实得 %d", len(ws))
	}
	for _, w := range ws {
		if w.IsMain {
			if !w.CreatedAt.IsZero() {
				t.Errorf("主工作树 %s 的 CreatedAt 应为零值（.git 目录 mtime 不是创建时间），实得 %v", w.Path, w.CreatedAt)
			}
			continue
		}
		if w.CreatedAt.IsZero() {
			t.Errorf("链接工作树 %s 的 CreatedAt 是零值，期望从 gitdir 取到真实时间", w.Path)
		}
	}
}

// TestWorkspaceCreatedAtMainIsZero 单独钉住「主工作树恒零值」这条。
//
// 与上面那个用例分开写，是因为这条是**刻意的取舍**而不是实现细节：把它
// 混在「探测出两个工作树」的断言里，将来有人改实现时会以为只是顺带的。
func TestWorkspaceCreatedAtMainIsZero(t *testing.T) {
	main := initGitRepo(t)
	if got := workspaceCreatedAt(main, true); !got.IsZero() {
		t.Errorf("主工作树应恒返回零值，实得 %v", got)
	}
}

// TestWorkspaceCreatedAtMissingIsZero 验证取不到时留零值而不是报错。
//
// 这是 spec §1.3 的诚实降级：整棵项目树不该因为一个 stat 失败就 500。
//
// isMain 必须传 false：主工作树那一支不经 stat 就直接返回零值，用它来测
// 「stat 失败」等于什么都没测——用例会因为走错分支而永远绿。
func TestWorkspaceCreatedAtMissingIsZero(t *testing.T) {
	got := workspaceCreatedAt(filepath.Join(t.TempDir(), "不存在"), false)
	if !got.IsZero() {
		t.Errorf("不存在的路径应得零值，实得 %v", got)
	}
}
