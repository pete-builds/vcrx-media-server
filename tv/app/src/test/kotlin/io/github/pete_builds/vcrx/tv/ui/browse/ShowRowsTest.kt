package io.github.pete_builds.vcrx.tv.ui.browse

import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.WatchState
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** A TV library opens on Home-style rows of show posters (Pete, 2026-11-03). */
class ShowRowsTest {
    private val tv = 2L

    private fun show(id: Long, lib: Long = tv) = Item(id = id, kind = "show", libraryId = lib, title = "Show $id", hasPoster = true)
    private fun movie(id: Long) = Item(id = id, kind = "movie", libraryId = 1, title = "Movie $id")
    private fun episode(id: Long, showId: Long, lib: Long = tv, pos: Long? = null) = Item(
        id = id, kind = "episode", libraryId = lib, title = "Ep $id", showId = showId, showTitle = "Show $showId",
        watch = pos?.let { WatchState(positionMs = it) },
    )
    private fun row(id: String, vararg items: Item) = HomeRow(id = id, title = id, items = items.toList())

    @Test
    fun anEpisodeBecomesItsShowAndKeepsItsProgress() {
        val card = ShowRows.asShow(episode(50, showId = 7, pos = 1_200_000))!!
        assertEquals(7L, card.id)
        assertEquals("show", card.kind)
        assertEquals("Show 7", card.title)
        assertEquals(1_200_000L, card.watch?.positionMs)
        assertNull(ShowRows.asShow(movie(3)))
    }

    @Test
    fun aBingeIsOnePosterAndOtherLibrariesDropOut() {
        val items = listOf(episode(50, 7), episode(51, 7), episode(60, 8), episode(70, 9, lib = 5), movie(3))
        assertEquals(listOf(7L, 8L), ShowRows.shows(items, tv).map { it.id })
    }

    @Test
    fun continueWatchingLeadsThenRecentlyAddedThenHomesOtherRows() {
        val home = listOf(
            row("trending", show(1), show(2), show(3), movie(4)),
            row("continue_watching", episode(50, 7, pos = 10), movie(3)),
            row("recently_added_tv:$tv", show(11), show(12), show(13)),
            row("recently_added_movies:1", movie(20), movie(21), movie(22)),
            row("top_rated_movies", movie(30), movie(31), movie(32)),
        )
        val rows = ShowRows.build(tv, home, emptyList())
        assertEquals(listOf("continue_watching", "recently_added_tv:$tv", "trending"), rows.map { it.id })
        // One show is enough for Continue Watching; the title on the library's own row is plain.
        assertEquals(listOf(7L), rows[0].items.map { it.id })
        assertEquals("Recently Added", rows[1].title)
        // Movies are stripped from mixed rows, and movie-only rows vanish.
        assertEquals(listOf(1L, 2L, 3L), rows[2].items.map { it.id })
    }

    @Test
    fun sparseRowsAreLeftOutButAnEmptyContinueWatchingIsToo() {
        val home = listOf(row("continue_watching", movie(3)), row("trending", show(1), show(2)))
        assertEquals(emptyList<String>(), ShowRows.build(tv, home, emptyList()).map { it.id })
    }

    @Test
    fun genreRowsFollowAndSkipGenresAlreadyShown() {
        val home = listOf(row("genre:Comedy", show(1), show(2), show(3)))
        val genres = listOf(
            "comedy" to listOf(show(4), show(5), show(6)),
            "Drama" to listOf(show(7), show(8), show(9)),
            "Western" to listOf(show(10)),
        )
        assertEquals(listOf("genre:Comedy", "genre:Drama"), ShowRows.build(tv, home, genres).map { it.id })
    }

    @Test
    fun topGenresAreTheMostCommonWithEnoughShows() {
        val counts = listOf("A" to 2, "B" to 40, "C" to 9, "D" to 12, "E" to 5, "F" to 7, "G" to 30, "H" to 3)
        assertEquals(listOf("B", "G", "D", "C", "F", "E"), ShowRows.topGenres(counts))
    }
}
