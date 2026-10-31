package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.CornerRadius
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange

/** Square cards per row in the music grids. */
const val MUSIC_COLUMNS = 6

/**
 * A square picture over a placeholder with the title, as posters are drawn:
 * an album cover, or (round) an artist. A missing or failed image leaves the
 * placeholder showing.
 */
@Composable
fun SquareArt(url: String?, title: String, modifier: Modifier = Modifier, round: Boolean = false) {
    Box(
        modifier
            .fillMaxWidth()
            .aspectRatio(1f)
            .clip(if (round) CircleShape else RoundedCornerShape(CornerRadius))
            .background(Graphite),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            textAlign = TextAlign.Center,
            maxLines = 4,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(14.dp),
        )
        if (url != null) {
            AsyncImage(model = url, contentDescription = title, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        }
    }
}

/** A focusable square card with a title and a line under it. [badge] marks a live show's source. */
@Composable
fun MusicCard(
    title: String,
    subtitle: String?,
    imageUrl: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    width: Dp? = null,
    round: Boolean = false,
    badge: String? = null,
    focusModifier: Modifier = Modifier,
) {
    Column(if (width != null) modifier.width(width) else modifier.fillMaxWidth()) {
        FrameCard(onClick = onClick, modifier = Modifier.fillMaxWidth().then(focusModifier)) {
            SquareArt(imageUrl, title, round = round)
            if (badge != null) {
                Box(Modifier.align(Alignment.TopStart).padding(6.dp).background(Orange, RoundedCornerShape(4.dp)).padding(horizontal = 6.dp, vertical = 2.dp)) {
                    Text(badge, style = MaterialTheme.typography.labelMedium, color = Black)
                }
            }
        }
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(top = 8.dp),
        )
        if (!subtitle.isNullOrEmpty()) Label(subtitle, style = MaterialTheme.typography.labelSmall, color = Gray)
    }
}

/** m:ss, or h:mm:ss for a long jam. */
fun clock(ms: Long?): String {
    val total = ((ms ?: 0L) / 1000).coerceAtLeast(0)
    val h = total / 3600
    val m = (total % 3600) / 60
    val s = total % 60
    val two = { n: Long -> n.toString().padStart(2, '0') }
    return if (h > 0) "$h:${two(m)}:${two(s)}" else "$m:${two(s)}"
}
