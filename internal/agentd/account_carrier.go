// account_carrier.go —— 载体 × 账号登录的 gateway 侧接缝（B453 段 3，spec S4；B464 段 1 改为
// 载体 → 登录 → 账号）。
//
// 职责：把「载体所选登录」翻成各条链路要的那一份事实——
//   - /api/squads 投影的派生只读字段（所属账号 id/名、登录状态）；
//   - 检测链发给目标机的登录 id（W1）与本机检测回合的 kind/代理；
//   - 目标机按登录 id 在本机账号域查 kind 与代理（代理串不过机间 wire，只认本机登录）；
//   - 派发落点查账号快照（账号 id、kind、登录代理）交编排（Q6，Manager 不 import 编制域）；
//   - 协调者回合按（CLI, HOME）反查本机载体所选登录的代理。
//
// 边界：账号规则（归并、推导）在 scheduling/accounts；这里只读、只映射。读不到登录或账号一律
// 降级为「未选账号登录」并记 Warn——载体照常可用、照常检测，不因账号读面故障拦住主链路。
// 登录代理经 PUT /api/logins/{id}/proxy 设置（plan A9）；未设代理时各链路逐字节同改动前。
package agentd

import (
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// carrierAccount 是载体所选登录在 gateway 侧要用到的快照。Account 空 = 未选或读不到。
type carrierAccount struct {
	Login   string // 登录 id
	Account string // 账号 id（任务行快照、撞额度标记、重跑排除集都存它，plan Q23）
	Kind    string
	Machine string // 登录所在机
	Proxy   string // 登录代理（原值，不打码；只在所在机内存与本机任务行里出现）
}

// loginOfCarrier 读载体所选登录的快照。未选、账号域未装配、登录读不到 → 零值。
func (s *Server) loginOfCarrier(c scheduling.Carrier) carrierAccount {
	id := strings.TrimSpace(c.Login)
	if id == "" || s.accounts == nil {
		return carrierAccount{}
	}
	l, err := s.accounts.Login(id)
	if err != nil {
		s.log.Warn("载体所选登录读取失败，按未选账号登录处理", "carrier", c.Name, "login", id, "cause", err)
		return carrierAccount{}
	}
	return carrierAccount{Login: l.ID, Account: l.Account, Kind: l.Kind, Machine: l.Machine, Proxy: l.Proxy}
}

// loginIndex 是 /api/squads 投影用的「登录 id → 所属账号与登录状态」表（一次 List 建成）。
type loginIndex map[string]struct {
	accountID, accountName string
	state                  accounts.LoginState
}

// accountLoginIndex 读全部账号建登录索引。账号域未装配或读失败返回 nil（投影不带派生字段，只 Warn）。
func (s *Server) accountLoginIndex() loginIndex {
	if s.accounts == nil {
		return nil
	}
	views, err := s.accounts.List()
	if err != nil {
		s.log.Warn("读账号失败，本次载体投影不带账号名与登录状态", "cause", err)
		return nil
	}
	idx := loginIndex{}
	for _, v := range views {
		for _, lv := range v.Logins {
			idx[lv.Login.ID] = struct {
				accountID, accountName string
				state                  accounts.LoginState
			}{v.Account.ID, v.Account.Name, lv.Login.State}
		}
	}
	return idx
}

// hostWakeLogin 是目标机处理 /api/host/wake 时按登录 id 查本机登录的 kind 与代理：只认所在机是
// 本机的登录（凭据与代理都只对所在机有意义）；查不到、不在本机 → 零值（沿用今天环境）。
func (s *Server) hostWakeLogin(loginID string) carrierAccount {
	loginID = strings.TrimSpace(loginID)
	if loginID == "" || s.accounts == nil {
		return carrierAccount{}
	}
	l, err := s.accounts.Login(loginID)
	if err != nil {
		s.log.Warn("检测回合查登录失败，沿用本机环境", "login", loginID, "cause", err)
		return carrierAccount{}
	}
	if !s.IsSelfMachine(l.Machine) {
		s.log.Warn("检测回合的登录不在本机，不套用其代理", "login", loginID, "login_machine", l.Machine)
		return carrierAccount{}
	}
	if l.Proxy != "" {
		s.log.Info("检测回合使用登录代理", "login", loginID, "proxy", proxycfg.Mask(l.Proxy))
	}
	return carrierAccount{Login: l.ID, Account: l.Account, Kind: l.Kind, Machine: l.Machine, Proxy: l.Proxy}
}

// dispatchAccount 是派发落点的账号快照：按载体名读载体再读其登录。读不到载体（准入刚读过，
// 极罕见）或登录 → 零值并 Warn，任务按未选账号跑，不因账号读面拦派发。
func (s *Server) dispatchAccount(carrierName string) carrierAccount {
	if strings.TrimSpace(carrierName) == "" || s.scheduling == nil {
		return carrierAccount{}
	}
	c, err := s.scheduling.Carrier(carrierName)
	if err != nil {
		s.log.Warn("派发读载体以取账号快照失败，按未选账号派发", "carrier", carrierName, "cause", err)
		return carrierAccount{}
	}
	acct := s.loginOfCarrier(c)
	if acct.Account != "" {
		s.log.Info("派发带载体账号快照", "carrier", carrierName, "login", acct.Login, "account", acct.Account,
			"account_kind", acct.Kind, "proxy_set", acct.Proxy != "")
	}
	return acct
}

// coordinatorAccountProxy 是协调者回合（keystone 经 coordinatorRunner 拉起/续接）的登录代理查询
// （B453 S4「该载体的任务回合」含协调者回合；B464 I2 优先按载体名取）。runner 拿得到 SessionSpec/
// SessionRef 的载体名（新引用）时直接读该载体所选登录；零值（旧引用）才回落按物理身份（CLI +
// 规范化 HOME）反查本机载体。回落时同一身份有多个载体且所选登录不一致：各登录代理相同仍套用，
// 不同才无从判定、只 Warn；登录不在本机同样不套用（hostWakeLogin 同一判据）。
func (s *Server) coordinatorAccountProxy(carrierName, cli, homeDir string) string {
	if s.scheduling == nil || s.accounts == nil {
		return ""
	}
	if name := strings.TrimSpace(carrierName); name != "" {
		c, err := s.scheduling.Carrier(name)
		if err != nil {
			s.log.Warn("协调者回合按载体名查登录失败，沿用本机环境", "carrier", name, "cause", err)
			return ""
		}
		if !s.IsSelfMachine(c.Machine) {
			s.log.Warn("协调者回合的载体不在本机，不套用登录代理", "carrier", name, "machine", c.Machine)
			return ""
		}
		login := strings.TrimSpace(c.Login)
		if login == "" {
			return ""
		}
		proxy := s.hostWakeLogin(login).Proxy
		if proxy != "" {
			s.log.Info("协调者回合使用登录代理", "carrier", name, "login", login, "proxy", proxycfg.Mask(proxy))
		}
		return proxy
	}
	if strings.TrimSpace(homeDir) == "" {
		return ""
	}
	rows, err := s.scheduling.CarrierRows()
	if err != nil {
		s.log.Warn("协调者回合查载体登录失败，沿用本机环境", "cli", cli, "home_dir", homeDir, "cause", err)
		return ""
	}
	want := filepath.Clean(homeDir)
	matched, logins := []string{}, map[string]bool{}
	for _, row := range rows {
		c := row.Carrier
		if filepath.Base(c.CLI) != filepath.Base(cli) || !s.IsSelfMachine(c.Machine) {
			continue
		}
		home := c.HomeDir
		if strings.TrimSpace(home) == "" {
			home = "~"
		}
		expanded, err := hostapi.ExpandHomePath(home)
		if err != nil || filepath.Clean(expanded) != want {
			continue
		}
		matched = append(matched, c.Name)
		if l := strings.TrimSpace(c.Login); l != "" {
			logins[l] = true
		}
	}
	if len(logins) == 0 {
		return ""
	}
	proxies := make([]string, 0, len(logins))
	for id := range logins {
		proxies = append(proxies, s.hostWakeLogin(id).Proxy)
	}
	for _, proxy := range proxies[1:] {
		if proxy != proxies[0] {
			s.log.Warn("协调者回合的载体身份对应多个不同登录代理，不套用", "cli", cli,
				"home_dir", homeDir, "carriers", matched)
			return ""
		}
	}
	if proxies[0] != "" {
		s.log.Info("协调者回合使用登录代理", "cli", cli, "carriers", matched,
			"proxy", proxycfg.Mask(proxies[0]))
	}
	return proxies[0]
}
