package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/release"
	"github.com/Xsxdot/handoff/internal/upgrade"
)

func boolPtrMachineUpgrade(b bool) *bool { return &b }

func machineStatusServer(t *testing.T, status proto.StatusResp) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}))
}

func postMachineUpgrade(t *testing.T, env *testAgentdEnv, name string, force bool) (int, proto.MachineUpgradeResp) {
	t.Helper()
	path := "/api/machines/" + name + "/upgrade"
	if force {
		path += "?force=1"
	}
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("构造升级请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("升级请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body proto.MachineUpgradeResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解码升级响应失败: %v", err)
	}
	return resp.StatusCode, body
}

func configureMachineUpgrade(t *testing.T, env *testAgentdEnv, name string, status proto.StatusResp) {
	t.Helper()
	remote := machineStatusServer(t, status)
	t.Cleanup(remote.Close)
	env.srv.cfg.Store(&config.Config{
		Token:   testToken,
		DataDir: env.srv.conf().DataDir,
		Targets: map[string]config.Target{name: {Addr: remote.URL, Token: "remote-token"}},
	})
	env.srv.latestFetch = func(context.Context) (release.Release, error) {
		return release.Release{Tag: "v0.3.1"}, nil
	}
	// 直接替换活动快照；测试只需要保证 target 在当前配置里。
}

func TestMachineUpgradeUnknownMachine(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, body := postMachineUpgrade(t, env, "missing", false)
	if code != http.StatusNotFound {
		t.Fatalf("未知机器应 404，code=%d body=%+v", code, body)
	}
}

func TestMachineUpgradeRefusesLocal(t *testing.T) {
	env := newTestAgentdEnv(t)
	code, body := postMachineUpgrade(t, env, "本机", false)
	if code != http.StatusBadRequest || !strings.Contains(body.Reason, "本机") {
		t.Fatalf("本机升级应 400，code=%d body=%+v", code, body)
	}
}

func TestMachineUpgradeBusyIsForcible(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "busy", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: true, Pull: boolPtrMachineUpgrade(false)},
		Active:  []proto.ActiveTask{{State: string(proto.TaskStateRunning)}},
	})
	code, body := postMachineUpgrade(t, env, "busy", false)
	if code != http.StatusConflict || body.Busy != 1 || !body.Forcible {
		t.Fatalf("busy 应 409 且 forcible=true，code=%d body=%+v", code, body)
	}
}

func TestMachineUpgradeUnmanagedIsNotForcible(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "unmanaged", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: false, Pull: boolPtrMachineUpgrade(false)},
	})
	code, body := postMachineUpgrade(t, env, "unmanaged", true)
	if code != http.StatusUnprocessableEntity || body.Forcible {
		t.Fatalf("非托管应 422 且 forcible=false，code=%d body=%+v", code, body)
	}
}

func TestMachineUpgradeUnreachableInventsNoRemedy(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"ghost": {Addr: "http://127.0.0.1:1", Token: "t"}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	code, body := postMachineUpgrade(t, env, "ghost", false)
	if code != http.StatusBadGateway || body.Remedy != "" {
		t.Fatalf("够不着应 502 且不编处置，code=%d body=%+v", code, body)
	}
}

func TestMachineUpgradeAcceptedRunsInBackground(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "devbox", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: true, Pull: boolPtrMachineUpgrade(false)},
	})
	env.srv.latestFetch = func(context.Context) (release.Release, error) {
		return release.Release{Tag: "v0.3.1"}, nil
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	env.srv.machineUpgradeRunner = func(_ context.Context, m upgrade.Machine, _ config.Target,
		_ release.Release, _ bool, _ func(string)) upgrade.Result {
		close(started)
		<-finished
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusOK,
			From: m.Agentd, To: "v0.3.1"}
	}
	code, body := postMachineUpgrade(t, env, "devbox", false)
	if code != http.StatusAccepted || !body.Accepted {
		t.Fatalf("可升级机器应 202/accepted=true，code=%d body=%+v", code, body)
	}
	<-started
	close(finished)
}

func TestMachineUpgradeRejectsDuplicate(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "devbox", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: true, Pull: boolPtrMachineUpgrade(false)},
	})
	started := make(chan struct{})
	finished := make(chan struct{})
	env.srv.machineUpgradeRunner = func(_ context.Context, _ upgrade.Machine, _ config.Target,
		_ release.Release, _ bool, _ func(string)) upgrade.Result {
		close(started)
		<-finished
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusOK}
	}
	code, _ := postMachineUpgrade(t, env, "devbox", false)
	if code != http.StatusAccepted {
		t.Fatalf("首次请求应受理，实际 %d", code)
	}
	<-started
	code, body := postMachineUpgrade(t, env, "devbox", false)
	if code != http.StatusConflict || body.Verdict != "in_progress" {
		t.Fatalf("重复请求应 409/in_progress，code=%d body=%+v", code, body)
	}
	close(finished)
}
