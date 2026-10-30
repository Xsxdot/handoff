package dev.gosuper.handoff.mobile.pair

import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeSecretStore
import dev.gosuper.handoff.mobile.FakeCurrentMachineStore
import dev.gosuper.handoff.mobile.core.MachineView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class PairEntryTest {

    @Test
    fun pair_成功_trim后整份透传并落存储() {
        val core = FakeCore()
        val store = FakeSecretStore()
        val raw = "  {\"v\":1,\"machines\":[]}  "
        val r = PairEntry(core, store).pair(raw)
        assertTrue(r.isSuccess)
        assertEquals(listOf("{\"v\":1,\"machines\":[]}"), core.paired)
        assertEquals("{\"v\":1,\"machines\":[]}", store.readBundle())
    }

    @Test
    fun pair_失败_不落存储不登记() {
        val core = FakeCore().apply { pairShouldThrow = true }
        val store = FakeSecretStore()
        val r = PairEntry(core, store).pair("{\"v\":1}")
        assertTrue(r.isFailure)
        assertTrue(store.writes.isEmpty())
        assertTrue(core.paired.isEmpty())
    }

    @Test
    fun pair_空串_不调Pair不落存储() {
        val core = FakeCore()
        val store = FakeSecretStore()
        val r = PairEntry(core, store).pair("   \n  ")
        assertTrue(r.isFailure)
        assertTrue(core.paired.isEmpty())
        assertTrue(store.writes.isEmpty())
    }

    @Test
    fun pair_内部空白与特殊字符_逐字节往返() {
        val core = FakeCore()
        val store = FakeSecretStore()
        // 含换行、制表、Unicode、引号转义——验证只 trim 首尾、不改内部。
        val raw = "\n\t{\"v\":1,\"m\":\"机\uD83D\uDE00\",\"s\":\"a\\\"b\"}\t\n"
        val expected = "{\"v\":1,\"m\":\"机\uD83D\uDE00\",\"s\":\"a\\\"b\"}"
        assertTrue(PairEntry(core, store).pair(raw).isSuccess)
        assertEquals(listOf(expected), core.paired)
        assertEquals(expected, store.readBundle())
    }

    @Test
    fun retrySavedMachine_重新校验后只进入原A() {
        val core = FakeCore(listOf(
            MachineView("A", "http://127.0.0.1:41001", false),
            MachineView("B", "http://127.0.0.1:41002", true),
        ))
        core.onPair = { core.machines = listOf(
            MachineView("A", "http://127.0.0.1:41001", true),
            MachineView("B", "http://127.0.0.1:41002", true),
        ) }
        val secret = FakeSecretStore("opaque-bundle")
        val selected = FakeCurrentMachineStore("A")
        val result = PairEntry(core, secret, selected).retrySelectedMachine()
        assertTrue(result.isSuccess)
        assertEquals("A", result.getOrThrow().name)
        assertEquals(listOf("opaque-bundle"), core.paired)
        assertEquals(emptyList<String>(), secret.writes)
        assertEquals("A", selected.read())
    }

    @Test
    fun retrySavedMachine_仍离线时失败并保留A不切B() {
        val core = FakeCore(listOf(
            MachineView("A", "", false),
            MachineView("B", "http://127.0.0.1:41002", true),
        ))
        val secret = FakeSecretStore("opaque-bundle")
        val selected = FakeCurrentMachineStore("A")
        val result = PairEntry(core, secret, selected).retrySelectedMachine()
        assertTrue(result.isFailure)
        assertEquals(listOf("opaque-bundle"), core.paired)
        assertEquals("A", selected.read())
        assertTrue(secret.writes.isEmpty())
        assertTrue(core.machines.first { it.name == "B" }.online)
    }
}
