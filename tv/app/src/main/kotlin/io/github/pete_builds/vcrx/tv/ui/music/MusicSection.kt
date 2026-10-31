package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.net.MusicAlbum
import io.github.pete_builds.vcrx.tv.net.MusicArtist
import io.github.pete_builds.vcrx.tv.net.albumCoverUrl
import io.github.pete_builds.vcrx.tv.net.artistImageUrl
import io.github.pete_builds.vcrx.tv.net.line
import io.github.pete_builds.vcrx.tv.net.musicAlbums
import io.github.pete_builds.vcrx.tv.net.musicArtists
import io.github.pete_builds.vcrx.tv.ui.browse.BrowseFocus
import io.github.pete_builds.vcrx.tv.ui.browse.itemFocus
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

enum class MusicTab(val label: String) { Artists("Artists"), Albums("Albums"), Live("Live") }

/** A music library's page in Browse: what one tab lists. */
data class MusicShelf(val artists: List<MusicArtist> = emptyList(), val albums: List<MusicAlbum> = emptyList())

class MusicSectionViewModel(private val graph: AppGraph, private val libraryId: Long) : ViewModel() {
    var tab by mutableStateOf(MusicTab.Artists)
        private set
    var state by mutableStateOf<LoadState<MusicShelf>>(LoadState.Loading)
        private set
    private var job: Job? = null

    init {
        load()
    }

    fun choose(t: MusicTab) {
        if (t == tab) return
        tab = t
        load()
    }

    fun load() {
        job?.cancel()
        state = LoadState.Loading
        job = viewModelScope.launch {
            state = try {
                LoadState.Ready(
                    when (tab) {
                        MusicTab.Artists -> MusicShelf(artists = graph.api.musicArtists(libraryId).artists)
                        MusicTab.Albums -> MusicShelf(albums = graph.api.musicAlbums(libraryId, "title", live = false).albums)
                        MusicTab.Live -> MusicShelf(albums = graph.api.musicAlbums(libraryId, "year", live = true).albums)
                    },
                )
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

// Music cards share Browse's focus memory with video cards, under ids that
// cannot clash with an item's.
private fun albumKey(id: Long) = -id
private fun artistKey(id: Long) = -(id + (1L shl 40))

/** A music library in Browse: artists, albums or live shows as a square grid. */
@Composable
fun MusicSection(library: Library, focus: BrowseFocus) {
    val vm = graphViewModel(key = "music:${library.id}") { MusicSectionViewModel(it, library.id) }
    val context = LocalContext.current
    val api = context.graph.api
    val nav = LocalMusicNav.current
    val playing by MusicPlayer.get(context.graph, context).state.collectAsState()

    Column(Modifier.padding(top = 24.dp)) {
        Row(Modifier.padding(horizontal = 24.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Label(library.name, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(end = 12.dp, top = 12.dp))
            MusicTab.entries.forEach { t ->
                BrandButton(text = t.label, onClick = { vm.choose(t) }, primary = t == vm.tab)
            }
            if (playing.current != null) {
                BrandButton(text = "Now playing", onClick = nav::openNowPlaying, icon = MusicIcons.Next)
            }
        }
        LoadStateContent(vm.state, onRetry = vm::load) { shelf ->
            if (shelf.artists.isEmpty() && shelf.albums.isEmpty()) {
                EmptyView(
                    if (vm.tab == MusicTab.Live) "No live recordings yet" else "This library is empty",
                    "Scan it on the server, or check that its folder is mounted.",
                )
            } else {
                LazyVerticalGrid(
                    columns = GridCells.Fixed(MUSIC_COLUMNS),
                    contentPadding = PaddingValues(start = 24.dp, end = 24.dp, top = 20.dp, bottom = 48.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(20.dp),
                ) {
                    itemsIndexed(shelf.artists, key = { _, a -> "artist:${a.id}" }) { index, a ->
                        MusicCard(
                            title = a.name,
                            subtitle = if (a.albumCount == 1) "1 album" else "${a.albumCount} albums",
                            imageUrl = if (a.hasImage) api.artistImageUrl(a.id, 320) else null,
                            round = true,
                            onClick = {
                                focus.restoreItemId = artistKey(a.id)
                                nav.openArtist(a.id)
                            },
                            focusModifier = itemFocus(focus, artistKey(a.id), first = index == 0),
                        )
                    }
                    itemsIndexed(shelf.albums, key = { _, a -> "album:${a.id}" }) { index, a ->
                        MusicCard(
                            title = a.title,
                            subtitle = a.line(),
                            imageUrl = if (a.hasCover) api.albumCoverUrl(a.id, 320) else null,
                            badge = if (a.live) a.source ?: "Live" else null,
                            onClick = {
                                focus.restoreItemId = albumKey(a.id)
                                nav.openAlbum(a.id)
                            },
                            focusModifier = itemFocus(focus, albumKey(a.id), first = index == 0),
                        )
                    }
                }
            }
        }
    }
}
