package io.github.pete_builds.vcrx.tv.ui.components

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Pete, 2026-10-31: "i don't like the spinning loading logo". The loading bar
 * (LoadingBar) is the one loading sign; this fails if a spinner comes back,
 * Compose's or Media3's buffering wheel.
 */
class NoSpinnerTest {
    private val spinners = listOf("CircularProgressIndicator", "SHOW_BUFFERING_WHEN_PLAYING", "SHOW_BUFFERING_ALWAYS", "ProgressBar(")

    @Test
    fun noSpinnerInTheApp() {
        // Unit tests run from the module directory.
        val src = File("src/main")
        assertTrue("source not found from ${File(".").absolutePath}", src.isDirectory)
        val found = src.walk().filter { it.isFile && (it.extension == "kt" || it.extension == "xml") }.flatMap { f ->
            f.readLines().withIndex().filter { (_, line) -> spinners.any { it in line } }.map { (i, line) -> "${f.path}:${i + 1}: ${line.trim()}" }
        }.toList()
        assertEquals("spinners found:\n" + found.joinToString("\n"), emptyList<String>(), found)
    }
}
