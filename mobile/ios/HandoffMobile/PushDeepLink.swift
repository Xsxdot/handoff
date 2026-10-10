import Foundation

// PushDeepLink —— 点系统通知进 App 的落点解析（B432 spec 验收②）。
//
// 职责：从 APNs userInfo 里取出 agentd 序列化的 `handoff` 节并读出 deep_link。
// 边界：
//   - 不自己解 JSON：APNs 的 userInfo 由系统整体解析成嵌套字典，壳只做取值
//     （壳禁自解析 JSON，条 4）
//   - 无 handoff 键 / 无 deep_link / 值为空 → nil，由调用方落工作台「需要你处理」
enum PushDeepLink {
    // route(from:) 返回要打开的站内路由；没有可用深链时返回 nil。
    //
    // 参数：userInfo 来自 UNNotification 的 content.userInfo。
    // 返回：形如 `/cards?card=B432` 的相对路由，或 nil（落工作台）。
    static func route(from userInfo: [AnyHashable: Any]) -> String? {
        guard let handoff = userInfo["handoff"] as? [String: Any] else { return nil }
        guard let link = handoff["deep_link"] as? String, !link.isEmpty else { return nil }
        return link
    }
}
