// 会话（群）域入站门面（B358）：会话生命周期、成员与状态投影、详情页三块，
// 以及投递寻址的唯一判定入口 WakeTargets。
//
// 锚点翻转：会话 = 工作单元（人 / 主 agent 开的一场会话），卡是会话里的工作项；
// 一卡同时只挂一个会话。成员不落表——由会话的显式成员（人与主 agent 外部会话
// 身份）+ 会话内各卡的当前席位派生。席位权威仍在 cards.driver_session。
//
// 投递寻址化（本卡核心）：一条消息的接收人由发送者写下的寻址决定，与「群里
// 有谁」无关；系统永不按成员集合投递。WakeTargets 是唯一判定入口，纯寻址规则
// 在 room.ResolveDelivery（签名里没有成员集合，扇出形状写不出来）。
package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// CreateSession 建一场会话（群）。owner = 群主（人或主 agent 会话身份），
// 群主是升级终点。
func (s *Service) CreateSession(title, owner, actor string) (proto.Session, error) {
	return s.lc.CreateSession(title, owner, actor)
}

// 改名入口哨兵（B444 S7）。
var (
	// ErrSessionTitle 会话标题为空（裁剪后）。
	ErrSessionTitle = errors.New("collab: 会话标题不能为空")
	// ErrTitleConflict 改名的 expect_title 与当前标题不符（plan Q6 甲 CAS），标题不变。
	ErrTitleConflict = client.ErrCASConflict
)

// CreateSessionInProject 建一场属于项目 homeProject 的会话（B444：会话属项目）。homeProject 为卡 project
// 字段的比较键，形状校验同 EnsureProjectRoom（非空、不含空白与控制字符）；非法 → ErrProjectName（零写入）。
// 只写会话本体与所属项目——拉卡、拉起驻场者由 agentd 编排层串起（spec 落位三问 ①）。
func (s *Service) CreateSessionInProject(title, owner, homeProject, actor string) (proto.Session, error) {
	if err := validProjectName(homeProject); err != nil {
		log().Warn("建项目内会话被拒：项目名非法", "home_project", homeProject, "actor", actor)
		return proto.Session{}, err
	}
	session, err := s.lc.CreateSessionInProject(title, owner, homeProject, actor)
	if err != nil {
		log().Warn("建项目内会话失败", "home_project", homeProject, "owner", owner, "cause", err)
		return proto.Session{}, err
	}
	log().Info("项目内会话已建", "session", session.ID, "home_project", homeProject, "owner", owner, "actor", actor)
	return session, nil
}

// RenameSession 改会话标题（B444 S7）——改名的唯一入口。参数：title 新标题（裁剪首尾空白）；expect 非空时
// 作 CAS 前值（驻场者首轮以占位名 proto.SessionUntitledTitle 改名，人先手改过就不覆盖，plan Q6 甲）；
// actor 是改名者身份，须为群主或本会话现驻场者。返回改名后的会话。错误：空标题 → ErrSessionTitle；
// 会话不存在 → ErrNoRoom；已归档 → ErrReadOnly；actor 无权 → ErrNotWriter；前值不符 → ErrTitleConflict。
// 权限判定先于写入：被拒零写入。
func (s *Service) RenameSession(id, title, expect, actor string) (proto.Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		log().Warn("会话改名被拒：标题为空", "session", id, "actor", actor)
		return proto.Session{}, ErrSessionTitle
	}
	session, err := s.lc.GetSession(id)
	if err != nil {
		log().Warn("会话改名前读会话失败", "session", id, "actor", actor, "cause", err)
		return proto.Session{}, mapSessionError(err)
	}
	if session.Archived {
		log().Warn("会话改名被拒：会话已归档", "session", id, "actor", actor)
		return proto.Session{}, fmt.Errorf("会话 %s 已归档，不能改名: %w", id, ErrReadOnly)
	}
	allowed := actor != "" && actor == session.Owner
	if !allowed {
		resident, seated, err := s.lc.SessionResident(id)
		if err != nil {
			log().Warn("会话改名前读驻场者失败", "session", id, "actor", actor, "cause", err)
			return proto.Session{}, fmt.Errorf("读会话 %s 驻场者: %w", id, err)
		}
		allowed = seated && actor != "" && resident.Identity == actor
	}
	if !allowed {
		log().Warn("会话改名被拒：既不是群主也不是驻场者", "session", id, "actor", actor, "owner", session.Owner)
		return proto.Session{}, fmt.Errorf("%s 不是会话 %s 的群主或驻场者，不能改名: %w", actor, id, ErrNotWriter)
	}
	renamed, err := s.lc.RenameSession(id, title, expect, actor)
	if err != nil {
		log().Warn("会话改名失败", "session", id, "actor", actor, "has_expect", expect != "", "cause", err)
		switch {
		case errors.Is(err, client.ErrNotFound):
			return proto.Session{}, ErrNoRoom
		case errors.Is(err, client.ErrBadState):
			return proto.Session{}, fmt.Errorf("%w（%s）", ErrReadOnly, err.Error())
		}
		return proto.Session{}, err
	}
	log().Info("会话已改名", "session", id, "title", title, "actor", actor)
	return renamed, nil
}

// ListSessions 列全部会话（含归档），member 非空时投影该成员未读数。
func (s *Service) ListSessions(member string) ([]proto.SessionSummary, error) {
	return s.ListSessionsContext(context.Background(), member)
}

