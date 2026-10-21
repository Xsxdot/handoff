// manualgate_test.go —— 人工清单判据的判定面测试（B383 S4 识别形态，B451 改出口）。
//
// B451（2026-10-21 用户裁定「人工清单改走审批链（硬升级不动）」）：四种形态
// ——git checkout/restore --、psql -c、内联 Python、rg --pre——命中后返回
// Consult（Rule=manual-gate 供审计区分），交审批链裁决；硬升级（自指令、黑名单
// 加包装器、eval、越界落点、截断）先判，不被人工清单截走；人工清单命中后不再进
// 白名单与复合只读静默，永不 AutoAllow。
package permgate

import (
	"strings"
	"testing"
)

// TestJudgeManualChecklistConsults S2：全部识别形态返回 Consult 且
// Rule=manual-gate，理由点明交审批链（B451 前为 Escalate）。
func TestJudgeManualChecklistConsults(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"git checkout -- 写工作树", `git checkout -- main.go`},
		{"git checkout -- 点路径", `git checkout -- .`},
		{"git restore -- 写工作树", `git restore -- main.go`},
		{"psql -c", `psql -c "select 1"`},
		{"psql -c 粘连形态", `psql -c'select 1'`},
		{"psql --command 长旗标", `psql --command="select 1"`},
		{"python -c", `python -c 'print(1)'`},
		{"python3 -c", `python3 -c 'print(1)'`},
		{"python3.11 -c", `python3.11 -c 'print(1)'`},
		{"python3 短旗标聚簇 -Sc", `python3 -Sc 'import os; os.system("id")'`},
		{"python 短旗标聚簇 -ESc", `python -ESc 'import os; os.system("id")'`},
		{"python 裸命令读标准输入", `python`},
		{"python 显式标准输入", `python -`},
		{"python heredoc 标准输入脚本", "python <<'EOF'\nprint(1)\nEOF"},
		{"spec S1 原形 python3 - heredoc", "python3 - <<'PY'\nopen('a.txt','w').write('x')\nPY"},
		{"python3 显式标准输入", `python3 -`},
		{"管道喂 python 标准输入", `echo x | python`},
		{"rg --pre", `rg --pre cat pattern .`},
		{"rg --pre= 粘连形态", `rg --pre=rm foo`},
		{"rg --pre 带 --no-config 仍拦", `rg --no-config --pre cat pattern .`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != Consult || v.Rule != RuleManualGate {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 Consult/manual-gate",
					tc.cmd, v.Action, v.Rule, v.Reason)
			}
			if !strings.HasPrefix(v.Reason, "人工清单：") || !strings.Contains(v.Reason, "交审批链裁决") {
				t.Fatalf("command %q reason = %q，期望「人工清单：」前缀且含「交审批链裁决」", tc.cmd, v.Reason)
			}
		})
	}
}

// TestJudgeManualChecklistShapeBoundaries 形态边界：可识别形态之外的用法维持
// 现状出口（不得把整个命令族一锅端进人工门——收口只覆盖清单点名的形态）。
func TestJudgeManualChecklistShapeBoundaries(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name      string
		cmd       string
		wantRule  string // 期望 Rule；空串表示不得是 manual-gate
		wantEqual Action // 断言 Action 等于它（零值跳过）
	}{
		{"git checkout 分支切换无 --", `git checkout main`, "", Consult},
		{"git checkout -b 建分支", `git checkout -b topic`, "", Consult},
		{"git status 不受影响", `git status`, RuleSafeCommand, AutoAllow},
		{"python 脚本文件参数", `python script.py`, "", Consult},
		{"python --version", `python3 --version`, "", Consult},
		{"rg 无 --pre 落白名单", `rg --no-config pattern .`, RuleSafeCommand, AutoAllow},
		{"psql 无 -c 维持现状", `psql -d mydb`, "", Consult},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if tc.wantEqual != 0 && v.Action != tc.wantEqual {
				t.Fatalf("command %q verdict = %s（rule=%q），期望 %s", tc.cmd, v.Action, v.Rule, tc.wantEqual)
			}
			if tc.wantRule == "" && v.Rule == RuleManualGate {
				t.Fatalf("command %q 不得落人工门（rule=%q）", tc.cmd, v.Rule)
			}
			if tc.wantRule != "" && v.Rule != tc.wantRule {
				t.Fatalf("command %q rule = %q，期望 %q", tc.cmd, v.Rule, tc.wantRule)
			}
		})
	}
}

