// probe.go —— 只读存活探测。
//
// 职责：
//   - Probe：读 serve.json，走 Proc.Alive 的既有判据（TCP 可连），如实返回结论
//
// 边界：
//   - **绝不写**：不回收 tmux 会话、不碰 store、不发事件
//   - 判据弱于 grok 的 HTTP 探活：端口活着不等于协议层活着（见 proc.go 文件头），
//     所以 Note 里如实写「端口可连」，不夸大成「executor 正常」
package codex

import (
	"fmt"

	"github.com/xushixin/handoff/internal/executor"
)

// Probe 只读探测 codex app-server 是否仍存活（manager 的 prober 可选接口）。
//
// 返回：
//   - err != nil：探不出结论（serve.json 缺失/损坏），调用方按 unknown 处理
func (a *Adapter) Probe(req executor.ProbeReq) (executor.ProbeOutcome, error) {
	proc, err := ReadServeInfo(req.TaskDir)
	if err != nil {
		a.log.Info("codex 探活：恢复凭据不可读，结论未知", "task", req.TaskID, "cause", err)
		return executor.ProbeOutcome{}, fmt.Errorf("读 codex 恢复凭据: %w", err)
	}
	if proc.Alive() {
		a.log.Info("codex 探活：app-server 端口可连", "task", req.TaskID, "port", proc.Port)
		return executor.ProbeOutcome{Alive: true}, nil
	}
	note := fmt.Sprintf("codex app-server 已不在（端口 %d 连不上）", proc.Port)
	a.log.Info("codex 探活：app-server 已不在", "task", req.TaskID, "port", proc.Port)
	return executor.ProbeOutcome{Alive: false, Note: note}, nil
}
