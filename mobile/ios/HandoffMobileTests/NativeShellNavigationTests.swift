import XCTest
@testable import HandoffMobile

final class NativeShellNavigationTests: XCTestCase {
    func testSwitchActionRequiresExactOriginLiteralPathAndMainFrame() {
        let origin = "http://127.0.0.1:41000"
        XCTAssertTrue(NativeShellNavigation.isSwitchRequest(
            origin: origin,
            target: URL(string: "http://127.0.0.1:41000/_handoff/native/switch-machine")!,
            mainFrame: true))
        for raw in [
            "http://127.0.0.1:41001/_handoff/native/switch-machine",
            "http://127.0.0.1:41000/_handoff/native/switch-machine/extra",
            "http://127.0.0.1:41000/_handoff/native/%73witch-machine",
            "http://127.0.0.1:41000/_handoff/native/switch-machine?x=1",
            "http://127.0.0.1:41000/_handoff/native/switch-machine#x",
            "http://user@127.0.0.1:41000/_handoff/native/switch-machine",
            "http://127.0.0.1.evil:41000/_handoff/native/switch-machine",
        ] {
            XCTAssertFalse(NativeShellNavigation.isSwitchRequest(
                origin: origin, target: URL(string: raw)!, mainFrame: true), raw)
        }
        XCTAssertFalse(NativeShellNavigation.isSwitchRequest(
            origin: origin,
            target: URL(string: "http://127.0.0.1:41000/_handoff/native/switch-machine")!,
            mainFrame: false))
    }
}
