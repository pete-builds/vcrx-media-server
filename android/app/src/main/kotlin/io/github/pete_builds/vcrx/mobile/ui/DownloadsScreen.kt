package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.annotation.OptIn
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.offline.Download
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.offline.ArtStore
import io.github.pete_builds.vcrx.mobile.offline.BookDownload
import io.github.pete_builds.vcrx.mobile.offline.DownloadEntry
import io.github.pete_builds.vcrx.mobile.offline.DownloadGroups
import io.github.pete_builds.vcrx.mobile.offline.DownloadMeta
import io.github.pete_builds.vcrx.mobile.player.VideoPlayerActivity
import kotlinx.coroutines.delay

/**
 * Everything kept on this phone. It needs no network at all: the app opens here
 * when the server cannot be reached, and everything listed plays from the phone.
 */
@Composable
fun DownloadsScreen(remote: MusicRemote?) {
    val context = LocalContext.current
    val graph = context.graph
    val entries by graph.downloads.entries.collectAsState()
    val online by graph.online.collectAsState()
    var wifiOnly by remember { mutableStateOf(graph.prefs.wifiOnly) }
    var used by remember { mutableStateOf(0L) }
    var deleting by remember { mutableStateOf<Deleting?>(null) }
    LaunchedEffect(Unit) {
        while (true) {
            graph.downloads.refresh()
            used = graph.downloads.usedBytes()
            delay(1_000)
        }
    }
    LazyColumn(Modifier.fillMaxSize()) {
        if (!online) {
            item {
                Text(
                    "Cannot reach the server. Your downloads still play, and where you got to is sent to the server when it is back.",
                    color = Brand.Muted,
                    modifier = Modifier.padding(16.dp),
                )
            }
        }
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("${formatBytes(used)} used, ${formatBytes(graph.downloads.freeBytes())} free on this device", color = Brand.Muted)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Download on Wi-Fi only", modifier = Modifier.weight(1f))
                    Switch(
                        checked = wifiOnly,
                        onCheckedChange = {
                            wifiOnly = it
                            graph.downloads.setWifiOnly(it)
                        },
                        colors = SwitchDefaults.colors(checkedTrackColor = Brand.Tint, checkedThumbColor = Brand.Green, checkedBorderColor = Brand.Green),
                    )
                }
            }
        }
        if (entries.isEmpty()) {
            item {
                Text(
                    "Nothing downloaded yet. Use Download on a movie, an episode, an album, an audiobook or a podcast episode.",
                    color = Brand.Muted,
                    modifier = Modifier.padding(16.dp),
                )
            }
        }
        val videos = entries.filter { it.meta.kind == DownloadMeta.VIDEO }
        val tracks = entries.filter { it.meta.kind == DownloadMeta.TRACK }
        val books = DownloadGroups.books(entries)
        val episodes = DownloadGroups.episodes(entries)
        if (videos.isNotEmpty()) item { RowHeader("Movies and episodes") }
        items(videos, key = { it.id }) { e ->
            DownloadRow(e, onPlay = { VideoPlayerActivity.playDownload(context, e.id) }, onDelete = { deleting = Deleting(e.meta.title) { graph.downloads.remove(e.id) } })
        }
        if (books.isNotEmpty()) item { RowHeader("Audiobooks") }
        items(books, key = { "book${it.bookId}" }) { b ->
            BookDownloadRow(b, onPlay = { remote?.playDownloadedBook(b.bookId) }, onDelete = { deleting = Deleting(b.title) { graph.downloads.removeBook(b.bookId) } })
        }
        if (episodes.isNotEmpty()) item { RowHeader("Podcasts") }
        items(episodes, key = { it.id }) { e ->
            DownloadRow(e, onPlay = { remote?.playDownloadedEpisode(e.meta.episodeId) }, onDelete = { deleting = Deleting(e.meta.title) { graph.downloads.removeEpisode(e.meta.episodeId) } })
        }
        if (tracks.isNotEmpty()) item { RowHeader("Music") }
        items(tracks, key = { it.id }) { e ->
            DownloadRow(e, onPlay = { remote?.playDownloadedTrack(e.meta.trackId) }, onDelete = { deleting = Deleting(e.meta.title) { graph.downloads.remove(e.id) } })
        }
    }
    deleting?.let { d ->
        AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text("Delete download?") },
            text = { Text("${d.title} is removed from this device. It still plays from the server.") },
            confirmButton = {
                BrandButton("Delete", onClick = {
                    d.delete()
                    deleting = null
                })
            },
            dismissButton = { QuietButton("Keep", onClick = { deleting = null }) },
        )
    }
}

