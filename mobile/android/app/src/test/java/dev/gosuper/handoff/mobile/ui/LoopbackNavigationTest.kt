// LoopbackNavigationTest.kt —— F1：导航白名单决策（WebViewClient.shouldOverrideUrlLoading 的判据）。
package dev.gosuper.handoff.mobile.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class LoopbackNavigationTest {

    @Test
    fun 回环源放行() {
        // 绑定面 origin（switchMachine 返回）正是此形态：http + 127.0.0.1 + 端口。
        assertFalse(LoopbackNavigation.shouldIntercept("http://127.0.0.1:41000"))
        assertFalse(LoopbackNavigation.shouldIntercept("http://127.0.0.1:41000/dashboard"))
        assertFalse(LoopbackNavigation.shouldIntercept("http://127.0.0.1:41000/a?b=c#d"))
    }

    @Test
    fun 外部URL被拦截() {
        assertTrue(LoopbackNavigation.shouldIntercept("http://evil.example/"))
        assertTrue(LoopbackNavigation.shouldIntercept("http://192.168.1.10:41000/"))
        assertTrue(LoopbackNavigation.shouldIntercept("http://localhost:41000/"))
        // 子域伪装：host 以 127.0.0.1 开头但不是它。
        assertTrue(LoopbackNavigation.shouldIntercept("http://127.0.0.1.evil.example/"))
    }

    @Test
    fun 非http协议被拦截() {
        assertTrue(LoopbackNavigation.shouldIntercept("https://127.0.0.1:41000/"))
        assertTrue(LoopbackNavigation.shouldIntercept("file:///etc/passwd"))
        assertTrue(LoopbackNavigation.shouldIntercept("javascript:alert(1)"))
        assertTrue(LoopbackNavigation.shouldIntercept("data:text/html,<script>1</script>"))
    }

    @Test
    fun 空与畸形URL被拦截() {
        assertTrue(LoopbackNavigation.shouldIntercept(null))
        assertTrue(LoopbackNavigation.shouldIntercept(""))
        assertTrue(LoopbackNavigation.shouldIntercept("http://[::1"))
    }
}
