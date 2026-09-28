// ShellError.kt —— 壳侧四类可归因错误（contract §4.6 条 35）。
//
// 职责：四类用户可归因错误的枚举（配对 / 离线 / 兑换 / 过期），供 UI 呈现与重试。
// 边界：枚举本身不含归类逻辑（在 T5 ErrorClassifier）；绝不解析绑定面 error 文案。
package dev.gosuper.handoff.mobile.pair

/** 四类用户可归因错误。 */
enum class ShellError {
    PAIRING,
    OFFLINE,
    EXCHANGE,
    EXPIRED,
}
