package io.github.pete_builds.vcrx.tv.ui.details

import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.WatchState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ShowDetailsTest {
    private fun ep(id: Long, n: Int, played: Boolean = false, positionMs: Long = 0) = Item(
        id = id, kind = "episode", title = "Episode $n", seasonNumber = 4, episodeNumber = n, runtimeMs = 1_260_000,
        watch = WatchState(played = played, positionMs = positionMs, durationMs = 1_260_000),
    )

    private val season = listOf(ep(1, 1, played = true), ep(2, 2, played = true), ep(3, 3, positionMs = 60_000), ep(4, 4))
    private val show = Item(id = 99, kind = "show", title = "A Show")

    @Test
    fun theStripStartsOnThePagesEpisodeThenNextThenFirstUnwatched() {
        assertEquals(4L, startEpisode(season[3], season)?.id)
        assertEquals(2L, startEpisode(show.copy(nextEpisode = season[1]), season)?.id)
        assertEquals(3L, startEpisode(show, season)?.id)
        assertEquals(1L, startEpisode(show, season.map { it.copy(watch = it.watch?.copy(played = true)) })?.id)
        assertNull(startEpisode(show, emptyList()))
    }

    @Test
    fun theFactsLineReadsLikePlex() {
        val e = ep(3, 3).copy(year = 1999, premiereDate = "1999-10-05", contentRating = "TV-14")
        val chips = episodeChips(e)
        assertEquals("S4 · E3", chips[0].text)
        assertEquals("Oct 5, 1999", chips[1].text)
        // The air date stands in for the year, which is not repeated.
        assertEquals(0, chips.count { it.text == "1999" })
        assertEquals(ChipStyle.Boxed, chips.first { it.text == "TV-14" }.style)
    }

    @Test
    fun theStillShowsHowFarInOnlyPartWayThrough() {
        assertEquals(60_000f / 1_260_000, episodeProgress(season[2])!!, 0.03f)
        assertNull(episodeProgress(season[0]))
        assertNull(episodeProgress(season[3]))
    }
}
