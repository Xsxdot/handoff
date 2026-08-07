// adapter 测试：用 httptest 起可脚本化的 fake opencode server 驱动
// Start → SSE 事件映射 → AdapterEvent 产出 → Send/RespondPermission 转发 →
// serve 死亡判定的全链路验证，不依赖真实 opencode 二进制与 tmux。
//
// 本文件是包内测试（package opencode）：经未导出的 startRun 注入 httptest
// 服务器与假探活，绕开 StartServe 的 tmux 依赖（探活接口见 serveHandle）。
package opencode

import (
	"context"
	"encoding/json"
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

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

const adapterTestPassword = "test-password-123"

// quietLog 把测试期间的 slog.Default 换成丢弃 logger，保证测试输出干净。
func quietLog(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
}

// fakeProbe 是 serveHandle 的测试替身：alive 可变（模拟 serve 死亡），
// Kill/PaneTail 无操作。
type fakeProbe struct {
	mu    sync.Mutex
	alive bool
}

func (p *fakeProbe) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

func (p *fakeProbe) Kill() error { return nil }

func (p *fakeProbe) PaneTail() string { return "fake stderr tail" }

func (p *fakeProbe) setAlive(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alive = v
}

// permCall 记录 fake server 收到的一次权限应答请求。
type permCall struct {
	path string
	body string
}

// fakeServer 是可脚本化的 opencode 假服务端：按 push 顺序逐条推送 SSE 事件，
// 记录建会话/发 prompt/应答权限的请求，全程不依赖真实 opencode。
type fakeServer struct {
	ts           *httptest.Server
	mu           sync.Mutex
	sessionID    string
	promptBodies []string
	permCalls    []permCall
	lines        chan string
}

// newFakeServer 启动假服务端；SSE 事件经 push 注入（可先入队、连接后送达）。
func newFakeServer(t *testing.T) *fakeServer {
	fs := &fakeServer{sessionID: "sess-1", lines: make(chan string, 64)}
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
			fs.mu.Unlock()
			w.Write([]byte("true"))
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

// sseLine 把任意事件 JSON 包装成单条 SSE data 行（data: <json> + 空行成帧）。
func sseLine(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// msgEvent 构造一条 message.updated 事件（role=user/assistant，单个文本 part）。
func msgEvent(id, role, text string) string {
	return sseLine(map[string]any{
		"type":      "message.updated",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"id": id, "sessionID": "sess-1", "role": role,
			"parts": []map[string]any{{"type": "text", "text": text}},
		},
	})
}

// idleEvent 构造一条 session.idle 事件（回合结束信号）。
func idleEvent() string {
	return sseLine(map[string]any{
		"type":      "session.idle",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1", "status": "idle",
		},
	})
}

// permissionEvent 构造一条 permission.updated 事件（含 permissionID 与描述）。
func permissionEvent(id, desc string) string {
	return sseLine(map[string]any{
		"type":      "permission.updated",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"id": id, "sessionID": "sess-1",
			"request": map[string]any{
				"description": desc, "tool": "Bash",
				"arguments": map[string]any{"command": "rm -rf node_modules"},
			},
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

// waitEventType 消费事件通道直到出现指定类型的事件（跳过 progress 等噪音）。
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
	fs.push(permissionEvent("perm-1", "Bash: rm -rf node_modules"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-1" {
		t.Errorf("PermissionID=%q，期望 perm-1", ev.PermissionID)
	}
	if !strings.Contains(ev.Text, "rm -rf node_modules") {
		t.Errorf("权限描述=%q，应含命令文本", ev.Text)
	}

	if err := ad.RespondPermission(context.Background(), taskID, "perm-1", "once"); err != nil {
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
	fs.push(msgEvent("m1", "user", "初始 prompt"))
	fs.push(msgEvent("m2", "assistant", "分析完毕\n{\"ask\":\"用哪个实现？\"}"))
	fs.push(idleEvent())

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
	fs.push(msgEvent("m1", "user", "初始 prompt"))
	fs.push(msgEvent("m2", "assistant", "全部完成\n{\"branch\":\"handoff/T1\",\"commit\":\"abc12345\",\"summary\":\"完成功能\"}"))
	fs.push(idleEvent())

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
}

// TestIdleFallbackNoTrailer 验证无 trailer 时的 git 兜底分类：
// 无新 commit → question（回合全文交审核者）；有新 commit → result OK
// （branch/commit 取 git 实况，summary 取回合末文本）。
func TestIdleFallbackNoTrailer(t *testing.T) {
	const assistantText = "我做了改动，但忘了按纪律输出协议 JSON"

	t.Run("no_new_commit", func(t *testing.T) {
		quietLog(t)
		repo := initGit(t)
		fs := newFakeServer(t)
		fs.push(msgEvent("m1", "user", "初始 prompt"))
		fs.push(msgEvent("m2", "assistant", assistantText))
		fs.push(idleEvent())

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
		fs.push(msgEvent("m1", "user", "初始 prompt"))
		fs.push(msgEvent("m2", "assistant", assistantText))
		fs.push(idleEvent())

		ev := waitEventType(t, ch, "result")
		if ev.Result == nil || !ev.Result.OK {
			t.Fatalf("期望兜底 result OK，实际 %+v", ev.Result)
		}
		if ev.Result.Branch != "main" {
			t.Errorf("兜底 Branch=%q，期望 main（git 实况）", ev.Result.Branch)
		}
		if ev.Result.CommitHash != wantHead {
			t.Errorf("兜底 Commit=%q，期望 git 实况 %q", ev.Result.CommitHash, wantHead)
		}
		if ev.Result.Summary != assistantText {
			t.Errorf("兜底 Summary=%q，期望回合末全文 %q", ev.Result.Summary, assistantText)
		}
	})
}

// TestServeDeathEmitsFailed 验证 serve 死亡判定：
// 探活失败 + SSE 断流后，产出 result !OK（FailReason 含 stderr 尾部），
// 事件通道随后关闭（执行终结）。
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
}
