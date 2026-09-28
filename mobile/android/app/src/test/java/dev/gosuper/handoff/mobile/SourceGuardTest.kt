// SourceGuardTest.kt —— 壳源码负向 guard：把 contract 的「不得出现」变成可红断言。
//
// 职责：扫描 src/main 的 Kotlin 与清单，断言只出现七函数、无绕过门禁的绑定、
//       无 bundle JSON 解析、无自建 HTTP 客户端、无 JS 桥、凭据不入日志、最小明文放行。
// 边界：只做词法/文本级断言，不做 AST 分析；测试工作目录以候选 src/main 定位。
package dev.gosuper.handoff.mobile

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class SourceGuardTest {

    private val srcMain: File by lazy {
        val candidates = listOf(File("src/main"), File("app/src/main"))
        candidates.firstOrNull { it.isDirectory }
            ?: error("找不到 src/main（user.dir=${System.getProperty("user.dir")}）")
    }

    private fun kotlinSources(): List<File> =
        srcMain.walkTopDown().filter { it.isFile && it.extension == "kt" }.toList()

    private fun readAllKotlin(): String = kotlinSources().joinToString("\n") { it.readText() }

    @Test
    fun 只出现七函数绑定调用() {
        val allowed = setOf("pair", "machineCount", "machineAt", "origin", "sessionCookie", "switchMachine", "close")
        val re = Regex("""\bBind\.([A-Za-z0-9_]+)""")
        val found = re.findAll(readAllKotlin()).map { it.groupValues[1] }.toSet()
        assertTrue("出现未授权绑定调用: ${found - allowed}", allowed.containsAll(found))
    }

    @Test
    fun 无Token_Dial_Credential绑定() {
        val text = readAllKotlin().lowercase()
        for (bad in listOf("bind.token", "bind.dial", "bind.credential", ".token(", ".dial(")) {
            assertFalse("出现门禁绕过调用: $bad", text.contains(bad))
        }
    }

    @Test
    fun 无bundle的JSON解析库() {
        val text = readAllKotlin()
        for (lib in listOf("com.google.gson", "kotlinx.serialization", "org.json.JSONObject", "org.json.JSONTokener")) {
            assertFalse("壳不得对 bundle 使用 JSON 解析库: $lib", text.contains(lib))
        }
    }

    @Test
    fun 无自建HTTP客户端访问agentd() {
        val text = readAllKotlin()
        for (api in listOf("HttpURLConnection", "OkHttpClient", "java.net.URL(", "HttpClient.newHttpClient")) {
            assertFalse("壳不得自建 HTTP 客户端: $api", text.contains(api))
        }
    }

    @Test
    fun 无JS桥暴露凭据() {
        assertFalse("不得 addJavascriptInterface", readAllKotlin().contains("addJavascriptInterface"))
    }

    @Test
    fun cookie名常量逐字为handoff_session() {
        val text = readAllKotlin()
        assertTrue(text.contains("const val SESSION_COOKIE_NAME = \"handoff_session\""))
    }

    @Test
    fun 日志不插值凭据变量() {
        // 禁止在 Log 调用里插值 value/bundleJSON/setCookieHeader/raw/bundle。
        val re = Regex("""Log\.[a-zA-Z]+\("[^"]*"[^)]*\$(value|bundleJSON|setCookieHeader|raw|bundle)\b""")
        val hit = re.find(readAllKotlin())
        assertTrue("日志插值了凭据变量: ${hit?.value}", hit == null)
    }

    @Test
    fun 清单关闭备份且最小明文放行() {
        val manifest = File(srcMain, "AndroidManifest.xml").readText()
        assertTrue(manifest.contains("android:allowBackup=\"false\""))
        assertTrue(manifest.contains("android:networkSecurityConfig=\"@xml/network_security_config\""))
        assertFalse("不得全局放行明文", manifest.contains("android:usesCleartextTraffic=\"true\""))

        val nsc = File(srcMain, "res/xml/network_security_config.xml").readText()
        assertTrue(nsc.contains("cleartextTrafficPermitted=\"false\""))
        assertTrue(nsc.contains("cleartextTrafficPermitted=\"true\""))
        assertTrue(nsc.contains("127.0.0.1"))
    }
}