/** What the delete dialog is about, and how to delete it. */
private class Deleting(val title: String, val delete: () -> Unit)

/** A downloaded book: its files as one row, with progress over the whole book. */
@OptIn(UnstableApi::class)
@Composable
private fun BookDownloadRow(b: BookDownload, onPlay: () -> Unit, onDelete: () -> Unit) {
    val context = LocalContext.current
    val art = ArtStore.existing(context, ArtStore.BOOK, b.bookId)
    val failed = b.files.any { it.state == Download.STATE_FAILED }
    Row(
        Modifier.fillMaxWidth().clickable(enabled = b.done, onClick = onPlay).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(art?.toURI()?.toString(), Modifier.width(48.dp).aspectRatio(1f).clip(RoundedCornerShape(4.dp)))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(b.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyLarge)
            if (b.author.isNotEmpty()) Text(b.author, maxLines = 1, color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
            val status = when {
                failed -> "Failed. Delete it and try again."
                b.done -> formatBytes(b.bytes)
                else -> "${b.percent.toInt()}%, ${b.filesDone} of ${b.files.size} files"
            }
            Text(status, color = if (failed) Brand.Error else Brand.Muted, style = MaterialTheme.typography.bodySmall)
            if (!b.done && !failed) {
                LinearProgressIndicator(progress = { b.percent / 100f }, color = Brand.Green, trackColor = Brand.SurfaceHigh, modifier = Modifier.fillMaxWidth())
            }
        }
        IconButton(onClick = onDelete) { Icon(Icons.Filled.Delete, "Delete download") }
    }
}

@OptIn(UnstableApi::class)
@Composable
private fun DownloadRow(e: DownloadEntry, onPlay: () -> Unit, onDelete: () -> Unit) {
    val context = LocalContext.current
    val video = e.meta.kind == DownloadMeta.VIDEO
    val art = when (e.meta.kind) {
        DownloadMeta.VIDEO -> ArtStore.existing(context, ArtStore.ITEM, e.meta.itemId)
        DownloadMeta.EPISODE -> ArtStore.existing(context, ArtStore.EPISODE, e.meta.episodeId)
        else -> ArtStore.existing(context, ArtStore.ALBUM, e.meta.albumId)
    }
    Row(
        Modifier.fillMaxWidth().clickable(enabled = e.done, onClick = onPlay).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(art?.toURI()?.toString(), Modifier.width(if (video) 56.dp else 48.dp).aspectRatio(if (video) 2f / 3f else 1f).clip(RoundedCornerShape(4.dp)))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(e.meta.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyLarge)
            if (e.meta.subtitle.isNotEmpty()) Text(e.meta.subtitle, maxLines = 1, color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
            Text(status(e), color = if (e.state == Download.STATE_FAILED) Brand.Error else Brand.Muted, style = MaterialTheme.typography.bodySmall)
            if (e.state == Download.STATE_DOWNLOADING) {
                LinearProgressIndicator(progress = { e.percent / 100f }, color = Brand.Green, trackColor = Brand.SurfaceHigh, modifier = Modifier.fillMaxWidth())
            }
        }
        IconButton(onClick = onDelete) { Icon(Icons.Filled.Delete, "Delete download") }
    }
}

@OptIn(UnstableApi::class)
private fun status(e: DownloadEntry): String = when (e.state) {
    Download.STATE_COMPLETED -> formatBytes(e.bytes)
    Download.STATE_DOWNLOADING -> "${e.percent.toInt()}% of ${formatBytes(e.total)}"
    Download.STATE_QUEUED -> "Waiting"
    Download.STATE_STOPPED -> "Paused"
    Download.STATE_FAILED -> "Failed. Delete it and try again."
    Download.STATE_REMOVING -> "Deleting"
    Download.STATE_RESTARTING -> "Restarting"
    else -> ""
}
