package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
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
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.auto.Features
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.ApiException
import io.github.pete_builds.vcrx.mobile.net.AudiobookBook
import io.github.pete_builds.vcrx.mobile.net.AudiobookDetail
import io.github.pete_builds.vcrx.mobile.net.audiobook
import io.github.pete_builds.vcrx.mobile.net.audiobookCoverUrl
import io.github.pete_builds.vcrx.mobile.net.audiobooks
import io.github.pete_builds.vcrx.mobile.net.audiobooksInProgress
import io.github.pete_builds.vcrx.mobile.net.inProgress
import io.github.pete_builds.vcrx.mobile.net.seriesLine
import io.github.pete_builds.vcrx.mobile.net.setAudiobookFinished
import io.github.pete_builds.vcrx.mobile.offline.DownloadEligibility
import io.github.pete_builds.vcrx.mobile.offline.DownloadGroups
import kotlinx.coroutines.launch

private enum class BookSort(val label: String, val key: String) {
    Title("Title", "title"),
    Author("Author", "author"),
    Added("Recently added", "added"),
    Recent("Recently listened", "recent"),
}

/** "11 h 4 min", "42 min". */
fun formatLength(seconds: Double): String {
    val minutes = ((seconds + 59) / 60).toLong().coerceAtLeast(0)
    return if (minutes >= 60) "${minutes / 60} h ${minutes % 60} min" else "$minutes min"
}

/** Audiobooks: Continue listening, then every book of the library, as covers. */
@Composable
fun AudiobooksScreen(onBook: (Long) -> Unit) {
    val graph = LocalContext.current.graph
    val libs by graph.libraries.collectAsState()
    val bookLibs = libs.filter { it.kind == Features.KIND_AUDIOBOOK }
    if (bookLibs.isEmpty()) return Message("No audiobook library on this server.")
    var libId by rememberSaveable { mutableLongStateOf(bookLibs.first().id) }
    val lib = bookLibs.firstOrNull { it.id == libId } ?: bookLibs.first()
    var sort by rememberSaveable { mutableStateOf(BookSort.Title) }
    Loadable("books${lib.id}${sort.key}", { graph.api.audiobooksInProgress() to graph.api.audiobooks(lib.id, sort.key).books }) { (started, books) ->
        LazyVerticalGrid(
            columns = GridCells.Adaptive(150.dp),
            contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
            modifier = Modifier.fillMaxSize(),
        ) {
            val inProgress = started.filter { it.libraryId == lib.id || it.libraryId == 0L }
            if (inProgress.isNotEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text("Continue listening", style = SectionTitle)
                        LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            items(inProgress, key = { it.id }) { b -> BookCard(b, onClick = { onBook(b.id) }, width = 130.dp) }
                        }
                    }
                }
            }
            item(span = { GridItemSpan(maxLineSpan) }) {
                LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (bookLibs.size > 1) {
                        items(bookLibs, key = { "lib${it.id}" }) { l ->
                            FilterChip(
                                selected = l.id == lib.id,
                                onClick = { libId = l.id },
                                label = { Text(l.name) },
                                colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Brand.Tint),
                            )
                        }
                    }
                    items(BookSort.entries, key = { it.key }) { s ->
                        FilterChip(
                            selected = sort == s,
                            onClick = { sort = s },
                            label = { Text(s.label) },
                            colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Brand.Tint),
                        )
                    }
                }
            }
            if (books.isEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) { Text("This library is empty. Scan it on the server.", color = Brand.Muted) }
            }
            items(books, key = { it.id }) { b -> BookCard(b, onClick = { onBook(b.id) }) }
        }
    }
}

