package orchestration

// b23314_empty_home_discipline_test.go —— B233.14 S2 行为缝的红绿判据。
//
// 缝：Manager.Dispatch → prepareTaskProfile。空 Task.HomeDir 是合法身份（沿用
// 主 HOME，B347），删除「空 HOME 拒绝非空纪律」门后，须成功把纪律落任务级
// overlay，且身份快照不被回写。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentd "github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
	"github.com/Xsxdot/handoff/internal/proto"
)

// withMainHome 把包内主 HOME 取值缝替换为测试值。
func withMainHome(t *testing.T, home string) {
	t.Helper()
	prev := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = prev })
}

func TestB23314TaskProfileHome(t *testing.T) {
	mainHome := t.TempDir()

	t.Run("空 HOME 取主 HOME", func(t *testing.T) {
		withMainHome(t, mainHome)
		got, err := taskProfileHome("")
		if err != nil || got != mainHome {
			t.Fatalf("taskProfileHome(\"\") = %q, %v；want %q, nil", got, err, mainHome)
		}
	})
	t.Run("非空绝对 HOME 原样", func(t *testing.T) {
		withMainHome(t, mainHome)
		abs := filepath.Join(t.TempDir(), "carrier")
		got, err := taskProfileHome(abs)
		if err != nil || got != abs {
			t.Fatalf("taskProfileHome(%q) = %q, %v；want %q, nil", abs, got, err, abs)
		}
	})
	t.Run("tilde 前缀展开为绝对", func(t *testing.T) {
		withMainHome(t, mainHome)
		got, err := taskProfileHome("~/.handoff/home/muse")
		want := filepath.Join(mainHome, ".handoff/home/muse")
		if err != nil || got != want {
			t.Fatalf("taskProfileHome(tilde) = %q, %v；want %q, nil", got, err, want)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("展开结果必须绝对: %q", got)
		}
	})
	t.Run("主 HOME 不可得而 taskHome 空则报错", func(t *testing.T) {
		prev := userHomeDir
		userHomeDir = func() (string, error) { return "", errors.New("no home") }
		t.Cleanup(func() { userHomeDir = prev })
		if got, err := taskProfileHome(""); err == nil {
			t.Fatalf("主 HOME 不可得必须报错，got %q", got)
		}
	})
	t.Run("主 HOME 不可得时 tilde 不得静默落相对", func(t *testing.T) {
		prev := userHomeDir
		userHomeDir = func() (string, error) { return "", errors.New("no home") }
		t.Cleanup(func() { userHomeDir = prev })
		if got, err := taskProfileHome("~/x"); err == nil {
			t.Fatalf("tilde 展开失败必须报错，不得返回相对路径 %q", got)
		}
	})
	t.Run("非 tilde 相对 HOME 报错不静默落相对", func(t *testing.T) {
		withMainHome(t, mainHome)
		if got, err := taskProfileHome("relative/home"); err == nil {
			t.Fatalf("相对写入点必须报错，不得落 %q", got)
		}
	})
}

// 主判据 #13/#14/#15/#16：空 HOME + 非空纪律经 Dispatch 成功；写入点为主 HOME
// 绝对路径、Isolated=false；Task.HomeDir 仍空；Rules/Skills 空。
func TestB23314EmptyHomeDisciplinePreparesProfile(t *testing.T) {
	mainHome := t.TempDir()
	withMainHome(t, mainHome)

	profile := &recordingProfile{}
	ad := &profileRecordingAdapter{
		chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		profile:     profile,
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	const discipline = "空 HOME 纪律\n逐字节保留\r\n"
	emptyHome := ""
	task, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "empty home discipline", Executor: "fake",
		HomeDir: &emptyHome, DisciplineText: discipline,
	})
	if err != nil {
		t.Fatalf("空 HOME + 非空纪律应成功: %v", err)
	}
	reqs := profile.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("Profile.Prepare 调用次数=%d, want 1（%+v）", len(reqs), reqs)
	}
	req := reqs[0]
	if req.HomeDir == "" || !filepath.IsAbs(req.HomeDir) {
		t.Fatalf("ProfileReq.HomeDir=%q, want 非空绝对路径", req.HomeDir)
	}
	if req.HomeDir != mainHome {
		t.Fatalf("写入点=%q, want 主 HOME %q", req.HomeDir, mainHome)
	}
	if req.Isolated {
		t.Fatal("空 HOME 沿用主 HOME，Isolated 必须 false")
	}
	if len(req.TaskOverlay) != 1 || req.TaskOverlay[0].Content != discipline {
		t.Fatalf("overlay=%+v, want 一条逐字节纪律", req.TaskOverlay)
	}
	if len(req.Rules) != 0 || len(req.Skills) != 0 {
		t.Fatalf("Rules/Skills 必须为空，got %d/%d", len(req.Rules), len(req.Skills))
	}
	got, gerr := st.GetTask(task.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.HomeDir != "" {
		t.Fatalf("Task.HomeDir 被回写为 %q，身份快照必须保持空串（B347）", got.HomeDir)
	}
	if got.State != proto.TaskStateRunning {
		t.Fatalf("任务状态=%s, want running", got.State)
	}
}

