package io.github.pete_builds.vcrx.mobile.offline

import android.content.Context
import io.github.pete_builds.vcrx.mobile.net.ApiJson
import io.github.pete_builds.vcrx.mobile.net.Capabilities
import io.github.pete_builds.vcrx.mobile.net.PlaybackEvent
import io.github.pete_builds.vcrx.mobile.net.PlaybackRequest
import io.github.pete_builds.vcrx.mobile.net.ServerApi
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.builtins.ListSerializer

/**
 * [OfflineProgressQueue] kept on disk. A downloaded title always records its
 * position here, online or not, and [sync] sends it: played becomes PUT watched,
 * a position becomes a short playback session that starts and stops there, the
 * same way any client leaves a resume point.
 */
class OfflineStore(context: Context) {
    private val prefs = context.getSharedPreferences("offline_progress", Context.MODE_PRIVATE)
    private val serializer = ListSerializer(OfflineProgress.serializer())
    private val queue = OfflineProgressQueue(load())
    private val lock = Mutex()

    @Synchronized
    fun record(entry: OfflineProgress) {
        queue.record(entry)
        save()
    }

    @Synchronized
    fun resumeFor(itemId: Long, serverMs: Long?): Long = queue.resumeFor(itemId, serverMs)

    val pending: Int @Synchronized get() = queue.entries.size

    suspend fun sync(api: ServerApi, caps: Capabilities) = lock.withLock {
        val sink = object : ProgressSink {
            override suspend fun markPlayed(itemId: Long) = api.setWatched(itemId, true)

            override suspend fun savePosition(entry: OfflineProgress) {
                val session = api.startPlayback(
                    PlaybackRequest(itemId = entry.itemId, versionId = entry.versionId, startPositionMs = entry.positionMs, capabilities = caps),
                )
                api.postEvent(session.sessionId, PlaybackEvent("start", entry.positionMs, durationMs = entry.durationMs))
                api.postEvent(session.sessionId, PlaybackEvent("stop", entry.positionMs, durationMs = entry.durationMs))
            }
        }
        queue.sync(sink)
        synchronized(this) { save() }
    }

    private fun load(): List<OfflineProgress> =
        prefs.getString(KEY, null)?.let { runCatching { ApiJson.decodeFromString(serializer, it) }.getOrNull() } ?: emptyList()

    private fun save() {
        // commit: the process may die right after the player closes.
        prefs.edit().putString(KEY, ApiJson.encodeToString(serializer, queue.entries)).commit()
    }

    private companion object {
        const val KEY = "queue"
    }
}
