package io.github.pete_builds.vcrx.tv.settings

/**
 * The Home rows in the viewer's order, without the hidden ones. Rows the viewer
 * has not placed (a new library, a new kind of row) keep the server's order
 * after the placed ones, so nothing new is ever lost. Pure, so it is unit tested.
 */
object HomeLayout {
    fun <R> arrange(rows: List<R>, prefs: HomePrefs, id: (R) -> String): List<R> {
        val placed = prefs.order.withIndex().associate { (i, key) -> key to i }
        return rows
            .filter { id(it) !in prefs.hidden }
            .withIndex()
            .sortedWith(compareBy({ placed[id(it.value)] ?: Int.MAX_VALUE }, { it.index }))
            .map { it.value }
    }

    /** [ids] (every row the server has now) in the order Home shows them, hidden ones included. */
    fun ordered(ids: List<String>, prefs: HomePrefs): List<String> =
        arrange(ids, prefs.copy(hidden = emptyList())) { it }

    /** Moves [rowId] one place up (-1) or down (+1) among [ids] and returns the order to save. */
    fun move(ids: List<String>, prefs: HomePrefs, rowId: String, delta: Int): HomePrefs {
        val list = ordered(ids, prefs).toMutableList()
        val from = list.indexOf(rowId)
        if (from < 0) return prefs
        val to = (from + delta).coerceIn(0, list.lastIndex)
        list.add(to, list.removeAt(from))
        return prefs.copy(order = list)
    }

    fun toggle(prefs: HomePrefs, rowId: String): HomePrefs =
        prefs.copy(hidden = if (rowId in prefs.hidden) prefs.hidden - rowId else prefs.hidden + rowId)
}
