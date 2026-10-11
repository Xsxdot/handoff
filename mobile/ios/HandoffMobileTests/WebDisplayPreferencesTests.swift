// Real WebKit roundtrip: changes must survive a new web view at a different port.
// The fixture uses inline documents only; no pairing credentials or server state.
import XCTest
import WebKit
@testable import HandoffMobile

final class WebDisplayPreferencesTests: XCTestCase, WKNavigationDelegate {
    private var loaded: XCTestExpectation?

    // Each test owns its WebKit storage; repeated runs must not inherit an older
    // loopback document's preferences from the simulator's persistent store.
    private func isolatedWebView() -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        return WKWebView(frame: .zero, configuration: configuration)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        loaded?.fulfill()
        loaded = nil
    }

    private func load(_ webView: WKWebView, origin: String) {
        loaded = expectation(description: "document loaded")
        webView.navigationDelegate = self
        webView.loadHTMLString("<html><body>preferences</body></html>", baseURL: URL(string: origin)!)
        wait(for: [loaded!], timeout: 5)
    }

    private func evaluate(_ webView: WKWebView, _ source: String) -> Any? {
        let done = expectation(description: "script finished")
        var value: Any?
        webView.evaluateJavaScript(source) { result, error in
            XCTAssertNil(error)
            value = result
            done.fulfill()
        }
        wait(for: [done], timeout: 5)
        return value
    }

    func testProjectVisibilitySurvivesNewLoopbackPort() {
        let suite = "display-prefs-test-\(UUID().uuidString)"
        let store = UserDefaults(suiteName: suite)!
        defer { store.removePersistentDomain(forName: suite) }
        let raw = #"{"v":1,"hideIdleWorktrees":false,"projectSort":"active","hiddenProjects":["p2"]}"#
        let first = isolatedWebView()
        let prefs = WebDisplayPreferences(store: store)
        prefs.install(on: first, origin: "http://127.0.0.1:49101")
        load(first, origin: "http://127.0.0.1:49101")
        _ = evaluate(first, "localStorage.setItem('handoff.tree.prefs', JSON.stringify(\(raw)))")
        let deadline = Date().addingTimeInterval(2)
        while store.string(forKey: "handoff.tree.prefs") == nil && Date() < deadline {
            RunLoop.current.run(until: Date().addingTimeInterval(0.01))
        }
        XCTAssertEqual(store.string(forKey: "handoff.tree.prefs"), raw)

        let second = isolatedWebView()
        let restored = WebDisplayPreferences(store: store)
        restored.install(on: second, origin: "http://127.0.0.1:49102")
        load(second, origin: "http://127.0.0.1:49102")
        XCTAssertEqual(evaluate(second, "localStorage.getItem('handoff.tree.prefs')") as? String, raw)
        _ = evaluate(second, "localStorage.setItem('unrelated', 'do not persist')")
        XCTAssertNil(store.string(forKey: "unrelated"))
    }

    func testReloadKeepsChangesMadeAfterInitialRestore() {
        let suite = "display-prefs-reload-\(UUID().uuidString)"
        let store = UserDefaults(suiteName: suite)!
        defer { store.removePersistentDomain(forName: suite) }
        store.set("initial", forKey: "handoff.tree.prefs")
        let view = isolatedWebView()
        let prefs = WebDisplayPreferences(store: store)
        prefs.install(on: view, origin: "http://127.0.0.1:49103")
        load(view, origin: "http://127.0.0.1:49103")
        XCTAssertEqual(evaluate(view, "localStorage.getItem('handoff.tree.prefs')") as? String, "initial")
        _ = evaluate(view, "localStorage.setItem('handoff.tree.prefs','newer')")
        load(view, origin: "http://127.0.0.1:49103")
        XCTAssertEqual(evaluate(view, "localStorage.getItem('handoff.tree.prefs')") as? String, "newer")
    }
}
