// EnterCoordinator.kt —— 「进入机器」动作的前置守卫与调度（离线不可进入）。
//
// 职责：离线机点击不触碰 WebviewSessionBinder（不 switchMachine/不 load）；在线机转交 binder。
// 边界：不直接触碰平台 webview/cookie；结果经 onResult 回调交 UI。
package dev.gosuper.handoff.mobile.list

import android.util.Log
import dev.gosuper.handoff.mobile.pair.ErrorClassifier
import dev.gosuper.handoff.mobile.pair.ShellError
import dev.gosuper.handoff.mobile.web.WebviewSessionBinder
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

private const val TAG = "EnterCoordinator"

class EnterCoordinator(
    private val binder: WebviewSessionBinder,
    private val scope: CoroutineScope,
    private val onResult: (Result<Unit>, ShellError?) -> Unit,
) {
    /** 点击一台机器：离线直接拒绝（不触碰 binder）；在线异步进入。 */
    fun enter(machine: String, online: Boolean) {
        if (!online) {
            Log.i(TAG, "离线机不可进入 machine=$machine")
            onResult(Result.failure(IllegalStateException("offline")), ShellError.OFFLINE)
            return
        }
        scope.launch {
            val r = binder.enterMachine(machine)
            onResult(r, if (r.isSuccess) null else ErrorClassifier.fromEnterFailure(online = true))
        }
    }
}
