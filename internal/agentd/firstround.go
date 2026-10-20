// firstround.go —— B448 协调者首轮按载体机转交：受理端出站 + 收件端 handler + 错误还原。
//
// 职责：
//   - 受理端（runOnCoordinatorCarrier 远端分支的 transfer 回调）：经设备表客户端池（clientForTarget）带防环头
//     把「这一轮」转交给载体机 agentd，同步等回执，把收件端的 kind 还原成既有的类型化错误（入口 handler 的
//     错误映射因此零改，plan §3）；出站只发一次（F36），ctx 截止 ≥ 回合上界（F34）。
//   - 收件端（两个新端点）：校验转交头与冻结字段（区分缺失与空，F4/F8–F11）→ 放宽写截止（F35）→ 按冻结载体
//     走本机拉起路径 → 回执或带 kind 的错误体（F16–F30）。
//
// 边界：只走 agentd → client 出站（与续轮 wake 转交同向）；不新增 scheduling / ledger / collab 的对外依赖；
// 执行机身份沿用 IsLocalMachine || IsSelfTarget（B441）；收件端不要求控制台身份（F5，信任由 Bearer 承担）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// firstRoundTransferSlack 是转交出站截止与收件端写截止在回合上界之上的余量：执行机回合恰到上界时，
// 回执的落账与写出仍有时间，受理机不得比本机路径更早放弃（spec「实现决定」超时条）。
const firstRoundTransferSlack = time.Minute

// firstRoundTransferBudget 返回一次首轮转交的时间预算（出站 ctx 截止与收件端写截止共用，F34/F35）。
func firstRoundTransferBudget() time.Duration { return coordWakeTurnTimeout + firstRoundTransferSlack }

// firstRoundTransferCtx 给转交出站套上预算截止；父 ctx 更早取消时以父为准（调用方断开 = 不再等）。
func firstRoundTransferCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, firstRoundTransferBudget())
}

// firstRoundSelfName 是本机在转交日志与回执里的名字：配置自名优先，其次 hostname。
func (s *Server) firstRoundSelfName() string {
	if cfg := s.conf(); cfg != nil && strings.TrimSpace(cfg.Name) != "" {
		return strings.TrimSpace(cfg.Name)
	}
	return s.localMachineName()
}

// firstRoundTransferError 是受理端的转交失败：err 是还原后的类型化错误（errors.Is/As 经 Unwrap 命中），
// received 报告是否拿到了 HTTP 回执，pointed / kind 来自收件端错误体（驻场指针归属，Q3 / F31）。
type firstRoundTransferError struct {
	machine  string
	received bool
	pointed  bool
	kind     string
	err      error
}

func (e *firstRoundTransferError) Error() string { return e.err.Error() }
func (e *firstRoundTransferError) Unwrap() error { return e.err }

