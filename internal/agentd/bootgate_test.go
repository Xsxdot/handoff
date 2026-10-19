package agentd

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// 竖切：agentd 启动期（账本挂载重试中）`handoff status` 经真实 client 拿到
// 「账本未挂载」诊断，而不是 connection refused；Ready 后透传到真实 handler。
func TestBootGateStatusDuringLedgerRetry(t *testing.T) {
	gate := NewBootGate(nil)
	ts := testhttp.NewServer(t, gate)
	gate.SetPhase("账本未挂载：共享账本连接失败，第 3 次重试（SQLSTATE 53300），约 4s 后再试")

	c := client.New(ts.URL, "tok")
	_, err := c.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "账本未挂载") || !strings.Contains(err.Error(), "503") {
		t.Fatalf("启动期 status 应得 503 + 账本未挂载诊断，得 %v", err)
	}
	if errors.Is(err, client.ErrUnreachable) {
		t.Fatalf("启动期 agentd 已占端口，不应判为够不着: %v", err)
	}

	gate.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	resp, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("Ready 后应透传真实 handler，得 %d", resp.StatusCode)
	}
}
