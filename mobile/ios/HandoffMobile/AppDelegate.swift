import UIKit
import UserNotifications

// App 入口：进程级生命周期、Go 核收尾、系统推送的委托接线（B432）。
// 边界：不在此建核/配对；授权与上报编排在 PushRegistrar，落点判定在
// PushPresentation / PushDeepLink，本类只做系统回调到这三者的转接。
@main
final class AppDelegate: UIResponder, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        Log.shell.info("App 启动")
        // 必须在横幅回调之前挂上委托，否则前台通知走系统默认呈现。
        UNUserNotificationCenter.current().delegate = self
        // 获权 → 注册 → 拿 device → 交核上报（链在 PushRegistrar 内）。
        AppComposition.makeIfNeeded().startPushRegistration()
        return true
    }

    func applicationWillTerminate(_ application: UIApplication) {
        Log.shell.info("App 终止：关闭 Go 核")
        AppComposition.current?.shutdown()
    }

    // MARK: - APNs device 回调

    func application(_ application: UIApplication,
                     didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        AppComposition.current?.pushRegistrar.didRegister(deviceToken: deviceToken)
    }

    func application(_ application: UIApplication,
                     didFailToRegisterForRemoteNotificationsWithError error: Error) {
        AppComposition.current?.pushRegistrar.didFail(error)
    }

    // MARK: - 通知中心委托

    // 前台呈现：前台且已在控制台 → 不重复弹横幅（spec 验收①）；否则交给系统。
    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        // 「对应面已打开」= 控制台已进入（活动机器在屏），此刻站内铃铛与角标可见。
        let onRelevantScreen = AppComposition.current?.cookieBridge.currentMachine != nil
        let present = PushPresentation.shouldPresent(isForeground: true, onRelevantScreen: onRelevantScreen)
        Log.shell.debug("前台通知呈现判定 decision=\(present ? "banner" : "suppress", privacy: .public)")
        completionHandler(present ? [.banner, .list] : [])
    }

    // 点通知进 App：有深链落对应会话/卡，无深链落工作台「需要你处理」（spec 验收②）。
    func userNotificationCenter(_ center: UNUserNotificationCenter,
                                didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let route = PushDeepLink.route(from: response.notification.request.content.userInfo) ?? "/"
        Log.shell.info("点开推送 route=\(route, privacy: .public)")
        AppComposition.current?.cookieBridge.openRoute(route)
        completionHandler()
    }
}
