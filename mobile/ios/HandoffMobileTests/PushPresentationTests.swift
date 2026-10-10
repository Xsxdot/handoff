import XCTest
@testable import HandoffMobile

// B432 验收①：前台且用户已在控制台（对应面已打开）→ 不弹系统横幅；
// 其余情形交给系统弹。纯函数，三条分支逐条可判。
final class PushPresentationTests: XCTestCase {
    func testForegroundOnRelevantScreenSuppressesBanner() {
        XCTAssertFalse(PushPresentation.shouldPresent(isForeground: true, onRelevantScreen: true),
                       "前台且对应面已打开：不重复弹系统横幅（spec 验收①）")
    }

    func testForegroundElsewhereStillPresents() {
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: true, onRelevantScreen: false),
                      "前台但不在对应面：必须弹（否则用户看不到新待办）")
    }

    func testBackgroundAlwaysPresents() {
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: false, onRelevantScreen: true))
        XCTAssertTrue(PushPresentation.shouldPresent(isForeground: false, onRelevantScreen: false))
    }
}
