package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.mobile.net.Item

/** What a screen's data is doing. */
sealed interface Load<out T> {
    data object Loading : Load<Nothing>
    data class Ready<T>(val value: T) : Load<T>
    data class Failed(val message: String) : Load<Nothing>
}

/**
 * Loads once per [key] and draws the data, a spinner, or the error with Try again.
 * Most screens are one call to the server, so this is their whole state.
 */
@Composable
fun <T> Loadable(key: Any?, load: suspend () -> T, content: @Composable (T) -> Unit) {
    var attempt by remember { mutableIntStateOf(0) }
    val state by produceState<Load<T>>(Load.Loading, key, attempt) {
        value = Load.Loading
        value = try {
            Load.Ready(load())
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            Load.Failed(e.message ?: "Something went wrong.")
        }
    }
    when (val s = state) {
        Load.Loading -> Centered { CircularProgressIndicator(color = Brand.Green) }
        is Load.Failed -> Centered {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(s.message, textAlign = TextAlign.Center, color = Brand.Muted, modifier = Modifier.padding(horizontal = 24.dp))
                BrandButton("Try again", onClick = { attempt++ })
            }
        }
        is Load.Ready -> content(s.value)
    }
}

@Composable
fun Centered(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) { content() }
}

@Composable
fun Message(text: String) = Centered { Text(text, color = Brand.Muted, textAlign = TextAlign.Center) }

/** Artwork from the server, on a dark tile while it loads or when there is none. */
@Composable
fun Art(url: String?, modifier: Modifier = Modifier, contentScale: ContentScale = ContentScale.Crop) {
    Box(modifier.background(Brand.SurfaceHigh)) {
        if (url != null) AsyncImage(model = url, contentDescription = null, contentScale = contentScale, modifier = Modifier.fillMaxSize())
    }
}

/** A title's card: poster (or an episode's thumb), title, a short line, and how far it was watched. */
@Composable
fun PosterCard(item: Item, artUrl: String?, onClick: () -> Unit, width: Dp = 120.dp, wide: Boolean = false) {
    Column(Modifier.width(width).clickable(onClick = onClick)) {
        Box {
            Art(artUrl, Modifier.fillMaxWidth().aspectRatio(if (wide) 16f / 9f else 2f / 3f).clip(RoundedCornerShape(6.dp)))
            val w = item.watch
            val d = w?.durationMs ?: item.runtimeMs
            if (w != null && w.positionMs > 0 && d != null && d > 0 && !w.played) {
                LinearProgressIndicator(
                    progress = { (w.positionMs.toFloat() / d).coerceIn(0f, 1f) },
                    color = Brand.Green,
                    trackColor = Brand.Black,
                    modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(3.dp),
                )
            }
        }
        Text(item.title, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
        subtitleOf(item)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis) }
    }
}

fun subtitleOf(item: Item): String? = when {
    item.note != null -> item.note
    item.kind == "episode" && item.seasonNumber != null && item.episodeNumber != null ->
        listOfNotNull(item.showTitle, "S${item.seasonNumber} E${item.episodeNumber}").joinToString(" · ")
    else -> item.year?.toString()
}

fun formatTime(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
}

fun formatBytes(bytes: Long): String {
    if (bytes < 0) return "unknown size"
    val units = listOf("B", "KB", "MB", "GB", "TB")
    var v = bytes.toDouble()
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return if (i == 0) "$bytes B" else "%.1f %s".format(v, units[i])
}
