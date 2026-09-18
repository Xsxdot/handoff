package permgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJudgeSafeCommandTable exercises the whitelist only through Gate.Judge;
// matcher internals are not a substitute for the range-before-command seam.
func TestJudgeSafeCommandTable(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir(), TaskTmpDir: filepath.Join(work, "tmp")}
	cases := []struct {
		id      string
		command string
	}{
		{"git-ledger-amend", "git add docs/ledger.md && git commit --amend --no-edit"},
		{"go-build", "go build ./..."},
		{"go-test", "go test ./..."},
		{"go-vet", "go vet ./..."},
		{"gofmt", "gofmt -w internal/a.go"},
		{"npm-test", "npm test -- --runInBand"},
		{"npm-run", "npm run lint -- --quiet"},
		{"make", "make test"},
		{"ls", "ls -la"},
		{"cat", "cat docs/spec.md"},
		{"grep", "grep -R pattern docs"},
		{"git-status", "git status --short"},
		{"git-diff", "git diff --stat"},
		{"git-log", "git log -5"},
		{"git-grep", `git grep -i "agy" internal/`},
		{"git-show", "git show --stat HEAD"},
		{"git-blame", "git blame README.md"},
		{"git-cat-file", "git cat-file -p HEAD"},
		{"git-rev-parse", "git rev-parse HEAD"},
		{"git-ls-files", "git ls-files internal/"},
		{"which", "which codegraph"},
		{"pwd", "pwd"},
		{"head", "head -n 20 README.md"},
		{"tail", "tail -n 5 go.mod"},
		{"wc", "wc -l README.md"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.command}, sc)
			if v.Action != AutoAllow || v.Rule != RuleSafeCommand || !strings.Contains(v.Reason, tc.id) {
				t.Fatalf("command %q verdict = %#v, want AutoAllow safe-command %s", tc.command, v, tc.id)
			}
		})
	}
}

// TestJudgeCompoundSilentTable 钉住 B376 第 1 条：复合只读拆段后整条静默。
//
// 入口仍是 Gate.Judge——拆段只服务静默判定，不是新的判据权威。每段都要能
// 单独证明只读才整条 AutoAllow；半段放行被产品明禁。
func TestJudgeCompoundSilentTable(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir(), TaskTmpDir: filepath.Join(work, "tmp")}
	cases := []struct {
		name    string
		command string
	}{
		{"与连接的两段只读", "grep a && grep b"},
		{"cd 段带出工作目录", "cd src && rg foo"},
		{"三段只读 git", "git status && git branch --show-current && git rev-parse HEAD"},
		{"管道只读", "ls | head"},
		{"sed -n 只读", "sed -n '1,20p' file"},
		{"分号串 sed 与 echo", `sed -n '219,300p' f; echo "=== cursor"`},
		{"codegraph 读图补全", "codegraph sym Gate.Judge"},
		{"npm --prefix", "npm --prefix web test"},
		{"printf 无重定向", `printf '%s\n' hello`},
		// B376 复审：守卫只拦执行型/改写型标志，只读形态仍须静默。
		{"rg 普通检索", `rg -n "pattern" internal/`},
		{"rg --pre-glob 非执行", `rg --pre-glob '*.go' foo`},
		{"git branch 只读列表", "git branch -a"},
		{"git branch --list", "git branch --list 'feat/*'"},
		{"sed -n 带 --quiet", "sed --quiet '1,20p' file"},
		// B376 复审：只读 sed 形态（替换打印、正则地址）仍须静默；守卫只拦写/执行。
		{"sed -n 替换只读", `sed -n 's/foo/bar/p' f`},
		{"sed -n 正则地址只读", `sed -n '/error/p' f`},
		// B376 复审 3：地址前缀闭族（数字范围、GNU 步长、! 取反、正则范围）
		// 本身是只读形态，真正的写/执行在他们之后的命令字母；未闭合地址
		// 解析不得把只读命令误判成写。
		{"sed -n 数字范围只读", `sed -n '1,5p' f`},
		{"sed -n 正则范围只读", `sed -n '/a/,/b/p' f`},
		{"sed -n 取反地址只读", `sed -n '2!p' f`},
		{"sed -n GNU 步长地址只读", `sed -n '1~2p' f`},
		// B376 复审 minor：codegraph 的 --repo 是全局旗标，其后子命令仍走闭集。
		{"codegraph --repo 读图", "codegraph --repo . sym Gate.Judge"},
		{"codegraph --view 读图", "codegraph --view cards-B376 sym Gate.Judge"},
		{"codegraph help", "codegraph --help"},
		{"sed 正则含 w 只读", `sed -n '/write/p' f`},
		{"git branch 远程/详情只读", "git branch -r"},
		{"git branch -v 只读", "git branch -v"},
		{"单段 go test 回归", "go test ./..."},
		{"单段 grep 回归", "grep -R x docs"},
		{"单段 git status 回归", "git status --short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Command: tc.command}, sc)
			if v.Action != AutoAllow || v.Rule != RuleSafeCommand {
				t.Fatalf("command %q verdict = %#v, want AutoAllow safe-command", tc.command, v)
			}
		})
	}
}

