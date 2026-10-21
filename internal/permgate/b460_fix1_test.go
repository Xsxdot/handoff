package permgate

// b460_fix1_test.go —— B460 修复轮 1（机内审查 B1/B1b/B2/N5）。
//
//   - B1/B1b：选项形 / 特殊目标的 cd（`cd -`、`cd --`、`cd -P`、`cd -L`、zsh `cd -1` `cd +1`、
//     裸 `cd`、`cd ~…`）不得当作已证明基准——修前它们被当成名为 `-`/`--` 的相对目录，
//     三处放行出口（范围内写入、heredoc-in-scope、rm-in-scope）都判 AutoAllow，而 shell
//     实际回到 $OLDPWD / $HOME。
//   - B2：白名单变量（TMPDIR、HANDOFF_ARTIFACTS_DIR）在命令里以 `$NAME`/`${NAME}` 展开
//     之外的任何形态出现，都按重绑定嫌疑处理；修前 `printf -vTMPDIR /etc` 连写形态
//     漏检，且 `printf -v` 被当静默段。
//   - N5：两条守卫与展开字符规则各有单元级钉子，关掉任一条都有测试变红。
import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

const b460Heredoc = " <<'EOF'\nx\nEOF"

// TestB460UntrustedCDNeverAutoAllows 锁 B1/B1b：表内命令经 Gate.Judge 一律不得 AutoAllow。
func TestB460UntrustedCDNeverAutoAllows(t *testing.T) {
	g := newTestGate(t)
	sc := b460Scope(t)
	art, tmp := sc.ArtifactsDir, sc.TaskTmpDir
	cases := []struct {
		name string
		cmd  string
		cwd  string
	}{
		// 审查给定的必备例
		{name: "写入 cd --", cmd: "cd $TMPDIR && cd -- && echo x > .bashrc"},
		{name: "mkdir cd -", cmd: "cd $TMPDIR && cd - && mkdir .ssh"},
		{name: "heredoc cd --", cmd: "cd " + tmp + " && cd -- && cat > .bashrc" + b460Heredoc},
		{name: "rm cd -", cmd: "cd " + tmp + " && cd - && rm -rf foo"},
		{name: "裸 cd 写入", cmd: "cd && echo x > .bashrc"},
		{name: "codex cwd 下 cd --", cmd: "cd -- && echo x > .bashrc", cwd: art},
		// 同族变体
		{name: "写入 cd -P", cmd: "cd " + art + " && cd -P && echo x > .bashrc"},
		{name: "写入 cd -L", cmd: "cd " + art + " && cd -L && printf x > .bashrc"},
		{name: "写入 zsh cd -1", cmd: "cd " + art + " && cd -1 && echo x > .bashrc"},
		{name: "写入 zsh cd +1", cmd: "cd " + art + " && cd +1 && echo x > .bashrc"},
		{name: "写入 cd ~", cmd: "cd " + art + " && cd ~ && echo x > .bashrc"},
		{name: "写入 cd ~/x", cmd: "cd " + art + " && cd ~/x && echo x > f"},
		{name: "写入 引号包 --", cmd: "cd " + art + ` && cd "--" && echo x > .bashrc`},
		{name: "写入 空串目标", cmd: "cd " + art + ` && cd "" && echo x > .bashrc`},
		{name: "cwd 下 cd - mkdir", cmd: "cd - && mkdir .ssh", cwd: art},
		{name: "cwd 下 cd +1", cmd: "cd +1 && printf x > .bashrc", cwd: art},
		{name: "cwd 下裸 cd", cmd: "cd && printf x > .bashrc", cwd: art},
		{name: "heredoc cd -", cmd: "cd $TMPDIR && cd - && cat > .bashrc" + b460Heredoc},
		{name: "heredoc 裸 cd", cmd: "cd " + tmp + " && cd && cat > .bashrc" + b460Heredoc},
		{name: "heredoc cd -P", cmd: "cd " + tmp + " && cd -P && cat > f" + b460Heredoc},
		{name: "rm cd --", cmd: "cd $TMPDIR && cd -- && rm -rf foo"},
		{name: "rm cd +1", cmd: "cd $TMPDIR && cd +1 && rm -rf foo"},
		{name: "rm 裸 cd", cmd: "cd $TMPDIR && cd && rm -rf foo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: executor.PermToolBash, Command: tc.cmd, Cwd: tc.cwd}, sc)
			if v.Action == AutoAllow {
				t.Fatalf("%q（cwd=%q）不得 AutoAllow（rule=%q %s）", tc.cmd, tc.cwd, v.Rule, v.Reason)
			}
		})
	}
}

