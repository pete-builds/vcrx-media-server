package io.github.pete_builds.vcrx.mobile.ui

import android.content.ComponentName
import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import io.github.pete_builds.vcrx.mobile.auto.BrowseId
import io.github.pete_builds.vcrx.mobile.auto.PlaybackService
import kotlinx.coroutines.delay
import kotlinx.coroutines.guava.await

/** What the mini player and Now Playing draw. */
data class NowPlaying(
    val title: String,
    val artist: String,
    val artworkUri: String?,
    val playing: Boolean,
    val mediaId: String,
)

/**
 * The phone's screens drive music through a MediaController bound to
 * [PlaybackService], exactly as Android Auto does, so playback carries on in
 * the background with notification controls. Commands are media ids; the
 * service resolves them.
 */
class MusicRemote(private val controller: MediaController) {
    fun playIds(ids: List<String>, start: Int = 0, shuffle: Boolean = false) {
        controller.shuffleModeEnabled = shuffle
        controller.setMediaItems(ids.map { MediaItem.Builder().setMediaId(it).build() }, start, 0)
        controller.prepare()
        controller.play()
    }

    fun playAlbum(albumId: Long, trackId: Long? = null, shuffle: Boolean = false) =
        playIds(listOf(trackId?.let { BrowseId.albumTrack(albumId, it) } ?: BrowseId.album(albumId)), shuffle = shuffle)

    fun playArtist(artistId: Long) = playIds(listOf(BrowseId.artist(artistId)))

    fun playDownloadedTrack(trackId: Long) = playIds(listOf(BrowseId.downloadedTrack(trackId)))

    fun toggle() = if (controller.isPlaying) controller.pause() else controller.play()

    fun next() = controller.seekToNextMediaItem()

    fun previous() = controller.seekToPrevious()

    fun seekTo(ms: Long) = controller.seekTo(ms)

    val positionMs: Long get() = controller.currentPosition
    val durationMs: Long get() = controller.duration.coerceAtLeast(0)

    fun now(): NowPlaying? {
        val item = controller.currentMediaItem ?: return null
        val m = item.mediaMetadata
        return NowPlaying(m.title?.toString().orEmpty(), m.artist?.toString().orEmpty(), m.artworkUri?.toString(), controller.isPlaying, item.mediaId)
    }

    val raw: Player get() = controller

    companion object {
        suspend fun connect(context: Context): MusicRemote {
            val token = SessionToken(context, ComponentName(context, PlaybackService::class.java))
            return MusicRemote(MediaController.Builder(context, token).buildAsync().await())
        }
    }
}

/** The remote, connected for as long as the calling screen shows; null until it connects. */
@Composable
fun rememberMusicRemote(): MusicRemote? {
    val context = LocalContext.current
    var remote by remember { mutableStateOf<MusicRemote?>(null) }
    LaunchedEffect(Unit) { remote = runCatching { MusicRemote.connect(context) }.getOrNull() }
    DisposableEffect(Unit) { onDispose { remote?.raw?.release() } }
    return remote
}

/** [MusicRemote.now], refreshed as the player changes and twice a second for the position. */
@Composable
fun rememberNowPlaying(remote: MusicRemote?): Pair<NowPlaying?, Long> {
    var now by remember { mutableStateOf<NowPlaying?>(null) }
    var position by remember { mutableLongStateOf(0L) }
    LaunchedEffect(remote) {
        while (remote != null) {
            now = remote.now()
            position = remote.positionMs
            delay(500)
        }
    }
    return now to position
}
