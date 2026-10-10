import Foundation
import UIKit
import UserNotifications

// PushRegistrar —— iOS 推送授权与设备登记（B432 APNs MVP 的壳半边）。
//
// 职责：申请通知授权 → 获准后向系统注册远程通知 → 收到 APNs device 转十六进制
// 串 → 经 ConnectCore.registerPushDevice 上报给当前活动机器；暂无活动机器或
// 上报失败时暂存，进入机器后补报。
//
// 边界：
//   - **不发 HTTP**（壳禁自建 HTTP 客户端，回环门禁承重属性）——上报唯一通路是
//     Go 核；主令牌只在核手里，壳碰不到
//   - 不做业务判定：什么时候推、推什么由 agentd 侧 fanout 决定
//   - 拒权 / 注册失败 / 无 token：一律**静默降级站内**（spec 验收③），不报假送达
//   - 日志只落 machine / deviceID / 长度，绝不落十六进制串本身
final class PushRegistrar {
    private let core: ConnectCore
    private let deviceID: String
    private let activeMachine: () -> String?
    // pendingHandle：已拿到但还没能上报（无活动机器 / 上报失败）的 device 串。
    private var pendingHandle: String?

    // 参数：core 壳↔核窄面；deviceID 本机稳定设备 id（默认取持久化 id）；
    // activeMachine 返回当前已进入的机器名，nil = 还没进控制台。
    init(core: ConnectCore,
         deviceID: String = PushRegistrar.makeDeviceID(),
         activeMachine: @escaping () -> String?) {
        self.core = core
        self.deviceID = deviceID
        self.activeMachine = activeMachine
    }

    // requestAuthorizationAndRegister 走「获权 → 注册」链。被拒/出错即停，
    // 不重试、不弹自造提示——站内铃铛兜底。
    func requestAuthorizationAndRegister() {
        UNUserNotificationCenter.current()
            .requestAuthorization(options: [.alert, .badge, .sound]) { granted, error in
                if let error {
                    Log.shell.error("推送授权失败，降级站内：\(String(describing: error), privacy: .public)")
                    return
                }
                guard granted else {
                    Log.shell.info("推送授权被拒，降级站内（不报假送达）")
                    return
                }
                Log.shell.info("推送已授权，向系统注册远程通知")
                DispatchQueue.main.async {
                    UIApplication.shared.registerForRemoteNotifications()
                }
            }
    }

    // didRegister 收到系统给的 APNs device（回调线程不定，内部统一切主线程外不
    // 需要——记账与上报都是纯内存 + 核调用，核侧自己串行）。
    func didRegister(deviceToken: Data) {
        let handle = deviceToken.map { String(format: "%02x", $0) }.joined()
        Log.shell.info("收到 APNs device（长度 \(handle.count, privacy: .public)）")
        report(handle)
    }

    // didFail 注册失败（模拟器、无网络等）：静默降级站内。
    func didFail(_ error: Error) {
        Log.shell.error("APNs 注册失败，降级站内：\(String(describing: error), privacy: .public)")
    }

    // retryPending 进入机器后补报暂存的 device（由 CookieBridge.onEntered 触发）。
    func retryPending() {
        guard let handle = pendingHandle else { return }
        Log.shell.info("补报暂存的 APNs device")
        report(handle)
    }

    // report 有活动机器就上报，没有就暂存（不猜目标机器）。
    private func report(_ handle: String) {
        guard let machine = activeMachine() else {
            pendingHandle = handle
            Log.shell.info("暂无活动机器，APNs device 暂存待报")
            return
        }
        submit(handle, to: machine)
    }

    private func submit(_ handle: String, to machine: String) {
        do {
            try core.registerPushDevice(machine, deviceID: deviceID, pushHandle: handle)
            pendingHandle = nil
            Log.shell.info("APNs device 已上报 machine=\(machine, privacy: .public) device=\(deviceID, privacy: .public)")
        } catch {
            pendingHandle = handle   // 失败保留，等下次进入/重试；绝不记成已上报
            Log.shell.error("APNs device 上报失败 machine=\(machine, privacy: .public)：\(String(describing: error), privacy: .public)")
        }
    }

    // makeDeviceID 造一个本机稳定的设备 id（Keychain 级持久交给系统：
    // identifierForVendor 在同 vendor 装机期内稳定，卸载即换——换 id 只多一行
    // 废设备行，fanout 410 会清）。
    static func makeDeviceID() -> String {
        let key = "push.device_id"
        let defaults = UserDefaults.standard
        if let existing = defaults.string(forKey: key), !existing.isEmpty {
            return existing
        }
        let id = UIDevice.current.identifierForVendor?.uuidString ?? UUID().uuidString
        defaults.set(id, forKey: key)
        return id
    }
}
