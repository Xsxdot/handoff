// b402_e2e_test.go —— B402 跨进程端到端回路：failure_class 从 fake adapter 的
// Result 出发，经 manager.handleResult → FailedPayload → JSON → 真 HTTP →
// GET /api/tasks/{id} 的 recent_events，一条链路穿真序列化边界不丢；同时锁
// POST /continue 的 waiting_review 状态门与「任何早退不归档」。
//
// 为什么只在外部测试包：真 agentd.Server 只接受 plain orchestration 的
// OrchestrationClient（B233.26，见 gateway_http_test.go:4-8）；包内测试变体的
// *Manager 挂不上真 Server。
//
// 边界：只用两包的导出面与既有测试缝（newHTTPEnv 同款装配、doReq、initRepo、
// gitAt、fake 脚本），不改任何生产代码。
package orchestration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// b402Detail 是 GET /api/tasks/{id} 的响应体子集（只取本卡要断言的两块）。
type b402Detail struct {
	Task         proto.TaskView `json:"task"`
	RecentEvents []proto.Event  `json:"recent_events"`
}

// newB402Env 与 newHTTPEnv 同款装配（真 store + 真 Hub + 真 Manager + 真
// agentd.Server），唯一差别是 fake adapter 带初始脚本——本卡要驱动
// zero_text → continue → 第二终态的分步事件流。
//
// 返回：httpEnv（复用 doReq/initRepo/gitAt/既有路由）与脚本 adapter（断言
// Sends/Add）。
func newB402Env(t *testing.T, script []fake.Step) (*httpEnv, *fake.Fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	dataDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "test", DataDir: dataDir,
		Executor: config.ExecutorConfig{Default: "fake"}}
	g, err := permgate.New(nil, logger)
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	fk := fake.New(script)
	m := orchestration.NewManager(st, orchestration.NewHub(),
		map[string]executor.Adapter{"fake": fk}, cfg, nil, nil, g, logger)
	m.SetWorkspace(workspace.NewCapability())
	srv := agentd.NewServer(&config.Config{Token: "test"}, st, logger)
	srv.SetManager(m)
	return &httpEnv{srv: srv, mgr: m, st: st, dataDir: dataDir}, fk
}

// b402RegisterProject 照抄 manager_test.go 的 registerTestProject：B62 之后
// 「必须先登记才能派发」是服务端不变式，测试不开旁路；origin 由仓库路径派生，
// 每个用例的临时仓库各不相同，project_id 天然不撞。
func b402RegisterProject(t *testing.T, m *orchestration.Manager, repo string) string {
	t.Helper()
	origin := "git@handoff.test:" + strings.ReplaceAll(strings.TrimPrefix(repo, "/"), "/", "-") + ".git"
	gitAt(t, repo, "remote", "add", "origin", origin)
	loc, err := m.RegisterProject(context.Background(), workspace.RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("RegisterProject(%s): %v", repo, err)
	}
	return loc.ProjectID
}

// b402Get 拉一次任务详情（真 HTTP），失败即 Fatal。
func b402Get(t *testing.T, env *httpEnv, id string) b402Detail {
	t.Helper()
	rec := doReq(t, env.srv, http.MethodGet, "/api/tasks/"+id, "", "test")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/tasks/%s: %d %s", id, rec.Code, rec.Body.String())
	}
	var d b402Detail
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("解任务详情: %v", err)
	}
	return d
}

// b402Events 取某类型的事件切片。
func b402Events(d b402Detail, typ string) []proto.Event {
	var out []proto.Event
	for _, ev := range d.RecentEvents {
		if string(ev.Type) == typ {
			out = append(out, ev)
		}
	}
	return out
}

