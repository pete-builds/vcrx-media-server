package io.github.pete_builds.vcrx.tv.ui.theme

import androidx.compose.animation.core.CubicBezierEasing
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

// The design tokens the web app shares (server/web/static/css/app.css :root),
// at TV sizes. The vocabulary is the same; the sizes are for ten feet away.

/** Corners: chips, then artwork, then controls, then panels and dialogs. */
object Radius {
    val Chip = 4.dp
    val Card = 8.dp
    val Control = 10.dp
    val Panel = 14.dp
}

object Shapes {
    val Chip = RoundedCornerShape(Radius.Chip)
    val Card = RoundedCornerShape(Radius.Card)
    val Control = RoundedCornerShape(Radius.Control)
    val Panel = RoundedCornerShape(Radius.Panel)

    /** The one main action on a screen, and filter chips. */
    val Pill = RoundedCornerShape(percent = 50)

    /** A button that is only an icon. */
    val Circle = CircleShape
}

/** Spacing on a 4dp grid. */
object Space {
    val XS = 4.dp
    val S = 8.dp
    val M = 12.dp
    val L = 16.dp
    val XL = 24.dp
    val XXL = 32.dp
    val Page = 48.dp
}

/**
 * Surfaces step up from black by lightness alone. Neutral grays, no hue: orange
 * stays the only colour.
 */
object Surfaces {
    val Raised = Color(0xFF1C1C20)
    val Wash = Color.White.copy(alpha = 0.10f)
    val WashStrong = Color.White.copy(alpha = 0.16f)
}

/** Focus: orange marks where the remote is, everywhere. Small growth, no glow. */
object Focus {
    const val ButtonScale = 1.05f
    const val CardScale = 1.05f
    const val RowScale = 1.02f
}

/** Motion: one easing, three speeds, the same as the web. */
object Motion {
    val Ease = CubicBezierEasing(0.2f, 0.7f, 0.2f, 1f)
    const val FAST = 120
    const val NORMAL = 200
    const val SLOW = 320
}
