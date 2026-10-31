package io.github.pete_builds.vcrx.tv.ui.collection

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.CollectionResponse
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.browse.POSTER_COLUMNS
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.ItemCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import kotlinx.coroutines.launch

class CollectionViewModel(private val graph: AppGraph, private val collectionId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<CollectionResponse>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load() {
        state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.collection(collectionId))
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }
}

/** A collection, such as a film series, as a poster grid in its own order (release order for a series). */
@Composable
fun CollectionScreen(collectionId: Long, name: String) {
    val vm = graphViewModel(key = "collection:$collectionId") { CollectionViewModel(it, collectionId) }
    val actions = LocalAppActions.current
    val first = remember { FocusRequester() }

    Column(Modifier.fillMaxSize().padding(top = 36.dp)) {
        Column(Modifier.padding(horizontal = 48.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Label("Collection", style = MaterialTheme.typography.labelMedium, color = Gray)
            Text(name.uppercase(), style = MaterialTheme.typography.headlineMedium, color = Orange)
        }
        LoadStateContent(vm.state, onRetry = vm::load) { data ->
            if (data.items.isEmpty()) {
                EmptyView("Nothing here yet", "None of this collection's titles are in a library you can see.")
            } else {
                LazyVerticalGrid(
                    columns = GridCells.Fixed(POSTER_COLUMNS),
                    contentPadding = PaddingValues(start = 48.dp, end = 48.dp, top = 24.dp, bottom = 48.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(20.dp),
                ) {
                    items(data.items, key = { it.id }) { item ->
                        ItemCard(
                            item = item,
                            onClick = { actions.openDetails(item.id) },
                            width = null,
                            focusModifier = if (item.id == data.items.first().id) Modifier.focusRequester(first) else Modifier,
                        )
                    }
                }
                LaunchedEffect(data.collection.id) { runCatching { first.requestFocus() } }
            }
        }
    }
}
