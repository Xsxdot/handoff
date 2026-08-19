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
	"bytes"
	"context"
	"encoding/base64"
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
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// newTestGate 造一个只带内置黑名单的判据网关（agentd_test 包的统一装配）。
func newTestGate(t *testing.T) *permgate.Gate {
	t.Helper()
	g, err := permgate.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	return g
}

// integEnv 聚合闭环集成测试环境：真实 store + httptest server + manager(fake adapter) + client。
type integEnv struct {
	srv  *agentd.Server
	ts   *httptest.Server
	st   *store.Store
	fake *fake.Fake
	cli  *client.Client
	mgr  *agentd.Manager // 供测试直接登记项目（B62：派发必须先登记）
	// repo 是任务仓库（沙箱里 git init 的干净仓库，Dispatch 的分支准备落在这里）。
	// repoPID 是它登记后的 project_id（懒登记，首次用时缓存）。
	repo    string
	repoPID string
}

// newIntegEnv 组装完整测试环境并注册清理；fake 脚本为 nil 时用空脚本（后续 fake.Add 补）。
func newIntegEnv(t *testing.T, script []fake.Step) *integEnv {
	return newIntegEnvCfg(t, script, nil)
}

// newIntegEnvCfg 组装测试环境，cfgMut 可在构造 manager 前调整配置（如 RepoRoot）。
func newIntegEnvCfg(t *testing.T, script []fake.Step, cfgMut func(*config.Config)) *integEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // cursor 落盘重定向到测试沙箱
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	if cfgMut != nil {
		cfgMut(cfg)
	}
	srv := agentd.NewServer(cfg, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	f := fake.New(script)
	mgr := agentd.NewManager(st, srv.Hub(), map[string]executor.Adapter{"fake": f}, cfg, nil, nil, nil, newTestGate(t), logger)
	srv.SetManager(mgr)
	quiesceOnCleanup(t, st, mgr)
	return &integEnv{srv: srv, ts: ts, st: st, fake: f, mgr: mgr, cli: client.New(ts.URL, testToken), repo: newTestRepo(t)}
}

