// manualgate_test.go —— B383 S4「人工清单保持 Escalate」的判定面测试。
//
// v6 §S2：先前用户裁决的人工清单仍须保持 Escalate——git checkout --、psql -c、
// 内联 Python、rg --pre。仅排除 AutoAllow 不够，因为 Consult 可由审批模型直接
// 批准；这些形态在 permgate 判定层就显式返回 Escalate（Rule=manual-gate），
// 不落 Consult、不落白名单。
package permgate

import "testing"

// TestJudgeManualChecklistEscalates 红例：修前这些形态落 Consult（审批模型可
// 直批），修后必须显式 Escalate 且 Rule=manual-gate 供审计区分。
func TestJudgeManualChecklistEscalates(t *testing.T) {
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
		{"python 裸命令读标准输入", `python`},
		{"python 显式标准输入", `python -`},
		{"python heredoc 标准输入脚本", "python <<'EOF'\nprint(1)\nEOF"},
		{"管道喂 python 标准输入", `echo x | python`},
		{"rg --pre", `rg --pre cat pattern .`},
		{"rg --pre= 粘连形态", `rg --pre=rm foo`},
		{"rg --pre 带 --no-config 仍拦", `rg --no-config --pre cat pattern .`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != Escalate || v.Rule != RuleManualGate {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 Escalate/manual-gate",
					tc.cmd, v.Action, v.Rule, v.Reason)
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
