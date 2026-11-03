package io.github.pete_builds.vcrx.tv.ui.details

import android.os.SystemClock
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import androidx.compose.material.icons.filled.Close
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.border
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.gestures.BringIntoViewSpec
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import io.github.pete_builds.vcrx.tv.ui.components.revealWithMargin
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Version
import io.github.pete_builds.vcrx.tv.settings.ResumeMode
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.Artwork
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandChip
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.BrandIconButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.CardShape
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.PillCard
import io.github.pete_builds.vcrx.tv.ui.components.Reveal
import io.github.pete_builds.vcrx.tv.ui.components.SectionTitle
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Motion
import io.github.pete_builds.vcrx.tv.ui.theme.White
import kotlinx.coroutines.launch

class DetailsViewModel(private val graph: AppGraph, private val itemId: Long) : ViewModel() {
    /** Settings > Playback > Resume, which orders the play buttons. */
    val resumeMode: ResumeMode get() = graph.preferences.current.playback.resume

    var state by mutableStateOf<LoadState<Item>>(LoadState.Loading)
        private set
    var seasons by mutableStateOf<List<Item>>(emptyList())
        private set
    var seasonId by mutableStateOf<Long?>(null)
        private set
    var episodes by mutableStateOf<LoadState<List<Item>>?>(null)
        private set
    var versionId by mutableStateOf<Long?>(null)
    var actionError by mutableStateOf<String?>(null)
        private set
    private var loadedAt = 0L

