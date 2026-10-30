package io.github.pete_builds.vcrx.tv.caps

import android.media.MediaCodecInfo.CodecProfileLevel as P
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CodecNamesTest {
    @Test
    fun videoMimeTypesMapToFfprobeNames() {
        val expected = mapOf(
            "video/avc" to "h264",
            "video/hevc" to "hevc",
            "video/av01" to "av1",
            "video/x-vnd.on2.vp9" to "vp9",
            "video/x-vnd.on2.vp8" to "vp8",
            "video/mpeg2" to "mpeg2video",
            "video/mp4v-es" to "mpeg4",
            "video/wvc1" to "vc1",
        )
        expected.forEach { (mime, codec) -> assertEquals(mime, codec, CodecNames.videoCodec(mime)) }
        assertEquals("h264", CodecNames.videoCodec("VIDEO/AVC"))
        assertNull(CodecNames.videoCodec("video/dolby-vision"))
        assertNull(CodecNames.videoCodec("video/unknown"))
    }

    @Test
    fun audioMimeTypesMapToFfprobeNames() {
        val expected = mapOf(
            "audio/mp4a-latm" to "aac",
            "audio/ac3" to "ac3",
            "audio/eac3" to "eac3",
            "audio/eac3-joc" to "eac3",
            "audio/vnd.dts" to "dts",
            "audio/vnd.dts.hd" to "dts",
            "audio/true-hd" to "truehd",
            "audio/flac" to "flac",
            "audio/opus" to "opus",
            "audio/vorbis" to "vorbis",
            "audio/mpeg" to "mp3",
            "audio/mpeg-L2" to "mp2",
            "audio/raw" to "pcm_s16le",
            "audio/alac" to "alac",
            "audio/g711-mlaw" to "pcm_mulaw",
            "audio/g711-alaw" to "pcm_alaw",
        )
        expected.forEach { (mime, codec) -> assertEquals(mime, codec, CodecNames.audioCodec(mime)) }
        assertNull(CodecNames.audioCodec("audio/ac4"))
    }

    @Test
    fun h264ProfilesIncludeTheProfilesTheyContain() {
        assertEquals(listOf("high", "main", "constrained baseline"), CodecNames.videoProfiles("h264", P.AVCProfileHigh))
        assertEquals(listOf("baseline", "constrained baseline"), CodecNames.videoProfiles("h264", P.AVCProfileBaseline))
        assertEquals(listOf("constrained baseline"), CodecNames.videoProfiles("h264", P.AVCProfileConstrainedBaseline))
        assertTrue("high" in CodecNames.videoProfiles("h264", P.AVCProfileHigh10))
        assertEquals(10, CodecNames.bitDepth("h264", P.AVCProfileHigh10))
        assertEquals(8, CodecNames.bitDepth("h264", P.AVCProfileHigh))
    }

    @Test
    fun hevcMain10CoversMainAndIsTenBit() {
        assertEquals(listOf("main"), CodecNames.videoProfiles("hevc", P.HEVCProfileMain))
        assertEquals(listOf("main 10", "main"), CodecNames.videoProfiles("hevc", P.HEVCProfileMain10))
        assertEquals(listOf("main 10", "main"), CodecNames.videoProfiles("hevc", P.HEVCProfileMain10HDR10))
        assertEquals(10, CodecNames.bitDepth("hevc", P.HEVCProfileMain10HDR10Plus))
        assertEquals(8, CodecNames.bitDepth("hevc", P.HEVCProfileMain))
        assertEquals(setOf("hdr10"), CodecNames.hdrFromProfile("hevc", P.HEVCProfileMain10HDR10))
        assertEquals(setOf("hdr10", "hdr10plus"), CodecNames.hdrFromProfile("hevc", P.HEVCProfileMain10HDR10Plus))
        assertEquals(emptySet<String>(), CodecNames.hdrFromProfile("hevc", P.HEVCProfileMain10))
    }

    @Test
    fun otherCodecProfiles() {
        assertEquals(listOf("main"), CodecNames.videoProfiles("av1", P.AV1ProfileMain10))
        assertEquals(10, CodecNames.bitDepth("av1", P.AV1ProfileMain10))
        assertEquals(8, CodecNames.bitDepth("av1", P.AV1ProfileMain8))
        assertEquals(listOf("profile 2"), CodecNames.videoProfiles("vp9", P.VP9Profile2HDR))
        assertEquals(listOf("high", "main", "simple"), CodecNames.videoProfiles("mpeg2video", P.MPEG2ProfileHigh))
        assertEquals(listOf("advanced simple profile", "simple profile"), CodecNames.videoProfiles("mpeg4", P.MPEG4ProfileAdvancedSimple))
        assertEquals(emptyList<String>(), CodecNames.videoProfiles("vp8", 1))
        assertEquals(emptyList<String>(), CodecNames.videoProfiles("h264", 0x7FFF_0000))
    }

    @Test
    fun displayHdrTypes() {
        assertEquals("dolby_vision", CodecNames.displayHdrType(1))
        assertEquals("hdr10", CodecNames.displayHdrType(2))
        assertEquals("hlg", CodecNames.displayHdrType(3))
        assertEquals("hdr10plus", CodecNames.displayHdrType(4))
        assertNull(CodecNames.displayHdrType(99))
    }

    @Test
    fun containersAndSubtitlesCoverWhatMedia3Reads() {
        assertEquals(listOf("mkv", "webm", "mp4", "mov", "ts", "flv", "ogg", "avi"), CodecNames.CONTAINERS)
        assertTrue(CodecNames.SUBTITLE_CODECS.containsAll(listOf("subrip", "ass", "webvtt", "hdmv_pgs_subtitle", "dvd_subtitle")))
    }
}
