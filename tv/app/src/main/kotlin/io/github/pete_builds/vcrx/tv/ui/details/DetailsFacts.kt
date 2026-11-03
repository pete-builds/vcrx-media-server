// What the Details page says, as the web's details page says it
// (server/web/static/js/views/item.js: chips, resumeBar, techFacts), so the two
// clients read the same. Pure, so unit tests cover it without a TV.
package io.github.pete_builds.vcrx.tv.ui.details

import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Track
import io.github.pete_builds.vcrx.tv.net.Version
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

data class Fact(val label: String, val value: String)

/** How a hero chip is drawn: plain text, text in a thin box, or a star score with its source. */
enum class ChipStyle { Plain, Boxed, Score }

/** One of the hero's facts in a line; [source] names a score's site, such as "IMDb". */
data class HeroChip(val text: String, val style: ChipStyle, val source: String? = null)

private val HDR = mapOf("hdr10" to "HDR10", "hdr10plus" to "HDR10+", "hlg" to "HLG", "dolby_vision" to "Dolby Vision")
private val AUDIO = mapOf(
    "ac3" to "Dolby Digital", "eac3" to "Dolby Digital+", "truehd" to "TrueHD", "dts" to "DTS", "aac" to "AAC",
    "flac" to "FLAC", "opus" to "Opus", "mp3" to "MP3", "vorbis" to "Vorbis",
)

/**
 * The hero's facts in one line, as the web's chips: year, length, seasons or
 * episodes, then rating, resolution, HDR and audio in boxes, then the IMDb and
 * TMDb scores.
 */
fun heroChips(item: Item): List<HeroChip> = buildList {
    item.year?.let { add(HeroChip(it.toString(), ChipStyle.Plain)) }
    item.runtimeMs?.takeIf { it > 0 }?.let { add(HeroChip(webRuntime(it), ChipStyle.Plain)) }
    val n = item.childCount ?: 0
    if (item.kind == "show" && n > 0) add(HeroChip("$n season${if (n == 1) "" else "s"}", ChipStyle.Plain))
    if (item.kind == "season" && n > 0) add(HeroChip("$n episode${if (n == 1) "" else "s"}", ChipStyle.Plain))
    item.contentRating?.takeIf { it.isNotBlank() }?.let { add(HeroChip(it, ChipStyle.Boxed)) }
    val v = item.versions.firstOrNull()
    (v?.resolution ?: item.resolution)?.takeIf { it.isNotBlank() }?.let { add(HeroChip(it, ChipStyle.Boxed)) }
    v?.hdrType?.takeIf { it.isNotBlank() }?.let { add(HeroChip(HDR[it] ?: it.uppercase(), ChipStyle.Boxed)) }
    val audio = v?.audioTracks?.let { tracks -> tracks.firstOrNull { it.isDefault } ?: tracks.firstOrNull() }
    audio?.codec?.takeIf { it.isNotBlank() }?.let { codec ->
        val ch = when (audio.channels) {
            8 -> "7.1"
            6 -> "5.1"
            2 -> "Stereo"
            1 -> "Mono"
            else -> null
        }
        add(HeroChip(listOfNotNull(AUDIO[codec] ?: codec.uppercase(), ch).joinToString(" "), ChipStyle.Boxed))
    }
    item.ratings["imdb"]?.let { add(HeroChip(oneDecimal(it), ChipStyle.Score, "IMDb")) }
    item.ratings["tmdb"]?.let { add(HeroChip(oneDecimal(it), ChipStyle.Score, "TMDb")) }
}

/** How far in a movie or episode someone is: the bar's fill and "1:02:03 left"; null when not started or finished. */
data class ResumeBar(val fraction: Float, val left: String)

fun resumeBar(item: Item): ResumeBar? {
    val w = item.watch ?: return null
    if ((item.kind != "movie" && item.kind != "episode") || w.positionMs <= 0 || w.played) return null
    val total = w.durationMs ?: item.versions.firstOrNull()?.durationMs ?: return null
    if (total <= 0) return null
    val fraction = (w.positionMs.toFloat() / total).coerceIn(0f, 1f)
    return ResumeBar(fraction, "${webClock((total - w.positionMs).coerceAtLeast(0))} left")
}

/**
 * The Details section, row for row as the web's: credits, release, ratings,
 * then each version's file, video, audio, subtitles and size. Empty for a
 * title with nothing to play (a show or season), as on the web.
 */
