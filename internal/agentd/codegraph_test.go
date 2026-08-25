// codegraph 端点测试：取图、视图叠加数据、源码读取与路径逃逸拒绝。
package agentd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/charter/graph/codegraph"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

func codegraphFixtureRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	sourceRoot := filepath.Join("testdata", "codegraph-repo")
	for _, rel := range []string{
		"codegraph/baseline.json",
		"codegraph/best.json",
		"codegraph/diffs/branch-x.json",
		"codegraph/domains/d_core.json",
		"codegraph/target.json",
		"cmd/run.go",
		"svc/server.go",
		"svc/task.go",
		"svc/notifier.go",
		"web/task.ts",
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

func codegraphRawGET(t *testing.T, env *testAgentdEnv, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s 读取响应: %v", path, err)
	}
	return resp.StatusCode, body
}

// roundTripReport 把库直调的报告过一遍 JSON 编解码，使其与 HTTP 响应侧的
// 反序列化产物处于同一表示口径（omitempty 字段的 nil/空值差异在此抹平）。
func roundTripReport(t *testing.T, rep *codegraph.Report) *codegraph.Report {
	t.Helper()
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("序列化直调报告: %v", err)
	}
	var out codegraph.Report
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("反序列化直调报告: %v", err)
	}
	return &out
}

type codegraphResponse struct {
	Baseline codegraph.Graph                 `json:"baseline"`
	Views    map[string]codegraph.Diff       `json:"views"`
	Stale    []codegraph.StaleNode           `json:"stale"`
	Best     *codegraph.Best                 `json:"best"`
	Target   *codegraph.Target               `json:"target"`
	Decls    map[string]codegraph.DomainDecl `json:"decls"`
	Report   *codegraph.Report               `json:"report"`
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

	var body codegraphResponse
	if status := env.getJSON(t, "/api/projects/demo/codegraph", &body); status != http.StatusOK {
		t.Fatalf("代码图状态=%d, body=%+v", status, body)
	}
	if len(body.Baseline.Nodes) != 8 || body.Views["branch-x"].View != "branch:x" || body.Stale == nil || len(body.Stale) != 0 {
		t.Fatalf("代码图响应形状: nodes=%d views=%v stale=%v", len(body.Baseline.Nodes), body.Views, body.Stale)
	}
	if body.Best == nil || body.Target == nil || body.Report == nil {
		t.Fatalf("代码图对照数据缺失: best=%v target=%v report=%v", body.Best, body.Target, body.Report)
	}
	decls, err := codegraph.LoadDomainDecls(repo)
	if err != nil {
		t.Fatalf("加载 fixture domain decls: %v", err)
	}
	if body.Decls == nil || !reflect.DeepEqual(body.Decls, decls) {
		t.Fatalf("代码图声明响应未逐字段保真: got=%+v want=%+v", body.Decls, decls)
	}
	best, err := codegraph.LoadBest(repo)
	if err != nil {
		t.Fatalf("加载 fixture best: %v", err)
	}
	target, err := codegraph.LoadTarget(repo)
	if err != nil {
		t.Fatalf("加载 fixture target: %v", err)
	}
	g, err := codegraph.LoadGraph(repo)
	if err != nil {
		t.Fatalf("加载 fixture baseline: %v", err)
	}
	// 直调结果要走一遍 JSON 往返再比：响应侧的 report 是反序列化产物，而
	// Report.LegacyHits 带 omitempty——空 map 在 wire 上被丢掉、解回来是 nil，
	// 与内存里恒非空的 map[string]int{} 天然不相等。往返后两侧同口径，比的才
	// 是「宿主传的报告 == 库直调的报告」，而不是内存表示的偶然差异。
	wantReport := roundTripReport(t, codegraph.Check(target, best, codegraph.Merge(g, nil), decls))
	if !reflect.DeepEqual(body.Report, wantReport) {
		t.Fatalf("代码图报告与直调 Check 不一致:\n got=%+v\nwant=%+v", body.Report, wantReport)
	}
	if len(body.Report.Fails) != 0 || len(body.Report.Warns) != 0 {
		t.Fatalf("fixture 报告应无 fails/warns: %+v", body.Report)
	}
	if status, raw := codegraphRawGET(t, env, "/api/projects/demo/codegraph"); status != http.StatusOK || !strings.Contains(string(raw), `"fails":[]`) || !strings.Contains(string(raw), `"warns":[]`) {
		t.Fatalf("空报告 JSON 未归一化为 []: status=%d body=%s", status, raw)
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

func TestCodegraphDeclsWireConditions(t *testing.T) {
	fullDecl := []byte(`{
  "domain": "d_core",
  "responsibility": "承载 fixture 的全部代码图节点。",
  "invariants": [{"text": "fixture invariant", "testRef": "TestCodegraphEndpoint"}],
  "lifecycle": {"from": "cmd/run.go#Run", "to": "cmd/run.go#Run"},
  "stateMachine": [{"from": "pending", "to": "running", "anchor": "cmd/run.go#Run"}]
}`)

	cases := []struct {
		name        string
		mutate      func(t *testing.T, repo string)
		wantDecls   bool
		wantBest    bool
		wantTarget  bool
		wantReport  bool
		wantDeclLog bool
	}{
		{
			name: "normal directory with legal declaration",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "codegraph", "domains", "d_core.json"), fullDecl, 0o644); err != nil {
					t.Fatalf("写入完整声明: %v", err)
				}
			},
			wantDecls: true, wantBest: true, wantTarget: true, wantReport: true,
		},
		{
			name: "empty directory is present",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				dir := filepath.Join(repo, "codegraph", "domains")
				if err := os.RemoveAll(dir); err != nil {
					t.Fatalf("删除声明目录: %v", err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("重建空声明目录: %v", err)
				}
			},
			wantDecls: true, wantBest: true, wantTarget: true, wantReport: true,
		},
		{
			name: "directory absent",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(repo, "codegraph", "domains")); err != nil {
					t.Fatalf("删除声明目录: %v", err)
				}
			},
			wantDecls: false, wantBest: true, wantTarget: true, wantReport: true,
		},
		{
			name: "best absent",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.Remove(filepath.Join(repo, "codegraph", "best.json")); err != nil {
					t.Fatalf("删除 best: %v", err)
				}
			},
			wantDecls: false, wantBest: false, wantTarget: false, wantReport: false,
		},
		{
			name: "target load failure",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "codegraph", "target.json"), []byte("{"), 0o644); err != nil {
					t.Fatalf("写入非法 target: %v", err)
				}
			},
			wantDecls: false, wantBest: true, wantTarget: false, wantReport: false,
		},
		{
			name: "single declaration parse failure",
			mutate: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "codegraph", "domains", "d_core.json"), []byte("{"), 0o644); err != nil {
					t.Fatalf("写入非法声明: %v", err)
				}
			},
			wantDecls: false, wantBest: true, wantTarget: true, wantReport: false, wantDeclLog: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken}, logger)
			repo := codegraphFixtureRepo(t)
			registerCodegraphProject(t, env, repo)
			tc.mutate(t, repo)

			status, raw := codegraphRawGET(t, env, "/api/projects/demo/codegraph")
			if status != http.StatusOK {
				t.Fatalf("代码图状态=%d body=%s", status, raw)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatalf("解码代码图响应: %v", err)
			}

			rawDecls, hasDecls := response["decls"]
			if tc.wantDecls {
				if !hasDecls || string(bytes.TrimSpace(rawDecls)) == "null" {
					t.Fatalf("正常/空目录响应必须有非 null decls: %s", raw)
				}
				var gotDecls map[string]codegraph.DomainDecl
				if err := json.Unmarshal(rawDecls, &gotDecls); err != nil {
					t.Fatalf("解码 decls: %v", err)
				}
				if gotDecls == nil {
					t.Fatalf("目录存在时 decls 不得为 nil map")
				}
				wantDecls, err := codegraph.LoadDomainDecls(repo)
				if err != nil {
					t.Fatalf("直调加载声明: %v", err)
				}
				if !reflect.DeepEqual(gotDecls, wantDecls) {
					t.Fatalf("decls 未逐字段保真:\n got=%+v\nwant=%+v", gotDecls, wantDecls)
				}
			} else if hasDecls {
				t.Fatalf("缺席/失败响应不得含 decls 键（包括 null）: %s", raw)
			}

			_, hasBest := response["best"]
			_, hasTarget := response["target"]
			_, hasReport := response["report"]
			if hasBest != tc.wantBest || hasTarget != tc.wantTarget || hasReport != tc.wantReport {
				t.Fatalf("对照段存在性错误: best=%t target=%t report=%t body=%s", hasBest, hasTarget, hasReport, raw)
			}
			if tc.wantDeclLog && !strings.Contains(logs.String(), "代码图领域声明加载失败") {
				t.Fatalf("非法声明应产生告警日志，logs=%s", logs.String())
			}
			if tc.wantDeclLog {
				for _, want := range []string{"d_core.json", "cause="} {
					if !strings.Contains(logs.String(), want) {
						t.Fatalf("非法声明告警应包含 %q，logs=%s", want, logs.String())
					}
				}
			}
		})
	}
}

