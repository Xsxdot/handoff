package shell

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 已经就绪时必须立刻返回：托盘菜单第二次「打开控制台」走的就是这条，
// 那时 channel 早已关闭，不该再等一次。
func TestAwaitWebviewReadyReturnsImmediatelyWhenAlreadyReady(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if err := AwaitWebviewReady(ctx, ready); err != nil {
		t.Fatalf("已就绪时不该报错: %v", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("已就绪时不该阻塞，实际等了 %v", d)
	}
}

// 等待期间就绪：这是首次启动的正常形态——SetURL 必须晚于 setupChromium。
func TestAwaitWebviewReadyWaitsUntilSignalled(t *testing.T) {
	ready := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(ready)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := AwaitWebviewReady(ctx, ready); err != nil {
		t.Fatalf("信号到达后不该报错: %v", err)
	}
}

// **超时必须报错，不能悄悄放行。** 这条是承重的：放行等于回到
// 「chromium 还没建好就 Navigate」，而那个失败没有 Go panic、没有弹框，
// 用户看到的只是「双击没反应」——最难排查的一类。
func TestAwaitWebviewReadyErrsOnTimeoutInsteadOfProceeding(t *testing.T) {
	ready := make(chan struct{}) // 永不关闭
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := AwaitWebviewReady(ctx, ready)
	if err == nil {
		t.Fatal("等不到就绪时必须报错；放行会让进程在 Navigate 时直接消失")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误必须裹住 ctx 的原因，实际: %v", err)
	}
}