// ListSessionsContext 用于 HTTP 请求，事件流读取随客户端取消而停止。
// B409.6：全部路径经 defer 统一收口一次投影日志（关联 id + 账本/装配分段 +
// 结果分类）。
func (s *Service) ListSessionsContext(ctx context.Context, member string) (summaries []proto.SessionSummary, err error) {
	t := &projectionTimer{started: time.Now()}
	defer func() {
		attrs := t.finishLogAttrs(ctx, len(summaries), err, "member", member)
		level := log().Info
		if err != nil {
			level = log().Warn
		}
		level("会话列表投影完成", attrs...)
	}()
	var sessions []proto.Session
	sessions, err = timedLedger(t, func() ([]proto.Session, error) { return s.lc.ListSessions() })
	if err != nil {
		return nil, err
	}
	var cards []proto.Card
	cards, err = timedLedger(t, func() ([]proto.Card, error) { return s.lc.ListAllCards("") })
	if err != nil {
		return nil, err
	}
	byCard := make(map[string]proto.Card, len(cards))
	for _, c := range cards {
		byCard[c.ID] = c
	}
	cardIDs := make([]string, 0)
	roomWatermarks := make(map[string]int64, len(sessions))
	for _, session := range sessions {
		cardIDs = append(cardIDs, session.Cards...)
		roomWatermarks[session.ID] = 0
	}
	if member != "" {
		var cursors map[string]int64
		cursors, err = s.cursor.Snapshot(member)
		if err != nil {
			log().Warn("会话列表组装失败：读游标快照", "member", member, "cause", err)
			return nil, err
		}
		for sessionID := range roomWatermarks {
			roomWatermarks[sessionID] = cursors[sessionID]
		}
	}
	var needsEvents []proto.LedgerEvent
	needsEvents, err = timedLedger(t, func() ([]proto.LedgerEvent, error) { return s.lc.LatestNeedsEventsContext(ctx, cardIDs) })
	if err != nil {
		log().Warn("会话列表组装失败：读成员卡 needs 状态", "sessions", len(sessions), "cards", len(cardIDs), "cause", err)
		return nil, err
	}
	needs := latestNeedsByCard(needsEvents)
	var roomSnapshots []client.RoomMessageSnapshot
	roomSnapshots, err = timedLedger(t, func() ([]client.RoomMessageSnapshot, error) {
		return s.lc.RoomMessageSnapshotsContext(ctx, roomWatermarks)
	})
	if err != nil {
		log().Warn("会话列表组装失败：读房间活动摘要", "sessions", len(sessions), "cause", err)
		return nil, err
	}
	roomByID := make(map[string]client.RoomMessageSnapshot, len(roomSnapshots))
	for _, snapshot := range roomSnapshots {
		roomByID[snapshot.RoomID] = snapshot
	}
	out := make([]proto.SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		snapshot, hasSnapshot := roomByID[session.ID]
		var lastMessage *client.RoomMessageSnapshot
		if hasSnapshot {
			lastMessage = &snapshot
		}
		// 计入租约读取的账本耗时（summarizeSession 内部经接口读账本）。
		var summary proto.SessionSummary
		timedLedgerVoid(t, func() {
			summary, err = s.summarizeSession(session, byCard, lastMessage, needs)
		})
		if err != nil {
			log().Warn("会话列表组装失败：读取成员租约", "session", session.ID, "cause", err)
			return nil, err
		}
		if member != "" {
			summary.Unread = int(snapshot.MessagesAfter)
		}
		out = append(out, summary)
	}
	summaries = out
	return summaries, nil
}

// SessionDetail 读会话详情页投影：成员与成员状态、协调者派发的任务节点、
// 会话 timeline。结构信息不进群聊流。
func (s *Service) SessionDetail(id string) (proto.SessionDetail, error) {
	return s.SessionDetailContext(context.Background(), id)
}

// SessionDetailContext 用于 HTTP 请求，取消详情投影的长事件流扫描。
// B409.6：全部路径经 defer 统一收口一次投影日志。
func (s *Service) SessionDetailContext(ctx context.Context, id string) (detail proto.SessionDetail, err error) {
	t := &projectionTimer{started: time.Now()}
	defer func() {
		attrs := t.finishLogAttrs(ctx, len(detail.Timeline), err, "session", id)
		level := log().Info
		if err != nil {
			level = log().Warn
		}
		level("会话详情投影完成", attrs...)
	}()
	var session proto.Session
	session, err = timedLedger(t, func() (proto.Session, error) { return s.lc.GetSession(id) })
	if err != nil {
		return proto.SessionDetail{}, mapSessionError(err)
	}
	var byCard map[string]proto.Card
	byCard, err = s.sessionCardsByID(ctx, session.Cards)
	if err != nil {
		return proto.SessionDetail{}, err
	}
	var events []proto.LedgerEvent
	events, err = timedLedger(t, func() ([]proto.LedgerEvent, error) {
		return s.lc.SessionProjectionEventsContext(ctx, session.ID, session.Cards)
	})
	if err != nil {
		log().Warn("会话详情组装失败：读限域投影事件", "session", session.ID, "cards", len(session.Cards), "cause", err)
		return proto.SessionDetail{}, err
	}
	needs := needsHumanByCard(events)
	var roomSnapshots []client.RoomMessageSnapshot
	roomSnapshots, err = timedLedger(t, func() ([]client.RoomMessageSnapshot, error) {
		return s.lc.RoomMessageSnapshotsContext(ctx, map[string]int64{session.ID: 0})
	})
	if err != nil {
		log().Warn("会话详情组装失败：读房间活动摘要", "session", session.ID, "cause", err)
		return proto.SessionDetail{}, err
	}
	var latestMessage *client.RoomMessageSnapshot
	if len(roomSnapshots) > 0 {
		latestMessage = &roomSnapshots[0]
	}
	detail = proto.SessionDetail{
		Nodes:    sessionNodes(session, byCard, events),
		Timeline: sessionTimeline(events, session),
	}
	timedLedgerVoid(t, func() {
		detail.Summary, err = s.summarizeSession(session, byCard, latestMessage, needs)
	})
	if err != nil {
		log().Warn("会话详情组装失败：读取成员租约", "session", session.ID, "cause", err)
		return proto.SessionDetail{}, err
	}
	return detail, nil
}

