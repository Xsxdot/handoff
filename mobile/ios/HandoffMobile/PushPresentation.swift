import Foundation

// PushPresentation —— 前台通知呈现策略（B432 spec 验收①）。
//
// 职责：回答「这条通知要不要交给系统弹横幅」，以及「用户当前是否已在该通知的
// 对应面」（修刀 2026-10-10：按路由判定，禁 currentMachine!=nil 近似）。
// 边界：纯函数、不碰通知内容、不读全局状态——谁知道自己在哪一面，谁来传参。
//
// 语义（MVP 判据）：前台 **且** 用户已在该通知的对应面（对应卡，或工作台
// 「需要你」入口）→ 不重复弹系统横幅；其余（后台 / 前台但在别的卡、别的
// tab、未进入）→ 交给系统弹。后台路径系统本就不回调 willPresent，这里仍
// 返回 true 是为了让判据在单测里可独立覆盖。
enum PushPresentation {
    // shouldPresent 返回是否交给系统弹横幅。
    //
    // 参数：isForeground 当前是否前台；onRelevantScreen 是否已打开「需要你」
    // 对应面（见 isRelevantRoute 的路由判据）。
    // 返回：true=弹横幅；false=抑制（对应面已在屏）。
    static func shouldPresent(isForeground: Bool, onRelevantScreen: Bool) -> Bool {
        if isForeground && onRelevantScreen { return false }
        return true
    }

    // isRelevantRoute 判定「当前路由是否就是这条通知的对应面」（修刀判据）。
    //
    // 对应面两处（其余一律别处，前台仍弹）：
    //   1. 对应卡：通知深链是 /cards?card=X，且当前路由也是 /cards 且 card=X；
    //   2. 工作台「需要你」入口：当前路由是工作台首页（/ 或 /?tab=projects——
    //      MobileWorkspace 的「需要你处理」行所在面；缺省 tab 即 projects）。
    //      此时无论通知有无深链都算在场（聚合入口可见即无需重复弹）。
    //
    // 参数：notificationRoute 该通知的深链（PushDeepLink.route 的返回值，
    //       nil=无深链、点进落工作台）；currentRoute 当前 SPA 路由
    //       （CookieBridge.currentRoute；未进入时为空串）。
    // 返回：true=在对应面；false=别处。
    static func isRelevantRoute(notificationRoute: String?, currentRoute: String) -> Bool {
        if currentRoute.isEmpty { return false }
        if isWorkbenchHome(currentRoute) { return true }
        guard let target = notificationRoute, isCardsRoute(target) else { return false }
        return isCardsRoute(currentRoute) && cardID(of: currentRoute) == cardID(of: target)
    }

    // isWorkbenchHome 判定路由是否为工作台首页（「需要你」聚合入口所在面）。
    // "/" 与 "/?tab=projects" 同面；别的 tab（sessions/cards/settings…）不是。
    private static func isWorkbenchHome(_ route: String) -> Bool {
        guard let comps = URLComponents(string: route), comps.path == "/" else { return false }
        guard let items = comps.queryItems else { return true }   // 纯 "/" 无 query
        let tab = items.first(where: { $0.name == "tab" })?.value
        return tab == nil || tab == "projects"
    }

    // isCardsRoute 判定路由是否落在 /cards 宿主（深链语汇只在此宿主上生效）。
    private static func isCardsRoute(_ route: String) -> Bool {
        return URLComponents(string: route)?.path == "/cards"
    }

    // cardID 取 /cards 宿主上 card 参数的值；缺失返回空串。
    private static func cardID(of route: String) -> String {
        guard let comps = URLComponents(string: route) else { return "" }
        return comps.queryItems?.first(where: { $0.name == "card" })?.value ?? ""
    }
}