// quiesceOnCleanup 让用例结束时先把写方停干净，再让 testing 去删沙箱目录。
//
// 为什么非有不可：Manager 没有停机接口，每个任务一条 go m.mediate 协程。用例
// 返回时仍在 running/waiting_* 的任务，协程还会继续 AppendEvent，而 AppendEvent
// 的同步钩子（eventFrameHook）会往 DataDir/tasks/<id>/frames.jsonl 追加。
// DataDir 是 t.TempDir()，testing 收尾时对它 RemoveAll——RemoveAll 刚把某个任务
// 目录清空、正要 unlink 它时对方又落一个 frames.jsonl，unlinkat 报
// "directory not empty"，用例被判失败。这条曾以 6% 左右的概率打红
// TestDispatchWorkdirBusyWhileWaitingReview（它故意留了个 running 的任务 B 不收）。
//
// 调用位置有讲究：t.Cleanup 是 LIFO，这行必须晚于第一次 t.TempDir()（那次注册了
// 删目录）与 st.Close 的注册，才能拿到「先停写 → 再关库 → 最后删目录」的顺序。
func quiesceOnCleanup(t *testing.T, st *store.Store, mgr *agentd.Manager) {
	t.Helper()
	t.Cleanup(func() {
		// 先敲掉还活着的任务。Stop 对已是终态的任务会报错，所以先筛一遍；
		// 报错一律忽略——这里是拆台阶段，收不掉也没有下一步可做。
		if tasks, err := st.ListTasks(); err == nil {
			for i := range tasks {
				if !tasks[i].State.IsTerminal() {
					_, _ = mgr.Stop(context.Background(), tasks[i].ID)
				}
			}
		}
		// Stop 只保证 executor 被敲掉，mediate 协程还要再跑几行（落终态事件）
		// 才退出。等所有任务落终态，写方才算真的停了。
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			tasks, err := st.ListTasks()
			if err != nil {
				break
			}
			busy := false
			for i := range tasks {
				if !tasks[i].State.IsTerminal() {
					busy = true
					break
				}
			}
			if !busy {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		// 兜住掉队者：终态之后仍可能有零星事件（清扫、回收）。目录马上就要被删，
		// 派生帧没有任何价值，摘钩子比跟它赛跑可靠。
		st.SetEventHook(nil)
	})
}

// registerProject 把 repo 登记成一个项目位置（同一仓库只登一次，结果缓存），
// 返回它的 project_id。
//
// 为什么每个派发用例都要先登记：B62 之后「必须先登记才能派发」是服务端单方面
// 保证的不变式，**不给测试开旁路**——开了旁路，测试就测不到真实调用路径。
// origin 由路径派生：每个用例的仓库各不相同，project_id 因此天然不撞。
func (e *integEnv) registerProject(t *testing.T, repo string) string {
	t.Helper()
	if repo == e.repo && e.repoPID != "" {
		return e.repoPID
	}
	origin := "git@handoff.test:" + strings.ReplaceAll(strings.TrimPrefix(repo, "/"), "/", "-") + ".git"
	runGit(t, repo, "remote", "add", "origin", origin)
	loc, err := e.mgr.RegisterProject(context.Background(), agentd.RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("registerProject(%s): %v", repo, err)
	}
	if repo == e.repo {
		e.repoPID = loc.ProjectID
	}
	return loc.ProjectID
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
	pid := e.registerProject(t, e.repo)
	task, err := e.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: base64.StdEncoding.EncodeToString([]byte(plan)), PlanName: "plan.md", Target: "local",
	})
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

	// 收到 completed 的那一刻状态就必须已经是 waiting_review——这是 handleResult
	// 「迁移 → 追加事件 → 广播」那条顺序的对外承诺。下面的 Done 紧跟着发，正是
	// 协调者脚本的真实形态；承诺一旦破了，它就会拿到 409。
	//
	// why 这里也要单独断一次（步骤 3 已断过一次）：步骤 3 的 wait 是**首次**连接，
	// 步骤 4 是重连后的历史重放路径——事件从 store 直接读出，一落库就可见，和实时
	// 订阅完全是两条路。2026-08-13 CI 实测炸的就是这一条。
	info, err = env.cli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if info.Task.State != proto.TaskStateWaitingReview {
		t.Fatalf("续接后 completed 到达时 state=%s, want waiting_review（事件先于状态可见）", info.Task.State)
	}

	// 5. 归档：done → 任务 completed 且 fake 收到 Stop
	if _, err := env.cli.Done(context.Background(), task.ID, ""); err != nil {
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
// fake 停在 Question 阻塞（executor 侧原地等待）→ 新建 client（模拟没有任何前文的全新协调者
// 会话）→ ListTasks 看到任务处于 waiting_answer → Attach 拿到 pending_tickets[0] 即未答提问
// → Reply 后流程继续走完。凭两条命令即可完整重建现场。
func TestRecoverMidTask(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{
		{Question: "表结构用单数还是复数?"},
		{Finish: executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123"}},
	})

	task := env.dispatchPlan(t, "把 users 表建出来")
	// P1-12：dispatch 响应即带 plan_summary（dispatch 时已从 plan 生成摘要落库）
	if task.PlanSummary != "把 users 表建出来" {
		t.Fatalf("dispatch 后 plan_summary=%q, want %q", task.PlanSummary, "把 users 表建出来")
	}
	ev := env.waitAction(t, task.ID)
	if ev.Type != proto.EventTypeQuestion {
		t.Fatalf("事件 type=%s, want question", ev.Type)
	}
	questionTicket := payloadMap(t, ev)["ticket_id"].(string)

	// 全新协调者会话：新 client，与前面的 wait/reply 调用完全无关
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
	// P1-12：全新会话凭 tasks 命令即可知道任务意图（plan_summary 由 Dispatch 落库）
	if tasks[0].PlanSummary != "把 users 表建出来" {
		t.Fatalf("tasks 里 plan_summary=%q, want %q", tasks[0].PlanSummary, "把 users 表建出来")
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

// TestReviewRoutes 覆盖协调者三条审阅命令的端到端链路（client → server → workspace → git）：
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

// TestPermissionImmediateVisible 覆盖 P1-2 时序修复：协调者收到权限事件后
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
		t.Fatalf("pending_tickets=%d, want 1（协调者必须立刻看到挂起项）", len(info.PendingTickets))
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
// 原因（协调者一条 git 命令即可修复），不再扁平化为「派发任务失败」的 500；
// 清理后恢复正常派发。
func TestDispatchDirtyWorktree409(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)

	// 弄脏工作区（未跟踪文件）
	dirtyPath := filepath.Join(env.repo, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("写脏文件: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dirtyPath) })

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local"})
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
	task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local"})
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

	// B62：项目必须先登记；登记要求路径是可用 git 仓库，登记完把目录删掉，
	// 让派发命中「路径不存在」的仓库可用性检查（原用例的直接塞不存在路径已不可行）
	repo := newTestRepo(t)
	pid := env.registerProject(t, repo)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("RemoveAll(%s): %v", repo, err)
	}

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local"})
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
	pid := env.registerProject(t, env.repo)

	// store 关闭后 CreateTask 失败：非 ErrDirtyWorktree/ErrRepoUnusable/
	// errBadDispatchRequest 的未知错误 → 走 500 兜底（store.Close 幂等，
	// Cleanup 二次关闭无害）
	if err := env.st.Close(); err != nil {
		t.Fatalf("关闭 store: %v", err)
	}
	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local"})
	if err == nil {
		t.Fatal("store 关闭后派发应失败")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "派发任务失败") {
		t.Fatalf("未知错误应映射为 500 统一提示, got: %v", err)
	}
}

// startFailAdapter 是 Start 恒失败的 adapter：模拟 executor 起不来（如 tmux 不在
// PATH）。用于断言 dispatch 失败时响应体携带可读真因而非扁平「派发任务失败」。
type startFailAdapter struct{}

func (startFailAdapter) Start(context.Context, executor.StartReq) error {
	return errors.New(`exec: "tmux": executable file not found in $PATH`)
}

func (startFailAdapter) Events(string) <-chan executor.AdapterEvent {
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

func (startFailAdapter) Send(context.Context, string, string) error { return nil }
func (startFailAdapter) RespondPermission(context.Context, string, string, string, string) error {
	return nil
}

func (startFailAdapter) Stop(string) error { return nil }

// TestDispatchExecutorStartFailureReturnsReason 覆盖修复 3：executor 启动失败
// （如 tmux 不在 PATH）时，dispatch 不能只回扁平「派发任务失败」的 500——executor
// 依赖缺失是环境问题而非 agentd 内部故障，响应体必须带上真因（exec: "tmux":
// executable file not found），否则协调者只能去 agentd.log 里翻一行 exec 错误，
// 完全没有可行动信息。
func TestDispatchExecutorStartFailureReturnsReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "opencode"}}
	srv := agentd.NewServer(cfg, st, logger)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	mgr := agentd.NewManager(st, srv.Hub(), map[string]executor.Adapter{"opencode": startFailAdapter{}}, cfg, nil, nil, nil, newTestGate(t), logger)
	srv.SetManager(mgr)

	repo := newTestRepo(t)
	// B62：派发必须先登记，登记会落到 store，随后 Dispatch 解析出同一路径
	origin := "git@handoff.test:" + strings.ReplaceAll(strings.TrimPrefix(repo, "/"), "/", "-") + ".git"
	runGit(t, repo, "remote", "add", "origin", origin)
	loc, rerr := mgr.RegisterProject(context.Background(), agentd.RegisterProjectReq{OriginURL: origin, Path: repo})
	if rerr != nil {
		t.Fatalf("RegisterProject: %v", rerr)
	}
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	_, err = client.New(ts.URL, testToken).Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: loc.ProjectID, PlanB64: plan, PlanName: "plan.md", Target: "local",
	})
	if err == nil {
		t.Fatal("executor 启动失败应使 dispatch 失败")
	}
	if !strings.Contains(err.Error(), "tmux") || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("dispatch 错误应带 executor 启动真因（而非扁平提示）, got: %v", err)
	}
}

