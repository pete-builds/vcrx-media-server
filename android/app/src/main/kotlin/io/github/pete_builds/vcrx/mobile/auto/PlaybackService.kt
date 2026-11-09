package io.github.pete_builds.vcrx.mobile.auto

import android.app.PendingIntent
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.Toast
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.CommandButton
import androidx.media3.session.LibraryResult
import androidx.media3.session.MediaLibraryService
import androidx.media3.session.MediaSession
import androidx.media3.session.SessionCommand
import androidx.media3.session.SessionResult
import com.google.common.collect.ImmutableList
import com.google.common.util.concurrent.Futures
import com.google.common.util.concurrent.ListenableFuture
import io.github.pete_builds.vcrx.mobile.AppGraph
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.music.BookSeek
import io.github.pete_builds.vcrx.mobile.music.BookSpan
import io.github.pete_builds.vcrx.mobile.music.BookTimeline
import io.github.pete_builds.vcrx.mobile.music.ListenSessions
import io.github.pete_builds.vcrx.mobile.music.OpenListen
import io.github.pete_builds.vcrx.mobile.music.PodcastPlayback
import io.github.pete_builds.vcrx.mobile.music.ProgressClock
import io.github.pete_builds.vcrx.mobile.music.SleepTimer
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.MusicAlbum
import io.github.pete_builds.vcrx.mobile.net.MusicQueueRequest
import io.github.pete_builds.vcrx.mobile.net.MusicTrack
import io.github.pete_builds.vcrx.mobile.net.PlaybackEvent
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.audiobook
import io.github.pete_builds.vcrx.mobile.net.audiobookListen
import io.github.pete_builds.vcrx.mobile.net.audiobookQueue
import io.github.pete_builds.vcrx.mobile.net.audiobooks
import io.github.pete_builds.vcrx.mobile.net.audiobooksInProgress
import io.github.pete_builds.vcrx.mobile.net.musicAlbum
import io.github.pete_builds.vcrx.mobile.net.musicAlbums
import io.github.pete_builds.vcrx.mobile.net.musicArtist
import io.github.pete_builds.vcrx.mobile.net.musicArtists
import io.github.pete_builds.vcrx.mobile.net.musicCapabilities
import io.github.pete_builds.vcrx.mobile.net.musicListen
import io.github.pete_builds.vcrx.mobile.net.musicQueue
import io.github.pete_builds.vcrx.mobile.net.podcastEpisode
import io.github.pete_builds.vcrx.mobile.net.podcastNewEpisodes
import io.github.pete_builds.vcrx.mobile.net.podcastShow
import io.github.pete_builds.vcrx.mobile.net.podcasts
import io.github.pete_builds.vcrx.mobile.offline.DownloadGroups
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys
import io.github.pete_builds.vcrx.mobile.offline.DownloadMeta
import io.github.pete_builds.vcrx.mobile.offline.ListenPlace
import io.github.pete_builds.vcrx.mobile.offline.OfflineAudioStore
import io.github.pete_builds.vcrx.mobile.offline.OfflineProgressQueue
import io.github.pete_builds.vcrx.mobile.ui.MainActivity
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.guava.future
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * The one audio player: music, audiobooks and podcast episodes on the phone
 * with notification and lock screen controls, and the media app Android Auto
 * browses. Auto sees only audio. Every playable id is resolved here
 * ([BrowseId]), so the phone's own screens and Auto start playback the same
 * way: by media id, through a MediaController.
 *
 * A book is its files as one playlist ([BookTimeline]); a place in it is
 * seconds across the whole book. Books and episodes save their place every
 * [PodcastPlayback.SAVE_EVERY_MS] while playing, on pause, on a new queue and
 * at the end; a save the server does not take is kept on the phone
 * ([OfflineAudioStore]) and sent when it is back. Each kind keeps its own
 * speed (music always 1), and the screens send the book commands ([Commands]):
 * skips, chapters, a seek in the whole book, and the sleep timer.
 */
@OptIn(UnstableApi::class)
class PlaybackService : MediaLibraryService() {
    private val scope = MainScope()
    private var session: MediaLibrarySession? = null
    private lateinit var app: AppGraph
    private lateinit var player: ExoPlayer
    private val searches = HashMap<String, List<BrowseNode>>()

    /** The listen in Activity for what plays now: a track's or a book file's. */
    private data class Listen(val sessionId: String, val kind: AudioKind, val id: Long, val heartbeatMs: Long)

    private var listen: Listen? = null
    private var lastBeat = 0L

    /** What the queue is: decides the speed kept, the notification's buttons, and whether a place is saved. */
    private var queueKind = AudioKind.Music

    /** The book playing, if one is: its files and chapters. */
    private var book: BookTimeline? = null

    /** The episode playing, if one is. */
    private var episode: PodcastEpisode? = null
    private val clock = ProgressClock()
    private var ticker: Job? = null

    /** When the sleep timer pauses: wall clock ms, or [SLEEP_END_OF_CHAPTER] with [sleepChapterEndS]. 0 is off. */
    private var sleepAt = 0L
    private var sleepChapterEndS = 0.0

    /**
     * On an invited device the server hands out a track, or a book's file, only
     * inside that device's own listen to it, so every such URL Media3 opens
     * carries its listen's session (see [ListenSessions]); for the household
     * nothing changes.
     */
    private val trackListens = ListenSessions(ListenSessions.TRACKS) { id ->
        app.api.musicListen(id, musicCapabilities(app.capabilities)).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
    }
    private val bookListens = ListenSessions(ListenSessions.BOOK_FILES) { id ->
        app.api.audiobookListen(id, musicCapabilities(app.capabilities)).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
    }

