// 协作房间 HTTP 面与收件箱编排（B156.2 C6）：六个房间端点的注册进
// registerLedgerRoutes 同一 mux；收件箱三源（open 裁决 / 未答复工单 / @提及）
// 按契约 §3.6 在 gateway 层编排，不进 collab 也不进 ledger。
//
// 边界：本文件是 gateway 对 d_collab 入站 api 门面的消费侧（spec 测试接缝清单
// #1 的调用方）；不得 import internal/collab 之外的门面（internal/ledger/api），
// 账本能力经 s.ledger 既有直调面（岔口二方案 A，仅既有能力）或 rebindPort
// 端口（组装点注入适配器）触达。actor/成员标识服务端注入，不经请求体。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// collabErr 把 collab.Service 哨兵翻译为 HTTP 状态（契约 §3.3 哨兵集 + §3.5 映射表）。
// ErrNoRoom/ErrKindNotAllowed→400、ErrNotWriter→403、ErrReadOnly/ErrNotBound→409。
// History 的 ErrNoRoom→404 特例由 handleRoomMessages 自行处理。
func collabErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, collab.ErrNoRoom), errors.Is(err, collab.ErrKindNotAllowed):
		code = http.StatusBadRequest
	case errors.Is(err, collab.ErrNotWriter):
		code = http.StatusForbidden
	case errors.Is(err, collab.ErrReadOnly), errors.Is(err, collab.ErrNotBound):
		code = http.StatusConflict
	}
	writeErr(w, code, err)
}

// roomUserActor 是房间面的控制台成员/actor 标识（服务端注入）：沿用
// ledgerapi.go:458 的 step 补 actor 先例（"web:"+hostOnly）。@用户的提及
// 与已读游标都以此标识为成员维度（D-identity，台账 L15）。
func (s *Server) roomUserActor(r *http.Request) string {
	return "web:" + hostOnly(r.RemoteAddr)
}

