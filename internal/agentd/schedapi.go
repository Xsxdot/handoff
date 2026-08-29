// schedapi.go —— 编制域 HTTP 面：squads/queue 端点（B156.3 K3，契约 §11 移交区）。
//
// 职责：解码 → 调编制域入站门面 → 编码与错误翻译。薄层——载体与小队的规则全在
// internal/scheduling，这里零业务判断；wire DTO 在 internal/proto，与
// web/src/api/scheduling.ts 镜像一字不差。
//
// 边界：
//   - 路由注册进 Handler() 的 api mux（本文件自己的注册点 registerSchedulingRoutes，
//     不碰 SetupAutomation），自动过 s.auth 鉴权与 hostGuard（server.go auth 包整个 mux）；
//   - 未装配编制域（SetupAutomation 未执行）时全部端点 503，同 withLedger 形态；
//   - 不做跨机转发：登记与队列都在协调机侧账本，转发语义无定义。
//
// 400/500 分类策略（B156.3.3 修复轮 Major-2 收敛后，两层）：
//  1. wire 线格式检查——body 解码、?expect= 解析、body.name 与路径 {name} 一致性。
//     只拦截「线格式就不合法」的请求，直接 400。不做任何词表/必填判断；
//  2. 域校验——PutCarrier/PutSquad 的实体规则（必填、凭据/角色词表、成员存在性）
//     全部在编制域内，校验失败以 %w 包 ErrInvalid（成员引用缺失包 ErrNotFound）
//     上浮；schedPutErr 用 errors.Is 分流：ErrInvalid → 400（用户填错）、
//     ErrNotFound → 400（成员引用缺失）、ErrCASConflict → 409、其余 → 500。
//     校验逻辑不在此层存在第二份（Major-2：词表检查点收敛到域单一入口）。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// registerSchedulingRoutes 注册编制域端点（Handler() 内调用一次）。
func (s *Server) registerSchedulingRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/squads", s.withScheduling(s.handleSquadsGet))
	api.HandleFunc("PUT /api/squads/carriers/{name}", s.withScheduling(s.handleCarrierPut))
	api.HandleFunc("PUT /api/squads/squads/{name}", s.withScheduling(s.handleSquadPut))
	api.HandleFunc("GET /api/queue", s.withScheduling(s.handleQueueGet))
	api.HandleFunc("POST /api/squads/carriers/{name}/detect", s.withScheduling(s.handleCarrierDetect))
	api.HandleFunc("GET /api/squads/carriers/{name}/run-command", s.withScheduling(s.handleCarrierRunCommand))
	s.registerHostProbeRoutes(api)
}

// withScheduling 与 withLedger 同形：编制域未装配时 503 并给出可行动原因。
func (s *Server) withScheduling(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.scheduling == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "编制域未装配（SetupAutomation 未执行）",
			})
			return
		}
		h(w, r)
	}
}

