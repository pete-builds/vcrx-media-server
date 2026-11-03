package io.github.pete_builds.vcrx.tv.ui.details

import android.graphics.Bitmap
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.unit.IntSize
import coil3.SingletonImageLoader
import coil3.request.ImageRequest
import coil3.request.SuccessResult
import coil3.request.allowHardware
import coil3.toBitmap
import io.github.pete_builds.vcrx.tv.ui.theme.Motion
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/**
 * The details page's colour wash: three colours taken from the backdrop, the way
 * Google TV tints the whole page from its picture. The web does the same sums in
 * server/web/static/js/wash.js; keep the two in step.
 *
 * [base] comes from the left half, under the text, and stays dark so white text
 * reads on it; [glow] from the top right, the brightest; [deep] from the bottom
 * half, between the two. Each is an ARGB int.
 */
data class Wash(val base: Int, val glow: Int, val deep: Int) {
    companion object {
        /** Before the picture loads, or with none: a dark slate. */
        val Default = Wash(0xFF0A0F14.toInt(), 0xFF1D2A33.toInt(), 0xFF10171D.toInt())

        private val BASE = 0.08f to 0.20f
        private val GLOW = 0.24f to 0.42f
        private val DEEP = 0.12f to 0.26f

        /**
         * The wash of an ARGB pixel grid, row by row. Vivid pixels count for more
         * than grey ones, so a red coat on a grey street gives red, not brown.
         */
        fun from(pixels: IntArray, width: Int, height: Int): Wash {
            fun region(inside: (Int, Int) -> Boolean, light: Pair<Float, Float>): Int {
                var r = 0.0
                var g = 0.0
                var b = 0.0
                var weight = 0.0
                for (y in 0 until height) for (x in 0 until width) {
                    if (!inside(x, y)) continue
                    val p = pixels[y * width + x]
                    val pr = (p shr 16) and 0xFF
                    val pg = (p shr 8) and 0xFF
                    val pb = p and 0xFF
                    val w = 0.15 + saturation(pr, pg, pb)
                    r += pr * w
                    g += pg * w
                    b += pb * w
                    weight += w
                }
                return if (weight == 0.0) tone(0.0, 0.0, 0.0, light) else tone(r / weight, g / weight, b / weight, light)
            }
            return Wash(
                base = region({ x, _ -> x < width / 2.0 }, BASE),
                glow = region({ x, y -> x >= width / 2.0 && y < height / 2.0 }, GLOW),
                deep = region({ _, y -> y >= height / 2.0 }, DEEP),
            )
        }

        private fun saturation(r: Int, g: Int, b: Int): Double {
            val mx = max(r, max(g, b))
            val mn = min(r, min(g, b))
            return if (mx == 0) 0.0 else (mx - mn).toDouble() / mx
        }

        /** Lifts the colour's saturation and holds its lightness inside [light]. */
        private fun tone(r: Double, g: Double, b: Double, light: Pair<Float, Float>): Int {
            val (h, s0, l0) = rgbToHsl(r / 255, g / 255, b / 255)
            val s = min(1.0, s0 * 1.3 + 0.08)
            val l = min(light.second.toDouble(), max(light.first.toDouble(), l0))
            val (rr, gg, bb) = hslToRgb(h, s, l)
            return (0xFF shl 24) or ((rr * 255).roundToInt() shl 16) or ((gg * 255).roundToInt() shl 8) or (bb * 255).roundToInt()
        }

        private fun rgbToHsl(r: Double, g: Double, b: Double): Triple<Double, Double, Double> {
            val mx = max(r, max(g, b))
            val mn = min(r, min(g, b))
            val l = (mx + mn) / 2
            if (mx == mn) return Triple(0.0, 0.0, l)
            val d = mx - mn
            val s = if (l > 0.5) d / (2 - mx - mn) else d / (mx + mn)
            val h = when (mx) {
                r -> (g - b) / d + (if (g < b) 6 else 0)
                g -> (b - r) / d + 2
                else -> (r - g) / d + 4
            }
            return Triple(h / 6, s, l)
        }

        private fun hslToRgb(h: Double, s: Double, l: Double): Triple<Double, Double, Double> {
            if (s == 0.0) return Triple(l, l, l)
            val q = if (l < 0.5) l * (1 + s) else l + s - l * s
            val p = 2 * l - q
            fun f(t0: Double): Double {
                var t = t0
                if (t < 0) t += 1
                if (t > 1) t -= 1
                return when {
                    t < 1.0 / 6 -> p + (q - p) * 6 * t
                    t < 1.0 / 2 -> q
                    t < 2.0 / 3 -> p + (q - p) * (2.0 / 3 - t) * 6
                    else -> p
                }
            }
            return Triple(f(h + 1.0 / 3), f(h), f(h - 1.0 / 3))
        }
    }
}

/** The wash of the picture at [url], read from a small software copy; [Wash.Default] until then. */
@Composable
fun rememberWash(url: String?): Wash {
    val context = LocalContext.current
    var wash by remember(url) { mutableStateOf(Wash.Default) }
    LaunchedEffect(url) {
        if (url == null) return@LaunchedEffect
        val request = ImageRequest.Builder(context).data(url).size(WASH_W * 4, WASH_H * 4).allowHardware(false).build()
        val result = SingletonImageLoader.get(context).execute(request) as? SuccessResult ?: return@LaunchedEffect
        val small = Bitmap.createScaledBitmap(result.image.toBitmap(), WASH_W, WASH_H, true)
        val pixels = IntArray(WASH_W * WASH_H)
        small.getPixels(pixels, 0, WASH_W, 0, 0, WASH_W, WASH_H)
        wash = Wash.from(pixels, WASH_W, WASH_H)
    }
    return wash
}

private const val WASH_W = 32
private const val WASH_H = 18

/**
 * The wash painted across the whole screen: a glow from the top right, the deep
 * colour rising from the bottom left, over a base that runs dark under the text.
 * It fades from one title's colours to the next.
 */
@Composable
fun WashBackground(wash: Wash, modifier: Modifier = Modifier) {
    val spec = tween<Color>(Motion.SLOW, easing = Motion.Ease)
    val base by animateColorAsState(Color(wash.base), spec, label = "wash-base")
    val glow by animateColorAsState(Color(wash.glow), spec, label = "wash-glow")
    val deep by animateColorAsState(Color(wash.deep), spec, label = "wash-deep")
    var size by remember { mutableStateOf(IntSize.Zero) }
    val w = size.width.toFloat().coerceAtLeast(1f)
    val h = size.height.toFloat().coerceAtLeast(1f)
    Box(
        modifier.fillMaxSize().onSizeChanged { size = it }
            .background(Brush.linearGradient(listOf(base, deep), start = Offset(0f, 0f), end = Offset(w * 0.5f, h)))
            .background(Brush.radialGradient(listOf(deep, Color.Transparent), center = Offset(w * 0.1f, h), radius = w * 0.6f))
            .background(Brush.radialGradient(listOf(glow, Color.Transparent), center = Offset(w * 0.92f, 0f), radius = w * 0.62f)),
    )
}
