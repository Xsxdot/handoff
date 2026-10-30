package dev.gosuper.handoff.mobile.pair

import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeSecretStore
import dev.gosuper.handoff.mobile.FakeCurrentMachineStore
import dev.gosuper.handoff.mobile.core.MachineView
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
    fun 已保存A离线且B在线_启动仍保留A不自动切B() {
        val core = FakeCore(listOf(
            MachineView("A", "", false),
            MachineView("B", "http://127.0.0.1:41000", true),
        ))
        val selection = FakeCurrentMachineStore("A")
        val sc = StartupController(FakeSecretStore("{\"v\":1}"), core, selection)
        sc.ensureStarted()
        assertEquals(StartupState.SelectedUnavailable("A"), sc.state)
        assertEquals("A", selection.read())
        assertTrue(core.paired.isNotEmpty())
    }

    @Test
    fun 已保存A在线_启动请求只恢复A() {
        val core = FakeCore(listOf(MachineView("A", "http://127.0.0.1:41000", true)))
        val sc = StartupController(FakeSecretStore("{\"v\":1}"), core, FakeCurrentMachineStore("A"))
        sc.ensureStarted()
        assertEquals(StartupState.ResumeMachine("A"), sc.state)
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
