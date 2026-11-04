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

    /**
     * Kotlin labels live in string literals; an XML resource's text sits
     * between tags, outside any quotes, so an XML line is read whole.
     */
    private fun offenders(lines: List<String>, name: String, xml: Boolean = name.endsWith(".xml")) =
        lines.withIndex().filter { (_, line) ->
            val texts = if (xml) sequenceOf(line) else literal.findAll(line).map { it.value }
            texts.any { oldName.containsMatchIn(wireNames.replace(it, "")) }
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
        val kotlin = listOf(
            "add(RailEntry(LIVE, \"Live TV\", AppIcons.Live))",
            "Label(\"Live tv  ·  \${channels.size} channels\")",
            "if (vm.liveTv) add(RailEntry(LIVE, \"IPTV\", AppIcons.Live))",
            "@SerialName(\"live_tv\") val liveTv: Boolean = false,",
        )
        assertEquals(2, offenders(kotlin, "control.kt").size)
        val xml = listOf(
            "<string name=\"live\">LiveTV</string>",
            "<string name=\"live\">IPTV</string>",
        )
        assertEquals(1, offenders(xml, "control.xml").size)
    }
}
