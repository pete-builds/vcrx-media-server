package io.github.pete_builds.vcrx.tv.caps

import android.media.MediaCodecInfo.CodecProfileLevel as P
import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.AudioCodecCapability
import io.github.pete_builds.vcrx.tv.net.Capabilities
import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CapabilityBuilderTest {
    private val avcHw = DecoderInfo(
        name = "c2.mtk.avc.decoder", mime = "video/avc", isVideo = true, isSoftware = false,
        profiles = listOf(P.AVCProfileHigh), maxWidth = 4096, maxHeight = 2176, maxFps = 60.0,
    )
    private val avcSw = DecoderInfo(
        name = "c2.android.avc.decoder", mime = "video/avc", isVideo = true, isSoftware = true,
        profiles = listOf(P.AVCProfileBaseline, P.AVCProfileMain), maxWidth = 1920, maxHeight = 1088, maxFps = 30.0,
    )
    private val hevc = DecoderInfo(
        name = "c2.mtk.hevc.decoder", mime = "video/hevc", isVideo = true, isSoftware = false,
        profiles = listOf(P.HEVCProfileMain, P.HEVCProfileMain10, P.HEVCProfileMain10HDR10), maxWidth = 3840, maxHeight = 2160, maxFps = 60.0,
    )
    private val aac = DecoderInfo(name = "c2.android.aac.decoder", mime = "audio/mp4a-latm", isVideo = false, isSoftware = true, maxChannels = 8)
    private val raw = DecoderInfo(name = "c2.android.raw.decoder", mime = "audio/raw", isVideo = false, isSoftware = true, maxChannels = 8)
    private val dolby = DecoderInfo(name = "c2.mtk.dv.decoder", mime = "video/dolby-vision", isVideo = true, isSoftware = false)

    private fun build(
        decoders: List<DecoderInfo> = listOf(avcHw, avcSw, hevc, aac, raw),
        hdr: Set<Int> = emptySet(),
        passthrough: List<Passthrough> = emptyList(),
        ffmpeg: List<String> = emptyList(),
    ) = CapabilityBuilder.build(decoders, hdr, passthrough, ffmpeg, 3840, 2160)

    @Test
    fun mergesDecodersOfOneCodec() {
        val h264 = build().videoCodecs.single { it.codec == "h264" }
        assertEquals(listOf("high", "main", "constrained baseline", "baseline"), h264.profiles)
        assertEquals(4096, h264.maxWidth)
        assertEquals(2176, h264.maxHeight)
        assertEquals(60.0, h264.maxFps!!, 0.0)
        assertEquals(8, h264.maxBitDepth)
        val hevc = build().videoCodecs.single { it.codec == "hevc" }
        assertEquals(10, hevc.maxBitDepth)
        assertEquals(listOf("main", "main 10"), hevc.profiles)
    }

    @Test
    fun dolbyVisionIsNotAVideoCodecEntry() {
        val caps = build(decoders = listOf(avcHw, dolby))
        assertEquals(listOf("h264"), caps.videoCodecs.map { it.codec })
    }

    @Test
    fun hdrNeedsBothDisplayAndDecoder() {
        assertTrue(build(hdr = emptySet()).hdrTypes.isEmpty())
        assertEquals(listOf("hdr10", "hlg"), build(hdr = setOf(2, 3)).hdrTypes)
        // HDR10+ and Dolby Vision on the display, but no decoder declares them.
        assertEquals(listOf("hdr10"), build(hdr = setOf(1, 2, 4)).hdrTypes)
        assertEquals(listOf("hdr10", "dolby_vision"), build(decoders = listOf(hevc, dolby), hdr = setOf(1, 2)).hdrTypes)
        // A display that does HDR10 with only 8-bit decoders gets nothing.
        assertTrue(build(decoders = listOf(avcHw), hdr = setOf(2, 3)).hdrTypes.isEmpty())
    }

    @Test
    fun audioListsEveryDecoderKind() {
        val caps = build(
            passthrough = listOf(Passthrough("eac3", 8), Passthrough("ac3", 6)),
            ffmpeg = listOf("truehd", "dts", "aac"),
        )
        val audio = caps.audioCodecs
        assertTrue(AudioCodecCapability("aac", 8, "software") in audio)
        assertTrue(AudioCodecCapability("aac", 8, "ffmpeg_extension") in audio)
        assertTrue(AudioCodecCapability("eac3", 8, "passthrough") in audio)
        assertTrue(AudioCodecCapability("ac3", 6, "passthrough") in audio)
        assertTrue(AudioCodecCapability("truehd", 8, "ffmpeg_extension") in audio)
        assertTrue(audio.any { it.codec == "pcm_s24le" })
        assertEquals(audio.sortedWith(compareBy({ it.codec }, { it.decoder })), audio)
    }

    @Test
    fun noRawDecoderMeansNoExtraPcm() {
        assertFalse(build(decoders = listOf(aac)).audioCodecs.any { it.codec.startsWith("pcm") })
    }

    @Test
    fun containersSubtitlesAndDisplaySize() {
        val caps = build()
        assertEquals(CodecNames.CONTAINERS, caps.containers)
        assertEquals(CodecNames.SUBTITLE_CODECS, caps.subtitleCodecs)
        assertEquals(3840, caps.maxWidth)
        assertEquals(2160, caps.maxHeight)
        val noDisplay = CapabilityBuilder.build(listOf(avcHw), emptySet(), emptyList(), emptyList(), null, null)
        assertEquals(4096, noDisplay.maxWidth)
    }

    @Test
    fun theRequestSaysThisPlayerTakesConvertedStreams() {
        val caps = build()
        assertTrue(caps.fmp4Stream)
        // Without it the server refuses every file that is not direct play with 422.
        val encoded = ApiJson.parseToJsonElement(ApiJson.encodeToString(Capabilities.serializer(), caps)).jsonObject
        assertEquals("true", encoded["fmp4_stream"].toString())
    }
}
