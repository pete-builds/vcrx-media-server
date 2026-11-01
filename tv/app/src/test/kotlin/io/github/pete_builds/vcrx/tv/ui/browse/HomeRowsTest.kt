package io.github.pete_builds.vcrx.tv.ui.browse

import io.github.pete_builds.vcrx.tv.net.ApiJson
import io.github.pete_builds.vcrx.tv.net.HomeLayout
import io.github.pete_builds.vcrx.tv.net.HomeResponse
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Home draws any rows the server sends, in its order, including kinds this app has never heard of. */
class HomeRowsTest {
    private fun item(id: Long, kind: String = "movie") =
        """{"id": $id, "kind": "$kind", "library_id": 1, "title": "Title $id", "has_poster": false, "has_backdrop": false, "has_thumb": false, "added_at": 1}"""

    private val body = """
        {"rows": [
          {"id": "continue_watching", "kind": "continue_watching", "title": "Continue Watching", "see_all": false,
           "items": [${item(5, "episode")}]},
          {"id": "seasonal:halloween", "kind": "seasonal", "title": "Halloween Picks", "see_all": true,
           "items": [${item(1)}, ${item(2)}, ${item(3)}, ${item(4)}]},
          {"id": "row_from_the_future", "kind": "something_new", "title": "A Row Nobody Planned", "see_all": true,
           "layout_hint": {"x": 1}, "items": [${item(7)}, ${item(7)}, ${item(8)}]},
          {"id": "new_seasons", "kind": "new_seasons", "title": "New Seasons", "items": []},
          {"id": "recently_added_tv:2", "kind": "recently_added_tv", "title": "Recently Added TV", "library_id": 2,
           "items": [{"id": 9, "kind": "show", "title": "A Show", "note": "3 new episodes"}]},
          {"id": "seasonal:halloween", "kind": "seasonal", "title": "Halloween again", "items": [${item(1)}]},
          {"id": "episodes", "kind": "x", "title": "Only Episodes", "items": [${item(11, "episode")}, ${item(12, "episode")}]}
        ]}
    """.trimIndent()

    @Test
    fun drawsEveryRowInTheServersOrder() {
        val rows = HomeRows.toShow(ApiJson.decodeFromString(HomeResponse.serializer(), body).rows)
        assertEquals(
            listOf("continue_watching", "seasonal:halloween", "row_from_the_future", "recently_added_tv:2", "episodes"),
            rows.map { it.id },
        )
        assertEquals("Halloween Picks", rows[1].title)
        assertTrue(rows[1].seeAll)
        // A repeated item would crash LazyRow's keys; it shows once.
        assertEquals(listOf(7L, 8L), rows[2].items.map { it.id })
        assertEquals("3 new episodes", rows[3].items.single().note)
    }

    @Test
    fun cardShapeFollowsTheItems() {
        val rows = HomeRows.toShow(ApiJson.decodeFromString(HomeResponse.serializer(), body).rows).associateBy { it.id }
        assertTrue(HomeRows.wide(rows.getValue("continue_watching")))
        assertTrue(HomeRows.wide(rows.getValue("episodes")))
        assertFalse(HomeRows.wide(rows.getValue("seasonal:halloween")))
        assertFalse(HomeRows.wide(rows.getValue("recently_added_tv:2")))
    }

    @Test
    fun focusReturnsToTheRowThatOpenedDetails() {
        // Title 4 is in Trending and Top Rated; only the row it was opened from takes focus back.
        assertTrue(HomeRows.restores(4, "trending", 4, "trending"))
        assertFalse(HomeRows.restores(4, "trending", 4, "top_rated_movies"))
        assertFalse(HomeRows.restores(4, "trending", 5, "trending"))
        // Grids have no rows: the title alone decides.
        assertTrue(HomeRows.restores(4, null, 4, null))
        assertTrue(HomeRows.restores(4, null, 4, "trending"))
        assertFalse(HomeRows.restores(null, null, 4, null))
    }

    @Test
    fun backFromMoreInfoFocusesMoreInfo() {
        // It used to land on the rail: nothing on Home claimed focus for the hero's More info.
        assertEquals(HomeRows.HeroFocus.MoreInfo, HomeRows.heroFocus(inFront = true, hasHero = true, toContent = false, restoreItemId = null, restoreHero = true))
        // The rail handing over still lands on Play.
        assertEquals(HomeRows.HeroFocus.First, HomeRows.heroFocus(inFront = true, hasHero = true, toContent = true, restoreItemId = null, restoreHero = false))
        // A card taking focus back keeps it.
        assertEquals(null, HomeRows.heroFocus(inFront = true, hasHero = true, toContent = true, restoreItemId = 4, restoreHero = false))
        assertEquals(null, HomeRows.heroFocus(inFront = true, hasHero = true, toContent = false, restoreItemId = null, restoreHero = false))
        assertEquals(null, HomeRows.heroFocus(inFront = true, hasHero = false, toContent = true, restoreItemId = null, restoreHero = true))
    }

