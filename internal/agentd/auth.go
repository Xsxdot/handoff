// 本文件实现浏览器鉴权的中间件侧：cookie 会话的查找、有效性判定、滑动续期节流，
// 以及把身份传递给下游 handler 的 context 载体。
//
// 职责：
//   - 定义会话/ticket 的寿命常量与 cookie 名
//   - sessionFromRequest：按 cookie 查会话并判定有效性，同时给出失败原因
//   - refreshSession：按节流规则写回滑动续期与最后活跃时刻
//   - identity：一次请求的身份（主令牌 or 某个会话），供 auth 路由与 WS 复验使用
//
// 边界：
//   - 不签发任何凭据（那是 authroutes.go 的事）
//   - 不做 Host 校验（那是 hostguard.go 的事，且它先于本层执行）
//   - 不碰 TLS：Secure 属性的判据只能是 r.TLS，见 authroutes.go 的说明
package agentd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/xushixin/handoff/internal/store"
)

const (
	// sessionCookieName 是浏览器会话 cookie 的名字。
	sessionCookieName = "handoff_session"
	// sessionLifetime 是会话的默认寿命，也是滑动续期的目标寿命。
	sessionLifetime = 30 * 24 * time.Hour
	// ticketLifetime 是一次性 ticket 的寿命。窗口刻意短：它只需要覆盖
	// 「CLI 拿到 URL → 浏览器完成一次跳转」这段时间。
	ticketLifetime = 60 * time.Second
	// lastSeenThrottle 是 last_seen_at 的写入节流阈值。它只用于展示，
	// 不参与任何鉴权判断，精度到分钟足够。
	lastSeenThrottle = 5 * time.Minute
	// defaultSessionRecheck 是 WS 连接上会话复验的周期（见 watchSession）。
	defaultSessionRecheck = 30 * time.Second
)

// identity 是一次请求通过鉴权后的身份。
//
// session 非空 = 由浏览器会话鉴权通过，值为会话 id；
// session 为空 = 由主令牌（Bearer，即 CLI）鉴权通过。
type identity struct {
	session string
}

// identityKey 是 identity 在 request context 中的键类型。
// 用私有空结构体做键，杜绝跨包键碰撞。
type identityKey struct{}

// withIdentity 返回携带身份的新 context。
func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// identityFrom 取出本次请求的身份；未经 auth 中间件的请求返回零值（=主令牌身份）。
//
// 注意：零值与「主令牌身份」不可区分是有意的——本函数的全部调用点都在 auth
// 中间件之内，不存在「没经过鉴权却调用它」的路径
func identityFrom(ctx context.Context) identity {
	id, _ := ctx.Value(identityKey{}).(identity)
	return id
}

// sessionFromRequest 用 cookie 查会话并判定其有效性。
//
// 参数：
//   - r: 当前请求
//
// 返回：
//   - 有效会话；无效时为 nil
//   - reason: 失败原因，用于鉴权失败日志。spec §11 要求区分「无凭据 / Bearer
//     不匹配 / 会话不存在 / 会话过期 / 会话已吊销」，因此不能只回一个 bool
func (s *Server) sessionFromRequest(r *http.Request) (*store.Session, string) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		// 走到这里说明 Bearer 分支也没过：带了 Authorization 头就是令牌不匹配，
		// 没带就是压根没给凭据。两者的排查方向完全不同（配对 token 未同步 vs
		// 有人在扫端口），必须分开记
		if _, ok := bearerToken(r); ok {
			return nil, "Bearer 不匹配"
		}
		return nil, "无凭据"
	}
	sess, err := s.st.SessionByTokenHash(store.HashCredential(c.Value))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, "会话不存在"
	case err != nil:
		s.log.Error("查询会话失败", "cause", err)
		return nil, "会话查询失败"
	case sess.RevokedAt != nil:
		return nil, "会话已吊销"
	case !time.Now().Before(sess.ExpiresAt):
		return nil, "会话过期"
	}
	return sess, ""
}

// refreshSession 按节流规则写回滑动续期与最后活跃时刻。
//
// 参数：
//   - sess: 刚刚通过鉴权的会话
//
// 注意：
//   - **必须节流**：文件树、事件流、终端这些高频路由若每个请求都写一次库，
//     会把 SQLite 写成瓶颈。续期只在剩余寿命不足一半时做（正常使用下每 15 天
//     最多一次），last_seen_at 只在与库中值相差超过 lastSeenThrottle 时写
//   - **写失败只 Warn、不影响放行**：会话是否有效在调用本方法前已经判完，
//     续期失败最坏结果是会话提前过期——属安全侧失败，不该把一次正常请求变成 500
func (s *Server) refreshSession(sess *store.Session) {
	now := time.Now()
	expires := sess.ExpiresAt
	if time.Until(expires) < sessionLifetime/2 {
		expires = now.Add(sessionLifetime)
	}
	lastSeen := sess.LastSeenAt
	if now.Sub(lastSeen) > lastSeenThrottle {
		lastSeen = now
	}
	if expires.Equal(sess.ExpiresAt) && lastSeen.Equal(sess.LastSeenAt) {
		return // 两项都没到写入阈值：本次请求不碰库
	}
	if err := s.st.TouchSession(sess.ID, lastSeen, expires); err != nil {
		s.log.Warn("会话续期写入失败（不影响本次请求放行）", "session", sess.ID, "cause", err)
	}
}