// TestResumeRoute 端到端验证「应答没送到 executor → delivery_failed 唤醒 →
// resume 解开」的完整闭环：真实 HTTP 路由 + 真实 client，走协调者实际的动作序列。
//
// 这是 P0-5 的收口验收。注意这里复现的是**更隐蔽的那个变体**：等待者还在时
// reply 返回 200（应答确实落库了、也确实唤醒了等待者），投递失败发生在
// waitPermission 内部——协调者拿不到任何错误码，工单却已被消耗、attach 无挂起项、
// executor 仍原地阻塞。所以恢复操作必须配一个可见信号，否则没人知道要去 resume。
func TestResumeRoute(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{{Permission: "bash: go test ./..."}})
	task := env.dispatchPlan(t, "跑测试")
	ev := env.waitAction(t, task.ID)
	ticketID := payloadMap(t, ev)["ticket_id"].(string)

	// executor 半死：应答落库并唤醒等待者，但回传 executor 时失败
	env.fake.SetPermError(errors.New("模拟半死 executor: i/o timeout"))
	if err := env.cli.Reply(context.Background(), task.ID, ticketID, "allow"); err != nil {
		t.Fatalf("有等待者时 reply 本身应成功（应答已落库）: %v", err)
	}

	// 可见信号：投递失败必须产出 delivery_failed 事件唤醒协调者
	var failedEv *proto.Event
	eventually(t, 2*time.Second, "产出 delivery_failed 事件", func() bool {
		evs, err := env.st.EventsFromAsc(task.ID, 0, 100)
		if err != nil {
			return false
		}
		for i := range evs {
			if evs[i].Type == proto.EventTypeDeliveryFailed {
				failedEv = &evs[i]
				return true
			}
		}
		return false
	})
	if hint, _ := payloadMap(t, failedEv)["hint"].(string); !strings.Contains(hint, "resume") {
		t.Errorf("delivery_failed 事件应告诉协调者该执行 resume，实际 hint=%q", hint)
	}

	// 卡死现场：工单已被消耗，attach 看不到挂起项
	detail, err := env.cli.Attach(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if len(detail.PendingTickets) != 0 {
		t.Fatalf("工单已被应答消耗，pending 应为空，实际 %d 条", len(detail.PendingTickets))
	}

	// executor 恢复后 resume：应答重投成功
	env.fake.SetPermError(nil)
	report, err := env.cli.Resume(context.Background(), task.ID, false)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !strings.Contains(report, `"redelivered":1`) {
		t.Errorf("恢复报告应显示重投 1 条，实际 %s", report)
	}
	perms := env.fake.Perms()
	if len(perms) != 1 || perms[0].PermID != "perm-1" || perms[0].Decision != "once" {
		t.Errorf("应以裸 permID + once 重投给 executor，实际 %+v", perms)
	}

	// 幂等：再执行一次不得重复投递
	report2, err := env.cli.Resume(context.Background(), task.ID, false)
	if err != nil {
		t.Fatalf("第二次 Resume: %v", err)
	}
	if !strings.Contains(report2, `"redelivered":0`) {
		t.Errorf("已送达的应答不应重复投递，实际 %s", report2)
	}
}

