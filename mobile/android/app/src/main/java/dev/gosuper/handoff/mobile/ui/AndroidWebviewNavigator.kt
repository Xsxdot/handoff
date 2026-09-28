// AndroidWebviewNavigator.kt —— WebviewNavigator 的 Android 实现：WebView.loadUrl。
//
// 职责：把 load(origin) 落到 WebView（切 UI 线程）。
// 边界：只 load 传入 URL，不加 header、不拼 URL、不注入 JS。
package dev.gosuper.handoff.mobile.ui

import android.util.Log
import android.webkit.WebView
import dev.gosuper.handoff.mobile.web.WebviewNavigator

private const val TAG = "WebviewNavigator"

class AndroidWebviewNavigator(private val webView: WebView) : WebviewNavigator {
    override fun load(url: String) {
        Log.i(TAG, "webview 加载回环源")
        webView.post { webView.loadUrl(url) }
    }
}
