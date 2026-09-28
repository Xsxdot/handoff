package dev.gosuper.handoff.mobile.pair

import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeSecretStore
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class StartupControllerTest {

    @Test
    fun 无存储bundle_进配对页() {
        val sc = StartupController(FakeSecretStore(null), FakeCore())
        sc.ensureStarted()
        assertEquals(StartupState.NeedsPairing, sc.state)
    }

    @Test
    fun 有存储bundle_配对成功_进列表() {
        val core = FakeCore()
        val sc = StartupController(FakeSecretStore("{\"v\":1}"), core)
        sc.ensureStarted()
        assertEquals(StartupState.MachineList, sc.state)
        assertEquals(listOf("{\"v\":1}"), core.paired)
    }

    @Test
    fun 有存储bundle_配对失败_进配对错误页() {
        val core = FakeCore().apply { pairShouldThrow = true }
        val sc = StartupController(FakeSecretStore("{\"v\":1}"), core)
        sc.ensureStarted()
        assertTrue(sc.state is StartupState.Failed)
        assertEquals(ShellError.PAIRING, (sc.state as StartupState.Failed).error)
    }

    @Test
    fun ensureStarted_只Pair一次() {
        val core = FakeCore()
        val sc = StartupController(FakeSecretStore("{\"v\":1}"), core)
        sc.ensureStarted()
        sc.ensureStarted()
        sc.ensureStarted()
        assertEquals(1, core.paired.size)
    }
}
