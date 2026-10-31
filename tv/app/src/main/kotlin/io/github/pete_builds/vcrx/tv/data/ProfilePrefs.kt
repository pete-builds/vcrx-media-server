package io.github.pete_builds.vcrx.tv.data

import android.content.Context

/**
 * "Who's watching?" on this TV: the profile used last, and whether to ask every
 * time the app opens. Not secret; the profile's token lives in [TokenStore].
 */
class ProfilePrefs(context: Context) {
    private val prefs = context.getSharedPreferences("profiles", Context.MODE_PRIVATE)

    /** Open on the picker instead of going straight back to the last profile. Off by default. */
    var alwaysAsk: Boolean
        get() = prefs.getBoolean(KEY_ALWAYS_ASK, false)
        set(value) = prefs.edit().putBoolean(KEY_ALWAYS_ASK, value).apply()

    /** The profile chosen last, where the picker's focus starts. */
    var lastProfileId: Long?
        get() = prefs.getLong(KEY_LAST, -1).takeIf { it > 0 }
        set(value) = prefs.edit().putLong(KEY_LAST, value ?: -1).apply()

    private companion object {
        const val KEY_ALWAYS_ASK = "always_ask"
        const val KEY_LAST = "last_profile_id"
    }
}
