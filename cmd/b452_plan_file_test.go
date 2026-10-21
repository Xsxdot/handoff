package cmd

// b452_plan_file_test.go —— 修复轮第 1 条：--template 接 plan 文件代替 --prompt（spec 方案「或 plan 文件代替
// --prompt」）。CLI 捕获的派发请求原样交给真实 orchestration.Manager.Dispatch，断言执行器收到的 PlanContent：
//   - 只给 plan 文件：模板正文 + 「## 本次任务」 + plan 文件内容；
//   - plan 文件与 --prompt 同给：照裸派发语义，--prompt 作为「## 附加指令（派发时提供）」接在其后。

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// b452PlanRecorder 包一层 fake，记录执行器收到的 StartReq。
type b452PlanRecorder struct {
	*fake.Fake
	mu   sync.Mutex
	reqs []executor.StartReq
}

func (r *b452PlanRecorder) Start(ctx context.Context, req executor.StartReq) error {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return r.Fake.Start(ctx, req)
}

// b452ReplayToManager 把 CLI 捕获的请求体喂给真实 Manager.Dispatch，返回执行器收到的 PlanContent。
func b452ReplayToManager(t *testing.T, body map[string]any) string {
	t.Helper()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{DataDir: dataDir, Executor: config.ExecutorConfig{Default: "fake"}}
	rec := &b452PlanRecorder{Fake: fake.New(nil)}
	m := orchestration.NewManager(st, orchestration.NewHub(), map[string]executor.Adapter{"fake": rec}, cfg,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetWorkspace(workspace.NewCapability())
	m.SetLiveConfig(func() *config.Config { return cfg })
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"commit", "-q", "--allow-empty", "-m", "init"}, {"remote", "add", "origin", "git@handoff.test:b452-plan.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	loc, err := m.RegisterProject(context.Background(), workspace.RegisterProjectReq{
		OriginURL: "git@handoff.test:b452-plan.git", Path: repo})
	if err != nil {
		t.Fatal(err)
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	task, err := m.Dispatch(context.Background(), orchestration.DispatchReq{ProjectID: loc.ProjectID,
		PlanB64: str("plan_b64"), PlanName: str("plan_name"), Prompt: str("prompt"), Mode: str("mode")})
	if err != nil {
		t.Fatalf("真实 Manager.Dispatch: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Stop(context.Background(), task.ID) })
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) != 1 {
		t.Fatalf("执行器应恰被启动一次，得 %d", len(rec.reqs))
	}
	return rec.reqs[0].PlanContent
}

func TestB452TemplatePlanFileBecomesTaskText(t *testing.T) {
	dataDir := t.TempDir()
	seedB452Template(t, dataDir, "ci", b452ReadonlyDef)
	plan := filepath.Join(t.TempDir(), "question.md")
	if err := os.WriteFile(plan, []byte("查 Foo 的全部调用方\n- 列文件:行号"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "正文 ||\n\n## 本次任务\n\n查 Foo 的全部调用方\n- 列文件:行号"

	t.Run("只给 plan 文件", func(t *testing.T) {
		a := newB452Agentd(t, &b452Agentd{})
		if _, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", plan,
			"--project", "p", "--no-terminal"); err != nil {
			t.Fatalf("--template + plan 文件应可派发: %v %s", err, stderr)
		}
		if raw, _ := base64.StdEncoding.DecodeString(a.body["plan_b64"].(string)); string(raw) != body {
			t.Fatalf("plan_b64 应为模板正文 + 本次任务（plan 文件），得 %q", raw)
		}
		got := b452ReplayToManager(t, a.body)
		if !strings.HasPrefix(got, body) || strings.Contains(got, "## 附加指令") {
			t.Fatalf("PlanContent 应以「正文 + 本次任务 = plan 文件」开头、无附加指令，得：\n%s", got)
		}
	})
	t.Run("plan 文件 + --prompt：prompt 作附加指令", func(t *testing.T) {
		a := newB452Agentd(t, &b452Agentd{})
		if _, stderr, err := runB452CLI(t, a, dataDir, "dispatch", "--template", "ci", plan, "--prompt", "只看 internal/",
			"--project", "p", "--no-terminal"); err != nil {
			t.Fatalf("%v %s", err, stderr)
		}
		got := b452ReplayToManager(t, a.body)
		if !strings.HasPrefix(got, body+"\n\n## 附加指令（派发时提供）\n\n只看 internal/") {
			t.Fatalf("PlanContent 应为「正文 + 本次任务 + 附加指令」，得：\n%s", got)
		}
	})
}