// ArchiveSession 显式归档会话；归档后只读。卡的终态不等于会话结束。
func (s *Service) ArchiveSession(id, actor string) error {
	return mapSessionError(s.lc.ArchiveSession(id, actor))
}

// JoinCard 把卡拉进会话（只建讨论面；进群 ≠ 配人，配人仍走 B307 三按钮）。
func (s *Service) JoinCard(sessionID, cardID, actor string) error {
	return mapSessionError(s.lc.JoinCardToSession(sessionID, cardID, actor))
}

// LeaveCard 把卡移出会话；幂等。
func (s *Service) LeaveCard(sessionID, cardID, actor string) error {
	return mapSessionError(s.lc.LeaveCardToSession(sessionID, cardID, actor))
}

// 驻场者写面哨兵（B435 R）。占位/不在座直接复用出站哨兵，调用方（CLI）只依赖 collab 包。
var (
	// ErrResidentIdentity 驻场者身份不是统一记法 user:<n>/agent:<n>（C-413），或是系统发信身份（Q14 甲）。
	ErrResidentIdentity = errors.New("collab: 驻场者身份非法")
	// ErrResidentOccupied 会话已有驻场者（C-414）。
	ErrResidentOccupied = client.ErrResidentOccupied
	// ErrResidentNotSeated 要请下的身份不是现驻场者。
	ErrResidentNotSeated = client.ErrResidentNotSeated
	// ErrResidentSeatConflict push 驻场者的 CLI 会话正坐卡席位（B444 P2 返工三 裁决 2）。
	ErrResidentSeatConflict = client.ErrResidentSeatConflict
)

// SitResident 登记会话房间驻场者——驻场者写面的唯一入口（R plan Q11 甲；O1 的
// `sit --project` 复用同一校验）。参数 r：Session 须为会话房间 id；Identity 须为统一记法
// （proto.ValidateMemberIdentity，C-413）且不得是系统发信身份 agent:handoff（Q14 甲：比
// C-413 更严——否则系统通知会自写自收，且 Q4 后它成为书写者，冒充面扩大）；桌面端三列由
// 调用方校验形状后原样落库。返回账本写下的一行（含 SatAt）。错误：非会话房间/会话不存在 →
// ErrNoRoom；记法非法 → ErrResidentIdentity；已归档 → ErrReadOnly；已有驻场者 →
// ErrResidentOccupied。校验失败零写入。
func (s *Service) SitResident(r proto.SessionResident) (proto.SessionResident, error) {
	if !room.IsSessionRoom(r.Session) {
		log().Warn("登记驻场者被拒：不是会话房间", "room", r.Session, "identity", r.Identity)
		return proto.SessionResident{}, fmt.Errorf("%q 不是会话房间（须形如 session:<n>）: %w", r.Session, ErrNoRoom)
	}
	if !proto.ValidateMemberIdentity(r.Identity) {
		log().Warn("登记驻场者被拒：身份非统一记法", "session", r.Session, "identity", r.Identity)
		return proto.SessionResident{}, fmt.Errorf("驻场者身份 %q 须为统一记法 user:<名字> 或 agent:<名字>: %w", r.Identity, ErrResidentIdentity)
	}
	if r.Identity == proto.SystemNotifierIdentity {
		log().Warn("登记驻场者被拒：系统发信身份不得驻场", "session", r.Session, "identity", r.Identity)
		return proto.SessionResident{}, fmt.Errorf("%s 是系统发信身份，不能当驻场者: %w", r.Identity, ErrResidentIdentity)
	}
	if err := validateResidentDriver(r); err != nil {
		log().Warn("登记驻场者被拒：驱动形态与载体落点不成形", "session", r.Session, "identity", r.Identity,
			"driver_mode", r.DriverMode, "cause", err)
		return proto.SessionResident{}, err
	}
	got, err := s.lc.SitResident(r)
	if err != nil {
		mapped := mapResidentError(err)
		log().Warn("登记驻场者失败", "session", r.Session, "identity", r.Identity, "cause", err)
		return proto.SessionResident{}, mapped
	}
	log().Info("驻场者已登记", "session", r.Session, "identity", r.Identity, "has_desktop", r.DesktopSession != "",
		"driver_mode", r.DriverMode, "carrier", r.Carrier)
	return got, nil
}

