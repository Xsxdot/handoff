import XCTest
import WebKit
@testable import HandoffMobile

final class CookieBridgeTests: XCTestCase {
    // main 队列执行器在测试里同步化并记账：load 必须经它派发（生产 = DispatchQueue.main.async）。
    private func make(g recorder: CallRecorder) -> (CookieBridge, FakeConnectCore, FakeCookieJar, FakeOriginLoader) {
        let core = FakeConnectCore(recorder: recorder)
        let jar = FakeCookieJar(recorder: recorder)
        let loader = FakeOriginLoader(recorder: recorder)
        let bridge = CookieBridge(core: core, jar: jar, loader: loader,
                                  onMain: { recorder.record("mainAsync"); $0() })
        return (bridge, core, jar, loader)
    }

    func testStrictCallOrderWithOldCookie() {
        let rec = CallRecorder()
        let (bridge, core, jar, _) = make(g: rec)
        core.switchOrigin = "http://127.0.0.1:50000"
        jar.cookies = [HTTPCookie(properties: [.name: "handoff_session", .value: "old",
                                              .path: "/", .domain: "127.0.0.1"])!]
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { if case .failure(let e) = $0 { XCTFail("\(e)") }; exp.fulfill() }
        wait(for: [exp], timeout: 2)
        XCTAssertEqual(rec.calls,
                       ["switchMachine:A", "allCookies", "delete",
                        "sessionCookie:A", "set", "mainAsync",
                        "load:http://127.0.0.1:50000"])   // 条 19
    }

