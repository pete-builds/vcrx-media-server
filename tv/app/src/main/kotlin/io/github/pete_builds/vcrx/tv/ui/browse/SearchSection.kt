package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.components.TvTextField
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class SearchViewModel(private val graph: AppGraph) : ViewModel() {
    var query by mutableStateOf("")
        private set
    var results by mutableStateOf<LoadState<List<Item>>?>(null)
        private set
    private var job: Job? = null

    /** Searches as the user types, after a short pause so each keystroke is not a request. */
    fun onQuery(text: String, immediate: Boolean = false) {
        query = text
        job?.cancel()
        val q = text.trim()
        if (q.isEmpty()) {
            results = null
            return
        }
        job = viewModelScope.launch {
            if (!immediate) delay(350)
            results = LoadState.Loading
            results = try {
                LoadState.Ready(graph.api.search(q))
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

@Composable
fun SearchSection(focus: BrowseFocus) {
    val vm = graphViewModel(key = SEARCH) { SearchViewModel(it) }
    val fieldFocus = remember { FocusRequester() }

    Column(Modifier.padding(top = 24.dp)) {
        TvTextField(
            value = vm.query,
            onValueChange = { vm.onQuery(it) },
            label = "Search movies, shows and episodes",
            imeAction = ImeAction.Search,
            onImeAction = { vm.onQuery(vm.query, immediate = true) },
            modifier = Modifier.padding(horizontal = 24.dp).width(560.dp).focusRequester(fieldFocus),
        )
        val results = vm.results
        Box(Modifier.padding(top = 8.dp)) {
            when {
                results == null -> Label(
                    "Type a title",
                    style = MaterialTheme.typography.labelMedium,
                    modifier = Modifier.padding(24.dp),
                )
                else -> LoadStateContent(results, onRetry = { vm.onQuery(vm.query, immediate = true) }) { items ->
                    if (items.isEmpty()) {
                        EmptyView("No matches", "Nothing on this server matches \"${vm.query.trim()}\".")
                    } else {
                        LazyVerticalGrid(
                            columns = GridCells.Fixed(POSTER_COLUMNS),
                            contentPadding = PaddingValues(start = 24.dp, end = 24.dp, top = 16.dp, bottom = 48.dp),
                            horizontalArrangement = Arrangement.spacedBy(16.dp),
                            verticalArrangement = Arrangement.spacedBy(20.dp),
                        ) {
                            itemsIndexed(items, key = { _, item -> item.id }) { index, item ->
                                // Only a returning card takes focus here; the field keeps it while typing.
                                GridCard(item, index = if (focus.restoreItemId == item.id) 0 else -1, focus = focus)
                            }
                        }
                    }
                }
            }
        }
    }
    LaunchedEffect(Unit) {
        if (focus.toContent && focus.restoreItemId == null) {
            runCatching { fieldFocus.requestFocus() }
            focus.toContent = false
        }
    }
}
