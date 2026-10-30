package io.github.pete_builds.vcrx.tv.player

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import io.github.pete_builds.vcrx.tv.caps.CodecNames
import java.util.Locale

/** Human names for audio and subtitle tracks in the track pickers. */
@OptIn(UnstableApi::class)
object TrackLabels {
    fun label(format: Format, index: Int): String {
        val parts = listOfNotNull(
            format.label?.takeIf { it.isNotBlank() },
            format.language?.takeIf { it.isNotBlank() && it != "und" }?.let { language(it) },
            channels(format.channelCount),
            codec(format.sampleMimeType),
            if ((format.selectionFlags and C.SELECTION_FLAG_FORCED) != 0) "forced" else null,
        )
        return parts.distinct().joinToString("  ").ifEmpty { "Track ${index + 1}" }
    }

    /**
     * "eng", "fre" or "en" to "English" or "French". Files carry ISO 639-2 codes, often
     * the bibliographic form, which Locale does not name, so they map to two letters first.
     * Unknown codes pass through unchanged.
     */
    fun language(code: String): String {
        val lower = code.lowercase()
        val twoLetter = BIBLIOGRAPHIC[lower] ?: if (lower.length == 3) {
            Locale.getISOLanguages().firstOrNull { runCatching { Locale.forLanguageTag(it).isO3Language }.getOrNull() == lower }
        } else {
            null
        }
        val locale = Locale.forLanguageTag(twoLetter ?: lower)
        val name = locale.getDisplayLanguage(Locale.ENGLISH)
        // Locale echoes the code back when it has no name for it.
        return if (name.isNullOrBlank() || name.equals(locale.language, ignoreCase = true)) code else name
    }

    private val BIBLIOGRAPHIC = mapOf(
        "alb" to "sq", "arm" to "hy", "baq" to "eu", "bur" to "my", "chi" to "zh", "cze" to "cs",
        "dut" to "nl", "fre" to "fr", "geo" to "ka", "ger" to "de", "gre" to "el", "ice" to "is",
        "mac" to "mk", "mao" to "mi", "may" to "ms", "per" to "fa", "rum" to "ro", "slo" to "sk",
        "tib" to "bo", "wel" to "cy",
    )

    fun channels(count: Int): String? = when (count) {
        Format.NO_VALUE, 0 -> null
        1 -> "mono"
        2 -> "stereo"
        6 -> "5.1"
        8 -> "7.1"
        else -> "${count}ch"
    }

    private fun codec(mime: String?): String? {
        if (mime == null) return null
        CodecNames.audioCodec(mime)?.let { return it.uppercase() }
        return when (mime) {
            MimeTypes.APPLICATION_SUBRIP -> "SRT"
            MimeTypes.TEXT_SSA -> "ASS"
            MimeTypes.TEXT_VTT -> "WebVTT"
            MimeTypes.APPLICATION_TTML -> "TTML"
            MimeTypes.APPLICATION_PGS -> "PGS"
            MimeTypes.APPLICATION_VOBSUB -> "VobSub"
            MimeTypes.APPLICATION_DVBSUBS -> "DVB"
            MimeTypes.APPLICATION_TX3G -> "mov_text"
            else -> null
        }
    }
}
