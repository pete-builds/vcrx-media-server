package io.github.pete_builds.vcrx.tv.ui.audiobooks

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.AudiobookPlayer
import io.github.pete_builds.vcrx.tv.net.audiobookCoverUrl
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.music.MusicIcons
import io.github.pete_builds.vcrx.tv.ui.music.SquareArt
import io.github.pete_builds.vcrx.tv.ui.music.clock
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray

/**
 * The book player: the cover, the chapter, where the book is, and the
 * controls (chapters, 15 and 30 s skips, speed). The book carries on when
 * Back leaves this screen; Stop ends it and saves the place.
 */
@Composable
fun AudiobookPlayingScreen() {
    val context = LocalContext.current
    val graph = context.graph
    val player = remember { AudiobookPlayer.get(graph, context) }
    val s by player.state.collectAsState()
    val playPause = remember { FocusRequester() }
    val book = s.book

    if (book == null) {
        EmptyView(s.message ?: "No book is playing", "Choose a book to play.")
        return
    }
    val chapter = book.chapters.getOrNull(s.chapter)
    Row(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 32.dp, bottom = 32.dp), horizontalArrangement = Arrangement.spacedBy(40.dp)) {
        SquareArt(if (book.hasCover) graph.api.audiobookCoverUrl(book.bookId, 640) else null, book.title, Modifier.width(320.dp))
        Column(Modifier.width(560.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(s.chapterTitle ?: book.title, style = MaterialTheme.typography.headlineSmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            Text(book.title, style = MaterialTheme.typography.titleMedium, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(book.author, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 1)
            if (chapter != null) {
                val into = ((s.positionS - chapter.startS) * 1000).toLong()
                val length = ((chapter.endS - chapter.startS) * 1000).toLong()
                Progress(into, length)
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    Text(clock(into), style = MaterialTheme.typography.labelSmall, color = Gray)
                    Text("Chapter ${s.chapter + 1} of ${book.chapters.size}", style = MaterialTheme.typography.labelSmall, color = Gray)
                    Text(clock(length), style = MaterialTheme.typography.labelSmall, color = Gray)
                }
            }
            Text(
                "${clock((s.positionS * 1000).toLong())} of ${clock((book.durationS * 1000).toLong())} in the book",
                style = MaterialTheme.typography.bodySmall,
                color = Gray,
            )
            s.message?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent, maxLines = 2) }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                BrandButton("Chapter", onClick = player::previousChapter, icon = MusicIcons.Previous)
                BrandButton("15 s", onClick = { player.skip(-15.0) }, icon = AppIcons.Rewind)
                BrandButton(
                    if (s.playing) "Pause" else "Play",
                    onClick = player::toggle,
                    primary = true,
                    icon = if (s.playing) AppIcons.Pause else Icons.Filled.PlayArrow,
                    modifier = Modifier.focusRequester(playPause),
                )
                BrandButton("30 s", onClick = { player.skip(30.0) }, icon = AppIcons.Forward)
                BrandButton("Chapter", onClick = player::nextChapter, icon = MusicIcons.Next, enabled = s.chapter + 1 < book.chapters.size)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                BrandButton("Speed ${speedLabel(s.speed)}", onClick = player::cycleSpeed, primary = s.speed != 1f)
                BrandButton("Stop", onClick = player::stop)
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { playPause.requestFocus() } }
}

private fun speedLabel(speed: Float): String = (if (speed == speed.toInt().toFloat()) speed.toInt().toString() else speed.toString()) + "×"

@Composable
private fun Progress(positionMs: Long, durationMs: Long) {
    val fraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
    Box(Modifier.fillMaxWidth().height(6.dp).background(Graphite, RoundedCornerShape(3.dp))) {
        Box(Modifier.fillMaxWidth(fraction).height(6.dp).background(Accent, RoundedCornerShape(3.dp)))
    }
}
