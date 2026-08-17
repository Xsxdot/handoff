package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

// TestLocalMachineReportsRevealSupported 断言本机探活会带上 reveal 能力位，
// 且它等于当前平台的实际支持度（不是恒 true）。
func TestLocalMachineReportsRevealSupported(t *testing.T) {
	env := newTestAgentdEnv(t)
	mgr, _, _, _ := newTestManager(t)
	env.srv.SetManager(mgr)
	m := env.srv.localMachine()
	if m.RevealSupported == nil {
		t.Fatal("本机探活没带 reveal_supported，前端三态门会退化成一律放行")
	}
	if *m.RevealSupported != revealSupportedOS {
		t.Fatalf("reveal_supported=%v，与平台实际支持度 %v 不符", *m.RevealSupported, revealSupportedOS)
	}
}

// TestFillFromStatusCarriesRevealSupported 断言远程机器的能力位被原样搬运，
// 包括 nil——探到了但对端没这个字段，结论就是「没上报」。
func TestFillFromStatusCarriesRevealSupported(t *testing.T) {
	yes := true
	var m proto.Machine
	fillFromStatus(&m, &proto.StatusResp{RevealSupported: &yes})
	if m.RevealSupported == nil || !*m.RevealSupported {
		t.Fatalf("true 没被搬运过来：%v", m.RevealSupported)
	}

	var m2 proto.Machine
	fillFromStatus(&m2, &proto.StatusResp{})
	if m2.RevealSupported != nil {
		t.Fatalf("对端没上报时应保持 nil，实际 %v", *m2.RevealSupported)
	}
}

// postMachine 带 Bearer 发一次新增请求，返回状态码与响应体原文。
func postMachine(t *testing.T, e *testAgentdEnv, req proto.AddMachineReq) (int, string) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("序列化请求失败: %v", err)
	}
	hr, err := http.NewRequest(http.MethodPost, e.ts.URL+"/api/machines", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	hr.Header.Set("Authorization", "Bearer "+testToken)
	hr.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// 地址不可达时必须 400 并带上探测失败原文，且不得落库。
func TestAddMachineUnreachableRejected(t *testing.T) {
	e := newTestAgentdEnv(t)
	// 127.0.0.1:1 上不会有服务
	code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("期望 400，实际 %d，体=%s", code, body)
	}
	if !strings.Contains(body, "探测 127.0.0.1:1 失败") {
		t.Fatalf("响应应带探测失败原文，实际 %s", body)
	}
	if got := getMachines(t, e); len(got.Machines) != 1 {
		t.Fatalf("探测失败不该落库，机器数应仍为 1（本机），实际 %d", len(got.Machines))
	}
}

// force=true 跳过探测直接落库。
func TestAddMachineForceSkipsProbe(t *testing.T) {
	e := newTestAgentdEnv(t)
	code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true,
	})
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d，体=%s", code, body)
	}
	if strings.Contains(body, "\"t\"") || strings.Contains(body, "token") {
		t.Fatalf("响应体不得包含令牌，实际 %s", body)
	}
	names := map[string]bool{}
	for _, m := range getMachines(t, e).Machines {
		names[m.Name] = true
	}
	if !names["box"] {
		t.Fatal("force 之后机器列表里应有 box")
	}
}

// 重名返回 409。
func TestAddMachineDuplicateConflict(t *testing.T) {
	e := newTestAgentdEnv(t)
	req := proto.AddMachineReq{Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true}
	if code, body := postMachine(t, e, req); code != http.StatusOK {
		t.Fatalf("首次新增应成功，实际 %d %s", code, body)
	}
	if code, _ := postMachine(t, e, req); code != http.StatusConflict {
		t.Fatalf("重名应返回 409，实际 %d", code)
	}
}

// 地址不合法返回 400，且不做探测（快速失败）。
func TestAddMachineBadAddr(t *testing.T) {
	e := newTestAgentdEnv(t)
	if code, _ := postMachine(t, e, proto.AddMachineReq{Name: "box", Addr: "nope", Token: "t"}); code != http.StatusBadRequest {
		t.Fatalf("非法地址应返回 400，实际 %d", code)
	}
}

// deleteMachine 带 Bearer 发一次删除请求。
func deleteMachine(t *testing.T, e *testAgentdEnv, name string) (int, string) {
	t.Helper()
	hr, err := http.NewRequest(http.MethodDelete, e.ts.URL+"/api/machines/"+name, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	hr.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestDeleteMachine(t *testing.T) {
	e := newTestAgentdEnv(t)
	if code, body := postMachine(t, e, proto.AddMachineReq{
		Name: "box", Addr: "127.0.0.1:1", Token: "t", Force: true,
	}); code != http.StatusOK {
		t.Fatalf("准备数据失败: %d %s", code, body)
	}
	if code, body := deleteMachine(t, e, "box"); code != http.StatusOK {
		t.Fatalf("删除应成功，实际 %d %s", code, body)
	}
	for _, m := range getMachines(t, e).Machines {
		if m.Name == "box" {
			t.Fatal("删除后列表里仍有 box")
		}
	}
	if code, _ := deleteMachine(t, e, "box"); code != http.StatusNotFound {
		t.Fatalf("删除不存在的机器应返回 404，实际 %d", code)
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
