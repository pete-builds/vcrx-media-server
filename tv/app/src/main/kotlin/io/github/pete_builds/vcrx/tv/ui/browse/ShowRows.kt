package io.github.pete_builds.vcrx.tv.ui.browse

import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.net.Item

/**
 * The rows a TV library opens on, as Home does (Pete, 2026-11-03: "when someone
 * goes to TV it shows sections like the home screen does starting with recently
 * watched but use posters ... then more TV categories under it"). Built on the
 * client from what the server already gives: Home's rows narrowed to this
 * library, then a row per leading genre. Every card is a show poster.
 */
object ShowRows {
    /** Rows with fewer shows than this look broken more than sparse, so they are left out. */
    const val MIN_ITEMS = 3

    /** How many genre rows the page adds under the Home-derived ones. */
    const val GENRE_ROWS = 6

    /** The Continue Watching row's id, kept from Home so focus restore works the same way. */
    const val CONTINUE = "continue_watching"

    /**
     * A show card for [item]: a show as it is, an episode or season as its show,
     * carrying the episode's progress so the poster still shows how far in you
     * are. Null for anything without a show (a movie, a stray clip).
     */
    fun asShow(item: Item): Item? = when {
        item.kind == "show" -> item
        item.showId != null && item.showTitle != null -> Item(
            id = item.showId,
            kind = "show",
            libraryId = item.libraryId,
            title = item.showTitle,
            hasPoster = true,
            hasBackdrop = true,
            watch = item.watch,
            note = item.note,
        )
        else -> null
    }

    /** [items] in this library as show cards, first appearance kept, so a binge is one poster. */
    fun shows(items: List<Item>, libraryId: Long): List<Item> {
        val seen = HashSet<Long>()
        return items.asSequence()
            .filter { it.libraryId == libraryId }
            .mapNotNull(::asShow)
            .filter { seen.add(it.id) }
            .toList()
    }

    /**
     * The page: Continue Watching first, then this library's Recently Added,
     * then Home's other rows narrowed to this library in Home's own order, then
     * [genres] (name to its newest shows) for the genres no earlier row already
     * covers. Rows under [MIN_ITEMS] are dropped, except Continue Watching,
     * which is worth showing with even one show in it.
     */
    fun build(libraryId: Long, home: List<HomeRow>, genres: List<Pair<String, List<Item>>>): List<HomeRow> {
        val out = ArrayList<HomeRow>()
        fun add(row: HomeRow, items: List<Item>, min: Int = MIN_ITEMS) {
            if (items.size >= min && out.none { it.id == row.id }) out += row.copy(items = items, libraryId = libraryId)
        }
        home.firstOrNull { it.id == CONTINUE }?.let { add(it, shows(it.items, libraryId), min = 1) }
        home.firstOrNull { it.id == "recently_added_tv:$libraryId" }?.let { add(it.copy(title = "Recently Added"), shows(it.items, libraryId)) }
        for (row in home) {
            if (row.id == CONTINUE || row.id.startsWith("recently_added")) continue
            add(row, shows(row.items, libraryId))
        }
        val covered = out.filter { it.id.startsWith("genre:") }.map { it.id.removePrefix("genre:").lowercase() }.toSet()
        for ((name, items) in genres) {
            if (name.lowercase() in covered) continue
            add(HomeRow(id = "genre:$name", title = name, kind = "genre"), shows(items, libraryId))
        }
        return out
    }

    /** The genres worth a row: the most common first, at most [GENRE_ROWS]. */
    fun topGenres(counts: List<Pair<String, Int>>): List<String> =
        counts.filter { it.second >= MIN_ITEMS }.sortedByDescending { it.second }.take(GENRE_ROWS).map { it.first }
}
