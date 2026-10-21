// account_carrier.go —— 载体 × 账号的 gateway 侧接缝（B453 段 3，spec S4）。
//
// 职责：把「载体所选账号」翻成各条链路要的那一份事实——
//   - /api/squads 投影的指纹比对（account_mismatch）；
//   - 检测链发给目标机的 kind/条目/账号名（F15）与本机检测回合的账号代理；
//   - 目标机按账号名在本机账号域查代理（代理串不过机间 wire）；
//   - 派发落点查账号快照（名、kind、代理）交编排（Q6，Manager 不 import 编制域）。
//
// 边界：账号规则（校验、推导）在 scheduling/accounts；这里只读、只映射。读不到账号一律
// 降级为「未选账号」并记 Warn——载体照常可用、照常检测，不因账号读面故障拦住主链路。
package agentd

import (
	"strings"

	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// carrierAccount 是载体所选账号在 gateway 侧要用到的快照。Name 空 = 未选或读不到。
type carrierAccount struct {
	Name  string
	Kind  string
	Entry string
	Proxy string
}

// accountOfCarrier 读载体所选账号的快照。未选、账号域未装配、账号读不到 → 零值。
func (s *Server) accountOfCarrier(c scheduling.Carrier) carrierAccount {
	name := strings.TrimSpace(c.Account)
	if name == "" || s.accounts == nil {
		return carrierAccount{}
	}
	v, err := s.accounts.Get(name)
	if err != nil {
		s.log.Warn("载体所选账号读取失败，按未选账号处理", "carrier", c.Name, "account", name, "cause", err)
		return carrierAccount{}
	}
	return carrierAccount{Name: name, Kind: v.Account.Kind, Entry: v.Account.Entry, Proxy: v.Account.Proxy}
}

// accountFingerprints 读全部账号最近成功读数的指纹（账号名 → 指纹，无读数/无指纹不入表），
// 供 /api/squads 投影比对。账号域未装配或读失败返回 nil（投影不比对，只 Warn）。
func (s *Server) accountFingerprints() map[string]string {
	if s.accounts == nil {
		return nil
	}
	views, err := s.accounts.List()
	if err != nil {
		s.log.Warn("读账号指纹失败，本次载体投影不比对指纹", "cause", err)
		return nil
	}
	out := make(map[string]string, len(views))
	for _, v := range views {
		if r := v.Record.Reading; r != nil && r.Fingerprint != "" {
			out[v.Account.Name] = r.Fingerprint
		}
	}
	return out
}

// accountMismatch 是载体行标红判据（spec S4、plan V8）：载体选了账号，且载体 HOME 的凭据
// 指纹与账号最近读数的指纹都在、且不同。任一侧缺席不比对。
func accountMismatch(c scheduling.Carrier, fingerprints map[string]string) bool {
	if c.Account == "" || c.CredFingerprint == "" {
		return false
	}
	fp, ok := fingerprints[c.Account]
	return ok && fp != c.CredFingerprint
}

// hostWakeProxy 是目标机处理 /api/host/wake 时按账号名查本机账号代理：只认所在机是本机的
// 账号（凭据与代理都只对所在机有意义）；查不到、不在本机、未填代理 → 空串（沿用今天环境）。
func (s *Server) hostWakeProxy(account string) string {
	account = strings.TrimSpace(account)
	if account == "" || s.accounts == nil {
		return ""
	}
	v, err := s.accounts.Get(account)
	if err != nil {
		s.log.Warn("检测回合查账号代理失败，沿用本机环境", "account", account, "cause", err)
		return ""
	}
	if !s.IsSelfMachine(v.Account.Machine) {
		s.log.Warn("检测回合的账号不在本机，不套用其代理", "account", account, "account_machine", v.Account.Machine)
		return ""
	}
	if v.Account.Proxy != "" {
		s.log.Info("检测回合使用账号代理", "account", account, "proxy", proxycfg.Mask(v.Account.Proxy))
	}
	return v.Account.Proxy
}
