package io.github.pete_builds.vcrx.tv.ui.details

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.Item
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DetailsFactsTest {
    // The server's /items/{id} shape for a film, trimmed to what the panel reads.
    private val movie = ApiJson.decodeFromString(
        Item.serializer(),
        """
        {"id": 5694, "kind": "movie", "title": "Twelve Jurors", "runtime_ms": 5820000, "content_rating": "NR",
         "premiere_date": "1957-04-10", "directors": ["A Director"], "writers": ["A Writer", "Another Writer"],
         "studios": ["A Studio"], "countries": ["United States of America"],
         "cast": [{"name": "Lead Actor", "role": "Juror 8"}, {"name": "Bit Part"}],
         "ratings": {"imdb": 9.0, "tmdb": 8.576}, "rating_votes": {"imdb": 1010165},
         "versions": [{"id": 1, "file_name": "x.mkv", "container": "mkv", "size": 7505064572, "video_codec": "h264",
           "bit_depth": 8, "resolution": "1080p",
           "audio_tracks": [
             {"id": 2, "kind": "audio", "codec": "eac3", "language": "eng", "channels": 6},
             {"id": 3, "kind": "audio", "codec": "aac", "language": "fre", "channels": 2},
             {"id": 4, "kind": "audio", "codec": "ac3", "language": "ger", "channels": 2},
             {"id": 5, "kind": "audio", "codec": "ac3", "language": "spa", "channels": 1}],
           "subtitle_tracks": [
             {"id": 6, "kind": "subtitle", "language": "eng"}, {"id": 7, "kind": "subtitle", "language": "eng", "title": "English [SDH]"},
             {"id": 8, "kind": "subtitle", "language": "fre"}, {"id": 9, "kind": "subtitle", "language": "ger"},
             {"id": 10, "kind": "subtitle", "language": "spa"}, {"id": 11, "kind": "subtitle", "language": "ita"},
             {"id": 12, "kind": "subtitle", "language": "dut"}, {"id": 13, "kind": "subtitle", "language": "jpn"}]}]}
        """.trimIndent(),
    )

    private fun Facts.value(label: String) = (about + media).firstOrNull { it.label == label }?.value

    @Test
    fun aFilmReadsLikeItsDetailsPage() {
        val f = detailFacts(movie, movie.versions.first())
        assertEquals(
            listOf("Released", "Runtime", "Rated", "IMDb", "TMDB", "Directed by", "Written by", "Studio", "Country"),
            f.about.map { it.label },
        )
        assertEquals("April 10, 1957", f.value("Released"))
        assertEquals("1h 37m", f.value("Runtime"))
        assertEquals("9.0 from 1M votes", f.value("IMDb"))
        // No count from TMDb: the score alone, never "from null votes".
        assertEquals("8.6", f.value("TMDB"))
        assertEquals("A Writer, Another Writer", f.value("Written by"))
        assertEquals("1080p   H264   8-bit", f.value("Video"))
        assertEquals("English   5.1   EAC3\nFrench   Stereo   AAC\nGerman   Stereo   AC3\nand 1 more", f.value("Audio"))
        // Each language once, six named, then a count.
        assertEquals("English, French, German, Spanish, Italian, Dutch, and 1 more", f.value("Subtitles"))
        assertEquals("7.5 GB   MKV", f.value("File"))
        assertEquals(listOf("Lead Actor", "Bit Part"), movie.cast.map { it.name })
        assertNull(movie.cast[1].role)
    }

    @Test
    fun anEpisodeAirsOnANetwork() {
        val ep = movie.copy(kind = "episode")
        val f = detailFacts(ep, null)
        assertEquals("April 10, 1957", f.value("Aired"))
        assertEquals("A Studio", f.value("Network"))
        assertTrue("no version, no media facts", f.media.isEmpty())
    }

    @Test
    fun missingFactsAreLeftOutNotPrintedEmpty() {
        val bare = Item(id = 1, kind = "movie", title = "Bare")
        val f = detailFacts(bare, null)
        assertTrue(f.about.isEmpty())
        assertTrue(f.media.isEmpty())
    }

    @Test
    fun votesReadCompactly() {
        assertEquals("950", compactVotes(950))
        assertEquals("1K", compactVotes(1_000))
        assertEquals("10.5K", compactVotes(10_538))
        assertEquals("310K", compactVotes(310_000))
        // Never "1000K".
        assertEquals("1M", compactVotes(999_960))
        assertEquals("1M", compactVotes(1_010_165))
        assertEquals("2.3M", compactVotes(2_345_678))
    }

    @Test
    fun onlyRealDatesBecomeDates() {
        assertEquals("March 9, 2001", releaseDate("2001-03-09"))
        assertNull(releaseDate(null))
        assertNull(releaseDate("2001"))
        assertNull(releaseDate("not a date"))
    }
}
