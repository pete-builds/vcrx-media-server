package io.github.pete_builds.vcrx.mobile.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
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
import io.github.pete_builds.vcrx.mobile.music.Opml
import io.github.pete_builds.vcrx.mobile.music.PodcastPlayback
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.PodcastEpisode
import io.github.pete_builds.vcrx.mobile.net.PodcastSearchResult
import io.github.pete_builds.vcrx.mobile.net.episodeArtworkUrl
import io.github.pete_builds.vcrx.mobile.net.podcastArtworkUrl
import io.github.pete_builds.vcrx.mobile.net.podcastExportOpml
import io.github.pete_builds.vcrx.mobile.net.podcastImportOpml
import io.github.pete_builds.vcrx.mobile.net.podcastNewEpisodes
import io.github.pete_builds.vcrx.mobile.net.podcastRefresh
import io.github.pete_builds.vcrx.mobile.net.podcastSearch
import io.github.pete_builds.vcrx.mobile.net.podcastSetPlayed
import io.github.pete_builds.vcrx.mobile.net.podcastShow
import io.github.pete_builds.vcrx.mobile.net.podcastSubscribe
import io.github.pete_builds.vcrx.mobile.net.podcastUnsubscribe
import io.github.pete_builds.vcrx.mobile.net.podcasts
import io.github.pete_builds.vcrx.mobile.offline.DownloadEligibility
import io.github.pete_builds.vcrx.mobile.offline.DownloadKeys
import java.text.DateFormat
import java.util.Date
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** "Nov 3, 2026", in the phone's own format. */
private fun formatDate(ms: Long?): String? = ms?.let { DateFormat.getDateInstance(DateFormat.MEDIUM).format(Date(it)) }

/** Podcasts: new episodes across every show, the shows subscribed to, and Add podcast. */
@Composable
fun PodcastsScreen(onShow: (Long) -> Unit, onAdd: () -> Unit, remote: MusicRemote?, onPlaying: () -> Unit) {
    val graph = LocalContext.current.graph
    var tab by rememberSaveable { mutableIntStateOf(0) }
    var reload by remember { mutableIntStateOf(0) }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            LazyRow(Modifier.weight(1f), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                items(listOf("New episodes", "Shows").withIndex().toList()) { (i, label) ->
                    FilterChip(
                        selected = tab == i,
                        onClick = { tab = i },
                        label = { Text(label) },
                        colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Brand.Tint),
                    )
                }
            }
            IconButton(onClick = onAdd) { Icon(Icons.Filled.Add, "Add a podcast") }
            OpmlMenu(onImported = { reload++ })
        }
        if (tab == 0) {
            Loadable("new$reload", { graph.api.podcastNewEpisodes() }) { episodes ->
                if (episodes.isEmpty()) return@Loadable Message("No new episodes in the last 30 days.")
                LazyColumn(Modifier.fillMaxSize()) {
                    items(episodes, key = { it.id }) { e -> EpisodeRow(e, remote, onPlaying, withShow = true, onChanged = { reload++ }) }
                }
            }
        } else {
            Loadable("shows$reload", { graph.api.podcasts() }) { shows ->
                if (shows.isEmpty()) return@Loadable Message("No podcasts yet. Tap + to add one by its feed's address.")
                LazyVerticalGrid(
                    columns = GridCells.Adaptive(150.dp),
                    contentPadding = PaddingValues(16.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp),
                    modifier = Modifier.fillMaxSize(),
                ) {
                    items(shows, key = { it.id }) { p ->
                        Column(Modifier.clickable { onShow(p.id) }) {
                            Art(if (p.hasArtwork) graph.api.podcastArtworkUrl(p.id) else null, Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(6.dp)))
                            Text(p.title, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                            val line = when {
                                p.newCount > 0 -> "${p.newCount} new"
                                p.unplayedCount > 0 -> "${p.unplayedCount} unplayed"
                                else -> p.author
                            }
                            Text(line, style = MaterialTheme.typography.bodySmall, color = if (p.newCount > 0) Brand.Green else Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        }
    }
}

/**
 * Import and Export OPML: move subscriptions from or to another podcast app.
 * Import picks a file with the system picker and sends it as it is; Export
 * saves the server's OPML where the listener chooses. Accounts only.
 */
@Composable
private fun OpmlMenu(onImported: () -> Unit) {
    val context = LocalContext.current
    val graph = context.graph
    val me by graph.me.collectAsState()
    if (!Opml.allowed(me, graph.tokens.isInvitedDevice)) return
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var outcome by remember { mutableStateOf<Pair<String, List<String>>?>(null) }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        busy = true
        scope.launch {
            outcome = try {
                val file = withContext(Dispatchers.IO) { context.contentResolver.openInputStream(uri)?.use(Opml::read) } ?: ByteArray(0)
                val problem = Opml.check(file)
                if (problem != null) {
                    problem to emptyList()
                } else {
                    val r = graph.api.podcastImportOpml(file)
                    onImported()
                    Opml.summary(r) to r.failed.map { "${it.url}: ${it.reason}" }
                }
            } catch (e: ApiException) {
                (e.message ?: "The import failed.") to emptyList()
            } catch (e: java.io.IOException) {
                "Cannot read that file: ${e.message}" to emptyList()
            } finally {
                busy = false
            }
        }
    }
    val save = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument(Opml.MEDIA_TYPE)) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        busy = true
        scope.launch {
            outcome = try {
                val file = graph.api.podcastExportOpml()
                withContext(Dispatchers.IO) { context.contentResolver.openOutputStream(uri)?.use { it.write(file) } }
                "Saved your podcasts as OPML." to emptyList()
            } catch (e: ApiException) {
                (e.message ?: "The export failed.") to emptyList()
            } catch (e: java.io.IOException) {
                "Cannot save there: ${e.message}" to emptyList()
            } finally {
                busy = false
            }
        }
    }
    Box {
        IconButton(onClick = { menu = true }, enabled = !busy) { Icon(Icons.Filled.MoreVert, "Import or export podcasts") }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(text = { Text("Import OPML") }, onClick = {
                menu = false
                pick.launch(Opml.PICKER_TYPES)
            })
            DropdownMenuItem(text = { Text("Export OPML") }, onClick = {
                menu = false
                save.launch(Opml.EXPORT_NAME)
            })
        }
    }
    outcome?.let { (summary, failed) ->
        AlertDialog(
            onDismissRequest = { outcome = null },
            title = { Text("Podcasts") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(summary)
                    failed.take(20).forEach { Text(it, color = Brand.Muted, style = MaterialTheme.typography.bodySmall) }
                    if (failed.size > 20) Text("And ${failed.size - 20} more.", color = Brand.Muted, style = MaterialTheme.typography.bodySmall)
                }
            },
            confirmButton = { QuietButton("OK", onClick = { outcome = null }) },
        )
    }
}

