// manager 状态机兜底路径的白盒回归测试。
//
// 背景：handleResult 的 transitToReview 存在一个残留竞态——result 事件在回答待决
// （waiting_answer）期间到达时，首跳必然失败；若应答 goroutine 恰在「首跳失败后、
// 重读前」抢先回迁 running，旧实现会直接报错，把已追加落库的 result 事件连同
// Publish 一起丢弃，任务卡死在 running 直到看门狗。本文件覆盖修复后的两条兜底路径：
//   - TestTransitToReviewResidualRace：确定性复现「重读见 running → 重试补跳」分支
//   - TestTransitToReviewAnswerRaceConverges：并发全流程（result 与应答同时注入），
//     断言结果事件绝不因该竞态被吞（修复后恒绿；旧实现下该用例必现红）
//   - TestTransitToReviewTwoHopFromWaitingAnswer：确定性防御性两跳 + 应答后到不丢
//
// 测试为白盒（package agentd）：直接驱动 manager 内部方法，绕开 fake 的阻塞语义
// （fake 的 Finish 步骤必须等 Send/RespondPermission 后才执行，无法在回答挂起时
// 产出 result，故 reviewer 指出的竞态在集成测试层面不可达）。
package agentd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/envfile"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/fake"
	"github.com/xushixin/handoff/internal/permgate"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// looseTempDir 建一个测试用临时目录，收尾时尽力删除、删不掉也不判用例失败。
//
// 为什么不用 t.TempDir()：Dispatch/Continue 成功后会 `go m.mediate(taskID)`，
// 中介循环起手就是一次 m.st.GetTask——一条 SQLite 查询。用例断言完就返回了，
// 这次查询可能还在飞：database/sql 的 Close 不等待「在用」连接，SQLite 收尾时
// 会把 -wal/-shm 落回库目录，而 t.TempDir() 注册的 RemoveAll 此刻正在删同一个
// 目录，于是报 "directory not empty"——并且失败会算在当时恰好在跑的那个用例
// 头上（08-10 实撞：误伤 TestContinueColdResumeEmitsProgressEvent，它自身的
// 断言全部通过）。这种误报最坏的地方是把排查引向无辜的用例。
//
// 为什么不是去修 goroutine：生产里中介循环随任务生命周期存在、随 agentd 进程
// 退出，本就没有需要回收的泄漏；需要放宽的只是「临时目录必须删干净」这条纯
// 测试侧断言。库目录是一次性草稿，删剩一个 -wal 不说明任何问题。
func looseTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agentd-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newTestGate 造一个只带内置黑名单的判据网关（manager 测试环境的统一装配）。
func newTestGate(t *testing.T) *permgate.Gate {
	t.Helper()
	g, err := permgate.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	return g
}

// chanAdapter 是测试用空操作 adapter：事件通道由测试直接控制（模拟 executor 侧事件流），
// 并记录 RespondPermission/Send 实参供断言（答案侧是否真正回传 executor）。
type chanAdapter struct {
	mu    sync.Mutex
	evCh  chan executor.AdapterEvent
	perms []string
	sends []string
	// respondErr 非 nil 时 RespondPermission/Send 直接返回它（模拟 executor
	// 已退出或调用失败），供恢复操作的失败分支断言
	respondErr error
}

// setRespondErr 设置（或用 nil 清除）RespondPermission/Send 的注入错误。
func (a *chanAdapter) setRespondErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.respondErr = err
}

func (a *chanAdapter) Start(context.Context, executor.StartReq) error { return nil }
func (a *chanAdapter) Events(string) <-chan executor.AdapterEvent     { return a.evCh }

func (a *chanAdapter) Send(_ context.Context, _ string, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.sends = append(a.sends, text)
	return nil
}

func (a *chanAdapter) RespondPermission(_ context.Context, _ string, permID, decision string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.perms = append(a.perms, permID+":"+decision)
	return nil
}

func (a *chanAdapter) Stop(string) error { return nil }

// permsRec 返回已记录的 RespondPermission 实参（副本）。
func (a *chanAdapter) permsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.perms...)
}

// sendsRec 返回已记录的 Send 实参（副本）。
func (a *chanAdapter) sendsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.sends...)
}

// newTestManager 组装 manager 白盒测试环境：真实 store + hub + 可控事件通道 adapter。
func newTestManager(t *testing.T) (*Manager, *store.Store, *Hub, *chanAdapter) {
	t.Helper()
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, hub := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	return m, st, hub, ad
}

// newTestManagerWithAds 组装带 adapter 注册表的 manager 白盒测试环境：
// 真实 store + hub + 给定注册表（defaultName 为缺省执行者名，写进 cfg.Executor.Default）；
// 不启用审批链（approver=nil）。
func newTestManagerWithAds(t *testing.T, ads map[string]executor.Adapter, defaultName string) (*Manager, *store.Store, *Hub) {
	return newTestManagerWithApprover(t, ads, defaultName, nil)
}

// newTestManagerWithApprover 组装带 adapter 注册表与可选审批者的 manager 白盒
// 测试环境（approver 可为 nil）。
func newTestManagerWithApprover(t *testing.T, ads map[string]executor.Adapter, defaultName string, approver *Approver) (*Manager, *store.Store, *Hub) {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: defaultName}}
	return NewManager(st, hub, ads, cfg, approver, newTestGate(t), logger), st, hub
}

// mustCreateTask 直接落库一个任务（绕过 Dispatch 的工作区准备），供路由类测试造数据。
func mustCreateTask(t *testing.T, st *store.Store, task *proto.Task) {
	t.Helper()
	if err := st.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// TestAdapterForRoutesByTaskExecutor 验证 adapterFor 按 task.Executor 路由：
// 显式 executor 命中注册表对应 adapter；executor 为空回退缺省执行者（老任务兼容）。
func TestAdapterForRoutesByTaskExecutor(t *testing.T) {
	adA, adB := fake.New(nil), fake.New(nil)
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"a": adA, "b": adB}, "a")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "b", State: proto.TaskStateRunning})
	mustCreateTask(t, st, &proto.Task{ID: "t2", RepoPath: "/r", Executor: "", State: proto.TaskStateRunning})
	if got, _ := m.adapterFor("t1"); got != adB {
		t.Fatalf("t1 应路由到 b")
	}
	if got, _ := m.adapterFor("t2"); got != adA {
		t.Fatalf("executor 为空应回退缺省 a")
	}
}

// TestResolveExecutorRejectsUnknown 验证 dispatch 期未注册执行者被拒，错误列出可用项。
func TestResolveExecutorRejectsUnknown(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"a": fake.New(nil)}, "a")
	if _, _, err := m.resolveExecutor("nope"); err == nil || !strings.Contains(err.Error(), "a") {
		t.Fatalf("未注册执行者应报错并列出可用项: %v", err)
	}
}

// TestDispatchPromptOnly 验证 prompt-only 派发：无 plan 文件时以 prompt 生成摘要与
// 展示名，任务正常进入 running。
func TestDispatchPromptOnly(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "把 README 安装命令改成 brew", Target: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.PlanSummary == "" || !strings.Contains(task.PlanSummary, "README") {
		t.Fatalf("prompt-only 派发应以 prompt 生成摘要: %q", task.PlanSummary)
	}
	if task.Name == "" {
		t.Fatalf("name 应从 prompt 派生: %q", task.Name)
	}
}

// TestDispatchRequiresPlanOrPrompt 验证 plan 与 prompt 都缺时 400 拒绝。
func TestDispatchRequiresPlanOrPrompt(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	if _, err := m.Dispatch(context.Background(), DispatchReq{Repo: "/r"}); !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("plan 与 prompt 都缺应 400: %v", err)
	}
}

