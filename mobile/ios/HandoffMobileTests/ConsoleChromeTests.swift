import UIKit
import WebKit
import XCTest
@testable import HandoffMobile

// 控制台壳的三条真机故障：原生「控制台」条盖住网页、聚焦输入框后整页横滑、
// 键盘弹出时网页高度不立刻让出键盘。能变红：导航条仍可见、底边不跟键盘、
// 或网页滚动视图还能停在非零偏移。
final class ConsoleChromeTests: XCTestCase {
    private var window: UIWindow?
    private var previousKeyWindow: UIWindow?

    private func pins(_ view: UIView, _ a: NSLayoutYAxisAnchor, _ b: NSLayoutYAxisAnchor) -> Bool {
        view.constraints.contains { c in
            (c.firstAnchor === a && c.secondAnchor === b) || (c.firstAnchor === b && c.secondAnchor === a)
        }
    }

    override func tearDown() {
        window?.isHidden = true
        window?.rootViewController = nil
        window = nil
        previousKeyWindow?.makeKeyAndVisible()
        previousKeyWindow = nil
        super.tearDown()
    }

    func testConsoleHidesNavigationBarAndRestoresItOnPop() {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        XCTAssertNotNil(scene, "测试宿主没有窗口场景，导航条显隐无法发生")
        guard let scene else { return }
        let nav = UINavigationController(rootViewController: UIViewController())
        let window = UIWindow(windowScene: scene)
        window.frame = CGRect(x: 0, y: 0, width: 390, height: 844)
        window.rootViewController = nav
        previousKeyWindow = scene.windows.first { $0.isKeyWindow && $0 !== window }
        window.makeKeyAndVisible()
        self.window = window

        let console = ConsoleWebViewController(webView: WKWebView())
        nav.pushViewController(console, animated: false)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))

        XCTAssertTrue(nav.isNavigationBarHidden, "控制台页不该留着「控制台」和返回")
        XCTAssertTrue(nav.interactivePopGestureRecognizer?.isEnabled ?? false, "左缘滑动仍要能回到机器列表")

        nav.popViewController(animated: false)
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
        XCTAssertFalse(nav.isNavigationBarHidden, "回到机器列表要恢复导航条")
    }

    func testWebViewSitsAboveKeyboardAndDoesNotPan() {
        let console = ConsoleWebViewController(webView: WKWebView())
        console.loadViewIfNeeded()
        let web = console.view.subviews.compactMap { $0 as? WKWebView }.first
        XCTAssertNotNil(web)
        guard let web else { return }

        XCTAssertEqual(console.overrideUserInterfaceStyle, .light,
                       "控制台网页是浅色，状态栏那一条要铺白，不能跟着系统深色留黑边")
        XCTAssertTrue(pins(console.view, web.bottomAnchor, console.view.bottomAnchor),
                      "网页底边钉在屏幕底，收起键盘时不能在底栏下面留一条黑边")
        XCTAssertFalse(pins(console.view, web.bottomAnchor, console.view.keyboardLayoutGuide.topAnchor),
                       "键盘布局引导在真机上没有把页面抬起来")
        XCTAssertTrue(pins(console.view, web.topAnchor, console.view.safeAreaLayoutGuide.topAnchor),
                      "网页顶边要让开状态栏")
        XCTAssertEqual(console.keyboardOverlap, 0)

        console.applyKeyboardOverlap(320)
        XCTAssertEqual(console.keyboardOverlap, 320, "键盘盖住多少，网页就从底部缩短多少")
        let lifted = console.view.constraints.first { c in
            (c.firstAnchor === web.bottomAnchor && c.secondAnchor === console.view.bottomAnchor)
                || (c.firstAnchor === console.view.bottomAnchor && c.secondAnchor === web.bottomAnchor)
        }
        XCTAssertEqual(lifted?.constant, -320, "底边常量要等于键盘重叠，页面才从键盘上方结束")

        let scroll = web.scrollView
        XCTAssertFalse(scroll.isScrollEnabled)
        XCTAssertEqual(scroll.contentInsetAdjustmentBehavior, .never)

        scroll.contentSize = CGSize(width: 2000, height: 4000)
        scroll.contentOffset = CGPoint(x: 48, y: 20)
        scroll.contentInset = UIEdgeInsets(top: 0, left: 0, bottom: 120, right: 0)
        XCTAssertEqual(scroll.contentOffset, .zero, "聚焦输入框不准把整页平移")
        XCTAssertEqual(scroll.contentInset, .zero, "键盘避让不准再给网页滚动视图加内边距")
    }
}
