// b396_archived_step_test.go —— B396 陈旧环节运行位不自愈的缝级回归回路。
//
// 职责：锁住「节点等待的 task 被外部归档（done）后，卡节点回合必须收口、
// 释放进程内环节槽位与账本运行锁，使同卡同节点可重派」；同时锁住反向断言
// ——真在跑时同卡同节点并发重派仍必须 409（不许把互斥一起放开）。
// 缝：HTTP `POST /api/cards/{id}/step`（`card dispatch --step` 落到同一入口），
// 即 409「该卡已有环节在运行」的判据面；不直调 waitForTurnEnd。
// 边界：不复制 keystone/编排规则；不测 task 生命周期本身（另有既有测试）。
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
	"github.com/coder/websocket"
)

// b396ArchivedTaskServer 造一个 dispatch 目标：每条连接都只产出 archived
// （外部 done 的形态），永不产出 completed/failed。第 1 条连接由 release 控制
// 何时归档——它对应「首次派发仍在飞」的窗口；release 之后所有连接（重派）立即
// 归档并关闭，避免测试结束时留下重试 goroutine。
func b396ArchivedTaskServer(t *testing.T, taskID string) (url string, connected <-chan struct{}, release func()) {
	t.Helper()
	var mu sync.Mutex
	conns := 0
	connReady := make(chan struct{})
	rel := make(chan struct{})
	var relOnce sync.Once
	ts := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/ws/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("Accept WS: %v", err)
				return
			}
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			if n == 1 {
				close(connReady)
				<-rel
			}
			ev := proto.Event{Seq: 1, TaskID: taskID, Type: proto.EventTypeArchived,
				Payload: json.RawMessage(`{"note":""}`)}
			body, _ := json.Marshal(ev)
			_ = conn.Write(r.Context(), websocket.MessageText, body)
			_ = conn.Close(websocket.StatusNormalClosure, "task archived")
		case r.URL.Path == "/api/tasks/"+taskID && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"recent_events":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	return ts.URL, connReady, func() { relOnce.Do(func() { close(rel) }) }
}

// b396FlowEnv 建一张钉在「pl/impl 一个派发裁决节点」上的卡，并让该节点真的
// 走 StepRunner.Run（生产 runStepFn），只替换 transport 与 client。
func b396FlowEnv(t *testing.T, taskURL, taskID string) (*ledgerEnv, string) {
	t.Helper()
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "本机测试实现纪律")
	if _, err := env.ledger.PutWorkflow("b396-flow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: ledger.StatusTodo, Next: "impl"},
		{Name: "impl", Dispatch: true, Verdict: true, Template: "feature-impl", MaxRounds: 3, Next: ledger.StatusDone},
		{Name: ledger.StatusDone},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := env.ledger.CreateCard(ledger.NewCard{
		Title: "B396 归档卡", Project: "handoff", Workflow: "b396-flow", Actor: "test",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, step string) {
		runner.Dispatcher.Transport = func(context.Context, ledgerstep.DispatchOpts) (string, string, error) {
			return taskID, "", nil
		}
		runner.Clients = func(string) (ledgerstep.StepClient, error) {
			return client.New(taskURL, "test-token"), nil
		}
		env.srv.runStep(ctx, runner, cardID, step)
	}
	return env, card.ID
}

// TestB396ArchivedTaskReleasesCardStepSlot 锁 B396 冻结条目 1/2：
// 节点等待的 task 被归档后，回合收口、槽位与运行锁释放、同卡同节点可重派；
// 且真在跑时并发重派仍 409（反例断言在场）。
//
// 红（当前 HEAD）：archived 不在 waitForTurnEnd 的终态集合里，Run 永不返回，
// cardStepFlight 永不清，第二次派发此后永远 409（本节点已在基线实跑）。
// 绿（T2）：waitForTurnEnd 把 archived 收口为终态错误，Run 落等人并释放锁与槽位。
// 变异复验：把 archived case 从 waitForTurnEnd 移除 → 本测试重新变红。
func TestB396ArchivedTaskReleasesCardStepSlot(t *testing.T) {
	const taskID = "task-b396-archived"
	url, connected, release := b396ArchivedTaskServer(t, taskID)
	env, cardID := b396FlowEnv(t, url, taskID)

	post := func() (int, string) {
		return ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+"/step",
			`{"step":"impl","actor":"cli:u@h#1"}`)
	}

	if code, body := post(); code != http.StatusAccepted {
		t.Fatalf("首次派发应 202，实得 %d（%s）", code, body)
	}
	<-connected
	if code, body := post(); code != http.StatusConflict {
		t.Fatalf("真在跑时并发重派应 409（互斥不得放开），实得 %d（%s）", code, body)
	}

	release()
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
	if _, ok, err := env.ledger.RunLockOf(cardID); err != nil || ok {
		t.Fatalf("归档后运行锁行应已释放: ok=%v err=%v", ok, err)
	}
	// 归档不得静默：卡上要留一条 needs_human 说明（haltForHuman 的产物）。
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 100)
	if err != nil {
		t.Fatalf("读卡事件: %v", err)
	}
	var sawNeedsHuman bool
	for _, e := range events {
		if e.Type == ledger.EvNeedsHuman {
			sawNeedsHuman = true
		}
	}
	if !sawNeedsHuman {
		t.Fatalf("归档后回合应落 needs_human 说明（不得静默挂死），事件: %+v", events)
	}

	if code, body := post(); code != http.StatusAccepted {
		t.Fatalf("归档后同卡同节点重派应被受理，实得 %d（%s）", code, body)
	}
	waitFor(t, func() bool { return !env.srv.cardStepInFlight(cardID) })
}
