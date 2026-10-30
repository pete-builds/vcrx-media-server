package io.github.pete_builds.vcrx.tv.caps

import io.github.pete_builds.vcrx.tv.net.AudioCodecCapability
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.VideoCodecCapability

/** One decoder as MediaCodecList describes it, reduced to what the contract needs. */
data class DecoderInfo(
    val name: String,
    val mime: String,
    val isVideo: Boolean,
    val isSoftware: Boolean,
    val profiles: List<Int> = emptyList(),
    val maxWidth: Int = 0,
    val maxHeight: Int = 0,
    val maxFps: Double = 0.0,
    val maxChannels: Int = 0,
)

/** An audio format the TV can pass through undecoded to a receiver or soundbar. */
data class Passthrough(val codec: String, val maxChannels: Int)

/**
 * Merges what the probe found into the request's [Capabilities]. Pure, so the
 * merge rules are unit tested: profiles union across decoders, the largest size
 * wins, HDR needs both a capable decoder and a capable display.
 */
object CapabilityBuilder {
    fun build(
        decoders: List<DecoderInfo>,
        displayHdrTypes: Set<Int>,
        passthrough: List<Passthrough>,
        ffmpegCodecs: List<String>,
        displayWidth: Int?,
        displayHeight: Int?,
    ): Capabilities {
        val video = decoders.filter { it.isVideo }
        val audio = decoders.filter { !it.isVideo }

        val videoCodecs = video
            .mapNotNull { d -> CodecNames.videoCodec(d.mime)?.let { it to d } }
            .groupBy({ it.first }, { it.second })
            .map { (codec, list) ->
                val profiles = list.flatMap { d -> d.profiles.flatMap { CodecNames.videoProfiles(codec, it) } }.distinct()
                val depth = list.flatMap { d -> d.profiles.map { CodecNames.bitDepth(codec, it) } }.maxOrNull() ?: 8
                VideoCodecCapability(
                    codec = codec,
                    profiles = if (CodecNames.hasProfileTable(codec)) profiles else emptyList(),
                    maxBitDepth = depth,
                    maxWidth = list.maxOf { it.maxWidth }.takeIf { it > 0 },
                    maxHeight = list.maxOf { it.maxHeight }.takeIf { it > 0 },
                    maxFps = list.maxOf { it.maxFps }.takeIf { it > 0 },
                )
            }
            .sortedBy { it.codec }

        val audioCodecs = buildList {
            audio.mapNotNull { d -> CodecNames.audioCodec(d.mime)?.let { it to d } }
                .groupBy({ it.first to if (it.second.isSoftware) SOFTWARE else HARDWARE }, { it.second })
                .forEach { (key, list) ->
                    add(AudioCodecCapability(key.first, list.maxOf { it.maxChannels }.takeIf { it > 0 }, key.second))
                }
            // The audio sink plays these PCM layouts directly when audio/raw exists.
            if (any { it.codec == "pcm_s16le" }) {
                CodecNames.EXTRA_PCM_CODECS.forEach { add(AudioCodecCapability(it, 8, SOFTWARE)) }
            }
            passthrough.forEach { add(AudioCodecCapability(it.codec, it.maxChannels, PASSTHROUGH)) }
            ffmpegCodecs.distinct().forEach { add(AudioCodecCapability(it, FFMPEG_MAX_CHANNELS, FFMPEG)) }
        }.distinct().sortedWith(compareBy({ it.codec }, { it.decoder }))

        val decoderHdr = video.flatMap { d ->
            val codec = CodecNames.videoCodec(d.mime) ?: return@flatMap emptyList()
            d.profiles.flatMap { CodecNames.hdrFromProfile(codec, it) }
        }.toSet()
        val tenBit = videoCodecs.any { it.codec in setOf("hevc", "vp9", "av1") && (it.maxBitDepth ?: 8) >= 10 }
        val dolbyDecoder = video.any { it.mime.equals(CodecNames.MIME_DOLBY_VISION, ignoreCase = true) }
        val display = displayHdrTypes.mapNotNull { CodecNames.displayHdrType(it) }.toSet()
        val hdr = buildList {
            if (CodecNames.HDR10 in display && (CodecNames.HDR10 in decoderHdr || tenBit)) add(CodecNames.HDR10)
            if (CodecNames.HDR10_PLUS in display && CodecNames.HDR10_PLUS in decoderHdr) add(CodecNames.HDR10_PLUS)
            if (CodecNames.HLG in display && tenBit) add(CodecNames.HLG)
            if (CodecNames.DOLBY_VISION in display && dolbyDecoder) add(CodecNames.DOLBY_VISION)
        }

        return Capabilities(
            containers = CodecNames.CONTAINERS,
            videoCodecs = videoCodecs,
            audioCodecs = audioCodecs,
            hdrTypes = hdr,
            subtitleCodecs = CodecNames.SUBTITLE_CODECS,
            maxWidth = displayWidth ?: videoCodecs.mapNotNull { it.maxWidth }.maxOrNull(),
            maxHeight = displayHeight ?: videoCodecs.mapNotNull { it.maxHeight }.maxOrNull(),
        )
    }

    const val HARDWARE = "hardware"
    const val SOFTWARE = "software"
    const val PASSTHROUGH = "passthrough"
    const val FFMPEG = "ffmpeg_extension"

    /** FFmpeg decodes to PCM and the platform mixes it down, so channel count is not the limit. */
    const val FFMPEG_MAX_CHANNELS = 8
}
