package permgate

// b460_cdchain_test.go —— B460 修复：依赖 cd 基准的放行出口（heredoc-in-scope、
// rm-in-scope、范围内写入）只在 cd 链可信（首个 cd 之后全 `&&`、cd 前不接 `||`）时放行。
// 修前 `cd $TMPDIR/missing; cat > .bashrc <<'EOF'…` 判 AutoAllow：cd 失败时 cat 在
// 执行器的未知 cwd（claude 跨调用保留）里写。
import "testing"

func TestB460CDChainTrusted(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	const body = " <<'EOF'\nx\nEOF"
	cases := []struct {
		name  string
		cmd   string
		allow bool
		rule  string
	}{
		{"heredoc cd 后分号", "cd $TMPDIR/missing; cat > .bashrc" + body, false, ""},
		{"heredoc cd 后或", "cd $TMPDIR/sub || cat > f" + body, false, ""},
		{"heredoc cd 后管道", "cd $TMPDIR/sub | cat > f" + body, false, ""},
		{"heredoc cd 后换行", "cd $TMPDIR/sub\ncat > f" + body, false, ""},
		{"heredoc cd 与 && 仍放行", "cd $TMPDIR/sub && cat > f" + body, true, RuleHeredocInScope},
		{"rm cd 后分号", "cd $TMPDIR/x; rm -rf foo", false, ""},
		{"rm cd 后或", "cd $TMPDIR/x || rm -rf foo", false, ""},
		{"rm cd 与 && 仍放行", "cd $TMPDIR/x && rm -rf foo", true, RuleRmInScope},
		{"写入 cd 后分号", "cd $TMPDIR/x; printf x > f", false, ""},
		{"写入 cd 与 && 放行", "cd $TMPDIR/x && printf x > f", true, RuleSafeCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if tc.allow {
				if v.Action != AutoAllow || v.Rule != tc.rule {
					t.Fatalf("%q 应 AutoAllow rule=%s，得 %s rule=%q（%s）", tc.cmd, tc.rule, v.Action, v.Rule, v.Reason)
				}
				return
			}
			if v.Action == AutoAllow {
				t.Fatalf("%q 不得 AutoAllow（rule=%q %s）", tc.cmd, v.Rule, v.Reason)
			}
		})
	}
}

func TestB460CDChainTrustedUnit(t *testing.T) {
	cases := []struct {
		segs, ops []string
		want      bool
	}{
		{[]string{"ls", "echo x"}, []string{";"}, true},
		{[]string{"ls", "cd /a", "echo x"}, []string{";", "&&"}, true},
		{[]string{"x", "cd /a", "echo x"}, []string{"||", "&&"}, false},
		{[]string{"cd /a", "x", "echo"}, []string{"&&", "||"}, false},
		{[]string{"cd /a", "echo"}, []string{"\n"}, false},
	}
	for _, tc := range cases {
		if got := cdChainTrusted(tc.segs, tc.ops); got != tc.want {
			t.Fatalf("cdChainTrusted(%v,%v) = %v, want %v", tc.segs, tc.ops, got, tc.want)
		}
	}
}
