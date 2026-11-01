package io.github.pete_builds.vcrx.tv.player

import androidx.media3.common.Player
import io.github.pete_builds.vcrx.tv.net.Track
import io.github.pete_builds.vcrx.tv.settings.AudioPrefs
import io.github.pete_builds.vcrx.tv.settings.Languages
import io.github.pete_builds.vcrx.tv.settings.PlaybackPrefs
import io.github.pete_builds.vcrx.tv.settings.ResumeMode

/** How a playback was asked to start. Explicit buttons win over the Resume setting. */
enum class StartRequest {
    /** A plain Play: the Resume setting decides. */
    Default,

    /** The Resume button: the saved position, whatever the setting says. */
    Resume,

    /** Play from start. */
    Beginning,
}

/** The Playback settings as the player applies them. Pure, so each is unit tested. */
object PlaybackPolicy {
    enum class Start { Resume, Beginning, Ask }

    /**
     * Where a playback starts. [savedMs] is the item's resume position, null or 0
     * when there is none (or it was watched to the end), in which case nothing is asked.
     */
    fun start(request: StartRequest, mode: ResumeMode, savedMs: Long?): Start = when {
        request == StartRequest.Beginning -> Start.Beginning
        request == StartRequest.Resume -> Start.Resume
        savedMs == null || savedMs <= 0 -> Start.Resume
        mode == ResumeMode.Resume -> Start.Resume
        mode == ResumeMode.StartOver -> Start.Beginning
        else -> Start.Ask
    }

    /** Seconds of Up Next countdown, or null for no countdown (the next episode waits for Play). */
    fun countdownSeconds(prefs: PlaybackPrefs): Int? =
        if (prefs.autoPlayNext) prefs.countdownSeconds.coerceIn(MIN_COUNTDOWN, MAX_COUNTDOWN) else null

    fun skipBackMs(prefs: PlaybackPrefs): Long = prefs.skipBackSeconds.coerceIn(MIN_SKIP, MAX_SKIP) * 1_000L

    fun skipForwardMs(prefs: PlaybackPrefs): Long = prefs.skipForwardSeconds.coerceIn(MIN_SKIP, MAX_SKIP) * 1_000L

    /**
     * The audio track to ask a converted stream for, by stream index; null lets the
     * server choose (the file's default, which is what "prefer original" means).
     */
    fun convertedAudioIndex(tracks: List<Track>, prefs: AudioPrefs, deviceLanguage: String): Int? {
        if (prefs.preferOriginal) return null
        val wanted = Languages.preferred(listOf(prefs.language), deviceLanguage)
        val match = tracks.filter { it.index != null && Languages.base(it.language) in wanted }
        return (match.firstOrNull { it.isDefault } ?: match.firstOrNull())?.index
    }

    /** The language of the audio a converted stream plays: the requested track, else the file's default. */
    fun convertedAudioLanguage(tracks: List<Track>, index: Int?): String? {
        val chosen = index?.let { i -> tracks.firstOrNull { it.index == i } }
        return (chosen ?: tracks.firstOrNull { it.isDefault } ?: tracks.firstOrNull())?.language
    }

    /** How long to wait before asking for a subtitle that is still being prepared, as the web player does. */
    fun subtitleRetrySeconds(retryAfter: Int?): Int = (retryAfter ?: 5).coerceIn(2, 15)

    /** Reading a track out of a very large file can take minutes; past this, give up and say so. */
    const val SUBTITLE_GIVE_UP_MS = 20 * 60 * 1000L

    /**
     * Whether the screen stays on: while the viewer wants the film to play,
     * playing or buffering toward it. Paused or ended, the TV may start its
     * screensaver or sleep, which ends the session at its position.
     */
    fun keepScreenOn(playWhenReady: Boolean, playbackState: Int): Boolean =
        playWhenReady && (playbackState == Player.STATE_READY || playbackState == Player.STATE_BUFFERING)

    private const val MIN_COUNTDOWN = 3
    private const val MAX_COUNTDOWN = 60
    private const val MIN_SKIP = 5
    private const val MAX_SKIP = 120
}
