// 账本 HTTP API：web 看板的唯一账本通道。薄层——业务全在
// internal/ledger，此处只做解码/调用/编码与错误翻译。写动作：
// move/note/answer/accept 同步返回；step（工作流节点）异步 202，
// 编排在 internal/ledgerstep。实现类派发仍只由 CLI 承载——它要挂 plan 文件，
// 浏览器里没有那个文件。
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
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// SetLedger 注入账本库（agentd 启动时；nil = 未配置，除 health 外 API 降级 503）。
func (s *Server) SetLedger(st *ledger.Store) { s.ledger = st }

func (s *Server) registerLedgerRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/cards", s.withLedger(s.handleCardsList))
	api.HandleFunc("GET /api/cards/{id}", s.withLedger(s.handleCardDetail))
	api.HandleFunc("POST /api/cards", s.withLedger(s.handleCardCreate))
	api.HandleFunc("PATCH /api/cards/{id}", s.withLedger(s.handleCardPatch))
	api.HandleFunc("POST /api/cards/{id}/attachments", s.withLedger(s.handleCardAttach))
	api.HandleFunc("DELETE /api/cards/{id}/attachments", s.withLedger(s.handleCardDetach))
	api.HandleFunc("POST /api/cards/{id}/move", s.withLedger(s.handleCardMove))
	api.HandleFunc("POST /api/cards/{id}/note", s.withLedger(s.handleCardNote))
	api.HandleFunc("POST /api/cards/{id}/accept", s.withLedger(s.handleCardAccept))
	api.HandleFunc("POST /api/cards/{id}/step", s.withLedger(s.handleCardStep))
	api.HandleFunc("POST /api/cards/{id}/migrate", s.withLedger(s.handleCardMigrate))
	api.HandleFunc("POST /api/cards/{id}/needs/clear", s.withLedger(s.handleCardNeedsClear))
	api.HandleFunc("GET /api/flows", s.withLedger(s.handleFlows))
	api.HandleFunc("GET /api/decisions", s.withLedger(s.handleDecisions))
	api.HandleFunc("POST /api/decisions/{id}/answer", s.withLedger(s.handleDecisionAnswer))
	api.HandleFunc("GET /api/flows/{name}", s.withLedger(s.handleFlowGet))
	api.HandleFunc("PUT /api/flows/{name}", s.withLedger(s.handleFlowPut))
	api.HandleFunc("GET /api/disciplines", s.withLedger(s.handleDisciplineNames))
	// health 是前端的门控探针，必须恒 200：503 与网络错在浏览器侧不可区分。
	// 其余 /api/cards* 等仍走 withLedger（未挂载 = 503）。
	api.HandleFunc("GET /api/ledger/health", s.handleLedgerHealth)
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
		errors.Is(err, ledger.ErrCycle), errors.Is(err, ledger.ErrStepInFlight):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// writeErr 按本文件既有约定写错误响应：{"error": "<原因>"}。
//
// 抽出来只是省重复，语义与散落各处的 writeJSON(w, code, map[string]string{"error": ...}) 完全一致。
func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// ledgerActor 为浏览器写操作生成与既有账本 handler 一致的 actor 标识。
func (s *Server) ledgerActor(r *http.Request) string {
	return "web:" + r.RemoteAddr
}

func ledgerCardWire(card ledger.Card) proto.Card {
	attachments := make([]proto.Attachment, 0, len(card.Attachments))
	for _, attachment := range card.Attachments {
		attachments = append(attachments, proto.Attachment{Kind: attachment.Kind, Path: attachment.Path})
	}
	return proto.Card{
		ID: card.ID, Title: card.Title, Status: card.Status, TerminateReason: card.TerminateReason,
		Priority: card.Priority, Project: card.Project, ParentID: card.ParentID,
		WorkflowName: card.WorkflowName, WorkflowVersion: card.WorkflowVersion, Attachments: attachments,
		AcceptanceCriteria: card.AcceptanceCriteria, BaseBranch: card.BaseBranch,
		DriverSession: card.DriverSession, DriverHeartbeatAt: card.DriverHeartbeatAt,
		CreatedAt: card.CreatedAt, UpdatedAt: card.UpdatedAt,
	}
}

func ledgerEventWire(event ledger.Event) proto.LedgerEvent {
	return proto.LedgerEvent{
		Seq: event.Seq, CardID: event.CardID, Type: event.Type, Actor: event.Actor,
		Payload: event.Payload, CreatedAt: event.CreatedAt,
	}
}

