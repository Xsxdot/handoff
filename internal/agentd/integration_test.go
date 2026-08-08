// agentd 核心闭环集成测试：dispatch → 权限门 → 提问 → 完成 → 审核 → 续接 → 归档。
//
// 这些是手把手全链路测试（fake adapter 脚本化驱动，不消耗真实 executor）：
//   - TestFullLoop        ：完整生命周期，断言应答原文无损透传与 adapter 收到全部动作
//   - TestFullLoopDeny    ：权限门拒绝路径（deny:原因 → RespondPermission("reject")）
//   - TestRecoverMidTask  ：spec §7 会话恢复验收——全新 client 凭 ListTasks + Attach 重建现场
//
// 约定：
//   - 走 client 包的真实 HTTP/WS 拨号（httptest 内网），fake 只做 executor 侧脚本
//   - t.Setenv("HOME", ...) 重定向 cursor 落盘位置，不污染真实主目录（与 client_test 同约定）
package agentd_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// integEnv 聚合闭环集成测试环境：真实 store + httptest server + manager(fake adapter) + client。
type integEnv struct {
	srv  *agentd.Server
	ts   *httptest.Server
	st   *store.Store
	fake *fake.Fake
	cli  *client.Client
	repo string // 任务仓库（沙箱里 git init 的干净仓库，Dispatch 的分支准备落在这里）
}

// newIntegEnv 组装完整测试环境并注册清理；fake 脚本为 nil 时用空脚本（后续 fake.Add 补）。
func newIntegEnv(t *testing.T, script []fake.Step) *integEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // cursor 落盘重定向到测试沙箱
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir()}
	srv := agentd.NewServer(cfg, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	f := fake.New(script)
	mgr := agentd.NewManager(st, srv.Hub(), f, cfg, logger)
	srv.SetManager(mgr)
	return &integEnv{srv: srv, ts: ts, st: st, fake: f, cli: client.New(ts.URL, testToken), repo: newTestRepo(t)}
}

// newTestRepo 在沙箱里造一个干净的 git 仓库（main 分支 + 初始提交），返回仓库路径。
func newTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "checkout", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@handoff.dev")
	runGit(t, repo, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatalf("写 README: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

// runGit 在 dir 执行 git，失败即 Fatal。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// dispatchPlan 用真实 client 派发一个任务并返回任务（仓库用沙箱里的干净 git 仓库）。
func (e *integEnv) dispatchPlan(t *testing.T, plan string) *proto.Task {
	t.Helper()
	task, err := e.cli.Dispatch(context.Background(), e.repo, base64.StdEncoding.EncodeToString([]byte(plan)), "plan.md", "local")
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	return task
}

// waitAction 用真实 client 等待下一个可动作事件并返回它。
func (e *integEnv) waitAction(t *testing.T, taskID string) *proto.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ev, err := e.cli.WaitEvent(ctx, taskID, false)
	if err != nil {
		t.Fatalf("WaitEvent: %v", err)
	}
	return ev
}

// payloadMap 把事件 payload 解析为 map 供断言。
func payloadMap(t *testing.T, ev *proto.Event) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		t.Fatalf("解析事件 payload: %v", err)
	}
	return m
}

// eventually 轮询断言：cond 在 timeout 内变为 true 才算通过。
func eventually(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", desc)
}