// TestDispatchPromptAppendedToPlan 验证 prompt 与 plan 同时存在时，prompt 作为
// 附加指令拼接在 plan 之后（任务目录里的 plan 文件含两段）。
func TestDispatchPromptAppendedToPlan(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	plan := base64.StdEncoding.EncodeToString([]byte("# 计划标题\n正文"))
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, PlanB64: plan, PlanName: "p.md", Prompt: "只改 X 模块",
	})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(task.PlanPath)
	if !strings.Contains(string(content), "附加指令") || !strings.Contains(string(content), "只改 X 模块") {
		t.Fatalf("prompt 应拼接在 plan 之后: %s", content)
	}
}

// TestDispatchUnknownExecutorRejected 验证未注册执行者 dispatch 被拒（400）。
func TestDispatchUnknownExecutorRejected(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	_, err := m.Dispatch(context.Background(), DispatchReq{Repo: "/r", Prompt: "x", Executor: "nope"})
	if !errors.Is(err, errBadDispatchRequest) {
		t.Fatalf("未注册执行者应 400: %v", err)
	}
}

// TestDispatchPersistsExecutorModelAndWorkspace 验证 executor/model/name 与新
// worktree 元数据随派发落库。
func TestDispatchPersistsExecutorModelAndWorkspace(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", Model: "m1",
		Name: "自定义名", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Executor != "fake" || task.Model != "m1" || task.Name != "自定义名" {
		t.Fatalf("executor/model/name 未落库: %+v", task)
	}
	if task.WorkDir == "" || !task.WorktreeManaged {
		t.Fatalf("new-worktree 元数据未落库: %+v", task)
	}
}

// TestDeriveName 覆盖展示名派生规则：显式优先 > plan 名去日期前缀/去 .md >
// prompt 前 20 rune。
func TestDeriveName(t *testing.T) {
	for _, c := range []struct{ name, planName, prompt, want string }{
		{"显式优先", "p.md", "x", "显式优先"},
		{"", "2026-08-08-fix-login.md", "", "fix-login"},
		{"", "", "把 README 里的安装命令改成 brew 并验证一遍效果", "把 README 里的安装命令改成 brew "},
	} {
		if got := deriveName(c.name, c.planName, c.prompt); got != c.want {
			t.Fatalf("deriveName(%q,%q,%q)=%q want %q", c.name, c.planName, c.prompt, got, c.want)
		}
	}
}

// mustDone 归档任务，失败即 Fatal。
func mustDone(t *testing.T, m *Manager, taskID string) {
	t.Helper()
	if err := m.Done(context.Background(), taskID); err != nil {
		t.Fatalf("Done: %v", err)
	}
}

// TestDispatchFailedAfterWorkspaceCleansManagedWorktree 验证 dispatch 在
// PrepareWorkspace 成功之后、任务记录落库之前失败时，补偿清理已建的 managed
// worktree（P2-2）：否则该 worktree 无任务记录持有，done 永不清理成为孤儿。
func TestDispatchFailedAfterWorkspaceCleansManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	// DataDir 下预置一个名为 tasks 的**文件**：PrepareWorkspace 的 worktrees
	// 子目录照常可建、worktree 照常创建，但 Dispatch 的 MkdirAll(DataDir/tasks/<id>)
	// 会因「tasks 不是目录」失败——精确命中「工作区已建、任务记录未落」的窗口
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "tasks"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fk := fake.New(nil)
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Token: "test", DataDir: dataDir, Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, hub, map[string]executor.Adapter{"fake": fk}, cfg, nil, newTestGate(t), logger)

	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	// 断言：worktrees 目录下已无残留 worktree
	wtDir := filepath.Join(dataDir, "worktrees")
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		t.Fatalf("读 worktrees 目录: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("managed worktree 应被补偿清理，仍有: %v", entries)
	}
}

// failStartAdapter 是 Start 恒失败的 adapter：模拟 executor 起不来（如 tmux 不在
// PATH），Dispatch 应把任务落 failed 并补偿清理已建的 managed worktree。
type failStartAdapter struct{}

func (a *failStartAdapter) Start(context.Context, executor.StartReq) error {
	return errors.New(`exec: "tmux": executable file not found in $PATH`)
}

func (a *failStartAdapter) Events(string) <-chan executor.AdapterEvent {
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

func (a *failStartAdapter) Send(context.Context, string, string) error { return nil }
func (a *failStartAdapter) RespondPermission(context.Context, string, string, string) error {
	return nil
}

func (a *failStartAdapter) Stop(string) error { return nil }

// TestDispatchStartFailureCleansManagedWorktree 覆盖 managed worktree 泄漏路径 (a)：
// adapter.Start 失败（如 tmux 不在 PATH，executor 起不来）时任务落 failed，已建的
// managed worktree 必须补偿删除——落 failed 的任务没有任何清理路径（done 只认
// waiting_review），不清就是永久残留。
func TestDispatchStartFailureCleansManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": &failStartAdapter{}}, "fake")

	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	}); err == nil {
		t.Fatal("adapter.Start 失败应使 dispatch 失败")
	}

	wtDir := filepath.Join(m.cfg.DataDir, "worktrees")
	entries, err := os.ReadDir(wtDir)
	if err != nil {
		t.Fatalf("读 worktrees 目录: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("adapter.Start 失败后 managed worktree 应被补偿清理，仍有: %v", entries)
	}
	// 任务落 failed：审核者仍能经 tasks 看到失败现场（PlanPath 等已落库）
	tasks, err := st.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].State != proto.TaskStateFailed {
		t.Fatalf("任务应落 failed 供审核者查看, got %+v", tasks)
	}
}

// TestStopRemovesManagedWorktree 覆盖 managed worktree 泄漏路径 (b)：被 stop 的任务
// 落 failed，managed worktree 必须随 stop 删除——否则 stop 过的任务永远归档不了、
// worktree 永久残留。任务分支必须保留（stop 不丢工作）。
func TestStopRemovesManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workDir := task.WorkDir
	if workDir == "" || !task.WorktreeManaged {
		t.Fatalf("new-worktree 元数据缺失: %+v", task)
	}
	if removed, err := m.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	} else if !removed {
		t.Fatalf("managed worktree 已删，stop 应报告 worktree_removed=true")
	}
	cur, _ := st.GetTask(task.ID)
	if cur.State != proto.TaskStateFailed {
		t.Fatalf("stop 后 state=%s, want failed", cur.State)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("stop 后 managed worktree 应被删除: %v", err)
	}
	if out := gitOut(t, repo, "branch", "--list", "handoff/"+id8(task.ID)); out == "" {
		t.Fatalf("stop 不得删除任务分支")
	}
}

// TestStopReportsWorktreeRemoved 验证 stop 的返回如实反映本次是否删除了 worktree：
// managed worktree（agentd 建的）已删 → worktree_removed=true；原地模式（没有
// worktree 概念）→ false。CLI 侧据此打印与行为一致的提示文案，不猜。
func TestStopReportsWorktreeRemoved(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, _, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)

	// managed worktree：stop 真删了 worktree → worktree_removed=true
	wtTask, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if wtTask.WorkDir == "" || !wtTask.WorktreeManaged {
		t.Fatalf("new-worktree 元数据缺失: %+v", wtTask)
	}
	removed, err := m.Stop(context.Background(), wtTask.ID)
	if err != nil {
		t.Fatalf("Stop(managed): %v", err)
	}
	if !removed {
		t.Fatal("managed worktree 已删，stop 应返回 worktree_removed=true")
	}

	// 原地模式：WorktreeManaged=false（WorkDir 回退为 RepoPath）→ 无 worktree 可删
	plainTask, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "y", Executor: "fake",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plainTask.WorktreeManaged {
		t.Fatalf("原地模式不应有 managed worktree: %+v", plainTask)
	}
	removed, err = m.Stop(context.Background(), plainTask.ID)
	if err != nil {
		t.Fatalf("Stop(plain): %v", err)
	}
	if removed {
		t.Fatal("原地模式没有 worktree，stop 应返回 worktree_removed=false")
	}
}

