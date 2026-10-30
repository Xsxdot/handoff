package dev.gosuper.handoff.mobile.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class NativeShellNavigationTest {
    private val origin = "http://127.0.0.1:41000"

    @Test fun switchRequest_requiresExactOriginPathAndMainFrame() {
        assertEquals(NativeShellNavigation.Action.SwitchMachine, NativeShellNavigation.action(
            origin, "http://127.0.0.1:41000/_handoff/native/switch-machine", true,
        ))
        listOf(
            "http://127.0.0.1:41001/_handoff/native/switch-machine",
            "http://127.0.0.1:41000/_handoff/native/switch-machine/extra",
            "http://127.0.0.1:41000/_handoff/native/%73witch-machine",
            "http://127.0.0.1:41000/_handoff/native/switch-machine?x=1",
            "http://127.0.0.1:41000/_handoff/native/switch-machine#x",
            "http://127.0.0.1:41000.evil/_handoff/native/switch-machine",
        ).forEach { assertNull(it, NativeShellNavigation.action(origin, it, true)) }
        assertNull(NativeShellNavigation.action(
            origin, "http://127.0.0.1:41000/_handoff/native/switch-machine", false,
        ))
    }
}