// validateResidentDriver 校验驻场者行的驱动形态与载体落点成形（B444 P1，plan A1.7）：
//   - push：身份必须是 agent:（push 驻场者记 agent:<小队名>；user: 是人，不能由载体冒用）；
//     push_cli、carrier 必填；push_session 可空（首轮在途，B444 P2 返工 F1）；不得同时带桌面端两列——
//     门铃只看 cli_session，带上就会门铃与 keystone 双重唤醒（缺陷族 R2）；
//   - desktop（空串）：不得带任何 push/bearing 字段（同理：半个 bearing 不是可续轮的落点）；
//   - 其余值：词表外，拒。
//
// 返回 nil 或包 ErrResidentIdentity 的错误（与身份记法错同一哨兵，HTTP/CLI 映射不新增分支）。
func validateResidentDriver(r proto.SessionResident) error {
	hasBearing := r.PushCLI != "" || r.PushSession != "" || r.Carrier != "" || r.CarrierMachine != "" ||
		r.HomeDir != "" || r.Workdir != "" || r.Model != ""
	switch r.DriverMode {
	case proto.ResidentDriverDesktop:
		if hasBearing {
			return fmt.Errorf("桌面端驻场者不得带 push 载体落点字段: %w", ErrResidentIdentity)
		}
		return nil
	case proto.ResidentDriverPush:
		kind, _, err := proto.ParseMemberIdentity(r.Identity)
		if err != nil || kind != proto.IdentityKindAgent {
			return fmt.Errorf("push 驻场者身份须为 agent:<小队名>，收到 %q: %w", r.Identity, ErrResidentIdentity)
		}
		// push_session 可空 = 首轮在途（B444 P2 返工 F1）：编排先坐下再拉起首轮，让驻场者首轮就是书写者，
		// 首轮返回会话 id 后经 SetResidentPushSession 补写；在途期间唤醒循环按「暂缓」处理不续轮。
		if r.PushCLI == "" || r.Carrier == "" {
			return fmt.Errorf("push 驻场者须带 push_cli、carrier（得 cli=%q carrier=%q）: %w",
				r.PushCLI, r.Carrier, ErrResidentIdentity)
		}
		if r.DesktopSession != "" || r.CLISession != "" {
			return fmt.Errorf("push 驻场者不得同时登记桌面端地址（会与门铃双重唤醒）: %w", ErrResidentIdentity)
		}
		return nil
	default:
		return fmt.Errorf("驻场者驱动形态 %q 不在词表（空=desktop | push）: %w", r.DriverMode, ErrResidentIdentity)
	}
}

// ErrResidentPushSessionSet 补写 push_session 时驻场行已不是首轮在途（已补写过或不是 push 行），原值不变。
var ErrResidentPushSessionSet = client.ErrCASConflict

// SetResidentPushSession 给首轮在途的 push 驻场者补写 push 会话 id（B444 P2 返工 F1；collab 只存事实，
// 拉起由 agentd 编排）。identity 须等于现驻场者（CAS）。错误：非会话房间 → ErrNoRoom；身份不是现驻场者
// 或无驻场者 → ErrResidentNotSeated；已补写过 → ErrResidentPushSessionSet；pushSession 为空 → ErrResidentIdentity。
func (s *Service) SetResidentPushSession(sessionID, identity, pushSession string) error {
	if !room.IsSessionRoom(sessionID) {
		log().Warn("补写驻场者 push 会话被拒：不是会话房间", "room", sessionID, "identity", identity)
		return fmt.Errorf("%q 不是会话房间（须形如 session:<n>）: %w", sessionID, ErrNoRoom)
	}
	if strings.TrimSpace(pushSession) == "" {
		return fmt.Errorf("补写驻场者 push 会话：会话 id 为空: %w", ErrResidentIdentity)
	}
	if err := s.lc.SetResidentPushSession(sessionID, identity, pushSession); err != nil {
		log().Warn("补写驻场者 push 会话失败", "session", sessionID, "identity", identity, "cause", err)
		return mapResidentError(err)
	}
	log().Info("驻场者 push 会话已补写", "session", sessionID, "identity", identity, "push_session", pushSession)
	return nil
}

// InflightPushResidents 列首轮在途的 push 驻场者（push_session 空，B444 启动清扫读面）。collab 只给事实，
// 归属哪台机器、是否请下由 agentd 判定。
func (s *Service) InflightPushResidents() ([]proto.SessionResident, error) {
	rows, err := s.lc.InflightPushResidents()
	if err != nil {
		log().Warn("列首轮在途驻场者失败", "cause", err)
		return nil, err
	}
	return rows, nil
}

// StandResident 请下会话房间驻场者（`session stand`，Q7 甲）：identity 须等于现驻场者，否则
// ErrResidentNotSeated、登记不变。非会话房间 → ErrNoRoom。
func (s *Service) StandResident(sessionID, identity string) error {
	if !room.IsSessionRoom(sessionID) {
		log().Warn("请下驻场者被拒：不是会话房间", "room", sessionID, "identity", identity)
		return fmt.Errorf("%q 不是会话房间（须形如 session:<n>）: %w", sessionID, ErrNoRoom)
	}
	if err := s.lc.StandResident(sessionID, identity); err != nil {
		log().Warn("请下驻场者失败", "session", sessionID, "identity", identity, "cause", err)
		return mapResidentError(err)
	}
	log().Info("驻场者已请下", "session", sessionID, "identity", identity)
	return nil
}

