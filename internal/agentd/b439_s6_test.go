// b439_s6_test.go —— B439 S6：agentd 侧两个入口（卡节点 step 的目标、执行机升级
// 的目标名）在配了自名的机器上拒收本机旧别名，未配自名保持今天行为（S5）。
package agentd

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func TestB439S6MachineUpgradeLegacyAliasGate(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	env := newTestAgentdEnv(t)
	env.srv.conf().Name = "linux-01"
	for _, alias := range []string{"local", "本机", host} {
		code, body := postMachineUpgrade(t, env, alias, false)
		if code != http.StatusBadRequest {
			t.Fatalf("升级 %q 应 400，实得 %d (%+v)", alias, code, body)
		}
		if !strings.Contains(body.Reason, "linux-01") {
			t.Fatalf("拒收文案应点名自名 linux-01，实得: %q", body.Reason)
		}
	}

	// 无自名保持今天行为：本机 → 400「本机版本由薄壳同步路决定」。
	plain := newTestAgentdEnv(t)
	code, body := postMachineUpgrade(t, plain, "本机", false)
	if code != http.StatusBadRequest || !strings.Contains(body.Reason, "本机") {
		t.Fatalf("无自名 本机 应保持今天 400，实得 %d (%+v)", code, body)
	}
}

func TestB439S6AgentdStepLegacyAliasGate(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	srv := newStepTestServer(t)
	srv.conf().Name = "linux-01"
	seedAgentdLedger(t, srv.ledger, "bug")
	card, err := srv.ledger.CreateCard(newCardForStepTest("demo"))
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	for _, alias := range []string{"local", "本机", host} {
		err := srv.startCardStep(card.ID, proto.CardStepReq{
			Step: ledger.StatusDoing, Target: alias, Actor: "test",
		})
		if err == nil {
			t.Fatalf("step target=%q 应被拒", alias)
		}
		if !strings.Contains(err.Error(), "linux-01") {
			t.Fatalf("拒收文案应点名自名 linux-01，实得: %v", err)
		}
	}
}

// TestB439S6FrozenDispatchRejectsLegacyCarrierMachine 是 S6 的派发准入竖切：B398
// 冻结派发（POST /api/tasks 带 carrier + frozen_target）在配了自名的机器上遇到
// **存量** carrier 记录的 machine 是旧别名（local）时，必须在占用写入之前拒发，
// 给点名 carrier、当前值与改法的可行动错误——不得经 samePhysicalMachine 的别名
// 语义静默放行后靠 handleDispatch 的接收机判据兜底。
func TestB439S6FrozenDispatchRejectsLegacyCarrierMachine(t *testing.T) {
	env := newReceiverTestEnv(t)
	svc := mustScheduling(t, env.srv)
	// 存量记录：注册时机器尚无自名，machine=local 合法落库。
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "stale-box", Machine: "local", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
	})
	// 机器现在配了自名，组装点注入自名与旧别名判据（镜像 cmd/agentd.go）。
	env.srv.conf().Name = "linux-01"
	svc.SetSelfName("linux-01")
	svc.SetLegacyAlias(config.IsLegacyLocalAlias)

	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"stale-box","target":"","frozen_target":"local","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("存量旧别名载体冻结派发应 400，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	for _, want := range []string{"stale-box", "local", "改成", "linux-01"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Fatalf("拒收文案应含 %q，实得: %s", want, rr.Body.String())
		}
	}
}