    // F2：load 只经注入的 main 队列执行器发出（set 的 completion 线程由平台决定，load 须回主线程）。
    func testLoadDispatchedThroughMainQueue() {
        let rec = CallRecorder()
        let (bridge, core, _, _) = make(g: rec)
        core.switchOrigin = "http://127.0.0.1:50000"
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in exp.fulfill() }
        wait(for: [exp], timeout: 2)
        guard let mainIdx = rec.calls.firstIndex(of: "mainAsync") else {
            return XCTFail("load 未经主队列执行器派发")
        }
        guard let loadIdx = rec.calls.firstIndex(where: { $0.hasPrefix("load") }) else {
            return XCTFail("未导航")
        }
        XCTAssertLessThan(mainIdx, loadIdx)   // load 发生在主队列派发之内
    }

    func testInjectedCookieAttributes() {
        let rec = CallRecorder()
        let (bridge, core, jar, _) = make(g: rec)
        core.cookieValue = "cv-xyz"
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in exp.fulfill() }
        wait(for: [exp], timeout: 2)
        guard let c = jar.lastSet else { return XCTFail("未注入 cookie") }
        XCTAssertEqual(c.name, "handoff_session")          // 条 23/31
        XCTAssertEqual(c.path, "/")                        // 条 24
        XCTAssertEqual(c.domain, "127.0.0.1")              // 条 25
        XCTAssertFalse(c.isSecure)                         // 条 26（不能传 .secure 键！）
        XCTAssertEqual(c.sameSitePolicy?.rawValue,
                       HTTPCookieStringPolicy.sameSiteLax.rawValue)   // 条 28
        XCTAssertTrue(c.isSessionOnly)                     // 条 29
        XCTAssertEqual(c.value, "cv-xyz")                  // 条 30
        // 条 27（HttpOnly）平台不可设 → §9 残余，不在此判 pass。
    }

    func testSwitchErrorDoesNotLoad() {
        let rec = CallRecorder()
        let (bridge, core, _, _) = make(g: rec)
        core.switchError = NSError(domain: "x", code: 1)
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { if case .failure(.exchange) = $0 {} else { XCTFail("want exchange") }; exp.fulfill() }
        wait(for: [exp], timeout: 2)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })   // 条 21
    }

    func testSessionErrorDoesNotLoad() {
        let rec = CallRecorder()
        let (bridge, core, _, _) = make(g: rec)
        core.sessionError = NSError(domain: "x", code: 2)
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { if case .failure(.exchange) = $0 {} else { XCTFail("want exchange") }; exp.fulfill() }
        wait(for: [exp], timeout: 2)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })   // 条 21
    }

    func testOfflineDoesNotTouchCoreNorNavigate() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: false) { if case .failure(.offline) = $0 {} else { XCTFail("want offline") }; exp.fulfill() }
        wait(for: [exp], timeout: 2)
        XCTAssertTrue(rec.calls.isEmpty)                              // 条 32
    }

    func testClearNeverCompletesMeansNoLoad() {
        let rec = CallRecorder()
        let (bridge, _, jar, _) = make(g: rec)
        jar.deleteCompletionHangs = true
        jar.cookies = [HTTPCookie(properties: [.name: "handoff_session", .value: "old",
                                              .path: "/", .domain: "127.0.0.1"])!]
        let exp = expectation(description: "no completion"); exp.isInverted = true
        bridge.enter(machine: "A", online: true) { _ in exp.fulfill() }
        wait(for: [exp], timeout: 0.5)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })   // 清罐未完成 → 不导航
    }

    // F5：SessionCookie 返回空值 → 错误态且不 load（条 21/33）。
    func testEmptySessionCookieDoesNotLoad() {
        let rec = CallRecorder()
        let (bridge, core, _, _) = make(g: rec)
        core.cookieValue = ""
        let exp = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) {
            if case .failure(.exchange) = $0 {} else { XCTFail("want exchange") }
            exp.fulfill()
        }
        wait(for: [exp], timeout: 2)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })
    }

    // F5：jar.set completion 不回调 → 不导航（条 19/21）。
    func testSetNeverCompletesMeansNoLoad() {
        let rec = CallRecorder()
        let (bridge, _, jar, _) = make(g: rec)
        jar.setCompletionHangs = true
        let exp = expectation(description: "no completion"); exp.isInverted = true
        bridge.enter(machine: "A", online: true) { _ in exp.fulfill() }
        wait(for: [exp], timeout: 0.5)
        XCTAssertFalse(rec.calls.contains { $0.hasPrefix("load") })
    }

    // F5：同机重复进入不误报错误态，两次都导航（条 22）。
    func testReenterSameMachineSucceedsTwice() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        var failures: [ShellError] = []
        let e1 = expectation(description: "first")
        bridge.enter(machine: "A", online: true) {
            if case .failure(let e) = $0 { failures.append(e) }
            e1.fulfill()
        }
        wait(for: [e1], timeout: 2)
        let e2 = expectation(description: "second")
        bridge.enter(machine: "A", online: true) {
            if case .failure(let e) = $0 { failures.append(e) }
            e2.fulfill()
        }
        wait(for: [e2], timeout: 2)
        XCTAssertTrue(failures.isEmpty, "同机重复进入进入错误态：\(failures)")
        XCTAssertEqual(rec.calls.filter { $0 == "load:http://127.0.0.1:50000" }.count, 2)
    }

    // B432 验收②：进入机器后点通知 → 立刻加载 origin+route。
    func testOpenRouteAfterEnterLoadsOriginPlusRoute() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        let e = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in e.fulfill() }
        wait(for: [e], timeout: 2)
        XCTAssertEqual(bridge.currentMachine, "A")

        bridge.openRoute("/cards?card=B432")
        XCTAssertEqual(rec.calls.last, "load:http://127.0.0.1:50000/cards?card=B432")
    }

    // B432 验收② 冷启动路径：还没进机器就点通知 → 路由暂存，enter 成功后补上；
    // onEntered 必须触发（补报 APNs device 靠它）。
    func testOpenRouteBeforeEnterIsPendingAndOnEnteredFires() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        bridge.openRoute("/")
        XCTAssertEqual(rec.calls.contains(where: { $0.hasPrefix("load:") }), false,
                       "没有活动机器时不得空导航")

        var entered = 0
        bridge.onEntered = { entered += 1 }
        let e = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in e.fulfill() }
        wait(for: [e], timeout: 2)
        XCTAssertEqual(rec.calls.last, "load:http://127.0.0.1:50000/", "暂存路由必须在进入时补上")
        XCTAssertEqual(entered, 1, "enter 成功必须回调 onEntered")
    }

    // MARK: - 修刀（2026-10-10）：currentRoute 记账（前台横幅抑制的路由事实源）

    // 进入机器（无暂存路由）→ currentRoute 为工作台首页 "/"。
    func testEnterWithoutPendingRouteSetsCurrentRouteHome() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        let e = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in e.fulfill() }
        wait(for: [e], timeout: 2)
        XCTAssertEqual(bridge.currentRoute, "/", "进入后未点通知：当前路由=工作台首页")
    }

    // 进入机器携带暂存深链 → currentRoute 落该深链。
    func testEnterWithPendingRouteSetsCurrentRoute() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        bridge.openRoute("/cards?card=B432")
        let e = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in e.fulfill() }
        wait(for: [e], timeout: 2)
        XCTAssertEqual(bridge.currentRoute, "/cards?card=B432")
    }

    // 已进入后点通知 → currentRoute 跟随 openRoute。
    func testOpenRouteAfterEnterUpdatesCurrentRoute() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        let e = expectation(description: "enter")
        bridge.enter(machine: "A", online: true) { _ in e.fulfill() }
        wait(for: [e], timeout: 2)
        bridge.openRoute("/cards?card=B999")
        XCTAssertEqual(bridge.currentRoute, "/cards?card=B999")
    }

    // 未进入时 currentRoute 为空串（不是 "/"——还没有任何页面在场）。
    func testCurrentRouteEmptyBeforeEnter() {
        let rec = CallRecorder()
        let (bridge, _, _, _) = make(g: rec)
        XCTAssertEqual(bridge.currentRoute, "", "未进入时无路由可言")
    }
}

