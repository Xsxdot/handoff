package permgate

// b460_fix2_test.go —— B460 修复轮 2（机内审查第 2 轮 F1–F6）。
//
//   - F1：判定「是不是 cd 段 / 命令词是什么」的分词与只读匹配分词不一致——前者保留引号，
//     后者剥引号，`cd $TMPDIR && 'cd' /etc && echo x > passwd` 被当成在 $TMPDIR 里写。
//   - F2：凭据守卫只认家目录及其下，`grep -r token /`、`/Users`、`$HOME/..`、cd 后的 `..` 漏网。
//   - F3：darwin 文件系统大小写不敏感，`~/.SSH/ID_RSA` 绕过。
//   - F4：软链检查对 `$TMPDIR/…`、cd 后的相对路径跳过。
//   - F5：`..` 被丢弃而不是化简；凭据清单与密钥变量名补全。
//   - F6：写落点是悬空软链时按「不存在」处理，写穿到范围外。
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func b460Judge(t *testing.T, g *Gate, sc Scope, cmd, cwd string) Verdict {
	t.Helper()
	return g.Judge(Request{Tool: executor.PermToolBash, Command: cmd, Cwd: cwd}, sc)
}

// TestB460F1QuotedCommandWordNeverAutoAllows：审查给定命令原样入表，一律不得 AutoAllow。
func TestB460F1QuotedCommandWordNeverAutoAllows(t *testing.T) {
	g := newTestGate(t)
	sc := b460Scope(t)
	art, tmp := sc.ArtifactsDir, sc.TaskTmpDir
	cases := []struct{ cmd, cwd string }{
		{"cd $TMPDIR && 'cd' /etc && echo x > passwd", ""},
		{"cd $TMPDIR && c''d /etc && echo x > passwd", ""},
		{"cd $TMPDIR && 'c'd /etc && echo x > passwd", ""},
		{`cd $TMPDIR && "cd" /etc && echo x > passwd`, ""},
		{`cd $TMPDIR && c\d /etc && echo x > passwd`, ""},
		{`cd $TMPDIR && \cd /etc && echo x > passwd`, ""},
		{"'cd' /etc && echo x > passwd", art},
		{"c''d /etc && printf x > passwd", art},
		{"cd $TMPDIR && 'cd' /etc && mkdir .ssh", ""},
		{"cd $TMPDIR && 'cd' /etc && printf x > passwd", ""},
		{"cd " + art + " && 'cd' - && echo x > .bashrc", ""},
		{"cd " + art + " && 'cd' .. && 'cd' .. && echo x > f", ""},
		{"'cd' .. && 'cd' .. && 'cd' .. && echo x > f", art},
		{"cd " + tmp + " && 'cd' /etc && cat > passwd <<'EOF'\nx\nEOF", ""},
		{"cd $TMPDIR && 'cd' /tmp && rm -rf x", ""},
		{"cd $TMPDIR && \"cd\" .. && rm -rf x", ""},
		// 命令词本身带引号/转义的写段与 rm 段（纵深防御：证明出口不认非朴素命令词）
		{"cd " + art + " && 'echo' x > r.md", ""},
		{"cd " + art + " && 'mkdir' sub", ""},
		{"cd $TMPDIR && 'rm' -rf x", ""},
		{"cd " + tmp + " && 'cat' > f <<'EOF'\nx\nEOF", ""},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if v := b460Judge(t, g, sc, tc.cmd, tc.cwd); v.Action == AutoAllow {
				t.Fatalf("%q（cwd=%q）不得 AutoAllow（rule=%q %s）", tc.cmd, tc.cwd, v.Rule, v.Reason)
			}
		})
	}
}