    @Test
    fun nothingTakesFocusWhileDetailsFadesInOverBrowse() {
        // Seen on the TV 2026-10-31 (vc208): pressing More info or a card set the
        // restore, Browse recomposed under the fading-in Details, and the restore
        // ran there and was used up, so Back landed on the rail.
        assertEquals(null, HomeRows.heroFocus(inFront = false, hasHero = true, toContent = false, restoreItemId = null, restoreHero = true))
        assertFalse(HomeRows.cardTakesFocus(inFront = false, restoring = true, first = false, toContent = false, restoreItemId = 4))
        assertFalse(HomeRows.cardTakesFocus(inFront = false, restoring = false, first = true, toContent = true, restoreItemId = null))
        // Back in front, the same flags take focus.
        assertTrue(HomeRows.cardTakesFocus(inFront = true, restoring = true, first = false, toContent = false, restoreItemId = 4))
        assertTrue(HomeRows.cardTakesFocus(inFront = true, restoring = false, first = true, toContent = true, restoreItemId = null))
        assertFalse(HomeRows.cardTakesFocus(inFront = true, restoring = false, first = true, toContent = true, restoreItemId = 4))
    }

    @Test
    fun layoutDecodes() {
        val layout = ApiJson.decodeFromString(
            HomeLayout.serializer(),
            """{"rows": [{"key": "trending", "title": "Trending on This Server", "description": "d", "enabled": false},
                {"key": "seasonal", "title": "Holiday and Seasonal Picks", "description": "d", "enabled": true}], "source": "user"}""",
        )
        assertEquals("user", layout.source)
        assertEquals(listOf("trending", "seasonal"), layout.rows.map { it.key })
        assertFalse(layout.rows[0].enabled)
    }

    private fun art(id: Long, kind: String = "movie", backdrop: Boolean = true) =
        """{"id": $id, "kind": "$kind", "title": "Title $id", "has_backdrop": $backdrop}"""

    private fun rows(json: String) = ApiJson.decodeFromString(HomeResponse.serializer(), json).rows

    @Test
    fun featuredMatchesTheWebPick() {
        val home = rows(
            """
            {"rows": [
              {"id": "continue_watching", "kind": "continue_watching", "title": "Continue", "items": [${art(1)}]},
              {"id": "trending", "kind": "trending", "title": "Trending", "items": [${art(2)}, ${art(3, "episode")}, ${art(4, backdrop = false)}]},
              {"id": "recently_added_movies:1", "kind": "recently_added_movies", "title": "New", "items": [${art(5)}, ${art(2)}]}
            ]}
            """.trimIndent(),
        )
        // Recently Added first (5, 2), then the rest (4 has no art); never
        // Continue Watching, never an episode. Day 0 is the first choice.
        assertEquals(5L, HomeRows.featured(home, 0)?.id)
        assertEquals(2L, HomeRows.featured(home, 1)?.id)
        assertEquals(5L, HomeRows.featured(home, 2)?.id)
        // The same all day: the pick turns over by date, not by visit.
        assertEquals(HomeRows.featured(home, 20_000)?.id, HomeRows.featured(home, 20_000)?.id)
    }

    @Test
    fun featuredFallsBackToTitlesWithoutArtAndToNothing() {
        val noArt = rows("""{"rows": [{"id": "a", "kind": "x", "title": "A", "items": [${art(7, backdrop = false)}]}]}""")
        assertEquals(7L, HomeRows.featured(noArt, 3)?.id)
        val onlyEpisodes = rows("""{"rows": [{"id": "a", "kind": "x", "title": "A", "items": [${art(8, "episode")}]}]}""")
        assertEquals(null, HomeRows.featured(onlyEpisodes, 3))
    }

    @Test
    fun featuredPicksFromTheFirstEightWithArt() {
        val many = (1L..12L).joinToString(", ") { art(it) }
        val home = rows("""{"rows": [{"id": "recently_added_movies:1", "kind": "recently_added_movies", "title": "New", "items": [$many]}]}""")
        val picks = (0L until 40L).mapNotNull { HomeRows.featured(home, it)?.id }.toSet()
        assertEquals((1L..8L).toSet(), picks)
    }
}
