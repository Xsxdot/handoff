package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
)

func TestWaitDeliveryPolicyMatchesIsDeliverableAlias(t *testing.T) {
	types := []proto.EventType{
		proto.EventTypeQuestion, proto.EventTypePermissionRequest,
		proto.EventTypeDeliveryFailed, proto.EventTypeCompleted,
		proto.EventTypeTurnFailed, proto.EventTypeFailed, proto.EventTypeArchived,
		proto.EventTypeProgress, proto.EventTypePermissionAutoAllow,
		proto.EventTypeApproverDecision, proto.EventTypeApproverDisabled,
		proto.EventTypePermissionReuse, proto.EventTypeTicketsVoided,
		proto.EventTypeTicketAnswered, proto.EventTypeStalled,
	}
	for _, tpe := range types {
		if got := client.WaitDeliveryPolicy(tpe); got != wantDeliverable(tpe) {
			t.Errorf("WaitDeliveryPolicy(%s) = %v, want %v", tpe, got, wantDeliverable(tpe))
		}
	}
}

func TestB353WaitDeliveryPolicyFrozenSet(t *testing.T) {
	falseTypes := []proto.EventType{
		proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled,
		proto.EventTypeTicketsVoided,
		proto.EventTypeTicketAnswered,
		proto.EventTypePermissionAutoAllow,
		proto.EventTypePermissionReuse,
	}
	trueTypes := []proto.EventType{
		proto.EventTypePermissionRequest,
		proto.EventTypeQuestion,
		proto.EventTypeCompleted,
		proto.EventTypeFailed,
		proto.EventTypeTurnFailed,
		proto.EventTypeStalled,
		proto.EventTypeDeliveryFailed,
		proto.EventTypeApprovalDropped,
		proto.EventTypeArchived,
		proto.EventTypeResourcePressure,
		proto.EventTypeTaskProcPressure,
	}
	for _, typ := range falseTypes {
		if got := client.WaitDeliveryPolicy(typ); got {
			t.Errorf("WaitDeliveryPolicy(%q)=true, want false", typ)
		}
	}
	for _, typ := range trueTypes {
		if got := client.WaitDeliveryPolicy(typ); !got {
			t.Errorf("WaitDeliveryPolicy(%q)=false, want true", typ)
		}
	}
}

func wantDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress, proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled, proto.EventTypeTicketsVoided,
		proto.EventTypeTicketAnswered, proto.EventTypePermissionAutoAllow,
		proto.EventTypePermissionReuse:
		return false
	default:
		return true
	}
}

func TestErrTunnelDisconnectedDistinctFromUnreachable(t *testing.T) {
	if errors.Is(client.ErrUnreachable, client.ErrTunnelDisconnected) {
		t.Fatal("两条失败诊断必须能用 errors.Is 分开")
	}
	if client.ErrTunnelDisconnected.Error() == "" {
		t.Fatal("哨兵文案不能为空")
	}
}

func TestDispatchOmitsStandardIdempotencyHeaders(t *testing.T) {
	var got http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proto.Task{ID: "T1"})
	}))
	t.Cleanup(ts.Close)

	if _, err := client.New(ts.URL, "tok").Dispatch(context.Background(), client.DispatchOpts{Prompt: "x"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	for _, h := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		if v := got.Get(h); v != "" {
			t.Fatalf("Dispatch 带了 %s=%q；该头会让 net/http 把 POST 当可重放", h, v)
		}
	}
}

// TestProductionHTTPClientCallersAreGatewayOnly 锁 B233.3 冻结条 25：生产代码不得
// 自取 `HTTPClient()` 拼请求——**自建第二套 HTTP 栈就绕开了选路**（relay 隧道 vs
// 直连），relay 机器会退化成 "no Host in request URL"。
//
// 白名单的判据（2026-09-19 协调者裁决，卡 B378）：**允许的是「复用同一份传输的
// 转发路径」，不是「某一类目录」**。agentd 的两条 forward 是服务端转发；mobilecore
// 的 core.go/proxy.go 是**客户端侧**转发（把对端 agentd 的 HTTP 面经回环反代交给
// webview），它取的是**共享 Transport**（`Transport: cl.HTTPClient().Transport`），
// 正是这条判据要保护的对象——移动端没有网关可绕（它就是客户端），自己 new 一个
// 反而违例。故按文件粒度豁免这两处，其余任何新增取用点（含 mobilecore 新文件）
// 一律红。mobilecore 侧另有对偶守卫 `TestSessionFileHasNoRawHTTPClient`。
func TestProductionHTTPClientCallersAreGatewayOnly(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../.."))
	allowed := map[string]bool{
		// 服务端转发路径（B233.3 原始白名单）
		"internal/agentd/forward.go":    true,
		"internal/agentd/forward_ws.go": true,
		// drop 32 MiB 专用转发（B272）：同属复用共享传输的服务端转发路径；
		// B272 并入基线晚于 B378 豁免裁决，故当时白名单未列它（B380 recon 补记）
		"internal/agentd/drop.go": true,
		// 客户端侧转发路径（B378 裁决：移动核复用共享 Transport，非另起栈）
		"internal/mobilecore/core.go":  true,
		"internal/mobilecore/proxy.go": true,
	}
	var unexpected []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" || base == "web" || base == ".worktrees" || base == "node_modules/.vite" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), ".HTTPClient()") {
			unexpected = append(unexpected, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unexpected) > 0 {
		t.Fatalf("生产代码不得自取 HTTPClient 拼请求（只允许复用共享传输的转发路径："+
			"agentd/forward*.go 服务端 + mobilecore/{core,proxy}.go 客户端侧）: %s",
			strings.Join(unexpected, ", "))
	}
}

