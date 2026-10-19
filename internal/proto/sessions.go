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
	// SessionMemberResident 房间驻场者（B435）：无席位的默认收件人，一房间至多一名。
	SessionMemberResident = "resident"
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
	// B444 P1：驻场者成员（kind=resident）的驱动形态与载体落点摘要，取自 SessionResident；
	// 桌面端驻场者与其他成员全部缺键（omitempty），消费方把缺键当 desktop。只投影「谁在哪跑」
	// 四键，不投影 HOME/workdir/会话 id 等执行细节。
	DriverMode     string `json:"driver_mode,omitempty"`
	Carrier        string `json:"carrier,omitempty"`
	CarrierMachine string `json:"carrier_machine,omitempty"`
	Model          string `json:"model,omitempty"`
}

// SessionCard 是会话里的一张工作项卡。Seat 为空 = 空座（进群 ≠ 配人）。
type SessionCard struct {
	CardID string `json:"card_id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Seat 该卡当前席位身份；空串 = 还没配人（虚线卡）。
	Seat string `json:"seat,omitempty"`
	// SeatMode 该卡席位驱动模式（B435：push|pull）；空座不出键。键名不用 mode，
	// 避免与 CoordinatorRebindReq.Mode（换绑方式 launch）同名异义。
	SeatMode string `json:"seat_mode,omitempty"`
}

// SessionResident 是房间驻场者登记的 wire 投影（B435 D5/D10）。一房间至多一名；
// 桌面端三字段同空 = 无桌面端地址（只靠 session wait 收件）。
type SessionResident struct {
	// Session 会话房间 id（session:<n>）。
	Session string `json:"session"`
	// Identity 驻场者成员身份，统一记法 user:<name> / agent:<name>（identity.go）。
	Identity string `json:"identity"`
	// DesktopSession Claude 桌面端会话 id（local_…）；continue 深链用。
	DesktopSession string `json:"desktop_session,omitempty"`
	// CLISession 该桌面端会话的 CLI 会话 id：信箱文件名键（mod 以 $.session.id() 取同值）。
	CLISession string `json:"cli_session,omitempty"`
	// Machine 桌面端所在机器；空串 = 本机。
	Machine string `json:"machine,omitempty"`
	// SatAt 登记时刻。
	SatAt time.Time `json:"sat_at"`
	// B444 P1 驱动形态与载体落点（plan Q2 甲；全部 omitempty——旧 desktop 行的 JSON **无**这些键，
	// 消费方须把缺键当 desktop 处理）。push 行的机器在 CarrierMachine，**不复用** Machine/CLISession
	// （那两列是桌面端门铃的地址，复用会造成门铃与 keystone 双重唤醒）。
	// DriverMode 见 ResidentDriver* 词表；空串 = desktop。
	DriverMode string `json:"driver_mode,omitempty"`
	// PushCLI push 载体的 CLI 名（opencode/claude…）。
	PushCLI string `json:"push_cli,omitempty"`
	// PushSession push 载体的 CLI 会话 id：每次续轮 resume 的目标。
	PushSession string `json:"push_session,omitempty"`
	// Carrier 载体登记名（scheduling）。
	Carrier string `json:"carrier,omitempty"`
	// CarrierMachine 载体所在机器；空串 = 本机。
	CarrierMachine string `json:"carrier_machine,omitempty"`
	// HomeDir 载体隔离 HOME。
	HomeDir string `json:"home_dir,omitempty"`
	// Workdir 回合工作目录。
	Workdir string `json:"workdir,omitempty"`
	// Model 载体模型。
	Model string `json:"model,omitempty"`
}

// SessionUntitledTitle 是零卡、标题留空的会话的占位名（B444 spec「自动命名规则」）。驻场者首轮以它
// 作改名 CAS 的期望值：标题仍等于它才改，人先手改过就保留人的名字（plan Q6 甲）。
const SessionUntitledTitle = "新会话"

// 驻场者驱动形态词表（B444 P1）。空串是旧行与桌面端驻场者的共同读数，不另设 "desktop" 字面值，
// 免得同义两值并存。
const (
	// ResidentDriverDesktop 桌面端驻场者：门铃信 + continue 深链驱动（B435 O2）。
	ResidentDriverDesktop = ""
	// ResidentDriverPush handoff 拉起的载体担任驻场者：唤醒走 keystone 续轮（B444）。
	ResidentDriverPush = "push"
)

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
	// Project 项目房间标记（B435 ⑤/D6）：取值项目名；非项目房间不出键。
	// 唯一写者是项目房间绑定（session sit --project / project room 创建的那条）。
	Project string `json:"project,omitempty"`
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

// SessionWake 是会话订阅通道的唤醒载荷（B358 R1）。
type SessionWake struct {
	Session    string       `json:"session"`              // 命中所在会话 id
	Hit        SessionCite  `json:"hit"`                  // 寻址命中那条
	Referenced *SessionCite `json:"referenced,omitempty"` // reply_to 指向的那条；无引用锚省键
	Unread     int          `json:"unread"`               // 该成员在该会话的未读数（含命中条）
}

// SessionBacklog 是监听重挂时单行输出的历史定向消息摘要。
type SessionBacklog struct {
	Type    string              `json:"type"`
	Member  string              `json:"member"`
	FromSeq int64               `json:"from_seq"`
	ToSeq   int64               `json:"to_seq"`
	Hits    []SessionBacklogHit `json:"hits"`
}

// SessionBacklogHit 保留可跳转的命中条与可选回复引用条。
type SessionBacklogHit struct {
	Hit        SessionCite  `json:"hit"`
	Referenced *SessionCite `json:"referenced,omitempty"`
}

// SessionCite 是命中条/引用条的最小引用形状（可点跳转的锚点集）。
type SessionCite struct {
	Seq   int64  `json:"seq"`   // 账本 seq（跳转与对质锚）
	Room  string `json:"room"`  // 会话房间 id
	Actor string `json:"actor"` // 发言者身份
	Body  string `json:"body"`  // 正文
}
