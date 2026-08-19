package shell_test

import (
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// TestPlanSyncExhaustive 穷举四态。
//
// 表里每一行都对应一种真实处境，不是为了凑覆盖率——尤其
// DecisionInstall + 已配置这一格：它意味着「已配置过、但二进制不见了」，
// 此时不该走同步（同步是换版），该让既有的释出路径去处理。
func TestPlanSyncExhaustive(t *testing.T) {
	cases := []struct {
		name  string
		d     shell.ReleaseDecision
		busy  int
		avail bool
		want  shell.SyncPlan
	}{
		{"内嵌更新且空闲 → 换", shell.DecisionEmbeddedNewer, 0, true, shell.SyncDo},
		{"内嵌更新但有任务 → 拦", shell.DecisionEmbeddedNewer, 1, true, shell.SyncBlocked},
		{"内嵌更新、多个任务 → 拦", shell.DecisionEmbeddedNewer, 7, true, shell.SyncBlocked},
		{"内嵌更新但没内嵌 → 开发构建", shell.DecisionEmbeddedNewer, 0, false, shell.SyncNoEmbed},
		{"已有的不旧 → 不动", shell.DecisionUseExisting, 0, true, shell.SyncSkip},
		{"已有的不旧、有任务 → 不动", shell.DecisionUseExisting, 3, true, shell.SyncSkip},
		{"没有既有安装 → 不归同步管", shell.DecisionInstall, 0, true, shell.SyncSkip},
	}
	for _, c := range cases {
		if got := shell.PlanSync(c.d, c.busy, c.avail); got != c.want {
			t.Errorf("%s: PlanSync(%v,%d,%v) = %v，想要 %v", c.name, c.d, c.busy, c.avail, got, c.want)
		}
	}
}

// TestPlanSyncNegativeBusyIsTreatedAsBlocked 钉住「探不出活跃任务数」的保守方向。
//
// busy 为负表示调用方探测失败（见 Task 8 的约定）。此时必须按「有任务」处置：
// 猜错的代价不对称——误判空闲会在用户有活跃任务时重启 agentd，误判繁忙只是
// 这次不升级。
func TestPlanSyncNegativeBusyIsTreatedAsBlocked(t *testing.T) {
	if got := shell.PlanSync(shell.DecisionEmbeddedNewer, -1, true); got != shell.SyncBlocked {
		t.Errorf("busy=-1 时 PlanSync = %v，想要 SyncBlocked", got)
	}
}
