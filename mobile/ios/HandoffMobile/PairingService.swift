import Foundation

// 配对缝（contract §3.2）：扫码与粘贴**归一**到 pair(bundleJSON:)。
// 成功 → 原始 bundle 写安全存储 + 核登记机器；失败 → 不落存储、不进列表（I1/I2）。
// 被配对屏（PairingViewController 经 PairEntry）调用。
final class PairingService {
    private let core: ConnectCore
    private let store: SecureStore
    private let selection: CurrentMachineStore?

    init(core: ConnectCore, store: SecureStore, selection: CurrentMachineStore? = nil) {
        self.core = core
        self.store = store
        self.selection = selection
    }

    // 唯一配对入口。仅去首尾空白，其余逐字透传给 Go 核（条 10），
    // 成功后把原始（trim 后）字符串整体写安全存储（条 11）。
    // 任一步失败抛 ShellError.pairing，调用方不得进列表（条 8/9）。
    func pair(bundleJSON raw: String) throws {
        let bundle = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        Log.shell.info("配对入口载荷 bytes=\(bundle.utf8.count, privacy: .public)")
        do {
            try core.pair(bundle)
        } catch {
            Log.shell.error("配对失败（不落存储、不进列表）")
            throw ShellError.pairing(String(describing: error))
        }
        do {
            try store.save(bundle)
        } catch {
            Log.shell.error("配对串写入安全存储失败")
            throw ShellError.pairing(String(describing: error))
        }
        if let selection {
            do { try selection.clear() }
            catch { Log.shell.error("新配对已保存但无法清除旧机器选择") }
        }
        Log.shell.info("配对成功且已持久化")
    }

    // 启动路径（I2 条 15/16）：有存储 → Pair(stored) 返回 true；无 → false。
    // Pair(stored) 失败向上抛 pairing（调用方回配对屏 + 可重试，不复用半态）。
    func pairFromStoredBundle() throws -> Bool {
        guard let stored = try store.load() else {
            Log.shell.info("无已存配对串：走配对屏")
            return false
        }
        do {
            try core.pair(stored)
        } catch {
            Log.shell.error("已存配对串校验失败：走配对屏")
            throw ShellError.pairing(String(describing: error))
        }
        // Startup validation re-registers the existing pairing; it is not a replacement.
        // Preserve the one-time machine choice so a normal restart resumes that machine.
        Log.shell.info("已存配对串校验成功")
        return true
    }
}
