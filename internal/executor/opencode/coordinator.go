// coordinator.go —— OpenCode 协调会话能力（B233.2）。
//
// 职责：把 Coordinator 三方法映射到 hostapi.Host.RunTurn。
// 边界：不进派发、不 CreateTask；hostapi 仍不是消费方面。
package opencode

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/hostapi"
)

type Coordinator struct {
	Host *hostapi.Host
	log  *slog.Logger
	mu   sync.Mutex
	canc map[string]context.CancelFunc
}

func NewCoordinator(h *hostapi.Host, log *slog.Logger) *Coordinator {
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{Host: h, log: log, canc: map[string]context.CancelFunc{}}
}

func (c *Coordinator) Launch(ctx context.Context, spec executor.CoordSessionSpec, prompt string) (executor.CoordTurnResult, error) {
	if err := executor.RequireLaunchPrompt(prompt); err != nil {
		return executor.CoordTurnResult{}, err
	}
	c.log.Info("Coordinator.Launch 进入", "cli", spec.CLI, "home_isolated", spec.HomeDir != "", "prompt_bytes", len(prompt))
	return c.turn(ctx, hostapi.TurnRequest{
		CLI: spec.CLI, HomeDir: spec.HomeDir, Workdir: spec.Workdir,
		Model: spec.Model, Prompt: prompt, Env: spec.Env,
	})
}

func (c *Coordinator) Resume(ctx context.Context, ref executor.CoordSessionRef, prompt string) (executor.CoordTurnResult, error) {
	if err := executor.RequireResumeRef(ref); err != nil {
		return executor.CoordTurnResult{}, err
	}
	c.log.Info("Coordinator.Resume 进入", "cli", ref.CLI, "session_set", ref.SessionID != "")
	return c.turn(ctx, hostapi.TurnRequest{
		CLI: ref.CLI, HomeDir: ref.HomeDir, Workdir: ref.Workdir,
		Model: ref.Model, Prompt: prompt, SessionID: ref.SessionID, Env: nil,
	})
}

func (c *Coordinator) CancelTurn(_ context.Context, ref executor.CoordSessionRef) error {
	key := ref.SessionID
	if key == "" {
		c.log.Info("CancelTurn 缺少 session id，跳过", "cli", ref.CLI)
		return nil
	}
	c.mu.Lock()
	cancel, ok := c.canc[key]
	c.mu.Unlock()
	if !ok {
		c.log.Info("CancelTurn 无在途回合", "cli", ref.CLI, "session_id", ref.SessionID)
		return nil
	}
	c.log.Info("CancelTurn 取消在途回合", "cli", ref.CLI, "session_id", ref.SessionID)
	cancel()
	return nil
}

func (c *Coordinator) turn(ctx context.Context, req hostapi.TurnRequest) (res executor.CoordTurnResult, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	key := req.SessionID
	c.mu.Lock()
	if key != "" {
		c.canc[key] = cancel
	}
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		if key != "" {
			delete(c.canc, key)
		}
		c.mu.Unlock()
		if err != nil {
			c.log.Error("协调回合失败", "cli", req.CLI, "cause", err)
		} else {
			c.log.Info("协调回合完成", "cli", req.CLI, "session_set", res.SessionID != "", "output_bytes", len(res.Output))
		}
	}()
	reply, err := c.Host.RunTurn(ctx, req)
	if err != nil {
		return executor.CoordTurnResult{}, err
	}
	return executor.CoordTurnResult{SessionID: reply.SessionID, Output: reply.Output}, nil
}
