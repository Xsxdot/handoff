// send_sentinel_internal_test.go —— Send 在 executor 已不在时必须带哨兵的回归测试。
//
// 职责：
//   - 断言 ACP 连接不可用时 Send 返回的错误可被 errors.Is 判为 ErrTaskNotRunning
//
// 边界：
//   - 不起 serve 进程、不连网络；只验错误归一化这一层
//   - 不验 manager 侧的恢复阶梯本身（那是 internal/agentd 的用例）
package grok

import (
	"context"
	"errors"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

// TestSendClosedConnCarriesNotRunning 连接已关闭时 Send 必须带 ErrTaskNotRunning。
//
// 现场（2026-08-09 grok 端到端验收）：杀掉 grok serve 进程后 `handoff continue`
// 直接 500。runs 表里的运行态还在（进程死了没人摘），lookup 返回它，CallAsync
// 撞上已关闭的连接返回裸错误 "ACP 连接已关闭"。manager 的恢复阶梯以
// errors.Is(err, ErrTaskNotRunning) 为触发条件（manager.go 的 doc 明写「Send 由
// executor 实现方自身包装该哨兵」），裸错误判 false，四级阶梯整个没启动——
// 对 grok 而言 Spec A 的冷恢复等于没接上。
func TestSendClosedConnCarriesNotRunning(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")
	r.sessionID = "sess-1"
	r.cli = &ACPClient{log: quietLogger(), pending: map[int]chan ACPResult{}, closed: true}

	err := a.Send(context.Background(), "t1", "继续")
	if err == nil {
		t.Fatal("连接已关闭时 Send 必须报错")
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("Send 错误必须带 ErrTaskNotRunning 哨兵（manager 据此启动恢复阶梯），实得 %v", err)
	}
}

// TestSendNoRunStateCarriesNotRunning 运行态整个不在时的既有行为不能被改坏。
func TestSendNoRunStateCarriesNotRunning(t *testing.T) {
	a := New(quietLogger())
	err := a.Send(context.Background(), "nope", "继续")
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("无运行态时应带哨兵，实得 %v", err)
	}
}
