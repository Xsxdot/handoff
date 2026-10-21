package permgate

// b460_scope_test.go —— B460 S6 V4：任务范围内建目录与写入的可证明放行（经 Gate.Judge）。
//
// Scope 用真实临时目录（macOS 上覆盖 /var → /private/var 软链）；正例要求 AutoAllow 且
// 审计 id 落在 Reason 里，反例按 plan §5.1 V4 表逐条断言（B1/B2/B3 的锁）。
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// b460Scope 建一份真实目录布局：Workdir、TaskDir（含 artifacts）、TaskTmpDir。
func b460Scope(t *testing.T) Scope {
	t.Helper()
	data := t.TempDir()
	taskDir := filepath.Join(data, "tasks", "t-b460")
	art := filepath.Join(taskDir, "artifacts")
	tmp := filepath.Join(data, "tmp", "t-b460")
	work := filepath.Join(data, "wt")
	for _, d := range []string{art, tmp, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Scope{Workdir: work, TaskDir: taskDir, TaskTmpDir: tmp, ArtifactsDir: art}
}

func TestB460ScopedWriteTable(t *testing.T) {
	g := newTestGate(t)
	sc := b460Scope(t)
	art, tmp := sc.ArtifactsDir, sc.TaskTmpDir

	// N10 的范围：产物目录是指向范围外目录（模拟 $HOME）的软链
	linked := b460Scope(t)
	outside := t.TempDir()
	if err := os.RemoveAll(linked.ArtifactsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, linked.ArtifactsDir); err != nil {
		t.Fatal(err)
	}

	type want int
	const (
		allow    want = iota // AutoAllow 且 Reason 含 id
		notAllow             // 任何非 AutoAllow
		escalate             // 必须 Escalate
		consult              // 必须 Consult（与基线一致）
	)
	cases := []struct {
		name  string
		req   Request
		scope Scope
		want  want
		id    string
	}{
		{name: "P1 mkdir 产物子目录", req: Request{Tool: executor.PermToolBash,
			Command: "mkdir -p " + art + "/sub"}, want: allow, id: "mkdir"},
		{name: "P2 external_directory 原形", req: Request{Tool: executor.PermToolBash,
			Command: "ls " + art + "; mkdir -p " + art, Paths: []string{art}}, want: allow, id: "mkdir"},
		{name: "P3 cd 后 printf 写", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && printf 'x' > r.md"}, want: allow, id: "printf-write"},
		{name: "P4 cd 后 echo 写再 ls", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && echo x > r.md && ls"}, want: allow, id: "echo-write"},
		{name: "P5 产物变量前缀", req: Request{Tool: executor.PermToolBash,
			Command: `mkdir -p "$HANDOFF_ARTIFACTS_DIR/sub"`}, want: allow, id: "mkdir"},
		{name: "P6 TMPDIR 前缀写", req: Request{Tool: executor.PermToolBash,
			Command: `printf x > "$TMPDIR/a.txt"`}, want: allow, id: "printf-write"},
		{name: "P7 临时目录建目录再写", req: Request{Tool: executor.PermToolBash,
			Command: "mkdir -p " + tmp + "/w && cd " + tmp + "/w && echo x > f"}, want: allow, id: "mkdir"},
		{name: "P8 codex cwd 基准", req: Request{Tool: executor.PermToolBash,
			Command: "printf x > r.md", Cwd: art}, want: allow, id: "printf-write"},
		{name: "P9 写文件工具", req: Request{Tool: executor.PermToolWrite,
			Text: "Write: r.md", Paths: []string{art + "/r.md"}}, want: allow},
		{name: "N1 mkdir 越界", req: Request{Tool: executor.PermToolBash,
			Command: "mkdir -p /etc/x"}, want: escalate},
		{name: "N2 HOME 展开", req: Request{Tool: executor.PermToolBash,
			Command: `mkdir -p "$HOME/x"`}, want: notAllow},
		{name: "N3 无基准相对写", req: Request{Tool: executor.PermToolBash,
			Command: "printf x > r.md"}, want: consult},
		{name: "N4 cd 越界", req: Request{Tool: executor.PermToolBash,
			Command: "cd /etc && printf x > passwd"}, want: escalate},
		{name: "N5 变量重绑定", req: Request{Tool: executor.PermToolBash,
			Command: `HANDOFF_ARTIFACTS_DIR=/etc; printf x > "$HANDOFF_ARTIFACTS_DIR/p"`}, want: notAllow},
		{name: "N6 命令替换", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && echo $(id) > r.md"}, want: notAllow},
		{name: "N7 黑名单", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && echo x > r.md && sudo ls"}, want: escalate},
		{name: "N8 一个落点不可证明", req: Request{Tool: executor.PermToolBash,
			Command: "mkdir -p " + art + "/a ~/b"}, want: notAllow},
		{name: "N9 解析后越界", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && printf x >> ../../../../etc/x"}, want: escalate},
		// 以下为执行者补的对抗例（B1/B2 的形态变体），非 plan 表原条目
		{name: "X1 引号后拼接逃逸", req: Request{Tool: executor.PermToolBash,
			Command: `printf x > "$TMPDIR"/../../etc/x`}, want: notAllow},
		{name: "X2 变量后引号内逃逸", req: Request{Tool: executor.PermToolBash,
			Command: `printf x > $TMPDIR/"../../../etc/x"`}, want: notAllow},
		{name: "X3 单引号字面量", req: Request{Tool: executor.PermToolBash,
			Command: `printf x > '$TMPDIR/x'`}, want: notAllow},
		{name: "X4 mkdir 未知旗标", req: Request{Tool: executor.PermToolBash,
			Command: "mkdir -Z " + art + "/c"}, want: notAllow},
		{name: "X5 cd 后分号", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + "/missing; printf x > r.md"}, want: notAllow},
		{name: "X6 cd 后或", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && printf x > r.md || printf x > passwd"}, want: notAllow},
		{name: "X7 相对 cd 无基准", req: Request{Tool: executor.PermToolBash,
			Command: "cd sub && printf x > r.md"}, want: notAllow},
		{name: "X8 echo 管道", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + art + " && echo x > r.md | cat"}, want: notAllow},
		{name: "X9 cwd 越界", req: Request{Tool: executor.PermToolBash,
			Command: "printf x > passwd", Cwd: "/etc"}, want: escalate},
		{name: "N10 软链越界", req: Request{Tool: executor.PermToolBash,
			Command: "cd " + linked.ArtifactsDir + " && echo x > r.md"}, scope: linked, want: notAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sc
			if tc.scope.Workdir != "" {
				s = tc.scope
			}
			v := g.Judge(tc.req, s)
			switch tc.want {
			case allow:
				if v.Action != AutoAllow || (tc.id != "" && !strings.Contains(v.Reason, tc.id)) {
					t.Fatalf("%q 应 AutoAllow（id %s），得 %s rule=%q（%s）", tc.req.Command, tc.id, v.Action, v.Rule, v.Reason)
				}
				if tc.id != "" && v.Rule != RuleSafeCommand {
					t.Fatalf("%q 放行规则应为 %s，得 %q", tc.req.Command, RuleSafeCommand, v.Rule)
				}
			case notAllow:
				if v.Action == AutoAllow {
					t.Fatalf("%q 不得 AutoAllow（%s）", tc.req.Command, v.Reason)
				}
			case escalate:
				if v.Action != Escalate {
					t.Fatalf("%q 应 Escalate，得 %s（%s）", tc.req.Command, v.Action, v.Reason)
				}
			case consult:
				if v.Action != Consult {
					t.Fatalf("%q 应 Consult，得 %s（%s）", tc.req.Command, v.Action, v.Reason)
				}
			}
		})
	}
}

// TestB460SafeCommandIDsIncludeScopedWrites 锁 F11：三个新审计 id 进政策快照。
func TestB460SafeCommandIDsIncludeScopedWrites(t *testing.T) {
	ids := strings.Join(SafeCommandIDs(), ",")
	for _, id := range []string{"mkdir", "echo-write", "printf-write"} {
		if !strings.Contains(","+ids+",", ","+id+",") {
			t.Fatalf("SafeCommandIDs 缺 %s：%s", id, ids)
		}
	}
}

// TestB460WebFetchRoute 锁 S4 权限门部分（V5）：结构化 webfetch 放行，截断与黑名单升级，
// Tool=other 不靠文本猜。
func TestB460WebFetchRoute(t *testing.T) {
	g, err := New([]string{`evil\.example`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sc := b460Scope(t)
	v := g.Judge(Request{Tool: executor.PermToolWebFetch, Text: "webfetch: https://go.dev/doc/",
		URL: "https://go.dev/doc/"}, sc)
	if v.Action != AutoAllow || v.Rule != RuleWebFetch {
		t.Fatalf("webfetch 应放行 rule=webfetch，得 %s rule=%q（%s）", v.Action, v.Rule, v.Reason)
	}
	v = g.Judge(Request{Tool: executor.PermToolWebFetch, Text: "webfetch: https://go.dev/" + executor.TruncationMarker,
		Truncated: true}, sc)
	if v.Action != Escalate {
		t.Fatalf("截断的 webfetch 应升级，得 %s", v.Action)
	}
	v = g.Judge(Request{Tool: executor.PermToolWebFetch, Text: "webfetch: https://evil.example/x",
		URL: "https://evil.example/x"}, sc)
	if v.Action != Escalate {
		t.Fatalf("命中自定义黑名单的 webfetch 应升级，得 %s", v.Action)
	}
	v = g.Judge(Request{Tool: executor.PermToolOther, Text: "webfetch: https://go.dev/doc/"}, sc)
	if v.Action == AutoAllow {
		t.Fatalf("Tool=other 不得按文本猜成抓取放行，得 %s", v.Action)
	}
}
