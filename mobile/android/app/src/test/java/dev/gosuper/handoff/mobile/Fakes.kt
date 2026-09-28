// Fakes.kt —— 壳接缝的 JVM 测试替身（无 Robolectric）。
//
// 职责：以可查询的 jar / 可记录的调用序替换绑定面与平台端口，锁 I3 序、fail-closed 与往返。
// 边界：不模拟真实 WebView/CookieManager/Keystore（那属真机/模拟器，spec OOS）。
package dev.gosuper.handoff.mobile

import dev.gosuper.handoff.mobile.core.CoreGateway
import dev.gosuper.handoff.mobile.core.MachineView
import dev.gosuper.handoff.mobile.core.SecretStore
import dev.gosuper.handoff.mobile.core.SessionCore

/** 记录全局调用序的假绑定面（同一 order 列表可跨设备共享）。 */
class FakeCore(
    private val machines: List<MachineView> = emptyList(),
    private val order: MutableList<String> = mutableListOf(),
) : CoreGateway, SessionCore {
    var pairShouldThrow = false
    var switchShouldThrow = false
    var cookieShouldThrow = false
    var cookieValue = "VALUE123"
    val paired = mutableListOf<String>()

    override fun pair(bundleJSON: String) {
        order.add("pair")
        if (pairShouldThrow) throw RuntimeException("pair failed")
        paired.add(bundleJSON)
    }

    override fun machineCount(): Int {
        order.add("machineCount")
        return machines.size
    }

    override fun machineAt(index: Int): MachineView? {
        order.add("machineAt:$index")
        return machines.getOrNull(index)
    }

    override fun switchMachine(machine: String): String {
        order.add("switchMachine:$machine")
        if (switchShouldThrow) throw RuntimeException("switch failed")
        return "http://127.0.0.1:41000"
    }

    override fun sessionCookie(machine: String): String {
        order.add("sessionCookie:$machine")
        if (cookieShouldThrow) throw RuntimeException("cookie failed")
        return cookieValue
    }
}

/** 内存 SecretStore（锁字节往返）。 */
class FakeSecretStore(private var value: String? = null) : SecretStore {
    val writes = mutableListOf<String>()
    override fun writeBundle(bundleJSON: String) {
        value = bundleJSON
        writes.add(bundleJSON)
    }

    override fun readBundle(): String? = value
    override fun clear() { value = null }
}
