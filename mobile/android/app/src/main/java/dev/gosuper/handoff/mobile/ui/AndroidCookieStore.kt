// AndroidCookieStore.kt —— CookieStore 的 Android 实现：包 WebView 的 CookieManager。
//
// 职责：把清 jar / 写 cookie 的异步回调转成可等待的 suspend，保证 I3 严格序。
// 边界：清回环专用 webview 的 jar（与定向删等价，contract §3.4 允许）；不解析 cookie 值。
package dev.gosuper.handoff.mobile.ui

import android.util.Log
import android.webkit.CookieManager
import dev.gosuper.handoff.mobile.web.CookieStore
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume

private const val TAG = "AndroidCookieStore"

class AndroidCookieStore(
    private val manager: CookieManager = CookieManager.getInstance(),
) : CookieStore {

    override suspend fun clearHost(host: String) {
        Log.i(TAG, "清 cookie jar host=$host")
        suspendCancellableCoroutine { cont ->
            manager.removeAllCookies { _ ->
                manager.flush()
                cont.resume(Unit)
            }
        }
    }

    override suspend fun setCookie(url: String, setCookieHeader: String) {
        Log.i(TAG, "注入会话 cookie")
        suspendCancellableCoroutine { cont ->
            manager.setCookie(url, setCookieHeader) { _ ->
                manager.flush()
                cont.resume(Unit)
            }
        }
    }
}
