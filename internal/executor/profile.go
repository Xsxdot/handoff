// profile.go —— HOME/配置 Profile 能力面（B233.2）。
//
// 职责：定义 Inspect / Prepare / Verify 与凭据策略字面值。
//
// 边界：
//   - HOME 期望（哪个目录、是否隔离、凭据策略、期望规则/skills）属载体
//   - 原生写入属 harness；本文件无 I/O
//   - 不是 hostapi.ProbeHome / WakeHome（那是载体登录探测，B293）
//   - 禁止复制会话库或整棵 HOME；失败不得把半残 HOME 标成功
package executor

import "context"

// CredentialPolicy 是凭据供给策略，字面值与 hostapi.ProbeRequest.Credential 对齐。
type CredentialPolicy string

const (
	CredentialStandalone   CredentialPolicy = "standalone"
	CredentialMainHomeSync CredentialPolicy = "main_home_sync"
)

// ProfileFile 是一条期望写入原生位置的受管文件（规则或 skill）。
type ProfileFile struct {
	Name    string
	Content string
}

// ProfileReq 是载体提交的期望环境。
//
// TaskOverlay 是任务层纪律/临时授权，必须放到会话或任务目录，不得覆盖
// 载体全局规则——同一载体并发两任务会互相污染。
type ProfileReq struct {
	HomeDir     string
	Isolated    bool
	Credential  CredentialPolicy
	Rules       []ProfileFile
	Skills      []ProfileFile
	TaskOverlay []ProfileFile
}

// ProfileReport 是一次检查/准备/校验的可见结果。
//
// Prepared、Verified、EngineOK 是不同事实，禁止用其中一个冒充另一个。
type ProfileReport struct {
	HomeDir  string
	Isolated bool
	Prepared bool
	Verified bool
	EngineOK bool
	Missing  []string
	Notes    []string
}

// Profile 是 HOME/配置能力面。
//
// Prepare 失败必须可重试，且不得把半残 HOME 报成成功。
type Profile interface {
	Inspect(ctx context.Context, req ProfileReq) (ProfileReport, error)
	Prepare(ctx context.Context, req ProfileReq) (ProfileReport, error)
	Verify(ctx context.Context, req ProfileReq) (ProfileReport, error)
}
