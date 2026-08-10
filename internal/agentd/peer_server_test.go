// agentd peer_server 测试：peer v1 路由的鉴权与线格式。
//
// 职责：
//   - GET /v1/peer/hello 返回 protocol version 与 capability map
//   - 未授权请求 401
//
// 边界：
//   - 不覆盖完整 catch-up（由 internal/peer 包测试负责）
//   - 使用真实 store + httptest
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/store"
)

// newPeerTestServer 组装带 peer 路由的测试 server。
func newPeerTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test-token"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// TestPeerHelloRoute 验证 hello 路由返回协议版本与 capability。
func TestPeerHelloRoute(t *testing.T) {
	_, ts := newPeerTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/peer/hello", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET hello: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var hello struct {
		ProtocolVersion int            `json:"protocol_version"`
		Capabilities    map[string]int `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if hello.ProtocolVersion != 1 {
		t.Fatalf("protocol_version = %d, want 1", hello.ProtocolVersion)
	}
	if hello.Capabilities["catalog"] != 1 || hello.Capabilities["machine_events"] != 1 {
		t.Fatalf("capabilities 缺核心项: %+v", hello.Capabilities)
	}
	if hello.Capabilities[peer.CapabilityFiles] != 1 {
		t.Fatalf("files capability 缺失: %+v", hello.Capabilities)
	}
	if hello.Capabilities[peer.CapabilityGit] != 1 {
		t.Fatalf("git capability 缺失: %+v", hello.Capabilities)
	}
	if hello.Capabilities[peer.CapabilityProjectCommands] != 1 {
		t.Fatalf("project_commands capability 缺失: %+v", hello.Capabilities)
	}
}

// TestPeerHelloRequiresAuth 验证未授权 401。
func TestPeerHelloRequiresAuth(t *testing.T) {
	_, ts := newPeerTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/peer/hello")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestPeerEventsAfterRoute 验证 events-after 路由存在且带鉴权。
func TestPeerEventsAfterRoute(t *testing.T) {
	_, ts := newPeerTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/machine/events?machine_id=m1&after=0", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

type projectAuthorityFake struct {
	inspectPath string
	clone       machineauthority.CloneCommand
}

func (f *projectAuthorityFake) InspectPath(_ context.Context, path string) (controlplane.PathInspection, error) {
	f.inspectPath = path
	return controlplane.PathInspection{Path: path, CanonicalPath: path, IsRepo: true, RepoIdentity: "github.com/o/r"}, nil
}

func (f *projectAuthorityFake) Clone(_ context.Context, command machineauthority.CloneCommand) (controlplane.PathInspection, error) {
	f.clone = command
	return controlplane.PathInspection{Path: command.ClonePath, CanonicalPath: command.ClonePath, IsRepo: true}, nil
}

// TestPeerProjectCommandsUseAgentdProtocol 验证远端目录检查/clone 由 agentd peer
// API 执行，不依赖 SSH、远端 shell PATH 或平台探针。
func TestPeerProjectCommandsUseAgentdProtocol(t *testing.T) {
	srv, ts := newPeerTestServer(t)
	authority := &projectAuthorityFake{}
	srv.SetMachineAuthority(authority)
	client := peer.NewClient(peer.ClientConfig{Endpoint: ts.URL, Token: "test-token"})

	inspection, err := client.InspectPath(context.Background(), controlplane.InspectPathCommand{
		OperationID: "op", TargetID: "inspect", MachineID: "remote", Path: "/srv/repo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if authority.inspectPath != "/srv/repo" || inspection.RepoIdentity != "github.com/o/r" {
		t.Fatalf("inspect path=%q result=%+v", authority.inspectPath, inspection)
	}
	clone, err := client.Clone(context.Background(), controlplane.CloneLocationCommand{
		OperationID: "op", TargetID: "clone", MachineID: "remote",
		GitURL: "git@github.com:o/r.git", ClonePath: "/srv/clone",
	})
	if err != nil {
		t.Fatal(err)
	}
	if authority.clone.GitURL != "git@github.com:o/r.git" || clone.Path != "/srv/clone" {
		t.Fatalf("clone command=%+v result=%+v", authority.clone, clone)
	}
}