// TestFullLoop 是系统心脏的全链路验收：
// dispatch → permission_request（reply allow → fake 收到 once）→ question（reply 原文 →
// fake 收到原文）→ completed（payload 含 branch/commit）→ waiting_review →
// continue（fake 收到指令）→ 再 completed → done（fake 收到 Stop）→ completed。
func TestFullLoop(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{
		{Permission: "Bash: go test ./..."},
		{Question: "表结构用单数还是复数?"},
		{Finish: executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123", Summary: "完成表结构设计"}},
	})

	task := env.dispatchPlan(t, "把 users 表建出来")
	if task.State != proto.TaskStateRunning {
		t.Fatalf("dispatch 后 state=%s, want running", task.State)
	}

	// 1. 权限门：wait 收到 permission_request，回复 allow → fake 收到 once
	ev := env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypePermissionRequest {
		t.Fatalf("首个事件 type=%s, want permission_request", ev.Type)
	}
	pm := payloadMap(t, ev)
	permTicket, _ := pm["ticket_id"].(string)
	if permTicket == "" {
		t.Fatalf("permission_request payload 缺 ticket_id: %v", pm)
	}
	// ticket id 按任务命名空间化（taskID:permID，P1-6）
	if permTicket != task.ID+":perm-1" {
		t.Fatalf("permission ticket_id=%q, want %q（命名空间化）", permTicket, task.ID+":perm-1")
	}
	if perm, _ := pm["permission"].(string); perm != "Bash: go test ./..." {
		t.Fatalf("permission 描述=%q, want 原文", perm)
	}
	if err := env.cli.Reply(context.Background(), task.ID, permTicket, "allow"); err != nil {
		t.Fatalf("Reply allow: %v", err)
	}
	// executor 收到的是裸 permID（adapter 契约），与命名空间化的 ticket id 解耦
	eventually(t, 2*time.Second, "fake 收到 RespondPermission(once)", func() bool {
		perms := env.fake.Perms()
		return len(perms) == 1 && perms[0].PermID == "perm-1" && perms[0].Decision == "once"
	})

	// 2. 提问：wait 收到 question，回复「复数」→ fake 收到原文（无损透传）
	ev = env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeQuestion {
		t.Fatalf("第二个事件 type=%s, want question", ev.Type)
	}
	qm := payloadMap(t, ev)
	questionTicket, _ := qm["ticket_id"].(string)
	if questionTicket == "" {
		t.Fatalf("question payload 缺 ticket_id: %v", qm)
	}
	if q, _ := qm["question"].(string); q != "表结构用单数还是复数?" {
		t.Fatalf("question 文本=%q, want 原文", q)
	}
	if err := env.cli.Reply(context.Background(), task.ID, questionTicket, "复数"); err != nil {
		t.Fatalf("Reply 复数: %v", err)
	}
	eventually(t, 2*time.Second, "fake 收到 Send(复数)", func() bool {
		sends := env.fake.Sends()
		return len(sends) == 1 && sends[0].Text == "复数"
	})

	// 3. 完成：wait 收到 completed（payload 含 branch/commit），任务进入 waiting_review
	ev = env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeCompleted {
		t.Fatalf("第三个事件 type=%s, want completed", ev.Type)
	}
	cm := payloadMap(t, ev)
	if cm["branch"] != "handoff/T1" || cm["commit"] != "abc123" {
		t.Fatalf("completed payload=%v, want branch/commit", cm)
	}
	info, err := env.cli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.State != proto.TaskStateWaitingReview {
		t.Fatalf("completed 后 state=%s, want waiting_review", info.Task.State)
	}

	// 4. 续接：追加一步 Finish，continue 指令原样送达 fake，随后再收一条 completed
	env.fake.Add(task.ID, fake.Step{Finish: executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "def456", Summary: "给 users 表加了索引"}})
	if err := env.cli.Continue(context.Background(), task.ID, "把 users 表加索引"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	eventually(t, 2*time.Second, "fake 收到 Send(续接指令)", func() bool {
		sends := env.fake.Sends()
		return len(sends) == 2 && sends[1].Text == "把 users 表加索引"
	})
	ev = env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeCompleted {
		t.Fatalf("续接后事件 type=%s, want completed", ev.Type)
	}
	if cm := payloadMap(t, ev); cm["commit"] != "def456" {
		t.Fatalf("续接后 completed payload=%v, want commit=def456", cm)
	}

	// 5. 归档：done → 任务 completed 且 fake 收到 Stop
	if err := env.cli.Done(context.Background(), task.ID); err != nil {
		t.Fatalf("Done: %v", err)
	}
	info, err = env.cli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.State != proto.TaskStateCompleted {
		t.Fatalf("done 后 state=%s, want completed", info.Task.State)
	}
	eventually(t, 2*time.Second, "fake 收到 Stop", func() bool {
		stops := env.fake.Stops()
		return len(stops) == 1 && stops[0] == task.ID
	})
}

