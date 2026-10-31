package io.github.pete_builds.vcrx.tv.ui.browse

import io.github.pete_builds.vcrx.tv.net.HomeRow

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
}
