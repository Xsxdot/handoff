// 会话（群）域的 wire DTO（B358）：会话列表、成员投影、详情页三块。
//
// 这是 B156.2 房间锚点翻转后的会话线格式唯一定义处；与 web/src/api/rooms.ts
// 的 TS 镜像由 rooms_fixture_test.go / rooms.test.ts 双侧金样本锁定，改形状
// 先回 contract 节点。会话 id 是账本分配的不透明字符串（形如 session:<n>），
// 消费方不得解析它——锚点解析规则住在 internal/collab/room。
package proto

import "time"

// SessionKind 是会话列表行的种类词表。B358 起只有一种：会话。
// 旧房间（卡/project/global）只读归档，不进新列表，故不出现在本词表。
const SessionKind = "session"

// Session 是会话本体的账本 wire 投影（会话列表详情的取数源）。
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Owner     string    `json:"owner"`
	Archived  bool      `json:"archived"`
	Members   []string  `json:"members,omitempty"`
	Cards     []string  `json:"cards,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionMember 的成员种类词表。
const (
	// SessionMemberHuman 显式人类成员（会话身份由控制面注入）。
	SessionMemberHuman = "human"
	// SessionMemberAgent 显式主 agent 成员（外部会话身份）。
	SessionMemberAgent = "agent"
	// SessionMemberSeat 由会话内某张卡的当前席位派生出的协调者成员。
	SessionMemberSeat = "seat"
)

// SessionMember 的成员状态词表（「看板不说谎」：只报可证实的）。
//   - working / listening：仅当该成员会话有未过期租约时报告（可续租）；
//   - last_active：不可续租的外部会话，只报最后活跃时间，不报在线；
//   - empty：卡在会话里但席位为空座（还没配人）。
const (
	SessionMemberWorking    = "working"
	SessionMemberListening  = "listening"
	SessionMemberLastActive = "last_active"
	SessionMemberEmpty      = "empty"
)

// SessionMember 是会话成员投影。席位成员随卡出入群派生，不落成员表。
type SessionMember struct {
	// Identity 成员身份：人/主 agent 的外部会话标识，或协调者席位身份
	// （cli:<cli>#<session_id>）。
	Identity string `json:"identity"`
	// Kind 见 SessionMember* 种类词表。
	Kind string `json:"kind"`
	// CardID 席位成员所属卡号；人与 agent 成员为空。
	CardID string `json:"card_id,omitempty"`
	// CardTitle 席位成员所属卡标题（列表展示辅助）；非席位成员为空。
	CardTitle string `json:"card_title,omitempty"`
	// Status 见 SessionMember* 状态词表。
	Status string `json:"status"`
	// LastActive 最后活跃时刻（不可续租成员的可证实读数）；零值=无。
	LastActive time.Time `json:"last_active,omitempty"`
}

// SessionCard 是会话里的一张工作项卡。Seat 为空 = 空座（进群 ≠ 配人）。
type SessionCard struct {
	CardID string `json:"card_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Seat 该卡当前席位身份；空串 = 还没配人（虚线卡）。
	Seat string `json:"seat,omitempty"`
}

// SessionSummary 是会话列表（谁需要我）的一行。
type SessionSummary struct {
	ID   string `json:"id"`   // session:<n>，不透明
	Kind string `json:"kind"` // SessionKind
	// Title 会话标题（人/主 agent 新建时给）。
	Title string `json:"title"`
	// Owner 群主身份（升级终点）：人 或 主 agent 会话身份。
	Owner string `json:"owner"`
	// Archived 显式归档；归档后只读。卡的终态不等于会话结束。
	Archived bool `json:"archived"`
	// Unread 该成员未读消息数。
	Unread int `json:"unread"`
	// NeedsHuman 会话内任意一张卡处于 needs_human（列表标签）。
	NeedsHuman   bool            `json:"needs_human"`
	LastActivity time.Time       `json:"last_activity"`
	Preview      *RoomPreview    `json:"preview,omitempty"`
	Members      []SessionMember `json:"members,omitempty"`
	Cards        []SessionCard   `json:"cards,omitempty"`
}

// SessionNode 是详情页「协调者派发的任务节点」投影（从卡挂账/镜像聚合，
// 不新增执行域依赖）。
type SessionNode struct {
	CardID string `json:"card_id"`
	Title  string `json:"title,omitempty"`
	Node   string `json:"node"`
	Round  int    `json:"round,omitempty"`
	State  string `json:"state"`
	Target string `json:"target,omitempty"`
}

// 会话 timeline 的结构事件种类词表（结构信息不进群聊流）。
//
// 载体对应：seat_bound ← ledger.EvDriverSeatBound（初始坐下）；seat_rebound ←
// EvDriverTakeover；card_closed ← 既有 EvStatusMoved 复用（B358 补签轮 R3），
// 由消费方按 payload.to 判定——仅 to ∈ {已完成, 终止} 记 card_closed，普通列
// 间转移不是结构事实、不进 timeline；终止的 reason 键随 payload 透传。
const (
	SessionEventCardJoined  = "card_joined"
	SessionEventCardLeft    = "card_left"
	SessionEventSeatBound   = "seat_bound"
	SessionEventSeatRebound = "seat_rebound"
	SessionEventNeedsHuman  = "needs_human"
	SessionEventCardClosed  = "card_closed"
	SessionEventArchived    = "archived"
	SessionEventCreated     = "created"
)

// SessionTimelineEvent 是详情页会话 timeline 的一行：结构事件 + 席位变更。
type SessionTimelineEvent struct {
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	CardID    string    `json:"card_id,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SessionDetail 是会话详情页投影：结构与状态（成员在 Summary 上）。
// 结构信息不进聊天流——群聊面只放人话。
type SessionDetail struct {
	Summary  SessionSummary         `json:"summary"`
	Nodes    []SessionNode          `json:"nodes,omitempty"`
	Timeline []SessionTimelineEvent `json:"timeline,omitempty"`
}

// SessionCreatedPayload 是 session_created 账本事件的载荷 schema。
type SessionCreatedPayload struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Owner string `json:"owner"`
}

// SessionArchivedPayload 是 session_archived 账本事件的载荷 schema。
type SessionArchivedPayload struct {
	Session string `json:"session"`
}

// SessionCardPayload 是 session_card_joined / session_card_left 的载荷 schema。
type SessionCardPayload struct {
	Session string `json:"session"`
	Card    string `json:"card"`
}
