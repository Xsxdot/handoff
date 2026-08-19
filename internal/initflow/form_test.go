// 本文件是字段描述表（form.go）的测试：字段表的键集合与顺序（对外契约）、
// 显隐矩阵（Roles + ShowWhen）、Apply 的校验与写回、监听预设随角色答案翻档。
package initflow

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
)

// 字段表的键集合与顺序是对外契约（桌面前端按 Key 取值），钉死它。
func TestFormKeysAndOrder(t *testing.T) {
	cfg, rs := goldenFixture()
	got := []string{}
	for _, f := range Form(cfg, rs, "darwin", false) {
		got = append(got, f.Key)
	}
	want := []string{
		"role", "executor_default", "executor_model",
		"listen_preset", "listen",
		"repo_root", "approver_executor", "approver_model", "sync_auto",
	}
	if len(got) != len(want) {
		t.Fatalf("字段数不对：期望 %v，实际 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个字段：期望 %s，实际 %s", i, want[i], got[i])
		}
	}
}

// B37 落地后 Windows 角色选项与其它平台一致，且不再带失效的限制文案。
func TestFormWindowsRoleAvailable(t *testing.T) {
	cfg, rs := goldenFixture()
	for _, f := range Form(cfg, rs, "windows", false) {
		if f.Key != "role" {
			continue
		}
		if len(f.Options) != 3 {
			t.Fatalf("Windows 上角色应有三个选项，实际 %+v", f.Options)
		}
		for _, want := range []string{RoleExecutor, RoleCoordinator, RoleBoth} {
			if !optionContains(f.Options, want) {
				t.Fatalf("Windows 上角色选项缺少 %q，实际 %+v", want, f.Options)
			}
		}
		if f.Notice != "" {
			t.Fatalf("Windows 上不应再有失效的限制文案，实际 %q", f.Notice)
		}
		return
	}
	t.Fatal("字段表里没有 role")
}

func TestVisible(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := map[string]Field{}
	for _, f := range Form(cfg, rs, "darwin", false) {
		fields[f.Key] = f
	}
	cases := []struct {
		name    string
		key     string
		answers map[string]string
		want    bool
	}{
		{"协调者看不到执行者选择", "executor_default", map[string]string{"role": RoleCoordinator}, false},
		{"执行机看得到执行者选择", "executor_default", map[string]string{"role": RoleExecutor}, true},
		{"两者都看得到", "executor_default", map[string]string{"role": RoleBoth}, true},
		{"执行机看不到 sync.auto", "sync_auto", map[string]string{"role": RoleExecutor}, false},
		{"协调者看得到 sync.auto", "sync_auto", map[string]string{"role": RoleCoordinator}, true},
		{"未选自定义则不问监听地址", "listen", map[string]string{"role": RoleExecutor, "listen_preset": "loopback"}, false},
		{"选了自定义才问监听地址", "listen", map[string]string{"role": RoleExecutor, "listen_preset": "custom"}, true},
		{"没选审批者就不问审批模型", "approver_model", map[string]string{"role": RoleExecutor, "approver_executor": ""}, false},
		{"选了审批者才问审批模型", "approver_model", map[string]string{"role": RoleExecutor, "approver_executor": "opencode"}, true},
		{"切角色后残留答案不影响判定", "sync_auto", map[string]string{"role": RoleExecutor, "sync_auto": "true"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Visible(fields[c.key], c.answers); got != c.want {
				t.Fatalf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestApply(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	answers := map[string]string{
		"role":              RoleBoth,
		"listen_preset":     "custom",
		"listen":            "0.0.0.0:7777",
		"executor_default":  "opencode",
		"executor_model":    "",
		"repo_root":         "/srv/repos",
		"approver_executor": "opencode",
		"approver_model":    "cheap",
		"sync_auto":         "false",
	}
	if err := Apply(cfg, fields, answers); err != nil {
		t.Fatalf("Apply 不该出错: %v", err)
	}
	if cfg.Listen != "0.0.0.0:7777" || cfg.Executor.Default != "opencode" ||
		cfg.RepoRoot != "/srv/repos" || cfg.Approver.Model != "cheap" || cfg.Sync.Auto {
		t.Fatalf("写回不对: %+v", cfg)
	}
}

func TestApplyRejectsAnswerOutsideOptions(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	err := Apply(cfg, fields, map[string]string{"role": RoleBoth, "executor_default": "不存在的执行者"})
	if err == nil {
		t.Fatal("越界的 Select 答案必须被拒")
	}
}

func TestApplyRejectsBadConfirmValue(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	err := Apply(cfg, fields, map[string]string{"role": RoleCoordinator, "sync_auto": "yes"})
	if err == nil {
		t.Fatal("Confirm 只接受 true/false，\"yes\" 必须被拒——否则会静默写成 false")
	}
}

func TestApplyIgnoresInvisibleAnswers(t *testing.T) {
	cfg, rs := goldenFixture()
	fields := Form(cfg, rs, "darwin", false)
	// 协调者角色下 executor_default 不可见；前端可能残留切角色前填的值
	err := Apply(cfg, fields, map[string]string{
		"role": RoleCoordinator, "executor_default": "不存在的执行者",
	})
	if err != nil {
		t.Fatalf("不可见字段的残留答案应被忽略而不是报错，实际 %v", err)
	}
}

// 默认推协调者（一家执行者都没装）时，用户改选执行机，监听预设必须翻成「所有网卡」。
func TestListenPresetFollowsRoleAnswer(t *testing.T) {
	cfg := &config.Config{Listen: "127.0.0.1:7777", Targets: map[string]config.Target{}}
	fields := Form(cfg, nil, "darwin", false) // rs 为空 → DefaultRole 推协调者
	var lp Field
	for _, f := range fields {
		if f.Key == "listen_preset" {
			lp = f
		}
	}
	if got := DefaultOf(lp, map[string]string{"role": RoleCoordinator}); got != "loopback" {
		t.Fatalf("协调者应预选仅本机，实际 %s", got)
	}
	if got := DefaultOf(lp, map[string]string{"role": RoleExecutor}); got != "all" {
		t.Fatalf("改选执行机后应预选所有网卡（否则协调者连不上），实际 %s", got)
	}
}
