package agentd

// b452_route_test.go —— B452 S3 跨机路由、S7 机器亲和、S4 只读名额闭环在控制门面的竖切（V3、V6、V8、V4 门面条）。
//
// 一律两台真实 agentd（b452_pair_test.go 夹具）+ 真实 HTTP；假执行器只装在 B。
import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	handoffclient "github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const b452Squad = "t452-run"

// b452Capture 是透传代理：记录经过它的请求（方法、路径、头、体）后原样转给 next。
type b452Capture struct {
	mu   sync.Mutex
	reqs []b452Captured
	next string // 下游基址；空 = 不转发，回 status
	code int
	body string
}

type b452Captured struct {
	method, path string
	header       http.Header
	body         []byte
}

func (c *b452Capture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.reqs = append(c.reqs, b452Captured{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: raw})
	c.mu.Unlock()
	if c.next == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(c.code)
		_, _ = io.WriteString(w, c.body)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, c.next+r.URL.RequestURI(), strings.NewReader(string(raw)))
	req.Header = r.Header.Clone()
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (c *b452Capture) posts(path string) []b452Captured {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []b452Captured
	for _, r := range c.reqs {
		if r.method == http.MethodPost && r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func (c *b452Capture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

// b452Post 向某台 agentd 发 POST /api/tasks（可选转发头），返回状态码与体。
func b452Post(t *testing.T, h *b452Host, body string, forwarded bool) (int, string) {
	t.Helper()
	return b448Raw(t, &b448Host{ledgerEnv: h.ledgerEnv}, "/api/tasks", body, forwarded)
}

func b452Get(t *testing.T, h *b452Host, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (p *b452Pair) body(extra string) string {
	return `{"project_id":"` + p.projectID + `","prompt":"调查一下"` + extra + `}`
}

// addLocalCarrierA 在 A 机登记载体 t452-local（A 没有 fake 执行器，选中它就会在 A 上开工失败）。
func (p *b452Pair) addLocalCarrierA(t *testing.T, maxConc int) {
	t.Helper()
	putOnlineCarrier(t, p.b.sched, scheduling.Carrier{ // 经 B 登记：B 的 targets 认得 t452-mac
		Name: b452CarrierA, Machine: b452MachineA, CLI: "fake", HomeDir: "",
		MaxConcurrency: maxConc, Status: scheduling.StatusOnline,
	})
}

func (p *b452Pair) putSquad(t *testing.T, members ...string) {
	t.Helper()
	var ms []scheduling.SquadMember
	for _, m := range members {
		ms = append(ms, scheduling.SquadMember{Carrier: m, MaxConcurrency: 1})
	}
	if err := p.a.sched.PutSquad(scheduling.Squad{Name: b452Squad, Role: scheduling.RoleExecutor, Members: ms}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
}

func decodeTask(t *testing.T, body string) proto.Task {
	t.Helper()
	var task proto.Task
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatalf("解码任务: %v (%s)", err, body)
	}
	return task
}

func (p *b452Pair) stopOnB(t *testing.T, id string) {
	t.Helper()
	code, body := b448Raw(t, &b448Host{ledgerEnv: p.b.ledgerEnv}, "/api/tasks/"+id+"/stop", `{}`, false)
	if code != http.StatusOK {
		t.Fatalf("清理 B 上任务 %s: %d %s", id, code, body)
	}
}

// V3(a)：载体直派到 B 的载体——A 转发、任务只在 B、A 即时有镜像行、不带 machine 的读也能路由、名额只在 B 占。
func TestB452ForwardCarrierToRemoteMachine(t *testing.T) {
	p := newB452Pair(t, nil)
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`"`), false)
	if code != http.StatusOK {
		t.Fatalf("A 受理跨机载体直派应 200，得 %d %s", code, body)
	}
	task := decodeTask(t, body)
	if task.Target != b452MachineB || task.Carrier != b452CarrierB {
		t.Fatalf("应答任务身份 target=%q carrier=%q", task.Target, task.Carrier)
	}
	if p.b.taskCount(t) != 1 || p.a.taskCount(t) != 0 {
		t.Fatalf("任务应只在 B：A=%d B=%d", p.a.taskCount(t), p.b.taskCount(t))
	}
	if m, ok, err := p.a.mgr.MirrorTaskTarget(task.ID); err != nil || !ok || m != b452MachineB {
		t.Fatalf("A 应即时登记镜像行指向 B：%q %v %v", m, ok, err)
	}
	if c, b := b452Get(t, p.a, "/api/tasks/"+task.ID); c != http.StatusOK || !strings.Contains(b, task.ID) {
		t.Fatalf("A 不带 machine 读该任务应经镜像路由 200，得 %d %s", c, b)
	}
	if got := p.carrierCount(t, b452CarrierB); got != 1 {
		t.Fatalf("名额只在 B 的 AdmitFrozen 占一次，得 %d", got)
	}
	if recs := p.a.logs.find("转发"); len(recs) == 0 {
		t.Fatal("A 应有一条转发 Info 日志")
	}
	p.stopOnB(t, task.ID)
	if got := p.carrierCount(t, b452CarrierB); got != 0 {
		t.Fatalf("stop 后名额归零，得 %d", got)
	}
}

// V3(b)：小队 [X@A(满), Y@B]：A 选 Y 并转发。基线上吃 400（旧缺陷），本条是回归锁。
func TestB452ForwardSquadPicksRemoteMember(t *testing.T) {
	p := newB452Pair(t, nil)
	p.addLocalCarrierA(t, 1)
	p.putSquad(t, b452CarrierA, b452CarrierB)
	if _, err := p.a.sched.AdmitCarrier(b452CarrierA); err != nil {
		t.Fatalf("占满 X@A: %v", err)
	}
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452Squad+`"`), false)
	if code != http.StatusOK {
		t.Fatalf("小队选到他机成员应转发 200，得 %d %s", code, body)
	}
	task := decodeTask(t, body)
	if task.Carrier != b452CarrierB || task.Squad != b452Squad || task.Target != b452MachineB {
		t.Fatalf("任务身份 = %+v", task)
	}
	p.stopOnB(t, task.ID)
}

