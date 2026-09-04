// coordapi.go 实现协调者生命周期接线（B312）：控制面端点——
//
//	POST /api/cards/{id}/coordinator/launch  叫机器人并占 coordinate 席位
//	GET  /api/cards/{id}/coordinator         绑定与接管态
//	POST /api/cards/{id}/coordinator/rebind  叫机器人换绑
//	POST /api/cards/{id}/coordinator/forget  驱逐进程内旧会话引用
//	POST /api/cards/{id}/attach              attach 接管/交回（人工接管互斥的牙齿）
//
// 职责：组装点在此解析协调者小队产出 SessionSpec（契约 §15 澄清 2——SquadRows
// 过滤 role=coordinator → LaunchAdmit → Binding → Carrier 读 HomeDir），这是编制域
// 与 keystone 域在控制面的唯一交汇点。
//
// 边界：
//   - 只走既有入站门面（scheduling.Service / keystone.Service），零新增业务规则；
//   - keystone/scheduling 未装配（SetupAutomation 未执行）时 withCoordinator 给 503，
//     不静默降级（与 withLedger 同形）；
//   - attach 跨机沿用 ?machine= 转发（forwardIfRequested，forward.go:54）；
//   - 拉起路径全程不产生 task（铁律，spec 测试接缝 3，T1 断言）。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// errNoCoordinatorSquad 表示仓内没有 role=coordinator 的小队（岔口四 B：不做出厂
// 种子，未登记返回指路错误）。
var errNoCoordinatorSquad = errors.New("未登记协调者小队")

// errAmbiguousCoordinatorSquad 表示存在多个协调者小队，拉起目标不唯一。
var errAmbiguousCoordinatorSquad = errors.New("协调者小队不唯一")

func (s *Server) registerCoordRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/cards/{id}/coordinator/launch", s.withLedger(s.withCoordinator(s.handleCoordLaunch)))
	api.HandleFunc("GET /api/cards/{id}/coordinator", s.withLedger(s.withCoordinator(s.handleCoordStatus)))
	api.HandleFunc("POST /api/cards/{id}/coordinator/rebind", s.withLedger(s.withCoordinator(s.handleCoordRebind)))
	api.HandleFunc("POST /api/cards/{id}/coordinator/forget", s.withLedger(s.withCoordinator(s.handleCoordForget)))
	api.HandleFunc("POST /api/cards/{id}/attach", s.withLedger(s.withCoordinator(s.handleCoordAttach)))
}

// withCoordinator 守卫自动化层装配：keystone 或 scheduling 未装配时 503。
func (s *Server) withCoordinator(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.keystone == nil || s.scheduling == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "自动化层未装配（SetupAutomation 未执行）",
			})
			return
		}
		h(w, r)
	}
}

// resolveCoordinatorSquad 找出唯一的协调者小队（SquadRows 过滤 role=coordinator）。
// 0 → errNoCoordinatorSquad；≥2 → errAmbiguousCoordinatorSquad（错误带全部候选名）。
// SquadRows 由 K3 交付（internal/scheduling/registry_read.go），开工前已确认并入。
func (s *Server) resolveCoordinatorSquad() (scheduling.Squad, error) {
	rows, err := s.scheduling.SquadRows()
	if err != nil {
		return scheduling.Squad{}, err
	}
	var names []string
	var single *scheduling.Squad
	for i := range rows {
		if rows[i].Squad.Role != scheduling.RoleCoordinator {
			continue
		}
		names = append(names, rows[i].Squad.Name)
		if single == nil {
			q := rows[i].Squad
			single = &q
		}
	}
	switch len(names) {
	case 0:
		return scheduling.Squad{}, errNoCoordinatorSquad
	case 1:
		return *single, nil
	default:
		return scheduling.Squad{}, fmt.Errorf("%w（候选：%s）", errAmbiguousCoordinatorSquad, strings.Join(names, "、"))
	}
}

