import XCTest
@testable import HandoffMobile

// B432 缝 S1 的壳半边：拿到 APNs device 后经 ConnectCore 上报，
// 逐字锁 machine / deviceID / 十六进制串；无活动机器暂存、进入后补报；
// 上报失败保留待报（不吞、不丢）。
final class PushRegistrarTests: XCTestCase {
    // machineRef 是可被闭包按引用捕获的活动机器槽（inout 不能进逃逸闭包）。
    private final class MachineRef {
        var value: String?
        init(_ v: String?) { value = v }
    }

    private func registrar(core: FakeConnectCore, machine: MachineRef) -> PushRegistrar {
        PushRegistrar(core: core, deviceID: "iphone-15",
                      activeMachine: { machine.value })
    }

    func testDidRegisterReportsHexToActiveMachine() {
        let rec = CallRecorder()
        let core = FakeConnectCore(recorder: rec)
        let machine = MachineRef("devbox")
        let r = registrar(core: core, machine: machine)

        r.didRegister(deviceToken: Data([0x0a, 0xbb, 0x01, 0xff]))

        XCTAssertEqual(core.registeredPushes.count, 1)
        guard let got = core.registeredPushes.first else { return XCTFail("未上报") }
        XCTAssertEqual(got.machine, "devbox")
        XCTAssertEqual(got.deviceID, "iphone-15")
        XCTAssertEqual(got.pushHandle, "0abb01ff", "device token 必须转小写十六进制串")
        XCTAssertTrue(rec.calls.contains("registerPushDevice:devbox:iphone-15"))
    }

    func testNoActiveMachinePendsThenReportsOnRetry() {
        let core = FakeConnectCore()
        let machine = MachineRef(nil)
        let r = registrar(core: core, machine: machine)

        r.didRegister(deviceToken: Data([0x01, 0x02]))
        XCTAssertEqual(core.registeredPushes.count, 0, "无活动机器不得乱报")

        machine.value = "devbox"
        r.retryPending()
        XCTAssertEqual(core.registeredPushes.count, 1, "进入机器后必须补报暂存的 device")
        XCTAssertEqual(core.registeredPushes.first?.machine, "devbox")
        XCTAssertEqual(core.registeredPushes.first?.pushHandle, "0102")
    }

    func testRetryWithoutPendingIsNoop() {
        let core = FakeConnectCore()
        let machine = MachineRef("devbox")
        let r = registrar(core: core, machine: machine)
        r.retryPending()
        XCTAssertTrue(core.registeredPushes.isEmpty)
    }

    func testReportFailureKeepsPendingForRetry() {
        let core = FakeConnectCore()
        core.pushError = NSError(domain: "agentd", code: 500)
        let machine = MachineRef(nil)
        let r = registrar(core: core, machine: machine)

        r.didRegister(deviceToken: Data([0x0a]))
        machine.value = "devbox"
        r.retryPending()
        XCTAssertTrue(core.registeredPushes.isEmpty, "失败不得记成已上报（防假送达）")

        core.pushError = nil
        r.retryPending()
        XCTAssertEqual(core.registeredPushes.count, 1, "失败的上报必须可重试")
    }

    func testDeviceIDIsStableAcrossInstances() {
        XCTAssertEqual(PushRegistrar.makeDeviceID(), PushRegistrar.makeDeviceID(),
                       "deviceID 必须稳定，否则每次启动都换一台「设备」")
    }
}
