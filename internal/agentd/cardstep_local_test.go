// 本文件锁住节点派发的本机 HTTP 接缝：空 target 与 loopback 登记名都归一到
// 当前 agentd，纪律探活仍经 Status，实际 Dispatch 请求的 wire target 保持空值。
// 边界：只替换外部 HTTP 服务，不替换 Server 的 client 路由；WS 等待回归在
// ledgerstep 的真实 client 测试中覆盖。
package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

type localStepHTTPHarness struct {
	srv          *Server
	backend      *store.Store
	ledger       *ledger.Store
	ts           *httptest.Server
	statusHits   atomic.Int32
	dispatchHits atomic.Int32
	mu           sync.Mutex
	statusBody   string
	bodies       []map[string]json.RawMessage
}

func newLocalStepHTTPHarness(t *testing.T, statusBody string) *localStepHTTPHarness {
	t.Helper()
	h := &localStepHTTPHarness{statusBody: statusBody}
	h.ts = testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/status":
			h.statusHits.Add(1)
			h.mu.Lock()
			body := h.statusBody
			h.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/api/tasks" && r.Method == http.MethodPost:
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			h.dispatchHits.Add(1)
			h.mu.Lock()
			h.bodies = append(h.bodies, body)
			id := fmt.Sprintf("local-step-%d", len(h.bodies))
			h.mu.Unlock()
			now := time.Now().UTC()
			task := &proto.Task{ID: id, State: proto.TaskStateRunning,
				BaseCommit: strings.Repeat("a", 40), CreatedAt: now, UpdatedAt: now}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(task); err != nil {
				t.Errorf("编码本机 task 响应: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	addr := strings.TrimPrefix(h.ts.URL, "http://")
	backend, err := store.Open(t.TempDir() + "/handoff.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	lst, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lst.Close() })
	seedAgentdLedger(t, lst)
	cfg := &config.Config{
		Listen: addr, Token: testToken, DataDir: t.TempDir(),
		Targets: map[string]config.Target{
			"local": {Addr: "http://" + addr, Token: testToken},
		},
	}
	h.srv = NewServer(cfg, backend, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.srv.SetLedger(lst)
	h.backend, h.ledger = backend, lst
	return h
}

func (h *localStepHTTPHarness) setStatusBody(body string) {
	h.mu.Lock()
	h.statusBody = body
	h.mu.Unlock()
}

func (h *localStepHTTPHarness) lastBody() map[string]json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.bodies) == 0 {
		return nil
	}
	return h.bodies[len(h.bodies)-1]
}

// TestLocalStepDisciplineProbesStatus 证明空 target 不再跳过纪律探活：本机
// Status 命中一次后按 capability 三态继续既有拒发闸，成功结果仍返回空身份。
func TestLocalStepDisciplineProbesStatus(t *testing.T) {
	h := newLocalStepHTTPHarness(t, `{"disciplines_supported":true}`)
	if _, err := h.ledger.PutDiscipline(discipline.NameImplement, "本机纪律探活MARK"); err != nil {
		t.Fatal(err)
	}
	node := ledger.NodeDef{Name: "进行中", Dispatch: true, Template: "feature-impl"}
	resolved, target, err := h.srv.resolveStepDiscipline(node, "")
	if err != nil {
		t.Fatalf("本机纪律探活: %v", err)
	}
	if target != "" || !strings.Contains(resolved.Text, "本机纪律探活MARK") {
		t.Fatalf("本机纪律结果 target/text = %q/%q", target, resolved.Text)
	}
	if got := h.statusHits.Load(); got != 1 {
		t.Fatalf("本机 /api/status 命中 %d 次，期望 1", got)
	}

	h.setStatusBody(`{}`)
	_, target, err = h.srv.resolveStepDiscipline(node, "")
	if err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("能力位缺失应走既有拒发闸，target=%q err=%v", target, err)
	}
	if target != "" || h.statusHits.Load() != 2 {
		t.Fatalf("拒发后 target/statusHits = %q/%d，期望空/2", target, h.statusHits.Load())
	}
}

// TestLocalStepTransportUsesLocalClient 穿过真实 client HTTP JSON 边界，锁住
// 空 target 与 local 登记名最终都以 target:"" 建出任务；空 target 不会进入池的
// For("")，而服务端响应的 BaseCommit 原样回传。
func TestLocalStepTransportUsesLocalClient(t *testing.T) {
	h := newLocalStepHTTPHarness(t, `{"disciplines_supported":true}`)
	for _, target := range []string{"", "local"} {
		id, base, err := h.srv.stepTransport(context.Background(), ledgerstep.DispatchOpts{
			Target: target, Project: "demo", Prompt: "本机任务", Executor: "opencode",
		})
		if err != nil {
			t.Fatalf("stepTransport(%q): %v", target, err)
		}
		if id == "" || base != strings.Repeat("a", 40) {
			t.Fatalf("stepTransport(%q) task/base = %q/%q", target, id, base)
		}
		body := h.lastBody()
		rawTarget, ok := body["target"]
		if !ok {
			t.Fatalf("stepTransport(%q) wire 缺 target 键: %v", target, body)
		}
		var gotTarget string
		if err := json.Unmarshal(rawTarget, &gotTarget); err != nil {
			t.Fatalf("stepTransport(%q) 解 target: %v", target, err)
		}
		if gotTarget != "" {
			t.Fatalf("stepTransport(%q) wire target = %q，期望空字符串", target, gotTarget)
		}
	}
	if got := h.dispatchHits.Load(); got != 2 {
		t.Fatalf("本机 HTTP 派发命中 %d 次，期望 2", got)
	}
}

// TestCanonicalTargetLocalMachineAliases 锁住 CanonicalTarget 的本机别名归一口径：
// 空串、"本机"、"local" 均折成本机空 target；未指向 Listen 的远端名（如 linux-01）保留原值。
func TestCanonicalTargetLocalMachineAliases(t *testing.T) {
	var s Server
	var cfg config.Config
	cfg.Listen = "127.0.0.1:9090"
	cfg.Targets = map[string]config.Target{
		"linux-01": {Addr: "192.168.1.50:9090", Token: "tok"},
	}
	s.cfg.Store(&cfg)

	cases := []struct {
		target string
		want   string
	}{
		{target: "", want: ""},
		{target: "本机", want: ""},
		{target: "local", want: ""},
		{target: "linux-01", want: "linux-01"},
	}
	for _, tc := range cases {
		if got := s.CanonicalTarget(tc.target); got != tc.want {
			t.Errorf("CanonicalTarget(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

