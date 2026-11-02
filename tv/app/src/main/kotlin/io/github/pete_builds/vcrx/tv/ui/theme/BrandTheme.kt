package io.github.pete_builds.vcrx.tv.ui.theme

import androidx.compose.foundation.LocalOverscrollFactory
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.text.selection.LocalTextSelectionColors
import androidx.compose.foundation.text.selection.TextSelectionColors
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.tv.material3.Typography
import androidx.tv.material3.darkColorScheme
import io.github.pete_builds.vcrx.tv.R

// The brand palette (brand/README.md): black is the ground, green marks the logo,
// focus, accents, primary actions and headings. Body text is white and secondary
// text light gray, as on the brand spec board. No other hues.
val Accent = Color(0xFF008F11) // VCRX Green, brand/colors.json
val Black = Color(0xFF000000)
val White = Color(0xFFFFFFFF)
val Gray = Color(0xFFA3A3A3)

/** Placeholder artwork and progress tracks: a neutral step up from the black ground. */
val Graphite = Color(0xFF1A1A1A)

val CornerRadius = Radius.Card
val FocusStroke = 3.dp

val Rajdhani = FontFamily(
    Font(R.font.rajdhani_semibold, FontWeight.SemiBold),
    Font(R.font.rajdhani_bold, FontWeight.Bold),
)
val Inter = FontFamily(
    Font(R.font.inter_regular, FontWeight.Normal),
    Font(R.font.inter_medium, FontWeight.Medium),
)

/** Wide tracking for Rajdhani uppercase labels, as on the brand spec board. */
val LabelSpacing: TextUnit = 0.15.em

private val colors = darkColorScheme(
    primary = Accent,
    onPrimary = Black,
    primaryContainer = Accent,
    onPrimaryContainer = Black,
    inversePrimary = Black,
    secondary = Gray,
    onSecondary = Black,
    secondaryContainer = Graphite,
    onSecondaryContainer = White,
    tertiary = Accent,
    onTertiary = Black,
    background = Black,
    onBackground = White,
    surface = Black,
    onSurface = White,
    surfaceVariant = Graphite,
    onSurfaceVariant = Gray,
    surfaceTint = Black,
    inverseSurface = Accent,
    inverseOnSurface = Black,
    error = Accent,
    onError = Black,
    border = Accent,
    borderVariant = Gray,
    scrim = Black,
)

private fun display(size: Int, weight: FontWeight, line: Int) =
    TextStyle(fontFamily = Rajdhani, fontWeight = weight, fontSize = size.sp, lineHeight = line.sp)

private fun inter(size: Int, weight: FontWeight, line: Int) =
    TextStyle(fontFamily = Inter, fontWeight = weight, fontSize = size.sp, lineHeight = line.sp)

// Labels are Inter in sentence case: Rajdhani is for display titles only, and
// all caps is kept for the one small eyebrow (Label caps = true).
private fun label(size: Int, line: Int) =
    TextStyle(fontFamily = Inter, fontWeight = FontWeight.Medium, fontSize = size.sp, lineHeight = line.sp)

private val typography = Typography(
    displayLarge = display(57, FontWeight.Bold, 64),
    displayMedium = display(45, FontWeight.Bold, 52),
    displaySmall = display(40, FontWeight.Bold, 46),
    headlineLarge = display(34, FontWeight.Bold, 40),
    headlineMedium = display(30, FontWeight.Bold, 36),
    headlineSmall = display(26, FontWeight.Bold, 32),
    titleLarge = inter(20, FontWeight.Medium, 28),
    titleMedium = inter(16, FontWeight.Medium, 24),
    titleSmall = inter(14, FontWeight.Medium, 20),
    bodyLarge = inter(16, FontWeight.Normal, 24),
    bodyMedium = inter(14, FontWeight.Normal, 20),
    bodySmall = inter(12, FontWeight.Normal, 16),
    labelLarge = label(16, 22),
    labelMedium = label(14, 20),
    labelSmall = label(13, 18),
)

private val selection = TextSelectionColors(handleColor = Accent, backgroundColor = Accent.copy(alpha = 0.4f))

@Composable
fun BrandTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = colors, typography = typography) {
        // No overscroll effect: D-pad scrolling never needs it, and before Android 12
        // it draws a gray glow over the edges of every row.
        CompositionLocalProvider(
            LocalTextSelectionColors provides selection,
            LocalOverscrollFactory provides null,
        ) {
            Surface(
                modifier = Modifier.fillMaxSize(),
                colors = SurfaceDefaults.colors(containerColor = Black, contentColor = White),
            ) {
                content()
            }
        }
    }
}