// TestDoneRemovesManagedWorktree 验证 done 归档时自动删除 agentd 管理的 worktree
// （目录消失、任务分支保留、任务 completed）。
func TestDoneRemovesManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New([]fake.Step{{Finish: executor.Result{OK: true, Branch: "handoff/x"}}})
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workDir := task.WorkDir
	if workDir == "" || !task.WorktreeManaged {
		t.Fatalf("new-worktree 元数据缺失: %+v", task)
	}
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview)
	mustDone(t, m, task.ID)

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("worktree 目录应已删除: %v", err)
	}
	if out := gitOut(t, repo, "branch", "--list", "handoff/"+id8(task.ID)); out == "" {
		t.Fatalf("任务分支不应被删除")
	}
	cur, _ := st.GetTask(task.ID)
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("任务应 completed，得到 %s", cur.State)
	}
}

// TestDoneKeepsUserWorktree 验证用户自带 worktree（Managed=false）done 后不被删除。
func TestDoneKeepsUserWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt1")
	gitT(t, repo, "worktree", "add", "-b", "pre-branch", wt)
	fk := fake.New([]fake.Step{{Finish: executor.Result{OK: true, Branch: "handoff/x"}}})
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", Worktree: wt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.WorktreeManaged {
		t.Fatalf("用户自带 worktree Managed 应为 false: %+v", task)
	}
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview)
	mustDone(t, m, task.ID)

	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("用户自带 worktree 不应被删除: %v", err)
	}
}

// TestDoneWorktreeRemoveFailureDoesNotBlockArchive 验证清理失败只降级为警告事件，
// 不影响归档结果（任务仍 completed）。
func TestDoneWorktreeRemoveFailureDoesNotBlockArchive(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New([]fake.Step{{Finish: executor.Result{OK: true, Branch: "handoff/x"}}})
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	workDir := task.WorkDir
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview)
	// Done 前往 worktree 塞未提交文件：git worktree remove 拒绝删除脏树
	if err := os.WriteFile(filepath.Join(workDir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustDone(t, m, task.ID)

	cur, _ := st.GetTask(task.ID)
	if cur.State != proto.TaskStateCompleted {
		t.Fatalf("清理失败不应阻塞归档，任务应 completed，得到 %s", cur.State)
	}
	evs := mustEvents(t, st, task.ID)
	found := false
	for _, e := range evs {
		if e.Type == proto.EventTypeProgress && strings.Contains(string(e.Payload), "worktree 清理失败") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应有含「worktree 清理失败」的 progress 事件: %v", evs)
	}
}

// createRunningTask 创建任务并迁移到 running（handlePermission 需要 running→waiting_answer 合法）。
func createRunningTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: id, Target: "local", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateRunning); err != nil {
		t.Fatalf("置为 running: %v", err)
	}
}

// waitAnswerRegistered 等待 waitPermission goroutine 完成 WaitAnswer 注册（hub.answers 里有等待者），
// 保证后续 NotifyAnswer 不会因注册未完成而被丢弃。
func waitAnswerRegistered(t *testing.T, hub *Hub, ticketID string) {
	t.Helper()
	eventually(t, 2*time.Second, "waitPermission 已注册到 hub", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.answers[ticketID]) > 0
	})
}

// resultEvent 构造一个 OK 的 result 事件（断言用 branch/commit）。
func resultEvent() executor.AdapterEvent {
	return executor.AdapterEvent{Type: "result", Result: &executor.Result{OK: true, Branch: "handoff/T1", CommitHash: "abc123"}}
}

// eventually 轮询断言：cond 在 timeout 内变为 true 才算通过（与 integration_test 同款）。
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

// TestExecutorSessionPersisted 断言 session id 最终出现在 task.ExecutorSession
// （store.GetTask 读回）——brief「session id 经 manager 写入 task.ExecutorSession」
// 的闭环验证：
//   - progress「会话就绪」信号：question 收尾主路径的唯一落库通道
//   - result 携带 SessionID：双保险通道（progress 乱序/丢失时兜底）
//   - 空 SessionID 不落库：向后兼容（老 adapter 事件不误写）
func TestExecutorSessionPersisted(t *testing.T) {
	t.Run("progress_signal", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		mgr.handleProgress("t1", executor.AdapterEvent{Type: "progress", SessionID: "sess-1", Text: "会话就绪"})
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "sess-1" {
			t.Fatalf("ExecutorSession=%q，期望 sess-1（会话就绪信号落库）", cur.ExecutorSession)
		}
	})
	t.Run("result_double_insurance", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		ev := resultEvent()
		ev.Result.SessionID = "sess-2"
		mgr.handleResult("t1", ev)
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "sess-2" {
			t.Fatalf("ExecutorSession=%q，期望 sess-2（result 双保险落库）", cur.ExecutorSession)
		}
	})
	t.Run("empty_session_ignored", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		mgr.handleProgress("t1", executor.AdapterEvent{Type: "progress", Text: "心跳"})
		cur, err := st.GetTask("t1")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.ExecutorSession != "" {
			t.Fatalf("空 SessionID 不应落库，实际=%q", cur.ExecutorSession)
		}
	})
}

// TestTransitToReviewResidualRace 确定性覆盖修复的核心分支：首跳（waiting_answer→
// waiting_review 非法）失败后、重读前应答 goroutine 已把状态回迁 running——
// 重读兜底必须重试补跳 waiting_review，而不是报错吞掉已追加的 result 事件。
//
// 对照旧实现：旧 transitToReview 在重读见 running（非 waiting_answer）时直接返回
// "任务不在 waiting_answer" 错误，本测试的 `transitToReviewRetry` 调用点即不复存在
// （编译期红）；旧行为的运行期错误路径由 TestTransitToReviewAnswerRaceConverges 实证。
func TestTransitToReviewResidualRace(t *testing.T) {
	mgr, st, _, _ := newTestManager(t)
	createRunningTask(t, st, "t1")
	// 任务挂起在回答待决——handleResult 首跳失败的必经状态
	if err := st.UpdateTaskState("t1", proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("置为 waiting_answer: %v", err)
	}
	// 竞态窗口第一步：与 handleResult 的首次尝试等价——waiting_answer→waiting_review 非法必被拒
	if err := mgr.transit("t1", proto.TaskStateWaitingReview, "test-首跳"); !errors.Is(err, store.ErrBadTransit) {
		t.Fatalf("首跳应被非法迁移拒绝, got %v", err)
	}
	// 竞态窗口第二步：应答 goroutine 在首跳失败后、重读前抢先回迁 running
	if err := mgr.transit("t1", proto.TaskStateRunning, "test-应答回迁"); err != nil {
		t.Fatalf("模拟应答回迁: %v", err)
	}
	// 重读兜底：见 running 必须重试补跳而非报错（旧实现此处报错并吞事件）
	if err := mgr.transitToReviewRetry("t1"); err != nil {
		t.Fatalf("重读见 running 应重试补跳 waiting_review, got %v", err)
	}
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("最终 state=%s, want waiting_review", cur.State)
	}
}

