package shell_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestDoSyncCallOrderIsLoadBearing 钉住四步的相对顺序。
//
// 顺序是承重的（spec §5）：
//   - Activate 必须在 SkillInstall 之前——skill 要从**新**二进制里装，
//     当前进程内嵌的是旧的（cmd/upgrade.go:591 已记此事）
//   - SkillInstall 必须在 RestartAgentd 之前——重启会让本进程与 agentd
//     的连接断掉，之后再 exec 新二进制装 skill 就成了「重启后才补」，
//     而重启期间协调者拿到的是旧 skill
func TestDoSyncCallOrderIsLoadBearing(t *testing.T) {
	var seq []string
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			seq = append(seq, "open")
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate: func(newPath, target string) (string, error) {
			seq = append(seq, "activate")
			return target + ".prev", nil
		},
		SkillInstall: func(context.Context, string) ([]byte, error) {
			seq = append(seq, "skill")
			return nil, nil
		},
		RestartAgentd: func(context.Context, bool) error {
			seq = append(seq, "restart")
			return nil
		},
	}
	target := filepath.Join(t.TempDir(), "handoff")
	if err := shell.DoSync(context.Background(), target, false, d, func(string) {}); err != nil {
		t.Fatalf("DoSync 返回错误：%v", err)
	}
	want := []string{"open", "activate", "skill", "restart"}
	if !slices.Equal(seq, want) {
		t.Errorf("调用序列 = %v，想要 %v", seq, want)
	}
}

// TestDoSyncSkillInstallFailureIsNotFatal 钉住 skill 同步失败不算同步失败。
//
// 二进制已经换好了——此时报错回去会让调用方以为换版没成功。但也**绝不能
// 静默**：留一份旧 skill 会按已经变了的状态机主动误导协调者（沿用
// cmd/upgrade.go:591 syncSkill 的既有语义）。
func TestDoSyncSkillInstallFailureIsNotFatal(t *testing.T) {
	restarted := false
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate:     func(_, target string) (string, error) { return target + ".prev", nil },
		SkillInstall: func(context.Context, string) ([]byte, error) { return []byte("boom"), errors.New("装不上") },
		RestartAgentd: func(context.Context, bool) error {
			restarted = true
			return nil
		},
	}
	target := filepath.Join(t.TempDir(), "handoff")
	if err := shell.DoSync(context.Background(), target, false, d, func(string) {}); err != nil {
		t.Fatalf("skill 装不上不该让 DoSync 失败，却返回：%v", err)
	}
	if !restarted {
		t.Error("skill 装不上时跳过了重启——二进制已经换了，不重启等于换了个寂寞")
	}
}

// TestDoSyncStopsAtActivateFailure 钉住换版失败时不往下走。
//
// Activate 失败意味着磁盘上还是旧二进制。此时若继续 SkillInstall 会把**新**
// skill 装到**旧**二进制的落点上，造出一个版本不匹配的组合；继续 RestartAgentd
// 更是白重启一次而调用方以为升级成功了。
func TestDoSyncStopsAtActivateFailure(t *testing.T) {
	var seq []string
	d := shell.SyncDeps{
		OpenEmbedded: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("BINARY")), nil
		},
		Activate:      func(string, string) (string, error) { return "", errors.New("权限不足") },
		SkillInstall:  func(context.Context, string) ([]byte, error) { seq = append(seq, "skill"); return nil, nil },
		RestartAgentd: func(context.Context, bool) error { seq = append(seq, "restart"); return nil },
	}
	target := filepath.Join(t.TempDir(), "handoff")
	err := shell.DoSync(context.Background(), target, false, d, func(string) {})
	if err == nil {
		t.Fatal("Activate 失败时 DoSync 必须返回错误")
	}
	if len(seq) != 0 {
		t.Errorf("Activate 失败后仍继续执行了 %v", seq)
	}
}

// TestDoSyncLeavesNoTempFileOnFailure 钉住失败时不留半截文件。
//
// 半截的临时文件若以 target 那个名字出现，launchd/schtasks 会把它当可执行
// 拉起来——症状是「装好了但 agentd 起不来」，而根因在一次失败的升级里。
func TestDoSyncLeavesNoTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "handoff")
	d := shell.SyncDeps{
		OpenEmbedded:  func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("BINARY")), nil },
		Activate:      func(string, string) (string, error) { return "", errors.New("权限不足") },
		SkillInstall:  func(context.Context, string) ([]byte, error) { return nil, nil },
		RestartAgentd: func(context.Context, bool) error { return nil },
	}
	_ = shell.DoSync(context.Background(), target, false, d, func(string) {})
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("失败后残留了文件 %s", e.Name())
	}
}