fun webFacts(item: Item): List<Fact> {
    if (item.versions.isEmpty()) return emptyList()
    val episode = item.kind == "episode"
    return buildList {
        if (item.directors.isNotEmpty()) add(Fact("Directed by", item.directors.joinToString(", ")))
        if (item.writers.isNotEmpty()) add(Fact("Written by", item.writers.joinToString(", ")))
        if (item.studios.isNotEmpty()) add(Fact(if (episode) "Network" else "Studio", item.studios.joinToString(", ")))
        if (item.countries.isNotEmpty()) add(Fact("Country", item.countries.joinToString(", ")))
        releaseDate(item.premiereDate)?.let { add(Fact(if (episode) "Aired" else "Released", it)) }
        ratingLines(item).takeIf { it.isNotEmpty() }?.let { add(Fact("Ratings", it)) }
        item.versions.forEachIndexed { i, v ->
            val prefix = if (item.versions.size > 1) "Version ${i + 1} " else ""
            add(Fact(prefix + "File", v.fileName))
            add(Fact(prefix + "Video", videoLine(v)))
            if (v.audioTracks.isNotEmpty()) add(Fact(prefix + "Audio", v.audioTracks.joinToString("\n") { trackName(it) }))
            if (v.subtitleTracks.isNotEmpty()) add(Fact(prefix + "Subtitles", v.subtitleTracks.joinToString("\n") { trackName(it) }))
            add(Fact(prefix + "Size", listOf(fileSize(v.size), bitrate(v.bitrate), v.container.orEmpty()).filter { it.isNotEmpty() }.joinToString(" · ")))
        }
    }
}

/** One line per rating, with the votes behind it when known: "IMDb 9.0 · 1M votes". */
private fun ratingLines(item: Item): String = listOf("imdb" to "IMDb", "tmdb" to "TMDb").mapNotNull { (key, name) ->
    item.ratings[key]?.let { score ->
        val votes = item.ratingVotes[key]?.takeIf { it > 0 }?.let { "${compactVotes(it)} vote${if (it == 1L) "" else "s"}" }
        listOfNotNull("$name ${oneDecimal(score)}", votes).joinToString(" · ")
    }
}.joinToString("\n")

private fun videoLine(v: Version): String = listOfNotNull(
    v.resolution,
    if (v.width != null && v.height != null) "${v.width}×${v.height}" else null,
    v.videoCodec,
    v.videoProfile,
    v.bitDepth?.let { "$it-bit" },
    v.hdrType?.uppercase(),
).filter { it.isNotEmpty() }.joinToString(" · ")

/** A track as the web names it: title, language, codec, channels, forced, file. */
fun trackName(t: Track): String = listOfNotNull(
    t.title,
    t.language?.uppercase(),
    t.codec,
    t.channels?.takeIf { it > 0 }?.let { "$it ch" },
    if (t.isForced) "forced" else null,
    if (t.isExternal) "file" else null,
).filter { it.isNotEmpty() }.joinToString(" · ")

/** "1 h 49 min", or "45 min" under an hour, as the web writes a runtime. */
fun webRuntime(ms: Long): String {
    val m = Math.round(ms / 60_000.0)
    return if (m >= 60) "${m / 60} h ${m % 60} min" else "$m min"
}

/** "1:02:03", or "4:05" under an hour. */
fun webClock(ms: Long): String {
    val t = Math.round(ms.coerceAtLeast(0) / 1000.0)
    val hh = t / 3600
    val mm = (t % 3600) / 60
    val ss = t % 60
    return if (hh > 0) "%d:%02d:%02d".format(Locale.US, hh, mm, ss) else "%d:%02d".format(Locale.US, mm, ss)
}

/** Binary units as the web shows a file size: "7.0 GB", "350 MB". */
fun fileSize(bytes: Long): String {
    if (bytes <= 0) return ""
    val units = listOf("B", "KB", "MB", "GB", "TB")
    var n = bytes.toDouble()
    var i = 0
    while (n >= 1024 && i < units.size - 1) {
        n /= 1024
        i++
    }
    return (if (n >= 10 || i == 0) "%.0f" else "%.1f").format(Locale.US, n) + " " + units[i]
}

/** "12.3 Mbps", or "640 kbps" under a megabit. */
fun bitrate(bps: Long?): String = when {
    bps == null || bps <= 0 -> ""
    bps >= 1_000_000 -> "%.1f Mbps".format(Locale.US, bps / 1_000_000.0)
    else -> "${Math.round(bps / 1000.0)} kbps"
}

private fun oneDecimal(v: Double): String = "%.1f".format(Locale.US, v)

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
