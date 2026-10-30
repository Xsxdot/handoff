import Foundation

// App 级装配：Go 核单例（LiveConnectCore）、安全存储、配对服务、CookieBridge（唯一 WKWebView）、
// 机器列表模型。进程内只建一次（I7 条 38）。
final class AppComposition {
    static private(set) var current: AppComposition?

    let core: ConnectCore
    let store: SecureStore
    let pairingService: PairingService
    let currentMachine: CurrentMachineStore
    let cookieBridge: CookieBridge
    let machineList: MachineListModel

    static func makeIfNeeded() -> AppComposition {
        if let c = current { return c }
        let c = AppComposition()
        current = c
        return c
    }

    private convenience init() {
        let core = LiveConnectCore()
        let store = KeychainSecureStore()
        let currentMachine = UserDefaultsCurrentMachineStore()
        self.init(core: core, store: store, currentMachine: currentMachine,
                  cookieBridge: CookieBridge(core: core))
    }

    init(core: ConnectCore, store: SecureStore, currentMachine: CurrentMachineStore,
         cookieBridge: CookieBridge) {
        self.core = core
        self.store = store
        self.currentMachine = currentMachine
        self.pairingService = PairingService(core: core, store: store, selection: currentMachine)
        self.cookieBridge = cookieBridge
        self.machineList = MachineListModel(core: core, bridge: cookieBridge, selection: currentMachine)
        Log.shell.info("App 级装配完成")
    }

    // App 终止：关核（幂等）。I7 条 39。
    func shutdown() {
        do {
            try core.close()
            Log.shell.info("Go 核已关闭")
        } catch {
            Log.shell.error("Go 核关闭失败：\(String(describing: error), privacy: .public)")
        }
    }
}
