// LoopbackNavigation.kt —— webview 导航白名单：只放行绑定面的回环源。
//
// 职责：判定导航 URL 是否为允许的回环地址（scheme=http 且 host=127.0.0.1），
//       并把「是否拦截」这一决策收敛成纯函数，供 WebViewClient 与 JVM 单测共用。
// 边界：纯函数、无 io；只认回环 host（未限定端口，绑定面 origin 端口由 switchMachine 决定）；
//       非 http/file/content/js 等一律拒绝（fail-closed），绝不因解析异常而放行。
package dev.gosuper.handoff.mobile.ui

import java.net.URI

object LoopbackNavigation {
    private const val LOOPBACK_HOST = "127.0.0.1"

    /** 只放行 http 回环源：外部域、https、file/content/javascript 等一律拒绝。 */
    fun isAllowed(url: String): Boolean {
        if (url.isEmpty()) return false
        val uri = try {
            URI(url)
        } catch (e: Exception) {
            return false
        }
        if (!uri.scheme.equals("http", ignoreCase = true)) return false
        // URI.host 对畸形/相对 URL 可能为 null；null == LOOPBACK_HOST 为 false，自然拒绝。
        return uri.host == LOOPBACK_HOST
    }

    /** shouldOverrideUrlLoading 的决策：非回环源返回 true（拦截）。 */
    fun shouldIntercept(url: String?): Boolean = !isAllowed(url.orEmpty())
}
