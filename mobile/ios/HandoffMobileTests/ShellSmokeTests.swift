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

    // B432：设备登记导出面可链接可调用（缝 S1 的壳烟测）。空核/已关核必须报错，
    // 绝不返回 true 假装登记成功（防假送达）。
    func testRegisterPushDeviceCallableFromTestBundle() {
        var err: NSError?
        let ok = BindRegisterPushDevice("ghost", "device-1", "00ab", &err)
        XCTAssertFalse(ok, "未配对机器登记必须失败")
        XCTAssertNotNil(err, "失败必须带可诊断错误")
    }
}
