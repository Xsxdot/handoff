package orchestration

// b452_readonly_test.go —— B452 S4 只读形态与 S8 产物目录在执行机编排一侧的行为（V4、V7 执行机条）。
//
// 入口一律是真实 Manager.Dispatch（真 git 仓 + fake 执行器）；只在 adapter 外包一层记录 StartReq。
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// b452StartRecorder 在 fake 外记录 StartReq（PlanContent/Env/TaskDir）。
type b452StartRecorder struct {
	executor.Adapter
	mu   sync.Mutex
	reqs []executor.StartReq
}

func (a *b452StartRecorder) Name() string { return "fake" }
func (a *b452StartRecorder) Report() executor.CapabilityReport {
	return executor.CapabilityReport{Harness: "fake",
		Caps: []executor.CapabilityDecl{{Name: executor.CapExecution, Supported: true}}}
}
func (a *b452StartRecorder) Start(ctx context.Context, req executor.StartReq) error {
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	return a.Adapter.Start(ctx, req)
}
func (a *b452StartRecorder) last() executor.StartReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reqs[len(a.reqs)-1]
}

// b452Notifier 记录终态回调。
type b452Notifier struct {
	mu    sync.Mutex
	tasks []proto.Task
}

func (n *b452Notifier) TaskTerminal(task *proto.Task) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tasks = append(n.tasks, *task)
}
func (n *b452Notifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.tasks)
}

type b452Env struct {
	m    *Manager
	st   *store.Store
	fk   *fake.Fake
	rec  *b452StartRecorder
	note *b452Notifier
	repo string
	pid  string
}

func newB452Env(t *testing.T, script []fake.Step) *b452Env {
	t.Helper()
	fk := fake.New(script)
	rec := &b452StartRecorder{Adapter: fk}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": rec}, "fake")
	note := &b452Notifier{}
	m.SetTerminalNotifier(note)
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)
	return &b452Env{m: m, st: st, fk: fk, rec: rec, note: note, repo: repo, pid: pid}
}

func (e *b452Env) eventTypes(t *testing.T, id string) []proto.EventType {
	t.Helper()
	evs, err := e.st.EventsFrom(id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.EventType
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func indexOfType(types []proto.EventType, want proto.EventType) int {
	for i, ty := range types {
		if ty == want {
			return i
		}
	}
	return -1
}

func TestB452ReadonlyDispatchDetachedNoBranch(t *testing.T) {
	e := newB452Env(t, nil)
	base := gitOut(t, e.repo, "rev-parse", "HEAD")
	// 调用方仓的未提交改动：只读派发照常受理，且改动不进托管树
	if err := os.WriteFile(filepath.Join(e.repo, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refsBefore := gitOut(t, e.repo, "for-each-ref", "refs/heads")
	task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "调查", Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatalf("只读派发失败: %v", err)
	}
	if task.Branch != "" || task.Mode != proto.TaskModeReadonly || !task.WorktreeManaged {
		t.Fatalf("只读任务 = branch:%q mode:%q managed:%v", task.Branch, task.Mode, task.WorktreeManaged)
	}
	stored, _ := e.st.GetTask(task.ID)
	if stored.Mode != proto.TaskModeReadonly || stored.Branch != "" {
		t.Fatalf("落库只读任务 mode=%q branch=%q", stored.Mode, stored.Branch)
	}
	if head := gitOut(t, task.WorkDir, "rev-parse", "HEAD"); head != base || task.BaseCommit != base {
		t.Fatalf("托管树 HEAD=%s BaseCommit=%s want %s", head, task.BaseCommit, base)
	}
	if cur := gitOut(t, task.WorkDir, "branch", "--show-current"); cur != "" {
		t.Fatalf("托管树应 detached，得分支 %q", cur)
	}
	if b, _ := os.ReadFile(filepath.Join(task.WorkDir, "README.md")); string(b) != "# repo\n" {
		t.Fatalf("脏改动不应进托管树: %q", b)
	}
	if after := gitOut(t, e.repo, "for-each-ref", "refs/heads"); after != refsBefore {
		t.Fatalf("只读派发不得新增分支：%q → %q", refsBefore, after)
	}
}

func TestB452ReadonlyCompletedAutoArchives(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: true, Summary: "s", FinalText: "## 结论\nok"}}})
	task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "调查", Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := TaskArtifactsDir(e.m.cfg.DataDir, task.ID)
	if err := os.WriteFile(filepath.Join(artifacts, "report.md"), []byte("长报告"), 0o600); err != nil {
		t.Fatalf("产物目录应可写: %v", err)
	}
	eventually(t, 5*time.Second, "只读任务自动归档", func() bool {
		cur, err := e.st.GetTask(task.ID)
		return err == nil && cur.State == proto.TaskStateCompleted && e.note.count() == 1
	})
	types := e.eventTypes(t, task.ID)
	ci, ai := indexOfType(types, proto.EventTypeCompleted), indexOfType(types, proto.EventTypeArchived)
	if ci < 0 || ai < 0 || ai < ci {
		t.Fatalf("事件序列应 completed → archived，得 %v", types)
	}
	evs, _ := e.st.EventsFrom(task.ID, 0, 500)
	var archived proto.ArchivedPayload
	for _, ev := range evs {
		if ev.Type == proto.EventTypeArchived {
			_ = json.Unmarshal(ev.Payload, &archived)
		}
	}
	if archived.Note != ReadonlyAutoArchiveNote {
		t.Fatalf("archived note = %q want %q", archived.Note, ReadonlyAutoArchiveNote)
	}
	if _, err := os.Stat(task.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("自动归档后托管树应已回收: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(artifacts, "report.md")); err != nil || string(b) != "长报告" {
		t.Fatalf("回收工作树后产物应原样保留: %q %v", b, err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := e.note.count(); n != 1 {
		t.Fatalf("终态回调应恰一次，得 %d", n)
	}
	e.note.mu.Lock()
	snap := e.note.tasks[0]
	e.note.mu.Unlock()
	if snap.ID != task.ID || snap.State != proto.TaskStateCompleted {
		t.Fatalf("回调快照 = %+v", snap)
	}
}

func TestB452ReadonlyTurnFailedStaysForReview(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: "跑挂了"}}})
	task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "调查", Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatal(err)
	}
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "turn_failed 落库", func() bool {
		return indexOfType(e.eventTypes(t, task.ID), proto.EventTypeTurnFailed) >= 0
	})
	time.Sleep(300 * time.Millisecond)
	cur, _ := e.st.GetTask(task.ID)
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("turn_failed 不自动归档，应停 waiting_review，得 %s", cur.State)
	}
	if _, err := os.Stat(task.WorkDir); err != nil {
		t.Fatalf("turn_failed 后托管树应保留: %v", err)
	}
	if n := e.note.count(); n != 0 {
		t.Fatalf("turn_failed 不得触发终态回调，得 %d", n)
	}
}

