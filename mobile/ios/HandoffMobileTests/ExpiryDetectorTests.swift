import XCTest
@testable import HandoffMobile

final class ExpiryDetectorTests: XCTestCase {
    func testOnly401IsExpired() {          // 条 34
        XCTAssertTrue(ExpiryDetector.isExpired(httpStatusCode: 401))
        XCTAssertFalse(ExpiryDetector.isExpired(httpStatusCode: 200))
        XCTAssertFalse(ExpiryDetector.isExpired(httpStatusCode: 403))
    }
}
