// 协作房间域的 wire DTO（B156.2）。本文件是房间消息、收件箱条目与会话
// 列表条目的线格式唯一定义处；Go 与 web/src/api/rooms.ts 的 TS 镜像由
// rooms_fixture_test.go / rooms.test.ts 双侧金样本锁定，改形状先回 contract 节点。
package proto

import (
	"encoding/json"
	"time"
)

// 房间消息 kind 受控词表（spec §5 白名单制：不在单上的 kind 拒收）。
const (
	RoomMsgEscalation = "escalation" // 推翻级简报（六段正文进 Body，选项挂 DecisionID）
	RoomMsgDeviation  = "deviation"  // 填补级偏差叙事
	RoomMsgClosing    = "closing"    // 收口摘要（四段+子卡汇总）
	RoomMsgRelay      = "relay"      // 父卡衔接消息（父协调者以成员身份书写）
	RoomMsgReply      = "reply"      // 协调者对用户发言的对话式应答
	RoomMsgUser       = "user"       // 用户发言
	RoomMsgPointer    = "pointer"    // 薄里程碑指针行（仅系统组件，HTTP 面不可达）
)

// RoomMessage 是 room_message 账本事件的载荷 schema。ledger 只存
// RawMessage 不解释字段；会话子系统与控制台按本结构编解码。
type RoomMessage struct {
	Room     string   `json:"room"` // 卡号 | project:<name> | global
	Kind     string   `json:"kind"` // RoomMsg* 受控词表
	Body     string   `json:"body"`
	Refs     []string `json:"refs,omitempty"`     // 引用锚：git 路径 / timeline 锚 / 卡号 / 附件路径
	Mentions []string `json:"mentions,omitempty"` // @成员：卡号（=该卡协调者）或用户标识
	// DecisionID 简报挂的裁决 id；kind=escalation 时应非零（关联决策答复直达）。
	DecisionID int64 `json:"decision_id,omitempty"`
	// BySystem true=系统组件书写的指针行；Send 一律拒收 pointer，
	// 本键只应由 Service.Pointer 置真。
	BySystem bool `json:"by_system,omitempty"`
}

// 收件箱条目来源受控词表。
const (
	InboxOriginDecision = "decision"
	InboxOriginTicket   = "ticket"
	InboxOriginMention  = "mention"
)

// InboxItem 是待回复收件箱的聚合条目（三源：open 裁决 / 兜底工单 / @提及）。
// 组装在 gateway 编排单元，不在任一域内。
type InboxItem struct {
	Origin string `json:"origin"`
	Title  string `json:"title"`
	CardID string `json:"card_id,omitempty"`
	// RefID 统一字符串形：decision id 十进制串 / ticket id 原文 / message seq 十进制串。
	RefID   string          `json:"ref_id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// RoomAttach 是房间详情可执行的任务 attach 投影。
// Target 为空表示当前 agentd；WorkDir 是任务 Workdir() 的结果；Command 是
// 当前 CLI 能执行的完整命令。它不把 BoundSession 当作 task/session。
type RoomAttach struct {
	Target  string `json:"target,omitempty"`
	TaskID  string `json:"task_id"`
	WorkDir string `json:"work_dir"`
	Command string `json:"command"`
}

// RoomSummary 是会话列表（扁平活动排序）的单行。
type RoomSummary struct {
	ID      string `json:"id"`   // 卡号 | project:<name> | global
	Kind    string `json:"kind"` // card | project | global
	Project string `json:"project,omitempty"`
	Title   string `json:"title"`
	// BoundSession 卡房间的当前绑定者（driver_session 投影）；群房间为空。
	BoundSession string `json:"bound_session,omitempty"`
	// Live 绑定者租约未过期（同一注入时钟判定）；无绑定时 false。
	Live bool `json:"live"`
	// ReadOnly 并入冻结或卡终态归档。
	ReadOnly     bool        `json:"read_only"`
	LastActivity time.Time   `json:"last_activity"`
	Unread       int         `json:"unread"`
	Attach       *RoomAttach `json:"attach,omitempty"`
}
