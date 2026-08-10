// agentd Workspace 文件资源 HTTP/WS 路由测试。
//
// 职责：
//   - 锁定 entries/file/search 路由只接收 workspace_id + relative path
//   - 锁定 typed Problem、版本冲突与文件流 subscribed/live 线格式
//   - 证明 Bearer 鉴权覆盖资源 HTTP 与 WebSocket
//
// 边界：
//   - 使用真实 store、resourcegateway 与 machineauthority；不启动外部 agentd 进程
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/resourcegateway"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

type noPeerResources struct{}

func (noPeerResources) AuthorityForMachine(context.Context, string) (workspaceapi.Authority, error) {
	return nil, context.Canceled
}

type resourceServerEnv struct {
	server    *httptest.Server
	workspace string
	root      string
	token     string
}

func newResourceServerEnv(t *testing.T) *resourceServerEnv {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	local, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := st.ResolveWorkspaceForPath(context.Background(), local.ID, root, root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authority := machineauthority.NewResourceAuthority(logger)
	router := resourcegateway.NewRouter(st, authority, noPeerResources{}, logger)
	srv := NewServer(&config.Config{Token: "resource-token"}, st, logger)
	srv.SetResourceRouter(router)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); st.Close() })
	return &resourceServerEnv{server: ts, workspace: ws.ID, root: root, token: "resource-token"}
}

