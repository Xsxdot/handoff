import XCTest
import WebKit
@testable import HandoffMobile

final class CookieBridgeTests: XCTestCase {
    private func make(g recorder: CallRecorder) -> (CookieBridge, FakeConnectCore, FakeCookieJar, FakeOriginLoader) {
        let core = FakeConnectCore(recorder: recorder)
        let jar = FakeCookieJar(recorder: recorder)
        let loader = FakeOriginLoader(recorder: recorder)
        return (CookieBridge(core: core, jar: jar, loader: loader), core, jar, loader)
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
                        "sessionCookie:A", "set", "load:http://127.0.0.1:50000"])   // 条 19
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
}
