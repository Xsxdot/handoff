package agentd

// B233.5 T3 接缝测试：Manager 只消费网关已经绑定的身份并写入任务快照。

import (
	"context"
	"github.com/Xsxdot/handoff/internal/workspace"
	"os"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
)

func TestDispatchPersistsFrozenIdentity(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	if m.Workspace() == nil {
		m.SetWorkspace(workspace.NewCapability())
	}
	pid := registerTestProject(t, m, repo)
	home := "~/.handoff/home/muse"
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x",
		Target: "linux-01", Executor: "fake", Carrier: "muse", HomeDir: &home, Model: "gpt-y",
		NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Carrier != "muse" || got.Target != "linux-01" || got.Executor != "fake" || got.HomeDir != home {
		t.Fatalf("冻结身份未落盘: %+v", got)
	}
}

func TestDispatchEmptyExecutorDoesNotInventCarrier(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, _, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("旧调用空 Executor 只许路由 adapter，实得 %v", err)
	}
	if task.Carrier != "" {
		t.Fatalf("空 Receiver 路径不得把 default 写成 Carrier，实得 %q", task.Carrier)
	}
}

func TestHistoryTaskIgnoresLiveCarrierHome(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	m.SetWorkspace(workspace.NewCapability())
	pid := registerTestProject(t, m, repo)
	home := "/old/home"
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", Carrier: "muse", HomeDir: &home, Target: "local",
		NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.HomeDir != "/old/home" || got.Carrier != "muse" {
		t.Fatalf("历史行被改写: %+v", got)
	}
}

func TestDispatchDoesNotImportSchedulingResolver(t *testing.T) {
	// B233.13：Manager 已迁至 internal/orchestration，读迁出后的实现文件断言。
	src, err := os.ReadFile("../orchestration/manager.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (m *Manager) Dispatch(")
	if start < 0 {
		t.Fatal("找不到 Dispatch")
	}
	rest := text[start:]
	end := strings.Index(rest[1:], "\nfunc (")
	if end > 0 {
		rest = rest[:end+1]
	}
	for _, needle := range []string{"ResolveLookup", "ClassifyRegistered", "EffectiveReceiverName", "AdmitCarrier"} {
		if strings.Contains(rest, needle) {
			t.Fatalf("Manager.Dispatch 不得调用 %s", needle)
		}
	}
	if strings.Contains(text, `"github.com/Xsxdot/handoff/internal/scheduling"`) {
		t.Fatal("manager.go 不得 import internal/scheduling")
	}
}