func (e *resourceServerEnv) request(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestWorkspaceFileHTTPRoutesAndVersionConflict(t *testing.T) {
	env := newResourceServerEnv(t)
	base := "/v1/workspaces/" + env.workspace
	resp := env.request(t, http.MethodGet, base+"/entries?path=", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("entries status = %d", resp.StatusCode)
	}
	var entries []desktopapi.FileEntryDTO
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(entries) != 1 || entries[0].Path != "README.md" {
		t.Fatalf("entries = %+v", entries)
	}

	resp = env.request(t, http.MethodGet, base+"/file?path="+url.QueryEscape("README.md"), nil)
	var doc desktopapi.FileDocumentDTO
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || doc.Version == "" {
		t.Fatalf("file status/doc = %d %+v", resp.StatusCode, doc)
	}
	if err := os.WriteFile(filepath.Join(env.root, "README.md"), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = env.request(t, http.MethodPut, base+"/file", desktopapi.WriteFileRequest{
		CommandID: "cmd-1", Path: "README.md", IfMatch: doc.Version, ContentBase64: "bWluZQ==",
	})
	defer resp.Body.Close()
	var problem desktopapi.Problem
	if err := json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict || problem.Code != desktopapi.ProblemVersionConflict {
		t.Fatalf("conflict = %d %+v", resp.StatusCode, problem)
	}
}

func TestWorkspaceGitStatusHTTPRoute(t *testing.T) {
	env := newResourceServerEnv(t)
	runResourceGit(t, env.root, "init", "-q", "-b", "main")
	runResourceGit(t, env.root, "config", "user.email", "test@example.com")
	runResourceGit(t, env.root, "config", "user.name", "test")
	runResourceGit(t, env.root, "add", "README.md")
	runResourceGit(t, env.root, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(env.root, "README.md"), []byte("modified"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := env.request(t, http.MethodGet, "/v1/workspaces/"+env.workspace+"/git/status", nil)
	defer resp.Body.Close()
	var status desktopapi.GitStatusSnapshotDTO
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !status.IsRepository || status.Branch != "main" || len(status.Entries) != 1 {
		t.Fatalf("git status = %d %+v", resp.StatusCode, status)
	}
}

func TestWorkspaceFileSearchAndStreamLiveEvent(t *testing.T) {
	env := newResourceServerEnv(t)
	base := "/v1/workspaces/" + env.workspace
	resp := env.request(t, http.MethodPost, base+"/files/search", desktopapi.SearchFilesRequest{Query: "resource", MaxResults: 10})
	var result desktopapi.FileSearchResultDTO
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(result.Matches) != 1 {
		t.Fatalf("search = %d %+v", resp.StatusCode, result)
	}

	wsURL := "ws" + env.server.URL[len("http"):] + base + "/files/stream?after=0"
	header := http.Header{"Authorization": []string{"Bearer " + env.token}}
	conn, response, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if response != nil {
			t.Fatalf("dial status=%d err=%v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	var subscribed desktopapi.FileStreamFrameDTO
	if err := wsReadJSON(t, conn, &subscribed); err != nil {
		t.Fatal(err)
	}
	if subscribed.Kind != "subscribed" || subscribed.WorkspaceID != env.workspace {
		t.Fatalf("subscribed = %+v", subscribed)
	}
	if err := os.WriteFile(filepath.Join(env.root, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	var live desktopapi.FileStreamFrameDTO
	if err := wsReadJSON(t, conn, &live); err != nil {
		t.Fatal(err)
	}
	if live.Kind != "event" || live.Event == nil || live.Event.Path != "new.txt" || live.Event.Seq == 0 {
		t.Fatalf("live = %+v", live)
	}
}

func TestWorkspaceFileRoutesThroughRemoteOwnerAgentd(t *testing.T) {
	remote := newResourceServerEnv(t)
	localStore, err := store.Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer localStore.Close()
	_, err = localStore.SyncConfiguredMachines(context.Background(), []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机", Kind: controlplane.MachineKindRemote,
		Endpoint: remote.server.URL, SecretRef: "config.targets.devbox.token",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.SetMachineProtocolCapabilities(context.Background(), "devbox", 1, map[string]int{
		"catalog": 1, "machine_events": 1, peer.CapabilityFiles: 1, peer.CapabilityGit: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := localStore.SetMachineStatus(context.Background(), "devbox", controlplane.MachineStatusConnected); err != nil {
		t.Fatal(err)
	}
	projected := controlplane.Workspace{
		ID: remote.workspace, MachineID: "devbox", Kind: controlplane.WorkspaceKindMain,
		Path: "/last-known/remote/path", CanonicalPath: "/last-known/remote/path",
		Availability: controlplane.AvailabilityAvailable, LastScannedAt: time.Now().UTC(),
	}
	if _, err := localStore.UpsertWorkspaceWithMachineEvent(context.Background(), projected, controlplane.MachineEventWorkspaceUpsert); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := peer.NewAuthorityRegistry([]peer.SyncMachine{{
		MachineID: "devbox", Endpoint: remote.server.URL, SecretRef: "config.targets.devbox.token",
	}}, func(string) string { return remote.token })
	defer registry.Close()
	router := resourcegateway.NewRouter(localStore, machineauthority.NewResourceAuthority(logger), registry, logger)
	server := NewServer(&config.Config{Token: "local-token"}, localStore, logger)
	server.SetResourceRouter(router)
	localHTTP := httptest.NewServer(server.Handler())
	defer localHTTP.Close()
	req, _ := http.NewRequest(http.MethodGet, localHTTP.URL+"/v1/workspaces/"+remote.workspace+"/file?path=README.md", nil)
	req.Header.Set("Authorization", "Bearer local-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc desktopapi.FileDocumentDTO
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || doc.WorkspaceID != remote.workspace || doc.Version == "" {
		t.Fatalf("remote file = status:%d doc:%+v", resp.StatusCode, doc)
	}

	wsURL := "ws" + localHTTP.URL[len("http"):] + "/v1/workspaces/" + remote.workspace + "/files/stream?after=0"
	conn, wsResponse, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer local-token"}},
	})
	if err != nil {
		if wsResponse != nil {
			t.Fatalf("remote stream dial status=%d err=%v", wsResponse.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test done")
	var subscribed desktopapi.FileStreamFrameDTO
	if err := wsReadJSON(t, conn, &subscribed); err != nil || subscribed.Kind != "subscribed" {
		t.Fatalf("remote stream subscribed = %+v, %v", subscribed, err)
	}
	if err := os.WriteFile(filepath.Join(remote.root, "remote-live.txt"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	var live desktopapi.FileStreamFrameDTO
	if err := wsReadJSON(t, conn, &live); err != nil {
		t.Fatal(err)
	}
	if live.Event == nil || live.Event.Path != "remote-live.txt" {
		t.Fatalf("remote live = %+v", live)
	}

	// Git 状态必须沿同一条 local agentd -> peer -> owner agentd 路径读取；
	// 不能因为文件路由已成功就回退到本机目录或 SSH。
	runResourceGit(t, remote.root, "init", "-q", "-b", "main")
	runResourceGit(t, remote.root, "config", "user.email", "test@example.com")
	runResourceGit(t, remote.root, "config", "user.name", "test")
	runResourceGit(t, remote.root, "add", "README.md")
	runResourceGit(t, remote.root, "commit", "-q", "-m", "init")
	gitReq, _ := http.NewRequest(http.MethodGet, localHTTP.URL+"/v1/workspaces/"+remote.workspace+"/git/status", nil)
	gitReq.Header.Set("Authorization", "Bearer local-token")
	gitResp, err := http.DefaultClient.Do(gitReq)
	if err != nil {
		t.Fatal(err)
	}
	defer gitResp.Body.Close()
	var gitStatus desktopapi.GitStatusSnapshotDTO
	if err := json.NewDecoder(gitResp.Body).Decode(&gitStatus); err != nil {
		t.Fatal(err)
	}
	if gitResp.StatusCode != http.StatusOK || !gitStatus.IsRepository || gitStatus.Branch != "main" {
		t.Fatalf("remote git = status:%d snapshot:%+v", gitResp.StatusCode, gitStatus)
	}
}

func wsReadJSON(t *testing.T, conn *websocket.Conn, out any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func runResourceGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