// handleCoordLaunch POST /api/cards/{id}/coordinator/launch：叫机器人并绑定
// coordinate 席位（spec §5.1 入口 2）。source 只允许 coordinate；旧 manual/card_create
// 来源不再接受。
//
// 行为顺序：source 校验 → 卡存在 → 协调者小队识别（0→400 指路 / ≥2→409 歧义）→
// LaunchAdmit 两级准入（满→409）→ 载体读 HomeDir → 组装 SessionSpec → LaunchForCard。
func (s *Server) handleCoordLaunch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	source := body.Source
	if source == "" {
		source = "coordinate"
	}
	if source != "coordinate" {
		s.log.Warn("协调者拉起被拒：来源已退役", "card", id, "source", source)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("source 只能是 coordinate，收到 %q", source))
		return
	}
	if _, err := s.ledger.GetCard(id); err != nil {
		ledgerErr(w, err)
		return
	}
	result, err := s.launchCoordinatorRound(r.Context(), id, source)
	if err != nil {
		var admissionErr *coordinatorAdmissionError
		var lookupErr *coordinatorLookupError
		var seatErr *coordinatorSeatConflict
		switch {
		case errors.As(err, &seatErr):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":      "协调者已启动但席位 CAS 冲突；新会话未自动终止，请人工回收",
				"session_id": seatErr.result.SessionID,
			})
		case errors.Is(err, ledger.ErrCASConflict):
			writeErr(w, http.StatusConflict, err)
		case errors.As(err, &lookupErr) && errors.Is(lookupErr.err, errNoCoordinatorSquad):
			writeErr(w, http.StatusBadRequest, fmt.Errorf(
				"未登记协调者小队：先登记载体，再登记协调者小队（示例：handoff squad create --name coord --role coordinator --member coord-carrier）"))
		case errors.As(err, &lookupErr) && errors.Is(lookupErr.err, errAmbiguousCoordinatorSquad):
			writeErr(w, http.StatusConflict, lookupErr.err)
		case errors.As(err, &lookupErr):
			s.log.Error("协调者小队识别失败", "card", id, "source", source, "cause", lookupErr.err)
			writeErr(w, http.StatusInternalServerError, lookupErr.err)
		case errors.As(err, &admissionErr) && errors.Is(admissionErr.err, scheduling.ErrNoSlot):
			writeErr(w, http.StatusConflict, fmt.Errorf(
				"协调者并发已满（小队 %s）：等现役回合结束名额自动回收后重试，或先 attach 现有会话",
				admissionErr.squad))
		case errors.As(err, &admissionErr):
			s.log.Error("协调者准入被拒", "card", id, "source", source,
				"squad", admissionErr.squad, "cause", admissionErr.err)
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("协调者准入被拒: %w", admissionErr.err))
		default:
			s.log.Error("协调者回合失败", "card", id, "source", source, "cause", err)
			writeErr(w, http.StatusBadGateway,
				fmt.Errorf("拉起协调者失败: %w", err))
		}
		return
	}
	s.log.Info("协调者 HTTP 拉起成功", "card", id, "source", source,
		"session", result.SessionID, "rebuilt", result.Rebuilt, "escalated", result.Escalated)
	writeJSON(w, http.StatusOK, proto.CoordinatorLaunchResp{
		Woke: result.Woke, SessionID: result.SessionID, Rebuilt: result.Rebuilt,
		Escalated: result.Escalated, Output: result.Output,
	})
}