// mapResidentError 把驻场者写面的出站哨兵映射成会话门面错误：不存在 → ErrNoRoom，
// 状态不允许（已归档）→ ErrReadOnly；占位/不在座哨兵与其余错误原样上抛（保留上下文）。
func mapResidentError(err error) error {
	switch {
	case errors.Is(err, client.ErrNotFound):
		return fmt.Errorf("%w（%s）", ErrNoRoom, err.Error())
	case errors.Is(err, client.ErrBadState):
		return fmt.Errorf("%w（%s）", ErrReadOnly, err.Error())
	default:
		return err
	}
}

// ErrProjectName 项目名形状非法（Q15 甲：裁剪后为空，或含空白/控制字符）。
var ErrProjectName = errors.New("collab: 项目名非法")

// validProjectName 是项目名形状的唯一判据（项目房间与 B444 会话所属项目共用）：非空且不含空白与控制
// 字符。非法返回包 ErrProjectName 的可行动错误。
func validProjectName(project string) error {
	if strings.TrimSpace(project) == "" || strings.IndexFunc(project, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("项目名 %q 须非空且不含空白与控制字符（即卡的 project 字段值）: %w", project, ErrProjectName)
	}
	return nil
}

// EnsureProjectRoom 取项目的项目房间，不存在则创建——项目房间创建的唯一入口（O1 plan Q3 甲；`handoff
// project room <p>` 与 `session sit --project <p>` 共用）。参数：project 为卡 project 字段的比较键
// （调用方已裁剪首尾空白；本层只校验形状，不要求账本已有该项目的卡，Q15 甲）；actor 落 session_created。
// 新建时群主 = 系统发信身份 agent:handoff（Q1 甲：群主即首个显式成员，系统通知因此是书写者，零书写者
// 规则改动）。返回会话本体与是否本次新建。错误：项目名非法 → ErrProjectName（零写入）；其余为账本错误。
// 注意：项目房间创建时零卡、不执法拉卡（Q12 甲）；归档后绑定不变（Q11 甲）。
func (s *Service) EnsureProjectRoom(project, actor string) (proto.Session, bool, error) {
	if err := validProjectName(project); err != nil {
		log().Warn("确保项目房间被拒：项目名非法", "project", project, "actor", actor)
		return proto.Session{}, false, err
	}
	session, created, err := s.lc.EnsureProjectRoom(project, proto.SystemNotifierIdentity, actor)
	if err != nil {
		log().Warn("确保项目房间失败", "project", project, "actor", actor, "cause", err)
		return proto.Session{}, false, err
	}
	log().Info("项目房间已确保", "project", project, "session", session.ID, "created", created, "archived", session.Archived)
	return session, created, nil
}

// WakeTargets 解析一条 room_message 的寻址，返回被唤醒成员身份集；空集表示
// 不唤醒任何人。规则：
//   - @卡号 解析为该卡当前席位（换绑后指向新席位）；解析不到卡时按外部会话
//     身份原样使用；
//   - reply_to 指向被回复消息的作者（隐式寻址原作者，一人）；作者若是某卡当前
//     席位则保持该席位身份（换绑后再次 @ 该卡仍命中新席位）；
//   - 系统结构行（by_system / pointer）恒不唤醒。
func (s *Service) WakeTargets(ev proto.LedgerEvent) ([]string, error) {
	var msg proto.RoomMessage
	if err := room.UnmarshalMessage(ev.Payload, &msg); err != nil {
		return nil, err
	}
	return s.MessageWakeTargets(msg)
}

// MessageWakeTargets 是 WakeTargets 的消息级半边（已在手解析好 replyAuthor 的
// 调用方用它）。签名里没有成员集合，扇出形状写不出来。B435 起 push 消费循环与
// card wait 改走匹配函数 Recipients/Matches（recipients.go）；本函数仅剩 session wait
// （cmd/session.go，后续子卡 R 改接后 collab 外零调用，C-241）。
func (s *Service) MessageWakeTargets(msg proto.RoomMessage) ([]string, error) {
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return nil, nil
	}
	replyAuthor, err := s.replyAuthorOf(msg)
	if err != nil {
		return nil, err
	}
	return s.resolveMessageTargets(msg, replyAuthor)
}

// resolveMessageTargets 解析消息寻址；@卡号 解析到该卡当前席位。
// 查询错误必须上抛：补拉扫描若误判为未命中，后续命中的持久水位会越过该消息。
func (s *Service) resolveMessageTargets(msg proto.RoomMessage, replyAuthor string) ([]string, error) {
	cards := make(map[string]proto.Card, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		mention = strings.TrimSpace(mention)
		if mention == "" {
			continue
		}
		card, err := s.lc.GetCard(mention)
		if errors.Is(err, client.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("解析会话寻址 %q: %w", mention, err)
		}
		cards[mention] = card
	}
	resolveSeat := func(mention string) (string, bool) {
		card, ok := cards[mention]
		return card.DriverSession, ok
	}
	return room.ResolveDelivery(msg, replyAuthor, resolveSeat), nil
}

// replyEventOf 按全局 seq 点读引用条；不存在、跨房间或非消息都不是有效回复。
// 单行升序读避免恢复积压时每条 reply 都重扫完整账本。
func (s *Service) replyEventOf(msg proto.RoomMessage) (*proto.LedgerEvent, error) {
	if msg.ReplyTo <= 0 || msg.Room == "" {
		return nil, nil
	}
	events, err := s.lc.EventsFromAsc(nil, msg.ReplyTo-1, 1)
	if err != nil {
		return nil, fmt.Errorf("读取回复事件 %d: %w", msg.ReplyTo, err)
	}
	if len(events) == 0 || events[0].Seq != msg.ReplyTo || events[0].Type != room.RoomEventType || !room.SameRoom(events[0], msg.Room) {
		return nil, nil
	}
	return &events[0], nil
}

