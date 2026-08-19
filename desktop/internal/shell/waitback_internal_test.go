// 本文件只验证 WaitAgentdBack 的 deadline 分支，使用内部时间缝避免真实等待 90 秒。
package shell

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestWaitAgentdBackDeadlineBranchReturnsTimeoutError 直接走到 deadline 分支。
//
// 外部包测试用 context 取消来避免真等 90 秒，但那条路径即使误把超时 return
// 写成 continue 也会从取消分支返回错误，测不到承重的超时结论。因此这里推进
// 时间缝，断言错误确实说明「超时」，而不是只断言「有错误」。
func TestWaitAgentdBackDeadlineBranchReturnsTimeoutError(t *testing.T) {
	originalNow := waitBackNow
	t.Cleanup(func() { waitBackNow = originalNow })
	nowCalls := 0
	waitBackNow = func() time.Time {
		nowCalls++
		if nowCalls == 1 {
			return time.Unix(0, 0)
		}
		return time.Unix(91, 0)
	}
	err := WaitAgentdBack(context.Background(), "v0.4.0", WaitDeps{
		Version: func(context.Context) (string, error) { return "v0.3.0", nil },
		Sleep:   func(time.Duration) {},
	})
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("deadline 分支应返回超时错误，得到 %v", err)
	}
}
