package io.github.pete_builds.vcrx.tv.net

import kotlinx.serialization.json.jsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Decodes payloads shaped like server/api/openapi.yaml, including nulls and fields this client does not know. */
class ModelsTest {
    private val json = ApiJson

    @Test
    fun itemDetailWithNextEpisodeAndNulls() {
        val body = """
            {"id": 7, "kind": "show", "library_id": 2, "title": "The Show", "year": null,
             "has_poster": true, "has_backdrop": false, "has_thumb": false, "added_at": 1761840000000,
             "watch": {"position_ms": 0, "played": false, "play_count": 0, "unplayed_count": 4, "duration_ms": null},
             "overview": "A show.", "genres": ["Drama"], "ratings": {"imdb": 8.1},
             "next_episode": {"id": 70, "kind": "episode", "library_id": 2, "title": "Pilot", "parent_id": 69,
               "season_number": 1, "episode_number": 1, "has_poster": false, "has_backdrop": false,
               "has_thumb": true, "added_at": 1, "watch": {"position_ms": 120000, "played": false, "play_count": 0}},
             "some_future_field": {"nested": true}}
        """.trimIndent()
        val item = json.decodeFromString(Item.serializer(), body)
        assertEquals("show", item.kind)
        assertNull(item.year)
        assertEquals(4, item.watch?.unplayedCount)
        assertEquals(8.1, item.ratings["imdb"]!!, 0.0)
        assertEquals(70L, item.nextEpisode?.id)
        assertEquals(120_000L, item.nextEpisode?.watch?.positionMs)
        assertTrue(item.versions.isEmpty())
    }

    @Test
    fun playbackSessionWithSidecarSubtitles() {
        val body = """
            {"session_id": "s1", "decision": "direct_play", "reason": "device decodes h264 and aac in mkv",
             "item_id": 5, "resume_position_ms": 61000, "heartbeat_interval_ms": 10000,
             "version": {"id": 9, "file_name": "Movie (2009).mkv", "size": 123, "container": "mkv",
                         "hdr_type": null, "audio_tracks": [{"id": 1, "kind": "audio", "codec": "eac3",
                         "channels": 6, "is_default": true, "is_forced": false, "is_external": false,
                         "is_image_based": false}]},
             "stream": {"url": "/api/v1/versions/9/file?session=s1", "protocol": "progressive", "mime_type": "video/x-matroska"},
             "subtitles": [{"track_id": 3, "url": "/api/v1/subtitles/3", "codec": "subrip",
                            "mime_type": "application/x-subrip", "language": "eng", "is_forced": false}]}
        """.trimIndent()
        val session = json.decodeFromString(PlaybackSession.serializer(), body)
        assertEquals("s1", session.sessionId)
        assertEquals(61_000L, session.resumePositionMs)
        assertEquals("/api/v1/versions/9/file?session=s1", session.stream.url)
        assertEquals("application/x-subrip", session.subtitles.single().mimeType)
        assertEquals(6, session.version.audioTracks.single().channels)
    }

    @Test
    fun requestsLeaveOutNullFields() {
        val request = PlaybackRequest(
            itemId = 5,
            capabilities = Capabilities(
                containers = listOf("mkv"),
                videoCodecs = listOf(VideoCodecCapability("h264", listOf("high"))),
                audioCodecs = listOf(AudioCodecCapability("aac", 2, "hardware")),
            ),
        )
        val encoded = json.parseToJsonElement(json.encodeToString(PlaybackRequest.serializer(), request)).jsonObject
        assertFalse("version_id" in encoded)
        assertFalse("start_position_ms" in encoded)
        assertTrue("capabilities" in encoded)
        val event = json.encodeToString(PlaybackEvent.serializer(), PlaybackEvent(type = "seek", positionMs = 5, fromPositionMs = 1))
        assertEquals("""{"type":"seek","position_ms":5,"from_position_ms":1}""", event)
    }

    @Test
    fun loginRequestCarriesTheDevice() {
        val request = LoginRequest(
            "pete", "secret",
            Device(id = "abc", name = "Living room TV", model = "TCL 55S546", platform = "android_tv", client = "vcrx-tv", clientVersion = "0.1.0"),
        )
        val encoded = json.encodeToString(LoginRequest.serializer(), request)
        assertTrue(encoded.contains("\"client_version\":\"0.1.0\""))
        assertTrue(encoded.contains("\"platform\":\"android_tv\""))
    }

    @Test
    fun serverInfoSaysWhetherAutoSignInWorksHere() {
        val on = json.decodeFromString(ServerInfo.serializer(),
            """{"name": "VCRX", "product": "vcrx-media-server", "version": "1", "api_version": 1, "auto_sign_in": true}""")
        assertTrue(on.autoSignIn)
        // Servers from before the feature send no field: the form is shown.
        val old = json.decodeFromString(ServerInfo.serializer(), """{"name": "VCRX", "product": "vcrx-media-server", "api_version": 1}""")
        assertFalse(old.autoSignIn)
    }
}
