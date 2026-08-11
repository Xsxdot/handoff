// 本文件实现浏览器鉴权的五条路由：签发 ticket、兑换 cookie、列出会话、吊销会话、登出。
//
// 职责：
//   - POST /api/auth/tickets      主令牌签发一次性 ticket，返回可打开的 /console URL
//   - GET  /console?ticket=<t>    原子消费 ticket → Set-Cookie → 302 到 /
//   - GET  /api/auth/sessions     列出会话（含已吊销，供人判断）
//   - DELETE /api/auth/sessions/{id}  吊销指定会话
//   - POST /api/auth/logout       吊销当前 cookie 会话并清除 cookie
//
// 边界：
//   - **不托管前端**：/console 的 302 目标固定为 /，本轮 agentd 尚未 embed 任何页面，
//     / 返回 404 是预期结果。不得为了「让页面别 404」而塞占位首页
//   - 不判断 Host（hostguard.go 已在更外层做完），不做会话续期（auth.go 的事）
//   - 不读 X-Forwarded-Proto：cookie 的 Secure 与 URL 的 scheme 只按 r.TLS 判定，
//     因为上游可能是一台不可信中转，让它决定安全属性方向是反的
package agentd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// deviceNameMaxRunes 是设备名的展示长度上限。
const deviceNameMaxRunes = 64

// randCredential 生成 256 位随机凭据明文（ticket 与会话 cookie 共用）。
//
// 返回：
//   - 64 字符十六进制串
//   - crypto/rand 读取失败时返回错误（不 panic：这是一条 HTTP 路径，
//     500 比让整个 agentd 崩掉合理）
func randCredential() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机凭据: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// sanitizeDeviceName 净化设备名：剥掉控制字符并按 rune 截断。
//
// 参数：
//   - s: 客户端提供的原始设备名（来自 --device 或 User-Agent）
//
// 返回：
//   - 可安全写入库、可安全打印到终端的展示名
//
// 注意：
//   - 设备名**纯展示，不参与任何鉴权判断**，但它来自客户端——一个构造过的
//     User-Agent 能往终端里注入 ANSI 转义序列，因此必须在入库这道边界剥掉
func sanitizeDeviceName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	rs := []rune(strings.TrimSpace(cleaned))
	if len(rs) > deviceNameMaxRunes {
		rs = rs[:deviceNameMaxRunes]
	}
	return string(rs)
}

// consoleURL 用请求自身的 Host 拼出 /console 的兑换地址。
//
// 为什么用 r.Host 而不是 cfg.Listen：CLI 可能经 --target 访问一台远端 agentd，
// 也可能经 --agentd 指定别的端点。请求打到哪里，能兑换的就是哪里。
// scheme 只按 r.TLS 判定，不读 X-Forwarded-Proto（理由见文件头边界）。
func consoleURL(r *http.Request, ticket string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/console?ticket=" + url.QueryEscape(ticket)
}

