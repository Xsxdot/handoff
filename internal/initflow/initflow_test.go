// initflow 的问答编排与纯函数测试：AskAll 取消语义、选项集合、监听预选、
// 角色默认值。
//
// 角色默认值那两条行为的判据是 GOOS，而 CI 跑在 linux 上——只有把判据参数化
// 才能测到 Windows 分支，测试本身就是这个设计的理由。
package initflow

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// cancelPrompter 每个问题都立刻取消。用来钉死「取消不写盘」：
// 半截答案绝不能 Save，否则会留下一份只配了一半的配置。
type cancelPrompter struct{}

func (cancelPrompter) Select(string, []Option, string) (string, error) {
	return "", ErrCanceled
}
func (cancelPrompter) Input(string, string) (string, error) {
	return "", ErrCanceled
}
func (cancelPrompter) Confirm(string, bool) (bool, error) {
	return false, ErrCanceled
}

// 任一问返回取消就立刻停，且不得写盘——半截答案绝不能 Save。
func TestAskAllCanceled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("listen: 127.0.0.1:7777\ntoken: keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("预载配置: %v", err)
	}
	if _, _, err := AskAll(io.Discard, cancelPrompter{}, cfg, nil, true); !errors.Is(err, ErrCanceled) {
		t.Fatalf("AskAll 取消应得 ErrCanceled，得到 %v", err)
	}
}

// 没装的执行者也必须出现在选项里——探测不阻断选择。
func TestExecutorOptionsIncludeMissing(t *testing.T) {
	rs := []toolchain.Result{
		{Name: "opencode", State: toolchain.StateReady},
		{Name: "claude", State: toolchain.StateAuthUnknown},
		{Name: "grok", State: toolchain.StateMissing},
		{Name: "codex", State: toolchain.StateNoCreds},
	}
	opts := ExecutorOptions(rs)
	if len(opts) != 4 {
		t.Fatalf("应列出四家，得到 %d", len(opts))
	}
	byName := map[string]string{}
	for _, o := range opts {
		byName[o.Value] = o.Label
	}
	if !strings.Contains(byName["grok"], "没装") {
		t.Errorf("没装的 grok 应留在列表并旁注状态，得到 %q", byName["grok"])
	}
	if !strings.Contains(byName["opencode"], "就绪") {
		t.Errorf("就绪项 Label 应含状态，得到 %q", byName["opencode"])
	}
}

func TestListenPreset(t *testing.T) {
	cases := []struct {
		listen  string
		existed bool
		isExec  bool
		want    string
	}{
		{"127.0.0.1:7777", false, true, listenAll},
		{"127.0.0.1:7777", true, true, listenLoopback},
		{"0.0.0.0:7777", true, true, listenAll},
		{"[::]:7777", true, true, listenAll},
		{"0.0.0:7777", true, true, listenCustom},
		{"0.0.0.0:7788", true, true, listenCustom},
		{"192.168.1.9:7788", true, true, listenCustom},
	}
	for _, tc := range cases {
		if got := ListenPreset(tc.listen, tc.existed, tc.isExec); got != tc.want {
			t.Errorf("ListenPreset(%q, existed=%v, exec=%v)=%q, want %q",
				tc.listen, tc.existed, tc.isExec, got, tc.want)
		}
	}
}

// Windows 上选执行机会一路走到 service install 才撞墙（agentd 的进程承载层
// 在非 unix 平台返回 not implemented，B37）。不如在这里就不给这个选项。
func TestRoleOptionsWindowsOnlyCoordinator(t *testing.T) {
	got := RoleOptions("windows")
	if len(got) != 1 {
		t.Fatalf("Windows 上应只有一个角色选项，实得 %d 个: %+v", len(got), got)
	}
	if got[0].Value != RoleCoordinator {
		t.Fatalf("Windows 上唯一的角色应是协调者，实得 %q", got[0].Value)
	}
}

func TestRoleOptionsUnixHasAllThree(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		got := RoleOptions(goos)
		if len(got) != 3 {
			t.Fatalf("%s 上应有三个角色选项，实得 %d 个: %+v", goos, len(got), got)
		}
	}
}

// 预选项必须落在 RoleOptions 给出的列表里，否则 huh 拿一个不在列表里的
// 默认值去匹配，选中项会落空——B83 刚踩过一次同类问题。
func TestDefaultRoleOnWindowsIgnoresProbe(t *testing.T) {
	rs := []toolchain.Result{{Name: "opencode", State: toolchain.StateReady}}
	if got := DefaultRole(&config.Config{}, false, rs, "windows"); got != RoleCoordinator {
		t.Fatalf("Windows 预选角色应为协调者，实得 %q", got)
	}
	// 同样的输入在 darwin 上仍应预选执行机——证明上一条不是因为探测结果为空
	if got := DefaultRole(&config.Config{}, false, rs, "darwin"); got != RoleExecutor {
		t.Fatalf("darwin 预选角色应为执行机，实得 %q", got)
	}
}