// TestFullLoopDeny 覆盖权限门拒绝路径：Reply "deny:太危险" → fake 收到 RespondPermission("reject")，
// 拒绝后流程照常走完（executor 侧被拒后继续执行并产出结果）。
func TestFullLoopDeny(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{
		{Permission: "Bash: rm -rf node_modules"},
		{Finish: executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123"}},
	})

	task := env.dispatchPlan(t, "清空依赖目录")
	ev := env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypePermissionRequest {
		t.Fatalf("事件 type=%s, want permission_request", ev.Type)
	}
	permTicket := payloadMap(t, ev)["ticket_id"].(string)
	if permTicket != task.ID+":perm-1" {
		t.Fatalf("permission ticket_id=%q, want %q（命名空间化）", permTicket, task.ID+":perm-1")
	}
	if err := env.cli.Reply(context.Background(), task.ID, permTicket, "deny:太危险"); err != nil {
		t.Fatalf("Reply deny: %v", err)
	}
	eventually(t, 2*time.Second, "fake 收到 RespondPermission(reject)", func() bool {
		perms := env.fake.Perms()
		return len(perms) == 1 && perms[0].PermID == "perm-1" && perms[0].Decision == "reject"
	})
	// 拒绝后流程继续：completed 仍到达，任务进 waiting_review
	ev = env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeCompleted {
		t.Fatalf("拒绝后事件 type=%s, want completed", ev.Type)
	}
}

// TestRecoverMidTask 是 spec §7 会话恢复的验收测试：
// fake 停在 Question 阻塞（executor 侧原地等待）→ 新建 client（模拟没有任何前文的全新审核者
// 会话）→ ListTasks 看到任务处于 waiting_answer → Attach 拿到 pending_tickets[0] 即未答提问
// → Reply 后流程继续走完。凭两条命令即可完整重建现场。
func TestRecoverMidTask(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{
		{Question: "表结构用单数还是复数?"},
		{Finish: executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123"}},
	})

	task := env.dispatchPlan(t, "把 users 表建出来")
	ev := env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeQuestion {
		t.Fatalf("事件 type=%s, want question", ev.Type)
	}
	questionTicket := payloadMap(t, ev)["ticket_id"].(string)

	// 全新审核者会话：新 client，与前面的 wait/reply 调用完全无关
	recoverCli := client.New(env.ts.URL, testToken)

	// tasks：看到任务且状态为 waiting_answer（正在等人回答）
	tasks, err := recoverCli.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("ListTasks 结果=%+v, want 仅任务 %s", tasks, task.ID)
	}
	if tasks[0].State != proto.TaskStateWaitingAnswer {
		t.Fatalf("recover 时任务 state=%s, want waiting_answer", tasks[0].State)
	}

	// attach：pending_tickets[0] 就是那个未答提问（现场恢复的关键数据源）
	info, err := recoverCli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(info.PendingTickets) != 1 {
		t.Fatalf("pending_tickets=%d, want 1", len(info.PendingTickets))
	}
	pt := info.PendingTickets[0]
	if pt.ID != questionTicket {
		t.Fatalf("pending_tickets[0].id=%s, want %s", pt.ID, questionTicket)
	}
	if !strings.Contains(string(pt.Request), "表结构用单数还是复数?") {
		t.Fatalf("ticket request=%s, want 含提问原文", pt.Request)
	}

	// 回答后流程继续走完：completed 到达、任务进 waiting_review、fake 收到原文
	if err := recoverCli.Reply(context.Background(), task.ID, pt.ID, "单数"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	ev = env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeCompleted {
		t.Fatalf("回复后事件 type=%s, want completed", ev.Type)
	}
	eventually(t, 2*time.Second, "fake 收到 Send(单数)", func() bool {
		sends := env.fake.Sends()
		return len(sends) == 1 && sends[0].Text == "单数"
	})
	info, err = recoverCli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.State != proto.TaskStateWaitingReview {
		t.Fatalf("回复后 state=%s, want waiting_review", info.Task.State)
	}
}