// TestJudgeCompoundNotSilent 钉住 B376 的反例面：只读段不能把写段带过门，
// 未知段一律整条 Consult，不做半段放行。
func TestJudgeCompoundNotSilent(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir(), TaskTmpDir: t.TempDir()}
	for _, command := range []string{
		"grep a && rm x",
		"git branch -d topic",
		"sed -i 's/a/b/' f",
		// B376 复审：sed 的 -i 守卫必须覆盖带后缀形态，否则可静默改写文件。
		"sed -n -i.bak 's/a/b/' f",
		"sed -n -i'.bak' 's/a/b/' f",
		"sed -n --in-place=.bak 's/a/b/' f",
		"sed -n -e 's/a/b/' -i.bak f",
		"sed -ni.bak 's/a/b/' f",
		// B376 复审 2：sed 的 w（写文件）与 e（执行命令）命令是静默面外的写/执行
		// 面，独立成 `-e`/脚本或作为 `s///w file` 标志出现；`-i` 守卫拦不住它们。
		`sed -n 'w /tmp/sedout' f`,
		`sed -n 'e echo pwned' f`,
		`sed -n 's/a/b/w /tmp/sedout' f`,
		`sed -n -e 'w /tmp/sedout' f`,
		`sed -ne 'w /tmp/sedout' f`,
		`sed -n --expression='w /tmp/sedout' f`,
		`sed -n '1,5w /tmp/sedout' f`,
		`sed -n 'p; w /tmp/sedout' f`,
		`sed -n '/re/p;w /tmp/sedout' f`,
		`sed -n 'p; e echo pwned' f`,
		// B376 复审 3：地址前缀（数字范围、GNU ~ 步长、! 取反、正则范围）后的
		// w/W/e 仍是写/执行面；s///e 的 e 标志执行替换结果。地址剥离或 flags
		// 解析不认这些形态就会被静默放行。
		`sed -n '1~2w /tmp/out' f`,
		`sed -n '/a/,/b/w /tmp/out' f`,
		`sed -n '2,4!w /tmp/out' f`,
		`sed -n '1~2e touch /tmp/x' f`,
		`sed -n '/a/,/b/e touch /tmp/x' f`,
		`sed -n '2,4!W /tmp/out' f`,
		`sed -n 's/a/x/e' f`,
		`sed -n 's/a/x/we /tmp/out' f`,
		// B376 复审 3：地址族还有更隐蔽的成员——正则地址的 GNU 标志 I/M、
		// 相对行数端点 +N、相对步长端点 ~N；s/// 的 pattern/replacement 正文里
		// 可以含分号，先按 `;` 切分会把执行标志吃掉。
		`sed -n '/a/Iw /tmp/out' f`,
		`sed -n '/a/Mw /tmp/out' f`,
		`sed -n '/a/,+2w /tmp/out' f`,
		`sed -n '/a/,~2w /tmp/out' f`,
		`sed -n '1,~3w /tmp/out' f`,
		`sed -n 's/a;b/c/e' f`,
		`sed -n 's/a;b/c/w /tmp/out' f`,
		`printf 'w /tmp/sedout\n' | sed -n -f /dev/stdin f`,
		// B376 复审 4：位置脚本在前、-e/--expression/-f 写/执行在后。
		// 旧扫描遇首个位置脚本即 return，其后的显式脚本源根本不被看见；
		// 真机 GNU sed 里 `-e` 仍会执行（位置参数此时按输入文件读，报错也拦不住
		// -e 的 w/e）。任何脚本源含写/执行都必须整段否决。
		`sed -n 'p' -e 'w /tmp/out' f`,
		`sed -n 'p' -e 'e touch /tmp/x' f`,
		`sed -n 'p' --expression='w /tmp/out' f`,
		`sed -n 'p' --expression='e touch /tmp/x' f`,
		`sed -n 'p' -f /tmp/script f`,
		`sed -n 'p' --file /tmp/script f`,
		`sed -n 'p' --file=/tmp/script f`,
		`sed -n 'p' -e 'p' -e 'w /tmp/out' f`,
		`sed -n 'p' -e 's/a/b/w /tmp/out' f`,
		`sed -n 'p; q' -e 'w /tmp/out' f`,
		`sed -n 'p' -ne 'w /tmp/out' f`,
		`sed -n 'p' -e 'w /tmp/out' --expression='e touch /tmp/x' f`,
		// B376 复审：rg 新入白名单必须约束执行型标志，--pre 会执行任意程序。
		`rg --pre 'rm -rf x' foo`,
		"rg --pre=rm foo",
		`rg --pre 'sh -c x' pattern .`,
		// B376 复审：cd 段改变后续段的实际工作目录，而写落点按 Workdir 解析，
		// 相对写落点会被误判成范围内。cd 之后的参数位写段一律不静默。
		"cd /etc && gofmt -w passwd",
		"cd /tmp && git diff --output=x HEAD",
		"cd src && gofmt -w x.go",
		// B376 复审 3：相对路径 w 落点不得因地址前缀绕过 cd 守卫。
		`cd /etc && sed -n '1~2w passwd' f`,
		`cd /etc && sed -n '/a/,/b/w passwd' f`,
		// B376 复审 2：git branch 的强制改名/上游/描述写入形态都改 ref 或 .git/config。
		"git branch -f main",
		"git branch -u origin/main",
		"git branch --set-upstream-to=origin/main",
		"git branch --edit-description",
		"git branch -M newname",
		"git branch -C copy",
		"ls | tee out",
		"echo x > file",
		"go test ./... | tee /tmp/out",
		`bash -c "go test ./..."`,
		"go run ./...",
		"git log -S x --oneline || true",
		"codegraph absorb",
		"npm --prefix web ci",
	} {
		v := g.Judge(Request{Tool: "bash", Command: command}, sc)
		if v.Action == AutoAllow && v.Rule == RuleSafeCommand {
			t.Fatalf("compound non-readonly %q was auto-allowed: %#v", command, v)
		}
	}
}

