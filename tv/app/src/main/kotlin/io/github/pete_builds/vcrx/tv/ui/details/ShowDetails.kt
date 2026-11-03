package io.github.pete_builds.vcrx.tv.ui.details

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.Artwork
import io.github.pete_builds.vcrx.tv.ui.components.BrandChip
import io.github.pete_builds.vcrx.tv.ui.components.BrandIconButton
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.White
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** The kinds that get the show layout below rather than a movie's. */
internal val SHOW_KINDS = setOf("show", "season", "episode")

/**
 * A show, season or episode, laid out as Plex does on Google TV (Pete's Spin
 * City screen, 2026-11-03): the seasons as pills along the top; the show's name,
 * then the focused episode's title, facts, plot and director; a sideways strip
 * of the season's episodes as wide stills; the episode's buttons under it; then
 * Cast & Crew. Moving along the strip changes what the header says.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun ShowDetails(item: Item, vm: DetailsViewModel, rowScroll: BringIntoViewSpec) {
    ThemeMusic(item)
    val list = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val stripReveal = remember { BringIntoViewRequester() }
    val episodes = (vm.episodes as? LoadState.Ready)?.value.orEmpty()
    val focusedId = vm.focusedId?.takeIf { id -> episodes.any { it.id == id } } ?: startEpisode(item, episodes)?.id
    // The header follows focus along the strip; the full details (date, director,
    // guest cast) come a moment later, once focus has settled on one episode.
    LaunchedEffect(focusedId) {
        if (focusedId == null) return@LaunchedEffect
        delay(DETAIL_SETTLE_MS)
        vm.loadEpisodeDetail(focusedId)
    }
    val summary = episodes.firstOrNull { it.id == focusedId }
    val episode = focusedId?.let { vm.episodeDetails[it] } ?: summary
    val showTitle = if (item.kind == "show") item.title else item.showTitle ?: item.title
    val keepRowScroll: @Composable (@Composable () -> Unit) -> Unit = { content ->
        CompositionLocalProvider(LocalBringIntoViewSpec provides rowScroll, content = content)
    }
    val scrolled by remember { derivedStateOf { list.firstVisibleItemIndex > 2 } }

    Box(Modifier.fillMaxSize()) {
        Backdrop(item, dimmed = scrolled)
        LazyColumn(
            state = list,
            contentPadding = PaddingValues(top = 24.dp, bottom = 96.dp),
            verticalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            if (vm.seasons.size > 1) item(key = "seasons") { keepRowScroll { SeasonPills(vm) } }
            item(key = "header") { EpisodeHeader(showTitle, episode, item) }
            item(key = "strip") {
                // Back up to the strip from below: the whole top of the page again, header included.
                Box(
                    Modifier.bringIntoViewRequester(stripReveal).onFocusChanged { state ->
                        if (state.hasFocus && (list.firstVisibleItemIndex > 0 || list.firstVisibleItemScrollOffset > 0)) {
                            scope.launch {
                                stripReveal.bringIntoView()
                                list.animateScrollToItem(0)
                            }
                        }
                    },
                ) { keepRowScroll { EpisodeStrip(vm, episodes, focusedId) } }
            }
            if (episode != null) item(key = "actions") { EpisodeActions(episode, vm) }
            if (item.extras.isNotEmpty()) item(key = "extras") { keepRowScroll { ExtrasRow(item, PAGE_EDGE) } }
            // The focused episode's cast (its guests, then the show's) once known, else the show's.
            val people = episode?.takeIf { it.cast.isNotEmpty() || it.crew.isNotEmpty() } ?: item
            if (people.cast.isNotEmpty() || people.crew.isNotEmpty()) item(key = "cast") { keepRowScroll { CastRow(people) } }
            val facts = episode?.let { webFacts(it) }.orEmpty()
            if (facts.isNotEmpty()) item(key = "facts") { FactsList(facts) }
        }
    }
}

/** How long focus rests on an episode before its full details are fetched. */
private const val DETAIL_SETTLE_MS = 250L