// handleIssueTicket 由主令牌签发一次性 ticket。
func (s *Server) handleIssueTicket(w http.ResponseWriter, r *http.Request) {
	if id := identityFrom(r.Context()); id.session != "" {
		// 会话代表「一台已授权设备」。让它签发 ticket 等于让一台丢失的手机
		// 无限制地再造设备，吊销就失去意义
		s.log.Warn("会话身份尝试签发 ticket，已拒绝", "session", id.session, "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "签发 ticket 需要主令牌"})
		return
	}
	var req struct {
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		// 空 body 合法（设备名可缺省），因此 EOF 不算错误
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体非法"})
		return
	}
	plain, err := randCredential()
	if err != nil {
		s.log.Error("签发 ticket 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发失败"})
		return
	}
	now := time.Now()
	expires := now.Add(ticketLifetime)
	device := sanitizeDeviceName(req.DeviceName)
	if err := s.st.CreateAuthTicket(store.HashCredential(plain), device, now, expires); err != nil {
		s.log.Error("签发 ticket 失败", "device_name", device, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发失败"})
		return
	}
	s.log.Info("签发 ticket", "device_name", device, "expires_at", expires)
	writeJSON(w, http.StatusOK, proto.AuthTicketResp{URL: consoleURL(r, plain), ExpiresAt: expires})
}

// handleConsole 兑换 ticket：原子消费 → 建会话 → Set-Cookie → 302 到 /。
//
// 这是唯一不经主令牌/cookie 的路由——ticket 本身就是它的凭据。
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	plain := r.URL.Query().Get("ticket")
	if plain == "" {
		s.log.Warn("消费 ticket 失败", "result", "缺少 ticket 参数", "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	}
	now := time.Now()
	device, expiresAt, err := s.st.ConsumeAuthTicket(store.HashCredential(plain), now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("消费 ticket 失败", "result", "不存在或已消费", "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	case err != nil:
		s.log.Error("消费 ticket 出错", "remote_addr", r.RemoteAddr, "cause", err)
		s.writeTicketError(w)
		return
	case !now.Before(expiresAt):
		s.log.Warn("消费 ticket 失败", "result", "已过期", "expires_at", expiresAt, "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	}
	token, err := randCredential()
	if err != nil {
		s.log.Error("建立会话失败", "cause", err)
		s.writeTicketError(w)
		return
	}
	sess := &store.Session{
		ID:         uuid.NewString(),
		TokenHash:  store.HashCredential(token),
		DeviceName: sanitizeDeviceName(joinDevice(device, browserName(r.UserAgent()))),
		CreatedAt:  now,
		ExpiresAt:  now.Add(sessionLifetime),
		LastSeenAt: now,
	}
	if err := s.st.CreateSession(sess); err != nil {
		s.log.Error("建立会话失败", "device_name", sess.DeviceName, "cause", err)
		s.writeTicketError(w)
		return
	}
	s.log.Info("消费 ticket 成功", "result", "成功", "session", sess.ID)
	s.log.Info("会话建立", "session", sess.ID, "device_name", sess.DeviceName, "expires_at", sess.ExpiresAt)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// 只按 r.TLS：明文 loopback 下设 Secure 会让 cookie 直接失效
		Secure: r.TLS != nil,
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	// 302 到 /：本轮不托管前端，/ 返回 404 是预期结果（cookie 此时已设好）
	http.Redirect(w, r, "/", http.StatusFound)
}

// writeTicketError 输出兑换失败的说明。
//
// 为什么是 text/plain 而不是一张 HTML 错误页：本轮 agentd 尚未托管任何前端，
// 纯文本既够用，又完全没有 HTML 注入面。
func (s *Server) writeTicketError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := io.WriteString(w, "这个链接已失效，请重新执行 handoff console\n"); err != nil {
		s.log.Warn("写出 ticket 失效说明失败", "err", err)
	}
}

// joinDevice 把登记设备名与浏览器名拼成展示名（任一为空时不留多余分隔符）。
func joinDevice(device, browser string) string {
	switch {
	case device == "":
		return browser
	case browser == "":
		return device
	}
	return device + " / " + browser
}

// browserName 从 User-Agent 里粗略识别浏览器名。
//
// 只做展示用途，因此刻意保持极简：识别不出就返回空串，绝不把整个 UA 串塞进
// 设备名（那既难看又是一条把攻击者可控长文本写进库的路径）。
// 注意顺序：Edge 与 Chrome 的 UA 都含 "Chrome"，Chrome 与 Safari 的都含 "Safari"。
func browserName(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "Firefox/"):
		return "Firefox"
	case strings.Contains(ua, "Chrome/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	return ""
}

// handleListSessions 列出全部会话（Bearer 或 cookie 均可）。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.st.ListSessions()
	if err != nil {
		s.log.Error("列出会话失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "列出会话失败"})
		return
	}
	out := make([]proto.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, proto.SessionInfo{
			ID: sess.ID, DeviceName: sess.DeviceName, CreatedAt: sess.CreatedAt,
			ExpiresAt: sess.ExpiresAt, LastSeenAt: sess.LastSeenAt, RevokedAt: sess.RevokedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevokeSession 吊销指定会话（Bearer 或 cookie 均可）。
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.revoke(r, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在或已吊销"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "吊销失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleLogout 吊销当前 cookie 会话并清除 cookie。
//
// 只接受会话身份：CLI 用主令牌调它没有「当前会话」可言，属用法错误。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if id.session == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "登出需要会话 cookie"})
		return
	}
	if err := s.revoke(r, id.session); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "登出失败"})
		return
	}
	// MaxAge<0 让浏览器立即删除该 cookie；属性必须与下发时一致，否则删不掉
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// revoke 执行吊销并记录发起方，供 handleRevokeSession 与 handleLogout 共用。
func (s *Server) revoke(r *http.Request, id string) error {
	by := "主令牌"
	if cur := identityFrom(r.Context()); cur.session != "" {
		by = "会话 " + cur.session
	}
	if err := s.st.RevokeSession(id, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("吊销会话未命中（不存在或已吊销）", "session", id, "by", by)
			return err
		}
		s.log.Error("吊销会话失败", "session", id, "by", by, "cause", err)
		return err
	}
	s.log.Info("吊销会话", "session", id, "by", by)
	return nil
}
