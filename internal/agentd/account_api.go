// account_api.go —— 账号 HTTP 面（B453 S1/S2/S11，spec §8 F1–F5、F9）。
//
// 职责：解码 → 调账号域（AccountsClient）→ 投影成 proto.AccountView 与错误翻译；
// 立即探测在所在机交本机探测器执行，所在机非本机时把同一路径转发过去（带
// X-Handoff-Forwarded，一跳封顶）——读数只由所在机写（plan 拍板 Q3）。
//
// 边界：
//   - 路由挂在 Handler() 的 api mux，与 /api/squads 同一鉴权门；
//   - 账号规则（校验、合并写、状态推导）全在 internal/scheduling/accounts，这里
//     只做缺省值填充（区分键缺席与「给了 0」，F9）与形状翻译；
//   - 本段只有 GET/PUT 与 probe；DELETE（F3）与载体引用计算在段 2/3。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// AccountsClient 是 gateway 对账号域的使用方接口（架构法第九条，实现为
// *accounts.Service，组装点注入）。方法集 = 生产调用闭包，多一个即越界：
//   - List：GET /api/accounts、探测器周期扫描；
//   - Get：立即探测查所在机、探测器读账号；
//   - Put：PUT /api/accounts/{name}；
//   - RecordProbe：探测器写回读数行。
type AccountsClient interface {
	List() ([]accounts.View, error)
	Get(name string) (accounts.View, error)
	Put(a accounts.Account, expect int) (int, error)
	RecordProbe(name string, o accounts.ProbeOutcome) error
}

// 编译期断言：账号域实现满足 gateway 使用方接口。
var _ AccountsClient = (*accounts.Service)(nil)

// SetAccounts 注入账号域（cmd 组装点；nil = 未装配，账号端点 503）。
func (s *Server) SetAccounts(c AccountsClient) { s.accounts = c }

// SetAccountProber 注入本机探测器（只在挂共享账本的机器上装配）。
func (s *Server) SetAccountProber(p *AccountProber) { s.accountProber = p }

// registerAccountRoutes 注册账号端点（Handler() 内调用一次）。
func (s *Server) registerAccountRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/accounts", s.withAccounts(s.handleAccountsGet))
	api.HandleFunc("PUT /api/accounts/{name}", s.withAccounts(s.handleAccountPut))
	api.HandleFunc("POST /api/accounts/{name}/probe", s.withAccounts(s.handleAccountProbe))
}

func (s *Server) withAccounts(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.accounts == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "账号域未装配（本机账本未挂载）",
			})
			return
		}
		h(w, r)
	}
}

