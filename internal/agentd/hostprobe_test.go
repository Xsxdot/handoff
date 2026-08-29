// hostprobe_test.go —— host probe/wake 与 carrier detect 的真实 HTTP 入口回归。
//
// 职责：穿过 Server.Handler、JSON 编解码和 forwardJSON，锁定本机检测、远程只
// 唤起后回协调机写 registry、unknown outcome fail closed 以及 credential 透传。
// 边界：真实四家 CLI、跨机器网络和 webview 由协调者真机清单承接。
package agentd

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

func installDetectCLI(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newDirectSchedEnv(t *testing.T, cfg *config.Config) *schedEnv {
	t.Helper()
	dataStore, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	ledgerStore, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		_ = dataStore.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close(); _ = ledgerStore.Close() })
	srv := NewServer(cfg, dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetupAutomation(ledgerStore)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &schedEnv{testAgentdEnv: &testAgentdEnv{srv: srv, ts: ts, st: dataStore, token: cfg.Token}, svc: srv.Scheduling()}
}

func TestCarrierDetectThroughHandlerWritesCoordinatorState(t *testing.T) {
	target := filepath.Join(t.TempDir(), "carrier-home")
	installDetectCLI(t, `printf '%s\n' '{"type":"text","sessionID":"ses_detect","part":{"type":"text","text":"ok"}}'`)
	env := newDirectSchedEnv(t, &config.Config{Token: testToken, DataDir: t.TempDir()})
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"local","cli":"opencode","home_dir":"`+target+`","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("登记载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/c1/detect", "{}")
	if code != http.StatusOK {
		t.Fatalf("本机检测: %d %s", code, body)
	}
	var detect proto.CarrierDetectResp
	if err := json.Unmarshal([]byte(body), &detect); err != nil {
		t.Fatal(err)
	}
	if detect.Status != "online" || detect.Version != 2 || detect.Name != "c1" {
		t.Fatalf("检测回执 = %+v，want online/version2/c1", detect)
	}
	if strings.Contains(body, "healthy") {
		t.Fatalf("检测回执不得出现 healthy: %s", body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK || !strings.Contains(body, `"status":"online"`) {
		t.Fatalf("协调机 registry 未写 online: %d %s", code, body)
	}

	installDetectCLI(t, `echo "network unavailable" >&2; exit 1`)
	code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/c1/detect", "{}")
	if code != http.StatusOK || !strings.Contains(body, `"status":"unreachable"`) {
		t.Fatalf("online→unreachable = %d %s", code, body)
	}
}

func newRemoteSchedEnv(t *testing.T, handler http.Handler) (*schedEnv, *httptest.Server) {
	t.Helper()
	remote := httptest.NewServer(handler)
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		remote.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(); remote.Close() })
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Targets: map[string]config.Target{
		"remote": {Addr: remote.URL, Token: "remote-token"},
	}}
	// Do not use the shared PTY-aware helper: remote probe tests only need the HTTP
	// server, scheduling ledger and hostapi assembly.
	dataStore, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		_ = st.Close()
		remote.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	srv := NewServer(cfg, dataStore, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetupAutomation(st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &schedEnv{testAgentdEnv: &testAgentdEnv{srv: srv, ts: ts, st: nil, token: cfg.Token}, svc: srv.Scheduling()}, remote
}

func TestCarrierDetectRemoteWakesOnlyHostAndWritesLocalRegistry(t *testing.T) {
	var gotPath, gotBody string
	env, _ := newRemoteSchedEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"ready","detail":"remote ready"}`))
	}))
	home := "~/.handoff/home/remote-c1"
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"remote","cli":"opencode","home_dir":"`+home+`","credential":"main_home_sync","model":"fast"}`)
	if code != http.StatusOK {
		t.Fatalf("登记远程载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/c1/detect", "{}")
	if code != http.StatusOK {
		t.Fatalf("远程检测: %d %s", code, body)
	}
	if gotPath != "/api/host/wake" {
		t.Fatalf("远程检测路径 = %q，want /api/host/wake", gotPath)
	}
	var wake proto.HomeWakeReq
	if err := json.Unmarshal([]byte(gotBody), &wake); err != nil {
		t.Fatal(err)
	}
	if wake.HomeDir != home || wake.Credential != "main_home_sync" || wake.Model != "fast" {
		t.Fatalf("远程唤起请求 = %+v", wake)
	}
	if strings.Contains(gotBody, "detect") {
		t.Fatalf("检测不得把自身 forward 到远端: %s", gotBody)
	}
	var detect proto.CarrierDetectResp
	if err := json.Unmarshal([]byte(body), &detect); err != nil {
		t.Fatal(err)
	}
	if detect.Status != "online" || detect.Version != 2 {
		t.Fatalf("远程检测回执 = %+v，want online/version2", detect)
	}
}

