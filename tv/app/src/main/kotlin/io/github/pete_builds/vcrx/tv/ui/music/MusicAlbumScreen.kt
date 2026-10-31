package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.MusicAlbumDetail
import io.github.pete_builds.vcrx.tv.net.albumCoverUrl
import io.github.pete_builds.vcrx.tv.net.musicAlbum
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import kotlinx.coroutines.launch

class MusicAlbumViewModel(private val graph: AppGraph, private val albumId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<MusicAlbumDetail>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load() {
        state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.musicAlbum(albumId))
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

/** An album: cover and facts on the left, the tracklist on the right. Choosing a track plays the album from there. */
@Composable
fun MusicAlbumScreen(albumId: Long) {
    val vm = graphViewModel(key = "music-album:$albumId") { MusicAlbumViewModel(it, albumId) }
    val context = LocalContext.current
    val graph = context.graph
    val player = remember { MusicPlayer.get(graph, context) }
    val playing by player.state.collectAsState()
    val nav = LocalMusicNav.current
    val scope = rememberCoroutineScope()
    val playButton = remember { FocusRequester() }
    var error by remember { mutableStateOf<String?>(null) }

    fun play(startTrackId: Long? = null, shuffle: Boolean = false) {
        scope.launch {
            try {
                player.playAlbum(albumId, startTrackId, shuffle)
                nav.openNowPlaying()
            } catch (e: ApiException) {
                error = e.message
            }
        }
    }

    LoadStateContent(vm.state, onRetry = vm::load) { album ->
        Row(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 32.dp), horizontalArrangement = Arrangement.spacedBy(40.dp)) {
            Column(Modifier.width(300.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                SquareArt(if (album.hasCover) graph.api.albumCoverUrl(album.id, 640) else null, album.title)
                Spacer(Modifier.height(10.dp))
                Label(if (album.live) "Live recording" else if (album.compilation) "Compilation" else "Album", color = Orange)
                Text(album.title, style = MaterialTheme.typography.headlineSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                Text(album.artistName, style = MaterialTheme.typography.titleMedium, color = Gray, maxLines = 1)
                val facts = listOfNotNull(
                    if (album.live) album.date else album.year?.toString(),
                    if (album.tracks.size == 1) "1 track" else "${album.tracks.size} tracks",
                    clock(album.durationMs).takeIf { album.durationMs > 0 },
                    album.genre,
                ).joinToString(" · ")
                Text(facts, style = MaterialTheme.typography.bodyMedium, color = Gray)
                if (album.live) {
                    listOfNotNull(
                        listOfNotNull(album.venue, album.location).joinToString(", ").ifEmpty { null },
                        album.sourceDetail ?: album.source,
                        album.taper?.let { "Taper: $it" },
                    ).forEach { Text(it, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                }
                error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Orange) }
                Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    BrandButton("Play", onClick = { play() }, primary = true, icon = Icons.Filled.PlayArrow, modifier = Modifier.focusRequester(playButton))
                    BrandButton("Shuffle", onClick = { play(shuffle = true) }, icon = MusicIcons.Shuffle)
                }
            }
            LazyColumn(contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                itemsIndexed(album.tracks, key = { _, t -> t.id }) { _, t ->
                    BrandRow(onClick = { play(startTrackId = t.id) }, selected = playing.current?.id == t.id) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text(t.trackNumber?.toString().orEmpty(), color = Gray, modifier = Modifier.width(40.dp))
                            Column(Modifier.weight(1f)) {
                                Text(t.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                if (t.artistName.isNotEmpty() && t.artistName != album.artistName) {
                                    Text(t.artistName, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 1)
                                }
                            }
                            Text(clock(t.durationMs), color = Gray, modifier = Modifier.padding(start = 16.dp))
                        }
                    }
                }
            }
        }
        LaunchedEffect(album.id) { runCatching { playButton.requestFocus() } }
    }
}
