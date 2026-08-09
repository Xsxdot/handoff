package permgate

import (
	"log/slog"
	"testing"
)

// newTestGate 造一个只带内置黑名单的 Gate。
func newTestGate(t *testing.T) *Gate {
	t.Helper()
	g, err := New(nil, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

// TestNarrativeTextNoLongerEscalates 锁死 B23 的验收基线。
//
// 这 9 条是 2026-08-09 用当时的 builtinBlacklist 原文实测出的**全部误命中**
// （spec §1.2），一条都不能少：少一条就是把已证实的误升级悄悄放回去。
func TestNarrativeTextNoLongerEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: git commit -m "fix: 清理逻辑不再误删，去掉 rm -rf 分支"`,
		`Bash: git commit -m "docs: 说明 production 部署流程"`,
		`Bash: go test ./internal/prod/...`,
		`Bash: grep -rn "sudo" internal/`,
		`Bash: cat docs/production-checklist.md`,
		`Bash: npm run build:prod`,
		`Write: /repo/docs/production.md`,
		`Bash: go run ./cmd --note "drop table 是危险操作"`,
		`Bash: echo "见 README：git reset --hard 会丢改动" >> notes.md`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action == Escalate {
			t.Errorf("叙述性文本不应硬升级\n  输入: %s\n  命中规则: %s\n  理由: %s",
				c, v.Rule, v.Reason)
		}
	}
}

// TestRealDangerStillEscalates 反向守卫：修误判不得放松真危险的拦截。
func TestRealDangerStillEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: rm -rf /tmp/x`,
		`Bash: rm --recursive --force /tmp/x`,
		`Bash: sudo systemctl restart nginx`,
		`Bash: git -C /repo push --force origin main`,
		`Bash: git reset --hard HEAD~3`,
		`Bash: psql -c 'drop table users'`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action != Escalate {
			t.Errorf("真危险命令必须硬升级\n  输入: %s\n  实得: %s（%s）", c, v.Action, v.Reason)
		}
	}
}

// TestQuoteBypassStillEscalates 堵引号绕过：剥离后干净但含执行包装器的，
// 不许降级为 Consult（spec §4.1 第二行）。
func TestQuoteBypassStillEscalates(t *testing.T) {
	g := newTestGate(t)
	cases := []string{
		`Bash: sh -c "rm -rf /"`,
		`Bash: bash -c 'sudo rm -rf /var'`,
		`Bash: eval "$DANGER"`,
		`Bash: echo x | xargs rm -rf`,
	}
	for _, c := range cases {
		if v := g.judgeCommand(c); v.Action != Escalate {
			t.Errorf("引号绕过形态必须硬升级\n  输入: %s\n  实得: %s（%s）", c, v.Action, v.Reason)
		}
	}
}

// TestCleanCommandGoesToApprover 未命中黑名单的普通命令走审批者，形状不变。
func TestCleanCommandGoesToApprover(t *testing.T) {
	g := newTestGate(t)
	if v := g.judgeCommand(`Bash: go build ./...`); v.Action != Consult {
		t.Fatalf("干净命令应交审批者，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestStripQuoted 逐条锁死剥离语义。
func TestStripQuoted(t *testing.T) {
	cases := []struct{ in, want string }{
		{`git commit -m "去掉 rm -rf 分支"`, `git commit -m ""`},
		{`echo 'sudo x' > a`, `echo '' > a`},
		{`rm -rf /tmp/x`, `rm -rf /tmp/x`},
		{`echo "a" b 'c'`, `echo "" b ''`},
	}
	for _, c := range cases {
		if got := StripQuoted(c.in); got != c.want {
			t.Errorf("StripQuoted(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestHasExecWrapper 确认包装器识别不会被 push/ssh 这类含 sh 的词误伤。
func TestHasExecWrapper(t *testing.T) {
	yes := []string{`sh -c "x"`, `bash -c 'x'`, `zsh -c x`, `eval x`, `xargs rm`}
	no := []string{`git push origin main`, `ssh host ls`, `echo shell`}
	for _, s := range yes {
		if !HasExecWrapper(s) {
			t.Errorf("应识别为执行包装器: %q", s)
		}
	}
	for _, s := range no {
		if HasExecWrapper(s) {
			t.Errorf("不应识别为执行包装器: %q", s)
		}
	}
}
