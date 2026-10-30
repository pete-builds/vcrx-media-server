package io.github.pete_builds.vcrx.tv.caps

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.MediaCodecInfo
import android.media.MediaCodecList
import android.os.Build
import android.util.Log
import android.view.Display
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.Format
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.common.util.Util
import androidx.media3.decoder.ffmpeg.FfmpegLibrary
import androidx.media3.exoplayer.audio.AudioCapabilities
import io.github.pete_builds.vcrx.tv.net.Capabilities

/**
 * Reads what this device can actually play: decoders from MediaCodecList, HDR from
 * the display, passthrough from the current audio route, and the bundled FFmpeg
 * audio decoders. Nothing is assumed; the server decides from this.
 */
@OptIn(UnstableApi::class)
object CapabilityProbe {
    private const val TAG = "CapabilityProbe"

    fun probe(context: Context): Capabilities {
        val decoders = decoders()
        val display = context.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY)
        val size = displaySize(context, display)
        return CapabilityBuilder.build(
            decoders = decoders,
            displayHdrTypes = displayHdrTypes(display),
            passthrough = passthrough(context),
            ffmpegCodecs = ffmpegCodecs(),
            displayWidth = size?.first,
            displayHeight = size?.second,
        )
    }

    /** True when the bundled FFmpeg native library loaded. Shown in About. */
    fun ffmpegVersion(): String? = if (FfmpegLibrary.isAvailable()) FfmpegLibrary.getVersion() else null

    private fun decoders(): List<DecoderInfo> {
        val out = mutableListOf<DecoderInfo>()
        for (info in MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos) {
            if (info.isEncoder) continue
            // Secure decoders are for DRM; aliases duplicate another entry.
            if (info.name.endsWith(".secure")) continue
            if (Build.VERSION.SDK_INT >= 29 && info.isAlias) continue
            for (mime in info.supportedTypes) {
                val caps = runCatching { info.getCapabilitiesForType(mime) }.getOrNull() ?: continue
                out += describe(info, mime, caps)
            }
        }
        return out
    }

    private fun describe(info: MediaCodecInfo, mime: String, caps: MediaCodecInfo.CodecCapabilities): DecoderInfo {
        val software = isSoftware(info)
        val video = caps.videoCapabilities
        if (video != null) {
            val maxWidth = video.supportedWidths.upper
            val maxHeight = video.supportedHeights.upper
            return DecoderInfo(
                name = info.name,
                mime = mime,
                isVideo = true,
                isSoftware = software,
                profiles = caps.profileLevels.map { it.profile }.distinct(),
                maxWidth = maxWidth,
                maxHeight = maxHeight,
                maxFps = maxFps(video, maxWidth, maxHeight),
            )
        }
        return DecoderInfo(
            name = info.name,
            mime = mime,
            isVideo = false,
            isSoftware = software,
            maxChannels = caps.audioCapabilities?.maxInputChannelCount ?: 0,
        )
    }

    /**
     * Frame rate at 1080p when the decoder supports it, else at its largest size. The
     * rate at 4K would undersell a decoder for the 1080p files most libraries hold,
     * and the raw upper bound (often 960) is meaningless.
     */
    private fun maxFps(video: MediaCodecInfo.VideoCapabilities, maxWidth: Int, maxHeight: Int): Double {
        val (w, h) = if (video.isSizeSupported(1920, 1080)) 1920 to 1080 else maxWidth to maxHeight
        return runCatching { video.getSupportedFrameRatesFor(w, h).upper }.getOrNull()
            ?: video.supportedFrameRates.upper.toDouble()
    }

    private fun isSoftware(info: MediaCodecInfo): Boolean {
        if (Build.VERSION.SDK_INT >= 29) return info.isSoftwareOnly
        // Before API 29 the platform's own software codecs are the OMX.google ones.
        val name = info.name.lowercase()
        return name.startsWith("omx.google.") || name.startsWith("c2.android.")
    }

    @Suppress("DEPRECATION")
    private fun displayHdrTypes(display: Display?): Set<Int> {
        if (display == null) return emptySet()
        return if (Build.VERSION.SDK_INT >= 34) {
            display.mode.supportedHdrTypes.toSet()
        } else {
            display.hdrCapabilities?.supportedHdrTypes?.toSet() ?: emptySet()
        }
    }

    /** The largest mode the display offers; TVs often run their UI below the panel's size. */
    private fun displaySize(context: Context, display: Display?): Pair<Int, Int>? {
        val sizes = mutableListOf<Pair<Int, Int>>()
        runCatching { Util.getCurrentDisplayModeSize(context) }.getOrNull()?.let { sizes += it.x to it.y }
        display?.supportedModes?.forEach { sizes += it.physicalWidth to it.physicalHeight }
        return sizes.maxByOrNull { it.first.toLong() * it.second }
    }

    private fun passthrough(context: Context): List<Passthrough> {
        val audio = runCatching {
            AudioCapabilities.getCapabilities(context, AudioAttributes.DEFAULT, null, emptyList())
        }.getOrElse {
            Log.w(TAG, "Audio capabilities unavailable", it)
            return emptyList()
        }
        val candidates = listOf(
            MimeTypes.AUDIO_AC3 to "ac3",
            MimeTypes.AUDIO_E_AC3 to "eac3",
            MimeTypes.AUDIO_E_AC3_JOC to "eac3",
            MimeTypes.AUDIO_DTS to "dts",
            MimeTypes.AUDIO_DTS_HD to "dts",
            MimeTypes.AUDIO_TRUEHD to "truehd",
        )
        return candidates.mapNotNull { (mime, codec) ->
            val channels = listOf(8, 6, 2).firstOrNull { count ->
                val format = Format.Builder()
                    .setSampleMimeType(mime)
                    .setChannelCount(count)
                    .setSampleRate(48_000)
                    .build()
                audio.isPassthroughPlaybackSupported(format, AudioAttributes.DEFAULT)
            }
            channels?.let { Passthrough(codec, it) }
        }.groupBy { it.codec }.map { (codec, list) -> Passthrough(codec, list.maxOf { it.maxChannels }) }
    }

    private fun ffmpegCodecs(): List<String> {
        if (!FfmpegLibrary.isAvailable()) {
            Log.w(TAG, "FFmpeg extension not loaded; software audio fallback unavailable")
            return emptyList()
        }
        val candidates = listOf(
            MimeTypes.AUDIO_AAC, MimeTypes.AUDIO_MPEG, MimeTypes.AUDIO_AC3, MimeTypes.AUDIO_E_AC3,
            MimeTypes.AUDIO_TRUEHD, MimeTypes.AUDIO_DTS, MimeTypes.AUDIO_DTS_HD, MimeTypes.AUDIO_VORBIS,
            MimeTypes.AUDIO_OPUS, MimeTypes.AUDIO_FLAC, MimeTypes.AUDIO_ALAC, MimeTypes.AUDIO_MLAW,
            MimeTypes.AUDIO_ALAW,
        )
        return candidates.filter { FfmpegLibrary.supportsFormat(it) }.mapNotNull { CodecNames.audioCodec(it) }.distinct()
    }
}
