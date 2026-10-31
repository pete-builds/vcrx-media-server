package io.github.pete_builds.vcrx.tv.ui.details

import android.os.SystemClock
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.net.Version
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.Artwork
import io.github.pete_builds.vcrx.tv.ui.components.CardShape
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Orange
import kotlinx.coroutines.launch

class DetailsViewModel(private val graph: AppGraph, private val itemId: Long) : ViewModel() {
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

@Composable
fun DetailsScreen(itemId: Long) {
    val vm = graphViewModel(key = "details:$itemId") { DetailsViewModel(it, itemId) }
    // The resume position changes during playback, which runs in its own activity.
    LifecycleResumeEffect(vm) {
        vm.refreshIfStale()
        onPauseOrDispose { }
    }
    LoadStateContent(vm.state, onRetry = { vm.load() }) { item -> Details(item, vm) }
}

@Composable
private fun Details(item: Item, vm: DetailsViewModel) {
    val primaryFocus = remember { FocusRequester() }
    var pickVersion by remember { mutableStateOf(false) }
    val isContainer = item.kind == "show" || item.kind == "season"
    ThemeMusic(item)

    LazyColumn(contentPadding = PaddingValues(48.dp), verticalArrangement = Arrangement.spacedBy(32.dp)) {
        item(key = "header") {
            Row(horizontalArrangement = Arrangement.spacedBy(40.dp)) {
                val episode = item.kind == "episode"
                Box(Modifier.width(if (episode) 384.dp else 240.dp).aspectRatio(if (episode) 16f / 9f else 2f / 3f)) {
                    if (episode) {
                        Artwork(item.id, if (item.hasThumb) "thumb" else "backdrop", item.hasThumb || item.hasBackdrop, item.title, 768)
                    } else {
                        Artwork(item.id, "poster", item.hasPoster, item.title, 480)
                    }
                }
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Label(metaLine(item), style = MaterialTheme.typography.labelMedium, color = Gray)
                    if (episode && item.showTitle != null) {
                        Label("${item.showTitle}  ${episodeCode(item).orEmpty()}", style = MaterialTheme.typography.labelLarge)
                    }
                    Text(item.title.uppercase(), style = MaterialTheme.typography.displaySmall, color = Orange, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    item.tagline?.takeIf { it.isNotBlank() }?.let { Text(it, style = MaterialTheme.typography.titleMedium) }
                    item.overview?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodyLarge, maxLines = 6, overflow = TextOverflow.Ellipsis)
                    }
                    if (item.genres.isNotEmpty()) Label(item.genres.joinToString("  "), style = MaterialTheme.typography.labelSmall)
                    if (item.directors.isNotEmpty()) {
                        Label("Directed by ${item.directors.joinToString(", ")}", style = MaterialTheme.typography.labelSmall)
                    }
                    Row(
                        Modifier.padding(top = 12.dp).focusRestorer(primaryFocus).focusGroup(),
                        horizontalArrangement = Arrangement.spacedBy(16.dp),
                    ) {
                        if (isContainer) ShowButtons(item, vm, primaryFocus) else PlayButtons(item, vm, primaryFocus)
                        TrailerButton(item)
                    }
                    // On its own row: four buttons do not fit beside the poster.
                    if (!isContainer && item.versions.size > 1) {
                        val chosen = item.versions.firstOrNull { it.id == vm.versionId } ?: item.versions.first()
                        BrandButton("Version: ${versionLabel(chosen)}", onClick = { pickVersion = true })
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
        if (item.extras.isNotEmpty()) {
            item(key = "extras") { ExtrasRow(item) }
        }
        if (isContainer && item.kind == "show" && vm.seasons.isNotEmpty()) {
            item(key = "seasons") { SeasonRow(vm) }
        }
        if (isContainer) {
            item(key = "episodes") { EpisodeList(vm) }
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
    if (resumeMs != null) {
        BrandButton(
            "Resume ${formatTime(resumeMs)}",
            onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = false) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
        BrandButton("Play from start", onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = true) })
    } else {
        BrandButton(
            "Play",
            onClick = { actions.play(item.id, playTitle(item), vm.versionId, fromStart = true) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
    }
    BrandButton(if (watch?.played == true) "Mark unwatched" else "Mark watched", onClick = { vm.toggleWatched(item) })
}

@Composable
private fun ShowButtons(item: Item, vm: DetailsViewModel, primaryFocus: FocusRequester) {
    val actions = LocalAppActions.current
    val next = item.nextEpisode
    if (next != null) {
        val resuming = (next.watch?.positionMs ?: 0) > 0
        BrandButton(
            "${if (resuming) "Resume" else "Play"} ${episodeCode(next) ?: next.title}",
            onClick = { actions.play(next.id, playTitle(next), null, fromStart = false) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
        BrandButton(if (item.watch?.played == true) "Mark unwatched" else "Mark watched", onClick = { vm.toggleWatched(item) })
    } else {
        BrandButton(
            if (item.watch?.played == true) "Mark unwatched" else "Mark watched",
            onClick = { vm.toggleWatched(item) },
            primary = true,
            modifier = Modifier.focusRequester(primaryFocus),
        )
    }
}

@Composable
private fun SeasonRow(vm: DetailsViewModel) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Label("Seasons", style = MaterialTheme.typography.labelLarge)
        LazyRow(Modifier.focusRestorer(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            items(vm.seasons, key = { it.id }) { season ->
                BrandButton(
                    season.title,
                    onClick = { vm.selectSeason(season.id) },
                    primary = season.id == vm.seasonId,
                )
            }
        }
    }
}

@Composable
private fun EpisodeList(vm: DetailsViewModel) {
    val actions = LocalAppActions.current
    val state = vm.episodes ?: return
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Label("Episodes", style = MaterialTheme.typography.labelLarge)
        when (state) {
            LoadState.Loading -> LoadingView(Modifier.padding(24.dp))
            is LoadState.Failed -> Text(state.error.message ?: "Could not load episodes.", style = MaterialTheme.typography.bodyLarge)
            is LoadState.Ready -> if (state.value.isEmpty()) {
                EmptyView("No episodes", "This season has no episodes on the server.")
            } else {
                Column(Modifier.focusRestorer().focusGroup(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    state.value.forEach { episode ->
                        FrameCard(onClick = { actions.openDetails(episode.id) }, modifier = Modifier.fillMaxWidth()) {
                            Row(horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
                                Box(Modifier.width(240.dp).aspectRatio(CardShape.Wide.aspect)) {
                                    Artwork(episode.id, if (episode.hasThumb) "thumb" else "backdrop", episode.hasThumb || episode.hasBackdrop, episode.title, 480)
                                }
                                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                    Label(episodeLine(episode), style = MaterialTheme.typography.labelMedium)
                                    Text(episode.title, style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    episode.overview?.takeIf { it.isNotBlank() }?.let {
                                        Text(it, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
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

private fun metaLine(item: Item): String = listOfNotNull(
    item.kind.replaceFirstChar { it.uppercase() },
    item.year?.toString(),
    item.runtimeMs?.let { formatRuntime(it) },
    item.resolution,
    item.contentRating,
    item.ratings["imdb"]?.let { "IMDb %.1f".format(it) } ?: item.ratings["tmdb"]?.let { "TMDB %.1f".format(it) },
    if (item.watch?.played == true) "Watched" else null,
).joinToString("   ")

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
