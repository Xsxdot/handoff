package dev.gosuper.handoff.mobile.list

import dev.gosuper.handoff.mobile.FakeCookieStore
import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeNavigator
import dev.gosuper.handoff.mobile.core.MachineView
import dev.gosuper.handoff.mobile.pair.ShellError
import dev.gosuper.handoff.mobile.web.WebviewSessionBinder
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class EnterCoordinatorTest {

    private class Rig {
        val order = mutableListOf<String>()
        val core = FakeCore(order = order)
        val cookies = FakeCookieStore(order)
        val nav = FakeNavigator(order)
        val binder = WebviewSessionBinder(core, cookies, nav)
    }

    @Test
    fun enter_离线机_不触碰binder() = runTest {
        val rig = Rig()
        var gotError: ShellError? = null
        var got: Result<Unit>? = null
        val c = EnterCoordinator(rig.binder, this) { r, e -> got = r; gotError = e }
        c.enter("A", online = false)
        advanceUntilIdle()
        // 离线：任何端口都不得被触碰。
        assertTrue(rig.order.isEmpty())
        assertEquals(ShellError.OFFLINE, gotError)
        assertTrue(got!!.isFailure)
    }

    @Test
    fun enter_在线机_走binder并发结果() = runTest {
        val rig = Rig()
        var gotError: ShellError? = ShellError.PAIRING
        var got: Result<Unit>? = null
        val c = EnterCoordinator(rig.binder, this) { r, e -> got = r; gotError = e }
        c.enter("A", online = true)
        advanceUntilIdle()
        assertTrue(got!!.isSuccess)
        assertEquals(null, gotError)
        assertEquals(
            listOf(
                "switchMachine:A",
                "clearHost:127.0.0.1",
                "sessionCookie:A",
                "setCookie:jarSize=0",
                "load:http://127.0.0.1:41000",
            ),
            rig.order,
        )
    }
}
