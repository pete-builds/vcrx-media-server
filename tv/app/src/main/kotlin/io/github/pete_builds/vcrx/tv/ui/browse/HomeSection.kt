package io.github.pete_builds.vcrx.tv.ui.browse

import android.os.SystemClock
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
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
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class HomeViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<LoadState<List<HomeRow>>>(LoadState.Loading)
        private set
    private var loadedAt = 0L

    /** The hero's title: the day's featured one, then whichever card has focus. */
    var hero by mutableStateOf<Item?>(null)
        private set
    var heroFeatured by mutableStateOf(true)
        private set
    private var featuredId: Long? = null
    private val details = HashMap<Long, Item>()
    private var heroJob: Job? = null

    /**
     * Show [item] in the hero: its summary at once, its details (plot, rating,
     * next episode) after focus has rested a moment, so a fast scroll along a
     * row does not ask the server about every card it passes.
     */
    fun showInHero(item: Item, featured: Boolean = false) {
        heroFeatured = featured
        hero = details[item.id] ?: item
        heroJob?.cancel()
        if (details.containsKey(item.id)) return
        heroJob = viewModelScope.launch {
            if (!featured) delay(HERO_DETAIL_DELAY_MS)
            val full = try {
                graph.api.item(item.id)
            } catch (e: ApiException) {
                return@launch
            }
            details[item.id] = full
            if (hero?.id == item.id) hero = full
        }
    }

    private fun feature(rows: List<HomeRow>) {
        val pick = HomeRows.featured(rows, System.currentTimeMillis() / 86_400_000L)
        if (pick == null) {
            hero = null
            return
        }
        // A refresh after playback keeps whatever the hero was showing.
        if (pick.id == featuredId && hero != null) return
        featuredId = pick.id
        showInHero(pick, featured = true)
    }

    init {
        load()
    }

    /** [quiet] keeps the current rows on screen and on failure, for refreshes after playback. */
    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(HomeRows.toShow(graph.api.home())).also {
                    loadedAt = SystemClock.elapsedRealtime()
                    // Played since? Details come again, for resume points.
                    details.clear()
                    feature(it.value)
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
            val heroItem = vm.hero
            val heroFocus = remember { FocusRequester() }
            val heroMoreInfo = remember { FocusRequester() }
            Column(Modifier.fillMaxSize()) {
                if (heroItem != null) {
                    HomeHero(heroItem, vm.heroFeatured, heroFocus, heroMoreInfo, onMoreInfo = {
                        focus.restoreHero = true
                        focus.restoreItemId = null
                        focus.restoreRowId = null
                    })
                }
                LazyColumn(
                    Modifier.weight(1f),
                    contentPadding = PaddingValues(top = if (heroItem != null) 8.dp else 24.dp, bottom = 48.dp),
                    verticalArrangement = Arrangement.spacedBy(20.dp),
                ) {
                    itemsIndexed(rows, key = { _, row -> row.id }) { index, row ->
                        HomeRowView(row, focus, firstRow = index == 0 && heroItem == null, onFocused = { vm.showInHero(it) })
                    }
                }
            }
            // The rail handed over: the hero's Play takes focus, as the first
            // card did before Home had a hero. Back from Details: More info.
            val heroTakes = HomeRows.heroFocus(heroItem != null, focus.toContent, focus.restoreItemId, focus.restoreHero)
            LaunchedEffect(heroTakes) {
                val requester = when (heroTakes) {
                    HomeRows.HeroFocus.First -> heroFocus
                    HomeRows.HeroFocus.MoreInfo -> heroMoreInfo
                    null -> return@LaunchedEffect
                }
                runCatching { requester.requestFocus() }
                focus.toContent = false
                focus.restoreHero = false
            }
        }
    }
}

private fun shapeFor(row: HomeRow): CardShape = if (HomeRows.wide(row)) CardShape.Wide else CardShape.Poster

/** Home's cards, smaller than the grids' toward Plex's density: about 20 percent. */
private val HOME_POSTER_WIDTH = 116.dp
private val HOME_WIDE_WIDTH = 200.dp

/** How long focus rests on a card before the hero asks for its details. */
private const val HERO_DETAIL_DELAY_MS = 350L

@Composable
private fun HomeRowView(row: HomeRow, focus: BrowseFocus, firstRow: Boolean, onFocused: (Item) -> Unit) {
    val actions = LocalAppActions.current
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Label(row.title, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 24.dp))
        LazyRow(
            modifier = Modifier.focusRestorer(),
            contentPadding = PaddingValues(horizontal = 24.dp),
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            itemsIndexed(row.items, key = { _, item -> item.id }) { index, item ->
                val shape = shapeFor(row)
                ItemCard(
                    item = item,
                    shape = shape,
                    width = if (shape == CardShape.Poster) HOME_POSTER_WIDTH else HOME_WIDE_WIDTH,
                    showTitle = false,
                    onFocused = { onFocused(item) },
                    onClick = {
                        focus.restoreItemId = item.id
                        focus.restoreRowId = row.id
                        actions.openDetails(item.id)
                    },
                    focusModifier = itemFocus(focus, item.id, first = firstRow && index == 0, rowId = row.id),
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
fun itemFocus(focus: BrowseFocus, itemId: Long, first: Boolean, rowId: String? = null): Modifier {
    val requester = remember { FocusRequester() }
    val restoring = HomeRows.restores(focus.restoreItemId, focus.restoreRowId, itemId, rowId)
    val take = restoring || (first && focus.toContent && focus.restoreItemId == null)
    LaunchedEffect(take) {
        if (take) {
            runCatching { requester.requestFocus() }
            focus.toContent = false
            if (restoring) {
                focus.restoreItemId = null
                focus.restoreRowId = null
            }
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
            focus.restoreRowId = null
            actions.openDetails(item.id)
        },
        // Grids size their own cells, so the card fills the cell.
        width = null,
        focusModifier = itemFocus(focus, item.id, first = index == 0),
    )
}