// TestB452ReadonlyAutoArchiveRacesExternalDone：自动归档与外部 done 并发，Claimed 去重保证
// 「回调次数 + 外部 Claimed 次数」恰为 1——名额只归还一次。
func TestB452ReadonlyAutoArchiveRacesExternalDone(t *testing.T) {
	for i := 0; i < 5; i++ {
		e := newB452Env(t, nil)
		task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "调查", Mode: proto.TaskModeReadonly})
		if err != nil {
			t.Fatal(err)
		}
		events, cancel := e.m.hub.Subscribe(task.ID)
		extClaimed := make(chan bool, 1)
		go func() {
			for ev := range events {
				if ev.Type == proto.EventTypeCompleted {
					out, err := e.m.Done(context.Background(), task.ID, "人工 done")
					extClaimed <- err == nil && out.Claimed
					return
				}
			}
			extClaimed <- false
		}()
		e.fk.Add(task.ID, fake.Step{Finish: executor.Result{OK: true, Summary: "s"}})
		_ = e.fk.Send(context.Background(), task.ID, "go")
		ext := <-extClaimed
		cancel()
		eventually(t, 3*time.Second, "任务 completed", func() bool {
			cur, err := e.st.GetTask(task.ID)
			return err == nil && cur.State == proto.TaskStateCompleted
		})
		time.Sleep(200 * time.Millisecond)
		total := e.note.count()
		if ext {
			total++
		}
		if total != 1 {
			t.Fatalf("第 %d 轮：回调 %d 次 + 外部 Claimed=%v，归还次数应恰为 1", i, e.note.count(), ext)
		}
	}
}

