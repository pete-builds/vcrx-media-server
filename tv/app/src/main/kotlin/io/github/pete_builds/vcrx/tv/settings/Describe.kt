package io.github.pete_builds.vcrx.tv.settings

import io.github.pete_builds.vcrx.tv.caps.CapabilityBuilder
import io.github.pete_builds.vcrx.tv.caps.CodecNames
import io.github.pete_builds.vcrx.tv.net.Capabilities
import java.util.Locale

/**
 * The words Settings shows for each value, and what this TV decodes laid out for
 * a person rather than as the codec list the server reads. Pure, so the wording
 * is unit tested and stays in one place.
 */
object Describe {
    fun mode(mode: SubtitleMode): String = when (mode) {
        SubtitleMode.Always -> "Always"
        SubtitleMode.ForcedOnly -> "Forced only"
        SubtitleMode.ForeignAudio -> "When the audio is not in my language"
    }

    fun modeHelp(mode: SubtitleMode): String = when (mode) {
        SubtitleMode.Always -> "A full track in your language whenever the file has one."
        SubtitleMode.ForcedOnly -> "Only the lines meant for everyone, such as signs and foreign dialogue."
        SubtitleMode.ForeignAudio -> "Full subtitles for foreign audio; otherwise only forced lines."
    }

    fun languages(codes: List<String>, deviceLanguage: String): String =
        if (codes.isEmpty()) "Same as the TV (${Languages.name(deviceLanguage)})"
        else codes.joinToString(", ") { Languages.name(it) }

    fun language(code: String, deviceLanguage: String): String =
        if (code.isBlank()) "Same as the TV (${Languages.name(deviceLanguage)})" else Languages.name(code)

    fun size(size: SubtitleSize): String = when (size) {
        SubtitleSize.Small -> "Small"
        SubtitleSize.Normal -> "Normal"
        SubtitleSize.Large -> "Large"
        SubtitleSize.Huge -> "Huge"
    }

    fun color(color: SubtitleColor): String = color.name

    fun background(background: SubtitleBackground): String = when (background) {
        SubtitleBackground.None -> "None"
        else -> "${background.alpha * 100 / 255}% black"
    }

    fun edge(edge: SubtitleEdge): String = when (edge) {
        SubtitleEdge.None -> "None"
        SubtitleEdge.Outline -> "Outline"
        SubtitleEdge.DropShadow -> "Drop shadow"
        SubtitleEdge.Raised -> "Raised"
        SubtitleEdge.Depressed -> "Depressed"
    }

    fun position(position: SubtitlePosition): String = when (position) {
        SubtitlePosition.Bottom -> "Bottom"
        SubtitlePosition.Raised -> "A little higher"
        SubtitlePosition.High -> "Higher still"
    }

    fun resume(mode: ResumeMode): String = when (mode) {
        ResumeMode.Ask -> "Ask each time"
        ResumeMode.Resume -> "Always resume"
        ResumeMode.StartOver -> "Always start over"
    }

    fun quality(cap: QualityCap): String = when (cap) {
        QualityCap.Original -> "Original (no limit)"
        else -> "${resolution(cap.height)}, up to ${mbps(cap.bitrate)}"
    }

    fun seconds(s: Int): String = if (s % 60 == 0 && s >= 60) "${s / 60} min" else "$s s"

    fun trailers(mode: TrailerMode): String = when (mode) {
        TrailerMode.Off -> "Off"
        TrailerMode.LocalOnly -> "Local only"
        TrailerMode.LocalAndYouTube -> "Local and YouTube"
    }

    fun skipIntro(mode: SkipIntroMode): String = when (mode) {
        SkipIntroMode.ShowButton -> "Show a Skip button"
        SkipIntroMode.Automatic -> "Skip automatically"
        SkipIntroMode.Off -> "Off"
    }

    fun onOff(on: Boolean): String = if (on) "On" else "Off"

    private fun resolution(height: Int?): String = when {
        height == null -> "Any size"
        height >= 2160 -> "4K"
        else -> "${height}p"
    }

