package io.github.pete_builds.vcrx.tv.ui.details

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class WashTest {
    private fun r(c: Int) = (c shr 16) and 0xFF
    private fun g(c: Int) = (c shr 8) and 0xFF
    private fun b(c: Int) = c and 0xFF

    /** HSL lightness of an ARGB colour, 0..1. */
    private fun lightness(c: Int) = (maxOf(r(c), g(c), b(c)) + minOf(r(c), g(c), b(c))) / 2.0 / 255

    /** A 32x18 picture: blue on the left half, yellow on the right, as Ali G's poster art. */
    private fun split(left: Int, right: Int) = IntArray(32 * 18) { i -> if (i % 32 < 16) left else right }

    @Test
    fun eachColourComesFromItsOwnPartOfThePicture() {
        val w = Wash.from(split(0xFF1030C0.toInt(), 0xFFF0C010.toInt()), 32, 18)
        assertTrue("base is blue, from the left: ${Integer.toHexString(w.base)}", b(w.base) > r(w.base) && b(w.base) > g(w.base))
        assertTrue("glow is yellow, from the top right: ${Integer.toHexString(w.glow)}", r(w.glow) > b(w.glow) && g(w.glow) > b(w.glow))
    }

    @Test
    fun theBaseStaysDarkEnoughForWhiteTextAndTheGlowIsBrighter() {
        val bright = Wash.from(IntArray(32 * 18) { 0xFFFFF0E0.toInt() }, 32, 18)
        assertTrue("base lightness ${lightness(bright.base)}", lightness(bright.base) <= 0.21)
        assertTrue("glow lightness ${lightness(bright.glow)}", lightness(bright.glow) <= 0.43)
        val dark = Wash.from(IntArray(32 * 18) { 0xFF000000.toInt() }, 32, 18)
        assertTrue("a black picture still gives a visible glow: ${lightness(dark.glow)}", lightness(dark.glow) >= 0.23)
        assertTrue(lightness(dark.glow) > lightness(dark.base))
    }

    @Test
    fun vividPixelsOutweighGreyOnes() {
        // Three grey pixels to every red one in the left half: still a red base, not brown-grey.
        val pixels = IntArray(32 * 18) { i -> if (i % 4 == 0) 0xFFD01010.toInt() else 0xFF808080.toInt() }
        val w = Wash.from(pixels, 32, 18)
        assertTrue("base ${Integer.toHexString(w.base)}", r(w.base) > g(w.base) * 2)
    }

    @Test
    fun differentPicturesGiveDifferentWashes() {
        assertNotEquals(Wash.from(split(0xFF1030C0.toInt(), 0xFF1030C0.toInt()), 32, 18), Wash.from(split(0xFF10C030.toInt(), 0xFF10C030.toInt()), 32, 18))
        assertEquals(0xFF, (Wash.Default.base ushr 24))
    }
}