// TestTransitToReviewAnswerRaceConverges 并发全流程回归：任务挂起在 waiting_answer 时
// result 事件与应答同时注入（经真实事件通道与真实 waitPermission goroutine），断言
// 结果事件必然被 Publish（不被竞态吞掉）且只追加一次。
//
// 修复后恒绿：无论应答回迁与 result 中介谁先落地，transitToReviewRetry 都能收敛到
// waiting_review 并 Publish。旧实现下，只要应答 CAS 落入「首跳失败→重读」窗口，
// 事件即被吞、任务卡死 running——本用例以高概率必现（多轮迭代放大命中率）。
func TestTransitToReviewAnswerRaceConverges(t *testing.T) {
	const rounds = 150
	for i := 0; i < rounds; i++ {
		taskID := "t1"
		mgr, st, hub, ad := newTestManager(t)
		createRunningTask(t, st, taskID)
		// 真实权限门路径：ticket + waiting_answer + waitPermission goroutine 挂起等应答
		mgr.handlePermission(context.Background(), taskID, executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"})
		waitAnswerRegistered(t, hub, "t1:perm-1")
		// 订阅 Publish 通道，断言 completed 事件确实被广播（不被竞态吞掉）
		subCh, _ := hub.Subscribe(taskID)

		// result 经真实事件通道进入中介循环，应答随即注入——两者并发竞争同一 CAS
		go mgr.mediate(taskID)
		ad.evCh <- resultEvent()
		// 略延迟应答注入：让 result 处理先行进入「首跳失败→重读」危险窗口，
		// 提高旧实现下竞态命中率；新实现无论先后都收敛，延迟不影响结论
		time.Sleep(100 * time.Microsecond)
		hub.NotifyAnswer("t1:perm-1", "allow")

		// 收敛断言 1：completed 事件必须被 Publish（旧实现竞态命中时此处超时红）
		eventually(t, 2*time.Second, "completed 事件已 Publish", func() bool {
			select {
			case ev := <-subCh:
				return ev.Type == proto.EventTypeCompleted
			default:
				return false
			}
		})
		// 收敛断言 2：答案侧完成（executor 收到 RespondPermission），所有状态迁移落定
		eventually(t, 2*time.Second, "executor 收到 RespondPermission", func() bool {
			return len(ad.permsRec()) == 1
		})
		// 收敛断言 3：恰好一条 completed 事件落库（不重复追加）
		events, err := st.EventsFrom(taskID, 0, 100)
		if err != nil {
			t.Fatalf("EventsFrom: %v", err)
		}
		completed := 0
		for _, e := range events {
			if e.Type == proto.EventTypeCompleted {
				completed++
			}
		}
		if completed != 1 {
			t.Fatalf("第 %d 轮 completed 事件数=%d, want 1", i, completed)
		}
		// 收敛断言 4：终态为 waiting_review（事件已交割给审核者）或 running（应答后到，
		// executor 被重新唤醒续跑，属正常语义而非卡死）；绝不静默停在等待者已应答的空转态
		cur, err := st.GetTask(taskID)
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		if cur.State != proto.TaskStateWaitingReview && cur.State != proto.TaskStateRunning {
			t.Fatalf("第 %d 轮最终 state=%s, want waiting_review 或 running", i, cur.State)
		}
		// 清理：关闭事件通道，中介 goroutine 随 range 退出，不泄漏
		close(ad.evCh)
	}
}

// TestTransitToReviewTwoHopFromWaitingAnswer 确定性覆盖防御性两跳：result 在回答挂起
// 期间到达（应答后到），handleResult 必须经 running 两跳进入 waiting_review 并 Publish；
// 应答随后到达时 waitPermission 合法回迁 running（executor 被唤醒续跑），事件不丢失。
func TestTransitToReviewTwoHopFromWaitingAnswer(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	mgr.handlePermission(context.Background(), "t1", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"})
	waitAnswerRegistered(t, hub, "t1:perm-1")
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("handlePermission 后 state=%s, want waiting_answer", cur.State)
	}
	// result 同步处理（无并发干扰，纯两跳路径）：append → 首跳失败 → 重读见
	// waiting_answer → 经 running 两跳 → waiting_review → Publish
	mgr.handleResult("t1", resultEvent())
	cur, err = st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("两跳后 state=%s, want waiting_review", cur.State)
	}
	events, err := st.EventsFrom("t1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	completed := 0
	for _, e := range events {
		if e.Type == proto.EventTypeCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completed 事件数=%d, want 1", completed)
	}
	// 应答后到：waitPermission 回迁 running（waiting_review→running 合法），
	// executor 收到 RespondPermission——续跑语义成立，事件不丢
	hub.NotifyAnswer("t1:perm-1", "allow")
	eventually(t, 2*time.Second, "executor 收到 RespondPermission 且任务回迁 running", func() bool {
		if len(ad.permsRec()) != 1 {
			return false
		}
		c, err := st.GetTask("t1")
		return err == nil && c.State == proto.TaskStateRunning
	})
}

// TestRelayAnswer 验证 reply 无等待者时的自愈中继（agentd 重启后等待 goroutine 已
// 消亡、/event 不重放历史的场景）：RelayAnswer 直接读工单驱动 adapter，
// gate 按 allow→once/其余→reject 翻译（与 waitPermission 同规则），ask 原文透传。
func TestRelayAnswer(t *testing.T) {
	// createTicket 建一张指定 kind 的工单并返回（request 与 manager 的 ticketRequest 同构）。
	createTicket := func(st *store.Store, id, taskID, kind string) {
		t.Helper()
		req := json.RawMessage(`{"kind":"gate","permission":"Bash: rm -rf node_modules"}`)
		if kind == "ask" {
			req = json.RawMessage(`{"kind":"ask","question":"表结构用单数还是复数?"}`)
		}
		if _, err := st.CreateTicket(&proto.Ticket{
			ID: id, TaskID: taskID, Kind: kind, Request: req, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
	}

	t.Run("gate_allow_once", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t1:perm-1", "t1", "gate")
		if err := mgr.RelayAnswer("t1", "t1:perm-1", "allow"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		// adapter 收到的是裸 permID（剥离 taskID 前缀），而非命名空间化的工单 id
		if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-1:once" {
			t.Fatalf("executor 收到 %v, want [perm-1:once]", got)
		}
	})

	t.Run("gate_deny_reject", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t1:perm-2", "t1", "gate")
		if err := mgr.RelayAnswer("t1", "t1:perm-2", "deny:太危险"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-2:reject" {
			t.Fatalf("executor 收到 %v, want [perm-2:reject]", got)
		}
	})

	t.Run("ask_original_send", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "ask-1", "t1", "ask")
		if err := mgr.RelayAnswer("t1", "ask-1", "复数"); err != nil {
			t.Fatalf("RelayAnswer: %v", err)
		}
		if got := ad.sendsRec(); len(got) != 1 || got[0] != "复数" {
			t.Fatalf("executor 收到 %v, want [复数]（原文透传）", got)
		}
	})

	t.Run("ticket_not_found", func(t *testing.T) {
		mgr, st, _, _ := newTestManager(t)
		createRunningTask(t, st, "t1")
		if err := mgr.RelayAnswer("t1", "ghost", "x"); err == nil {
			t.Fatal("工单不存在应报错")
		}
	})

	t.Run("ticket_of_other_task", func(t *testing.T) {
		mgr, st, _, ad := newTestManager(t)
		createRunningTask(t, st, "t1")
		createTicket(st, "t2:perm-3", "t2", "gate")
		if err := mgr.RelayAnswer("t1", "t2:perm-3", "allow"); err == nil {
			t.Fatal("跨任务工单应报错")
		}
		if got := ad.permsRec(); len(got) != 0 {
			t.Fatalf("跨任务工单不得触达 executor, got %v", got)
		}
	})
}

// TestWaiterCanceledOnTaskEnd 覆盖 P1-2 的 ctx 取消半段：任务执行终结（adapter
// 事件通道关闭）后，挂起的应答等待 goroutine 必须被取消并从 hub 等待表移除——
// 旧实现 waitPermission 用 context.Background() 永久挂死（审核者不再回答即泄漏）。
//
// 为什么事件通道关闭是唯一取消时机：result → waiting_review 后任务仍活，回答
// 晚于 result 到达是合法流程（见 mediate 的 why 注释），只有「执行终结」才
// 取消在等应答的 waiter。
func TestWaiterCanceledOnTaskEnd(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	go mgr.mediate("t1")
	ad.evCh <- executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"}
	waitAnswerRegistered(t, hub, "t1:perm-1")

	// 执行终结：关闭事件通道 → 中介循环退出 → defer cancel → waiter 被取消并移除
	close(ad.evCh)
	eventually(t, 2*time.Second, "waiter 已被取消并从 hub 等待表移除", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.answers["t1:perm-1"]) == 0
	})
}

