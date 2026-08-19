// 本文件定义浏览器鉴权相关接口的线格式：ticket 签发响应与会话展示条目。
//
// 职责：
//   - 作为 agentd 服务端与 internal/client 之间的单一契约来源
//
// 边界：
//   - 只有线格式，不含任何行为；凭据明文永远不出现在这里
//     （AuthTicketResp 只回 URL，会话 cookie 只经 Set-Cookie 下发）
package proto

import "time"

// AuthTicketResp 是 POST /api/auth/tickets 的响应。
//
// URL 是可直接打开的兑换地址（含一次性 ticket）；ExpiresAt 是该 ticket 的过期时刻。
type AuthTicketResp struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionInfo 是 GET /api/auth/sessions 的单条会话。
//
// 注意：不含任何凭据字段——cookie 哈希都不给，展示与吊销只需要 id
type SessionInfo struct {
	ID         string     `json:"id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