// handleSquadsGet GET /api/squads：载体+小队全量读面（各行带 registry 版本）。
// 未登记时返回空集而非 404——「什么都没配」是合法态（breakdown K3 门禁判据）。
func (s *Server) handleSquadsGet(w http.ResponseWriter, r *http.Request) {
	carrierRows, err := s.scheduling.CarrierRows()
	if err != nil {
		s.log.Error("列载体失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	squadRows, err := s.scheduling.SquadRows()
	if err != nil {
		s.log.Error("列小队失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := proto.SquadsResp{
		Carriers: make([]proto.CarrierView, 0, len(carrierRows)),
		Squads:   make([]proto.SquadView, 0, len(squadRows)),
	}
	for _, row := range carrierRows {
		resp.Carriers = append(resp.Carriers, carrierView(row.Carrier, row.Version))
	}
	for _, row := range squadRows {
		resp.Squads = append(resp.Squads, squadView(row.Squad, row.Version))
	}
	s.log.Info("已读取编制登记面", "carriers", len(resp.Carriers), "squads", len(resp.Squads))
	writeJSON(w, http.StatusOK, resp)
}

// handleCarrierPut PUT /api/squads/carriers/{name}?expect=N：以 CAS 写载体。
// expect 必填（0=新建），缺席一律 400——「没带就当 0」会把更新静默变新建。
func (s *Server) handleCarrierPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	expect, ok, err := expectVersion(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var in proto.CarrierInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if in.Name != "" && in.Name != name {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("请求体 name %q 与路径 %q 不一致", in.Name, name)})
		return
	}
	carrier := scheduling.Carrier{
		Name: name, Machine: in.Machine, CLI: in.CLI, HomeDir: in.HomeDir,
		Model: in.Model, Credential: scheduling.CredentialSource(in.Credential),
		MaxConcurrency: in.MaxConcurrency,
	}
	s.log.Info("登记载体", "name", name, "expect", expect,
		"machine", in.Machine, "cli", in.CLI)
	if err := s.scheduling.PutCarrier(carrier, expect); err != nil {
		s.schedPutErr(w, "载体", name, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.SquadPutResp{Name: name, Version: expect + 1})
}

// handleSquadPut PUT /api/squads/squads/{name}?expect=N：以 CAS 写小队。
// 规则校验（角色词表、成员存在性）权威在 PutSquad，这里只做形状翻译与
// 错误分类（ErrNotFound 臂=成员引用缺失 → 400，schedPutErr=其余）。
func (s *Server) handleSquadPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	expect, ok, err := expectVersion(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var in proto.SquadInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if in.Name != "" && in.Name != name {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("请求体 name %q 与路径 %q 不一致", in.Name, name)})
		return
	}
	squad := scheduling.Squad{
		Name: name, Role: scheduling.SquadRole(in.Role),
		Members: in.Members, MaxConcurrency: in.MaxConcurrency,
	}
	s.log.Info("登记小队", "name", name, "expect", expect,
		"role", in.Role, "members", len(in.Members))
	if err := s.scheduling.PutSquad(squad, expect); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			// 成员引用不存在是唯一会从 PutSquad 带出 NotFound 的路径：
			// PUT 引用缺失资源 = 客户端可修，400 而非 500（Major-1：409→400）。
			s.log.Warn("小队登记被拒（成员引用缺失）", "name", name, "cause", err)
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.schedPutErr(w, "小队", name, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.SquadPutResp{Name: name, Version: expect + 1})
}

