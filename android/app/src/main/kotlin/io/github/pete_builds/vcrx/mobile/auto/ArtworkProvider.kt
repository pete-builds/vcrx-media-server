package io.github.pete_builds.vcrx.mobile.auto

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.net.Uri
import android.os.ParcelFileDescriptor
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.offline.ArtStore
import java.io.FileNotFoundException

/**
 * Serves album covers, artist pictures, book covers and podcast artwork to Android Auto and the media
 * notification as content URIs. Auto loads artwork in its own process, where
 * the server's bearer token is not, so it cannot fetch the http URLs itself:
 * this provider fetches them with the app's client (or reads the copy kept for
 * a download, which works offline). Not exported; the session grants read
 * access to each URI it hands a browser.
 */
class ArtworkProvider : ContentProvider() {
    override fun onCreate(): Boolean = true

    override fun openFile(uri: Uri, mode: String): ParcelFileDescriptor {
        if (mode != "r") throw FileNotFoundException("read only")
        val ctx = context ?: throw FileNotFoundException("no context")
        val segments = uri.pathSegments
        if (segments.size != 2) throw FileNotFoundException("not artwork")
        val kind = segments[0]
        val id = segments[1].toLongOrNull() ?: throw FileNotFoundException("not artwork")
        val file = ArtStore.existing(ctx, kind, id) ?: run {
            val graph = ctx.graph
            val url = when (kind) {
                ArtStore.ALBUM -> graph.api.absoluteUrl("/api/v1/music/albums/$id/cover?width=$SIZE")
                ArtStore.ARTIST -> graph.api.absoluteUrl("/api/v1/music/artists/$id/image?width=$SIZE")
                ArtStore.ITEM -> graph.api.artworkUrl(id, "poster", SIZE)
                ArtStore.BOOK -> graph.api.absoluteUrl("/api/v1/audiobooks/books/$id/cover?width=$SIZE")
                ArtStore.PODCAST -> graph.api.absoluteUrl("/api/v1/podcasts/shows/$id/artwork")
                ArtStore.EPISODE -> graph.api.absoluteUrl("/api/v1/podcasts/episodes/$id/artwork")
                else -> null
            } ?: throw FileNotFoundException("no server")
            ArtStore.fetch(ctx, graph.httpClient, kind, id, url, keep = false)
        } ?: throw FileNotFoundException("no artwork")
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
    }

    override fun getType(uri: Uri): String = "image/jpeg"

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null

    override fun insert(uri: Uri, values: ContentValues?): Uri? = null

    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0

    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    companion object {
        private const val SIZE = 480

        fun uri(context: Context, art: ArtRef): Uri =
            Uri.Builder().scheme("content").authority("${context.packageName}.artwork").appendPath(art.kind).appendPath(art.id.toString()).build()
    }
}
