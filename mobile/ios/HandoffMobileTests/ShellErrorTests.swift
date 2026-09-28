import XCTest
@testable import HandoffMobile

final class ShellErrorTests: XCTestCase {
    func testClassificationByCallSiteAndOnline() {
        XCTAssertEqual(ErrorClassifier.classify(.pair, machine: "m", online: true), .pairing("m"))
        XCTAssertEqual(ErrorClassifier.classify(.switchMachine, machine: "m", online: true), .exchange("m"))
        XCTAssertEqual(ErrorClassifier.classify(.switchMachine, machine: "m", online: false), .offline("m"))
        XCTAssertEqual(ErrorClassifier.classify(.sessionCookie, machine: "m", online: true), .exchange("m"))
    }

    func testFourClassesAreRetryableAndHaveCopy() {
        for e in [ShellError.pairing("m"), .offline("m"), .exchange("m"), .expired("m")] {
            XCTAssertTrue(e.canRetry)
            XCTAssertFalse(e.title.isEmpty)
            XCTAssertFalse(e.message.isEmpty)
        }
    }
}
