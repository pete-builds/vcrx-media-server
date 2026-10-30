@file:Suppress("InlinedApi")

package io.github.pete_builds.vcrx.tv.caps

import android.media.MediaCodecInfo.CodecProfileLevel as P

/**
 * Translates Android codec vocabulary (MIME types, CodecProfileLevel constants) into
 * the FFprobe names the server records for each version, which is what the contract
 * compares against. Pure functions: unit tested without a device.
 */
object CodecNames {
    /** Containers ExoPlayer demuxes. Mp4Extractor also reads QuickTime files, hence mov. */
    val CONTAINERS = listOf("mkv", "webm", "mp4", "mov", "ts", "flv", "ogg", "avi")

    /** Subtitle formats Media3 1.11 parses, embedded or side-loaded. */
    val SUBTITLE_CODECS = listOf(
        "subrip", "ass", "ssa", "webvtt", "mov_text", "ttml",
        "dvb_subtitle", "hdmv_pgs_subtitle", "dvd_subtitle",
    )

    const val MIME_DOLBY_VISION = "video/dolby-vision"

    fun videoCodec(mime: String): String? = when (mime.lowercase()) {
        "video/avc" -> "h264"
        "video/hevc" -> "hevc"
        "video/av01" -> "av1"
        "video/x-vnd.on2.vp9" -> "vp9"
        "video/x-vnd.on2.vp8" -> "vp8"
        "video/mpeg2" -> "mpeg2video"
        "video/mp4v-es" -> "mpeg4"
        "video/wvc1" -> "vc1"
        "video/3gpp" -> "h263"
        else -> null
    }

    fun audioCodec(mime: String): String? {
        val m = mime.lowercase()
        return when {
            m == "audio/mp4a-latm" -> "aac"
            m == "audio/ac3" -> "ac3"
            m == "audio/eac3" || m == "audio/eac3-joc" -> "eac3"
            // audio/vnd.dts, .hd and .uhd: FFprobe calls all of them dts and puts the
            // variant in the profile.
            m.startsWith("audio/vnd.dts") -> "dts"
            m == "audio/true-hd" -> "truehd"
            m == "audio/flac" -> "flac"
            m == "audio/opus" -> "opus"
            m == "audio/vorbis" -> "vorbis"
            m == "audio/mpeg" -> "mp3"
            m == "audio/mpeg-l2" -> "mp2"
            m == "audio/raw" -> "pcm_s16le"
            m == "audio/alac" -> "alac"
            m == "audio/g711-mlaw" -> "pcm_mulaw"
            m == "audio/g711-alaw" -> "pcm_alaw"
            m == "audio/3gpp" -> "amr_nb"
            m == "audio/amr-wb" -> "amr_wb"
            else -> null
        }
    }

    /**
     * PCM layouts the audio sink plays directly from Matroska and MP4, so they need
     * no decoder: reported beside pcm_s16le, which audio/raw stands for.
     */
    val EXTRA_PCM_CODECS = listOf("pcm_s24le", "pcm_s32le", "pcm_f32le")

