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

// 处置建议必须是一条**能直接粘贴执行**的命令。
//
// 改前它打的是 short8(taskID)，而 handoff 的 <task> 是精确匹配、不认短 id，
// 照着敲必然 404「记录不存在」——真机实测过（B145）。错误提示给出一条注定
// 失败的命令，比不给建议更伤：它把人引向一次必然白费的重试。
func TestRenderDirtyRejectionListsFilesAndForceHint(t *testing.T) {
	const full = "9413e365-3cc3-429f-90db-252890a973f2"
	var buf bytes.Buffer
	renderDirtyRejection(&buf, full, "/w/9413e365", []proto.DirtyFile{
		{Status: " M", Path: "internal/prochost/fence.go"},
		{Status: "??", Path: "scratch/probe.log"},
	})
	out := buf.String()
	for _, want := range []string{"internal/prochost/fence.go", "scratch/probe.log",
		"共 2 项", "handoff reclaim " + full + " --force"} {
		if !strings.Contains(out, want) {
			t.Fatalf("拒绝输出应含 %q，实得：\n%s", want, out)
		}
	}
	// 反面断言：不能只出现被截短的形态。少了这条，把 taskID 换回 short8 也照样过
	if strings.Contains(out, "handoff reclaim 9413e365 --force") {
		t.Fatalf("处置建议不得使用短 id（精确匹配，照敲必 404），实得：\n%s", out)
	}
}

// 远程任务的处置建议必须带上 --target：任务只存在于那台机器的 agentd 上，
// 漏掉它的命令会打到本机，同样以 404 收场——那就又是一条照着敲必然失败的建议。
func TestRenderDirtyRejectionCarriesTarget(t *testing.T) {
	const full = "9413e365-3cc3-429f-90db-252890a973f2"
	old := targetName
	targetName = "win-b37"
	t.Cleanup(func() { targetName = old })

	var buf bytes.Buffer
	renderDirtyRejection(&buf, full, "/w/9413e365", []proto.DirtyFile{{Status: "??", Path: "a.md"}})
	want := "handoff reclaim " + full + " --target win-b37 --force"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("处置建议应含 %q，实得：\n%s", want, buf.String())
	}
}

// 本机模式下不得凭空加 --target：那会让建议在本机场景下变成一条错命令。
func TestRenderDirtyRejectionLocalHasNoTarget(t *testing.T) {
	old := targetName
	targetName = ""
	t.Cleanup(func() { targetName = old })

	var buf bytes.Buffer
	renderDirtyRejection(&buf, "9413e365-3cc3-429f-90db-252890a973f2", "/w/x",
		[]proto.DirtyFile{{Status: "??", Path: "a.md"}})
	if strings.Contains(buf.String(), "--target") {
		t.Fatalf("本机模式不应出现 --target，实得：\n%s", buf.String())
	}
}