// TestTicketNamespacePerTask 覆盖 P1-6：ticket id 按任务命名空间隔离
// （taskID:permID）。两个任务收到相同 PermissionID 时，旧实现（裸 permID 作
// ticket id）第二个任务的工单被 INSERT OR IGNORE 静默吞掉——attach 显示 0
// 挂起项且永远无法应答；命名空间化后两个工单都存在且可分别应答。
func TestTicketNamespacePerTask(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	createRunningTask(t, st, "t2")
	mgr.handlePermission(context.Background(), "t1", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "x"})
	mgr.handlePermission(context.Background(), "t2", executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "y"})
	waitAnswerRegistered(t, hub, "t1:perm-1")
	waitAnswerRegistered(t, hub, "t2:perm-1")

	// 两个任务的工单都存在，id 均带任务前缀
	p1, err := st.PendingTickets("t1")
	if err != nil {
		t.Fatalf("PendingTickets(t1): %v", err)
	}
	if len(p1) != 1 || p1[0].ID != "t1:perm-1" {
		t.Fatalf("t1 pending=%+v, want [t1:perm-1]", p1)
	}
	p2, err := st.PendingTickets("t2")
	if err != nil {
		t.Fatalf("PendingTickets(t2): %v", err)
	}
	if len(p2) != 1 || p2[0].ID != "t2:perm-1" {
		t.Fatalf("t2 pending=%+v, want [t2:perm-1]（旧实现此处为 0，工单被 t1 吞掉）", p2)
	}

	// 分别可应答（store 层断言：命名空间化后两个工单互不干扰）；
	// 经 hub 唤醒各自 waiter 后，executor 各收到一次裸 permID 的 RespondPermission
	if err := st.AnswerTicket("t1:perm-1", "allow"); err != nil {
		t.Fatalf("AnswerTicket(t1): %v", err)
	}
	if err := st.AnswerTicket("t2:perm-1", "allow"); err != nil {
		t.Fatalf("AnswerTicket(t2): %v", err)
	}
	hub.NotifyAnswer("t1:perm-1", "allow")
	hub.NotifyAnswer("t2:perm-1", "allow")
	eventually(t, 2*time.Second, "两个任务的 executor 各收到一次应答", func() bool {
		return len(ad.permsRec()) == 2
	})
}

// TestPermissionReplaySkipsDuplicates 覆盖 P1-7 的 manager 层：同任务同 permID
// 的事件重放（SSE 断线重连/重启后订阅重建）必须跳过全部中介动作——
// 只有一条 permission_request 事件、一次状态迁移、一个 waiter、一次
// RespondPermission；已答后的重放不注册新 waiter、不重复唤醒。
func TestPermissionReplaySkipsDuplicates(t *testing.T) {
	mgr, st, hub, ad := newTestManager(t)
	createRunningTask(t, st, "t1")
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "perm-1", Text: "test"}
	mgr.handlePermission(context.Background(), "t1", ev)
	waitAnswerRegistered(t, hub, "t1:perm-1")
	cur, err := st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("首次中介后 state=%s, want waiting_answer", cur.State)
	}

	// 重放：created=false → 跳过，不追加事件、不再起 waiter
	mgr.handlePermission(context.Background(), "t1", ev)
	events, err := st.EventsFrom("t1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	permReq := 0
	for _, e := range events {
		if e.Type == proto.EventTypePermissionRequest {
			permReq++
		}
	}
	if permReq != 1 {
		t.Fatalf("permission_request 事件数=%d, want 1（重放不得重复追加）", permReq)
	}
	cur, err = st.GetTask("t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if cur.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("重放后 state=%s, want waiting_answer（不重复迁移）", cur.State)
	}

	// 一次应答 → 恰好一次 RespondPermission（不重复唤醒审核者/executor）
	hub.NotifyAnswer("t1:perm-1", "allow")
	eventually(t, 2*time.Second, "executor 收到恰一次应答", func() bool {
		return len(ad.permsRec()) == 1
	})

	// 已答后重放：不注册新 waiter（NotifyAnswer 应无等待者可投递）、
	// 不出现第二次 RespondPermission
	mgr.handlePermission(context.Background(), "t1", ev)
	if hub.NotifyAnswer("t1:perm-1", "allow") {
		t.Fatal("已答后重放不应注册新 waiter")
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(ad.permsRec()) != 1 {
			t.Fatalf("已答后重放不得再次 RespondPermission, got %v", ad.permsRec())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPlanSummaryFromContent（P1-12）：摘要规则——取首个非空行（markdown 计划的
// 标题位），按 planSummaryLimit 截断；内容为空或全空行时返回空串。
func TestPlanSummaryFromContent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"标题行", "# 修复登录态丢失\n\n## 背景\n...", "# 修复登录态丢失"},
		{"开头空行跳过后取标题", "\n\n   \n# 建表\nrest", "# 建表"},
		{"超长首行按 rune 截断", strings.Repeat("长", 300), strings.Repeat("长", 200)},
		{"空内容", "", ""},
		{"全空行", "\n  \n\n", ""},
	}
	for _, c := range cases {
		if got := planSummaryFromContent([]byte(c.content)); got != c.want {
			t.Errorf("%s: planSummaryFromContent=%q, want %q", c.name, got, c.want)
		}
	}
}

// TestPermissionTicketKeepsFullText 验证权限工单存全文、事件 payload 截断。
// why：旧实现在 adapter 侧就把描述截到 200 字，工单里存的本身就是截断版——
// 审核者无论怎么查都看不到完整命令，等于让他批准自己没看全的命令。
func TestPermissionTicketKeepsFullText(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	task := &proto.Task{ID: "T-full", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	mustCreateTask(t, st, task)

	long := "bash: " + strings.Repeat("x", 500) + " && rm -rf /tmp/danger"
	m.handleEvent(context.Background(), task.ID, executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: long,
	})

	tk, err := st.GetTicket(task.ID + ":p1")
	if err != nil {
		t.Fatalf("读取工单: %v", err)
	}
	if !strings.Contains(string(tk.Request), "rm -rf /tmp/danger") {
		t.Errorf("工单必须存权限描述全文（尾部的危险片段不能丢），实得 %s", tk.Request)
	}

	evs, err := st.EventsFromAsc(task.ID, 0, 10)
	if err != nil {
		t.Fatalf("读取事件: %v", err)
	}
	var payload string
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionRequest {
			payload = string(e.Payload)
		}
	}
	if payload == "" {
		t.Fatal("未产出 permission_request 事件")
	}
	if len([]rune(payload)) > 600 {
		t.Errorf("事件 payload 必须截断（唤醒消息保持短），实得 %d 字符", len([]rune(payload)))
	}
	if !strings.Contains(payload, executor.TruncationMarker) {
		t.Errorf("截断的事件 payload 必须带截断标记，实得 %s", payload)
	}
}

