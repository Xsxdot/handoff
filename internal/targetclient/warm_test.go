// 本文件验证 relay 预热的筛选、逐台退避和上下文退出行为。
//
// 边界：通过 ensure 缝替换真实拨号，只验证循环调度，不依赖外部 relay 服务。
package targetclient

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
)

// TestWarmOnlyTouchesRelayTargets：预热只碰 relay 机器。
//
// why：直连没有隧道可预热，对它调 Ensure 纯属空转；更要紧的是别让直连机器的
// 「预热失败」进日志——那会造出一个不存在的故障。
func TestWarmOnlyTouchesRelayTargets(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"direct": {Addr: "10.0.0.2:7777", Token: "t"},
		"relayed": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "n", Token: "0123456789abcdef0123456789abcdef"},
	}), slog.Default())
	defer p.Close()

	var mu sync.Mutex
	var touched []string
	p.ensure = func(ctx context.Context, name string) error {
		mu.Lock()
		defer mu.Unlock()
		touched = append(touched, name)
		return nil
	}
	p.warmTick = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	p.Warm(ctx)

	mu.Lock()
	defer mu.Unlock()
	for _, name := range touched {
		if name != "relayed" {
			t.Fatalf("预热碰了非 relay 机器: %v", touched)
		}
	}
	if len(touched) == 0 {
		t.Fatal("relay 机器一次都没被预热")
	}
}

// TestWarmBacksOffPerTarget：一台机器失败不影响另一台的节奏。
//
// why：一台长期离线的机器如果能把全局退避拖长，另一台刚上线的机器就要陪着等——
// 退避必须各算各的。
func TestWarmBacksOffPerTarget(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{
		"bad": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "bad", Token: "0123456789abcdef0123456789abcdef"},
		"good": {Relay: "wss://r.example.com/relay", Credential: "c",
			Node: "good", Token: "0123456789abcdef0123456789abcdef"},
	}), slog.Default())
	defer p.Close()

	var mu sync.Mutex
	counts := map[string]int{}
	p.ensure = func(ctx context.Context, name string) error {
		mu.Lock()
		counts[name]++
		mu.Unlock()
		if name == "bad" {
			return errors.New("节点离线")
		}
		return nil
	}
	p.warmTick = 10 * time.Millisecond
	p.warmBackoffInitial = 500 * time.Millisecond // 远大于 tick：bad 会被跳过

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()
	p.Warm(ctx)

	mu.Lock()
	defer mu.Unlock()
	if counts["good"] < 3 {
		t.Fatalf("正常机器要每轮都预热，实得 %d 次", counts["good"])
	}
	if counts["bad"] > 2 {
		t.Fatalf("失败机器要退避，实得 %d 次", counts["bad"])
	}
}

// TestWarmStopsOnContextCancel：ctx 取消后立刻返回。
func TestWarmStopsOnContextCancel(t *testing.T) {
	p := NewPool(confOf(map[string]config.Target{}), slog.Default())
	defer p.Close()
	p.warmTick = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Warm(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Warm 没有返回")
	}
}
