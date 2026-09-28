import UIKit
import WebKit

// 控制台容器：承载 CookieBridge 拥有的唯一 WKWebView（单槽）。
// 作为 WKNavigationDelegate 观测 401 → 「过期」错误态 + 重试（I5 条 34）。
// 它不自己发协议请求；导航目标由 CookieBridge 用绑定面返回值加载（条 5/36）。
final class ConsoleWebViewController: UIViewController, WKNavigationDelegate {
    private let webView: WKWebView

    init(webView: WKWebView) {
        self.webView = webView
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = "控制台"
        view.backgroundColor = .systemBackground
        webView.frame = view.bounds
        webView.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        webView.navigationDelegate = self
        view.addSubview(webView)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        if let http = navigationResponse.response as? HTTPURLResponse,
           ExpiryDetector.isExpired(httpStatusCode: http.statusCode) {
            Log.shell.error("控制台收到 401：会话过期")
            decisionHandler(.cancel)
            presentExpired()
            return
        }
        decisionHandler(.allow)
    }

    private func presentExpired() {
        let e = ShellError.expired("webview")
        let alert = UIAlertController(title: e.title, message: e.message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "重试", style: .default) { [weak self] _ in
            // 重走 I3：回列表让用户重新进入（最简、无旧 cookie 复用）。
            self?.navigationController?.popViewController(animated: true)
        })
        present(alert, animated: true)
    }
}
