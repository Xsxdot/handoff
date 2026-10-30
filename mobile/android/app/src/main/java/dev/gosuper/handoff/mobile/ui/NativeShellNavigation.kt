// NativeShellNavigation.kt —— classify the one Web-to-native navigation action.
// Responsibility: recognize a same-origin, main-frame switch link exactly; it does not
// accept commands, destination names, or URL variants from page content.
package dev.gosuper.handoff.mobile.ui

import java.net.URI

object NativeShellNavigation {
    const val SWITCH_PATH = "/_handoff/native/switch-machine"

    enum class Action { SwitchMachine }

    /** Return the native action only for the exact active origin and literal path. */
    fun action(activeOrigin: String, target: String, mainFrame: Boolean): Action? {
        if (!mainFrame) return null
        val active = runCatching { URI(activeOrigin) }.getOrNull() ?: return null
        val requested = runCatching { URI(target) }.getOrNull() ?: return null
        if (active.scheme != "http" || active.host != "127.0.0.1" || active.port < 1 || active.rawUserInfo != null) return null
        if (requested.scheme != active.scheme || requested.host != active.host || requested.port != active.port || requested.rawUserInfo != null) return null
        if (requested.rawPath != SWITCH_PATH || requested.rawQuery != null || requested.rawFragment != null) return null
        return Action.SwitchMachine
    }
}
