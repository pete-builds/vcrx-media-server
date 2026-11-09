package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.R
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.Item
import io.github.pete_builds.vcrx.mobile.offline.DownloadEligibility
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys
import io.github.pete_builds.vcrx.mobile.player.VideoPlayerActivity

/** A movie, show, season or episode: art, facts, Play or Resume, Download, seasons and episodes, cast. */
@Composable
fun DetailsScreen(itemId: Long, onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    Loadable(itemId, {
        val item = graph.api.item(itemId)
        item to (if (item.kind == "show" || item.kind == "season") graph.api.children(itemId) else emptyList())
    }) { (item, children) ->
        Details(item, children, onItem)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Details(item: Item, children: List<Item>, onItem: (Long) -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val api = graph.api
    val me by graph.me.collectAsState()
    val online by graph.online.collectAsState()
    val downloads by graph.downloads.entries.collectAsState()
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
        item {
            val art = when {
                item.hasBackdrop -> api.artworkUrl(item.id, "backdrop", 1280)
                item.hasThumb -> api.artworkUrl(item.id, "thumb", 1280)
                item.showId != null -> api.artworkUrl(item.showId, "backdrop", 1280)
                else -> null
            }
            Art(art, Modifier.fillMaxWidth().aspectRatio(16f / 9f))
        }
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                item.showTitle?.let { Text(it, color = Brand.Muted, style = MaterialTheme.typography.titleSmall) }
                Text(item.title, style = MaterialTheme.typography.headlineMedium)
                FactLine(
                    listOf(
                        if (item.kind == "episode" && item.seasonNumber != null) "Season ${item.seasonNumber}, episode ${item.episodeNumber}" else null,
                        item.year?.toString(),
                        item.contentRating,
                        item.runtimeMs?.let { "${it / 60_000} min" },
                        item.versions.firstOrNull()?.resolution,
                    ),
                )
                if (item.genres.isNotEmpty()) FactLine(listOf(item.genres.take(3).joinToString(", ")))
                if (item.kind == "movie" || item.kind == "episode") {
                    val resume = item.watch?.positionMs?.takeIf { it > 0 && !item.watch.played }
                    val versionId = DownloadEligibility.versionToKeep(item)
                    val key = versionId?.let { DownloadKeys.version(it) }
                    val download = key?.let { k -> downloads.firstOrNull { it.id == k } }
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        if (download?.done == true) {
                            BrandButton("Play downloaded", onClick = { VideoPlayerActivity.playDownload(context, key) }, leading = {
                                Icon(painterResource(R.drawable.ic_play), null, Modifier.size(18.dp).padding(end = 4.dp))
                            })
                        }
                        if (online) {
                            BrandButton(if (resume != null) "Resume ${formatTime(resume)}" else "Play", onClick = {
                                VideoPlayerActivity.playItem(context, item.id, versionId, fromStart = false, title = item.title)
                            }, leading = { Icon(painterResource(R.drawable.ic_play), null, Modifier.size(18.dp).padding(end = 4.dp)) })
                            if (resume != null) QuietButton("From the start", onClick = { VideoPlayerActivity.playItem(context, item.id, versionId, fromStart = true, title = item.title) })
                        }
                        if (online && DownloadEligibility.canDownload(item, me, graph.tokens.isInvitedDevice) && key != null) {
                            when {
                                download == null -> QuietButton("Download", onClick = { graph.downloads.addVideo(item) })
                                download.done -> QuietButton("Remove download", onClick = { graph.downloads.remove(key) })
                                else -> QuietButton("Downloading ${download.percent.toInt()}%, cancel", onClick = { graph.downloads.remove(key) })
                            }
                        }
                    }
                }
                item.tagline?.let { Text(it, style = MaterialTheme.typography.titleSmall) }
                item.overview?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
                if (item.directors.isNotEmpty()) FactLine(listOf("Directed by " + item.directors.joinToString(", ")))
            }
        }
        if (item.kind == "show" && children.isNotEmpty()) {
            item { SeasonPicker(children, onItem) }
        }
        if (item.kind == "season") {
            item { RowHeader("Episodes") }
            items(children, key = { it.id }) { ep -> EpisodeRow(ep, onClick = { onItem(ep.id) }) }
        }
        if (item.cast.isNotEmpty()) {
            item { RowHeader("Cast") }
            item {
                LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    items(item.cast.take(30)) { c ->
                        Column(Modifier.width(84.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                            Art(if (c.hasPhoto && c.personId != null) api.castPhotoUrl(item.id, c.personId) else null, Modifier.size(72.dp).clip(CircleShape))
                            Text(c.name, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            c.role?.let { Text(it, style = MaterialTheme.typography.labelSmall, color = Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SeasonPicker(seasons: List<Item>, onItem: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    var chosen by rememberSaveable { mutableStateOf(seasons.first().id) }
    Column {
        RowHeader("Seasons")
        LazyRow(contentPadding = PaddingValues(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            items(seasons, key = { it.id }) { s ->
                FilterChip(
                    selected = s.id == chosen,
                    onClick = { chosen = s.id },
                    label = { Text(s.title) },
                    colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Brand.Tint),
                )
            }
        }
        Box(Modifier.fillMaxWidth()) {
            Loadable(chosen, { graph.api.children(chosen) }) { eps ->
                Column { eps.forEach { ep -> EpisodeRow(ep, onClick = { onItem(ep.id) }) } }
            }
        }
    }
}

@Composable
private fun EpisodeRow(ep: Item, onClick: () -> Unit) {
    val graph = LocalContext.current.graph
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Box(Modifier.width(140.dp)) {
            Art(if (ep.hasThumb) graph.api.artworkUrl(ep.id, "thumb", 320) else null, Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(6.dp)))
        }
        Column(Modifier.weight(1f)) {
            Text(listOfNotNull(ep.episodeNumber?.let { "$it." }, ep.title).joinToString(" "), style = MaterialTheme.typography.titleSmall, maxLines = 2)
            FactLine(listOf(ep.runtimeMs?.let { "${it / 60_000} min" }, if (ep.watch?.played == true) "Watched" else null))
            ep.overview?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Brand.Muted, maxLines = 2, overflow = TextOverflow.Ellipsis) }
        }
    }
}
