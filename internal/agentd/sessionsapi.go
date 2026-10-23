// 会话（群）HTTP 面（B358.4；B366 增补员端点）：会话建/列/详情/归档/拉卡/移出
// 加员七端点，注册进 registerLedgerRoutes 同一 mux；发言、已读、历史复用既有
// /api/rooms 端点（roomsapi.go——会话房间经 B358.1 会话语义已可读写）。
//
// 边界：本文件是 gateway 对 d_collab 入站 api 门面会话半边的消费侧（spec 测试
// 接缝清单 #1 的调用方）；不得 import internal/collab 之外的门面，账本能力经
// s.autoLedger 既有薄门面触达。
//
// 身份两字段（B358.4 拍板①，B358.9 换脸后统一）：
//   - 成员身份（会话群主/列表 member 维度）用统一记法 user:<name>/agent:<name>
//     ——群主缺省取解析人名，显式 owner 仍按统一记法校验；
//   - 审计 actor 恒为同一解析人名（resolveConsoleIdentity），请求体不带该字段
//     ——body 里的 actor 字段一律不解码。
//
// 错误映射：sessionErr（会话写面）+ 既有 collabErr（发言路径）。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/diag"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// validSessionOwner 校验统一记法（B358.9 §8.3 HTTP 半边）：收敛到 proto 唯一
// 定义处，退役第二份重复实现（契约 §5 H 组条 42）。
func validSessionOwner(owner string) bool {
	return proto.ValidateMemberIdentity(owner)
}

// sessionErr 把 collab.Service 会话写面哨兵翻译为 HTTP 状态（b358.4-plan §2.2
// 定稿映射表）：ErrNoRoom（含 mapSessionError 归一的 client.ErrNotFound，及
// B366 补员端点 facade 直调面 translateNotFound 出站的 client.ErrNotFound——
// 同族哨兵，collab.ErrNoRoom 本就是它的入站翻译）→404、ledger.ErrBadState
// （已属他会话/会话已归档）→409、其余 500。覆盖会话写面七端点可达的哨兵，
// 不与 collabErr 的发言路径映射混流（缺陷族 7）——若未来会话写面冒出
// ErrNotWriter/ErrReadOnly，按 500 暴露，不静默归并。
func sessionErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, collab.ErrNoRoom), errors.Is(err, client.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ledger.ErrBadState):
		code = http.StatusConflict
	}
	writeErr(w, code, err)
}

