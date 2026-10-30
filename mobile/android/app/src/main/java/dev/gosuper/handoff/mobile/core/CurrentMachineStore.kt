// CurrentMachineStore.kt —— device-local preference for the user's active machine.
// It stores only the display name, separate from encrypted pairing credentials and Web prefs.
package dev.gosuper.handoff.mobile.core

interface CurrentMachineStore {
    /** Return the last machine whose authenticated main document completed. */
    fun read(): String?

    /** Persist the machine after its authenticated main document completes. */
    fun write(name: String)

    /** Clear selection after a new pairing bundle is successfully committed. */
    fun clear()
}