func TestB452ArchiveCompletedReadonlyOnStartup(t *testing.T) {
	e := newB452Env(t, nil)
	now := time.Now().UTC()
	seed := func(id, mode string, last proto.EventType) {
		mustCreateTask(t, e.st, &proto.Task{ID: id, RepoPath: e.repo, Executor: "fake", Mode: mode,
			Carrier: "c1", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now})
		for _, st := range []proto.TaskState{proto.TaskStateRunning, proto.TaskStateWaitingReview} {
			if err := e.st.UpdateTaskState(id, st); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.st.AppendEvent(id, last, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.AppendEvent(id, proto.EventTypeProgress, map[string]any{"text": "尾巴"}); err != nil {
			t.Fatal(err)
		}
	}
	seed("ro-done", proto.TaskModeReadonly, proto.EventTypeCompleted)
	seed("ro-failed", proto.TaskModeReadonly, proto.EventTypeTurnFailed)
	seed("br-done", "", proto.EventTypeCompleted)
	if n := e.m.ArchiveCompletedReadonly(); n != 1 {
		t.Fatalf("启动补归档应恰处理 1 个任务，得 %d", n)
	}
	for id, want := range map[string]proto.TaskState{
		"ro-done": proto.TaskStateCompleted, "ro-failed": proto.TaskStateWaitingReview, "br-done": proto.TaskStateWaitingReview,
	} {
		cur, _ := e.st.GetTask(id)
		if cur.State != want {
			t.Fatalf("%s 状态 %s want %s", id, cur.State, want)
		}
	}
	if n := e.note.count(); n != 1 || e.note.tasks[0].ID != "ro-done" {
		t.Fatalf("启动补归档回调应恰一次（ro-done），得 %d %+v", n, e.note.tasks)
	}
}

func TestB452ReadonlyRejectsBranchParamsAndBadMode(t *testing.T) {
	e := newB452Env(t, nil)
	wt := filepath.Join(t.TempDir(), "wt")
	for name, req := range map[string]DispatchReq{
		"branch":     {Branch: "main"},
		"new-branch": {NewBranch: "x"},
		"worktree":   {Worktree: wt},
	} {
		req.ProjectID, req.Prompt, req.Mode = e.pid, "x", proto.TaskModeReadonly
		if _, err := e.m.Dispatch(context.Background(), req); !errors.Is(err, workspace.ErrBadWorkspaceReq) {
			t.Fatalf("%s × readonly 应 ErrBadWorkspaceReq，得 %v", name, err)
		}
	}
	if _, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "x", Mode: "ro"}); !errors.Is(err, workspace.ErrBadDispatchRequest) ||
		!strings.Contains(err.Error(), "readonly") {
		t.Fatalf("mode=ro 应 ErrBadDispatchRequest 且点名合法值，得 %v", err)
	}
	tasks, _ := e.st.ListTasks()
	if len(tasks) != 0 {
		t.Fatalf("拒发不得建任务，得 %d", len(tasks))
	}
	entries, _ := os.ReadDir(filepath.Join(e.m.cfg.DataDir, "worktrees"))
	if len(entries) != 0 {
		t.Fatalf("拒发不得建工作树，得 %v", entries)
	}
}

