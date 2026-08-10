package opencode

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/prochost"
)

// TestStopPropagatesStillAlive 验证 opencode 的保留-重试机制在**新的** Kill
// 语义下真的会被触发：这条路径过去是休眠的（Kill 从不报「还活着」），
// 所以必须实测，不能假定它一直好着。
func TestStopPropagatesStillAlive(t *testing.T) {
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, "t-still-alive", t.TempDir(), t.TempDir())
	ad.reapInterval = 5 * time.Millisecond
	probe := ad.lookup("t-still-alive").handle.(*fakeProbe)
	probe.setKillErr(fmt.Errorf("回收进程组 4242: %w", prochost.ErrStillAlive))

	err := ad.Stop("t-still-alive")
	if !errors.Is(err, prochost.ErrStillAlive) {
		t.Fatalf("Stop err = %v, want errors.Is(..., prochost.ErrStillAlive)", err)
	}
	// 保留分支的前提是 handle.Alive() 为真；若 fakeProbe 默认不存活，
	// Stop 会走「kill 失败但 serve 已自灭」分支返回 nil。此时用该包既有的
	// 存活 setter 把它置为存活（**不要**改生产代码去迁就测试）。
	if ad.lookup("t-still-alive") == nil {
		t.Fatal("kill 报仍存活时应保留运行态，交给 reapRetained 后台重试")
	}
}
