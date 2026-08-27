// B229 T3 cardstep 装配链测试：startCardStep 在同步段经缝 1 解析纪律正文并探
// 目标机能力位，正文三元组随 client.DispatchOpts 上 wire；目标机不支持时同步
// 拒发、HTTP 层给可行动文案、目标机零任务请求。假目标机是 httptest 真服务，
// 断言穿真实 JSON wire 边界。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeTargetMachine 是一台 httptest 假目标机：/api/status 按给定能力位应答，
// /api/tasks 记录收到的派发 body 并计数。
type fakeTargetMachine struct {
	ts           *httptest.Server
	mu           sync.Mutex
	dispatchBody map[string]any
	dispatches   int
	capSupported *bool
}

func newFakeTargetMachine(t *testing.T, capSupported *bool) *fakeTargetMachine {
	t.Helper()
	ftm := &fakeTargetMachine{capSupported: capSupported}
	ftm.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/status":
			w.Header().Set("Content-Type", "application/json")
			if ftm.capSupported != nil && *ftm.capSupported {
				fmt.Fprint(w, `{"disciplines_supported":true}`)
			} else if ftm.capSupported != nil {
				fmt.Fprint(w, `{"disciplines_supported":false}`)
			} else {
				fmt.Fprint(w, `{}`)
			}
		case r.URL.Path == "/api/tasks" && r.Method == http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
			ftm.mu.Lock()
			ftm.dispatchBody = body
			ftm.dispatches++
			ftm.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"T-fake-01","state":"running"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ftm.ts.Close)
	return ftm
}

func (f *fakeTargetMachine) dispatchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dispatches
}

func (f *fakeTargetMachine) lastDispatch() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dispatchBody
}

// registerFakeTarget 把假目标机以直连形态写进 agentd 活配置。
func registerFakeTarget(t *testing.T, s *Server, name string, ftm *fakeTargetMachine) {
	t.Helper()
	addr := strings.TrimPrefix(ftm.ts.URL, "http://")
	if err := s.swapConf(func(c *config.Config) error {
		c.Targets[name] = config.Target{Addr: addr, Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("登记假目标机: %v", err)
	}
}

// seedDisciplineOnLedger 给账本种一份 v1 纪律块，返回版本号。
func seedDisciplineOnLedger(t *testing.T, env *ledgerEnv, name, body string) int {
	t.Helper()
	ver, err := env.ledger.PutDiscipline(name, body)
	if err != nil {
		t.Fatalf("种子账本纪律块 %s: %v", name, err)
	}
	return ver
}

// TestCardStepDeliversResolvedDiscipline 装配链全链路：HTTP 受理 → startCardStep
// 同步解析（账本 lookup + 能力位探活）→ ViaTemplate 透传 → stepTransport 发出的
// POST /api/tasks body 含组装正文与版本号。断言落在假目标机收到的原始 JSON 上。
func TestCardStepDeliversResolvedDiscipline(t *testing.T) {
	env := newLedgerEnv(t)
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "实现角色正文B229MARKER")
	yes := true
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "fake-01", ftm)

	code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step",
		`{"step":"进行中","target":"fake-01","actor":"cli:t@h#1"}`)
	if code != http.StatusAccepted {
		t.Fatalf("受理应 202，实得 %d（%s）", code, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ftm.dispatchCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	sent := ftm.lastDispatch()
	if sent == nil {
		t.Fatalf("环节派发未到达假目标机；最近 HTTP 应答：%s", body)
	}
	text, _ := sent["discipline_text"].(string)
	if !strings.Contains(text, "平台不变量") || !strings.Contains(text, "实现角色正文B229MARKER") {
		t.Fatalf("wire 上的 discipline_text 应是平台层+角色层组装产物，实得前 100 字节: %q",
			truncateRunes(text, 100))
	}
	if sent["discipline_version"] != float64(1) {
		t.Fatalf("wire discipline_version = %v, want 1", sent["discipline_version"])
	}
	if sent["discipline"] != discipline.NameImplement {
		t.Fatalf("审计名字 discipline = %v, want %q", sent["discipline"], discipline.NameImplement)
	}
}

// TestStartCardStepRejectsUnsupportedTarget 三态能力位 nil/false 都必须拒发：
// 错误可经 errors.Is(err, ErrUnsupportedTarget) 辨认，文案含升级指引（HTTP 层
// 原文透出），且目标机一个任务请求都不收到。
func TestStartCardStepRejectsUnsupportedTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  *bool
	}{
		{"能力位缺席(nil)", nil},
		{"能力位false", ptrFalse()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newLedgerEnv(t)
			seedCardWithProject(t, env.srv, "handoff")
			card, err := env.ledger.GetCard("B1")
			if err != nil {
				t.Fatal(err)
			}
			seedDisciplineOnLedger(t, env, discipline.NameImplement, "不该被下发的正文")
			ftm := newFakeTargetMachine(t, tc.cap)
			registerFakeTarget(t, env.srv, "fake-01", ftm)

			err = env.srv.startCardStep(card.ID, proto.CardStepReq{
				Step: "进行中", Target: "fake-01", Actor: "cli:t@h#1",
			})
			if err == nil {
				t.Fatal("目标机不支持时必须拒发")
			}
			if !errors.Is(err, discipline.ErrUnsupportedTarget) {
				t.Fatalf("错误应可辨认为 ErrUnsupportedTarget，实得: %v", err)
			}
			if !strings.Contains(err.Error(), "升级") {
				t.Fatalf("拒发文案应含可行动的升级指引: %v", err)
			}
			if n := ftm.dispatchCount(); n != 0 {
				t.Fatalf("拒发后目标机不应收到任何任务请求，实际 %d 次", n)
			}

			// HTTP 层映射：同一拒发经 step 端点应答非 2xx 且原文带升级指引。
			code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+card.ID+"/step",
				`{"step":"进行中","target":"fake-01","actor":"cli:t@h#2"}`)
			if code != http.StatusBadRequest || !strings.Contains(body, "升级") {
				t.Fatalf("HTTP 层应把拒发映射为可行动文案，实得 %d（%s）", code, body)
			}
			if n := ftm.dispatchCount(); n != 0 {
				t.Fatalf("HTTP 路径同样不得发出任务，实际 %d 次", n)
			}
		})
	}
}

