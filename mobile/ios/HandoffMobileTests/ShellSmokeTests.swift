import XCTest

// 构建闸烟测：真实 gomobile 产物可从测试包调用。能变红：产物与冻结面错配/链接失败。
final class ShellSmokeTests: XCTestCase {
    func testBindingsCallableFromTestBundle() {
        XCTAssertEqual(BindMachineCount(), 0)
        XCTAssertNil(BindMachineAt(0))
        XCTAssertNil(BindMachineAt(-1))
        var err: NSError?
        _ = BindClose(&err)
    }
}
