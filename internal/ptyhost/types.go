// types.go —— agentd 与 ptyhost 引擎共用的会话数据形态。
//
// 职责：定义会话快照、开会话参数，以及平台 PTY 能力查询。
//
// 边界：不持有会话、不启动 shell、不连接 socket；生命周期与实现分别由 engine 和
// client 负责。这里的结构只描述跨实现边界所需的数据。
package ptyhost

import "time"

// Session 是一个会话的快照，跨出实现内部的锁之后可以自由持有。
//
// 参数：无；字段由引擎或客户端填充。
// 返回：作为值传递的静态事实与活事实快照。
// 注意：ExitCode 为 nil 表示还活着；Foreground 与 BytesOut 来自 stat，不能用旧元数据猜。
type Session struct {
	ID        string
	BasePath  string
	BaseKind  string
	Shell     string
	CreatedAt time.Time
	Cols      int
	Rows      int
	Attached  int
	PID       int
	ExitCode  *int
	// Incompatible 表示该会话由当前客户端不认识的协议版本托管；进程仍活着，
	// 但本版不能 Attach，只能在界面走「重开一个终端」出口。
	Incompatible bool
	// Foreground 表示会话里当前有一个跑在前台的命令。
	Foreground bool
	BytesOut   uint64
}

// OpenOptions 是开会话的入参。
//
// 参数：Env 是完整环境，不会再自动追加 os.Environ；Cols/Rows <= 0 时实现使用默认尺寸。
// 返回：由 Open 消费，不保留调用方切片的生命周期承诺。
// 注意：Shell 与 BasePath 必须是目标机器上可执行且可访问的值。
type OpenOptions struct {
	BasePath string
	BaseKind string
	Shell    string
	Env      []string
	Cols     int
	Rows     int
}

// Supported 报告本平台是否支持 PTY，供 /api/status 的 pty_supported 上报。
//
// 它是编译期常量而不是运行时探测：agentd 与 ptyhost 由同一个二进制在同一台机器上运行，
// 两者能力必然相同。
func Supported() bool { return ptySupported }