// TestDispatchNewWorktreeRepoUnusable400 覆盖 B45 报告里的那半：managed 路径
// （--new-worktree）上仓库不可用，旧行为一路走到 worktree add 失败、落 500
// 「派发任务失败」，真因只在 agentd.log 里。现在必须是 400 + 可读原因。
func TestDispatchNewWorktreeRepoUnusable400(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	// B62：项目必须先登记；登记要求路径是可用 git 仓库，登记完把目录删掉，
	// 让派发命中「路径不存在」的仓库可用性检查（等价于旧用例的非 git 路径）
	repo := newTestRepo(t)
	pid := env.registerProject(t, repo)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("RemoveAll(%s): %v", repo, err)
	}

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err == nil {
		t.Fatal("非 git 路径 + --new-worktree 应被拒绝")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("应为 400 + 可读原因, got: %v", err)
	}
}

// TestDispatchRepoUnusableNotMisdiagnosed 是 B45 动机场景（远程派发）的守门人：
// 带 base_commit 时，非 git 路径旧行为会被 ResolveBaseline 误诊成
// ErrBaseCommitMissing 的 400「任务仓库落后于本地；请先在本地 git push」——
// 一个自信的错答案，比沉默更糟。
func TestDispatchRepoUnusableNotMisdiagnosed(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))

	// B62：项目必须先登记；登记要求路径是可用 git 仓库，登记完把目录删掉，
	// 让派发命中「路径不存在」的仓库可用性检查
	repo := newTestRepo(t)
	pid := env.registerProject(t, repo)
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("RemoveAll(%s): %v", repo, err)
	}

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
		BaseCommit: strings.Repeat("a", 40),
	})
	if err == nil {
		t.Fatal("非 git 路径应被拒绝")
	}
	if strings.Contains(err.Error(), "git push") || strings.Contains(err.Error(), "落后") {
		t.Fatalf("非 git 仓库不该被误诊为基线缺失: %v", err)
	}
	if !strings.Contains(err.Error(), "不可用") {
		t.Fatalf("应归入仓库不可用, got: %v", err)
	}
}

