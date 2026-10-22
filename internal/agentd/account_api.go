// account_api.go —— 账号 HTTP 面（B453 S1/S2/S11；B464 段 1：账号 = 身份、登录 = 凭据落点，
// plan A1–A8）。
//
// 职责：解码 → 调账号域（AccountsClient）→ 投影成 proto.AccountView/LoginView/DiscoveryView
// 与错误翻译；立即探测在「可探登录」所在机交本机探测器执行，所在机非本机时把同一路径转发
// 过去（带 X-Handoff-Forwarded，一跳封顶）——读数只由可探登录所在机写（plan Q3/Q10）。
//
// 边界：
//   - 路由挂在 Handler() 的 api mux，与 /api/squads 同一鉴权门；
//   - 账号规则（归并、校验、合并写、状态推导）全在 internal/scheduling/accounts，这里只做
//     「缺席 = 保留现值」的填充（区分键缺席与「给了 0」）与形状翻译；
//   - 读面里登录代理的密码段打码（accounts.MaskProxy）；载体引用由账号域经注入的查询给出；
//   - 账号只由发现建：PUT expect=0 → 400（spec「永不做：手填账号登记」）；改名撞名 409；
//   - 登录代理（A9）：PUT /api/logins/{id}/proxy 服务端读-合并-CAS，不要求 expect；
//   - 不支持额度探测的账号（na：claude、第三方 key）立即探测 409。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd/accountwork"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// AccountsClient 是 gateway 对账号域的使用方接口（架构法第九条，实现为
// *accounts.Service，组装点注入）。方法集 = 生产调用闭包，多一个即越界：
//   - List：GET /api/accounts、/api/squads 投影载体所选登录的账号名与登录状态；
//   - Get：PUT/立即探测按 ref 读账号与可探登录、任务撞额度读窗口；
//   - Put：PUT /api/accounts/{ref}；
//   - Delete：DELETE /api/accounts/{ref}；
//   - MarkQuota：任务回合撞额度（quota_rerun.go）；
//   - Login：载体所选登录 → kind/代理/所属账号（检测链、派发快照、协调者代理，account_carrier.go）；
//   - DiscoveryRuns：GET /api/accounts 的「上次发现」；
//   - SetLoginProxy：PUT /api/logins/{id}/proxy（A9）。
type AccountsClient interface {
	List() ([]accounts.View, error)
	Get(ref string) (accounts.View, error)
	Put(a accounts.Account, expect int) (int, error)
	Delete(ref string, expect int) error
	// MarkQuota 给账号打撞额度标记（B453 S6/S7：任务回合撞额度，见 quota_rerun.go）。
	MarkQuota(id string, m accounts.QuotaMark) error
	Login(id string) (accounts.Login, error)
	DiscoveryRuns() ([]accounts.DiscoveryRun, error)
	SetLoginProxy(id, proxy string) (accounts.LoginView, error)
}

// 编译期断言：账号域实现满足 gateway 使用方接口。
var _ AccountsClient = (*accounts.Service)(nil)

// SetAccounts 注入账号域（cmd 组装点；nil = 未装配，账号端点 503）。
func (s *Server) SetAccounts(c AccountsClient) { s.accounts = c }

// SetAccountProber 注入本机探测器（只在挂共享账本的机器上装配）。
func (s *Server) SetAccountProber(p *accountwork.Prober) { s.accountProber = p }

// registerAccountRoutes 注册账号端点（Handler() 内调用一次）。{ref} = 账号 id 或名字（plan Q15）。
func (s *Server) registerAccountRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /api/accounts", s.withAccounts(s.handleAccountsGet))
	api.HandleFunc("PUT /api/accounts/{ref}", s.withAccounts(s.handleAccountPut))
	api.HandleFunc("DELETE /api/accounts/{ref}", s.withAccounts(s.handleAccountDelete))
	api.HandleFunc("POST /api/accounts/{ref}/probe", s.withAccounts(s.handleAccountProbe))
	api.HandleFunc("PUT /api/logins/{id}/proxy", s.withAccounts(s.handleLoginProxyPut))
	s.registerLoginRoutes(api)
}

