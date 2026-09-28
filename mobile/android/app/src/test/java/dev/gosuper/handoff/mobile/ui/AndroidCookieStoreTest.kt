// AndroidCookieStoreTest.kt —— F2：平台 cookie 回调 false 必须转成失败（fail-closed），不得静默成功。
package dev.gosuper.handoff.mobile.ui

import android.webkit.ValueCallback
import dev.gosuper.handoff.mobile.FakeCore
import dev.gosuper.handoff.mobile.FakeNavigator
import dev.gosuper.handoff.mobile.web.WebviewSessionBinder
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class AndroidCookieStoreTest {

    /** 假端口：立即以预设结果回调，模拟 CookieManager 同步/异步返回。 */
    private class FakeCookieManagerPort(
        var removeResult: Boolean = true,
        var setResult: Boolean = true,
    ) : CookieManagerPort {
        var flushCount = 0
        override fun removeAllCookies(callback: ValueCallback<Boolean>) {
            callback.onReceiveValue(removeResult)
        }

        override fun setCookie(url: String, value: String, callback: ValueCallback<Boolean>) {
            callback.onReceiveValue(setResult)
        }

        override fun flush() {
            flushCount++
        }
    }

    @Test
    fun removeAllCookies返回false_抛错且不flush() = runTest {
        val port = FakeCookieManagerPort(removeResult = false)
        var failed = false
        try {
            AndroidCookieStore(port).clearHost("127.0.0.1")
        } catch (e: Exception) {
            failed = true
        }
        assertTrue("false 必须转成失败", failed)
        assertEquals("失败态不得 flush", 0, port.flushCount)
    }

    @Test
    fun setCookie返回false_抛错且不flush() = runTest {
        val port = FakeCookieManagerPort(setResult = false)
        var failed = false
        try {
            AndroidCookieStore(port).setCookie("http://127.0.0.1:41000", "handoff_session=x; Path=/")
        } catch (e: Exception) {
            failed = true
        }
        assertTrue("false 必须转成失败", failed)
        assertEquals("失败态不得 flush", 0, port.flushCount)
    }

    @Test
    fun 成功时才flush() = runTest {
        val port = FakeCookieManagerPort()
        val store = AndroidCookieStore(port)
        store.clearHost("127.0.0.1")
        store.setCookie("http://127.0.0.1:41000", "handoff_session=x; Path=/")
        assertEquals(2, port.flushCount)
    }

    @Test
    fun 清罐返回false_进入错误态_不取cookie不load后续() = runTest {
        val order = mutableListOf<String>()
        val core = FakeCore(order = order)
        val nav = FakeNavigator(order)
        val binder = WebviewSessionBinder(
            session = core,
            cookies = AndroidCookieStore(FakeCookieManagerPort(removeResult = false)),
            navigator = nav,
        )

        assertTrue(binder.enterMachine("A").isFailure)
        assertTrue("失败后不得 load", nav.loaded.isEmpty())
        assertTrue("清罐失败后不得取 cookie", order.none { it.startsWith("sessionCookie") })
    }

    @Test
    fun 注入返回false_进入错误态_不load后续() = runTest {
        val order = mutableListOf<String>()
        val core = FakeCore(order = order)
        val nav = FakeNavigator(order)
        val binder = WebviewSessionBinder(
            session = core,
            cookies = AndroidCookieStore(FakeCookieManagerPort(setResult = false)),
            navigator = nav,
        )

        assertTrue(binder.enterMachine("A").isFailure)
        assertTrue("失败后不得 load", nav.loaded.isEmpty())
    }
}
