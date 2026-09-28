// StartupController.kt —— 启动路径：安全存储有 bundle → Pair(stored) → 机器列表；无 → 配对页。
//
// 职责：进程内只跑一次（Activity 重建不重跑），遵守 I7「启动只 Pair 一次」。
// 边界：不解析 bundle；Pair 失败不破坏已存 bundle（用户可重配覆盖）。
package dev.gosuper.handoff.mobile.pair

import android.util.Log
import dev.gosuper.handoff.mobile.core.CoreGateway
import dev.gosuper.handoff.mobile.core.SecretStore

private const val TAG = "StartupController"

sealed class StartupState {
    object Uninitialized : StartupState()
    object NeedsPairing : StartupState()
    object MachineList : StartupState()
    data class Failed(val error: ShellError) : StartupState()
}

class StartupController(
    private val store: SecretStore,
    private val core: CoreGateway,
) {
    var state: StartupState = StartupState.Uninitialized
        private set

    /** 幂等：首次调用决定启动态，后续调用直接返回（进程内只 Pair 一次）。 */
    fun ensureStarted() {
        if (state != StartupState.Uninitialized) return
        val stored = store.readBundle()?.trim()
        if (stored.isNullOrEmpty()) {
            Log.i(TAG, "无已存 bundle → 配对页")
            state = StartupState.NeedsPairing
            return
        }
        try {
            core.pair(stored)
            Log.i(TAG, "已存 bundle 配对成功 → 机器列表 machines=${core.machineCount()}")
            state = StartupState.MachineList
        } catch (e: Exception) {
            Log.e(TAG, "已存 bundle 配对失败 category=PAIRING → 配对页")
            state = StartupState.Failed(ShellError.PAIRING)
        }
    }
}