/** A book's cover, with how far it was listened, its title and author. */
@Composable
fun BookCard(b: AudiobookBook, onClick: () -> Unit, width: Dp? = null) {
    val graph = LocalContext.current.graph
    Column(Modifier.then(if (width != null) Modifier.width(width) else Modifier).clickable(onClick = onClick)) {
        Box {
            Art(if (b.hasCover) graph.api.audiobookCoverUrl(b.id, 320) else null, Modifier.fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(6.dp)))
            val p = b.progress
            if (p.inProgress && b.durationS > 0) {
                LinearProgressIndicator(
                    progress = { (p!!.positionS / b.durationS).toFloat().coerceIn(0f, 1f) },
                    color = Brand.Green,
                    trackColor = Brand.Black,
                    modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(3.dp),
                )
            }
        }
        Text(b.title, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
        val line = when {
            b.progress?.finished == true -> "Finished"
            else -> b.author
        }
        Text(line, style = MaterialTheme.typography.bodySmall, color = Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/**
 * A book: cover, author, narrator and series, Resume or Play, From the start,
 * Mark finished, Download, what it is about, and its chapters (each plays from
 * its start). With no server, a downloaded book still opens from the phone.
 */
@Composable
fun BookScreen(bookId: Long, remote: MusicRemote?, onPlaying: () -> Unit) {
    val graph = LocalContext.current.graph
    val me by graph.me.collectAsState()
    val online by graph.online.collectAsState()
    val downloads by graph.downloads.entries.collectAsState()
    val scope = rememberCoroutineScope()
    var reload by remember { mutableIntStateOf(0) }
    var error by remember { mutableStateOf<String?>(null) }
    Loadable(bookId to reload, {
        try {
            graph.api.audiobook(bookId)
        } catch (e: ApiException) {
            graph.offlineAudio.book(bookId) ?: throw e
        }
    }) { book: AudiobookDetail ->
        val group = DownloadGroups.books(downloads).firstOrNull { it.bookId == bookId }
        fun play(atMs: Long? = null) {
            val r = remote ?: return
            if (!online && group?.done == true && atMs == null) r.playDownloadedBook(bookId) else r.playBook(bookId, atMs)
            onPlaying()
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
            item {
                Column(Modifier.fillMaxWidth().padding(16.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Art(if (book.hasCover) graph.api.audiobookCoverUrl(book.id, 640) else null, Modifier.widthIn(max = 300.dp).fillMaxWidth().aspectRatio(1f).clip(RoundedCornerShape(8.dp)))
                    Text(book.title, style = MaterialTheme.typography.headlineSmall)
                    book.subtitle?.let { Text(it, color = Brand.Muted) }
                    if (book.author.isNotEmpty()) Text(book.author, color = Brand.Green)
                    FactLine(listOf(book.narrator?.let { "Read by $it" }, book.seriesLine()))
                    val p = book.progress
                    FactLine(
                        listOf(
                            formatLength(book.durationS),
                            "${book.chapters.size} chapters".takeIf { book.chapters.isNotEmpty() },
                            when {
                                p?.finished == true -> "Finished"
                                p.inProgress -> "${formatLength(book.durationS - p!!.positionS)} left"
                                else -> null
                            },
                        ),
                    )
                    val started = p.inProgress
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        BrandButton(if (started) "Resume" else "Play", enabled = remote != null, onClick = { play() })
                        if (started || p?.finished == true) QuietButton("From the start", enabled = remote != null, onClick = { play(0L) })
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
                        if (online) {
                            val finished = p?.finished == true
                            QuietButton(if (finished) "Mark not finished" else "Mark finished", onClick = {
                                scope.launch {
                                    try {
                                        graph.api.setAudiobookFinished(book.id, !finished)
                                        reload++
                                    } catch (e: ApiException) {
                                        error = e.message
                                    }
                                }
                            })
                        }
                        if (DownloadEligibility.canDownloadBook(book.files.size, me, graph.tokens.isInvitedDevice)) {
                            when {
                                group == null -> if (online) QuietButton("Download", onClick = { graph.downloads.addBook(book) })
                                group.done -> QuietButton("Remove download", onClick = { graph.downloads.removeBook(book.id) })
                                else -> QuietButton("Downloading ${group.filesDone} of ${group.files.size}", onClick = {})
                            }
                        }
                    }
                    error?.let { Text(it, color = Brand.Error) }
                    book.description?.takeIf { it.isNotBlank() }?.let {
                        var open by remember { mutableStateOf(false) }
                        Text(
                            it,
                            color = Brand.Muted,
                            style = MaterialTheme.typography.bodyMedium,
                            maxLines = if (open) Int.MAX_VALUE else 5,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.clickable { open = !open },
                        )
                    }
                }
            }
            if (book.chapters.isNotEmpty()) item { RowHeader("Chapters") }
            items(book.chapters, key = { it.index }) { c ->
                Row(
                    Modifier.fillMaxWidth().clickable(enabled = remote != null) { play((c.startS * 1000).toLong()) }.padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(c.title.ifEmpty { "Chapter ${c.index + 1}" }, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                    Text(formatTime((c.startS * 1000).toLong()), color = Brand.Muted, modifier = Modifier.padding(start = 8.dp))
                }
            }
        }
    }
}
