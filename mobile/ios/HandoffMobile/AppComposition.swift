import Foundation

// App 级装配：Go 核单例（LiveConnectCore）、安全存储、配对服务、CookieBridge（唯一 WKWebView）、
// 机器列表模型、推送登记器（B432）。进程内只建一次（I7 条 38）。
final class AppComposition {
    static private(set) var current: AppComposition?

    let core: ConnectCore
    let store: SecureStore
    let pairingService: PairingService
    let currentMachine: CurrentMachineStore
    let cookieBridge: CookieBridge
    let machineList: MachineListModel
    let pushRegistrar: PushRegistrar

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
        // 推送登记以桥接成功进入的机器为准，保留新版壳的持久选择与 APNs 补报。
        let registrar = PushRegistrar(core: core, activeMachine: { [weak cookieBridge] in
            cookieBridge?.currentMachine
        })
        self.pushRegistrar = registrar
        cookieBridge.onEntered = { [weak registrar] in registrar?.retryPending() }
        Log.shell.info("App 级装配完成")
    }

    // startPushRegistration 申请通知授权并在获准后向系统注册远程通知（B432）。
    // 幂等：重复调用只是再问一次系统（授权态缓存，不会重复弹窗）。
    func startPushRegistration() {
        pushRegistrar.requestAuthorizationAndRegister()
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
