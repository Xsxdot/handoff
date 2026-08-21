// codegraph 端点测试：取图、视图叠加数据、源码读取与路径逃逸拒绝。
package agentd

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/codegraph"
	"github.com/Xsxdot/handoff/internal/proto"
)

func codegraphFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	sourceRoot := filepath.Join("..", "codegraph", "testdata", "repo")
	for _, rel := range []string{
		"codegraph/baseline.json",
		"codegraph/diffs/branch-x.json",
		"cmd/run.go",
		"svc/server.go",
		"svc/task.go",
		"svc/notifier.go",
	} {
		raw, err := os.ReadFile(filepath.Join(sourceRoot, rel))
		if err != nil {
			t.Fatalf("读取 fixture %s: %v", rel, err)
		}
		dst := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("创建 fixture 目录 %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			t.Fatalf("写入 fixture %s: %v", rel, err)
		}
	}
	return repo
}

func registerCodegraphProject(t *testing.T, env *testAgentdEnv, repo string) {
	t.Helper()
	err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "p-codegraph", Name: "demo", Path: repo,
		OriginURL: "https://example.test/demo", CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("登记项目: %v", err)
	}
}

func TestCodegraphEndpoint(t *testing.T) {
	env := newTestAgentdEnv(t)
	repo := codegraphFixtureRepo(t)
	registerCodegraphProject(t, env, repo)

	var body struct {
		Baseline codegraph.Graph           `json:"baseline"`
		Views    map[string]codegraph.Diff `json:"views"`
		Stale    []codegraph.StaleNode     `json:"stale"`
	}
	if status := env.getJSON(t, "/api/projects/demo/codegraph", &body); status != http.StatusOK {
		t.Fatalf("代码图状态=%d, body=%+v", status, body)
	}
	if len(body.Baseline.Nodes) != 7 || body.Views["branch-x"].View != "branch:x" || body.Stale == nil || len(body.Stale) != 0 {
		t.Fatalf("代码图响应形状: nodes=%d views=%v stale=%v", len(body.Baseline.Nodes), body.Views, body.Stale)
	}

	if status := env.getJSON(t, "/api/projects/ghost/codegraph", &map[string]string{}); status != http.StatusNotFound {
		t.Fatalf("未知项目状态=%d", status)
	}
	if err := os.RemoveAll(filepath.Join(repo, "codegraph")); err != nil {
		t.Fatalf("删除 codegraph: %v", err)
	}
	var missing map[string]string
	if status := env.getJSON(t, "/api/projects/demo/codegraph", &missing); status != http.StatusNotFound || !strings.Contains(missing["error"], "未生成代码图") {
		t.Fatalf("缺失代码图响应: status=%d body=%v", status, missing)
	}
}

func TestCodegraphSource(t *testing.T) {
	env := newTestAgentdEnv(t)
	repo := codegraphFixtureRepo(t)
	registerCodegraphProject(t, env, repo)

	var source struct {
		File  string   `json:"file"`
		From  int      `json:"from"`
		Lines []string `json:"lines"`
	}
	path := "/api/projects/demo/codegraph/source?file=svc/server.go&line=4&span=3"
	if status := env.getJSON(t, path, &source); status != http.StatusOK {
		t.Fatalf("源码状态=%d, body=%+v", status, source)
	}
	if source.File != "svc/server.go" || source.From != 1 || len(source.Lines) != 3 {
		t.Fatalf("源码窗口: %+v", source)
	}

	for _, file := range []string{"../../etc/passwd", "/etc/passwd"} {
		var errBody map[string]string
		path := "/api/projects/demo/codegraph/source?file=" + url.QueryEscape(file) + "&line=4"
		if status := env.getJSON(t, path, &errBody); status != http.StatusBadRequest {
			t.Fatalf("路径 %q 状态=%d body=%v", file, status, errBody)
		}
	}
	if status := env.getJSON(t, "/api/projects/demo/codegraph/source?file=svc/server.go&line=999&span=3", &source); status != http.StatusOK || len(source.Lines) == 0 {
		t.Fatalf("越界行号应截到文件尾: status=%d source=%+v", status, source)
	}
}
