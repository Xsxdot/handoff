package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestProbeMachinesLocalAndRemote 起两个真实 agentd：本机 + 一台可达的“远程”。
//
// 为什么用真 server 当远程而不是打桩 HTTP：探活打的是 GET /api/status，
// 桩会把「响应形状变了」这类真实故障挡在测试之外。
func TestProbeMachinesLocalAndRemote(t *testing.T) {
	remote := newTestAgentdEnv(t)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:  testToken,
		Listen: "127.0.0.1:7777",
		Targets: map[string]config.Target{
			"devbox": {Addr: remote.ts.URL, Token: testToken},
			"nas":    {Addr: "http://127.0.0.1:1", Token: testToken}, // 必然拒连
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := local.srv.probeMachines(context.Background())
	if len(resp.Machines) != 3 {
		t.Fatalf("机器数 = %d，期望 3（本机 + devbox + nas）", len(resp.Machines))
	}
	byName := map[string]int{}
	for i, m := range resp.Machines {
		byName[m.Name] = i
	}
	self := resp.Machines[byName[""]]
	if !self.Reachable || self.ProbeMs != 0 {
		t.Errorf("本机必须可达且 probe_ms 恒 0：%+v", self)
	}
	// 不可达是数据不是错误：那台仍然在列表里，且 error 非空
	nas := resp.Machines[byName["nas"]]
	if nas.Reachable {
		t.Errorf("127.0.0.1:1 不该可达：%+v", nas)
	}
	if nas.Error == "" {
		t.Error("不可达时 error 必须带原文——静默少一行是本设计的头号失败模式")
	}
	if nas.Executors == nil {
		t.Error("不可达时 executors 也要是 []，不能是 null")
	}
}

// TestMachinesCarriesPtyCapability 断言：探活拿到的 pty_supported 被投影进
// Machine，而不是在 fillFromStatus 里丢掉。
func TestMachinesCarriesPtyCapability(t *testing.T) {
	remote := newTestAgentdEnv(t)
	rm, _, _, _ := newTestManager(t)
	remote.srv.SetManager(rm)
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"devbox": {Addr: remote.ts.URL, Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lm, _, _, _ := newTestManager(t)
	local.srv.SetManager(lm)

	resp := getMachines(t, local)
	for _, m := range resp.Machines {
		if !m.Reachable {
			continue
		}
		if m.PtySupported == nil {
			t.Fatalf("机器 %q 可达却没带能力位：nil 是「对端没上报」，这里对端明明上报了", m.Name)
		}
	}
}

// TestMachinesUnreachableHasNilCapability 断言：够不着的机器能力位是 nil，
// **不是 false**——「探不到」与「明确不支持」是两个结论。
func TestMachinesUnreachableHasNilCapability(t *testing.T) {
	local := newTestAgentdEnvWithCfg(t, &config.Config{
		Token:   testToken,
		Targets: map[string]config.Target{"ghost": {Addr: "http://127.0.0.1:1", Token: testToken}},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, m := range getMachines(t, local).Machines {
		if m.Name == "ghost" && m.PtySupported != nil {
			t.Fatalf("够不着的机器能力位必须是 nil，实得 %v", *m.PtySupported)
		}
	}
}

// getMachines 带 Bearer 请求 /api/machines 并解出响应。
func getMachines(t *testing.T, e *testAgentdEnv) proto.MachinesResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	var out proto.MachinesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解响应失败: %v", err)
	}
	return out
}
