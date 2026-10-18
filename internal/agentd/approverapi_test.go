// approverapi_test.go —— B437「代他机裁决」端点与竖切测试。
//
// 覆盖：端点 401/400/no_executor/invalid_output/approve、端点零写事件；
// S2 竖切——发起方 Approver 经真实 HTTP 调另一个 agentd 组装点的真实端点，
// 断言审计带 machine 与对端回报的 model、请求体无 prompt、对端零事件。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// fakeRingProvider 是可注入的 Provider + OneShot 替身：回显 prompt 里的 nonce。
type fakeRingProvider struct {
	name  string
	reply string
	err   error

	mu          sync.Mutex
	last        executor.OneShotReq
	hadDeadline bool
}

func (p *fakeRingProvider) Name() string { return p.name }
func (p *fakeRingProvider) Report() executor.CapabilityReport {
	return executor.CapabilityReport{Harness: p.name}
}
func (p *fakeRingProvider) Invoke(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
	_, hadDeadline := ctx.Deadline()
	p.mu.Lock()
	p.last = req
	p.hadDeadline = hadDeadline
	p.mu.Unlock()
	if p.err != nil {
		return executor.OneShotReply{}, p.err
	}
	out := strings.ReplaceAll(p.reply, "NONCE", extractPromptNonce(req.Prompt))
	return executor.OneShotReply{Text: out, Status: executor.OneShotOK}, nil
}

func (p *fakeRingProvider) lastPrompt() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last.Prompt
}

// lastHadDeadline 报告最近一次 OneShot 调用的 ctx 是否带截止（端点封顶探针）。
func (p *fakeRingProvider) lastHadDeadline() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hadDeadline
}

// extractPromptNonce 从裁决 prompt 里取 nonce 值（模板里形如 nonce=<hex>）。
func extractPromptNonce(prompt string) string {
	idx := strings.Index(prompt, "nonce=")
	if idx < 0 {
		return ""
	}
	rest := prompt[idx+len("nonce="):]
	end := 0
	for end < len(rest) && isHex(rest[end]) {
		end++
	}
	return rest[:end]
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}

func newApproverEndpointEnv(t *testing.T, providers ...executor.Provider) *testAgentdEnv {
	t.Helper()
	env := newTestAgentdEnv(t)
	env.srv.SetProviders(executor.NewRegistry(providers...))
	env.srv.SetApproverEnv(nil)
	return env
}

func postApproverDecide(t *testing.T, env *testAgentdEnv, body string, withToken bool) (int, proto.ApproverDecideResp) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.ts.URL+"/api/approver/decide", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if withToken {
		req.Header.Set("Authorization", "Bearer "+env.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out proto.ApproverDecideResp
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestApproverDecideEndpointAuthAndValidation(t *testing.T) {
	p := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","nonce":"NONCE"}`}
	env := newApproverEndpointEnv(t, p)
	// 无 token → 401
	if code, _ := postApproverDecide(t, env, `{"permission":"x","executor":"codex"}`, false); code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，得到 %d", code)
	}
	// permission 空 → 400
	if code, _ := postApproverDecide(t, env, `{"executor":"codex"}`, true); code != http.StatusBadRequest {
		t.Fatalf("permission 空应 400，得到 %d", code)
	}
	// executor 空 → 400
	if code, _ := postApproverDecide(t, env, `{"permission":"x"}`, true); code != http.StatusBadRequest {
		t.Fatalf("executor 空应 400，得到 %d", code)
	}
}

func TestApproverDecideEndpointNoExecutor(t *testing.T) {
	// 未注册执行器 → error/no_executor，200
	env := newApproverEndpointEnv(t)
	code, out := postApproverDecide(t, env, `{"permission":"x","executor":"ghost"}`, true)
	if code != http.StatusOK || out.Decision != "error" || out.ErrorKind != "no_executor" {
		t.Fatalf("未注册执行器应 200 error/no_executor: %d %+v", code, out)
	}
	// 注册了但不实现 OneShot → 同样 no_executor
	env2 := newApproverEndpointEnv(t, executor.StaticProvider{HarnessName: "claude", Rep: executor.CapabilityReport{Harness: "claude"}})
	code, out = postApproverDecide(t, env2, `{"permission":"x","executor":"claude"}`, true)
	if code != http.StatusOK || out.Decision != "error" || out.ErrorKind != "no_executor" {
		t.Fatalf("非 OneShot 执行器应 error/no_executor: %d %+v", code, out)
	}
}

func TestApproverDecideEndpointInvalidOutputAndApprove(t *testing.T) {
	// 不可解析输出 → invalid_output
	bad := &fakeRingProvider{name: "codex", reply: "我觉得可以批"}
	env := newApproverEndpointEnv(t, bad)
	code, out := postApproverDecide(t, env, `{"permission":"Bash: ls","executor":"codex","task_summary":"摘要"}`, true)
	if code != http.StatusOK || out.Decision != "error" || out.ErrorKind != "invalid_output" {
		t.Fatalf("不可解析应 error/invalid_output: %d %+v", code, out)
	}
	// 合法裁决 → approve，模型默认占位
	good := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","reason":"安全","nonce":"NONCE"}`}
	env2 := newApproverEndpointEnv(t, good)
	code, out = postApproverDecide(t, env2, `{"permission":"Bash: ls","executor":"codex","task_summary":"摘要"}`, true)
	if code != http.StatusOK || out.Decision != "approve" || out.Model != orchestration.ApproverModelDefaultLabel {
		t.Fatalf("应 approve + 默认模型占位: %d %+v", code, out)
	}
	if !strings.Contains(good.lastPrompt(), "Bash: ls") || !strings.Contains(good.lastPrompt(), "摘要") {
		t.Fatalf("对端应自己拼提示词（含 permission/summary）: %q", good.lastPrompt())
	}
	// 端点全程零 AppendEvent：store 里没有任务
	tasks, err := env2.st.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("端点不应产生任何任务/事件: %+v", tasks)
	}
}

