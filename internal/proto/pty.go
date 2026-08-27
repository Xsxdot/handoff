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
	// Incompatible 表示会话由协议不兼容的旧版本托管：进程还活着，但本版接不进去，
	// 前端只能给出「重开一个终端」的出口。不带 omitempty，false 也是明确结论。
	Incompatible bool `json:"incompatible"`
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
	// EnvFile 是要额外注入的 env 文件名（该机 <DataDir>/env 下的纯文件名）。
	// 空 = 不注入。**文件不存在时创建会话直接 400 失败，不降级成无变量终端**：
	// 降级的症状是「请求 200、终端正常出现、变量悄悄不在」，用户可能半小时后
	// 才发现（2026-08-22 需求 B 契约 §3.1）。
	EnvFile string `json:"env_file,omitempty"`
	// InitCommand 是 shell 就绪后送进终端**输入**的命令原文（不含换行，服务端补）。
	// 空 = 不送。
	//
	// 它在交互 shell 内部执行，命令退出后会话继续存在。**不进 argv**——走 argv
	// 会把 login shell 变成非交互 shell，命令一退会话就没了。
	InitCommand string `json:"init_command,omitempty"`
}

// /ws/pty 的 text 帧类型。binary 帧恒为 PTY 原始字节，不走 JSON。
const (
	PtyCtrlAttached = "attached" // 服务端 → 客户端，建连首帧
	PtyCtrlExit     = "exit"     // 服务端 → 客户端，shell 已退出
	PtyCtrlError    = "error"    // 服务端 → 客户端
	PtyCtrlResize   = "resize"   // 客户端 → 服务端
	PtyCtrlDebug    = "debug"    // 客户端 → 服务端，B270 取证，不进 PTY
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
