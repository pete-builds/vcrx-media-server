package io.github.pete_builds.vcrx.tv.trailers

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.PlaybackRequest
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.net.Trailer
import io.github.pete_builds.vcrx.tv.settings.Preferences
import io.github.pete_builds.vcrx.tv.settings.TrailerMode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test

class TrailersTest {
    private val local = Trailer(source = "local", title = "Trailer", extraId = 12)
    private val youtube = Trailer(source = "youtube", title = "Official Trailer", youtubeKey = "OffTrlrEn01")

    @Test
    fun intentUrisForAYouTubeKey() {
        assertEquals(listOf("vnd.youtube:OffTrlrEn01", "https://www.youtube.com/watch?v=OffTrlrEn01"), youTubeUris("OffTrlrEn01"))
    }

    @Test
    fun keysThatAreNotYouTubeIdsBuildNoIntent() {
        for (bad in listOf(null, "", "short", "OffTrlrEn01x", "x/../embed?", "abc def ghi", "a&b=c#defgh")) {
            assertTrue("uris for $bad", youTubeUris(bad).isEmpty())
            var opened = false
            val result = launchYouTube(bad) { opened = true; true }
            assertFalse("opened something for $bad", opened)
            assertEquals(LaunchResult.Failed(NOT_YOUTUBE), result)
        }
    }

    @Test
    fun theYouTubeAppFirst() {
        val tried = mutableListOf<String>()
        val result = launchYouTube("OffTrlrEn01") { tried += it; true }
        assertEquals(LaunchResult.Opened("vnd.youtube:OffTrlrEn01"), result)
        assertEquals(listOf("vnd.youtube:OffTrlrEn01"), tried)
    }

    @Test
    fun theWebAddressWhenNoAppTakesTheScheme() {
        val tried = mutableListOf<String>()
        val result = launchYouTube("OffTrlrEn01") { tried += it; it.startsWith("https://") }
        assertEquals(LaunchResult.Opened("https://www.youtube.com/watch?v=OffTrlrEn01"), result)
        assertEquals(2, tried.size)
    }

    @Test
    fun aClearMessageWhenNothingOpensYouTube() {
        val tried = mutableListOf<String>()
        val result = launchYouTube("OffTrlrEn01") { tried += it; false }
        assertEquals(LaunchResult.Failed(NO_YOUTUBE), result)
        assertEquals(youTubeUris("OffTrlrEn01"), tried)
    }

    @Test
    fun trailerSourceDefaultsToWhatTheServerOffers() {
        assertEquals(TrailerSource.LocalAndYouTube, trailerSource(null, serverOffersYouTube = true))
        assertEquals(TrailerSource.LocalOnly, trailerSource(null, serverOffersYouTube = false))
        assertEquals(TrailerSource.Off, trailerSource(TrailerSource.Off, serverOffersYouTube = true))
        assertEquals(TrailerSource.LocalOnly, trailerSource(TrailerSource.LocalOnly, serverOffersYouTube = true))
    }

    @Test
    fun theSettingsRowDrivesTheChoice() {
        // Untouched Settings: local and YouTube, so a server offering YouTube trailers gets its button.
        assertEquals(TrailerSource.LocalAndYouTube, TrailerSettings.chosen(Preferences()))
        assertSame(youtube, offeredTrailer(youtube, TrailerSettings.chosen(Preferences())))
        assertEquals(TrailerSource.Off, TrailerSettings.chosen(Preferences(trailers = TrailerMode.Off)))
        assertEquals(TrailerSource.LocalOnly, TrailerSettings.chosen(Preferences(trailers = TrailerMode.LocalOnly)))
        assertNull(offeredTrailer(youtube, TrailerSettings.chosen(Preferences(trailers = TrailerMode.LocalOnly))))
        assertTrue(Preferences().themeMusic)
    }

    @Test
    fun offeredTrailerFollowsTheChoice() {
        assertSame(local, offeredTrailer(local, TrailerSource.LocalOnly))
        assertSame(local, offeredTrailer(local, TrailerSource.LocalAndYouTube))
        assertNull(offeredTrailer(local, TrailerSource.Off))
        assertSame(youtube, offeredTrailer(youtube, TrailerSource.LocalAndYouTube))
        assertNull(offeredTrailer(youtube, TrailerSource.LocalOnly))
        assertNull(offeredTrailer(youtube, TrailerSource.Off))
        assertNull(offeredTrailer(null, TrailerSource.LocalAndYouTube))
        assertNull(offeredTrailer(local.copy(extraId = null), TrailerSource.LocalOnly))
        assertNull(offeredTrailer(youtube.copy(youtubeKey = "\"><x>"), TrailerSource.LocalAndYouTube))
    }

    @Test
    fun detailsDecodeExtrasTrailerAndTheme() {
        val body = """
            {"id": 3, "kind": "movie", "title": "Harbor Lights",
             "extras": [{"id": 12, "kind": "trailer", "title": "Trailer", "duration_ms": 12000, "container": "mp4", "resolution": "720p"},
                        {"id": 13, "kind": "featurette", "title": "Raising the Lighthouse", "duration_ms": null}],
             "trailer": {"source": "local", "title": "Trailer", "extra_id": 12, "duration_ms": 12000, "youtube_key": null},
             "theme_music": null}
        """.trimIndent()
        val item = ApiJson.decodeFromString(Item.serializer(), body)
        assertEquals(listOf(12L, 13L), item.extras.map { it.id })
        assertNull(item.extras[1].durationMs)
        assertEquals(12L, item.trailer?.extraId)
        assertNull(item.themeMusic)
        // An older server sends none of these.
        val old = ApiJson.decodeFromString(Item.serializer(), """{"id": 3, "kind": "movie", "title": "Harbor Lights"}""")
        assertTrue(old.extras.isEmpty())
        assertNull(old.trailer)
    }

    @Test
    fun playbackRequestCarriesTheExtraOnlyWhenThereIsOne() {
        val caps = Capabilities(containers = listOf("mp4"), videoCodecs = emptyList(), audioCodecs = emptyList())
        val trailer = ApiJson.encodeToString(PlaybackRequest.serializer(), PlaybackRequest(3, null, 0, caps, extraId = 12))
        assertTrue(trailer, trailer.contains("\"extra_id\":12"))
        val film = ApiJson.encodeToString(PlaybackRequest.serializer(), PlaybackRequest(3, null, null, caps))
        assertFalse(film, film.contains("extra_id"))
    }
}
