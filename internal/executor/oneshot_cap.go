// oneshot_cap.go —— 一次性调用能力面（B233.2）。
//
// 职责：定义 OneShot.Invoke 与调用方限制（Effort/Timeout）。
//
// 边界：
//   - 不拼 argv。argv 留在各家包（spec：厂商细节不进消费方）
//   - 不解析审批 JSON；审批模型语义留在 Approver
//   - grok 的 low effort 是 Limits.Effort，不是 grok 能力默认
//   - 本文件无 I/O；各家 argv 编码与进程承载由提供方实现
package executor

import (
	"context"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// EffortLow 是调用方传入的廉价档字面值。只有调用方显式填写才编进 grok argv。
const EffortLow = "low"

// OneShotStatus 是一次性调用的终态。
type OneShotStatus string

const (
	OneShotOK       OneShotStatus = "ok"
	OneShotFailed   OneShotStatus = "failed"
	OneShotCanceled OneShotStatus = "canceled"
)

// OneShotLimits 是调用方策略，不是 harness 默认。
//
// Timeout=0 表示未设；取消走 ctx，不把超时编进 argv。
type OneShotLimits struct {
	Effort  string
	Timeout time.Duration
}

// OneShotReq 是一次 Invoke 的输入。
type OneShotReq struct {
	Prompt  string
	Model   string
	Workdir string
	HomeDir string
	Env     []string
	Limits  OneShotLimits
}

// OneShotReply 是一次 Invoke 的产出。Usage 可空；Status 必须是 OneShot* 之一。
type OneShotReply struct {
	Text   string
	Usage  *proto.Usage
	Status OneShotStatus
}

// OneShot 是一次性调用能力面。ctx 取消必须打断实际进程。
type OneShot interface {
	Invoke(ctx context.Context, req OneShotReq) (OneShotReply, error)
}