// handleCoordRebind 只接受 mode=launch。mode=self 与 identity 均不属于 HTTP
// 可信输入：当前会话坐下由 CLI 本机账本完成，机器人换绑则由本端重新 Launch。
func (s *Server) handleCoordRebind(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req proto.CoordinatorRebindReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析协调者换绑请求: %w", err))
		return
	}
	if req.Mode == "self" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("mode=self 仅支持 CLI：handoff card rebind %s --self", id))
		return
	}
	if req.Mode != "launch" || req.Identity != "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("协调者 HTTP 换绑只接受 {\"mode\":\"launch\"}"))
		return
	}
	card, err := s.ledger.GetCard(id)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	_, tabLive := s.coordinatorTab(id)
	if card.DriverSession == "" && card.DriverSource == "" && !tabLive {
		writeErr(w, http.StatusConflict, fmt.Errorf("卡 %s 为空座，请使用 card bind 或 card coordinate", id))
		return
	}
	if card.DriverSession == "" && !tabLive {
		err := fmt.Errorf("卡 %s 的旧席位缺少身份，不能直接换绑", id)
		s.log.Warn("协调者换绑被拒：存量席位身份为空", "card", id, "source", card.DriverSource, "cause", err)
		writeErr(w, http.StatusConflict, err)
		return
	}
	result, err := s.launchCoordinatorRoundForRebind(r.Context(), id, "coordinate", card.DriverSession)
	if err != nil {
		var admissionErr *coordinatorAdmissionError
		var lookupErr *coordinatorLookupError
		var seatErr *coordinatorSeatConflict
		switch {
		case errors.As(err, &seatErr):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":      "协调者已启动但席位 CAS 冲突；新会话未自动终止，请人工回收",
				"session_id": seatErr.result.SessionID,
			})
		case errors.Is(err, ledger.ErrCASConflict):
			writeErr(w, http.StatusConflict, err)
		case errors.As(err, &lookupErr) && errors.Is(lookupErr.err, errNoCoordinatorSquad):
			writeErr(w, http.StatusBadRequest, fmt.Errorf(
				"未登记协调者小队：先登记载体，再登记协调者小队（示例：handoff squad create --name coord --role coordinator --member coord-carrier）"))
		case errors.As(err, &lookupErr) && errors.Is(lookupErr.err, errAmbiguousCoordinatorSquad):
			writeErr(w, http.StatusConflict, lookupErr.err)
		case errors.As(err, &lookupErr):
			s.log.Error("协调者换绑小队识别失败", "card", id, "source", "coordinate", "cause", lookupErr.err)
			writeErr(w, http.StatusInternalServerError, lookupErr.err)
		case errors.As(err, &admissionErr) && errors.Is(admissionErr.err, scheduling.ErrNoSlot):
			writeErr(w, http.StatusConflict, fmt.Errorf(
				"协调者并发已满（小队 %s）：等现役回合结束名额自动回收后重试，或先 attach 现有会话",
				admissionErr.squad))
		case errors.As(err, &admissionErr):
			s.log.Error("协调者换绑准入被拒", "card", id, "source", "coordinate",
				"squad", admissionErr.squad, "cause", admissionErr.err)
			writeErr(w, http.StatusBadRequest, fmt.Errorf("协调者准入被拒: %w", admissionErr.err))
		default:
			s.log.Error("协调者换绑回合失败", "card", id, "source", "coordinate", "cause", err)
			writeErr(w, http.StatusBadGateway, fmt.Errorf("换绑协调者失败: %w", err))
		}
		return
	}
	s.log.Info("协调者 HTTP 换绑成功", "card", id, "source", "coordinate", "session", result.SessionID)
	writeJSON(w, http.StatusOK, proto.CoordinatorLaunchResp{Woke: result.Woke, SessionID: result.SessionID,
		Rebuilt: result.Rebuilt, Escalated: result.Escalated, Output: result.Output})
}

