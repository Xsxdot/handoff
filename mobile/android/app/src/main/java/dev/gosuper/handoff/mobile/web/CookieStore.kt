// CookieStore.kt —— webview cookie jar 的窄抽象 + Set-Cookie 头构造。
//
// 职责：清 host 旧 cookie、写入新 cookie；把「手拼 cookie 头」这一高危序列化点收敛成一个纯函数。
// 边界：不拥有 WebView；值的安全校验 fail-closed（含分隔符即拒绝）。
package dev.gosuper.handoff.mobile.web

/** webview cookie jar 抽象（平台实现包 CookieManager）。 */
interface CookieStore {
    /** 清掉该 host 的全部旧 cookie（不区分端口）。 */
    suspend fun clearHost(host: String)

    /** 以完整 Set-Cookie 头写入 cookie。 */
    suspend fun setCookie(url: String, setCookieHeader: String)
}

/** 会话 cookie 名（与 internal/agentd/auth.go 的 sessionCookieName 逐字对齐）。 */
const val SESSION_COOKIE_NAME = "handoff_session"

/**
 * 构造注入用的 Set-Cookie 头。
 * 属性按 contract §3.4 硬编码：Path=/、HttpOnly、SameSite=Lax；
 * host-only（不带 Domain）、非 Secure（明文回环）、不设 Max-Age（进程内会话 cookie）。
 */
fun buildSessionCookieHeader(value: String): String =
    "$SESSION_COOKIE_NAME=$value; Path=/; HttpOnly; SameSite=Lax"

/**
 * cookie value 安全校验（fail-closed）：
 * 手拼头对分隔符敏感——空串、含 `;`/`,`、空白或控制符一律拒绝，避免属性注入/截断。
 */
fun isSafeCookieValue(value: String): Boolean {
    if (value.isEmpty()) return false
    return value.none { it == ';' || it == ',' || it.isWhitespace() || it.isISOControl() }
}
