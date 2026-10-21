package permgate

// b460_credential_test.go —— B460 S10（spec r1.3，用户裁决「收紧」）：规则层本会自动放行的命令
// （safe-command / 复合只读 / 范围内写入 / heredoc / rm 出口）一旦触及凭据位置或密钥类环境变量，
// 改交审批链（Consult）——不自动放行，也不硬拒。网页抓取路由不变。
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// b460CredHome 把 HOME 指到临时目录（覆盖「绝对路径落在家目录下」的形态），返回该目录。
func b460CredHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestB460CredentialReadsConsult(t *testing.T) {
	home := b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	// 软链例：任务临时目录里一个指向家目录凭据的软链
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.WriteFile(key, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sc.TaskTmpDir, "k")
	if err := os.Symlink(key, link); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		// 路径族（~ 形态）
		"cat ~/.ssh/id_rsa",
		"ls ~/.ssh",
		"cat ~/.ssh/*",
		"cat ~/.aws/credentials",
		"cat ~/.config/gh/hosts.yml",
		"cat ~/.netrc",
		"cat ~/.git-credentials",
		"cat ~/.docker/config.json",
		"cat ~/.kube/config",
		"cat ~/.codex/auth.json",
		"cat ~/.local/share/opencode/auth.json",
		"cat $XDG_DATA_HOME/opencode/auth.json",
		"cat ~/.grok/auth.json",
		"cat ~/.claude/.credentials.json",
		"cat ~/.claude.json",
		"cat ~/.handoff/config.yaml",
		"cat ~/.handoff/env/prod",
		"cat ~/.claude/infra/credentials.md",
		"cat ~/.gemini/antigravity-cli/antigravity-oauth-token",
		"cat ~/.gemini/oauth_creds.json",
		// $HOME / 引号 / 绝对路径 / 转义 / glob / 花括号 / 重定向 / 赋值形态
		"cat $HOME/.ssh/id_rsa",
		`cat "$HOME"/.aws/config`,
		"cat ${HOME}/.netrc",
		"cat " + home + "/.ssh/id_rsa",
		`cat ~/.s\sh/id_rsa`,
		"cat ~/.ss*/id_rsa",
		"cat ~/.{ssh,aws}/id_rsa",
		"cat ~/.s$X/id_rsa",
		"cat $HOME/$D/credentials",
		"head -n1 < ~/.netrc",
		"grep --file=~/.ssh/id_rsa x README.md",
		// 祖先目录（递归读会读到凭据）与回家目录的 cd
		"grep -r token ~",
		"grep -r token ~/.config",
		"cd ~ && cat .ssh/id_rsa",
		"cd && cat .ssh/id_rsa",
		"cd -- && grep -r x .",
		// 相对路径（cwd 可能是家目录）与任务目录内的凭据副本、软链
		"cat .ssh/id_rsa",
		"cat ../../.aws/credentials",
		"cat " + sc.TaskDir + "/grokhome/auth.json",
		"cat " + sc.TaskDir + "/agyhome/.gemini/antigravity-cli/antigravity-oauth-token",
		"cat " + link,
		// .env
		"cat .env",
		"cat .env.local",
		"cat config/.env",
		"cat " + sc.Workdir + "/.env.production",
		// /proc 环境
		"cat /proc/self/environ",
		// 复合只读、范围内写入出口
		"ls && cat ~/.aws/credentials",
		"cat ~/.ssh/id_rsa | head -1",
		"cd $TMPDIR && cat ~/.ssh/id_rsa && echo x > f",
		"cat ~/.codex/auth.json > $TMPDIR/a.txt",
		// 密钥类环境变量
		"echo $GITHUB_TOKEN",
		`echo "${OPENAI_API_KEY}"`,
		"echo $DB_PASSWORD",
		"echo $MYSQL_PASSWD",
		"echo $AWS_SECRET_ACCESS_KEY",
		"printf '%s' $GOOGLE_CREDENTIALS",
		"echo $X_AUTH",
		"env",
		"env | grep A",
		"printenv",
		"printenv PATH",
		"set",
		"export",
		"export -p",
		"declare -p",
		"declare -x",
		"typeset -p",
		"ls && printenv",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			v := g.Judge(Request{Tool: executor.PermToolBash, Command: cmd}, sc)
			if v.Action != Consult {
				t.Fatalf("%q 触及凭据应 Consult，得 %s rule=%q（%s）", cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}

// TestB460CredentialLookalikesUnchanged：形似但不是凭据位置的命令维持现状（AutoAllow）。
func TestB460CredentialLookalikesUnchanged(t *testing.T) {
	b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	for _, cmd := range []string{
		"cat README.md",
		"cat ~/.sshrc-notes",
		"cat ~/.ssh-notes/x",
		"ls ~/workspace",
		"cat ~/.claude/CLAUDE.md",
		"cat ~/.config/opencode/AGENTS.md",
		"cat .envrc",
		"cat env.go",
		"cat docs/environment.md",
		"grep -r token .",
		"wc -l */*.go",
		"echo $HOME/.ssh/id_rsa",
		"echo $PATH",
		"echo $TMPDIR",
		"git status",
		"cat $TMPDIR/x",
		"ls -la",
		"cd " + sc.ArtifactsDir + " && printf x > r.md",
		`printf x > "$HANDOFF_ARTIFACTS_DIR/r.md"`,
		"rm -rf $TMPDIR/x",
		"cd $TMPDIR && cat > f <<'EOF'\n读 ~/.ssh/id_rsa 与 $GITHUB_TOKEN 的说明\nEOF",
	} {
		t.Run(cmd, func(t *testing.T) {
			v := g.Judge(Request{Tool: executor.PermToolBash, Command: cmd}, sc)
			if v.Action != AutoAllow {
				t.Fatalf("%q 不是凭据位置，应维持 AutoAllow，得 %s rule=%q（%s）", cmd, v.Action, v.Rule, v.Reason)
			}
		})
	}
}

// TestB460CredentialGuardOtherTools：写文件工具的落点、执行器检出的 Paths 同样过守卫；网页抓取不受影响。
func TestB460CredentialGuardOtherTools(t *testing.T) {
	b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	grok := filepath.Join(sc.TaskDir, "grokhome", "auth.json")
	v := g.Judge(Request{Tool: executor.PermToolWrite, Text: "Write: auth.json", Paths: []string{grok}}, sc)
	if v.Action != Consult {
		t.Fatalf("写任务目录内的凭据副本应 Consult，得 %s（%s）", v.Action, v.Reason)
	}
	v = g.Judge(Request{Tool: executor.PermToolBash, Command: "ls", Paths: []string{"~/.ssh/*"}}, sc)
	if v.Action == AutoAllow {
		t.Fatalf("Paths 带凭据目录不得 AutoAllow，得 %s（%s）", v.Action, v.Reason)
	}
	v = g.Judge(Request{Tool: executor.PermToolWebFetch, Text: "webfetch: https://x/?token=1", URL: "https://x/?token=1"}, sc)
	if v.Action != AutoAllow || v.Rule != RuleWebFetch {
		t.Fatalf("网页抓取路由不变，得 %s rule=%q", v.Action, v.Rule)
	}
}

// TestB460CredentialLocationsDetected：导出清单（审批提示词的单一来源）里的每一项都被规则层识别。
func TestB460CredentialLocationsDetected(t *testing.T) {
	b460CredHome(t)
	g := newTestGate(t)
	sc := b460Scope(t)
	locs := CredentialLocations()
	if len(locs) < 15 {
		t.Fatalf("凭据清单条目过少：%v", locs)
	}
	for _, loc := range locs {
		p := loc.Path
		if strings.HasSuffix(p, "/") {
			p += "x"
		}
		v := g.Judge(Request{Tool: executor.PermToolBash, Command: "cat " + p}, sc)
		if v.Action != Consult {
			t.Errorf("清单项 %q 未被识别：cat %s → %s（%s）", loc.Path, p, v.Action, v.Reason)
		}
	}
}