    override fun onCreate() {
        super.onCreate()
        app = graph
        refreshStreamOnly()
        player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(app.downloads.readingFactory { trackListens.dataSource(bookListens.dataSource(it)) }))
            .setAudioAttributes(attributes(AudioKind.Music), true)
            .setHandleAudioBecomingNoisy(true)
            .setWakeMode(C.WAKE_MODE_NETWORK)
            .setSeekBackIncrementMs(PodcastPlayback.SKIP_BACK_MS)
            .setSeekForwardIncrementMs(PodcastPlayback.SKIP_FORWARD_MS)
            .build()
        player.addListener(listenTracker)
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        session = MediaLibrarySession.Builder(this, player, Callback()).setSessionActivity(open).build()
        startTicker()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaLibrarySession? = session

    override fun onTaskRemoved(rootIntent: Intent?) {
        val p = session?.player
        if (p == null || !p.playWhenReady || p.mediaItemCount == 0) stopSelf()
    }

    override fun onDestroy() {
        savePlace(force = true)
        endListen()
        closeUnplayed()
        ticker?.cancel()
        session?.run {
            player.release()
            release()
        }
        session = null
        scope.cancel()
        super.onDestroy()
    }

    private fun kindOf(item: MediaItem?): AudioKind = item?.mediaId?.let { BrowseId.audioKind(it) } ?: AudioKind.Music

    private fun attributes(kind: AudioKind): AudioAttributes = AudioAttributes.Builder()
        .setUsage(C.USAGE_MEDIA)
        .setContentType(if (kind == AudioKind.Music) C.AUDIO_CONTENT_TYPE_MUSIC else C.AUDIO_CONTENT_TYPE_SPEECH)
        .build()

    // Each track or book file that starts opens a listen in Activity, as the web and
    // TV players do. On an invited device the listen may already be open: Media3
    // asked for the file's data ahead of time, inside it, and this picks up the same one.
    private val listenTracker = object : Player.Listener {
        override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
            endListen()
            val extras = mediaItem?.mediaMetadata?.extras ?: return
            val kind = kindOf(mediaItem)
            if (!app.online.value) return
            val id = when (kind) {
                AudioKind.Music -> extras.getLong(EXTRA_TRACK_ID)
                AudioKind.Book -> extras.getLong(EXTRA_FILE_ID)
                AudioKind.Episode -> return
            }
            if (id == 0L) return
            scope.launch {
                runCatching {
                    when {
                        kind == AudioKind.Music && trackListens.enabled -> trackListens.get(id)
                        kind == AudioKind.Music -> app.api.musicListen(id, musicCapabilities(app.capabilities)).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
                        bookListens.enabled -> bookListens.get(id)
                        else -> app.api.audiobookListen(id, musicCapabilities(app.capabilities)).let { OpenListen(it.sessionId, it.heartbeatIntervalMs) }
                    }
                }.getOrNull()?.let {
                    listen = Listen(it.sessionId, kind, id, it.heartbeatMs.coerceAtLeast(5_000))
                    lastBeat = System.currentTimeMillis()
                    app.events.send(it.sessionId, PlaybackEvent("start", 0))
                }
            }
        }

