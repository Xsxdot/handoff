// PTY 终端会话的线格式类型（W4 PTY 终端 spec §3.1、§5）。
//
// 职责：定义 REST 与 /ws/pty 的请求/响应/控制帧形状。
// 边界：不含任何行为；会话真相在 internal/ptyhost，这里只是它的线格式投影。
package proto

import "time"

// PtySession 是一个终端会话的线格式投影。
//
// ExitCode 用指针表达三态里的两态：**缺席 = 还活着**，出现 = 已退出且这是退出码。
// 与 StatusResp.Update / StatusResp.Proc 同一纪律——不用 0 或 -1 冒充「不知道」。
type PtySession struct {
	ID string `json:"id"`
	// Machine 是**线注解，不入库**：""=本机，否则为汇总方 cfg.Targets 的键，
	// 由汇总方盖章。与 Task.Machine 同款。
	Machine   string    `json:"machine"`
	BasePath  string    `json:"base_path"`
	BaseKind  string    `json:"base_kind"` // "workspace" | "home"
	Shell     string    `json:"shell"`
	CreatedAt time.Time `json:"created_at"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	Attached  int       `json:"attached"`
	// Foreground 表示会话里有命令跑在前台。控制台据此决定关 tab 时要不要先确认
	//（spec §6.2）。**不带 omitempty**：false 是一个有意义的结论（「空闲，随便关」），
	// 缺键会让前端分不清它和「这版服务端还不认识这个字段」。
	Foreground bool `json:"foreground"`
	PID        int  `json:"pid"`
	ExitCode   *int `json:"exit_code,omitempty"`
	// BytesOut 是该会话累计输出的字节数，也是 /ws/pty 的 since 水位。
	BytesOut uint64 `json:"bytes_out"`
}

// PtySessionsResp 是 GET /api/pty/sessions 的响应。
// Machines 仅在 ?scope=all 时出现，形状与 ProjectTreeResp 一致。
type PtySessionsResp struct {
	Sessions []PtySession    `json:"sessions"`
	Machines []MachineStatus `json:"machines,omitempty"`
}

// CreatePtySessionReq 是 POST /api/pty/sessions 的请求体。
// BaseKind="home" 时 BasePath 被忽略（服务端用它自己的 $HOME，见 spec §5.2）。
type CreatePtySessionReq struct {
	BasePath string `json:"base_path"`
	BaseKind string `json:"base_kind"`
	// Rel 是相对 BasePath 的子目录，空串=工作树根；BaseKind=home 时忽略。
	Rel  string `json:"rel"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// /ws/pty 的 text 帧类型。binary 帧恒为 PTY 原始字节，不走 JSON。
const (
	PtyCtrlAttached = "attached" // 服务端 → 客户端，建连首帧
	PtyCtrlExit     = "exit"     // 服务端 → 客户端，shell 已退出
	PtyCtrlError    = "error"    // 服务端 → 客户端
	PtyCtrlResize   = "resize"   // 客户端 → 服务端
)

// PtyControl 是 /ws/pty 上双向共用的控制帧。
//
// 为什么一个结构体走两个方向：控制帧是低频路径（建连、退出、改尺寸），
// 拆成四个类型只会让两端各多三个分支。高频的数据路径**不经过它**——
// PTY 字节走 binary 帧，零解析零 base64 膨胀（spec §5.3）。
//
// Since / Truncated 刻意不带 omitempty：attached 帧里「从 0 开始」与
// 「没有截断」都是有意义的结论，缺键会让前端分不清「服务端说了 false」
// 和「服务端这版还不认识这个字段」。
type PtyControl struct {
	Type      string `json:"type"`
	Since     uint64 `json:"since"`
	Truncated bool   `json:"truncated"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Message   string `json:"message,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}
