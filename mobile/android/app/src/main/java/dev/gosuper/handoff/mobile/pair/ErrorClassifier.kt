// ErrorClassifier.kt —— 四类错误按「失败调用点 + Machine.Online」归类。
//
// 职责：纯映射，无 IO；供 UI 呈现与重试决策使用。
// 边界：**不解析**绑定面 error 文案（跨 gomobile 面没有结构化错误哨兵）。
package dev.gosuper.handoff.mobile.pair

object ErrorClassifier {
    /** 配对入口（PairEntry）失败 → 配对类。 */
    fun fromPairFailure(): ShellError = ShellError.PAIRING

    /** 进入机器时失败：离线机 → 离线类，在线机 → 兑换类。 */
    fun fromEnterFailure(online: Boolean): ShellError =
        if (online) ShellError.EXCHANGE else ShellError.OFFLINE

    /** webview HTTP 错误码 → 过期类；其他状态码返回 null（不误归类）。 */
    fun fromHttpStatus(statusCode: Int): ShellError? =
        if (statusCode == 401 || statusCode == 403) ShellError.EXPIRED else null
}
