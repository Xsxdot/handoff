// AndroidWebviewNavigator.kt —— WebviewNavigator 的 Android 实现：WebView.loadUrl。
//
// 职责：把 load(origin) 落到 WebView（切 UI 线程）；只放行回环源并仅对它开启 JS；
//       destroy 后不再 load（post 中的 Runnable 可能晚于 Activity.onDestroy 执行）。
// 边界：只 load 传入 URL，不加 header、不拼 URL、不注入 JS（不调 evaluateJavascript）。
package dev.gosuper.handoff.mobile.ui

import android.util.Log
import android.webkit.WebView
import dev.gosuper.handoff.mobile.web.WebviewNavigator

private const val TAG = "WebviewNavigator"

class AndroidWebviewNavigator(private val webView: WebView) : WebviewNavigator {

    @Volatile
    private var destroyed = false

    /**
     * Activity.onDestroy 时同步调用（先于 webView.destroy()）：声明该 WebView 已不可用，
     * 让仍在 UI 队列里的 post 任务跳过 loadUrl。用自持标志而非 WebView.isDestroyed()，
     * 后者 API 26 才可用，而本壳 minSdk=21。
     */
    fun markDestroyed() {
        destroyed = true
    }

    override fun load(url: String) {
        // 纵深防御：loadUrl 不受 WebViewClient 导航白名单约束，这里再拒一次非回环源。
        if (LoopbackNavigation.shouldIntercept(url)) {
            Log.w(TAG, "拒绝加载非回环源")
            return
        }
        Log.i(TAG, "webview 加载回环源")
        webView.post {
            if (destroyed) {
                Log.w(TAG, "webview 已销毁，跳过 load")
                return@post
            }
            // JS 只在回环源开启（默认关闭见 WebviewActivity.onCreate）。
            webView.settings.javaScriptEnabled = true
            webView.loadUrl(url)
        }
    }
}
