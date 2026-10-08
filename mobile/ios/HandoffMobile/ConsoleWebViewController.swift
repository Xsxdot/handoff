import UIKit
import WebKit

// 控制台容器：承载 CookieBridge 拥有的唯一 WKWebView（单槽）。
// 作为 WKNavigationDelegate 观测 401 → 「过期」错误态 + 重试（I5 条 34）。
// 它不自己发协议请求；导航目标由 CookieBridge 用绑定面返回值加载（条 5/36）。
//
// 真机壳对照 Android 的无标题栏 + IME insets：
// 网页自己有顶栏、底栏和房间返回。原生导航条在 iOS 26 上浮在内容之上，会一直盖住会话。
// 键盘避让不交给 WKWebView 的滚动视图——它要等第一个字符才把焦点滚进可视区，
// 并且会把整页横向平移。键盘盖住多少，网页底边就缩短多少；页面高度跟这个视图走，
// 不跟整屏的 100dvh。收起键盘时网页铺到屏幕底，底栏自己留出主屏幕条。
final class ConsoleWebViewController: UIViewController, WKNavigationDelegate, UIGestureRecognizerDelegate {
    private let webView: WKWebView
    private var bottomConstraint: NSLayoutConstraint?
    private var lockingScroll = false
    private var didLogLateralShift = false
    private var publishedHeight: CGFloat = -1
    private var offsetObservation: NSKeyValueObservation?
    private var insetObservation: NSKeyValueObservation?
    private var keyboardObservation: NSObjectProtocol?
    private weak var savedPopDelegate: UIGestureRecognizerDelegate?
    private var capturedPopDelegate = false

    // 键盘盖住本页的高度。0 表示收起，网页铺到屏幕底。
    private(set) var keyboardOverlap: CGFloat = 0

    init(webView: WKWebView) {
        self.webView = webView
        super.init(nibName: nil, bundle: nil)
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) 未实现") }

    deinit {
        if let keyboardObservation {
            NotificationCenter.default.removeObserver(keyboardObservation)
        }
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        // 控制台网页固定浅色。跟着系统深色时，状态栏和底边没被网页盖住的地方是黑的。
        overrideUserInterfaceStyle = .light
        view.backgroundColor = .white
        // push 期间 viewDidLoad 已经能拿到导航控制器；这里藏一次，避免出现动画结束前闪出标题。
        navigationController?.setNavigationBarHidden(true, animated: false)
        FormAccessorySuppressor.suppress()
        ViewportLock.install(on: webView)
        installWebView()
        observeKeyboard()
    }

