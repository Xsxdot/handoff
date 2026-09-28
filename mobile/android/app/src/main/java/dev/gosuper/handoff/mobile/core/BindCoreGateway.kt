// BindCoreGateway.kt —— CoreGateway / SessionCore 的生产实现：包住 bind.Bind。
//
// 职责：唯一触碰 gomobile 生成面的地方；把 Java 的 long 索引与可空 Machine 适配成 Kotlin。
// 边界：不缓存状态、不做协议；凭据卫生——日志只记机器名与长度，绝不记 payload/cookie 值。
package dev.gosuper.handoff.mobile.core

import android.util.Log
import bind.Bind

private const val TAG = "BindCoreGateway"

/** 配对面 + 会话面的生产网关。两接口由同一实例实现（核是包级单例）。 */
class BindCoreGateway : CoreGateway, SessionCore {

    override fun pair(bundleJSON: String) {
        Log.i(TAG, "pair 开始 payloadBytes=${bundleJSON.length}")
        try {
            Bind.pair(bundleJSON)
        } catch (e: Exception) {
            // 凭据卫生：不记录异常消息（可能携带载荷上下文），只记类别。
            Log.e(TAG, "pair 失败 category=PAIRING")
            throw e
        }
        Log.i(TAG, "pair 成功 machines=${Bind.machineCount()}")
    }

    override fun machineCount(): Int = Bind.machineCount().toInt()

    override fun machineAt(index: Int): MachineView? {
        val m = Bind.machineAt(index.toLong()) ?: return null
        return MachineView(name = m.name, origin = m.origin, online = m.online)
    }

    override fun switchMachine(machine: String): String {
        Log.i(TAG, "switchMachine 开始 machine=$machine")
        return try {
            Bind.switchMachine(machine).also { Log.i(TAG, "switchMachine 完成 machine=$machine") }
        } catch (e: Exception) {
            Log.e(TAG, "switchMachine 失败 category=EXCHANGE machine=$machine")
            throw e
        }
    }

    override fun sessionCookie(machine: String): String {
        Log.i(TAG, "sessionCookie 开始 machine=$machine")
        return try {
            val v = Bind.sessionCookie(machine)
            Log.i(TAG, "sessionCookie 完成 machine=$machine valueLen=${v.length}")
            v
        } catch (e: Exception) {
            Log.e(TAG, "sessionCookie 失败 category=EXCHANGE machine=$machine")
            throw e
        }
    }
}
