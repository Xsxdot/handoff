package opencode

import (
	"context"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/executor"
)

type OneShot struct{ log *slog.Logger }

func NewOneShot(log *slog.Logger) *OneShot {
	if log == nil {
		log = slog.Default()
	}
	return &OneShot{log: log}
}

func (s *OneShot) Invoke(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
	s.log.Info("OneShot.Invoke 进入", "harness", executor.HarnessOpenCode, "model", req.Model, "prompt_bytes", len(req.Prompt), "effort", req.Limits.Effort)
	argv, err := OneShotArgv(req.Model, req.Prompt, req.Limits)
	if err != nil {
		s.log.Error("OneShot argv 编码失败", "cause", err)
		return executor.OneShotReply{Status: executor.OneShotFailed}, err
	}
	out, err := executor.RunOneShotProcess(ctx, argv, req.Env, req.Workdir, req.HomeDir)
	if ctx.Err() != nil {
		s.log.Info("OneShot 已取消", "cause", ctx.Err())
		return executor.OneShotReply{Text: out, Status: executor.OneShotCanceled}, ctx.Err()
	}
	if err != nil {
		s.log.Error("OneShot 进程失败", "cause", err)
		return executor.OneShotReply{Text: out, Status: executor.OneShotFailed}, err
	}
	s.log.Info("OneShot.Invoke 完成", "status", executor.OneShotOK, "output_bytes", len(out))
	return executor.OneShotReply{Text: out, Status: executor.OneShotOK}, nil
}

func (a *Adapter) Invoke(ctx context.Context, req executor.OneShotReq) (executor.OneShotReply, error) {
	var log *slog.Logger
	if a != nil {
		log = a.log
	}
	return NewOneShot(log).Invoke(ctx, req)
}

var (
	_ executor.OneShot = (*OneShot)(nil)
	_ executor.OneShot = (*Adapter)(nil)
)
