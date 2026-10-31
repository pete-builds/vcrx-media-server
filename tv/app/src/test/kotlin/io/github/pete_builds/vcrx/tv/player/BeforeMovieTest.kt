package io.github.pete_builds.vcrx.tv.player

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.BeforeClip
import io.github.pete_builds.vcrx.tv.net.BeforeResponse
import io.github.pete_builds.vcrx.tv.net.PlaybackSettings
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BeforeMovieTest {
    @Test
    fun onlyAPlayFromTheBeginningAsks() {
        assertTrue(BeforeMovie.asks(StartRequest.Beginning, null))
        assertFalse(BeforeMovie.asks(StartRequest.Resume, null))
        assertFalse(BeforeMovie.asks(StartRequest.Default, null))
        // A trailer played on its own from the details screen.
        assertFalse(BeforeMovie.asks(StartRequest.Beginning, 12))
    }

    @Test
    fun theServersListReads() {
        val body = """{"clips": [
            {"kind": "trailer", "title": "Harbor Lights", "url": "/api/v1/extras/3/file",
             "trailer": {"extra_id": 3, "item_id": 9, "item_title": "Harbor Lights"}},
            {"kind": "preroll", "title": "Retro Countdown", "url": "/api/v1/preroll?name=Retro+Countdown.mp4"}]}"""
        val clips = ApiJson.decodeFromString(BeforeResponse.serializer(), body).clips
        assertEquals(listOf("trailer", "preroll"), clips.map { it.kind })
        assertEquals("Trailer  ·  Harbor Lights", BeforeMovie.label(clips[0]))
        assertEquals("Your movie is next", BeforeMovie.label(clips[1]))
        assertEquals("Skip", BeforeMovie.skipLabel(0, 2))
        assertEquals("Skip to the movie", BeforeMovie.skipLabel(1, 2))
    }

    // Saving Skip intro sends only skip_markers: the Before a movie fields are
    // null and left out, so the server keeps them.
    @Test
    fun savingSkipIntroLeavesBeforeAMovieAlone() {
        val sent = ApiJson.encodeToString(PlaybackSettings.serializer(), PlaybackSettings(PlaybackSettings.AUTO))
        assertEquals("""{"skip_markers":"auto"}""", sent)
        val back = ApiJson.decodeFromString(
            PlaybackSettings.serializer(),
            """{"skip_markers": "button", "before_movie_trailers": 2, "before_movie_preroll": true}""",
        )
        assertEquals(2, back.beforeMovieTrailers)
        assertEquals(true, back.beforeMoviePreroll)
        assertEquals(BeforeClip("preroll", "", "/x"), BeforeClip(kind = "preroll", url = "/x"))
    }
}
