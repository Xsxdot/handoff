// attachment.go —— 一次订阅的对外形态。
//
// 职责：定义调用方（agentd 的 pty_ws.go）看得见的四个字段与三个方法。
//
// 边界：它自己什么都不做——三个方法逐字转交注入进来的 ops。
//
// 为什么是「壳 + 注入」而不是接口：pty_ws.go 的 pumpPtyUplink 签名写死了
// *ptyhost.Attachment。壳保住具体类型，让进程内引擎与 socket 客户端各自注入行为。
package ptyhost

// AttachOps 是一次订阅的三个行为，由构造它的一方提供。
//
// 导出它是因为 internal/ptyhost/engine 要在包外实现它；本包之外没有别的合法实现者。
type AttachOps interface {
	Detach()
	ExitCode() *int
	Resize(cols, rows int) error
}

// Attachment 是一次订阅。Backlog 是建连瞬间的历史回放，Out 是后续实时输出；
// Out 被关闭意味着会话结束（不是网络抖动），客户端应停止重连。
//
// 注意：Backlog 与 Out 必须按构造方的协议语义消费；Detach 只退订，不杀会话。
type Attachment struct {
	Backlog   []byte
	Since     uint64
	Truncated bool
	Out       <-chan []byte

	ops AttachOps
}

// NewAttachment 组装一个订阅壳。
//
// 参数：backlog/since/truncated/out 是订阅结果；ops 提供 Detach、ExitCode、Resize 行为。
// 返回：可交给 pty_ws.go 的具体 Attachment。
// 注意：ops 不应为 nil；调用三个方法前必须由构造者注入有效行为。
func NewAttachment(backlog []byte, since uint64, truncated bool, out <-chan []byte, ops AttachOps) *Attachment {
	return &Attachment{Backlog: backlog, Since: since, Truncated: truncated, Out: out, ops: ops}
}

// Detach 退订，不杀会话；切 tab、切目录、关页面都走它。
func (a *Attachment) Detach() { a.ops.Detach() }

// ExitCode 返回 shell 的退出码；nil 表示还活着，或对端没给出退出码。
func (a *Attachment) ExitCode() *int { return a.ops.ExitCode() }

// Resize 上报本订阅者的尺寸；实际尺寸由所有订阅者取最小值协商而来。
func (a *Attachment) Resize(cols, rows int) error { return a.ops.Resize(cols, rows) }
