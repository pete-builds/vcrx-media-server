package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
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
import io.github.pete_builds.vcrx.tv.ui.theme.Focus
import io.github.pete_builds.vcrx.tv.ui.theme.FocusStroke
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.LabelSpacing
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.Shapes
import io.github.pete_builds.vcrx.tv.ui.theme.Surfaces
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

/**
 * Inter, sentence case: buttons, metadata and codes. [caps] widens and uppercases
 * it, for the one small eyebrow over a title ("Featured movie").
 */
@Composable
fun Label(
    text: String,
    modifier: Modifier = Modifier,
    style: TextStyle = MaterialTheme.typography.labelLarge,
    color: Color = Color.Unspecified,
    maxLines: Int = 1,
    caps: Boolean = false,
) {
    val shown = if (caps) text.uppercase() else text
    val styled = if (caps) style.copy(letterSpacing = LabelSpacing) else style
    Text(shown, modifier = modifier, style = styled, color = color, maxLines = maxLines, overflow = TextOverflow.Ellipsis)
}

/** A row or section heading: Seasons, Cast, Continue Watching. */
@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier, style: TextStyle = MaterialTheme.typography.titleLarge) {
    Text(text, modifier = modifier, style = style, color = White, maxLines = 1, overflow = TextOverflow.Ellipsis)
}

private val noGlow = Glow.None

/**
 * The button. Three shapes, one focus: the main action is a pill, the rest
 * softly rounded, and every button the remote is on fills green with black
 * text and grows a little, so focus reads at a glance from the couch. At rest
 * they are a translucent wash; the main action's label rests green.
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
    val shape = if (primary) Shapes.Pill else Shapes.Control
    Button(
        onClick = onClick,
        modifier = modifier,
        enabled = enabled,
        shape = ButtonDefaults.shape(shape, shape, shape, shape, shape),
        colors = brandButtonColors(primary),
        border = ButtonDefaults.border(
            border = Border.None,
            focusedBorder = Border.None,
            pressedBorder = Border.None,
            disabledBorder = Border.None,
            focusedDisabledBorder = Border(BorderStroke(2.dp, Gray), shape = shape),
        ),
        glow = ButtonDefaults.glow(noGlow, noGlow, noGlow),
        scale = ButtonDefaults.scale(focusedScale = Focus.ButtonScale),
        tonalElevation = 0.dp,
        contentPadding = PaddingValues(start = if (icon != null) 18.dp else 22.dp, end = 22.dp, top = 10.dp, bottom = 10.dp),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = null, modifier = Modifier.size(20.dp))
            Spacer(Modifier.width(8.dp))
        }
        Label(text, style = MaterialTheme.typography.labelLarge)
    }
}

/**
 * An action that is only an icon, in a circle: Mark watched beside Play. The
 * name is for TalkBack; focus fills it green like every button.
 */
@Composable
fun BrandIconButton(
    icon: ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    on: Boolean = false,
) {
    val shape = Shapes.Circle
    Button(
        onClick = onClick,
        modifier = modifier.size(44.dp),
        shape = ButtonDefaults.shape(shape, shape, shape, shape, shape),
        colors = ButtonDefaults.colors(
            containerColor = Surfaces.WashStrong,
            contentColor = if (on) Accent else White,
            focusedContainerColor = Accent,
            focusedContentColor = Black,
            pressedContainerColor = Accent,
            pressedContentColor = Black,
        ),
        border = ButtonDefaults.border(border = Border.None, focusedBorder = Border.None, pressedBorder = Border.None),
        glow = ButtonDefaults.glow(noGlow, noGlow, noGlow),
        scale = ButtonDefaults.scale(focusedScale = Focus.ButtonScale),
        tonalElevation = 0.dp,
        contentPadding = PaddingValues(0.dp),
    ) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Icon(icon, contentDescription = contentDescription, modifier = Modifier.size(22.dp))
        }
    }
}

/**
 * A pill to pick one of a few: a season, a filter. The chosen one rests white
 * with black text; focus fills green.
 */
