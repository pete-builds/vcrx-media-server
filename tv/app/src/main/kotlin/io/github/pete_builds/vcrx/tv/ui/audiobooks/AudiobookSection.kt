package io.github.pete_builds.vcrx.tv.ui.audiobooks

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
import io.github.pete_builds.vcrx.tv.music.AudiobookPlayer
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.AudiobookBook
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.net.audiobookCoverUrl
import io.github.pete_builds.vcrx.tv.net.audiobooks
import io.github.pete_builds.vcrx.tv.net.inProgress
import io.github.pete_builds.vcrx.tv.ui.browse.BrowseFocus
import io.github.pete_builds.vcrx.tv.ui.browse.itemFocus
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.music.MUSIC_COLUMNS
import io.github.pete_builds.vcrx.tv.ui.music.MusicCard
import io.github.pete_builds.vcrx.tv.ui.music.MusicIcons
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

enum class BookSort(val label: String, val key: String) { Title("Title", "title"), Author("Author", "author"), Recent("Recently listened", "recent") }

class AudiobookSectionViewModel(private val graph: AppGraph, private val libraryId: Long) : ViewModel() {
    var sort by mutableStateOf(BookSort.Title)
        private set
    var state by mutableStateOf<LoadState<List<AudiobookBook>>>(LoadState.Loading)
        private set
    private var job: Job? = null

    init {
        load()
    }

    fun choose(s: BookSort) {
        if (s == sort) return
        sort = s
        load()
    }

    fun load() {
        job?.cancel()
        state = LoadState.Loading
        job = viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.audiobooks(libraryId, sort.key).books)
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

// Book cards share Browse's focus memory with video cards, under ids that
// cannot clash with an item's or music's.
private fun bookKey(id: Long) = -(id + (1L shl 42))

/** "In progress" or "Finished" under a book's author, when either is so. */
private fun AudiobookBook.line(): String = listOfNotNull(
    author.ifEmpty { null },
    when {
        progress?.finished == true -> "Finished"
        progress.inProgress -> "In progress"
        else -> null
    },
).joinToString(" · ")

/** An audiobook library in Browse: its books as a grid of covers. */
@Composable
fun AudiobookSection(library: Library, focus: BrowseFocus) {
    val vm = graphViewModel(key = "audiobooks:${library.id}") { AudiobookSectionViewModel(it, library.id) }
    val context = LocalContext.current
    val api = context.graph.api
    val nav = LocalAudiobookNav.current
    val playing by AudiobookPlayer.get(context.graph, context).state.collectAsState()

    Column(Modifier.padding(top = 24.dp)) {
        Row(Modifier.padding(horizontal = 24.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Label(library.name, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(end = 12.dp, top = 12.dp))
            BookSort.entries.forEach { s -> BrandButton(text = s.label, onClick = { vm.choose(s) }, primary = s == vm.sort) }
            if (playing.book != null) BrandButton(text = "Now playing", onClick = nav::openPlaying, icon = MusicIcons.Next)
        }
        LoadStateContent(vm.state, onRetry = vm::load) { books ->
            if (books.isEmpty()) {
                EmptyView("This library is empty", "Scan it on the server, or check that its folder is mounted.")
            } else {
                LazyVerticalGrid(
                    columns = GridCells.Fixed(MUSIC_COLUMNS),
                    contentPadding = PaddingValues(start = 24.dp, end = 24.dp, top = 20.dp, bottom = 48.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(20.dp),
                ) {
                    itemsIndexed(books, key = { _, b -> "book:${b.id}" }) { index, b ->
                        MusicCard(
                            title = b.title,
                            subtitle = b.line(),
                            imageUrl = if (b.hasCover) api.audiobookCoverUrl(b.id, 320) else null,
                            onClick = {
                                focus.restoreItemId = bookKey(b.id)
                                nav.openBook(b.id)
                            },
                            focusModifier = itemFocus(focus, bookKey(b.id), first = index == 0),
                        )
                    }
                }
            }
        }
    }
}
