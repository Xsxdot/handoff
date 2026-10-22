// b466_cd_heredoc_test.go —— B466：cd 进工作树子目录后，heredoc 正文被当写落点、
// glob 写落点一律不可解释，导致整条 Escalate。
//
// 红例取 E40.3（9e598686@linux-01）被升级的原文形态：正文保留触发误判的那几行，
// 其余删短。全部走 Gate.Judge（生产判定入口）。
package permgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newB466Scope 建一个带 server/ 与 admin/ 子目录的工作树（E40.3 仓的形状）。
func newB466Scope(t *testing.T) Scope {
	t.Helper()
	sc := newRmScope(t)
	for _, d := range []string{"server/system/cs/internal/app", "admin/test"} {
		if err := os.MkdirAll(filepath.Join(sc.Workdir, d), 0o755); err != nil {
			t.Fatalf("建子目录 %s: %v", d, err)
		}
	}
	for _, f := range []string{"server/system/cs/internal/app/ai.go", "server/system/cs/internal/app/ai_send.go"} {
		if err := os.WriteFile(filepath.Join(sc.Workdir, f), []byte("package app\n"), 0o644); err != nil {
			t.Fatalf("建文件 %s: %v", f, err)
		}
	}
	return sc
}

// E40.3 seq 4630 / 9776 同形：python3 正文 + 正文后 `gofmt -w … *.go`。
const b466PyGlob = "cd server && python3 - <<'EOF'\n" +
	"p='system/cs/internal/app/ai.go'\n" +
	"s=open(p).read()\n" +
	"old='''// activityFor 会话实时状态优先级：人工输入锁 > ai_typing（拟稿中）> ai_pending\n" +
	"func (a *App) activityFor(c *model.Conversation) string {'''\n" +
	"s=s.replace(old,old,1)\n" +
	"open(p,'w').write(s)\n" +
	"EOF\n" +
	"gofmt -w system/cs/internal/app/*.go && go build ./system/cs/... 2>&1 | head -30"

// E40.3 seq 8718 同形：cat 正文（TS，含 `=> {`）+ 正文后 `node --test`。
const b466CatTS = "cd admin && cat > test/cs-workbench-draft.test.ts <<'EOF'\n" +
	"import { test } from 'node:test';\n" +
	"test('draft head', () => {\n" +
	"  assert.ok(!JSON.stringify({ ...snap.items[0], dueAt: undefined }).includes('dueAt'));\n" +
	"});\n" +
	"EOF\n" +
	"node --test test/cs-workbench-draft.test.ts 2>&1 | tail -12"

// E40.3 seq 2514 同形：python3 正文含 Go 的 `*p`（`x > *p` 被当重定向）。
const b466PyStar = "cd server && python3 - <<'EOF'\n" +
	"p='system/cs/internal/app/ai_send.go'\n" +
	"s=open(p).read()\n" +
	"s=s.replace('if n > *p {','if n >= *p {')\n" +
	"open(p,'w').write(s)\n" +
	"EOF\n"

// TestB466CDSubdirHeredocNotEscalated 红例：cd 进工作树子目录后，正文里的伪落点与
// 后继的 glob 落点不得再让整条升级人工。
func TestB466CDSubdirHeredocNotEscalated(t *testing.T) {
	g := newTestGate(t)
	sc := newB466Scope(t)
	cases := []struct {
		name string
		cmd  string
		want Action
		rule string
	}{
		// python 正文按 B451 人工清单交审批链——本卡不扩放行面，只是不再升级人工
		{"python3 正文 + gofmt glob 后继", b466PyGlob, Consult, RuleManualGate},
		{"python3 正文含 *p", b466PyStar, Consult, RuleManualGate},
		// cat 正文 + node --test：node 不在白名单（cd 无关），Consult
		{"cat 正文 + node --test 后继", b466CatTS, Consult, ""},
		// 已知安全命令 gofmt -w 的 glob 落点在 cd 子目录后可证明
		{"cd 子目录 gofmt -w glob", "cd server && gofmt -w system/cs/internal/app/*.go", AutoAllow, RuleSafeCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != tc.want || v.Rule != tc.rule {
				t.Fatalf("verdict = %s（rule=%q reason=%q），期望 %s（rule=%q）\ncommand:\n%s",
					v.Action, v.Rule, v.Reason, tc.want, tc.rule, tc.cmd)
			}
		})
	}
}

// TestB466SafeCommandsAfterCDSubdir 见证例：已知安全命令在 cd 工作树子目录后仍走静态放行。
func TestB466SafeCommandsAfterCDSubdir(t *testing.T) {
	g := newTestGate(t)
	sc := newB466Scope(t)
	for _, cmd := range []string{
		"cd server && gofmt -l system/cs app",
		"cd server && go vet ./system/cs/...",
		"cd server && go test ./system/cs/... -count=1 2>&1 | tail -30",
		"cd server && gofmt -l system/cs app | head; go vet ./system/cs/... 2>&1 | head -20; go test ./system/cs/... -count=1 2>&1 | tail -30",
		"cd admin && npm run typecheck",
		"cd server && gofmt -w system/cs/internal/app/ai.go",
	} {
		v := g.Judge(Request{Tool: "bash", Command: cmd}, sc)
		if v.Action != AutoAllow {
			t.Errorf("%q verdict = %s（rule=%q reason=%q），期望 AutoAllow", cmd, v.Action, v.Rule, v.Reason)
		}
	}
}

