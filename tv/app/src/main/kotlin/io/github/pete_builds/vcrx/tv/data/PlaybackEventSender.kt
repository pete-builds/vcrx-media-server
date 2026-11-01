package io.github.pete_builds.vcrx.tv.data

import android.content.Context
import android.util.Log
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ServerApi
import io.github.pete_builds.vcrx.tv.net.PlaybackEvent
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import kotlinx.serialization.json.Json

/**
 * Sends playback events in order from the application scope, so they outlive the
 * player activity. A stop event is written to disk before it is sent and removed
 * only once the server has it, which keeps the resume position even when the user
 * presses Home or the network drops. A stop that fails for a passing reason (no
 * network, the server restarting) is tried again while the app runs, backing off
 * per [EventRetry]; [flushPending] also retries on the next launch.
 */
class PlaybackEventSender(
    context: Context,
    private val api: ServerApi,
    private val json: Json,
    private val scope: CoroutineScope,
) {
    private val prefs = context.getSharedPreferences("pending_events", Context.MODE_PRIVATE)
    private val queue = Channel<Queued>(Channel.UNLIMITED)

    init {
        scope.launch {
            for (item in queue) deliver(item)
        }
    }

    fun send(sessionId: String, event: PlaybackEvent) {
        val key = if (event.type == "stop") persist(sessionId, event) else null
        queue.trySend(Queued(sessionId, event, key))
    }

    /** Re-queues stop events that never reached the server. */
    fun flushPending() {
        for ((key, value) in prefs.all) {
            val pending = runCatching { json.decodeFromString(Pending.serializer(), value as String) }.getOrNull()
            if (pending == null) {
                prefs.edit().remove(key).apply()
                continue
            }
            queue.trySend(Queued(pending.sessionId, pending.event, key))
        }
    }

    private fun persist(sessionId: String, event: PlaybackEvent): String {
        val key = "$sessionId:${System.currentTimeMillis()}"
        // commit, not apply: the process may die right after onStop.
        prefs.edit().putString(key, json.encodeToString(Pending.serializer(), Pending(sessionId, event))).commit()
        return key
    }

    private suspend fun deliver(item: Queued) {
        try {
            api.postEvent(item.sessionId, item.event)
            item.pendingKey?.let { prefs.edit().remove(it).apply() }
        } catch (e: ApiException) {
            val key = item.pendingKey
            if (EventRetry.permanent(e)) {
                // 404 and 409 mean the server already closed or forgot the session: drop it.
                key?.let { prefs.edit().remove(it).apply() }
            } else if (key != null) {
                // A TV app's process lives for days, so waiting for the next launch
                // could mean days without the resume position.
                val wait = EventRetry.backoffMs(item.attempt)
                scope.launch {
                    delay(wait)
                    queue.trySend(item.copy(attempt = item.attempt + 1))
                }
            }
            Log.w(TAG, "Event ${item.event.type} for ${item.sessionId} not sent: ${e.message}")
        }
    }

    private data class Queued(val sessionId: String, val event: PlaybackEvent, val pendingKey: String?, val attempt: Int = 0)

    @Serializable
    private data class Pending(
        @SerialName("session_id") val sessionId: String,
        val event: PlaybackEvent,
    )

    private companion object {
        const val TAG = "PlaybackEvents"
    }
}

/** Which failed events are worth sending again, and when. Pure, so it is unit tested. */
object EventRetry {
    /** The server answered and will not take it: signed out, or a 4xx for this session. */
    fun permanent(e: ApiException): Boolean =
        e is ApiException.Unauthorized || (e is ApiException.Http && e.status in 400..499)

    /** 15 s, doubling to at most 10 minutes. */
    fun backoffMs(attempt: Int): Long = (FIRST_MS shl attempt.coerceIn(0, 6)).coerceAtMost(MAX_MS)

    private const val FIRST_MS = 15_000L
    private const val MAX_MS = 600_000L
}