// TestReviewRoutes 覆盖审核者三条审阅命令的端到端链路（client → server → workspace → git）：
// dispatch 已把仓库切到任务分支 → 模拟 executor 提交 → diff 可见新文件与提交主题；
// fetch 可读文件内容且逃逸路径被拒；run 返回输出与退出码。
func TestReviewRoutes(t *testing.T) {
	env := newIntegEnv(t, nil)
	task := env.dispatchPlan(t, "加个文件")
	if task.Branch == "" {
		t.Fatalf("dispatch 后任务应带分支名（PrepareBranch 产物）")
	}

	// 在任务分支上提交一个文件，模拟 executor 产出
	if err := os.WriteFile(filepath.Join(env.repo, "impl.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("写 impl.go: %v", err)
	}
	runGit(t, env.repo, "add", "impl.go")
	runGit(t, env.repo, "commit", "-q", "-m", "feat: add impl")

	// diff：包含新文件与提交主题
	diff, err := env.cli.Diff(context.Background(), task.ID, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "impl.go") || !strings.Contains(diff, "feat: add impl") {
		t.Fatalf("diff 内容缺失:\n%s", diff)
	}

	// fetch：读文件内容；逃逸路径被拒
	content, err := env.cli.Fetch(context.Background(), task.ID, "impl.go")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if content != "package main\n" {
		t.Fatalf("Fetch 内容=%q, want %q", content, "package main\n")
	}
	if _, err := env.cli.Fetch(context.Background(), task.ID, "../etc/passwd"); err == nil {
		t.Fatalf("Fetch 逃逸路径应报错")
	}
	// fetch 目录：明确错误（400 语义，而非 500「读取失败」）
	if err := os.MkdirAll(filepath.Join(env.repo, "subdir"), 0o755); err != nil {
		t.Fatalf("建 subdir: %v", err)
	}
	if _, err := env.cli.Fetch(context.Background(), task.ID, "subdir"); err == nil {
		t.Fatalf("Fetch 目录应报错")
	}

	// run：正常输出 + 非零退出码（命令执行了就不算错误，退出码回传）
	stdout, code, err := env.cli.Run(context.Background(), task.ID, "echo review-ok")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 || !strings.Contains(stdout, "review-ok") {
		t.Fatalf("Run 输出=%q code=%d, want 含 review-ok 且 0", stdout, code)
	}
	_, code, err = env.cli.Run(context.Background(), task.ID, "exit 3")
	if err != nil {
		t.Fatalf("Run exit 3: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit 3 的退出码=%d, want 3", code)
	}
}

// TestPermissionImmediateVisible 覆盖 P1-2 时序修复：审核者收到权限事件后
// **立即** attach 与 reply（不 sleep），状态与挂起项必须已就位——
// 旧实现「先 Publish 后置 waiting_answer/注册 waiter」，事件到达瞬间状态可能
// 还是 running；reply 会走「无等待者 → 自愈中继」而 resumeIfIdle 看不到
// waiting_answer 跳过回迁，任务随后落回 waiting_answer 且无人再答，永久卡死
// （探针 1/60 复现「waiting_answer 但 pending_tickets=0」）。
func TestPermissionImmediateVisible(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{{Permission: "Bash: go test ./..."}})
	task := env.dispatchPlan(t, "把 users 表建出来")

	// 事件到达（WS 收到 permission_request = Publish 已完成）后立即 attach
	ev := env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypePermissionRequest {
		t.Fatalf("事件 type=%s, want permission_request", ev.Type)
	}
	info, err := env.cli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("事件后立即 attach state=%s, want waiting_answer（时序修复后立即可见）", info.Task.State)
	}
	if len(info.PendingTickets) != 1 {
		t.Fatalf("pending_tickets=%d, want 1（审核者必须立刻看到挂起项）", len(info.PendingTickets))
	}

	// 立即 reply：等待者已注册 → 走正常唤醒，任务回 running、executor 收到
	// 一次裸 permID 应答；绝不出现「reply 走自愈中继 + 任务卡死 waiting_answer」
	// 的错序竞态（P1-2 核心回归）
	if err := env.cli.Reply(context.Background(), task.ID, info.PendingTickets[0].ID, "allow"); err != nil {
		t.Fatalf("立即 Reply: %v", err)
	}
	eventually(t, 2*time.Second, "任务回迁 running 且 fake 收到 once", func() bool {
		if perms := env.fake.Perms(); len(perms) != 1 || perms[0].PermID != "perm-1" || perms[0].Decision != "once" {
			return false
		}
		cur, err := env.st.GetTask(task.ID)
		return err == nil && cur.State == proto.TaskStateRunning
	})
}

