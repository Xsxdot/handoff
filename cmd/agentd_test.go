// agentd 命令测试：HTTP server 超时配置（P1-3）。
//
// 覆盖：newAgentdHTTPServer 的四个超时字段全部非零——这是「防 slowloris / 防
// 半死连接挂起」的配置级守卫；另断言 WriteTimeout ≥ agentd.RunCmdTimeout——
// handleTaskRun 同步执行 RunCmd，写超时小于命令执行上限会把长审阅命令掐断
// （退出码 124 契约无法兑现，见 cmd/agentd.go newAgentdHTTPServer 注释）。
// http.Server 超时行为本身由 net/http 保证，httptest 用自己的 server 无法覆盖，
// 故只做配置存在性断言（why 见 P1-3 修法）。
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/store"
)

// 注册表必须认识全部执行者名：dispatch --executor <name> 的路由前提。
//
// 为什么每个名字都要断言而不是只断言数量：B2（claude）与 B3（grok）是并行开发的
// 两条分支，各自往注册表里加了一行，合并时 cmd/agentd.go 这一处必然冲突——手工
// 解冲突时漏掉任一行都不会编译报错，症状要拖到「派发时报未注册」才暴露。
func TestAdapterRegistryHasAllExecutors(t *testing.T) {
	ads := defaultAdapters(slog.Default())
	for _, want := range []string{"opencode", "claude", "grok", "fake"} {
		if _, ok := ads[want]; !ok {
			names := make([]string, 0, len(ads))
			for n := range ads {
				names = append(names, n)
			}
			t.Fatalf("adapter 注册表缺 %s，实际注册: %v", want, names)
		}
	}
}

func TestNewAgentdHTTPServerTimeouts(t *testing.T) {
	s := newAgentdHTTPServer("127.0.0.1:0", http.NewServeMux())
	if s.Addr != "127.0.0.1:0" {
		t.Errorf("Addr=%q, want 127.0.0.1:0", s.Addr)
	}
	if s.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout 必须非零（防 slowloris），实际 %v", s.ReadHeaderTimeout)
	}
	if s.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout 必须非零（请求体读取上限），实际 %v", s.ReadTimeout)
	}
	if s.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout 必须非零（响应写入上限），实际 %v", s.WriteTimeout)
	}
	if s.WriteTimeout < agentd.RunCmdTimeout {
		t.Errorf("WriteTimeout %v 必须 >= run 路由执行上限 %v（否则长审阅命令被掐断）",
			s.WriteTimeout, agentd.RunCmdTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout 必须非零（keep-alive 空闲回收），实际 %v", s.IdleTimeout)
	}
}

// TestConfiguredMachinesFromConfigSecretRefOnly 验证 targets 投影为 ConfiguredMachine
// 时只带 secret_ref 引用（config.targets.<name>.token），不落 token 值。
func TestConfiguredMachinesFromConfigSecretRefOnly(t *testing.T) {
	cfg := &config.Config{Targets: map[string]config.Target{
		"devbox": {Addr: "http://10.0.0.5:7777", Token: "super-secret-token", DisplayName: "开发机"},
	}}
	configured, err := configuredMachinesFromConfig(cfg)
	if err != nil {
		t.Fatalf("configuredMachinesFromConfig: %v", err)
	}
	if len(configured) != 1 {
		t.Fatalf("configured = %d, want 1", len(configured))
	}
	cm := configured[0]
	if cm.ConfigKey != "devbox" || cm.Endpoint != "http://10.0.0.5:7777" || cm.DisplayName != "开发机" {
		t.Fatalf("configured = %+v", cm)
	}
	if cm.Kind != controlplane.MachineKindRemote {
		t.Fatalf("kind = %s, want remote", cm.Kind)
	}
	if cm.SecretRef != "config.targets.devbox.token" {
		t.Fatalf("secret_ref = %q, want config.targets.devbox.token", cm.SecretRef)
	}
}

