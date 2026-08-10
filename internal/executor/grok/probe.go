// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 serve.json，走 Proc.Alive 的既有判据（端口 HTTP 应答），
//     如实返回存活结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不动凭据软链、不碰 store、不发事件
//   - 不打印 Proc.Secret / WSURL：两者都含 secret，绝不进日志
package grok

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 grok serve 是否仍存活（manager 的 prober 可选接口）。
//
// 判据与 Resume 共用同一份 Proc.Alive：端口收到任何 HTTP 响应即算活（含 404）。
//
// 返回：
//   - err != nil：探不出结论（serve.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	proc, err := ReadServeInfo(req.TaskDir)
	if err != nil {
		a.log.Info("grok 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 grok 恢复凭据: %w", err)
	}
	if proc.Alive() {
		a.log.Info("grok 探活：serve 存活", "task", req.TaskID, "port", proc.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("grok serve 已不在（端口 %d 无应答）", proc.Port)
	a.log.Info("grok 探活：serve 已不在", "task", req.TaskID, "port", proc.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
