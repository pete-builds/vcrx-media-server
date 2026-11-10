package io.github.pete_builds.vcrx.mobile.auto

import io.github.pete_builds.vcrx.mobile.net.AudiobookBook
import io.github.pete_builds.vcrx.mobile.net.Podcast
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode

/**
 * Search across music, audiobooks and podcasts, for Android Auto's search and
 * for voice requests ("play ... on VCRX"), which play the first answer.
 *
 * Music matches exactly as before ([BrowseTree.search]) and keeps its order.
 * Books (by title, author or series), shows (title or author) and episodes
 * (title or show) are ranked by how well they match: the whole title, the
 * title's start, or every word somewhere. A book, show or episode goes ahead
 * of the music only when it matches better than the best music answer, so a
 * query that found an album still plays that album. Books need the
 * audiobooks feature and shows and episodes the podcasts feature, which an
 * invited device never has ([Features.of]). Pure, so it is unit tested.
 */
object VoiceSearch {
    const val TITLE = 0
    const val TITLE_START = 1
    const val ALL_WORDS = 2
    private const val NONE = 3

    /** Lower case, punctuation as spaces, so a spoken "part two" meets a written "Part Two:". */
    fun normalize(text: String): String = text.lowercase().replace(Regex("[^\\p{L}\\p{N}]+"), " ").trim()

    fun words(text: String): List<String> = normalize(text).split(' ').filter { it.isNotBlank() }

    /**
     * How well [query] matches something titled [title], with [others] (author,
     * series, show) counting for the every-word match: [TITLE], [TITLE_START],
     * [ALL_WORDS], or null for no match.
     */
    fun tier(query: String, title: String, vararg others: String?): Int? {
        val q = normalize(query)
        if (q.isEmpty()) return null
        val t = normalize(title)
        return when {
            t == q -> TITLE
            t.startsWith("$q ") -> TITLE_START
            words(query).all { w -> (listOf(t) + others.filterNotNull().map(::normalize)).joinToString(" ").split(' ').any { it.startsWith(w) } } -> ALL_WORDS
            else -> null
        }
    }

    /**
     * Every answer for [query], best first: [music] as the music search gave it,
     * with the books, shows and episodes this session may see placed by their
     * match. Shows play their newest unplayed episode when a voice request picks one.
     */
    fun rank(
        query: String,
        f: Features,
        music: List<BrowseNode>,
        books: List<AudiobookBook>,
        shows: List<Podcast>,
        episodes: List<PodcastEpisode>,
        limit: Int = 30,
    ): List<BrowseNode> {
        if (words(query).isEmpty()) return emptyList()
        // Order among equal matches: books, then shows, then episodes.
        val audio = buildList {
            if (f.audiobooks) books.forEach { b -> tier(query, b.title, b.author, b.series)?.let { add(Triple(it, 0, BrowseTree.book(b))) } }
            if (f.podcasts) {
                shows.forEach { p -> tier(query, p.title, p.author)?.let { add(Triple(it, 1, BrowseTree.show(p))) } }
                episodes.distinctBy { it.id }.forEach { e -> tier(query, e.title, e.podcastTitle)?.let { add(Triple(it, 2, BrowseTree.episode(e))) } }
            }
        }.sortedWith(compareBy({ it.first }, { it.second }))
        val bestMusic = music.minOfOrNull { tier(query, it.title, it.subtitle) ?: ALL_WORDS } ?: NONE
        val ahead = audio.filter { it.first < bestMusic }.map { it.third }
        val behind = audio.filter { it.first >= bestMusic }.map { it.third }
        return (ahead + music + behind).distinctBy { it.id }.take(limit)
    }
}
