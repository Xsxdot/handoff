// b398_frozen_target_test.go —— B398 冻结物理身份与路由 target 分字段的缝级回归。
//
// 职责：锁住「冻结载体的原始机器名（Binding.Target）不被路由归一改写，原样到达
// 接收端并被 AdmitFrozen 采用」，以及「旧发送方不带冻结字段时行为与今天逐字一致」。
// 缝：
//   - 接缝 3（端到端）：HTTP `POST /api/cards/{id}/step` → startCardStep → runner.Run →
//     ViaTemplate → stepTransport → 本机 HTTP `POST /api/tasks` → handleDispatch；
//   - 接缝 2（接收端准入）：`POST /api/tasks` 的冻结分支。
//
// 边界：不复制 AdmitFrozen/samePhysicalMachine 的判据；不直调 stepTransport 冒充接缝 3。
package agentd

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// TestB398FrozenCarrierLocalAliasDispatches 接缝 3：本机别名（配置里 linux-01 指向
// 本 agentd）作冻结载体机器时，整条装配→派发→接收准入必须成功形成 task，且任务
// 物理身份仍是原名 linux-01。基线红：NormalizeTarget 把冻结值折成空串，接收端
// AdmitFrozen 报「冻结物理身份与载体登记不一致」。
func TestB398FrozenCarrierLocalAliasDispatches(t *testing.T) {
	env := setupB23310CardTaskEnvWithMachine(t, []fake.Step{{Finish: executor.Result{OK: true}}}, "linux-01")
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
		`{"step":"implement","actor":"cli:t@h#1"}`)
	if code != http.StatusAccepted {
		t.Fatalf("本机别名冻结节点应受理（202），实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	links, err := env.ledger.TasksOf(cardID)
	if err != nil {
		t.Fatalf("读卡挂账: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("本机别名冻结派发应形成 1 条 task，实得 %d；卡事件=%s",
			len(links), b398CardEvents(t, env, cardID))
	}
	task, err := env.st.GetTask(links[0].TaskID)
	if err != nil {
		t.Fatalf("读回 task: %v", err)
	}
	if task.Target != "linux-01" {
		t.Fatalf("冻结任务物理身份 = %q，want linux-01", task.Target)
	}
}

// TestB398ReceiverUsesFrozenTargetForFrozenAdmission 接缝 2：新发送方（路由字段空、
// 冻结字段带原名）必须用冻结字段建 Binding 并准入通过。基线红：字段缺席于协议，
// 接收端只读 target=""，AdmitFrozen 报角色不符。
func TestB398ReceiverUsesFrozenTargetForFrozenAdmission(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"","frozen_target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("带冻结字段的冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Carrier != "self-box" || task.Target != "linux-01" {
		t.Fatalf("任务身份 = carrier:%q target:%q，want self-box/linux-01", task.Carrier, task.Target)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理冻结任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}

// TestB398ReceiverLegacyTargetFallback 接缝 2 回归锁：旧发送方不带 frozen_target，
// 行为必须与今天逐字一致（用 req.Target 建 Binding 并准入通过）。基线即绿。
func TestB398ReceiverLegacyTargetFallback(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"linux-01","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusOK {
		t.Fatalf("旧发送方冻结派发应 200，实得 %d（%s）", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Target != "linux-01" {
		t.Fatalf("旧发送方任务物理身份 = %q，want linux-01", task.Target)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("清理旧发送方任务返回 %d: %s", stop.Code, stop.Body.String())
	}
}

// TestB398AssemblySeparatesFrozenIdentityFromRoute 接缝 1：装配路径（HTTP
// `POST /api/cards/{id}/step` 受理 → startCardStep → runner.Run）产出的
// DispatchOpts 必须把「路由 target」与「冻结身份」分成两个值——本机别名
// linux-01 时路由字段是 canonical 空串，冻结字段是原名。断言落在 transport
// 收到的 DispatchOpts 上，不绕开装配路径。
func TestB398AssemblySeparatesFrozenIdentityFromRoute(t *testing.T) {
	env := setupB23310CardTaskEnvWithMachine(t, []fake.Step{{Finish: executor.Result{OK: true}}}, "linux-01")
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]

	type captured struct {
		opts ledgerstep.DispatchOpts
	}
	gotCh := make(chan captured, 1)
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, id, node string) {
		runner.Dispatcher.Transport = func(_ context.Context, opts ledgerstep.DispatchOpts) (string, string, error) {
			gotCh <- captured{opts: opts}
			return "T-b398-seam1", "", nil
		}
		_, _ = runner.Run(ctx, id, node)
	}
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	var got captured
	select {
	case got = <-gotCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("装配路径未到达 Transport；卡事件=%s", b398CardEvents(t, env, cardID))
	}
	if got.opts.FrozenTarget != "linux-01" {
		t.Fatalf("冻结身份字段 = %q，want linux-01", got.opts.FrozenTarget)
	}
	if got.opts.Target != "" {
		t.Fatalf("路由字段 = %q，want 空（canonical）", got.opts.Target)
	}
}

// TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim 接缝 2：字段缺席与显式空串必须可
// 分辨——缺席回落 req.Target；显式空串按快照丢失处理（400），不静默回落。
func TestB398ReceiverEmptyFrozenTargetIsTakenVerbatim(t *testing.T) {
	env := newReceiverTestEnv(t)
	putTarget(t, env.srv, "linux-01", env.srv.conf().Listen)
	putOnlineCarrier(t, mustScheduling(t, env.srv), scheduling.Carrier{
		Name: "self-box", Machine: "linux-01", CLI: "fake", HomeDir: "",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
		Status: scheduling.StatusOnline,
	})
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID,
		`,"carrier":"self-box","target":"linux-01","frozen_target":"","executor":"fake","home_dir":""`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("显式空冻结字段应被拒（快照丢失可见），实得 %d（%s）", rr.Code, rr.Body.String())
	}
}

// b398CardEvents 把卡的事件流拼成一行，供失败时保留现场。
func b398CardEvents(t *testing.T, env *ledgerEnv, cardID string) string {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		return "读事件失败: " + err.Error()
	}
	out := ""
	for _, e := range events {
		out += "\n  " + string(e.Type) + " " + string(e.Payload)
	}
	return out
}

var _ = proto.Task{}