// V3(c)：B 够不着（起后即关的本地地址）→ 502 点名机器；两机零任务、名额全 0。
func TestB452ForwardUnreachableRemote502(t *testing.T) {
	p := newB452Pair(t, nil)
	p.a.setTargets(t, map[string]string{b452MachineB: deadPeerAddr(t)})
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`"`), false)
	// 不断言「若请求已送达」后缀：连接被拒时它本不该出现（roadmap B448 残余，协调者 2026-10-21）
	if code != http.StatusBadGateway || !strings.Contains(body, b452MachineB) {
		t.Fatalf("够不着应 502 点名 %s，得 %d %s", b452MachineB, code, body)
	}
	if p.a.taskCount(t) != 0 || p.b.taskCount(t) != 0 || p.carrierCount(t, b452CarrierB) != 0 {
		t.Fatalf("零任务零名额：A=%d B=%d count=%d", p.a.taskCount(t), p.b.taskCount(t), p.carrierCount(t, b452CarrierB))
	}
}

// V3(d)：防环——B 收到带转发头、无 carrier、落点判在他机的请求 → 拒绝且不出站。
func TestB452ForwardedOrdinaryRequestNeverFansOut(t *testing.T) {
	p := newB452Pair(t, nil)
	p.addLocalCarrierA(t, 1)
	counter := &b452Capture{code: http.StatusOK, body: `{}`}
	cs := testhttp.NewServer(t, counter)
	p.b.setTargets(t, map[string]string{b452MachineA: strings.TrimPrefix(cs.URL, "http://")})
	code, body := b452Post(t, p.b, p.body(`,"receiver":"`+b452CarrierA+`"`), true)
	if code == http.StatusOK {
		t.Fatalf("已转发请求判出他机落点必须拒绝，得 200 %s", body)
	}
	if n := counter.count(); n != 0 {
		t.Fatalf("防环：B 不得再出站，计数 %d", n)
	}
	if p.b.taskCount(t) != 0 {
		t.Fatal("B 不得建任务")
	}
}

// V3(e)：转发只读请求、B 不报能力位 → 502「版本过旧」，B 零任务（桩上零派发）。
func TestB452ForwardReadonlyToOldAgentdRefused(t *testing.T) {
	p := newB452Pair(t, nil)
	stub := &b452Capture{code: http.StatusOK, body: `{"version":{"version":"old"}}`}
	ss := testhttp.NewServer(t, stub)
	p.a.setTargets(t, map[string]string{b452MachineB: strings.TrimPrefix(ss.URL, "http://")})
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`","mode":"readonly"`), false)
	if code != http.StatusBadGateway || !strings.Contains(body, "版本过旧") || !strings.Contains(body, b452MachineB) {
		t.Fatalf("老 agentd 应 502 版本过旧并点名机器，得 %d %s", code, body)
	}
	if n := len(stub.posts("/api/tasks")); n != 0 {
		t.Fatalf("老 agentd 不得收到派发，得 %d", n)
	}
}

