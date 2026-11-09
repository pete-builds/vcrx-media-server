package io.github.pete_builds.vcrx.tv.music

import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.ResolvingDataSource
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.io.IOException
import java.net.URLEncoder

/** An open listen in Activity: its session, and how often to report progress. */
data class OpenListen(val sessionId: String, val heartbeatMs: Long)

/**
 * The listens of a player on an invited device, which is stream only: the
 * server hands it a track or a book's file only inside its own open listen to
 * that file, named by session= on the URL. Media3 opens the next file of a
 * playlist ahead of time, for gapless play, so the listen for a file is opened
 * when its data is first asked for ([dataSource]), and the player's own listen
 * for that file then picks up the same one ([get]).
 *
 * [pattern] finds a file's id in its URL (its first group). With [enabled]
 * false, as for the household's own accounts, URLs pass through untouched and
 * nothing is opened ahead.
 */
class ListenSessions(private val pattern: Regex, private val openListen: suspend (Long) -> OpenListen) {
    private val opening = Mutex()
    private val open = HashMap<Long, OpenListen>()

    /** Whether files are asked for inside a listen: true for an invited device. */
    @Volatile
    var enabled = false

    /** The listen for file [id], opened when there is none. */
    suspend fun get(id: Long): OpenListen = opening.withLock {
        synchronized(open) { open[id] } ?: openListen(id).also { l -> synchronized(open) { open[id] = l } }
    }

    /** Forgets file [id]'s listen, which the player is closing. */
    fun forget(id: Long) {
        synchronized(open) { open.remove(id) }
    }

    /** Forgets every listen still open, and returns their sessions to close: files loaded ahead and never played. */
    fun drain(): List<String> = synchronized(open) { open.values.map { it.sessionId }.also { open.clear() } }

    /** The file id [url] asks for, or null when it is not one of this player's files. */
    fun fileId(url: String): Long? = pattern.find(url)?.groupValues?.get(1)?.toLongOrNull()

    /** A data source that asks for every file of this player inside its listen. */
    @OptIn(UnstableApi::class)
    fun dataSource(upstream: DataSource.Factory): DataSource.Factory = ResolvingDataSource.Factory(upstream) { spec ->
        val url = spec.uri.toString()
        val id = if (enabled && !hasSession(url)) fileId(url) else null
        if (id == null) {
            spec
        } else {
            val listen = try {
                runBlocking { get(id) }
            } catch (e: ApiException) {
                throw IOException(e.message, e)
            }
            spec.withUri(Uri.parse(withSession(url, listen.sessionId)))
        }
    }

    companion object {
        private val SESSION = Regex("[?&]session=")

        fun hasSession(url: String): Boolean = SESSION.containsMatchIn(url)

        /** [url] with session= added, unless it already names one. */
        fun withSession(url: String, session: String): String =
            if (hasSession(url)) url else url + (if ('?' in url) "&" else "?") + "session=" + URLEncoder.encode(session, "UTF-8")
    }
}

/** Whether this device is stream only: an invited device. A server that cannot say is taken as not. */
suspend fun streamOnly(graph: AppGraph): Boolean = try {
    graph.api.me().kind == "token"
} catch (_: ApiException) {
    false
}

/** Closes the listens opened ahead for files that never played, through [close]. */
fun closeUnplayed(listens: ListenSessions, close: (String) -> Unit) {
    listens.drain().forEach(close)
}