        override fun onIsPlayingChanged(isPlaying: Boolean) {
            if (!isPlaying) savePlace(force = true)
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState != Player.STATE_ENDED) return
            // The end: a book is finished, an episode played.
            val now = System.currentTimeMillis()
            book?.let { b -> recordPlace(ListenPlace(ListenPlace.BOOK, b.bookId, b.durationS, finished = true, recordedAt = now)) }
            episode?.let { e ->
                val d = player.duration.takeIf { it > 0 }?.let { it / 1000.0 }
                recordPlace(ListenPlace(ListenPlace.EPISODE, e.id, d ?: (e.durationSeconds ?: 0L).toDouble(), d, finished = true, recordedAt = now))
            }
            clearSleep()
        }

        override fun onPlaybackParametersChanged(playbackParameters: PlaybackParameters) {
            when (queueKind) {
                AudioKind.Book -> app.prefs.bookSpeed = playbackParameters.speed
                AudioKind.Episode -> app.prefs.podcastSpeed = playbackParameters.speed
                AudioKind.Music -> Unit
            }
        }
    }

    private fun refreshStreamOnly() {
        val streamOnly = ListenSessions.streamOnly(app.me.value, app.tokens.isInvitedDevice)
        trackListens.enabled = streamOnly
        bookListens.enabled = streamOnly
    }

    /** Closes the listens opened ahead for tracks and files that never played: a new queue, or the player going away. */
    private fun closeUnplayed() {
        (trackListens.drain() + bookListens.drain()).forEach { app.events.send(it, PlaybackEvent("stop", 0)) }
    }

    private fun endListen() {
        val l = listen ?: return
        listen = null
        if (l.kind == AudioKind.Music) trackListens.forget(l.id) else bookListens.forget(l.id)
        val position = session?.player?.currentPosition ?: 0
        val offset = player.currentMediaItem?.mediaMetadata?.extras?.getLong(EXTRA_CONVERTED_START_MS) ?: 0L
        app.events.send(l.sessionId, PlaybackEvent("stop", (position + offset).coerceAtLeast(0)))
    }

    // ---- the place in a book or an episode ----

    /** Where the listener is now in the book or episode playing; null for music. */
    private fun currentPlace(): ListenPlace? {
        val item = player.currentMediaItem ?: return null
        val now = System.currentTimeMillis()
        return when (kindOf(item)) {
            AudioKind.Book -> book?.let { b ->
                val converted = item.mediaMetadata.extras?.getLong(EXTRA_CONVERTED_START_MS) ?: 0L
                ListenPlace(ListenPlace.BOOK, b.bookId, b.positionS(player.currentMediaItemIndex, converted, player.currentPosition), recordedAt = now)
            }
            AudioKind.Episode -> episode?.let { e ->
                val d = player.duration.takeIf { it > 0 }?.let { it / 1000.0 }
                ListenPlace(ListenPlace.EPISODE, e.id, player.currentPosition.coerceAtLeast(0) / 1000.0, d, recordedAt = now)
            }
            AudioKind.Music -> null
        }
    }

    /** Saves the place now ([force]), or when [ProgressClock] says one is due. At the end, the end's own save stands. */
    private fun savePlace(force: Boolean) {
        if (player.playbackState == Player.STATE_ENDED) return
        val place = currentPlace() ?: return
        if (clock.shouldSend(place.recordedAt, (place.positionS * 1000).toLong(), player.isPlaying, force)) recordPlace(place)
    }

    /**
     * Sends [place] to the server and keeps it on the phone either way, as not yet
     * sent when the server did not take it. Runs on the app's scope, so a save on
     * the way out still finishes.
     */
    private fun recordPlace(place: ListenPlace) {
        app.appScope.launch(Dispatchers.IO) {
            val sent = try {
                OfflineAudioStore.send(app.api, place)
                true
            } catch (e: ApiException) {
                // Refused for good (the book is gone): nothing to retry.
                OfflineProgressQueue.permanent(e)
            }
            app.offlineAudio.record(place, sent)
        }
    }

    private fun startTicker() {
        ticker = scope.launch {
            while (isActive) {
                delay(1_000)
                val now = System.currentTimeMillis()
                if (player.isPlaying) {
                    if (queueKind != AudioKind.Music) savePlace(force = false)
                    // A book file's listen reports progress at its heartbeat, as the TV's does.
                    listen?.takeIf { it.kind == AudioKind.Book && now - lastBeat >= it.heartbeatMs }?.let { l ->
                        lastBeat = now
                        app.events.send(l.sessionId, PlaybackEvent("progress", player.currentPosition.coerceAtLeast(0)))
                    }
                }
                checkSleep(now)
            }
        }
    }

    // ---- the sleep timer ----

    private fun setSleep(choice: Int) {
        when (choice) {
            SleepTimer.OFF -> sleepAt = 0L
            SleepTimer.END_OF_CHAPTER -> {
                val b = book
                val pos = currentPlace()?.positionS
                if (b == null || pos == null) {
                    sleepAt = 0L
                } else {
                    sleepAt = SLEEP_END_OF_CHAPTER
                    sleepChapterEndS = b.chapterEnd(pos)
                }
            }
            else -> sleepAt = System.currentTimeMillis() + choice * 60_000L
        }
        publishExtras()
    }

    private fun clearSleep() {
        if (sleepAt == 0L) return
        sleepAt = 0L
        publishExtras()
    }

    private fun checkSleep(now: Long) {
        val due = when {
            sleepAt == 0L -> false
            sleepAt == SLEEP_END_OF_CHAPTER -> (currentPlace()?.positionS ?: 0.0) >= sleepChapterEndS - 0.3
            else -> now >= sleepAt
        }
        if (due) {
            player.pause()
            clearSleep()
        }
    }

    // ---- what the screens read: the book's files and chapters, and the timer ----

    private fun publishExtras() {
        val s = session ?: return
        val extras = Bundle()
        book?.let { b ->
            extras.putLong(KEY_BOOK_ID, b.bookId)
            extras.putDouble(KEY_BOOK_DURATION_S, b.durationS)
            extras.putDoubleArray(KEY_FILE_STARTS, b.files.map { it.startS }.toDoubleArray())
            extras.putDoubleArray(KEY_CHAPTER_STARTS, b.chapters.map { it.startS }.toDoubleArray())
            extras.putStringArray(KEY_CHAPTER_TITLES, b.chapters.map { it.title }.toTypedArray())
        }
        extras.putLong(KEY_SLEEP_AT, sleepAt)
        s.setSessionExtras(extras)
    }

    /** Back 15 s and forward 30 s in the notification and on the lock screen, for books and episodes. */
    private fun skipButtons(kind: AudioKind): List<CommandButton> = if (kind == AudioKind.Music) {
        emptyList()
    } else {
        listOf(
            CommandButton.Builder(CommandButton.ICON_SKIP_BACK_15)
                .setDisplayName("Back 15 seconds")
                .setSessionCommand(SessionCommand(Commands.SKIP_BACK, Bundle.EMPTY))
                .setSlots(CommandButton.SLOT_BACK)
                .build(),
            CommandButton.Builder(CommandButton.ICON_SKIP_FORWARD_30)
                .setDisplayName("Forward 30 seconds")
                .setSessionCommand(SessionCommand(Commands.SKIP_FORWARD, Bundle.EMPTY))
                .setSlots(CommandButton.SLOT_FORWARD)
                .build(),
        )
    }

    // ---- the book and episode commands ----

    /** Moves [deltaS] in a book (across its files) or an episode. */
    private fun skip(deltaS: Double) {
        val b = book
        if (queueKind == AudioKind.Book && b != null) {
            val pos = currentPlace()?.positionS ?: return
            seekBook(b.skipTarget(pos, deltaS))
        } else {
            player.seekTo(PodcastPlayback.skipTarget(player.currentPosition, (deltaS * 1000).toLong(), player.duration.coerceAtLeast(0L)))
        }
    }

    private fun chapter(direction: Int) {
        val b = book ?: return
        val pos = currentPlace()?.positionS ?: return
        val target = if (direction > 0) b.nextChapter(pos) ?: return else b.previousChapter(pos)
        seekBook(target)
    }

    /** Moves to [targetS] in the book: in place where Media3 can seek, else by loading it again from there. */
    private fun seekBook(targetS: Double) {
        val b = book ?: return
        when (val s = b.seek(targetS)) {
            is BookSeek.InPlace -> {
                player.seekTo(s.index, s.offsetMs)
                savePlace(force = true)
            }
            is BookSeek.Reload -> scope.launch {
                val r = runCatching { bookQueue(b.bookId, s.positionS) }.getOrNull() ?: return@launch
                if (r.items.isEmpty()) return@launch
                val playing = player.playWhenReady
                endListen()
                apply(r)
                player.setMediaItems(r.items, r.index, r.positionMs ?: 0L)
                player.prepare()
                player.playWhenReady = playing
            }
        }
    }

    private inner class Callback : MediaLibrarySession.Callback {
        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            val commands = MediaSession.ConnectionResult.DEFAULT_SESSION_AND_LIBRARY_COMMANDS.buildUpon()
                .apply { Commands.ALL.forEach { add(SessionCommand(it, Bundle.EMPTY)) } }
                .build()
            return MediaSession.ConnectionResult.AcceptedResultBuilder(session)
                .setAvailableSessionCommands(commands)
                .setMediaButtonPreferences(skipButtons(queueKind))
                .build()
        }

        override fun onCustomCommand(
            session: MediaSession,
            controller: MediaSession.ControllerInfo,
            customCommand: SessionCommand,
            args: Bundle,
        ): ListenableFuture<SessionResult> {
            when (customCommand.customAction) {
                Commands.SKIP_BACK -> skip(-PodcastPlayback.SKIP_BACK_MS / 1000.0)
                Commands.SKIP_FORWARD -> skip(PodcastPlayback.SKIP_FORWARD_MS / 1000.0)
                Commands.NEXT_CHAPTER -> chapter(1)
                Commands.PREVIOUS_CHAPTER -> chapter(-1)
                Commands.SEEK_BOOK -> seekBook(args.getDouble(Commands.ARG_SECONDS))
                Commands.SLEEP -> setSleep(args.getInt(Commands.ARG_MINUTES))
                else -> return Futures.immediateFuture(SessionResult(SessionResult.RESULT_ERROR_NOT_SUPPORTED))
            }
            return Futures.immediateFuture(SessionResult(SessionResult.RESULT_SUCCESS))
        }

        override fun onGetLibraryRoot(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<MediaItem>> {
            val extras = Bundle().apply {
                putBoolean(AutoExtras.CONTENT_STYLE_SUPPORTED, true)
                putBoolean(AutoExtras.SEARCH_SUPPORTED, true)
                putInt(AutoExtras.BROWSABLE_HINT, AutoExtras.LIST)
                putInt(AutoExtras.PLAYABLE_HINT, AutoExtras.LIST)
            }
            val root = folder(BrowseTree.ROOT, "VCRX")
            return Futures.immediateFuture(LibraryResult.ofItem(root, LibraryParams.Builder().setExtras(extras).build()))
        }

        override fun onGetChildren(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            parentId: String,
            page: Int,
            pageSize: Int,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> = scope.future {
            val nodes = try {
                children(parentId)
            } catch (e: ApiException) {
                // Offline: the server's nodes are empty and Downloads still plays.
                emptyList()
            }
            val all = nodes.map { toItem(it, browser.packageName) }
            val from = (page * pageSize).coerceAtMost(all.size)
            val to = if (pageSize <= 0) all.size else (from + pageSize).coerceAtMost(all.size)
            LibraryResult.ofItemList(ImmutableList.copyOf(all.subList(from, to)), params)
        }

        override fun onGetItem(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            mediaId: String,
        ): ListenableFuture<LibraryResult<MediaItem>> =
            Futures.immediateFuture(LibraryResult.ofItem(MediaItem.Builder().setMediaId(mediaId).build(), null))

        override fun onSearch(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            query: String,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<Void>> {
            scope.launch {
                val hits = runCatching { search(query) }.getOrDefault(emptyList())
                searches[query] = hits
                session.notifySearchResultChanged(browser, query, hits.size, params)
            }
            return Futures.immediateFuture(LibraryResult.ofVoid())
        }

        override fun onGetSearchResult(
            session: MediaLibrarySession,
            browser: MediaSession.ControllerInfo,
            query: String,
            page: Int,
            pageSize: Int,
            params: LibraryParams?,
        ): ListenableFuture<LibraryResult<ImmutableList<MediaItem>>> {
            val hits = searches[query].orEmpty().map { toItem(it, browser.packageName) }
            return Futures.immediateFuture(LibraryResult.ofItemList(ImmutableList.copyOf(hits), params))
        }

        override fun onSetMediaItems(
            mediaSession: MediaSession,
            controller: MediaSession.ControllerInfo,
            mediaItems: MutableList<MediaItem>,
            startIndex: Int,
            startPositionMs: Long,
        ): ListenableFuture<MediaSession.MediaItemsWithStartPosition> = scope.future {
            // A new queue: save the place in what played, end its listen and close
            // those opened ahead, as the TV does.
            savePlace(force = true)
            endListen()
            closeUnplayed()
            refreshStreamOnly()
            val r = resolve(idsFor(mediaItems), startIndex)
            apply(r)
            val position = r.positionMs ?: if (r.index == startIndex) startPositionMs else C.TIME_UNSET
            MediaSession.MediaItemsWithStartPosition(r.items, r.index.coerceAtLeast(0), position)
        }

        override fun onAddMediaItems(
            mediaSession: MediaSession,
            controller: MediaSession.ControllerInfo,
            mediaItems: MutableList<MediaItem>,
        ): ListenableFuture<MutableList<MediaItem>> = scope.future {
            resolve(idsFor(mediaItems), 0).items.toMutableList()
        }
    }

    /** A resolved queue: its items, where to start, and the book or episode it is, if one. */
    private data class Resolved(
        val items: List<MediaItem>,
        val index: Int,
        /** Into the start item; null leaves it to the controller. */
        val positionMs: Long? = null,
        val book: BookTimeline? = null,
        val episode: PodcastEpisode? = null,
    ) {
        val kind: AudioKind get() = items.firstOrNull()?.mediaId?.let { BrowseId.audioKind(it) } ?: AudioKind.Music
    }

    /** Makes [r] the player's: the book or episode, its speed and audio type, the notification's buttons. */
    private fun apply(r: Resolved) {
        book = r.book
        episode = r.episode
        clock.reset()
        queueKind = r.kind
        player.setAudioAttributes(attributes(queueKind), true)
        player.setPlaybackSpeed(
            when (queueKind) {
                AudioKind.Music -> 1f
                AudioKind.Book -> PodcastPlayback.clampSpeed(app.prefs.bookSpeed)
                AudioKind.Episode -> PodcastPlayback.clampSpeed(app.prefs.podcastSpeed)
            },
        )
        // End of chapter means nothing without a book.
        if (book == null && sleepAt == SLEEP_END_OF_CHAPTER) sleepAt = 0L
        session?.setMediaButtonPreferences(skipButtons(queueKind))
        publishExtras()
        r.episode?.let { app.offlineAudio.notePlayed(it) }
    }

    /**
     * The ids to play. A voice request ("play ... on VCRX") comes with no id and a
     * search query: the best album or artist match plays, and with no query (just
     * "play VCRX") the album played last does.
     */
    private suspend fun idsFor(items: List<MediaItem>): List<String> = items.mapNotNull { item ->
        if (item.mediaId.isNotEmpty()) return@mapNotNull item.mediaId
        val query = item.requestMetadata.searchQuery?.trim().orEmpty()
        if (query.isEmpty()) {
            app.prefs.recentAlbums().firstOrNull()?.let { BrowseId.album(it.id) }
        } else {
            runCatching { search(query) }.getOrDefault(emptyList()).firstOrNull()?.id
        }
    }

    private fun musicLibrary(): Long? = app.libraries.value.firstOrNull { it.kind == Features.KIND_MUSIC }?.id

    private suspend fun children(parentId: String): List<BrowseNode> {
        val f = app.features()
        return when (val id = BrowseId.parse(parentId)) {
            is BrowseId.Folder -> when (id.id) {
                BrowseTree.ROOT -> BrowseTree.root(f)
                BrowseTree.CONTINUE -> BrowseTree.continueListening(
                    f,
                    books = if (f.audiobooks) runCatching { app.api.audiobooksInProgress() }.getOrDefault(emptyList()) else emptyList(),
                    episodes = app.offlineAudio.recentEpisodes(),
                    albums = app.prefs.recentAlbums(),
                )
                BrowseTree.MUSIC -> BrowseTree.music()
                BrowseTree.RECENT_ALBUMS -> musicLibrary()?.let { BrowseTree.albums(app.api.musicAlbums(it, "added", null, limit = 50).albums) }.orEmpty()
                BrowseTree.ALBUMS -> musicLibrary()?.let { BrowseTree.albums(app.api.musicAlbums(it, "title", null, limit = 500).albums) }.orEmpty()
                BrowseTree.ARTISTS -> musicLibrary()?.let { BrowseTree.artists(app.api.musicArtists(it, limit = 500).artists) }.orEmpty()
                BrowseTree.AUDIOBOOKS -> {
                    val all = app.libraries.value.filter { it.kind == Features.KIND_AUDIOBOOK }.flatMap { app.api.audiobooks(it.id).books }
                    BrowseTree.books(app.api.audiobooksInProgress(), all)
                }
                BrowseTree.PODCASTS -> if (f.podcasts) BrowseTree.podcasts(app.api.podcasts()) else emptyList()
                BrowseTree.NEW_EPISODES -> if (f.podcasts) BrowseTree.episodes(app.api.podcastNewEpisodes()) else emptyList()
                BrowseTree.DOWNLOADS -> BrowseTree.downloads(downloadedBooks(), downloadedEpisodes(), downloadedTracks())
                else -> emptyList()
            }
            is BrowseId.Album -> app.api.musicAlbum(id.albumId).let { BrowseTree.tracks(it.id, it.tracks) }
            is BrowseId.Artist -> app.api.musicArtist(id.artistId).let { BrowseTree.albums(it.albums + it.live, withArtist = false) }
            is BrowseId.Book -> {
                val detail = try {
                    app.api.audiobook(id.bookId)
                } catch (e: ApiException) {
                    app.offlineAudio.book(id.bookId) ?: throw e
                }
                BrowseTree.chapters(detail.id, detail.chapters, detail.hasCover)
            }
            is BrowseId.Show -> BrowseTree.episodes(app.api.podcastShow(id.podcastId).episodes, withShow = false)
            else -> emptyList()
        }
    }

    private suspend fun search(query: String): List<BrowseNode> {
        val lib = musicLibrary() ?: return emptyList()
        return BrowseTree.search(query, app.api.musicAlbums(lib, "title", null, limit = 500).albums, app.api.musicArtists(lib, limit = 500).artists)
    }

    private fun downloadedTracks(): List<DownloadedTrack> = app.downloads.entries.value
        .filter { it.done && it.meta.kind == DownloadMeta.TRACK }
        .map { DownloadedTrack(it.meta.trackId, it.meta.title, it.meta.subtitle.substringBefore(" · "), it.meta.albumId) }

    private fun downloadedBooks(): List<DownloadedBook> = DownloadGroups.books(app.downloads.entries.value)
        .filter { it.done }
        .map { DownloadedBook(it.bookId, it.title, it.author) }

    private fun downloadedEpisodes(): List<DownloadedEpisode> = DownloadGroups.episodes(app.downloads.entries.value)
        .filter { it.done }
        .map { DownloadedEpisode(it.meta.episodeId, it.meta.title, it.meta.subtitle) }

    /**
     * Turns the ids a controller asked to play into playable items: an album
     * (from a track), an artist (shuffled), a book, an episode, or downloads,
     * which play with no network.
     */
    private suspend fun resolve(ids: List<String>, startIndex: Int): Resolved {
        val parsed = ids.mapNotNull { BrowseId.parse(it) }
        val first = parsed.getOrNull(startIndex) ?: parsed.firstOrNull() ?: return Resolved(emptyList(), 0)
        return try {
            when {
                parsed.size > 1 && parsed.all { it is BrowseId.AlbumTrack } -> {
                    val trackIds = parsed.map { (it as BrowseId.AlbumTrack).trackId }
                    val tracks = queue(MusicQueueRequest(trackIds = trackIds, capabilities = musicCapabilities(app.capabilities)))
                    Resolved(playable(tracks), startIndex)
                }
                first is BrowseId.Album || first is BrowseId.AlbumTrack -> {
                    val albumId = (first as? BrowseId.Album)?.albumId ?: (first as BrowseId.AlbumTrack).albumId
                    val tracks = queue(MusicQueueRequest(albumId = albumId, capabilities = musicCapabilities(app.capabilities)))
                    rememberAlbum(albumId)
                    val items = playable(tracks)
                    val at = (first as? BrowseId.AlbumTrack)?.let { t -> items.indexOfFirst { it.mediaMetadata.extras?.getLong(EXTRA_TRACK_ID) == t.trackId } } ?: 0
                    Resolved(items, at.coerceAtLeast(0))
                }
                first is BrowseId.Artist -> {
                    Resolved(playable(queue(MusicQueueRequest(artistId = first.artistId, shuffle = true, capabilities = musicCapabilities(app.capabilities)))), 0)
                }
                first is BrowseId.DownloadedTrack -> offlineQueue(first.trackId)
                first is BrowseId.Book -> bookQueue(first.bookId, first.atMs?.let { it / 1000.0 })
                first is BrowseId.BookFile -> bookQueue(first.bookId, null, fromFileId = first.fileId)
                first is BrowseId.DownloadedBook -> offlineBook(first.bookId, null) ?: Resolved(emptyList(), 0)
                first is BrowseId.Episode -> episodeQueue(first.episodeId)
                first is BrowseId.DownloadedEpisode -> offlineEpisode(first.episodeId) ?: Resolved(emptyList(), 0)
                else -> Resolved(emptyList(), 0)
            }
        } catch (e: ApiException) {
            // No server: whatever of it is downloaded still plays.
            when (first) {
                is BrowseId.AlbumTrack -> offlineQueue(first.trackId)
                is BrowseId.DownloadedTrack -> offlineQueue(first.trackId)
                is BrowseId.Book -> offlineBook(first.bookId, first.atMs?.let { it / 1000.0 })
                is BrowseId.BookFile -> offlineBook(first.bookId, null)
                is BrowseId.Episode -> offlineEpisode(first.episodeId)
                else -> null
            } ?: run {
                say(e.message)
                Resolved(emptyList(), 0)
            }
        }
    }

    private suspend fun queue(request: MusicQueueRequest): List<MusicTrack> = app.api.musicQueue(request)

    private suspend fun rememberAlbum(albumId: Long) {
        runCatching { app.api.musicAlbum(albumId) }.getOrNull()?.let {
            app.prefs.notePlayed(MusicAlbum(id = it.id, title = it.title, artistId = it.artistId, artistName = it.artistName, year = it.year, hasCover = it.hasCover))
        }
    }

    /** Every downloaded track, starting at [trackId]. */
    private fun offlineQueue(trackId: Long?): Resolved {
        val tracks = downloadedTracks()
        val items = tracks.map { t ->
            item(t.trackId, t.title, t.artist, "", t.albumId, DownloadKeys.trackPath(t.trackId), cacheKey = DownloadKeys.track(t.trackId))
        }
        return Resolved(items, tracks.indexOfFirst { it.trackId == trackId }.coerceAtLeast(0))
    }

    private fun playable(tracks: List<MusicTrack>): List<MediaItem> = tracks.mapNotNull { t ->
        val key = DownloadKeys.track(t.id)
        when {
            app.downloads.isDownloaded(key) -> item(t.id, t.title, t.artistName, t.albumTitle, t.albumId, DownloadKeys.trackPath(t.id), cacheKey = key)
            t.stream != null -> item(t.id, t.title, t.artistName, t.albumTitle, t.albumId, t.stream.url, cacheKey = null)
            else -> null
        }
    }

    /**
     * A book from the server: its files as the playlist, starting at [atS], at
     * the start of [fromFileId], or where the listener left off. A file on the
     * phone plays from there; a converted file started part way asks its stream
     * to begin at that place (start_ms), as the TV does.
     */
    private suspend fun bookQueue(bookId: Long, atS: Double?, fromFileId: Long? = null): Resolved {
        val q = app.api.audiobookQueue(bookId, musicCapabilities(app.capabilities))
        val onPhone = q.files.map { app.downloads.isDownloaded(DownloadKeys.bookFile(it.id)) }
        val refused = q.files.indices.firstOrNull { !onPhone[it] && q.files[it].stream == null }
        if (q.files.isEmpty() || refused != null) {
            say(refused?.let { q.files[it].refusal?.message } ?: "This book cannot play on this device.")
            return Resolved(emptyList(), 0)
        }
        val spans = q.files.mapIndexed { i, f -> BookSpan(f.id, f.startS, f.durationS, seekable = onPhone[i] || f.stream?.seekable == true) }
        val timeline = BookTimeline(bookId, q.durationS, spans, q.chapters)
        val pos = atS ?: q.files.firstOrNull { it.id == fromFileId }?.startS
            ?: app.offlineAudio.resumeFor(ListenPlace.BOOK, bookId, q.progress.positionS, q.progress.finished)
        val start = timeline.start(pos)
        val items = q.files.mapIndexed { i, f ->
            val converted = if (i == start.index) start.convertedStartMs else 0L
            val stream = f.stream?.url.orEmpty()
            val path = when {
                onPhone[i] -> DownloadKeys.bookFilePath(f.id)
                converted > 0 -> stream + (if ('?' in stream) "&" else "?") + "start_ms=$converted"
                else -> stream
            }
            bookItem(bookId, f.id, q.title, q.author, q.hasCover, path, if (onPhone[i]) DownloadKeys.bookFile(f.id) else null, converted)
        }
        return Resolved(items, start.index, if (start.convertedStartMs > 0) 0L else start.offsetMs, book = timeline)
    }

    /** A downloaded book with no server: its files from the phone, from [atS] or the place the phone last saw. */
    private fun offlineBook(bookId: Long, atS: Double?): Resolved? {
        val detail = app.offlineAudio.book(bookId) ?: return null
        if (!DownloadGroups.bookDownloaded(app.downloads.entries.value, bookId)) return null
        val spans = detail.files.map { BookSpan(it.id, it.startS, it.durationS, seekable = true) }
        val timeline = BookTimeline(bookId, detail.durationS, spans, detail.chapters)
        val start = timeline.start(atS ?: app.offlineAudio.resumeFor(ListenPlace.BOOK, bookId, null))
        val items = detail.files.map { f ->
            bookItem(bookId, f.id, detail.title, detail.author, detail.hasCover, DownloadKeys.bookFilePath(f.id), DownloadKeys.bookFile(f.id), 0L)
        }
        return Resolved(items, start.index, start.offsetMs, book = timeline)
    }

    private suspend fun episodeQueue(episodeId: Long): Resolved {
        val e = app.api.podcastEpisode(episodeId)
        val key = DownloadKeys.episode(e.id)
        val onPhone = app.downloads.isDownloaded(key)
        if (!onPhone && e.streamUrl.isEmpty()) {
            say("This episode has no audio to play.")
            return Resolved(emptyList(), 0)
        }
        val atS = app.offlineAudio.resumeFor(ListenPlace.EPISODE, e.id, e.positionSeconds, e.played)
        val path = if (onPhone) DownloadKeys.episodePath(e.id) else e.streamUrl
        return Resolved(listOf(episodeItem(e, path, if (onPhone) key else null)), 0, (atS * 1000).toLong(), episode = e)
    }

    private fun offlineEpisode(episodeId: Long): Resolved? {
        val key = DownloadKeys.episode(episodeId)
        if (!app.downloads.isDownloaded(key)) return null
        val e = app.offlineAudio.episode(episodeId) ?: return null
        val atS = app.offlineAudio.resumeFor(ListenPlace.EPISODE, e.id, null)
        return Resolved(listOf(episodeItem(e, DownloadKeys.episodePath(e.id), key)), 0, (atS * 1000).toLong(), episode = e)
    }

    /** A message for the person holding the phone: a book or episode that cannot play. */
    private fun say(message: String?) {
        Toast.makeText(this, message ?: "This cannot play right now.", Toast.LENGTH_LONG).show()
    }

    /** The absolute URL for [path]; offline with no server address left, a placeholder the cache key alone resolves. */
    private fun urlFor(path: String): String = app.api.absoluteUrl(path) ?: "http://offline.invalid$path"

    private fun item(trackId: Long, title: String, artist: String, album: String, albumId: Long, path: String, cacheKey: String?): MediaItem {
        val art = ArtworkProvider.uri(this, ArtRef(ArtRef.ALBUM, albumId))
        return MediaItem.Builder()
            .setMediaId(BrowseId.albumTrack(albumId, trackId))
            .setUri(Uri.parse(urlFor(path)))
            .apply { if (cacheKey != null) setCustomCacheKey(cacheKey) }
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(title)
                    .setArtist(artist)
                    .setAlbumTitle(album)
                    .setArtworkUri(art)
                    .setIsBrowsable(false)
                    .setIsPlayable(true)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_MUSIC)
                    .setExtras(Bundle().apply { putLong(EXTRA_TRACK_ID, trackId) })
                    .build(),
            )
            .build()
    }

    private fun bookItem(bookId: Long, fileId: Long, title: String, author: String, hasCover: Boolean, path: String, cacheKey: String?, convertedStartMs: Long): MediaItem {
        val art = if (hasCover) ArtworkProvider.uri(this, ArtRef(ArtRef.BOOK, bookId)) else null
        return MediaItem.Builder()
            .setMediaId(BrowseId.bookFile(bookId, fileId))
            .setUri(Uri.parse(urlFor(path)))
            .apply { if (cacheKey != null) setCustomCacheKey(cacheKey) }
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(title)
                    .setArtist(author)
                    .setAlbumTitle(title)
                    .setArtworkUri(art)
                    .setIsBrowsable(false)
                    .setIsPlayable(true)
                    .setMediaType(MediaMetadata.MEDIA_TYPE_AUDIO_BOOK_CHAPTER)
                    .setExtras(
                        Bundle().apply {
                            putLong(EXTRA_BOOK_ID, bookId)
                            putLong(EXTRA_FILE_ID, fileId)
                            putLong(EXTRA_CONVERTED_START_MS, convertedStartMs)
                        },
                    )
                    .build(),
            )
            .build()
    }

    private fun episodeItem(e: PodcastEpisode, path: String, cacheKey: String?): MediaItem = MediaItem.Builder()
        .setMediaId(BrowseId.episode(e.id))
        .setUri(Uri.parse(urlFor(path)))
        .apply { if (cacheKey != null) setCustomCacheKey(cacheKey) }
        .setMediaMetadata(
            MediaMetadata.Builder()
                .setTitle(e.title)
                .setArtist(e.podcastTitle)
                .setAlbumTitle(e.podcastTitle)
                .setArtworkUri(ArtworkProvider.uri(this, ArtRef(ArtRef.EPISODE, e.id)))
                .setIsBrowsable(false)
                .setIsPlayable(true)
                .setMediaType(MediaMetadata.MEDIA_TYPE_PODCAST_EPISODE)
                .setExtras(Bundle().apply { putLong(EXTRA_EPISODE_ID, e.id) })
                .build(),
        )
        .build()

    private fun folder(id: String, title: String): MediaItem = MediaItem.Builder()
        .setMediaId(id)
        .setMediaMetadata(
            MediaMetadata.Builder().setTitle(title).setIsBrowsable(true).setIsPlayable(false)
                .setMediaType(MediaMetadata.MEDIA_TYPE_FOLDER_MIXED).build(),
        )
        .build()

    private fun toItem(node: BrowseNode, browserPackage: String): MediaItem {
        val art = node.art?.let { ArtworkProvider.uri(this, it) }
        if (art != null) runCatching { grantUriPermission(browserPackage, art, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
        val style = if (node.childStyle == Style.Grid) AutoExtras.GRID else AutoExtras.LIST
        val type = when (node.type) {
            BrowseNode.TYPE_ALBUM -> MediaMetadata.MEDIA_TYPE_ALBUM
            BrowseNode.TYPE_ARTIST -> MediaMetadata.MEDIA_TYPE_ARTIST
            BrowseNode.TYPE_TRACK -> MediaMetadata.MEDIA_TYPE_MUSIC
            BrowseNode.TYPE_BOOK -> MediaMetadata.MEDIA_TYPE_AUDIO_BOOK
            BrowseNode.TYPE_CHAPTER -> MediaMetadata.MEDIA_TYPE_AUDIO_BOOK_CHAPTER
            BrowseNode.TYPE_PODCAST -> MediaMetadata.MEDIA_TYPE_PODCAST
            BrowseNode.TYPE_EPISODE -> MediaMetadata.MEDIA_TYPE_PODCAST_EPISODE
            else -> MediaMetadata.MEDIA_TYPE_FOLDER_MIXED
        }
        return MediaItem.Builder()
            .setMediaId(node.id)
            .setMediaMetadata(
                MediaMetadata.Builder()
                    .setTitle(node.title)
                    .setSubtitle(node.subtitle)
                    .setArtist(node.subtitle)
                    .setIsBrowsable(node.browsable)
                    .setIsPlayable(node.playable)
                    .setMediaType(type)
                    .setArtworkUri(art)
                    .setExtras(
                        Bundle().apply {
                            putInt(AutoExtras.BROWSABLE_HINT, style)
                            putInt(AutoExtras.PLAYABLE_HINT, style)
                        },
                    )
                    .build(),
            )
            .build()
    }

    companion object {
        const val EXTRA_TRACK_ID = "vcrx.track_id"
        const val EXTRA_BOOK_ID = "vcrx.book_id"
        const val EXTRA_FILE_ID = "vcrx.file_id"
        const val EXTRA_EPISODE_ID = "vcrx.episode_id"

        /** Where a converted book file's stream began (start_ms), which the player's position does not include. */
        const val EXTRA_CONVERTED_START_MS = "vcrx.converted_start_ms"

        // Session extras the screens read while a book plays.
        const val KEY_BOOK_ID = "vcrx.book.id"
        const val KEY_BOOK_DURATION_S = "vcrx.book.duration_s"
        const val KEY_FILE_STARTS = "vcrx.book.file_starts"
        const val KEY_CHAPTER_STARTS = "vcrx.book.chapter_starts"
        const val KEY_CHAPTER_TITLES = "vcrx.book.chapter_titles"

        /** When the sleep timer pauses: wall clock ms, [SLEEP_END_OF_CHAPTER], or 0 for off. */
        const val KEY_SLEEP_AT = "vcrx.sleep_at"
        const val SLEEP_END_OF_CHAPTER = -1L
    }
}

/** The custom commands the screens send for books and episodes. */
object Commands {
    const val SKIP_BACK = "vcrx.skip_back"
    const val SKIP_FORWARD = "vcrx.skip_forward"
    const val NEXT_CHAPTER = "vcrx.next_chapter"
    const val PREVIOUS_CHAPTER = "vcrx.previous_chapter"

    /** Seek to [ARG_SECONDS] from the start of the whole book. */
    const val SEEK_BOOK = "vcrx.seek_book"

    /** Set the sleep timer to [ARG_MINUTES], [SleepTimer.END_OF_CHAPTER] or [SleepTimer.OFF]. */
    const val SLEEP = "vcrx.sleep"
    const val ARG_SECONDS = "seconds"
    const val ARG_MINUTES = "minutes"
    val ALL = listOf(SKIP_BACK, SKIP_FORWARD, NEXT_CHAPTER, PREVIOUS_CHAPTER, SEEK_BOOK, SLEEP)
}

/** Android Auto's content style keys, as its media documentation names them. */
object AutoExtras {
    const val CONTENT_STYLE_SUPPORTED = "android.media.browse.CONTENT_STYLE_SUPPORTED"
    const val SEARCH_SUPPORTED = "android.media.browse.SEARCH_SUPPORTED"
    const val BROWSABLE_HINT = "android.media.browse.CONTENT_STYLE_BROWSABLE_HINT"
    const val PLAYABLE_HINT = "android.media.browse.CONTENT_STYLE_PLAYABLE_HINT"
    const val LIST = 1
    const val GRID = 2
}
