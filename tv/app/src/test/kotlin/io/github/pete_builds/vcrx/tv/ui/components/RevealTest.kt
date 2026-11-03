package io.github.pete_builds.vcrx.tv.ui.components

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Details stays put while Play is in view: the poster and title stay on screen. */
class RevealTest {
    private val screen = 540f

    @Test
    fun aChildInFullViewDoesNotScroll() {
        // Play on Details as it opens: 384dp down, 44dp tall. The TV default scrolled it to 30 percent.
        assertEquals(0f, Reveal.distance(384f, 44f, screen))
        assertEquals(0f, Reveal.distance(0f, 44f, screen))
        assertEquals(0f, Reveal.distance(496f, 44f, screen))
    }

    @Test
    fun aChildCutOffAtTheBottomScrollsJustIntoView() {
        // The cast row, 100dp below the bottom edge: 100dp down, no further.
        assertEquals(100f, Reveal.distance(500f, 140f, screen))
    }

    @Test
    fun aChildCutOffAtTheTopScrollsBackDown() {
        assertEquals(-60f, Reveal.distance(-60f, 44f, screen))
    }

    @Test
    fun theHeaderScrollsBackToTheTopOnlyWhenItFits() {
        assertTrue(Reveal.fitsAtTop(size = 400, topPadding = 48, viewport = 540))
        assertTrue(Reveal.fitsAtTop(size = 492, topPadding = 48, viewport = 540))
        // A long title and plot: scrolling to the top would hide Play below the bottom edge.
        assertFalse(Reveal.fitsAtTop(size = 520, topPadding = 48, viewport = 540))
    }

    @Test
    fun aChildTallerThanTheListShowsItsTop() {
        assertEquals(-30f, Reveal.distance(-30f, 700f, screen))
        assertEquals(20f, Reveal.distance(20f, 700f, screen))
    }

    @Test
    fun aMarginKeepsAFocusedRowOffTheBottomEdge() {
        // The cast row, flush on the bottom edge: lifted a 48 margin clear of it.
        assertEquals(48f, Reveal.distance(480f, 60f, screen, margin = 48f))
        // Already a margin clear: stays put.
        assertEquals(0f, Reveal.distance(400f, 60f, screen, margin = 48f))
        // Cut off at the top: comes down to a margin below it.
        assertEquals(-58f, Reveal.distance(-10f, 60f, screen, margin = 48f))
        // Too tall for a margin on both sides: the margin shrinks to what fits.
        assertEquals(20f, Reveal.distance(20f, 540f, screen, margin = 48f))
    }
}
