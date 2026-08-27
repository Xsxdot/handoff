// Package collab 是协作房间域（B156.2）的入站门面：房间消息播发、白名单
// 与书写者执法、@定向消费队列与注意力面读模型的唯一入口。外界（gateway、
// CLI）只 import 本包与 proto DTO，不得深入内部；对外部账本的能力需求经
// client.LedgerClient 接口由组装点绑定（双向门面，架构法第四条第 3 档）。
//
// 根包只保留门面：执法规则（房间解析、只读判定、书写者校验）与读侧过滤
// 助手全部沉在 internal/collab/room 子包；未读游标介质在 internal/collab/cursor
// 子包；错误哨兵定义在 room、由本包再导出。
//
// 与 d_sessions（终端 PTY 回放域）同名不同物：本包管协作房间，不管 PTY。
package collab

import (
	"log/slog"
	"sort"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/collab/cursor"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/proto"
)

// log 结构化 logger（同 ledger 包 log() 先例，slog.Default()）。
func log() *slog.Logger { return slog.Default() }

// nowFn 可注入时钟，仅供测试替换（breakdown：活性判定所需时钟用 collab 包内
// 变量注入，不改构造器）。生产不设，回退到 time.Now。ListRooms 的 live 字段
// 判定读它；测试把它与假 client 的租约 expiresAt 拨到同一可拨源，防「判据
// 读真实时钟、夹具拨别的钟」的假绿。
var nowFn = time.Now

// 错误哨兵。调用方（CLI/agentd）按哨兵翻译为退出码或 HTTP 状态：
// ErrNoRoom→400/404、ErrKindNotAllowed→400、ErrNotWriter→403、
// ErrReadOnly/ErrNotBound→409。定义在 room 子包（执法内核产生它们），
// 本包再导出以保持 collab.ErrXxx 契约符号不变。
var (
	ErrNoRoom         = room.ErrNoRoom
	ErrKindNotAllowed = room.ErrKindNotAllowed
	ErrReadOnly       = room.ErrReadOnly
	ErrNotWriter      = room.ErrNotWriter
	ErrNotBound       = room.ErrNotBound
)

// historyDefaultLimit History/Mentions 未给 limit 时的取数上限。
const historyDefaultLimit = 200

// roomPreviewMaxRunes 限制列表 wire 中携带的最后一条正文，避免单条消息把列表响应撑大。
const roomPreviewMaxRunes = 120

// pointerActor Pointer 写指针行时落账的 actor 标识（契约 §3.3 Pointer 无
// actor 参数、调用方身份不可得，固定系统组件标识）。
const pointerActor = "system:pointer"

// Service 协作房间域入站门面。依赖只收 client 接口。
type Service struct {
	lc     client.LedgerClient
	cursor *cursor.Store
}

// New 组装点调用；lc 的具体实现是 internal/ledger/api.Facade。未读游标默认
// 纯内存（拍板 5.4 降为缓存）；组装点持 DataDir 时用 SetCursorStore 换文件介质。
func New(lc client.LedgerClient) *Service {
	return &Service{lc: lc, cursor: cursor.New("")}
}

// SetCursorStore 把未读游标换成指定介质（移交区 A.1 岔口四方案甲：datadir 下
// JSON 文件缓存，tmp+rename 原子写）。组装点（agentd server / CLI main）持
// DataDir，用 cursor.New(filepath.Join(cfg.DataDir, "room-cursors.json")) 接线；
// 不调用则保持 New 的纯内存默认——游标非权威，重启丢失无害、打开房间即自愈。
func (s *Service) SetCursorStore(st *cursor.Store) { s.cursor = st }

// Send 发消息：白名单 + 书写者执法的唯一写入口（pointer 除外——它只能走
// Pointer，经本方法一律拒收）。返回落账 seq。
//
// 执法矩阵（契约 §3.3 逐条 + §4 冻结清单）：kind 词表校验（pointer 拒收）
// → 房间解析（room.Resolve：存在性 + 只读判定）→ 只读拒写 → 按 kind 的
// 书写者校验（room.VerifyWriter）→ 经 client 发布。全部规则实现在 room
// 子包，本方法只做编排。
func (s *Service) Send(roomID string, msg proto.RoomMessage, actor string) (int64, error) {
	if !room.KindAllowed(msg.Kind) || msg.Kind == proto.RoomMsgPointer {
		return 0, room.ErrKindNotAllowed
	}
	r, err := room.Resolve(s.lc, roomID)
	if err != nil {
		return 0, err
	}
	if r.ReadOnly {
		return 0, room.ErrReadOnly
	}
	if err := room.VerifyWriter(r, msg.Kind, actor); err != nil {
		return 0, err
	}
	msg.Room = roomID
	seq, err := s.lc.RecordRoomMessage(r.CardID, msg, actor)
	if err != nil {
		return 0, err
	}
	log().Info("房间消息已落账", "room", roomID, "kind", msg.Kind, "actor", actor, "seq", seq)
	return seq, nil
}