type probeErrorTargetMachine struct {
	ts         *httptest.Server
	mu         sync.Mutex
	dispatches int
}

func newProbeErrorTargetMachine(t *testing.T) *probeErrorTargetMachine {
	t.Helper()
	target := &probeErrorTargetMachine{}
	target.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/status" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not-json"))
			return
		}
		if r.URL.Path == "/api/tasks" && r.Method == http.MethodPost {
			target.mu.Lock()
			target.dispatches++
			target.mu.Unlock()
			http.Error(w, "probe failure test must not dispatch", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(target.ts.Close)
	return target
}

func registerProbeErrorTarget(t *testing.T, s *Server, target *probeErrorTargetMachine) {
	t.Helper()
	addr := strings.TrimPrefix(target.ts.URL, "http://")
	if err := s.swapConf(func(c *config.Config) error {
		c.Targets["probe-error"] = config.Target{Addr: addr, Token: testToken}
		return nil
	}); err != nil {
		t.Fatalf("登记探活错误目标: %v", err)
	}
}

func (target *probeErrorTargetMachine) dispatchCount() int {
	target.mu.Lock()
	defer target.mu.Unlock()
	return target.dispatches
}

func TestCardStepProbeFailureDoesNotClaimUnsupported(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	env.srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	seedCardWithProject(t, env.srv, "handoff")
	card, err := env.ledger.GetCard("B1")
	if err != nil {
		t.Fatal(err)
	}
	seedDisciplineOnLedger(t, env, discipline.NameImplement, "探活失败不应下发")
	target := newProbeErrorTargetMachine(t)
	registerProbeErrorTarget(t, env.srv, target)

	err = env.srv.startCardStep(card.ID, proto.CardStepReq{
		Step: "进行中", Target: "probe-error", Actor: "test",
	})
	if err == nil {
		t.Fatal("Status 失败时环节派发必须返回错误")
	}
	if !strings.Contains(err.Error(), "探活失败") ||
		!strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("错误必须含探活语义和 cause：%v", err)
	}
	if strings.Contains(err.Error(), "升级到同批版本") {
		t.Fatalf("探活失败不得归因成版本升级：%v", err)
	}
	if got := target.dispatchCount(); got != 0 {
		t.Fatalf("探活失败不得发送任务，实际 %d 次", got)
	}
}

func ptrFalse() *bool { b := false; return &b }