// TestStopEndsRunningTask 验证 stop 的完整效果：executor 停、挂起工单作废、
// failed 事件写明中止原因、状态落 failed。
func TestStopEndsRunningTask(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	task := &proto.Task{ID: "T-stop", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	mustCreateTask(t, st, task)
	// 造一个挂起工单：stop 后它必须被作废，否则审核者仍会看到可操作项，
	// 一 reply 就打进已死会话（与 handleResult 失败分支同一条理由）
	if _, err := st.CreateTicket(&proto.Ticket{ID: "T-stop:p1", TaskID: "T-stop", Kind: "gate",
		Request: []byte(`{"kind":"gate","permission":"bash: ls"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Stop(context.Background(), task.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != proto.TaskStateFailed {
		t.Errorf("stop 后状态 = %s，期望 failed", got.State)
	}
	pending, err := st.PendingTickets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("stop 后挂起工单必须清空，实得 %d 条", len(pending))
	}
	evs, err := st.EventsFromAsc(task.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range evs {
		if e.Type == proto.EventTypeFailed && strings.Contains(string(e.Payload), "中止") {
			found = true
		}
	}
	if !found {
		t.Error("stop 必须产出写明中止原因的 failed 事件（否则与真失败无法区分）")
	}
	// stop 走的是终态 failed，作废由 transit 收口完成，并且必须留下审计痕迹——
	// 否则 stop 与 done 两条终态路径的痕迹不一致，而痕迹不一致正是 B63 要修的东西
	if evs := voidedEvents(t, st, task.ID); len(evs) != 1 {
		t.Errorf("stop 后 tickets_voided = %d 条，期望 1 条", len(evs))
	}
}

// TestStopOnTerminalTaskRejected 验证已终结任务重复 stop 返回状态冲突而不是崩掉。
func TestStopOnTerminalTaskRejected(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	task := &proto.Task{ID: "T-stop2", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateCompleted, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	mustCreateTask(t, st, task)
	if _, err := m.Stop(context.Background(), task.ID); !errors.Is(err, store.ErrBadTransit) {
		t.Fatalf("已终结任务 stop 必须返回 ErrBadTransit（映射 409），实得 %v", err)
	}
}

// TestDispatchRejectsWhenEnvFileMissing 钉住 spec §6：env 解析失败必须在
// 建任务与工作区准备之前拒发，不能留下一个注定 failed 的任务。
func TestDispatchRejectsWhenEnvFileMissing(t *testing.T) {
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		Token: "test", DataDir: t.TempDir(),
		Executor: config.ExecutorConfig{Default: "fake"},
		Env:      map[string]string{"fake": "missing.env"},
	}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg, nil, newTestGate(t), logger)

	// Repo 随便给一个不存在的路径即可：env 解析发生在任何 git 动作之前，
	// 这条断言同时证明了「解析确实排在最前段」
	_, derr := m.Dispatch(context.Background(), DispatchReq{
		Repo: "/nonexistent/repo", Prompt: "任意指令",
	})
	if derr == nil {
		t.Fatal("env 文件缺失时应拒发")
	}
	if !errors.Is(derr, errEnvResolveFailed) {
		t.Fatalf("应为 errEnvResolveFailed，实际 %v", derr)
	}
	if !strings.Contains(derr.Error(), "missing.env") {
		t.Errorf("错误应带文件名，实际 %q", derr.Error())
	}
	tasks, lerr := st.ListTasks()
	if lerr != nil {
		t.Fatalf("ListTasks: %v", lerr)
	}
	if len(tasks) != 0 {
		t.Fatalf("拒发时不应创建任务，实际创建了 %d 个", len(tasks))
	}
}

// TestDispatchPassesEnvToAdapter 钉住解析结果确实到达了 adapter。
func TestDispatchPassesEnvToAdapter(t *testing.T) {
	dataDir := t.TempDir()
	envDir := envfile.Dir(dataDir)
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "dev.env"), []byte("HTTPS_PROXY=http://p:1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	repo := initTestRepo(t)

	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		Token: "test", DataDir: dataDir,
		Executor: config.ExecutorConfig{Default: "fake"},
		Env:      map[string]string{"fake": "dev.env"},
	}
	rec := &envRecordingAdapter{Adapter: fake.New(nil)}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": rec}, cfg, nil, newTestGate(t), logger)

	if _, derr := m.Dispatch(context.Background(), DispatchReq{Repo: repo, Prompt: "任意指令"}); derr != nil {
		t.Fatalf("Dispatch: %v", derr)
	}
	if len(rec.gotEnv) != 1 || rec.gotEnv[0] != "HTTPS_PROXY=http://p:1" {
		t.Fatalf("adapter 收到的 Env 不对: %v", rec.gotEnv)
	}
}

// envRecordingAdapter 包一层 fake adapter，只为记录 Start 收到的 Env。
type envRecordingAdapter struct {
	executor.Adapter
	gotEnv []string
}

func (a *envRecordingAdapter) Start(ctx context.Context, req executor.StartReq) error {
	a.gotEnv = req.Env
	return a.Adapter.Start(ctx, req)
}

// fakeVolatileAdapter 是权限随连接消亡的假 adapter（模拟 grok）。
type fakeVolatileAdapter struct {
	*chanAdapter
	resumeCalled bool
}

func (f *fakeVolatileAdapter) PermissionsVolatile() bool { return true }
func (f *fakeVolatileAdapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	f.resumeCalled = true
	return executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeReattach,
		SessionID: req.SessionID}, nil
}

// resumableChanAdapter 是支持 Resume 且权限无状态的假 adapter（模拟 opencode，
// 不实现 PermissionsVolatile）。
type resumableChanAdapter struct {
	*chanAdapter
}

func (a *resumableChanAdapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	return executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeReattach,
		SessionID: req.SessionID}, nil
}

func newResumableChanAdapter() *resumableChanAdapter {
	return &resumableChanAdapter{chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
}

// seedTaskWithPendingPermissionTicket 建一个 running 任务并挂一张未应答的权限工单。
func seedTaskWithPendingPermissionTicket(t *testing.T, st *store.Store, taskID, executorName string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: executorName,
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: taskID + ":perm-1", TaskID: taskID, Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate","permission":"Bash: ls"}`), CreatedAt: now,
	}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
}

// TestResumeRefusedWhenVolatilePermitterHasPendingTicket 固定 spec §5.2：
// grok 类 adapter 若任务尚有未决权限工单，agentd 重启后不得恢复——实测
// session/load 只恢复会话历史，不恢复未决授权请求，恢复了也永远不会前进。
func TestResumeRefusedWhenVolatilePermitterHasPendingTicket(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "t-volatile"
	seedTaskWithPendingPermissionTicket(t, st, taskID, "grok")

	ad := &fakeVolatileAdapter{chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m.ads["grok"] = ad

	if alive := m.ResumeTask(taskID); alive {
		t.Error("有未决权限工单时必须拒绝恢复")
	}
	if ad.resumeCalled {
		t.Error("必须在调用 adapter.Resume 之前就拒绝，避免建立一条永远不会前进的连接")
	}
}

// TestResumeUnaffectedForNonVolatileAdapter 保证 opencode 不被这条规则波及：
// 它的权限应答是无状态 HTTP，agentd 重启后仍可应答。
func TestResumeUnaffectedForNonVolatileAdapter(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	taskID := "t-stateless"
	seedTaskWithPendingPermissionTicket(t, st, taskID, "opencode")

	ad := newResumableChanAdapter()
	m.ads["opencode"] = ad

	if alive := m.ResumeTask(taskID); !alive {
		t.Error("无状态权限的 adapter 不应受未决工单影响")
	}
}

// TestDispatchAutoBranchStartsAtBaseCommit 是 B35 在 dispatch 全链路上的回归：
// 任务仓库 HEAD 已经前进，但派发时上送的基线是更早那个提交——新 worktree
// 必须落在基线上，不能落在仓库 HEAD 上。
func TestDispatchAutoBranchStartsAtBaseCommit(t *testing.T) {
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	base := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	writeAndCommit(t, repo, "drift.txt", "x") // 仓库 HEAD 前进，模拟执行机落后/超前
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, BaseCommit: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitAt(t, task.Workdir(), "rev-parse", "HEAD"))
	if head != base {
		t.Fatalf("新 worktree 应开在基线 %s 上，实得 %s（B35：校验了基线却从仓库 HEAD 开分支）", base, head)
	}
}

// TestDispatchRecordsBaseline 验证派发落库的基线就是实际用的起点，且领先数
// 被如实记下——这正是 08-10 事故复盘时谁都答不上来的那个数字。
func TestDispatchRecordsBaseline(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	base := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	writeAndCommit(t, repo, "one.txt", "1")
	writeAndCommit(t, repo, "two.txt", "2")
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, BaseCommit: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != base {
		t.Fatalf("落库基线应是实际起点 %s，实得 %q", base, got.BaseCommit)
	}
	if got.BaseAhead != 2 {
		t.Fatalf("任务仓库领先 2 个提交，落库实得 %d", got.BaseAhead)
	}
}

// TestDispatchExplicitBaseWinsOverBaseline 验证起点优先级：显式 --base 压过
// 决议出的基线，且此时不记领先数——用户已经明确指定了起点，「你丢了 N 个
// 提交」这句话对他毫无意义，是噪音不是信息。
func TestDispatchExplicitBaseWinsOverBaseline(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	repo := initTestRepo(t)
	explicit := strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
	baseline := writeAndCommit(t, repo, "mid.txt", "m") // 基线比 explicit 新
	writeAndCommit(t, repo, "tip.txt", "t")             // 仓库 HEAD 再前进一格
	task, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true,
		BaseCommit: baseline, Base: explicit,
	})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitAt(t, task.Workdir(), "rev-parse", "HEAD"))
	if head != explicit {
		t.Fatalf("显式 --base 应压过基线：worktree head=%s explicit=%s baseline=%s", head, explicit, baseline)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != explicit {
		t.Fatalf("落库基线应是实际用的起点 %s，实得 %q", explicit, got.BaseCommit)
	}
	if got.BaseAhead != 0 {
		t.Fatalf("显式起点不该报领先数，实得 %d", got.BaseAhead)
	}
}