// handleCoordForget 清除 agentd 进程内的旧协调者会话引用。
// 参数：请求路径中的 id 是卡号；请求体忽略。返回：成功时 {"ok":true}。
// 注意：账本席位由调用方先完成 CAS；本端点只调用 keystone 的唯一 Forget 面，
// 不复制或修改席位真相。
func (s *Server) handleCoordForget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.log.Info("驱逐协调者旧内存入口", "card", id)
	if _, err := s.ledger.GetCard(id); err != nil {
		s.log.Warn("驱逐协调者旧内存前读取卡失败", "card", id, "cause", err)
		ledgerErr(w, err)
		return
	}
	s.keystone.Forget(id)
	s.log.Info("协调者旧内存已驱逐", "card", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resolveCoordWorkdir 尽力解析卡所属项目在本机的位置根作为会话工作目录
// （SessionSpec.Workdir=项目位置根）。解析不到不阻断拉起（置空由承载层缺省），
// 只留日志——plan §D5.2 的 best-effort 定案。
func (s *Server) resolveCoordWorkdir(cardID string) string {
	card, err := s.ledger.GetCard(cardID)
	if err != nil {
		return ""
	}
	entries, err := s.st.ListProjectLocations()
	if err != nil {
		s.log.Warn("协调者工作目录解析失败：列项目位置出错", "card", cardID, "cause", err)
		return ""
	}
	loc, err := resolveProject("", card.Project, entries)
	if err != nil {
		s.log.Warn("协调者工作目录解析失败：项目未登记", "card", cardID, "project", card.Project, "cause", err)
		return ""
	}
	return loc.Path
}

// coordinatorAttachInfo 将 keystone 定位器的结果投影到 agentd wire DTO。
// 不修改服务端返回值、不拼接命令；命令仍由 locator 产生，Machine 为空串仍表示本机。
func coordinatorAttachInfo(info keysclient.AttachInfo) proto.CoordinatorAttachInfo {
	return proto.CoordinatorAttachInfo{
		Machine: info.Machine,
		Dir:     info.Dir,
		Command: info.Command,
	}
}

func (s *Server) handleCoordStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.log.Info("读取协调者状态", "card", id)
	card, err := s.ledger.GetCard(id)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	validSeat := card.DriverSession != "" && proto.ValidateSeat(card.DriverSession, proto.SeatSource(card.DriverSource)) == nil
	active := s.keystone.AttachState(id)
	var attach *proto.CoordinatorAttachInfo
	workdir := s.resolveCoordWorkdir(id)
	if info, err := s.keystone.Locate(id, workdir); err != nil {
		s.log.Warn("协调者状态定位失败", "card", id, "workdir", workdir, "cause", err)
	} else {
		mapped := coordinatorAttachInfo(info)
		attach = &mapped
		s.log.Info("协调者状态已读", "card", id, "bound", validSeat,
			"attach_active", active, "dir", mapped.Dir)
	}
	if attach == nil {
		s.log.Info("协调者状态已读", "card", id, "bound", false, "attach_active", active)
	}
	writeJSON(w, http.StatusOK, proto.CoordinatorStatus{Bound: validSeat, AttachActive: active, Attach: attach})
}

// handleCoordAttach POST /api/cards/{id}/attach：attach 深聊的接管/交回同一端点
// （配对原语表：SetAttach true 与 false 都在本端点）。
//
// active=true：先置接管再定位（缺口期宁可占用不可唤醒）；定位失败回滚接管态并 400。
// active=false：交回无头，自动唤醒恢复。
func (s *Server) handleCoordAttach(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) { // 跨机 ?machine= 转发（契约 §11）
		return
	}
	id := r.PathValue("id")
	s.log.Info("处理协调者 attach", "card", id)
	var body struct {
		Active  *bool  `json:"active"`
		Workdir string `json:"workdir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if body.Active == nil {
		s.log.Warn("协调者 attach 请求缺少 active", "card", id, "reason", "active_missing")
		writeErr(w, http.StatusBadRequest, fmt.Errorf("active 必填（true=接管，false=交回无头）"))
		return
	}
	if _, err := s.ledger.GetCard(id); err != nil {
		ledgerErr(w, err)
		return
	}
	if *body.Active {
		s.log.Info("协调者 attach 定位开始", "card", id, "workdir", body.Workdir)
		s.keystone.SetAttach(id, true)
		info, err := s.keystone.Locate(id, body.Workdir)
		if err != nil {
			s.keystone.SetAttach(id, false) // 回滚：没有终端可接管，不得留下静默占用
			s.log.Warn("attach 定位失败，接管态已回滚", "card", id, "workdir", body.Workdir, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		mapped := coordinatorAttachInfo(info)
		// command 只返回给请求方，不写日志：它包含 resume session id。
		s.log.Info("attach 接管完成", "card", id, "machine", mapped.Machine, "dir", mapped.Dir)
		writeJSON(w, http.StatusOK, mapped)
		return
	}
	s.keystone.SetAttach(id, false)
	s.log.Info("attach 交回", "card", id)
	writeJSON(w, http.StatusOK, proto.CoordinatorAttachReleaseResp{OK: true})
}
