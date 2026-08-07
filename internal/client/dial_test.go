// WS 拨号超时的白盒测试（package client）：访问未导出的 waitOnce 与可注入的
// dialTimeout，验证单次拨号对「黑洞对端」在一个 dialTimeout 内失败，
// 不让每次重连拖到 ~2min。
package client

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestWaitOnceDialTimeout 对「接受 TCP 但永不响应 WS 握手」的黑洞对端拨号：
// 单次 waitOnce 必须在一个 dialTimeout 内返回错误——修复前每次拨号会挂到外层
// ctx（本测试 3s）才失败，修复后 ~200ms 即失败并交给外层退避重连。
func TestWaitOnceDialTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	// 黑洞服务端：接受连接后不写任何字节（握手永不完成），5s 后关闭连接兜底
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				time.Sleep(5 * time.Second)
				c.Close()
			}(c)
		}
	}()

	orig := dialTimeout
	dialTimeout = 200 * time.Millisecond
	defer func() { dialTimeout = orig }()

	// 外层 ctx 3s 兜底：修复前拨号会挂到外层 ctx 才失败（elapsed≈3s），
	// 修复后必在 dialTimeout 内失败（elapsed≈200ms）
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cl := New("http://"+ln.Addr().String(), "test-token")

	start := time.Now()
	_, err = cl.waitOnce(ctx, "t1", 0, false)
	if err == nil {
		t.Fatal("黑洞对端拨号应失败")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("拨号超时未生效: 单次拨号耗时 %v（应 ≤ dialTimeout=200ms，外层 ctx 只是兜底）", elapsed)
	}
}
