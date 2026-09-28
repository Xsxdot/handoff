// WebviewNavigator.kt —— 导航抽象：webview 加载回环源（I3 最后一步）。
//
// 职责：让 WebviewSessionBinder 能以可控顺序调用 load，并使单测可断言「失败不 load」。
// 边界：只 load 绑定面返回的 origin，不自行拼 URL。
package dev.gosuper.handoff.mobile.web

interface WebviewNavigator {
    /** 加载给定的回环源 URL（逐字取自绑定面返回值）。 */
    fun load(url: String)
}