/** A show: its artwork and what it is, Refresh and Unsubscribe, and its episodes, newest first. */
@Composable
fun ShowScreen(podcastId: Long, remote: MusicRemote?, onPlaying: () -> Unit, onGone: () -> Unit) {
    val graph = LocalContext.current.graph
    val scope = rememberCoroutineScope()
    var reload by remember { mutableIntStateOf(0) }
    var confirm by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    Loadable(podcastId to reload, { graph.api.podcastShow(podcastId) }) { show ->
        val p = show.podcast
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Column(Modifier.fillMaxWidth().padding(16.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Art(if (p.hasArtwork) graph.api.podcastArtworkUrl(p.id) else null, Modifier.widthIn(max = 260.dp).fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(8.dp)))
                    Text(p.title, style = MaterialTheme.typography.headlineSmall)
                    if (p.author.isNotEmpty()) Text(p.author, color = Brand.Green)
                    FactLine(listOf("${show.total} episodes", "${p.unplayedCount} unplayed".takeIf { p.unplayedCount > 0 }, "Explicit".takeIf { p.explicit }))
                    p.lastError?.let { Text("The last refresh failed: $it", color = Brand.Error, style = MaterialTheme.typography.bodySmall) }
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        QuietButton("Refresh", onClick = {
                            scope.launch {
                                try {
                                    graph.api.podcastRefresh(p.id)
                                    reload++
                                } catch (e: ApiException) {
                                    error = e.message
                                }
                            }
                        })
                        QuietButton("Unsubscribe", onClick = { confirm = true })
                    }
                    error?.let { Text(it, color = Brand.Error) }
                    if (p.description.isNotBlank()) {
                        var open by remember { mutableStateOf(false) }
                        Text(
                            p.description,
                            color = Brand.Muted,
                            style = MaterialTheme.typography.bodyMedium,
                            maxLines = if (open) Int.MAX_VALUE else 4,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.clickable { open = !open },
                        )
                    }
                }
            }
            if (show.episodes.isEmpty()) item { Text("The feed lists no episodes yet.", color = Brand.Muted, modifier = Modifier.padding(16.dp)) }
            items(show.episodes, key = { it.id }) { e -> EpisodeRow(e, remote, onPlaying, withShow = false, onChanged = { reload++ }) }
        }
        if (confirm) {
            AlertDialog(
                onDismissRequest = { confirm = false },
                title = { Text("Unsubscribe?") },
                text = { Text("${p.title} leaves your podcasts, with your place in its episodes.") },
                confirmButton = {
                    BrandButton("Unsubscribe", onClick = {
                        confirm = false
                        scope.launch {
                            try {
                                graph.api.podcastUnsubscribe(p.id)
                                onGone()
                            } catch (e: ApiException) {
                                error = e.message
                            }
                        }
                    })
                },
                dismissButton = { QuietButton("Cancel", onClick = { confirm = false }) },
            )
        }
    }
}