func TestB452ReadonlyBaseCommitMissingRejected(t *testing.T) {
	e := newB452Env(t, nil)
	_, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "x",
		Mode: proto.TaskModeReadonly, BaseCommit: strings.Repeat("a", 40)})
	if !errors.Is(err, workspace.ErrBaseCommitMissing) {
		t.Fatalf("基线不在执行机仓应 ErrBaseCommitMissing，得 %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(e.m.cfg.DataDir, "worktrees"))
	if len(entries) != 0 {
		t.Fatalf("拒发不得建工作树，得 %v", entries)
	}
}

func TestB452ReadonlyStopRetainsWorktree(t *testing.T) {
	e := newB452Env(t, nil)
	task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "x", Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Stop(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(task.WorkDir); err != nil {
		t.Fatalf("Stop 只读任务应留树（现状语义）: %v", err)
	}
	if n := e.note.count(); n != 0 {
		t.Fatalf("Stop 走 HTTP 层归还，编排回调不得触发，得 %d", n)
	}
}

// V7 执行机条：产物目录对所有任务建（0700）、产物提示词小节只给只读形态（Q7 乙）。
func TestB452ArtifactsDirAndPromptSection(t *testing.T) {
	e := newB452Env(t, nil)
	for _, mode := range []string{proto.TaskModeReadonly, ""} {
		task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: "本次任务正文",
			Mode: mode, NewWorktree: true})
		if err != nil {
			t.Fatalf("mode=%q 派发: %v", mode, err)
		}
		dir := TaskArtifactsDir(e.m.cfg.DataDir, task.ID)
		if dir != filepath.Join(e.m.cfg.DataDir, "tasks", task.ID, "artifacts") {
			t.Fatalf("产物目录布局 = %s", dir)
		}
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("mode=%q 产物目录应存在且 0700: %v %v", mode, fi, err)
		}
		req := e.rec.last()
		hasSection := strings.Contains(req.PlanContent, "## 产物目录")
		if mode == proto.TaskModeReadonly {
			if !hasSection || !strings.Contains(req.PlanContent, dir) || !strings.Contains(req.PlanContent, proto.ArtifactsEnvVar) ||
				!strings.HasPrefix(req.PlanContent, "本次任务正文") {
				t.Fatalf("只读任务提示词应以正文开头并含产物目录小节与路径：\n%s", req.PlanContent)
			}
		} else if hasSection || req.PlanContent != "本次任务正文" {
			t.Fatalf("分支形态提示词应零变化，得：\n%s", req.PlanContent)
		}
		if _, err := e.m.Stop(context.Background(), task.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// V7：续接冷恢复与启动恢复两条路径，产物目录存在时都把 HANDOFF_ARTIFACTS_DIR 注入执行器环境；
// 目录不存在（B452 之前派发的任务）时不注入、不凭空造目录。
func TestB452ResumePathsInjectArtifactsEnv(t *testing.T) {
	want := func(m *Manager, id string) string {
		return proto.ArtifactsEnvVar + "=" + TaskArtifactsDir(m.cfg.DataDir, id)
	}
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	// 启动恢复（ResumeTask）
	rr := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": rr}, "fake")
	for _, id := range []string{"with-dir", "no-dir"} {
		mustCreateTask(t, st, &proto.Task{ID: id, RepoPath: "/r", Executor: "fake",
			State: proto.TaskStateRunning, ExecutorSession: "s"})
	}
	if err := ensureArtifactsDir(TaskArtifactsDir(m.cfg.DataDir, "with-dir")); err != nil {
		t.Fatal(err)
	}
	m.ResumeTask("with-dir")
	rr.mu.Lock()
	got := rr.got
	rr.mu.Unlock()
	if !has(got.Env, want(m, "with-dir")) {
		t.Fatalf("启动恢复 env 应含产物变量: %v", got.Env)
	}
	m.ResumeTask("no-dir")
	rr.mu.Lock()
	got = rr.got
	rr.mu.Unlock()
	for _, e := range got.Env {
		if strings.HasPrefix(e, proto.ArtifactsEnvVar+"=") {
			t.Fatalf("无产物目录的旧任务不应注入: %v", got.Env)
		}
	}
	if _, err := os.Stat(TaskArtifactsDir(m.cfg.DataDir, "no-dir")); !os.IsNotExist(err) {
		t.Fatalf("恢复不得凭空建产物目录: %v", err)
	}
	// 续接冷恢复（Continue → resumeForContinue）
	la := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold, SessionID: "s"}}
	m2, st2, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": la}, "fake")
	mustCreateTask(t, st2, &proto.Task{ID: "cont", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingReview, ExecutorSession: "s"})
	if err := ensureArtifactsDir(TaskArtifactsDir(m2.cfg.DataDir, "cont")); err != nil {
		t.Fatal(err)
	}
	if err := m2.Continue(context.Background(), "cont", "继续"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	la.mu.Lock()
	gotC := la.gotReq
	la.mu.Unlock()
	if !has(gotC.Env, want(m2, "cont")) {
		t.Fatalf("续接冷恢复 env 应含产物变量: %v", gotC.Env)
	}
}

// V7 保留期清扫：终态且超期 → 只删 artifacts/，任务目录其他文件保留；非终态、未超期、保留期 0 → 不动。
func TestB452SweepExpiredArtifacts(t *testing.T) {
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake")
	now := time.Now().UTC()
	old := now.Add(-200 * time.Hour)
	seed := func(id string, state proto.TaskState, updated time.Time) string {
		mustCreateTask(t, st, &proto.Task{ID: id, RepoPath: "/r", State: state, CreatedAt: updated, UpdatedAt: updated})
		dir := TaskArtifactsDir(m.cfg.DataDir, id)
		if err := ensureArtifactsDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "r.md"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.cfg.DataDir, "tasks", id, "prompt.md"), []byte("p"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	expired := seed("expired", proto.TaskStateCompleted, old)
	failedOld := seed("failed-old", proto.TaskStateFailed, old)
	running := seed("running-old", proto.TaskStateWaitingReview, old)
	fresh := seed("fresh", proto.TaskStateCompleted, now)

	if n := m.SweepExpiredArtifacts(now, 0); n != 0 {
		t.Fatalf("保留期 0 不得清理，删了 %d", n)
	}
	if n := m.SweepExpiredArtifacts(now, DefaultArtifactsRetentionForTest); n != 2 {
		t.Fatalf("应恰清理 2 个（completed/failed 超期），得 %d", n)
	}
	for _, d := range []string{expired, failedOld} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("%s 应被清理: %v", d, err)
		}
	}
	for _, d := range []string{running, fresh} {
		if _, err := os.Stat(filepath.Join(d, "r.md")); err != nil {
			t.Fatalf("%s 不应被清理: %v", d, err)
		}
	}
	for _, id := range []string{"expired", "failed-old"} {
		if _, err := os.Stat(filepath.Join(m.cfg.DataDir, "tasks", id, "prompt.md")); err != nil {
			t.Fatalf("清扫只删 artifacts/，任务目录其他文件应在: %v", err)
		}
	}
}

// DefaultArtifactsRetentionForTest 与 config.DefaultArtifactsRetention 同值（编排包不引 config 常量做断言）。
const DefaultArtifactsRetentionForTest = 168 * time.Hour