// firstRoundFailure 把一次转交出站的失败还原成受理端错误（plan §8 F19–F29）。
//
// 参数：squad 是本次小队名（no_slot / admission_rejected 还原成 *coordinatorAdmissionError 需要它）；
// carrier / machine 是冻结载体与目标机（文案点名）。
func firstRoundFailure(squad, carrier, machine string, err error) *firstRoundTransferError {
	if errors.Is(err, client.ErrFirstRoundUnsupported) {
		return &firstRoundTransferError{machine: machine, received: true,
			err: fmt.Errorf("%s 的 agentd 版本过旧，不支持首轮转交，请升级", machine)}
	}
	var fe *client.FirstRoundError
	if !errors.As(err, &fe) {
		hint := ""
		if errors.Is(err, client.ErrUnreachable) || errors.Is(err, client.ErrTunnelDisconnected) ||
			errors.Is(err, context.DeadlineExceeded) {
			// 「拨不通」与「发出后读写中断」在 client 层同为 ErrUnreachable：后者可能已在该机跑完。
			hint = "（若请求已送达，首轮可能仍在该机执行或已完成，请刷新查看）"
		}
		return &firstRoundTransferError{machine: machine,
			err: fmt.Errorf("协调者载体 %s 在 %s，转交到该机失败：%w%s", carrier, machine, err, hint)}
	}
	out := &firstRoundTransferError{machine: machine, received: true, pointed: fe.Pointed, kind: fe.Kind}
	msg := fe.Message
	switch fe.Kind {
	case proto.FirstRoundKindNoSlot:
		out.err = &coordinatorAdmissionError{squad: squad,
			err: fmt.Errorf("执行机 %s：%s: %w", machine, msg, scheduling.ErrNoSlot)}
	case proto.FirstRoundKindAdmissionRejected:
		out.err = &coordinatorAdmissionError{squad: squad, err: fmt.Errorf("执行机 %s：%s", machine, msg)}
	case proto.FirstRoundKindCASConflict:
		out.err = fmt.Errorf("执行机 %s：%s: %w", machine, msg, ledger.ErrCASConflict)
	case proto.FirstRoundKindSeatConflict:
		out.err = &coordinatorSeatConflict{result: keystone.RoundResult{SessionID: fe.SessionID}}
	case proto.FirstRoundKindResidentOccupied:
		out.err = fmt.Errorf("执行机 %s：%s: %w", machine, msg, collab.ErrResidentOccupied)
	case proto.FirstRoundKindReadOnly:
		out.err = fmt.Errorf("执行机 %s：%s: %w", machine, msg, collab.ErrReadOnly)
	case proto.FirstRoundKindNoRoom:
		out.err = fmt.Errorf("执行机 %s：%s: %w", machine, msg, collab.ErrNoRoom)
	case proto.FirstRoundKindNotLocal:
		out.err = fmt.Errorf("%s（执行机 %s）", msg, machine)
	case proto.FirstRoundKindRoundFailed:
		out.err = fmt.Errorf("执行机 %s 首轮失败：%s", machine, msg)
	case proto.FirstRoundKindInvalid:
		// F29：受理端发出不成形的请求是受理端自己的缺陷，不伪装成用户请求错误（入口 502）。
		out.err = fmt.Errorf("执行机 %s 拒绝了不成形的首轮转交请求（受理机缺陷）：%s", machine, msg)
	default:
		out.err = fmt.Errorf("执行机 %s 回 HTTP %d：%s", machine, fe.Status, msg)
	}
	return out
}

// transferCoordinatorFirstRound 是卡首轮的受理端出站（runOnCoordinatorCarrier 远端分支，名额已归还）。
// expect 非空 = 换绑（F13）。成功返回执行机回执的 RoundResult（不含 handled_by，F18）。
func (s *Server) transferCoordinatorFirstRound(ctx context.Context, card string, binding scheduling.Binding,
	carrier scheduling.Carrier, expect string) (keystone.RoundResult, error) {
	machine, self := carrier.Machine, s.firstRoundSelfName()
	req := proto.FirstRoundTransferReq{Squad: binding.Squad, Carrier: binding.Carrier, AcceptedBy: self, Expect: expect}
	s.log.Log(ctx, wakeRoundLogLevel, "协调者首轮转交：受理机发出", "card", card, "accepted_by", self,
		"handled_by", machine, "squad", binding.Squad, "carrier", binding.Carrier, "rebind", expect != "")
	target, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("协调者首轮转交取目标客户端失败", "card", card, "accepted_by", self, "handled_by", machine,
			"carrier", binding.Carrier, "cause", err)
		return keystone.RoundResult{}, &firstRoundTransferError{machine: machine,
			err: fmt.Errorf("协调者载体 %s 在 %s，转交到该机失败：%w", binding.Carrier, machine, err)}
	}
	tctx, cancel := firstRoundTransferCtx(ctx)
	defer cancel()
	resp, err := target.MarkForwarded().CoordinatorFirstRound(tctx, card, req)
	if err != nil {
		failure := firstRoundFailure(binding.Squad, binding.Carrier, machine, err)
		s.log.Log(ctx, wakeRoundLogLevel, "协调者首轮转交失败：受理机", "card", card, "accepted_by", self,
			"handled_by", machine, "carrier", binding.Carrier, "received", failure.received, "kind", failure.kind,
			"cause", err)
		return keystone.RoundResult{}, failure
	}
	s.log.Log(ctx, wakeRoundLogLevel, "协调者首轮转交完成：受理机收到回执", "card", card, "accepted_by", self,
		"handled_by", resp.HandledBy, "carrier", binding.Carrier, "session", resp.SessionID)
	return keystone.RoundResult{Woke: resp.Woke, SessionID: resp.SessionID, Rebuilt: resp.Rebuilt,
		Escalated: resp.Escalated, Output: resp.Output}, nil
}

