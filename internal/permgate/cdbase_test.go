// cdbase_test.go —— B383 S2b①「cd 后相对写按实际目录判」的判定面测试。
//
// 全部用例走 Gate.Judge（bash 路由 → judgeBash）——局部解析测试不能替代生产
// 判定（spec §4）。红例 = 现状按 Workdir 拼接误判的形态，修后期望 Escalate；
// 见证例 = 无 cd 或 cd 可证明的形态，修前修后出口必须一致（回归面）。
package permgate

import "testing"

// TestJudgeCDRelativeWriteEscalatesWhenUnprovable 红例：cd 目标越出三基准或
// 含不可证明形态时，其后相对写落点无法解释，整条必须 Escalate——修前这些命令
// 的相对落点被按 Workdir 拼接判成范围内（redirect.go:15-17 的已知误放行残余；
// 基线出口是 Consult 而非硬升级，台账 #60——落点被误判成范围内，越界从未
// 触发落点循环的确定性升级，交由廉价模型裁决）。
func TestJudgeCDRelativeWriteEscalatesWhenUnprovable(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"卡事件原始形态 cd etc 后重定向写", `cd /etc && echo x > passwd`},
		{"cd etc 后白名单命令写越界", `cd /etc && go test ./... > passwd`},
		{"cd etc 后管道写", `cd /etc && echo x | tee passwd`},
		{"cd etc 后写命令参数位", `cd /etc && git diff --output=passwd HEAD`},
		{"cd 出工作树", `cd .. && echo x > f`},
		{"cd 进 home", `cd $HOME && echo x > .bashrc`},
		{"未定义变量展开", `cd $UNSET_DIR/x && echo y > f`},
		{"先写后 cd 再写", `echo a > f && cd /etc && echo b > p`},
		{"不可证明 cd 后 tee", `cd /etc && printf y | tee passwd`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != Escalate {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 Escalate：cd 落点不可证明时相对写落点不可解释",
					tc.cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}

// TestJudgeCDRelativeWriteWitnessesUnchanged 见证例：无 cd、cd 可证明、或无相对
// 写落点的命令，修前修后出口必须逐一相同（无 cd 的命令行为零变化 = 回归面）。
func TestJudgeCDRelativeWriteWitnessesUnchanged(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
		want Action
		rule string
	}{
		{"裸 cd 白名单不变", `cd /etc`, AutoAllow, RuleSafeCommand},
		{"cd 加只读段不变", `cd /etc && ls`, AutoAllow, RuleSafeCommand},
		{"无 cd 的相对写不变", `echo x > out.txt`, Consult, ""},
		{"cd 进 TMPDIR 后写草稿", `cd $TMPDIR && echo x > out`, Consult, ""},
		{"cd 进 TMPDIR 后白名单命令写草稿", `cd $TMPDIR && go test ./... > out`, Consult, ""},
		{"cd 进工作树子目录后相对写", `cd src && echo x > f`, Consult, ""},
		{"rm 正例不受影响", `cd "$TMPDIR" && rm -rf verify`, AutoAllow, RuleRmInScope},
		{"rm 出基准仍升级", `cd /etc && rm -rf passwd`, Escalate, `(?i)rm\s+-[a-z]*[rf][a-z]*[rf]`},
		{"绝对落点不受 cd 影响", `cd /etc && echo x > /etc/passwd`, Escalate, ""},
		{"先写后 cd 无后继相对写", `echo a > f && cd /etc`, Consult, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != tc.want || v.Rule != tc.rule {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 %s（rule=%q）",
					tc.cmd, v.Action, v.Rule, v.Reason, tc.want, tc.rule)
			}
		})
	}
}

// TestCDWalkBase 单元面：cd 基准行走的可证明/破坏判定（heredoc 闭集共用同一面）。
func TestCDWalkBase(t *testing.T) {
	sc := newRmScope(t)
	cases := []struct {
		name    string
		cmd     string
		wantBro bool
	}{
		{"无 cd", `echo x > f`, false},
		{"cd 进 TMPDIR", `cd $TMPDIR && echo x > f`, false},
		{"cd 进 TMPDIR 花括号形态", `cd ${TMPDIR} && echo x > f`, false},
		{"cd 相对进工作树", `cd src && echo x > f`, false},
		{"cd 双引号 TMPDIR", `cd "$TMPDIR/sub" && echo x > f`, false},
		{"cd 出基准", `cd /etc && echo x > f`, true},
		{"cd 带旗标", `cd -L /etc && echo x > f`, true},
		{"cd 重绑定 TMPDIR", `TMPDIR=/etc; cd $TMPDIR && echo x > f`, true},
		{"cd 多词元", `cd /etc extra && echo x > f`, true},
		{"越界后再进可证明基准仍破坏", `cd /etc && cd $TMPDIR && echo x > f`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, bro := cdWalkBase(tc.cmd, sc)
			if bro != tc.wantBro {
				t.Fatalf("cdWalkBase(%q) broken = %v, want %v", tc.cmd, bro, tc.wantBro)
			}
		})
	}
}
