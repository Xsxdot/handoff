// AndroidCookieStore.kt —— CookieStore 的 Android 实现：包 WebView 的 CookieManager。
//
// 职责：把清 jar / 写 cookie 的异步回调转成可等待的 suspend，保证 I3 严格序；
//       平台回调结果为 false（拒绝）时必须转成失败（fail-closed），绝不静默成功继续 load。
// 边界：清回环专用 webview 的 jar（与定向删等价，contract §3.4 允许）；不解析 cookie 值、不记其内容。
package dev.gosuper.handoff.mobile.ui

import android.util.Log
import android.webkit.CookieManager
import android.webkit.ValueCallback
import dev.gosuper.handoff.mobile.web.CookieStore
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

private const val TAG = "AndroidCookieStore"

/** CookieManager 的窄端口，便于 JVM 单测注入「平台返回 false」的替身（WebView/CookieManager 属真机范围）。 */
internal interface CookieManagerPort {
    fun removeAllCookies(callback: ValueCallback<Boolean>)
    fun setCookie(url: String, value: String, callback: ValueCallback<Boolean>)
    fun flush()
}

/** 生产实现：直通系统 CookieManager。 */
internal class SystemCookieManagerPort(private val manager: CookieManager) : CookieManagerPort {
    override fun removeAllCookies(callback: ValueCallback<Boolean>) {
        manager.removeAllCookies(callback)
    }

    override fun setCookie(url: String, value: String, callback: ValueCallback<Boolean>) {
        manager.setCookie(url, value, callback)
    }

    override fun flush() {
        manager.flush()
    }
}

class AndroidCookieStore internal constructor(
    private val port: CookieManagerPort,
) : CookieStore {

    constructor() : this(SystemCookieManagerPort(CookieManager.getInstance()))

    override suspend fun clearHost(host: String) {
        Log.i(TAG, "清 cookie jar host=$host")
        awaitCookieAccepted { port.removeAllCookies(it) }
        port.flush()
    }

    override suspend fun setCookie(url: String, setCookieHeader: String) {
        Log.i(TAG, "注入会话 cookie")
        awaitCookieAccepted { port.setCookie(url, setCookieHeader, it) }
        port.flush()
    }

    /**
     * 等待平台 cookie 操作回调：true 才通过；false/null 一律 resumeWithException。
     * 这样 WebviewSessionBinder 进入 failure 分支且不 load 后续（fail-closed，不复用旧会话）。
     * 仅成功后才 flush（失败态不需要落盘）。
     */
    private suspend fun awaitCookieAccepted(register: (ValueCallback<Boolean>) -> Unit) {
        suspendCancellableCoroutine<Unit> { cont ->
            register { result ->
                if (!cont.isActive) return@register
                if (result == true) cont.resume(Unit) else cont.resumeWithException(CookieRejectedException())
            }
        }
    }
}

/** 平台拒绝 cookie 操作（回调返回 false/null）时抛出，交 WebviewSessionBinder 归为失败。 */
private class CookieRejectedException : IllegalStateException("cookie operation rejected by platform")
