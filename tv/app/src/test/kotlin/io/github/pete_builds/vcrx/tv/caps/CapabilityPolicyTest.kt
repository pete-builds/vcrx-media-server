package io.github.pete_builds.vcrx.tv.caps

import io.github.pete_builds.vcrx.tv.net.AudioCodecCapability
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.VideoCodecCapability
import io.github.pete_builds.vcrx.tv.settings.AudioPrefs
import io.github.pete_builds.vcrx.tv.settings.PlaybackPrefs
import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.QualityCap
import io.github.pete_builds.vcrx.tv.settings.VideoPrefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Each Settings switch that changes what the TV tells the server, and nothing else. */
class CapabilityPolicyTest {
    private val probed = Capabilities(
        containers = listOf("mkv", "mp4"),
        videoCodecs = listOf(
            VideoCodecCapability("hevc", maxBitDepth = 10, maxWidth = 3840, maxHeight = 2160),
            VideoCodecCapability("h264", maxBitDepth = 8, maxWidth = 4096, maxHeight = 2304),
            // A decoder that reported no size: the cap must still bound it.
            VideoCodecCapability("vp9"),
        ),
        audioCodecs = listOf(
            AudioCodecCapability("aac", 8, CapabilityBuilder.HARDWARE),
            AudioCodecCapability("eac3", 8, CapabilityBuilder.PASSTHROUGH),
            AudioCodecCapability("eac3", 8, CapabilityBuilder.FFMPEG),
            AudioCodecCapability("truehd", 8, CapabilityBuilder.PASSTHROUGH),
        ),
        hdrTypes = listOf(CodecNames.HDR10, CodecNames.HDR10_PLUS, CodecNames.HLG, CodecNames.DOLBY_VISION),
        subtitleCodecs = listOf("subrip"),
        maxWidth = 3840,
        maxHeight = 2160,
        fmp4Stream = true,
    )

    @Test
    fun defaultsChangeNothing() {
        assertEquals(probed, CapabilityPolicy.apply(probed, Preferences()))
    }

    @Test
    fun passthroughOffRemovesOnlyThePassthroughEntries() {
        val out = CapabilityPolicy.apply(probed, Preferences(audio = AudioPrefs(passthrough = false)))
        assertEquals(
            listOf(AudioCodecCapability("aac", 8, CapabilityBuilder.HARDWARE), AudioCodecCapability("eac3", 8, CapabilityBuilder.FFMPEG)),
            out.audioCodecs,
        )
        // Decoded here still: E-AC-3 plays through FFmpeg instead of the receiver.
        assertEquals(probed.copy(audioCodecs = out.audioCodecs), out)
    }

    @Test
    fun hdrAndDolbyVisionAreSwitchedSeparately() {
        assertEquals(
            listOf(CodecNames.DOLBY_VISION),
            CapabilityPolicy.apply(probed, Preferences(video = VideoPrefs(hdr = false))).hdrTypes,
        )
        assertEquals(
            listOf(CodecNames.HDR10, CodecNames.HDR10_PLUS, CodecNames.HLG),
            CapabilityPolicy.apply(probed, Preferences(video = VideoPrefs(dolbyVision = false))).hdrTypes,
        )
        assertEquals(emptyList<String>(), CapabilityPolicy.apply(probed, Preferences(video = VideoPrefs(hdr = false, dolbyVision = false))).hdrTypes)
    }

    @Test
    fun theQualityCapBoundsTheDeviceAndEveryDecoder() {
        val out = CapabilityPolicy.apply(probed, Preferences(playback = PlaybackPrefs(quality = QualityCap.Fhd8)))
        assertEquals(1920, out.maxWidth)
        assertEquals(1080, out.maxHeight)
        assertEquals(8_000_000L, out.maxBitrate)
        // The server checks a decoder's own limit before the device's, so each one is capped too.
        out.videoCodecs.forEach {
            assertEquals(it.codec, 1920, it.maxWidth)
            assertEquals(it.codec, 1080, it.maxHeight)
        }
        assertEquals(probed.hdrTypes, out.hdrTypes)
        assertEquals(probed.audioCodecs, out.audioCodecs)
    }

    @Test
    fun theCapNeverRaisesALimit() {
        val small = probed.copy(
            maxWidth = 1280,
            maxHeight = 720,
            maxBitrate = 3_000_000,
            videoCodecs = listOf(VideoCodecCapability("h264", maxWidth = 1280, maxHeight = 720)),
        )
        val out = CapabilityPolicy.apply(small, Preferences(playback = PlaybackPrefs(quality = QualityCap.Uhd4k)))
        assertEquals(small, out)
    }

    @Test
    fun everyCapIsSmallerThanTheOneAboveIt() {
        val caps = QualityCap.entries.drop(1)
        caps.zipWithNext().forEach { (a, b) ->
            assertTrue("$a then $b", b.bitrate!! < a.bitrate!! && b.height!! <= a.height!!)
        }
        assertEquals(null, QualityCap.Original.bitrate)
    }
}
