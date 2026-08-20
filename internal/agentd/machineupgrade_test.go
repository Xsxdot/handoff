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
	"time"

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

// machineUpgradeStateOf 从 GET /api/machines 里取出某台机器的升级状态。
func machineUpgradeStateOf(t *testing.T, env *testAgentdEnv, name string) *proto.MachineUpgrade {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/machines", nil)
	if err != nil {
		t.Fatalf("构造机器列表请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("机器列表请求失败: %v", err)
	}
	defer resp.Body.Close()
	var body proto.MachinesResp
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解码机器列表失败: %v", err)
	}
	for _, m := range body.Machines {
		if m.Name == name {
			return m.Upgrade
		}
	}
	t.Fatalf("机器列表里没有 %s", name)
	return nil
}

// waitMachineUpgrade 轮询到条件成立为止；轮询的是「结束」这个状态本身，
// 不是某个中途副产物。
func waitMachineUpgrade(t *testing.T, env *testAgentdEnv, name string,
	ok func(*proto.MachineUpgrade) bool) *proto.MachineUpgrade {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := machineUpgradeStateOf(t, env, name)
		if ok(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("等 %s 的升级状态超时，最后一次读到 %+v", name, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMachineUpgradeFailureSurfacesInMachines 钉住「失败必须有出口」。
//
// 这是 B166 二期漏掉的那条路：升级没有进度流是刻意的，但当时只想了成功怎么结束
// （版本变成最新），没想失败怎么结束。真机上的后果是控制台永远显示「升级中」，
// 而 agentd 三分钟前就已经放弃了。
func TestMachineUpgradeFailureSurfacesInMachines(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "devbox", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: true, Pull: boolPtrMachineUpgrade(false)},
	})
	gate := make(chan struct{}) // 不叫 release：会遮蔽 release 包
	env.srv.machineUpgradeRunner = func(_ context.Context, _ upgrade.Machine, _ config.Target,
		_ release.Release, _ bool, _ func(string)) upgrade.Result {
		<-gate
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusFail,
			Reason: "下载 checksums.txt: 尝试 3 次仍失败: i/o timeout"}
	}
	if code, _ := postMachineUpgrade(t, env, "devbox", false); code != http.StatusAccepted {
		t.Fatalf("可升级机器应 202，实际 %d", code)
	}
	running := waitMachineUpgrade(t, env, "devbox", func(u *proto.MachineUpgrade) bool {
		return u != nil && u.Running
	})
	if running.Status != "" {
		t.Errorf("首次升级运行中不该有终态，实际 %+v", running)
	}

	close(gate)
	done := waitMachineUpgrade(t, env, "devbox", func(u *proto.MachineUpgrade) bool {
		return u != nil && !u.Running
	})
	if done.Status != "fail" {
		t.Errorf("失败应记为 fail，实际 %+v", done)
	}
	if !strings.Contains(done.Reason, "i/o timeout") {
		t.Errorf("失败原文必须原样透出（用户要靠它知道是网络问题），实际 %q", done.Reason)
	}
}

// TestMachineUpgradeSuccessRecordsVersions 成功也要留痕：控制台清态之外，
// 还要能说出「从哪版到哪版」。
func TestMachineUpgradeSuccessRecordsVersions(t *testing.T) {
	env := newTestAgentdEnv(t)
	configureMachineUpgrade(t, env, "devbox", proto.StatusResp{
		Version: proto.BuildInfo{Version: "v0.3.0", Platform: "linux/amd64"},
		Update:  &proto.UpdateStatus{Managed: true, Pull: boolPtrMachineUpgrade(false)},
	})
	env.srv.machineUpgradeRunner = func(_ context.Context, m upgrade.Machine, _ config.Target,
		_ release.Release, _ bool, _ func(string)) upgrade.Result {
		return upgrade.Result{Verdict: upgrade.VerdictNeedsUpgrade, Status: upgrade.StatusOK,
			From: m.Agentd, To: "v0.3.1"}
	}
	if code, _ := postMachineUpgrade(t, env, "devbox", false); code != http.StatusAccepted {
		t.Fatalf("可升级机器应 202")
	}
	done := waitMachineUpgrade(t, env, "devbox", func(u *proto.MachineUpgrade) bool {
		return u != nil && !u.Running
	})
	if done.Status != "ok" || done.From != "v0.3.0" || done.To != "v0.3.1" {
		t.Errorf("成功应记下版本迁移，实际 %+v", done)
	}
}