// handleSessionCreate POST /api/sessions → 建会话（B358.4；B444 P2 扩为项目内建会话编排）。
//
// 两种形态（proto.SessionCreateReq，版本化增量）：
//   - 旧形态 {title, owner}（project/cards/squad 全缺）：只建会话，标题必填（空 → 400），响应 proto.Session；
//   - 新形态（带 project/cards/squad 任一）：createProjectSession 编排——project 必填、驻场小队须为协调小队
//     （空 = 唯一协调小队）、title 可空（按已接受的卡自动命名）。校验失败 400 零写入；建好即 200，响应
//     proto.SessionCreateResp（拒卡在 rejected_cards，驻场者首轮失败在 resident.error，Q3 甲）。
//
// owner（群主=初始成员，契约条 3）缺省 = 解析出的人名（§3.8、决定 6：owner=创建者），
// 显式 owner 仍按统一记法校验；actor（审计「谁调的 HTTP」）与 owner 同源解析人名
// （B358.9 换脸后两字段不再是两个来源）。
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	var req proto.SessionCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	title := strings.TrimSpace(req.Title)
	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		owner = id.Member
	} else if !validSessionOwner(owner) {
		writeErr(w, http.StatusBadRequest,
			errors.New(`owner 必须是统一记法 user:<name> 或 agent:<name>`))
		return
	}
	actor := id.Member
	if req.Project == "" && len(req.Cards) == 0 && req.Squad == "" {
		if title == "" {
			writeErr(w, http.StatusBadRequest, errors.New("会话标题不能为空"))
			return
		}
		session, err := s.rooms.CreateSession(title, owner, actor)
		if err != nil {
			// 可达失败只有账本写失败（owner/title 已验）——服务端错误。
			s.log.Warn("建会话失败", "title", title, "owner", owner, "actor", actor, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		s.log.Info("会话已创建", "session", session.ID, "title", title, "owner", owner, "actor", actor)
		writeJSON(w, http.StatusOK, session)
		return
	}
	if s.keystone == nil || s.scheduling == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "自动化层未装配（SetupAutomation 未执行），无法拉起驻场者",
		})
		return
	}
	s.log.Info("项目内建会话请求", "project", req.Project, "cards", len(req.Cards), "squad", req.Squad,
		"has_title", title != "", "actor", actor)
	resp, err := s.createProjectSession(r.Context(), sessionCreateInput{
		Title: title, Owner: owner, Project: strings.TrimSpace(req.Project),
		Cards: req.Cards, Squad: req.Squad, Actor: actor,
	})
	if err != nil {
		var lookupErr *coordinatorLookupError
		switch {
		case errors.As(err, &lookupErr):
			writeErr(w, http.StatusBadRequest, errors.New(residentSquadMessage(lookupErr.err)))
		case errors.Is(err, errSessionCreateInvalid):
			writeErr(w, http.StatusBadRequest, err)
		default:
			s.log.Warn("项目内建会话失败", "project", req.Project, "actor", actor, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSessionRename POST /api/sessions/{id}/rename {title, expect_title?}（B444 S7，plan Q6 甲）。改名者 =
// 控制台身份（服务端注入，body 不带身份）；群主或现驻场者才可改。错误映射：空标题 400、会话不存在 404、
// 非群主非驻场者 403、expect_title 不符或已归档 409、其余 500。
func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	roomID := r.PathValue("id")
	var req proto.SessionRenameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	session, err := s.rooms.RenameSession(roomID, req.Title, req.ExpectTitle, id.Member)
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, collab.ErrSessionTitle):
			code = http.StatusBadRequest
		case errors.Is(err, collab.ErrNoRoom):
			code = http.StatusNotFound
		case errors.Is(err, collab.ErrNotWriter):
			code = http.StatusForbidden
		case errors.Is(err, collab.ErrTitleConflict), errors.Is(err, collab.ErrReadOnly):
			code = http.StatusConflict
		}
		s.log.Warn("会话改名失败", "session", roomID, "actor", id.Member, "status", code, "cause", err)
		writeErr(w, code, fmt.Errorf("会话 %s 改名: %w", roomID, err))
		return
	}
	s.log.Info("会话已改名", "session", roomID, "title", session.Title, "actor", id.Member)
	writeJSON(w, http.StatusOK, session)
}

