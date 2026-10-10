import XCTest
@testable import HandoffMobile

// B432 验收② 的深链半边：点通知取 agentd 序列化的 deep_link；
// 无 handoff 键 / 键缺失 / 值为空 → nil（UI 落工作台「需要你处理」）。
final class PushDeepLinkTests: XCTestCase {
    func testRouteReadsHandoffDeepLink() {
        let handoff: [String: Any] = ["deep_link": "/cards?card=B432",
                                      "event_type": "decision", "ref_id": "7"]
        let userInfo: [AnyHashable: Any] = ["handoff": handoff, "aps": ["badge": 3]]
        XCTAssertEqual(PushDeepLink.route(from: userInfo), "/cards?card=B432")
    }

    func testRouteWithoutDeepLinkIsNil() {
        let userInfo: [AnyHashable: Any] = ["handoff": ["event_type": "mention"],
                                            "aps": ["badge": 1]]
        XCTAssertNil(PushDeepLink.route(from: userInfo), "缺 deep_link → 落工作台")
    }

    func testRouteEmptyDeepLinkIsNil() {
        let userInfo: [AnyHashable: Any] = ["handoff": ["deep_link": ""]]
        XCTAssertNil(PushDeepLink.route(from: userInfo), "空深链等同无深链，不得导航到空串")
    }

    func testRouteWithoutHandoffKeyIsNil() {
        XCTAssertNil(PushDeepLink.route(from: ["aps": ["badge": 1]]))
        XCTAssertNil(PushDeepLink.route(from: [:]))
    }
}
