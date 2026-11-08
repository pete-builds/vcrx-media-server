package io.github.pete_builds.vcrx.mobile.data

import android.content.Context
import io.github.pete_builds.vcrx.mobile.net.ApiJson
import io.github.pete_builds.vcrx.mobile.net.Library
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.User
import kotlinx.serialization.builtins.ListSerializer

/** The phone's own choices and the little it keeps for offline starts. Never the token. */
class PhonePrefs(context: Context) {
    private val prefs = context.getSharedPreferences("phone", Context.MODE_PRIVATE)

    /** Downloads wait for Wi-Fi (an unmetered network). On by default. */
    var wifiOnly: Boolean
        get() = prefs.getBoolean(KEY_WIFI_ONLY, true)
        set(value) = prefs.edit().putBoolean(KEY_WIFI_ONLY, value).apply()

    fun cachedMe(): User? = prefs.getString(KEY_ME, null)?.let { runCatching { ApiJson.decodeFromString(User.serializer(), it) }.getOrNull() }

    fun saveMe(user: User?) {
        prefs.edit().apply { if (user == null) remove(KEY_ME) else putString(KEY_ME, ApiJson.encodeToString(User.serializer(), user)) }.apply()
    }

    fun cachedLibraries(): List<Library> = prefs.getString(KEY_LIBRARIES, null)
        ?.let { runCatching { ApiJson.decodeFromString(ListSerializer(Library.serializer()), it) }.getOrNull() }
        ?: emptyList()

    fun saveLibraries(list: List<Library>) {
        prefs.edit().putString(KEY_LIBRARIES, ApiJson.encodeToString(ListSerializer(Library.serializer()), list)).apply()
    }

    /** Albums played lately, newest first, for Android Auto's Continue listening. */
    fun recentAlbums(): List<MusicAlbum> = prefs.getString(KEY_RECENT_ALBUMS, null)
        ?.let { runCatching { ApiJson.decodeFromString(ListSerializer(MusicAlbum.serializer()), it) }.getOrNull() }
        ?: emptyList()

    fun notePlayed(album: MusicAlbum) {
        val list = (listOf(album) + recentAlbums().filter { it.id != album.id }).take(12)
        prefs.edit().putString(KEY_RECENT_ALBUMS, ApiJson.encodeToString(ListSerializer(MusicAlbum.serializer()), list)).apply()
    }

    fun clearAccountData() {
        prefs.edit().remove(KEY_ME).remove(KEY_LIBRARIES).remove(KEY_RECENT_ALBUMS).apply()
    }

    private companion object {
        const val KEY_WIFI_ONLY = "wifi_only"
        const val KEY_ME = "me"
        const val KEY_LIBRARIES = "libraries"
        const val KEY_RECENT_ALBUMS = "recent_albums"
    }
}
