// unreachable_test.go —— ErrUnreachable 哨兵的判别边界。
//
// 三条用例锁死同一件事的三个面：够不着要判为够不着，拿到了响应的失败不许
// 判为够不着，ctx 取消也不许判为够不着。中间那条是这个哨兵存在的理由——
// 调用方据它决定「降级继续」还是「就此失败」，判错方向就是脏登记或假失败。
package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
)

// testOrigin 是三条用例共用的请求体填充值，内容本身不参与断言。
const testOrigin = "git@example.com:x/handoff.git"

// TestUnreachableWhenNoResponse：对着无人监听的端口请求 → 判为够不着。
//
// 为什么用端口 1：它必定 connection refused，且不发起任何真实网络访问，
// 在 CI 与离线机器上结论一致。
func TestUnreachableWhenNoResponse(t *testing.T) {
	cl := client.New("http://127.0.0.1:1", "tok")
	_, err := cl.ProjectAdd(context.Background(), client.ProjectAddOpts{OriginURL: testOrigin})
	if err == nil {
		t.Fatal("对着无人监听的端口请求应失败")
	}
	if !errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("应判为够不着，实得 %v", err)
	}
}

// TestNotUnreachableOnHTTPError：409 拿到了响应 → 不是够不着。
//
// 能收到 409 说明 TCP 通、HTTP 正常、Bearer 已过——这是真冲突，调用方
// 必须失败而不是降级继续，否则就是往表里写脏登记。
func TestNotUnreachableOnHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "路径已被另一个项目占用", http.StatusConflict)
	}))
	defer ts.Close()

	cl := client.New(ts.URL, "tok")
	_, err := cl.ProjectAdd(context.Background(), client.ProjectAddOpts{OriginURL: testOrigin})
	if err == nil {
		t.Fatal("409 应返回错误")
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("拿到了响应就不是够不着：%v", err)
	}
}

// TestNotUnreachableOnContextCancel：ctx 取消 → 不是够不着，且保留 context.Canceled。
//
// 为什么必须单列：取消同样从 hc.Do 的错误返回出来。混进 ErrUnreachable
// 会让调用方的降级分支在用户按下 Ctrl-C 之后继续往下走。
func TestNotUnreachableOnContextCancel(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // 挂住直到用例结束，保证取消一定发生在响应之前
	}))
	defer func() { close(block); ts.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	cl := client.New(ts.URL, "tok")
	_, err := cl.ProjectAdd(ctx, client.ProjectAddOpts{OriginURL: testOrigin})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应保留 ctx 取消语义，实得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("ctx 取消不是够不着：%v", err)
	}
}
