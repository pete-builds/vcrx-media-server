package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.BringIntoViewSpec

/**
 * How far a list scrolls to show the child that took focus, kept free of
 * Compose so it is unit tested. On a TV, Compose's default moves every focused
 * child to 30 percent down the screen, even one already in full view; on
 * Details that scrolled the poster and title off the top the moment Play took
 * focus (Pete, 2026-10-31: "it's chipping off the poster at the top"). This
 * scrolls only when the child is cut off, and only as far as it takes to show
 * it. [offset] is the child's top relative to the list's, [size] its height,
 * [containerSize] the list's height; the answer is how far to scroll down.
 */
object Reveal {
    /**
     * [margin] keeps that much space between a revealed child and the list's
     * edge, so a focused row never sits flush on the bottom of the screen
     * (Pete, 2026-11-02: the cast pills sat cut off at the bottom). It shrinks
     * for a child too tall to have it on both sides.
     */
    fun distance(offset: Float, size: Float, containerSize: Float, margin: Float = 0f): Float {
        val m = margin.coerceAtMost(((containerSize - size) / 2f).coerceAtLeast(0f))
        val top = offset - m
        val end = offset + size + m
        return when {
            top >= 0f && end <= containerSize -> 0f
            // Cut off at the top, or taller than the list: its top at the top.
            top < 0f || size > containerSize -> top
            else -> end - containerSize
        }
    }

    /** Whether a list's first item, [size] tall below [topPadding], fits whole in a [viewport]-tall list. */
    fun fitsAtTop(size: Int, topPadding: Int, viewport: Int): Boolean = topPadding + size <= viewport
}

/** [Reveal] for a list: give it through LocalBringIntoViewSpec. */
@OptIn(ExperimentalFoundationApi::class)
val RevealOnly: BringIntoViewSpec = object : BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float =
        Reveal.distance(offset, size, containerSize)
}

/** [Reveal] with [marginPx] of space kept between the revealed child and the list's edges. */
@OptIn(ExperimentalFoundationApi::class)
fun revealWithMargin(marginPx: Float): BringIntoViewSpec = object : BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float =
        Reveal.distance(offset, size, containerSize, marginPx)
}
