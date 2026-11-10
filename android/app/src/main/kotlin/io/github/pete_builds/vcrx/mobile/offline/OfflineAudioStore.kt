package io.github.pete_builds.vcrx.mobile.offline

import android.content.Context
import io.github.pete_builds.vcrx.mobile.net.ApiJson
import io.github.pete_builds.vcrx.mobile.net.AudiobookDetail
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.PodcastProgressRequest
import io.github.pete_builds.vcrx.mobile.net.ServerApi
import io.github.pete_builds.vcrx.mobile.net.podcastSetPlayed
import io.github.pete_builds.vcrx.mobile.net.podcastSetProgress
import io.github.pete_builds.vcrx.mobile.net.saveAudiobookProgress
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer

/**
 * What the phone keeps to play books and episodes with no server: each
 * downloaded book's files and chapters, each downloaded episode, the episodes
 * played lately (Android Auto's Continue listening), and the listener's place
 * in each ([ListenPlaces]), sent to the server by [sync] when it is back.
 */
class OfflineAudioStore(context: Context) {
    private val prefs = context.getSharedPreferences("offline_audio", Context.MODE_PRIVATE)
    private val placeList = ListSerializer(ListenPlace.serializer())
    private val bookList = ListSerializer(AudiobookDetail.serializer())
    private val episodeList = ListSerializer(PodcastEpisode.serializer())
    private val places = ListenPlaces(load(KEY_LATEST, placeList), load(KEY_PENDING, placeList))
    private val lock = Mutex()

    // ---- places ----

    @Synchronized
    fun record(place: ListenPlace, sent: Boolean) {
        places.record(place, sent)
        persistPlaces()
    }

    private fun persistPlaces() {
        // commit: the process may die right after the player stops.
        prefs.edit()
            .putString(KEY_LATEST, ApiJson.encodeToString(placeList, places.latest.sortedBy { it.recordedAt }.takeLast(MAX_PLACES)))
            .putString(KEY_PENDING, ApiJson.encodeToString(placeList, places.pending))
            .commit()
    }

    @Synchronized
    fun resumeFor(kind: String, id: Long, serverS: Double?, serverFinished: Boolean = false): Double =
        places.resumeFor(kind, id, serverS, serverFinished)

    @Synchronized
    fun last(kind: String, id: Long): ListenPlace? = places.last(kind, id)

    val pending: Int @Synchronized get() = places.pending.size

    suspend fun sync(api: ServerApi) = lock.withLock {
        val sink = object : ListenSink {
            override suspend fun saveBook(place: ListenPlace) = send(api, place)

            override suspend fun saveEpisode(place: ListenPlace) = send(api, place)
        }
        // The queue is only touched under the monitor; the network calls run outside it.
        // A place recorded while this runs is newer than the snapshot and stays pending.
        val snapshot = synchronized(this) { places.pending }
        if (snapshot.isEmpty()) return@withLock
        val copy = ListenPlaces(pending = snapshot)
        copy.sync(sink)
        synchronized(this) {
            snapshot.filter { it !in copy.pending }.forEach { places.record(it, sent = true) }
            persistPlaces()
        }
    }

    // ---- downloaded books and episodes ----

    @Synchronized
    fun book(bookId: Long): AudiobookDetail? = books().firstOrNull { it.id == bookId }

    @Synchronized
    fun books(): List<AudiobookDetail> = load(KEY_BOOKS, bookList)

    @Synchronized
    fun saveBook(book: AudiobookDetail) {
        // The description can be long and nothing offline shows it.
        val slim = book.copy(description = null, progress = null)
        save(KEY_BOOKS, bookList, books().filter { it.id != book.id } + slim)
    }

    @Synchronized
    fun forgetBook(bookId: Long) {
        save(KEY_BOOKS, bookList, books().filter { it.id != bookId })
    }

    @Synchronized
    fun episode(episodeId: Long): PodcastEpisode? = episodes().firstOrNull { it.id == episodeId }

    @Synchronized
    fun episodes(): List<PodcastEpisode> = load(KEY_EPISODES, episodeList)

    @Synchronized
    fun saveEpisode(episode: PodcastEpisode) {
        save(KEY_EPISODES, episodeList, episodes().filter { it.id != episode.id } + episode.copy(description = ""))
    }

    @Synchronized
    fun forgetEpisode(episodeId: Long) {
        save(KEY_EPISODES, episodeList, episodes().filter { it.id != episodeId })
    }

    // ---- episodes played lately ----

    /** Episodes played lately, newest first, with the place the phone last saw in each. */
    @Synchronized
    fun recentEpisodes(): List<PodcastEpisode> = load(KEY_RECENT_EPISODES, episodeList).map { e ->
        places.last(ListenPlace.EPISODE, e.id)?.let { p -> e.copy(positionSeconds = p.positionS, played = p.finished) } ?: e
    }

    @Synchronized
    fun notePlayed(episode: PodcastEpisode) {
        val list = (listOf(episode.copy(description = "")) + load(KEY_RECENT_EPISODES, episodeList).filter { it.id != episode.id }).take(12)
        save(KEY_RECENT_EPISODES, episodeList, list)
    }

    @Synchronized
    fun clearAccountData() {
        prefs.edit().remove(KEY_RECENT_EPISODES).apply()
    }

    private fun <T> load(key: String, serializer: KSerializer<List<T>>): List<T> =
        prefs.getString(key, null)?.let { runCatching { ApiJson.decodeFromString(serializer, it) }.getOrNull() } ?: emptyList()

    private fun <T> save(key: String, serializer: KSerializer<List<T>>, list: List<T>) {
        prefs.edit().putString(key, ApiJson.encodeToString(serializer, list)).commit()
    }

    companion object {
        /**
         * Saves [place] on the server: a book's place (finished at the end), or an
         * episode's place, or played once it reached the end. Throws [io.github.pete_builds.vcrx.mobile.net.ApiException].
         */
        suspend fun send(api: ServerApi, place: ListenPlace) {
            when {
                place.kind == ListenPlace.BOOK -> api.saveAudiobookProgress(place.id, place.positionS, if (place.finished) true else null)
                place.finished -> api.podcastSetPlayed(place.id, true)
                else -> api.podcastSetProgress(place.id, PodcastProgressRequest(place.positionS, place.durationS))
            }
        }

        private const val KEY_LATEST = "latest"
        private const val KEY_PENDING = "pending"
        private const val KEY_BOOKS = "books"
        private const val KEY_EPISODES = "episodes"
        private const val KEY_RECENT_EPISODES = "recent_episodes"
        private const val MAX_PLACES = 200
    }
}
