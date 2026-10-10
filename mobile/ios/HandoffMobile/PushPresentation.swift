import Foundation

// PushPresentation —— 前台通知呈现策略（B432 spec 验收①）。
//
// 职责：回答「这条通知要不要交给系统弹横幅」。
// 边界：纯函数、不碰通知内容、不读全局状态——谁知道自己在哪一面，谁来传参。
//
// 语义（MVP 判据）：前台 **且** 用户已在控制台（对应面已打开，站内铃铛与
// 收件箱角标就在眼前）→ 不重复弹系统横幅；其余（后台 / 前台但在配对屏、
// 机器列表屏）→ 交给系统弹。后台路径系统本就不回调 willPresent，这里仍
// 返回 true 是为了让判据在单测里可独立覆盖。
enum PushPresentation {
    // shouldPresent 返回是否交给系统弹横幅。
    //
    // 参数：isForeground 当前是否前台；onRelevantScreen 是否已打开「需要你」
    // 对应面（控制台在屏）。
    // 返回：true=弹横幅；false=抑制（站内铃铛/角标兜底）。
    static func shouldPresent(isForeground: Bool, onRelevantScreen: Bool) -> Bool {
        if isForeground && onRelevantScreen { return false }
        return true
    }
}