// TestJudgeCommandSubstitutionNotSilent 钉住命令替换不得静默：`echo "$(cmd)"`
// 的词元匹配只看得到 echo，但 shell 在引号内照样执行替换。判据是「静态可
// 证明」，内容不可见就不在静默面——这是不要用 echo 白名单开洞的守卫。
func TestJudgeCommandSubstitutionNotSilent(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir(), TaskTmpDir: t.TempDir()}
	for _, command := range []string{
		`echo "$(rm -rf x)"`,
		`echo "$(cat /etc/passwd)"`,
		`printf '%s' "$(whoami)"`,
		"grep \"$(cat /etc/passwd)\" f",
	} {
		v := g.Judge(Request{Tool: "bash", Command: command}, sc)
		if v.Action == AutoAllow && v.Rule == RuleSafeCommand {
			t.Fatalf("命令替换 %q 不得静默放行: %#v", command, v)
		}
	}
}

// TestJudgeEchoMimicIsEchoNotGoTest 反锁子串误判：echo 的正文含 "go test"
// 时只能命中 echo，不得被当成 go-test 命中。
func TestJudgeEchoMimicIsEchoNotGoTest(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir(), TaskTmpDir: t.TempDir()}
	v := g.Judge(Request{Tool: "bash", Command: `echo "go test ./..."`}, sc)
	if v.Action != AutoAllow || v.Rule != RuleSafeCommand {
		t.Fatalf(`echo "go test ./..." 应按 echo 静默，实得 %#v`, v)
	}
	if !strings.Contains(v.Reason, "echo") || strings.Contains(v.Reason, "go-test") {
		t.Fatalf("reason = %q，必须含 echo 且不得含 go-test", v.Reason)
	}
}