// TestDispatchDirtyWorktree409 覆盖 P1-14：脏工作区 dispatch 返回 409 + 可读
// 原因（审核者一条 git 命令即可修复），不再扁平化为「派发任务失败」的 500；
// 清理后恢复正常派发。
func TestDispatchDirtyWorktree409(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	// 弄脏工作区（未跟踪文件）
	dirtyPath := filepath.Join(env.repo, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("写脏文件: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dirtyPath) })

	_, err := env.cli.Dispatch(context.Background(), env.repo, plan, "plan.md", "local")
	if err == nil {
		t.Fatal("脏工作区派发应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "工作区不干净") {
		t.Fatalf("脏工作区错误应含 409 与可读原因, got: %v", err)
	}

	// 清理后恢复正常派发
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatalf("清理脏文件: %v", err)
	}
	task, err := env.cli.Dispatch(context.Background(), env.repo, plan, "plan.md", "local")
	if err != nil {
		t.Fatalf("清理后 Dispatch: %v", err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("清理后 dispatch state=%s, want running", task.State)
	}
}

// TestDispatchRepoUnusable400 覆盖 P1-14 的 400 分支：仓库路径不存在（git 探活
// 失败 → ErrRepoUnusable）时返回 400 + 可读原因，而不是扁平化的 500。
func TestDispatchRepoUnusable400(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	_, err := env.cli.Dispatch(context.Background(), filepath.Join(t.TempDir(), "no-such-repo"), plan, "plan.md", "local")
	if err == nil {
		t.Fatal("仓库不可用应被拒绝")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("仓库不可用错误应含 400 与可读原因, got: %v", err)
	}
}

// TestDispatchUnknownError500 覆盖 P1-14 的 500 兜底分支：agentd 侧故障（store
// 已关闭）且错误不属于任何已知哨兵时返回 500「派发任务失败」，错误细节不外泄。
func TestDispatchUnknownError500(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	// store 关闭后 CreateTask 失败：非 ErrDirtyWorktree/ErrRepoUnusable/
	// errBadDispatchRequest 的未知错误 → 走 500 兜底（store.Close 幂等，
	// Cleanup 二次关闭无害）
	if err := env.st.Close(); err != nil {
		t.Fatalf("关闭 store: %v", err)
	}
	_, err := env.cli.Dispatch(context.Background(), env.repo, plan, "plan.md", "local")
	if err == nil {
		t.Fatal("store 关闭后派发应失败")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "派发任务失败") {
		t.Fatalf("未知错误应映射为 500 统一提示, got: %v", err)
	}
}
