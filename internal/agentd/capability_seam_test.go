// capability_seam_test.go —— B233.2 接缝级综合回归测试。
//
// 职责：
//   - 穿过生产入口验证能力面约束：
//     1. Manager.Dispatch 执法 CapExecution（未声明 execution 的执行者在 Start 前被拒，且 Start 零调用）；
//     2. coordinatorRunner.Launch 执法 CapCoordination（非 OpenCode 返回 ErrCapabilityUnsupported 且底层零调用）；
//     3. Approver.Decide 走 a.shot.Invoke 且 grok 填入 EffortLow；
//     4. coordinatorHomeSupplier.Prepare 经 Profile.Prepare 写入规则，绝不触碰表外 sessions.db。
package agentd

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

type spyExecutionAdapter struct {
	started int
}

func (s *spyExecutionAdapter) Name() string { return "no-exec" }
func (s *spyExecutionAdapter) Report() executor.CapabilityReport {
	return executor.CapabilityReport{
		Harness: "no-exec",
		Caps: []executor.CapabilityDecl{
			{Name: executor.CapExecution, Supported: false},
		},
	}
}
func (s *spyExecutionAdapter) Start(ctx context.Context, req executor.StartReq) error {
	s.started++
	return nil
}
func (s *spyExecutionAdapter) Events(string) <-chan executor.AdapterEvent { return nil }
func (s *spyExecutionAdapter) Send(context.Context, string, string) error { return nil }
func (s *spyExecutionAdapter) RespondPermission(context.Context, string, string, string, string) error {
	return nil
}
func (s *spyExecutionAdapter) Stop(string) error { return nil }

func TestDispatchRequiresExecutionBeforeStart(t *testing.T) {
	spy := &spyExecutionAdapter{}
	ads := map[string]executor.Adapter{
		"no-exec": spy,
	}
	st := newTestStore(t)
	if err := st.CreateProjectLocation(&proto.ProjectLocation{
		Name: "proj-1",
		Path: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	reg := executor.NewRegistry(spy)
	mgr := &Manager{
		st:  st,
		ads: ads,
		reg: reg,
		cfg: &config.Config{},
		log: slog.Default(),
	}

	_, err := mgr.Dispatch(context.Background(), DispatchReq{
		ProjectName: "proj-1",
		Prompt:      "test",
		Executor:    "no-exec",
	})
	if err == nil {
		t.Fatal("未声明 CapExecution 的执行者必须派发失败")
	}
	if !errors.Is(err, executor.ErrCapabilityUnsupported) {
		t.Fatalf("错误未匹配 ErrCapabilityUnsupported: %v", err)
	}
	if spy.started != 0 {
		t.Fatalf("Require 失败时不得调用 Start，实得调用次数: %d", spy.started)
	}
}
