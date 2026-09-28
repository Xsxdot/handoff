// MachineList.kt —— 绑定面清单消费：按 0..<MachineCount 迭代，nil 跳过，越界不崩。
//
// 职责：把绑定面「计数 + 按索引取指针」形状转成 List<MachineView>。
// 边界：不解析 origin、不做网络访问；离线机保留在列表（UI 负责不可进入）。
package dev.gosuper.handoff.mobile.list

import android.util.Log
import dev.gosuper.handoff.mobile.core.CoreGateway
import dev.gosuper.handoff.mobile.core.MachineView

private const val TAG = "MachineList"

object MachineList {
    /** 读取全部已登记机器；MachineAt 返回 null（越界）时跳过，不崩溃。 */
    fun load(core: CoreGateway): List<MachineView> {
        val count = core.machineCount()
        val out = ArrayList<MachineView>(count)
        for (i in 0 until count) {
            val m = core.machineAt(i) ?: continue
            out.add(m)
        }
        Log.i(TAG, "清单读取 count=$count effective=${out.size} online=${out.count { it.online }}")
        return out
    }
}