// TestDispatchWorkdirBusyWhileRunning409 覆盖 B42 的主场景：任务 A 原地占着仓库，
// 同仓库再派 B 必须被 409 拒绝且点名 A。旧行为是放行——A 一提交完
// git status 就干净了，脏检查这道「保护」恰好在最危险的时刻消失，B 的
// checkout -b 直接把共享 HEAD 切走，A 的下一次提交落到 B 的分支上。
func TestDispatchWorkdirBusyWhileRunning409(t *testing.T) {
	env := newIntegEnv(t, nil) // 空脚本：A 起来后停在 running
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)
	a := env.dispatchPlan(t, "第一个任务")

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local",
	})
	if err == nil {
		t.Fatal("同一仓库的第二个原地任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Fatalf("占用冲突应为 409, got: %v", err)
	}
	if !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("报文必须点名占用者 %s, got: %v", a.ID, err)
	}
	if !strings.Contains(err.Error(), "--new-worktree") {
		t.Fatalf("报文必须给出出路, got: %v", err)
	}

	// stop 让 A 落 failed（终态）→ 目录释放
	if _, err := env.cli.Stop(context.Background(), a.ID); err != nil {
		t.Fatalf("Stop(A): %v", err)
	}
	b := env.dispatchPlan(t, "第二个任务")
	if b.State != proto.TaskStateRunning {
		t.Fatalf("释放后 dispatch state=%s, want running", b.State)
	}
}

// TestDispatchWorkdirBusyWhileWaitingReview 钉住 spec §3.3 里最容易被质疑的一条：
// waiting_review 也算占用。审核期间要跑 diff/fetch/run/continue，HEAD 被切走这些
// 全会看错东西，continue 回去更是在别人的分支上干活。代价是必须先 done 掉。
func TestDispatchWorkdirBusyWhileWaitingReview(t *testing.T) {
	env := newIntegEnv(t, []fake.Step{{Finish: executor.Result{OK: true, Summary: "干完了"}}})
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)
	a := env.dispatchPlan(t, "第一个任务")

	if ev := env.waitAction(t, a.ID); ev.Type != proto.EventTypeCompleted {
		t.Fatalf("首个事件 type=%s, want completed", ev.Type)
	}
	eventually(t, 2*time.Second, "A 进入 waiting_review", func() bool {
		cur, err := env.st.GetTask(a.ID)
		return err == nil && cur.State == proto.TaskStateWaitingReview
	})

	_, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local",
	})
	if err == nil {
		t.Fatal("waiting_review 的任务仍占着工作树，第二个任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "waiting_review") {
		t.Fatalf("报文应为 409 并说明占用者状态, got: %v", err)
	}

	if _, err := env.cli.Done(context.Background(), a.ID, ""); err != nil {
		t.Fatalf("Done(A): %v", err)
	}
	b := env.dispatchPlan(t, "第二个任务")
	if b.State != proto.TaskStateRunning {
		t.Fatalf("done 后 dispatch state=%s, want running", b.State)
	}
}

