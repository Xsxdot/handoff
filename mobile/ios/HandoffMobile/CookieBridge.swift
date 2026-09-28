import Foundation
import WebKit

// main 队列执行器缝：WKHTTPCookieStore.set 的 completion 线程由平台决定（非主线程），
// 其中触发的 webView.load 必须在主线程（WebKit 硬要求，`mainDocumentURL`/`WKWebView` 非线程安全）。
// 生产 = DispatchQueue.main.async；测试注入同步执行器把「派发到主队列」这一步确定化。
typealias MainQueueExecutor = (@escaping () -> Void) -> Void

// 导航加载缝（测试可注入假 loader 断言「失败不 load」）。
protocol OriginLoader: AnyObject { func load(origin: String) }

final class WKWebViewOriginLoader: OriginLoader {
    private let webView: WKWebView
    init(webView: WKWebView) { self.webView = webView }
    func load(origin: String) {
        guard let url = URL(string: origin) else {
            Log.shell.error("origin 非法，放弃导航")
            return
        }
        webView.load(URLRequest(url: url))
    }
}

// cookie 罐缝：生产包装 WKHTTPCookieStore，测试注入假实现。
protocol CookieJar: AnyObject {
    func allCookies(_ completion: @escaping ([HTTPCookie]) -> Void)
    func delete(_ cookie: HTTPCookie, completion: @escaping () -> Void)
    func set(_ cookie: HTTPCookie, completion: @escaping () -> Void)
}

// 为什么清罐用「枚举 + 逐个 delete」而非 deleteAllCookies()：
// deleteAllCookies 无完成回调，无法保证「清完再注入」；逐个 delete 有 completion，
// 用 DispatchGroup 汇聚成确定性完成信号（contract §3.4 以可观测不变量为准，机制归子卡）。
final class WebKitCookieJar: CookieJar {
    private let store: WKHTTPCookieStore
    init(store: WKHTTPCookieStore) { self.store = store }
    func allCookies(_ completion: @escaping ([HTTPCookie]) -> Void) { store.getAllCookies(completion) }
    func delete(_ cookie: HTTPCookie, completion: @escaping () -> Void) {
        store.delete(cookie, completionHandler: completion)
    }
    func set(_ cookie: HTTPCookie, completion: @escaping () -> Void) {
        store.setCookie(cookie, completionHandler: completion)
    }
}

// 壳注入 cookie 的固定属性（contract §3.4）。name 与 agentd auth.go:29 对齐（条 31）。
enum ShellCookie {
    static let name = "handoff_session"
    static let domain = "127.0.0.1"
    static let path = "/"

    // 平台坑（已核）：显式传 .secure（任何值）会让 isSecure 变 true——
    // 要 Secure=false（条 26）必须**不设** .secure 键。
    // HttpOnly 客户端不可设（只读属性，无 property key）→ §9 残余。
    static func make(value: String) -> HTTPCookie? {
        let props: [HTTPCookiePropertyKey: Any] = [
            .name: name,
            .value: value,
            .path: path,
            .domain: domain,
            .sameSitePolicy: HTTPCookieStringPolicy.sameSiteLax,
        ]
        return HTTPCookie(properties: props)
    }
}

// CookieBridge —— 拥有唯一 WKWebView 与其 httpCookieStore，实现 I3 承载调用序。
// 被「机器列表屏·进入」（MachineListModel ← MachineListViewController）调用。
// 单槽：任何时刻一罐只装一机 cookie（I3）。
final class CookieBridge {
    let webView: WKWebView
    private let core: ConnectCore
    private let jar: CookieJar
    private let loader: OriginLoader
    private let onMain: MainQueueExecutor

    // 生产装配：本类拥有 webView 与其 cookie store。
    convenience init(core: ConnectCore) {
        let config = WKWebViewConfiguration()
        let webView = WKWebView(frame: .zero, configuration: config)
        self.init(core: core,
                  jar: WebKitCookieJar(store: config.websiteDataStore.httpCookieStore),
                  loader: WKWebViewOriginLoader(webView: webView),
                  webView: webView)
    }

    // 测试缝：注入假 jar / 假 loader / 同步化的 main 队列执行器。
    init(core: ConnectCore, jar: CookieJar, loader: OriginLoader, webView: WKWebView = WKWebView(),
         onMain: @escaping MainQueueExecutor = { DispatchQueue.main.async(execute: $0) }) {
        self.core = core
        self.jar = jar
        self.loader = loader
        self.webView = webView
        self.onMain = onMain
    }

    // I3 承载调用序：SwitchMachine → 清罐（等完成）→ SessionCookie → 注入（等完成）→ load。
    // 任一步失败：completion(.failure) 且绝不 load（条 21）。离线直接拒绝、不触碰核（条 32）。
    func enter(machine: String, online: Bool, completion: @escaping (Result<Void, ShellError>) -> Void) {
        guard online else {
            Log.shell.error("拒绝进入离线机器 name=\(machine, privacy: .public)")
            completion(.failure(.offline(machine)))
            return
        }
        Log.shell.info("进入机器 name=\(machine, privacy: .public)")
        let origin: String
        do {
            origin = try core.switchMachine(machine)
        } catch {
            Log.shell.error("切机失败 name=\(machine, privacy: .public)")
            completion(.failure(ErrorClassifier.classify(.switchMachine, machine: machine, online: online)))
            return
        }
        clearJar { [weak self] in
            self?.inject(machine: machine, origin: origin, completion: completion)
        }
    }

    private func clearJar(completion: @escaping () -> Void) {
        jar.allCookies { [weak self] cookies in
            guard let self else { return }
            guard !cookies.isEmpty else {
                Log.shell.debug("cookie jar 已空")
                completion()
                return
            }
            let group = DispatchGroup()
            for c in cookies {
                group.enter()
                self.jar.delete(c) { group.leave() }
            }
            group.notify(queue: .main) {
                Log.shell.debug("清罐完成 count=\(cookies.count, privacy: .public)")
                completion()
            }
        }
    }

    private func inject(machine: String, origin: String,
                        completion: @escaping (Result<Void, ShellError>) -> Void) {
        let value: String
        do {
            value = try core.sessionCookie(machine)
        } catch {
            Log.shell.error("取会话 cookie 失败 name=\(machine, privacy: .public)")
            completion(.failure(ErrorClassifier.classify(.sessionCookie, machine: machine, online: true)))
            return
        }
        guard !value.isEmpty, let cookie = ShellCookie.make(value: value) else {
            Log.shell.error("会话 cookie 为空/不可构造 name=\(machine, privacy: .public)")
            completion(.failure(.exchange(machine)))
            return
        }
        jar.set(cookie) { [weak self] in
            guard let self else { return }
            // set 的 completion 线程由平台决定，load 必须在主线程 → 统一经 onMain 派发。
            self.onMain { [weak self] in
                guard let self else { return }
                Log.shell.info("cookie 注入完成并导航 name=\(machine, privacy: .public)")
                self.loader.load(origin: origin)
                completion(.success(()))
            }
        }
    }
}