func TestCarrierDetectUnknownRemoteOutcomeFailsClosed(t *testing.T) {
	env, _ := newRemoteSchedEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"outcome":"surprise"}`))
	}))
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"remote","cli":"opencode","home_dir":"/tmp/c1","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("登记载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodPost, "/api/squads/carriers/c1/detect", "{}")
	if code != http.StatusBadGateway || !strings.Contains(body, "未知") {
		t.Fatalf("未知 outcome 应 502 且可诊断: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK || strings.Contains(body, `"status":"online"`) {
		t.Fatalf("未知 outcome 不得写 online: %d %s", code, body)
	}
}

func TestCarrierRunCommandThroughDirectHandler(t *testing.T) {
	env := newDirectSchedEnv(t, &config.Config{Token: testToken, DataDir: t.TempDir()})
	code, body := schedReq(t, env, http.MethodPut, "/api/squads/carriers/c1?expect=0",
		`{"machine":"local","cli":"codex","home_dir":"~/.handoff/home/c1","credential":"standalone"}`)
	if code != http.StatusOK {
		t.Fatalf("登记载体: %d %s", code, body)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/squads/carriers/c1/run-command", "")
	if code != http.StatusOK || body != `{"command":"HOME=~/.handoff/home/c1 codex"}`+"\n" {
		t.Fatalf("运行命令 = %d %s", code, body)
	}
}

func TestHostWakeAndProbeForwardPreserveCredentialAndRejectUnknownMachine(t *testing.T) {
	var gotPath, gotBody string
	env, _ := newRemoteSchedEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		if r.URL.Path == "/api/host/wake" {
			_, _ = w.Write([]byte(`{"outcome":"need_login"}`))
			return
		}
		_, _ = w.Write([]byte(`{"kind":"occupied"}`))
	}))
	code, body := schedReq(t, env, http.MethodPost, "/api/host/wake?machine=remote",
		`{"cli":"opencode","home_dir":"~/.handoff/home/c1","credential":"main_home_sync","model":"fast"}`)
	if code != http.StatusOK || gotPath != "/api/host/wake" {
		t.Fatalf("远程 wake: %d %s path=%s", code, body, gotPath)
	}
	var wake proto.HomeWakeReq
	if err := json.Unmarshal([]byte(gotBody), &wake); err != nil {
		t.Fatal(err)
	}
	if wake.Credential != "main_home_sync" || wake.Model != "fast" {
		t.Fatalf("credential/model 未 roundtrip: %+v", wake)
	}
	code, body = schedReq(t, env, http.MethodPost, "/api/host/probe?machine=remote",
		`{"cli":"opencode","path":"~/.handoff/home/c1","credential":"standalone"}`)
	if code != http.StatusOK || gotPath != "/api/host/probe" || !strings.Contains(body, "occupied") {
		t.Fatalf("远程 probe: %d %s path=%s", code, body, gotPath)
	}
	unknown := newDirectSchedEnv(t, &config.Config{Token: testToken, DataDir: t.TempDir()})
	code, body = schedReq(t, unknown, http.MethodPost, "/api/host/probe?machine=missing",
		`{"cli":"opencode","path":"/tmp/c1"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "未在") {
		t.Fatalf("未知机器应 400: %d %s", code, body)
	}
}