// Pointer 系统组件写指针行的专用入口；HTTP/CLI 面不得路由它（冻结清单有
// 对应断言）。kind=pointer 与 BySystem=true 由本方法自置（rooms.go 注释
// 「本键只应由 Service.Pointer 置真」），调用方只递 roomID 与正文；房间
// 解析复用 room.Resolve，并入冻结/终态归档房间拒写（ErrReadOnly）。
func (s *Service) Pointer(roomID string, msg proto.RoomMessage) (int64, error) {
	r, err := room.Resolve(s.lc, roomID)
	if err != nil {
		return 0, err
	}
	if r.ReadOnly {
		return 0, room.ErrReadOnly
	}
	msg.Kind = proto.RoomMsgPointer
	msg.BySystem = true
	msg.Room = roomID
	seq, err := s.lc.RecordRoomMessage(r.CardID, msg, pointerActor)
	if err != nil {
		return 0, err
	}
	log().Info("指针行已落账", "room", roomID, "card", r.CardID, "seq", seq)
	return seq, nil
}

// History 读房间历史：seq 游标分页（beforeSeq 排他、升序截尾），只返回
// type=room_message 的事件。limit<=0 取 historyDefaultLimit。
func (s *Service) History(roomID string, beforeSeq int64, limit int) ([]proto.LedgerEvent, error) {
	if limit <= 0 {
		limit = historyDefaultLimit
	}
	events, err := room.ReadAllEvents(s.lc, beforeSeq)
	if err != nil {
		return nil, err
	}
	out := make([]proto.LedgerEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type != room.RoomEventType || !room.SameRoom(ev, roomID) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Pending @定向消费队列（读侧）：全流扫描「mentions∋consumer 且尚无本人
// message_consumed 标记的群级消息」∪「consumer 所绑卡房间内未消费的用户
// 留言（kind==user）」。所绑卡房间 = ListAllCards 里 DriverSession==consumer
// 的全部卡（含终态/并入卡——只读冻结防新写，不抹旧留言，杜绝无人消费的黑洞）。
func (s *Service) Pending(consumer string) ([]proto.LedgerEvent, error) {
	events, err := room.ReadAllEvents(s.lc, 0)
	if err != nil {
		log().Warn("待消费队列组装失败：读事件流", "consumer", consumer, "cause", err)
		return nil, err
	}
	consumed := room.ConsumedSeqs(events, consumer)
	bound := map[string]bool{}
	cards, err := s.lc.ListAllCards("")
	if err != nil {
		log().Warn("待消费队列组装失败：读卡列表", "consumer", consumer, "cause", err)
		return nil, err
	}
	for _, c := range cards {
		if c.DriverSession == consumer {
			bound[c.ID] = true
		}
	}
	out := []proto.LedgerEvent{}
	for _, ev := range events {
		if ev.Type != room.RoomEventType || consumed[ev.Seq] {
			continue
		}
		if ev.CardID == "" {
			if room.MentionsMember(ev, consumer) {
				out = append(out, ev)
			}
			continue
		}
		if bound[ev.CardID] && room.MessageKind(ev) == proto.RoomMsgUser {
			out = append(out, ev)
		}
	}
	log().Info("待消费队列已组装", "consumer", consumer, "count", len(out))
	return out, nil
}

// Consume 消费落账（恰好一次的权威在账本 message_consumed 事件，拍板 5.4）。
// 幂等：同参重复消费返回 nil；seq 不存在或非 room_message 同样幂等 nil
// （岔口六方案甲——「已消费」目标态对不存在消息天然成立，静默面由
// Pending/Mentions 只列未消费兜底）。查重与写入的原子性在账本同 mutate
// 事务内（C2 已锁），本方法只负责定位消息所在卡并把 cardID 传给 client。
func (s *Service) Consume(seq int64, consumer string) error {
	events, err := room.ReadAllEvents(s.lc, 0)
	if err != nil {
		log().Warn("消费失败：读事件流", "seq", seq, "consumer", consumer, "cause", err)
		return err
	}
	for _, ev := range events {
		if ev.Seq != seq {
			continue
		}
		if ev.Type != room.RoomEventType {
			return nil // 非 room_message：幂等 nil，不落标记
		}
		if err := s.lc.RecordMessageConsumed(ev.CardID, seq, consumer); err != nil {
			log().Warn("消费落账失败", "seq", seq, "consumer", consumer, "card", ev.CardID, "cause", err)
			return err
		}
		log().Info("消息已消费", "seq", seq, "consumer", consumer, "card", ev.CardID)
		return nil
	}
	return nil // 不存在：幂等 nil
}

// Mentions 收件箱源③：@member 的未消费提及。全流扫描 afterSeq 之后、
// mentions∋member 且尚无本人 message_consumed 标记的 room_message。
// limit<=0 取 historyDefaultLimit。
func (s *Service) Mentions(member string, afterSeq int64, limit int) ([]proto.LedgerEvent, error) {
	if limit <= 0 {
		limit = historyDefaultLimit
	}
	events, err := room.ReadAllEvents(s.lc, afterSeq)
	if err != nil {
		log().Warn("提及读取失败：读事件流", "member", member, "cause", err)
		return nil, err
	}
	consumed := room.ConsumedSeqs(events, member)
	out := []proto.LedgerEvent{}
	for _, ev := range events {
		if ev.Type != room.RoomEventType || !room.MentionsMember(ev, member) {
			continue
		}
		if consumed[ev.Seq] {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	log().Info("未消费提及已组装", "member", member, "count", len(out))
	return out, nil
}

// ListRooms 会话列表：扁平活动排序 + 项目筛选（project=="" 表示全部）。
// 条目=全部卡房间（终态卡沉底）+ 各项目的 project:<name> 群 + global 恒在。
// LastActivity=该房间最新一条 room_message 时刻；卡房间无消息时回退卡的
// UpdatedAt。Live=绑定者租约未过期（同一注入时钟 nowFn 判定），无绑定时
// false；DriverLease 读失败向上传播（C6 handler 退化 503，不吞错）。
// ListRooms 返回不带成员未读视图的会话列表。需要成员维度时使用
// ListRoomsForMember；保留本入口供不关心用户身份的调用方使用。
func (s *Service) ListRooms(project string) ([]proto.RoomSummary, error) {
	return s.listRooms(project, "")
}

// ListRoomsForMember 返回会话列表并把指定成员的未读数投影到每一行。
// member 为空时不读取游标，保持 ListRooms 的旧语义。
func (s *Service) ListRoomsForMember(project, member string) ([]proto.RoomSummary, error) {
	return s.listRooms(project, member)
}

func (s *Service) listRooms(project, member string) ([]proto.RoomSummary, error) {
	cards, err := s.lc.ListAllCards(project)
	if err != nil {
		log().Warn("会话列表组装失败：读卡列表", "project", project, "cause", err)
		return nil, err
	}
	events, err := room.ReadAllEvents(s.lc, 0)
	if err != nil {
		log().Warn("会话列表组装失败：读事件流", "project", project, "cause", err)
		return nil, err
	}
	rooms := []proto.RoomSummary{}
	byID := map[string]int{}
	terminal := map[string]bool{}
	projects := map[string]bool{}
	for _, c := range cards {
		isTerm := room.IsTerminalStatus(c.Status)
		terminal[c.ID] = isTerm
		byID[c.ID] = len(rooms)
		rooms = append(rooms, proto.RoomSummary{
			ID: c.ID, Kind: room.KindCard, Project: c.Project, Title: c.Title,
			BoundSession: c.DriverSession,
			ReadOnly:     isTerm || c.Following != "",
			LastActivity: c.UpdatedAt,
		})
		if c.Project != "" {
			projects[c.Project] = true
		}
	}
	for p := range projects {
		byID["project:"+p] = len(rooms)
		rooms = append(rooms, proto.RoomSummary{ID: "project:" + p, Kind: room.KindProject, Project: p, Title: p})
	}
	byID["global"] = len(rooms)
	rooms = append(rooms, proto.RoomSummary{ID: "global", Kind: room.KindGlobal, Title: "全员"})
	// 活动：扫全流，逐房间取最新 room_message 时刻（升序遍历，后写覆盖）。
	for _, ev := range events {
		if ev.Type != room.RoomEventType {
			continue
		}
		roomID := room.RoomIDOf(ev)
		idx, ok := byID[roomID]
		if !ok {
			continue
		}
		if ev.CreatedAt.After(rooms[idx].LastActivity) {
			rooms[idx].LastActivity = ev.CreatedAt
		}
		var msg proto.RoomMessage
		if err := room.UnmarshalMessage(ev.Payload, &msg); err != nil {
			log().Warn("会话列表预览投影跳过：消息载荷无效",
				"project", project, "room", roomID, "seq", ev.Seq, "cause", err)
			continue
		}
		if rooms[idx].Preview != nil && rooms[idx].Preview.Seq >= ev.Seq {
			continue
		}
		rooms[idx].Preview = &proto.RoomPreview{
			Body: truncateRoomPreview(msg.Body), Seq: ev.Seq, CreatedAt: ev.CreatedAt,
		}
	}
	// 活性：按不同绑定会话去重读租约（兼任多席只问一次「会话活着吗」）。
	liveOf := map[string]bool{}
	sessionSeen := map[string]bool{}
	for _, r := range rooms {
		if r.BoundSession == "" || sessionSeen[r.BoundSession] {
			continue
		}
		sessionSeen[r.BoundSession] = true
		expiresAt, exists, err := s.lc.DriverLease(r.BoundSession)
		if err != nil {
			log().Warn("会话列表组装失败：读租约", "session", r.BoundSession, "cause", err)
			return nil, err
		}
		liveOf[r.BoundSession] = exists && expiresAt.After(nowFn())
	}
	for i := range rooms {
		if bs := rooms[i].BoundSession; bs != "" {
			rooms[i].Live = liveOf[bs]
		}
	}
	if member != "" {
		cursors, err := s.cursor.Snapshot(member)
		if err != nil {
			log().Warn("会话列表组装失败：读游标快照",
				"project", project, "member", member, "cause", err)
			return nil, err
		}
		unread := unreadByRoom(events, cursors)
		for i := range rooms {
			rooms[i].Unread = unread[rooms[i].ID]
		}
		log().Info("会话列表未读聚合完成", "project", project, "member", member,
			"rooms", len(rooms))
	}
	// 排序：非终态（含群房间）按活动降序在前，终态卡房间沉底（各自内部按
	// 活动降序；Stable 保证同活动时保持插入序，输出形状确定）。
	active := []proto.RoomSummary{}
	sunk := []proto.RoomSummary{}
	for _, r := range rooms {
		if terminal[r.ID] {
			sunk = append(sunk, r)
		} else {
			active = append(active, r)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].LastActivity.After(active[j].LastActivity) })
	sort.SliceStable(sunk, func(i, j int) bool { return sunk[i].LastActivity.After(sunk[j].LastActivity) })
	log().Info("会话列表已组装", "project", project, "count", len(rooms))
	return append(active, sunk...), nil
}

// truncateRoomPreview 按 rune 截断列表预览，避免多字节正文被半截字节切坏；
// 省略号占一个预算位，调用方可据此把超长正文识别为摘要。
func truncateRoomPreview(body string) string {
	runes := []rune(body)
	if len(runes) <= roomPreviewMaxRunes {
		return body
	}
	return string(runes[:roomPreviewMaxRunes-1]) + "…"
}

// unreadByRoom 从列表已经读取的全量事件中聚合成员未读数。
//
// 列表本来就需要一次事件流读取来计算 LastActivity；复用这批事件，并在一次
// Snapshot 游标快照上比较水位，避免每个房间再次 Cursor+ReadAllEvents。
func unreadByRoom(events []proto.LedgerEvent, cursors map[string]int64) map[string]int {
	out := make(map[string]int)
	for _, ev := range events {
		if ev.Type != room.RoomEventType {
			continue
		}
		roomID := room.RoomIDOf(ev)
		if roomID == "" || ev.Seq <= cursors[roomID] {
			continue
		}
		out[roomID]++
	}
	return out
}

// MarkRead 未读游标置位（打开房间即已读）。按成员按房间记 seq 水位，单调
// 只进不退；并发到达由 cursor.Store 互斥锁 + tmp+rename 原子写兜底。
func (s *Service) MarkRead(member, roomID string, uptoSeq int64) error {
	if err := s.cursor.MarkRead(member, roomID, uptoSeq); err != nil {
		log().Warn("未读游标置位失败", "member", member, "room", roomID, "upto_seq", uptoSeq, "cause", err)
		return err
	}
	log().Info("未读游标已置位", "member", member, "room", roomID, "upto_seq", uptoSeq)
	return nil
}

// Unread 未读计数：该房间内 room_message 事件中 seq 大于该成员游标的条数
// （水位语义：打开房间 MarkRead 到当前最大 seq 后即 0；未读过 = 全量）。
func (s *Service) Unread(member, roomID string) (int, error) {
	cursorSeq, err := s.cursor.Cursor(member, roomID)
	if err != nil {
		log().Warn("未读计数失败：读游标", "member", member, "room", roomID, "cause", err)
		return 0, err
	}
	events, err := room.ReadAllEvents(s.lc, cursorSeq)
	if err != nil {
		log().Warn("未读计数失败：读事件流", "member", member, "room", roomID, "cause", err)
		return 0, err
	}
	n := 0
	for _, ev := range events {
		if ev.Type != room.RoomEventType || !room.SameRoom(ev, roomID) {
			continue
		}
		n++
	}
	log().Info("未读计数完成", "member", member, "room", roomID, "unread", n)
	return n, nil
}