// TestJudgeSafeCommandRejectsMimicsAndConnectors keeps shell wrappers and
// untrusted command joins outside the positive whitelist.
//
// B376 起，纯只读的 `;` / `&&` 连接串改走 TestJudgeCompoundSilentTable 正例，
// 本表只保留真正的包装器、管道进写工具、换行与写落点形态。
func TestJudgeSafeCommandRejectsMimicsAndConnectors(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir(), TaskTmpDir: t.TempDir()}
	for _, command := range []string{
		`bash -c "go test ./..."`,
		"go test ./... | tee /tmp/out",
		"go test ./...\ncat file",
		"git log -S HANDOFF_SESSION_CLI --oneline || true",
		"git show --output=/tmp/x HEAD",
		"go run ./...",
		"handoff graph dispatch --doc x",
		"handoff graph unknown --doc x",
	} {
		v := g.Judge(Request{Tool: "bash", Command: command}, sc)
		if v.Action == AutoAllow && v.Rule == RuleSafeCommand {
			t.Fatalf("unsafe mimic/connector %q was auto-allowed: %#v", command, v)
		}
	}
	if v := g.Judge(Request{Tool: "webfetch", Text: "go test ./..."}, sc); v.Action == AutoAllow && v.Rule == RuleSafeCommand {
		t.Fatalf("non-bash command was auto-allowed: %#v", v)
	}
}

func TestJudgeUnknownGraphSubcommandFailsClosed(t *testing.T) {
	g := newTestGate(t)
	for _, sub := range []string{"resolve", "inspect"} {
		v := g.Judge(Request{Tool: "bash", Command: "handoff graph " + sub + " --doc docs/spec.md"}, Scope{Workdir: t.TempDir()})
		if v.Action != Escalate || v.Rule != RuleSelfCommand {
			t.Fatalf("removed graph subcommand %s verdict = %#v, want Escalate/self-command", sub, v)
		}
	}
}

// TestJudgeFailClosedTable 把 spec §7 的 fail-closed 表逐行钉死。
//
// 表里没有任何一行导向 AutoAllow——这是整个设计的支点，一旦有人加了新的
// 提前返回并误落到 AutoAllow，本用例必须红。
func TestJudgeFailClosedTable(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: t.TempDir()}
	cases := []struct {
		name string
		req  Request
	}{
		{"描述含截断标记", Request{Tool: "bash", Text: "Bash: x", Command: "x", Truncated: true}},
		{"写文件但无路径", Request{Tool: "write", Text: "Write: ?"}},
		{"路径越界", Request{Tool: "write", Text: "Write: /etc/hosts", Paths: []string{"/etc/hosts"}}},
		{"多路径任一越界", Request{Tool: "edit", Text: "Edit: x",
			Paths: []string{filepath.Join(work, "a.go"), "/etc/hosts"}}},
		{"写文件描述命中黑名单", Request{Tool: "write", Text: "Write: /x/sudoers-sudo",
			Paths: []string{filepath.Join(work, "a.go")}}},
		{"剥离后仍命中", Request{Tool: "bash", Text: "Bash: rm -rf /", Command: "rm -rf /"}},
		{"含执行包装器", Request{Tool: "bash", Text: `Bash: sh -c "rm -rf /"`, Command: `sh -c "rm -rf /"`}},
	}
	for _, c := range cases {
		if v := g.Judge(c.req, sc); v.Action != Escalate {
			t.Errorf("%s：必须 Escalate，实得 %s（%s）", c.name, v.Action, v.Reason)
		}
	}
}

