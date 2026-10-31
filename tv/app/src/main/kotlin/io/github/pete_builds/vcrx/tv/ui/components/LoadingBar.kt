package io.github.pete_builds.vcrx.tv.ui.components

import android.provider.Settings
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import kotlinx.coroutines.delay

/** How long a load runs before the bar shows, so a fast one shows nothing. */
const val LOADING_BAR_DELAY_MS = 150L

private val BAR_WIDTH = 220.dp
private val Glow = Color(0xFFFFB27A)

/**
 * The one loading sign, in place of a spinner: a thin VCRX Orange bar pinned
 * top left, with a brighter streak running along it. With animations off on
 * the TV (Settings, Accessibility) the streak holds still.
 */
@Composable
fun LoadingBar(modifier: Modifier = Modifier, description: String = "Loading") {
    var shown by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(LOADING_BAR_DELAY_MS)
        shown = true
    }
    if (!shown) return
    val resolver = LocalContext.current.contentResolver
    val still = remember { Settings.Global.getFloat(resolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f }
    val run = rememberInfiniteTransition(label = "loading")
    val x by run.animateFloat(
        initialValue = -0.4f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(tween(1100, easing = FastOutSlowInEasing), RepeatMode.Restart),
        label = "streak",
    )
    Box(
        modifier
            .width(BAR_WIDTH)
            .height(3.dp)
            .clipToBounds()
            .background(Orange.copy(alpha = 0.3f))
            .semantics { contentDescription = description },
    ) {
        Box(
            Modifier
                .fillMaxHeight()
                .fillMaxWidth(if (still) 1f else 0.4f)
                .graphicsLayer { translationX = if (still) 0f else x * size.width / 0.4f }
                .background(Brush.horizontalGradient(listOf(Orange.copy(alpha = 0f), Orange, Glow, Orange, Orange.copy(alpha = 0f)))),
        )
    }
}
