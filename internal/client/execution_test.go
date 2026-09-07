package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
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

func TestProductionHTTPClientCallersAreGatewayOnly(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../.."))
	allowed := map[string]bool{
		"internal/agentd/forward.go":    true,
		"internal/agentd/forward_ws.go": true,
	}
	var unexpected []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" || base == "web" {
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
		t.Fatalf("生产代码不得再取 HTTPClient 拼请求（网关 forward 除外）: %s", strings.Join(unexpected, ", "))
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
