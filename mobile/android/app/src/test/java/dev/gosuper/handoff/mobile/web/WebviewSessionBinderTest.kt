package dev.gosuper.handoff.mobile.web

import dev.gosuper.handoff.mobile.FakeCookieStore
import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeNavigator
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WebviewSessionBinderTest {

    private class Rig {
        val order = mutableListOf<String>()
        val core = FakeCore(order = order)
        val cookies = FakeCookieStore(order)
        val nav = FakeNavigator(order)
        val binder = WebviewSessionBinder(core, cookies, nav)
    }

    @Test
    fun enterMachine_调用序严格为I3() = runTest {
        val rig = Rig()
        val r = rig.binder.enterMachine("A")
        assertTrue(r.isSuccess)
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
        assertEquals(1, rig.cookies.cookies.size)
    }

    @Test
    fun enterMachine_清罐后注入前jar为空_注入后仅一条() = runTest {
        val rig = Rig()
        assertTrue(rig.binder.enterMachine("A").isSuccess)
        // setCookie 记录的时刻 jarSize=0，证明「清罐完成先于注入」。
        assertTrue(rig.order.contains("setCookie:jarSize=0"))
        assertEquals(1, rig.cookies.cookies.size)
    }

    @Test
    fun enterMachine_switch失败_不load() = runTest {
        val rig = Rig().apply { core.switchShouldThrow = true }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.nav.loaded.isEmpty())
    }

    @Test
    fun enterMachine_清罐失败_不取cookie不load() = runTest {
        val rig = Rig().apply { cookies.clearShouldThrow = true }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.nav.loaded.isEmpty())
        assertTrue(rig.cookies.cookies.isEmpty())
    }

    @Test
    fun enterMachine_cookie失败_不注入不load() = runTest {
        val rig = Rig().apply { core.cookieShouldThrow = true }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.cookies.cookies.isEmpty())
        assertTrue(rig.nav.loaded.isEmpty())
    }

    @Test
    fun enterMachine_注入失败_不load() = runTest {
        val rig = Rig().apply { cookies.setShouldThrow = true }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.nav.loaded.isEmpty())
    }

    @Test
    fun enterMachine_cookie逐键属性() = runTest {
        val rig = Rig()
        assertTrue(rig.binder.enterMachine("A").isSuccess)
        val header = rig.cookies.cookies.single()
        assertTrue(header.contains("handoff_session=VALUE123"))
        assertTrue(header.contains("Path=/"))
        assertTrue(header.contains("HttpOnly"))
        assertTrue(header.contains("SameSite=Lax"))
        assertFalse(header.contains("Secure"))
        assertFalse(header.contains("Domain"))
        assertFalse(header.contains("Max-Age"))
        assertFalse(header.contains("Expires"))
    }

    @Test
    fun enterMachine_值含分号_failClosed不注入不load() = runTest {
        val rig = Rig().apply { core.cookieValue = "bad;value" }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.cookies.cookies.isEmpty())
        assertTrue(rig.nav.loaded.isEmpty())
    }

    @Test
    fun enterMachine_值含空白_failClosed() = runTest {
        val rig = Rig().apply { core.cookieValue = "a b" }
        assertTrue(rig.binder.enterMachine("A").isFailure)
        assertTrue(rig.nav.loaded.isEmpty())
    }

    @Test
    fun enterMachine_值逐字来自绑定面() = runTest {
        val rig = Rig().apply { core.cookieValue = "opaque.token_ABC-123" }
        assertTrue(rig.binder.enterMachine("A").isSuccess)
        assertEquals("handoff_session=opaque.token_ABC-123; Path=/; HttpOnly; SameSite=Lax", rig.cookies.cookies.single())
    }

    @Test
    fun enterMachine_同机重复进入_两次都成功() = runTest {
        val rig = Rig()
        assertTrue(rig.binder.enterMachine("A").isSuccess)
        assertTrue(rig.binder.enterMachine("A").isSuccess)
        assertEquals(2, rig.nav.loaded.size)
    }
}