/**
 * The episode the strip starts on: the page's own episode, else the one Play
 * would start (the show's next episode), else the first unwatched, else the first.
 */
internal fun startEpisode(item: Item, episodes: List<Item>): Item? =
    episodes.firstOrNull { it.id == item.id }
        ?: episodes.firstOrNull { it.id == item.nextEpisode?.id }
        ?: episodes.firstOrNull { it.watch?.played != true }
        ?: episodes.firstOrNull()

/** The episode's facts in a line, as Plex: "S4 · E3", the air date, then the usual chips (length, rating, scores). */
internal fun episodeChips(episode: Item): List<HeroChip> {
    val code = episode.seasonNumber?.let { s -> episode.episodeNumber?.let { e -> "S$s · E$e" } }
    val aired = airDate(episode.premiereDate)
    val rest = heroChips(episode).filterNot { aired != null && it.text == episode.year?.toString() }
    return listOfNotNull(code?.let { HeroChip(it, ChipStyle.Plain) }, aired?.let { HeroChip(it, ChipStyle.Plain) }) + rest
}

/** "Oct 5, 1999" for "1999-10-05", short as Plex writes it; null for anything else. */
internal fun airDate(ymd: String?): String? = try {
    ymd?.let { java.time.LocalDate.parse(it).format(java.time.format.DateTimeFormatter.ofPattern("MMM d, yyyy", java.util.Locale.ENGLISH)) }
} catch (_: java.time.format.DateTimeParseException) {
    null
}

@Composable
private fun SeasonPills(vm: DetailsViewModel) {
    // A dark track with the seasons in it, the chosen one lit, as Plex's.
    Box(Modifier.padding(horizontal = PAGE_EDGE)) {
        LazyRow(
            Modifier.clip(RoundedCornerShape(50)).background(Color.Black.copy(alpha = 0.35f)).focusRestorer(),
            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            itemsIndexed(vm.seasons, key = { _, s -> s.id }) { _, season ->
                BrandChip(season.title, selected = season.id == vm.seasonId, onClick = {
                    vm.focusedId = null
                    vm.selectSeason(season.id)
                })
            }
        }
    }
}

/** The text above the strip. A fixed height, so moving between episodes never moves the strip. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun EpisodeHeader(showTitle: String, episode: Item?, page: Item) {
    Column(
        Modifier.padding(horizontal = PAGE_EDGE).height(HEADER_HEIGHT).widthIn(max = HEADER_TEXT_WIDTH),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(showTitle, style = MaterialTheme.typography.displaySmall, color = White, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (episode == null) {
            HeroChipRow(heroChips(page))
            page.overview?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodyLarge, maxLines = 4, overflow = TextOverflow.Ellipsis)
            }
            return@Column
        }
        Text(episode.title, style = MaterialTheme.typography.titleLarge, color = White, maxLines = 1, overflow = TextOverflow.Ellipsis)
        HeroChipRow(episodeChips(episode))
        episode.overview?.takeIf { it.isNotBlank() }?.let {
            Text(it, style = MaterialTheme.typography.bodyLarge, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
        directedBy(episode)?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis) }
    }
}

/** Show name, episode title, facts, three lines of plot and the director, without the strip moving. */
private val HEADER_HEIGHT = 232.dp
private val HEADER_TEXT_WIDTH = 560.dp

/**
 * The season's episodes as wide stills in a row: the number in a corner, a bar
 * for how far in, a tick once watched. Focus picks the episode the header
 * shows; select plays it (the player asks or resumes, per Settings).
 */
