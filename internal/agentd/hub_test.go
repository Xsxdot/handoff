// agentd 包测试：验证 hub 的事件实时扇出（Subscribe/Publish）与 ticket 应答路由（WaitAnswer/NotifyAnswer）。
package agentd_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestMain 把默认 logger 指向丢弃输出，保证测试输出干净（hub 内部日志点不在本包断言输出）。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	os.Exit(m.Run())
}

// TestPublishFanout 验证：两个订阅者都收到广播；cancel 后不再收且通道关闭、重复取消不 panic；Publish 不阻塞。
func TestPublishFanout(t *testing.T) {
	hub := agentd.NewHub()
	ch1, cancel1 := hub.Subscribe("t1")
	ch2, cancel2 := hub.Subscribe("t1")
	defer cancel2()

	// 两个订阅者都能收到同一条事件
	hub.Publish(proto.Event{Seq: 1, TaskID: "t1", Type: proto.EventTypeProgress})
	for name, ch := range map[string]<-chan proto.Event{"ch1": ch1, "ch2": ch2} {
		select {
		case ev := <-ch:
			if ev.Seq != 1 || ev.TaskID != "t1" {
				t.Fatalf("%s 收到的事件不对: %+v", name, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 未收到广播事件", name)
		}
	}

	// cancel 后：不再收到新事件、通道被关闭；重复取消幂等不 panic；Publish 不阻塞
	cancel1()
	cancel1()
	hub.Publish(proto.Event{Seq: 2, TaskID: "t1", Type: proto.EventTypeProgress})

	select {
	case ev, ok := <-ch1:
		if ok {
			t.Fatalf("cancel 后仍收到事件: %+v", ev)
		}
		// ok == false：通道已关闭，符合取消语义
	case <-time.After(2 * time.Second):
		t.Fatalf("cancel 后通道未关闭")
	}

	select {
	case ev := <-ch2:
		if ev.Seq != 2 {
			t.Fatalf("ch2 收到的事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("ch2 未收到取消后的事件")
	}
}

// TestWaitAnswerBeforeAndAfter 覆盖应答路由：先 Wait 后 Notify 能收到；ctx 取消返回 ctx.Err() 并清理等待者；
// Notify 无人等待不 panic；应答一次性（先 Notify 后 Wait 拿不到旧应答）。
func TestWaitAnswerBeforeAndAfter(t *testing.T) {
	t.Run("先 Wait 后 Notify 能收到", func(t *testing.T) {
		hub := agentd.NewHub()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		type res struct {
			ans string
			err error
		}
		got := make(chan res, 1)
		go func() {
			ans, err := hub.WaitAnswer(ctx, "ticket-1")
			got <- res{ans, err}
		}()

		// 等 WaitAnswer 完成注册后再通知，保证「先 Wait 后 Notify」的时序
		time.Sleep(50 * time.Millisecond)
		if !hub.NotifyAnswer("ticket-1", "yes") {
			t.Fatal("有等待者时 NotifyAnswer 应返回 true")
		}

		select {
		case r := <-got:
			if r.err != nil || r.ans != "yes" {
				t.Fatalf("应答不对: ans=%q err=%v", r.ans, r.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("WaitAnswer 未收到应答")
		}
	})

	t.Run("Notify 无人等待不 panic 且返回 false，应答一次性", func(t *testing.T) {
		hub := agentd.NewHub()
		// 无人等待时 Notify 不应 panic，且必须返回 false（供调用方走 RelayAnswer 自愈中继）
		if hub.NotifyAnswer("ticket-ghost", "42") {
			t.Fatal("无人等待时 NotifyAnswer 应返回 false")
		}

		// 先 Notify 的旧应答不会被后来的 Wait 拿到：等待应超时
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if _, err := hub.WaitAnswer(ctx, "ticket-ghost"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("旧应答被投递，期望超时，实际 err=%v", err)
		}
	})

	t.Run("ctx 取消返回 ctx.Err() 并清理等待者", func(t *testing.T) {
		hub := agentd.NewHub()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := hub.WaitAnswer(ctx, "ticket-cancel")
			done <- err
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ctx 取消后返回 %v，期望 context.Canceled", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("ctx 取消后 WaitAnswer 未返回")
		}

		// done 收到意味着等待者已从表里移除，此时 Notify 应返回 false（无等待者）
		if hub.NotifyAnswer("ticket-cancel", "late") {
			t.Fatal("等待者已移除后 NotifyAnswer 应返回 false")
		}
	})
}

// TestPublishDropsSlowSubscriber 验证慢订阅者被 select-default 丢弃：Publish 永不阻塞，快订阅者不受影响收全量。
func TestPublishDropsSlowSubscriber(t *testing.T) {
	hub := agentd.NewHub()
	slow, cancelSlow := hub.Subscribe("t1") // 从不消费，缓冲必然写满
	fast, cancelFast := hub.Subscribe("t1")
	defer cancelSlow()
	defer cancelFast()

	const n = 100
	publishDone := make(chan struct{})
	go func() {
		for i := 1; i <= n; i++ {
			hub.Publish(proto.Event{Seq: int64(i), TaskID: "t1", Type: proto.EventTypeProgress})
			// 给快订阅者留出消费时间，保证它不因自身掉队而丢事件
			time.Sleep(time.Millisecond)
		}
		close(publishDone)
	}()

	// 快订阅者边发布边消费，应收到全部 n 条且顺序完整
	for i := 1; i <= n; i++ {
		select {
		case ev := <-fast:
			if ev.Seq != int64(i) {
				t.Fatalf("快订阅者第 %d 条 seq=%d", i, ev.Seq)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("快订阅者只收到 %d/%d 条", i-1, n)
		}
	}

	select {
	case <-publishDone:
		// 若 Publish 会阻塞在慢订阅者的满缓冲上，发布协程无法在 2s 内完成，这里将超时
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢订阅者阻塞")
	}

	// 慢订阅者只残留缓冲内的部分事件（被丢弃），而不是全部
	if got := len(slow); got <= 0 || got >= n {
		t.Fatalf("慢订阅者残留 %d 条事件，期望 0 < n < %d", got, n)
	}
}

// TestWatchersCountsSubscribers 验证 Watchers 精确反映当前订阅数：
// 未订阅为 0、订阅后逐个累加、取消后逐个回落、全部取消后归零。
//
// 为什么这个数字必须干净：handoff status 的「⚠ 无人值守」直接以它为判据，
// 多算一个（内部订阅者虚高）就是漏报，少算一个就是误报。
func TestWatchersCountsSubscribers(t *testing.T) {
	hub := agentd.NewHub()

	if n := hub.Watchers("t-watch"); n != 0 {
		t.Fatalf("未订阅时 Watchers = %d, want 0", n)
	}
	_, cancel1 := hub.Subscribe("t-watch")
	if n := hub.Watchers("t-watch"); n != 1 {
		t.Fatalf("一个订阅者时 Watchers = %d, want 1", n)
	}
	_, cancel2 := hub.Subscribe("t-watch")
	if n := hub.Watchers("t-watch"); n != 2 {
		t.Fatalf("两个订阅者时 Watchers = %d, want 2", n)
	}
	// 别的任务不受影响：hub 按 taskID 分表，串号会让整条判据失效
	if n := hub.Watchers("t-other"); n != 0 {
		t.Fatalf("其他任务的 Watchers = %d, want 0", n)
	}

	cancel1()
	if n := hub.Watchers("t-watch"); n != 1 {
		t.Fatalf("取消一个后 Watchers = %d, want 1", n)
	}
	cancel2()
	cancel2() // 重复取消幂等，不得把计数减成负数
	if n := hub.Watchers("t-watch"); n != 0 {
		t.Fatalf("全部取消后 Watchers = %d, want 0", n)
	}
}

// TestWatchersConcurrent 验证并发订阅/取消/读取下 Watchers 不数据竞争。
// 单跑无意义，价值在 -race 下（见本 task 的 Step 4）。
func TestWatchersConcurrent(t *testing.T) {
	hub := agentd.NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, cancel := hub.Subscribe("t-race")
			_ = hub.Watchers("t-race")
			cancel()
		}()
	}
	wg.Wait()
	if n := hub.Watchers("t-race"); n != 0 {
		t.Fatalf("并发收尾后 Watchers = %d, want 0", n)
	}
}

// TestCloseTaskClosesAllSubscribers 验证 CloseTask 关闭该任务全部订阅、
// 返回关闭数、不误伤别的任务，且随后的 cancel 幂等不 panic（不得二次 close）。
func TestCloseTaskClosesAllSubscribers(t *testing.T) {
	hub := agentd.NewHub()
	ch1, cancel1 := hub.Subscribe("t-done")
	ch2, cancel2 := hub.Subscribe("t-done")
	chOther, cancelOther := hub.Subscribe("t-live")
	defer cancelOther()

	if n := hub.CloseTask("t-done"); n != 2 {
		t.Fatalf("CloseTask 返回 %d, want 2", n)
	}
	for name, ch := range map[string]<-chan proto.Event{"ch1": ch1, "ch2": ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("%s 归档后仍收到事件", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s 未被关闭", name)
		}
	}
	if n := hub.Watchers("t-done"); n != 0 {
		t.Fatalf("归档后 Watchers = %d, want 0", n)
	}
	// 别的任务不受影响
	hub.Publish(proto.Event{Seq: 9, TaskID: "t-live", Type: proto.EventTypeProgress})
	select {
	case ev := <-chOther:
		if ev.Seq != 9 {
			t.Fatalf("t-live 收到的事件不对: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("t-live 的订阅被误伤")
	}

	// 连接收尾时的 defer cancel 必须是空操作：通道已不在表中，重复 close 会 panic
	cancel1()
	cancel2()
	if n := hub.CloseTask("t-done"); n != 0 {
		t.Fatalf("重复 CloseTask 返回 %d, want 0", n)
	}
	// Publish 到已归档任务不得 panic（向已关闭通道发送）
	hub.Publish(proto.Event{Seq: 10, TaskID: "t-done", Type: proto.EventTypeProgress})
}
