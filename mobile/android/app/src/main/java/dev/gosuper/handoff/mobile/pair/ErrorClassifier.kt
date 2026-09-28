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

    /**
     * webview 主框资源错误码（WebViewClient.onReceivedError）→ 兑换类。
     *
     * 连接/超时/DNS/IO 等一切加载失败都是可重试的网络类问题，**绝不能归 EXPIRED**——
     * 过期只由 HTTP 401/403 表达（见 [fromHttpStatus]）。WebViewClient 的 ERROR_* 恒为负
     * （ERROR_UNKNOWN=-1 起），故以 `errorCode < 0` 统一兜底为 EXCHANGE（含未知负数，fail-closed）；
     * 0/正数（非错误）返回 null，不臆断。
     */
    fun fromWebResourceError(errorCode: Int): ShellError? =
        if (errorCode < 0) ShellError.EXCHANGE else null
}
