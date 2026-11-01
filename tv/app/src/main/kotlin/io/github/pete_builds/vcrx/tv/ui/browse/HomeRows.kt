package io.github.pete_builds.vcrx.tv.ui.browse

import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.net.Item

/**
 * What Home draws from the server's rows, kept free of Compose so it can be
 * unit tested. The server owns which rows exist and their order (each user's
 * Home layout); this keeps that order and only makes the list safe to draw:
 * empty rows go, and repeated row or item ids, which would crash a Lazy list's
 * keys, are dropped after their first appearance.
 */
object HomeRows {
    fun toShow(rows: List<HomeRow>): List<HomeRow> {
        val seenRows = HashSet<String>()
        return rows.mapNotNull { row ->
            if (!seenRows.add(row.id)) return@mapNotNull null
            val seenItems = HashSet<Long>()
            val items = row.items.filter { seenItems.add(it.id) }
            if (items.isEmpty()) null else if (items.size == row.items.size) row else row.copy(items = items)
        }
    }

    /**
     * Wide cards (stills) for Continue Watching and any row made only of
     * episodes; posters for everything else, whatever row the server invents.
     */
    fun wide(row: HomeRow): Boolean =
        row.id == "continue_watching" || (row.items.isNotEmpty() && row.items.all { it.kind == "episode" })

    /**
     * Whether a card should take focus back on returning from Details. The same
     * title can sit in several rows (Trending and Top Rated, say), so the row
     * that opened Details is remembered too; without a row, any card of that
     * title will do (Library and Search grids).
     */
    fun restores(restoreItemId: Long?, restoreRowId: String?, itemId: Long, rowId: String?): Boolean =
        restoreItemId == itemId && (restoreRowId == null || rowId == null || restoreRowId == rowId)

    /** A button in the hero that can take focus. */
    enum class HeroFocus { First, MoreInfo }

    /**
     * Which hero button takes focus as Home appears, or null to leave it to the
     * cards: More info again after it opened Details (it used to fall through
     * to the rail), the first button when the rail hands over, nothing while a
     * card is taking focus back.
     */
    fun heroFocus(hasHero: Boolean, toContent: Boolean, restoreItemId: Long?, restoreHero: Boolean): HeroFocus? = when {
        !hasHero -> null
        restoreHero -> HeroFocus.MoreInfo
        toContent && restoreItemId == null -> HeroFocus.First
        else -> null
    }

    /**
     * The hero's featured title, the same pick as the web's Home (views/home.js
     * feature()): movies and shows from the Recently Added rows first, then the
     * rest, never Continue Watching; those with backdrop art preferred; the
     * first eight; one per day, so Home does not reshuffle on every visit and
     * both clients show the same title. [epochDay] is days since 1970 in UTC.
     */
    fun featured(rows: List<HomeRow>, epochDay: Long): Item? {
        val recent = { r: HomeRow -> r.kind.startsWith("recently_added") }
        val pool = LinkedHashMap<Long, Item>()
        for (row in rows.filter(recent) + rows.filterNot(recent)) {
            if (row.id == "continue_watching") continue
            row.items.filter { it.kind == "movie" || it.kind == "show" }.forEach { pool.putIfAbsent(it.id, it) }
        }
        val withArt = pool.values.filter { it.hasBackdrop }
        val choices = (withArt.ifEmpty { pool.values.toList() }).take(8)
        if (choices.isEmpty()) return null
        return choices[Math.floorMod(epochDay, choices.size.toLong()).toInt()]
    }
}
