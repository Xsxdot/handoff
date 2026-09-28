package dev.gosuper.handoff.mobile.pair

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShellErrorTest {
    @Test fun 配对失败归PAIRING() = assertEquals(ShellError.PAIRING, ErrorClassifier.fromPairFailure())

    @Test fun 离线机进入失败归OFFLINE() =
        assertEquals(ShellError.OFFLINE, ErrorClassifier.fromEnterFailure(online = false))

    @Test fun 在线机进入失败归EXCHANGE() =
        assertEquals(ShellError.EXCHANGE, ErrorClassifier.fromEnterFailure(online = true))

    @Test fun http401_403归EXPIRED() {
        assertEquals(ShellError.EXPIRED, ErrorClassifier.fromHttpStatus(401))
        assertEquals(ShellError.EXPIRED, ErrorClassifier.fromHttpStatus(403))
    }

    @Test fun http其他状态不误归类() {
        assertNull(ErrorClassifier.fromHttpStatus(200))
        assertNull(ErrorClassifier.fromHttpStatus(500))
    }

    @Test fun 连接类资源错误归EXCHANGE不归EXPIRED() {
        // WebViewClient ERROR_*：HOST_LOOKUP=-2, CONNECT=-6, IO=-7, TIMEOUT=-8, REDIRECT_LOOP=-9。
        for (code in listOf(-2, -6, -7, -8, -9)) {
            assertEquals("错误码 $code", ShellError.EXCHANGE, ErrorClassifier.fromWebResourceError(code))
            assertNotEquals("错误码 $code 绝不能报过期", ShellError.EXPIRED, ErrorClassifier.fromWebResourceError(code))
        }
    }

    @Test fun 非错误码不归类() {
        assertNull(ErrorClassifier.fromWebResourceError(0))
        assertNull(ErrorClassifier.fromWebResourceError(200))
    }
}