// recordHandler 记录每个请求的原始体，再转交下游 handler。
type recordHandler struct {
	next http.Handler
	mu   sync.Mutex
	body []string
}

func (r *recordHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(b))
	r.mu.Lock()
	r.body = append(r.body, string(b))
	r.mu.Unlock()
	r.next.ServeHTTP(w, req)
}

func (r *recordHandler) lastBody() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.body) == 0 {
		return ""
	}
	return r.body[len(r.body)-1]
}

// newBareServer 起一个只装配了端点所需依赖的 agentd 测试服（无 PTY/账本）。
func newBareServer(t *testing.T, providers ...executor.Provider) (*Server, *store.Store, *httptest.Server, *recordHandler) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Token: testToken, DataDir: dir, Listen: "127.0.0.1:0"}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetConfigPath(filepath.Join(dir, "config.yaml"))
	srv.SetProviders(executor.NewRegistry(providers...))
	srv.SetApproverEnv(nil)
	rec := &recordHandler{next: srv.Handler()}
	ts := testhttp.NewServer(t, rec)
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	return srv, st, ts, rec
}

// TestS2VerticalSliceRemoteRingDecide 是承重竖切：发起方 Approver 的共享链首环
// 指向另一台 agentd（他机），经真实 HTTP 调它的 /api/approver/decide 端点裁决。
func TestS2VerticalSliceRemoteRingDecide(t *testing.T) {
	// 对端 B：本机 codex 由 fake OneShot 裁决
	pB := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","reason":"B 放行","nonce":"NONCE"}`}
	_, stB, tsB, recB := newBareServer(t, pB)

	// 发起方 A：设备表/配置里登记 linux-01 → B
	dirA := t.TempDir()
	cfgA := &config.Config{
		Token:   testToken,
		DataDir: dirA,
		Listen:  "127.0.0.1:0",
		Targets: map[string]config.Target{"linux-01": {Addr: strings.TrimPrefix(tsB.URL, "http://"), Token: testToken}},
	}
	stA, err := store.Open(filepath.Join(dirA, "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stA.Close() })
	srvA := NewServer(cfgA, stA, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srvA.SetConfigPath(filepath.Join(dirA, "config.yaml"))

	ap, err := orchestration.NewApproverForShared(config.ApproverConfig{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ap.SetSelfMachine(srvA.IsSelfMachine)
	ap.SetRemoteDecider(srvA.ApproverRemoteDecider())
	ap.SetChainReader(&staticChainReader{chain: orchestration.ApproverChain{
		Rings: []orchestration.ApproverRing{{Machine: "linux-01", Executor: "codex"}},
	}})

	d := ap.Decide(context.Background(), "Bash: rm -rf build", "清理构建产物")
	if !d.Approve || d.Err != nil {
		t.Fatalf("首环他机应 approve: %+v", d)
	}
	if len(d.Attempts) != 1 {
		t.Fatalf("应一条 attempt: %+v", d.Attempts)
	}
	at := d.Attempts[0]
	if at.Machine != "linux-01" || at.Executor != "codex" {
		t.Fatalf("审计应带 machine=linux-01 executor=codex: %+v", at)
	}
	if at.Model != orchestration.ApproverModelDefaultLabel {
		t.Fatalf("模型应取对端回报（默认占位）: %+v", at)
	}
	if at.Reason != "B 放行" {
		t.Fatalf("理由应来自对端: %+v", at)
	}

	// (b) 对端收到的请求体没有 prompt 字段，permission/task_summary 原样
	body := recB.lastBody()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("对端请求体应为 JSON: %q %v", body, err)
	}
	if _, ok := raw["prompt"]; ok {
		t.Fatalf("请求体不得含 prompt 字段: %s", body)
	}
	if got := string(raw["permission"]); got != `"Bash: rm -rf build"` {
		t.Fatalf("permission 应原样: %s", got)
	}
	if got := string(raw["task_summary"]); got != `"清理构建产物"` {
		t.Fatalf("task_summary 应原样: %s", got)
	}

	// (c) 对端未写任何事件（无任务）
	tasks, err := stB.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("对端不应写事件/任务: %+v", tasks)
	}
}

// staticChainReader 固定返回一条链。
type staticChainReader struct{ chain orchestration.ApproverChain }

func (r *staticChainReader) Current(context.Context) (orchestration.ApproverChain, error) {
	return r.chain, nil
}

// TestApproverDecideEndpointDoesNotCapChainTimeout 钉住本次补充 (2)：端点不得对
// OneShot 施加自身封顶，只依赖 r.Context()（发起方链级时限经 HTTP 取消传播）。
// 若端点硬编码 60s 封顶，OneShot 会看到一个人造截止——断言「无截止」即可证伪。
func TestApproverDecideEndpointDoesNotCapChainTimeout(t *testing.T) {
	p := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","nonce":"NONCE"}`}
	env := newApproverEndpointEnv(t, p)
	code, out := postApproverDecide(t, env, `{"permission":"x","executor":"codex"}`, true)
	if code != http.StatusOK || out.Decision != "approve" {
		t.Fatalf("应 approve: %d %+v", code, out)
	}
	if p.lastHadDeadline() {
		t.Fatal("端点不得对 OneShot 施加自身封顶（应只依赖 r.Context()，不掐断用户配置的链级时限）")
	}
}

// ---- DecideRemote 五分类（本次补充 (4)：表驱动 + 拨号超时回归钉）----

// dialTimeoutNetError 模拟「拨号超时形态的 net.Error」：满足 net.Error 且
// errors.Is(err, context.DeadlineExceeded) 为真（真实拨号超时在部分路径上如此），
// 但整体截止并未到点——按 §4.6 必须判「不可达」而非「超时」。
type dialTimeoutNetError struct{}

func (dialTimeoutNetError) Error() string   { return "dial tcp 100.64.0.1:443: i/o timeout" }
func (dialTimeoutNetError) Timeout() bool   { return true }
func (dialTimeoutNetError) Temporary() bool { return true }
func (dialTimeoutNetError) Is(target error) bool {
	return target == context.DeadlineExceeded
}

// fakeDecideCaller 是 approverDecideCaller 的故障替身。
type fakeDecideCaller struct {
	err  error
	resp *proto.ApproverDecideResp
}

func (f *fakeDecideCaller) ApproverDecide(context.Context, proto.ApproverDecideReq) (*proto.ApproverDecideResp, error) {
	return f.resp, f.err
}

// newServerWithTarget 起一个把 target 名指向 addr 的 agentd 组装点。
func newServerWithTarget(t *testing.T, name, addr string) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Token: testToken, DataDir: dir, Listen: "127.0.0.1:0",
		Targets: map[string]config.Target{name: {Addr: strings.TrimPrefix(addr, "http://"), Token: testToken}}}
	st, err := store.Open(filepath.Join(dir, "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// runRingFailover 跑一条 [坏环(他机 badMachine), 本机好环] 的共享链，返回首环
// attempt 与整链是否放行——验证「坏环失败后转下一环」。
func runRingFailover(t *testing.T, badMachine string, chainTO time.Duration, rd orchestration.RemoteDecider) (orchestration.ApproverAttempt, bool) {
	t.Helper()
	local := &fakeRingProvider{name: "codex", reply: `{"decision":"approve","reason":"本机放行","nonce":"NONCE"}`}
	ap, err := orchestration.NewApproverForShared(config.ApproverConfig{}, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ap.SetSelfMachine(func(name string) bool { return name == "self" })
	ap.SetLocalShot(func(string) (executor.OneShot, error) { return local, nil })
	ap.SetRemoteDecider(rd)
	ap.SetChainReader(&staticChainReader{chain: orchestration.ApproverChain{
		Timeout: chainTO,
		Rings: []orchestration.ApproverRing{
			{Machine: badMachine, Executor: "codex"},
			{Machine: "self", Executor: "codex"},
		},
	}})
	d := ap.Decide(context.Background(), "Bash: ls", "摘要")
	if len(d.Attempts) < 2 {
		t.Fatalf("应至少有两条 attempt（坏环 + 好环）: %+v", d.Attempts)
	}
	return d.Attempts[0], d.Approve
}

func TestDecideRemoteFailureClassification(t *testing.T) {
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 真实端点对端：无 providers → no_executor
	_, _, tsNoExec, _ := newBareServer(t)
	// 真实端点对端：垃圾输出 → invalid_output
	_, _, tsInvalid, _ := newBareServer(t, &fakeRingProvider{name: "codex", reply: "我觉得可以批"})
	// 挂起对端：连上后一直不回，超过链截止 → 超时
	hangRelease := make(chan struct{})
	tsHang := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-hangRelease:
		}
	}))
	t.Cleanup(func() { close(hangRelease) })
	// 404 对端：旧版无此端点 → 不支持
	tsNotFound := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	// 未知 error_kind 对端 → internal
	tsWeird := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"decision":"error","reason":"对端怪错","model":"默认","elapsed_ms":1,"error_kind":"weird"}`)
	}))

	cases := []struct {
		name       string
		machine    string
		chainTO    time.Duration
		decider    func(t *testing.T) orchestration.RemoteDecider
		wantKind   string
		wantReason string
	}{
		{
			name: "设备表无此名→不可达", machine: "ghost", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				srv, _, _, _ := newBareServer(t)
				return srv.ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailUnreachable, wantReason: "机器不可达",
		},
		{
			name: "拨号超时形态且整体截止未到→不可达", machine: "linux-01", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return &remoteApproverDecider{log: discard, clientFor: func(string) (approverDecideCaller, error) {
					return &fakeDecideCaller{err: dialTimeoutNetError{}}, nil
				}}
			},
			wantKind: orchestration.ApproverFailUnreachable, wantReason: "机器不可达",
		},
		{
			name: "404→不支持", machine: "linux-01", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return newServerWithTarget(t, "linux-01", tsNotFound.URL).ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailUnsupported, wantReason: "对端不支持",
		},
		{
			name: "no_executor→对端无此执行器", machine: "linux-01", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return newServerWithTarget(t, "linux-01", tsNoExec.URL).ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailNoExecutor, wantReason: "对端无此执行器",
		},
		{
			name: "对端挂起超过链截止→超时", machine: "linux-01", chainTO: 400 * time.Millisecond,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return newServerWithTarget(t, "linux-01", tsHang.URL).ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailTimeout, wantReason: "超时",
		},
		{
			name: "invalid_output→裁决无效", machine: "linux-01", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return newServerWithTarget(t, "linux-01", tsInvalid.URL).ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailInvalidOutput, wantReason: "裁决无效",
		},
		{
			name: "未知 error_kind→internal", machine: "linux-01", chainTO: 2 * time.Second,
			decider: func(t *testing.T) orchestration.RemoteDecider {
				return newServerWithTarget(t, "linux-01", tsWeird.URL).ApproverRemoteDecider()
			},
			wantKind: orchestration.ApproverFailInternal, wantReason: "对端怪错",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := tc.decider(t)
			// (a) 分类
			out := rd.DecideRemote(context.Background(),
				orchestration.ApproverRing{Machine: tc.machine, Executor: "codex"}, "Bash: ls", "摘要", tc.chainTO)
			if out.ErrorKind != tc.wantKind {
				t.Fatalf("应分类 %s，得到 %s（reason=%q）", tc.wantKind, out.ErrorKind, out.Reason)
			}
			// (b) 坏环失败后转下一环
			at, approved := runRingFailover(t, tc.machine, tc.chainTO, rd)
			if !approved {
				t.Fatalf("坏环失败后应转本机好环放行: %+v", at)
			}
			if at.Reason != tc.wantReason {
				t.Fatalf("首环审计理由应 %q，得到 %q", tc.wantReason, at.Reason)
			}
		})
	}
}
