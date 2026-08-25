// dispatch_discipline_test.go —— B229 §2.5 执行机侧行为契约的判据。
//
// 职责：
//   - 收文即用：DispatchReq.DisciplineText 逐字节成为注入正文，本机零解析
//   - 反向断言：点名但没收到正文 = 拒派（防「查不到就兜底」的降级复活）
//   - 显式空正文 + 未点名 = 不注入任何块（机器合法形态）
//   - 正文落盘先于 executor 启动；continue/resume 消费首派落盘正文
//
// 边界：白盒（package agentd）；协调者侧缝 1 的组装与拒发闸归
// internal/discipline/dispatch.go 与派发侧接线卡，此处只钉执行机行为。
package agentd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// discTextPath 返回任务目录里纪律正文落盘文件的全路径。
func discTextPath(dataDir, taskID string) string {
	return filepath.Join(dataDir, "tasks", taskID, disciplineFileName)
}

// TestDispatchConsumesDeliveredText 点名 + 下发正文：注入正文逐字节等于下发值，
// 本机 <DataDir>/discipline/ 不存在也照样成功；版本号随任务元数据落盘可读回。
func TestDispatchConsumesDeliveredText(t *testing.T) {
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "codex",
		Discipline: "review", DisciplineText: "X", DisciplineVersion: 3,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := ad.lastStartReq().Discipline; got != "X" {
		t.Fatalf("注入正文应逐字节等于下发的 X，实得 %q（%d 字节）", got, len(got))
	}
	// 本地纪律目录退役：<DataDir>/discipline 不存在不是问题，更不被读取
	if _, statErr := os.Stat(filepath.Join(m.cfg.DataDir, "discipline")); !os.IsNotExist(statErr) {
		t.Fatalf("<DataDir>/discipline 应保持不存在，stat err = %v", statErr)
	}
	// 正文落任务目录，供 continue/resume 消费同一份世界
	persisted, readErr := os.ReadFile(discTextPath(m.cfg.DataDir, task.ID))
	if readErr != nil {
		t.Fatalf("纪律正文未落盘: %v", readErr)
	}
	if string(persisted) != "X" {
		t.Fatalf("落盘正文 = %q, want X", string(persisted))
	}
	// 版本号随任务元数据落盘并有读回断言
	cur, gerr := st.GetTask(task.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if cur.DisciplineVersion != 3 || cur.DisciplineName != "review" {
		t.Fatalf("落盘任务 version/name = %d/%q, want 3/review", cur.DisciplineVersion, cur.DisciplineName)
	}
}

// TestDispatchRefusesNamedWithoutDeliveredText 反向断言（防降级复活）：
// 点名非空而下发正文为空 → 拒派。磁盘上有同名文件也一样拒——
// 这条在有人加回「本地盘/内置」兜底分支时变红。
func TestDispatchRefusesNamedWithoutDeliveredText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seedFn func(m *Manager)
	}{
		{name: "本地目录不存在", seedFn: func(m *Manager) {}},
		{name: "磁盘有同名文件仍拒", seedFn: func(m *Manager) {
			dir := filepath.Join(m.cfg.DataDir, "discipline")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("本地残留正文"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
			m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
			tc.seedFn(m)
			repo := initTestRepo(t)
			pid := registerTestProject(t, m, repo)

			task, err := m.Dispatch(context.Background(), DispatchReq{
				ProjectID: pid, Prompt: "x", Executor: "codex", Discipline: "review",
			})
			if err == nil {
				t.Fatalf("点名无正文必须拒派，实得任务 %+v", task)
			}
			if !strings.Contains(err.Error(), "review") {
				t.Fatalf("错误应点名缺正文的块名: %v", err)
			}
			if tasks, lerr := st.ListTasks(); lerr != nil || len(tasks) != 0 {
				t.Fatalf("拒派必须干净（不留任务行）: %v %+v", lerr, tasks)
			}
			if req := ad.lastStartReq(); req.Task.ID != "" {
				t.Fatal("拒派不得启动 executor")
			}
		})
	}
}

// TestDispatchEmptyTextNoNameInjectsNothing §2.5 第 3 条：显式空正文且未点名
// 是结论不是缺失——不注入任何块，执行机不得自己找一块来注入。
func TestDispatchEmptyTextNoNameInjectsNothing(t *testing.T) {
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "codex",
	})
	if err != nil {
		t.Fatalf("空正文派发应成功: %v", err)
	}
	if got := ad.lastStartReq().Discipline; got != "" {
		t.Fatalf("未点名无正文应零注入，实得 %d 字节", len(got))
	}
	if _, statErr := os.Stat(discTextPath(m.cfg.DataDir, task.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("零注入不应留正文文件，stat err = %v", statErr)
	}
}

// startProbeAdapter 在 Start 现场读任务目录里的落盘正文并记录——
// 用于锁「先落盘后启动」的顺序。
type startProbeAdapter struct {
	chanAdapter
	mu      sync.Mutex
	atStart string
}

func (a *startProbeAdapter) Start(ctx context.Context, req executor.StartReq) error {
	data, _ := os.ReadFile(filepath.Join(req.TaskDir, disciplineFileName))
	a.mu.Lock()
	a.atStart = string(data)
	a.mu.Unlock()
	return a.chanAdapter.Start(ctx, req)
}

