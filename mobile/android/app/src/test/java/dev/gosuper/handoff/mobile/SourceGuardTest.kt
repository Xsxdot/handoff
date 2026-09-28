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
        // 禁止在 Log 调用里泄漏凭据变量（value/bundleJSON/setCookieHeader/raw/bundle/token/cookie）。
        // 三种形态都要拦（此前只拦 `$var` 内插值，`+ var` 与 String.format 均可逃逸）：
        //   1) 内插值：Log.i(TAG, "... ${bundleJSON}") / "... $value"
        //   2) 字符串拼接：Log.i(TAG, "probe " + bundle)
        //   3) 格式化参数：Log.i(TAG, String.format("%s", value))
        // `[^;\n]*` 限定在单条语句/单行内，避免跨语句误命中；`(?!\s*\.length)` 放行「只记长度」
        // 的合法写法（如 `${bundleJSON.length}` / `+ value.length`），只拦凭据值本身。
        val creds = setOf("value", "bundleJSON", "setCookieHeader", "raw", "bundle", "token", "cookie")
        val patterns = listOf(
            Regex("""Log\.[a-zA-Z]+\([^;\n]*\$\{?([a-zA-Z_][a-zA-Z0-9_]*)\b(?!\s*\.length)"""),
            Regex("""Log\.[a-zA-Z]+\([^;\n]*\+\s*([a-zA-Z_][a-zA-Z0-9_]*)\b(?!\s*\.length)"""),
            Regex("""String\.format\([^;\n]*\b([a-zA-Z_][a-zA-Z0-9_]*)\b"""),
        )
        val src = readAllKotlin()
        val hits = patterns
            .flatMap { re -> re.findAll(src).filter { it.groupValues[1] in creds }.map { it.value }.toList() }
        assertTrue("日志泄漏了凭据变量: $hits", hits.isEmpty())
    }

    @Test
    fun 清单关闭备份且最小明文放行() {
        val manifest = File(srcMain, "AndroidManifest.xml").readText()
        assertTrue(manifest.contains("android:allowBackup=\"false\""))
        assertTrue(manifest.contains("android:networkSecurityConfig=\"@xml/network_security_config\""))
        // 不得显式声明 usesCleartextTraffic：该属性 API23 生效、NSC 却 API24 才读，
        // 显式 false 会把 API23 的回环明文一并阻断；放行范围只由 NSC 表达。
        assertFalse("不得声明 usesCleartextTraffic", manifest.contains("android:usesCleartextTraffic"))

        val nsc = File(srcMain, "res/xml/network_security_config.xml").readText()
        assertTrue(nsc.contains("cleartextTrafficPermitted=\"false\""))
        assertTrue(nsc.contains("cleartextTrafficPermitted=\"true\""))
        assertTrue(nsc.contains("127.0.0.1"))
    }
}
