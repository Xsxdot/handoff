// coordapi.go 实现协调者生命周期接线（B156.3 K4）：三个端点——
//
//	POST /api/cards/{id}/coordinator/launch  一键拉起（source=manual，看板按钮）
//	GET  /api/cards/{id}/coordinator         绑定与接管态
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

// handleCoordLaunch POST /api/cards/{id}/coordinator/launch：一键拉起协调者并绑定
// （spec §5.1 入口 2）。source 记录拉起来源（manual=看板按钮，card_create=开卡即绑），
// 只进审计；两入口共用 keystone.LaunchForCard 单一实现（§6.2）。
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
		source = "manual"
	}
	if source != "manual" && source != "card_create" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("source 只能是 manual 或 card_create，收到 %q", source))
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
		switch {
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
	if _, err := s.ledger.GetCard(id); err != nil {
		ledgerErr(w, err)
		return
	}
	active := s.keystone.AttachState(id)
	var attach *proto.CoordinatorAttachInfo
	workdir := s.resolveCoordWorkdir(id)
	if info, err := s.keystone.Locate(id, workdir); err != nil {
		s.log.Warn("协调者状态定位失败", "card", id, "workdir", workdir, "cause", err)
	} else {
		mapped := coordinatorAttachInfo(info)
		attach = &mapped
		s.log.Info("协调者状态已读", "card", id, "bound", true,
			"attach_active", active, "dir", mapped.Dir)
	}
	if attach == nil {
		s.log.Info("协调者状态已读", "card", id, "bound", false, "attach_active", active)
	}
	writeJSON(w, http.StatusOK, proto.CoordinatorStatus{
		Bound: attach != nil, AttachActive: active, Attach: attach,
	})
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
