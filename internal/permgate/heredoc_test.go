// heredoc_test.go —— B383 S2b②「heredoc 受限闭集」的判定面测试。
//
// 首批只认卡事件 17074 的 `cd $TMPDIR/cardq && cat > main.go <<'EOF' … EOF`
// 同构形态：引用的字面定界符、单个裸 cat 写入 scope 内一个文件、整条命令及
// 所有段无管道/替换/包装器/执行型后继。命中时正文作为数据从各判据面剔除；
// 不命中维持现状升级，绝不剔除正文后交既有宽松白名单。
package permgate

import "testing"

// TestJudgeHeredocInScopeReleases 正例闭集：修前全部落 Consult/升级（红），
// 修后 AutoAllow（Rule=heredoc-in-scope）。
func TestJudgeHeredocInScopeReleases(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"卡事件 17074 同构形态", "cd $TMPDIR/cardq && cat > main.go <<'EOF'\npackage main\n\nfunc main() {}\nEOF"},
		{"无前置 cd 写工作树", "cat > main.go <<'EOF'\nhello\nEOF"},
		{"追加写", "cat >> main.go <<'EOF'\nmore\nEOF"},
		{"双引号定界符", `cat > f <<"EOF"` + "\nplain\nEOF"},
		{"cd 双引号 TMPDIR 进子目录", "cd \"$TMPDIR\" && cat > sub/dir/f <<'EOF'\nx\nEOF"},
		{"重定向与目标无空格", "cat >f <<'EOF'\nx\nEOF"},
		{"正文含 handoff 字样按数据剔除", "cat > notes.md <<'EOF'\nhandoff dispatch T1\nEOF"},
		{"正文含重定向按数据剔除", "cat > main.go <<'EOF'\necho x > /etc/y\nEOF"},
		{"正文含黑名单字样按数据剔除", "cat > notes.md <<'EOF'\nsudo rm -rf /\nEOF"},
		{"目标含引号空格", `cat > "a && b.txt" <<'EOF'` + "\nx\nEOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != AutoAllow || v.Rule != RuleHeredocInScope {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 AutoAllow/heredoc-in-scope",
					tc.cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}

// TestJudgeHeredocUnmatchedKeepsStatusQuo 反例面：一丝不满足闭集即整条不放宽，
// 正文**不剔除**、按现状（含正文）判定——修前修后出口逐一相同。
func TestJudgeHeredocUnmatchedKeepsStatusQuo(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
		want Action
	}{
		{"管道至 bash 消费正文", "cat <<'EOF' | bash\nrm -rf /\nEOF", Escalate},
		{"未引用定界符正文黑名单命中", "cat <<EOF\nrm -rf /\nEOF", Escalate},
		{"未引用定界符正文藏越界写", "cat > f <<EOF\nhi > /etc/x\nEOF", Escalate},
		{"cat 无重定向正文藏越界写", "cat <<'EOF'\necho x > /etc/y\nEOF", Escalate},
		{"bash 消费正文", "bash <<'EOF'\nrm -rf /\nEOF", Escalate},
		{"sh 消费正文", "sh > f <<'EOF'\nrm -rf /\nEOF", Escalate},
		{"执行型后继", "cat > main.go <<'EOF'\nbody\nEOF\necho done", Consult},
		{"双 cat", "cat > a.txt <<'EOF'\nA\nEOF\ncat > b.txt <<'EOF'\nB\nEOF", Consult},
		{"tee 消费正文", "tee > f <<'EOF'\nx\nEOF", Consult},
		{"写落点越界", "cat > /etc/passwd <<'EOF'\nx\nEOF", Escalate},
		{"cd 出基准后相对写正文", "cd /etc && cat > passwd <<'EOF'\nx\nEOF", Escalate},
		{"TMPDIR 重绑定", "TMPDIR=/etc; cd $TMPDIR && cat > f <<'EOF'\nx\nEOF", Escalate},
		{"未终止的正文", "cat > f <<'EOF'\nno terminator", Consult},
		{"终止符后还有命令", "cat > f <<'EOF'\nx\nEOF\necho more", Consult},
		{"cat 段带额外旗标", "cat -n > f <<'EOF'\nx\nEOF", Consult},
		{"操作符后还有词元", "cat > f <<'EOF' extra\nx\nEOF", Consult},
		{"python 消费正文走人工门", "python <<'EOF'\nprint(1)\nEOF", Escalate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != tc.want {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 %s（现状出口）",
					tc.cmd, v.Action, v.Rule, v.Reason, tc.want)
			}
			if v.Action == AutoAllow {
				t.Fatalf("command %q 不得经 heredoc 闭集放行（rule=%q）", tc.cmd, v.Rule)
			}
		})
	}
}

// TestClosedHeredocParse 提取器单元面：闭集形态的识别与正文剥离边界。
func TestClosedHeredocParse(t *testing.T) {
	cases := []struct {
		name         string
		cmd          string
		wantMatch    bool
		wantTarget   string
		wantStripped string
	}{
		{
			"原始形态", "cd $TMPDIR/cardq && cat > main.go <<'EOF'\nbody\nEOF",
			true, "main.go", "cd $TMPDIR/cardq && cat > main.go <<'EOF'",
		},
		{
			"双引号定界符", `cat > f <<"EOF"` + "\nbody\nEOF",
			true, "f", `cat > f <<"EOF"`,
		},
		{
			"追加写", "cat >> f <<'X'\nbody\nX",
			true, "f", "cat >> f <<'X'",
		},
		{
			"未引用定界符", "cat > f <<EOF\nbody\nEOF",
			false, "", "",
		},
		{
			"正文无终止符", "cat > f <<'EOF'\nbody",
			false, "", "",
		},
		{
			"正文含早现定界符行", "cat > f <<'EOF'\nEOF\nmore\nEOF",
			false, "", "",
		},
		{
			"终止符后还有命令", "cat > f <<'EOF'\nbody\nEOF\necho more",
			false, "", "",
		},
		{
			"cat 无重定向", "cat <<'EOF'\nbody\nEOF",
			false, "", "",
		},
		{
			"here-string 不算", "cat > f <<<'text'",
			false, "", "",
		},
		{
			"fd 前缀 heredoc 不算", "cat > f 2<<'EOF'\nbody\nEOF",
			false, "", "",
		},
		{
			"同一命令行两个 heredoc", "cat > f <<'A' <<'B'\nx\nA\ny\nB",
			false, "", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hd := closedHeredoc(tc.cmd)
			if tc.wantMatch {
				if hd == nil {
					t.Fatalf("closedHeredoc(%q) = nil, want match", tc.cmd)
				}
				if hd.target != tc.wantTarget {
					t.Fatalf("target = %q, want %q", hd.target, tc.wantTarget)
				}
				if hd.stripped != tc.wantStripped {
					t.Fatalf("stripped = %q, want %q", hd.stripped, tc.wantStripped)
				}
				return
			}
			if hd != nil {
				t.Fatalf("closedHeredoc(%q) = %+v, want nil", tc.cmd, hd)
			}
		})
	}
}
