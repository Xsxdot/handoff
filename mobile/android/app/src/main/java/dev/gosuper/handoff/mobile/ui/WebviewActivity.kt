// WebviewActivity.kt —— webview 容器：进入机器走 I3 会话桥接，失败显错误态可重试。
//
// 职责：持 WebView 与 WebviewSessionBinder；进入/失败重试；webview HTTP 401/403 → 过期错误态。
// 边界：协议零重实现；只加载绑定面返回的 origin；JS 仅对回环源开启，绝不注入任何原生对象接口。
package dev.gosuper.handoff.mobile.ui

import android.os.Bundle
import android.util.Log
import android.view.View
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import dev.gosuper.handoff.mobile.HandoffApp
import dev.gosuper.handoff.mobile.R
import dev.gosuper.handoff.mobile.list.EnterCoordinator
import dev.gosuper.handoff.mobile.pair.ErrorClassifier
import dev.gosuper.handoff.mobile.pair.ShellError
import dev.gosuper.handoff.mobile.web.WebviewSessionBinder

class WebviewActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private lateinit var navigator: AndroidWebviewNavigator

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_webview)

        val machine = intent.getStringExtra(EXTRA_MACHINE).orEmpty()
        val online = intent.getBooleanExtra(EXTRA_ONLINE, false)

        webView = findViewById(R.id.webview)
        // 安全：JS 默认关闭；仅当导航目标是绑定面回环源时才开启（见 shouldOverrideUrlLoading 与导航器）。
        webView.settings.javaScriptEnabled = false
        // 安全：绝不对回环源暴露 JS 桥；不注入任何原生对象接口（源码 guard 词法禁止该 API 名）。
        webView.webViewClient = object : WebViewClient() {
            // 导航白名单：非回环源一律拦截（API24+ 走 WebResourceRequest 重载）。
            override fun shouldOverrideUrlLoading(
                view: WebView?,
                request: WebResourceRequest?,
            ): Boolean = interceptIfNonLoopback(view, request?.url?.toString())

            // API21–23 只有 String 重载；不覆写则这些版本上白名单形同虚设。
            @Suppress("OVERRIDE_DEPRECATION", "DEPRECATION")
            override fun shouldOverrideUrlLoading(view: WebView?, url: String?): Boolean =
                interceptIfNonLoopback(view, url)

            override fun onReceivedHttpError(
                view: WebView?,
                request: WebResourceRequest?,
                errorResponse: WebResourceResponse?,
            ) {
                val code = errorResponse?.statusCode ?: return
                ErrorClassifier.fromHttpStatus(code)?.let { showError(it) }
            }

            // API23+：按错误码归类，连接/DNS/超时等 → 兑换类，绝不报过期。
            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?,
            ) {
                if (request?.isForMainFrame != true) return
                ErrorClassifier.fromWebResourceError(error?.errorCode ?: 0)?.let { showError(it) }
            }

            // API21–22 只有旧重载；主框错误同样按错误码归类。
            @Suppress("OVERRIDE_DEPRECATION", "DEPRECATION")
            override fun onReceivedError(
                view: WebView?,
                errorCode: Int,
                description: String?,
                failingUrl: String?,
            ) {
                ErrorClassifier.fromWebResourceError(errorCode)?.let { showError(it) }
            }
        }

        val graph = (application as HandoffApp).graph
        navigator = AndroidWebviewNavigator(webView)
        val binder = WebviewSessionBinder(
            session = graph.core,
            cookies = AndroidCookieStore(),
            navigator = navigator,
        )
        val coordinator = EnterCoordinator(binder, lifecycleScope) { result, error ->
            if (result.isSuccess) hideError() else showError(error ?: ShellError.EXCHANGE)
        }
        coordinator.enter(machine, online)

        findViewById<Button>(R.id.webviewRetryButton).setOnClickListener {
            hideError()
            coordinator.enter(machine, online)
        }
    }

    override fun onDestroy() {
        navigator.markDestroyed()
        webView.destroy()
        super.onDestroy()
    }

    /**
     * 导航白名单决策：非回环源拦截（返回 true）并关闭 JS；回环源放行并开启 JS。
     * 决策落在 [LoopbackNavigation]（纯函数、JVM 可测），此处只做 JS 开关与日志。
     */
    private fun interceptIfNonLoopback(view: WebView?, url: String?): Boolean {
        val allowed = !LoopbackNavigation.shouldIntercept(url)
        view?.settings?.javaScriptEnabled = allowed
        if (!allowed) Log.w(TAG, "拦截非回环导航")
        return !allowed
    }

    private fun showError(error: ShellError) {
        findViewById<View>(R.id.webview).visibility = View.GONE
        findViewById<View>(R.id.errorPanel).visibility = View.VISIBLE
        findViewById<TextView>(R.id.webviewErrorText).text = when (error) {
            ShellError.PAIRING -> getString(R.string.error_pairing)
            ShellError.OFFLINE -> getString(R.string.error_offline)
            ShellError.EXCHANGE -> getString(R.string.error_exchange)
            ShellError.EXPIRED -> getString(R.string.error_expired)
        }
    }

    private fun hideError() {
        findViewById<View>(R.id.errorPanel).visibility = View.GONE
        findViewById<View>(R.id.webview).visibility = View.VISIBLE
    }

    companion object {
        private const val TAG = "WebviewActivity"
        const val EXTRA_MACHINE = "machine"
        const val EXTRA_ONLINE = "online"
    }
}
