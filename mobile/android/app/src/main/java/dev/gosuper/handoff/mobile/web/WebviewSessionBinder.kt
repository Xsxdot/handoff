// WebviewSessionBinder.kt —— 进入/切换机器的会话桥接（contract §3.3，I3 承重）。
//
// 职责：严格按 SwitchMachine → 清 jar → SessionCookie → 注入 → load 的次序完成会话切换；
//       任一步失败都不 load、不复用旧 cookie（fail-closed）。
// 边界：零协议逻辑；不构造 ticket/兑换；只消费 SessionCore 与平台 CookieStore。
package dev.gosuper.handoff.mobile.web

import android.util.Log
import dev.gosuper.handoff.mobile.core.SessionCore
import java.net.URI

private const val TAG = "WebviewSessionBinder"

class WebviewSessionBinder(
    private val session: SessionCore,
    private val cookies: CookieStore,
    private val navigator: WebviewNavigator,
) {
    /**
     * 进入/切换机器，严格 I3 序：
     * `switchMachine(m)` → `clearHost(host)` → `sessionCookie(m)` →
     * `setCookie(url, header)` → `navigator.load(origin)`。
     *
     * 任一步抛错即返回 failure 且不 load；value 非法（isSafeCookieValue=false）同样 fail-closed。
     */
    suspend fun enterMachine(machine: String): Result<Unit> {
        Log.i(TAG, "进入机器开始 machine=$machine")

        val origin = try {
            session.switchMachine(machine)
        } catch (e: Exception) {
            Log.e(TAG, "切机失败 category=EXCHANGE machine=$machine")
            return Result.failure(e)
        }

        val host = hostOf(origin)
        if (host == null) {
            Log.e(TAG, "回环源非法 machine=$machine")
            return Result.failure(IllegalStateException("invalid origin"))
        }

        try {
            cookies.clearHost(host)
        } catch (e: Exception) {
            Log.e(TAG, "清 jar 失败 category=EXCHANGE machine=$machine")
            return Result.failure(e)
        }

        val value = try {
            session.sessionCookie(machine)
        } catch (e: Exception) {
            Log.e(TAG, "取会话 cookie 失败 category=EXCHANGE machine=$machine")
            return Result.failure(e)
        }

        if (!isSafeCookieValue(value)) {
            Log.e(TAG, "cookie 值非法 category=EXCHANGE machine=$machine valueLen=${value.length}")
            return Result.failure(IllegalStateException("unsafe cookie value"))
        }

        try {
            cookies.setCookie(origin, buildSessionCookieHeader(value))
        } catch (e: Exception) {
            Log.e(TAG, "注入 cookie 失败 category=EXCHANGE machine=$machine")
            return Result.failure(e)
        }

        navigator.load(origin)
        Log.i(TAG, "进入机器完成 machine=$machine")
        return Result.success(Unit)
    }

    /** 从 origin 取 host；解析不出返回 null（fail-closed，不猜）。 */
    private fun hostOf(origin: String): String? =
        try {
            URI(origin).host?.takeIf { it.isNotEmpty() }
        } catch (e: Exception) {
            null
        }
}