    fun mbps(bitsPerSecond: Long?): String {
        if (bitsPerSecond == null) return "no limit"
        val mbps = bitsPerSecond / 1_000_000.0
        return if (mbps >= 10) "${mbps.toLong()} Mbps" else String.format(Locale.ROOT, "%.1f Mbps", mbps)
    }

    /** A speed test's answer: "212 Mbps (48 MB in 1.8 s)". */
    fun speed(bytes: Long, millis: Long): String {
        val ms = millis.coerceAtLeast(1)
        val mbps = bytes * 8.0 / ms / 1_000
        val rate = if (mbps >= 10) "${mbps.toLong()} Mbps" else String.format(Locale.ROOT, "%.1f Mbps", mbps)
        return "$rate (${bytes / 1_000_000} MB in ${String.format(Locale.ROOT, "%.1f", ms / 1_000.0)} s)"
    }

    /** One line per subject: what a person wants to know about this TV, from the reported capabilities. */
    fun decoding(caps: Capabilities): List<Pair<String, String>> = buildList {
        val video = caps.videoCodecs.mapNotNull { v ->
            val name = VIDEO_NAMES[v.codec] ?: return@mapNotNull null
            val size = v.maxHeight?.let { h -> " up to ${resolution(h)}" }.orEmpty()
            val depth = if ((v.maxBitDepth ?: 8) >= 10) ", 10-bit" else ""
            "$name$size$depth"
        }
        add("Video" to video.ifEmpty { listOf("No hardware video decoders reported") }.joinToString("\n"))
        add("HDR" to caps.hdrTypes.mapNotNull { HDR_NAMES[it] }.ifEmpty { listOf("None (HDR is converted to SDR)") }.joinToString(", "))
        val passthrough = caps.audioCodecs.filter { it.decoder == CapabilityBuilder.PASSTHROUGH }
            .map { "${AUDIO_NAMES[it.codec] ?: it.codec}${channels(it.maxChannels)}" }.distinct()
        add("Sent to the receiver" to passthrough.ifEmpty { listOf("Nothing; the TV decodes all audio itself") }.joinToString(", "))
        val decoded = caps.audioCodecs.filter { it.decoder != CapabilityBuilder.PASSTHROUGH }
            .mapNotNull { AUDIO_NAMES[it.codec] }.distinct()
        add("Audio decoded here" to decoded.joinToString(", ").ifEmpty { "None reported" })
        val w = caps.maxWidth
        val h = caps.maxHeight
        add("Screen" to if (w != null && h != null) "$w x $h (${resolution(h)})" else "Unknown")
        caps.maxBitrate?.let { add("Quality limit" to "Up to ${mbps(it)}") }
    }

    private fun channels(count: Int?): String = when (count) {
        null, 0 -> ""
        2 -> " (stereo)"
        6 -> " (5.1)"
        8 -> " (7.1)"
        else -> " ($count ch)"
    }

    private val VIDEO_NAMES = mapOf(
        "h264" to "H.264", "hevc" to "HEVC (H.265)", "av1" to "AV1", "vp9" to "VP9", "vp8" to "VP8",
        "mpeg2video" to "MPEG-2", "mpeg4" to "MPEG-4 Part 2", "vc1" to "VC-1", "h263" to "H.263",
    )

    private val HDR_NAMES = mapOf(
        CodecNames.HDR10 to "HDR10", CodecNames.HDR10_PLUS to "HDR10+", CodecNames.HLG to "HLG",
        CodecNames.DOLBY_VISION to "Dolby Vision",
    )

    private val AUDIO_NAMES = mapOf(
        "aac" to "AAC", "ac3" to "Dolby Digital", "eac3" to "Dolby Digital Plus", "truehd" to "Dolby TrueHD",
        "dts" to "DTS", "flac" to "FLAC", "opus" to "Opus", "vorbis" to "Vorbis", "mp3" to "MP3", "mp2" to "MP2",
        "alac" to "ALAC", "pcm_s16le" to "PCM",
    )
}
