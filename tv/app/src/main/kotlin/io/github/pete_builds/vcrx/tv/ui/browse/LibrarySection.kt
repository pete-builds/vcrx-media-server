package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Library
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** Posters per row in library and search grids: two rows fit on a 16:9 screen. */
const val POSTER_COLUMNS = 5

enum class SortField(val api: String, val label: String, val defaultOrder: String) {
    Title("title", "Title", "asc"),
    Added("added", "Recently added", "desc"),
    Year("year", "Year", "desc"),
}

class LibraryViewModel(private val graph: AppGraph, private val libraryId: Long) : ViewModel() {
    var sort by mutableStateOf(SortField.Title)
        private set
    var order by mutableStateOf(SortField.Title.defaultOrder)
        private set
    var items by mutableStateOf<List<Item>>(emptyList())
        private set
    var total by mutableStateOf(0)
        private set
    var state by mutableStateOf<LoadState<Unit>>(LoadState.Loading)
        private set
    private var job: Job? = null

    init {
        reload()
    }

    fun chooseSort(field: SortField) {
        if (field == sort) {
            order = if (order == "asc") "desc" else "asc"
        } else {
            sort = field
            order = field.defaultOrder
        }
        reload()
    }

    fun reload() {
        job?.cancel()
        state = LoadState.Loading
        items = emptyList()
        job = viewModelScope.launch {
            state = try {
                val page = graph.api.libraryItems(libraryId, sort.api, order, PAGE, 0)
                items = page.items
                total = page.total
                LoadState.Ready(Unit)
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }

    fun loadMore() {
        if (job?.isActive == true || state !is LoadState.Ready || items.size >= total) return
        job = viewModelScope.launch {
            try {
                val page = graph.api.libraryItems(libraryId, sort.api, order, PAGE, items.size)
                items = items + page.items
                total = page.total
            } catch (_: ApiException) {
                // The next scroll retries; the items already shown stay usable.
            }
        }
    }

    private companion object {
        const val PAGE = 100
    }
}

private fun orderLabel(field: SortField, order: String): String = when (field) {
    SortField.Title -> if (order == "asc") "A to Z" else "Z to A"
    SortField.Added, SortField.Year -> if (order == "desc") "newest" else "oldest"
}

@Composable
fun LibrarySection(library: Library, focus: BrowseFocus) {
    val vm = graphViewModel(key = librarySection(library.id)) { LibraryViewModel(it, library.id) }
    val grid = rememberLazyGridState()

    Column(Modifier.padding(top = 24.dp)) {
        Row(
            Modifier.padding(horizontal = 24.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Label(library.name, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(end = 12.dp, top = 12.dp))
            SortField.entries.forEach { field ->
                val selected = field == vm.sort
                BrandButton(
                    text = if (selected) "${field.label}, ${orderLabel(field, vm.order)}" else field.label,
                    onClick = { vm.chooseSort(field) },
                    primary = selected,
                )
            }
        }
        LoadStateContent(vm.state, onRetry = vm::reload) {
            if (vm.items.isEmpty()) {
                EmptyView("This library is empty", "Scan it on the server, or check that its folder is mounted.")
            } else {
                LazyVerticalGrid(
                    state = grid,
                    columns = GridCells.Fixed(POSTER_COLUMNS),
                    contentPadding = PaddingValues(start = 24.dp, end = 24.dp, top = 20.dp, bottom = 48.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(20.dp),
                ) {
                    itemsIndexed(vm.items, key = { _, item -> item.id }) { index, item ->
                        GridCard(item, index, focus)
                    }
                }
                LaunchedEffect(grid, vm.items.size) {
                    snapshotFlow { grid.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }
                        .collect { last -> if (last >= vm.items.size - 24) vm.loadMore() }
                }
            }
        }
    }
}