// handleAccountsGet GET /api/accounts（F1）：全部账号读面，空库 []。
func (s *Server) handleAccountsGet(w http.ResponseWriter, r *http.Request) {
	views, err := s.accounts.List()
	if err != nil {
		s.log.Error("列账号失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := proto.AccountsResp{Accounts: make([]proto.AccountView, 0, len(views))}
	for _, v := range views {
		resp.Accounts = append(resp.Accounts, accountView(v))
	}
	s.log.Info("已读取账号列表", "accounts", len(resp.Accounts))
	writeJSON(w, http.StatusOK, resp)
}

// handleAccountPut PUT /api/accounts/{name}?expect=N（F2）：expect 必填（0=新建）；
// 校验失败 400、版本冲突 409；成功回 AccountView（含新版本与当前读数）。
func (s *Server) handleAccountPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	expect, ok, err := expectVersion(r)
	if !ok {
		s.log.Warn("登记账号 expect 校验未过", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var in proto.AccountInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.log.Warn("登记账号请求体解析失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	a := accountFromInput(name, in)
	s.log.Info("登记账号", "name", name, "expect", expect, "kind", a.Kind, "machine", a.Machine,
		"entry", a.Entry, "proxy", proxycfg.Redact(a.Proxy), "interval_minutes", a.IntervalMinutes)
	if _, err := s.accounts.Put(a, expect); err != nil {
		switch {
		case errors.Is(err, accounts.ErrInvalid):
			writeErr(w, http.StatusBadRequest, err)
		case errors.Is(err, schedclient.ErrCASConflict):
			writeErr(w, http.StatusConflict, fmt.Errorf("账号 %s 已被修改或已存在（重新读取版本后再保存）: %w", name, err))
		default:
			s.log.Error("登记账号失败", "name", name, "expect", expect, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	v, err := s.accounts.Get(name)
	if err != nil {
		s.log.Error("登记账号后读回失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("登记账号成功", "name", name, "version", v.Version, "state", v.State)
	writeJSON(w, http.StatusOK, accountView(v))
}

// accountFromInput 填缺省值：键缺席取缺省（间隔 15、预警 60、低额度 80、条目按 kind）；
// 给了 0 原样交校验（F9：「给了 0」必须被识别，不能当缺省吞掉）。
func accountFromInput(name string, in proto.AccountInput) accounts.Account {
	a := accounts.Account{
		Name: name, Kind: strings.TrimSpace(in.Kind), Machine: strings.TrimSpace(in.Machine),
		HomeDir: strings.TrimSpace(in.HomeDir), Entry: strings.TrimSpace(in.Entry), Proxy: strings.TrimSpace(in.Proxy),
		IntervalMinutes: accounts.DefaultIntervalMinutes, WarnPercent: accounts.DefaultWarnPercent,
		LowPercent: accounts.DefaultLowPercent,
	}
	if a.Entry == "" {
		a.Entry = accounts.DefaultEntry(a.Kind)
	}
	if in.IntervalMinutes != nil {
		a.IntervalMinutes = *in.IntervalMinutes
	}
	if in.WarnPercent != nil {
		a.WarnPercent = *in.WarnPercent
	}
	if in.LowPercent != nil {
		a.LowPercent = *in.LowPercent
	}
	return a
}

// handleAccountProbe POST /api/accounts/{name}/probe（F4）：
//   - 所在机是本机：交本机探测器执行，200 AccountView（失败也写在 last_error 里）；
//     本机探测器未运行 503（不报成功空读数）；
//   - 所在机是他机：转发同一路径；转发失败 502「转发到 X 失败」，读数不变；
//   - 本请求已是转发来的却不是所在机：409（不再外扇，防环）。
func (s *Server) handleAccountProbe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	v, err := s.accounts.Get(name)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Error("立即探测：读账号失败", "name", name, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	machine := v.Account.Machine
	s.log.Info("立即探测开始", "name", name, "machine", machine, "forwarded", isForwarded(r))
	if s.IsSelfMachine(machine) {
		if s.accountProber == nil {
			err := fmt.Errorf("账号 %s 的所在机 %s 上账号探测器未运行（该机未挂共享账本？看 agentd 启动日志 dsn_configured）", name, machine)
			s.log.Warn("立即探测失败：本机探测器未运行", "name", name, "machine", machine)
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		nv, err := s.accountProber.ProbeNow(r.Context(), name)
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, ErrAccountProberStopped):
				code = http.StatusServiceUnavailable
			case errors.Is(err, accounts.ErrNotFound):
				code = http.StatusNotFound
			}
			s.log.Error("立即探测失败", "name", name, "machine", machine, "cause", err)
			writeErr(w, code, err)
			return
		}
		s.log.Info("立即探测完成", "name", name, "machine", machine, "state", nv.State)
		writeJSON(w, http.StatusOK, accountView(nv))
		return
	}
	if isForwarded(r) {
		err := fmt.Errorf("本机不是账号 %s 的所在机 %s，拒绝代探（转发只走一跳）", name, machine)
		s.log.Warn("立即探测被拒：转发到达非所在机", "name", name, "machine", machine)
		writeErr(w, http.StatusConflict, err)
		return
	}
	target, ok := s.remoteTarget(machine)
	if !ok {
		s.log.Warn("立即探测转发失败：所在机未登记", "name", name, "machine", machine)
		writeErr(w, http.StatusBadGateway, errors.New(unknownMachineMsg(machine)))
		return
	}
	client, err := s.pool.For(machine)
	if err != nil {
		s.log.Error("立即探测转发失败：取目标客户端", "name", name, "machine", machine, "cause", err)
		writeErr(w, http.StatusBadGateway, fmt.Errorf("转发到 %s 失败: %w", machine, err))
		return
	}
	status, headers, payload, err := s.forwardJSON(r, machine, client, target.Token, nil)
	if err != nil {
		s.log.Error("立即探测转发失败：所在机不可达", "name", name, "machine", machine, "cause", err)
		writeErr(w, http.StatusBadGateway, fmt.Errorf("转发到 %s 失败: %w", machine, err))
		return
	}
	s.log.Info("立即探测转发完成", "name", name, "machine", machine, "status", status)
	copyForwardHeaders(w, headers)
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		s.log.Warn("回送立即探测结果失败", "name", name, "machine", machine, "cause", err)
	}
}

// accountView 是账号域 View → wire 的唯一手写投影。last_error 取最近一次尝试失败；
// 最近成功读数明示不可用（如无订阅）时取其原因——两者都是「为什么不能用」。
func accountView(v accounts.View) proto.AccountView {
	a, rec := v.Account, v.Record
	out := proto.AccountView{
		Name: a.Name, Kind: a.Kind, Machine: a.Machine, HomeDir: a.HomeDir, Entry: a.Entry,
		Proxy: a.Proxy, IntervalMinutes: a.IntervalMinutes, WarnPercent: a.WarnPercent,
		LowPercent: a.LowPercent, Version: v.Version, State: string(v.State), Stale: v.Stale,
		LastAttemptAt: utcPtr(rec.LastAttemptAt), Carriers: []string{},
	}
	if r := rec.Reading; r != nil {
		rv := &proto.AccountReadingView{Windows: make([]proto.AccountWindowView, 0, len(r.Windows)),
			PlanType: r.PlanType, ProbedAt: r.ProbedAt.UTC()}
		for _, w := range r.Windows {
			rv.Windows = append(rv.Windows, proto.AccountWindowView{Key: w.Key, Group: w.Group,
				UsedPercent: w.UsedPercent, ResetsAt: utcPtr(w.ResetsAt)})
		}
		out.Reading = rv
	}
	switch {
	case rec.LastError != nil:
		out.LastError = &proto.AccountProblemView{Category: rec.LastError.Category, Message: rec.LastError.Message}
	case rec.Reading != nil && rec.Reading.Blocked != nil:
		out.LastError = &proto.AccountProblemView{Category: rec.Reading.Blocked.Category, Message: rec.Reading.Blocked.Message}
	}
	return out
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