// TestJudgeAutoAllowOnlyForInScopeWrites AutoAllow 的唯一合法来源。
func TestJudgeAutoAllowOnlyForInScopeWrites(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	task := t.TempDir()
	sc := Scope{Workdir: work, TaskDir: task}
	ok := []Request{
		{Tool: "write", Text: "Write: main.go", Paths: []string{"main.go"}},
		{Tool: "edit", Text: "Edit: " + filepath.Join(work, "a.go"),
			Paths: []string{filepath.Join(work, "a.go")}},
		{Tool: "write", Text: "Write: notes.md", Paths: []string{filepath.Join(task, "notes.md")}},
	}
	for _, r := range ok {
		if v := g.Judge(r, sc); v.Action != AutoAllow {
			t.Errorf("范围内写入应自动放行：%v，实得 %s（%s）", r.Paths, v.Action, v.Reason)
		}
	}
}

// TestJudgeNonBashToolsRemainNonAutoAllowed keeps the whitelist scoped to Bash.
func TestJudgeNonBashToolsRemainNonAutoAllowed(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir()}
	reqs := []Request{
		{Tool: "bash", Text: "Bash: unknown-command", Command: "unknown-command"},
		{Tool: "webfetch", Text: "WebFetch: https://example.com"},
		{Tool: "other", Text: "SomeTool: whatever"},
	}
	for _, r := range reqs {
		if v := g.Judge(r, sc); v.Action == AutoAllow {
			t.Errorf("非写文件工具不得自动放行：%s", r.Text)
		}
	}
}

// TestJudgeNormalizationFailureEscalates 路径归一化失败走 fail-closed。
//
// 用一个 NUL 字节构造必然失败的路径——EvalSymlinks/Abs 都无法处理。
func TestJudgeNormalizationFailureEscalates(t *testing.T) {
	g := newTestGate(t)
	work := t.TempDir()
	// 目标路径落在一个「父目录是普通文件」的位置：EvalSymlinks 解不动，
	// resolveExistingPrefix 会退回原路径，最终仍应判越界或归一化失败——
	// 两者都是 Escalate。
	f := filepath.Join(work, "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	v := g.Judge(Request{Tool: "write", Text: "Write: x",
		Paths: []string{filepath.Join(f, "..", "..", "outside.go")}}, Scope{Workdir: work})
	if v.Action == AutoAllow {
		t.Fatalf("越出 Workdir 的路径不得自动放行，实得 %s（%s）", v.Action, v.Reason)
	}
}

// TestJudgeBashPathsEscalate 钉住 B134 主修：bash 请求的落点越界必须升级人工，
// 而不是落到 Consult 交廉价模型。落点有两个来源，都要覆盖。
func TestJudgeBashPathsEscalate(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []struct {
		name string
		req  Request
	}{
		{"executor 检出的越界目录", Request{
			Tool: "bash", Text: "external_directory: ls /etc", Command: "ls /etc",
			Paths: []string{"/etc"}}},
		{"handoff 自己摘的重定向落点", Request{
			Tool: "bash", Text: "Bash: echo x > /etc/hosts", Command: "echo x > /etc/hosts"}},
		{"追加写到家目录", Request{
			Tool: "bash", Text: "Bash: echo x >> ~/.zshrc", Command: "echo x >> ~/.zshrc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := g.Judge(c.req, scope)
			if v.Action != Escalate {
				t.Fatalf("Action = %v，期望 Escalate（reason=%q）", v.Action, v.Reason)
			}
			if !strings.Contains(v.Reason, "目标路径越出任务范围") {
				t.Fatalf("Reason = %q，必须逐字复用 judgeFileWrite 的越界文案", v.Reason)
			}
		})
	}
}

