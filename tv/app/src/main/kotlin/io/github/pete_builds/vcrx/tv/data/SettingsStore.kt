package io.github.pete_builds.vcrx.tv.data

import android.content.Context
import java.util.UUID

/** Non-secret settings in private preferences. The token lives in [TokenStore]. */
class SettingsStore(context: Context) {
    private val prefs = context.getSharedPreferences("settings", Context.MODE_PRIVATE)

    var serverUrl: String?
        get() = prefs.getString(KEY_SERVER_URL, null)
        set(value) = prefs.edit().putString(KEY_SERVER_URL, value).apply()

    var serverName: String?
        get() = prefs.getString(KEY_SERVER_NAME, null)
        set(value) = prefs.edit().putString(KEY_SERVER_NAME, value).apply()

    var username: String?
        get() = prefs.getString(KEY_USERNAME, null)
        set(value) = prefs.edit().putString(KEY_USERNAME, value).apply()

    /**
     * Set by Switch user: the next Login screen shows the form instead of signing
     * this device straight back in on a trusted network. Cleared when read.
     */
    var skipAutoSignIn: Boolean
        get() = prefs.getBoolean(KEY_SKIP_AUTO, false)
        set(value) = prefs.edit().putBoolean(KEY_SKIP_AUTO, value).apply()

    /** Random, generated on first launch, stable until the app's data is cleared. */
    val deviceId: String
        @Synchronized get() {
            prefs.getString(KEY_DEVICE_ID, null)?.let { return it }
            val id = UUID.randomUUID().toString()
            prefs.edit().putString(KEY_DEVICE_ID, id).commit()
            return id
        }

    private companion object {
        const val KEY_SERVER_URL = "server_url"
        const val KEY_SERVER_NAME = "server_name"
        const val KEY_USERNAME = "username"
        const val KEY_DEVICE_ID = "device_id"
        const val KEY_SKIP_AUTO = "skip_auto_sign_in"
    }
}
