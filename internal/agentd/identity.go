// identity.go —— 会话身份解析的唯一收口（B358.9 契约 §3.2/§3.4）。
//
// 职责：把「transport 事实」（本机配置 console_user + 本次登录会话登记的设备名）
// 解析成控制台成员的人名统一记法 user:<name> 与端戳；缺名 fail-closed。
//
// 边界：
//   - 权力/成员/@ 只认人名；端戳只做落款，不参与权力判定（spec 决定 1）。
//   - 缺名不回落旧脸 web:<host>（决定 8）——返回 Configured=false 让调用方拒收。
//   - 端戳缺失不拦门：主令牌身份（CLI，无登录会话）端戳为空，不是失败（决定 3）。
//
// 本文件是解析唯一入口；控制台各端点（房间/会话/已读/收件箱）在实现节点改为
// 消费它，取代今天散落的 roomUserActor()="web:"+hostOnly（见契约欠账）。
package agentd

import (
	"context"
	"net/http"

	"github.com/Xsxdot/handoff/internal/proto"
)

// consoleIdentity 是 agentd 侧一次请求的身份解析结果（B358.9）。
//
// Member 恒为本文请求的人名统一记法 user:<name>（Configured=false 时为空串）；
// Device 是本次登录会话登记的设备名（主令牌/无会话时为空串）；
// Configured 表示本机 console_user 是否已配置——false 时发话面调用方 fail-closed。
type consoleIdentity struct {
	Member     string
	Device     string
	Configured bool
}

// resolveConsoleIdentity 是本卡身份解析唯一入口（契约 §3「身份解析只在 agentd
// 一处」）：人名取配置 console_user，端戳取登录会话设备名；缺名 fail-closed。
//
// 参数 r 只用于取本次请求的登录会话（cookie 会话 id → store.Session.DeviceName）。
// 主令牌（CLI）请求 identity.session 为空，端戳留空——端戳缺失不拦门。
func (s *Server) resolveConsoleIdentity(r *http.Request) consoleIdentity {
	name := ""
	if cfg := s.conf(); cfg != nil {
		name = cfg.ConsoleUser
	}
	if name == "" {
		// 未配名：fail-closed。不回落到 web:<host> 旧脸（决定 8）。
		return consoleIdentity{Configured: false}
	}
	member, err := proto.MemberIdentity(proto.IdentityKindUser, name)
	if err != nil {
		// 配置名不合法等同未配：可行动报错由调用方给（写清 console_user）。
		s.log.Warn("console_user 配置不合法，身份解析失败", "cause", err)
		return consoleIdentity{Configured: false}
	}
	return consoleIdentity{
		Member:     member,
		Device:     sessionDeviceName(r.Context()),
		Configured: true,
	}
}

// sessionDeviceName 取本次请求登录会话登记的设备名（端戳，B358.9）。
//
// 端戳在鉴权中间件查会话行时随 identity 一次读出（server.go auth 处装填），
// 本函数不再二次查库——无新增调用边。无登录会话（主令牌）时为空串：端戳缺失
// 不拦门（少的是落款，不是权力）。
func sessionDeviceName(ctx context.Context) string {
	return identityFrom(ctx).device
}

// handleIdentity 返回本请求的控制台身份（GET /api/identity，契约接缝 #4）。
//
// 这是控制台会话页/新建会话表单的数据源：owner 预填、落款显示、缺名可行动
// 提示。三个键恒出（无 omitempty）——前端要能区分「空串」与「后端没发这个
// 字段」（老 agentd）。本端点不落账、不改状态，读配置与会话行。
func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	id := s.resolveConsoleIdentity(r)
	s.log.Debug("控制台身份查询", "member", id.Member, "configured", id.Configured,
		"device_present", id.Device != "")
	writeJSON(w, http.StatusOK, proto.IdentityResp{
		Member: id.Member, Device: id.Device, Configured: id.Configured,
	})
}
