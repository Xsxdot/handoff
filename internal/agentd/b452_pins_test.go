package agentd

// b452_pins_test.go —— 修复轮第 6 条：冻结承诺的直接锁（F14 防环、F6 零出站、F2/F5 HTTP 层 400、F12 能力位）。
// 「零出站」一律用计数对端判，不只看零任务。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Xsxdot/handoff/internal/testhttp"
)

// b452CountingPeer 把 A 的 t452-linux 目标换成计数对端（任何请求都记一笔并回 200 {}）。
func b452CountingPeer(t *testing.T, p *b452Pair) *b452Capture {
	t.Helper()
	counter := &b452Capture{code: http.StatusOK, body: `{}`}
	cs := testhttp.NewServer(t, counter)
	p.a.setTargets(t, map[string]string{b452MachineB: strings.TrimPrefix(cs.URL, "http://")})
	return counter
}

// F14：带转发头的 /ws/events 忽略 machine，不再转发——在本机判任务，不存在即 1008，计数对端零请求。
func TestB452EventsWSForwardedIgnoresMachine(t *testing.T) {
	p := newB452Pair(t, nil)
	counter := b452CountingPeer(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(p.a.ts)+"/ws/events?task=no-such-task&from_seq=0&machine="+b452MachineB,
		&websocket.DialOptions{HTTPHeader: http.Header{
			"Authorization": {"Bearer " + testToken}, forwardedHeader: {"1"}}})
	if err != nil {
		t.Fatalf("带转发头的拨号应在本机受理: %v", err)
	}
	defer conn.CloseNow()
	_, _, rerr := conn.Read(ctx)
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("本机没有该任务应 1008 关闭，得 %v", rerr)
	}
	if n := counter.count(); n != 0 {
		t.Fatalf("带转发头时不得再转发，计数对端收到 %d 次", n)
	}
}

// F6：亲和拒绝零出站——载体在他机 + here → 422，计数对端零请求（不探活、不转发）。
func TestB452AffinityRefusalZeroOutbound(t *testing.T) {
	p := newB452Pair(t, nil)
	counter := b452CountingPeer(t, p)
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`","here":true`), false)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `"kind":"affinity_unmet"`) {
		t.Fatalf("应 422 affinity_unmet，得 %d %s", code, body)
	}
	if n := counter.count(); n != 0 {
		t.Fatalf("亲和拒绝不得出站，计数对端收到 %d 次", n)
	}
	if p.a.taskCount(t) != 0 || p.b.taskCount(t) != 0 {
		t.Fatal("亲和拒绝零任务")
	}
}

// F2/F5：HTTP 层——非法 mode → 400；machine 与 here 同给 → 400；都零出站、零任务。
func TestB452BadModeAndMachineHere400ZeroOutbound(t *testing.T) {
	p := newB452Pair(t, nil)
	counter := b452CountingPeer(t, p)
	for name, extra := range map[string]string{
		"非法 mode":        `,"receiver":"` + b452CarrierB + `","mode":"ro"`,
		"machine × here": `,"receiver":"` + b452CarrierB + `","here":true,"machine":"` + b452MachineB + `"`,
	} {
		code, body := b452Post(t, p.a, p.body(extra), false)
		if code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，得 %d %s", name, code, body)
		}
	}
	if n := counter.count(); n != 0 {
		t.Fatalf("400 拒绝不得出站，计数对端收到 %d 次", n)
	}
	if p.a.taskCount(t) != 0 || p.b.taskCount(t) != 0 {
		t.Fatal("400 拒绝零任务")
	}
}

// F12：/api/status 直接上报 skilled_dispatch_supported:true。
func TestB452StatusReportsSkilledDispatchSupported(t *testing.T) {
	p := newB452Pair(t, nil)
	code, body := b452Get(t, p.a, "/api/status")
	if code != http.StatusOK {
		t.Fatalf("status: %d %s", code, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["skilled_dispatch_supported"]) != "true" {
		t.Fatalf("应上报 skilled_dispatch_supported:true，得 %s", raw["skilled_dispatch_supported"])
	}
}