// TestB466CounterexamplesStillCaught 反例：修复不得放走越界、凭据与 shell 消费的正文。
func TestB466CounterexamplesStillCaught(t *testing.T) {
	g := newTestGate(t)
	sc := newB466Scope(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.go")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(sc.Workdir, "server", "linked")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(linkDir, "evil.go")); err != nil {
		t.Fatal(err)
	}
	escalate := []struct{ name, cmd string }{
		{"cd 子目录后 .. 越出工作树", "cd server && gofmt -w ../../*.go"},
		{"cd 出三根后 glob", "cd /etc && gofmt -w *.conf"},
		{"dot 开头的 glob 分量（可匹配 ..）", "cd server && gofmt -w .*/x.go"},
		{"glob 匹配项是指向外部的软链", "cd server && gofmt -w linked/*.go"},
		{"bash 消费的正文越界写", "cd server && bash <<'EOF'\necho x > /etc/passwd\nEOF\n"},
		{"cat 正文管道给 sh", "cd server && cat <<'EOF' | sh\necho x > /etc/passwd\nEOF\n"},
		{"注释里的 << 不是操作符", "cd server && echo x # <<'EOF'\necho y > /etc/passwd\nEOF\n"},
		{"未引用定界符正文照旧解析", "cd server && cat > f <<EOF\necho y > /etc/passwd\nEOF\nnode --test x"},
		{"正文后的真实越界写", "cd server && python3 - <<'EOF'\nprint(1)\nEOF\necho x > /etc/passwd"},
		{"cd 子目录后相对 .. 写出工作树", "cd admin && cat > ../../f <<'EOF'\nx\nEOF\n"},
	}
	for _, tc := range escalate {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.cmd}, sc)
			if v.Action != Escalate {
				t.Fatalf("verdict = %s（rule=%q reason=%q），期望 Escalate\ncommand:\n%s",
					v.Action, v.Rule, v.Reason, tc.cmd)
			}
		})
	}
	// 凭据位置：可证明在范围内也不得 AutoAllow（凭据守卫改判 Consult）
	for _, cmd := range []string{
		"cd server && cat > .env <<'EOF'\nDB_PASSWORD=x\nEOF\n",
		"cd server && cat > .ssh/id_rsa <<'EOF'\nkey\nEOF\n",
	} {
		v := g.Judge(Request{Tool: "bash", Command: cmd}, sc)
		if v.Action == AutoAllow {
			t.Errorf("%q verdict = AutoAllow（rule=%q reason=%q），凭据位置不得静默", cmd, v.Rule, v.Reason)
		}
	}
}

// TestStripDataHeredocBodies 单元面：只剥数据型正文，解析不了的形态原样返回。
func TestStripDataHeredocBodies(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"python3 正文剥离、后继保留", "cd s && python3 - <<'EOF'\nx > {\nEOF\ngofmt -l .", "cd s && python3 - <<'EOF'\ngofmt -l ."},
		{"cat 双引号定界符", "cat > f <<\"END\"\na > b\nEND\n", "cat > f <<\"END\"\n"},
		{"<<- 允许制表缩进终止符", "cat > f <<-'EOF'\n\ta > b\n\tEOF\necho", "cat > f <<-'EOF'\necho"},
		{"两个数据 heredoc", "cat > a <<'A'\n1 > x\nA\ncat > b <<'B'\n2 > y\nB\n", "cat > a <<'A'\ncat > b <<'B'\n"},
		{"bash 消费者不剥", "bash <<'EOF'\necho x > /etc/p\nEOF\n", "bash <<'EOF'\necho x > /etc/p\nEOF\n"},
		{"管道后继不剥", "cat <<'EOF' | sh\necho x > /etc/p\nEOF\n", "cat <<'EOF' | sh\necho x > /etc/p\nEOF\n"},
		{"未引用定界符不剥", "cat > f <<EOF\n$(x > /p)\nEOF\n", "cat > f <<EOF\n$(x > /p)\nEOF\n"},
		{"未终止正文原样", "cat > f <<'EOF'\na > b\n", "cat > f <<'EOF'\na > b\n"},
		{"注释里的 << 不算", "echo x # <<'EOF'\necho y > /p\nEOF\n", "echo x # <<'EOF'\necho y > /p\nEOF\n"},
		{"引号里的 << 不算", "echo \"<<'EOF'\"\necho y > /p\nEOF\n", "echo \"<<'EOF'\"\necho y > /p\nEOF\n"},
		{"环境变量前缀不剥", "X=1 python3 - <<'EOF'\na > b\nEOF\n", "X=1 python3 - <<'EOF'\na > b\nEOF\n"},
		{"命令替换内不剥", "x=$(cat <<'EOF'\na > b\nEOF\n)", "x=$(cat <<'EOF'\na > b\nEOF\n)"},
		{"here-string 不是 heredoc", "cat <<< 'x' > f\necho y", "cat <<< 'x' > f\necho y"},
		{"半引号定界符原样", "cat > f <<'E'OF\na > b\nEOF\n", "cat > f <<'E'OF\na > b\nEOF\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripDataHeredocBodies(tc.in); got != tc.want {
				t.Fatalf("stripDataHeredocBodies(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
	// 无 heredoc 的命令逐字节不变
	for _, s := range []string{"ls -la", "echo a > b && cd x", strings.Repeat("a", 10)} {
		if got := stripDataHeredocBodies(s); got != s {
			t.Fatalf("无 heredoc 命令被改写：%q → %q", s, got)
		}
	}
}