extension CookieBridgeTests {
    private func resumeFixture(_ rec: CallRecorder, jar: FakeCookieJar? = nil) -> (CookieBridge, FakeConnectCore) {
        let core = FakeConnectCore(recorder: rec)
        let bridge = CookieBridge(core: core, jar: jar ?? FakeCookieJar(recorder: rec),
                                  loader: FakeOriginLoader(recorder: rec))
        let entered = expectation(description: "entered")
        bridge.enter(machine: "A", online: true) { _ in entered.fulfill() }
        wait(for: [entered], timeout: 2)
        let ready = expectation(description: "event listener ready")
        bridge.webView.evaluateJavaScript("window.resumeCount=0; document.addEventListener('handoff-foreground',()=>window.resumeCount++)") { _, error in
            XCTAssertNil(error); ready.fulfill()
        }
        wait(for: [ready], timeout: 3)
        return (bridge, core)
    }

    private func assertResumeEvents(_ bridge: CookieBridge, count: Int) {
        let event = expectation(description: "event observed")
        bridge.webView.evaluateJavaScript("window.resumeCount") { value, error in
            XCTAssertNil(error); XCTAssertEqual(value as? Int, count); event.fulfill()
        }
        wait(for: [event], timeout: 3)
    }

    func testResumeKeepsDocumentAndEmitsForegroundAfterCoreRecovery() {
        let rec = CallRecorder()
        let (bridge, _) = resumeFixture(rec)
        let before = rec.calls.count
        let resumed = expectation(description: "core and web resumed")
        bridge.resumeConnection { success in XCTAssertTrue(success); resumed.fulfill() }
        wait(for: [resumed], timeout: 5)
        XCTAssertEqual(Array(rec.calls.dropFirst(before)), ["resume:A"])
        XCTAssertEqual(bridge.currentOrigin, "http://127.0.0.1:50000")
        assertResumeEvents(bridge, count: 1)
    }

    func testFailedCoreResumeDoesNotWakeTheDocument() {
        let (bridge, core) = resumeFixture(CallRecorder())
        core.resumeError = NSError(domain: "resume failed", code: 1)
        let resumed = expectation(description: "resume rejected")
        bridge.resumeConnection { success in XCTAssertFalse(success); resumed.fulfill() }
        wait(for: [resumed], timeout: 5)
        assertResumeEvents(bridge, count: 0)
    }

    func testMachineSwitchWhileResumingDiscardsTheOldWake() {
        let (bridge, core) = resumeFixture(CallRecorder())
        core.onResume = {
            let entered = DispatchSemaphore(value: 0)
            DispatchQueue.main.async {
                core.switchOrigin = "http://127.0.0.1:50001"
                bridge.enter(machine: "B", online: true) { _ in entered.signal() }
            }
            XCTAssertEqual(entered.wait(timeout: .now() + 2), .success)
        }
        let resumed = expectation(description: "old wake discarded")
        bridge.resumeConnection { success in XCTAssertFalse(success); resumed.fulfill() }
        wait(for: [resumed], timeout: 5)
        XCTAssertEqual(bridge.currentMachine, "B")
        XCTAssertEqual(bridge.currentOrigin, "http://127.0.0.1:50001")
        assertResumeEvents(bridge, count: 0)
    }

    func testMachineSwitchStillClearingCookiesDiscardsTheOldWake() {
        let rec = CallRecorder()
        let jar = FakeCookieJar(recorder: rec)
        let (bridge, core) = resumeFixture(rec, jar: jar)
        jar.deleteCompletionHangs = true
        core.onResume = {
            let switching = DispatchSemaphore(value: 0)
            DispatchQueue.main.async {
                core.switchOrigin = "http://127.0.0.1:50001"
                bridge.enter(machine: "B", online: true) { _ in XCTFail("cookie clear should still be pending") }
                switching.signal()
            }
            XCTAssertEqual(switching.wait(timeout: .now() + 2), .success)
        }
        let resumed = expectation(description: "in-flight switch rejects old wake")
        bridge.resumeConnection { success in XCTAssertFalse(success); resumed.fulfill() }
        wait(for: [resumed], timeout: 5)
        // Public context still describes A until B's cookie transaction finishes.
        XCTAssertEqual(bridge.currentMachine, "A")
        XCTAssertEqual(bridge.currentOrigin, "http://127.0.0.1:50000")
        assertResumeEvents(bridge, count: 0)
    }
}
