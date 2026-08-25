// Package keysclient 是 keystone 域的出站 client 缝：协调者会话承载、终端
// attach 定位、叙事落点与账本只读面四条能力接口，全部定义在使用方（keystone
// 域）这一侧（架构法第九条），由组装点绑定实现；组装点之外不得 new 他方具体
// 类型（架构法第八条）。测试缝：假 Runner/假 Narrator 供 keystone 域在不起
// agentd、不起账本的情况下独立测试。
//
// 铁律挂在本缝：以协调者身份拉起/续接一个 CLI 会话不进派发状态机、不产生
// task（B156.3 spec 测试接缝 3）——Runner 的任何实现都不得调用执行域派发路径。
package keysclient

import "github.com/Xsxdot/handoff/internal/ledgerapi"

// SessionSpec 是一次无头拉起的会话规格。HomeDir 是隔离 HOME 档案：协调者
// 全套（全局规则/skill/MCP/账本凭据），与执行者的干净 HOME 相反（spec §4.3）。
type SessionSpec struct {
	CLI     string   // opencode / claude / ...
	HomeDir string   // 隔离 HOME 根；空 = 主 HOME（协调者载体禁空，由配置审核保证）
	Model   string   // 协调者模型独立配置（载体属性）
	Workdir string   // 会话工作目录（项目位置根）
	Env     []string // 追加环境变量 KEY=VALUE（已解析已展开；值不得进日志）
}

// SessionRef 指向一个可 resume 的持久会话。
type SessionRef struct {
	CLI       string `json:"cli,omitempty"`
	SessionID string `json:"session_id"`
	Machine   string `json:"machine,omitempty"`
}

// TurnResult 是一个唤醒回合的产出。
type TurnResult struct {
	SessionID string // 实际生效的会话 id（fresh 重建时是新 id）
	Output    string // 回合末输出原文（裁决块等由上层解析）
}

// Runner 是协调者会话承载缝（进程承载半段经 hostapi 门面实现，spec §7.0）：
// 无头拉起一个 CLI 会话、喂 prompt、收回合输出。没有派发状态机的任何概念。
type Runner interface {
	// Launch 新建会话并送入第一回合（prompt 为空表示只建立会话）。
	Launch(spec SessionSpec, prompt string) (TurnResult, error)
	// Resume 在既有会话上无头续一回合——事件唤醒的常规路径。
	Resume(ref SessionRef, prompt string) (TurnResult, error)
}

// AttachInfo 是「在哪台机器哪个目录用什么命令打开原生 TUI」的定位三元组
// （spec §4.4 attach：不做第二套 TUI，handoff 只负责把用户送到门口）。
type AttachInfo struct {
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
	Command string `json:"command"` // 例：opencode --session <id>；在对应机器上执行
}

// TerminalLocator 是 attach 定位缝（PTY 域薄门面 ptyapi 承载终端 tab 本体）。
type TerminalLocator interface {
	Locate(ref SessionRef, workdir string) (AttachInfo, error)
}

// Narrator 是叙事落点缝。会话子系统（B156.2 房间制）落地前，组装点绑账本
// 卡 note 兜底实现；落地后换绑房间消息，keystone 不感知差异。
type Narrator interface {
	Say(cardID, text string) error
}

// LedgerView 是 keystone 对账本的只读面加一条兜底写：读卡/事件流/基线用于
// 开场评估与重建四步，MarkNeedsHuman 用于兜底链终点「转等人」。组装点直接绑
// *ledgerapi.Facade（方法集天然满足）。
type LedgerView interface {
	GetCard(id string) (ledgerapi.Card, error)
	EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]ledgerapi.Event, error)
	EffectiveBaseBranch(id string) (string, error)
	MarkNeedsHuman(cardID, reason, actor string) error
}
