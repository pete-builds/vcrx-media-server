package io.github.pete_builds.vcrx.mobile.offline

import io.github.pete_builds.vcrx.mobile.net.ApiException
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Where the listener is in a book or an episode, as the phone last knew it. */
@Serializable
data class ListenPlace(
    /** [BOOK] or [EPISODE]. */
    val kind: String,
    val id: Long,
    /** Seconds: from the start of the whole book, or into the episode. */
    @SerialName("position_s") val positionS: Double,
    /** What the player measured, for an episode whose feed gives no length. */
    @SerialName("duration_s") val durationS: Double? = null,
    /** Listened to the end: a finished book, a played episode. */
    val finished: Boolean = false,
    @SerialName("recorded_at") val recordedAt: Long = 0,
) {
    val key: String get() = key(kind, id)

    companion object {
        const val BOOK = "book"
        const val EPISODE = "episode"

        fun key(kind: String, id: Long) = "$kind:$id"
    }
}

/** What the sync needs from the server, so the queue is tested without one. */
interface ListenSink {
    suspend fun saveBook(place: ListenPlace)

    suspend fun saveEpisode(place: ListenPlace)
}

/**
 * The listener's place in books and episodes, kept on the phone. Every save is
 * recorded here ([latest]), so a downloaded book resumes in the right place
 * with no server; one the server did not take (offline, or it failed) is also
 * [pending] until [sync] sends it, as OfflineProgressQueue does for video.
 * Pure: persistence is the caller's, through [snapshot] and the constructor.
 */
class ListenPlaces(latest: List<ListenPlace> = emptyList(), pending: List<ListenPlace> = emptyList()) {
    private val latestByKey = LinkedHashMap<String, ListenPlace>()
    private val pendingByKey = LinkedHashMap<String, ListenPlace>()

    init {
        latest.forEach { put(latestByKey, it) }
        pending.forEach { put(pendingByKey, it) }
    }

    val latest: List<ListenPlace> get() = latestByKey.values.toList()

    val pending: List<ListenPlace> get() = pendingByKey.values.toList()

    /** Notes [place]; [sent] true when the server already took it, false to queue it for [sync]. */
    fun record(place: ListenPlace, sent: Boolean) {
        put(latestByKey, place)
        if (sent) {
            // The server has this place or a later one: an older queued one must not overwrite it.
            pendingByKey[place.key]?.let { if (it.recordedAt <= place.recordedAt) pendingByKey.remove(place.key) }
        } else {
            put(pendingByKey, place)
        }
    }

    /**
     * Where to start [kind] [id], in seconds. A place still waiting to be sent is
     * newer than anything the server knows; else the server's [serverS] when
     * there is an answer; else the last place the phone saw. A finished one starts
     * from the top.
     */
    fun resumeFor(kind: String, id: Long, serverS: Double?, serverFinished: Boolean = false): Double {
        val key = ListenPlace.key(kind, id)
        pendingByKey[key]?.let { return if (it.finished) 0.0 else it.positionS }
        if (serverS != null) return if (serverFinished) 0.0 else serverS
        val local = latestByKey[key] ?: return 0.0
        return if (local.finished) 0.0 else local.positionS
    }

    /** The last place the phone saw for [kind] [id], if any. */
    fun last(kind: String, id: Long): ListenPlace? = latestByKey[ListenPlace.key(kind, id)]

    /** Forgets [kind] [id], as when its download is deleted. A place still pending stays until sent. */
    fun forget(kind: String, id: Long) {
        latestByKey.remove(ListenPlace.key(kind, id))
    }

    /**
     * Sends every pending place, oldest first. One the server took, or refused for
     * good (the book is gone, or this session may not see it), leaves the queue.
     * Unreachable or a server error stops the sync and keeps the rest for next
     * time. Returns how many were sent.
     */
    suspend fun sync(sink: ListenSink): Int {
        var sent = 0
        for (place in pending.sortedBy { it.recordedAt }) {
            try {
                if (place.kind == ListenPlace.BOOK) sink.saveBook(place) else sink.saveEpisode(place)
                sent++
                remove(place)
            } catch (e: ApiException) {
                if (OfflineProgressQueue.permanent(e)) remove(place) else break
            }
        }
        return sent
    }

    private fun remove(place: ListenPlace) {
        if (pendingByKey[place.key] == place) pendingByKey.remove(place.key)
    }

    private fun put(map: LinkedHashMap<String, ListenPlace>, place: ListenPlace) {
        val old = map[place.key]
        if (old != null && old.recordedAt > place.recordedAt) return
        map[place.key] = place
    }
}
