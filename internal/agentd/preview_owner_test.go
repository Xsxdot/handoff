package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
	"github.com/coder/websocket"
)

type previewStaticStub struct {
	mu     sync.Mutex
	starts int
	stops  int
}

func (s *previewStaticStub) Start(context.Context, string, string) (string, func() error, error) {
	s.mu.Lock()
	s.starts++
	s.mu.Unlock()
	return "http://localhost:4400/index.html", func() error {
		s.mu.Lock()
		s.stops++
		s.mu.Unlock()
		return nil
	}, nil
}

func newPreviewOwnerEnv(t *testing.T) (*testAgentdEnv, *PreviewOwner) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatalf("open preview store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{Token: testToken, DataDir: dir}
	srv := NewServer(cfg, st, previewTestLogger(t))
	ts := testhttp.NewServer(t, srv.Handler())
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	env := &testAgentdEnv{srv: srv, ts: ts, st: st, token: testToken}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("preview"), 0o600); err != nil {
		t.Fatalf("write preview fixture: %v", err)
	}
	now := time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)
	static := &previewStaticStub{}
	owner := NewPreviewOwner(env.st, NewPreviewHub(previewTestLogger(t)), PreviewOwnerDeps{
		Now:   func() time.Time { return now },
		NewID: func() string { return "preview-test" },
		Getwd: func() (string, error) { return root, nil },
		ProbePort: func(_ context.Context, port int) error {
			if port != 5173 {
				return errors.New("端口未监听")
			}
			return nil
		},
		ResolveWorkspace: func(context.Context, string) (string, string, string, error) {
			return root, "https://example.test/repo", "feature/demo", nil
		},
		Static: static,
	}, previewTestLogger(t))
	env.srv.SetPreviewOwner(owner)
	return env, owner
}

func previewPost(t *testing.T, env *testAgentdEnv, path string, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new preview request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview POST %s: %v", path, err)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read preview response: %v", err)
	}
	resp.Body.Close()
	return resp, bodyBytes
}

func previewRequest(t *testing.T, env *testAgentdEnv, method, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, env.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new preview request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview %s %s: %v", method, path, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read preview response: %v", err)
	}
	resp.Body.Close()
	return resp, body
}

func TestPreviewOwnerHTTPCreateListClose(t *testing.T) {
	env, _ := newPreviewOwnerEnv(t)
	resp, body := previewPost(t, env, "/api/previews", `{"port":5173}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
	}
	var session proto.PreviewSession
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if session.ID != "preview-test" || session.EntryURL != "http://localhost:5173" || session.CWD == "" || session.OriginURL != "https://example.test/repo" || session.Branch != "feature/demo" || session.TTLSeconds != 7200 || session.Machine != "" {
		t.Fatalf("session=%+v", session)
	}
	resp, body = previewRequest(t, env, http.MethodGet, "/api/previews")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, body)
	}
	var list proto.PreviewListResp
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != session.ID || list.Machines != nil {
		t.Fatalf("list=%+v", list)
	}
	resp, body = previewRequest(t, env, http.MethodDelete, "/api/previews/"+session.ID)
	if resp.StatusCode != http.StatusOK || string(body) == "" {
		t.Fatalf("close status=%d body=%s", resp.StatusCode, body)
	}
	var closeResp proto.PreviewCloseResp
	if err := json.Unmarshal(body, &closeResp); err != nil || !closeResp.OK {
		t.Fatalf("close response=%s err=%v", body, err)
	}
	resp, body = previewRequest(t, env, http.MethodDelete, "/api/previews/"+session.ID)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "预览会话") {
		t.Fatalf("second close status=%d body=%s", resp.StatusCode, body)
	}
}

func TestDefaultPreviewWorkspaceResolverReadsGitMetadata(t *testing.T) {
	repo := initGitRepoWithOrigin(t, "git@github.com:Xsxdot/handoff.git")
	gitAt(t, repo, "checkout", "-q", "-b", "feature/preview")

	root, origin, branch, err := defaultPreviewWorkspaceResolver(context.Background(), func() (string, error) {
		return repo, nil
	})
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("eval repo: %v", err)
	}
	if root != wantRoot || origin != "git@github.com:Xsxdot/handoff.git" || branch != "feature/preview" {
		t.Fatalf("workspace metadata root=%q origin=%q branch=%q want root=%q", root, origin, branch, wantRoot)
	}
}

func TestPreviewOwnerRejectsInvalidCreate(t *testing.T) {
	env, _ := newPreviewOwnerEnv(t)
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty", body: `{}`, want: "port/path"},
		{name: "too low", body: `{"port":0}`, want: "port"},
		{name: "too high", body: `{"port":65536}`, want: "65536"},
		{name: "xor", body: `{"port":5173,"path":"index.html"}`, want: "二选一"},
		{name: "unlistened", body: `{"port":9000}`, want: "未监听"},
		{name: "bad via", body: `{"port":5173,"via":["*"]}`, want: "via"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := previewPost(t, env, "/api/previews", tc.body)
			if resp.StatusCode < http.StatusBadRequest || resp.StatusCode >= http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), tc.want) || !strings.Contains(string(body), "preview") {
				t.Fatalf("body=%s, want operation and %q", body, tc.want)
			}
		})
	}
}

func TestPreviewOwnerPathUsesStaticAndWSPublishesFullEvents(t *testing.T) {
	env, _ := newPreviewOwnerEnv(t)
	wsURL := "ws" + strings.TrimPrefix(env.ts.URL, "http") + "/ws/previews"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + env.token}}})
	if err != nil {
		t.Fatalf("dial preview ws: %v", err)
	}
	defer conn.CloseNow()
	resp, body := previewPost(t, env, "/api/previews", `{"path":"index.html"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("path create status=%d body=%s", resp.StatusCode, body)
	}
	var created proto.PreviewEvent
	if _, body, err := conn.Read(ctx); err != nil {
		t.Fatalf("read created event: %v", err)
	} else if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created event: %v", err)
	}
	if created.Type != proto.PreviewEventCreated || created.Session.EntryURL != "http://localhost:4400/index.html" || created.Session.OriginURL == "" || created.Session.Branch == "" {
		t.Fatalf("created=%+v", created)
	}
	resp, body = previewRequest(t, env, http.MethodDelete, "/api/previews/"+created.Session.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close status=%d body=%s", resp.StatusCode, body)
	}
	var closed proto.PreviewEvent
	if _, body, err := conn.Read(ctx); err != nil {
		t.Fatalf("read closed event: %v", err)
	} else if err := json.Unmarshal(body, &closed); err != nil {
		t.Fatalf("decode closed event: %v", err)
	}
	if closed.Type != proto.PreviewEventClosed || closed.Session.ID != created.Session.ID || closed.Session.EntryURL != created.Session.EntryURL {
		t.Fatalf("closed=%+v", closed)
	}
}

func previewTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
