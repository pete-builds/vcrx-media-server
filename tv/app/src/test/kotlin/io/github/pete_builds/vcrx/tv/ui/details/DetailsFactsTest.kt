package io.github.pete_builds.vcrx.tv.ui.details

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.WatchState
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
         "cast": [{"name": "Lead Actor", "role": "Juror 8", "person_id": 39, "has_photo": true}, {"name": "Bit Part"}],
         "crew": [{"name": "A Director", "role": "Director", "person_id": 40, "has_photo": true}],
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

    private fun List<Fact>.value(label: String) = firstOrNull { it.label == label }?.value

    @Test
    fun theHeaderSaysWhoDirectedIt() {
        assertEquals("Directed by A Director", directedBy(movie))
        assertNull(directedBy(movie.copy(directors = emptyList())))
    }

    @Test
    fun castAndCrewListTheCastThenTheDirectors() {
        assertEquals(listOf("Lead Actor", "Bit Part", "A Director"), castAndCrew(movie).map { it.name })
        assertEquals("Director", castAndCrew(movie).last().role)
    }

    @Test
    fun aFilmsDetailsReadLikeTheWebPage() {
        val f = webFacts(movie)
        assertEquals(
            listOf("Directed by", "Written by", "Studio", "Country", "Released", "Ratings", "File", "Video", "Audio", "Subtitles", "Size"),
            f.map { it.label },
        )
        assertEquals("A Writer, Another Writer", f.value("Written by"))
        assertEquals("April 10, 1957", f.value("Released"))
        // One line per rating; TMDb has no count, so no "votes" after it.
        assertEquals("IMDb 9.0 · 1M votes\nTMDb 8.6", f.value("Ratings"))
        assertEquals("x.mkv", f.value("File"))
        assertEquals("1080p · h264 · 8-bit", f.value("Video"))
        assertEquals("ENG · eac3 · 6 ch", f.value("Audio")!!.lines().first())
        assertEquals(4, f.value("Audio")!!.lines().size)
        assertEquals("English [SDH] · ENG", f.value("Subtitles")!!.lines()[1])
        assertEquals("7.0 GB · mkv", f.value("Size"))
        assertEquals(listOf("Lead Actor", "Bit Part"), movie.cast.map { it.name })
        assertNull(movie.cast[1].role)
        // The photo flag and TMDb id come through; an actor without them has none.
        assertEquals(39L, movie.cast[0].personId)
        assertTrue(movie.cast[0].hasPhoto)
        assertNull(movie.cast[1].personId)
        assertEquals(false, movie.cast[1].hasPhoto)
    }

    @Test
    fun theHeroChipsMatchTheWeb() {
        val chips = heroChips(movie.copy(year = 1957))
        assertEquals(
            listOf("1957", "1 h 37 min", "NR", "1080p", "Dolby Digital+ 5.1", "9.0", "8.6"),
            chips.map { it.text },
        )
        assertEquals(listOf(ChipStyle.Plain, ChipStyle.Plain, ChipStyle.Boxed, ChipStyle.Boxed, ChipStyle.Boxed, ChipStyle.Score, ChipStyle.Score), chips.map { it.style })
        assertEquals(listOf("IMDb", "TMDb"), chips.filter { it.style == ChipStyle.Score }.map { it.source })
        // A show counts its seasons; nothing it lacks is drawn empty.
        val show = Item(id = 2, kind = "show", title = "S", childCount = 1)
        assertEquals(listOf("1 season"), heroChips(show).map { it.text })
    }

    @Test
    fun theResumeBarShowsTimeLeftOnlyPartWayThrough() {
        val started = movie.copy(watch = WatchState(positionMs = 1_000_000, durationMs = 5_820_000))
        val bar = resumeBar(started)!!
        assertEquals("1:20:20 left", bar.left)
        assertEquals(1_000_000f / 5_820_000f, bar.fraction, 0.0001f)
        assertNull(resumeBar(movie))
        assertNull(resumeBar(started.copy(watch = started.watch!!.copy(played = true))))
        assertNull(resumeBar(started.copy(kind = "show")))
    }

    @Test
    fun anEpisodeAirsOnANetwork() {
        val f = webFacts(movie.copy(kind = "episode"))
        assertEquals("April 10, 1957", f.value("Aired"))
        assertEquals("A Studio", f.value("Network"))
    }

    @Test
    fun nothingToPlayMeansNoDetails() {
        // As on the web: a show or a title with no file has no Details section.
        assertTrue(webFacts(Item(id = 1, kind = "movie", title = "Bare")).isEmpty())
        assertTrue(webFacts(movie.copy(versions = emptyList())).isEmpty())
    }

    @Test
    fun sizesClocksAndRuntimesReadAsOnTheWeb() {
        assertEquals("7.0 GB", fileSize(7_505_064_572))
        assertEquals("350 MB", fileSize(367_001_600))
        assertEquals("", fileSize(0))
        assertEquals("12.3 Mbps", bitrate(12_300_000))
        assertEquals("640 kbps", bitrate(640_000))
        assertEquals("4:05", webClock(245_000))
        assertEquals("1:02:03", webClock(3_723_000))
        assertEquals("45 min", webRuntime(2_700_000))
        assertEquals("2 h 0 min", webRuntime(7_200_000))
    }

    @Test
    fun castInitialsStandInForAMissingPhoto() {
        assertEquals("LA", castInitials("Lead Actor"))
        assertEquals("BP", castInitials("  bit   part  extra "))
        assertEquals("C", castInitials("Cher"))
        assertEquals("", castInitials(""))
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
