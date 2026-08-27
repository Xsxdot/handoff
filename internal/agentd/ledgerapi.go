// 账本 HTTP API：web 看板的唯一账本通道。薄层——业务全在
// internal/ledger，此处只做解码/调用/编码与错误翻译。写动作：
// move/note/answer/accept 同步返回；step（工作流节点）异步 202，
// 编排在 internal/ledgerstep。step 不区分节点名——包括 implement 在内的所有节点都走
// 这条通道；被拒的只有「要求内联调用方本地文件」的请求，而 CardStepReq 里没有这类
// 字段（见 requiresInlineLocalFile）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
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
	// 详情 GET 的账本到 proto 投影必须保留 Purpose；同时显式投影指针，
	// 保留旧节点字段缺失与新节点显式对象之间的区别。
	var produces *proto.NodeOutput
	if node.Produces != nil {
		produces = &proto.NodeOutput{
			Kind: node.Produces.Kind,
			Path: node.Produces.Path,
		}
	}
	return proto.NodeDef{
		Name: node.Name, Template: node.Template,
		Override: proto.NodeOverride{
			Executor: node.Override.Executor, Discipline: node.Override.Discipline,
			Target: node.Override.Target, Model: node.Override.Model,
			Purpose: node.Override.Purpose,
		},
		Dispatch: node.Dispatch, Verdict: node.Verdict, CarryCardContext: node.CarryCardContext,
		MaxRounds: node.MaxRounds, OmitAcceptance: node.OmitAcceptance, Next: node.Next, OnFail: node.OnFail,
		Gate: proto.Gate{
			RequireAttachment:    node.Gate.RequireAttachment,
			RequireAttachmentAny: node.Gate.RequireAttachmentAny,
			RequireAcceptance:    node.Gate.RequireAcceptance,
			RequireChildrenDone:  node.Gate.RequireChildrenDone,
		},
		HumanBases: node.HumanBases,
		Produces:   produces,
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
		BaseFrozen: view.BaseFrozen, BlockedBy: view.BlockedBy, MergedCount: view.MergedCount, Needs: view.NeedsReason,
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
	runLocks, lockErr := s.ledger.AllRunLocks()
	if lockErr != nil {
		s.log.Warn("运行锁批量读取失败（冲突徽标退化为不显示，不阻塞列表）", "err", lockErr)
		runLocks = nil
	}
	activeLock := make(map[string]struct{}, len(runLocks))
	now := time.Now()
	for _, lock := range runLocks {
		if lock.ExpiresAt.After(now) {
			activeLock[lock.CardID] = struct{}{}
		}
	}
	out := make([]proto.CardView, 0, len(views))
	for _, view := range views {
		conflict := false
		if _, locked := activeLock[view.ID]; locked {
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

// unlinkedSummary 「未挂账」摘要：登记 target 上存在、card_tasks 里没有、
// **且还没走到终态**的 task。拨号失败进入 unknown_targets，而不是假装为零。
//
// 为什么排除终态（见 unlinkedRowsFor）：这个摘要的用途是「账目对不上，需要
// 补挂卡」的兜底提醒——已 completed/failed 的历史任务补挂已无意义，把它们
// 算进来只会让角标常年停在三位数（实测本机 164 条全是终态），提醒退化成噪声。
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
		rows = append(rows, unlinkedRowsFor(name, tasks, linked)...)
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

// unlinkedRowsFor 从一台 target 的任务列表里挑出该计入「未挂账」的行。
//
// 参数：target 为本机配置里这台机器的键名；tasks 为它当前的任务列表；
// linked 为 "target/task_id" → true 的挂账索引（由 ledger.AllTaskLinks 建立）。
// 返回：可直接进 JSON 的行，顺序与 tasks 一致（排序由调用方统一做）。
//
// 两条排除规则，缺一不可：
//   - 已挂账的跳过——它们在工作项看板里有卡认领，不属于「对不上账」
//   - 终态（completed / failed）的跳过——终态任务不再有 executor 持有工作区，
//     事后补挂卡改变不了任何事实；它们是历史残留而非待办
func unlinkedRowsFor(target string, tasks []proto.TaskView, linked map[string]bool) []map[string]any {
	rows := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		if linked[target+"/"+task.ID] {
			continue
		}
		if task.State.IsTerminal() {
			continue
		}
		rows = append(rows, map[string]any{
			"target": target, "task_id": task.ID, "title": task.Name, "state": task.State,
		})
	}
	return rows
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

// maxCardStepBody 是 step 请求体的大小上限。请求只有六个短字段，--extra 是其中唯一
// 可能长的一项（一段中文补充说明）；1 MiB 给它留了三个数量级的余量，同时挡住把
// 整个进程内存喂进来的畸形请求。
//
// 它是**上限**不是**截断点**：超过就整体拒绝。只截断的话，一段恰好等于上限的合法
// JSON 后面接任何尾随内容，都会被截断切干净、当成合法请求受理——那道「拒尾随内容」
// 的防线就在边界上破了。判定手法沿用 envfile.Parse：多读 1 字节再比长度。
const maxCardStepBody = 1 << 20

// handleCardStep 受理一个卡节点，受理即 202；202 不代表回合已完成。
//
// 规范 CLI 请求必须带非空 actor；旧看板只发送 {"step":...}，仅在原始 JSON
// 缺少 actor 键时补 web:<r.RemoteAddr>。显式 actor:"" 不能借 fallback 进入驱动锁。
// JSON 对**未知字段**宽松（忽略，避免版本错配把新可选字段变成 400），但对**尾随
// 内容**严格：整段读进来用 json.Unmarshal 解，多出的字节一律 400。Decoder.Decode
// 只吃第一个 JSON 值，被截断又重发的请求体会被它当成合法请求受理——而受理是有
// 副作用的（认领驱动、派任务），不能建立在只看了前半句上。
//
// 受理前只做四件事：解出规范请求、校验 step/actor 非空、拒掉要求内联本地文件的
// 请求、确认卡与节点都解得开（卡不存在 404，节点名不对 400）。其余一切——门是否
// 放行、目标机是否够得着、回合是否超轮——都在后台 goroutine 里由 StepRunner 判定，
// 结果落卡的事件流。
func (s *Server) handleCardStep(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 多读 1 字节用于判定「是否超限」：正好等于上限时 LimitReader 读满但未越界。
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxCardStepBody+1))
	if err != nil {
		s.log.Warn("卡节点请求读取失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(payload) > maxCardStepBody {
		s.log.Warn("卡节点请求被拒：请求体超限", "card", id, "limit", maxCardStepBody)
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": fmt.Sprintf("card step 请求体超过 %d 字节", maxCardStepBody),
		})
		return
	}
	// 两次解同一段字节：raw 只用来区分「actor 键缺席」与「显式空串」，req 是规范请求。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		s.log.Warn("卡节点请求解码失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var req proto.CardStepReq
	if err := json.Unmarshal(payload, &req); err != nil {
		s.log.Warn("卡节点请求字段解码失败", "card", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if _, ok := raw["actor"]; !ok {
		req.Actor = "web:" + hostOnly(r.RemoteAddr)
		s.log.Info("legacy 卡节点请求补 actor", "card", id, "node", req.Step,
			"actor", req.Actor, "remote_addr", r.RemoteAddr)
	}
	if req.Step == "" {
		s.log.Warn("卡节点请求被拒：step 为空", "card", id)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "step 不能为空"})
		return
	}
	if req.Actor == "" {
		s.log.Warn("卡节点请求被拒：actor 为空", "card", id, "node", req.Step)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "actor 不能为空"})
		return
	}
	if requiresInlineLocalFile(req) {
		s.log.Warn("卡节点请求被拒：要求内联本地文件", "card", id, "node", req.Step,
			"actor", req.Actor)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "card step 不接受调用方本地文件",
		})
		return
	}
	if _, err := ledgerstep.ResolveNode(s.ledger, id, req.Step); err != nil {
		s.log.Warn("卡节点请求被拒：卡或节点解不开", "card", id, "node", req.Step,
			"actor", req.Actor, "cause", err)
		if errors.Is(err, ledger.ErrNotFound) {
			ledgerErr(w, err)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("开始装配卡节点", "card", id, "node", req.Step, "actor", req.Actor,
		"target", req.Target, "executor", req.Executor, "model", req.Model,
		"has_extra", strings.TrimSpace(req.Extra) != "")
	err = s.startCardStep(id, req)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	case errors.Is(err, errStepInFlight):
		s.log.Warn("节点被拒：已有在飞", "card", id, "node", req.Step,
			"actor", req.Actor, "cause", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ledger.ErrNotFound):
		s.log.Warn("卡节点所属卡不存在", "card", id, "node", req.Step, "cause", err)
		ledgerErr(w, err)
	default:
		s.log.Warn("节点被拒", "card", id, "node", req.Step, "actor", req.Actor, "cause", err)
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

// handleDisciplineNames 列出可选的纪律块名：账本 disciplines 聚合的全部名字。
//
// 给节点配置的下拉用。返回去重升序的名字列表，不带正文——正文有专门的
// 读写接口，列表接口没必要驮着几十 KB。
//
// B229 起权威副本在账本：内置角色清单与 <DataDir>/discipline 磁盘文件都已退役，
// 下拉只反映账本现状（ListDisciplineNames）。账本读不了就如实 500——
// 它是纪律块的必需品，静默空清单会让用户以为没有可选项。
func (s *Server) handleDisciplineNames(w http.ResponseWriter, r *http.Request) {
	names, err := s.ledger.ListDisciplineNames()
	if err != nil {
		s.log.Error("列纪律块名失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("列出纪律块名", "count", len(names))
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
// 「进入某一列的门槛」的判据（Gate.RequireAttachment/RequireAttachmentAny），拼错一个字母会让门
// 永远过不去，而界面上看着附件明明挂着——那种问题极难自查。新增
// Gate.RequireAttachment 或 Gate.RequireAttachmentAny 取值时必须同步登记，家族回归测试会拦住遗漏。
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
	migration, err := s.ledger.MigrateCardWorkflow(r.PathValue("id"), body.Workflow, body.Version, body.Status, s.ledgerActor(r))
	if err != nil {
		// handler 不做在飞检查，只翻 Store 的错误码；否则 HTTP 与 CLI 会再次分裂
		// （契约拍板记录④）。ErrStepInFlight 等 409 原因原样留在响应中。
		s.log.Warn("迁移请求失败", "card", r.PathValue("id"), "cause", err)
		ledgerErr(w, err)
		return
	}
	toWireLocation := func(location ledger.WorkflowLocation) proto.CardWorkflowLocation {
		return proto.CardWorkflowLocation{
			ID: migration.CardID, Workflow: location.Workflow,
			WorkflowVersion: location.Version, Status: location.Status,
		}
	}
	response := proto.MigrateCardResp{
		OK: true, ID: migration.CardID,
		From: toWireLocation(migration.From), To: toWireLocation(migration.To),
		Event: proto.LedgerEvent{
			Seq: migration.Event.Seq, CardID: migration.Event.CardID,
			Type: migration.Event.Type, Actor: migration.Event.Actor,
			Payload: migration.Event.Payload, CreatedAt: migration.Event.CreatedAt,
		},
	}
	s.log.Info("迁移响应已投影", "card", migration.CardID,
		"from_workflow", migration.From.Workflow, "from_version", migration.From.Version,
		"from_status", migration.From.Status, "to_workflow", migration.To.Workflow,
		"to_version", migration.To.Version, "to_status", migration.To.Status)
	writeJSON(w, http.StatusOK, response)
}

// handleCardPatch 改卡的标题 / 优先级 / 验收判据 / 显式基线。
//
// **缺字段 = 不动该字段，不是置空。** 四个字段都用 *string 收，靠指针区分
// 「没给」与「给了空串」——用值类型收会让「只改优先级」把标题和判据一起清掉。
func (s *Server) handleCardPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Title              *string `json:"title"`
		Priority           *string `json:"priority"`
		AcceptanceCriteria *string `json:"acceptance_criteria"`
		BaseBranch         *string `json:"base_branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.log.Warn("卡 patch 请求体解析失败", "card", id, "cause", err)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	s.log.Info("卡 patch 请求", "card", id, "has_title", body.Title != nil,
		"has_priority", body.Priority != nil, "has_acceptance", body.AcceptanceCriteria != nil,
		"has_base_branch", body.BaseBranch != nil)
	card, err := s.ledger.GetCard(id)
	if err != nil {
		s.log.Warn("卡 patch 读取卡失败", "card", id, "cause", err)
		ledgerErr(w, err)
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
	if body.BaseBranch != nil {
		if err := s.ledger.SetCardBaseBranch(id, *body.BaseBranch, actor); err != nil {
			s.log.Warn("写卡基线失败", "card", id, "branch", *body.BaseBranch, "cause", err)
			ledgerErr(w, err)
			return
		}
		s.log.Info("已写卡基线", "card", id, "branch", *body.BaseBranch, "actor", actor)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCardAttach 给卡挂一个附件（同 kind、path 二元组幂等）。kind 只认 spec|plan|doc|contract。
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
	if _, err := s.ledger.AttachFile(id, body.Kind, strings.TrimSpace(body.Path), actor); err != nil {
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
	if _, err := s.ledger.DetachFile(id, body.Path, actor); err != nil {
		s.log.Warn("摘附件失败", "card", id, "path", body.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("已摘附件", "card", id, "path", body.Path, "actor", actor)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
