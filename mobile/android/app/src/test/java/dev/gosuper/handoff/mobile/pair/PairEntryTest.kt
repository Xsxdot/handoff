package dev.gosuper.handoff.mobile.pair

import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeSecretStore
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
}