@Composable
private fun EpisodeStrip(vm: DetailsViewModel, episodes: List<Item>, focusedId: Long?) {
    val actions = LocalAppActions.current
    when (val state = vm.episodes) {
        null -> return
        LoadState.Loading -> { LoadingView(Modifier.padding(start = PAGE_EDGE).height(EPISODE_WIDTH * 9 / 16)); return }
        is LoadState.Failed -> { Text(state.error.message ?: "Could not load episodes.", Modifier.padding(start = PAGE_EDGE)); return }
        is LoadState.Ready -> if (episodes.isEmpty()) {
            Label("No episodes of this season on the server.", Modifier.padding(start = PAGE_EDGE), color = Gray)
            return
        }
    }
    val strip = rememberLazyListState()
    val start = remember { FocusRequester() }
    val startIndex = episodes.indexOfFirst { it.id == focusedId }.coerceAtLeast(0)
    // Opening the page, or another season: the strip starts on its episode. Focus
    // goes there only when the page opens; picking a season leaves it on the pill.
    LaunchedEffect(vm.seasonId, episodes.size) {
        strip.scrollToItem(startIndex)
        if (!vm.stripTookFocus) {
            delay(50)
            if (runCatching { start.requestFocus() }.isSuccess) vm.stripTookFocus = true
        }
    }
    LazyRow(
        Modifier.focusRestorer(start).focusGroup(),
        state = strip,
        contentPadding = PaddingValues(horizontal = PAGE_EDGE, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        itemsIndexed(episodes, key = { _, e -> e.id }) { index, ep ->
            FrameCard(
                onClick = { actions.play(ep.id, playTitle(ep), null) },
                modifier = Modifier.width(EPISODE_WIDTH)
                    .then(if (index == startIndex) Modifier.focusRequester(start) else Modifier)
                    .onFocusChanged { if (it.isFocused) vm.focusedId = ep.id },
            ) {
                Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f)) {
                    Artwork(ep.id, if (ep.hasThumb) "thumb" else "backdrop", ep.hasThumb || ep.hasBackdrop, ep.title, 480)
                    ep.episodeNumber?.let { n ->
                        Label(
                            "E$n",
                            Modifier.align(Alignment.TopEnd).padding(6.dp)
                                .background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(6.dp)).padding(horizontal = 6.dp, vertical = 2.dp),
                            style = MaterialTheme.typography.labelMedium,
                            color = White,
                        )
                    }
                    if (ep.watch?.played == true) {
                        Label(
                            "✓",
                            Modifier.align(Alignment.TopStart).padding(6.dp)
                                .background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(6.dp)).padding(horizontal = 6.dp, vertical = 2.dp),
                            style = MaterialTheme.typography.labelMedium,
                            color = Accent,
                        )
                    }
                    episodeProgress(ep)?.let { fraction ->
                        Box(
                            Modifier.align(Alignment.BottomCenter).padding(horizontal = 10.dp, vertical = 8.dp).fillMaxWidth().height(4.dp)
                                .clip(RoundedCornerShape(2.dp)).background(Color.White.copy(alpha = 0.3f)),
                        ) {
                            Box(Modifier.fillMaxWidth(fraction).height(4.dp).background(Accent))
                        }
                    }
                }
            }
        }
    }
}

/** A quarter of the screen and a bit: three and a half stills across, as Plex. */
private val EPISODE_WIDTH = 256.dp

/** How far into an episode someone is, 0..1, for the bar on its still; null when not started or finished. */
internal fun episodeProgress(ep: Item): Float? {
    val w = ep.watch ?: return null
    if (w.played || w.positionMs <= 0) return null
    val total = w.durationMs ?: ep.runtimeMs ?: return null
    if (total <= 0) return null
    return (w.positionMs.toFloat() / total).coerceIn(0.02f, 1f)
}

/** Under the strip, for the focused episode: Play (or Resume), Mark watched, Remove from Continue Watching. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun EpisodeActions(episode: Item, vm: DetailsViewModel) {
    val primary = remember { FocusRequester() }
    FlowRow(
        Modifier.padding(horizontal = PAGE_EDGE).focusRestorer(primary).focusGroup(),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        PlayButtons(episode, vm, primary)
        WatchedButton(episode.watch?.played == true) { vm.toggleWatched(episode) }
        if (resumeBar(episode) != null || episodeProgress(episode) != null) {
            BrandIconButton(Icons.Filled.Close, contentDescription = "Remove from Continue Watching", onClick = { vm.forgetProgress(episode) })
        }
        vm.actionError?.let { Text(it, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.align(Alignment.CenterVertically)) }
    }
}