@Composable
fun BrandChip(
    text: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = Shapes.Pill
    Button(
        onClick = onClick,
        modifier = modifier,
        shape = ButtonDefaults.shape(shape, shape, shape, shape, shape),
        colors = ButtonDefaults.colors(
            containerColor = if (selected) White else Surfaces.Wash,
            contentColor = if (selected) Black else Gray,
            focusedContainerColor = Accent,
            focusedContentColor = Black,
            pressedContainerColor = Accent,
            pressedContentColor = Black,
        ),
        border = ButtonDefaults.border(border = Border.None, focusedBorder = Border.None, pressedBorder = Border.None),
        glow = ButtonDefaults.glow(noGlow, noGlow, noGlow),
        scale = ButtonDefaults.scale(focusedScale = Focus.ButtonScale),
        tonalElevation = 0.dp,
        contentPadding = PaddingValues(horizontal = 18.dp, vertical = 8.dp),
    ) {
        Label(text, style = MaterialTheme.typography.labelMedium)
    }
}

@Composable
private fun brandButtonColors(primary: Boolean) = ButtonDefaults.colors(
    containerColor = if (primary) Surfaces.WashStrong else Surfaces.Wash,
    contentColor = if (primary) Accent else White,
    focusedContainerColor = Accent,
    focusedContentColor = Black,
    pressedContainerColor = Accent,
    pressedContentColor = Black,
    disabledContainerColor = Surfaces.Wash,
    disabledContentColor = Gray,
)

/**
 * A focusable card for artwork: rounded corners, and when focused a 3dp green
 * border and a slight scale. No glow or shadow.
 */
@Composable
fun FrameCard(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    /** For text cards (cast, episodes): no fill at rest, a faint one when focused. */
    quiet: Boolean = false,
    content: @Composable BoxScope.() -> Unit,
) {
    val shape = if (quiet) Shapes.Control else Shapes.Card
    val focusedFill = if (quiet) Surfaces.Wash else Color.Transparent
    Surface(
        onClick = onClick,
        modifier = modifier,
        shape = ClickableSurfaceDefaults.shape(shape, shape, shape, shape, shape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent,
            contentColor = White,
            focusedContainerColor = focusedFill,
            focusedContentColor = White,
            pressedContainerColor = focusedFill,
            pressedContentColor = White,
        ),
        border = ClickableSurfaceDefaults.border(
            border = Border.None,
            focusedBorder = Border(BorderStroke(FocusStroke, Accent), shape = shape),
            pressedBorder = Border(BorderStroke(FocusStroke, Accent), shape = shape),
        ),
        glow = ClickableSurfaceDefaults.glow(noGlow, noGlow, noGlow),
        scale = ClickableSurfaceDefaults.scale(focusedScale = if (quiet) Focus.RowScale else Focus.CardScale),
        tonalElevation = 0.dp,
        content = content,
    )
}

/**
 * A full-width row for menus, lists and dialogs. Focus fills it green with black
 * text; a selected row reads white with a short green mark at its leading edge.
 * [restColor] is the label colour of rows that are neither: the rail rests gray.
 */
@Composable
fun BrandRow(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    selected: Boolean = false,
    icon: ImageVector? = null,
    restColor: Color = White,
    content: @Composable () -> Unit,
) {
    val shape = Shapes.Control
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(shape, shape, shape, shape, shape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent,
            contentColor = if (selected) White else restColor,
            focusedContainerColor = Accent,
            focusedContentColor = Black,
            pressedContainerColor = Accent,
            pressedContentColor = Black,
        ),
        border = ClickableSurfaceDefaults.border(border = Border.None, focusedBorder = Border.None),
        glow = ClickableSurfaceDefaults.glow(noGlow, noGlow, noGlow),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
        tonalElevation = 0.dp,
    ) {
        Row(
            Modifier.padding(start = 6.dp, end = 16.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            // The mark is green at rest and takes the label's black when focus fills the row green.
            val mark = if (LocalContentColor.current == Black) Black else Accent
            val marker = if (selected) Modifier.background(mark, Shapes.Pill) else Modifier
            Box(Modifier.width(3.dp).height(18.dp).then(marker))
            if (icon != null) Icon(icon, contentDescription = null, modifier = Modifier.size(22.dp))
            Box(Modifier.padding(vertical = 11.dp)) { content() }
        }
    }
}