func ledgerNodeWire(node ledger.NodeDef) proto.NodeDef {
	return proto.NodeDef{
		Name: node.Name, Template: node.Template,
		Override: proto.NodeOverride{
			Executor: node.Override.Executor, Discipline: node.Override.Discipline,
			Target: node.Override.Target, Model: node.Override.Model,
		},
		Dispatch: node.Dispatch, Verdict: node.Verdict, CarryCardContext: node.CarryCardContext,
		MaxRounds: node.MaxRounds, Next: node.Next, OnFail: node.OnFail,
		Gate: proto.Gate{
			RequireAttachment:   node.Gate.RequireAttachment,
			RequireAcceptance:   node.Gate.RequireAcceptance,
			RequireChildrenDone: node.Gate.RequireChildrenDone,
		},
		HumanBases: node.HumanBases,
	}
}

func ledgerCardViewWire(view ledger.CardView, conflict bool, openTickets int) proto.CardView {
	attachments := make([]proto.Attachment, 0, len(view.Attachments))
	for _, attachment := range view.Attachments {
		attachments = append(attachments, proto.Attachment{Kind: attachment.Kind, Path: attachment.Path})
	}
	return proto.CardView{
		ID: view.ID, Title: view.Title, Status: view.Status, Priority: view.Priority,
		Project: view.Project, Workflow: view.WorkflowName, Parent: view.ParentID, BaseBranch: view.BaseBranch,
		Attachments: attachments, Following: view.Following, Blocked: view.Blocked,
		BlockedBy: view.BlockedBy, MergedCount: view.MergedCount, Needs: view.NeedsReason,
		OpenDecisions: view.OpenDecisions, ChildrenTotal: view.ChildrenTotal, ChildrenDone: view.ChildrenDone,
		Conflict: conflict, OpenTickets: openTickets,
	}
}

