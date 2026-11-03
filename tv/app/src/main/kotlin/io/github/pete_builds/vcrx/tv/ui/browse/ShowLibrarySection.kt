package io.github.pete_builds.vcrx.tv.ui.browse

import android.os.SystemClock
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.CardShape
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch

/** How many shows each genre row asks for. */
private const val GENRE_ROW_ITEMS = 20

/**
 * A TV library's Recommended rows ([ShowRows]): one Home request, the library's
 * genre counts, then a request per leading genre, all at once. Kept per library
 * so the rows and the chosen mode survive a trip to Details.
 */
class ShowRowsViewModel(private val graph: AppGraph, private val libraryId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<List<HomeRow>>>(LoadState.Loading)
        private set

    /** Recommended (rows) or All shows (the grid). */
    var recommended by mutableStateOf(true)

    private var loadedAt = 0L

    init {
        load()
    }

    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                val rows = coroutineScope {
                    val home = async { graph.api.home() }
                    // Genre rows are extras: if they fail, the Home-derived rows still show.
                    val genreRows = try {
                        val genres = ShowRows.topGenres(graph.api.libraryFilters(libraryId).genres.map { it.value to it.count })
                        genres.map { name ->
                            async { name to graph.api.libraryItems(libraryId, "added", "desc", GENRE_ROW_ITEMS, 0, genre = name).items }
                        }.awaitAll()
                    } catch (_: ApiException) {
                        emptyList()
                    }
                    ShowRows.build(libraryId, home.await(), genreRows)
                }
                loadedAt = SystemClock.elapsedRealtime()
                LoadState.Ready(rows)
            } catch (e: ApiException) {
                if (quiet && state is LoadState.Ready) state else LoadState.Failed(e)
            }
        }
    }

    /** Continue Watching moves after playback, which happens in another activity. */
    fun refreshIfStale() {
        if (state is LoadState.Ready && SystemClock.elapsedRealtime() - loadedAt > 3_000) load(quiet = true)
    }
}

/**
 * A TV library: Recommended rows of show posters first (Continue Watching,
 * Recently Added, then more categories), or All shows, the full sortable grid,
 * which is still the only way to reach every show. The two buttons sit in one
 * header that never changes place, so switching keeps focus on the button.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun ShowLibrarySection(library: Library, focus: BrowseFocus) {
    val vm = graphViewModel(key = "showrows:${library.id}") { ShowRowsViewModel(it, library.id) }
    LifecycleResumeEffect(vm) {
        vm.refreshIfStale()
        onPauseOrDispose { }
    }
    Column(Modifier.padding(top = 24.dp)) {
        val grid = if (vm.recommended) null else libraryGridModel(library)
        LibraryHeader(library.name) {
            BrandButton("Recommended", onClick = { vm.recommended = true }, primary = vm.recommended)
            BrandButton("All shows", onClick = { vm.recommended = false }, primary = !vm.recommended)
            if (grid != null) SortButtons(grid)
        }
        if (grid != null) {
            LibraryGrid(grid, focus)
        } else {
            LoadStateContent(vm.state, onRetry = { vm.load() }) { rows ->
                if (rows.isEmpty()) {
                    EmptyView("Nothing to recommend yet", "Choose All shows to browse every show in this library.")
                } else {
                    val list = rememberLazyListState()
                    val scope = rememberCoroutineScope()
                    val rowScroll = LocalBringIntoViewSpec.current
                    CompositionLocalProvider(LocalBringIntoViewSpec provides RowsSnapThemselves) {
                        LazyColumn(
                            state = list,
                            contentPadding = PaddingValues(top = 20.dp, bottom = 48.dp),
                            verticalArrangement = Arrangement.spacedBy(20.dp),
                        ) {
                            itemsIndexed(rows, key = { _, row -> row.id }) { index, row ->
                                HomeRowView(
                                    row, focus, firstRow = index == 0, onFocused = { },
                                    rowScroll = rowScroll,
                                    shape = CardShape.Poster,
                                    modifier = Modifier.onFocusChanged { if (it.hasFocus) scope.launch { list.animateScrollToItem(index) } },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