func TestWSDialOptionsPinsClientTransport(t *testing.T) {
	c := client.New("http://127.0.0.1:1", "tok")
	opts := client.ExportWSDialOptions(c)
	if opts.HTTPClient == nil {
		t.Fatal("wsDialOptions 必须显式给出 HTTPClient，否则 coder/websocket 退回 DefaultClient")
	}
	if opts.HTTPClient.Timeout != 0 {
		t.Fatalf("HTTPClient.Timeout = %s, want 0（coder/websocket 硬要求）", opts.HTTPClient.Timeout)
	}
	if opts.HTTPClient != c.HTTPClient() {
		t.Fatal("ws 拨号必须复用本 Client 的传输（直连 Proxy:nil / relay 隧道）")
	}
}

func TestTransportAndExecutionClientCompileOnAggregate(t *testing.T) {
	c := client.New("http://127.0.0.1:1", "tok")
	var tr client.Transport = c
	var ex client.ExecutionClient = c
	if tr.BaseURL() == "" {
		t.Fatal("Transport.BaseURL 不能为空")
	}
	if tr.HTTPClient() == nil {
		t.Fatal("Transport.HTTPClient 不能为空")
	}
	_ = ex
}

func TestRelayClosedDispatchIsTunnelDisconnected(t *testing.T) {
	d := relay.NewDialer("ws://127.0.0.1:1/relay", "c", "n",
		"0123456789abcdef0123456789abcdef", "", slog.Default())
	cl := client.NewRelay(d, "tok")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := cl.Dispatch(context.Background(), client.DispatchOpts{Prompt: "x"})
	if err == nil {
		t.Fatal("关闭的 relay 隧道上 Dispatch 必须失败")
	}
	if !errors.Is(err, client.ErrTunnelDisconnected) {
		t.Fatalf("要 ErrTunnelDisconnected，实得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("隧道断开不得与 ErrUnreachable 互认：%v", err)
	}
}

func TestRelayCanceledDispatchIsNotTunnelOrUnreachable(t *testing.T) {
	d := relay.NewDialer("ws://127.0.0.1:1/relay", "c", "n",
		"0123456789abcdef0123456789abcdef", "", slog.Default())
	t.Cleanup(func() { _ = d.Close() })
	cl := client.NewRelay(d, "tok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cl.Dispatch(ctx, client.DispatchOpts{Prompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("要 context.Canceled，实得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) || errors.Is(err, client.ErrTunnelDisconnected) {
		t.Fatalf("ctx 取消不得包装成两条网络哨兵：%v", err)
	}
}

func TestMarkForwardedRelayCopyKeepsTunnelSentinel(t *testing.T) {
	d := relay.NewDialer("ws://127.0.0.1:1/relay", "c", "n",
		"0123456789abcdef0123456789abcdef", "", slog.Default())
	cl := client.NewRelay(d, "tok").MarkForwarded()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := cl.Dispatch(context.Background(), client.DispatchOpts{Prompt: "x"})
	if !errors.Is(err, client.ErrTunnelDisconnected) {
		t.Fatalf("MarkForwarded 副本必须仍识别隧道断开，实得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("副本不得退回 ErrUnreachable：%v", err)
	}
}

type countRoundTripper struct {
	n    int
	next http.RoundTripper
}

func (c *countRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.n++
	return c.next.RoundTrip(req)
}

func TestDispatchDoOncePerCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(proto.Task{ID: "T1"})
	}))
	t.Cleanup(ts.Close)
	cl := client.New(ts.URL, "tok")
	ct := &countRoundTripper{next: cl.HTTPClient().Transport}
	cl.HTTPClient().Transport = ct
	if _, err := cl.Dispatch(context.Background(), client.DispatchOpts{Prompt: "x"}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if ct.n != 1 {
		t.Fatalf("do 单次调用 hc.Do 次数=%d，want 1（重试不得放进 do）", ct.n)
	}
}
