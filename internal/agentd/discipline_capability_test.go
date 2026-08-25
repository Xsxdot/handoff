// B229.2 能力位 DisciplinesSupported 上报的白盒测试。
//
// 职责：锁住上报点三处投影链——HTTP /api/status 响应、本机机器记录
// （localMachine）、探活搬运（fillFromStatus）——与 pty_launcher_test.go 的
// LaunchersSupported 三连同构。边界：派发侧拒发闸（ResolveDispatch 对
// nil/false 拒发）归 internal/discipline 与 dispatch_discipline_test.go，
// 本文件不重复；web 投影位由 Ticket 0 预置，不在此覆盖。
package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestStatusReportsDisciplinesSupported 断言 HTTP status 响应按判据要求以原始
// JSON 含 `"disciplines_supported":true`：writeJSON 是紧凑编码，子串匹配即等价
// 于「字段存在且为 true」——omitempty 会把缺席(nil)整个吞掉，子串断言恰好同时
// 排除「没置位」与「置了 false」两种回归。
func TestStatusReportsDisciplinesSupported(t *testing.T) {
	env := newTestAgentdEnv(t)
	m, _, _, _ := newTestManager(t)
	env.srv.SetManager(m)
	req, err := http.NewRequest(http.MethodGet, env.ts.URL+"/api/status", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"disciplines_supported":true`) {
		t.Fatalf(`status JSON 应含 "disciplines_supported":true，实得：%s`, body)
	}
}

func TestLocalMachineReportsDisciplinesSupported(t *testing.T) {
	env := newTestAgentdEnvWithCfg(t, &config.Config{Token: testToken, DataDir: t.TempDir()},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	m, _, _, _ := newTestManager(t)
	env.srv.SetManager(m)
	machine := env.srv.localMachine()
	if machine.DisciplinesSupported == nil || !*machine.DisciplinesSupported {
		t.Fatalf("本机 disciplines_supported 应为 true，实得 %v", machine.DisciplinesSupported)
	}
}

func TestFillFromStatusCarriesDisciplinesSupportedIncludingNil(t *testing.T) {
	yes := true
	var m proto.Machine
	fillFromStatus(&m, &proto.StatusResp{DisciplinesSupported: &yes})
	if m.DisciplinesSupported == nil || !*m.DisciplinesSupported {
		t.Fatalf("true 没被搬运过来：%v", m.DisciplinesSupported)
	}
	var m2 proto.Machine
	fillFromStatus(&m2, &proto.StatusResp{})
	if m2.DisciplinesSupported != nil {
		t.Fatalf("对端没上报时应保持 nil，实际 %v", *m2.DisciplinesSupported)
	}
}