// #18：非空 Task.HomeDir 时 Isolated=true，写入点为该非空绝对路径。
// 锁住「展开后的绝对路径不等于 Isolated=true」的反向：非空 HOME 必须隔离。
func TestB23314NonEmptyHomeDisciplineIsIsolated(t *testing.T) {
	mainHome := t.TempDir()
	withMainHome(t, mainHome)

	profile := &recordingProfile{}
	ad := &profileRecordingAdapter{
		chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		profile:     profile,
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	carrierHome := filepath.Join(t.TempDir(), "carrier")
	const discipline = "非空 HOME 纪律"
	task, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "non-empty home discipline", Executor: "fake",
		HomeDir: &carrierHome, DisciplineText: discipline,
	})
	if err != nil {
		t.Fatalf("非空 HOME + 非空纪律应成功: %v", err)
	}
	reqs := profile.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("Profile.Prepare 调用次数=%d, want 1（%+v）", len(reqs), reqs)
	}
	req := reqs[0]
	if !req.Isolated {
		t.Fatalf("非空 HOME 必须 Isolated=true，got HomeDir=%q", req.HomeDir)
	}
	if req.HomeDir != carrierHome {
		t.Fatalf("写入点=%q, want 载体 HOME %q", req.HomeDir, carrierHome)
	}
	got, gerr := st.GetTask(task.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.HomeDir != carrierHome {
		t.Fatalf("Task.HomeDir=%q, want 原字面 %q", got.HomeDir, carrierHome)
	}
}

// realProfileAdapter 把真实 executor.Profile 挂到可记录事件的 adapter 上，
// 用于「真落盘」边界测试（现有 profileRecordingAdapter 只接受 *recordingProfile）。
type realProfileAdapter struct {
	*chanAdapter
	profile executor.Profile
}

func (a *realProfileAdapter) Profile() executor.Profile { return a.profile }

// 真落盘边界（防 recordingProfile 假绿）：真实 opencode.Profile.Prepare + 临时
// 主 HOME，断 overlay 真在 <写入点>/.handoff/task-overlay/<task>/discipline.md，
// 且载体全局规则/skills 树不被本次 overlay 触碰。
func TestB23314EmptyHomeDisciplineReallyWritesOverlay(t *testing.T) {
	mainHome := t.TempDir()
	withMainHome(t, mainHome)

	real := opencode.New(nil)
	ad := &realProfileAdapter{
		chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		profile:     real.Profile(),
	}
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	emptyHome := ""
	task, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "real overlay", Executor: "fake",
		HomeDir: &emptyHome, DisciplineText: "REAL-OVERLAY",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	overlay := filepath.Join(mainHome, ".handoff", "task-overlay", task.ID, "discipline.md")
	data, rerr := os.ReadFile(overlay)
	if rerr != nil {
		t.Fatalf("overlay 未真落盘 %q: %v", overlay, rerr)
	}
	if string(data) != "REAL-OVERLAY" {
		t.Fatalf("overlay 内容=%q, want REAL-OVERLAY", string(data))
	}
	rulePath := filepath.Join(mainHome, ".config", "opencode", "AGENTS.md")
	if _, serr := os.Stat(rulePath); !os.IsNotExist(serr) {
		t.Fatalf("载体全局规则不应被本次 overlay 写入: %q stat err=%v", rulePath, serr)
	}
}

// #19：主 HOME 不可得时拒派并落 failed，不静默吞纪律。
func TestB23314EmptyHomeDisciplineRejectsWhenMainHomeUnavailable(t *testing.T) {
	prev := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("主 HOME 不可得（注入）") }
	t.Cleanup(func() { userHomeDir = prev })

	profile := &recordingProfile{}
	ad := &profileRecordingAdapter{
		chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		profile:     profile,
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	emptyHome := ""
	if _, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "no main home", Executor: "fake",
		HomeDir: &emptyHome, DisciplineText: "必须拒派",
	}); err == nil {
		t.Fatal("主 HOME 不可得必须拒派")
	}
	tasks, lerr := st.ListTasks()
	if lerr != nil || len(tasks) != 1 || tasks[0].State != proto.TaskStateFailed {
		t.Fatalf("拒派后应留一条 failed 任务: err=%v tasks=%+v", lerr, tasks)
	}
}