func TestCodegraphComparisonDataOptional(t *testing.T) {
	env := newTestAgentdEnv(t)
	repo := codegraphFixtureRepo(t)
	registerCodegraphProject(t, env, repo)

	status, raw := codegraphRawGET(t, env, "/api/projects/demo/codegraph")
	if status != http.StatusOK {
		t.Fatalf("带 best 请求状态=%d body=%s", status, raw)
	}
	var before map[string]any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatalf("解码带 best 响应: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "codegraph", "best.json")); err != nil {
		t.Fatalf("删除 fixture best: %v", err)
	}

	status, raw = codegraphRawGET(t, env, "/api/projects/demo/codegraph")
	if status != http.StatusOK {
		t.Fatalf("无 best 请求状态=%d body=%s", status, raw)
	}
	var after map[string]any
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatalf("解码无 best 响应: %v", err)
	}
	for _, key := range []string{"best", "target", "report"} {
		if _, ok := after[key]; ok {
			t.Fatalf("无 best 响应不应含 %q: %s", key, raw)
		}
	}
	for _, key := range []string{"baseline", "views", "stale"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("删除 best 改变既有字段 %q: before=%v after=%v", key, before[key], after[key])
		}
	}
}

func TestCodegraphComparisonDataTargetFailure(t *testing.T) {
	env := newTestAgentdEnv(t)
	repo := codegraphFixtureRepo(t)
	registerCodegraphProject(t, env, repo)
	if err := os.WriteFile(filepath.Join(repo, "codegraph", "target.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("写入非法 target: %v", err)
	}

	status, raw := codegraphRawGET(t, env, "/api/projects/demo/codegraph")
	if status != http.StatusOK {
		t.Fatalf("非法 target 请求状态=%d body=%s", status, raw)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("解码非法 target 响应: %v", err)
	}
	if _, ok := response["best"]; !ok {
		t.Fatalf("非法 target 响应缺少 best: %s", raw)
	}
	for _, key := range []string{"target", "report"} {
		if _, ok := response[key]; ok {
			t.Fatalf("非法 target 响应不应含 %q: %s", key, raw)
		}
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