// handleQueueGet GET /api/queue：两队列的只读快照，按清队顺序给全局位次。
// 只读不出队——PopReady 才删除头部。
func (s *Server) handleQueueGet(w http.ResponseWriter, r *http.Request) {
	rows, err := s.scheduling.QueueSnapshot()
	if err != nil {
		s.log.Error("读队列快照失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := proto.QueueResp{Queue: make([]proto.QueueEntry, 0, len(rows))}
	for _, row := range rows {
		resp.Queue = append(resp.Queue, queueEntryView(row))
	}
	s.log.Info("已读取点火队列", "entries", len(resp.Queue))
	writeJSON(w, http.StatusOK, resp)
}

// handleCarrierDetect POST /api/squads/carriers/{name}/detect：检测写状态。
// 协调机持有 registry，本端点不整段 forwardIfRequested。Ticket 0 空壳只调
// ApplyDetect（恒 ErrDetectUnwired → 503）；实现票在此编排本机/跨机 WakeHome。
func (s *Server) handleCarrierDetect(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := s.scheduling.Carrier(name); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Error("读载体失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("检测载体", "name", name)
	c, err := s.scheduling.ApplyDetect(name, scheduling.DetectEvidence{}, "")
	if err != nil {
		if errors.Is(err, scheduling.ErrDetectUnwired) {
			s.log.Warn("载体检测尚未接线", "name", name)
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		s.log.Error("载体检测失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.CarrierDetectResp{
		Name: c.Name, Status: string(c.Status), LastError: c.LastError,
	})
}

// handleCarrierRunCommand GET /api/squads/carriers/{name}/run-command：
// 服务端生成复制命令，客户端不得拼接。
func (s *Server) handleCarrierRunCommand(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	c, err := s.scheduling.Carrier(name)
	if err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Error("读载体失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cmd := scheduling.RunCommand(c)
	s.log.Info("生成载体运行命令", "name", name, "command_bytes", len(cmd))
	writeJSON(w, http.StatusOK, proto.CarrierRunCommandResp{Command: cmd})
}

// expectVersion 解析 ?expect= 版本参数。缺席/非整数/负数都是 400：版本语义
// 不设隐含默认，防止「没带就当 0」把更新静默变新建。
func expectVersion(r *http.Request) (int, bool, error) {
	raw := r.URL.Query().Get("expect")
	if raw == "" {
		return 0, false, errors.New("缺少 expect 版本参数（GET /api/squads 取各行 version；新建传 0）")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("expect 不是合法版本号: %q", raw)
	}
	return n, true, nil
}

// schedPutErr 把编制域登记错误翻译成状态码：400 校验未过 / 409 CAS 冲突 /
// 500 registry 故障。分级日志让「用户填错」与「存储坏了」在日志里第一眼可分。
//
// ErrCASConflict 判定用 schedclient 哨兵正主：它经组装点适配器 translateRegistryErr
// 翻译后随域内错误上浮（K2 已落地，本卡不改写）；ErrInvalid 由 PutCarrier/PutSquad
// 校验行的 %w 包装产生（B156.3.3 修复轮 Major-2：400 而非 409，与 doc 注释一致）。
func (s *Server) schedPutErr(w http.ResponseWriter, kind, name string, err error) {
	switch {
	case errors.Is(err, scheduling.ErrInvalid):
		s.log.Warn("编制域登记被拒", "kind", kind, "name", name, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, schedclient.ErrCASConflict):
		s.log.Warn("编制域登记版本冲突", "kind", kind, "name", name, "cause", err)
		writeErr(w, http.StatusConflict, err)
	default:
		s.log.Error("编制域登记失败", "kind", kind, "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
	}
}

// carrierView 把载体实体投影成 wire DTO。逐字段来源：八个业务字段来自实体，
// Version 来自 registry 行版本——没有派生字段，不存在恒空死字段。
func carrierView(c scheduling.Carrier, version int) proto.CarrierView {
	return proto.CarrierView{
		Name: c.Name, Machine: c.Machine, CLI: c.CLI, HomeDir: c.HomeDir,
		Model: c.Model, Credential: string(c.Credential),
		MaxConcurrency: c.MaxConcurrency, Healthy: c.Healthy,
		Status: string(c.Status), LastError: c.LastError, Version: version,
	}
}

// squadView 把小队实体投影成 wire DTO（来源同上）。
func squadView(q scheduling.Squad, version int) proto.SquadView {
	return proto.SquadView{
		Name: q.Name, Role: string(q.Role), Members: q.Members,
		MaxConcurrency: q.MaxConcurrency, Version: version,
	}
}

// queueEntryView 把队列快照行投影成 wire DTO。Kind/ID/Seq/Position 来自快照
// 元数据，其余九个字段逐一来自入队时刻的 IgnitionRequest 快照。
func queueEntryView(row scheduling.QueuedRequest) proto.QueueEntry {
	return proto.QueueEntry{
		Kind: row.Kind, ID: row.ID,
		Card: row.Req.Card, Node: row.Req.Node, Squad: row.Req.Squad,
		Target: row.Req.Target, Executor: row.Req.Executor, Model: row.Req.Model,
		Priority: row.Req.Priority, Ready: row.Req.Ready, Actor: row.Req.Actor,
		Seq: row.Seq, Position: row.Position,
	}
}
