import Foundation
@testable import HandoffMobile

// 统一调用记录器：让跨对象的调用序可断言（I3 承重）。
final class CallRecorder {
    private(set) var calls: [String] = []
    func record(_ s: String) { calls.append(s) }
}

final class FakeConnectCore: ConnectCore {
    var machines: [MachineView] = []
    var nilIndices: Set<Int> = []   // 界内但 machineAt 返回 nil 的索引（模拟核的内存/平台缺口）
    var pairError: Error?
    var switchError: Error?
    var sessionError: Error?
    var switchOrigin = "http://127.0.0.1:50000"
    var cookieValue = "cookie-value-123"
    // B432：设备登记上报的记账与可注入故障（防假送达断言用）。
    var pushError: Error?
    private(set) var registeredPushes: [(machine: String, deviceID: String, pushHandle: String)] = []
    private let recorder: CallRecorder?

    init(recorder: CallRecorder? = nil) { self.recorder = recorder }

    func pair(_ bundleJSON: String) throws {
        recorder?.record("pair:\(bundleJSON)")
        if let e = pairError { throw e }
    }
    func machineCount() -> Int { machines.count }
    func machineAt(_ index: Int) -> MachineView? {
        guard index >= 0 && index < machines.count else { return nil }
        if nilIndices.contains(index) { return nil }
        return machines[index]
    }
    func origin(_ machine: String) throws -> String { switchOrigin }
    func sessionCookie(_ machine: String) throws -> String {
        recorder?.record("sessionCookie:\(machine)")
        if let e = sessionError { throw e }
        return cookieValue
    }
    func switchMachine(_ machine: String) throws -> String {
        recorder?.record("switchMachine:\(machine)")
        if let e = switchError { throw e }
        return switchOrigin
    }
    func registerPushDevice(_ machine: String, deviceID: String, pushHandle: String) throws {
        recorder?.record("registerPushDevice:\(machine):\(deviceID)")
        if let e = pushError { throw e }
        registeredPushes.append((machine: machine, deviceID: deviceID, pushHandle: pushHandle))
    }
    func close() throws { recorder?.record("close") }
}

final class FakeSecureStore: SecureStore {
    var stored: String?
    var saveError: Error?
    var loadError: Error?
    private let recorder: CallRecorder?
    init(recorder: CallRecorder? = nil) { self.recorder = recorder }
    func save(_ value: String) throws {
        recorder?.record("save")
        if let e = saveError { throw e }
        stored = value
    }
    func load() throws -> String? {
        recorder?.record("load")
        if let e = loadError { throw e }
        return stored
    }
    func delete() throws { recorder?.record("delete"); stored = nil }
}