// TestJudgeManualChecklistHardEscalationWins S4：人工清单与硬升级同现时仍为
// Escalate，且 Rule/Reason 与「去掉人工清单段的参照请求」逐字相等——硬升级
// 的真因不被人工清单的 Consult 截走，也不被改写。
func TestJudgeManualChecklistHardEscalationWins(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name     string
		req      Request
		ref      Request // 去掉人工清单段后的参照请求
		wantRule string  // 参照裁决的规则名（钉住参照本身，防参照判错导致假绿）
	}{
		{"内联 Python -c 加自指令",
			Request{Tool: "bash", Command: `python3 -c 'print(1)' && handoff card move B1 done`},
			Request{Tool: "bash", Command: `handoff card move B1 done`}, RuleSelfCommand},
		{"heredoc 内联 Python 加自指令",
			Request{Tool: "bash", Command: "python3 - <<'PY' && handoff card move B1 done\nprint(1)\nPY"},
			Request{Tool: "bash", Command: `handoff card move B1 done`}, RuleSelfCommand},
		{"内联 Python 越界落点",
			Request{Tool: "bash", Command: `python3 -c 'print(1)' > /etc/x`},
			Request{Tool: "bash", Command: `echo 1 > /etc/x`}, ""},
		{"psql -c 黑名单加执行包装器",
			Request{Tool: "bash", Command: `psql -c 'drop table users'`},
			Request{Tool: "bash", Command: `sh -c 'drop table users'`}, "-"},
		{"eval 加内联 Python",
			Request{Tool: "bash", Command: `eval "$X"; python3 -c 'print(1)'`},
			Request{Tool: "bash", Command: `eval "$X"; true`}, ""},
		{"截断描述",
			Request{Tool: "bash", Command: `python3 -c 'print(1)'`, Truncated: true},
			Request{Tool: "bash", Command: `ls`, Truncated: true}, ""},
		{"rg --pre 加自指令",
			Request{Tool: "bash", Command: `rg --pre handoff dispatch -e x`},
			Request{Tool: "bash", Command: `rg handoff dispatch -e x`}, RuleSelfCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := g.Judge(tc.ref, sc)
			if ref.Action != Escalate || ref.Rule == RuleManualGate {
				t.Fatalf("参照 %q verdict = %s（rule=%q），参照本身须为非人工清单的 Escalate",
					tc.ref.Command, ref.Action, ref.Rule)
			}
			switch tc.wantRule {
			case "-": // 黑名单规则名：只要求非空
				if ref.Rule == "" {
					t.Fatalf("参照 %q rule 为空，期望黑名单规则名", tc.ref.Command)
				}
			default:
				if ref.Rule != tc.wantRule {
					t.Fatalf("参照 %q rule = %q，期望 %q", tc.ref.Command, ref.Rule, tc.wantRule)
				}
			}
			v := g.Judge(tc.req, sc)
			if v.Action != Escalate || v.Rule != ref.Rule || v.Reason != ref.Reason {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望同参照 Escalate（rule=%q reason=%q）",
					tc.req.Command, v.Action, v.Rule, v.Reason, ref.Rule, ref.Reason)
			}
		})
	}
}

// TestJudgeManualChecklistCompoundNeverAutoAllow S5：人工清单段与只读段复合时
// 永不 AutoAllow——命中即返回 Consult/manual-gate，不进白名单与复合只读静默。
func TestJudgeManualChecklistCompoundNeverAutoAllow(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	for _, cmd := range []string{
		`python3 -c 'print(1)' && ls`,
		`git checkout -- a.go; git status`,
		`rg --pre cat x . | head`,
		`psql -c 'select 1' | wc -l`,
		`ls && python3 -`,
	} {
		t.Run(cmd, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: cmd}, sc)
			if v.Action == AutoAllow {
				t.Fatalf("command %q 被 AutoAllow（rule=%q reason=%q），人工清单形态永不自动放行",
					cmd, v.Rule, v.Reason)
			}
			if v.Action != Consult || v.Rule != RuleManualGate {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 Consult/manual-gate",
					cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}
