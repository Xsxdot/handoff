// 按工作树寻址的两个只读接口（/api/workspaces/dir、/api/workspaces/file）的
// 白盒测试。核心红线：未登记的任意路径（含 $HOME）一律 400——白名单是
// **参数校验**不是安全边界，agentd 不是任意目录浏览器（PTY spec §1）。
package agentd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// TestWorkspaceDirRejectsUnknownPath 钉住白名单语义：未登记的任意目录一律
// 400——这是参数校验不是权限边界（PTY spec §1），agentd 不是任意目录浏览器。
func TestWorkspaceDirRejectsUnknownPath(t *testing.T) {
	env, _ := wsFilesFixture(t)
	outside := t.TempDir()
	code, body := doGet(t, env, "/api/workspaces/dir", url.Values{"path": {outside}})
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400；体 = %s", code, body)
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
	if code != http.StatusBadRequest {
		t.Fatalf("$HOME 列举状态码 = %d, want 400", code)
	}
}

// TestWorkspaceWhitelistRejectsWith400 钉住状态码语义：白名单是**参数校验**
// 不是权限边界，所以是 400 不是 403。403 会让人以为控制台会话比主令牌弱，
// 而它们在能力上等价（PTY spec §1）。
func TestWorkspaceWhitelistRejectsWith400(t *testing.T) {
	env, _ := wsFilesFixture(t)
	outside := t.TempDir()
	code, body := doGet(t, env, "/api/workspaces/dir", url.Values{"path": {outside}})
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400；体 = %s", code, body)
	}
	if !strings.Contains(string(body), "路径不是本机已探测到的工作树，拒绝访问") {
		t.Errorf("响应体不含白名单拒绝文案：%s", body)
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

// TestTaskFileKeepsTruncatedNotice 是 CLI 契约的回归防线：GET /api/tasks/{id}/file
// 在截断时仍必须把那行中文提示拼进正文，否则 handoff fetch 的输出静默变样。
// 这是 ReadFile 截断提示迁出后唯一还在拼提示的地方（handleTaskFile 端点）。
func TestTaskFileKeepsTruncatedNotice(t *testing.T) {
	env, repo := wsFilesFixture(t)
	big := bytes.Repeat([]byte("y"), maxRunOutput+4096)
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), big, 0o644); err != nil {
		t.Fatalf("写大文件: %v", err)
	}
	taskID := "task-big"
	mustCreateTask(t, env.st, &proto.Task{ID: taskID, RepoPath: repo})

	code, body := doGet(t, env, "/api/tasks/"+taskID+"/file", url.Values{"path": {"big.txt"}})
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", code, body)
	}
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	notice := truncatedNotice(int64(len(big)))
	if !strings.HasSuffix(got.Content, notice) {
		tail := got.Content
		if len(tail) > 80 {
			tail = tail[len(tail)-80:]
		}
		t.Errorf("content 应以截断提示结尾，实得尾部 %q", tail)
	}
	if want := maxRunOutput + len(notice); len(got.Content) != want {
		t.Errorf("len(content) = %d, want %d（正文 maxRunOutput + 提示长度）", len(got.Content), want)
	}
}

// doPut 发一个带 token 的 PUT（body 为 []byte 时按原文发送，否则 JSON 编码）到
// env.ts 并返回状态码与响应体。
func doPut(t *testing.T, env *testAgentdEnv, path string, q url.Values, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case []byte:
		reader = bytes.NewReader(b)
	case nil:
		reader = nil
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(http.MethodPut, env.ts.URL+path+"?"+q.Encode(), reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	return resp.StatusCode, got
}

// TestWorkspaceFileWriteOK 走完整 HTTP 链路：白名单 → 写入 → 200 + 新哈希。
func TestWorkspaceFileWriteOK(t *testing.T) {
	env, repo := wsFilesFixture(t)
	// 先经 ReadFile 拿到磁盘现状的哈希，模拟「调用方读到那一版」
	cur, err := ReadFile(repo, "go.mod")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if cur.SHA256 == "" {
		t.Fatalf("go.mod 的 SHA256 为空，无法作为写入基线")
	}
	newContent := "module handoff\n\ngo 1.26.1\n"
	sum := sha256.Sum256([]byte(newContent))

	code, body := doPut(t, env, "/api/workspaces/file",
		url.Values{"path": {repo}, "rel": {"go.mod"}},
		proto.FileWriteReq{Content: newContent, BaseSHA256: cur.SHA256})
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", code, body)
	}
	var got proto.FileWriteResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %q, want 新内容哈希 %q", got.SHA256, hex.EncodeToString(sum[:]))
	}
	if got.Size != int64(len(newContent)) {
		t.Errorf("size = %d, want %d", got.Size, len(newContent))
	}
	// 落盘核对：内容确实被替换
	disk, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		t.Fatalf("读磁盘: %v", err)
	}
	if string(disk) != newContent {
		t.Errorf("磁盘内容 = %q, want %q", disk, newContent)
	}
}