// V3(f)：B 的错误原样回传，带「执行机 <B>：」前缀。
func TestB452ForwardRelaysRemoteErrorWithPrefix(t *testing.T) {
	p := newB452Pair(t, nil)
	code, body := b452Post(t, p.a, `{"project_id":"no-such-project","prompt":"x","receiver":"`+b452CarrierB+`"}`, false)
	if code != http.StatusBadRequest || !strings.Contains(body, "执行机 "+b452MachineB+"：") {
		t.Fatalf("B 的 400 应原样回传并带前缀，得 %d %s", code, body)
	}
	if p.carrierCount(t, b452CarrierB) != 0 {
		t.Fatal("B 拒发后名额应为 0")
	}
}

// V8：转发链路的原始字节——A 发往 B 的请求带冻结身份四键、mode、new_worktree 与转发头；
// Task 的 mode/artifacts_dir 经 B → A → client 三段解码不变。
func TestB452ForwardWireCarriesFrozenIdentityAndMode(t *testing.T) {
	p := newB452Pair(t, nil)
	tap := &b452Capture{next: p.b.ts.URL}
	ts := testhttp.NewServer(t, tap)
	p.a.setTargets(t, map[string]string{b452MachineB: strings.TrimPrefix(ts.URL, "http://")})
	front := &b452Capture{next: p.a.ts.URL}
	fs := testhttp.NewServer(t, front)

	cl := handoffclient.New(fs.URL, testToken)
	task, err := cl.Dispatch(context.Background(), handoffclient.DispatchOpts{
		ProjectID: p.projectID, Prompt: "x", Receiver: b452CarrierB, Mode: proto.TaskModeReadonly,
	})
	if err != nil {
		t.Fatalf("client.Dispatch 经 A 转发: %v", err)
	}
	// client → A：mode 有键；machine/here 零值无键
	var toA map[string]json.RawMessage
	_ = json.Unmarshal(front.posts("/api/tasks")[0].body, &toA)
	if string(toA["mode"]) != `"readonly"` {
		t.Fatalf("client→A 应带 mode 键，得 %s", toA["mode"])
	}
	for _, k := range []string{"machine", "here"} {
		if _, ok := toA[k]; ok {
			t.Fatalf("零值 %s 不得进 wire: %s", k, front.posts("/api/tasks")[0].body)
		}
	}
	// A → B：冻结身份四键 + mode + new_worktree + 转发头
	posts := tap.posts("/api/tasks")
	if len(posts) != 1 {
		t.Fatalf("A→B 应恰一次派发，得 %d", len(posts))
	}
	var toB map[string]json.RawMessage
	_ = json.Unmarshal(posts[0].body, &toB)
	for _, k := range []string{"carrier", "home_dir", "frozen_target", "mode"} {
		if _, ok := toB[k]; !ok {
			t.Fatalf("A→B 缺键 %s: %s", k, posts[0].body)
		}
	}
	if string(toB["new_worktree"]) != "true" || string(toB["frozen_target"]) != `"`+b452MachineB+`"` {
		t.Fatalf("A→B 只读转发应 new_worktree=true、frozen_target=B: %s", posts[0].body)
	}
	if posts[0].header.Get(forwardedHeader) != "1" {
		t.Fatal("A→B 应带转发头")
	}
	for _, k := range []string{"machine", "here"} {
		if _, ok := toB[k]; ok {
			t.Fatalf("A→B 不得再带亲和键 %s", k)
		}
	}
	wantDir := orchestration.TaskArtifactsDir(p.b.srv.conf().DataDir, task.ID)
	if task.Mode != proto.TaskModeReadonly || task.ArtifactsDir != wantDir || task.Target != b452MachineB {
		t.Fatalf("三段解码后 mode=%q artifacts_dir=%q target=%q，want readonly/%s/%s",
			task.Mode, task.ArtifactsDir, task.Target, wantDir, b452MachineB)
	}
	stored, _ := p.b.st.GetTask(task.ID)
	if stored.Mode != proto.TaskModeReadonly {
		t.Fatalf("B 落库 mode=%q", stored.Mode)
	}
	p.stopOnB(t, task.ID)
}

// V8：旧任务（空 mode）序列化无 mode 键。
func TestB452TaskJSONOmitsEmptyMode(t *testing.T) {
	raw, _ := json.Marshal(proto.Task{ID: "x"})
	if strings.Contains(string(raw), `"mode"`) || strings.Contains(string(raw), `"artifacts_dir"`) {
		t.Fatalf("空 mode/artifacts_dir 应省略: %s", raw)
	}
	var st proto.StatusResp
	_ = json.Unmarshal([]byte(`{"version":{"version":"v"}}`), &st)
	if st.SkilledDispatchSupported != nil {
		t.Fatal("老结构无键应解出 nil")
	}
}

