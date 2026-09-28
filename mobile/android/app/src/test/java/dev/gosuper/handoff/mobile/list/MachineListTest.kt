package dev.gosuper.handoff.mobile.list

import dev.gosuper.handoff.mobile.core.CoreGateway
import dev.gosuper.handoff.mobile.core.MachineView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** 稀疏绑定面替身：machineCount 报 n，但某索引返回 null，模拟越界/空洞。 */
private class SparseCore(
    private val n: Int,
    private val machines: Map<Int, MachineView>,
) : CoreGateway {
    override fun pair(bundleJSON: String) = Unit
    override fun machineCount(): Int = n
    override fun machineAt(index: Int): MachineView? = machines[index]
}

class MachineListTest {

    @Test
    fun load_按计数迭代_nil跳过_不崩溃() {
        val core = SparseCore(
            n = 3,
            machines = mapOf(
                0 to MachineView("a", "http://127.0.0.1:1", true),
                2 to MachineView("c", "http://127.0.0.1:3", true),
            ),
        )
        val got = MachineList.load(core)
        assertEquals(listOf("a", "c"), got.map { it.name })
    }

    @Test
    fun load_离线机保留在列表() {
        val core = SparseCore(
            n = 2,
            machines = mapOf(
                0 to MachineView("online", "http://127.0.0.1:1", true),
                1 to MachineView("offline", "", false),
            ),
        )
        val got = MachineList.load(core)
        assertEquals(2, got.size)
        assertTrue(got.any { it.name == "offline" && !it.online })
    }
}
