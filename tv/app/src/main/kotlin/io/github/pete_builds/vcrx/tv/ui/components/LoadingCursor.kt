package io.github.pete_builds.vcrx.tv.ui.components

import android.provider.Settings
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import kotlinx.coroutines.delay

/** How long a load runs before the cursor shows, so a fast one shows nothing. */
const val LOADING_CURSOR_DELAY_MS = 150L

/** One blink: on for half, off for half, as a terminal's cursor. */
private const val BLINK_MS = 1060

/**
 * The one loading sign, in place of a spinner: a terminal's block cursor in
 * VCRX Green, blinking in the top left corner, with a faint phosphor halo.
 * Placed inside the overscan-safe margin so every TV shows it. With
 * animations off on the TV (Settings, Accessibility) it shows steady.
 */
@Composable
fun LoadingCursor(modifier: Modifier = Modifier, description: String = "Loading") {
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(LOADING_CURSOR_DELAY_MS)
        shown = true
    }
    if (!shown) return
    val resolver = LocalContext.current.contentResolver
    val still = remember { Settings.Global.getFloat(resolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f }
    val blink = rememberInfiniteTransition(label = "cursor")
    val phase by blink.animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(tween(BLINK_MS, easing = LinearEasing), RepeatMode.Restart),
        label = "blink",
    )
    Box(
        modifier
            .padding(start = 24.dp, top = 20.dp)
            .size(width = 18.dp, height = 30.dp)
            // Read in the draw phase, so a blink redraws without recomposing.
            .graphicsLayer { alpha = if (still || phase < 0.5f) 1f else 0f }
            .semantics { contentDescription = description },
        contentAlignment = Alignment.Center,
    ) {
        // The halo: the cursor's colour, faint, a little larger than it.
        Box(Modifier.size(width = 18.dp, height = 30.dp).background(Accent.copy(alpha = 0.28f), RoundedCornerShape(3.dp)))
        Box(Modifier.size(width = 12.dp, height = 24.dp).background(Accent))
    }
}
