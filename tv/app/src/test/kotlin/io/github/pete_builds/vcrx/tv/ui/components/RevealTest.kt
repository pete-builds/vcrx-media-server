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
}
