// 本文件覆盖 agentd 重启后的版本就绪判据、主动催起时机与取消语义。
//
// 边界：只替换 WaitAgentdBack 的外部依赖，不连接真实 agentd 或平台服务管理器。
package shell_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// TestWaitAgentdBackRequiresVersionMatch 是本文件最承重的一条。
//
// 判据必须是「版本号等于期望值」，不能是「Status 调得通」：agentd 优雅
// 关停期间**仍会应答在途请求**，只判连通会立刻通过，然后握手到一个正在
// 退出的进程上。这是「就绪判据取早了」那一类假绿，本仓库已经吃过。
func TestWaitAgentdBackRequiresVersionMatch(t *testing.T) {
	calls := 0
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) {
			calls++
			// 前两次还是旧版本在应答（正在关停），第三次才换上新的
			if calls < 3 {
				return "v0.3.0", nil
			}
			return "v0.4.0", nil
		},
		Nudge: func() error { return nil },
		Sleep: func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatalf("等待失败：%v", err)
	}
	if calls != 3 {
		t.Errorf("查询了 %d 次，想要 3 次——旧版本应答时不能算就绪", calls)
	}
}

// TestWaitAgentdBackNudgesOnceAfterFirstMiss 钉住 Windows 那一下主动催。
//
// Windows 的 KeepAlive 是每分钟一次的模拟（internal/service/windows.go:150
// 的 PT1M），不催就要干等最多 60 秒。催必须在**首次探测失败之后**——
// MultipleInstancesPolicy=IgnoreNew 会把旧进程还没退时的那次催拒掉，而拒绝
// 时写进「上次结果」的正是 0x800710E0，也就是 rc5 那个 bug 的同一个值。
func TestWaitAgentdBackNudgesOnceAfterFirstMiss(t *testing.T) {
	calls, nudges := 0, 0
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "", errors.New("connection refused")
			}
			if calls == 2 {
				return "v0.3.0", nil
			}
			return "v0.4.0", nil
		},
		Nudge: func() error { nudges++; return nil },
		Sleep: func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatalf("等待失败：%v", err)
	}
	if nudges != 1 {
		t.Errorf("催了 %d 次，想要恰好 1 次", nudges)
	}
}

// TestWaitAgentdBackDoesNotNudgeBeforeFirstProbe 钉住「不在旧进程还活着时催」。
func TestWaitAgentdBackDoesNotNudgeBeforeFirstProbe(t *testing.T) {
	nudgedBeforeProbe := false
	probed := false
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) { probed = true; return "v0.4.0", nil },
		Nudge:   func() error { nudgedBeforeProbe = !probed; return nil },
		Sleep:   func(time.Duration) {},
	}
	if err := shell.WaitAgentdBack(context.Background(), "v0.4.0", d); err != nil {
		t.Fatal(err)
	}
	if nudgedBeforeProbe {
		t.Error("在第一次探测之前就催了——此时旧进程可能还没退，催会被 IgnoreNew 拒掉")
	}
}

// TestWaitAgentdBackTimesOut 钉住超时会返回错误而不是永远挂着。
//
// 永远挂着的后果是用户双击了应用、窗口一直不出来，且没有任何解释。
func TestWaitAgentdBackTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := shell.WaitDeps{
		Version: func(context.Context) (string, error) { return "v0.3.0", nil },
		Nudge:   func() error { return nil },
		Sleep:   func(time.Duration) { cancel() },
	}
	err := shell.WaitAgentdBack(ctx, "v0.4.0", d)
	if err == nil {
		t.Fatal("超时必须返回错误")
	}
}
