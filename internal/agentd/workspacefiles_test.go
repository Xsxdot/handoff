// 按工作树寻址的两个只读接口（/api/workspaces/dir、/api/workspaces/file）的
// 白盒测试。核心红线：未登记的任意路径（含 $HOME）一律 403——agentd 不是
// 任意目录浏览器，论证见 spec §2.6。
package agentd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// wsFilesFixture 起一个带单个已登记项目的 agentd，返回 env 与该项目路径。
func wsFilesFixture(t *testing.T) (*testAgentdEnv, string) {
	t.Helper()
	env := newTestAgentdEnv(t)
	repo := initGitRepoWithOrigin(t, "git@github.com:x/demo.git")
	if err := os.MkdirAll(filepath.Join(repo, "internal"), 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("写文件: %v", err)
	}
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "cccc777788889999", Name: "demo", Path: repo,
		OriginURL: "git@github.com:x/demo.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	return env, repo
}

// doGet 发一个带 token 的 GET 到 env.ts 并返回状态码与响应体。
func doGet(t *testing.T, env *testAgentdEnv, path string, q url.Values) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+path+"?"+q.Encode(), nil)
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
		t.Fatalf("读响应体: %v", err)
	}
	return resp.StatusCode, body
}

// TestWorkspaceDirListsWhitelistedPath 断言已探测到的工作树可以被列举。
func TestWorkspaceDirListsWhitelistedPath(t *testing.T) {
	env, repo := wsFilesFixture(t)
	code, body := doGet(t, env, "/api/workspaces/dir", url.Values{"path": {repo}})
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", code, body)
	}
	var got proto.DirListResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	// .git 也会被列出，只断言我们建的两项都在、且目录排在文件前
	if len(got.Entries) == 0 || !got.Entries[0].IsDir {
		t.Errorf("列举结果 = %v, want 目录在前", names)
	}
	found := map[string]bool{}
	for _, e := range got.Entries {
		found[e.Name] = true
	}
	if !found["internal"] || !found["go.mod"] {
		t.Errorf("列举结果 = %v, want 含 internal 与 go.mod", names)
	}
}

// TestWorkspaceDirRejectsUnknownPath 是本任务的安全红线：
// 未登记的任意目录一律 403，agentd 不是任意目录浏览器。
func TestWorkspaceDirRejectsUnknownPath(t *testing.T) {
	env, _ := wsFilesFixture(t)
	outside := t.TempDir()
	code, body := doGet(t, env, "/api/workspaces/dir", url.Values{"path": {outside}})
	if code != http.StatusForbidden {
		t.Fatalf("状态码 = %d, want 403；体 = %s", code, body)
	}
}

// TestWorkspaceDirRejectsHome 单独钉住 $HOME：spec §2.6 的整条论证依赖它。
func TestWorkspaceDirRejectsHome(t *testing.T) {
	env, _ := wsFilesFixture(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到 home 目录")
	}
	code, _ := doGet(t, env, "/api/workspaces/dir", url.Values{"path": {home}})
	if code != http.StatusForbidden {
		t.Fatalf("$HOME 列举状态码 = %d, want 403", code)
	}
}

// TestWorkspaceDirMissingPath 断言缺 path 参数是 400 而不是 403。
func TestWorkspaceDirMissingPath(t *testing.T) {
	env, _ := wsFilesFixture(t)
	code, _ := doGet(t, env, "/api/workspaces/dir", url.Values{})
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", code)
	}
}

// TestWorkspaceFileReads 断言按工作树寻址的读文件与 /api/tasks/{id}/file 同语义。
func TestWorkspaceFileReads(t *testing.T) {
	env, repo := wsFilesFixture(t)
	code, body := doGet(t, env, "/api/workspaces/file", url.Values{"path": {repo}, "rel": {"go.mod"}})
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", code, body)
	}
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.Content != "module x\n" {
		t.Errorf("content = %q, want %q", got.Content, "module x\n")
	}
}

// TestWorkspaceFileErrorMapping 断言四种路径错误各自映射到正确状态码。
func TestWorkspaceFileErrorMapping(t *testing.T) {
	env, repo := wsFilesFixture(t)
	cases := []struct {
		name string
		rel  string
		want int
	}{
		{"逃逸", "../etc/passwd", http.StatusBadRequest},
		{"不存在", "nope.go", http.StatusNotFound},
		{"是目录", "internal", http.StatusBadRequest},
		{"缺 rel", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := url.Values{"path": {repo}}
			if c.rel != "" {
				q.Set("rel", c.rel)
			}
			code, body := doGet(t, env, "/api/workspaces/file", q)
			if code != c.want {
				t.Errorf("状态码 = %d, want %d；体 = %s", code, c.want, body)
			}
		})
	}
}
