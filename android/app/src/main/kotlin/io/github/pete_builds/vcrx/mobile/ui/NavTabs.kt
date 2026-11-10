package io.github.pete_builds.vcrx.mobile.ui

/**
 * Which top level destinations a phone's bottom bar shows, and which go under
 * More. Up to [MAX_BAR] fit as they are (the layout before audiobooks and
 * podcasts, unchanged); past that the bar keeps the first ones, More, and the
 * last (Downloads, which must stay one tap away for a phone with no signal).
 * A tablet's rail has room for every one. Pure, so it is unit tested.
 */
object NavTabs {
    const val MAX_BAR = 6

    /** The routes in the bar, with [MORE] where the overflow opens, and the routes under More. */
    fun split(routes: List<String>, max: Int = MAX_BAR): Pair<List<String>, List<String>> {
        if (routes.size <= max) return routes to emptyList()
        val keep = max - 2
        return (routes.take(keep) + MORE + routes.last()) to routes.subList(keep, routes.size - 1)
    }

    const val MORE = "more"
}
