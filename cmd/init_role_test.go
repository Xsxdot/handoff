// 角色选项与预选项的平台行为测试。
//
// 为什么单独一个文件：这两条行为的判据是 GOOS，而 CI 跑在 linux 上——
// 只有把判据参数化才能测到 Windows 分支，测试本身就是这个设计的理由。
package cmd

import (
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/toolchain"
)

// Windows 上选执行机会一路走到 service install 才撞墙（agentd 的进程承载层
// 在非 unix 平台返回 not implemented，B37）。不如在这里就不给这个选项。
func TestRoleOptionsWindowsOnlyCoordinator(t *testing.T) {
	got := roleOptions("windows")
	if len(got) != 1 {
		t.Fatalf("Windows 上应只有一个角色选项，实得 %d 个: %+v", len(got), got)
	}
	if got[0].Value != roleCoordinator {
		t.Fatalf("Windows 上唯一的角色应是协调者，实得 %q", got[0].Value)
	}
}

func TestRoleOptionsUnixHasAllThree(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		got := roleOptions(goos)
		if len(got) != 3 {
			t.Fatalf("%s 上应有三个角色选项，实得 %d 个: %+v", goos, len(got), got)
		}
	}
}

// 预选项必须落在 roleOptions 给出的列表里，否则 huh 拿一个不在列表里的
// 默认值去匹配，选中项会落空——B83 刚踩过一次同类问题。
func TestDefaultRoleOnWindowsIgnoresProbe(t *testing.T) {
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateReady}}
	if got := defaultRole(&config.Config{}, false, rs, "windows"); got != roleCoordinator {
		t.Fatalf("Windows 预选角色应为协调者，实得 %q", got)
	}
	// 同样的输入在 darwin 上仍应预选执行机——证明上一条不是因为探测结果为空
	if got := defaultRole(&config.Config{}, false, rs, "darwin"); got != roleExecutor {
		t.Fatalf("darwin 预选角色应为执行机，实得 %q", got)
	}
}
