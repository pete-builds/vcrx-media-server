package io.github.pete_builds.vcrx.mobile.player

/** One subtitle line, in file time. */
data class Cue(val startMs: Long, val endMs: Long, val text: String)

/**
 * A small WebVTT reader for the server's webvtt_subtitles. The app draws these
 * itself, timed to the file position, so the same code serves direct play and a
 * converted stream that starts part way into the file. Pure, so it is unit tested.
 */
object WebVtt {
    private val TIMING = Regex("^\\s*((?:\\d+:)?\\d{1,2}:\\d{2}[.,]\\d{3})\\s+-->\\s+((?:\\d+:)?\\d{1,2}:\\d{2}[.,]\\d{3})")
    private val TAG = Regex("<[^>]*>")

    fun parse(text: String): List<Cue> {
        val out = mutableListOf<Cue>()
        val lines = text.replace("\r\n", "\n").replace('\r', '\n').split('\n')
        var i = 0
        while (i < lines.size) {
            val m = TIMING.find(lines[i])
            if (m == null) {
                i++
                continue
            }
            val start = time(m.groupValues[1])
            val end = time(m.groupValues[2])
            i++
            val body = StringBuilder()
            while (i < lines.size && lines[i].isNotBlank()) {
                if (body.isNotEmpty()) body.append('\n')
                body.append(lines[i].replace(TAG, ""))
                i++
            }
            if (start != null && end != null && end > start && body.isNotBlank()) {
                out += Cue(start, end, decode(body.toString()))
            }
        }
        return out.sortedBy { it.startMs }
    }

    /** The text showing at [positionMs]; overlapping cues stack, earliest first. */
    fun at(cues: List<Cue>, positionMs: Long): String? =
        cues.filter { positionMs >= it.startMs && positionMs < it.endMs }.joinToString("\n") { it.text }.ifEmpty { null }

    private fun time(s: String): Long? {
        val parts = s.replace(',', '.').split(':')
        return runCatching {
            val (h, m, sec) = when (parts.size) {
                3 -> Triple(parts[0].toLong(), parts[1].toLong(), parts[2])
                2 -> Triple(0L, parts[0].toLong(), parts[1])
                else -> return null
            }
            val (whole, frac) = sec.split('.')
            ((h * 60 + m) * 60 + whole.toLong()) * 1000 + frac.padEnd(3, '0').take(3).toLong()
        }.getOrNull()
    }

    private fun decode(s: String): String =
        s.replace("&lt;", "<").replace("&gt;", ">").replace("&nbsp;", " ").replace("&amp;", "&")
}
