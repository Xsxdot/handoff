import UIKit

// 单入口：建窗口，根 = RootViewController。
final class SceneDelegate: UIResponder, UIWindowSceneDelegate {
    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession,
               options connectionOptions: UIScene.ConnectionOptions) {
        guard let ws = scene as? UIWindowScene else { return }
        let composition = AppComposition.makeIfNeeded()
        let nav = UINavigationController(rootViewController: RootViewController(composition: composition))
        let w = UIWindow(windowScene: ws)
        w.rootViewController = nav
        w.makeKeyAndVisible()
        window = w
        Log.shell.info("场景已连接，根屏=启动路径")
    }
}
