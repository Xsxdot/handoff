// WebviewActivity.kt —— webview 容器：进入机器走 I3 会话桥接，失败显错误态可重试。
//
// 职责：持 WebView 与 WebviewSessionBinder；进入/失败重试；webview HTTP 401/403 → 过期错误态。
// 边界：协议零重实现；只加载绑定面返回的 origin；JS 仅对回环源开启，绝不注入任何原生对象接口。
package dev.gosuper.handoff.mobile.ui

import android.os.Bundle
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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_webview)

        val machine = intent.getStringExtra(EXTRA_MACHINE).orEmpty()
        val online = intent.getBooleanExtra(EXTRA_ONLINE, false)

        webView = findViewById(R.id.webview)
        webView.settings.javaScriptEnabled = true
        // 安全：绝不对回环源暴露 JS 桥；不注入任何原生对象接口（源码 guard 词法禁止该 API 名）。
        webView.webViewClient = object : WebViewClient() {
            override fun onReceivedHttpError(
                view: WebView?,
                request: WebResourceRequest?,
                errorResponse: WebResourceResponse?,
            ) {
                val code = errorResponse?.statusCode ?: return
                ErrorClassifier.fromHttpStatus(code)?.let { showError(it) }
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?,
            ) {
                if (request?.isForMainFrame == true) showError(ShellError.EXPIRED)
            }
        }

        val graph = (application as HandoffApp).graph
        val binder = WebviewSessionBinder(
            session = graph.core,
            cookies = AndroidCookieStore(),
            navigator = AndroidWebviewNavigator(webView),
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
        webView.destroy()
        super.onDestroy()
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
        const val EXTRA_MACHINE = "machine"
        const val EXTRA_ONLINE = "online"
    }
}
