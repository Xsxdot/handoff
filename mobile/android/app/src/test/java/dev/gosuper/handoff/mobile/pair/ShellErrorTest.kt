package dev.gosuper.handoff.mobile.pair

import org.junit.Assert.assertEquals
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
}
