// coordinator.go —— 协调会话能力面（B233.2）。
//
// 职责：
//   - 定义 Coordinator：Launch / Resume / CancelTurn
//   - 定义可选 CoordAttacher（类型断言，不进三方法）
//   - 冻结与 keysclient.SessionSpec/Ref/TurnResult/AttachInfo 同形的字段
//
// 边界：
//   - 不进派发状态机、不产生 task
//   - 本文件无 I/O；hostapi.Host.RunTurn 是 OpenCode 实现机制，不是消费方面
//   - keysclient.Runner 在实现节点退役为对本接口的包装，不得长期两套语义
package executor

import (
	"context"
	"errors"
	"strings"
)

// ErrEmptyLaunchPrompt 表示 Coordinator.Launch 收到空 prompt。
//
// 现状 hostapi 对「空 prompt 且空 SessionID」拒绝；Launch 总是新建，所以空
// prompt 必须响报。不得静默理解成「只建会话」。
var ErrEmptyLaunchPrompt = errors.New("coordinator: 新建会话需要非空 prompt")

// ErrEmptyResumeSession 表示 Coordinator.Resume 缺少会话 id。
var ErrEmptyResumeSession = errors.New("coordinator: Resume 需要非空 SessionID")

// CoordSessionSpec 是一次无头拉起的会话规格，字段与 keysclient.SessionSpec 同形。
type CoordSessionSpec struct {
	CLI     string
	HomeDir string
	Model   string
	Workdir string
	Env     []string
}

// CoordSessionRef 指向一个可 resume 的持久会话，字段与 keysclient.SessionRef 同形。
type CoordSessionRef struct {
	CLI       string `json:"cli,omitempty"`
	SessionID string `json:"session_id"`
	Machine   string `json:"machine,omitempty"`
	HomeDir   string `json:"home_dir,omitempty"`
	Workdir   string `json:"workdir,omitempty"`
	Model     string `json:"model,omitempty"`
}

// CoordTurnResult 是一个唤醒回合的产出，字段与 keysclient.TurnResult 同形。
type CoordTurnResult struct {
	SessionID string
	Output    string
}

// CoordAttachInfo 是 attach 定位三元组，字段与 keysclient.AttachInfo 同形。
type CoordAttachInfo struct {
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
	Command string `json:"command"`
}

// Coordinator 是协调会话能力面。
//
// ctx 取消必须打断进行中的回合（实现可走进程组杀，但语义是这条 ctx）。
type Coordinator interface {
	Launch(ctx context.Context, spec CoordSessionSpec, prompt string) (CoordTurnResult, error)
	Resume(ctx context.Context, ref CoordSessionRef, prompt string) (CoordTurnResult, error)
	CancelTurn(ctx context.Context, ref CoordSessionRef) error
}

// CoordAttacher 是可选能力：给出「在哪台机器哪个目录用什么命令打开原生 TUI」。
// 不进 Coordinator 三方法；消费方类型断言。未实现不得伪造 attach。
type CoordAttacher interface {
	AttachInfo(ctx context.Context, ref CoordSessionRef) (CoordAttachInfo, error)
}

// RequireLaunchPrompt 执法 Launch 的非空 prompt。空串（含纯空白）非法。
func RequireLaunchPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return ErrEmptyLaunchPrompt
	}
	return nil
}

// RequireResumeRef 执法 Resume 必须带会话 id。
func RequireResumeRef(ref CoordSessionRef) error {
	if strings.TrimSpace(ref.SessionID) == "" {
		return ErrEmptyResumeSession
	}
	return nil
}