// TestDispatchTwoNewWorktreesNotBlocked 防误伤：managed 树每任务一棵，天然不冲突，
// 守卫不该挡住本来就安全的路径——挡住了等于把并行派发这个核心能力废掉。
func TestDispatchTwoNewWorktreesNotBlocked(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)
	for i := 0; i < 2; i++ {
		task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
			ProjectID: pid, PlanB64: plan, PlanName: "plan.md",
			Target: "local", NewWorktree: true,
		})
		if err != nil {
			t.Fatalf("第 %d 个 --new-worktree 派发失败: %v", i+1, err)
		}
		if task.State != proto.TaskStateRunning {
			t.Fatalf("第 %d 个任务 state=%s, want running", i+1, task.State)
		}
	}
}

// TestDispatchUserWorktreeBusy 覆盖第三种模式：两个任务指同一棵用户自带
// worktree，第二个被拒。判定键是 WorkDir，一条规则覆盖三种模式。
func TestDispatchUserWorktreeBusy(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, env.repo, "worktree", "add", "-b", "wt-branch", wt)

	first, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local", Worktree: wt,
	})
	if err != nil {
		t.Fatalf("首个用户树派发: %v", err)
	}
	_, err = env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md", Target: "local", Worktree: wt,
	})
	if err == nil {
		t.Fatal("同一棵用户 worktree 的第二个任务应被拒绝")
	}
	if !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("应为 409 且点名占用者 %s, got: %v", first.ID, err)
	}
}

// TestDispatchNewWorktreeCarriesDirtySnapshot 覆盖 B43：--new-worktree 免脏检查是
// 对的（新树天然干净），但主仓的未提交改动不在基线里、executor 在新树里看不到
// 它们——派发照常成功，但任务必须带上快照，否则这件事在任何输出里都不留痕迹。
// 造 9 个脏文件验证封顶：只列 5 个，条数仍是 9。
func TestDispatchNewWorktreeCarriesDirtySnapshot(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)
	for i := 0; i < 9; i++ {
		name := fmt.Sprintf("dirty-%d.txt", i)
		if err := os.WriteFile(filepath.Join(env.repo, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("写脏文件 %s: %v", name, err)
		}
	}

	task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("主仓脏不该阻塞 --new-worktree 派发: %v", err)
	}
	if task.RepoDirtyCount != 9 {
		t.Fatalf("RepoDirtyCount = %d, want 9", task.RepoDirtyCount)
	}
	if strings.Count(task.RepoDirtyFiles, "dirty-") != 5 {
		t.Fatalf("文件串应封顶 5 个, got %q", task.RepoDirtyFiles)
	}
	if !strings.Contains(task.RepoDirtyFiles, "等 9 处") {
		t.Fatalf("截断后必须仍说得出总数, got %q", task.RepoDirtyFiles)
	}
}

// TestDispatchNewWorktreeCleanRepoNoSnapshot 主仓干净时两个字段为零值——
// 不能打一条「有 0 处未提交改动」的空提示。
func TestDispatchNewWorktreeCleanRepoNoSnapshot(t *testing.T) {
	env := newIntegEnv(t, nil)
	plan := base64.StdEncoding.EncodeToString([]byte("加个文件"))
	pid := env.registerProject(t, env.repo)

	task, err := env.cli.Dispatch(context.Background(), client.DispatchOpts{
		ProjectID: pid, PlanB64: plan, PlanName: "plan.md",
		Target: "local", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if task.RepoDirtyCount != 0 || task.RepoDirtyFiles != "" {
		t.Fatalf("干净仓库不该有快照: count=%d files=%q", task.RepoDirtyCount, task.RepoDirtyFiles)
	}
}

// initBareOrigin 建一个可 clone 的裸仓库，返回其路径。
//
// 注意：本文件是 package agentd_test（外部测试包），看不见 repoadmin_test.go
// 里的白盒同名 helper，这里按相同实现放一份外部包副本（包内不冲突）。
func initBareOrigin(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	return dir
}

// initWorkRepo 建一个带 origin 且有一个提交的工作仓库，返回其路径。
//
// 注意：package agentd_test 的副本，与 repoadmin_test.go 里的白盒 helper 同实现。
func initWorkRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("remote", "add", "origin", origin)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-m", "init")
	return dir
}

// newTestServer 是 repo API 用例的测试服务器构造：复用 newIntegEnv 组装完整环境，
// 返回 httptest.Server（供 postJSON/getJSON/deleteReq/postRaw 使用）。
func newTestServer(t *testing.T) (*httptest.Server, *integEnv) {
	t.Helper()
	env := newIntegEnv(t, nil)
	return env.ts, env
}

// repoReq 发起一个带 Bearer token 的 HTTP 请求并返回响应（调用方负责关闭 Body）。
func repoReq(t *testing.T, srv *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, rd)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	return resp
}