// b402Wait 轮询任务详情直到 pred 成立；超时即 Fatal 并附最后状态与事件序列
// （不 sleep 猜时间，判据只看服务端事实）。
func b402Wait(t *testing.T, env *httpEnv, id, what string, pred func(b402Detail) bool) b402Detail {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last b402Detail
	for time.Now().Before(deadline) {
		last = b402Get(t, env, id)
		if pred(last) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	types := make([]string, 0, len(last.RecentEvents))
	for _, ev := range last.RecentEvents {
		types = append(types, string(ev.Type))
	}
	t.Fatalf("等待 %s 超时：state=%s events=%v", what, last.Task.State, types)
	return b402Detail{}
}

// b402ZeroTextStep 是本卡脚本首步：明确零文本失败（FailReason 与实现无关，
// 证明分支只依赖结构化字段）。
func b402ZeroTextStep() fake.Step {
	return fake.Step{Finish: executor.Result{
		OK: false, FailureClass: proto.FailureClassZeroText, FailReason: "供应商文案随便写",
	}}
}

// TestB402E2EZeroTextContinueRoundtrip 主回路 zero_text → continue → completed，
// 外加状态门、续接计数与归档出口；每条断言穿真 HTTP/JSON 边界。
func TestB402E2EZeroTextContinueRoundtrip(t *testing.T) {
	env, fk := newB402Env(t, []fake.Step{b402ZeroTextStep()})
	repo := initRepo(t)
	pid := b402RegisterProject(t, env.mgr, repo)
	task, err := env.mgr.Dispatch(context.Background(), orchestration.DispatchReq{
		ProjectID: pid, Prompt: "B402 主回路", Executor: "fake",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// 断言 1：首个终态 turn_failed 的 wire payload 精确含 failure_class=zero_text
	//（Result → FailedPayload → JSON → HTTP 一条链路，不是包内 Marshal）
	d := b402Wait(t, env, task.ID, "首个 turn_failed", func(d b402Detail) bool {
		return len(b402Events(d, "turn_failed")) > 0
	})
	fails := b402Events(d, "turn_failed")
	raw := string(fails[len(fails)-1].Payload)
	if !strings.Contains(raw, `"failure_class":"zero_text"`) {
		t.Fatalf("turn_failed wire payload 缺 failure_class=zero_text: %s", raw)
	}
	var p orchestration.FailedPayload
	if err := json.Unmarshal(fails[len(fails)-1].Payload, &p); err != nil {
		t.Fatalf("解 FailedPayload: %v", err)
	}
	if p.FailureClass != proto.FailureClassZeroText {
		t.Fatalf("解出 FailureClass=%q, want zero_text", p.FailureClass)
	}
	// 夹具脱敏：FailReason 是与实现无关文案，分支只认结构化字段
	if p.FailReason != "供应商文案随便写" {
		t.Fatalf("FailReason 应原样透传，got %q", p.FailReason)
	}
	if d.Task.State != proto.TaskStateWaitingReview {
		t.Fatalf("首个失败后应 waiting_review，实得 %s", d.Task.State)
	}

	// 断言 2 + 8：先注入第二步，再打 /continue（fake 续接门禁由 Send 放行，
	// Add 不唤醒——顺序反了会永久卡住），200 且 fake 恰收到 1 条非空指令。
	fk.Add(task.ID, fake.Step{Finish: executor.Result{
		OK: true, Summary: "完成", FinalText: "```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```",
	}})
	cont := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+task.ID+"/continue",
		`{"instructions":"继续"}`, "test")
	if cont.Code != http.StatusOK {
		t.Fatalf("waiting_review 上 continue 应 200，实得 %d %s", cont.Code, cont.Body.String())
	}
	var n int
	for _, s := range fk.Sends() {
		if s.TaskID != task.ID {
			continue
		}
		n++
		if s.Text == "" {
			t.Fatal("续接指令不得为空")
		}
		if s.Text != "继续" {
			t.Fatalf("HTTP 层透传的指令应是测试给定文案，实得 %q", s.Text)
		}
	}
	if n != 1 {
		t.Fatalf("本次 /continue 对应的 Send 恰 1 条，实得 %d", n)
	}

	// 断言 3：第二终态 completed，final_text 可解析出 handoff-verdict
	d = b402Wait(t, env, task.ID, "completed", func(d b402Detail) bool {
		return len(b402Events(d, "completed")) > 0
	})
	comps := b402Events(d, "completed")
	var cp struct {
		FinalText *string `json:"final_text"`
	}
	if err := json.Unmarshal(comps[len(comps)-1].Payload, &cp); err != nil {
		t.Fatalf("解 completed payload: %v", err)
	}
	if cp.FinalText == nil || !strings.Contains(*cp.FinalText, "handoff-verdict") {
		t.Fatalf("completed final_text 应含 handoff-verdict，实得 %+v", cp.FinalText)
	}

	// 断言 4：/done 归档 200，终态不再是 waiting_review
	done := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+task.ID+"/done", `{"note":""}`, "test")
	if done.Code != http.StatusOK {
		t.Fatalf("done 应 200，实得 %d %s", done.Code, done.Body.String())
	}
	d = b402Wait(t, env, task.ID, "done 后离开 waiting_review", func(d b402Detail) bool {
		return d.Task.State != proto.TaskStateWaitingReview
	})
	if d.Task.State != proto.TaskStateCompleted {
		t.Fatalf("done 后应 completed，实得 %s", d.Task.State)
	}

	// 断言 2（反例）：另一个 running 任务同一请求必须 409（Manager.Continue 状态门）
	now := time.Now().UTC()
	runningID := "b402-e2e-running-409"
	if err := env.st.CreateTask(&proto.Task{
		ID: runningID, Target: "local", RepoPath: repo, Branch: "main", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	gate := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+runningID+"/continue",
		`{"instructions":"继续"}`, "test")
	if gate.Code != http.StatusConflict {
		t.Fatalf("running 上 continue 应 409，实得 %d %s", gate.Code, gate.Body.String())
	}
}

// TestB402E2EZeroTextTwiceStaysReviewable 主反例：zero_text → zero_text 第二轮
// 仍不归档，且此后 /continue 不是「task 已归档」的 404/409。
func TestB402E2EZeroTextTwiceStaysReviewable(t *testing.T) {
	env, fk := newB402Env(t, []fake.Step{b402ZeroTextStep()})
	repo := initRepo(t)
	pid := b402RegisterProject(t, env.mgr, repo)
	task, err := env.mgr.Dispatch(context.Background(), orchestration.DispatchReq{
		ProjectID: pid, Prompt: "B402 反例", Executor: "fake",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	d := b402Wait(t, env, task.ID, "首个 turn_failed", func(d b402Detail) bool {
		return len(b402Events(d, "turn_failed")) > 0
	})
	if d.Task.State != proto.TaskStateWaitingReview {
		t.Fatalf("首个失败后应 waiting_review，实得 %s", d.Task.State)
	}

	// 第二轮仍零文本：先 Add 再 continue（门禁顺序同主回路）
	fk.Add(task.ID, b402ZeroTextStep())
	cont := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+task.ID+"/continue",
		`{"instructions":"继续"}`, "test")
	if cont.Code != http.StatusOK {
		t.Fatalf("首轮 continue 应 200，实得 %d %s", cont.Code, cont.Body.String())
	}
	d = b402Wait(t, env, task.ID, "第二条 turn_failed", func(d b402Detail) bool {
		return len(b402Events(d, "turn_failed")) >= 2
	})
	if d.Task.State == proto.TaskStateCompleted || d.Task.State == proto.TaskStateFailed {
		t.Fatalf("第二轮零文本后任务不得归档，实得 %s", d.Task.State)
	}
	if len(b402Events(d, "completed")) > 0 {
		t.Fatal("第二轮仍零文本时不得出现 completed 事件")
	}

	// 此后再 /continue：不得因「已归档」拿到 404，也不得是归档语义的 409
	again := doReq(t, env.srv, http.MethodPost, "/api/tasks/"+task.ID+"/continue",
		`{"instructions":"再试一次"}`, "test")
	if again.Code == http.StatusNotFound {
		t.Fatalf("任务仍在 waiting_review，continue 不得 404: %s", again.Body.String())
	}
	if strings.Contains(again.Body.String(), "已归档") || strings.Contains(again.Body.String(), "归档") {
		t.Fatalf("continue 报文不得说任务已归档: %d %s", again.Code, again.Body.String())
	}
	if again.Code != http.StatusOK && again.Code != http.StatusConflict {
		t.Fatalf("continue 应 200（状态门放行）或明确状态错误，实得 %d %s", again.Code, again.Body.String())
	}
}

// TestB402E2EWireOmitsEmptyFailureClass wire 保真反面：未分类失败的 turn_failed
// payload 不得出现 failure_class 键——区分「字段缺失」与「值为零」，旧消费者靠
// 缺键 fail-closed。
func TestB402E2EWireOmitsEmptyFailureClass(t *testing.T) {
	env, _ := newB402Env(t, []fake.Step{{Finish: executor.Result{
		OK: false, FailReason: "executor 进程退出 code=1",
	}}})
	repo := initRepo(t)
	pid := b402RegisterProject(t, env.mgr, repo)
	task, err := env.mgr.Dispatch(context.Background(), orchestration.DispatchReq{
		ProjectID: pid, Prompt: "B402 未分类", Executor: "fake",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	d := b402Wait(t, env, task.ID, "turn_failed", func(d b402Detail) bool {
		return len(b402Events(d, "turn_failed")) > 0
	})
	fails := b402Events(d, "turn_failed")
	raw := string(fails[len(fails)-1].Payload)
	if strings.Contains(raw, "failure_class") {
		t.Fatalf("未分类失败的 wire payload 不得出现 failure_class 键: %s", raw)
	}
}
