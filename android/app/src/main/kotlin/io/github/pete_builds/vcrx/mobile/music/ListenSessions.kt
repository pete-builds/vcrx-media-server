package io.github.pete_builds.vcrx.mobile.music

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.ResolvingDataSource
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.User
import io.github.pete_builds.vcrx.mobile.offline.DownloadEligibility
import java.io.IOException
import java.net.URLEncoder
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** An open listen in Activity: its session, and how often to report progress. */
data class OpenListen(val sessionId: String, val heartbeatMs: Long)

/**
 * The listens of the audio player on an invited device, which is stream only:
 * the server hands it a track only inside its own open listen to that track,
 * named by session= on the URL. Media3 opens the next track of a queue ahead of
 * time, for gapless play, so a track's listen is opened when its data is first
 * asked for ([resolve], through [dataSource]), and the player's own listen for
 * that track then picks up the same one ([get]). Ported from the TV's
 * music/ListenSessions.kt.
 *
 * [pattern] finds a track's id in its URL (its first group). With [enabled]
 * false, as for the household's own accounts and profiles, URLs pass through
 * untouched and nothing is opened ahead.
 */
class ListenSessions(private val pattern: Regex, private val openListen: suspend (Long) -> OpenListen) {
    private val opening = Mutex()
    private val open = HashMap<Long, OpenListen>()

    /** Whether tracks are asked for inside a listen: true for an invited device. */
    @Volatile
    var enabled = false

    /** The listen for track [id], opened when there is none. */
    suspend fun get(id: Long): OpenListen = opening.withLock {
        synchronized(open) { open[id] } ?: openListen(id).also { l -> synchronized(open) { open[id] = l } }
    }

    /** Forgets track [id]'s listen, which the player is closing. */
    fun forget(id: Long) {
        synchronized(open) { open.remove(id) }
    }

    /** Forgets every listen still open, and returns their sessions to close: tracks loaded ahead and never played. */
    fun drain(): List<String> = synchronized(open) { open.values.map { it.sessionId }.also { open.clear() } }

    /** The track id [url] asks for, or null when it is not one of this player's files. */
    fun fileId(url: String): Long? = pattern.find(url)?.groupValues?.get(1)?.toLongOrNull()

    /**
     * The URL Media3 should open for [url]: unchanged for the household, and for an
     * invited device the same URL inside that track's own listen. Blocks while the
     * listen opens; Media3 calls it on its loader thread.
     */
    fun resolve(url: String): String {
        val id = if (enabled && !hasSession(url)) fileId(url) else null
        id ?: return url
        val listen = try {
            runBlocking { get(id) }
        } catch (e: ApiException) {
            throw IOException(e.message, e)
        }
        return withSession(url, listen.sessionId)
    }

    /** A data source that asks for every track of this player inside its listen. */
    @OptIn(UnstableApi::class)
    fun dataSource(upstream: DataSource.Factory): DataSource.Factory = ResolvingDataSource.Factory(upstream) { spec ->
        val url = spec.uri.toString()
        val resolved = resolve(url)
        if (resolved == url) spec else spec.withUri(Uri.parse(resolved))
    }

    companion object {
        /** Music track files and streams: /api/v1/music/tracks/{id}/file and /stream. */
        val TRACKS = Regex("/api/v1/music/tracks/(\\d+)/(?:file|stream)")

        private val SESSION = Regex("[?&]session=")

        fun hasSession(url: String): Boolean = SESSION.containsMatchIn(url)

        /** [url] with session= added, unless it already names one. */
        fun withSession(url: String, session: String): String =
            if (hasSession(url)) url else url + (if ('?' in url) "&" else "?") + "session=" + URLEncoder.encode(session, "UTF-8")

        /** Whether this session is stream only: an invited device, the same rule that hides Download. */
        fun streamOnly(me: User?, invitedDevice: Boolean): Boolean =
            invitedDevice || me?.kind == DownloadEligibility.KIND_TOKEN
    }
}
