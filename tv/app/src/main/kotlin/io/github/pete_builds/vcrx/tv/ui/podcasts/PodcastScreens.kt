package io.github.pete_builds.vcrx.tv.ui.podcasts

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.music.MusicState
import io.github.pete_builds.vcrx.tv.music.PodcastPlayback
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Podcast
import io.github.pete_builds.vcrx.tv.net.PodcastEpisode
import io.github.pete_builds.vcrx.tv.net.PodcastShowResponse
import io.github.pete_builds.vcrx.tv.net.episodeArtworkUrl
import io.github.pete_builds.vcrx.tv.net.podcastArtworkUrl
import io.github.pete_builds.vcrx.tv.net.podcastSetPlayed
import io.github.pete_builds.vcrx.tv.net.podcastShow
import io.github.pete_builds.vcrx.tv.net.podcasts
import io.github.pete_builds.vcrx.tv.ui.browse.BrowseFocus
import io.github.pete_builds.vcrx.tv.ui.browse.itemFocus
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.music.LocalMusicNav
import io.github.pete_builds.vcrx.tv.ui.music.MUSIC_COLUMNS
import io.github.pete_builds.vcrx.tv.ui.music.MusicCard
import io.github.pete_builds.vcrx.tv.ui.music.SquareArt
import io.github.pete_builds.vcrx.tv.ui.music.clock
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import kotlinx.coroutines.launch

class PodcastsViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<LoadState<List<Podcast>>>(LoadState.Loading)
        private set

    init {
        load()
    }

    /** [quiet] keeps the grid on screen, for the refresh when the section comes back. */
    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.podcasts())
            } catch (e: ApiException) {
                if (quiet && state is LoadState.Ready) state else LoadState.Failed(e)
            }
        }
    }
}

// Podcast cards share Browse's focus memory with the others, under ids that
// cannot clash with an item's, an album's or an artist's.
private fun podcastKey(id: Long) = -(id + (2L shl 40))

/**
 * Podcasts in the Browse rail: the shows this user subscribes to, with how
 * many new episodes each has. The TV does not subscribe: typing a feed URL
 * with a remote is no way to spend an evening, so that is the web's and the phone's.
 */
@Composable
fun PodcastsSection(focus: BrowseFocus) {
    val vm = graphViewModel(key = "podcasts") { PodcastsViewModel(it) }
    val api = LocalContext.current.graph.api
    val nav = LocalMusicNav.current
    LifecycleResumeEffect(vm) {
        vm.load(quiet = true)
        onPauseOrDispose { }
    }
    LoadStateContent(vm.state, onRetry = { vm.load() }) { shows ->
        if (shows.isEmpty()) {
            EmptyView("No podcasts yet", "Subscribe to a show in VCRX on the web or on your phone, and it shows here.")
            return@LoadStateContent
        }
        Column(Modifier.padding(top = 24.dp)) {
            Label("Podcasts", style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(start = 24.dp, top = 12.dp))
            LazyVerticalGrid(
                columns = GridCells.Fixed(MUSIC_COLUMNS),
                contentPadding = PaddingValues(start = 24.dp, end = 24.dp, top = 20.dp, bottom = 48.dp),
                horizontalArrangement = Arrangement.spacedBy(16.dp),
                verticalArrangement = Arrangement.spacedBy(20.dp),
            ) {
                itemsIndexed(shows, key = { _, p -> "podcast:${p.id}" }) { index, p ->
                    MusicCard(
                        title = p.title,
                        subtitle = p.author.ifEmpty { null },
                        imageUrl = if (p.hasArtwork) api.podcastArtworkUrl(p.id) else null,
                        badge = newBadge(p.newCount),
                        onClick = {
                            focus.restoreItemId = podcastKey(p.id)
                            nav.openPodcast(p.id)
                        },
                        focusModifier = itemFocus(focus, podcastKey(p.id), first = index == 0),
                    )
                }
            }
        }
    }
}

/** "3 new", or nothing when there are none. */
fun newBadge(count: Int): String? = if (count > 0) "$count new" else null

class PodcastShowViewModel(private val graph: AppGraph, private val podcastId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<PodcastShowResponse>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.podcastShow(podcastId))
            } catch (e: ApiException) {
                if (quiet && state is LoadState.Ready) state else LoadState.Failed(e)
            }
        }
    }

    fun setPlayed(episode: PodcastEpisode, played: Boolean, onError: (String?) -> Unit) {
        viewModelScope.launch {
            try {
                graph.api.podcastSetPlayed(episode.id, played)
                load(quiet = true)
            } catch (e: ApiException) {
                onError(e.message)
            }
        }
    }
}