// postJSON 发送 POST JSON 并断言状态码；out 非 nil 时解码响应体到 out。
func postJSON(t *testing.T, srv *httptest.Server, path string, body any, wantStatus int, out any) {
	t.Helper()
	resp := repoReq(t, srv, http.MethodPost, path, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s 状态码 %d, want %d: %s", path, resp.StatusCode, wantStatus, rb)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("解码 %s 响应: %v", path, err)
		}
	}
}

// getJSON 发送 GET 并断言状态码，解码响应体到 out。
func getJSON(t *testing.T, srv *httptest.Server, path string, wantStatus int, out any) {
	t.Helper()
	resp := repoReq(t, srv, http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s 状态码 %d, want %d: %s", path, resp.StatusCode, wantStatus, rb)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("解码 %s 响应: %v", path, err)
	}
}

// deleteReq 发送 DELETE 并断言状态码。
func deleteReq(t *testing.T, srv *httptest.Server, path string, wantStatus int) {
	t.Helper()
	resp := repoReq(t, srv, http.MethodDelete, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("DELETE %s 状态码 %d, want %d: %s", path, resp.StatusCode, wantStatus, rb)
	}
}

// postRaw 发送 POST JSON 并返回响应体原文（供报文内容断言）。
func postRaw(t *testing.T, srv *httptest.Server, path string, body any, wantStatus int) string {
	t.Helper()
	resp := repoReq(t, srv, http.MethodPost, path, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		rb, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s 状态码 %d, want %d: %s", path, resp.StatusCode, wantStatus, rb)
	}
	rb, _ := io.ReadAll(resp.Body)
	return string(rb)
}

// TestProjectAPIAddListRemove 走完整 HTTP 面：登记 → 列出 → 注销。
func TestProjectAPIAddListRemove(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)

	var added proto.ProjectLocation
	postJSON(t, srv, "/api/projects", map[string]any{"name": "r1", "path": repo, "origin_url": origin}, http.StatusOK, &added)
	if added.OriginURL != origin {
		t.Fatalf("OriginURL = %q, want %q", added.OriginURL, origin)
	}
	if added.ProjectID == "" {
		t.Fatal("project_id 应由 origin 派生，不应为空")
	}

	var list []proto.ProjectLocation
	getJSON(t, srv, "/api/projects", http.StatusOK, &list)
	if len(list) != 1 || list[0].Name != "r1" || list[0].Status != "有效" {
		t.Fatalf("列表不符: %+v", list)
	}

	deleteReq(t, srv, "/api/projects/r1", http.StatusOK)
	getJSON(t, srv, "/api/projects", http.StatusOK, &list)
	if len(list) != 0 {
		t.Fatalf("注销后仍有 %d 条", len(list))
	}
}

// TestProjectAPIRejectsNonRepoWithReadableReason 验证非 git 路径 → 400 且带 git 原文，
// 不被扁平化成「操作失败」（B45 立下的规矩）。
func TestProjectAPIRejectsNonRepoWithReadableReason(t *testing.T) {
	srv, _ := newTestServer(t)
	body := postRaw(t, srv, "/api/projects",
		map[string]any{"name": "x", "path": t.TempDir(), "origin_url": "git@example.com:org/x.git"},
		http.StatusBadRequest)
	if !strings.Contains(body, "not a git repository") {
		t.Fatalf("响应体未带 git 原文: %s", body)
	}
}

