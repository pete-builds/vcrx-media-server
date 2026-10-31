package io.github.pete_builds.vcrx.tv.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Decodes the converted-stream shapes in server/api/openapi.yaml. */
class ConvertedModelsTest {
    private val json = ApiJson

    @Test
    fun convertedSessionCarriesItsSeekUrl() {
        val body = """
            {"session_id": "s2", "decision": "audio_transcode", "reason": "the device has no dts decoder",
             "item_id": 5, "resume_position_ms": 1200000, "heartbeat_interval_ms": 10000,
             "version": {"id": 9, "file_name": "Sample Film (2001).mkv", "size": 123, "container": "mkv",
                         "duration_ms": 7200000, "video_codec": "h264"},
             "stream": {"url": "/api/v1/playback/sessions/s2/stream.mp4", "protocol": "fmp4",
                        "mime_type": "video/mp4", "seek_url": "/api/v1/playback/sessions/s2/seek"},
             "subtitles": [], "webvtt_subtitles": []}
        """.trimIndent()
        val session = json.decodeFromString(PlaybackSession.serializer(), body)
        assertEquals("fmp4", session.stream.protocol)
        assertEquals("/api/v1/playback/sessions/s2/seek", session.stream.seekUrl)
        assertEquals(7_200_000L, session.version.durationMs)
        // Direct play sessions have none.
        val direct = json.decodeFromString(StreamInfo.serializer(), """{"url": "/api/v1/versions/9/file?session=s1", "protocol": "progressive"}""")
        assertNull(direct.seekUrl)
    }

    @Test
    fun seekAnswer() {
        val answer = json.decodeFromString(ConvertedStart.serializer(),
            """{"start_ms": 1198000, "url": "/api/v1/playback/sessions/s2/stream.mp4?start_ms=1198000"}""")
        assertEquals(1_198_000L, answer.startMs)
        assertEquals("/api/v1/playback/sessions/s2/stream.mp4?start_ms=1198000", answer.url)
    }
}
