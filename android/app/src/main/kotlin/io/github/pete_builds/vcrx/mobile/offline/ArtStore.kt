package io.github.pete_builds.vcrx.mobile.offline

import android.content.Context
import java.io.File
import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * Artwork on disk: kept for downloads (files/offline-art, survives until the
 * download is deleted) and cached for Android Auto and the notification
 * (cache/art, which Android may clear). Only numeric ids of known kinds, so a
 * path can never name anything else.
 */
object ArtStore {
    const val ALBUM = "album"
    const val ARTIST = "artist"
    const val ITEM = "item"
    private val KINDS = setOf(ALBUM, ARTIST, ITEM)

    fun kept(context: Context, kind: String, id: Long): File = File(File(context.filesDir, "offline-art"), "$kind-$id.jpg")

    private fun cached(context: Context, kind: String, id: Long): File = File(File(context.cacheDir, "art"), "$kind-$id.jpg")

    /** The file on disk for [kind] [id], if either copy exists. */
    fun existing(context: Context, kind: String, id: Long): File? {
        if (kind !in KINDS) return null
        return kept(context, kind, id).takeIf { it.isFile } ?: cached(context, kind, id).takeIf { it.isFile }
    }

    /** Downloads [url] to disk; blocking, so call it off the main thread. Null when the server has none. */
    fun fetch(context: Context, client: OkHttpClient, kind: String, id: Long, url: String, keep: Boolean): File? {
        if (kind !in KINDS) return null
        val target = if (keep) kept(context, kind, id) else cached(context, kind, id)
        if (target.isFile) return target
        return runCatching {
            client.newCall(Request.Builder().url(url).build()).execute().use { response ->
                if (!response.isSuccessful) return null
                target.parentFile?.mkdirs()
                val tmp = File(target.path + ".part")
                tmp.outputStream().use { response.body.byteStream().copyTo(it) }
                tmp.renameTo(target)
                target
            }
        }.getOrNull()
    }
}
