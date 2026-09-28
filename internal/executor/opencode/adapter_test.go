// adapter 测试：用 httptest 起可脚本化的 fake opencode server 驱动
// Start → SSE 事件映射 → AdapterEvent 产出 → Send/RespondPermission 转发 →
// serve 死亡判定的全链路验证，不依赖真实 opencode 二进制与 tmux。
//
// 本文件是包内测试（package opencode）：经未导出的 startRun 注入 httptest
// 服务器与假探活，绕开 StartServe 的 tmux 依赖（探活接口见 serveHandle）。
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/proto"
)

const adapterTestPassword = "test-password-123"

// quietLog 把测试期间的 slog.Default 换成丢弃 logger，保证测试输出干净。
func quietLog(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
}

// bufWriter 是并发安全的日志缓冲（slog handler 的 io.Writer 目标）：
// captureLog 用它收集日志，供「断言不应出现某类日志」的场景读回。
type bufWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *bufWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *bufWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// captureLog 把 slog.Default 换成写入内存缓冲的 handler，返回缓冲：
// 订阅 goroutine 与测试协程并发读写，bufWriter 的互斥保证 race 下安全。
func captureLog(t *testing.T) *bufWriter {
	t.Helper()
	buf := &bufWriter{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// fakeProbe 是 serveHandle 的测试替身：alive 可变（模拟 serve 死亡），
// Kill 行为可注入（killErr 模拟 kill 失败，P1-9），并记录每次 Alive 调用
// 时间（探活降频断言用，P1-17）。
type fakeProbe struct {
	mu        sync.Mutex
	alive     bool
	killErr   error
	callTimes []time.Time
}

func (p *fakeProbe) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callTimes = append(p.callTimes, time.Now())
	return p.alive
}

func (p *fakeProbe) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killErr
}

func (p *fakeProbe) LogTail() string { return "fake stderr tail" }

func (p *fakeProbe) setAlive(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alive = v
}

func (p *fakeProbe) setKillErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killErr = err
}

// times 返回 Alive 的调用时间戳快照（探活间隔断言用）。
func (p *fakeProbe) times() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.callTimes...)
}

// permCall 记录 fake server 收到的一次权限应答请求。
type permCall struct {
	path string
	body string
}

// childSession 是假服务端持有的子会话详情（GET /session/{id} 的应答素材）。
type childSession struct {
	parent string
	title  string
}

// fakeServer 是可脚本化的 opencode 假服务端：按 push 顺序逐条推送 SSE 事件，
// 记录建会话/发 prompt/应答权限的请求，全程不依赖真实 opencode。
type fakeServer struct {
	ts               *httptest.Server
	mu               sync.Mutex
	sessionID        string
	promptBodies     []string
	permCalls        []permCall
	lines            chan string
	children         map[string]childSession // 子会话 id -> 详情
	sessionGets      []string                // 收到的 GET /session/{id} 顺序
	sessionGetStatus map[string]int          // 一次性故障注入：子会话 id -> 要返回的状态码
	permissionStatus int                     // 非零时权限回传使用该 HTTP 状态码
	permHold         chan struct{}           // 非 nil 时权限应答挂起直至通道关闭（模拟 API 在飞，B383 T2/T3/T4）
}