// handleRoomsList GET /api/rooms?project= → ListRooms（扁平活动排序列表）。
func (s *Server) handleRoomsList(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	member := s.roomUserActor(r)
	rooms, err := s.rooms.ListRoomsForMember(project, member)
	if err != nil {
		s.log.Warn("会话列表读取失败", "project", project, "member", member, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.enrichRoomAttachments(r.Context(), rooms)
	s.log.Info("会话列表响应成功", "project", project, "member", member, "rooms", len(rooms))
	writeJSON(w, http.StatusOK, map[string]any{"rooms": rooms})
}

const (
	roomAttachBackgroundTimeout = 10 * time.Second
	roomAttachRefreshInterval   = 5 * time.Second
	roomAttachRefreshWorkers    = 16
	roomAttachCacheTTL          = time.Minute
)

// roomAttachCacheEntry 把远端任务详情和其有效期绑定；Attach 只是投影，不是
// 账本事实。失效时间到达或后台刷新失败后，调用方必须回到不可 attach 态。
type roomAttachCacheEntry struct {
	attach    *proto.RoomAttach
	expiresAt time.Time
}

// enrichRoomAttachments 给 card 房间补最新可解析的挂账 task；群房间没有 task，
// 保持 Attach=nil。远端 attach 是非承重字段：请求只读取本地挂账与缓存，缺失项
// 交给后台刷新，避免一个 relay 延迟拖住整个房间列表。
func (s *Server) enrichRoomAttachments(_ context.Context, rooms []proto.RoomSummary) {
	if s.ledger == nil {
		return
	}
	links, err := s.ledger.AllTaskLinks()
	if err != nil {
		s.log.Warn("读取房间挂账失败", "cause", err)
		return
	}
	linksByRoom := make(map[string][]ledger.TaskLink)
	for _, link := range links {
		linksByRoom[link.CardID] = append(linksByRoom[link.CardID], link)
	}
	for roomID := range linksByRoom {
		sort.SliceStable(linksByRoom[roomID], func(i, j int) bool {
			return linksByRoom[roomID][i].CreatedAt.Before(linksByRoom[roomID][j].CreatedAt)
		})
	}

	localTasks := make(map[string]proto.Task)
	for _, link := range links {
		if link.Target != "" || s.st == nil {
			continue
		}
		tasks, listErr := s.st.ListTasks()
		if listErr != nil {
			s.log.Warn("读取本机任务列表失败，attach 降级禁用", "cause", listErr)
			break
		}
		for _, task := range tasks {
			localTasks[task.ID] = task
		}
		s.log.Debug("本机 attach 任务索引已就绪", "tasks", len(localTasks))
		break
	}

	for i := range rooms {
		if rooms[i].Kind != room.KindCard {
			continue
		}
		roomLinks := linksByRoom[rooms[i].ID]
		if len(roomLinks) == 0 {
			continue
		}
		for linkIndex := len(roomLinks) - 1; linkIndex >= 0; linkIndex-- {
			link := roomLinks[linkIndex]
			if link.Target == "" {
				attach, lookupErr := s.lookupRoomAttach(context.Background(), link, localTasks)
				if lookupErr != nil {
					s.log.Warn("房间 attach 本机任务不可解析", "room", rooms[i].ID,
						"task", link.TaskID, "cause", lookupErr)
					continue
				}
				rooms[i].Attach = attach
				s.log.Info("房间 attach 投影成功", "room", rooms[i].ID,
					"target", link.Target, "task", link.TaskID, "workdir", attach.WorkDir)
				break
			}
			if attach := s.cachedRoomAttach(link); attach != nil {
				rooms[i].Attach = attach
				s.log.Info("房间 attach 从缓存投影成功", "room", rooms[i].ID,
					"target", link.Target, "task", link.TaskID, "workdir", attach.WorkDir)
				break
			}
		}
	}
	s.startRoomAttachRefresh(links)
	s.log.Info("房间 attach 投影完成", "rooms", len(rooms), "links", len(links))
}

// startRoomAttachRefresh asynchronously resolves remote task workdirs. A single
// refresh is shared by concurrent list requests and throttled between passes.
func (s *Server) startRoomAttachRefresh(links []ledger.TaskLink) {
	remoteLinks := make(map[string]ledger.TaskLink)
	for _, link := range links {
		if link.Target == "" {
			continue
		}
		remoteLinks[roomAttachCacheKey(link)] = link
	}
	if len(remoteLinks) == 0 {
		return
	}

	s.roomAttachMu.Lock()
	now := time.Now()
	if s.roomAttachRefreshing || (!s.roomAttachLastRefresh.IsZero() &&
		now.Sub(s.roomAttachLastRefresh) < roomAttachRefreshInterval) {
		s.roomAttachMu.Unlock()
		return
	}
	s.roomAttachRefreshing = true
	s.roomAttachLastRefresh = now
	s.roomAttachMu.Unlock()

	go func() {
		defer func() {
			s.roomAttachMu.Lock()
			s.roomAttachRefreshing = false
			s.roomAttachMu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), roomAttachBackgroundTimeout)
		defer cancel()
		workers := make(chan struct{}, roomAttachRefreshWorkers)
		var wg sync.WaitGroup
		for _, link := range remoteLinks {
			link := link
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case workers <- struct{}{}:
				case <-ctx.Done():
					s.log.Warn("房间 attach 后台刷新取消", "target", link.Target,
						"task", link.TaskID, "cause", ctx.Err())
					return
				}
				defer func() { <-workers }()
				attach, err := s.lookupRoomAttach(ctx, link, nil)
				if err != nil {
					s.invalidateRoomAttach(link)
					s.log.Warn("房间 attach 后台刷新失败", "target", link.Target,
						"task", link.TaskID, "cause", err)
					return
				}
				s.storeRoomAttach(link, attach)
				s.log.Info("房间 attach 后台刷新成功", "target", link.Target,
					"task", link.TaskID, "workdir", attach.WorkDir)
			}()
		}
		wg.Wait()
		s.log.Info("房间 attach 后台刷新完成", "links", len(remoteLinks),
			"timed_out", ctx.Err() != nil)
	}()
}

func roomAttachCacheKey(link ledger.TaskLink) string {
	return link.Target + "\x00" + link.TaskID
}

// cachedRoomAttach 返回仍在 TTL 内的远端 attach 投影副本；缺失或过期都返回
// nil，调用方据此保持禁用态。返回副本避免响应组装方读到后台刷新中的指针。
func (s *Server) cachedRoomAttach(link ledger.TaskLink) *proto.RoomAttach {
	now := time.Now()
	s.roomAttachMu.Lock()
	entry, ok := s.roomAttachCache[roomAttachCacheKey(link)]
	if !ok || entry.attach == nil {
		s.roomAttachMu.Unlock()
		return nil
	}
	if !entry.expiresAt.IsZero() && !now.Before(entry.expiresAt) {
		delete(s.roomAttachCache, roomAttachCacheKey(link))
		s.roomAttachMu.Unlock()
		s.log.Debug("房间 attach 缓存已过期", "target", link.Target, "task", link.TaskID)
		return nil
	}
	copy := *entry.attach
	s.roomAttachMu.Unlock()
	return &copy
}

// invalidateRoomAttach 删除一次失败刷新留下的旧投影；失败不能让不可达目标
// 永久看起来仍可执行。
func (s *Server) invalidateRoomAttach(link ledger.TaskLink) {
	s.roomAttachMu.Lock()
	delete(s.roomAttachCache, roomAttachCacheKey(link))
	s.roomAttachMu.Unlock()
}