// TestProjectAPICloneIntoExistingPathConflicts 验证克隆落点已存在 → 409
// （不给 path 让 agentd 自己 clone，落点 repo_root/<名字> 已被占住）。
func TestProjectAPICloneIntoExistingPathConflicts(t *testing.T) {
	root := t.TempDir()
	env := newIntegEnvCfg(t, nil, func(cfg *config.Config) { cfg.RepoRoot = root })
	origin := initBareOrigin(t)
	dest := filepath.Join(root, "x")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	postRaw(t, env.ts, "/api/projects",
		map[string]any{"name": "x", "origin_url": origin},
		http.StatusConflict)
}

// TestProjectAPIRemoveMissing 验证注销不存在的位置 → 404。
func TestProjectAPIRemoveMissing(t *testing.T) {
	srv, _ := newTestServer(t)
	deleteReq(t, srv, "/api/projects/nope", http.StatusNotFound)
}

// TestDispatchResolvesRegisteredShortName 验证 project_name 派发落到登记的路径上。
func TestDispatchResolvesRegisteredShortName(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/projects", map[string]any{"name": "r1", "path": repo, "origin_url": origin},
		http.StatusOK, &proto.ProjectLocation{})

	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"project_name": "r1", "prompt": "干活", "new_worktree": true},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want 登记的 %q", task.RepoPath, repo)
	}
}

// TestDispatchResolvesProjectID 验证按 project_id 派发落到登记的路径上
// （B62 的主路径：CLI 从 cwd 的 origin 离线算出 project_id 上送）。
func TestDispatchResolvesProjectID(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/projects", map[string]any{"name": "r1", "path": repo, "origin_url": origin},
		http.StatusOK, &proto.ProjectLocation{})

	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"project_id": projectid.FromOrigin(origin), "prompt": "干活", "new_worktree": true},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", task.RepoPath, repo)
	}
}

// TestProjectAPISecondLocationSameProject409 验证 ADR-0008 在 HTTP 面的新边界：
// 同一项目登记第二个位置（同 origin、换路径）→ 409。B62 之前同名 origin 会造出
// 派发歧义，之后被「一台机器一个位置」的主键直接拒掉。
func TestProjectAPISecondLocationSameProject409(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	first := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/projects", map[string]any{"name": "a", "path": first, "origin_url": origin},
		http.StatusOK, &proto.ProjectLocation{})
	second := initWorkRepo(t, origin)
	body := postRaw(t, srv, "/api/projects",
		map[string]any{"name": "b", "path": second, "origin_url": origin},
		http.StatusConflict)
	if !strings.Contains(body, "已登记") {
		t.Fatalf("409 报文应指向已有位置: %s", body)
	}
}

// TestDispatchUnregisteredNameLists 验证 project_name 查不到 → 400 且报文带已登记清单。
func TestDispatchUnregisteredNameLists(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/projects", map[string]any{"name": "known", "path": repo, "origin_url": origin},
		http.StatusOK, &proto.ProjectLocation{})
	body := postRaw(t, srv, "/api/tasks",
		map[string]any{"project_name": "unknown", "prompt": "干活", "new_worktree": true},
		http.StatusBadRequest)
	if !strings.Contains(body, "known") {
		t.Fatalf("报文未列出已登记的项目: %s", body)
	}
}

// TestDispatchBodyPathFieldIgnored 验证 B62 的边界：请求体里即使塞了 repo 路径
// 也会被忽略——「代码在这台机器的哪个目录」由本机位置表决定，不由调用方描述。
func TestDispatchBodyPathFieldIgnored(t *testing.T) {
	srv, _ := newTestServer(t)
	origin := initBareOrigin(t)
	repo := initWorkRepo(t, origin)
	postJSON(t, srv, "/api/projects", map[string]any{"name": "r1", "path": repo, "origin_url": origin},
		http.StatusOK, &proto.ProjectLocation{})

	// 塞一个指向别的仓库的 repo 字段：应当被忽略，任务仍落在登记的位置上
	other := initWorkRepo(t, initBareOrigin(t))
	var task proto.Task
	postJSON(t, srv, "/api/tasks",
		map[string]any{"project_id": projectid.FromOrigin(origin), "prompt": "干活", "new_worktree": true, "repo": other},
		http.StatusOK, &task)
	if task.RepoPath != repo {
		t.Fatalf("请求体里的 repo 路径应被忽略：RepoPath = %q, want 登记的 %q", task.RepoPath, repo)
	}
}