    override var preferredStatusBarStyle: UIStatusBarStyle { .darkContent }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        publishViewportHeight()
    }

    override func viewWillAppear(_ animated: Bool) {
        super.viewWillAppear(animated)
        navigationController?.setNavigationBarHidden(true, animated: animated)
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        // 转场结束时导航控制器可能把条又放出来，出现后再藏一次。
        navigationController?.setNavigationBarHidden(true, animated: false)
        // 导航条藏起来之后，系统经常把边缘返回手势也关掉。机器列表只剩这一条退路。
        guard let gesture = navigationController?.interactivePopGestureRecognizer else { return }
        if !capturedPopDelegate {
            savedPopDelegate = gesture.delegate
            capturedPopDelegate = true
        }
        gesture.delegate = self
        gesture.isEnabled = true
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        // 弹「会话过期」时本页也会消失，那时不能把机器列表的导航条提前放出来。
        guard isMovingFromParent || isBeingDismissed else { return }
        navigationController?.setNavigationBarHidden(false, animated: animated)
        navigationController?.interactivePopGestureRecognizer?.delegate = savedPopDelegate
    }

    func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
        (navigationController?.viewControllers.count ?? 0) > 1
    }

    private func installWebView() {
        let scroll = webView.scrollView
        // 页面内部区域自己滚。文档滚动视图一旦可滚，聚焦输入框就会把整页（含横向）平移。
        scroll.isScrollEnabled = false
        scroll.bounces = false
        scroll.alwaysBounceHorizontal = false
        scroll.alwaysBounceVertical = false
        scroll.showsHorizontalScrollIndicator = false
        scroll.contentInsetAdjustmentBehavior = .never
        offsetObservation = scroll.observe(\.contentOffset, options: [.new]) { [weak self] scroll, _ in
            self?.resetDocumentScroll(scroll)
        }
        insetObservation = scroll.observe(\.contentInset, options: [.new]) { [weak self] scroll, _ in
            self?.resetDocumentScroll(scroll)
        }

        webView.translatesAutoresizingMaskIntoConstraints = false
        webView.isOpaque = true
        webView.backgroundColor = .white
        webView.scrollView.backgroundColor = .white
        webView.navigationDelegate = self
        view.addSubview(webView)
        // 底边钉屏幕底，不用键盘布局引导：真机上那条引导没有把页面抬离键盘，
        // 发言框停在键盘下面。键盘高度改底边常量。收起时常量是 0，底栏铺到屏幕底。
        let bottom = webView.bottomAnchor.constraint(equalTo: view.bottomAnchor)
        bottomConstraint = bottom
        NSLayoutConstraint.activate([
            webView.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            webView.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor),
            bottom,
        ])
    }

    // 键盘帧是屏幕坐标。盖住本页多少，网页就从底部缩短多少。
    func applyKeyboardOverlap(_ height: CGFloat) {
        let overlap = max(0, height)
        guard overlap != keyboardOverlap else { return }
        keyboardOverlap = overlap
        bottomConstraint?.constant = -overlap
        Log.shell.info("键盘重叠高度=\(overlap, privacy: .public)")
        view.layoutIfNeeded()
        publishViewportHeight()
    }

    // 100dvh 是整屏，不会跟着变矮的网页视图走。把视图高度写给页面，发言框才停在键盘上方。
    private func publishViewportHeight() {
        let height = webView.bounds.height
        guard height > 1, abs(height - publishedHeight) > 0.5 else { return }
        publishedHeight = height
        let px = String(Int(height.rounded()))
        webView.evaluateJavaScript(
            "document.documentElement.style.setProperty('--handoff-viewport','\(px)px')"
        )
    }

    // WKWebView 在键盘动画期间仍会改 contentOffset / contentInset。拉回零，
    // 高度变化只走上面的键盘约束。
    private func resetDocumentScroll(_ scroll: UIScrollView) {
        guard !lockingScroll else { return }
        let offset = scroll.contentOffset
        let inset = scroll.contentInset
        guard offset != .zero || inset != .zero else { return }
        if offset.x != 0 && !didLogLateralShift {
            didLogLateralShift = true
            Log.shell.info("聚焦输入把页面横向平移了 x=\(offset.x, privacy: .public)，已拉回")
        }
        lockingScroll = true
        scroll.contentOffset = .zero
        scroll.contentInset = .zero
        lockingScroll = false
    }

    private func observeKeyboard() {
        keyboardObservation = NotificationCenter.default.addObserver(
            forName: UIResponder.keyboardWillChangeFrameNotification,
            object: nil,
            queue: .main
        ) { [weak self] note in
            guard let self,
                  let end = note.userInfo?[UIResponder.keyboardFrameEndUserInfoKey] as? CGRect else { return }
            let overlap = self.view.bounds.intersection(self.view.convert(end, from: nil))
            let height = overlap.isNull ? 0 : overlap.height
            let duration = note.userInfo?[UIResponder.keyboardAnimationDurationUserInfoKey] as? Double ?? 0
            UIView.animate(withDuration: duration) {
                self.applyKeyboardOverlap(height)
            }
        }
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

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        publishedHeight = -1
        publishViewportHeight()
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

// 把视口锁进网页：禁止聚焦放大，并让安全区数值生效。
// 页面可能在本控制器出现之前就已经开始加载，所以脚本要挂在网页创建时，每次文档开头再跑。
enum ViewportLock {
    static let source = """
    (function(){
      function lock(){
        var meta=document.querySelector('meta[name="viewport"]');
        if(!meta){
          var head=document.head||document.getElementsByTagName('head')[0];
          if(!head) return;
          meta=document.createElement('meta');
          meta.setAttribute('name','viewport');
          head.appendChild(meta);
        }
        var next='width=device-width, initial-scale=1, maximum-scale=1, viewport-fit=cover';
        if(meta.getAttribute('content')!==next) meta.setAttribute('content', next);
        var style=document.getElementById('handoff-ios-viewport');
        if(!style){
          style=document.createElement('style');
          style.id='handoff-ios-viewport';
          style.textContent='@media (pointer:coarse){textarea,input,select,.xterm-helper-textarea{font-size:16px !important}}';
          (document.head||document.documentElement).appendChild(style);
        }
      }
      if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', lock);
      else lock();
    })();
    """

    static func install(on webView: WKWebView) {
        let scripts = webView.configuration.userContentController.userScripts
        if scripts.contains(where: { $0.source == source }) { return }
        let script = WKUserScript(source: source, injectionTime: .atDocumentStart, forMainFrameOnly: true)
        webView.configuration.userContentController.addUserScript(script)
    }
}

// 收起 WKWebView 自带的表单辅助条（上一项 / 下一项 / 完成）。
// 聊天框和终端都不是多字段表单。这条插在键盘和网页之间，输入区就贴不到键盘上沿。
// 第一响应者是网页内容视图，不是 WKWebView 本身，所以要改内容视图的辅助条属性。
enum FormAccessorySuppressor {
    private static var didSuppress = false

    static func suppress() {
        guard !didSuppress else { return }
        guard let cls = NSClassFromString("WKContentView"),
              let method = class_getInstanceMethod(cls, NSSelectorFromString("inputAccessoryView")) else {
            Log.shell.error("收起表单辅助条失败：找不到网页内容视图")
            return
        }
        let block: @convention(block) (AnyObject) -> UIView? = { _ in nil }
        method_setImplementation(method, imp_implementationWithBlock(block))
        didSuppress = true
        Log.shell.info("已收起网页表单辅助条")
    }
}
