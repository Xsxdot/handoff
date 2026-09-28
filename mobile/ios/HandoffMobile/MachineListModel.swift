import Foundation

// 机器列表屏模型：按 0..<count 迭代取机器（条 6，nil 跳过、不越界崩溃）；
// 离线机可见不可进入（条 32）；在线机进入走 CookieBridge 的 I3 序列。
// 生产调用方 = MachineListViewController。
final class MachineListModel {
    private let core: ConnectCore
    private let bridge: CookieBridge

    init(core: ConnectCore, bridge: CookieBridge) {
        self.core = core
        self.bridge = bridge
    }

    func machines() -> [MachineView] {
        var out: [MachineView] = []
        for i in 0..<core.machineCount() {
            if let m = core.machineAt(i) { out.append(m) }   // nil 跳过
        }
        return out
    }

    func enter(index: Int, completion: @escaping (Result<Void, ShellError>) -> Void) {
        let list = machines()
        guard index >= 0, index < list.count else {
            Log.shell.error("进入越界索引 index=\(index, privacy: .public)")
            completion(.failure(.offline("invalid-index")))
            return
        }
        let m = list[index]
        guard m.online else {
            Log.shell.error("点击离线机，拒绝进入 name=\(m.name, privacy: .public)")
            completion(.failure(.offline(m.name)))
            return
        }
        bridge.enter(machine: m.name, online: true, completion: completion)
    }
}