// transferResidentFirstRound 是驻场者首轮的受理端出站（名额已归还）。成功返回执行机回执的驻场落点。
func (s *Server) transferResidentFirstRound(ctx context.Context, sessionID string, binding scheduling.Binding,
	carrier scheduling.Carrier) (proto.SessionResidentOutcome, error) {
	machine, self := carrier.Machine, s.firstRoundSelfName()
	req := proto.FirstRoundTransferReq{Squad: binding.Squad, Carrier: binding.Carrier, AcceptedBy: self}
	s.log.Log(ctx, wakeRoundLogLevel, "驻场者首轮转交：受理机发出", "session", sessionID, "accepted_by", self,
		"handled_by", machine, "squad", binding.Squad, "carrier", binding.Carrier)
	target, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("驻场者首轮转交取目标客户端失败", "session", sessionID, "accepted_by", self,
			"handled_by", machine, "carrier", binding.Carrier, "cause", err)
		return proto.SessionResidentOutcome{}, &firstRoundTransferError{machine: machine,
			err: fmt.Errorf("协调者载体 %s 在 %s，转交到该机失败：%w", binding.Carrier, machine, err)}
	}
	tctx, cancel := firstRoundTransferCtx(ctx)
	defer cancel()
	resp, err := target.MarkForwarded().ResidentFirstRound(tctx, sessionID, req)
	if err != nil {
		failure := firstRoundFailure(binding.Squad, binding.Carrier, machine, err)
		s.log.Log(ctx, wakeRoundLogLevel, "驻场者首轮转交失败：受理机", "session", sessionID, "accepted_by", self,
			"handled_by", machine, "carrier", binding.Carrier, "received", failure.received, "kind", failure.kind,
			"pointed", failure.pointed, "cause", err)
		return proto.SessionResidentOutcome{}, failure
	}
	s.log.Log(ctx, wakeRoundLogLevel, "驻场者首轮转交完成：受理机收到回执", "session", sessionID,
		"accepted_by", self, "handled_by", resp.HandledBy, "carrier", binding.Carrier,
		"carrier_machine", resp.CarrierMachine)
	return resp.SessionResidentOutcome, nil
}

// firstRoundReqBodyLimit 是收件端请求体上限（请求只有四个短字段）。
const firstRoundReqBodyLimit = 64 << 10

