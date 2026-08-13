// reclaim_test.go —— handoff reclaim 的渲染与退出语义测试。
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestRenderReclaimListShowsAllStates(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimList(&buf, &proto.ReclaimListResp{
		Scanned: 40,
		Rows: []proto.ReclaimRow{
			{TaskID: "2c58bbb7-0000-0000-0000-000000000000", Name: "b73 围栏 r2",
				State: "failed", WorkDir: "/w/2c58bbb7",
				Worktree: proto.WorktreeDirty, DirtyCount: 4},
			{TaskID: "ef012345-0000-0000-0000-000000000000", Name: "b69 足迹",
				State: "failed", WorkDir: "/w/ef012345",
				Worktree: proto.WorktreePrunable, Note: "gitdir file points to non-existent location"},
			{TaskID: "9a8b7c6d-0000-0000-0000-000000000000", Name: "b52 子会话",
				State: "failed", WorkDir: "/w/9a8b7c6d",
				Worktree: proto.WorktreeUnknown, Note: "仓库不可达"},
		},
	})
	out := buf.String()
	for _, want := range []string{"共体检 40", "2c58bbb7", "脏", "4", "元数据残留", "判不出"} {
		if !strings.Contains(out, want) {
			t.Fatalf("列表输出应含 %q，实得：\n%s", want, out)
		}
	}
}

func TestRenderReclaimListEmptyIsOneLine(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimList(&buf, &proto.ReclaimListResp{Scanned: 40})
	out := buf.String()
	if !strings.Contains(out, "无") || !strings.Contains(out, "40") {
		t.Fatalf("无残留应一行收口并报体检数，实得：%s", out)
	}
}

func TestRenderReclaimResultKeepsBranchNotice(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimResult(&buf, "2c58bbb7", &proto.ReclaimResp{
		Removed: true, Action: proto.ReclaimRemoved,
		WorkDir: "/w/2c58bbb7", Branch: "feat/b73-proc-fence-r2",
	})
	out := buf.String()
	// 「分支保留」必须每次都说：删了树没删分支是本命令最容易被误解的地方
	if !strings.Contains(out, "分支") || !strings.Contains(out, "feat/b73-proc-fence-r2") {
		t.Fatalf("成功输出必须点名分支被保留，实得：%s", out)
	}
}

func TestRenderReclaimResultAlreadyAbsent(t *testing.T) {
	var buf bytes.Buffer
	renderReclaimResult(&buf, "2c58bbb7", &proto.ReclaimResp{
		Removed: false, Action: proto.ReclaimAlreadyAbsent, WorkDir: "/w/2c58bbb7",
	})
	if !strings.Contains(buf.String(), "无残留") {
		t.Fatalf("幂等成功应明说无残留，实得：%s", buf.String())
	}
}

func TestRenderDirtyRejectionListsFilesAndForceHint(t *testing.T) {
	var buf bytes.Buffer
	renderDirtyRejection(&buf, "2c58bbb7", "/w/2c58bbb7", []proto.DirtyFile{
		{Status: " M", Path: "internal/prochost/fence.go"},
		{Status: "??", Path: "scratch/probe.log"},
	})
	out := buf.String()
	for _, want := range []string{"internal/prochost/fence.go", "scratch/probe.log",
		"共 2 项", "handoff reclaim 2c58bbb7 --force"} {
		if !strings.Contains(out, want) {
			t.Fatalf("拒绝输出应含 %q，实得：\n%s", want, out)
		}
	}
}
