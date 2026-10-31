package io.github.pete_builds.vcrx.tv.player

import android.os.SystemClock
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.text.Cue
import androidx.media3.common.util.UnstableApi
import androidx.media3.extractor.text.SubtitleParser
import androidx.media3.extractor.text.webvtt.WebvttParser
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.ServerApi
import io.github.pete_builds.vcrx.tv.net.SubtitleAnswer
import io.github.pete_builds.vcrx.tv.net.SubtitleSource
import io.github.pete_builds.vcrx.tv.settings.Languages
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/**
 * Subtitles for a converted stream, which carries none of its own: the session's
 * WebVTT copies, fetched when chosen (waiting out the server's 202 while it reads
 * an embedded track out of the file), parsed once by Media3's own WebVTT parser,
 * and kept in file time in a [CueTimeline]. The player shows the cues active at
 * the file position, so they stay in sync however often the stream restarts.
 */
@OptIn(UnstableApi::class)
class ConvertedSubtitles(private val api: ServerApi, val sources: List<SubtitleSource>) {
    private val loaded = HashMap<Long, CueTimeline<Cue>>()

    /** The tracks as the shared picker sees them; [SubtitleCandidate.id] is the track id. */
    val candidates: List<SubtitleCandidate> = sources.map { source ->
        SubtitleCandidate(
            id = source.trackId.toString(),
            language = source.language,
            forced = source.isForced,
            label = source.title,
        )
    }

    fun source(candidate: SubtitleCandidate): SubtitleSource? = sources.firstOrNull { it.trackId.toString() == candidate.id }

    /** "English  forced", or the track's own title; the same words the direct-play picker uses. */
    fun label(source: SubtitleSource): String {
        val parts = listOfNotNull(
            source.title?.takeIf { it.isNotBlank() },
            source.language?.let { Languages.base(it) }?.let { Languages.name(it) },
            if (source.isForced) "forced" else null,
        )
        return parts.distinct().joinToString("  ").ifEmpty { "Track ${sources.indexOf(source) + 1}" }
    }

    /** Loads [source], calling [onWait] with a status line while the server prepares it. */
    suspend fun load(source: SubtitleSource, onWait: (String) -> Unit): CueTimeline<Cue> {
        loaded[source.trackId]?.let { return it }
        val started = SystemClock.elapsedRealtime()
        while (true) {
            when (val answer = api.subtitle(source.url)) {
                is SubtitleAnswer.Ready -> {
                    val timeline = withContext(Dispatchers.Default) { parse(answer.bytes) }
                    loaded[source.trackId] = timeline
                    return timeline
                }
                is SubtitleAnswer.Preparing -> {
                    val waited = SystemClock.elapsedRealtime() - started
                    if (waited > PlaybackPolicy.SUBTITLE_GIVE_UP_MS) throw ApiException.BadResponse("Subtitles took too long to prepare.")
                    onWait(if (waited > 10_000) "Preparing subtitles (${waited / 1000} s; large files take a few minutes)" else "Preparing subtitles")
                    delay(PlaybackPolicy.subtitleRetrySeconds(answer.retryAfterSeconds) * 1_000L)
                }
            }
        }
    }

    private fun parse(bytes: ByteArray): CueTimeline<Cue> {
        val cues = mutableListOf<CueTimeline.TimedCue<Cue>>()
        try {
            WebvttParser().parse(bytes, SubtitleParser.OutputOptions.allCues()) { timed ->
                val start = timed.startTimeUs / 1_000
                val end = if (timed.durationUs == C.TIME_UNSET) Long.MAX_VALUE else timed.endTimeUs / 1_000
                timed.cues.forEach { cues += CueTimeline.TimedCue(start, end, it) }
            }
        } catch (e: RuntimeException) {
            throw ApiException.BadResponse("The subtitle file could not be read.", e)
        }
        return CueTimeline(cues)
    }
}