func (s *Server) storeRoomAttach(link ledger.TaskLink, attach *proto.RoomAttach) {
	copy := *attach
	s.roomAttachMu.Lock()
	if s.roomAttachCache == nil {
		s.roomAttachCache = make(map[string]roomAttachCacheEntry)
	}
	s.roomAttachCache[roomAttachCacheKey(link)] = roomAttachCacheEntry{
		attach: &copy, expiresAt: time.Now().Add(roomAttachCacheTTL),
	}
	s.roomAttachMu.Unlock()
}

// lookupRoomAttach 只接受真实任务详情和 Workdir()；不从 bound_session 猜测 task。
// localTasks 是列表装配点的一次性本机任务索引，避免逐挂账查询。
func (s *Server) lookupRoomAttach(ctx context.Context, link ledger.TaskLink, localTasks map[string]proto.Task) (*proto.RoomAttach, error) {
	var workDir string
	if link.Target == "" {
		task, ok := localTasks[link.TaskID]
		if !ok {
			return nil, fmt.Errorf("读取本机任务 %s: 任务不存在", link.TaskID)
		}
		workDir = task.Workdir()
	} else {
		peer, err := s.pool.For(link.Target)
		if err != nil {
			return nil, fmt.Errorf("获取 target %s 客户端: %w", link.Target, err)
		}
		info, err := peer.Attach(ctx, link.TaskID)
		if err != nil {
			return nil, fmt.Errorf("读取远端任务 %s: %w", link.TaskID, err)
		}
		workDir = info.Task.Workdir()
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, errors.New("任务没有可用工作目录")
	}
	return &proto.RoomAttach{
		Target: link.Target, TaskID: link.TaskID, WorkDir: workDir,
		Command: "handoff attach " + link.TaskID,
	}, nil
}