    init {
        load()
    }

    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            try {
                val item = graph.api.item(itemId)
                when (item.kind) {
                    "show" -> {
                        seasons = graph.api.children(item.id)
                        val wanted = seasonId
                            ?: item.nextEpisode?.parentId?.takeIf { id -> seasons.any { it.id == id } }
                            ?: seasons.firstOrNull()?.id
                        if (wanted != null) loadEpisodes(wanted, quiet)
                    }
                    "season" -> loadEpisodes(item.id, quiet)
                }
                state = LoadState.Ready(item)
                loadedAt = SystemClock.elapsedRealtime()
            } catch (e: ApiException) {
                if (!quiet || state !is LoadState.Ready) state = LoadState.Failed(e)
            }
        }
    }

    fun refreshIfStale() {
        if (state is LoadState.Ready && SystemClock.elapsedRealtime() - loadedAt > 3_000) load(quiet = true)
    }

    fun selectSeason(id: Long) {
        if (id == seasonId) return
        viewModelScope.launch { loadEpisodes(id, quiet = false) }
    }

    private suspend fun loadEpisodes(id: Long, quiet: Boolean) {
        seasonId = id
        if (!quiet) episodes = LoadState.Loading
        episodes = try {
            LoadState.Ready(graph.api.children(id))
        } catch (e: ApiException) {
            if (quiet && episodes is LoadState.Ready) episodes else LoadState.Failed(e)
        }
    }

    /** Remove from Continue Watching: forgets the resume point. */
    fun forgetProgress(item: Item) {
        actionError = null
        viewModelScope.launch {
            try {
                graph.api.clearProgress(item.id)
                load(quiet = true)
            } catch (e: ApiException) {
                actionError = e.message
            }
        }
    }

    fun toggleWatched(item: Item) {
        actionError = null
        viewModelScope.launch {
            try {
                graph.api.setWatched(item.id, !(item.watch?.played ?: false))
                load(quiet = true)
            } catch (e: ApiException) {
                actionError = e.message
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DetailsScreen(itemId: Long) {
    val vm = graphViewModel(key = "details:$itemId") { DetailsViewModel(it, itemId) }
    // The resume position changes during playback, which runs in its own activity.
    LifecycleResumeEffect(vm) {
        vm.refreshIfStale()
        onPauseOrDispose { }
    }
    // The page scrolls only to show what is cut off, so the poster and title
    // stay on screen while Play has focus; Details hands the TV's own scrolling
    // back to the rows inside it.
    val rowScroll = LocalBringIntoViewSpec.current
    LoadStateContent(vm.state, onRetry = { vm.load() }) { item ->
        // A focused row stops a page margin short of the screen's bottom edge.
        val density = LocalDensity.current
        val reveal = remember(density) { revealWithMargin(with(density) { REVEAL_MARGIN.toPx() }) }
        CompositionLocalProvider(LocalBringIntoViewSpec provides reveal) { Details(item, vm, rowScroll) }
    }
}

@OptIn(ExperimentalFoundationApi::class, ExperimentalLayoutApi::class)
@Composable
private fun Details(item: Item, vm: DetailsViewModel, rowScroll: BringIntoViewSpec) {
    val primaryFocus = remember { FocusRequester() }
    var pickVersion by remember { mutableStateOf(false) }
    val isContainer = item.kind == "show" || item.kind == "season"
    val list = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val headerReveal = remember { BringIntoViewRequester() }
    ThemeMusic(item)

    // The rows inside the page keep the TV's own sideways scrolling.
    val keepRowScroll: @Composable (@Composable () -> Unit) -> Unit = { content ->
        CompositionLocalProvider(LocalBringIntoViewSpec provides rowScroll, content = content)
    }
    // Past the header, the backdrop dims so the rows below read on black.
    val scrolled by remember { derivedStateOf { list.firstVisibleItemIndex > 0 || list.firstVisibleItemScrollOffset > 120 } }
    Box(Modifier.fillMaxSize()) {
        Backdrop(item, dimmed = scrolled)
        // Only top and bottom padding on the list: the rows inside run the full
        // width with their own side padding, so a focused pill or card has room
        // to grow instead of being cut off at the list's inner edge.
        LazyColumn(
            state = list,
            contentPadding = PaddingValues(top = 48.dp, bottom = 96.dp),
            verticalArrangement = Arrangement.spacedBy(36.dp),
        ) {
            item(key = "header") {
                Row(
                    // Coming back up from the seasons or the cast: the whole header,
                    // not just its buttons. Focus also starts the list's own reveal of
                    // the focused button, which would cancel a scroll launched beside
                    // it; so ask for the header through the same reveal, wait for it
                    // to finish, then settle the list at its top.
                    Modifier.bringIntoViewRequester(headerReveal).padding(top = 24.dp).padding(horizontal = PAGE_EDGE).onFocusChanged { state ->
                        if (state.hasFocus && headerFits(list)) {
                            scope.launch {
                                headerReveal.bringIntoView()
                                list.animateScrollToItem(0)
                            }
                        }
                    },
                    horizontalArrangement = Arrangement.spacedBy(40.dp),
                ) {
                    val episode = item.kind == "episode"
                    Box(Modifier.width(if (episode) 352.dp else 200.dp).aspectRatio(if (episode) 16f / 9f else 2f / 3f)) {
                        if (episode) {
                            Artwork(item.id, if (item.hasThumb) "thumb" else "backdrop", item.hasThumb || item.hasBackdrop, item.title, 768)
                        } else {
                            Artwork(item.id, "poster", item.hasPoster, item.title, 480)
                        }
                    }
                    // In the web page's order: where it sits, the title, the facts in a
                    // line, genres, tagline, plot, its series, how far in, then the buttons.
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        if (episode && item.showTitle != null) {
                            Label(item.showTitle, style = MaterialTheme.typography.labelLarge, color = Gray)
                        }
                        if (episode && item.episodeNumber != null) {
                            val end = item.episodeNumberEnd?.let { "-$it" }.orEmpty()
                            Label("Season ${item.seasonNumber} · Episode ${item.episodeNumber}$end", style = MaterialTheme.typography.labelMedium, color = Accent, caps = true)
                        }
                        Text(item.title, style = MaterialTheme.typography.displaySmall, color = White, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        HeroChipRow(heroChips(item))
                        if (item.genres.isNotEmpty()) Label(item.genres.take(4).joinToString(" · "), style = MaterialTheme.typography.labelMedium, color = Gray)
                        item.tagline?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.titleMedium, color = Gray) }
                        item.overview?.takeIf { it.isNotBlank() }?.let {
                            Text(it, style = MaterialTheme.typography.bodyLarge, maxLines = 5, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 760.dp))
                        }
                        resumeBar(item)?.let { ResumeBarView(it) }
                        // Wraps: Resume, Play from the start, Version, Trailer and the icons do not fit on one line.
                        FlowRow(
                            Modifier.padding(top = 8.dp).focusRestorer(primaryFocus).focusGroup(),
                            horizontalArrangement = Arrangement.spacedBy(16.dp),
                            verticalArrangement = Arrangement.spacedBy(16.dp),
                        ) {
                            if (isContainer) {
                                ShowButtons(item, vm, primaryFocus)
                                TrailerButton(item)
                            } else {
                                PlayButtons(item, vm, primaryFocus)
                                if (item.versions.size > 1) {
                                    val chosen = item.versions.firstOrNull { it.id == vm.versionId } ?: item.versions.first()
                                    BrandButton("Version: ${versionLabel(chosen)}", onClick = { pickVersion = true })
                                }
                                TrailerButton(item)
                                WatchedButton(item.watch?.played == true) { vm.toggleWatched(item) }
                                if (resumeBar(item) != null) {
                                    BrandIconButton(
                                        Icons.Filled.Close,
                                        contentDescription = "Remove from Continue Watching",
                                        onClick = { vm.forgetProgress(item) },
                                    )
                                }
                            }
                        }
                        // A film series: open its other titles.
                        if (item.collections.isNotEmpty()) {
                            val actions = LocalAppActions.current
                            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                                item.collections.forEach { c ->
                                    BrandButton("Part of ${c.name}", onClick = { actions.openCollection(c.id, c.name) })
                                }
                            }
                        }
                        vm.actionError?.let { Text(it, style = MaterialTheme.typography.bodyLarge) }
                    }
                }
            }
            // The rest in the web page's order: seasons and episodes, extras, cast, then Details.
            if (isContainer && item.kind == "show" && vm.seasons.isNotEmpty()) {
                item(key = "seasons") {
                    // Down to the seasons lifts them to the top, so the episodes come
                    // into view: the chips alone are never cut off, so RevealOnly
                    // would leave the episode list below the screen.
                    Box(
                        Modifier.onFocusChanged { state ->
                            if (state.hasFocus) {
                                list.layoutInfo.visibleItemsInfo.firstOrNull { it.key == "seasons" }?.index?.let { i ->
                                    scope.launch { list.animateScrollToItem(i) }
                                }
                            }
                        },
                    ) { keepRowScroll { SeasonRow(vm) } }
                }
            }
            if (isContainer) {
                item(key = "episodes") { Box(Modifier.padding(horizontal = PAGE_EDGE)) { EpisodeList(vm) } }
            }
            if (item.extras.isNotEmpty()) {
                item(key = "extras") { keepRowScroll { ExtrasRow(item, PAGE_EDGE) } }
            }
            if (item.cast.isNotEmpty()) item(key = "cast") { keepRowScroll { CastRow(item) } }
            val facts = webFacts(item)
            if (facts.isNotEmpty()) item(key = "facts") { FactsList(facts) }
        }
    }

    LaunchedEffect(item.id) { runCatching { primaryFocus.requestFocus() } }

    if (pickVersion) {
        VersionPicker(item.versions, vm.versionId ?: item.versions.first().id, onPick = {
            vm.versionId = it
            pickVersion = false
        }, onDismiss = { pickVersion = false })
    }
}