// TestWorkspaceFileWriteConflict 验证 409 的 body 带着 current（省掉前端一次往返）。
func TestWorkspaceFileWriteConflict(t *testing.T) {
	env, repo := wsFilesFixture(t)
	cur, err := ReadFile(repo, "go.mod")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// 故意传一个对不上的基线
	wrong := "0000000000000000000000000000000000000000"
	if wrong == cur.SHA256 {
		wrong = "1111111111111111111111111111111111111111"
	}
	code, body := doPut(t, env, "/api/workspaces/file",
		url.Values{"path": {repo}, "rel": {"go.mod"}},
		proto.FileWriteReq{Content: "module handoff\n", BaseSHA256: wrong})
	if code != http.StatusConflict {
		t.Fatalf("状态码 = %d, want 409；体 = %s", code, body)
	}
	if !strings.Contains(string(body), "文件已被改动") {
		t.Errorf("响应体不含冲突文案「文件已被改动」：%s", body)
	}
	var got proto.FileConflictResp
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if got.Current.Content != "module x\n" {
		t.Errorf("current.content = %q, want 磁盘现状 %q", got.Current.Content, "module x\n")
	}
	if got.Current.SHA256 == "" {
		t.Errorf("current.sha256 为空，冲突界面拿不到新基线")
	}
}

// TestWorkspaceFileWriteStatusMap 逐条钉住错误到状态码的映射。
func TestWorkspaceFileWriteStatusMap(t *testing.T) {
	env, repo := wsFilesFixture(t)
	// 造各拒绝面需要的文件：符号链接、目录、二进制（含 NUL）、超限
	if err := os.Symlink("go.mod", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatalf("建符号链接: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "logo.png"),
		[]byte{0x89, 'P', 'N', 'G', 0, 0, 0}, 0o644); err != nil {
		t.Fatalf("写二进制: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "big.txt"),
		bytes.Repeat([]byte("y"), maxRunOutput+1), 0o644); err != nil {
		t.Fatalf("写大文件: %v", err)
	}
	cur, err := ReadFile(repo, "go.mod")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	wrongBase := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if wrongBase == cur.SHA256 {
		wrongBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	}

	cases := []struct {
		name string
		path string // 空串表示 repo
		rel  string
		body any
		want int
	}{
		{"白名单不中", t.TempDir(), "go.mod", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"缺 rel", "", "", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{".git", "", ".git/config", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"符号链接", "", "link.txt", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"目录", "", "sub", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"二进制", "", "logo.png", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"超限", "", "big.txt", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"逃逸", "", "../etc/passwd", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusBadRequest},
		{"不存在", "", "nope.go", proto.FileWriteReq{Content: "x\n", BaseSHA256: cur.SHA256}, http.StatusNotFound},
		{"哈希不匹配", "", "go.mod", proto.FileWriteReq{Content: "x\n", BaseSHA256: wrongBase}, http.StatusConflict},
		{"请求体不是合法 JSON", "", "go.mod", []byte("{"), http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := c.path
			if p == "" {
				p = repo
			}
			q := url.Values{"path": {p}}
			if c.rel != "" {
				q.Set("rel", c.rel)
			}
			code, _ := doPut(t, env, "/api/workspaces/file", q, c.body)
			if code != c.want {
				t.Errorf("状态码 = %d, want %d", code, c.want)
			}
		})
	}
}