// MessageReference 取回复引用摘要；旧消息不受 History 默认 200 条窗口限制。
func (s *Service) MessageReference(msg proto.RoomMessage) (*proto.SessionCite, error) {
	event, err := s.replyEventOf(msg)
	if err != nil || event == nil {
		return nil, err
	}
	var ref proto.RoomMessage
	if err := room.UnmarshalMessage(event.Payload, &ref); err != nil {
		return nil, fmt.Errorf("解码回复事件 %d: %w", event.Seq, err)
	}
	return &proto.SessionCite{Seq: event.Seq, Room: msg.Room, Actor: event.Actor, Body: ref.Body}, nil
}

// replyAuthorOf 找同会话被回复消息的作者；找不到返回空串。
func (s *Service) replyAuthorOf(msg proto.RoomMessage) (string, error) {
	event, err := s.replyEventOf(msg)
	if err != nil || event == nil {
		return "", err
	}
	return event.Actor, nil
}

// summarizeSession 组装会话列表/详情头部的会话摘要。
func (s *Service) summarizeSession(session proto.Session, byCard map[string]proto.Card,
	latestMessage *client.RoomMessageSnapshot, needs map[string]bool) (proto.SessionSummary, error) {
	members, err := s.sessionMembers(session, byCard)
	if err != nil {
		return proto.SessionSummary{}, err
	}
	// 项目房间标记（B435 ⑤ C-510–C-512，O1 plan Q9 甲）：本函数是列表与详情头的唯一装配点，故 project
	// 只在此填；读失败上抛——静默缺键会让控制台把项目房间显示成普通会话。
	project, err := s.lc.SessionProject(session.ID)
	if err != nil {
		log().Warn("会话项目绑定读取失败", "session", session.ID, "cause", err)
		return proto.SessionSummary{}, fmt.Errorf("读取会话 %s 项目绑定: %w", session.ID, err)
	}
	summary := proto.SessionSummary{
		ID: session.ID, Kind: proto.SessionKind, Title: session.Title,
		Owner: session.Owner, Archived: session.Archived, Project: project, HomeProject: session.HomeProject,
		LastActivity: session.UpdatedAt,
		Members:      members,
		Cards:        sessionCards(session, byCard),
	}
	for _, cardID := range session.Cards {
		if needs[cardID] {
			summary.NeedsHuman = true
			break
		}
	}
	if latestMessage != nil {
		if latestMessage.LastActivity.After(summary.LastActivity) {
			summary.LastActivity = latestMessage.LastActivity
		}
		var msg proto.RoomMessage
		if err := room.UnmarshalMessage(latestMessage.Latest.Payload, &msg); err != nil {
			log().Warn("会话摘要预览投影跳过：消息载荷无效", "session", session.ID,
				"seq", latestMessage.Latest.Seq, "cause", err)
		} else {
			summary.Preview = &proto.RoomPreview{
				Body: truncateRoomPreview(msg.Body), Seq: latestMessage.Latest.Seq, CreatedAt: latestMessage.Latest.CreatedAt,
			}
		}
	}
	return summary, nil
}

func (s *Service) sessionCardsByID(ctx context.Context, cardIDs []string) (map[string]proto.Card, error) {
	byCard := make(map[string]proto.Card, len(cardIDs))
	for _, cardID := range cardIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		card, err := s.lc.GetCard(cardID)
		if errors.Is(err, client.ErrNotFound) {
			continue
		}
		if err != nil {
			log().Warn("会话详情组装失败：读成员卡", "card", cardID, "cause", err)
			return nil, err
		}
		byCard[card.ID] = card
	}
	return byCard, nil
}

func latestNeedsByCard(events []proto.LedgerEvent) map[string]bool {
	needs := make(map[string]bool, len(events))
	for _, event := range events {
		needs[event.CardID] = event.Type == "needs_human"
	}
	return needs
}

