// 本文件锁死请求转发基座对 relay 机器的行为：REST 转发（forwardIfRequested）、
// 按任务 id 的透明路由（byTask）、WS 反代（forwardWS）三条路都必须经
// targetclient 池选路。relay 形态的机器没有 addr，拿 t.Addr 直连构造会退化成
// "http:///..."——失败必须给 relay 的真实原因（dial relay …），不能是
// "no Host in request URL"（与 fanout_relay_test.go 同族约束）。
package agentd

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

// relayTargetCfg 返回只含一台 relay 形态机器的配置。
//
// relay 地址指向本机端口 1（恒拒连），让隧道建立立刻失败——用例断言的是
// 失败**文案**走了 relay 选路，不是隧道通。
func relayTargetCfg() *config.Config {
	return &config.Config{
		Token: testToken,
		Targets: map[string]config.Target{
			"linux-01": {
				Relay: "wss://127.0.0.1:1/relay", Credential: "cred",
				Node: "linux-01", Token: "0123456789abcdef0123456789abcdef",
			},
		},
	}
}

// newRelayForwardEnv 构造带 relay target 的完整测试环境，并接管池的收尾。
func newRelayForwardEnv(t *testing.T) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnvWithCfg(t, relayTargetCfg(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = env.srv.CloseTargets() })
	return env
}

// assertRelayCause 断言转发失败文案是 relay 的真实原因。
//
// 两条腿缺一不可：只断「不含 no Host」是反面断言，选路彻底不发请求也能绿；
// 「含 dial relay」钉死了错误确实来自 relay 拨号这条路。
func assertRelayCause(t *testing.T, errMsg string) {
	t.Helper()
	if strings.Contains(errMsg, "no Host in request URL") {
		t.Fatalf("转发对 relay 机器不该报 no Host（直连构造的症状）：%s", errMsg)
	}
	if !strings.Contains(errMsg, "dial relay") {
		t.Fatalf("失败原因应来自 relay 拨号（含 \"dial relay\"），实得：%s", errMsg)
	}
}

// TestForwardIfRequestedRelayCause：?machine= 的 REST 转发对 relay 机器走池选路。
func TestForwardIfRequestedRelayCause(t *testing.T) {
	env := newRelayForwardEnv(t)
	var body map[string]string
	code := env.getJSON(t, "/api/discipline?machine=linux-01", &body)
	if code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", code)
	}
	assertRelayCause(t, body["error"])
}

// TestTaskRouteRelayCause：镜像索引指向 relay 机器时，按任务 id 的转发走池选路。
func TestTaskRouteRelayCause(t *testing.T) {
	env := newRelayForwardEnv(t)
	taskID := uuid.NewString()
	if err := env.st.UpsertMirrorTask("linux-01", proto.Task{
		ID: taskID, Name: "远端任务", State: proto.TaskStateRunning,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("UpsertMirrorTask: %v", err)
	}

	var body map[string]string
	code := env.getJSON(t, "/api/tasks/"+taskID, &body)
	if code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", code)
	}
	assertRelayCause(t, body["error"])
}

// TestForwardWSRelayCause：WS 反代对 relay 机器走池选路。
//
// 直接调 forwardWS：拨上游失败发生在 Accept 本地之前，recorder 里就是一个
// 普通的 502 JSON，无需真的建 WS 连接。
func TestForwardWSRelayCause(t *testing.T) {
	env := newRelayForwardEnv(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws/pty/whatever?machine=linux-01", nil)
	env.srv.forwardWS(rec, req, "linux-01")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", rec.Code)
	}
	assertRelayCause(t, rec.Body.String())
}