// TestB460F1CommandWordSharedNormalizer 钉住共用命令词归一：cd 判定与只读匹配同源。
func TestB460F1CommandWordSharedNormalizer(t *testing.T) {
	cases := []struct {
		seg   string
		word  string
		plain bool
	}{
		{"cd /a", "cd", true},
		{"  ls -la", "ls", true},
		{"'cd' /etc", "cd", false},
		{"c''d /etc", "cd", false},
		{"'c'd /etc", "cd", false},
		{`"cd" /etc`, "cd", false},
		{`c\d /etc`, "cd", false},
		{`\cd`, "cd", false},
		{"$C /etc", "$C", false},
		{"X=1 make", "X=1", false},
		{"go test ./...", "go", true},
	}
	for _, tc := range cases {
		w, plain, ok := commandWord(tc.seg)
		if !ok || w != tc.word || plain != tc.plain {
			t.Errorf("commandWord(%q) = %q,%v,%v；want %q,%v", tc.seg, w, plain, ok, tc.word, tc.plain)
		}
	}
	// 只读匹配不认非朴素命令词（与 cd 判定不可能分歧）
	for _, seg := range []string{"'cd' /etc", "c''d /etc", `"ls"`, `l\s`, "'cat' README.md"} {
		if id, ok := matchSilentSegment(seg); ok {
			t.Errorf("matchSilentSegment(%q) 不得静默（id=%s）", seg, id)
		}
	}
	// cd 跟踪认出引号形态的 cd（不再当成普通段）
	if !isCDSegment("'cd' /etc") || !isCDSegment(`c\d /etc`) {
		t.Error("isCDSegment 应认出引号/转义形态的 cd")
	}
	if cdChainTrusted([]string{"cd /a", "'cd' /b", "echo x > f"}, []string{"&&", "&&"}) {
		t.Error("非朴素 cd 让整条 cd 链不可信")
	}
}

// TestB460F2CredentialAncestors：家目录的祖先（含根）与 cd 后相对 `..` 一律 Consult。
func TestB460F2CredentialAncestors(t *testing.T) {
	home := b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	deep := filepath.Join(home, "w", "x", "y")
	cases := []struct{ cmd, cwd string }{
		{"grep -r token /", ""},
		{"grep -r token /Users", ""},
		{"grep -r token /Users/", ""},
		{"grep -r token /home", ""},
		{"grep -r token /root", ""},
		{"grep -r token /home/bob", ""},
		{"ls -laR /", ""},
		{"ls /", ""},
		{"grep -r token $HOME/..", ""},
		{"grep -r token " + filepath.Dir(home), ""},
		{"cd ~/a && grep -r token ..", ""},
		{"grep -r token ../../../..", deep},
		{"grep -r token ../..", filepath.Join(home, "w")},
		{"cd " + deep + " && grep -r token ../../..", ""},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			if v := b460Judge(t, g, sc, tc.cmd, tc.cwd); v.Action != Consult {
				t.Fatalf("%q（cwd=%q）应 Consult，得 %s rule=%q（%s）", tc.cmd, tc.cwd, v.Action, v.Rule, v.Reason)
			}
		})
	}
	// 家目录之下的非凭据目录维持现状
	for _, tc := range []struct{ cmd, cwd string }{
		{"grep -r token ..", deep},
		{"ls " + filepath.Join(home, "w"), ""},
	} {
		if v := b460Judge(t, g, sc, tc.cmd, tc.cwd); v.Action != AutoAllow {
			t.Errorf("%q（cwd=%q）不是凭据祖先，应维持 AutoAllow，得 %s（%s）", tc.cmd, tc.cwd, v.Action, v.Reason)
		}
	}
}

// TestB460F3CaseInsensitive：凭据位置按大小写不敏感比较。
func TestB460F3CaseInsensitive(t *testing.T) {
	b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	for _, cmd := range []string{
		"cat ~/.SSH/ID_RSA", "cat $HOME/.Codex/Auth.json", "cat ~/.Aws/credentials",
		"cat .ENV", "cat config/.Env.Local", "cat /PROC/self/ENVIRON", "cat ~/.Config/GH/hosts.yml",
	} {
		if v := b460Judge(t, g, sc, cmd, ""); v.Action != Consult {
			t.Errorf("%q 应 Consult，得 %s（%s）", cmd, v.Action, v.Reason)
		}
	}
}