// TestJudgeBashInScopeFallsBack 落点全部在范围内时必须落回命令判据，
// 而不是因为「有落点」就升级——否则每次往工作区里写日志都叫人。
func TestJudgeBashInScopeFallsBack(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []struct {
		req  Request
		want Action
	}{
		{Request{Tool: "bash", Text: "Bash: echo x > out.txt", Command: "echo x > out.txt"}, Consult},
		{Request{Tool: "bash", Text: "Bash: go test ./... > " + wd + "/log", Command: "go test ./... > " + wd + "/log"}, AutoAllow},
		{Request{Tool: "bash", Text: "Bash: go test ./... > /dev/null", Command: "go test ./... > /dev/null"}, AutoAllow},
		{Request{Tool: "bash", Text: "Bash: go test ./... 2>&1", Command: "go test ./... 2>&1"}, AutoAllow},
	}
	for _, tc := range cases {
		t.Run(tc.req.Command, func(t *testing.T) {
			if v := g.Judge(tc.req, scope); v.Action != tc.want {
				t.Fatalf("Action = %v（reason=%q），期望 %v——落点合法后再按白名单判定",
					v.Action, v.Reason, tc.want)
			}
		})
	}
}

// TestJudgeBashNoPathsUnchanged keeps non-whitelisted and blacklisted commands
// unchanged while allowing the explicit build whitelist.
func TestJudgeBashNoPathsUnchanged(t *testing.T) {
	g := newTestGate(t)
	scope := Scope{Workdir: t.TempDir()}
	if v := g.Judge(Request{Tool: "bash", Text: "Bash: go build ./...", Command: "go build ./..."}, scope); v.Action != AutoAllow || v.Rule != RuleSafeCommand {
		t.Fatalf("白名单命令 Action/Rule = %v/%q，期望 AutoAllow/safe-command", v.Action, v.Rule)
	}
	if v := g.Judge(Request{Tool: "bash", Text: "Bash: rm -rf /", Command: "rm -rf /"}, scope); v.Action != Escalate {
		t.Fatalf("黑名单命令 Action = %v，期望 Escalate", v.Action)
	}
}

// TestJudgeBashWriteArgEscalate 钉住 B151 主修：落点在参数位的写命令越界，
// 必须升级人工，而不是落 Consult 交廉价模型。
//
// 真机基线（2026-08-18，claude 任务 b0327cd8）：这两条当时都判成
// `交审批者 黑名单未命中`——本用例就是那条基线的反面。
func TestJudgeBashWriteArgEscalate(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []struct {
		name string
		cmd  string
	}{
		{"管道后的 tee 写到 /tmp", "echo x | tee /tmp/b151-probe.txt"},
		{"tee 追加到家目录", "echo x | tee -a ~/.zshrc"},
		{"cp 到仓库外", "cp go.mod /tmp/b151-cp.txt"},
		{"mv 到仓库外", "mv go.mod /etc/go.mod"},
		{"dd 写到仓库外", "dd if=/dev/zero of=/tmp/b151-dd.bin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Text: "Bash: " + c.cmd, Command: c.cmd}, scope)
			if v.Action != Escalate {
				t.Fatalf("Action = %v，期望 Escalate（reason=%q）", v.Action, v.Reason)
			}
			if !strings.Contains(v.Reason, "目标路径越出任务范围") {
				t.Fatalf("Reason = %q，必须逐字复用越界文案", v.Reason)
			}
		})
	}
}

// TestJudgeBashWriteArgInScopeFallsBack 误伤面：落点在工作区内、或压根不是写命令时，
// 必须落回命令判据。少了这条，每次 `go test | tee out.log` 都要叫人。
func TestJudgeBashWriteArgInScopeFallsBack(t *testing.T) {
	wd := t.TempDir()
	scope := Scope{Workdir: wd}
	g := newTestGate(t)
	cases := []string{
		"go test ./... | tee out.log",
		"cp a.txt b.txt",
		"cp go.mod " + wd + "/copy.mod",
		"echo x | tee /dev/null",
		"ls /usr/bin/tee",
		`git commit -m "cp a /etc/x"`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			v := g.Judge(Request{Tool: "bash", Text: "Bash: " + cmd, Command: cmd}, scope)
			if v.Action == Escalate {
				t.Fatalf("不该升级：Action = %v，reason = %q", v.Action, v.Reason)
			}
		})
	}
}