// handleSessionResidentLaunch POST /api/sessions/{id}/resident/launch {squad?}（B444 plan Q4 甲）：为会话
// 拉起 push 驻场者——与建会话共用 launchSessionResident。错误映射：小队不可用 400、会话不存在 404、
// 已归档/已有驻场者/名额满 409、拉起失败 502（会话里同时落系统指针行）。
func (s *Server) handleSessionResidentLaunch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireConsoleIdentity(w, r); !ok {
		return
	}
	roomID := r.PathValue("id")
	var req proto.SessionResidentLaunchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	squad, err := s.residentSquadFor(req.Squad)
	if err != nil {
		var lookupErr *coordinatorLookupError
		if errors.As(err, &lookupErr) {
			writeErr(w, http.StatusBadRequest, errors.New(residentSquadMessage(lookupErr.err)))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	outcome, err := s.launchSessionResident(r.Context(), roomID, squad)
	if err != nil {
		var admissionErr *coordinatorAdmissionError
		var lookupErr *coordinatorLookupError
		code := http.StatusBadGateway
		switch {
		case errors.As(err, &lookupErr):
			code = http.StatusBadRequest
		case errors.Is(err, collab.ErrNoRoom):
			code = http.StatusNotFound
		case errors.Is(err, collab.ErrReadOnly), errors.Is(err, collab.ErrResidentOccupied), errors.As(err, &admissionErr):
			code = http.StatusConflict
		}
		s.log.Warn("叫驻场者失败", "session", roomID, "squad", squad.Name, "status", code, "cause", err)
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, outcome)
}

// handleSessionMemberAdd POST /api/sessions/{id}/members → AddSessionMember
// （B366 补员端点：控制台「以当前身份加入会话」的唯一生产入口，发言权岔口1
// 用户故事 1 的接线；AddSessionMember 的 Store/Facade/Client 三层随 B358
// Ticket 0 冻结，此处纯接线）。调用面是 Server.autoLedger（SetupAutomation
// 装配、与 s.rooms 同一 facade 实例＝LedgerClient 实现，server.go 组装处）。
//
// 成员身份服务端权威（roomsapi.go 门禁纪律「成员标识服务端注入，不经请求体」，
// 拍板①两字段不混用的同族应用）：body 可空或 {identity:""}＝以调用者身份加入
// （resolveConsoleIdentity 注入值）；body 塞非空 identity 时仍按 validSessionOwner
// 统一记法口径校验（违者 400），但成员恒取服务端注入 actor——塞值被忽略不入成员列
// （TestSessionMemberAddEndpoint ③ 反例锁）；body actor 字段一律不解码。
func (s *Server) handleSessionMemberAdd(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	roomID := r.PathValue("id")
	var req struct {
		Identity string `json:"identity"`
	}
	// body 可空：空体（io.EOF）视同 {}——控制台一键以调用者身份加入。
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	if identity := strings.TrimSpace(req.Identity); identity != "" && !validSessionOwner(identity) {
		writeErr(w, http.StatusBadRequest,
			errors.New(`identity 必须是统一记法 user:<name> 或 agent:<name>`))
		return
	}
	// 成员与审计 actor 同源恒为服务端注入值；加员幂等（Store 级短路、零事件副作用）。
	actor := id.Member
	if err := s.autoLedger.AddSessionMember(roomID, actor, actor); err != nil {
		s.log.Warn("会话加员失败", "session", roomID, "member", actor, "cause", err)
		// 包回会话 id：facade 出站哨兵不携带 Store 层文案（与 handleSessionDetail
		// 同款边界处理），404/409 文案可行动（缺陷族 2）；%w 保住哨兵映射。
		sessionErr(w, fmt.Errorf("会话 %s: %w", roomID, err))
		return
	}
	s.log.Info("成员已加入会话", "session", roomID, "member", actor)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSessionsList GET /api/sessions → ListSessions（谁需要我：未读/标签/预览）。
// member 维度 = 控制台成员身份（服务端注入）；?member= 查询参数不参与
// （TestSessionsListUnreadAndMemberForgery 反例锁）。
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	member := id.Member
	summaries, err := s.rooms.ListSessionsContext(r.Context(), member)
	if err != nil {
		s.log.Warn("会话列表读取失败", append(diag.Attrs(r.Context()),
			"member", member, "cause", err)...)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	annotateReadRows(r.Context(), len(summaries))
	s.log.Info("会话列表响应成功", "member", member, "sessions", len(summaries))
	writeJSON(w, http.StatusOK, map[string]any{"sessions": summaries})
}

// handleSessionDetail GET /api/sessions/{id} → SessionDetail（成员/节点/timeline
// 三块）。不存在 → 404 且文案含会话 id（breakdown 缺陷族 2 可行动文案）：门面
// 出栈前 ErrNotFound 已被 translateNotFound/mapSessionError 双重归一成裸
// ErrNoRoom，Store 层「会话 %s: …」文案不随哨兵透传，handler 在边界把 id 包回
// （%w 保住 ErrNoRoom 映射）。
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	detail, err := s.rooms.SessionDetailContext(r.Context(), id)
	if err != nil {
		s.log.Warn("会话详情读取失败", "session", id, "cause", err)
		sessionErr(w, fmt.Errorf("会话 %s: %w", id, err))
		return
	}
	s.enrichSessionRoleTaskStates(&detail)
	annotateReadRows(r.Context(), len(detail.Timeline))
	s.log.Info("会话详情响应成功", "session", id)
	writeJSON(w, http.StatusOK, detail)
}

// enrichSessionRoleTaskStates overlays the existing local task snapshot, then the
// cached remote mirror snapshot. A cached snapshot is observation only, never a heartbeat.
func (s *Server) enrichSessionRoleTaskStates(detail *proto.SessionDetail) {
	if detail == nil || detail.RoleStatus == nil || s.mgr == nil {
		return
	}
	observations := map[string]collab.SessionTaskObservation{}
	for i := range detail.RoleStatus.Roles {
		role := &detail.RoleStatus.Roles[i]
		if role.Kind != "executor" || role.TaskID == "" {
			continue
		}
		task, err := s.mgr.GetTask(role.TaskID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Warn("会话角色本地任务快照读取失败", "task", role.TaskID, "cause", err)
				observations[role.TaskID] = collab.SessionTaskObservation{Err: err}
				continue
			}
			mirror, ok, mirrorErr := s.mgr.GetMirrorTask(role.TaskID)
			if mirrorErr != nil {
				s.log.Warn("会话角色任务快照读取失败", "task", role.TaskID, "cause", mirrorErr)
				observations[role.TaskID] = collab.SessionTaskObservation{Err: mirrorErr}
				continue
			}
			if !ok {
				observations[role.TaskID] = collab.SessionTaskObservation{Missing: true}
				continue
			}
			if mirror.Target != role.Target {
				observations[role.TaskID] = collab.SessionTaskObservation{Target: mirror.Target}
				continue
			}
			task = &mirror.Task
			observations[role.TaskID] = collab.SessionTaskObservation{Target: mirror.Target, State: string(task.State), FetchedAt: mirror.FetchedAt, UpdatedAt: task.UpdatedAt, Cached: true}
			continue
		}
		if task == nil {
			observations[role.TaskID] = collab.SessionTaskObservation{Missing: true}
			continue
		}
		observations[role.TaskID] = collab.SessionTaskObservation{State: string(task.State), UpdatedAt: task.UpdatedAt}
	}
	collab.ApplySessionTaskObservations(detail, observations)
}

