import Foundation

// 机器列表屏模型：按 0..<count 迭代取机器（条 6，nil 跳过、不越界崩溃）；
// 离线机可见不可进入（条 32）；在线机进入走 CookieBridge 的 I3 序列。
// 生产调用方 = MachineListViewController。
final class MachineListModel {
    private let core: ConnectCore
    private let bridge: CookieBridge
    private let selection: CurrentMachineStore?

    init(core: ConnectCore, bridge: CookieBridge, selection: CurrentMachineStore? = nil) {
        self.core = core
        self.bridge = bridge
        self.selection = selection
    }

    var currentMachine: String? { selection?.read() }
    var activeOrigin: String? { bridge.activeOrigin }

    /// Manual offline retry refreshes the opaque saved pairing, then checks only saved A.
    func refreshSavedMachine(revalidatePairing: () throws -> Bool) -> Result<MachineView, ShellError> {
        guard let selected = selection?.read(), !selected.isEmpty else {
            return .failure(.offline("no-saved-machine"))
        }
        do {
            guard try revalidatePairing() else { return .failure(.pairing("no-stored-pairing")) }
        } catch let error as ShellError {
            return .failure(error)
        } catch {
            return .failure(.pairing("stored-pairing-validation"))
        }
        guard let machine = machines().first(where: { $0.name == selected && $0.online }) else {
            Log.shell.warning("手动配对校验后原机器仍不可用 machine=\(selected, privacy: .public)")
            return .failure(.offline(selected))
        }
        return .success(machine)
    }

    /// Return only the saved online machine for cold-start recovery; never select a fallback.
    func resumeIndex() -> Int? {
        guard let selected = selection?.read() else { return nil }
        return machines().firstIndex(where: { $0.name == selected && $0.online })
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
        enter(machine: list[index].name, completion: completion)
    }

    /// Resolve a retry/restore by the saved machine name against the live list, never a stale row index.
    func enter(machine name: String, completion: @escaping (Result<Void, ShellError>) -> Void) {
        guard let m = machines().first(where: { $0.name == name }) else {
            Log.shell.error("当前机器已不在列表，拒绝回落 machine=\(name, privacy: .public)")
            completion(.failure(.offline(name)))
            return
        }
        guard m.online else {
            Log.shell.error("点击离线机，拒绝进入 name=\(m.name, privacy: .public)")
            completion(.failure(.offline(m.name)))
            return
        }
        bridge.enter(machine: m.name, online: true, completion: completion)
    }
}
