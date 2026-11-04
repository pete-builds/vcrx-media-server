package io.github.pete_builds.vcrx.tv.ui.components

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Pete, 2026-11-04: "Change live TV to say IP TV." Channels come from M3U
 * playlists, so every word on screen says IPTV. Code names (liveTv, LIVE,
 * LivePlayerActivity) and the server's wire names stay as they are; this
 * looks only inside string literals, which is where a label lives.
 */
class IptvLabelTest {
    private val literal = Regex("\"([^\"\\\\]|\\\\.)*\"")
    private val oldName = Regex("(?i)\\blive[ -]?tv\\b")
    private val wireNames = Regex("\\b(allow_live_tv|live_tv)\\b")

    private fun offenders(lines: List<String>, name: String) =
        lines.withIndex().filter { (_, line) ->
            literal.findAll(line).any { oldName.containsMatchIn(wireNames.replace(it.value, "")) }
        }.map { (i, line) -> "$name:${i + 1}: ${line.trim()}" }

    @Test
    fun noLabelSaysLiveTv() {
        // Unit tests run from the module directory.
        val src = File("src/main")
        assertTrue("source not found from ${File(".").absolutePath}", src.isDirectory)
        val found = src.walk().filter { it.isFile && (it.extension == "kt" || it.extension == "xml") }
            .flatMap { f -> offenders(f.readLines(), f.path) }.toList()
        assertEquals("labels still say Live TV:\n" + found.joinToString("\n"), emptyList<String>(), found)
    }

    /** The control: the old labels are caught; code names and wire names are not. */
    @Test
    fun controlCatchesTheOldLabel() {
        val old = listOf(
            "add(RailEntry(LIVE, \"Live TV\", AppIcons.Live))",
            "Label(\"Live tv  ·  \${channels.size} channels\")",
            "<string name=\"live\">LiveTV</string>",
            "if (vm.liveTv) add(RailEntry(LIVE, \"IPTV\", AppIcons.Live))",
            "@SerialName(\"live_tv\") val liveTv: Boolean = false,",
        )
        assertEquals(3, offenders(old, "control").size)
    }
}
