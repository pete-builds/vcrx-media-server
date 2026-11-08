package io.github.pete_builds.vcrx.tv.ui.audiobooks

import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.music.AudiobookPlayer
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.AudiobookDetail
import io.github.pete_builds.vcrx.tv.net.audiobook
import io.github.pete_builds.vcrx.tv.net.audiobookCoverUrl
import io.github.pete_builds.vcrx.tv.net.inProgress
import io.github.pete_builds.vcrx.tv.net.seriesLine
import io.github.pete_builds.vcrx.tv.net.setAudiobookFinished
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.music.SquareArt
import io.github.pete_builds.vcrx.tv.ui.music.clock
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import kotlinx.coroutines.launch

class AudiobookViewModel(private val graph: AppGraph, private val bookId: Long) : ViewModel() {
    var state by mutableStateOf<LoadState<AudiobookDetail>>(LoadState.Loading)
        private set

    init {
        load()
    }

    fun load() {
        state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.audiobook(bookId))
            } catch (e: ApiException) {
                LoadState.Failed(e)
            }
        }
    }

    fun setFinished(finished: Boolean) {
        viewModelScope.launch {
            try {
                graph.api.setAudiobookFinished(bookId, finished)
                load()
            } catch (_: ApiException) {
                // The page still shows what the server last said.
            }
        }
    }
}

/** A book: cover and facts on the left, its chapters on the right. Choosing a chapter plays the book from there. */
@Composable
fun AudiobookScreen(bookId: Long) {
    val vm = graphViewModel(key = "audiobook:$bookId") { AudiobookViewModel(it, bookId) }
    val context = LocalContext.current
    val graph = context.graph
    val player = remember { AudiobookPlayer.get(graph, context) }
    val playing by player.state.collectAsState()
    val nav = LocalAudiobookNav.current
    val scope = rememberCoroutineScope()
    val playButton = remember { FocusRequester() }
    var error by remember { mutableStateOf<String?>(null) }

    fun play(atS: Double? = null, fromStart: Boolean = false) {
        scope.launch {
            try {
                player.play(bookId, atS, fromStart)
                nav.openPlaying()
            } catch (e: ApiException) {
                error = e.message
            }
        }
    }

    LoadStateContent(vm.state, onRetry = vm::load) { book ->
        val started = book.progress.inProgress
        val here = playing.book?.bookId == book.id
        Row(Modifier.fillMaxSize().padding(start = 48.dp, top = 40.dp, end = 32.dp), horizontalArrangement = Arrangement.spacedBy(40.dp)) {
            Column(Modifier.width(300.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                SquareArt(if (book.hasCover) graph.api.audiobookCoverUrl(book.id, 640) else null, book.title)
                Spacer(Modifier.height(10.dp))
                Label("Audiobook", color = Accent)
                Text(book.title, style = MaterialTheme.typography.headlineSmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
                Text(book.author, style = MaterialTheme.typography.titleMedium, color = Gray, maxLines = 1)
                val facts = listOfNotNull(
                    clock((book.durationS * 1000).toLong()).takeIf { book.durationS > 0 },
                    if (book.chapters.size == 1) "1 chapter" else "${book.chapters.size} chapters",
                    book.year?.toString(),
                ).joinToString(" · ")
                Text(facts, style = MaterialTheme.typography.bodyMedium, color = Gray)
                listOfNotNull(book.narrator?.let { "Read by $it" }, book.seriesLine()).forEach {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = Gray, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
                book.progress?.let { p ->
                    val line = if (p.finished) "Finished" else if (started) "${clock((p.positionS * 1000).toLong())} listened" else null
                    line?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent) }
                }
                error?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Accent) }
                Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    BrandButton(
                        if (started) "Resume" else "Play",
                        onClick = { play(fromStart = !started) },
                        primary = true,
                        icon = Icons.Filled.PlayArrow,
                        modifier = Modifier.focusRequester(playButton),
                    )
                    if (started) BrandButton("From the start", onClick = { play(fromStart = true) })
                }
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    if (book.progress?.finished == true) {
                        BrandButton("Not finished", onClick = { vm.setFinished(false) })
                    } else {
                        BrandButton("Mark finished", onClick = { vm.setFinished(true) })
                    }
                }
            }
            LazyColumn(contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                book.description?.let { d ->
                    item { Text(d, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 6, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(bottom = 16.dp)) }
                }
                itemsIndexed(book.chapters, key = { _, c -> c.index }) { i, c ->
                    BrandRow(onClick = { play(atS = c.startS) }, selected = here && playing.chapter == i) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            Text((i + 1).toString(), color = Gray, modifier = Modifier.width(40.dp))
                            Text(c.title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                            Text(clock(((c.endS - c.startS) * 1000).toLong()), color = Gray, modifier = Modifier.padding(start = 16.dp))
                        }
                    }
                }
            }
        }
        LaunchedEffect(book.id) { runCatching { playButton.requestFocus() } }
    }
}
