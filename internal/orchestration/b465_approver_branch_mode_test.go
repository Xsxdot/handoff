package orchestration

// b465_approver_branch_mode_test.go —— B465：上下文段的「任务形态」行只解释本任务实际形态。
// 分支任务（含 Mode 为空的旧任务）的提示词不得出现只读解释——flash 审批模型曾把它读成
// 「本任务是只读」而把工作树内的改码、提交一律升级；readonly 任务的只读解释与政策段不变。
import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB465BranchContextHasNoReadonlyExplanation(t *testing.T) {
	for _, mode := range []string{proto.TaskModeBranch, ""} {
		tc := ApproverTaskContext{Mode: mode, Workdir: "/wt/x", TaskTmpDir: "/data/tmp/t1"}
		got := approverPrompt(b460Summary, b460Perm, "N0NCE", tc)
		for _, bad := range []string{"只读调查", "readonly", "只读任务的裁决政策"} {
			if strings.Contains(got, bad) {
				t.Fatalf("形态 %q 的提示词不得出现 %q：\n%s", mode, bad, got)
			}
		}
		// 「提交到任务分支」会被读成推送远端：括注须写明本地 commit、并把 push 排除在外
		if !strings.Contains(got, "- 任务形态：branch（分支任务：可在任务工作树内改代码、git commit 到本地任务分支；push 等改变远端仓库的操作不在此列）") {
			t.Fatalf("形态 %q 的提示词缺分支形态说明：\n%s", mode, got)
		}
	}
}

func TestB465ReadonlyContextKeepsReadonlyExplanation(t *testing.T) {
	tc := ApproverTaskContext{Mode: proto.TaskModeReadonly, Workdir: "/wt/x"}
	got := approverPrompt(b460Summary, b460Perm, "N0NCE", tc)
	for _, s := range []string{"- 任务形态：readonly（readonly = 只读调查：不改仓库、不提交，结论写成报告交回）", "只读任务的裁决政策："} {
		if !strings.Contains(got, s) {
			t.Fatalf("只读提示词缺 %q：\n%s", s, got)
		}
	}
	if strings.Contains(got, "分支任务") {
		t.Fatalf("只读提示词不得出现分支形态说明：\n%s", got)
	}
}
