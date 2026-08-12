package agentd

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/xushixin/handoff/internal/config"
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
