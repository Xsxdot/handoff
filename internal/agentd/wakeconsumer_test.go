// wakeconsumer_test.go —— K5 账本事件消费者的缝级测试。
//
// 职责：通过真实 ledger Facade 读取事件，锁住 task/room 映射、过滤、同卡合并、
// attach 延迟和 cursor 回退幂等。
// 边界：不复制 keystone briefing/重建规则；只观察消费者到 Wake 的真实入口。
package agentd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

func newNoPTYAutomationEnv(t *testing.T) (*ledgerEnv, *queueTraceRunner) {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	return env, seedQueueCoordinator(t, env)
}

func appendMirroredForConsumer(t *testing.T, st *ledger.Store, cardID, task, typ string, seq int64, payload string) int64 {
	t.Helper()
	if _, err := st.AppendMirroredEvent(cardID, ledger.MirroredEvent{
		Target: "consumer-target", Task: task, SourceSeq: seq, Type: typ,
		Payload: []byte(payload), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写镜像事件 %s: %v", typ, err)
	}
	events, err := st.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读镜像事件 %s: %v", typ, err)
	}
	return events[len(events)-1].Seq
}

func prebindConsumerSession(t *testing.T, env *ledgerEnv, cardID string) {
	t.Helper()
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("预绑定协调者会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码预绑定协调者席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("写预绑定协调者席位: %v", err)
	}
}

func appendUserMessage(t *testing.T, st *ledger.Store, cardID, body string, bySystem bool) {
	t.Helper()
	if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
		Room: cardID, Kind: proto.RoomMsgUser, Body: body, BySystem: bySystem,
	}, "test"); err != nil {
		t.Fatalf("写用户消息: %v", err)
	}
}

func appendPointerMessage(t *testing.T, st *ledger.Store, cardID string) {
	t.Helper()
	if _, err := st.RecordRoomMessage(cardID, proto.RoomMessage{
		Room: cardID, Kind: proto.RoomMsgPointer, Body: "协调者指针",
	}, "system:pointer"); err != nil {
		t.Fatalf("写指针消息: %v", err)
	}
}

func TestAutomationEventMappingThroughConsumer(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	for i, typ := range []string{"completed", "failed", "turn_failed", "permission_request", "question", "progress"} {
		appendMirroredForConsumer(t, env.ledger, cardID, "task-"+typ, typ, int64(i+1), `{"text":"`+typ+`"}`)
	}
	appendUserMessage(t, env.ledger, cardID, "用户留言", false)
	appendPointerMessage(t, env.ledger, cardID)
	appendUserMessage(t, env.ledger, cardID, "系统用户形状", true)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费事件: %v", err)
	}
	if escalated {
		t.Fatal("正常事件消费不应升级人工")
	}
	if processed != 6 {
		t.Fatalf("处理唤醒事件数=%d，want 6", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("同卡事件应合并为一次 Resume，实得 %d", len(resumes))
	}
	for _, want := range []string{"completed", "failed", "turn_failed", "permission_request", "question", "用户留言"} {
		if !strings.Contains(resumes[0], want) {
			t.Fatalf("briefing 缺 %q: %s", want, resumes[0])
		}
	}
	for _, unwanted := range []string{"progress", "协调者指针", "系统用户形状"} {
		if strings.Contains(resumes[0], unwanted) {
			t.Fatalf("不应唤醒/进入 briefing 的内容 %q 出现: %s", unwanted, resumes[0])
		}
	}
}

func TestAutomationApproverDisabledFallsBackToWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "disabled", "approver_disabled", 1, `{"reason":"disabled"}`)
	appendMirroredForConsumer(t, env.ledger, cardID, "permission", "permission_request", 1, `{"permission":"go test"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil {
		t.Fatalf("消费 disabled 后权限事件: %v", err)
	}
	if processed != 1 {
		t.Fatalf("permission_request 应处理 1 条，实得 %d", processed)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 || !strings.Contains(resumes[0], "permission_request") {
		t.Fatalf("disabled 后 permission 未唤醒一次: resumes=%v", resumes)
	}
}

func TestAutomationCursorRewindIsIdempotent(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)

	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("首次消费 processed=%d err=%v，want 1/nil", processed, err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("首次应 Resume 一次，实得 %d", len(resumes))
	}
	env.srv.automationMu.Lock()
	env.srv.automationCursor = 0
	env.srv.automationMu.Unlock()
	processed, _, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 0 {
		t.Fatalf("游标回退后 processed=%d err=%v，want 0/nil", processed, err)
	}
	_, resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("游标回退不应二次 Resume，实得 %d", len(resumes))
	}
}

func TestAutomationAttachDefersAndThenWakes(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var terminalSeq int64
	for _, ev := range events {
		if ev.Type == ledger.EvTaskMirrored {
			terminalSeq = ev.Seq
		}
	}
	env.srv.keystone.SetAttach(cardID, true)
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 0 {
		t.Fatalf("attach 中 processed=%d err=%v，want 0/nil", processed, err)
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor >= terminalSeq {
		t.Fatalf("attach 暂缓却推进 cursor=%d 到事件=%d", cursor, terminalSeq)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 0 {
		t.Fatalf("attach 中不应 Resume，实得 %d", len(resumes))
	}
	env.srv.keystone.SetAttach(cardID, false)
	processed, _, err = env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("解除 attach 后 processed=%d err=%v，want 1/nil", processed, err)
	}
	_, resumes, _ = runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("解除 attach 后应 Resume 一次，实得 %d", len(resumes))
	}
}

type fallbackConsumerRunner struct {
	mu         sync.Mutex
	launches   int
	resumes    int
	failLaunch bool
	failResume bool
}

func (r *fallbackConsumerRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches++
	if r.failLaunch {
		return keysclient.TurnResult{}, errors.New("launch failed")
	}
	return keysclient.TurnResult{SessionID: "fallback-session"}, nil
}

func (r *fallbackConsumerRunner) Resume(keysclient.SessionRef, string) (keysclient.TurnResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resumes++
	if r.failResume {
		return keysclient.TurnResult{}, errors.New("resume failed")
	}
	return keysclient.TurnResult{SessionID: "fallback-session"}, nil
}

func TestAutomationFallbackResumeRebuildFailure(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	putOnlineCarrier(t, env.srv.Scheduling(), scheduling.Carrier{
		Name: "coord-carrier", Machine: "ftm", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline,
	})
	if err := env.srv.Scheduling().PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := &fallbackConsumerRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	result, err := env.srv.keystone.LaunchForCard(context.Background(), cardID, "coordinate", keysclient.SessionSpec{CLI: "opencode"})
	if err != nil {
		t.Fatalf("建立既有会话: %v", err)
	}
	identity, err := proto.EncodeSeatIdentity("opencode", result.SessionID)
	if err != nil {
		t.Fatalf("编码既有会话席位: %v", err)
	}
	if err := env.ledger.BindSeat(cardID, identity, proto.SeatSourceCoordinate); err != nil {
		t.Fatalf("写既有会话席位: %v", err)
	}
	runner.failResume = true
	runner.failLaunch = true
	appendMirroredForConsumer(t, env.ledger, cardID, "terminal", "completed", 1, `{"text":"done"}`)

	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil || processed != 0 || !escalated {
		t.Fatalf("resume/rebuild 双失败 processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	if runner.resumes != 1 || runner.launches != 2 {
		t.Fatalf("兜底调用次数 resume=%d launch=%d，want 1/2", runner.resumes, runner.launches)
	}
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	foundNeeds := false
	for _, ev := range events {
		if ev.Type == ledger.EvNeedsHuman && strings.Contains(string(ev.Payload), "resume") && strings.Contains(string(ev.Payload), "重建") {
			foundNeeds = true
		}
	}
	if !foundNeeds {
		t.Fatalf("缺少含 resume/重建 原因的 needs_human 事件")
	}
}

// TestAutomationWakeFailureAdvancesCursor 唤醒失败必须推进 cursor，不得对
// 同一条用户消息无限 Launch（B274：空 spec + 失败不推 cursor + kick = 指针洪流）。
func TestAutomationWakeFailureAdvancesCursor(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	allowCarrierMachines(t, env.srv, "ftm")
	putOnlineCarrier(t, env.srv.Scheduling(), scheduling.Carrier{
		Name: "coord-carrier", Machine: "ftm", CLI: "opencode",
		HomeDir: "/tmp/coord-home", Credential: scheduling.CredentialStandalone,
		Status: scheduling.StatusOnline,
	})
	if err := env.srv.Scheduling().PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator, Members: []scheduling.SquadMember{{Carrier: "coord-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := &fallbackConsumerRunner{failLaunch: true}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	runner.failLaunch = false
	prebindConsumerSession(t, env, cardID)
	runner.failLaunch = true
	runner.failResume = true
	appendUserMessage(t, env.ledger, cardID, "用户留言", false)

	_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err == nil {
		t.Fatal("无绑定且 Launch 失败时期望 err")
	}
	first := runner.launches
	if first < 1 {
		t.Fatal("至少应尝试一次 Launch")
	}
	env.srv.automationMu.Lock()
	cursor := env.srv.automationCursor
	env.srv.automationMu.Unlock()
	if cursor == 0 {
		t.Fatal("唤醒失败后 cursor 仍为 0，同一条消息会被再消费")
	}

	_, _, _ = env.srv.consumeAutomationEventsOnce(context.Background())
	if runner.launches != first {
		t.Fatalf("失败后不得对同一条再 Launch：%d → %d", first, runner.launches)
	}
}