// decodeFirstRoundReq 校验转交头并解码冻结字段，区分「缺键」与「空串」（F4/F8–F11）。不成形时已写 400
// kind=invalid 并返回 ok=false；此路径零写入、零计数、零回合。
func (s *Server) decodeFirstRoundReq(w http.ResponseWriter, r *http.Request, attrs ...any) (proto.FirstRoundTransferReq, bool) {
	var zero proto.FirstRoundTransferReq
	reject := func(err error) (proto.FirstRoundTransferReq, bool) {
		s.log.Warn("首轮转交请求不成形，拒绝", append(attrs, "cause", err)...)
		writeFirstRoundError(w, http.StatusBadRequest, proto.FirstRoundKindInvalid, err, "", false)
		return zero, false
	}
	if !isForwarded(r) {
		return reject(fmt.Errorf("缺转交头 %s：首轮转交端点只接受 agentd 之间的转交", forwardedHeader))
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, firstRoundReqBodyLimit))
	if err != nil {
		return reject(fmt.Errorf("读请求体: %w", err))
	}
	var body struct {
		Squad      *string `json:"squad"`
		Carrier    *string `json:"carrier"`
		AcceptedBy string  `json:"accepted_by"`
		Expect     string  `json:"expect"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return reject(fmt.Errorf("解析请求体: %w", err))
	}
	for _, f := range []struct {
		key string
		val *string
	}{{"squad", body.Squad}, {"carrier", body.Carrier}} {
		if f.val == nil {
			return reject(fmt.Errorf("冻结字段 %s 缺失", f.key))
		}
		if strings.TrimSpace(*f.val) == "" {
			return reject(fmt.Errorf("冻结字段 %s 为空", f.key))
		}
	}
	return proto.FirstRoundTransferReq{Squad: strings.TrimSpace(*body.Squad), Carrier: strings.TrimSpace(*body.Carrier),
		AcceptedBy: body.AcceptedBy, Expect: body.Expect}, true
}

// extendFirstRoundWriteDeadline 把本次响应的写截止放宽到转交预算（F35）：server 级 WriteTimeout（11m）在
// handler 执行前就已设定，而回合上界是 30m，不放宽则长回合的回执写不出去、受理机只能收到「发出后中断」。
func (s *Server) extendFirstRoundWriteDeadline(w http.ResponseWriter, attrs ...any) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(firstRoundTransferBudget())); err != nil {
		s.log.Warn("首轮转交收件端放宽写截止失败，长回合回执可能被 server 级写超时截断", append(attrs, "cause", err)...)
	}
}

// writeFirstRoundError 写首轮转交错误体 {"error","kind","session_id"?,"pointed"?}。
func writeFirstRoundError(w http.ResponseWriter, status int, kind string, err error, sessionID string, pointed bool) {
	writeJSON(w, status, proto.FirstRoundTransferError{Error: err.Error(), Kind: kind, SessionID: sessionID, Pointed: pointed})
}

// firstRoundErrorKind 把收件端本机拉起的错误映射成 wire 状态码与 kind（plan §8 F20–F28）。
func firstRoundErrorKind(err error) (int, string, string) {
	var notLocal *firstRoundNotLocalError
	var seatErr *coordinatorSeatConflict
	var admissionErr *coordinatorAdmissionError
	var lookupErr *coordinatorLookupError
	switch {
	case errors.As(err, &notLocal):
		return http.StatusConflict, proto.FirstRoundKindNotLocal, ""
	case errors.As(err, &seatErr):
		return http.StatusConflict, proto.FirstRoundKindSeatConflict, seatErr.result.SessionID
	case errors.As(err, &admissionErr) && errors.Is(admissionErr.err, scheduling.ErrNoSlot):
		return http.StatusConflict, proto.FirstRoundKindNoSlot, ""
	case errors.As(err, &admissionErr):
		return http.StatusBadRequest, proto.FirstRoundKindAdmissionRejected, ""
	case errors.As(err, &lookupErr):
		return http.StatusBadRequest, proto.FirstRoundKindInvalid, ""
	case errors.Is(err, ledger.ErrCASConflict):
		return http.StatusConflict, proto.FirstRoundKindCASConflict, ""
	case errors.Is(err, collab.ErrNoRoom):
		return http.StatusConflict, proto.FirstRoundKindNoRoom, ""
	case errors.Is(err, collab.ErrReadOnly):
		return http.StatusConflict, proto.FirstRoundKindReadOnly, ""
	case errors.Is(err, collab.ErrResidentOccupied):
		return http.StatusConflict, proto.FirstRoundKindResidentOccupied, ""
	default:
		return http.StatusBadGateway, proto.FirstRoundKindRoundFailed, ""
	}
}

// handleCoordFirstRound POST /api/cards/{id}/coordinator/first-round：卡首轮转交收件端（F1）。
// 卡不存在回 409 not_found（F20，业务上绝不回 404——404 专属「对端版本过旧」）。
func (s *Server) handleCoordFirstRound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.extendFirstRoundWriteDeadline(w, "card", id)
	req, ok := s.decodeFirstRoundReq(w, r, "card", id)
	if !ok {
		return
	}
	self := s.firstRoundSelfName()
	s.log.Log(r.Context(), wakeRoundLogLevel, "协调者首轮转交：执行机收件", "card", id,
		"accepted_by", req.AcceptedBy, "handled_by", self, "squad", req.Squad, "carrier", req.Carrier,
		"rebind", req.Expect != "")
	if _, err := s.ledger.GetCard(id); err != nil {
		status, kind := http.StatusBadGateway, proto.FirstRoundKindRoundFailed
		if errors.Is(err, ledger.ErrNotFound) {
			status, kind = http.StatusConflict, proto.FirstRoundKindNotFound
		}
		s.log.Warn("协调者首轮转交收件：读卡失败", "card", id, "accepted_by", req.AcceptedBy, "kind", kind, "cause", err)
		writeFirstRoundError(w, status, kind, fmt.Errorf("读卡 %s: %w", id, err), "", false)
		return
	}
	result, err := s.launchCoordinatorRoundFrozen(r.Context(), id, req.Squad, req.Carrier, req.Expect)
	if err != nil {
		status, kind, sessionID := firstRoundErrorKind(err)
		s.log.Log(r.Context(), wakeRoundLogLevel, "协调者首轮转交失败：执行机", "card", id,
			"accepted_by", req.AcceptedBy, "handled_by", self, "carrier", req.Carrier, "kind", kind, "cause", err)
		writeFirstRoundError(w, status, kind, err, sessionID, false)
		return
	}
	s.log.Log(r.Context(), wakeRoundLogLevel, "协调者首轮转交完成：执行机", "card", id,
		"accepted_by", req.AcceptedBy, "handled_by", self, "carrier", req.Carrier, "session", result.SessionID)
	writeJSON(w, http.StatusOK, proto.CoordinatorFirstRoundResp{
		CoordinatorLaunchResp: proto.CoordinatorLaunchResp{Woke: result.Woke, SessionID: result.SessionID,
			Rebuilt: result.Rebuilt, Escalated: result.Escalated, Output: result.Output},
		HandledBy: self,
	})
}

// handleResidentFirstRound POST /api/sessions/{id}/resident/first-round：驻场者首轮转交收件端（F2）。
// 错误体 pointed 为真当且仅当本端已在该会话落了失败指针行（F30）。不要求控制台身份（F5）。
func (s *Server) handleResidentFirstRound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.extendFirstRoundWriteDeadline(w, "session", id)
	req, ok := s.decodeFirstRoundReq(w, r, "session", id)
	if !ok {
		return
	}
	self := s.firstRoundSelfName()
	s.log.Log(r.Context(), wakeRoundLogLevel, "驻场者首轮转交：执行机收件", "session", id,
		"accepted_by", req.AcceptedBy, "handled_by", self, "squad", req.Squad, "carrier", req.Carrier)
	outcome, pointed, err := s.launchSessionResidentOn(r.Context(), id, req.Squad, req.Carrier)
	if err != nil {
		status, kind, _ := firstRoundErrorKind(err)
		s.log.Log(r.Context(), wakeRoundLogLevel, "驻场者首轮转交失败：执行机", "session", id,
			"accepted_by", req.AcceptedBy, "handled_by", self, "carrier", req.Carrier, "kind", kind,
			"pointed", pointed, "cause", err)
		writeFirstRoundError(w, status, kind, err, "", pointed)
		return
	}
	s.log.Log(r.Context(), wakeRoundLogLevel, "驻场者首轮转交完成：执行机", "session", id,
		"accepted_by", req.AcceptedBy, "handled_by", self, "carrier", req.Carrier,
		"carrier_machine", outcome.CarrierMachine)
	writeJSON(w, http.StatusOK, proto.ResidentFirstRoundResp{SessionResidentOutcome: outcome, HandledBy: self})
}