// compensateFixture 造一个「PrepareWorkspace 之后必失败」的 Manager：
// DataDir 下预置名为 tasks 的普通文件，使 MkdirAll(DataDir/tasks/<id>) 失败。
func compensateFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "tasks"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: dataDir, Executor: config.ExecutorConfig{Default: "fake"}}
	m := NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return m, dataDir
}

// branchExists 报告 repo 里是否存在名为 branch 的本地分支。
func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	c := exec.Command("git", "-C", repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return c.Run() == nil
}

// actionName 让断言失败时打出可读的枚举名，而不是 0/1/2/3。
// 只服务测试可读性，故不做成生产侧的 String() 方法。
var actionName = map[branchAction]string{
	branchDelete:         "branchDelete",
	branchKeepNotOurs:    "branchKeepNotOurs",
	branchKeepTipUnknown: "branchKeepTipUnknown",
	branchKeepTipMoved:   "branchKeepTipMoved",
}

// TestDecideBranchAction 逐条钉住补偿路径的分支处置规则。
//
// 第二行（recordedTip 空 + tipErr 非 nil）是本用例存在的全部理由：它是
// 「不是本次新建的」这道闸的独占角落——旧结构里 branchTip 失败塌缩成空串，
// 与 recordedTip 的空串撞车，闸2 的 cur != recordedTip 会变成「放行删除」，
// 而该状态在真实仓库里可由悬空 symref 达成（已实测，见 spec §2.1）。
func TestDecideBranchAction(t *testing.T) {
	const (
		shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	errTip := errors.New("rev-parse 失败")

	cases := []struct {
		name        string
		recordedTip string
		currentTip  string
		tipErr      error
		want        branchAction
	}{
		{"用户自带分支：尖端取得到", "", shaA, nil, branchKeepNotOurs},
		{"用户自带分支：尖端取不到（悬空 symref）", "", "", errTip, branchKeepNotOurs},
		{"本次新建且自创建以来零提交", shaA, shaA, nil, branchDelete},
		{"本次新建但尖端已移动", shaA, shaB, nil, branchKeepTipMoved},
		{"本次新建但尖端取不到", shaA, "", errTip, branchKeepTipUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideBranchAction(tc.recordedTip, tc.currentTip, tc.tipErr)
			if got != tc.want {
				t.Fatalf("decideBranchAction(%q, %q, %v) = %s，期望 %s",
					tc.recordedTip, tc.currentTip, tc.tipErr, actionName[got], actionName[tc.want])
			}
		})
	}
}

// TestCompensateDeletesCreatedBranch 验证 managed 模式补偿把**本次新建的**分支
// 一并删掉——这正是 B39 的原始诉求：修好环境后用同一分支名重试必须能成功。
func TestCompensateDeletesCreatedBranch(t *testing.T) {
	repo := initTestRepo(t)
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, NewBranch: "e2e/retry",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if branchExists(t, repo, "e2e/retry") {
		t.Fatal("本次新建的分支应被补偿删除，否则同名重试会撞 already exists")
	}
}

// TestCompensateKeepsExistingBranch 验证 --branch <已存在分支> 模式下补偿
// **不删**分支。判别力：一个无脑删分支的实现会在这里删掉用户自己的分支。
func TestCompensateKeepsExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "mine")
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewWorktree: true, Branch: "mine",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if !branchExists(t, repo, "mine") {
		t.Fatal("已存在分支不是本次新建的，补偿绝不能删")
	}
}

// TestCompensateInPlaceRestoresPrevRef 验证原地模式（当前缺省）补偿把主仓
// 切回原分支并删掉新建分支。
//
// 判别力：这是 brainstorm 中扩大范围的那一半——旧实现 `if !ws.Managed { return }`
// 在这里直接早退，主仓会停在空分支上。
func TestCompensateInPlaceRestoresPrevRef(t *testing.T) {
	repo := initTestRepo(t)
	before := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewBranch: "e2e/inplace",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	after := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if after != before {
		t.Fatalf("原地模式补偿应把主仓切回 %s，实际停在 %s", before, after)
	}
	if branchExists(t, repo, "e2e/inplace") {
		t.Fatal("原地模式下本次新建的分支同样应被删除")
	}
}

// TestCompensateInPlaceRestoresDetached 验证 detached HEAD 起步时补偿切回原 commit。
func TestCompensateInPlaceRestoresDetached(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "--detach", "-q", head)
	m, _ := compensateFixture(t)
	if _, err := m.Dispatch(context.Background(), DispatchReq{
		Repo: repo, Prompt: "x", Executor: "fake", NewBranch: "e2e/detached",
	}); err == nil {
		t.Fatal("taskDir 创建失败场景应派发失败")
	}
	if got := gitOut(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("detached 起步应切回 %s，实际 %s", head, got)
	}
	if branchExists(t, repo, "e2e/detached") {
		t.Fatal("新建分支应被删除")
	}
}

// compensateOnlyManager 造一个只用来调 compensateWorkspace 的 Manager——
// 这三条用例不经过 Dispatch，store/hub 只需能构造出来。
func compensateOnlyManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	return NewManager(st, NewHub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, newTestGate(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestCompensateKeepsBranchWhenWorktreeRemoveFails 验证 worktree 删不掉时
// **绝不删分支**：分支还被那棵树 checkout 着，且失败现场要留给人排查。
//
// 注入方式：给一个根本没在 git 里注册过的 WorkDir，worktree remove 必然失败。
func TestCompensateKeepsBranchWhenWorktreeRemoveFails(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "e2e/stuck")
	tip := gitOut(t, repo, "rev-parse", "refs/heads/e2e/stuck")
	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, Workspace{
		Branch: "e2e/stuck", WorkDir: filepath.Join(t.TempDir(), "not-a-worktree"),
		Managed: true, NewBranchTip: tip,
	})
	if !branchExists(t, repo, "e2e/stuck") {
		t.Fatal("worktree 删除失败时分支必须保留")
	}
}

// TestCompensateKeepsBranchWhenTipMoved 验证分支尖端与创建时不符（疑似已有
// 提交）时保留分支。删分支不可逆，宁可留残留也不能删错。
func TestCompensateKeepsBranchWhenTipMoved(t *testing.T) {
	repo := initTestRepo(t)
	orig := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	staleTip := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "-q", "-b", "e2e/moved")
	writeAndCommit(t, repo, "extra.txt", "x\n") // 尖端前移，与 staleTip 不再相等
	gitT(t, repo, "checkout", "-q", orig)
	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, Workspace{
		Branch: "e2e/moved", WorkDir: repo, Managed: false,
		NewBranchTip: staleTip, PrevRef: orig,
	})
	if !branchExists(t, repo, "e2e/moved") {
		t.Fatal("尖端与创建时不符的分支必须保留，日志记 WARN 即可")
	}
}