// TestB460UntrustedCDBreaksChain 锁共用判据本身：选项形 / 特殊目标 / 无参 cd 让整条 cd 链
// 不可信；普通目标与 && 链仍可信（防过度拒绝）。
func TestB460UntrustedCDBreaksChain(t *testing.T) {
	and := func(n int) []string {
		ops := make([]string, n)
		for i := range ops {
			ops[i] = "&&"
		}
		return ops
	}
	for _, c := range []string{"cd -", "cd --", "cd -P", "cd -L", "cd -1", "cd +1", "cd", "cd ~", "cd ~/x", `cd ""`, `cd "-"`, `cd \-`} {
		segs := []string{"cd /a", c, "echo x > f"}
		if cdChainTrusted(segs, and(2)) {
			t.Errorf("cdChainTrusted 含 %q 应为假", c)
		}
	}
	for _, c := range []string{"cd /a/b", "cd sub", "cd $TMPDIR", `cd "$TMPDIR/x"`, "cd ./-x"} {
		segs := []string{c, "echo x > f"}
		if !cdChainTrusted(segs, and(1)) {
			t.Errorf("cdChainTrusted 含 %q 应为真", c)
		}
	}
}

// TestB460UntrustedCDRangeLoopBroken 锁落点范围循环：选项形 cd 之后的相对写落点不可解释
// （与既有「带旗标 cd 破坏基准」同结论），不得被当成 <基准>/--/.bashrc 判在范围内。
func TestB460UntrustedCDRangeLoopBroken(t *testing.T) {
	sc := b460Scope(t)
	for _, cmd := range []string{
		"cd $TMPDIR && cd -- && echo x > .bashrc",
		"cd $TMPDIR && cd - && mkdir .ssh",
		"cd " + sc.TaskTmpDir + " && cd +1 && echo x > f",
	} {
		if _, unres := cdResolvedTargets(cmd, sc, ""); unres == "" {
			t.Errorf("%q：选项形 cd 后的相对落点应不可解释", cmd)
		}
		if _, broken := cdWalkBase(cmd, sc); !broken {
			t.Errorf("%q：cdWalkBase 应判基准破坏", cmd)
		}
	}
	if _, unres := cdResolvedTargets("cd -- && echo x > .bashrc", sc, sc.ArtifactsDir); unres == "" {
		t.Error("cwd 起点下 cd -- 后的相对落点应不可解释")
	}
}

// b460RebindForms 是重绑定形态全集（NAME 占位），审查 B2 列举 + 同族补充。
var b460RebindForms = []string{
	"printf -vNAME /etc",
	"printf -v NAME /etc",
	"read NAME",
	"read -r NAME",
	"export NAME=/etc",
	"declare NAME=/etc",
	"typeset NAME=/etc",
	"local NAME=/etc",
	"readonly NAME=/etc",
	"NAME=/etc",
	"NAME=/etc true",
	"for NAME in /etc; do true; done",
	"mapfile NAME",
	"readarray NAME",
	"unset NAME",
	"getopts a NAME",
	"declare -n x=NAME",
	": ${NAME:=/etc}",
	": ${NAME=/etc}",
	"echo ${!NAME}",
}

func b460Form(form, name string) string {
	out := ""
	for i := 0; i < len(form); i++ {
		if i+4 <= len(form) && form[i:i+4] == "NAME" {
			out += name
			i += 3
			continue
		}
		out += string(form[i])
	}
	return out
}

// TestB460RebindGuardPerVar 单元级钉住两条守卫（N5：修前关掉 HANDOFF_ARTIFACTS_DIR 守卫无测试变红）。
// 每个变量名各自过全部形态；只出现另一个变量名的命令不连坐（证明两者独立判定）。
func TestB460RebindGuardPerVar(t *testing.T) {
	for _, name := range []string{"TMPDIR", "HANDOFF_ARTIFACTS_DIR"} {
		for _, form := range b460RebindForms {
			cmd := b460Form(form, name) + ` && printf x > "$` + name + `/a"`
			if !scopeVarRebindSuspect(cmd) {
				t.Errorf("scopeVarRebindSuspect(%q) 应为真", cmd)
			}
		}
		// 合法展开不算嫌疑
		for _, ok := range []string{`printf x > "$NAME/a"`, `cd ${NAME} && echo x > f`, `mkdir -p "${NAME}/s" $NAME/t`} {
			cmd := b460Form(ok, name)
			if scopeVarRebindSuspect(cmd) {
				t.Errorf("scopeVarRebindSuspect(%q) 应为假（纯展开）", cmd)
			}
		}
	}
	// TMPDIR 专用守卫（rm 出口用）同样认全部形态
	for _, form := range b460RebindForms {
		cmd := b460Form(form, "TMPDIR") + " && rm -rf $TMPDIR/x"
		if !tmpdirRebindSuspect(cmd) {
			t.Errorf("tmpdirRebindSuspect(%q) 应为真", cmd)
		}
	}
}