// sessionMembers 派生会话成员：显式成员（人/主 agent）+ 房间驻场者（B435）+ 会话内各卡的当前席位。
// 状态只报可证实的——有未过期租约报 working/listening，否则报 last_active；
// 空座报 empty。不报「在线」（外部会话随时可能被关掉）。
//
// 显式成员 kind 按统一记法前缀判定（B358.9 契约 §5 H 组条 39/40）：user:→human、
// agent:→agent。历史脏行（旧 web:/cli: 脸）不崩：kind 按最保守的 human 兜底并留痕。
func (s *Service) sessionMembers(session proto.Session, byCard map[string]proto.Card) ([]proto.SessionMember, error) {
	members := make([]proto.SessionMember, 0, len(session.Members)+len(session.Cards))
	for _, identity := range session.Members {
		kind := proto.SessionMemberHuman
		parsedKind, _, err := proto.ParseMemberIdentity(identity)
		switch {
		case err != nil:
			log().Warn("会话成员身份非统一记法，kind 按 human 兜底",
				"session", session.ID, "identity", identity, "cause", err)
		case parsedKind == proto.IdentityKindAgent:
			kind = proto.SessionMemberAgent
		}
		status, lastActive, err := s.memberStatus(identity)
		if err != nil {
			log().Warn("会话成员租约读取失败", "session", session.ID, "identity", identity, "cause", err)
			return nil, fmt.Errorf("读取会话 %s 成员 %s 租约: %w", session.ID, identity, err)
		}
		members = append(members, proto.SessionMember{
			Identity: identity, Kind: kind, Status: status, LastActive: lastActive,
		})
	}
	// 房间驻场者（B435 C-208、契约 §13 第 5 条）：显式成员之后、卡席位之前恰一行；驻场者
	// `card bind` 升格后同一人另以 seat 行出现（两种记法，N2）。读失败上抛——静默少一行会让
	// 详情页与判定表（驻场者是默认收件人）说两种话。
	resident, seated, err := s.lc.SessionResident(session.ID)
	if err != nil {
		log().Warn("会话驻场者读取失败", "session", session.ID, "cause", err)
		return nil, fmt.Errorf("读取会话 %s 驻场者: %w", session.ID, err)
	}
	if seated {
		status, lastActive, err := s.memberStatus(resident.Identity)
		if err != nil {
			log().Warn("会话驻场者租约读取失败", "session", session.ID, "identity", resident.Identity, "cause", err)
			return nil, fmt.Errorf("读取会话 %s 驻场者 %s 租约: %w", session.ID, resident.Identity, err)
		}
		members = append(members, proto.SessionMember{
			Identity: resident.Identity, Kind: proto.SessionMemberResident, Status: status, LastActive: lastActive,
			DriverMode: resident.DriverMode, Carrier: resident.Carrier,
			CarrierMachine: resident.CarrierMachine, Model: resident.Model,
		})
	}
	for _, cardID := range session.Cards {
		card, ok := byCard[cardID]
		if !ok {
			continue
		}
		member := proto.SessionMember{
			Kind: proto.SessionMemberSeat, CardID: cardID, CardTitle: card.Title,
		}
		if card.DriverSession == "" {
			member.Status = proto.SessionMemberEmpty
		} else {
			member.Identity = card.DriverSession
			status, lastActive, err := s.memberStatus(card.DriverSession)
			if err != nil {
				log().Warn("会话席位租约读取失败", "session", session.ID,
					"card", cardID, "identity", card.DriverSession, "cause", err)
				return nil, fmt.Errorf("读取会话 %s 卡 %s 席位租约: %w", session.ID, cardID, err)
			}
			member.Status, member.LastActive = status, lastActive
		}
		members = append(members, member)
	}
	return members, nil
}

// memberStatus 判定成员状态：有未过期租约报 working（活跃租约视为可续租），
// 缺租约或已过期时报 last_active；租约读取失败向摘要组装方返回错误，不能伪装离线。
func (s *Service) memberStatus(identity string) (string, time.Time, error) {
	expiresAt, exists, err := s.lc.DriverLease(identity)
	if err != nil {
		return "", time.Time{}, err
	}
	if !exists {
		return proto.SessionMemberLastActive, time.Time{}, nil
	}
	if expiresAt.After(nowFn()) {
		return proto.SessionMemberWorking, expiresAt, nil
	}
	return proto.SessionMemberLastActive, expiresAt, nil
}

// sessionCards 投影会话里的工作项卡（空座卡 Seat 为空串）。
func sessionCards(session proto.Session, byCard map[string]proto.Card) []proto.SessionCard {
	out := make([]proto.SessionCard, 0, len(session.Cards))
	for _, cardID := range session.Cards {
		card, ok := byCard[cardID]
		if !ok {
			out = append(out, proto.SessionCard{CardID: cardID})
			continue
		}
		out = append(out, proto.SessionCard{
			CardID: card.ID, Title: card.Title, Status: card.Status, Seat: card.DriverSession,
			SeatMode: card.DriverMode,
		})
	}
	return out
}

// sessionNodes 从卡挂账/镜像投影协调者派发的任务节点（不新增执行域依赖）。
func sessionNodes(session proto.Session, byCard map[string]proto.Card, events []proto.LedgerEvent) []proto.SessionNode {
	out := []proto.SessionNode{}
	for _, cardID := range session.Cards {
		for _, ev := range events {
			if ev.CardID != cardID || ev.Type != "task_mirrored" {
				continue
			}
			var envelope struct {
				Node     *string `json:"node"`
				TaskType string  `json:"task_type"`
			}
			if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
				log().Warn("task_mirrored 载荷不可解析，节点投影跳过该行",
					"seq", ev.Seq, "card", ev.CardID, "cause", err)
				continue
			}
			if envelope.Node == nil {
				log().Warn("task_mirrored 载荷缺 node 字段，节点投影跳过该行",
					"seq", ev.Seq, "card", ev.CardID)
				continue
			}
			out = append(out, proto.SessionNode{
				CardID: cardID, Node: *envelope.Node, State: envelope.TaskType,
			})
		}
	}
	return out
}

// structEventFields 解析结构事件载荷为键值表；解析失败留日志并返回 nil，
// 调用方跳过该行（缺陷族 2：静默跳过只许丢一行，不吞整段 timeline）。
func structEventFields(ev proto.LedgerEvent) map[string]any {
	var fields map[string]any
	if err := json.Unmarshal(ev.Payload, &fields); err != nil {
		log().Warn("会话结构事件载荷不可解析，timeline 跳过该行",
			"seq", ev.Seq, "type", ev.Type, "cause", err)
		return nil
	}
	return fields
}