    /**
     * FFprobe profile names one Android profile constant can decode, lower case. A
     * profile includes the ones it is a superset of (a High decoder plays Main), so a
     * decoder that lists only its top profile still matches every file it can play.
     * Returns an empty list for constants with no FFprobe equivalent.
     */
    fun videoProfiles(codec: String, profile: Int): List<String> = when (codec) {
        "h264" -> when (profile) {
            P.AVCProfileConstrainedBaseline -> listOf("constrained baseline")
            P.AVCProfileBaseline -> listOf("baseline", "constrained baseline")
            P.AVCProfileMain -> listOf("main", "constrained baseline")
            P.AVCProfileExtended -> listOf("extended", "baseline", "constrained baseline")
            P.AVCProfileHigh -> listOf("high", "main", "constrained baseline")
            P.AVCProfileHigh10 -> listOf("high 10", "high", "main", "constrained baseline")
            P.AVCProfileHigh422 -> listOf("high 4:2:2", "high 10", "high", "main", "constrained baseline")
            P.AVCProfileHigh444 ->
                listOf("high 4:4:4 predictive", "high 4:2:2", "high 10", "high", "main", "constrained baseline")
            else -> emptyList()
        }
        "hevc" -> when (profile) {
            P.HEVCProfileMain -> listOf("main")
            P.HEVCProfileMain10, P.HEVCProfileMain10HDR10, P.HEVCProfileMain10HDR10Plus -> listOf("main 10", "main")
            P.HEVCProfileMainStill -> listOf("main still picture")
            else -> emptyList()
        }
        "av1" -> when (profile) {
            P.AV1ProfileMain8, P.AV1ProfileMain10, P.AV1ProfileMain10HDR10, P.AV1ProfileMain10HDR10Plus -> listOf("main")
            else -> emptyList()
        }
        "vp9" -> when (profile) {
            P.VP9Profile0 -> listOf("profile 0")
            P.VP9Profile1 -> listOf("profile 1")
            P.VP9Profile2, P.VP9Profile2HDR, P.VP9Profile2HDR10Plus -> listOf("profile 2")
            P.VP9Profile3, P.VP9Profile3HDR, P.VP9Profile3HDR10Plus -> listOf("profile 3")
            else -> emptyList()
        }
        "mpeg2video" -> when (profile) {
            P.MPEG2ProfileSimple -> listOf("simple")
            P.MPEG2ProfileMain -> listOf("main", "simple")
            P.MPEG2Profile422 -> listOf("4:2:2")
            P.MPEG2ProfileSNR -> listOf("snr scalable")
            P.MPEG2ProfileSpatial -> listOf("spatially scalable")
            P.MPEG2ProfileHigh -> listOf("high", "main", "simple")
            else -> emptyList()
        }
        "mpeg4" -> when (profile) {
            P.MPEG4ProfileSimple -> listOf("simple profile")
            P.MPEG4ProfileAdvancedSimple -> listOf("advanced simple profile", "simple profile")
            else -> emptyList()
        }
        else -> emptyList()
    }

    /** Codecs whose profile list is meaningful; for the rest an empty list means any. */
    fun hasProfileTable(codec: String): Boolean =
        codec in setOf("h264", "hevc", "av1", "vp9", "mpeg2video", "mpeg4")

    /** Highest bit depth a profile decodes. */
    fun bitDepth(codec: String, profile: Int): Int = when (codec) {
        "h264" -> when (profile) {
            P.AVCProfileHigh10, P.AVCProfileHigh422 -> 10
            P.AVCProfileHigh444 -> 14
            else -> 8
        }
        "hevc" -> when (profile) {
            P.HEVCProfileMain10, P.HEVCProfileMain10HDR10, P.HEVCProfileMain10HDR10Plus -> 10
            else -> 8
        }
        "av1" -> if (profile == P.AV1ProfileMain8) 8 else 10
        "vp9" -> when (profile) {
            P.VP9Profile0, P.VP9Profile1 -> 8
            else -> 10
        }
        else -> 8
    }

    /** HDR formats a decoder profile declares outright. */
    fun hdrFromProfile(codec: String, profile: Int): Set<String> = when (codec) {
        "hevc" -> when (profile) {
            P.HEVCProfileMain10HDR10 -> setOf(HDR10)
            P.HEVCProfileMain10HDR10Plus -> setOf(HDR10, HDR10_PLUS)
            else -> emptySet()
        }
        "vp9" -> when (profile) {
            P.VP9Profile2HDR, P.VP9Profile3HDR -> setOf(HDR10)
            P.VP9Profile2HDR10Plus, P.VP9Profile3HDR10Plus -> setOf(HDR10, HDR10_PLUS)
            else -> emptySet()
        }
        "av1" -> when (profile) {
            P.AV1ProfileMain10HDR10 -> setOf(HDR10)
            P.AV1ProfileMain10HDR10Plus -> setOf(HDR10, HDR10_PLUS)
            else -> emptySet()
        }
        else -> emptySet()
    }

    /** android.view.Display.HdrCapabilities.HDR_TYPE_* to contract names. */
    fun displayHdrType(type: Int): String? = when (type) {
        1 -> DOLBY_VISION
        2 -> HDR10
        3 -> HLG
        4 -> HDR10_PLUS
        else -> null
    }

    const val HDR10 = "hdr10"
    const val HDR10_PLUS = "hdr10plus"
    const val HLG = "hlg"
    const val DOLBY_VISION = "dolby_vision"
}
