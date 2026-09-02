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

// TestJudgeSafeCommandRejectsMimicsAndConnectors keeps shell wrappers and
// untrusted command joins outside the positive whitelist.
func TestJudgeSafeCommandRejectsMimicsAndConnectors(t *testing.T) {
	g := newTestGate(t)
	sc := Scope{Workdir: t.TempDir(), TaskDir: t.TempDir(), TaskTmpDir: t.TempDir()}
	for _, command := range []string{
		`echo "go test ./..."`,
		`bash -c "go test ./..."`,
		"go test ./... | tee /tmp/out",
		"go test ./...; cat file",
		"go test ./...\ncat file",
		"git status && git log",
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
