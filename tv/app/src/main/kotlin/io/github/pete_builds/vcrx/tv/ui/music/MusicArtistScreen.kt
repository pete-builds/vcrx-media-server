package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.MusicAlbum
import io.github.pete_builds.vcrx.tv.net.MusicArtistDetail
import io.github.pete_builds.vcrx.tv.net.albumCoverUrl
import io.github.pete_builds.vcrx.tv.net.artistImageUrl
import io.github.pete_builds.vcrx.tv.net.line
import io.github.pete_builds.vcrx.tv.net.musicArtist
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import kotlinx.coroutines.launch

class MusicArtistViewModel(private val graph: AppGraph, private val artistId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<MusicArtistDetail>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load() {
        state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.musicArtist(artistId))
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

/** An artist: Shuffle play, then rows of their albums, live shows and appearances. */
@Composable
fun MusicArtistScreen(artistId: Long) {
    val vm = graphViewModel(key = "music-artist:$artistId") { MusicArtistViewModel(it, artistId) }
    val context = LocalContext.current
    val graph = context.graph
    val nav = LocalMusicNav.current
    val scope = rememberCoroutineScope()
    val shuffleButton = remember { FocusRequester() }
    var error by remember { mutableStateOf<String?>(null) }

    LoadStateContent(vm.state, onRetry = vm::load) { artist ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = 48.dp, top = 40.dp, end = 32.dp, bottom = 48.dp)) {
            item {
                Row(horizontalArrangement = Arrangement.spacedBy(32.dp), verticalAlignment = Alignment.CenterVertically) {
                    SquareArt(if (artist.hasImage) graph.api.artistImageUrl(artist.id, 480) else null, artist.name, Modifier.width(180.dp), round = true)
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Label("Artist", color = Accent)
                        Text(artist.name, style = MaterialTheme.typography.headlineMedium)
                        Text(
                            "${artist.albumCount} albums · ${artist.trackCount} tracks",
                            style = MaterialTheme.typography.bodyMedium,
                            color = Gray,
                        )
                        error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent) }
                        BrandButton(
                            "Shuffle play",
                            onClick = {
                                scope.launch {
                                    try {
                                        MusicPlayer.get(graph, context).playArtist(artist.id)
                                        nav.openNowPlaying()
                                    } catch (e: ApiException) {
                                        error = e.message
                                    }
                                }
                            },
                            primary = true,
                            icon = MusicIcons.Shuffle,
                            modifier = Modifier.padding(top = 8.dp).focusRequester(shuffleButton),
                        )
                    }
                }
            }
            listOf("Albums" to artist.albums, "Live recordings" to artist.live, "Appears on" to artist.appearsOn).forEach { (label, albums) ->
                if (albums.isNotEmpty()) {
                    item(key = label) { AlbumRow(label, albums, withArtist = label == "Appears on") }
                }
            }
        }
        LaunchedEffect(artist.id) { runCatching { shuffleButton.requestFocus() } }
    }
}

@Composable
private fun AlbumRow(label: String, albums: List<MusicAlbum>, withArtist: Boolean) {
    val context = LocalContext.current
    val api = context.graph.api
    val nav = LocalMusicNav.current
    Column(Modifier.padding(top = 28.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Label(label, style = MaterialTheme.typography.labelLarge)
        LazyRow(horizontalArrangement = Arrangement.spacedBy(16.dp), contentPadding = PaddingValues(end = 48.dp)) {
            items(albums, key = { it.id }) { a ->
                MusicCard(
                    title = a.title,
                    subtitle = a.line(withArtist),
                    imageUrl = if (a.hasCover) api.albumCoverUrl(a.id, 320) else null,
                    badge = if (a.live) a.source ?: "Live" else null,
                    onClick = { nav.openAlbum(a.id) },
                    width = 170.dp,
                )
            }
        }
    }
}