// handleLoginProxyPut PUT /api/logins/{id}/proxy（A9）：体 {proxy}（空串 = 清除，键缺席 400）；
// 服务端读-合并-CAS，不要求 expect；打码回填保留原值；校验失败 400；登录不存在 404；成功
// 200 LoginView（代理打码、带引用载体）。登录行在共享账本，任一机都可写，不转发。
func (s *Server) handleLoginProxyPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in proto.LoginProxyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.log.Warn("设置登录代理请求体解析失败", "login", id, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if in.Proxy == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体缺 proxy（清除代理请传空串）"})
		return
	}
	lv, err := s.accounts.SetLoginProxy(id, strings.TrimSpace(*in.Proxy))
	if err != nil {
		switch {
		case errors.Is(err, accounts.ErrInvalid):
			writeErr(w, http.StatusBadRequest, err)
		case errors.Is(err, accounts.ErrNotFound):
			writeErr(w, http.StatusNotFound, err)
		default:
			s.log.Error("设置登录代理失败", "login", id, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	// 引用载体只在账号读面上（注入的引用查询）：回读所属账号取这条登录的完整读面。
	if v, err := s.accounts.Get(lv.Login.Account); err == nil {
		for _, own := range v.Logins {
			if own.Login.ID == id {
				lv = own
			}
		}
	} else {
		s.log.Warn("设置登录代理后读所属账号失败，响应不带引用载体", "login", id, "account", lv.Login.Account, "cause", err)
	}
	s.log.Info("设置登录代理成功", "login", id, "machine", lv.Login.Machine, "home", lv.Login.Home,
		"proxy", accounts.MaskProxy(lv.Login.Proxy))
	writeJSON(w, http.StatusOK, s.loginView(lv))
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

// handleAccountsGet GET /api/accounts（A1）：全部账号读面 + 各机「上次发现」，空库两者 []。
func (s *Server) handleAccountsGet(w http.ResponseWriter, r *http.Request) {
	views, err := s.accounts.List()
	if err != nil {
		s.log.Error("列账号失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	runs, err := s.accounts.DiscoveryRuns()
	if err != nil {
		s.log.Error("列发现结果失败", "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := proto.AccountsResp{Accounts: make([]proto.AccountView, 0, len(views)), Discovery: s.discoveryViews(runs)}
	for _, v := range views {
		resp.Accounts = append(resp.Accounts, s.accountView(v))
	}
	s.log.Info("已读取账号列表", "accounts", len(resp.Accounts), "machines", len(resp.Discovery))
	writeJSON(w, http.StatusOK, resp)
}

// discoveryViews 投影各机发现结果（A5）；本机即使还没跑过发现也出一行（at 缺席）。
func (s *Server) discoveryViews(runs []accounts.DiscoveryRun) []proto.DiscoveryView {
	out := make([]proto.DiscoveryView, 0, len(runs)+1)
	selfSeen := false
	for _, run := range runs {
		at := run.At.UTC()
		v := proto.DiscoveryView{Machine: run.Machine, Self: s.IsSelfMachine(run.Machine), At: &at}
		if len(run.Errors) > 0 {
			v.Error = strings.Join(run.Errors, "；")
		}
		selfSeen = selfSeen || v.Self
		out = append(out, v)
	}
	if self := s.conf().Name; !selfSeen && self != "" {
		out = append(out, proto.DiscoveryView{Machine: self, Self: true})
	}
	return out
}

// handleAccountPut PUT /api/accounts/{ref}?expect=N（A6）：expect 必填且 ≠ 0（账号由发现产生）；
// 键缺席 = 保留现值；校验失败 400、版本冲突 409；成功回 AccountView。
func (s *Server) handleAccountPut(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	expect, ok, err := expectVersion(r)
	if !ok {
		s.log.Warn("编辑账号 expect 校验未过", "ref", ref, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var in proto.AccountInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		s.log.Warn("编辑账号请求体解析失败", "ref", ref, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if expect == 0 {
		err := fmt.Errorf("账号由发现产生，不能手工登记（新建）；在要用的机器上登录对应 CLI，等发现挂上（%w）", accounts.ErrInvalid)
		s.log.Warn("编辑账号被拒：expect=0", "ref", ref)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.accounts.Get(ref)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Error("编辑账号：读现值失败", "ref", ref, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	a := accountFromInput(cur.Account, in)
	s.log.Info("编辑账号", "id", a.ID, "name", cur.Account.Name, "expect", expect, "interval_minutes", a.IntervalMinutes,
		"rename", in.Name != "" && in.Name != cur.Account.Name)
	if _, err := s.accounts.Put(a, expect); err != nil {
		switch {
		case errors.Is(err, accounts.ErrInvalid):
			writeErr(w, http.StatusBadRequest, err)
		case errors.Is(err, accounts.ErrNameTaken):
			writeErr(w, http.StatusConflict, err)
		case errors.Is(err, schedclient.ErrCASConflict):
			writeErr(w, http.StatusConflict, fmt.Errorf("账号 %s 已被修改（重新读取版本后再保存）: %w", cur.Account.Name, err))
		default:
			s.log.Error("编辑账号失败", "id", a.ID, "expect", expect, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	v, err := s.accounts.Get(a.ID)
	if err != nil {
		s.log.Error("编辑账号后读回失败", "id", a.ID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("编辑账号成功", "id", a.ID, "version", v.Version, "state", v.State)
	writeJSON(w, http.StatusOK, s.accountView(v))
}

// handleAccountDelete DELETE /api/accounts/{ref}?expect=N（A7）：名下登录被载体引用 409 且体含
// carriers；不存在 404；版本冲突 409；缺 expect 400。成功连带删读数行。
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	expect, ok, err := expectVersion(r)
	if !ok {
		s.log.Warn("删除账号 expect 校验未过", "ref", ref, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("删除账号", "ref", ref, "expect", expect)
	if err := s.accounts.Delete(ref, expect); err != nil {
		var refErr *accounts.ReferencedError
		switch {
		case errors.As(err, &refErr):
			writeJSON(w, http.StatusConflict, proto.AccountDeleteRefusal{Error: err.Error(), Carriers: refErr.Carriers})
		case errors.Is(err, accounts.ErrNotFound):
			writeErr(w, http.StatusNotFound, err)
		case errors.Is(err, schedclient.ErrCASConflict):
			writeErr(w, http.StatusConflict, fmt.Errorf("账号 %s 已被修改（重新读取版本后再删）: %w", ref, err))
		default:
			s.log.Error("删除账号失败", "ref", ref, "expect", expect, "cause", err)
			writeErr(w, http.StatusInternalServerError, err)
		}
		return
	}
	s.log.Info("删除账号成功", "ref", ref, "expect", expect)
	writeJSON(w, http.StatusOK, proto.SquadPutResp{Name: ref, Version: expect})
}

// accountFromInput 在现值上套用请求：键缺席 = 保留现值；给了 0 原样交校验（「给了 0」必须被
// 识别，不能当缺省吞掉）。发现字段一律取现值。
func accountFromInput(cur accounts.Account, in proto.AccountInput) accounts.Account {
	a := cur
	if n := strings.TrimSpace(in.Name); n != "" {
		a.Name = n
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

// handleAccountProbe POST /api/accounts/{ref}/probe（A8）：
//   - 不支持额度探测的账号（na）：409；
//   - 没有可探登录（名下无正常登录、或所在机发现已过期）：409；
//   - 可探登录在本机：交本机探测器执行，200 AccountView（失败也写在 last_error 里）；
//     本机探测器未运行 503（不报成功空读数）；
//   - 可探登录在他机：转发同一路径；转发失败 502「转发到 X 失败」，读数不变；
//   - 本请求已是转发来的却不在可探登录所在机：409（不再外扇，防环）。
func (s *Server) handleAccountProbe(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	v, err := s.accounts.Get(ref)
	if err != nil {
		if errors.Is(err, accounts.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		s.log.Error("立即探测：读账号失败", "ref", ref, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !accounts.SupportsProbe(v.Account.Kind) {
		err := fmt.Errorf("%w：账号 %s 是 %s，只管登录、不探额度", accountwork.ErrProbeUnsupported, v.Account.Name, v.Account.Kind)
		s.log.Warn("立即探测被拒：账号类型不支持额度探测", "id", v.Account.ID, "kind", v.Account.Kind)
		writeErr(w, http.StatusConflict, err)
		return
	}
	machine := ""
	for _, lv := range v.Logins {
		if lv.Login.ID == v.ProbeLogin {
			machine = lv.Login.Machine
		}
	}
	if machine == "" {
		err := fmt.Errorf("账号 %s 没有可用登录（名下没有状态正常、且所在机器最近 %s 内发现过的登录），无法探测额度",
			v.Account.Name, accounts.DiscoveryFreshFor)
		s.log.Warn("立即探测被拒：没有可探登录", "id", v.Account.ID, "name", v.Account.Name)
		writeErr(w, http.StatusConflict, err)
		return
	}
	name := v.Account.Name
	s.log.Info("立即探测开始", "id", v.Account.ID, "name", name, "login", v.ProbeLogin, "machine", machine,
		"forwarded", isForwarded(r))
	if s.IsSelfMachine(machine) {
		if s.accountProber == nil {
			err := fmt.Errorf("账号 %s 的可探登录所在机 %s 上账号探测器未运行（该机未挂共享账本？看 agentd 启动日志 dsn_configured）", name, machine)
			s.log.Warn("立即探测失败：本机探测器未运行", "name", name, "machine", machine)
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		nv, err := s.accountProber.ProbeNow(r.Context(), v.Account.ID)
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, accountwork.ErrStopped):
				code = http.StatusServiceUnavailable
			case errors.Is(err, accounts.ErrNotFound):
				code = http.StatusNotFound
			case errors.Is(err, accountwork.ErrNoProbeLogin), errors.Is(err, accountwork.ErrNotLocal),
				errors.Is(err, accountwork.ErrProbeUnsupported):
				code = http.StatusConflict
			}
			s.log.Error("立即探测失败", "name", name, "machine", machine, "cause", err)
			writeErr(w, code, err)
			return
		}
		s.log.Info("立即探测完成", "name", name, "machine", machine, "state", nv.State)
		writeJSON(w, http.StatusOK, s.accountView(nv))
		return
	}
	if isForwarded(r) {
		err := fmt.Errorf("本机不是账号 %s 的可探登录所在机 %s，拒绝代探（转发只走一跳）", name, machine)
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
func (s *Server) accountView(v accounts.View) proto.AccountView {
	a, rec := v.Account, v.Record
	out := proto.AccountView{
		ID: a.ID, Name: a.Name, Kind: a.Kind, Provider: a.Provider, Label: a.Label, Fingerprint: a.Fingerprint,
		IntervalMinutes: a.IntervalMinutes, WarnPercent: a.WarnPercent, LowPercent: a.LowPercent,
		Version: v.Version, State: string(v.State), Stale: v.Stale,
		LastAttemptAt: utcPtr(rec.LastAttemptAt), RecoverAt: utcPtr(v.RecoverAt), ProbeLogin: v.ProbeLogin,
		Logins: make([]proto.LoginView, 0, len(v.Logins)), Carriers: append([]string{}, v.Carriers...),
	}
	for _, lv := range v.Logins {
		out.Logins = append(out.Logins, s.loginView(lv))
	}
	if r := rec.Reading; r != nil {
		rv := &proto.AccountReadingView{Windows: make([]proto.AccountWindowView, 0, len(r.Windows)),
			PlanType: r.PlanType, Fingerprint: r.Fingerprint, ProbedAt: r.ProbedAt.UTC()}
		if e := r.ExtraUsage; e != nil {
			rv.ExtraUsage = &proto.AccountExtraUsageView{Unit: e.Unit, Balance: e.Balance, Used: e.Used,
				Limit: e.Limit, Unlimited: e.Unlimited}
		}
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

// loginView 是登录行 → wire 的唯一手写投影（A4）：代理打码，未设代理键缺席；到期缺席与零值区分；
// relogin 用同一份登录命令表（login_api.go），ssh 行按入口机设备表给出（relay 目标与本机缺席）。
func (s *Server) loginView(lv accounts.LoginView) proto.LoginView {
	l := lv.Login
	out := proto.LoginView{ID: l.ID, Machine: l.Machine, Home: l.Home, Source: l.Source, Fingerprint: l.Fingerprint,
		State: string(l.State), ChangedAt: l.ChangedAt.UTC(), ExpiresAt: utcPtr(l.ExpiresAt),
		Proxy: accounts.MaskProxy(l.Proxy), Carriers: append([]string{}, lv.Carriers...)}
	if cmd, ok := loginCommandFor(l.Kind, l.Provider); ok {
		homeAbs := l.HomeAbs
		if strings.TrimSpace(homeAbs) == "" {
			homeAbs = l.Home
		}
		out.Relogin = &proto.LoginCommand{SSH: s.loginSSHLine(l.Machine), Run: loginRunCommand(homeAbs, cmd.run),
			Note: cmd.note}
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
