package io.github.pete_builds.vcrx.tv.player

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.util.Consumer
import androidx.media3.common.util.UnstableApi
import androidx.media3.extractor.text.CuesWithTiming
import androidx.media3.extractor.text.SubtitleParser
import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.PlaybackSession
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Test

/**
 * Direct play moves every cue by the session's subtitle_offset_ms. The parser here
 * stands in for Media3's own (SubRip, WebVTT, PGS and the rest): what matters is
 * what the player receives from the wrapped parser.
 */
@OptIn(UnstableApi::class)
class SubtitleDelayTest {
    /** Emits fixed cues: one at 1 s for 2 s, and one with no start of its own (as a picture track sends). */
    private class FakeParser : SubtitleParser {
        var resets = 0

        override fun parse(
            data: ByteArray,
            offset: Int,
            length: Int,
            outputOptions: SubtitleParser.OutputOptions,
            output: Consumer<CuesWithTiming>,
        ) {
            output.accept(CuesWithTiming(emptyList(), 1_000_000, 2_000_000))
            output.accept(CuesWithTiming(emptyList(), C.TIME_UNSET, C.TIME_UNSET))
        }

        override fun reset() {
            resets++
        }

        override fun getCueReplacementBehavior(): Int = Format.CUE_REPLACEMENT_BEHAVIOR_MERGE
    }

    private fun emitted(parser: SubtitleParser): List<CuesWithTiming> {
        val out = mutableListOf<CuesWithTiming>()
        parser.parse(ByteArray(0), 0, 0, SubtitleParser.OutputOptions.allCues()) { out += it }
        return out
    }

    @Test
    fun aPositiveDelayShowsEveryCueLater() {
        val delay = SubtitleDelay().apply { offsetMs = 1_500 }
        val cues = emitted(delay.wrap(FakeParser()))
        assertEquals(2_500_000L, cues[0].startTimeUs)
        assertEquals(2_000_000L, cues[0].durationUs)
        assertEquals(4_500_000L, cues[0].endTimeUs)
        // No start of its own: it showed at its sample's time, so it now shows 1.5 s after it.
        assertEquals(1_500_000L, cues[1].startTimeUs)
        assertEquals(C.TIME_UNSET, cues[1].durationUs)
    }

    @Test
    fun aNegativeDelayShowsEveryCueEarlier() {
        val delay = SubtitleDelay().apply { offsetMs = -750 }
        val cues = emitted(delay.wrap(FakeParser()))
        assertEquals(250_000L, cues[0].startTimeUs)
        assertEquals(2_250_000L, cues[0].endTimeUs)
        assertEquals(-750_000L, cues[1].startTimeUs)
    }

    @Test
    fun noDelayLeavesTheParserAsItIs() {
        val parser = FakeParser()
        assertSame(parser, SubtitleDelay().wrap(parser))
        assertEquals(1_000_000L, emitted(SubtitleDelay().wrap(parser))[0].startTimeUs)
    }

    @Test
    fun theDelayIsTakenWhenTheParserIsMade() {
        // The player makes a track's parser after the file is set on it. A later change
        // (the next file's delay) must not move cues of a parser already made.
        val delay = SubtitleDelay().apply { offsetMs = 1_000 }
        val parser = delay.wrap(FakeParser())
        delay.offsetMs = 0
        assertEquals(2_000_000L, emitted(parser)[0].startTimeUs)
    }

    @Test
    fun theWrappedParserStillResetsAndReportsItsBehavior() {
        val inner = FakeParser()
        val parser = SubtitleDelay().apply { offsetMs = 250 }.wrap(inner)
        parser.reset()
        assertEquals(1, inner.resets)
        assertEquals(Format.CUE_REPLACEMENT_BEHAVIOR_MERGE, parser.cueReplacementBehavior)
    }

    @Test
    fun theSessionCarriesTheDelay() {
        val body = """
            {"session_id": "s1", "item_id": 5, "subtitle_offset_ms": -2500,
             "version": {"id": 9, "file_name": "Movie.mkv", "size": 1, "container": "mkv"},
             "stream": {"url": "/api/v1/versions/9/file?session=s1"}}
        """.trimIndent()
        assertEquals(-2_500L, ApiJson.decodeFromString(PlaybackSession.serializer(), body).subtitleOffsetMs)
        val none = body.replace("\"subtitle_offset_ms\": -2500,", "")
        assertEquals(0L, ApiJson.decodeFromString(PlaybackSession.serializer(), none).subtitleOffsetMs)
    }
}
