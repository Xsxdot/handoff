// SharedPreferencesCurrentMachineStore.kt —— app-private current-machine preference.
// It contains no credentials and does not participate in bundle encryption or backup.
package dev.gosuper.handoff.mobile.core

import android.content.Context
import android.util.Log

private const val TAG = "CurrentMachineStore"
private const val PREFS = "handoff_mobile_selection"
private const val KEY_MACHINE = "current_machine"

class SharedPreferencesCurrentMachineStore(context: Context) : CurrentMachineStore {
    private val prefs = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    override fun read(): String? = prefs.getString(KEY_MACHINE, null)?.takeIf(String::isNotBlank)

    override fun write(name: String) {
        check(prefs.edit().putString(KEY_MACHINE, name).commit()) { "current machine preference write failed" }
        Log.i(TAG, "当前机器选择已保存")
    }

    override fun clear() {
        check(prefs.edit().remove(KEY_MACHINE).commit()) { "current machine preference clear failed" }
        Log.i(TAG, "当前机器选择已清除")
    }
}