// newFakeServer 启动假服务端；SSE 事件经 push 注入（可先入队、连接后送达）。
func newFakeServer(t *testing.T) *fakeServer {
	fs := &fakeServer{
		sessionID:        "sess-1",
		lines:            make(chan string, 64),
		children:         map[string]childSession{},
		sessionGetStatus: map[string]int{},
	}
	fs.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprintf(w, `{"id":%q}`, fs.sessionID)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			body, _ := io.ReadAll(r.Body)
			fs.mu.Lock()
			fs.promptBodies = append(fs.promptBodies, string(body))
			fs.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/permissions/"):
			body, _ := io.ReadAll(r.Body)
			fs.mu.Lock()
			fs.permCalls = append(fs.permCalls, permCall{path: r.URL.Path, body: string(body)})
			status := fs.permissionStatus
			hold := fs.permHold
			fs.mu.Unlock()
			// 先挂住再回包：permHold 模拟「应答已到 server 但 API 未返回」的在飞窗口，
			// 驱动 idle 与应答结算的交错（B383 T2/T3/T4）。放行后才按 status 回包
			if hold != nil {
				<-hold
			}
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("permission delivery failed"))
				return
			}
			w.Write([]byte("true"))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/session/") &&
			!strings.Contains(strings.TrimPrefix(r.URL.Path, "/session/"), "/"):
			child := strings.TrimPrefix(r.URL.Path, "/session/")
			fs.mu.Lock()
			fs.sessionGets = append(fs.sessionGets, child)
			// 一次性故障注入：用完即清，供「负结果不缓存」用例对同一 child 先失败后成功
			if code, bad := fs.sessionGetStatus[child]; bad {
				delete(fs.sessionGetStatus, child)
				fs.mu.Unlock()
				w.WriteHeader(code)
				return
			}
			d, ok := fs.children[child]
			fs.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"id":%q,"parentID":%q,"title":%q}`, child, d.parent, d.title)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			// SSE 流：脚本行逐条 flush，脚本耗尽后保持连接直到客户端断开
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			for {
				select {
				case line := <-fs.lines:
					fmt.Fprint(w, line)
					fl.Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	// 先断活跃连接再关服务器：SSE 处理器阻塞在 r.Context().Done() 上，
	// 直接 Close 会死等处理器结束
	t.Cleanup(func() {
		fs.ts.CloseClientConnections()
		fs.ts.Close()
	})
	return fs
}

// push 把一条完整 SSE 事件帧（data 行 + 结尾空行）注入订阅流。
func (fs *fakeServer) push(line string) { fs.lines <- line }

// prompts 返回收到的 prompt_async 请求体（按顺序）。
func (fs *fakeServer) prompts() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]string(nil), fs.promptBodies...)
}

// perms 返回收到的权限应答请求（按顺序）。
func (fs *fakeServer) perms() []permCall {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return append([]permCall(nil), fs.permCalls...)
}

// failPermissionResponses 让下一轮权限回传返回指定状态码，验证决定已形成但
// 原生投递失败时由调用方观察到失败，而不是误记为 AckDelivered。
func (fs *fakeServer) failPermissionResponses(code int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.permissionStatus = code
}

// holdPermissionResponses 让后续权限应答挂住不回（模拟 API 在飞），由
// releasePermissionResponses 放行。挂起期间应答请求本身已到达 server——
// 用于驱动「idle 先到、应答后结算」的交错（B383 T2/T3/T4）。
func (fs *fakeServer) holdPermissionResponses() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.permHold = make(chan struct{})
}

// releasePermissionResponses 放行挂起的权限应答（幂等）：回包状态取
// failPermissionResponses 的预设（0=成功）。测试结束前必须放行，否则挂起的
// handler 会让 httptest.Close 死等。
func (fs *fakeServer) releasePermissionResponses() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.permHold != nil {
		close(fs.permHold)
		fs.permHold = nil
	}
}

// waitPermCalls 等到 fake server 收到 n 次权限应答请求（应答在飞检测的同步点）。
func waitPermCalls(t *testing.T, fs *fakeServer, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if len(fs.perms()) >= n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("等待权限应答请求超时：到达 %d/%d", len(fs.perms()), n)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// assertNoResultWithin 断言 d 内事件通道不产出 result 事件（usage/progress 等
// 噪音允许出现）。「应答在飞时不得提前分类」用。
func assertNoResultWithin(t *testing.T, ch <-chan executor.AdapterEvent, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-ch:
			if ev.Type == "result" {
				t.Fatalf("不应产出 result，实得 %+v", ev.Result)
			}
		case <-deadline:
			return
		}
	}
}

// addChild 登记一个子会话，供 GET /session/{id} 应答。
func (fs *fakeServer) addChild(id, parent, title string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.children[id] = childSession{parent: parent, title: title}
}

// failNextSessionGet 让下一次 GET /session/{id} 返回指定状态码（一次性）。
func (fs *fakeServer) failNextSessionGet(id string, code int) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.sessionGetStatus[id] = code
}

// sessionGetCount 返回针对某个子会话的 GET /session/{id} 次数。
func (fs *fakeServer) sessionGetCount(id string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	n := 0
	for _, got := range fs.sessionGets {
		if got == id {
			n++
		}
	}
	return n
}

// sseLine 把任意事件 JSON 包装成单条 SSE data 行（data: <json> + 空行成帧）。
func sseLine(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// 事件构造器全部对齐 spike3/spike5 抓到的真实 opencode 1.18.15 样本：
//   - message.updated 只有 properties.info（role/messageID），不带文本；
//   - 文本载体是 message.part.updated（part.type=text 全量快照）与
//     message.part.delta（properties.field=text 增量）；
//   - 回合结束主信号是 session.status 的 status.type=idle，同现的 session.idle 冗余；
//   - 权限是 permission.asked（properties.id 即 PermissionID），应答回显
//     permission.replied 必须被忽略。

// userMsgEvent 构造一条 message.updated（user 消息信息，仅 info 无文本）。
func userMsgEvent(id string) string {
	return sseLine(map[string]any{
		"type":      "message.updated",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"info":      map[string]any{"id": id, "role": "user"},
		},
	})
}

// partUpdatedEvent 构造一条 message.part.updated（text 类型 part 的全量快照）。
func partUpdatedEvent(msgID, partID, text string) string {
	return partUpdatedTypedEvent(msgID, partID, "text", text)
}

// partUpdatedTypedEvent 构造一条 message.part.updated（可指定 part 类型：
// text/reasoning/tool——非文本 part 隔离测试用，spike5 实测 reasoning 的
// part.updated 先于其 delta 流到达且 text 为空）。
func partUpdatedTypedEvent(msgID, partID, partType, text string) string {
	return sseLine(map[string]any{
		"type":      "message.part.updated",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"part": map[string]any{
				"type": partType, "text": text, "messageID": msgID,
				"sessionID": "sess-1", "id": partID,
			},
		},
	})
}

// partDeltaEvent 构造一条 message.part.delta（text 字段流式增量）。
func partDeltaEvent(msgID, partID, delta string) string {
	return sseLine(map[string]any{
		"type":      "message.part.delta",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"messageID": msgID, "partID": partID, "field": "text", "delta": delta,
		},
	})
}

// statusIdleEvent 构造一条 session.status（status.type=idle，回合结束主信号）。
func statusIdleEvent() string {
	return sseLine(map[string]any{
		"type":      "session.status",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"status":    map[string]any{"type": "idle"},
		},
	})
}

// sessionIdleEvent 构造一条 session.idle（与 session.status idle 同现的冗余信号，
// 用于验证去重：两个信号不得重复触发分类）。
func sessionIdleEvent() string {
	return sseLine(map[string]any{
		"type":      "session.idle",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
		},
	})
}

// permissionAskedEvent 构造一条 permission.asked 事件（真实结构：id/
// permission/patterns/metadata/tool）。
func permissionAskedEvent(id, perm, command string) string {
	return sseLine(map[string]any{
		"type":      "permission.asked",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"id": id, "sessionID": "sess-1", "permission": perm,
			"patterns": []string{command},
			"metadata": map[string]any{"command": command},
			"tool":     map[string]any{"messageID": "msg-1", "callID": "call-1"},
		},
	})
}

// permissionAskedEventFrom 构造一条来自指定会话的 permission.asked 事件。
func permissionAskedEventFrom(sessionID, id, perm, command string) string {
	return sseLine(map[string]any{
		"type":      "permission.asked",
		"sessionID": sessionID,
		"properties": map[string]any{
			"id": id, "sessionID": sessionID, "permission": perm,
			"patterns": []string{command},
			"metadata": map[string]any{"command": command},
			"tool":     map[string]any{"messageID": "msg-1", "callID": "call-1"},
		},
	})
}

// permissionRepliedEvent 构造一条 permission.replied 事件（应答回显，应被忽略）。
func permissionRepliedEvent(id string) string {
	return sseLine(map[string]any{
		"type":      "permission.replied",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1", "requestID": id, "reply": "once",
		},
	})
}

// gitAt 在 dir 里执行 git 命令，失败即 Fatal（测试环境问题，不是被测行为）。
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initGit 造一个带初始提交的干净仓库（main 分支），返回仓库路径。
func initGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitAt(t, dir, "init", "-q")
	gitAt(t, dir, "checkout", "-b", "main")
	gitAt(t, dir, "config", "user.email", "test@handoff.dev")
	gitAt(t, dir, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatalf("写 README: %v", err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// gitCommit 在仓库里追加一个文件并提交（模拟 executor 干完活留了新 commit）。
func gitCommit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s: %v", name, err)
	}
	gitAt(t, dir, "add", name)
	gitAt(t, dir, "commit", "-q", "-m", msg)
}

// startFakeRun 以 fake server + 假探活启动一次运行并返回事件通道。
func startFakeRun(t *testing.T, fs *fakeServer, taskID, repo, taskDir string) (*Adapter, <-chan executor.AdapterEvent) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("执行计划"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	ad := New(slog.Default())
	// idle 去抖宽限期压到毫秒级：回合分类的断言不必真等生产的 1.5s
	// （去抖语义本身由 regression_round2_test.go 专门覆盖）
	ad.idleGrace = 20 * time.Millisecond
	probe := &fakeProbe{alive: true}
	req := executor.StartReq{
		Task:        proto.Task{ID: taskID, RepoPath: repo},
		TaskDir:     taskDir,
		PlanContent: "plan",
	}
	if _, err := ad.startRun(context.Background(), req, NewAPI(fs.ts.URL, adapterTestPassword), probe); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	return ad, ad.Events(taskID)
}

// isTimingEvent 判断 usage 通道里的耗时事件。它是正常的元数据噪音，
// 不应改变这些行为测试对 permission/question/result 的断言顺序。
func isTimingEvent(ev executor.AdapterEvent) bool {
	return ev.Type == "usage" && ev.Timing != nil
}

// waitEventType 消费事件通道直到出现指定类型的事件（跳过 progress/耗时等噪音）。
func waitEventType(t *testing.T, ch <-chan executor.AdapterEvent, wantType string) executor.AdapterEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == wantType {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待 %s 事件超时", wantType)
		}
	}
}

// TestStartToPermissionFlow 验证启动链路与权限事件映射：
// fake server 推 permission 事件 → 事件通道产出 Type=permission（PermissionID/
// 描述正确）；RespondPermission 转发到 fake server 恰一次（path/body 契约）；
// 初始 prompt 已按 prompt.md 内容发出。
func TestStartToPermissionFlow(t *testing.T) {
	quietLog(t)
	taskID := "task-perm-0001"
	fs := newFakeServer(t)
	fs.push(permissionAskedEvent("perm-1", "bash", "echo spike-hi"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-1" {
		t.Errorf("PermissionID=%q，期望 perm-1", ev.PermissionID)
	}
	if !strings.Contains(ev.Text, "echo spike-hi") {
		t.Errorf("权限描述=%q，应含命令文本", ev.Text)
	}

	if err := ad.RespondPermission(context.Background(), taskID, "perm-1", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	perms := fs.perms()
	if len(perms) != 1 {
		t.Fatalf("fake server 应恰收 1 次权限应答，实际 %d", len(perms))
	}
	if perms[0].path != "/session/sess-1/permissions/perm-1" {
		t.Errorf("权限应答路径=%q，期望 /session/sess-1/permissions/perm-1", perms[0].path)
	}
	if !strings.Contains(perms[0].body, `"response":"once"`) {
		t.Errorf("权限应答体=%q，应含 \"response\":\"once\"", perms[0].body)
	}

	prompts := fs.prompts()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "执行计划") {
		t.Errorf("初始 prompt 应恰好发出 1 次且含 prompt.md 内容，实际 %d 次: %v", len(prompts), prompts)
	}
}

// TestIdleClassifyAsk 验证回合结束分类：文本 + idle → question 事件文本正确，
// 且消息文本已追加进 render.log（tmux 第二窗口可见性）。
func TestIdleClassifyAsk(t *testing.T) {
	quietLog(t)
	taskID := "task-ask-0001"
	taskDir := t.TempDir()
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "分析完毕\n{\"ask\":\"用哪个实现？\"}"))
	fs.push(statusIdleEvent())

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), taskDir)
	ev := waitEventType(t, ch, "question")
	if ev.Text != "用哪个实现？" {
		t.Errorf("question 文本=%q，期望 \"用哪个实现？\"", ev.Text)
	}

	render, err := os.ReadFile(filepath.Join(taskDir, "render.log"))
	if err != nil {
		t.Fatalf("读取 render.log: %v", err)
	}
	if !strings.Contains(string(render), "分析完毕") {
		t.Errorf("render.log 应包含模型文本，实际:\n%s", render)
	}
}

// TestIdleClassifyFinish 验证 finish trailer → result OK，Branch/Commit/
// Summary/SessionID 字段全部正确。
func TestIdleClassifyFinish(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", strings.Repeat("前文 ", turn.FinalTextLimit+100)+
		"{\"branch\":\"handoff/T1\",\"commit\":\"abc12345\",\"summary\":\"完成功能\"}\n"+
		"```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```"))
	fs.push(statusIdleEvent())

	_, ch := startFakeRun(t, fs, "task-fin-0001", t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "result")
	if ev.Result == nil || !ev.Result.OK {
		t.Fatalf("期望 result OK，实际 %+v", ev.Result)
	}
	if ev.Result.Branch != "handoff/T1" {
		t.Errorf("Branch=%q，期望 handoff/T1", ev.Result.Branch)
	}
	if ev.Result.CommitHash != "abc12345" {
		t.Errorf("Commit=%q，期望 abc12345", ev.Result.CommitHash)
	}
	if ev.Result.Summary != "完成功能" {
		t.Errorf("Summary=%q，期望 完成功能", ev.Result.Summary)
	}
	if ev.Result.SessionID != "sess-1" {
		t.Errorf("SessionID=%q，期望 sess-1（供 manager 落 ExecutorSession）", ev.Result.SessionID)
	}
	if !strings.Contains(ev.Result.FinalText, "handoff-verdict") {
		t.Fatalf("成功结果必须带回合末正文，实得 %q", ev.Result.FinalText)
	}
	if len([]rune(ev.Result.FinalText)) != turn.FinalTextLimit {
		t.Fatalf("超长正文应尾截断到 %d rune，实得 %d", turn.FinalTextLimit, len([]rune(ev.Result.FinalText)))
	}
}

// TestIdleFallbackNoTrailer 验证无 trailer 时的 git 兜底分类：
// 无新 commit → question（回合全文交协调者）；有新 commit → result !OK
// （B74：绝不替模型宣布完成，branch/commit 取 git 实况留结构化字段）。
func TestIdleFallbackNoTrailer(t *testing.T) {
	const assistantText = "我做了改动，但忘了按纪律输出协议 JSON"

	t.Run("no_new_commit", func(t *testing.T) {
		quietLog(t)
		repo := initGit(t)
		fs := newFakeServer(t)
		fs.push(userMsgEvent("msg-u1"))
		fs.push(partUpdatedEvent("msg-a1", "prt-a1", assistantText))
		fs.push(statusIdleEvent())

		_, ch := startFakeRun(t, fs, "task-fb1-0001", repo, t.TempDir())
		ev := waitEventType(t, ch, "question")
		if ev.Text != assistantText {
			t.Errorf("兜底 question 文本=%q，期望回合全文 %q", ev.Text, assistantText)
		}
	})

	t.Run("with_new_commit", func(t *testing.T) {
		quietLog(t)
		repo := initGit(t)
		fs := newFakeServer(t)
		_, ch := startFakeRun(t, fs, "task-fb2-0001", repo, t.TempDir())
		// 起点 commit 在 startRun 时已捕获；现在模拟 executor 干活留了新 commit，
		// 再推送回合结束信号（先落库后推事件，保证兜底判定可见新提交）
		gitCommit(t, repo, "impl.go", "package main\n", "feat: impl")
		wantHead := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
		fs.push(userMsgEvent("msg-u1"))
		fs.push(partUpdatedEvent("msg-a1", "prt-a1", assistantText))
		fs.push(statusIdleEvent())

		ev := waitEventType(t, ch, "result")
		// B74 翻转：有新提交 ≠ 干完了，绝不宣布完成
		if ev.Result == nil || ev.Result.OK {
			t.Fatalf("期望兜底 result !OK，实际 %+v", ev.Result)
		}
		if ev.Result.Branch != "main" {
			t.Errorf("兜底 Branch=%q，期望 main（git 实况）", ev.Result.Branch)
		}
		if ev.Result.CommitHash != wantHead {
			t.Errorf("兜底 Commit=%q，期望 git 实况 %q", ev.Result.CommitHash, wantHead)
		}
		// git 实况留在结构化字段，失败原因仍带回合尾部给人看
		if !strings.Contains(ev.Result.FailReason, "未输出协议 trailer") {
			t.Errorf("兜底 FailReason 缺判定依据: %q", ev.Result.FailReason)
		}
		if !strings.Contains(ev.Result.FailReason, assistantText) {
			t.Errorf("兜底 FailReason 缺回合尾部: %q", ev.Result.FailReason)
		}
	})
}

// TestSessionReadyProgress 验证「会话就绪」信号：CreateSession 成功后立即产出
// 一条带 SessionID 的 progress 事件（非耗时事件首条为它，见 startRun 顺序）。
// manager 据此落 task.ExecutorSession——question 收尾的审核主路径不经 result，
// 这是会话 id 到达 manager 的可靠通道。
func TestSessionReadyProgress(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-ready-001", t.TempDir(), t.TempDir())
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if isTimingEvent(ev) {
				continue
			}
			if ev.Type != "progress" {
				t.Fatalf("首个非耗时事件类型=%s，期望 progress（会话就绪信号）", ev.Type)
			}
			if ev.SessionID != "sess-1" {
				t.Errorf("会话就绪事件 SessionID=%q，期望 sess-1（manager 落 ExecutorSession 用）", ev.SessionID)
			}
			if !strings.Contains(ev.Text, "会话就绪") {
				t.Errorf("会话就绪事件文本=%q，应含 会话就绪", ev.Text)
			}
			return
		case <-deadline:
			t.Fatal("等待会话就绪 progress 事件超时")
		}
	}
}

// TestIdleEmptyTurnSkips 验证空回合 idle 的收尾行为（B21 修复后）：
// 无任何文本 part 时回合文本为空，idle 必须产出失败结果（交协调者，不再静默
// 挂到 2h 看门狗）；冗余的 session.idle 信号同现时不得重复产出第二条事件。
func TestIdleEmptyTurnSkips(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-empty-001", t.TempDir(), t.TempDir())
	// 先消费会话就绪 progress，再注入空回合 idle（两个冗余信号都推，验证只产一条）
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if isTimingEvent(ev) {
				continue
			}
			if ev.Type != "progress" {
				t.Fatalf("首个非耗时事件类型=%s，期望 progress", ev.Type)
			}
			goto ready
		case <-deadline:
			t.Fatal("等待会话就绪 progress 事件超时")
		}
	}
ready:
	fs.push(statusIdleEvent())
	fs.push(sessionIdleEvent())
	deadline = time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if isTimingEvent(ev) {
				continue
			}
			if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
				t.Fatalf("空回合 idle 应产出失败结果（B21），实际 %+v", ev)
			}
			goto resultReceived
		case <-deadline:
			t.Fatal("等待空回合失败结果超时")
		}
	}
resultReceived:
	select {
	case ev := <-ch:
		t.Fatalf("冗余 session.idle 不应再产出第二条事件，收到 %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRejectedPermissionEndsTurnEmitsDeniedResult（B383 Wave 0 T1）验证「权限被拒 →
// 回合静默终止」的实时终局改为失败结果（旧实现发合成 question，manager 会再建一张
// ask 工单）：
//
// opencode 收到 reject 后**直接终结整个回合**——最后一条 assistant 消息只有一个
// error 状态的 tool part，零文本 part。API 结算（成功）先于 idle 到达时，idle 必须
// 产出**恰一条** result{OK:false, VoidReason=权限被拒}，FailReason 点明被拒的权限，
// 让协调者在 waiting_review 里用 continue 续接（换方式/跳过/收尾）。
func TestRejectedPermissionEndsTurnEmitsDeniedResult(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(permissionAskedEvent("per-rej-1", "bash", "git push --dry-run origin main"))
	ad, ch := startFakeRun(t, fs, "task-reject-01", t.TempDir(), t.TempDir())

	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "per-rej-1" {
		t.Fatalf("PermissionID=%q，期望 per-rej-1", ev.PermissionID)
	}
	// 协调者拒绝：adapter 回传 reject 后，opencode 侧回合随即终止（无文本产出）
	if err := ad.RespondPermission(context.Background(), "task-reject-01", "per-rej-1", "reject", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	fs.push(statusIdleEvent())

	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("被拒终止的空回合应产出失败结果，实际 %+v", got)
	}
	// 文本必须让协调者一眼看懂「为什么停了」：点明权限被拒 + 原始命令
	if !strings.Contains(got.Result.FailReason, "权限被拒") {
		t.Errorf("FailReason 应说明回合因权限被拒终止，实际=%q", got.Result.FailReason)
	}
	if !strings.Contains(got.Result.FailReason, "git push --dry-run origin main") {
		t.Errorf("FailReason 应含被拒的权限描述，实际=%q", got.Result.FailReason)
	}
	if got.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Errorf("VoidReason=%q，期望 %q（executor 仍在线，审计必须说真话）",
			got.Result.VoidReason, executor.VoidReasonPermissionDenied)
	}
	// 恰一次：回合已终结，冗余 idle 信号不得再发第二条终局
	fs.push(sessionIdleEvent())
	assertNoResultWithin(t, ch, 300*time.Millisecond)
}

// TestRejectSettleAfterIdleEmitsDeniedResultOnce（B383 Wave 0 T2）：idle 先到、
// 应答界内结算成功。
//
// 交错现场：reject 的 HTTP 转发还没返回，SSE idle 已把空回合送进分类。此刻不得
// 提前判（应答成功=被拒终局，失败=普通空回合，两种结局都在后面）——必须复用
// idle 去抖机制延迟到应答结算后判；结算成功后产出恰一条被拒 result。
func TestRejectSettleAfterIdleEmitsDeniedResultOnce(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-reject-t2"
	fs.push(permissionAskedEvent("perm-t2", "bash", "echo t2"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	// 重武装宽限注入毫秒级（模式同 idleGrace）：500ms ≫ 结算延迟，到期时必已结算
	ad.rejectSettleGrace = 500 * time.Millisecond
	waitEventType(t, ch, "permission")

	fs.holdPermissionResponses()
	t.Cleanup(fs.releasePermissionResponses)
	done := make(chan error, 1)
	go func() { done <- ad.RespondPermission(context.Background(), taskID, "perm-t2", "reject", "") }()
	waitPermCalls(t, fs, 1) // 应答请求已到达 server，回包挂起（在飞）

	// idle 先到：应答在飞，不得凭「登记了拒绝意向」提前宣判
	fs.push(statusIdleEvent())
	assertNoResultWithin(t, ch, 100*time.Millisecond)

	// 界内结算成功 → 延迟分类产出被拒 result 恰一次
	fs.releasePermissionResponses()
	if err := <-done; err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("结算成功后应产出被拒失败结果，实际 %+v", got)
	}
	if got.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Errorf("VoidReason=%q，期望 %q", got.Result.VoidReason, executor.VoidReasonPermissionDenied)
	}
	if !strings.Contains(got.Result.FailReason, "echo t2") {
		t.Errorf("FailReason 应含被拒描述，实际=%q", got.Result.FailReason)
	}
	// 重复 idle（冗余信号）不重发
	fs.push(sessionIdleEvent())
	assertNoResultWithin(t, ch, 300*time.Millisecond)
}

// TestRejectDeliveryFailureAfterIdleFallsToZeroText（B383 Wave 0 T3）：idle 先到、
// 应答失败（含超时）。没送达的拒绝不会终结 executor 的回合，标记必须摘除——
// 延迟分类到期时按普通零文本回合（B21）收口，绝不冒充被拒终局。
func TestRejectDeliveryFailureAfterIdleFallsToZeroText(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-reject-t3"
	fs.push(permissionAskedEvent("perm-t3", "bash", "echo t3"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ad.rejectSettleGrace = 500 * time.Millisecond
	waitEventType(t, ch, "permission")

	fs.failPermissionResponses(http.StatusBadGateway)
	fs.holdPermissionResponses()
	t.Cleanup(fs.releasePermissionResponses)
	done := make(chan error, 1)
	go func() { done <- ad.RespondPermission(context.Background(), taskID, "perm-t3", "reject", "") }()
	waitPermCalls(t, fs, 1)

	fs.push(statusIdleEvent())
	assertNoResultWithin(t, ch, 100*time.Millisecond)

	fs.releasePermissionResponses() // 放行后按预设回 502 → 失败结算
	if err := <-done; err == nil {
		t.Fatal("API 失败应返回错误")
	}
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("零文本回合应产出失败结果（B21），实际 %+v", got)
	}
	if got.Result.VoidReason == executor.VoidReasonPermissionDenied {
		t.Fatalf("投递失败的拒绝不得按被拒归因: %+v", got.Result)
	}
	if !strings.Contains(got.Result.FailReason, "零文本") {
		t.Fatalf("应落到 B21 零文本形态: %q", got.Result.FailReason)
	}
}

// TestRejectDeliveryTimeoutFailsAndLateSettleLeaksNothing（B383 Wave 0 T4）：
// 应答转发挂死（httptest 挂起 + 注入短超时）→ 硬截止生效走失败路径；
// 迟到的真实结算（发生在标记摘除之后）零泄漏——绝不凭迟到回包重新登记已拒。
func TestRejectDeliveryTimeoutFailsAndLateSettleLeaksNothing(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-reject-t4"
	fs.push(permissionAskedEvent("perm-t4", "bash", "echo t4"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ad.respondTimeout = 100 * time.Millisecond    // 注入短超时（生产为 30s 常量）
	ad.rejectSettleGrace = 500 * time.Millisecond // 宽限 > 超时：到期时必已结算
	waitEventType(t, ch, "permission")

	fs.holdPermissionResponses()
	t.Cleanup(fs.releasePermissionResponses)
	done := make(chan error, 1)
	go func() { done <- ad.RespondPermission(context.Background(), taskID, "perm-t4", "reject", "") }()
	waitPermCalls(t, fs, 1)

	fs.push(statusIdleEvent())
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("超时后应按零文本失败收口，实际 %+v", got)
	}
	if got.Result.VoidReason == executor.VoidReasonPermissionDenied {
		t.Fatalf("超时不是送达，不得按被拒归因: %+v", got.Result)
	}
	if err := <-done; err == nil {
		t.Fatal("应答转发超时应返回错误")
	}
	// 下一回合零泄漏：拒绝标记已随失败结算摘除，冗余 idle 静默
	fs.push(sessionIdleEvent())
	assertNoResultWithin(t, ch, 300*time.Millisecond)
}

// TestChildSessionRejectParentEmptyTurnEmitsDeniedResult（B383 Wave 0 T6）：
// 子会话权限被拒 + 父回合空文本终结 → 被拒 result。
//
// 拒绝标记是任务级的（子会话的 reject 同样由父任务的回合收口）：子会话 idle 被
// acceptForeign 丢弃不终结父回合，父会话空文本 idle 才是终局点，此刻必须按被拒
// 收口而不是 B21 零文本。
func TestChildSessionRejectParentEmptyTurnEmitsDeniedResult(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	taskID := "task-reject-t6"
	fs.push(permissionAskedEventFrom("sess-child", "perm-t6", "bash", "curl https://example.com"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())

	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-t6" {
		t.Fatalf("PermissionID=%q，期望 perm-t6", ev.PermissionID)
	}
	if err := ad.RespondPermission(context.Background(), taskID, "perm-t6", "reject", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	// 父会话空文本 idle 终结回合（子会话的 idle 事件即使到达也被丢弃）
	fs.push(statusIdleEvent())

	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("子会话拒绝+父空文本终结应产出被拒失败结果，实际 %+v", got)
	}
	if got.Result.VoidReason != executor.VoidReasonPermissionDenied {
		t.Errorf("VoidReason=%q，期望 %q", got.Result.VoidReason, executor.VoidReasonPermissionDenied)
	}
	if !strings.Contains(got.Result.FailReason, "curl https://example.com") {
		t.Errorf("FailReason 应含被拒的权限描述，实际=%q", got.Result.FailReason)
	}
}

// TestApprovedPermissionEmptyTurnEmitsFailedResult 验证 B21 修复后的边界：
// 权限被批准后回合正常继续，但若回合最终零文本（供应商流中断/文本流没被接住），
// idle 产出的是一份故障报告 result{OK:false}，而不是 question——被拒终止才有内容
// 可问（走 question），零文本回合没有任何可问的，只能按故障报交协调者。
func TestApprovedPermissionEmptyTurnEmitsFailedResult(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(permissionAskedEvent("per-ok-1", "bash", "go test ./..."))
	ad, ch := startFakeRun(t, fs, "task-approve-01", t.TempDir(), t.TempDir())

	waitEventType(t, ch, "permission")
	if err := ad.RespondPermission(context.Background(), "task-approve-01", "per-ok-1", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	fs.push(statusIdleEvent())
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if isTimingEvent(ev) {
				continue
			}
			if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
				t.Fatalf("批准后的零文本回合应产出失败结果（B21），实际 %+v", ev)
			}
			return
		case <-deadline:
			t.Fatal("等待零文本回合失败结果超时")
		}
	}
}

// TestPartDeltaAccumulation 验证 part 级文本累积（spike5 实测的两种流式顺序）：
//   - part.updated(空串创建) → delta 流 → part.updated(全量快照)：快照与
//     delta 已累积一致 → 去重，不重复追加；
//   - 全量快照先行 → 后续 delta：快照已含该文本 → delta 必须被跳过，
//     否则回合文本被污染（render.log 断言捕获该回归）。
func TestPartDeltaAccumulation(t *testing.T) {
	quietLog(t)
	taskDir := t.TempDir()
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	// spike5 实测顺序：part 创建（空文本）→ 逐条 delta → 回发全量快照
	fs.push(partUpdatedEvent("msg-a1", "prt-d1", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-d1", "sp"))
	fs.push(partDeltaEvent("msg-a1", "prt-d1", "ike"))
	fs.push(partDeltaEvent("msg-a1", "prt-d1", "-hi"))
	fs.push(partUpdatedEvent("msg-a1", "prt-d1", "spike-hi"))
	// 另一 part：全量快照先行，随后的 delta 冗余必须被跳过
	fs.push(partUpdatedEvent("msg-a1", "prt-d2", "完成\n{\"ask\":\"选 A 还是 B？\"}"))
	fs.push(partDeltaEvent("msg-a1", "prt-d2", "!!"))
	fs.push(statusIdleEvent())

	_, ch := startFakeRun(t, fs, "task-part-0001", t.TempDir(), taskDir)
	ev := waitEventType(t, ch, "question")
	if ev.Text != "选 A 还是 B？" {
		t.Errorf("question 文本=%q，期望 \"选 A 还是 B？\"", ev.Text)
	}

	render, err := os.ReadFile(filepath.Join(taskDir, "render.log"))
	if err != nil {
		t.Fatalf("读取 render.log: %v", err)
	}
	if strings.Count(string(render), "spike-hi") != 1 {
		t.Errorf("render.log 中 spike-hi 应恰出现 1 次（快照去重失败会重复），实际:\n%s", render)
	}
	if strings.Contains(string(render), "!!") {
		t.Errorf("render.log 不应含快照后的冗余 delta !!，实际:\n%s", render)
	}
}

// TestReasoningDeltaIsolated 验证 reasoning part 的流式增量不混入回合：
// part.updated 先登记 part 类型（spike5 实测：reasoning part.updated 空文本
// 创建先到，其后约 150 条 field=text 的 delta；delta 无 part 类型字段），
// 已登记的 reasoning 类型必须让增量被跳过——否则推理文本会累积进回合与
// render.log：兜底 question 变推理墙、reasoning-only 回合不再命中空回合 Warn、
// reasoning 含 { 开头行还会被 ParseTrailer 误判 finish。text part 的 delta
// 不受类型过滤影响，照常累积。
func TestReasoningDeltaIsolated(t *testing.T) {
	quietLog(t)
	taskDir := t.TempDir()
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	// spike5:123→125 实测顺序：reasoning part.updated（空文本创建）先于其 delta 流
	fs.push(partUpdatedTypedEvent("msg-a1", "prt-r1", "reasoning", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-r1", "让我思考一下这个需求："))
	fs.push(partDeltaEvent("msg-a1", "prt-r1", "{"))
	fs.push(partDeltaEvent("msg-a1", "prt-r1", "最快路径是直接问用户"))
	// 文本 part：照 spike5 顺序走「创建（空）→ delta 流」，验证 text delta 仍累积
	fs.push(partUpdatedEvent("msg-a1", "prt-t1", ""))
	fs.push(partDeltaEvent("msg-a1", "prt-t1", "分析完毕\n{\"ask\":\"选 A 还是 B？\"}"))
	fs.push(statusIdleEvent())

	_, ch := startFakeRun(t, fs, "task-reason-001", t.TempDir(), taskDir)
	ev := waitEventType(t, ch, "question")
	if ev.Text != "选 A 还是 B？" {
		t.Errorf("question 文本=%q，期望 \"选 A 还是 B？\"（reasoning 混入会污染回合）", ev.Text)
	}

	render, err := os.ReadFile(filepath.Join(taskDir, "render.log"))
	if err != nil {
		t.Fatalf("读取 render.log: %v", err)
	}
	if strings.Contains(string(render), "让我思考") || strings.Contains(string(render), "最快路径") {
		t.Errorf("render.log 不应含 reasoning 增量，实际:\n%s", render)
	}
	if !strings.Contains(string(render), "分析完毕") {
		t.Errorf("render.log 应含 text part 增量（text delta 不能被类型过滤误伤），实际:\n%s", render)
	}
}

// TestPermissionAskedMapping 验证 permission.asked 映射：PermissionID 取
// properties.id，Text 由 permission 字段 + metadata.command 组合（无 command
// 时退回 patterns）。
func TestPermissionAskedMapping(t *testing.T) {
	t.Run("with_command_metadata", func(t *testing.T) {
		quietLog(t)
		fs := newFakeServer(t)
		fs.push(permissionAskedEvent("per_abc123", "bash", "echo spike-hi"))

		_, ch := startFakeRun(t, fs, "task-pmap-0001", t.TempDir(), t.TempDir())
		ev := waitEventType(t, ch, "permission")
		if ev.PermissionID != "per_abc123" {
			t.Errorf("PermissionID=%q，期望 per_abc123（取 properties.id）", ev.PermissionID)
		}
		if ev.Text != "bash: echo spike-hi" {
			t.Errorf("权限描述=%q，期望 \"bash: echo spike-hi\"", ev.Text)
		}
	})

	t.Run("patterns_only", func(t *testing.T) {
		quietLog(t)
		fs := newFakeServer(t)
		fs.push(sseLine(map[string]any{
			"type":      "permission.asked",
			"sessionID": "sess-1",
			"properties": map[string]any{
				"id": "per_pat1", "sessionID": "sess-1", "permission": "edit",
				"patterns": []string{"src/a.ts", "src/b.ts"},
			},
		}))

		_, ch := startFakeRun(t, fs, "task-pmap-0002", t.TempDir(), t.TempDir())
		ev := waitEventType(t, ch, "permission")
		if ev.Text != "edit: src/a.ts src/b.ts" {
			t.Errorf("权限描述=%q，期望 \"edit: src/a.ts src/b.ts\"（退回 patterns）", ev.Text)
		}
	})
}

// TestPermissionRepliedIgnored 验证 permission.replied（应答回显）不产出事件：
// 若把 replied 也当新权限，respond 会被当成再次询问，权限流程死循环。
func TestPermissionRepliedIgnored(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(permissionAskedEvent("per-1", "bash", "echo spike-hi"))
	fs.push(permissionRepliedEvent("per-1"))

	_, ch := startFakeRun(t, fs, "task-prep-0001", t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "per-1" {
		t.Fatalf("PermissionID=%q，期望 per-1", ev.PermissionID)
	}
	select {
	case ev2 := <-ch:
		if ev2.Type == "permission" {
			t.Fatalf("permission.replied 不应再产出权限事件，收到 %+v", ev2)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// TestUserMessageResendDoesNotClearTurn 验证同一 user 消息的 message.updated
// 重发不清空回合：spike5 实测同一 user msg id 的 message.updated 出现 3 次
// （每次紧跟 session.diff 广播）；重发若落在末段文本/trailer 之后、idle 之前
// （executor 提交代码→session.diff→重发，正是 handoff 的典型节奏），无条件
// 清空会让 idle 走空回合 Warn、任务永不分类。修复后仅 first-seen 归零。
func TestUserMessageResendDoesNotClearTurn(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "完成\n{\"ask\":\"选 A 还是 B？\"}"))
	fs.push(userMsgEvent("msg-u1")) // 同 id 重发：模拟 session.diff 广播后的服务端重发
	fs.push(statusIdleEvent())

	_, ch := startFakeRun(t, fs, "task-uresend-001", t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "question")
	if ev.Text != "选 A 还是 B？" {
		t.Errorf("question 文本=%q，期望 \"选 A 还是 B？\"（重发清空回合后 idle 走空回合 Warn）", ev.Text)
	}
}

// TestIdleDedupe 验证 idle 双信号去重：真实流在 session.status idle 后同现
// session.idle，两条都触发会重复分类（重复 question/result）——只认
// session.status idle 主信号，session.idle 必须被忽略。
func TestIdleDedupe(t *testing.T) {
	buf := captureLog(t)
	fs := newFakeServer(t)
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "完成\n{\"ask\":\"选 A 还是 B？\"}"))
	fs.push(statusIdleEvent())
	fs.push(sessionIdleEvent())

	_, ch := startFakeRun(t, fs, "task-dedupe-001", t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "question")
	if ev.Text != "选 A 还是 B？" {
		t.Errorf("question 文本=%q，期望 \"选 A 还是 B？\"", ev.Text)
	}
	// 结构性抓回归：mapIdle 分类后无条件 clearTurn，若 session.idle 被误映射
	// 进 mapIdle，第二次触发只命中空回合 Warn、不产事件——事件通道断言在正确/
	// 错误实现下都通过（300ms select 形同虚设）。改断言缓冲中不出现「idle 但
	// 回合无文本」Warn：正确实现下 session.idle 走 Debug 跳过，永不进 mapIdle
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "idle 但回合无文本") {
			t.Fatal("session.idle 冗余信号不应触发空回合 Warn（被误映射进 mapIdle）")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 事件通道层断言保留：不重复产出分类事件
	select {
	case ev2 := <-ch:
		if ev2.Type == "question" || ev2.Type == "result" {
			t.Fatalf("session.idle 冗余信号不应重复触发分类，收到 %+v", ev2)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// TestStopReclaimsRun 验证 Stop 后运行态被注销：runs 表不随任务累积无界增长；
// 重复 Stop 幂等（返回「不在运行中」而不是 panic）。
func TestStopReclaimsRun(t *testing.T) {
	quietLog(t)
	taskID := "task-reclaim-01"
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	if ad.lookup(taskID) == nil {
		t.Fatal("启动后运行态应已登记")
	}
	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ad.lookup(taskID) != nil {
		t.Fatal("Stop 后运行态应已注销（runs 表不泄漏）")
	}
	if err := ad.Stop(taskID); err == nil {
		t.Fatal("重复 Stop 应返回「不在运行中」（幂等不 panic）")
	}
}

func TestStopRejectsPendingPermissions(t *testing.T) {
	quietLog(t)
	taskID := "task-stop-perm-01"
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	r := ad.lookup(taskID)
	if r == nil {
		t.Fatal("启动后运行态应已登记")
	}

	r.sessMu.Lock()
	r.permTiming["perm-stop-1"] = permissionTiming{part: "call-1", paused: true}
	r.permSession["perm-stop-1"] = "sess-1"
	r.sessMu.Unlock()

	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	fs.mu.Lock()
	calls := append([]permCall(nil), fs.permCalls...)
	fs.mu.Unlock()

	if len(calls) != 1 {
		t.Fatalf("期望收到 1 次权限拒绝调用，实际收到 %d 次", len(calls))
	}
	if calls[0].path != "/session/sess-1/permissions/perm-stop-1" {
		t.Errorf("权限路径不符: got %s", calls[0].path)
	}
	if !strings.Contains(calls[0].body, `"reject"`) {
		t.Errorf("期望拒绝权限，实际 body=%s", calls[0].body)
	}
}

// TestServeDeathEmitsFailed 验证 serve 死亡判定：
// 探活失败 + SSE 断流后，产出 result !OK（FailReason 含 stderr 尾部），
// 事件通道随后关闭（执行终结），运行态随订阅退出被注销。
func TestServeDeathEmitsFailed(t *testing.T) {
	quietLog(t)
	taskID := "task-death-001"
	fs := newFakeServer(t)

	ad := New(slog.Default())
	probe := &fakeProbe{alive: true}
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: t.TempDir(),
	}
	if err := os.WriteFile(filepath.Join(req.TaskDir, promptFileName), []byte("执行计划"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	if _, err := ad.startRun(context.Background(), req, NewAPI(fs.ts.URL, adapterTestPassword), probe); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	ch := ad.Events(taskID)

	// 模拟 serve 进程死亡：探活失败 + SSE 连接断流
	probe.setAlive(false)
	fs.ts.CloseClientConnections()

	ev := waitEventType(t, ch, "result")
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("期望 result !OK（serve 死亡），实际 %+v", ev.Result)
	}
	if !strings.Contains(ev.Result.FailReason, "serve 已退出") {
		t.Errorf("FailReason=%q，应含 serve 已退出", ev.Result.FailReason)
	}
	if !strings.Contains(ev.Result.FailReason, "fake stderr tail") {
		t.Errorf("FailReason=%q，应含 stderr 尾部", ev.Result.FailReason)
	}

	// 死亡后事件通道应关闭（执行终结，中介循环据此退出）
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("死亡后事件通道应关闭，仍收到 %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("死亡后事件通道未在 2s 内关闭")
	}

	// 运行态应被注销（subscribeLoop 退出时 drop）——轮询：close(evCh) 与 drop
	// 在同一 defer 内，通道关闭先于 drop，需等 drop 执行完
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ad.lookup(taskID) != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if ad.lookup(taskID) != nil {
		t.Fatal("serve 死亡后运行态应被注销（runs 表不泄漏）")
	}
}

// TestEventsClosedAfterStop 覆盖 P1-11：Stop 后（及从未启动的任务）Events
// 立即返回**已关闭**通道——range 立即结束，而不是 nil 通道让消费方
// （manager 中介循环）永久阻塞。「Dispatch → go mediate 调度窗口内 serve 死亡」
// 的缝隙场景下，运行态已注销而中介循环尚未开始，已关闭通道保证中介循环
// 立即退出、不泄漏 goroutine。
func TestEventsClosedAfterStop(t *testing.T) {
	quietLog(t)
	taskID := "task-events-stop"
	fs := newFakeServer(t)
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertClosed := func(desc string, ch <-chan executor.AdapterEvent) {
		t.Helper()
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("%s: 通道应已关闭（契约：通道关闭 = 执行终结）", desc)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: 通道未立即关闭", desc)
		}
	}
	assertClosed("Stop 后", ad.Events(taskID))
	assertClosed("从未启动的任务", ad.Events("task-never-started"))
}

// TestStopKillFailureRetainsRunState 覆盖 P1-9：Stop 的 kill 失败时运行态必须
// 保留——serve 可能还活着（占着端口与模型会话），drop 掉就没有任何途径回收，
// 保留后重试 Stop 是唯一回收路径。事件通道照常关闭（契约：通道关闭=执行终结，
// 消费方不阻塞），但运行态仍可 lookup；kill 恢复后重试 Stop 完成注销。
func TestStopKillFailureRetainsRunState(t *testing.T) {
	quietLog(t)
	taskID := "task-killfail-01"
	fs := newFakeServer(t)
	ad := New(slog.Default())
	probe := &fakeProbe{alive: true, killErr: errors.New("tmux 挂死")}
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: t.TempDir(),
	}
	if err := os.WriteFile(filepath.Join(req.TaskDir, promptFileName), []byte("执行计划"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	if _, err := ad.startRun(context.Background(), req, NewAPI(fs.ts.URL, adapterTestPassword), probe); err != nil {
		t.Fatalf("startRun: %v", err)
	}

	if err := ad.Stop(taskID); err == nil {
		t.Fatal("kill 失败时 Stop 应返回错误")
	}
	// 等订阅 goroutine 退出（事件通道关闭 = 订阅已终结），随后运行态必须仍在：
	// 回归点——subscribeLoop 的 defer 若无条件 drop，这里 lookup 会变 nil
	deadline := time.Now().Add(2 * time.Second)
	for {
		ch := ad.Events(taskID)
		select {
		case _, ok := <-ch:
			if !ok {
				goto subDone
			}
			time.Sleep(10 * time.Millisecond)
		case <-time.After(deadline.Sub(time.Now())):
			t.Fatal("事件通道未在 2s 内关闭")
		}
	}
subDone:
	if ad.lookup(taskID) == nil {
		t.Fatal("kill 失败后运行态应保留（供重试 Stop 回收孤儿 serve，P1-9）")
	}
	// 保留期间 Events 返回的仍是已关闭通道（执行已终结），消费方不阻塞
	assertClosed := func(desc string, ch <-chan executor.AdapterEvent) {
		t.Helper()
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("%s: 通道应已关闭（契约：通道关闭 = 执行终结）", desc)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: 通道未立即关闭", desc)
		}
	}
	assertClosed("kill 失败保留期间", ad.Events(taskID))
	// 保留态是惰性的：Send/RespondPermission 必须拒绝（订阅已退出，指令发出
	// 也没有事件回程，会静默挂死——宁可让协调者看到明确错误）
	if err := ad.Send(context.Background(), taskID, "继续"); err == nil {
		t.Fatal("保留态不应接受 Send 续接指令")
	}
	if err := ad.RespondPermission(context.Background(), taskID, "perm-x", "once", ""); err == nil {
		t.Fatal("保留态不应接受权限应答")
	}

	// kill 恢复后重试 Stop：注销完成，孤儿 serve 可回收
	probe.setKillErr(nil)
	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("kill 恢复后重试 Stop 应成功: %v", err)
	}
	if ad.lookup(taskID) != nil {
		t.Fatal("重试 Stop 成功后运行态应注销")
	}
}

// TestStopKillFailureServeDeadDrops 覆盖 P1-9 的边界：kill 失败但 serve 已自灭
// （探活为死）时 Stop 照常注销运行态——进程已死，无孤儿资源可留，保留反而是
// 无法回收的僵尸条目。
func TestStopKillFailureServeDeadDrops(t *testing.T) {
	quietLog(t)
	taskID := "task-killdead-01"
	fs := newFakeServer(t)
	ad := New(slog.Default())
	probe := &fakeProbe{alive: false, killErr: errors.New("tmux 会话已消失")}
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: t.TempDir(),
	}
	if err := os.WriteFile(filepath.Join(req.TaskDir, promptFileName), []byte("执行计划"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	if _, err := ad.startRun(context.Background(), req, NewAPI(fs.ts.URL, adapterTestPassword), probe); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	if err := ad.Stop(taskID); err != nil {
		t.Fatalf("serve 已死时 kill 失败不应阻断 Stop: %v", err)
	}
	if ad.lookup(taskID) != nil {
		t.Fatal("serve 已死时运行态应注销（无孤儿可留，保留即僵尸）")
	}
}

// TestReconnectWarnsLostPermission 覆盖 P1-10b 降级方案：SSE 断连恢复时 adapter
// 必须显式告警「断连间隙的权限请求可能丢失」——/event 无重放语义（fix-J spike
// 实测重连只收 server.connected/heartbeat），间隙内服务端产出的 permission.asked
// 永久丢失；又无「按会话拉取未决权限 id」的可用端点（GET /session/{id}/message
// 的 tool part 无权限 id，应答端点要求真实 id），只能告警留痕、人工兜底。
func TestReconnectWarnsLostPermission(t *testing.T) {
	buf := captureLog(t)
	taskID := "task-recon-0001"
	var mu sync.Mutex
	conns := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprint(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
				fl.Flush()
				return // 断流：连接 1 结束，触发重连
			}
			fl.Flush()           // 先送响应头：客户端收到 200 才认为连接建立（onReconnect 在此触发）
			<-r.Context().Done() // 连接 2 起保持
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	ad := New(slog.Default())
	// 清理顺序：先 Stop（runCancel 让订阅退出、客户端断开，handler 的
	// r.Context().Done() 放行）再关 server——倒过来 Close 会死等被保持的 handler
	t.Cleanup(func() {
		_ = ad.Stop(taskID)
		ts.CloseClientConnections()
		ts.Close()
	})
	probe := &fakeProbe{alive: true}
	req := executor.StartReq{
		Task:    proto.Task{ID: taskID, RepoPath: t.TempDir()},
		TaskDir: t.TempDir(),
	}
	if err := os.WriteFile(filepath.Join(req.TaskDir, promptFileName), []byte("执行计划"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	if _, err := ad.startRun(context.Background(), req,
		NewAPIWithSSEBackoff(ts.URL, adapterTestPassword, 100*time.Millisecond, 500*time.Millisecond), probe); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	t.Cleanup(func() { _ = ad.Stop(taskID) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "断连间隙") {
			if !strings.Contains(buf.String(), taskID) {
				t.Fatal("断连间隙 Warn 应带任务上下文")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("断连恢复后未出现「断连间隙权限可能丢失」Warn 日志（P1-10b 降级方案）")
}

// probeLevelRecorder 是测试用 slog.Handler：记录「探活降频/探活回到高频」两条探活
// 档位切换日志的级别，供断言两条对称（修复 6）。
type probeLevelRecorder struct {
	mu     sync.Mutex
	levels map[string]slog.Level
}

func (r *probeLevelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *probeLevelRecorder) Handle(_ context.Context, rec slog.Record) error {
	if rec.Message == "探活降频：任务静默，探活间隔升到慢档" ||
		rec.Message == "探活回到高频：收到新事件，任务活跃" {
		r.mu.Lock()
		r.levels[rec.Message] = rec.Level
		r.mu.Unlock()
	}
	return nil
}

func (r *probeLevelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *probeLevelRecorder) WithGroup(string) slog.Handler      { return r }

// TestWatchdogProbeLevelSwitchSymmetric 覆盖修复 6：探活档位切换的两条日志级别必须
// 对称（都是 Debug）。降频打 Info、回高频打 Debug 时，任务正常干活两档来回切会在
// 日志里刷出一串「探活降频」而看不到对应的回高频——既是噪音又误导成「任务卡住」。
func TestWatchdogProbeLevelSwitchSymmetric(t *testing.T) {
	rec := &probeLevelRecorder{levels: map[string]slog.Level{}}
	a := New(slog.New(rec))
	probe := &fakeProbe{alive: true}
	r := &runState{taskID: "task-wd-0003", handle: probe}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	r.stopCh = make(chan struct{})
	t.Cleanup(func() { r.runCancel() })
	cfg := watchdogConfig{fastInterval: 10 * time.Millisecond,
		slowInterval: 50 * time.Millisecond, fastProbes: 1}
	go a.watchdogWithConfig(r, cfg)

	waitLevel := func(msg string) slog.Level {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			rec.mu.Lock()
			lv, ok := rec.levels[msg]
			rec.mu.Unlock()
			if ok {
				return lv
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("未出现探活日志 %q", msg)
		return 0
	}
	if lv := waitLevel("探活降频：任务静默，探活间隔升到慢档"); lv != slog.LevelDebug {
		t.Fatalf("探活降频日志级别 = %s, want Debug（与回高频对称）", lv)
	}
	// 打点模拟新事件，随后应出现回高频日志，级别同样必须是 Debug
	r.lastEventAt.Store(time.Now().UnixNano())
	if lv := waitLevel("探活回到高频：收到新事件，任务活跃"); lv != slog.LevelDebug {
		t.Fatalf("探活回到高频日志级别 = %s, want Debug", lv)
	}
}

// TestWatchdogBacksOffWhenStable 覆盖 P1-17 探活降频：任务稳定（探活连续成功且
// 无新事件）后探活间隔从 fast 升到 slow——不再每 200ms 一次 tmux fork + HTTP
// 请求（waiting_review 挂过夜 = 每天每任务约 43 万次 fork）；出现失败立即回到
// 高频，连续失败达阈值即判死。时间敏感断言用注入间隔（fast=20ms/slow=300ms/
// 阈值 3 次），避免依赖真实 200ms/2s 的慢节奏。
func TestWatchdogBacksOffWhenStable(t *testing.T) {
	quietLog(t)
	a := New(slog.Default())
	probe := &fakeProbe{alive: true}
	r := &runState{taskID: "task-wd-0001", handle: probe}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	r.stopCh = make(chan struct{})
	cfg := watchdogConfig{fastInterval: 20 * time.Millisecond,
		slowInterval: 300 * time.Millisecond, fastProbes: 3}
	go a.watchdogWithConfig(r, cfg)

	// 稳定期观察：前 fastProbes 次高频（~60ms）后应出现 ≥200ms 的慢间隔
	deadline := time.Now().Add(1 * time.Second)
	maxGap := time.Duration(0)
	for time.Now().Before(deadline) {
		ts := probe.times()
		for i := 1; i < len(ts); i++ {
			if g := ts[i].Sub(ts[i-1]); g > maxGap {
				maxGap = g
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if maxGap < 200*time.Millisecond {
		t.Errorf("稳定期应出现慢探活间隔（≥200ms），实测最大间隔 %v", maxGap)
	}

	// serve 死亡：回到高频，连续 3 次失败（~60ms）后取消运行 ctx
	probe.setAlive(false)
	select {
	case <-r.runCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("serve 死亡后看门狗未在 2s 内取消运行 ctx（降频后必须能及时判死）")
	}
}

// TestWatchdogEventResetsToFast 覆盖 P1-17 的事件复位：降频稳定后再产出事件
// （任务重新活跃，emit 打点 lastEventAt），探活间隔应回落 fast 级——活跃任务
// 保持快速死亡检测。
func TestWatchdogEventResetsToFast(t *testing.T) {
	quietLog(t)
	a := New(slog.Default())
	probe := &fakeProbe{alive: true}
	r := &runState{taskID: "task-wd-0002", handle: probe}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	r.stopCh = make(chan struct{})
	t.Cleanup(func() { r.runCancel() })
	cfg := watchdogConfig{fastInterval: 20 * time.Millisecond,
		slowInterval: 300 * time.Millisecond, fastProbes: 3}
	go a.watchdogWithConfig(r, cfg)

	// 先确认已降频（出现慢间隔）
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		ts := probe.times()
		for i := 1; i < len(ts); i++ {
			if ts[i].Sub(ts[i-1]) >= 200*time.Millisecond {
				goto slowed
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("未观察到慢探活间隔（降频未生效）")
slowed:
	// 模拟新事件（emit 的打点）：随后应出现 fast 级的间隔（≤100ms）
	r.lastEventAt.Store(time.Now().UnixNano())
	bumpAt := time.Now()
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		ts := probe.times()
		for i := 1; i < len(ts); i++ {
			if ts[i].Before(bumpAt) {
				continue // 只统计打点之后的探活间隔
			}
			if g := ts[i].Sub(ts[i-1]); g <= 100*time.Millisecond {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("新事件后探活未回到高频间隔（≤100ms），事件复位未生效")
}

// TestSessionIsolationUsesPropertiesSessionID 验证会话隔离从 properties.sessionID
// 提取（真实事件 sessionID 在 properties 而非顶层，spike 实测）——其他会话的
// 事件必须被跳过，不产生任何 AdapterEvent。
func TestSessionIsolationUsesPropertiesSessionID(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	// 顶层无 sessionID（真实样本形态），properties.sessionID 指向其他会话
	fs.push(sseLine(map[string]any{
		"type": "permission.asked",
		"properties": map[string]any{
			"id": "per_other", "sessionID": "sess-OTHER",
			"permission": "bash", "patterns": []string{"echo x"},
			"metadata": map[string]any{"command": "echo x"},
			"tool":     map[string]any{"messageID": "msg-9", "callID": "call-9"},
		},
	}))

	_, ch := startFakeRun(t, fs, "task-iso-0001", t.TempDir(), t.TempDir())
	// 跳过「会话就绪」progress 噪音，等待非 progress 事件
	for {
		select {
		case ev := <-ch:
			if ev.Type == "progress" || isTimingEvent(ev) {
				continue
			}
			t.Fatalf("其他会话的 permission 事件未被隔离: %+v", ev)
		case <-time.After(300 * time.Millisecond):
			return // 期望：无 permission 事件（隔离生效）
		}
	}
}

// TestPermissionEventCarriesFullText 验证 adapter 不再在 200 字处截断权限描述，
// 只保留 64KB 的防失控硬上限。
// 注：本文件是 package opencode（内部测试），直接断言内部截断规则，无需导出缝。
func TestPermissionEventCarriesFullText(t *testing.T) {
	long := strings.Repeat("a", 1000)
	got := turn.TruncateMarked(long, permTextHardLimit)
	if got != long {
		t.Fatalf("1000 字的权限描述必须原样上传，实得 %d 字符", len([]rune(got)))
	}
	huge := strings.Repeat("b", 70000)
	got = turn.TruncateMarked(huge, permTextHardLimit)
	if !strings.HasSuffix(got, executor.TruncationMarker) {
		t.Error("超 64KB 硬上限时仍必须带截断标记（审批链据此 fail-closed）")
	}
}

// newAdapterWithRunForTest 造一个带空运行态的 adapter，供权限结构化提取的
// 纯映射断言（不起 serve、不连网络）。
func newAdapterWithRunForTest(t *testing.T) (*Adapter, *runState) {
	t.Helper()
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := &runState{
		taskID:        "t1",
		evCh:          make(chan executor.AdapterEvent, 8),
		stopCh:        make(chan struct{}),
		permText:      make(map[string]string),
		childSessions: make(map[string]string),
		permSession:   make(map[string]string),
	}
	r.runCtx, r.runCancel = context.WithCancel(context.Background())
	t.Cleanup(r.runCancel)
	a.runs["t1"] = r
	return a, r
}

// EventsForTest 暴露事件通道（供断言 mapPermissionAsked 的产出）。
func (r *runState) EventsForTest() <-chan executor.AdapterEvent { return r.evCh }

// TestPermissionAskedCarriesStructure 用真机取样的 permission.asked 载荷
// 断言结构提取。
//
// 主用例是 perm_external_directory_file.json 而不是 perm_edit.json：生产配置
// 下 edit 是 allow，工作树内的编辑根本不产生事件，真正会到达 handoff 的文件
// 类事件就是这个 external_directory（Task 1 探针 §3.1 实测）。perm_edit.json
// 作为次要用例保留，防止将来有人把 edit 翻成 ask 时提取路径的代码不在位。
func TestPermissionAskedCarriesStructure(t *testing.T) {
	for _, f := range []string{"perm_external_directory_file.json", "perm_edit.json"} {
		t.Run(f, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", f))
			if err != nil {
				t.Fatalf("读真机载荷样本: %v", err)
			}
			a, r := newAdapterWithRunForTest(t)
			a.mapPermissionAsked(r, raw)
			ev := <-r.EventsForTest()
			if ev.Type != "permission" {
				t.Fatalf("应产出 permission 事件，实得 %q", ev.Type)
			}
			if ev.Perm == nil {
				t.Fatal("真机文件类载荷必须能提取出结构")
			}
			if len(ev.Perm.Paths) != 1 || !filepath.IsAbs(ev.Perm.Paths[0]) {
				t.Fatalf("路径 = %v，期望恰好一个绝对路径（取自 metadata.filepath，"+
					"不是 patterns——后者是相对/通配摘要）", ev.Perm.Paths)
			}
		})
	}
}

// TestPermissionAskedExternalDirBash external_directory 的 bash 形态没有
// filepath，路径在 metadata.directories，可能多项。
func TestPermissionAskedExternalDirBash(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "perm_external_directory_bash.json"))
	if err != nil {
		t.Fatalf("读真机载荷样本: %v", err)
	}
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, raw)
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Command == "" {
		t.Fatalf("bash 形态必须带命令原文，实得 %+v", ev.Perm)
	}
	if len(ev.Perm.Paths) == 0 {
		t.Fatal("bash 形态的越界目录必须进 Paths，否则 permgate 判不出越界")
	}
}

// TestPermissionAskedBashCarriesCommand bash 请求带完整命令。
func TestPermissionAskedBashCarriesCommand(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	props := []byte(`{"id":"p1","permission":"bash","metadata":{"command":"go build ./..."}}`)
	a.mapPermissionAsked(r, props)
	ev := <-r.EventsForTest()
	if ev.Perm == nil || ev.Perm.Tool != executor.PermToolBash {
		t.Fatalf("bash 请求应归一化为 bash，实得 %+v", ev.Perm)
	}
	if ev.Perm.Command != "go build ./..." {
		t.Fatalf("command = %q，期望完整原文", ev.Perm.Command)
	}
}

// TestPermissionAskedNilPermWhenNoStructure 无可用字段时 Perm 为 nil。
func TestPermissionAskedNilPermWhenNoStructure(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	a.mapPermissionAsked(r, []byte(`{"id":"p1"}`))
	ev := <-r.EventsForTest()
	if ev.Perm != nil {
		t.Fatalf("无可用字段时必须为 nil，实得 %+v", ev.Perm)
	}
}

// quietLogger 返回丢弃所有输出的 logger（与包内 quietLog(t) 同目的，
// 供不依赖 t 的纯函数级用例直接构造）。
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestMapIdleEmptyTurnEmitsFailedResult 空回合必须产出失败结果而不是静默。
//
// 现场（B21）：opencode 连做 7 个回合工具调用后，最后一步 step-finish 的
// reason=unknown、tokens 全 0（供应商流中断），会话随即 idle、零文本。旧实现
// 只打一条 WARN 就 return，任务停在 running 静止 1 小时，协调者要等 2h 看门狗。
func TestMapIdleEmptyTurnEmitsFailedResult(t *testing.T) {
	a := New(quietLogger())
	r := a.newRun("t1", t.TempDir(), t.TempDir())
	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" {
			t.Fatalf("空回合应产出 result，实际 %s", ev.Type)
		}
		if ev.Result == nil || ev.Result.OK {
			t.Fatalf("空回合应是失败结果: %+v", ev.Result)
		}
		if ev.Result.FailReason == "" {
			t.Fatalf("FailReason 必须写清现场，否则协调者不知道发生了什么")
		}
	default:
		t.Fatalf("空回合静默（无事件产出）——这正是 B21 的缺陷")
	}
}

// TestMapIdleRejectedTurnEmitsDeniedResult（B383 Wave 0 改写，旧名
// TestMapIdleRejectedTurnStillAsks 期望 question）——被拒终止的空回合必须产出
// 带 VoidReasonPermissionDenied 的失败结果，不再合成 question（那会让 manager
// 再建一张 ask 工单；被拒终局沿 handleResult 作废挂起工单、落 waiting_review，
// 协调者用 continue 续接）。
func TestMapIdleRejectedTurnEmitsDeniedResult(t *testing.T) {
	a := New(quietLogger())
	r := a.newRun("t1", t.TempDir(), t.TempDir())
	r.permText["perm-1"] = "bash: git push"
	r.markRejectPending("perm-1")
	r.settleReject("perm-1", true) // API 成功结算：标记保留为「已确认已拒」
	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" {
			t.Fatalf("被拒终止的空回合应产出 result，实际 %s", ev.Type)
		}
		if ev.Result == nil || ev.Result.OK {
			t.Fatalf("应是失败结果: %+v", ev.Result)
		}
		if ev.Result.VoidReason != executor.VoidReasonPermissionDenied {
			t.Fatalf("VoidReason=%q，期望 %q", ev.Result.VoidReason, executor.VoidReasonPermissionDenied)
		}
		if !strings.Contains(ev.Result.FailReason, "bash: git push") {
			t.Fatalf("FailReason 应含被拒描述: %q", ev.Result.FailReason)
		}
	default:
		t.Fatalf("被拒终止的空回合应产出事件")
	}
}

// TestTextTurnAfterRejectClassifiesTrailerAndClearsMarkers（B383 Wave 0 T5）：
// 拒绝标记在场时父回合产出了文本 → 走正常 trailer 分类（不冒充被拒终局），
// 回合终结时标记随 clearTurn 清掉，下一回合零泄漏。
func TestTextTurnAfterRejectClassifiesTrailerAndClearsMarkers(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-reject-t5"
	fs.push(permissionAskedEvent("perm-t5", "bash", "echo t5"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	waitEventType(t, ch, "permission")
	if err := ad.RespondPermission(context.Background(), taskID, "perm-t5", "reject", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	// 父回合后续产出文本（模型换方式继续干）：标记在场但回合有正文
	fs.push(userMsgEvent("msg-t5-u1"))
	fs.push(partUpdatedEvent("msg-t5-a1", "prt-t5-a1", "继续用别的方式\n{\"ask\":\"改用脚本方式可以吗？\"}"))
	fs.push(statusIdleEvent())

	got := waitEventType(t, ch, "question")
	if !strings.Contains(got.Text, "改用脚本方式可以吗？") {
		t.Fatalf("有文本时应走正常 trailer 分类，实得 %+v", got)
	}
	// 标记已随回合终结清掉（白盒）
	r := ad.lookup(taskID)
	r.turnMu.Lock()
	if len(r.turnRejected) != 0 || r.rejectInFlight != 0 {
		r.turnMu.Unlock()
		t.Fatalf("回合终结应清拒绝标记与计数器: map=%v inFlight=%d", r.turnRejected, r.rejectInFlight)
	}
	r.turnMu.Unlock()
	// 下一回合零泄漏：正常 finish 分类不受残留标记影响
	fs.push(userMsgEvent("msg-t5-u2"))
	fs.push(partUpdatedEvent("msg-t5-a2", "prt-t5-a2",
		"干完了\n{\"branch\":\"handoff/T1\",\"commit\":\"abc12345\",\"summary\":\"完成功能\"}"))
	fs.push(statusIdleEvent())
	fin := waitEventType(t, ch, "result")
	if fin.Result == nil || !fin.Result.OK {
		t.Fatalf("下一回合应正常完成分类，实得 %+v", fin)
	}
	if fin.Result.VoidReason == executor.VoidReasonPermissionDenied {
		t.Fatalf("下一回合不得带被拒归因: %+v", fin.Result)
	}
}

// TestClearTurnClearsRejectionMarkers（B383 Wave 0 T7）：clearTurn 三清——
// 拒绝标记表置空、在飞计数归零、重武装代次复位；下一回合零泄漏（空文本 idle
// 走 B21 而不是被拒 result）。
func TestClearTurnClearsRejectionMarkers(t *testing.T) {
	a := New(quietLogger())
	r := a.newRun("t7", t.TempDir(), t.TempDir())
	r.permText["perm-t7"] = "bash: echo t7"
	r.markRejectPending("perm-t7")
	r.settleReject("perm-t7", true)
	r.turnMu.Lock()
	if len(r.turnRejected) != 1 || r.rejectInFlight != 0 {
		r.turnMu.Unlock()
		t.Fatalf("结算后应恰留一条已确认标记且计数归零: map=%v inFlight=%d",
			r.turnRejected, r.rejectInFlight)
	}
	r.turnMu.Unlock()

	r.clearTurn()
	r.turnMu.Lock()
	dirty := len(r.turnRejected) != 0 || r.rejectInFlight != 0 || r.rejectRearmGen != 0
	r.turnMu.Unlock()
	if dirty {
		t.Fatalf("clearTurn 必须清拒绝标记、归零计数器并复位重武装代次")
	}

	// 下一回合零泄漏：空文本 idle 走 B21，绝不按被拒归因
	a.mapIdle(r, json.RawMessage(`{"type":"session.idle"}`))
	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
			t.Fatalf("空回合应产出失败结果（B21）: %+v", ev)
		}
		if ev.Result.VoidReason == executor.VoidReasonPermissionDenied {
			t.Fatalf("标记已清，不得按被拒归因: %+v", ev.Result)
		}
	default:
		t.Fatal("空回合静默")
	}
}

// TestRejectInFlightRearmDefensiveFallback（B383 Wave 0 防御分支）：重武装到期
// 仍不可判（应答仍未结算——生产上不可达：应答截止 30s < 宽限 31s）→ 按 B21
// 零文本收口 + 诊断文案，且至多重武装一次、绝不循环武装。
func TestRejectInFlightRearmDefensiveFallback(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-reject-def"
	fs.push(permissionAskedEvent("perm-def", "bash", "echo def"))
	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ad.rejectSettleGrace = 60 * time.Millisecond // 宽限 < 应答截止（注入 300ms）：到期时必仍在飞
	ad.respondTimeout = 300 * time.Millisecond
	waitEventType(t, ch, "permission")

	fs.holdPermissionResponses()
	t.Cleanup(fs.releasePermissionResponses)
	done := make(chan error, 1)
	go func() { done <- ad.RespondPermission(context.Background(), taskID, "perm-def", "reject", "") }()
	waitPermCalls(t, fs, 1)

	fs.push(statusIdleEvent())
	got := waitEventType(t, ch, "result")
	if got.Result == nil || got.Result.OK {
		t.Fatalf("防御分支应按零文本失败收口，实际 %+v", got)
	}
	if got.Result.VoidReason == executor.VoidReasonPermissionDenied {
		t.Fatalf("应答未结算时不得按被拒归因: %+v", got.Result)
	}
	if !strings.Contains(got.Result.FailReason, "仍未结算") {
		t.Fatalf("防御分支应带诊断文案点明现场: %q", got.Result.FailReason)
	}
	// 恰一次：防御收口后不得循环重武装再发第二条
	fs.push(sessionIdleEvent())
	assertNoResultWithin(t, ch, 200*time.Millisecond)
	// 硬截止先到（hold 未放行）：应答转发按超时失败结算；放行只为了归还
	// handler goroutine，此时客户端早已断开
	if err := <-done; err == nil {
		t.Fatal("应答转发最终应因超时失败")
	}
	fs.releasePermissionResponses()
}

// TestChildSessionPermissionProducesTicket 覆盖 B52 的核心修复：子会话发来的
// permission.asked 必须认亲成功并产出工单（修复前被 acceptForeign 丢弃，任务挂死）。
func TestChildSessionPermissionProducesTicket(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0001"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "Run probe curl command (@general subagent)")
	fs.push(permissionAskedEventFrom("sess-child", "perm-child-1", "bash", "curl https://example.com"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-child-1" {
		t.Errorf("PermissionID=%q，期望 perm-child-1", ev.PermissionID)
	}
	if !strings.Contains(ev.Text, "curl https://example.com") {
		t.Errorf("权限描述=%q，应含命令文本", ev.Text)
	}
}

// TestChildSessionOwnershipCached 认亲结果必须缓存：同一子会话连发两条审批，
// GET /session/{id} 只能被调一次，否则每条事件都在热路径上做一次网络 I/O。
func TestChildSessionOwnershipCached(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0002"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.push(permissionAskedEventFrom("sess-child", "perm-a", "bash", "echo a"))
	fs.push(permissionAskedEventFrom("sess-child", "perm-b", "bash", "echo b"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	waitEventType(t, ch, "permission")
	waitEventType(t, ch, "permission")
	if n := fs.sessionGetCount("sess-child"); n != 1 {
		t.Fatalf("GET /session/sess-child 调用 %d 次，期望 1 次（认亲结果应缓存）", n)
	}
}

// TestOwnershipFailureEmitsProgress 认亲失败必须不静默：无工单，但要有一条
// progress 事件让协调者在 handoff show 的事件历史里看得见。
func TestOwnershipFailureEmitsProgress(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0003"
	fs := newFakeServer(t)
	// 不登记该子会话 → GET /session 返回 404
	fs.push(permissionAskedEventFrom("sess-unknown", "perm-x", "bash", "echo x"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	deadline := time.After(5 * time.Second)
	sawProgress := false
	for !sawProgress {
		select {
		case ev := <-ch:
			if ev.Type == "permission" {
				t.Fatalf("认亲失败却产出了工单: %+v", ev)
			}
			if ev.Type == "progress" && strings.Contains(ev.Text, "sess-unknown") {
				sawProgress = true
			}
		case <-deadline:
			t.Fatal("等待认亲失败的 progress 事件超时")
		}
	}
}

// TestOwnershipNegativeResultNotCached 认亲失败不得入缓存：首次 500、二次正常，
// 第二条审批必须能成功产出工单。缓存了负结果，一次网络抖动就把这个子会话永久拉黑。
func TestOwnershipNegativeResultNotCached(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0004"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.failNextSessionGet("sess-child", http.StatusInternalServerError)
	fs.push(permissionAskedEventFrom("sess-child", "perm-fail", "bash", "echo first"))
	fs.push(permissionAskedEventFrom("sess-child", "perm-ok", "bash", "echo second"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-ok" {
		t.Fatalf("PermissionID=%q，期望 perm-ok（首条因 500 丢弃，第二条应重新认亲成功）", ev.PermissionID)
	}
	if n := fs.sessionGetCount("sess-child"); n != 2 {
		t.Fatalf("GET /session/sess-child 调用 %d 次，期望 2 次（负结果不得缓存）", n)
	}
}

// TestChildPermissionTextCarriesSubagentPrefix 工单文案必须让协调者一眼看出
// 这条审批来自子 agent——子 agent 的越权和主 agent 的越权含义完全不同。
func TestChildPermissionTextCarriesSubagentPrefix(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0005"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "Run probe curl command (@general subagent)")
	fs.push(permissionAskedEventFrom("sess-child", "perm-p", "bash", "curl https://example.com"))

	_, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	want := "[子 agent: Run probe curl command (@general subagent)] "
	if !strings.HasPrefix(ev.Text, want) {
		t.Fatalf("权限描述=%q，应以 %q 开头", ev.Text, want)
	}
	if !strings.Contains(ev.Text, "curl https://example.com") {
		t.Errorf("权限描述=%q，前缀之后应保留原描述", ev.Text)
	}
}

// TestRespondPermissionRoutesToChildSession 应答必须发回请求所在的子会话。
// 发给父会话 opencode 不认，协调者的批准落不了地，任务照样挂死。
func TestRespondPermissionRoutesToChildSession(t *testing.T) {
	quietLog(t)
	taskID := "task-child-0006"
	fs := newFakeServer(t)
	fs.addChild("sess-child", "sess-1", "子任务")
	fs.push(permissionAskedEventFrom("sess-child", "perm-r", "bash", "echo r"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	waitEventType(t, ch, "permission")
	if err := ad.RespondPermission(context.Background(), taskID, "perm-r", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	calls := fs.perms()
	if len(calls) != 1 {
		t.Fatalf("权限应答请求 %d 次，期望 1 次", len(calls))
	}
	want := "/session/sess-child/permissions/perm-r"
	if calls[0].path != want {
		t.Fatalf("应答 path=%q，期望 %q（必须发往子会话，不是父会话）", calls[0].path, want)
	}
}

func TestOpenCodeNilApprovalIsCapabilityError(t *testing.T) {
	a := New(slog.Default())
	err := a.Start(context.Background(), executor.StartReq{
		Task:     proto.Task{ID: "t-nil"},
		TaskDir:  t.TempDir(),
		Approval: nil,
	})
	if err == nil {
		t.Fatal("Start with nil Approval must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "不是") && !strings.Contains(msg, "免审") && !strings.Contains(msg, "Approval") {
		t.Fatalf("unexpected error message: %v", msg)
	}
}

type fakeApprovalClient struct {
	mu       sync.Mutex
	reqs     []executor.ApprovalRequest
	acks     []executor.ApprovalAck
	failures []deliveryFailure
	decision executor.ApprovalDecision
}

type deliveryFailure struct {
	taskID   string
	ticketID string
	cause    error
}

func (f *fakeApprovalClient) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	return executor.PolicySnapshot{Version: "v1"}, nil
}

func (f *fakeApprovalClient) setDecision(dec executor.ApprovalDecision) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decision = dec
}

func (f *fakeApprovalClient) Request(_ context.Context, req executor.ApprovalRequest) (executor.ApprovalResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	dec := f.decision
	if dec.Status == "" {
		dec = executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "approver"}
	}
	return executor.ApprovalResult{
		Ref:      executor.ApprovalRef{ID: "ref-1"},
		Decision: dec,
	}, nil
}
func (f *fakeApprovalClient) Await(context.Context, executor.ApprovalRef) (executor.ApprovalDecision, error) {
	return executor.ApprovalDecision{Status: executor.ApprovalAllow}, nil
}
func (f *fakeApprovalClient) recordAck(ack executor.ApprovalAck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acks = append(f.acks, ack)
}

func (f *fakeApprovalClient) Acknowledge(_ context.Context, ack executor.ApprovalAck) error {
	f.recordAck(ack)
	return nil
}

// NoteDeliveryFailed 实现 adapter 的可选回调缝；生产实现由 Manager 注入的
// 生产 approval.Client 提供，测试仅记录参数以验证 adapter 没有自行写 store。
func (f *fakeApprovalClient) NoteDeliveryFailed(taskID, ticketID string, cause error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, deliveryFailure{taskID: taskID, ticketID: ticketID, cause: cause})
	return
}

func (f *fakeApprovalClient) snapshot() (reqs []executor.ApprovalRequest, acks []executor.ApprovalAck, failures []deliveryFailure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]executor.ApprovalRequest(nil), f.reqs...),
		append([]executor.ApprovalAck(nil), f.acks...),
		append([]deliveryFailure(nil), f.failures...)
}

func TestMapPermissionAskedWithApprovalClientDoesNotEmitPermission(t *testing.T) {
	a, r := newAdapterWithRunForTest(t)
	fakeClient := &fakeApprovalClient{}
	r.approval = fakeClient
	props := []byte(`{"id":"p1","permission":"bash","metadata":{"command":"ls"}}`)
	a.mapPermissionAsked(r, props)
	select {
	case ev := <-r.EventsForTest():
		t.Fatalf("ApprovalClient 存在时不得向事件通道 emit permission，实得 %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// 正常不产出
	}
}

// TestAuthorizeNativePermissionReportsDeliveryFailure exercises the real
// permission.asked → Authorize → RespondPermission path. The reporter is the
// existing injected approval object; the adapter must not write manager/store
// state itself and must not acknowledge delivery after the HTTP failure.
func TestAuthorizeNativePermissionReportsDeliveryFailure(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.failPermissionResponses(http.StatusBadGateway)
	taskID := "task-permission-delivery-failed"
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	r := ad.lookup(taskID)
	approval := &fakeApprovalClient{}
	r.approval = approval

	fs.push(permissionAskedEvent("perm-delivery-failed", "bash", "echo fail"))
	deadline := time.After(2 * time.Second)
	for {
		_, _, failures := approval.snapshot()
		if len(failures) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("权限审批与回传失败回调超时")
		case <-time.After(10 * time.Millisecond):
		}
	}

	reqs, acks, failures := approval.snapshot()
	if len(reqs) != 1 || reqs[0].NativeID != "perm-delivery-failed" {
		t.Fatalf("审批 client 未消费当前 permission: %+v", reqs)
	}
	calls := fs.perms()
	if len(calls) != 1 || calls[0].path != "/session/sess-1/permissions/perm-delivery-failed" {
		t.Fatalf("RespondPermission 调用=%+v", calls)
	}
	if len(failures) != 1 || failures[0].taskID != taskID || failures[0].ticketID != taskID+":perm-delivery-failed" {
		t.Fatalf("delivery failure 回调=%+v", failures)
	}
	if !strings.Contains(failures[0].cause.Error(), "502") {
		t.Fatalf("delivery failure 原因未保留 HTTP 错误: %v", failures[0].cause)
	}
	for _, ack := range acks {
		if ack.Stage == executor.AckDelivered || ack.Stage == executor.AckExecuted {
			t.Fatalf("投递失败后不得 AckDelivered/AckExecuted: %+v", acks)
		}
	}
}

func ackStagePresent(acks []executor.ApprovalAck, stage executor.AckStage) bool {
	for _, ack := range acks {
		if ack.Stage == stage {
			return true
		}
	}
	return false
}

func TestAuthorizeNativePermissionAllowDeliversOnce(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	taskID := "task-permission-allow-once"
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	r := ad.lookup(taskID)
	approval := &fakeApprovalClient{}
	approval.setDecision(executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "approver"})
	r.approval = approval

	fs.push(permissionAskedEvent("perm-allow-once", "bash", "echo once"))
	deadline := time.After(2 * time.Second)
	for {
		_, acks, _ := approval.snapshot()
		if ackStagePresent(acks, executor.AckDelivered) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("等待 AckDelivered 超时")
		case <-time.After(10 * time.Millisecond):
		}
	}
	calls := fs.perms()
	if len(calls) != 1 {
		t.Fatalf("一次权限请求应恰发 1 次原生应答，实得 %d", len(calls))
	}
	if !strings.Contains(calls[0].body, `"response":"once"`) {
		t.Fatalf("应答体=%q，应含 once", calls[0].body)
	}
	_, _, failures := approval.snapshot()
	if len(failures) != 0 {
		t.Fatalf("成功路径不得上报投递失败: %+v", failures)
	}
}

// 缝 #11：免审 AutoAllow（无工单）回传失败不得上报 NoteDeliveryFailed。
// 反向变异：去掉 decide 闸门无条件上报 → failures 非空，测试红。
func TestAuthorizeNativePermissionAutoAllowFailureDoesNotReport(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	fs.failPermissionResponses(http.StatusBadGateway)
	taskID := "task-permission-autoallow-fail"
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	r := ad.lookup(taskID)
	approval := &fakeApprovalClient{}
	approval.setDecision(executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "safe-command"})
	r.approval = approval

	fs.push(permissionAskedEvent("perm-autoallow-fail", "bash", "go test ./..."))
	deadline := time.After(2 * time.Second)
	for len(fs.perms()) == 0 {
		select {
		case <-deadline:
			t.Fatal("等待原生应答尝试超时")
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)
	_, acks, failures := approval.snapshot()
	if len(failures) != 0 {
		t.Fatalf("免审 AutoAllow 无工单，失败不得上报 delivery_failed: %+v", failures)
	}
	if ackStagePresent(acks, executor.AckDelivered) || ackStagePresent(acks, executor.AckExecuted) {
		t.Fatalf("投递失败后不得 AckDelivered/AckExecuted: %+v", acks)
	}
}

// TestApplySnapshotDefaultFailClosed 验证生产路径无 probe 时 fail-closed 报能力不足（Major 2 item 3）
func TestApplySnapshotDefaultFailClosed(t *testing.T) {
	fs := newFakeServer(t)
	taskID := "task-snap-failclosed"
	taskDir := t.TempDir()
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), taskDir)

	snap := executor.PolicySnapshot{
		Version: "v1-test",
		TaskID:  taskID,
		Scope:   executor.ApprovalScope{Workdir: t.TempDir(), TaskDir: taskDir},
	}

	err := ad.ApplySnapshot(context.Background(), taskID, snap)
	if err == nil {
		t.Fatal("生产路径默认无 probe 必须报能力不足，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "未重载") {
		t.Fatalf("错误信息必须指出未重载能力不足，实际: %v", err)
	}
}

// TestApplySnapshotInjectedProbeSuccess 读取真实写入的配置文件作为 observable
// probe，验证 ApplySnapshot 成功且配置落盘；这不是跨机真实引擎 ready 证据。
func TestApplySnapshotInjectedProbeSuccess(t *testing.T) {
	fs := newFakeServer(t)
	taskID := "task-snap-probe-ok"
	taskDir := t.TempDir()
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), taskDir)

	r := ad.lookup(taskID)
	if r == nil {
		t.Fatal("lookup task 失败")
	}
	r.reloadProbe = func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		cfgPath := filepath.Join(taskDir, configFileName)
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return false, fmt.Errorf("读取 observable 配置 %s: %w", cfgPath, err)
		}
		var cfg opencodeConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return false, fmt.Errorf("解析 observable 配置 %s: %w", cfgPath, err)
		}
		if cfg.Permission.ExternalDirectory != "ask" || cfg.Permission.Bash["*"] != "ask" {
			return false, fmt.Errorf("observable 配置权限状态不符: external_directory=%q bash_default=%q",
				cfg.Permission.ExternalDirectory, cfg.Permission.Bash["*"])
		}
		return true, nil
	}

	snap := executor.PolicySnapshot{
		Version: "v2-probed",
		TaskID:  taskID,
		Scope:   executor.ApprovalScope{Workdir: t.TempDir(), TaskDir: taskDir},
	}

	if err := ad.ApplySnapshot(context.Background(), taskID, snap); err != nil {
		t.Fatalf("注入 probe=true 时 ApplySnapshot 必须成功: %v", err)
	}

	cfgPath := filepath.Join(taskDir, configFileName)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("读取新快照配置文件 %s: %v", cfgPath, err)
	}
	var cfg opencodeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("解析新快照配置文件 %s: %v", cfgPath, err)
	}
	if cfg.Permission.ExternalDirectory != "ask" || cfg.Permission.Bash["*"] != "ask" {
		t.Fatalf("配置内容不正确: %s", string(data))
	}
}

// TestAdapterBindApproval 验证运行态任务成功重绑 ApprovalClient
func TestAdapterBindApproval(t *testing.T) {
	fs := newFakeServer(t)
	taskID := "task-bind-approval"
	ad, _ := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())

	fakeClient := &fakeApprovalClient{}
	if err := ad.BindApproval(taskID, fakeClient); err != nil {
		t.Fatalf("BindApproval 失败: %v", err)
	}

	r := ad.lookup(taskID)
	if r == nil || r.approval != fakeClient {
		t.Fatal("BindApproval 后 r.approval 必须更新为新 client")
	}
}
