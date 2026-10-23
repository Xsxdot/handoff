package agentd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// handleRetryTaskTicketNotification rechecks source truth before re-resolving an
// existing notice. It never answers a ticket or creates a second ticket surface.
func (s *Server) handleRetryTaskTicketNotification(w http.ResponseWriter, r *http.Request) {
	taskID, ticketID := r.PathValue("id"), r.PathValue("ticket")
	s.log.Info("工单通知重试请求", "task", taskID, "ticket", ticketID)
	var request map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是空 JSON 对象 {}"})
		return
	}
	if request == nil || len(request) != 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是空 JSON 对象 {}"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体只能包含一个空 JSON 对象 {}"})
		return
	}
	sourceMachine := s.TaskNoticeSourceMachine()
	if s.ledger == nil || s.mgr == nil || sourceMachine == "" {
		s.log.Warn("工单通知重试未就绪", "task", taskID, "ticket", ticketID)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "通知服务未就绪"})
		return
	}
	task, err := s.mgr.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		s.log.Error("通知重试读取任务失败", "task", taskID, "ticket", ticketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取任务失败"})
		return
	}
	notice, noticeErr := s.ledger.GetTaskTicketNotice(sourceMachine, taskID, ticketID)
	if noticeErr != nil && !errors.Is(noticeErr, sql.ErrNoRows) && !errors.Is(noticeErr, ledger.ErrNotFound) && !errors.Is(noticeErr, store.ErrNotFound) {
		s.log.Error("通知重试读取路由失败", "task", taskID, "ticket", ticketID, "cause", noticeErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取通知状态失败"})
		return
	}
	sourceTicket, sourceTicketErr := s.st.GetTicket(ticketID)
	if errors.Is(sourceTicketErr, store.ErrNotFound) || (sourceTicketErr == nil && sourceTicket.TaskID != taskID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "工单不存在"})
		return
	}
	if sourceTicketErr != nil {
		s.log.Error("通知重试读取源工单失败", "task", taskID, "ticket", ticketID, "cause", sourceTicketErr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "读取源工单失败"})
		return
	}
	if noticeErr == nil && (notice.Status == "answered" || notice.Status == "superseded") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "工单已关闭，不能重试通知"})
		return
	}
	pending, err := s.mgr.PendingTickets(taskID)
	if err != nil {
		s.log.Error("通知重试读取待答工单失败", "task", taskID, "ticket", ticketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取工单失败"})
		return
	}
	var ticket *proto.Ticket
	for i := range pending {
		if pending[i].ID == ticketID {
			ticket = &pending[i]
			break
		}
	}
	if sourceTicket.Answer != nil {
		reason := "ticket_answered"
		if *sourceTicket.Answer == store.VoidAnswer {
			reason = "tickets_voided"
		}
		if noticeErr == nil {
			if err := s.ledger.SetTaskTicketNoticeStatus(sourceMachine, taskID, ticketID,
				notice.OwnershipVersion, "answered", reason, ""); err != nil {
				s.log.Warn("源工单已结束但通知收口失败", "task", taskID, "ticket", ticketID, "cause", err)
				writeJSON(w, http.StatusConflict, map[string]string{"error": "工单已结束，通知状态待同步"})
				return
			}
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "工单已结束，不能重试通知"})
		return
	}
	events, err := s.mgr.EventsFrom(taskID, 0, 10000)
	if err != nil {
		s.log.Error("通知重试读取源事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取工单事件失败"})
		return
	}
	if ticket == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "源任务没有该待答工单，通知未重试"})
		return
	}
	if noticeErr == nil && (notice.TicketKind != ticket.Kind || notice.SourceSeq <= 0) {
		s.log.Warn("通知重试工单身份与来源不一致", "task", taskID, "ticket", ticketID,
			"notice_kind", notice.TicketKind, "source_kind", ticket.Kind, "source_seq", notice.SourceSeq)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "通知来源与待答工单不一致"})
		return
	}
	var sourceEvent *proto.Event
	for i := range events {
		if (events[i].Type != proto.EventTypeQuestion && events[i].Type != proto.EventTypePermissionRequest) || events[i].Seq <= 0 {
			continue
		}
		var payload struct {
			TicketID string `json:"ticket_id"`
		}
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			s.log.Warn("通知重试忽略损坏的历史工单事件", "task", taskID, "seq", events[i].Seq, "cause", err)
			continue
		}
		if payload.TicketID == ticketID {
			event := events[i]
			sourceEvent = &event
			break
		}
	}
	if sourceEvent == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "找不到原始工单事件，通知未重试"})
		return
	}
	cardID := ""
	if linked, linkErr := s.ledger.CardOfTask(sourceMachine, taskID); linkErr == nil {
		cardID = linked
	} else if !errors.Is(linkErr, ledger.ErrNotFound) {
		s.log.Error("通知重试查询卡关联失败", "task", taskID, "ticket", ticketID, "cause", linkErr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "读取卡路由失败"})
		return
	}
	if err := s.RecordTaskTicketNotice(sourceMachine, taskID, cardID, task.OriginSession, *sourceEvent); err != nil {
		s.log.Warn("通知重试重新解析路由失败", "task", taskID, "ticket", ticketID,
			"source_seq", sourceEvent.Seq, "cause", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "通知路由暂不可用"})
		return
	}
	updated, err := s.ledger.GetTaskTicketNotice(sourceMachine, taskID, ticketID)
	if err != nil {
		s.log.Error("通知重试读取更新结果失败", "task", taskID, "ticket", ticketID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取通知结果失败"})
		return
	}
	if updated.Status == "needs_human" && updated.Target != nil {
		updated, _, err = s.ledger.RetryTaskTicketNotice(sourceMachine, taskID, ticketID,
			updated.OwnershipVersion, "user")
		if err != nil {
			s.log.Warn("工单通知重试重新入队失败", "task", taskID, "ticket", ticketID, "cause", err)
			writeJSON(w, http.StatusConflict, map[string]string{"error": "通知已变化，请刷新后重试"})
			return
		}
	}
	s.log.Info("工单通知重试完成", "task", taskID, "ticket", ticketID,
		"ownership_version", updated.OwnershipVersion, "status", updated.Status, "target_set", updated.Target != nil)
	writeJSON(w, http.StatusOK, proto.RetryTaskTicketNotificationResponse{Notification: proto.TicketNotification{
		Status: updated.Status, Reason: updated.Reason, OwnershipVersion: updated.OwnershipVersion, LastError: updated.LastError,
	}})
}