// V6：亲和。
func TestB452AffinityRefusals(t *testing.T) {
	p := newB452Pair(t, nil)
	p.addLocalCarrierA(t, 1)
	// (a) here + 载体在他机 → 422，点名本机与载体所在机
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`","here":true`), false)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `"kind":"affinity_unmet"`) ||
		!strings.Contains(body, `"error":"机器亲和不满足：`) || !strings.Contains(body, b452MachineA) || !strings.Contains(body, b452MachineB) {
		t.Fatalf("(a) 应 422 affinity_unmet 点名两机，得 %d %s", code, body)
	}
	// (b) here + 小队本机无在线成员 → 422，列成员@机器
	p.putSquad(t, b452CarrierB)
	code, body = b452Post(t, p.a, p.body(`,"receiver":"`+b452Squad+`","here":true`), false)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, b452CarrierB+"@"+b452MachineB) {
		t.Fatalf("(b) 应 422 列出成员@机器，得 %d %s", code, body)
	}
	// (d) machine 与 here 同给 → 400
	code, body = b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`","here":true,"machine":"`+b452MachineB+`"`), false)
	if code != http.StatusBadRequest {
		t.Fatalf("(d) machine×here 应 400，得 %d %s", code, body)
	}
	if p.a.taskCount(t) != 0 || p.b.taskCount(t) != 0 {
		t.Fatalf("拒绝零任务：A=%d B=%d", p.a.taskCount(t), p.b.taskCount(t))
	}
	if recs := p.a.logs.find("亲和"); len(recs) == 0 {
		t.Fatal("亲和拒绝应有 Info 及以上日志")
	}
}

// V6(c)：machine=B + 小队在 B 有成员 → 转发到 B；(e) 亲和满足但满员 → 409 不是 422。
func TestB452AffinityMachineForwardsAndFullIs409(t *testing.T) {
	p := newB452Pair(t, nil)
	p.addLocalCarrierA(t, 1)
	p.putSquad(t, b452CarrierA, b452CarrierB)
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452Squad+`","machine":"`+b452MachineB+`"`), false)
	if code != http.StatusOK {
		t.Fatalf("(c) machine=B 应转发成功，得 %d %s", code, body)
	}
	task := decodeTask(t, body)
	if task.Carrier != b452CarrierB {
		t.Fatalf("(c) 应落在 B 的成员，得 %s", task.Carrier)
	}
	// B 的成员已满（容量 1）：再派 machine=B → 409
	code, body = b452Post(t, p.a, p.body(`,"receiver":"`+b452Squad+`","machine":"`+b452MachineB+`"`), false)
	if code != http.StatusConflict {
		t.Fatalf("(e) 亲和满足但满员应 409，得 %d %s", code, body)
	}
	p.stopOnB(t, task.ID)
}

// V4 门面条：只读任务经转发到 B 跑完 → 自动归档 → 共享名额归零（终态回调接线在 SetManager）。
func TestB452ReadonlyForwardedCompletesAndReleasesSlot(t *testing.T) {
	p := newB452Pair(t, []fake.Step{{Finish: executor.Result{OK: true, Summary: "s", FinalText: "结论"}}})
	code, body := b452Post(t, p.a, p.body(`,"receiver":"`+b452CarrierB+`","mode":"readonly"`), false)
	if code != http.StatusOK {
		t.Fatalf("只读跨机派发应 200，得 %d %s", code, body)
	}
	task := decodeTask(t, body)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := p.b.st.GetTask(task.ID)
		if cur != nil && cur.State == proto.TaskStateCompleted && p.carrierCount(t, b452CarrierB) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cur, _ := p.b.st.GetTask(task.ID)
	t.Fatalf("只读任务应 completed 且名额归零：state=%v count=%d", cur.State, p.carrierCount(t, b452CarrierB))
}

// F14：/ws/events?task=&machine=B 经 A 转发，能从 0 回放到 completed（任务不在 A、也不在 A 的镜像订阅里）。
func TestB452EventsWSFollowsMachine(t *testing.T) {
	p := newB452Pair(t, []fake.Step{{Finish: executor.Result{OK: true, Summary: "s", FinalText: "结论"}}})
	home := ""
	cl, _ := p.a.srv.clientForTarget(b452MachineB)
	task, err := cl.Dispatch(context.Background(), handoffclient.DispatchOpts{
		ProjectID: p.projectID, Prompt: "x", Target: b452MachineB, Carrier: b452CarrierB,
		HomeDir: &home, FrozenTarget: b452MachineB, Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(p.a.ts)+"/ws/events?task="+task.ID+"&from_seq=0&machine="+b452MachineB,
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}}})
	if err != nil {
		t.Fatalf("经 A 按机拨事件流失败: %v", err)
	}
	defer conn.CloseNow()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("事件流在 completed 之前断开: %v", err)
		}
		var ev proto.Event
		if json.Unmarshal(data, &ev) == nil && ev.Type == proto.EventTypeCompleted {
			break
		}
	}
	p.stopOnB(t, task.ID)
}