@Composable
private fun PlayButtons(item: Item, vm: DetailsViewModel, primaryFocus: FocusRequester) {
    val actions = LocalAppActions.current
    val watch = item.watch
    val resumeMs = watch?.positionMs?.takeIf { it > 0 && !watch.played }
    if (resumeMs != null && vm.resumeMode == ResumeMode.StartOver) {
        // Settings say start over: that is the first button, and resuming is one step right.
        BrandButton(
            "Play from the start",
            onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = true) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
        BrandButton("Resume from ${formatTime(resumeMs)}", onClick = { actions.play(item.id, playTitle(item), vm.versionId, resume = true) })
    } else if (resumeMs != null) {
        BrandButton(
            "Resume from ${formatTime(resumeMs)}",
            onClick = { actions.play(item.id, playTitle(item), vm.versionId, resume = true) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
        BrandButton("Play from the start", onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = true) })
    } else {
        BrandButton(
            "Play",
            onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = true) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
    }
}

@Composable
private fun ShowButtons(item: Item, vm: DetailsViewModel, primaryFocus: FocusRequester) {
    val actions = LocalAppActions.current
    val next = item.nextEpisode
    if (next != null) {
        // A plain Play: Settings > Playback > Resume decides, and the player asks when set to Ask.
        val resuming = (next.watch?.positionMs ?: 0) > 0 && vm.resumeMode == ResumeMode.Resume
        BrandButton(
            "${if (resuming) "Resume" else "Play"} ${episodeCode(next) ?: next.title}",
            onClick = { actions.play(next.id, playTitle(next), null) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
        WatchedButton(item.watch?.played == true) { vm.toggleWatched(item) }
    } else {
        BrandButton(
            if (item.watch?.played == true) "Mark unwatched" else "Mark watched",
            onClick = { vm.toggleWatched(item) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
    }
}

/** How far the page's text and rows sit in from the screen's edges. */
private val PAGE_EDGE = 48.dp

/** The space a focused row keeps from the top and bottom of the screen. */
private val REVEAL_MARGIN = 48.dp

/** The hero's facts in a line, as the web's chips: plain text, boxed facts, then star scores. */
@Composable
private fun HeroChipRow(chips: List<HeroChip>) {
    if (chips.isEmpty()) return
    FlowRow(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        chips.forEach { c ->
            when (c.style) {
                ChipStyle.Plain -> Label(c.text, Modifier.align(Alignment.CenterVertically), style = MaterialTheme.typography.labelLarge, color = Gray)
                ChipStyle.Boxed -> Label(
                    c.text,
                    Modifier.align(Alignment.CenterVertically).border(1.dp, Color.White.copy(alpha = 0.28f), RoundedCornerShape(4.dp))
                        .padding(horizontal = 8.dp, vertical = 2.dp),
                    style = MaterialTheme.typography.labelMedium,
                    color = Gray,
                )
                ChipStyle.Score -> Row(Modifier.align(Alignment.CenterVertically), verticalAlignment = Alignment.CenterVertically) {
                    Text("★", style = MaterialTheme.typography.labelLarge, color = ScoreStar)
                    Text(" ${c.text}", style = MaterialTheme.typography.labelLarge, color = White)
                    Text(" ${c.source.orEmpty()}", style = MaterialTheme.typography.labelSmall, color = Gray)
                }
            }
        }
    }
}

/** The IMDb star's yellow, as on the web. */
private val ScoreStar = Color(0xFFF5C518)

/** How far in, above the buttons: a thin bar and the time left, as on the web. */
@Composable
private fun ResumeBarView(bar: ResumeBar) {
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.width(260.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Color.White.copy(alpha = 0.22f))) {
            Box(Modifier.fillMaxWidth(bar.fraction).fillMaxSize().background(Accent))
        }
        Label(bar.left, style = MaterialTheme.typography.labelMedium, color = Gray)
    }
}

/**
 * Details, row for row as the web's: a label and its value, one per line. It
 * takes focus, quietly, so the D-pad can reach it below the cast; text alone
 * cannot stop focus, and it would stay below the screen.
 */
@Composable
private fun FactsList(facts: List<Fact>) {
    Column(Modifier.padding(horizontal = PAGE_EDGE - 12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle("Details", modifier = Modifier.padding(start = 12.dp))
        FrameCard(onClick = {}, quiet = true, modifier = Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                facts.forEach { f ->
                    Row(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
                        Label(f.label, Modifier.width(180.dp), style = MaterialTheme.typography.labelMedium, color = Gray)
                        Text(f.value, style = MaterialTheme.typography.bodyMedium, color = White)
                    }
                }
            }
        }
    }
}

/** The cast in billing order, as small pills: a headshot, the name and the part. Pills take focus so the D-pad can reach the panel and the cast. */
@Composable
private fun CastRow(item: Item) {
    val api = LocalContext.current.graph.api
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle("Cast", modifier = Modifier.padding(start = PAGE_EDGE))
        LazyRow(
            Modifier.focusRestorer(),
            contentPadding = PaddingValues(horizontal = PAGE_EDGE, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            items(item.cast, key = { it.name }) { member ->
                PillCard(onClick = {}, modifier = Modifier.widthIn(max = 300.dp)) {
                    Row(
                        Modifier.padding(start = 5.dp, top = 5.dp, bottom = 5.dp, end = 18.dp),
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        val photo = member.personId?.takeIf { member.hasPhoto }?.let { api.castPhotoUrl(item.id, it) }
                        CastPhoto(member.name, photo)
                        Column {
                            Text(member.name, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            member.role?.takeIf { it.isNotBlank() }?.let {
                                Text(it, style = MaterialTheme.typography.labelSmall, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        }
    }
}

/**
 * A round headshot over the actor's initials, which show while it loads and when there is none.
 * TMDb headshots are 2:3 portraits: anchored at the top, the circle keeps the whole head
 * (hair, hats) and loses only the chin and shoulders.
 */
@Composable
private fun CastPhoto(name: String, url: String?) {
    Box(Modifier.size(40.dp).clip(CircleShape).background(Color.White.copy(alpha = 0.08f)), contentAlignment = Alignment.Center) {
        Text(castInitials(name), style = MaterialTheme.typography.labelMedium, color = Gray)
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                alignment = Alignment.TopCenter,
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

/** Up to two initials, from the first letters of the name's words. */
internal fun castInitials(name: String): String =
    name.split(Regex("\\s+")).filter { it.isNotEmpty() }.take(2).joinToString("") { it.take(1) }.uppercase()

@Composable
private fun SeasonRow(vm: DetailsViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle("Seasons", modifier = Modifier.padding(start = PAGE_EDGE))
        LazyRow(
            Modifier.focusRestorer(),
            contentPadding = PaddingValues(horizontal = PAGE_EDGE, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            items(vm.seasons, key = { it.id }) { season ->
                BrandChip(season.title, selected = season.id == vm.seasonId, onClick = { vm.selectSeason(season.id) })
            }
        }
    }
}

@Composable
private fun EpisodeList(vm: DetailsViewModel) {
    val actions = LocalAppActions.current
    val state = vm.episodes ?: return
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SectionTitle("Episodes")
        when (state) {
            LoadState.Loading -> LoadingView(Modifier.padding(24.dp))
            is LoadState.Failed -> Text(state.error.message ?: "Could not load episodes.", style = MaterialTheme.typography.bodyLarge)
            is LoadState.Ready -> if (state.value.isEmpty()) {
                EmptyView("No episodes", "This season has no episodes on the server.")
            } else {
                Column(Modifier.focusRestorer().focusGroup(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    state.value.forEach { episode ->
                        FrameCard(onClick = { actions.openDetails(episode.id) }, quiet = true, modifier = Modifier.fillMaxWidth()) {
                            Row(
                                Modifier.padding(10.dp),
                                horizontalArrangement = Arrangement.spacedBy(24.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Box(Modifier.width(240.dp).aspectRatio(CardShape.Wide.aspect)) {
                                    Artwork(episode.id, if (episode.hasThumb) "thumb" else "backdrop", episode.hasThumb || episode.hasBackdrop, episode.title, 480)
                                }
                                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                    Label(episodeLine(episode), style = MaterialTheme.typography.labelMedium, color = Gray)
                                    Text(episode.title, style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    episode.overview?.takeIf { it.isNotBlank() }?.let {
                                        Text(it, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun VersionPicker(versions: List<Version>, selected: Long, onPick: (Long) -> Unit, onDismiss: () -> Unit) {
    val first = remember { FocusRequester() }
    BrandDialog("Choose a version", onDismiss) {
        versions.forEachIndexed { index, version ->
            BrandRow(
                onClick = { onPick(version.id) },
                selected = version.id == selected,
                modifier = if (index == 0) Modifier.focusRequester(first) else Modifier,
            ) {
                Column {
                    Label(versionLabel(version))
                    Label(versionDetail(version), style = MaterialTheme.typography.labelSmall)
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

/** Mark watched beside Play: a tick in a circle, green once watched. */
@Composable
private fun WatchedButton(played: Boolean, onClick: () -> Unit) {
    BrandIconButton(
        Icons.Filled.Check,
        contentDescription = if (played) "Mark unwatched" else "Mark watched",
        onClick = onClick,
        on = played,
    )
}

/**
 * The title's backdrop across the screen behind the page, under a wash from the
 * left and the bottom so the text reads on any picture. An episode or season
 * borrows its show's. It dims once the page scrolls past the header.
 */
@Composable
private fun Backdrop(item: Item, dimmed: Boolean) {
    val api = LocalContext.current.graph.api
    val borrows = item.kind == "episode" || item.kind == "season"
    val artId = if (borrows) item.showId ?: item.id else item.id
    val url = if (item.hasBackdrop || borrows) api.artworkUrl(artId, "backdrop", 1280) else null
    val alpha by animateFloatAsState(if (dimmed) 0.25f else 1f, tween(Motion.SLOW, easing = Motion.Ease), label = "backdrop")
    Box(Modifier.fillMaxSize().graphicsLayer { this.alpha = alpha }) {
        if (url != null) {
            AsyncImage(model = url, contentDescription = null, contentScale = ContentScale.Crop, alignment = Alignment.TopCenter, modifier = Modifier.fillMaxSize())
        }
        Box(Modifier.fillMaxSize().background(Brush.horizontalGradient(0f to Black.copy(alpha = 0.94f), 0.5f to Black.copy(alpha = 0.72f), 1f to Black.copy(alpha = 0.25f))))
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.45f to Color.Transparent, 0.85f to Black)))
    }
}

/**
 * Whether the header fits on screen whole, so scrolling back to the top shows
 * all of it; one taller than the screen would push its buttons off the bottom.
 */
private fun headerFits(list: LazyListState): Boolean {
    val info = list.layoutInfo
    val header = info.visibleItemsInfo.firstOrNull { it.key == "header" } ?: return false
    return Reveal.fitsAtTop(header.size, info.beforeContentPadding, info.viewportSize.height)
}

private fun episodeLine(episode: Item): String = listOfNotNull(
    episodeCode(episode),
    episode.runtimeMs?.let { formatRuntime(it) },
    when {
        episode.watch?.played == true -> "Watched"
        (episode.watch?.positionMs ?: 0) > 0 -> "Resume at ${formatTime(episode.watch!!.positionMs)}"
        else -> null
    },
).joinToString("   ")

fun versionLabel(v: Version): String =
    v.label?.takeIf { it.isNotBlank() }
        ?: listOfNotNull(v.resolution, v.videoCodec?.uppercase(), v.hdrType?.uppercase()).joinToString(" ").ifEmpty { v.fileName }

private fun versionDetail(v: Version): String = listOfNotNull(
    v.container?.uppercase(),
    v.videoProfile,
    v.bitDepth?.let { "$it-bit" },
    v.audioTracks.firstOrNull()?.let { listOfNotNull(it.codec?.uppercase(), it.channels?.let { c -> "${c}ch" }).joinToString(" ") },
    "%.1f GB".format(v.size / 1_000_000_000.0),
).joinToString("   ")

/** What the player shows at the top: the title, with show and episode code for episodes. */
private fun playTitle(item: Item): String =
    if (item.kind == "episode") listOfNotNull(item.showTitle, episodeCode(item), item.title).joinToString("  ") else item.title

fun formatTime(ms: Long): String {
    val total = ms / 1000
    val h = total / 3600
    val m = (total % 3600) / 60
    val s = total % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, s) else "%d:%02d".format(m, s)
}

fun formatRuntime(ms: Long): String {
    val minutes = ms / 60_000
    return if (minutes >= 60) "${minutes / 60}h ${minutes % 60}m" else "${minutes}m"
}
