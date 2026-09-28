// EncryptedSecretStore.kt —— SecretStore 的 Android 实现：EncryptedSharedPreferences（Keystore 支撑）。
//
// 职责：以 MasterKey(AES256_GCM) 支撑的 EncryptedSharedPreferences 存原始 bundle 串。
// 边界：不做云备份（Manifest allowBackup=false）、不落日志、不解析 bundle。
package dev.gosuper.handoff.mobile.core

import android.content.Context
import android.util.Log
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

private const val TAG = "EncryptedSecretStore"
private const val PREFS_FILE = "handoff_mobile_secret"
private const val KEY_BUNDLE = "pair_bundle"

class EncryptedSecretStore(context: Context) : SecretStore {

    private val prefs = run {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context,
            PREFS_FILE,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    override fun writeBundle(bundleJSON: String) {
        prefs.edit().putString(KEY_BUNDLE, bundleJSON).apply()
        Log.i(TAG, "写入配对 bundle bytes=${bundleJSON.length}")
    }

    override fun readBundle(): String? {
        val v = prefs.getString(KEY_BUNDLE, null)
        Log.i(TAG, "读取配对 bundle present=${v != null} bytes=${v?.length ?: 0}")
        return v
    }

    override fun clear() {
        prefs.edit().remove(KEY_BUNDLE).apply()
        Log.i(TAG, "清除配对 bundle")
    }
}