// TestB460F4SymlinkViaVarsAndCD：软链判定前先展开白名单变量、按命令内 cd 基准解析相对路径。
func TestB460F4SymlinkViaVarsAndCD(t *testing.T) {
	home := b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(ssh, "id_ed25519")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, l := range []struct{ link, to string }{
		{filepath.Join(sc.TaskTmpDir, "k"), key},
		{filepath.Join(sc.TaskTmpDir, "d"), ssh},
		{filepath.Join(sc.ArtifactsDir, "k2"), key},
		{filepath.Join(sc.TaskTmpDir, "rel"), "../../../../" + filepath.Base(filepath.Dir(home)) + "/" + filepath.Base(home) + "/.ssh/id_ed25519"},
	} {
		if err := os.Symlink(l.to, l.link); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range []string{
		"cat $TMPDIR/k",
		`cat "$TMPDIR/k"`,
		"cat ${TMPDIR}/k",
		"cat $TMPDIR/d/id_ed25519",
		`cat "$HANDOFF_ARTIFACTS_DIR"/k2`,
		"cd $TMPDIR && cat k",
		"cd $TMPDIR && cat d/id_ed25519",
		"cd " + sc.TaskTmpDir + " && cat d/x",
		"cd $HANDOFF_ARTIFACTS_DIR && cat k2",
	} {
		if v := b460Judge(t, g, sc, cmd, ""); v.Action != Consult {
			t.Errorf("%q 经软链读凭据，应 Consult，得 %s（%s）", cmd, v.Action, v.Reason)
		}
	}
	if v := b460Judge(t, g, sc, "cat k", sc.TaskTmpDir); v.Action != Consult {
		t.Errorf("cwd 下相对软链应 Consult，得 %s", v.Action)
	}
}

// TestB460F5DotDotAndList：`..` 按 Clean 语义化简；清单与变量名补全项。
func TestB460F5DotDotAndList(t *testing.T) {
	b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	for _, cmd := range []string{
		"cat /proc/self/../self/environ",
		"cat /proc/1/../self/environ",
		"cat ~/x/../.ssh/id_rsa",
		"cat ~/.npmrc", "cat ~/.pypirc", "cat ~/.config/gcloud/credentials.db",
		"cat ~/.cargo/credentials", "cat ~/.cargo/credentials.toml", "cat ~/.gnupg/secring.gpg",
		"cat ~/.kube/cache/discovery", "cat ~/.bash_history", "cat ~/.zsh_history", "cat ~/.history",
		"cat ~/.local/share/fish/fish_history",
		"echo $GH_PAT", "echo ${PAT}", "echo $GITLAB_PAT_RO", "echo $SENTRY_DSN", "echo $DSN",
		"echo $DATABASE_URL", "echo $PG_DATABASE_URL",
	} {
		if v := b460Judge(t, g, sc, cmd, ""); v.Action != Consult {
			t.Errorf("%q 应 Consult，得 %s（%s）", cmd, v.Action, v.Reason)
		}
	}
	for _, cmd := range []string{
		"cat ~/.ssh/../notes.md", "echo $PATH", "echo $PATTERN", "cat ~/.cargo/config.toml",
		"cat ~/.config/gcloudx", "cat ~/.bash_profile",
	} {
		if v := b460Judge(t, g, sc, cmd, ""); v.Action != AutoAllow {
			t.Errorf("%q 不是凭据，应维持 AutoAllow，得 %s（%s）", cmd, v.Action, v.Reason)
		}
	}
}

// TestB460F6DanglingSymlinkWrites：写落点（含 Write 工具）是悬空软链时按软链目标判范围。
func TestB460F6DanglingSymlinkWrites(t *testing.T) {
	g := newTestGate(t)
	sc := b460Scope(t)
	outside := t.TempDir()
	tmp, art := sc.TaskTmpDir, sc.ArtifactsDir
	mk := func(link, to string) {
		t.Helper()
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(tmp, "dl"), filepath.Join(outside, "new"))
	mk(filepath.Join(art, "dl"), filepath.Join(outside, "new2"))
	mk(filepath.Join(tmp, "dd"), filepath.Join(outside, "nodir")) // 悬空目录软链
	mk(filepath.Join(tmp, "loop"), filepath.Join(tmp, "loop"))
	mk(filepath.Join(tmp, "ok"), filepath.Join(tmp, "real-not-yet")) // 悬空但目标在范围内
	for _, tc := range []struct{ cmd, cwd string }{
		{"printf x > $TMPDIR/dl", ""},
		{"echo x > " + tmp + "/dl", ""},
		{"cd $TMPDIR && echo x > dl", ""},
		{"cd " + art + " && printf x > dl", ""},
		{"printf x > dl", art},
		{"mkdir -p $TMPDIR/dd/sub", ""},
		{"cat > " + tmp + "/dl <<'EOF'\nx\nEOF", ""},
		{"cd $TMPDIR && cat > dd/f <<'EOF'\nx\nEOF", ""},
		{"printf x > $TMPDIR/loop", ""},
	} {
		if v := b460Judge(t, g, sc, tc.cmd, tc.cwd); v.Action == AutoAllow {
			t.Errorf("%q 写穿悬空软链，不得 AutoAllow（rule=%q %s）", tc.cmd, v.Rule, v.Reason)
		}
	}
	for _, p := range []string{filepath.Join(tmp, "dl"), filepath.Join(art, "dl"), filepath.Join(tmp, "dd", "f"), filepath.Join(tmp, "loop")} {
		v := g.Judge(Request{Tool: executor.PermToolWrite, Text: "Write: x", Paths: []string{p}}, sc)
		if v.Action == AutoAllow {
			t.Errorf("Write 工具写悬空软链 %s 不得 AutoAllow（%s）", p, v.Reason)
		}
	}
	if v := b460Judge(t, g, sc, "printf x > $TMPDIR/ok", ""); v.Action != AutoAllow {
		t.Errorf("悬空但目标在范围内的软链仍可证明，得 %s（%s）", v.Action, v.Reason)
	}
}

// TestB460F1EachLayerPinned 单独钉住 F1 的每一层（变异时任一层关掉都要有测试变红）：
// 落点循环认引号形态的 cd；三处证明出口各自拒绝非朴素命令词（双引号包住的 mkdir/echo/rm/cat
// 在 rmTokens 里会被剥成普通词，只有命令词朴素性检查能拦）。
func TestB460F1EachLayerPinned(t *testing.T) {
	sc := b460Scope(t)
	art, tmp := sc.ArtifactsDir, sc.TaskTmpDir
	if _, unres := cdResolvedTargets("cd $TMPDIR && 'cd' /etc && echo x > passwd", sc, ""); unres == "" {
		t.Error("落点循环应把 'cd' /etc 当 cd（基准破坏），其后相对落点不可解释")
	}
	if _, broken := cdWalkBase("cd $TMPDIR && c''d /etc", sc); !broken {
		t.Error("cdWalkBase 应把 c''d /etc 当 cd 且判基准破坏")
	}
	for _, cmd := range []string{
		"cd " + art + ` && "mkdir" sub`,
		"cd " + art + ` && "echo" x > r.md`,
		`"printf" x > ` + art + "/r.md",
	} {
		if id, ok := scopedWriteID(cmd, "", sc); ok {
			t.Errorf("scopedWriteID(%q) 应拒非朴素命令词，得 %s", cmd, id)
		}
	}
	for _, cmd := range []string{`"rm" -rf $TMPDIR/x`, `cd $TMPDIR && "rm" -rf x`} {
		if _, ok := rmInScopeVerdict(cmd, sc); ok {
			t.Errorf("rmInScopeVerdict(%q) 应拒非朴素命令词", cmd)
		}
	}
	if hd := closedHeredoc(`"cat" > ` + tmp + "/f <<'EOF'\nx\nEOF"); hd != nil {
		t.Error(`closedHeredoc 应拒 "cat" 命令词`)
	}
	if hd := closedHeredoc("cd " + tmp + ` && "cd" sub && cat > f <<'EOF'` + "\nx\nEOF"); hd != nil {
		t.Error(`closedHeredoc 应拒 "cd" 命令词`)
	}
}
