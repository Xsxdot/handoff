import Foundation

// 壳对 Go 核的窄消费面（contract §3.1 七函数）。生产实现 = LiveConnectCore；
// 测试注入假实现。壳只经这七个方法与核交互（I6）。
protocol ConnectCore: AnyObject {
    func pair(_ bundleJSON: String) throws
    func machineCount() -> Int
    func machineAt(_ index: Int) -> MachineView?
    func origin(_ machine: String) throws -> String
    func sessionCookie(_ machine: String) throws -> String
    func switchMachine(_ machine: String) throws -> String
    func close() throws
}

// 七函数的生产实现：逐字包装 gomobile C 函数。
// 关键（已核）：这些 C 函数在 Swift 里**不 throwing**，末参是 NSErrorPointer，
// 必须显式检查 NSError 输出；不要写 try。
final class LiveConnectCore: ConnectCore {
    func pair(_ bundleJSON: String) throws {
        var err: NSError?
        let ok = BindPair(bundleJSON, &err)
        if !ok { throw err ?? NSError(domain: "BindPair", code: -1) }
    }

    func machineCount() -> Int { Int(BindMachineCount()) }

    func machineAt(_ index: Int) -> MachineView? {
        // 越界/负索引经绑定面返回 nil；这里不越界访问（contract §4.1 条 6）。
        guard let m = BindMachineAt(index) else { return nil }
        return MachineView(name: m.name, origin: m.origin, online: m.online)
    }

    func origin(_ machine: String) throws -> String {
        var err: NSError?
        let s = BindOrigin(machine, &err)
        if let e = err { throw e }
        return s
    }

    func sessionCookie(_ machine: String) throws -> String {
        var err: NSError?
        let s = BindSessionCookie(machine, &err)
        if let e = err { throw e }
        return s
    }

    func switchMachine(_ machine: String) throws -> String {
        var err: NSError?
        let s = BindSwitchMachine(machine, &err)
        if let e = err { throw e }
        return s
    }

    func close() throws {
        var err: NSError?
        let ok = BindClose(&err)
        if !ok, let e = err { throw e }   // Close 幂等：无 error 的 false 不算失败
    }
}
