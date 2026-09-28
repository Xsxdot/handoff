import XCTest
@testable import HandoffMobile

// S6：ConnectCore 生产实现可调、空核语义正确。能变红：包装漏检查 NSError / 越界崩溃。
final class ConnectCoreTests: XCTestCase {
    func testLiveCoreStartsEmptyAndCallable() throws {
        let core = LiveConnectCore()
        XCTAssertEqual(core.machineCount(), 0)
        XCTAssertNil(core.machineAt(0))
        XCTAssertNil(core.machineAt(-1))
        try core.close()
    }
}
