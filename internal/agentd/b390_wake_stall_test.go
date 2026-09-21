// b390_wake_stall_test.go —— B390 回归回路：唤醒消费轮在持续准入失败后不得静默。
//
// 职责：用「准入恒返回并发已满」的调度桩驱动 runAutomationPass，锁住三条同时
// 成立——(a) 按节拍重试而非一次就停；(b) 停摆落成账本可见 needs_human；
// (c) 循环不静默退出。入口缝是 Server.runAutomationPass。
// 边界：只覆写 AdmitSeatCarrier 这一级唤醒准入面（B389 后唤醒路径走
// AdmitSeatCarrier，plan 撰写时写的 LaunchAdmit 是 B389 之前的路径；偏差记入
// B390 implement 台账），其余走真实 ledger/keystone/rooms 接线；不复制
// scheduling 的准入或 keystone 的重建规则。
package agentd

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// alwaysNoSlotScheduling 把协调者唤醒准入恒判为满员，并记录尝试次数。
type alwaysNoSlotScheduling struct {
	*scheduling.Service
	mu     sync.Mutex
	admits int
}

func (s *alwaysNoSlotScheduling) AdmitSeatCarrier(string, string) (scheduling.Binding, error) {
	s.mu.Lock()
	s.admits++
	s.mu.Unlock()
	return scheduling.Binding{}, fmt.Errorf("stub 准入满员: %w", scheduling.ErrNoSlot)
}

func (s *alwaysNoSlotScheduling) admitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admits
}

// b390HasNeedsHuman 扫全流看该卡是否落了 needs_human。
func b390HasNeedsHuman(t *testing.T, env *ledgerEnv, cardID string) bool {
	t.Helper()
	return b390NeedsHumanCount(t, env, cardID) > 0
}

// b390NeedsHumanCount 数该卡落的 needs_human 条数（P4 去重判据：连续失败达阈值
// 恰落一次，之后继续重试不再重复落）。
func b390NeedsHumanCount(t *testing.T, env *ledgerEnv, cardID string) int {
	t.Helper()
	events, err := env.ledger.EventsFromAsc(nil, 0, 100000)
	if err != nil {
		t.Fatalf("读账本事件: %v", err)
	}
	count := 0
	for _, ev := range events {
		if ev.Type == ledger.EvNeedsHuman && ev.CardID == cardID {
			count++
		}
	}
	return count
}

// nonAdmissionScheduling 把唤醒准入判为「非满员」的真实故障（如载体不在线），
// 用于反例：非准入错误不得进入重试节拍、不得落 needs_human。
type nonAdmissionScheduling struct {
	*scheduling.Service
	mu     sync.Mutex
	admits int
}

func (s *nonAdmissionScheduling) AdmitSeatCarrier(string, string) (scheduling.Binding, error) {
	s.mu.Lock()
	s.admits++
	s.mu.Unlock()
	return scheduling.Binding{}, fmt.Errorf("stub 载体不在线: %w", scheduling.ErrNoHealthy)
}

func (s *nonAdmissionScheduling) admitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admits
}

// TestB390NonAdmissionErrorDoesNotStall 是 T2 的反例（plan T2 缺陷族表「准入满员
// 与真实故障分流」）：非 ErrNoSlot 的准入错误必须走既有终局路径（收尾认领、标
// seen、推进游标、退避），不得按节拍重试，也不得落 needs_human 冒充满员。
func TestB390NonAdmissionErrorDoesNotStall(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	stub := &nonAdmissionScheduling{Service: mustScheduling(t, env.srv)}
	env.srv.SetScheduling(stub)

	sendSessionMessage(t, svc, sessionID, "user:tester", "请看这张卡", []string{cardID}, 0)

	const passes = 3
	for i := 0; i < passes; i++ {
		env.srv.runAutomationPass(context.Background())
	}

	if got := stub.admitCount(); got != 1 {
		t.Errorf("非准入错误不得按节拍重试：%d 轮尝试 %d 次（want 1）", passes, got)
	}
	if b390HasNeedsHuman(t, env, cardID) {
		t.Errorf("非准入错误不得落 needs_human（不得把真实故障说成满员）")
	}
}

func TestB390WakeStallRedLoop(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	sessionID, svc := mustWakeSessionFixture(t, env, cardID)
	drainAutomation(t, env)

	stub := &alwaysNoSlotScheduling{Service: mustScheduling(t, env.srv)}
	env.srv.SetScheduling(stub)

	sendSessionMessage(t, svc, sessionID, "user:tester", "请看这张卡", []string{cardID}, 0)

	const passes = 3
	for i := 0; i < passes; i++ {
		env.srv.runAutomationPass(context.Background())
	}

	// (a) 按节拍重试：N 轮后准入至少被尝试 N 次。
	if got := stub.admitCount(); got < passes {
		t.Errorf("RED(a) 准入持续失败时未按节拍重试：%d 轮只尝试 %d 次（want ≥ %d）", passes, got, passes)
	}
	// (b) 停摆必须落成账本可见 needs_human，不许只写日志。
	if !b390HasNeedsHuman(t, env, cardID) {
		t.Errorf("RED(b) 准入持续失败未落成 needs_human（账本无卡级可见事件）")
	}
	// (c) 循环不静默退出：仍应有准入尝试。
	if got := stub.admitCount(); got == 0 {
		t.Errorf("RED(c) 消费循环静默退出：无任何准入尝试")
	}
	// (d) P4 去重：连续失败达阈值恰落一次 needs_human，继续重试不重复落。
	if got := b390NeedsHumanCount(t, env, cardID); got != 1 {
		t.Errorf("RETRY(d) 停摆等人应恰落一次，实得 %d", got)
	}
}
