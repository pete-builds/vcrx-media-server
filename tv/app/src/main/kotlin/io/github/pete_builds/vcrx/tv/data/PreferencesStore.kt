package io.github.pete_builds.vcrx.tv.data

import android.content.Context
import io.github.pete_builds.vcrx.tv.player.StickyChoice
import io.github.pete_builds.vcrx.tv.player.SubtitleMemory
import io.github.pete_builds.vcrx.tv.settings.Preferences
import java.security.MessageDigest
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

/**
 * Settings and remembered subtitle choices for the signed-in user on the current
 * server, each pair in its own preferences file, so two people sharing a TV, or
 * one TV pointed at two servers, keep their own. The scope is read again on each
 * use, so signing in as someone else switches to their settings without a restart.
 */
class PreferencesStore(private val context: Context, private val settings: SettingsStore) {
    private val state = MutableStateFlow(Preferences())
    private val readOnly = state.asStateFlow()
    private var scope: String? = null
    private var memory = SubtitleMemory()

    /** The current user's settings; Compose collects it, so changes show at once. One instance, so collectors keep it. */
    val flow: StateFlow<Preferences>
        get() {
            ensureScope()
            return readOnly
        }

    val current: Preferences
        get() {
            ensureScope()
            return state.value
        }

    @Synchronized
    fun update(change: (Preferences) -> Preferences) {
        ensureScope()
        val before = state.value
        val after = change(before)
        if (after == before) return
        if (Preferences.resetsSubtitleChoices(before, after)) {
            memory.clear()
            saveMemory()
        }
        state.value = after
        prefs().edit().putString(KEY_PREFERENCES, Preferences.encode(after)).apply()
    }

    @Synchronized
    fun stickySubtitle(itemId: Long, showId: Long?): StickyChoice? {
        ensureScope()
        return memory.lookup(itemId, showId)
    }

    @Synchronized
    fun rememberSubtitle(itemId: Long, showId: Long?, choice: StickyChoice) {
        ensureScope()
        memory.remember(itemId, showId, choice)
        saveMemory()
    }

    /** How many titles and shows have a remembered subtitle choice, for Settings to show. */
    @Synchronized
    fun rememberedCount(): Int {
        ensureScope()
        return memory.size
    }

    @Synchronized
    fun forgetSubtitleChoices() {
        ensureScope()
        memory.clear()
        saveMemory()
    }

    @Synchronized
    private fun ensureScope() {
        val now = scopeId(settings.serverUrl, settings.username)
        if (now == scope) return
        scope = now
        val p = prefs()
        state.value = Preferences.decode(p.getString(KEY_PREFERENCES, null))
        memory = SubtitleMemory(
            p.getString(KEY_MEMORY, null)?.let { runCatching { json.decodeFromString(memorySerializer, it) }.getOrNull() } ?: emptyMap(),
        )
    }

    private fun saveMemory() {
        prefs().edit().putString(KEY_MEMORY, json.encodeToString(memorySerializer, memory.snapshot())).apply()
    }

    private fun prefs() = context.getSharedPreferences("user_${scope ?: "none"}", Context.MODE_PRIVATE)

    private companion object {
        const val KEY_PREFERENCES = "preferences"
        const val KEY_MEMORY = "subtitle_memory"
        val json = Json { ignoreUnknownKeys = true }
        val memorySerializer = MapSerializer(String.serializer(), StickyChoice.serializer())

        /** A short hash, so neither the address nor the name appears in a file name. */
        fun scopeId(server: String?, user: String?): String {
            val digest = MessageDigest.getInstance("SHA-256").digest("${server.orEmpty()}\n${user.orEmpty()}".toByteArray())
            return digest.take(8).joinToString("") { "%02x".format(it) }
        }
    }
}
