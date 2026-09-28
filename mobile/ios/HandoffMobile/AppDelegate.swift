import UIKit

// App 入口：只负责进程级生命周期与 Go 核收尾。
// 边界：不在此建核/配对；终止时关核（I7）。
@main
final class AppDelegate: UIResponder, UIApplicationDelegate {
    func application(_ application: UIApplication,
                     didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?) -> Bool {
        Log.shell.info("App 启动")
        return true
    }

    func applicationWillTerminate(_ application: UIApplication) {
        Log.shell.info("App 终止：关闭 Go 核")
        AppComposition.current?.shutdown()
    }
}