// handleSessionArchive POST /api/sessions/{id}/archive → ArchiveSession（幂等，
// Store 级已归档短路）。归档后只读由 collab.Send / collab.JoinCard 在会话本体
// 上执法（B358.1），本 handler 不重复判定。
func (s *Server) handleSessionArchive(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	roomID := r.PathValue("id")
	actor := id.Member
	if err := s.rooms.ArchiveSession(roomID, actor); err != nil {
		s.log.Warn("归档会话失败", "session", roomID, "actor", actor, "cause", err)
		sessionErr(w, err)
		return
	}
	s.log.Info("会话已归档", "session", roomID, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSessionJoinCard POST /api/sessions/{id}/cards {card} → JoinCard
// （进群 ≠ 配人，配人仍走 B307 三按钮）。已属他会话/会话已归档 → 409
// （ErrBadState）；卡或会话不存在 → 404（ErrNoRoom）。
func (s *Server) handleSessionJoinCard(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	roomID := r.PathValue("id")
	var req struct {
		Card string `json:"card"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	cardID := strings.TrimSpace(req.Card)
	if cardID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("卡号不能为空"))
		return
	}
	actor := id.Member
	if err := s.rooms.JoinCard(roomID, cardID, actor); err != nil {
		s.log.Warn("拉卡进群失败", "session", roomID, "card", cardID, "actor", actor, "cause", err)
		sessionErr(w, err)
		return
	}
	s.log.Info("卡已进群", "session", roomID, "card", cardID, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSessionLeaveCard DELETE /api/sessions/{id}/cards/{cardID} → LeaveCard
// （幂等：不在会话内返回 nil）。
func (s *Server) handleSessionLeaveCard(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	roomID := r.PathValue("id")
	cardID := r.PathValue("cardID")
	actor := id.Member
	if err := s.rooms.LeaveCard(roomID, cardID, actor); err != nil {
		s.log.Warn("移卡出群失败", "session", roomID, "card", cardID, "actor", actor, "cause", err)
		sessionErr(w, err)
		return
	}
	s.log.Info("卡已移出会话", "session", roomID, "card", cardID, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