// handleRoomMessages GET /api/rooms/{id}/messages?before=&limit= → History。
// 契约 §3.5 要求 ErrNoRoom→404 这格映射，分支保留；但今天不可达——读侧宽容的
// History（C4 合规实现）对不存在的房间返回空列表而非 ErrNoRoom，读不存在房间与
// 读空房间对渲染方是同一件事（协调者裁决，台账 L21）。无端到端输入能让此分支
// 触发，故不写声称验证它的测试。
func (s *Server) handleRoomMessages(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.rooms.History(roomID, before, limit)
	if err != nil {
		if errors.Is(err, collab.ErrNoRoom) {
			s.log.Warn("房间历史请求命中不存在房间", "room", roomID)
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Warn("房间历史读取失败", "room", roomID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": events})
}

// handleRoomSend POST /api/rooms/{id}/messages → 用户发言（kind 服务端固定 user，
// actor 服务端注入，均不经请求体）。空正文拒绝（无意义消息不进房间）。
func (s *Server) handleRoomSend(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	var req struct {
		Body     string   `json:"body"`
		Refs     []string `json:"refs,omitempty"`
		Mentions []string `json:"mentions,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("消息正文不能为空"))
		return
	}
	seq, err := s.rooms.Send(roomID, proto.RoomMessage{
		Kind: proto.RoomMsgUser, Body: req.Body, Refs: req.Refs, Mentions: req.Mentions,
	}, s.roomUserActor(r))
	if err != nil {
		s.log.Warn("房间消息发送失败", "room", roomID, "actor", s.roomUserActor(r), "cause", err)
		collabErr(w, err)
		return
	}
	s.log.Info("房间消息已发送", "room", roomID, "seq", seq, "actor", s.roomUserActor(r))
	writeJSON(w, http.StatusOK, map[string]int64{"seq": seq})
}

// handleRoomRead POST /api/rooms/{id}/read {upto_seq} → MarkRead（打开房间即已读）。
func (s *Server) handleRoomRead(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	var req struct {
		UptoSeq int64 `json:"upto_seq"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	if err := s.rooms.MarkRead(s.roomUserActor(r), roomID, req.UptoSeq); err != nil {
		s.log.Warn("已读游标置位失败", "room", roomID, "member", s.roomUserActor(r), "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleInbox GET /api/inbox → 收件箱三源聚合（契约 §3.6 编排单元，位置冻结在
// gateway 层）。decision/mention 源失败如实 500（承重）；ticket 源失败降级跳过
// （与 handleCardsList 的 tickets 徽标同族，不吞主查询）。
func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	items := make([]proto.InboxItem, 0, 8)

	// 源① decision：open 裁决全量（含 CardID 为空的项目级），岔口二方案 A 直调面。
	decisions, err := s.ledger.ListDecisions(true)
	if err != nil {
		s.log.Warn("收件箱 decision 源读取失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, d := range decisions {
		payload, err := json.Marshal(ledgerDecisionWire(d))
		if err != nil {
			s.log.Warn("收件箱 decision 投影失败", "decision", d.ID, "cause", err)
			continue
		}
		items = append(items, proto.InboxItem{
			Origin: proto.InboxOriginDecision, Title: decisionTitle(d), CardID: d.CardID,
			RefID: strconv.FormatInt(d.ID, 10), Payload: payload,
		})
	}

	// 源② ticket：等待人工输入态任务上的未答复工单；Watchers>0 排除（无人驱动
	// 才上浮），破坏性工单不受限（既有硬纪律，D-destructive 判据见 ticketDestructive）。
	tasks, err := s.st.ListTasks()
	if err != nil {
		s.log.Warn("收件箱 ticket 源读取失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, task := range tasks {
		if task.State != proto.TaskStateWaitingAnswer {
			continue
		}
		pending, err := s.st.PendingTickets(task.ID)
		if err != nil {
			s.log.Warn("收件箱 ticket 源读取失败", "task", task.ID, "cause", err)
			continue
		}
		if len(pending) == 0 {
			continue
		}
		watchers := s.hub.Watchers(task.ID)
		for _, tk := range pending {
			if watchers > 0 && !s.ticketDestructive(task.ID, tk.ID) {
				continue
			}
			items = append(items, proto.InboxItem{
				Origin: proto.InboxOriginTicket, Title: ticketTitle(tk), RefID: tk.ID,
			})
		}
	}

	// 源③ mention：@用户的未消费提及（collab.Mentions 已含未消费过滤，C5 锁住）。
	mentions, err := s.rooms.Mentions(s.roomUserActor(r), 0, 0)
	if err != nil {
		s.log.Warn("收件箱 mention 源读取失败", "member", s.roomUserActor(r), "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, ev := range mentions {
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			continue
		}
		items = append(items, proto.InboxItem{
			Origin: proto.InboxOriginMention, Title: "@你：" + msg.Body, CardID: ev.CardID,
			RefID: strconv.FormatInt(ev.Seq, 10),
		})
	}

	s.log.Info("收件箱已聚合", "member", s.roomUserActor(r), "items", len(items))
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ticketDestructive 判定工单是否属「破坏性/不可逆/外部可见」类（既有硬纪律的机械
// 代理，D-destructive 台账 L10）：该工单 task 事件流存在 approver_decision 事件
// （ticket_id 匹配且 decision ∈ {escalate, error}）——审批者不敢放行或裁决失败即
// 系统标记的危险请求。窗口用 recentEventsLimit=100（server.go:65，与任务详情同款）。
func (s *Server) ticketDestructive(taskID, ticketID string) bool {
	events, err := s.st.EventsFrom(taskID, 0, recentEventsLimit)
	if err != nil {
		return false
	}
	for _, ev := range events {
		if ev.Type != proto.EventTypeApproverDecision {
			continue
		}
		var p approverDecisionPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			continue
		}
		if p.TicketID == ticketID && (p.Decision == "escalate" || p.Decision == "error") {
			return true
		}
	}
	return false
}

// decisionTitle 收件箱 decision 条目标题 = 裁决正文首行（简报六段的第一段是「一句话」）。
func decisionTitle(d ledger.Decision) string {
	body := strings.TrimSpace(d.Body)
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	if n := len([]rune(body)); n > 80 {
		return string([]rune(body)[:80]) + "…"
	}
	return body
}

// ticketTitle 收件箱 ticket 条目标题：gate=权限工单、ask=提问工单。
func ticketTitle(tk proto.Ticket) string {
	if tk.Kind == "gate" {
		return "权限工单待答复"
	}
	return "提问工单待答复"
}

// handleCardRebind POST /api/cards/{id}/rebind {to_session,carrier,expect} → 换绑。
// 走 rebindPort 端口（岔口二条件 2/4：换绑是本期新增账本能力、一律经门面，而
// gateway handler 不得引用门面——端口接口定义在此、具体绑定由组装点注入）。
// CAS 冲突由账本哨兵映射 409（ledgerErr）。
func (s *Server) handleCardRebind(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		ToSession string `json:"to_session"`
		Carrier   string `json:"carrier"`
		Expect    string `json:"expect"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	if s.rebind == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "换绑端口未装配"})
		return
	}
	if err := s.rebind.Rebind(id, req.ToSession, req.Carrier, req.Expect); err != nil {
		s.log.Warn("换绑失败", "card", id, "to", req.ToSession, "cause", err)
		ledgerErr(w, err)
		return
	}
	s.log.Info("已换绑", "card", id, "to", req.ToSession, "carrier", req.Carrier)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// rebindPort 是 gateway 房间 handler 对换绑能力的消费侧端口（岔口二条件 4 的
// 「另一个子系统消费侧端口」按此窄读：接口定义在 gateway 消费侧、实现适配器
// 只出现在组装点 server.go）。实现为 facadeBindAdapter（server.go）。
type rebindPort interface {
	Rebind(id, toSession, carrier, expect string) error
}
