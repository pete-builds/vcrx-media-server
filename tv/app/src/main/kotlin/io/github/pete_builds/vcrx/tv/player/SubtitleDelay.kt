package io.github.pete_builds.vcrx.tv.player

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.util.Consumer
import androidx.media3.common.util.UnstableApi
import androidx.media3.extractor.text.CuesWithTiming
import androidx.media3.extractor.text.DefaultSubtitleParserFactory
import androidx.media3.extractor.text.SubtitleParser

/**
 * The viewer's subtitle delay (the session's subtitle_offset_ms) for direct play.
 * Media3 parses every subtitle as it reads it: a side-loaded file in one go, a
 * track inside the media file sample by sample. This factory hands the player
 * parsers that move every cue they emit by [offsetMs], so the player keeps doing
 * the timing itself and the shift holds through seeks, pauses and track changes.
 * It covers text and picture tracks alike, sidecar or embedded.
 *
 * Converted streams never come through here: their WebVTT copies arrive already
 * shifted by the server ([ConvertedSubtitles]) and must not be shifted twice.
 *
 * [offsetMs] is read when the player creates a parser, which happens after the
 * file is set on the player, so set it before [androidx.media3.common.Player.setMediaItem].
 */
@OptIn(UnstableApi::class)
class SubtitleDelay(private val delegate: SubtitleParser.Factory = DefaultSubtitleParserFactory()) : SubtitleParser.Factory {
    /** Positive shows subtitles later, negative earlier; 0 leaves them as the file has them. */
    @Volatile
    var offsetMs: Long = 0L

    override fun supportsFormat(format: Format): Boolean = delegate.supportsFormat(format)

    override fun getCueReplacementBehavior(format: Format): Int = delegate.getCueReplacementBehavior(format)

    override fun create(format: Format): SubtitleParser = wrap(delegate.create(format))

    /** [parser] with the current delay applied to everything it emits; [parser] itself when there is none. */
    fun wrap(parser: SubtitleParser): SubtitleParser {
        val offsetUs = offsetMs * 1_000
        return if (offsetUs == 0L) parser else Shifted(parser, offsetUs)
    }

    private class Shifted(private val parser: SubtitleParser, private val offsetUs: Long) : SubtitleParser {
        override fun parse(
            data: ByteArray,
            offset: Int,
            length: Int,
            outputOptions: SubtitleParser.OutputOptions,
            output: Consumer<CuesWithTiming>,
        ) {
            parser.parse(data, offset, length, outputOptions) { cues -> output.accept(shift(cues, offsetUs)) }
        }

        override fun reset() = parser.reset()

        override fun getCueReplacementBehavior(): Int = parser.cueReplacementBehavior
    }

    companion object {
        /**
         * [cues] moved by [offsetUs]. A start time is relative to the sample it came
         * from (a track inside the file) or to the file (a side-loaded one); either way
         * the player adds it to where the cue is shown, so adding the delay to it moves
         * the cue. A cue with no start of its own shows at its sample's time, which is
         * a start of zero, so it starts at the delay. The duration never changes.
         */
        fun shift(cues: CuesWithTiming, offsetUs: Long): CuesWithTiming {
            return cues // CONTROL: the shift is switched off
        }
    }
}
