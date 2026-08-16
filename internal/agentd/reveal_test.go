package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// newRevealEnv 起一个带单个已登记项目（目录里有 a.txt）的 agentd，返回完整
// 测试环境（env.ts.URL 可走 mux + hostGuard + auth 全链路）与已被白名单认可
// 的工作树目录。照抄 wsFilesFixture 的既有做法：newTestAgentdEnv +
// initGitRepoWithOrigin + CreateProjectLocation。
func newRevealEnv(t *testing.T) (*testAgentdEnv, string) {
	t.Helper()
	env := newTestAgentdEnv(t)
	repo := initGitRepoWithOrigin(t, "git@github.com:x/demo.git")
	mustWriteFile(t, filepath.Join(repo, "a.txt"), "a")
	if err := env.st.CreateProjectLocation(&proto.ProjectLocation{
		ProjectID: "cccc777788889999", Name: "demo", Path: repo,
		OriginURL: "git@github.com:x/demo.git", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateProjectLocation: %v", err)
	}
	return env, repo
}

// newRevealServer 同 newRevealEnv，但只返回 server 与工作树目录，给直调
// handleWorkspaceReveal 的白盒用例用。
func newRevealServer(t *testing.T) (*Server, string) {
	t.Helper()
	env, repo := newRevealEnv(t)
	return env.srv, repo
}

// revealCapture 换掉真的 open，记下收到的绝对路径。返回的 restore 必须 defer。
func revealCapture(t *testing.T) (*string, func()) {
	t.Helper()
	var got string
	prev := revealOpener
	revealOpener = func(_ context.Context, abs string) error {
		got = abs
		return nil
	}
	return &got, func() { revealOpener = prev }
}

// revealReq 造一条指向 root 的 reveal 请求。remote 为空时用回环地址（127.0.0.1:54321）
// ——要测空串 RemoteAddr 必须先构造后手动覆盖 r.RemoteAddr。
func revealReq(root, rel, machine, remote string) *http.Request {
	u := "/api/workspaces/reveal?path=" + root + "&rel=" + rel
	if machine != "" {
		u += "&machine=" + machine
	}
	r := httptest.NewRequest(http.MethodPost, u, nil)
	if remote == "" {
		remote = "127.0.0.1:54321"
	}
	r.RemoteAddr = remote
	return r
}

func TestRevealHappyPath(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d，body=%s", w.Code, w.Body.String())
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "a.txt"))
	if *got != want {
		t.Fatalf("open 收到 %q，期望 %q", *got, want)
	}
}

// TestRevealEmptyRel 断言空 rel 揭示工作树根本身——与 DeleteEntry 不同，
// 揭示根是正当操作，不能照抄「空串按非法名拒」。
func TestRevealEmptyRel(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "", "", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d，body=%s", w.Code, w.Body.String())
	}
	want, _ := filepath.EvalSymlinks(root)
	if *got != want {
		t.Fatalf("open 收到 %q，期望工作树根 %q", *got, want)
	}
}

func TestRevealRejectsMachine(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "devbox", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealRejectsNonLoopback(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", "100.73.238.21:54321"))
	if w.Code != http.StatusConflict {
		t.Fatalf("状态码 %d，期望 409；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

// TestRevealRejectsUnparseableRemote 钉住 isLoopbackAddr 的 fail-closed 语义：
// RemoteAddr 判不出来（空串、非 IP 形态）时**拒绝**而不是放行。若无此用例，
// 把返回值变异成 `ip == nil || ip.IsLoopback()`（fail-open）仍会全绿。
// revealReq 对空串 remote 有回环 fallback，这里先构造再手动覆盖 RemoteAddr。
func TestRevealRejectsUnparseableRemote(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	for _, remote := range []string{"", "@", "unix"} {
		w := httptest.NewRecorder()
		r := revealReq(root, "a.txt", "", "")
		r.RemoteAddr = remote // 绕过 revealReq 的空串 fallback
		s.handleWorkspaceReveal(w, r)
		if w.Code != http.StatusConflict {
			t.Fatalf("RemoteAddr=%q 状态码 %d，期望 409；body=%s", remote, w.Code, w.Body.String())
		}
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealUnsupportedPlatform(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()
	prev := revealSupportedOS
	revealSupportedOS = false
	defer func() { revealSupportedOS = prev }()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 %d，期望 501；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

func TestRevealPathEscape(t *testing.T) {
	s, root := newRevealServer(t)
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "../../etc/hosts", "", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("逃逸路径居然执行了 open：%q", *got)
	}
}

// TestRevealSymlinkEscape 断言工作树内的符号链接指向树外时被拒——这是
// EvalSymlinks 前缀校验存在的全部理由，纯字符串 Clean 挡不住它。
func TestRevealSymlinkEscape(t *testing.T) {
	s, root := newRevealServer(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	got, restore := revealCapture(t)
	defer restore()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "link.txt", "", ""))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400；body=%s", w.Code, w.Body.String())
	}
	if *got != "" {
		t.Fatalf("越界符号链接居然执行了 open：%q", *got)
	}
}

// TestRevealOpenFails 断言 open 的失败原文透传，不吞成「操作失败」。
func TestRevealOpenFails(t *testing.T) {
	s, root := newRevealServer(t)
	prev := revealOpener
	revealOpener = func(context.Context, string) error {
		return errors.New("kLSNoExecutableErr")
	}
	defer func() { revealOpener = prev }()

	w := httptest.NewRecorder()
	s.handleWorkspaceReveal(w, revealReq(root, "a.txt", "", ""))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 %d，期望 500", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "kLSNoExecutableErr") {
		t.Fatalf("错误原文被吞了：%q", body["error"])
	}
}

// TestRevealRouteHappyPath 走 env.ts.URL 完整路由栈（mux + hostGuard + auth）
// 钉住 reveal 的路由注册：POST /api/workspaces/reveal?path=&rel= 正常返回 200
// 并真的执行 open。若 server.go 里注册行被删，这里会 404。
func TestRevealRouteHappyPath(t *testing.T) {
	env, repo := newRevealEnv(t)
	got, restore := revealCapture(t)
	defer restore()

	code, body := doJSON(t, env, http.MethodPost,
		"/api/workspaces/reveal?path="+url.QueryEscape(repo)+"&rel=a.txt", "")
	if code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200；体 = %s", code, body)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(repo, "a.txt"))
	if *got != want {
		t.Fatalf("open 收到 %q，期望 %q", *got, want)
	}
}

// TestRevealRouteRejectsMachine 走完整路由栈钉住 ?machine= 拒绝分支：400。
func TestRevealRouteRejectsMachine(t *testing.T) {
	env, repo := newRevealEnv(t)
	got, restore := revealCapture(t)
	defer restore()

	code, body := doJSON(t, env, http.MethodPost,
		"/api/workspaces/reveal?path="+url.QueryEscape(repo)+"&rel=a.txt&machine=devbox", "")
	if code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400；体 = %s", code, body)
	}
	if *got != "" {
		t.Fatalf("被拒的请求居然执行了 open：%q", *got)
	}
}

// TestRevealRouteRejectsGet 顺手钉住方法路由：GET 打同一 URL 应得 405。
func TestRevealRouteRejectsGet(t *testing.T) {
	env, repo := newRevealEnv(t)
	code, body := doJSON(t, env, http.MethodGet,
		"/api/workspaces/reveal?path="+url.QueryEscape(repo)+"&rel=a.txt", "")
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d, want 405；体 = %s", code, body)
	}
}