func ledgerDecisionWire(decision ledger.Decision) proto.Decision {
	return proto.Decision{
		ID: decision.ID, CardID: decision.CardID, Body: decision.Body, Options: decision.Options,
		Status: decision.Status, CreatedBy: decision.CreatedBy, Answer: decision.Answer,
		AnsweredBy: decision.AnsweredBy, CreatedAt: decision.CreatedAt, AnsweredAt: decision.AnsweredAt,
	}
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
	out := make([]proto.CardView, 0, len(views))
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
		out = append(out, ledgerCardViewWire(view, conflict, tickets[view.ID]))
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
		// 走 target 客户端池：relay 形态的机器没有 addr，直连构造对它们恒失败
		// （见 internal/targetclient 与 nodirectclient_test）。取不到客户端与
		// 拿不到任务列表同归「未知」——本区块的语义就是「这台机器对不上账」。
		cl, err := s.pool.For(name)
		if err != nil {
			s.log.Warn("取 target 客户端失败，该机器计入未对账", "target", name, "cause", err)
			unknown = append(unknown, name)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		tasks, err := cl.ListTasks(ctx)
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
	// 裁决随详情一起给：抽屉是「卡的一切信息只在一处看」的那一处，少了它
	// 挂卡的请示在界面上只剩 timeline 里一行原文，看不到候选项也没法答复
	decisions, _ := s.ledger.DecisionsOf(id)
	// 等人原因也随详情给：看板卡片上有「需要你」角标，点进抽屉却看不到
	// 为什么，等于把「卡的一切只在抽屉一处看」拆成了两处
	needs, _ := s.ledger.NeedsOf(id)
	// 子任务随详情给：抽屉是「卡的一切只在一处看」的那一处，为一个只读列表
	// 单开端点会让抽屉多打一次网络往返，还得自己处理它的 loading 与失败态
	children, err := s.ledger.ChildrenOf(id)
	if err != nil {
		// 与 relations/decisions 同款降级：主查询已经决定了 200，
		// 附加信息拿不到时给空列表，不能让整个抽屉打不开
		s.log.Warn("读子卡失败，详情降级为无子任务", "card", id, "cause", err)
		children = nil
	}
	relationWire := make([]proto.Relation, 0, len(relations))
	for _, relation := range relations {
		relationWire = append(relationWire, proto.Relation{From: relation.From, To: relation.To, Type: relation.Type, CreatedAt: relation.CreatedAt})
	}
	taskStateWire := make([]proto.TaskStateRow, 0, len(taskStates))
	for _, state := range taskStates {
		taskStateWire = append(taskStateWire, proto.TaskStateRow{Target: state.Target, TaskID: state.TaskID, Purpose: state.Purpose, LastType: state.LastType, LastSeq: state.LastSeq})
	}
	decisionWire := make([]proto.Decision, 0, len(decisions))
	for _, decision := range decisions {
		decisionWire = append(decisionWire, ledgerDecisionWire(decision))
	}
	childrenWire := make([]proto.CardBrief, 0, len(children))
	for _, child := range children {
		childrenWire = append(childrenWire, proto.CardBrief{ID: child.ID, Title: child.Title, Status: child.Status})
	}
	eventWire := make([]proto.LedgerEvent, 0, len(events))
	for _, event := range events {
		eventWire = append(eventWire, ledgerEventWire(event))
	}
	writeJSON(w, http.StatusOK, proto.CardDetail{
		Card: ledgerCardWire(card), Relations: relationWire, Events: eventWire,
		TaskStates: taskStateWire, EffectiveBaseBranch: base,
		Decisions: decisionWire, Needs: needs, Children: childrenWire,
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

// handleCardStep 发起一个卡节点，受理即 202。
//
// 为什么是 202 而不是 200：环节要跑几分钟到几十分钟，200 会让前端以为
// 它已经做完了。202 的语义正是「收到了，正在做」，界面据此把按钮置灰并
// 提示「进展见下方 Timeline」。
//
// 为什么不支持 implement：实现派发通常要挂 plan 文件，浏览器里没有那个文件。
// 它留在 CLI；其余节点名透传给卡钉工作流，由 StepRunner 做合法性判断。
func (s *Server) handleCardStep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Step string `json:"step"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	if req.Step == discipline.NameImplement {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "implement 节点只能通过 CLI 派发（浏览器没有 plan 文件）",
		})
		return
	}
	actor := "web:" + r.RemoteAddr
	err := s.startCardStep(id, req.Step, actor)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	case errors.Is(err, errStepInFlight):
		s.log.Warn("节点被拒：已有在飞", "card", id, "node", req.Step, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ledger.ErrNotFound):
		ledgerErr(w, err)
	default:
		// 其余都是前置校验失败（节点名不在卡钉工作流、卡不存在）：这些是调用方能改的，
		// 400 比 500 更准确，且错误原文里已经写了该怎么办
		s.log.Warn("节点被拒", "card", id, "node", req.Step, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}

// handleCardAccept 记一条「已真机验」验收，body 只收证据。
//
// 为什么不收 verified 字段：标记未验是补记动作而不是日常，留 CLI 的
// `card accept --unverified`。界面上只提供「标记已验」这一个方向，语义更窄也更难误点。
//
// 为什么空证据必须在这里拦：RecordAcceptance 自己不校验，「已验必须带证据」
// 这条规则今天只由 CLI 守着。只靠前端不让空提交的话，curl 一下就能落一条
// 没有证据的「已验」——而验收记录正是事后唯一能复查的东西。
func (s *Server) handleCardAccept(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Evidence string `json:"evidence"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	id := r.PathValue("id")
	evidence := strings.TrimSpace(req.Evidence)
	if evidence == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "标记已验必须带证据（与 CLI card accept 同规则）",
		})
		return
	}
	actor := "web:" + r.RemoteAddr
	if err := s.ledger.RecordAcceptance(id, true, evidence, actor); err != nil {
		s.log.Error("记验收失败", "card", id, "actor", actor, "cause", err)
		ledgerErr(w, err)
		return
	}
	s.log.Info("已记验收", "card", id, "actor", actor, "evidence_bytes", len(evidence))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleCardNeedsClear 人工撤回卡上的「需要你 · 等人」标记。
//
// why 要有这个入口：撤回此前只有 CLI 一条路（card needs --clear）。红旗挂在
// 抽屉上、撤它却要回命令行，等于把一张卡的处置拆成了两处，而抽屉本该是
// 「卡的一切只在一处看」的那一处（2026-08-20 真机看到）。
//
// 这里是无条件清除，与环节侧的 ClearNeedsHumanFrom（只撤自己打的那条）
// 有意不同：撤回权属于打标记的一方，而人对任何来源的标记都有处置权。
func (s *Server) handleCardNeedsClear(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := "web:" + r.RemoteAddr
	if err := s.ledger.ClearNeedsHuman(id, actor); err != nil {
		s.log.Error("清等人标记失败", "card", id, "actor", actor, "cause", err)
		ledgerErr(w, err)
		return
	}
	s.log.Info("已清等人标记", "card", id, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

// handleFlowGet 取单条工作流的最新版本（含节点定义）。
//
// 老 def（只有 states）读出时会被补出等价的纯人工节点序列，所以前端永远
// 只需要看 nodes 一个字段。
func (s *Server) handleFlowGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workflow, err := s.ledger.GetWorkflow(name, 0)
	if err != nil {
		s.log.Warn("取工作流失败", "name", name, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("读出工作流", "name", name, "version", workflow.Version, "nodes", len(workflow.Def.Nodes))
	nodes := make([]proto.NodeDef, 0, len(workflow.Def.Nodes))
	for _, node := range workflow.Def.Nodes {
		nodes = append(nodes, ledgerNodeWire(node))
	}
	writeJSON(w, http.StatusOK, proto.FlowDetail{
		Name: workflow.Name, Version: workflow.Version, Nodes: nodes, States: workflow.Def.States,
	})
}

// handleFlowPut 发布该工作流的**下一个版本**。
//
// 注意：这不是「改」——工作流不可变版本化，每次保存都是插一个新版本，
// 已经钉在老版本上的卡完全不受影响。想让老卡用新流程要显式迁移
// （MigrateCardWorkflow），那是另一个动作。
func (s *Server) handleFlowPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Nodes []ledger.NodeDef `json:"nodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Errorf("解析请求体: %w", err).Error()})
		return
	}
	if len(body.Nodes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nodes 不能为空"})
		return
	}
	version, err := s.ledger.PutWorkflow(name, ledger.WorkflowDef{Nodes: body.Nodes})
	if err != nil {
		// 节点校验不过是用户输入问题（400），不是服务器故障（500）。
		if errors.Is(err, ledger.ErrBadState) {
			s.log.Warn("工作流节点校验未过", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.log.Error("写工作流失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("已发布工作流新版本", "name", name, "version", version, "nodes", len(body.Nodes))
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "version": version})
}

// handleDisciplineNames 列出可选的纪律块名：内置角色 + DataDir 下的自定义文件。
//
// 给节点配置的下拉用。返回去重升序的名字列表，不带正文——正文有专门的
// 纪律块读写接口，列表接口没必要驮着几十 KB。
func (s *Server) handleDisciplineNames(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	names := []string{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, name := range []string{
		discipline.NameImplement, discipline.NameReview,
		discipline.NameSpecDraft, discipline.NamePlanWriting, discipline.NameFinishing,
	} {
		add(name)
	}
	files, err := discipline.List(discipline.Dir(s.conf().DataDir))
	if err != nil {
		// 目录读不了不该让整个下拉空掉——内置的那几个仍然可用，如实告警即可。
		s.log.Warn("列自定义纪律块失败，只返回内置", "cause", err)
	}
	for _, file := range files {
		add(strings.TrimSuffix(file.Name, ".md"))
	}
	sort.Strings(names)
	s.log.Info("列出纪律块名", "count", len(names), "custom", len(files))
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
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

// handleLedgerHealth 账本健康探针：恒 200。未启用时只回 {"enabled":false}，
// 启用时附镜像水位。前端据此决定要不要渲染账本入口。
func (s *Server) handleLedgerHealth(w http.ResponseWriter, r *http.Request) {
	if s.ledger == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	rows, err := s.ledger.MirrorHealth()
	if err != nil {
		ledgerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "mirror": rows})
}

// attachmentKinds 是允许的附件类型。收窄成白名单不是洁癖：附件 kind 是
// 「进入某一列的门槛」的判据（Gate.RequireAttachment），拼错一个字母会让门
// 永远过不去，而界面上看着附件明明挂着——那种问题极难自查。新增
// Gate.RequireAttachment 取值时必须同步登记，家族回归测试会拦住遗漏。
var attachmentKinds = map[string]bool{"spec": true, "plan": true, "doc": true, "contract": true}

// handleCardCreate 建卡。
//
// 请求体：title（必填）、project（必填）、workflow（可省略）、priority、parent、
// base_branch。响应：{"id": "<新卡号>"}。
//
// 注意：**base_branch 只在这里能设**，建完不可改（改基线会让已经派出去的
// 任务与卡的说法对不上）。子卡不传 base_branch 时自动继承父卡的有效基线。
func (s *Server) handleCardCreate(w http.ResponseWriter, r *http.Request) {
	var body proto.NewCardReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("标题不能为空"))
		return
	}
	if body.Project == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("project 不能为空"))
		return
	}
	actor := s.ledgerActor(r)
	card, err := s.ledger.CreateCard(ledger.NewCard{
		Title: strings.TrimSpace(body.Title), Project: body.Project,
		Priority: body.Priority, Parent: body.Parent,
		Workflow: body.Workflow, BaseBranch: body.BaseBranch, Actor: actor,
	})
	if err != nil {
		s.log.Warn("建卡失败", "title", body.Title, "workflow", body.Workflow, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已建卡", "card", card.ID, "title", card.Title,
		"workflow", card.WorkflowName, "version", card.WorkflowVersion,
		"base_branch", card.BaseBranch, "actor", actor)
	writeJSON(w, http.StatusOK, proto.CardCreateResp{ID: card.ID})
}

// handleCardMigrate 是跨流迁移的控制面骨架：只解码显式目标、调用账本并翻译错误。
// 在飞判定属于账本迁移事务，handler 不读取 agentd 的 cardStepFlight map。
func (s *Server) handleCardMigrate(w http.ResponseWriter, r *http.Request) {
	var body proto.MigrateCardReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	body.Workflow = strings.TrimSpace(body.Workflow)
	body.Status = strings.TrimSpace(body.Status)
	if body.Workflow == "" || body.Status == "" || body.Version < 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("workflow、status 必填，version 不能为负数"))
		return
	}
	if err := s.ledger.MigrateCardWorkflow(r.PathValue("id"), body.Workflow, body.Version, body.Status, s.ledgerActor(r)); err != nil {
		ledgerErr(w, err)
		return
	}
	// TODO(B167-A): 账本迁移结果投影为完整 from/to/event；Ticket 0 只冻结响应形状。
	writeJSON(w, http.StatusOK, proto.MigrateCardResp{OK: true, ID: r.PathValue("id")})
}

// handleCardPatch 改卡的标题 / 优先级 / 验收判据。
//
// **缺字段 = 不动该字段，不是置空。** 三个字段都用 *string 收，靠指针区分
// 「没给」与「给了空串」——用值类型收会让「只改优先级」把标题和判据一起清掉。
func (s *Server) handleCardPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title              *string `json:"title"`
		Priority           *string `json:"priority"`
		AcceptanceCriteria *string `json:"acceptance_criteria"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	card, err := s.ledger.GetCard(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	actor := s.ledgerActor(r)
	if body.Title != nil || body.Priority != nil {
		// UpdateCardMeta 收的是最终值而不是「改动」，所以没给的那一半要用现值补齐。
		title, priority := card.Title, card.Priority
		if body.Title != nil {
			if strings.TrimSpace(*body.Title) == "" {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("标题不能改成空"))
				return
			}
			title = strings.TrimSpace(*body.Title)
		}
		if body.Priority != nil {
			priority = *body.Priority
		}
		if err := s.ledger.UpdateCardMeta(id, title, priority, actor); err != nil {
			s.log.Warn("改卡元信息失败", "card", id, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Info("已改卡元信息", "card", id, "title", title, "priority", priority, "actor", actor)
	}
	if body.AcceptanceCriteria != nil {
		if err := s.ledger.SetAcceptance(id, *body.AcceptanceCriteria, actor); err != nil {
			s.log.Warn("写验收判据失败", "card", id, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Info("已写验收判据", "card", id, "bytes", len(*body.AcceptanceCriteria), "actor", actor)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCardAttach 给卡挂一个附件（同 path 幂等）。kind 只认 spec|plan|doc|contract。
func (s *Server) handleCardAttach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if !attachmentKinds[body.Kind] {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件 kind 只认 spec|plan|doc|contract，收到 %q", body.Kind))
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件路径不能为空"))
		return
	}
	actor := s.ledgerActor(r)
	if err := s.ledger.AttachFile(id, body.Kind, strings.TrimSpace(body.Path), actor); err != nil {
		s.log.Warn("挂附件失败", "card", id, "kind", body.Kind, "path", body.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已挂附件", "card", id, "kind", body.Kind, "path", body.Path, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCardDetach 摘掉卡上指定路径的附件（不存在也返回 ok，摘除天然幂等）。
func (s *Server) handleCardDetach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if body.Path == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("附件路径不能为空"))
		return
	}
	actor := s.ledgerActor(r)
	if err := s.ledger.DetachFile(id, body.Path, actor); err != nil {
		s.log.Warn("摘附件失败", "card", id, "path", body.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已摘附件", "card", id, "path", body.Path, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
