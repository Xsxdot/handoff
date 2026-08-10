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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/ptyservice"
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
	store     *store.Store
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
	terminal, err := ptyservice.NewService(st, local.ID, logger)
	if err != nil {
		t.Fatal(err)
	}
	authority.SetTerminalService(terminal)
	router := resourcegateway.NewRouter(st, authority, noPeerResources{}, logger)
	srv := NewServer(&config.Config{Token: "resource-token"}, st, logger)
	srv.SetResourceRouter(router)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); terminal.Close(); st.Close() })
	return &resourceServerEnv{server: ts, workspace: ws.ID, root: root, token: "resource-token", store: st}
}

func TestWorkspacePtyHTTPAndWebSocketLifecycle(t *testing.T) {
	env := newResourceServerEnv(t)
	base := "/v1/workspaces/" + env.workspace
	response := env.request(t, http.MethodPost, base+"/terminals", desktopapi.CreateTerminalRequest{
		CommandID: "command-http-1", Cols: 90, Rows: 32,
	})
	defer response.Body.Close()
	var session desktopapi.PtySessionDTO
	if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || session.State != "active" || session.Incarnation == "" {
		t.Fatalf("create terminal = status:%d session:%+v", response.StatusCode, session)
	}

	conn := dialPtyStream(t, env, session, 0)
	var subscribed desktopapi.PtyServerFrameDTO
	if err := wsReadJSON(t, conn, &subscribed); err != nil {
		t.Fatal(err)
	}
	if subscribed.Kind != "subscribed" || subscribed.TerminalSessionID != session.TerminalSessionID ||
		subscribed.Incarnation != session.Incarnation || subscribed.Capabilities["input"] != 1 {
		t.Fatalf("subscribed = %+v", subscribed)
	}
	// 先发一个超过 coder/websocket 默认 32 KiB 的合法控制帧，证明 adapter 的
	// read limit 与 service 1 MiB input 边界一致。未知 padding 由 DTO 忽略。
	writePtyClientFrameWithPadding(t, conn, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "ack",
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
	}, strings.Repeat("x", 40<<10))
	writePtyClientFrame(t, conn, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "input",
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("printf '__AGENTD_PTY__\\n'\n")),
	})
	lastSeq := readPtyUntil(t, conn, "__AGENTD_PTY__")
	conn.Close(websocket.StatusNormalClosure, "disconnect only")

	get := env.request(t, http.MethodGet, "/v1/terminals/"+session.TerminalSessionID, nil)
	defer get.Body.Close()
	var stillActive desktopapi.PtySessionDTO
	if err := json.NewDecoder(get.Body).Decode(&stillActive); err != nil {
		t.Fatal(err)
	}
	if get.StatusCode != http.StatusOK || stillActive.State != "active" {
		t.Fatalf("disconnect must not stop PTY = status:%d session:%+v", get.StatusCode, stillActive)
	}

	reconnected := dialPtyStream(t, env, session, lastSeq)
	defer reconnected.CloseNow()
	if err := wsReadJSON(t, reconnected, &subscribed); err != nil || subscribed.Kind != "subscribed" {
		t.Fatalf("reconnect subscribed = %+v, %v", subscribed, err)
	}
	writePtyClientFrame(t, reconnected, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "resize",
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation, Cols: 100, Rows: 41,
	})
	writePtyClientFrame(t, reconnected, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "input",
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("printf '__SIZE__'; stty size\n")),
	})
	readPtyUntil(t, reconnected, "41 100")

	closed := env.request(t, http.MethodDelete, "/v1/terminals/"+session.TerminalSessionID+
		"?incarnation="+url.QueryEscape(session.Incarnation), nil)
	defer closed.Body.Close()
	var ended desktopapi.PtySessionDTO
	if err := json.NewDecoder(closed.Body).Decode(&ended); err != nil {
		t.Fatal(err)
	}
	if closed.StatusCode != http.StatusOK || ended.State != "ended" {
		t.Fatalf("close terminal = status:%d session:%+v", closed.StatusCode, ended)
	}
}

