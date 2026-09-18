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
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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

// handleSessionCreate POST /api/sessions {title, owner} → CreateSession。
// owner（群主=初始成员，契约条 3）缺省 = 解析出的人名（§3.8、决定 6：owner=创建者），
// 显式 owner 仍按统一记法校验；actor（审计「谁调的 HTTP」）与 owner 同源解析人名
// （B358.9 换脸后两字段不再是两个来源）。
func (s *Server) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeErr(w, http.StatusBadRequest, errors.New("会话标题不能为空"))
		return
	}
	owner := strings.TrimSpace(req.Owner)
	if owner == "" {
		owner = id.Member
	} else if !validSessionOwner(owner) {
		writeErr(w, http.StatusBadRequest,
			errors.New(`owner 必须是统一记法 user:<name> 或 agent:<name>`))
		return
	}
	actor := id.Member
	session, err := s.rooms.CreateSession(title, owner, actor)
	if err != nil {
		// 可达失败只有账本写失败（owner/title 已验）——服务端错误。
		s.log.Warn("建会话失败", "title", title, "owner", owner, "actor", actor, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("会话已创建", "session", session.ID, "title", title, "owner", owner, "actor", actor)
	writeJSON(w, http.StatusOK, session)
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
	summaries, err := s.rooms.ListSessions(member)
	if err != nil {
		s.log.Warn("会话列表读取失败", "member", member, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
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
	detail, err := s.rooms.SessionDetail(id)
	if err != nil {
		s.log.Warn("会话详情读取失败", "session", id, "cause", err)
		sessionErr(w, fmt.Errorf("会话 %s: %w", id, err))
		return
	}
	s.log.Info("会话详情响应成功", "session", id)
	writeJSON(w, http.StatusOK, detail)
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