/** One episode: tap to play from its place; the menu marks it played or keeps it on the phone. */
@Composable
fun EpisodeRow(e: PodcastEpisode, remote: MusicRemote?, onPlaying: () -> Unit, withShow: Boolean, onChanged: () -> Unit) {
    val graph = LocalContext.current.graph
    val me by graph.me.collectAsState()
    val downloads by graph.downloads.entries.collectAsState()
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    val download = downloads.firstOrNull { it.id == DownloadKeys.episode(e.id) }
    Row(
        Modifier.fillMaxWidth().clickable(enabled = remote != null) {
            remote?.playEpisode(e.id)
            onPlaying()
        }.padding(start = 16.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Art(graph.api.episodeArtworkUrl(e.id), Modifier.size(56.dp).clip(RoundedCornerShape(4.dp)))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            if (withShow && e.podcastTitle.isNotEmpty()) Text(e.podcastTitle, color = Brand.Muted, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(e.title, maxLines = 2, overflow = TextOverflow.Ellipsis, color = if (e.played) Brand.Muted else androidx.compose.ui.graphics.Color.White)
            val line = listOfNotNull("New".takeIf { e.new }, formatDate(e.publishedAt), PodcastPlayback.remaining(e)).joinToString(" · ")
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                if (download?.done == true) Icon(painterResource(R.drawable.ic_download), "Downloaded", tint = Brand.Green, modifier = Modifier.size(14.dp))
                Text(line, color = if (e.new) Brand.Green else Brand.Muted, style = MaterialTheme.typography.bodySmall)
            }
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, "More for this episode") }
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                DropdownMenuItem(
                    text = { Text(if (e.played) "Mark unplayed" else "Mark played") },
                    onClick = {
                        menu = false
                        scope.launch {
                            runCatching { graph.api.podcastSetPlayed(e.id, !e.played) }
                            onChanged()
                        }
                    },
                )
                if (DownloadEligibility.canDownloadEpisode(e.streamUrl, me, graph.tokens.isInvitedDevice)) {
                    when {
                        download == null -> DropdownMenuItem(text = { Text("Download") }, onClick = {
                            menu = false
                            graph.downloads.addEpisode(e)
                        })
                        else -> DropdownMenuItem(text = { Text(if (download.done) "Remove download" else "Cancel download") }, onClick = {
                            menu = false
                            graph.downloads.removeEpisode(e.id)
                        })
                    }
                }
            }
        }
    }
}

/**
 * Add a podcast: paste the feed's address, or, when the server searches a
 * podcast directory, search by name. The server fetches the feed before it
 * answers, so a bad address says why right here.
 */
@Composable
fun AddPodcastScreen(onAdded: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    val me by graph.me.collectAsState()
    val scope = rememberCoroutineScope()
    var url by rememberSaveable { mutableStateOf("") }
    var query by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var results by remember { mutableStateOf<List<PodcastSearchResult>?>(null) }
    fun subscribe(feed: String) {
        if (busy || feed.isBlank()) return
        busy = true
        error = null
        scope.launch {
            try {
                onAdded(graph.api.podcastSubscribe(feed.trim()).id)
            } catch (e: ApiException) {
                error = e.message
            } finally {
                busy = false
            }
        }
    }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item {
            Text("The show's RSS feed address, from its website or another podcast app.", color = Brand.Muted)
        }
        item {
            OutlinedTextField(url, { url = it }, label = { Text("Feed address") }, placeholder = { Text("https://example.com/feed.xml") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        }
        item { BrandButton(if (busy) "Adding" else "Subscribe", enabled = !busy && url.isNotBlank(), onClick = { subscribe(url) }) }
        error?.let { item { Text(it, color = Brand.Error) } }
        if (me?.podcastSearch == true) {
            item { RowHeader("Or search") }
            item {
                OutlinedTextField(
                    query,
                    { query = it },
                    leadingIcon = { Icon(Icons.Filled.Search, null) },
                    placeholder = { Text("A show's name") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            item {
                QuietButton("Search", enabled = query.isNotBlank() && !busy, onClick = {
                    scope.launch {
                        try {
                            results = graph.api.podcastSearch(query.trim())
                            error = null
                        } catch (e: ApiException) {
                            error = e.message
                        }
                    }
                })
            }
            results?.let { list ->
                if (list.isEmpty()) item { Text("No shows found.", color = Brand.Muted) }
                items(list, key = { it.feedUrl }) { r ->
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Column(Modifier.weight(1f)) {
                            Text(r.title, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            FactLine(listOf(r.author, r.genre, "Explicit".takeIf { r.explicit }))
                        }
                        QuietButton("Subscribe", enabled = !busy, onClick = { subscribe(r.feedUrl) })
                    }
                }
            }
        }
    }
}
