package io.github.pete_builds.vcrx.tv.ui.components

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Pete, 2026-11-01: "the green buttons on vcrx are too intense. don't use full
 * green buttons." A focused or pressed control takes AccentTint with a green
 * ring and white text (brand/README.md). This fails if any control's container
 * fills with the full Accent again; small marks drawn with background(Accent),
 * such as progress bars and badges, are not controls and are not matched.
 */
class NoSolidGreenButtonTest {
    private val solid = Regex("""\b\w*[Cc]ontainerColor\s*=\s*(if\s*\(.*\)\s*)?Accent\b(?!Tint)|\belse\s+Accent\s*,\s*$""")

    private fun offenders(lines: List<String>, name: String) =
        lines.withIndex().filter { (_, line) -> "ContainerColor" in line || "containerColor" in line }
            .filter { (_, line) -> solid.containsMatchIn(line) }
            .map { (i, line) -> "$name:${i + 1}: ${line.trim()}" }

    @Test
    fun noControlFillsWithTheFullGreen() {
        // Unit tests run from the module directory.
        val src = File("src/main")
        assertTrue("source not found from ${File(".").absolutePath}", src.isDirectory)
        val found = src.walk().filter { it.isFile && it.extension == "kt" }.flatMap { f -> offenders(f.readLines(), f.path) }.toList()
        assertEquals("solid green control fills found:\n" + found.joinToString("\n"), emptyList<String>(), found)
    }

    /** The control: the old focus colours must be caught, and the tint must not be. */
    @Test
    fun controlCatchesTheOldFill() {
        val old = listOf(
            "focusedContainerColor = Accent,",
            "pressedContainerColor = Accent,",
            "containerColor = if (on) Accent else Surfaces.Wash,",
            "focusedContainerColor = AccentTint,",
            "contentColor = Accent,",
        )
        assertEquals(3, offenders(old, "control").size)
    }
}
