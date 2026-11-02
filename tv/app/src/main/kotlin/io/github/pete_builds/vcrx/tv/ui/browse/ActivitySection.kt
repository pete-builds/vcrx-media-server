package io.github.pete_builds.vcrx.tv.ui.browse

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.AppGraph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.NowPlaying
import io.github.pete_builds.vcrx.tv.net.NowSession
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.details.formatTime
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** The message a viewer sees when their stream is stopped from the TV. */
const val STOP_MESSAGE = "Stopped from the living room TV."

class ActivityViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<LoadState<NowPlaying>>(LoadState.Loading)
        private set
    var note by mutableStateOf<String?>(null)
        private set

    init {
        viewModelScope.launch {
            while (isActive) {
                refresh()
                delay(REFRESH_MS)
            }
        }
    }

    suspend fun refresh() {
        state = try {
            LoadState.Ready(graph.api.nowPlaying())
        } catch (e: ApiException) {
            if (state is LoadState.Ready) state else LoadState.Failed(e)
        }
    }

    fun stop(session: NowSession) {
        viewModelScope.launch {
            note = try {
                graph.api.terminate(session.id, STOP_MESSAGE)
                "Stopped ${session.username ?: "the"} stream of ${session.itemTitle ?: "that title"}."
            } catch (e: ApiException) {
                e.message
            }
            refresh()
        }
    }

    private companion object {
        const val REFRESH_MS = 5_000L
    }
}

/** Now playing on every device, for admins, with a Stop button per stream. */
@Composable
fun ActivitySection(focus: BrowseFocus) {
    val vm = graphViewModel(key = "activity") { ActivityViewModel(it) }
    val first = remember { FocusRequester() }
    Column(Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Label("Now playing", style = MaterialTheme.typography.labelLarge)
        vm.note?.let { Text(it, style = MaterialTheme.typography.bodyLarge) }
        LoadStateContent(vm.state, onRetry = { }) { now ->
            if (now.sessions.isEmpty()) {
                EmptyView("Nothing is playing", "Streams on any device show up here.")
            } else {
                LazyColumn(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    items(now.sessions, key = { it.id }) { s ->
                        SessionRow(s, onStop = { vm.stop(s) }, modifier = if (s.id == now.sessions.first().id) Modifier.focusRequester(first) else Modifier)
                    }
                }
                LaunchedEffect(now.sessions.firstOrNull()?.id) {
                    if (focus.toContent) {
                        runCatching { first.requestFocus() }
                        focus.toContent = false
                    }
                }
            }
        }
    }
}

@Composable
private fun SessionRow(s: NowSession, onStop: () -> Unit, modifier: Modifier) {
    Row(
        Modifier.fillMaxWidth().background(Graphite, RoundedCornerShape(8.dp)).padding(16.dp),
        horizontalArrangement = Arrangement.spacedBy(24.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(s.itemTitle ?: "Unknown title", style = MaterialTheme.typography.titleMedium)
            Label(listOfNotNull(s.username, s.deviceName ?: s.client, s.versionSummary).joinToString("  "), color = Gray)
            val duration = s.durationMs?.takeIf { it > 0 }
            Box(Modifier.fillMaxWidth().height(4.dp).background(Gray.copy(alpha = 0.3f))) {
                if (duration != null) {
                    Box(Modifier.fillMaxWidth((s.positionMs.toFloat() / duration).coerceIn(0f, 1f)).height(4.dp).background(Accent))
                }
            }
            Label(
                listOfNotNull(s.state.replaceFirstChar { it.uppercase() }, formatTime(s.positionMs) + (duration?.let { " / " + formatTime(it) } ?: ""),
                    s.decision.replace('_', ' ')).joinToString("  "),
                color = Gray,
            )
        }
        BrandButton("Stop", onClick = onStop, modifier = modifier)
    }
}
