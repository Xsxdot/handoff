// ws_backoff_test.go —— WaitEvent 断线重连退避的回归测试（审查报告 A-9）。
//
// 职责：验证退避在「连接活够健康门槛」之后复位，不会在一次长时间离线之后
// 于整个 wait 期间钉死在封顶值。
//
// 边界：只用一个自控的 WS 端点，不起真 agentd——被测的是重连节奏本身。
package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/client"
)

// TestWaitEventBackoffResetsAfterHealthyConnection 验证 A-9：
// 连续失败把退避抬到高位后，一次真正活过健康门槛的连接必须让退避复位。
//
// 缺陷形态：backoff 声明在重连循环外且永不复位——长时间离线把它推到 60s 封顶后，
// 即便对端早已恢复，余下整个 wait 期间每次断线都要再等 60s 才重连。
//
// 时间线（注入退避 100ms 起、2s 封顶，健康门槛 150ms）：
//   - conn1/2/3 立即断开 → 退避 100→200→400→800
//   - conn4 保持 250ms（> 门槛）后断开 → 退避复位 100ms
//   - conn5 应在 conn4 之后约 350ms（250ms 存活 + 100ms 退避）到来；
//     未复位则要 250ms + 800ms
func TestWaitEventBackoffResetsAfterHealthyConnection(t *testing.T) {
	var mu sync.Mutex
	var connTimes []time.Time
	conns := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		conns++
		n := conns
		connTimes = append(connTimes, time.Now())
		mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		switch {
		case n == 4:
			time.Sleep(250 * time.Millisecond) // 健康连接：活过门槛
		case n >= 5:
			<-r.Context().Done() // 末次连接挂住，等测试取消
		}
		c.Close(websocket.StatusNormalClosure, "")
	}))
	defer func() {
		ts.CloseClientConnections()
		ts.Close()
	}()

	cli := client.NewWithWSTiming(ts.URL, "", 100*time.Millisecond, 2*time.Second, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cli.WaitEvent(ctx, "task-backoff", false)
	}()

	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		n := conns
		mu.Unlock()
		if n >= 5 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("超时未等到 5 次连接，当前 %d 次", n)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if gap := connTimes[4].Sub(connTimes[3]); gap > 600*time.Millisecond {
		t.Errorf("健康连接后退避未复位：conn4→conn5 间隔 %v，期望约 350ms（250ms 存活 + 100ms 退避）", gap)
	}
}
