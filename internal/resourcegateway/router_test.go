// resourcegateway Router 路由与离线门禁测试。
//
// 职责：
//   - 锁定 local Workspace 只走 local Authority、remote 只走 peer resolver
//   - 锁定未知资源、离线机器和缺 capability 的稳定 Problem code
//   - 证明 peer resolver 只接收 machine_id，不接触 endpoint 或 secret
//
// 边界：
//   - 使用内存 fake 验证路由决策，不执行文件、PTY 或网络 I/O
package resourcegateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

type routerCatalogFake struct {
	workspaces map[string]controlplane.Workspace
	machines   map[string]controlplane.Machine
}

func (f *routerCatalogFake) GetWorkspace(_ context.Context, id string) (controlplane.Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return controlplane.Workspace{}, controlplane.ErrNotFound
	}
	return ws, nil
}

func (f *routerCatalogFake) GetMachine(_ context.Context, id string) (controlplane.Machine, error) {
	machine, ok := f.machines[id]
	if !ok {
		return controlplane.Machine{}, controlplane.ErrNotFound
	}
	return machine, nil
}

type authorityFake struct {
	listCalls       []workspaceapi.WorkspaceRef
	readCalls       []workspaceapi.WorkspaceRef
	connectCalls    []string
	closeCalls      []string
	terminalSession workspaceapi.PtySession
	listErr         error
}

func (f *authorityFake) ListDirectory(_ context.Context, ws workspaceapi.WorkspaceRef, _ string) ([]workspaceapi.FileEntry, error) {
	f.listCalls = append(f.listCalls, ws)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return []workspaceapi.FileEntry{{WorkspaceID: ws.WorkspaceID, Path: "README.md", Name: "README.md", Kind: workspaceapi.FileKindFile}}, nil
}

func (f *authorityFake) ReadFile(_ context.Context, ws workspaceapi.WorkspaceRef, path string) (workspaceapi.FileDocument, error) {
	f.readCalls = append(f.readCalls, ws)
	return workspaceapi.FileDocument{WorkspaceID: ws.WorkspaceID, Path: path, Version: "v1"}, nil
}

func (f *authorityFake) WriteFile(context.Context, workspaceapi.WorkspaceRef, workspaceapi.WriteFileCommand) (workspaceapi.FileDocument, error) {
	return workspaceapi.FileDocument{}, nil
}

func (f *authorityFake) SearchFiles(context.Context, workspaceapi.WorkspaceRef, workspaceapi.SearchFilesCommand) (workspaceapi.FileSearchResult, error) {
	return workspaceapi.FileSearchResult{}, nil
}

func (f *authorityFake) GitStatus(context.Context, workspaceapi.WorkspaceRef) (workspaceapi.GitStatusSnapshot, error) {
	return workspaceapi.GitStatusSnapshot{}, nil
}

func (f *authorityFake) CreateTerminal(context.Context, workspaceapi.WorkspaceRef, workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	return workspaceapi.PtySession{}, nil
}

func (f *authorityFake) GetTerminal(context.Context, string) (workspaceapi.PtySession, error) {
	return workspaceapi.PtySession{}, nil
}

func (f *authorityFake) ConnectTerminal(_ context.Context, sessionID, incarnation string, after int64) (*workspaceapi.PtySubscription, error) {
	f.connectCalls = append(f.connectCalls, sessionID+":"+incarnation+":"+fmt.Sprint(after))
	return workspaceapi.NewPtySubscription(f.terminalSession, nil, nil, nil, false, nil, nil, nil), nil
}

func (f *authorityFake) CloseTerminal(_ context.Context, sessionID, incarnation string) (workspaceapi.PtySession, error) {
	f.closeCalls = append(f.closeCalls, sessionID+":"+incarnation)
	return f.terminalSession, nil
}