// TestDispatchPersistsDisciplineBeforeExecutorStarts 顺序锁：
// executor 启动那一刻落盘正文必须已经在盘上。
func TestDispatchPersistsDisciplineBeforeExecutorStarts(t *testing.T) {
	probe := &startProbeAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": probe}, "codex")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	if _, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "codex",
		DisciplineText: "PERSIST-ME", DisciplineVersion: 1,
	}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	probe.mu.Lock()
	got := probe.atStart
	probe.mu.Unlock()
	if got != "PERSIST-ME" {
		t.Fatalf("Start 现场落盘正文 = %q, want PERSIST-ME（先落盘后启动被破坏）", got)
	}
}

// TestDispatchSkipsStartWhenPersistFails 落盘失败 → 不启动、不留任务行。
func TestDispatchSkipsStartWhenPersistFails(t *testing.T) {
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"codex": ad}, "codex")
	boom := errors.New("磁盘满（注入）")
	m.writeTaskFile = func(dir, name string, data []byte) error {
		if name == disciplineFileName {
			return boom
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0o600)
	}
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)

	_, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "codex",
		DisciplineText: "X", DisciplineVersion: 2,
	})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("落盘失败应原样上抛，实得 %v", err)
	}
	if req := ad.lastStartReq(); req.Task.ID != "" {
		t.Fatal("落盘失败不得启动 executor")
	}
	if tasks, lerr := st.ListTasks(); lerr != nil || len(tasks) != 0 {
		t.Fatalf("落盘失败不得留任务行: %v %+v", lerr, tasks)
	}
}

// TestContinueConsumesPersistedDisciplineText 冷恢复续接消费首派落盘正文，
// 逐字节回注——不再按名字重新解析。
func TestContinueConsumesPersistedDisciplineText(t *testing.T) {
	const body = "FIRST\nDISPATCH\r\nBODY"
	ad := &ladderAdapter{
		chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome:     executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold, SessionID: "sess-1"},
	}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		DisciplineName: "review", State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})
	if err := os.MkdirAll(filepath.Dir(discTextPath(m.cfg.DataDir, "t1")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(discTextPath(m.cfg.DataDir, "t1"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	ad.mu.Lock()
	got := ad.gotReq
	ad.mu.Unlock()
	if !got.Cold {
		t.Fatal("该路径必须 Cold=true")
	}
	if got.Discipline != body {
		t.Fatalf("续接注入应逐字节等于首派落盘正文，实得 %q（%d 字节）", got.Discipline, len(got.Discipline))
	}
}

// TestContinueColdNamedWithoutPersistedTextIsRefused 点名任务缺落盘正文：
// Cold 续接拒绝（沿用既有不对称的拒绝半边）；未点名沿用降级不阻断。
func TestContinueColdNamedWithoutPersistedTextIsRefused(t *testing.T) {
	for _, tc := range []struct {
		label        string
		discName     string
		wantErr      bool
		wantResume   bool
		wantInjected bool
	}{
		{label: "named/refuse", discName: "review", wantErr: true},
		{label: "unnamed/degrade", discName: "", wantResume: true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			ad := &ladderAdapter{
				chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
				outcome:     executor.ResumeOutcome{Alive: true, SessionID: "sess-1"},
			}
			m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
				DisciplineName: tc.discName, State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

			err := m.Continue(context.Background(), "t1", "继续干")
			if tc.wantErr {
				if err == nil {
					t.Fatal("点名任务缺落盘正文必须拒绝 Cold 续接")
				}
				if !strings.Contains(err.Error(), tc.discName) {
					t.Fatalf("错误应点明块名 %q: %v", tc.discName, err)
				}
				ad.mu.Lock()
				called := ad.gotReq.TaskID != ""
				ad.mu.Unlock()
				if called {
					t.Fatal("拒绝路径不得触发恢复")
				}
				return
			}
			if err != nil {
				t.Fatalf("未点名缺正文沿用降级，不应阻断续接: %v", err)
			}
			ad.mu.Lock()
			got := ad.gotReq
			ad.mu.Unlock()
			if got.TaskID == "" {
				t.Fatal("降级路径仍应完成恢复")
			}
			if got.Discipline != "" {
				t.Fatalf("缺正文降级应零注入，实得 %d 字节", len(got.Discipline))
			}
		})
	}
}

// TestResumeHotReconnectUsesPersistedText 热重连消费落盘正文；
// 缺正文记 Error 但**不阻断**恢复（既有不对称的另一半）。
func TestResumeHotReconnectUsesPersistedText(t *testing.T) {
	for _, tc := range []struct {
		label string
		body  string
	}{
		{label: "有落盘正文逐字节回注", body: "HOT\nRECONNECT"},
		{label: "缺正文不阻断", body: ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			ad := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
			m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
				DisciplineName: "review", State: proto.TaskStateRunning, ExecutorSession: "sess-1"})
			if tc.body != "" {
				if err := os.MkdirAll(filepath.Dir(discTextPath(m.cfg.DataDir, "t1")), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(discTextPath(m.cfg.DataDir, "t1"), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if alive := m.ResumeTask("t1"); alive {
				t.Fatal("Resume 返回不存活时 ResumeTask 应为 false")
			}
			ad.mu.Lock()
			got := ad.got
			ad.mu.Unlock()
			if got.TaskID != "t1" {
				t.Fatalf("热重连缺正文也不得阻断恢复尝试（Resume 未被调用）: %+v", got)
			}
			if got.Discipline != tc.body {
				t.Fatalf("回注正文 = %q, want %q", got.Discipline, tc.body)
			}
		})
	}
}
