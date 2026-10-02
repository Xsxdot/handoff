// rmtargets_test.go —— B383 S2a「rm 范围内删除放行」的判定面测试。
//
// 全部用例走 Gate.Judge（bash 路由 → judgeBash）——局部解析测试不能替代
// 生产判定（spec §4）。正例现状红（Escalate/Consult）→ 修后 AutoAllow；
// 反例修前修后都必须 Escalate，一丝不满足整条不放宽。
package permgate

import (
	"os"
	"path/filepath"
	"testing"
)

// newRmScope 构造带 TaskTmpDir 的测试范围。TaskTmpDir 真实建目录——软链反例
// 需要在其下建链接；workdir/taskdir 用临时目录凑齐三基准。
func newRmScope(t *testing.T) Scope {
	t.Helper()
	work := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "task-tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatalf("建 TaskTmpDir: %v", err)
	}
	return Scope{Workdir: work, TaskDir: t.TempDir(), TaskTmpDir: tmp}
}

// TestJudgeRmInScopeReleases 正例闭集：全部目标可证明落在 TaskTmpDir 内时，
// 黑名单 rm 命中解除，整条 AutoAllow（Rule=rm-in-scope）。
func TestJudgeRmInScopeReleases(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"原始场景 cd 进 TMPDIR 后相对删", `cd "$TMPDIR" && rm -rf verify`},
		{"直接目标", `rm -rf $TMPDIR/x`},
		{"多目标 -f", `rm -f $TMPDIR/a $TMPDIR/b`},
		{"无旗标单目标", `rm $TMPDIR/x`},
		{"-- 分隔符后目标", `rm -rf -- $TMPDIR/x`},
		{"cd 进子目录后相对删", `cd "$TMPDIR/sub" && rm -rf y`},
		{"-- 后形似旗标的目标", `cd "$TMPDIR" && rm -- -rf-looking-name`},
		{"花括号变量形态", `rm -rf ${TMPDIR}/x`},
		{"双引号包裹的直接目标", `rm -rf "$TMPDIR/x"`},
		{"混合引号的多目标", `rm -rf "$TMPDIR"/x $TMPDIR/y`},
		{"跨段全过", `rm -rf $TMPDIR/a; rm -rf $TMPDIR/b`},
		// 评审安全对照（实测定性语义安全，须保持放行）：双引号内整串是
		// 单个文件名字面量——分号与空格都在文件名里，真实 rm 只删 TaskTmpDir
		// 下那个名字怪异的文件。词元不含 rmPathBanChars 任何字符，不受伤。
		{"双引号内整串单文件名字面量", `rm -rf "$TMPDIR/x; rm -rf /etc"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != AutoAllow || v.Rule != RuleRmInScope {
				t.Fatalf("command %q verdict = %s（rule=%q reason=%q），期望 AutoAllow/rm-in-scope",
					tc.cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}

// TestJudgeRmEscalateUnchanged 反例面：修前修后都必须 Escalate，不得放宽。
// 覆盖 spec §S2 的反例集与闭集边界（glob、软链、基准自身、单引号、大小写）。
func TestJudgeRmEscalateUnchanged(t *testing.T) {
	g := newTestGate(t)
	sc := newRmScope(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(sc.TaskTmpDir, "evil-link")); err != nil {
		t.Fatalf("建软链: %v", err)
	}
	cases := []struct {
		name string
		cmd  string
	}{
		{"解析后逃逸", `rm -rf $TMPDIR/../../etc`},
		{"多级 .. 逃逸", `rm -rf $TMPDIR/x/y/../../../etc`},
		{"跨段一票否决", `rm -rf $TMPDIR/a; rm -rf /etc`},
		{"同名赋值重绑定", `TMPDIR=/etc; cd "$TMPDIR" && rm -rf verify`},
		{"env 前缀赋值重绑定", `TMPDIR=/etc rm -rf $TMPDIR/*`},
		{"export 重绑定", `export TMPDIR=/x && rm -rf $TMPDIR/*`},
		{"sh 包装器", `sh -c "rm -rf $TMPDIR/x"`},
		{"bash 包装器", `bash -c 'rm -rf $TMPDIR/x'`},
		{"cd 出基准", `cd /etc && rm -rf passwd`},
		{"无 cd 裸相对目标", `rm -rf verify`},
		{"cd 进 Workdir 后相对删（rm 放行面不含 Workdir）", `cd ` + sc.Workdir + ` && rm -rf file.txt`},
		{"尾段 cd 出基准", `cd "$TMPDIR" && rm -rf verify && cd /`},
		{"未定义变量", `rm -rf $UNSET_VAR/x`},
		{"命令替换", `rm -rf $(pwd)/x`},
		{"glob 不在闭集", `rm -rf $TMPDIR/*`},
		{"删除基准自身", `rm -rf $TMPDIR`},
		{"软链解析后逃逸", `rm -rf $TMPDIR/evil-link`},
		{"非 cd/rm 段破坏闭集", `rm -rf $TMPDIR/x && echo done`},
		{"rm 前序段破坏闭集", `echo prep && rm -rf $TMPDIR/x`},
		{"管道段", `rm -rf $TMPDIR/x | tee /tmp/out`},
		{"单引号字面量不做变量展开", `rm -rf '$TMPDIR/x'`},
		{"大写 RM 不在闭集", `RM -rf $TMPDIR/x`},
		// —— 以下 12 例为批次 B 独立评审（NEEDS-FIX）实测的绕过形态：修复前
		// proveDirInTmp 相对分支不拒展开字符、Clean 不认 \ 分隔、花括号/引号
		// 邻接拼接，全部被误放行（其中 4 例经真实 bash 验证删到 TaskTmpDir
		// 之外）。修复 = 词元字符拒绝（$ 白名单外残留、\、~、{、}、,）。
		{"cd 后变量展开成绝对路径", `cd "$TMPDIR" && rm -rf $HOME/x`},
		{"cd 后花括号变量展开", `cd "$TMPDIR" && rm -rf ${HOME}/x`},
		{"cd 后未定义变量展开为空", `cd "$TMPDIR" && rm -rf $UNSET_VAR/x`},
		{"变量名边界 $TMPDIRX", `cd "$TMPDIR" && rm -rf $TMPDIRX/sub`},
		{"默认值展开 ${TMPDIR:-…}", `cd "$TMPDIR" && rm -rf ${TMPDIR:-/etc}/x`},
		{"词首 tilde 展开", `cd "$TMPDIR" && rm -rf ~/.ssh`},
		{"反斜杠转义剥出 ..", `rm -rf $TMPDIR/\../etc`},
		{"cd 后反斜杠转义剥出 ..", `cd "$TMPDIR" && rm -rf \../etc`},
		{"引号邻接拼接 $TMPDIRx", `cd "$TMPDIR" && rm -rf "$TMPDIR"x`},
		{"花括号邻接拼接 ${TMPDIR}x", `cd "$TMPDIR" && rm -rf ${TMPDIR}x`},
		{"花括号展开一变多", `rm -rf $TMPDIR/{a,/etc/passwd}`},
		{"cd 后花括号展开成绝对目标", `cd "$TMPDIR" && rm -rf {/etc/passwd,/etc/hosts}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != Escalate {
				t.Fatalf("command %q verdict = %s（reason=%q），必须维持 Escalate 不放宽",
					tc.cmd, v.Action, v.Reason)
			}
		})
	}
}

// TestRmDeletionTargetsTable 提取器单测：旗标跳过、-- 分隔符、簇旗标与未知
// 旗标的边界。-- 之后的词元一律是目标，形如旗标也不得误吞。
func TestRmDeletionTargetsTable(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		want   []string
		wantOK bool
	}{
		{"连写旗标", []string{"-rf", "$TMPDIR/x"}, []string{"$TMPDIR/x"}, true},
		{"分写旗标", []string{"-r", "-f", "a", "b"}, []string{"a", "b"}, true},
		{"未知簇旗标不放宽", []string{"-rX", "a"}, nil, false},
		{"-- 后旗标形是目标", []string{"--", "-rf-looking-name"}, []string{"-rf-looking-name"}, true},
		{"-- 前旗标仍跳过", []string{"-rf", "--", "$TMPDIR/x", "-y"}, []string{"$TMPDIR/x", "-y"}, true},
		{"长旗标闭集", []string{"--recursive", "--force", "a"}, []string{"a"}, true},
		{"未知长旗标不放宽", []string{"--unknown", "a"}, nil, false},
		{"interactive 带等号值", []string{"--interactive=never", "a"}, []string{"a"}, true},
		{"零目标", []string{"-rf"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rmDeletionTargets(tc.args)
			if ok != tc.wantOK {
				t.Fatalf("rmDeletionTargets(%v) ok = %v，期望 %v", tc.args, ok, tc.wantOK)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("rmDeletionTargets(%v) = %v，期望 %v", tc.args, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("rmDeletionTargets(%v) = %v，期望 %v", tc.args, got, tc.want)
				}
			}
		})
	}
}