func TestWireWorkspaceResourcesActivatesProductionServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("wired"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	local, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := st.ResolveWorkspaceForPath(context.Background(), local.ID, root, root)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "token", Targets: map[string]config.Target{}}
	server := agentd.NewServer(cfg, st, logger)
	_, closeResources, err := wireWorkspaceResources(server, st, cfg, local.ID, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResources()
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/workspaces/"+workspace.ID+"/entries", nil)
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource route status = %d", resp.StatusCode)
	}
	terminalBody, err := json.Marshal(desktopapi.CreateTerminalRequest{CommandID: "production-pty-1", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	terminalReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/workspaces/"+workspace.ID+"/terminals", bytes.NewReader(terminalBody))
	terminalReq.Header.Set("Authorization", "Bearer token")
	terminalReq.Header.Set("Content-Type", "application/json")
	terminalResp, err := http.DefaultClient.Do(terminalReq)
	if err != nil {
		t.Fatal(err)
	}
	defer terminalResp.Body.Close()
	var session desktopapi.PtySessionDTO
	if err := json.NewDecoder(terminalResp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if terminalResp.StatusCode != http.StatusCreated || session.State != "active" {
		t.Fatalf("production PTY route = status:%d session:%+v", terminalResp.StatusCode, session)
	}
}

// TestWireWorkspaceResourcesActivatesProjectCreation 防止生产 agentd 只接通
// files/git，却漏注入 ProjectService。该缺口会让真实桌面创建项目恒定返回 503，
// 而 handler 单测因手工注入 fake service 无法发现。
func TestWireWorkspaceResourcesActivatesProjectCreation(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	local, err := st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "token", Targets: map[string]config.Target{}}
	server := agentd.NewServer(cfg, st, logger)
	var published []controlplane.ControlEventKind
	_, closeResources, err := wireWorkspaceResources(server, st, cfg, local.ID, logger, func(event controlplane.ControlEvent) {
		published = append(published, event.Kind)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeResources()
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body, err := json.Marshal(map[string]any{
		"operation_id": "op-production-wire",
		"name":         "wired-project",
		"locations": []map[string]string{{
			"machine_id": local.ID,
			"role":       "local",
			"source":     "existing_path",
			"path":       root,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/projects/operations", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("project route status = %d, want 202: %s", resp.StatusCode, payload)
	}
	var operation struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&operation); err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" {
		t.Fatalf("operation state = %q, want succeeded", operation.State)
	}
	snapshot, err := st.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projects) != 1 || len(snapshot.Locations) != 1 || len(snapshot.Workspaces) != 1 {
		t.Fatalf("catalog project/location/workspace = %d/%d/%d, want 1/1/1",
			len(snapshot.Projects), len(snapshot.Locations), len(snapshot.Workspaces))
	}
	workspace := snapshot.Workspaces[0]
	if workspace.Kind != controlplane.WorkspaceKindMain || workspace.LocationID == nil ||
		*workspace.LocationID != snapshot.Locations[0].ID {
		t.Fatalf("workspace 未归并为 main location: %+v", workspace)
	}
	wantKinds := []controlplane.ControlEventKind{
		controlplane.ControlEventKindOperationUpsert,
		controlplane.ControlEventKindOperationUpsert,
		controlplane.ControlEventKindProjectUpsert,
		controlplane.ControlEventKindLocationUpsert,
		controlplane.ControlEventKindWorkspaceUpsert,
		controlplane.ControlEventKindOperationUpsert,
	}
	if len(published) != len(wantKinds) {
		t.Fatalf("published kinds = %v, want %v", published, wantKinds)
	}
	for index, want := range wantKinds {
		if published[index] != want {
			t.Fatalf("published[%d] = %s, want %s", index, published[index], want)
		}
	}
}

// TestRemoteProjectCreationUsesPeerAgentd 验证 remote-only 项目通过已配对 agentd
// 协议检查目录；测试环境没有 SSH client，也不会执行远端 shell 探针。
func TestRemoteProjectCreationUsesPeerAgentd(t *testing.T) {
	remoteRoot := t.TempDir()
	remoteStore, err := store.Open(filepath.Join(t.TempDir(), "remote.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer remoteStore.Close()
	remoteLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	remoteServer := agentd.NewServer(&config.Config{Token: "remote-token"}, remoteStore, remoteLogger)
	remoteServer.SetMachineAuthority(&machineauthority.Inventory{})
	remoteHTTP := httptest.NewServer(remoteServer.Handler())
	defer remoteHTTP.Close()

	localStore, err := store.Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer localStore.Close()
	localMachine, err := localStore.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		t.Fatal(err)
	}
	configured := []controlplane.ConfiguredMachine{{
		ConfigKey: "devbox", DisplayName: "开发机", Kind: controlplane.MachineKindRemote,
		Endpoint: remoteHTTP.URL, SecretRef: "config.targets.devbox.token",
	}}
	if _, err := localStore.SyncConfiguredMachines(context.Background(), configured); err != nil {
		t.Fatal(err)
	}
	if err := localStore.SetMachineProtocolCapabilities(context.Background(), "devbox", 1,
		map[string]int{peer.CapabilityProjectCommands: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := localStore.SetMachineStatusWithControlEvent(context.Background(), "devbox",
		controlplane.MachineStatusConnected); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Token: "local-token", Targets: map[string]config.Target{
		"devbox": {Addr: remoteHTTP.URL, Token: "remote-token", DisplayName: "开发机"},
	}}
	localServer := agentd.NewServer(cfg, localStore, remoteLogger)
	_, closeResources, err := wireWorkspaceResources(localServer, localStore, cfg, localMachine.ID, remoteLogger, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeResources()
	localHTTP := httptest.NewServer(localServer.Handler())
	defer localHTTP.Close()

	body, err := json.Marshal(map[string]any{
		"operation_id": "op-remote-peer",
		"name":         "remote-project",
		"locations": []map[string]string{{
			"machine_id": "devbox", "role": "remote", "source": "existing_path", "path": remoteRoot,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, localHTTP.URL+"/v1/projects/operations", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer local-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("remote project status=%d: %s", response.StatusCode, payload)
	}
	var operation struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&operation); err != nil {
		t.Fatal(err)
	}
	if operation.State != "succeeded" {
		t.Fatalf("remote operation state=%q, want succeeded", operation.State)
	}
	snapshot, err := localStore.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Locations) != 1 || snapshot.Locations[0].MachineID != "devbox" ||
		len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].Path != remoteRoot {
		t.Fatalf("remote catalog projection locations=%+v workspaces=%+v",
			snapshot.Locations, snapshot.Workspaces)
	}
}