func TestPeerReceivesProblemWhenOwnerCannotPersistTerminalExit(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	machine, err := st.EnsureLocalMachine(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.ResolveWorkspaceForPath(context.Background(), machine.ID, root, root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := &rejectingPtyExitRepository{Store: st}
	terminal, err := ptyservice.NewService(repo, machine.ID, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	authority := machineauthority.NewResourceAuthority(logger)
	authority.SetTerminalService(terminal)
	router := resourcegateway.NewRouter(st, authority, noPeerResources{}, logger)
	srv := NewServer(&config.Config{Token: "owner-token"}, st, logger)
	srv.SetResourceRouter(router)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := peer.NewClient(peer.ClientConfig{Endpoint: ts.URL, Token: "owner-token"})
	session, err := client.CreateTerminal(context.Background(), workspaceapi.WorkspaceRef{WorkspaceID: workspace.ID},
		workspaceapi.CreateTerminalCommand{CommandID: "exit-persistence-failure", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := client.ConnectTerminal(context.Background(), session.TerminalSessionID, session.Incarnation, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	if err := subscription.Send(context.Background(), workspaceapi.PtyClientFrame{
		Version: 1, Kind: workspaceapi.PtyClientFrameInput,
		TerminalSessionID: session.TerminalSessionID, Incarnation: session.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("exit\n")),
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case frame, ok := <-subscription.Events:
			if !ok {
				goto streamClosed
			}
			if frame.Kind == workspaceapi.PtyFrameExit {
				t.Fatalf("owner persistence failure leaked exit before problem: %+v", frame)
			}
		case <-deadline.C:
			t.Fatal("等待 owner persistence problem 超时")
		}
	}

streamClosed:
	select {
	case streamErr := <-subscription.Done:
		if streamErr == nil {
			t.Fatal("owner persistence failure 被报告为正常结束")
		}
	case <-time.After(time.Second):
		t.Fatal("peer 未收到 owner persistence problem")
	}
}

type rejectingPtyExitRepository struct{ *store.Store }

func (r *rejectingPtyExitRepository) UpdatePtySessionWithMachineEvent(ctx context.Context, machineID string,
	session workspaceapi.PtySession, kind controlplane.MachineEventKind) (controlplane.MachineEvent, error) {
	if kind == controlplane.MachineEventPtyExit {
		return controlplane.MachineEvent{}, fmt.Errorf("injected durable PTY exit failure")
	}
	return r.Store.UpdatePtySessionWithMachineEvent(ctx, machineID, session, kind)
}

func dialPtyStream(t *testing.T, env *resourceServerEnv, session desktopapi.PtySessionDTO, after int64) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + env.server.URL[len("http"):] + "/v1/terminals/" + session.TerminalSessionID +
		"/stream?incarnation=" + url.QueryEscape(session.Incarnation) + "&after=" + fmt.Sprint(after)
	conn, response, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + env.token}},
	})
	if err != nil {
		if response != nil {
			t.Fatalf("PTY stream status=%d err=%v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	return conn
}

func writePtyClientFrame(t *testing.T, conn *websocket.Conn, frame desktopapi.PtyClientFrameDTO) {
	t.Helper()
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

func writePtyClientFrameWithPadding(t *testing.T, conn *websocket.Conn,
	frame desktopapi.PtyClientFrameDTO, padding string) {
	t.Helper()
	raw, err := json.Marshal(struct {
		desktopapi.PtyClientFrameDTO
		Padding string `json:"padding"`
	}{PtyClientFrameDTO: frame, Padding: padding})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}

func readPtyUntil(t *testing.T, conn *websocket.Conn, needle string) int64 {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var output strings.Builder
	var lastSeq int64
	for time.Now().Before(deadline) {
		var frame desktopapi.PtyServerFrameDTO
		if err := wsReadJSON(t, conn, &frame); err != nil {
			t.Fatalf("读取 PTY frame 失败: %v; 已收到输出=%q last_seq=%d", err, output.String(), lastSeq)
		}
		if frame.Kind != "data" && frame.Kind != "snapshot" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(frame.DataBase64)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(data)
		lastSeq = frame.Seq
		if strings.Contains(output.String(), needle) {
			return lastSeq
		}
	}
	t.Fatalf("PTY output %q 未包含 %q", output.String(), needle)
	return 0
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
		"catalog": 1, "machine_events": 1, peer.CapabilityFiles: 1, peer.CapabilityGit: 1, peer.CapabilityPty: 1,
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

	// 普通终端同样必须沿 local agentd -> peer WebSocket -> owner agentd 双向代理，
	// 本机不得按 last-known remote path 启动 shell。
	ptyBody, err := json.Marshal(desktopapi.CreateTerminalRequest{CommandID: "remote-command-1", Cols: 88, Rows: 29})
	if err != nil {
		t.Fatal(err)
	}
	ptyReq, _ := http.NewRequest(http.MethodPost, localHTTP.URL+"/v1/workspaces/"+remote.workspace+"/terminals", bytes.NewReader(ptyBody))
	ptyReq.Header.Set("Authorization", "Bearer local-token")
	ptyReq.Header.Set("Content-Type", "application/json")
	ptyResp, err := http.DefaultClient.Do(ptyReq)
	if err != nil {
		t.Fatal(err)
	}
	defer ptyResp.Body.Close()
	var remoteSession desktopapi.PtySessionDTO
	if err := json.NewDecoder(ptyResp.Body).Decode(&remoteSession); err != nil {
		t.Fatal(err)
	}
	if ptyResp.StatusCode != http.StatusCreated || remoteSession.State != "active" {
		t.Fatalf("remote PTY create = status:%d session:%+v", ptyResp.StatusCode, remoteSession)
	}
	ptyURL := "ws" + localHTTP.URL[len("http"):] + "/v1/terminals/" + remoteSession.TerminalSessionID +
		"/stream?incarnation=" + url.QueryEscape(remoteSession.Incarnation) + "&after=0"
	ptyConn, ptyWSResponse, err := websocket.Dial(context.Background(), ptyURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer local-token"}},
	})
	if err != nil {
		if ptyWSResponse != nil {
			t.Fatalf("remote PTY stream status=%d err=%v", ptyWSResponse.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer ptyConn.CloseNow()
	var ptySubscribed desktopapi.PtyServerFrameDTO
	if err := wsReadJSON(t, ptyConn, &ptySubscribed); err != nil || ptySubscribed.Kind != "subscribed" {
		t.Fatalf("remote PTY subscribed = %+v, %v", ptySubscribed, err)
	}
	writePtyClientFrame(t, ptyConn, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "input",
		TerminalSessionID: remoteSession.TerminalSessionID, Incarnation: remoteSession.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("printf '__REMOTE_PTY__%s\\n' \"$PWD\"\n")),
	})
	canonicalRemoteRoot, err := filepath.EvalSymlinks(remote.root)
	if err != nil {
		t.Fatal(err)
	}
	readPtyUntil(t, ptyConn, "__REMOTE_PTY__"+canonicalRemoteRoot)
	writePtyClientFrame(t, ptyConn, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "resize",
		TerminalSessionID: remoteSession.TerminalSessionID, Incarnation: remoteSession.Incarnation,
		Cols: 101, Rows: 42,
	})
	writePtyClientFrame(t, ptyConn, desktopapi.PtyClientFrameDTO{Version: 1, Kind: "input",
		TerminalSessionID: remoteSession.TerminalSessionID, Incarnation: remoteSession.Incarnation,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("printf '__REMOTE_SIZE__'; stty size\n")),
	})
	readPtyUntil(t, ptyConn, "__REMOTE_SIZE__42 101")
	closeReq, _ := http.NewRequest(http.MethodDelete, localHTTP.URL+"/v1/terminals/"+remoteSession.TerminalSessionID+
		"?incarnation="+url.QueryEscape(remoteSession.Incarnation), nil)
	closeReq.Header.Set("Authorization", "Bearer local-token")
	closeResp, err := http.DefaultClient.Do(closeReq)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResp.Body.Close()
	var remoteEnded desktopapi.PtySessionDTO
	if err := json.NewDecoder(closeResp.Body).Decode(&remoteEnded); err != nil {
		t.Fatal(err)
	}
	if closeResp.StatusCode != http.StatusOK || remoteEnded.State != "ended" {
		t.Fatalf("remote PTY close = status:%d session:%+v", closeResp.StatusCode, remoteEnded)
	}
	readPtyKind(t, ptyConn, "exit")
}

func readPtyKind(t *testing.T, conn *websocket.Conn, kind string) desktopapi.PtyServerFrameDTO {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		var frame desktopapi.PtyServerFrameDTO
		if err := wsReadJSON(t, conn, &frame); err != nil {
			t.Fatalf("等待 PTY %s frame: %v", kind, err)
		}
		if frame.Kind == kind {
			return frame
		}
	}
	t.Fatalf("未收到 PTY %s frame", kind)
	return desktopapi.PtyServerFrameDTO{}
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