/** A show: artwork and facts on the left, its episodes newest first on the right. OK plays one from its saved place. */
@Composable
fun PodcastShowScreen(podcastId: Long) {
    val vm = graphViewModel(key = "podcast:$podcastId") { PodcastShowViewModel(it, podcastId) }
    val context = LocalContext.current
    val graph = context.graph
    val player = remember { MusicPlayer.get(graph, context) }
    val playing by player.state.collectAsState()
    val nav = LocalMusicNav.current
    val first = remember { FocusRequester() }
    var error by remember { mutableStateOf<String?>(null) }
    val dates = remember { java.text.DateFormat.getDateInstance(java.text.DateFormat.MEDIUM) }

    // Back from Now Playing: the places saved while it played show here.
    LifecycleResumeEffect(vm) {
        vm.load(quiet = true)
        onPauseOrDispose { }
    }
    LoadStateContent(vm.state, onRetry = { vm.load() }) { show ->
        val p = show.podcast
        Row(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 32.dp), horizontalArrangement = Arrangement.spacedBy(40.dp)) {
            Column(Modifier.width(300.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                SquareArt(if (p.hasArtwork) graph.api.podcastArtworkUrl(p.id) else null, p.title)
                Spacer(Modifier.height(10.dp))
                Label("Podcast", color = Accent)
                Text(p.title, style = MaterialTheme.typography.headlineSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                if (p.author.isNotEmpty()) Text(p.author, style = MaterialTheme.typography.titleMedium, color = Gray, maxLines = 1)
                val facts = listOfNotNull(
                    if (show.total == 1) "1 episode" else "${show.total} episodes",
                    "${p.newCount} new".takeIf { p.newCount > 0 },
                ).joinToString(" · ")
                Text(facts, style = MaterialTheme.typography.bodyMedium, color = Gray)
                if (p.description.isNotEmpty()) {
                    Text(p.description, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 6, overflow = TextOverflow.Ellipsis)
                }
                error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent) }
            }
            if (show.episodes.isEmpty()) {
                EmptyView("No episodes yet", "The feed lists none. The server reads it again every few hours.")
            } else {
                LazyColumn(contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    items(show.episodes, key = { it.id }) { e ->
                        val isFirst = e.id == show.episodes.first().id
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            BrandRow(
                                onClick = {
                                    player.playEpisode(e)
                                    nav.openNowPlaying()
                                },
                                selected = playing.episode?.id == e.id,
                                modifier = Modifier.weight(1f).then(if (isFirst) Modifier.focusRequester(first) else Modifier),
                            ) {
                                EpisodeRow(e) { dates.format(java.util.Date(it)) }
                            }
                            BrandButton(
                                if (e.played) "Unplayed" else "Played",
                                onClick = { vm.setPlayed(e, !e.played) { error = it } },
                            )
                        }
                    }
                }
            }
        }
        LaunchedEffect(p.id) { runCatching { first.requestFocus() } }
    }
}

@Composable
private fun EpisodeRow(e: PodcastEpisode, date: (Long) -> String) {
    Column(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            if (e.new) Label("New", style = MaterialTheme.typography.labelSmall, color = Accent)
            Text(e.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f), color = if (e.played) Gray else Color.Unspecified)
        }
        val line = listOfNotNull(e.publishedAt?.let(date), PodcastPlayback.remaining(e)).joinToString(" · ")
        if (line.isNotEmpty()) Text(line, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 1)
        val total = e.durationSeconds ?: 0
        if (!e.played && e.positionSeconds > 0 && total > 0) {
            val fraction = (e.positionSeconds / total).toFloat().coerceIn(0f, 1f)
            Box(Modifier.fillMaxWidth().height(3.dp).background(Graphite, RoundedCornerShape(2.dp))) {
                Box(Modifier.fillMaxWidth(fraction).height(3.dp).background(Accent, RoundedCornerShape(2.dp)))
            }
        }
    }
}

/**
 * Now Playing for a podcast episode: the artwork, the episode, a progress bar,
 * play and pause, back 15 and forward 30 seconds, and the speed. The place is
 * saved on the server as it plays (MusicPlayer), so the web and the phone
 * carry on from here.
 */
@Composable
fun PodcastNowPlaying(s: MusicState, player: MusicPlayer) {
    val e = s.episode ?: return
    val api = LocalContext.current.graph.api
    val playPause = remember { FocusRequester() }
    Column(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 48.dp, bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(40.dp)) {
            SquareArt(api.episodeArtworkUrl(e.id), e.podcastTitle, Modifier.width(300.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Label(e.podcastTitle, color = Accent)
                Text(e.title, style = MaterialTheme.typography.headlineSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                if (e.description.isNotEmpty()) {
                    Text(e.description, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 6, overflow = TextOverflow.Ellipsis)
                }
            }
        }
        val fraction = if (s.durationMs > 0) (s.positionMs.toFloat() / s.durationMs).coerceIn(0f, 1f) else 0f
        Box(Modifier.fillMaxWidth().height(6.dp).background(Graphite, RoundedCornerShape(3.dp))) {
            Box(Modifier.fillMaxWidth(fraction).height(6.dp).background(Accent, RoundedCornerShape(3.dp)))
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(clock(s.positionMs), style = MaterialTheme.typography.labelSmall, color = Gray)
            Text(clock(s.durationMs), style = MaterialTheme.typography.labelSmall, color = Gray)
        }
        s.message?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent, maxLines = 2) }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            BrandButton("15 s", onClick = { player.seekBy(-PodcastPlayback.SKIP_BACK_MS) }, icon = AppIcons.Rewind)
            BrandButton(
                if (s.playing) "Pause" else "Play",
                onClick = player::toggle,
                primary = true,
                icon = if (s.playing) AppIcons.Pause else Icons.Filled.PlayArrow,
                modifier = Modifier.focusRequester(playPause),
            )
            BrandButton("30 s", onClick = { player.seekBy(PodcastPlayback.SKIP_FORWARD_MS) }, icon = AppIcons.Forward)
            BrandButton("Speed ${PodcastPlayback.speedLabel(s.speed)}", onClick = player::cycleSpeed)
            BrandButton("Stop", onClick = player::stop)
        }
    }
    LaunchedEffect(e.id) { runCatching { playPause.requestFocus() } }
}
