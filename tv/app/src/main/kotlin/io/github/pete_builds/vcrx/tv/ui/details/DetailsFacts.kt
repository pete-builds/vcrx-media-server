// The details panel's facts, as label and value pairs. Pure, so unit tests
// cover what the panel says without a TV.
package io.github.pete_builds.vcrx.tv.ui.details

import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Track
import io.github.pete_builds.vcrx.tv.net.Version
import io.github.pete_builds.vcrx.tv.player.TrackLabels
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

data class Fact(val label: String, val value: String)

/** About the title on the left, the chosen version's file on the right. */
data class Facts(val about: List<Fact>, val media: List<Fact>)

/** How many subtitle languages are named before "and N more". */
private const val SUBTITLE_LANGUAGES = 6

/** How many audio tracks are listed before "and N more". */
private const val AUDIO_TRACKS = 3

fun detailFacts(item: Item, version: Version?): Facts {
    val episode = item.kind == "episode"
    val about = buildList {
        releaseDate(item.premiereDate)?.let { add(Fact(if (episode) "Aired" else "Released", it)) }
        item.runtimeMs?.let { add(Fact("Runtime", formatRuntime(it))) }
        item.contentRating?.takeIf { it.isNotBlank() }?.let { add(Fact("Rated", it)) }
        listOf("imdb" to "IMDb", "tmdb" to "TMDB").forEach { (key, name) ->
            item.ratings[key]?.let { score ->
                val votes = item.ratingVotes[key]?.let { " from ${compactVotes(it)} votes" }.orEmpty()
                add(Fact(name, "%.1f".format(Locale.US, score) + votes))
            }
        }
        if (item.directors.isNotEmpty()) add(Fact("Directed by", item.directors.joinToString(", ")))
        if (item.writers.isNotEmpty()) add(Fact("Written by", item.writers.joinToString(", ")))
        if (item.studios.isNotEmpty()) add(Fact(if (episode) "Network" else "Studio", item.studios.joinToString(", ")))
        if (item.countries.isNotEmpty()) add(Fact("Country", item.countries.joinToString(", ")))
    }
    val media = if (version == null) emptyList() else buildList {
        videoLine(version).takeIf { it.isNotEmpty() }?.let { add(Fact("Video", it)) }
        if (version.audioTracks.isNotEmpty()) {
            val shown = version.audioTracks.take(AUDIO_TRACKS).map { audioLine(it) }
            add(Fact("Audio", (shown + more(version.audioTracks.size - shown.size)).joinToString("\n")))
        }
        val languages = version.subtitleTracks.map { languageOf(it) }.distinct()
        if (languages.isNotEmpty()) {
            val shown = languages.take(SUBTITLE_LANGUAGES)
            add(Fact("Subtitles", (shown + more(languages.size - shown.size)).joinToString(", ")))
        }
        add(Fact("File", listOfNotNull(gigabytes(version.size), version.container?.uppercase()).joinToString("   ")))
    }
    return Facts(about, media)
}

private fun more(n: Int): List<String> = if (n > 0) listOf("and $n more") else emptyList()

/** "April 10, 1957" for "1957-04-10"; null for anything else. */
fun releaseDate(ymd: String?): String? = try {
    ymd?.let { LocalDate.parse(it).format(DateTimeFormatter.ofPattern("MMMM d, yyyy", Locale.ENGLISH)) }
} catch (_: DateTimeParseException) {
    null
}

/** "1M" for 1,010,165, "10.5K" for 10,538, "950" for 950. */
fun compactVotes(n: Long): String {
    fun one(v: Double, unit: String) = "%.1f".format(Locale.US, v).removeSuffix(".0") + unit
    return when {
        n >= 999_950 -> one(n / 1_000_000.0, "M")
        n >= 1_000 -> one(n / 1_000.0, "K")
        else -> n.toString()
    }
}

private fun videoLine(v: Version): String = listOfNotNull(
    v.resolution,
    v.videoCodec?.uppercase(),
    v.bitDepth?.let { "$it-bit" },
    v.hdrType?.uppercase(),
).joinToString("   ")

private fun audioLine(t: Track): String = listOfNotNull(
    languageOf(t),
    channelName(t.channels),
    t.codec?.uppercase(),
).distinct().joinToString("   ")

private fun languageOf(t: Track): String =
    t.language?.takeIf { it.isNotBlank() && it != "und" }?.let { TrackLabels.language(it) }
        ?: t.title?.takeIf { it.isNotBlank() }
        ?: "Unknown"

private fun channelName(channels: Int?): String? = when (channels) {
    null -> null
    1 -> "Mono"
    2 -> "Stereo"
    6 -> "5.1"
    8 -> "7.1"
    else -> "$channels ch"
}

private fun gigabytes(bytes: Long): String? =
    if (bytes > 0) "%.1f GB".format(Locale.US, bytes / 1_000_000_000.0) else null