// sessionTimeline 投影会话 timeline：结构事实（建群/归档/进群/移出）+
// 席位变更（换绑）。归属判据（契约 §9「仅当事件所属卡在该会话内时归入」的
// 全族应用，B358.2 修复 breakdown 发现 A）：
//   - 无卡结构事件：载荷归属键（created 是 id——与 ledger 写面
//     map{"id",...} 同形；其余是 session）等于本会话 id 才归入；
//   - 卡事件：仅当 ev.CardID ∈ 本会话当前 Cards 才归入。判定按当前成员——
//     卡移出后其历史行不再出现在本会话 timeline（契约 §9 字面）。
//
// Ticket 0 偏差：driver_takeover 曾用全量卡表判归属（任何卡的换绑落进每场
// 会话）、created 曾查错载荷键（session vs id，过滤恒空）、joined/left 曾无
// 判定——三处同族一并修复，反例见 TestSessionTimelineBelongsToOneSession。
func sessionTimeline(events []proto.LedgerEvent, session proto.Session) []proto.SessionTimelineEvent {
	inSession := make(map[string]bool, len(session.Cards))
	for _, cardID := range session.Cards {
		inSession[cardID] = true
	}
	out := []proto.SessionTimelineEvent{}
	for _, ev := range events {
		kind := ""
		cardID := ""
		detail := ""
		switch ev.Type {
		case "session_created":
			fields := structEventFields(ev)
			if fields == nil || fields["id"] != session.ID {
				continue
			}
			kind = proto.SessionEventCreated
		case "session_archived":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			kind = proto.SessionEventArchived
		case "session_card_joined":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			card, _ := fields["card"].(string)
			kind, cardID = proto.SessionEventCardJoined, card
		case "session_card_left":
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			card, _ := fields["card"].(string)
			kind, cardID = proto.SessionEventCardLeft, card
		case "session_renamed":
			// B444 S7：改名结构事实，归属判据同其余无卡结构事件（载荷 session 键）；载荷 {session,title,from}
			// 原样透传 Detail（可对质）。
			fields := structEventFields(ev)
			if fields == nil || fields["session"] != session.ID {
				continue
			}
			kind, detail = proto.SessionEventRenamed, string(ev.Payload)
		case "driver_takeover":
			// 席位变更：仅当事件所属卡在该会话内时归入本 timeline（契约 §9）。
			// detail 原样透传 {from,to} 载荷——可对质，不做二次解释（缺陷族 6）。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventSeatRebound, ev.CardID, string(ev.Payload)
		case "driver_seat_bound":
			// R2（契约 §4.7 条 54）：EvDriverSeatBound 是 seat_bound 行的唯一
			// 载体（唯一定义点 internal/ledger/types.go——collab 非测试零 import
			// ledger，契约条 36，故字面量 + 注释指认）；归属判据沿用 §9「仅当
			// 事件所属卡在该会话内」。载荷 {to}、actor=席位自称，detail 原样
			// 透传（可对质，缺陷族 6）。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventSeatBound, ev.CardID, string(ev.Payload)
		case "status_moved":
			// R3（契约 §4.8 条 56）：载体是既有 EvStatusMoved。当且仅当
			// to ∈ {已完成, 终止} 记 card_closed（字面值即 ledger.StatusDone /
			// ledger.StatusClosed；漂移由测试用 ledger 常量生产事件钉住）；
			// 普通列间转移不是结构事实。终止的 reason 随 payload 透传 Detail。
			if !inSession[ev.CardID] {
				continue
			}
			switch payloadString(ev.Payload, "to") {
			case "已完成", "终止":
				kind, cardID, detail = proto.SessionEventCardClosed, ev.CardID, string(ev.Payload)
			default:
				continue
			}
		case "needs_human":
			// 等人标记进 timeline（proto.SessionEventNeedsHuman；spec §4.3 末条
			// 「needs_human 亮起归详情页 timeline」）。归属判据同 §9；载荷
			// {reason} 原样透传 Detail。needs_cleared 不进 timeline——词表 8 值
			// 无它（补签轮逐值冻结），标签熄灭由 needsHumanByCard 承担。
			if !inSession[ev.CardID] {
				continue
			}
			kind, cardID, detail = proto.SessionEventNeedsHuman, ev.CardID, string(ev.Payload)
		default:
			// 未知 ledger 事件类型不进 timeline（缺陷族 7：不得被默认分支收编）。
			// seat_bound / card_closed / needs_human 分支已增补（Task 2/3）。
			continue
		}
		out = append(out, proto.SessionTimelineEvent{
			Seq: ev.Seq, Kind: kind, CardID: cardID, Detail: detail,
			Actor: ev.Actor, CreatedAt: ev.CreatedAt,
		})
	}
	return out
}

// needsHumanByCard 扫描全流，返回当前处于 needs_human 未清除的卡集合。
func needsHumanByCard(events []proto.LedgerEvent) map[string]bool {
	out := map[string]bool{}
	for _, ev := range events {
		switch ev.Type {
		case "needs_human":
			if ev.CardID != "" {
				out[ev.CardID] = true
			}
		case "needs_cleared":
			delete(out, ev.CardID)
		}
	}
	return out
}

// payloadString 从事件载荷取一个字符串键。
func payloadString(raw []byte, key string) string {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return ""
	}
	value, _ := fields[key].(string)
	return value
}

// mapSessionError 把出站哨兵 client.ErrNotFound 翻译为 collab.ErrNoRoom
// （gateway 据此映射 404）。
func mapSessionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, client.ErrNotFound) {
		return ErrNoRoom
	}
	return err
}
