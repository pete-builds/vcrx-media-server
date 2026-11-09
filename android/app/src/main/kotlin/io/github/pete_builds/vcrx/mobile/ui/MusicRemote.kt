package io.github.pete_builds.vcrx.mobile.ui

import android.content.ComponentName
import android.content.Context
import android.os.Bundle
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
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionToken
import io.github.pete_builds.vcrx.mobile.auto.AudioKind
import io.github.pete_builds.vcrx.mobile.auto.BrowseId
import io.github.pete_builds.vcrx.mobile.auto.Commands
import io.github.pete_builds.vcrx.mobile.auto.PlaybackService
import io.github.pete_builds.vcrx.mobile.music.BookPosition
import kotlinx.coroutines.delay
import kotlinx.coroutines.guava.await

/** What the mini player and Now Playing draw. */
data class NowPlaying(
    val title: String,
    val artist: String,
    val artworkUri: String?,
    val playing: Boolean,
    val mediaId: String,
    val kind: AudioKind = AudioKind.Music,
    val speed: Float = 1f,
    /** For a book: its id, and the place and length across the whole book. */
    val book: BookNow? = null,
    /** When the sleep timer pauses: wall clock ms, -1 for the end of the chapter, 0 for off. */
    val sleepAt: Long = 0L,
)

/** Where the listener is in the book playing, from what the service publishes. */
data class BookNow(
    val bookId: Long,
    val positionS: Double,
    val durationS: Double,
    val chapter: Int,
    val chapterCount: Int,
    val chapterTitle: String?,
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

    /** A book from where the listener left off, or from [atMs] into the whole book (0 for the start). */
    fun playBook(bookId: Long, atMs: Long? = null) = playIds(listOf(if (atMs != null) BrowseId.bookAt(bookId, atMs) else BrowseId.book(bookId)))

    fun playDownloadedBook(bookId: Long) = playIds(listOf(BrowseId.downloadedBook(bookId)))

    fun playEpisode(episodeId: Long) = playIds(listOf(BrowseId.episode(episodeId)))

    fun playDownloadedEpisode(episodeId: Long) = playIds(listOf(BrowseId.downloadedEpisode(episodeId)))

    /** Back 15 s, across a book's files. */
    fun skipBack() = command(Commands.SKIP_BACK)

    /** Forward 30 s, across a book's files. */
    fun skipForward() = command(Commands.SKIP_FORWARD)

    fun nextChapter() = command(Commands.NEXT_CHAPTER)

    fun previousChapter() = command(Commands.PREVIOUS_CHAPTER)

    /** Moves to [seconds] from the start of the whole book. */
    fun seekBook(seconds: Double) = command(Commands.SEEK_BOOK, Bundle().apply { putDouble(Commands.ARG_SECONDS, seconds) })

    /** Minutes, [io.github.pete_builds.vcrx.mobile.music.SleepTimer.END_OF_CHAPTER] or OFF. */
    fun sleep(choice: Int) = command(Commands.SLEEP, Bundle().apply { putInt(Commands.ARG_MINUTES, choice) })

    fun setSpeed(speed: Float) = controller.setPlaybackSpeed(speed)

    private fun command(action: String, args: Bundle = Bundle.EMPTY) {
        controller.sendCustomCommand(SessionCommand(action, Bundle.EMPTY), args)
    }

    fun toggle() = if (controller.isPlaying) controller.pause() else controller.play()

    fun next() = controller.seekToNextMediaItem()

    fun previous() = controller.seekToPrevious()

    fun seekTo(ms: Long) = controller.seekTo(ms)

    val positionMs: Long get() = controller.currentPosition
    val durationMs: Long get() = controller.duration.coerceAtLeast(0)

    fun now(): NowPlaying? {
        val item = controller.currentMediaItem ?: return null
        val m = item.mediaMetadata
        val kind = BrowseId.audioKind(item.mediaId)
        val extras = controller.sessionExtras
        return NowPlaying(
            title = m.title?.toString().orEmpty(),
            artist = m.artist?.toString().orEmpty(),
            artworkUri = m.artworkUri?.toString(),
            playing = controller.isPlaying,
            mediaId = item.mediaId,
            kind = kind,
            speed = controller.playbackParameters.speed,
            book = if (kind == AudioKind.Book) book(extras, m.extras?.getLong(PlaybackService.EXTRA_CONVERTED_START_MS) ?: 0L) else null,
            sleepAt = extras.getLong(PlaybackService.KEY_SLEEP_AT),
        )
    }

    private fun book(extras: Bundle, convertedStartMs: Long): BookNow? {
        val id = extras.getLong(PlaybackService.KEY_BOOK_ID).takeIf { it != 0L } ?: return null
        val starts = extras.getDoubleArray(PlaybackService.KEY_FILE_STARTS) ?: return null
        val chapterStarts = extras.getDoubleArray(PlaybackService.KEY_CHAPTER_STARTS) ?: DoubleArray(0)
        val titles = extras.getStringArray(PlaybackService.KEY_CHAPTER_TITLES) ?: emptyArray()
        val pos = BookPosition.inBook(starts, controller.currentMediaItemIndex, convertedStartMs, controller.currentPosition)
        val chapter = BookPosition.chapterIndex(chapterStarts, pos)
        return BookNow(id, pos, extras.getDouble(PlaybackService.KEY_BOOK_DURATION_S), chapter, chapterStarts.size, titles.getOrNull(chapter))
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
