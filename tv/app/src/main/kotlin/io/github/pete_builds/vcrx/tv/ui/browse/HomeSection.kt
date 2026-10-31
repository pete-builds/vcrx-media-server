package io.github.pete_builds.vcrx.tv.ui.browse

import android.os.SystemClock
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.HomeRow
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.settings.HomeLayout
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.CardShape
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.ItemCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import kotlinx.coroutines.launch

class HomeViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<LoadState<List<HomeRow>>>(LoadState.Loading)
        private set
    private var loadedAt = 0L

    init {
        load()
    }

    /** [quiet] keeps the current rows on screen and on failure, for refreshes after playback. */
    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.home().filter { it.items.isNotEmpty() }).also {
                    loadedAt = SystemClock.elapsedRealtime()
                }
            } catch (e: ApiException) {
                if (quiet && state is LoadState.Ready) state else LoadState.Failed(e)
            }
        }
    }

    fun refreshIfStale() {
        if (state is LoadState.Ready && SystemClock.elapsedRealtime() - loadedAt > 3_000) load(quiet = true)
    }
}

@Composable
fun HomeSection(focus: BrowseFocus, onRetry: () -> Unit) {
    val vm = graphViewModel(key = HOME) { HomeViewModel(it) }
    // Settings > Home screen: hidden rows left out, the rest in the viewer's order.
    val homePrefs by LocalContext.current.graph.preferences.flow.collectAsState()
    // Continue Watching changes after playback, which happens in another activity.
    LifecycleResumeEffect(vm) {
        vm.refreshIfStale()
        onPauseOrDispose { }
    }
    LoadStateContent(vm.state, onRetry = { vm.load(); onRetry() }) { loaded ->
        val rows = HomeLayout.arrange(loaded, homePrefs.home) { it.id }
        if (rows.isEmpty() && loaded.isNotEmpty()) {
            EmptyView("Every row is hidden", "Show them again in Settings, Home screen.")
        } else if (rows.isEmpty()) {
            EmptyView(
                "Nothing here yet",
                "Add a library on the server and scan it, or ask the server admin to grant you one.",
            )
        } else {
            LazyColumn(
                contentPadding = PaddingValues(top = 24.dp, bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
            ) {
                itemsIndexed(rows, key = { _, row -> row.id }) { index, row ->
                    HomeRowView(row, focus, firstRow = index == 0)
                }
            }
        }
    }
}

private fun shapeFor(row: HomeRow): CardShape =
    if (row.id == "continue_watching" || row.id == "recently_added_episodes") CardShape.Wide else CardShape.Poster

@Composable
private fun HomeRowView(row: HomeRow, focus: BrowseFocus, firstRow: Boolean) {
    val actions = LocalAppActions.current
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Label(row.title, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 24.dp))
        LazyRow(
            modifier = Modifier.focusRestorer(),
            contentPadding = PaddingValues(horizontal = 24.dp),
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            itemsIndexed(row.items, key = { _, item -> item.id }) { index, item ->
                ItemCard(
                    item = item,
                    shape = shapeFor(row),
                    onClick = {
                        focus.restoreItemId = item.id
                        actions.openDetails(item.id)
                    },
                    focusModifier = itemFocus(focus, item.id, first = firstRow && index == 0),
                )
            }
        }
    }
}

/**
 * Attaches a requester to a card and takes focus when Browse wants it here: the
 * card that opened Details, or the first card after the rail handed focus over.
 */
@Composable
fun itemFocus(focus: BrowseFocus, itemId: Long, first: Boolean): Modifier {
    val requester = remember { FocusRequester() }
    val restoring = focus.restoreItemId == itemId
    val take = restoring || (first && focus.toContent && focus.restoreItemId == null)
    LaunchedEffect(take) {
        if (take) {
            runCatching { requester.requestFocus() }
            focus.toContent = false
            if (restoring) focus.restoreItemId = null
        }
    }
    return Modifier.focusRequester(requester)
}

/** Grid helper shared by Library and Search: the same card and focus rules as Home. */
@Composable
fun GridCard(item: Item, index: Int, focus: BrowseFocus) {
    val actions = LocalAppActions.current
    ItemCard(
        item = item,
        onClick = {
            focus.restoreItemId = item.id
            actions.openDetails(item.id)
        },
        // Grids size their own cells, so the card fills the cell.
        width = null,
        focusModifier = itemFocus(focus, item.id, first = index == 0),
    )
}