func TestRouterRoutesPtyConnectAndCloseToWorkspaceOwner(t *testing.T) {
	terminal := workspaceapi.PtySession{TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-remote"}
	remote := &authorityFake{terminalSession: terminal}
	repo := &routerCatalogFake{
		workspaces: map[string]controlplane.Workspace{
			"ws-remote": {ID: "ws-remote", MachineID: "m-remote", Path: "/remote", Availability: controlplane.AvailabilityAvailable},
		},
		machines: map[string]controlplane.Machine{
			"m-remote": {ID: "m-remote", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusConnected,
				Capabilities: map[string]int{"pty": 1}},
		},
	}
	router := NewRouter(repo, &authorityFake{}, &peerResolverFake{authority: remote}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	subscription, err := router.ConnectTerminal(context.Background(), "ws-remote", "term-1", "inc-1", 7)
	if err != nil || subscription.Session.WorkspaceID != "ws-remote" {
		t.Fatalf("ConnectTerminal = %+v, %v", subscription, err)
	}
	closed, err := router.CloseTerminal(context.Background(), "ws-remote", "term-1", "inc-1")
	if err != nil || closed.TerminalSessionID != "term-1" {
		t.Fatalf("CloseTerminal = %+v, %v", closed, err)
	}
	if !reflect.DeepEqual(remote.connectCalls, []string{"term-1:inc-1:7"}) ||
		!reflect.DeepEqual(remote.closeCalls, []string{"term-1:inc-1"}) {
		t.Fatalf("PTY owner calls = connect %v close %v", remote.connectCalls, remote.closeCalls)
	}
}

func (f *authorityFake) CreatePreview(context.Context, workspaceapi.WorkspaceRef, workspaceapi.CreatePreviewCommand) (workspaceapi.PreviewSession, error) {
	return workspaceapi.PreviewSession{}, nil
}

type peerResolverFake struct {
	authority  workspaceapi.Authority
	machineIDs []string
}

func (f *peerResolverFake) AuthorityForMachine(_ context.Context, machineID string) (workspaceapi.Authority, error) {
	f.machineIDs = append(f.machineIDs, machineID)
	return f.authority, nil
}

func TestRouterRoutesLocalAndRemoteOwners(t *testing.T) {
	local := &authorityFake{}
	remote := &authorityFake{}
	peers := &peerResolverFake{authority: remote}
	repo := &routerCatalogFake{
		workspaces: map[string]controlplane.Workspace{
			"ws-local":  {ID: "ws-local", MachineID: "m-local", Path: "/local", Availability: controlplane.AvailabilityAvailable},
			"ws-remote": {ID: "ws-remote", MachineID: "m-remote", Path: "/remote", Availability: controlplane.AvailabilityAvailable},
		},
		machines: map[string]controlplane.Machine{
			"m-local":  {ID: "m-local", Kind: controlplane.MachineKindLocal, Status: controlplane.MachineStatusConnected, Capabilities: map[string]int{"files": 1}},
			"m-remote": {ID: "m-remote", Kind: controlplane.MachineKindRemote, Endpoint: "http://secret-host:7777", SecretRef: "config.targets.devbox.token", Status: controlplane.MachineStatusConnected, Capabilities: map[string]int{"files": 1}},
		},
	}
	router := NewRouter(repo, local, peers, slog.New(slog.NewTextHandler(io.Discard, nil)))

	entries, err := router.ListDirectory(context.Background(), "ws-local", "")
	if err != nil {
		t.Fatalf("ListDirectory local: %v", err)
	}
	if len(entries) != 1 || entries[0].WorkspaceID != "ws-local" || len(local.listCalls) != 1 {
		t.Fatalf("local entries/calls = %+v / %+v", entries, local.listCalls)
	}
	if len(peers.machineIDs) != 0 {
		t.Fatalf("local 路由不应触达 peer: %+v", peers.machineIDs)
	}

	doc, err := router.ReadFile(context.Background(), "ws-remote", "README.md")
	if err != nil {
		t.Fatalf("ReadFile remote: %v", err)
	}
	if doc.WorkspaceID != "ws-remote" || len(remote.readCalls) != 1 {
		t.Fatalf("remote doc/calls = %+v / %+v", doc, remote.readCalls)
	}
	if len(peers.machineIDs) != 1 || peers.machineIDs[0] != "m-remote" {
		t.Fatalf("peer resolver 只应收到 machine_id: %+v", peers.machineIDs)
	}
}

func TestRouterRejectsUnknownOfflineAndUnsupportedResources(t *testing.T) {
	cases := []struct {
		name        string
		workspace   controlplane.Workspace
		machine     controlplane.Machine
		workspaceID string
		wantCode    desktopapi.ProblemCode
	}{
		{name: "unknown workspace", workspaceID: "missing", wantCode: desktopapi.ProblemResourceNotFound},
		{name: "offline machine", workspaceID: "ws", workspace: controlplane.Workspace{ID: "ws", MachineID: "m", Availability: controlplane.AvailabilityAvailable}, machine: controlplane.Machine{ID: "m", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusUnavailable, Capabilities: map[string]int{"files": 1}}, wantCode: desktopapi.ProblemMachineOffline},
		{name: "incompatible machine", workspaceID: "ws", workspace: controlplane.Workspace{ID: "ws", MachineID: "m", Availability: controlplane.AvailabilityAvailable}, machine: controlplane.Machine{ID: "m", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusIncompatible, Capabilities: map[string]int{"files": 1}}, wantCode: desktopapi.ProblemCapabilityUnsupported},
		{name: "missing capability", workspaceID: "ws", workspace: controlplane.Workspace{ID: "ws", MachineID: "m", Availability: controlplane.AvailabilityAvailable}, machine: controlplane.Machine{ID: "m", Kind: controlplane.MachineKindRemote, Status: controlplane.MachineStatusConnected, Capabilities: map[string]int{}}, wantCode: desktopapi.ProblemCapabilityUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &routerCatalogFake{workspaces: map[string]controlplane.Workspace{}, machines: map[string]controlplane.Machine{}}
			if tc.workspace.ID != "" {
				repo.workspaces[tc.workspace.ID] = tc.workspace
			}
			if tc.machine.ID != "" {
				repo.machines[tc.machine.ID] = tc.machine
			}
			router := NewRouter(repo, &authorityFake{}, &peerResolverFake{authority: &authorityFake{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err := router.ListDirectory(context.Background(), tc.workspaceID, "")
			var problemErr *desktopapi.ProblemError
			if !errors.As(err, &problemErr) {
				t.Fatalf("error = %T %v, want *ProblemError", err, err)
			}
			if problemErr.Problem.Code != tc.wantCode {
				t.Fatalf("code = %s, want %s", problemErr.Problem.Code, tc.wantCode)
			}
		})
	}
}

func TestRouterNeverLeaksUntypedAuthorityErrors(t *testing.T) {
	authority := &authorityFake{listErr: errors.New("sensitive owner failure")}
	repo := &routerCatalogFake{
		workspaces: map[string]controlplane.Workspace{
			"ws": {ID: "ws", MachineID: "m", Path: "/local", Availability: controlplane.AvailabilityAvailable},
		},
		machines: map[string]controlplane.Machine{
			"m": {ID: "m", Kind: controlplane.MachineKindLocal, Status: controlplane.MachineStatusConnected, Capabilities: map[string]int{"files": 1}},
		},
	}
	router := NewRouter(repo, authority, &peerResolverFake{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := router.ListDirectory(context.Background(), "ws", "")
	var problemErr *desktopapi.ProblemError
	if !errors.As(err, &problemErr) {
		t.Fatalf("error = %T %v, want *ProblemError", err, err)
	}
	if problemErr.Problem.Code != desktopapi.ProblemLocalAgentdUnavailable || problemErr.Error() == authority.listErr.Error() {
		t.Fatalf("problem error = %+v", problemErr)
	}
}
