// 账本 HTTP API：web 看板的唯一账本通道。薄层——业务全在
// internal/ledger，此处只做解码/调用/编码与错误翻译。写动作只有
// move/note/answer 三个；派发、合并等动作由 CLI 承载。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// SetLedger 注入账本库（agentd 启动时；nil = 未配置，API 降级 503）。
func (s *Server) SetLedger(st *ledger.Store) { s.ledger = st }

func (s *Server) registerLedgerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/cards", s.withLedger(s.handleCardsList))
	api.HandleFunc("GET /api/cards/{id}", s.withLedger(s.handleCardDetail))
	api.HandleFunc("POST /api/cards/{id}/move", s.withLedger(s.handleCardMove))
	api.HandleFunc("POST /api/cards/{id}/note", s.withLedger(s.handleCardNote))
	api.HandleFunc("GET /api/flows", s.withLedger(s.handleFlows))
	api.HandleFunc("GET /api/decisions", s.withLedger(s.handleDecisions))
	api.HandleFunc("POST /api/decisions/{id}/answer", s.withLedger(s.handleDecisionAnswer))
	api.HandleFunc("GET /api/ledger/health", s.withLedger(s.handleLedgerHealth))
}

func (s *Server) withLedger(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.ledger == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "账本库未配置（config.ledger.dsn 或单机回退）",
			})
			return
		}
		h(w, r)
	}
}

func ledgerErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, ledger.ErrCASConflict), errors.Is(err, ledger.ErrGateBlocked),
		errors.Is(err, ledger.ErrBadState), errors.Is(err, ledger.ErrBadMerge),
		errors.Is(err, ledger.ErrCycle):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) handleCardsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	views, err := s.ledger.ListCards(ledger.CardFilter{
		Project:         q.Get("project"),
		Status:          q.Get("status"),
		BaseBranch:      q.Get("base_branch"),
		Needs:           q.Get("needs") == "1",
		Blocked:         q.Get("blocked") == "1",
		IncludeTerminal: q.Get("all") == "1",
	})
	if err != nil {
		ledgerErr(w, err)
		return
	}
	tickets, err := s.ledger.OpenTicketCounts()
	if err != nil {
		s.log.Warn("未决工单推导失败（徽标退化为不显示，不阻塞列表）", "err", err)
		tickets = nil
	}
	out := make([]map[string]any, 0, len(views))
	for _, view := range views {
		conflict := false
		if view.Status == ledger.StatusDoing {
			states, stateErr := s.ledger.LatestTaskStates(view.ID)
			if stateErr == nil {
				for _, state := range states {
					if state.LastType == "failed" {
						conflict = true
						break
					}
				}
			}
		}
		out = append(out, map[string]any{
			"id": view.ID, "title": view.Title, "status": view.Status, "priority": view.Priority,
			"project": view.Project, "workflow": view.WorkflowName, "parent": view.ParentID, "base_branch": view.BaseBranch,
			"attachments": view.Attachments, "following": view.Following,
			"blocked": view.Blocked, "blocked_by": view.BlockedBy, "needs": view.NeedsReason,
			"open_decisions": view.OpenDecisions, "conflict": conflict,
			"open_tickets": tickets[view.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cards":    out,
		"unlinked": s.unlinkedSummary(),
	})
}

// cfgTargets returns the current target snapshot. Config is copy-on-write, so
// callers may iterate this map without racing a config mutation.
func (s *Server) cfgTargets() map[string]config.Target {
	return s.conf().Targets
}

// unlinkedSummary 「未挂账」摘要：登记 target 上存在、但 card_tasks 里
// 没有的 task。拨号失败进入 unknown_targets，而不是假装为零。
func (s *Server) unlinkedSummary() map[string]any {
	s.unlinkedMu.Lock()
	defer s.unlinkedMu.Unlock()
	if time.Since(s.unlinkedAt) < 30*time.Second && s.unlinkedCache != nil {
		return s.unlinkedCache
	}

	linked := map[string]bool{}
	if links, err := s.ledger.AllTaskLinks(); err == nil {
		for _, link := range links {
			linked[link.Target+"/"+link.TaskID] = true
		}
	} else {
		s.log.Warn("读取挂账 task 失败，未挂账摘要可能不完整", "err", err)
	}

	rows := make([]map[string]any, 0)
	unknown := make([]string, 0)
	targets := s.cfgTargets()
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := targets[name]
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		tasks, err := client.New(target.Addr, target.Token).ListTasks(ctx)
		cancel()
		if err != nil {
			unknown = append(unknown, name)
			continue
		}
		for _, task := range tasks {
			if linked[name+"/"+task.ID] {
				continue
			}
			rows = append(rows, map[string]any{
				"target": name, "task_id": task.ID, "title": task.Name, "state": task.State,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		leftTarget, _ := rows[i]["target"].(string)
		rightTarget, _ := rows[j]["target"].(string)
		if leftTarget != rightTarget {
			return leftTarget < rightTarget
		}
		leftTask, _ := rows[i]["task_id"].(string)
		rightTask, _ := rows[j]["task_id"].(string)
		return leftTask < rightTask
	})
	sort.Strings(unknown)
	s.unlinkedCache = map[string]any{
		"count": len(rows), "tasks": rows, "unknown_targets": unknown,
	}
	s.unlinkedAt = time.Now()
	return s.unlinkedCache
}

func (s *Server) handleCardDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	card, err := s.ledger.GetCard(id)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	relations, _ := s.ledger.RelationsOf(id)
	events, _ := s.ledger.EventsFromAsc([]string{id}, 0, 500)
	taskStates, _ := s.ledger.LatestTaskStates(id)
	base, _ := s.ledger.EffectiveBaseBranch(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"card": card, "relations": relations, "events": events,
		"task_states": taskStates, "effective_base_branch": base,
	})
}

func (s *Server) handleCardMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		To     string `json:"to"`
		Expect string `json:"expect"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := s.ledger.MoveCard(r.PathValue("id"), req.To, req.Expect, "web:"+r.RemoteAddr); err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCardNote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Body string `json:"body"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Kind == "" {
		req.Kind = "普通"
	}
	event, err := s.ledger.AddComment(r.PathValue("id"), req.Body, req.Kind, "web:"+r.RemoteAddr)
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleFlows(w http.ResponseWriter, r *http.Request) {
	names, err := s.ledger.ListWorkflowNames()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	flows := make([]map[string]any, 0, len(names))
	for _, name := range names {
		workflow, err := s.ledger.GetWorkflow(name, 0)
		if err != nil {
			continue
		}
		flows = append(flows, map[string]any{
			"name": workflow.Name, "version": workflow.Version, "def": workflow.Def,
		})
	}
	templateNames, _ := s.ledger.ListTemplateNames()
	templates := make([]map[string]any, 0, len(templateNames))
	for _, name := range templateNames {
		template, err := s.ledger.GetTemplate(name, 0)
		if err != nil {
			continue
		}
		templates = append(templates, map[string]any{
			"name": template.Name, "version": template.Version, "def": template.Def,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": flows, "templates": templates})
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	decisions, err := s.ledger.ListDecisions(r.URL.Query().Get("open") == "1")
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

func (s *Server) handleDecisionAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := s.ledger.AnswerDecision(id, req.Answer, "web:"+r.RemoteAddr); err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleLedgerHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := s.ledger.MirrorHealth()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mirror": rows})
}
