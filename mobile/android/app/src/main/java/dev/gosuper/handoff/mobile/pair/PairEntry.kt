// PairEntry.kt —— 配对入口的唯一归一化点（contract §3.3 / §4.2 条 7）。
//
// 职责：扫码结果与粘贴文本都走这里；trim 后整份透传 CoreGateway.pair，成功才写 SecretStore。
// 边界：不解析/不重组 bundle（JSON 库禁用于 bundle）；失败不落存储、不进列表、不加载 webview。
package dev.gosuper.handoff.mobile.pair

import android.util.Log
import dev.gosuper.handoff.mobile.core.CoreGateway
import dev.gosuper.handoff.mobile.core.SecretStore

private const val TAG = "PairEntry"

class PairEntry(
    private val core: CoreGateway,
    private val store: SecretStore,
) {
    /**
     * 归一化并配对：
     * 1) 去首尾空白（contract §4.2 条 10 允许）；
     * 2) 空串直接失败（不调 Pair、不落存储）；
     * 3) `core.pair(normalized)` 成功后才 `store.writeBundle(normalized)`。
     *
     * @return success 表示已登记且已持久化；failure 表示未登记、未持久化。
     */
    fun pair(bundleJSON: String): Result<Unit> {
        val normalized = bundleJSON.trim()
        if (normalized.isEmpty()) {
            Log.w(TAG, "配对失败 category=PAIRING reason=empty")
            return Result.failure(IllegalArgumentException("empty bundle"))
        }
        Log.i(TAG, "配对开始 payloadBytes=${normalized.length}")
        try {
            core.pair(normalized)
        } catch (e: Exception) {
            // 不记录异常消息（可能含载荷上下文）；只记类别。
            Log.e(TAG, "配对失败 category=PAIRING")
            return Result.failure(e)
        }
        store.writeBundle(normalized)
        Log.i(TAG, "配对成功 machines=${core.machineCount()}")
        return Result.success(Unit)
    }
}
