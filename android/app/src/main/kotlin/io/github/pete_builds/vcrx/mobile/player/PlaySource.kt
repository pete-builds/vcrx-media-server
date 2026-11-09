package io.github.pete_builds.vcrx.mobile.player

/** Where a video plays from. */
enum class PlaySource {
    /** The downloaded copy, from the app's own storage. */
    Local,

    /** A playback session on the server: the file itself or a converted stream, as the server decides. */
    Stream,

    /** Neither: no copy here and no server to ask. */
    Unavailable,
}

/**
 * The phone's playback choices. Pure, so each is unit tested. Whether the file
 * plays directly or converted is the server's call, from the capabilities this
 * phone sends ([io.github.pete_builds.vcrx.mobile.caps.CapabilityProbe]).
 */
object PhonePlaybackPolicy {
    /**
     * A finished download always wins, online or not: it starts at once and costs no
     * bandwidth. Without one, stream when the server is reachable.
     */
    fun source(downloaded: Boolean, online: Boolean): PlaySource = when {
        downloaded -> PlaySource.Local
        online -> PlaySource.Stream
        else -> PlaySource.Unavailable
    }

    /** Where to start: from the beginning when asked, else the saved position unless it is at the very end. */
    fun startMs(fromStart: Boolean, savedMs: Long?, durationMs: Long?): Long {
        if (fromStart) return 0
        val saved = savedMs?.takeIf { it > 0 } ?: return 0
        val d = durationMs?.takeIf { it > 0 } ?: return saved
        return if (saved >= d - END_MS) 0 else saved
    }

    /** Picture in picture only for video that is playing; never for music or a paused screen. */
    fun enterPip(isPlaying: Boolean, isVideo: Boolean): Boolean = isPlaying && isVideo

    private const val END_MS = 30_000L
}
