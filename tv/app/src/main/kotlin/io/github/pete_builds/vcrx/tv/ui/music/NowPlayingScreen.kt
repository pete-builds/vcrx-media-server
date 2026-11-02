package io.github.pete_builds.vcrx.tv.ui.music

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.MusicPlayer
import io.github.pete_builds.vcrx.tv.music.Repeat
import io.github.pete_builds.vcrx.tv.net.albumCoverUrl
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent

/**
 * Now Playing: the cover, the track, a progress bar and the controls on the
 * left, the queue on the right. The music carries on when Back leaves this
 * screen; Stop ends it.
 */
@Composable
fun NowPlayingScreen() {
    val context = LocalContext.current
    val graph = context.graph
    val player = remember { MusicPlayer.get(graph, context) }
    val s by player.state.collectAsState()
    val playPause = remember { FocusRequester() }
    val track = s.current

    if (track == null) {
        EmptyView(s.message ?: "Nothing is playing", "Choose an album or an artist to play.")
        return
    }
    Row(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 32.dp, bottom = 32.dp), horizontalArrangement = Arrangement.spacedBy(40.dp)) {
        Column(Modifier.width(420.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            SquareArt(if (track.hasCover) graph.api.albumCoverUrl(track.albumId, 640) else null, track.albumTitle, Modifier.width(300.dp))
            Text(track.title, style = MaterialTheme.typography.headlineSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(track.artistName, style = MaterialTheme.typography.titleMedium, color = Gray, maxLines = 1)
            Text(track.albumTitle, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Progress(s.positionMs, s.durationMs)
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text(clock(s.positionMs), style = MaterialTheme.typography.labelSmall, color = Gray)
                Text(clock(s.durationMs), style = MaterialTheme.typography.labelSmall, color = Gray)
            }
            s.message?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent, maxLines = 2) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                BrandButton("Back", onClick = player::previous, icon = MusicIcons.Previous)
                BrandButton(
                    if (s.playing) "Pause" else "Play",
                    onClick = player::toggle,
                    primary = true,
                    icon = if (s.playing) AppIcons.Pause else Icons.Filled.PlayArrow,
                    modifier = Modifier.focusRequester(playPause),
                )
                BrandButton("Next", onClick = player::next, icon = MusicIcons.Next, enabled = s.position + 1 < s.queue.size || s.repeat == Repeat.All)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                BrandButton(if (s.shuffle) "Shuffle on" else "Shuffle", onClick = { player.setShuffle(!s.shuffle) }, primary = s.shuffle, icon = MusicIcons.Shuffle)
                BrandButton(
                    when (s.repeat) {
                        Repeat.Off -> "Repeat"
                        Repeat.All -> "Repeat all"
                        Repeat.One -> "Repeat one"
                    },
                    onClick = { player.cycleRepeat() },
                    primary = s.repeat != Repeat.Off,
                    icon = MusicIcons.Repeat,
                )
                BrandButton("Stop", onClick = player::stop)
            }
            if (track.stream?.seekable == true) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    BrandButton("10 s", onClick = { player.seekBy(-10_000) }, icon = AppIcons.Rewind)
                    BrandButton("30 s", onClick = { player.seekBy(30_000) }, icon = AppIcons.Forward)
                }
            }
        }
        Column(Modifier.fillMaxHeight(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Label(if (s.queue.size == 1) "Queue · 1 track" else "Queue · ${s.queue.size} tracks", style = MaterialTheme.typography.labelLarge)
            val list = rememberLazyListState()
            LazyColumn(state = list, contentPadding = PaddingValues(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                itemsIndexed(s.queue, key = { i, t -> "$i:${t.id}" }) { i, t ->
                    BrandRow(onClick = { player.jumpTo(i) }, selected = i == s.position) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(t.title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(t.artistName, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 1)
                            }
                            Text(clock(t.durationMs), color = Gray, modifier = Modifier.padding(start = 16.dp))
                        }
                    }
                }
            }
            LaunchedEffect(s.position) { if (s.position >= 0) list.animateScrollToItem(s.position) }
        }
    }
    LaunchedEffect(Unit) { runCatching { playPause.requestFocus() } }
}

@Composable
private fun Progress(positionMs: Long, durationMs: Long) {
    val fraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
    Box(Modifier.fillMaxWidth().height(6.dp).background(Graphite, RoundedCornerShape(3.dp))) {
        Box(Modifier.fillMaxWidth(fraction).height(6.dp).background(Accent, RoundedCornerShape(3.dp)))
    }
}
