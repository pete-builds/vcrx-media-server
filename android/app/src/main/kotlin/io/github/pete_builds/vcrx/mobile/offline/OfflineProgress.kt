package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.ApiException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Where a downloaded title was left while the app could not reach the server. */
@Serializable
data class OfflineProgress(
    @SerialName("item_id") val itemId: Long,
    @SerialName("version_id") val versionId: Long? = null,
    @SerialName("position_ms") val positionMs: Long,
    @SerialName("duration_ms") val durationMs: Long? = null,
    /** Watched to the end. */
    val played: Boolean = false,
    @SerialName("recorded_at") val recordedAt: Long = 0,
)

/** What the sync needs from the server, so the queue is tested without one. */
interface ProgressSink {
    suspend fun markPlayed(itemId: Long)

    suspend fun savePosition(entry: OfflineProgress)
}

/**
 * Progress made offline, one entry per title (the newest wins), sent to the
 * server when it is reachable again. Pure: persistence is the caller's, through
 * [entries] and the constructor.
 */
class OfflineProgressQueue(initial: List<OfflineProgress> = emptyList()) {
    private val byItem = LinkedHashMap<Long, OfflineProgress>()

    init {
        initial.forEach { record(it) }
    }

    val entries: List<OfflineProgress> get() = byItem.values.toList()

    val isEmpty: Boolean get() = byItem.isEmpty()

    /** Keeps [entry] unless an entry recorded later is already queued for the title. */
    fun record(entry: OfflineProgress) {
        val old = byItem[entry.itemId]
        if (old != null && old.recordedAt > entry.recordedAt) return
        byItem[entry.itemId] = entry
    }

    /** The resume point the app knows best for [itemId]: queued offline progress, else [serverMs]. */
    fun resumeFor(itemId: Long, serverMs: Long?): Long {
        val local = byItem[itemId] ?: return serverMs ?: 0
        return if (local.played) 0 else local.positionMs
    }

    /**
     * Sends every entry, oldest first. An entry the server took, or refused for
     * good (the title is gone, or this session may not see it), leaves the queue.
     * Unreachable or a server error stops the sync and keeps the rest for next
     * time. Returns how many were sent.
     */
    suspend fun sync(sink: ProgressSink): Int {
        var sent = 0
        for (entry in entries.sortedBy { it.recordedAt }) {
            try {
                if (entry.played) sink.markPlayed(entry.itemId) else sink.savePosition(entry)
                sent++
                remove(entry)
            } catch (e: ApiException) {
                if (permanent(e)) remove(entry) else break
            }
        }
        return sent
    }

    private fun remove(entry: OfflineProgress) {
        if (byItem[entry.itemId] == entry) byItem.remove(entry.itemId)
    }

    companion object {
        /** A refusal that will not change on a retry. A 401 is not one: sign in again and the entry still goes. */
        fun permanent(e: ApiException): Boolean = e is ApiException.Http && e.status in 400..499 && e.status != 408 && e.status != 429

        /** Watched to the end, as the server counts it: within the last 5% or the last two minutes. */
        fun isPlayed(positionMs: Long, durationMs: Long?): Boolean {
            val d = durationMs?.takeIf { it > 0 } ?: return false
            return positionMs >= d - minOf(d / 20, 120_000L)
        }
    }
}
