package io.github.pete_builds.vcrx.tv.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Podcast payloads as openapi.yaml describes them, nulls, missing and unknown fields included. */
class PodcastModelsTest {
    private val json = ApiJson

    @Test
    fun subscriptionsWithCounts() {
        val body = """
            {"podcasts":[{"id":3,"title":"Harbor Talk","author":"Mara Quill","description":"Weekly.","link":"https://example.com",
              "feed_url":"https://example.com/feed.xml","explicit":false,"has_artwork":true,
              "artwork_url":"/api/v1/podcasts/shows/3/artwork","episode_count":12,"unplayed_count":10,"new_count":2,
              "latest_published_at":1762200000000,"subscribed_at":1762000000000,"refreshed_at":null,"last_error":null,
              "something_newer":{"a":1}}]}
        """.trimIndent()
        val p = json.decodeFromString(PodcastsResponse.serializer(), body).podcasts.single()
        assertEquals(3, p.id)
        assertTrue(p.hasArtwork)
        assertEquals(2, p.newCount)
        assertEquals(10, p.unplayedCount)
        assertNull(p.lastError)
    }

    @Test
    fun showWithEpisodesAndMissingOptionals() {
        val body = """
            {"podcast":{"id":3,"title":"Harbor Talk","feed_url":"https://example.com/feed.xml","explicit":true,"has_artwork":false,
              "episode_count":2,"unplayed_count":1,"new_count":0,"subscribed_at":1},
             "episodes":[
               {"id":11,"podcast_id":3,"podcast_title":"Harbor Talk","title":"Two","explicit":false,
                "stream_url":"/api/v1/podcasts/episodes/11/stream","artwork_url":"/api/v1/podcasts/episodes/11/artwork",
                "position_seconds":42.5,"played":false,"new":true,"published_at":1762200000000,"duration_seconds":600,
                "season":1,"episode":2,"episode_type":"full","mime_type":"audio/mpeg","size_bytes":null},
               {"id":10,"podcast_id":3,"podcast_title":"Harbor Talk","title":"One","explicit":false,
                "stream_url":"/api/v1/podcasts/episodes/10/stream","artwork_url":"/api/v1/podcasts/episodes/10/artwork",
                "position_seconds":0,"played":true,"new":false,"published_at":null,"duration_seconds":null,"season":null}],
             "total":2}
        """.trimIndent()
        val show = json.decodeFromString(PodcastShowResponse.serializer(), body)
        assertTrue(show.podcast.explicit)
        assertEquals("", show.podcast.author)
        assertEquals(2, show.total)
        val (two, one) = show.episodes
        assertEquals(42.5, two.positionSeconds, 0.0)
        assertEquals(600L, two.durationSeconds)
        assertTrue(two.new)
        assertEquals("/api/v1/podcasts/episodes/11/stream", two.streamUrl)
        assertNull(one.durationSeconds)
        assertNull(one.publishedAt)
        assertTrue(one.played)
    }

    @Test
    fun progressAnswer() {
        val p = json.decodeFromString(PodcastProgress.serializer(), """{"position_seconds":12.0,"played":false,"duration_seconds":null}""")
        assertEquals(12.0, p.positionSeconds, 0.0)
        assertFalse(p.played)
    }

    @Test
    fun aServerWithoutPodcastsHidesThem() {
        val old = json.decodeFromString(User.serializer(), """{"id":1,"username":"dev","is_admin":true,"live_tv":true,"kind":"user"}""")
        assertFalse(old.podcasts)
        assertFalse(old.podcastSearch)
        val now = json.decodeFromString(User.serializer(), """{"id":1,"username":"dev","podcasts":true,"podcast_search":false}""")
        assertTrue(now.podcasts)
    }

    @Test
    fun progressBodyLeavesOutAnUnknownDuration() {
        val body = json.encodeToString(PodcastProgressRequest.serializer(), PodcastProgressRequest(30.5))
        assertEquals("""{"position_seconds":30.5}""", body)
    }
}