// TestB460RebindNeverAutoAllows 端到端：每种重绑定形态 + 白名单变量前缀写，三处出口都不放行。
func TestB460RebindNeverAutoAllows(t *testing.T) {
	g := newTestGate(t)
	sc := b460Scope(t)
	for _, name := range []string{"TMPDIR", "HANDOFF_ARTIFACTS_DIR"} {
		for _, form := range b460RebindForms {
			pre := b460Form(form, name)
			for _, tail := range []string{
				`echo x > $NAME/passwd`,
				`mkdir -p "$NAME/.ssh"`,
				`cd $NAME && printf x > f`,
				`cd "$NAME" && cat > f` + b460Heredoc,
				`rm -rf $NAME/x`,
			} {
				cmd := pre + " && " + b460Form(tail, name)
				v := g.Judge(Request{Tool: executor.PermToolBash, Command: cmd}, sc)
				if v.Action == AutoAllow {
					t.Errorf("%q 不得 AutoAllow（rule=%q %s）", cmd, v.Rule, v.Reason)
				}
			}
		}
	}
	// eval 维持硬升级（确认，不改）
	v := g.Judge(Request{Tool: executor.PermToolBash, Command: `eval "TMPDIR=/etc" && echo x > $TMPDIR/passwd`}, sc)
	if v.Action != Escalate {
		t.Fatalf("eval 应 Escalate，得 %s（%s）", v.Action, v.Reason)
	}
}

// TestB460PrintfVNeverSilent 锁 B2 附加项：`printf -v` 在任何写法下都不是静默段。
func TestB460PrintfVNeverSilent(t *testing.T) {
	for _, seg := range []string{
		"printf -v x hi", "printf -vx hi", `printf "-vx" hi`, `printf '-v' x hi`, `printf \-vx hi`,
		`printf ""-vx hi`,
	} {
		if id, ok := matchSilentSegment(seg); ok {
			t.Errorf("matchSilentSegment(%q) 不得静默（id=%s）", seg, id)
		}
	}
	if _, ok := matchSilentSegment("printf 'a %s' b"); !ok {
		t.Error("普通 printf 仍应静默")
	}
}

// TestB460ExpansionCharRule 钉住 B1 展开字符规则（N5）：白名单变量前缀之后、或已证明基准下的
// 相对落点，残留 $ \ ~ { } , 任一字符即不可证明。
func TestB460ExpansionCharRule(t *testing.T) {
	sc := b460Scope(t)
	for _, c := range []string{"$X", `\.`, "~", "{a,b}", "a,b", "${X}"} {
		tok := "$TMPDIR/" + c + "/f"
		if _, isVar, ok := scopeVarPath(tok, sc, false); !isVar || ok {
			t.Errorf("scopeVarPath(%q) 应识别为白名单形态但不可解释，得 isVar=%v ok=%v", tok, isVar, ok)
		}
		tok = "$HANDOFF_ARTIFACTS_DIR/" + c
		if _, isVar, ok := scopeVarPath(tok, sc, false); !isVar || ok {
			t.Errorf("scopeVarPath(%q) 应不可解释，得 isVar=%v ok=%v", tok, isVar, ok)
		}
		rel := "a" + c
		if _, ok := proveScopedToken(rel, sc.ArtifactsDir, sc, false); ok {
			t.Errorf("proveScopedToken(%q) 在已证明基准下仍应不可证明", rel)
		}
	}
	// 对照：干净后缀可解释
	if _, isVar, ok := scopeVarPath("$TMPDIR/a/b", sc, false); !isVar || !ok {
		t.Error("$TMPDIR/a/b 应可解释")
	}
	g := newTestGate(t)
	for _, cmd := range []string{
		`printf x > "$TMPDIR/$X/f"`,
		`mkdir -p $HANDOFF_ARTIFACTS_DIR/~`,
		"cd " + sc.ArtifactsDir + ` && echo x > {a,b}`,
		"cd " + sc.ArtifactsDir + ` && echo x > \.bashrc`,
		`cd $TMPDIR && printf x > $X`,
	} {
		v := g.Judge(Request{Tool: executor.PermToolBash, Command: cmd}, sc)
		if v.Action == AutoAllow {
			t.Errorf("%q 不得 AutoAllow（rule=%q %s）", cmd, v.Rule, v.Reason)
		}
	}
}