// TestCompensateUserWorktreeRestores 验证用户自带 worktree 模式：树切回原分支、
// 新建分支删掉。这是 spec §6 表里第六行，也是「非 managed 不止原地一种」的证据。
func TestCompensateUserWorktreeRestores(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "userwt")
	gitT(t, repo, "worktree", "add", "-q", "-b", "userbase", wt)

	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "eeeeeeee-0000-0000-0000-000000000000",
		NewBranch: "e2e/userwt", Worktree: wt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != "userbase" {
		t.Fatalf("用户树的 PrevRef 应为 userbase，得到 %q", ws.PrevRef)
	}

	m := compensateOnlyManager(t)
	m.compensateWorkspace(context.Background(), repo, ws)

	if got := gitOut(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "userbase" {
		t.Fatalf("用户树应被切回 userbase，实际停在 %s", got)
	}
	if branchExists(t, repo, "e2e/userwt") {
		t.Fatal("用户树模式下本次新建的分支同样应被删除")
	}
}

// TestPermissionReuseSkipsSecondTicket 验证 B57②：同一任务内同一权限描述
// 第二次到达时不再建工单、不再叫醒审核者，而是复用首次的人工批准自动放行。
func TestPermissionReuseSkipsSecondTicket(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	permText := "external_directory: /Users/x/go/pkg/mod/github.com/coder/websocket@v1.8.15/*"

	// 第一次：升级人工 → 审核者批准 → 送达
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: permText,
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	// 第二次：同一文案、不同 perm id
	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: permText,
	}, "T1:p2")

	// 断言 1：没有新的挂起工单
	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("复用后仍有 %d 张挂起工单，期望 0：%+v", len(pending), pending)
	}

	// 断言 2：落了 permission_reuse 审计事件
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var reuse int
	for _, e := range evs {
		if e.Type == proto.EventTypePermissionReuse {
			reuse++
		}
	}
	if reuse != 1 {
		t.Fatalf("permission_reuse 事件 %d 条，期望 1", reuse)
	}

	// 断言 3：批准真的回传给了 executor
	if perms := ad.permsRec(); len(perms) == 0 || perms[len(perms)-1] != "p2:once" {
		t.Fatalf("RespondPermission 实参 = %v，期望末条 p2:once", perms)
	}
}

// TestPermissionReuseIgnoresDeny 验证只复用 allow：首次被拒后，同文案的第二次
// 仍然升级人工。自动重复拒绝会静默掐死回合，方向与 deny 原因下发正好相反。
func TestPermissionReuseIgnoresDeny(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	permText := "bash: rm -rf /tmp/x"

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: permText,
	}, "T1:p1")
	if err := st.AnswerTicket("T1:p1", "deny: 太危险"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	m.markDelivered("T1", "T1:p1")

	m.escalatePermission(context.Background(), "T1", executor.AdapterEvent{
		Type: "permission", PermissionID: "p2", Text: permText,
	}, "T1:p2")

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "T1:p2" {
		t.Fatalf("deny 之后第二次应照常出单，得到 %+v", pending)
	}
}

// TestQuestionTicketIdempotentOnReplay 验证 B58：带原生 id 的提问重放（agentd
// 重启后 executor 重发同一个 request）不产生第二张工单。
func TestQuestionTicketIdempotentOnReplay(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", QuestionID: "que_ff", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	m.handleQuestion(context.Background(), "T1", ev) // 重放

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("重放后有 %d 张挂起工单，期望 1：%+v", len(pending), pending)
	}
	if pending[0].ID != "T1:que_ff" {
		t.Fatalf("工单 id = %q，期望 T1:que_ff", pending[0].ID)
	}

	// 事件也只该有一条 question——重放不该再唤醒审核者一次
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var qn int
	for _, e := range evs {
		if e.Type == proto.EventTypeQuestion {
			qn++
		}
	}
	if qn != 1 {
		t.Fatalf("question 事件 %d 条，期望 1", qn)
	}
}

// TestQuestionReissueAfterAnswerCreatesNewTicket 钉住三岔的第三条：opencode 的
// 「答复没对上选项 → 重发工单」用的是同一个 reqID。若无脑幂等，审核者答错一次
// 之后就再也答不了，任务停在 waiting_answer 直到 stall 超时——比 B58 本身严重。
func TestQuestionReissueAfterAnswerCreatesNewTicket(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", QuestionID: "que_ff", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	if err := st.AnswerTicket("T1:que_ff", "5000ms"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}

	// 折算失败，adapter 用同一个 reqID 重发
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{
		Type: "question", QuestionID: "que_ff", Text: "上一次答复没能对上选项。选哪个？",
	})

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("重发后挂起工单 %d 张，期望 1（新单）：%+v", len(pending), pending)
	}
	if pending[0].ID == "T1:que_ff" {
		t.Fatal("重发复用了已答工单的 id，审核者将无法作答")
	}
}

// TestQuestionWithoutIDFallsBackToUUID 验证无原生 id 的 executor（claudecode /
// codex / grok 的 trailer ask）行为不变：每次提问都是一张新单。
func TestQuestionWithoutIDFallsBackToUUID(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ev := executor.AdapterEvent{Type: "question", Text: "选哪个？"}

	m.handleQuestion(context.Background(), "T1", ev)
	m.handleQuestion(context.Background(), "T1", ev)

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("无 id 的两次提问应出两张单，得到 %d 张", len(pending))
	}
}

// TestGateDecisionParsesReason 表驱动钉住 gate 应答的翻译：只有严格 "allow"
// 放行，其余一律 reject；reason 从 deny/deny: 前缀后取余文。
func TestGateDecisionParsesReason(t *testing.T) {
	cases := []struct {
		name, answer, wantDecision, wantReason string
	}{
		{"批准", "allow", "once", ""},
		{"批准带空白", "  allow  ", "once", ""},
		{"裸拒绝", "deny", "reject", ""},
		{"带原因", "deny: 改用 go build ./...", "reject", "改用 go build ./..."},
		{"带原因无空格", "deny:改用 go build", "reject", "改用 go build"},
		{"任意文本", "看着办", "reject", ""},
		{"空串", "", "reject", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, r := gateDecision(c.answer)
			if d != c.wantDecision || r != c.wantReason {
				t.Fatalf("gateDecision(%q) = (%q,%q)，期望 (%q,%q)",
					c.answer, d, r, c.wantDecision, c.wantReason)
			}
		})
	}
}

// TestDenyGuidanceRelayedOnNextQuestion 验证 B50：带原因的拒绝，其原因在下一条
// question 到达时被 Send 给 executor，且该分支不建工单、不落 waiting_answer。
func TestDenyGuidanceRelayedOnNextQuestion(t *testing.T) {
	m, st, _, ad := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidance("T1", "改用 go build ./...")
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{
		Type: "question", Text: "上一步操作因权限被拒而终止了本回合",
	})

	// 不建工单
	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("guidance 分支不应建工单，得到 %+v", pending)
	}
	// 不落 waiting_answer（否则就是「等你回答却零挂起工单」的死形态）
	task, err := st.GetTask("T1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateRunning {
		t.Fatalf("状态 = %q，期望保持 running", task.State)
	}
	// 原因真的发给了 executor
	sends := ad.sendsRec()
	if len(sends) != 1 || !strings.Contains(sends[0], "改用 go build ./...") {
		t.Fatalf("Send 记录 = %v，未包含拒绝原因", sends)
	}
	// 落了审计事件
	evs, err := st.EventsFromAsc("T1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var relayed int
	for _, e := range evs {
		if e.Type == proto.EventTypeDenyGuidanceRelayed {
			relayed++
		}
	}
	if relayed != 1 {
		t.Fatalf("deny_guidance_relayed 事件 %d 条，期望 1", relayed)
	}
}

// TestDenyGuidanceConsumedOnce 验证取走式：guidance 只抑制一条 question，
// 第二条正常出单。常驻会让后续真提问被永久吞掉，任务停在 running 无人知晓。
func TestDenyGuidanceConsumedOnce(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	m.noteDenyGuidance("T1", "改用别的办法")
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{Type: "question", Text: "问题一"})
	m.handleQuestion(context.Background(), "T1", executor.AdapterEvent{Type: "question", Text: "问题二"})

	pending, err := st.PendingTickets("T1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("第二条 question 应正常出单，挂起工单 %d 张", len(pending))
	}
}
