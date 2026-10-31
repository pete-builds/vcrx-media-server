package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.Item
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.CornerRadius
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange

enum class CardShape(val aspect: Float, val requestWidth: Int) {
    Poster(2f / 3f, 300),
    Wide(16f / 9f, 480),
}

/**
 * A focusable card for one item, framed in orange when focused. Without artwork, or
 * when the server answers 404, the placeholder beneath shows the title instead.
 */
@Composable
fun ItemCard(
    item: Item,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    shape: CardShape = CardShape.Poster,
    width: Dp? = if (shape == CardShape.Poster) 146.dp else 252.dp,
    focusModifier: Modifier = Modifier,
    /** Home leaves the title off: its hero names whichever card has focus, as Plex does. */
    showTitle: Boolean = true,
    onFocused: (() -> Unit)? = null,
) {
    Column(if (width != null) modifier.width(width) else modifier.fillMaxWidth()) {
        val focusWatch = if (onFocused != null) Modifier.onFocusChanged { if (it.isFocused) onFocused() } else Modifier
        FrameCard(onClick = onClick, modifier = Modifier.fillMaxWidth().then(focusModifier).then(focusWatch)) {
            Box(Modifier.fillMaxWidth().aspectRatio(shape.aspect)) {
                val (kind, hasArt) = artworkFor(item, shape)
                Artwork(item.id, kind, hasArt, item.title, shape.requestWidth)
                WatchBadge(item)
            }
        }
        if (showTitle) {
            Text(
                item.title,
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 8.dp),
            )
            subtitle(item)?.let {
                Label(it, style = MaterialTheme.typography.labelSmall, color = Gray)
            }
        }
    }
}

private fun artworkFor(item: Item, shape: CardShape): Pair<String, Boolean> = when (shape) {
    CardShape.Poster -> "poster" to item.hasPoster
    CardShape.Wide -> when {
        item.hasThumb -> "thumb" to true
        item.hasBackdrop -> "backdrop" to true
        else -> "thumb" to false
    }
}

fun subtitle(item: Item): String? = item.note ?: when (item.kind) {
    "episode" -> listOfNotNull(episodeCode(item), item.showTitle).joinToString("  ").ifEmpty { null }
    "season" -> item.childCount?.let { if (it == 1) "1 episode" else "$it episodes" }
    "show" -> item.childCount?.let { if (it == 1) "1 season" else "$it seasons" } ?: item.year?.toString()
    else -> item.year?.toString()
}

fun episodeCode(item: Item): String? {
    val season = item.seasonNumber ?: return null
    val episode = item.episodeNumber ?: return "S$season"
    val end = item.episodeNumberEnd?.takeIf { it != episode }?.let { "-E$it" } ?: ""
    return "S${season}E$episode$end"
}

/**
 * Artwork over a placeholder: a graphite tile with the title. A failed or missing
 * image leaves the placeholder showing.
 */
@Composable
fun Artwork(itemId: Long, kind: String, hasArt: Boolean, title: String, requestWidth: Int, modifier: Modifier = Modifier) {
    val api = LocalContext.current.graph.api
    Box(
        modifier
            .fillMaxSize()
            .clip(RoundedCornerShape(CornerRadius))
            .background(Graphite),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            textAlign = TextAlign.Center,
            maxLines = 4,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(12.dp),
        )
        val url = if (hasArt) api.artworkUrl(itemId, kind, requestWidth) else null
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = title,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** Orange badges: a watched tick, an unplayed count, and a progress bar along the bottom. */
@Composable
private fun WatchBadge(item: Item) {
    val watch = item.watch ?: return
    Box(Modifier.fillMaxSize()) {
        if (watch.played && item.kind != "show" && item.kind != "season") {
            Box(
                Modifier.align(Alignment.TopEnd).padding(6.dp).size(24.dp).background(Orange, RoundedCornerShape(12.dp)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(Icons.Filled.Check, contentDescription = "Watched", tint = Black, modifier = Modifier.size(18.dp))
            }
        }
        val unplayed = watch.unplayedCount
        if (unplayed != null && unplayed > 0 && (item.kind == "show" || item.kind == "season")) {
            Box(Modifier.align(Alignment.TopEnd).padding(6.dp).background(Orange, RoundedCornerShape(4.dp)).padding(horizontal = 6.dp, vertical = 2.dp)) {
                Text(unplayed.toString(), style = MaterialTheme.typography.labelMedium, color = Black)
            }
        }
        val duration = watch.durationMs ?: item.runtimeMs
        if (!watch.played && watch.positionMs > 0 && duration != null && duration > 0) {
            val fraction = (watch.positionMs.toFloat() / duration).coerceIn(0.02f, 1f)
            Box(Modifier.align(Alignment.BottomStart).fillMaxWidth().height(5.dp).background(Black)) {
                Box(Modifier.fillMaxWidth(fraction).height(5.dp).background(Orange))
            }
        }
    }
}
