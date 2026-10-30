package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Border
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Glow
import androidx.tv.material3.Icon
import androidx.tv.material3.LocalContentColor
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.CornerRadius
import io.github.pete_builds.vcrx.tv.ui.theme.FocusStroke
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import io.github.pete_builds.vcrx.tv.ui.theme.White

/**
 * The wordmark artwork (res/drawable-nodpi/brand_wordmark.png) at its own aspect
 * ratio. It is raster art from the brand kit: never re-type the name as a logo.
 */
@Composable
fun Wordmark(modifier: Modifier = Modifier, height: Dp = 40.dp) {
    BrandImage(R.drawable.brand_wordmark, height, modifier)
}

/** The standalone V mark, for small spots. */
@Composable
fun Mark(modifier: Modifier = Modifier, height: Dp = 22.dp) {
    BrandImage(R.drawable.brand_mark, height, modifier)
}

@Composable
private fun BrandImage(id: Int, height: Dp, modifier: Modifier) {
    val painter = painterResource(id)
    val size = painter.intrinsicSize
    Image(
        painter = painter,
        contentDescription = stringResource(R.string.app_name),
        contentScale = ContentScale.Fit,
        modifier = modifier.height(height).aspectRatio(size.width / size.height),
    )
}

/** Rajdhani, uppercase, widely tracked: buttons, row titles, metadata and codes. */
@Composable
fun Label(
    text: String,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.labelLarge,
    color: Color = Color.Unspecified,
    maxLines: Int = 1,
) {
    Text(text.uppercase(), modifier = modifier, style = style, color = color, maxLines = maxLines, overflow = TextOverflow.Ellipsis)
}

private val noGlow = Glow.None
private val buttonShape = RoundedCornerShape(6.dp)

/**
 * The one button style. Primary actions rest in orange (outline and label), others
 * in gray and white. Focused, every button fills orange with black text, grows a
 * little and gains a white edge, so focus reads at a glance from the couch.
 */
@Composable
fun BrandButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    primary: Boolean = false,
    icon: ImageVector? = null,
    enabled: Boolean = true,
) {
    val rest = if (primary) Orange else Gray
    Button(
        onClick = onClick,
        modifier = modifier,
        enabled = enabled,
        shape = ButtonDefaults.shape(buttonShape, buttonShape, buttonShape, buttonShape, buttonShape),
        colors = ButtonDefaults.colors(
            containerColor = Black,
            contentColor = if (primary) Orange else White,
            focusedContainerColor = Orange,
            focusedContentColor = Black,
            pressedContainerColor = Orange,
            pressedContentColor = Black,
            disabledContainerColor = Black,
            disabledContentColor = Gray,
        ),
        border = ButtonDefaults.border(
            border = Border(BorderStroke(if (primary) 2.dp else 1.dp, rest), shape = buttonShape),
            focusedBorder = Border(BorderStroke(2.dp, White), shape = buttonShape),
            pressedBorder = Border(BorderStroke(2.dp, White), shape = buttonShape),
            disabledBorder = Border(BorderStroke(1.dp, Gray), shape = buttonShape),
            focusedDisabledBorder = Border(BorderStroke(1.dp, Gray), shape = buttonShape),
        ),
        glow = ButtonDefaults.glow(noGlow, noGlow, noGlow),
        scale = ButtonDefaults.scale(focusedScale = 1.06f),
        tonalElevation = 0.dp,
        contentPadding = PaddingValues(horizontal = 22.dp, vertical = 10.dp),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp).padding(end = 6.dp))
        }
        Label(text, style = MaterialTheme.typography.labelLarge)
    }
}

/**
 * A focusable card for artwork: rounded corners, and when focused a 3dp orange
 * border and a slight scale. No glow or shadow.
 */
@Composable
fun FrameCard(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    val shape = RoundedCornerShape(CornerRadius)
    Surface(
        onClick = onClick,
        modifier = modifier,
        shape = ClickableSurfaceDefaults.shape(shape, shape, shape, shape, shape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Black,
            contentColor = White,
            focusedContainerColor = Black,
            focusedContentColor = White,
            pressedContainerColor = Black,
            pressedContentColor = White,
        ),
        border = ClickableSurfaceDefaults.border(
            border = Border.None,
            focusedBorder = Border(BorderStroke(FocusStroke, Orange), shape = shape),
            pressedBorder = Border(BorderStroke(FocusStroke, Orange), shape = shape),
        ),
        glow = ClickableSurfaceDefaults.glow(noGlow, noGlow, noGlow),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.06f),
        tonalElevation = 0.dp,
        content = content,
    )
}

/**
 * A full-width row for menus, lists and dialogs. Focus fills it orange with black
 * text; a selected row carries an orange bar at its leading edge.
 */
@Composable
fun BrandRow(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    selected: Boolean = false,
    icon: ImageVector? = null,
    content: @Composable () -> Unit,
) {
    val shape = RoundedCornerShape(6.dp)
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(shape, shape, shape, shape, shape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Black,
            contentColor = if (selected) Orange else White,
            focusedContainerColor = Orange,
            focusedContentColor = Black,
            pressedContainerColor = Orange,
            pressedContentColor = Black,
        ),
        border = ClickableSurfaceDefaults.border(border = Border.None, focusedBorder = Border.None),
        glow = ClickableSurfaceDefaults.glow(noGlow, noGlow, noGlow),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
        tonalElevation = 0.dp,
    ) {
        Row(
            Modifier.padding(end = 16.dp).height(IntrinsicSize.Min),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            val marker = if (selected) Modifier.background(LocalContentColor.current) else Modifier
            Box(Modifier.width(4.dp).fillMaxHeight().then(marker))
            if (icon != null) Icon(icon, contentDescription = null, modifier = Modifier.size(22.dp))
            Box(Modifier.padding(vertical = 12.dp)) { content() }
        }
    }
}
