package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.RowScope
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.github.pete_builds.vcrx.mobile.R

/** The VCRX brand (brand/README.md): green on black, Rajdhani for titles, Inter for text. */
object Brand {
    val Green = Color(0xFF008F11)

    /** The green at about 30% over black: the fill of a button, never the full green. */
    val Tint = Color(0xFF002B05)
    val Black = Color(0xFF000000)
    val Surface = Color(0xFF0E0E0E)
    val SurfaceHigh = Color(0xFF1A1A1A)
    val Muted = Color(0xFFA0A0A0)
    val Error = Color(0xFFFF6B6B)

    val Rajdhani = FontFamily(Font(R.font.rajdhani_semibold, FontWeight.SemiBold), Font(R.font.rajdhani_bold, FontWeight.Bold))
    val Inter = FontFamily(Font(R.font.inter_regular, FontWeight.Normal), Font(R.font.inter_medium, FontWeight.Medium))
}

private val colors = darkColorScheme(
    primary = Brand.Green,
    onPrimary = Color.White,
    primaryContainer = Brand.Tint,
    onPrimaryContainer = Color.White,
    secondary = Brand.Green,
    onSecondary = Color.White,
    secondaryContainer = Brand.Tint,
    onSecondaryContainer = Color.White,
    background = Brand.Black,
    onBackground = Color.White,
    surface = Brand.Black,
    onSurface = Color.White,
    surfaceVariant = Brand.SurfaceHigh,
    onSurfaceVariant = Brand.Muted,
    surfaceContainer = Brand.Surface,
    surfaceContainerHigh = Brand.SurfaceHigh,
    surfaceContainerHighest = Brand.SurfaceHigh,
    surfaceContainerLow = Brand.Surface,
    error = Brand.Error,
    outline = Brand.Green,
)

private val base = Typography()

private val type = Typography(
    displaySmall = base.displaySmall.copy(fontFamily = Brand.Rajdhani, fontWeight = FontWeight.Bold),
    headlineMedium = base.headlineMedium.copy(fontFamily = Brand.Rajdhani, fontWeight = FontWeight.Bold),
    headlineSmall = base.headlineSmall.copy(fontFamily = Brand.Rajdhani, fontWeight = FontWeight.Bold),
    titleLarge = base.titleLarge.copy(fontFamily = Brand.Rajdhani, fontWeight = FontWeight.Bold),
    titleMedium = base.titleMedium.copy(fontFamily = Brand.Rajdhani, fontWeight = FontWeight.SemiBold, fontSize = 18.sp),
    titleSmall = base.titleSmall.copy(fontFamily = Brand.Inter, fontWeight = FontWeight.Medium),
    bodyLarge = base.bodyLarge.copy(fontFamily = Brand.Inter),
    bodyMedium = base.bodyMedium.copy(fontFamily = Brand.Inter),
    bodySmall = base.bodySmall.copy(fontFamily = Brand.Inter),
    labelLarge = base.labelLarge.copy(fontFamily = Brand.Inter, fontWeight = FontWeight.Medium),
    labelMedium = base.labelMedium.copy(fontFamily = Brand.Inter),
    labelSmall = base.labelSmall.copy(fontFamily = Brand.Inter),
)

@Composable
fun VcrxTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = colors, typography = type, content = content)
}

/** The brand's button: a dark green tint with a green ring and white text, never a solid green fill. */
@Composable
fun BrandButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    leading: (@Composable RowScope.() -> Unit)? = null,
) {
    OutlinedButton(
        onClick = onClick,
        modifier = modifier,
        enabled = enabled,
        border = BorderStroke(1.dp, if (enabled) Brand.Green else Brand.SurfaceHigh),
        colors = ButtonDefaults.outlinedButtonColors(containerColor = Brand.Tint, contentColor = Color.White),
        contentPadding = PaddingValues(horizontal = 18.dp, vertical = 10.dp),
    ) {
        leading?.invoke(this)
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
}

/** A quieter action: no fill, white text. */
@Composable
fun QuietButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    TextButton(onClick = onClick, modifier = modifier, enabled = enabled, colors = ButtonDefaults.textButtonColors(contentColor = Color.White)) {
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
}

val SectionTitle: TextStyle @Composable get() = MaterialTheme.typography.titleLarge
