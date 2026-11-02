package io.github.pete_builds.vcrx.tv.ui.browse

import android.text.format.DateFormat
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
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
import io.github.pete_builds.vcrx.tv.live.LivePlayerActivity
import io.github.pete_builds.vcrx.tv.live.LiveTv
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.LiveChannel
import io.github.pete_builds.vcrx.tv.ui.components.EmptyView
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadState
import io.github.pete_builds.vcrx.tv.ui.components.LoadStateContent
import io.github.pete_builds.vcrx.tv.ui.graphViewModel
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import kotlinx.coroutines.launch

class LiveViewModel(private val graph: AppGraph) : ViewModel() {
    var state by mutableStateOf<LoadState<List<LiveChannel>>>(LoadState.Loading)
        private set

    init {
        load()
    }

    /** [quiet] keeps the list on screen and on failure, for the refresh when Live comes back. */
    fun load(quiet: Boolean = false) {
        if (!quiet) state = LoadState.Loading
        viewModelScope.launch {
            state = try {
                LoadState.Ready(graph.api.liveChannels())
            } catch (e: ApiException) {
                if (quiet && state is LoadState.Ready) state else LoadState.Failed(e)
            }
        }
    }
}

/**
 * Live TV in the Browse rail: every channel in the guide's order with its
 * number, logo, what is on now and how far in, and what is next. OK plays it.
 */
@Composable
fun LiveSection(focus: BrowseFocus) {
    val vm = graphViewModel(key = "live") { LiveViewModel(it) }
    // What is on changes while a channel plays in its own activity.
    LifecycleResumeEffect(vm) {
        vm.load(quiet = true)
        onPauseOrDispose { }
    }
    LoadStateContent(vm.state, onRetry = { vm.load() }) { channels ->
        if (channels.isEmpty()) {
            EmptyView("No channels", "An admin lists playlists under live_tv on the server, and grants Live TV in Settings.")
            return@LoadStateContent
        }
        val context = LocalContext.current
        val now = System.currentTimeMillis()
        val time = DateFormat.getTimeFormat(context)
        LazyColumn(
            Modifier.focusRestorer(),
            contentPadding = PaddingValues(24.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item(key = "head") {
                Label("Live TV  ·  ${channels.size} channels", style = MaterialTheme.typography.labelMedium, color = Gray,
                    modifier = Modifier.padding(start = 8.dp, bottom = 8.dp))
            }
            itemsIndexed(channels, key = { _, c -> c.id }) { index, c ->
                FrameCard(
                    onClick = { context.startActivity(LivePlayerActivity.intent(context, c.id)) },
                    modifier = Modifier.fillMaxWidth().then(itemFocus(focus, c.id, first = index == 0, rowId = "live")),
                ) {
                    ChannelRow(c, now) { time.format(java.util.Date(it)) }
                }
            }
        }
    }
}

@Composable
private fun ChannelRow(c: LiveChannel, now: Long, time: (Long) -> String) {
    val api = LocalContext.current.graph.api
    Row(
        Modifier.fillMaxWidth().background(Graphite, RoundedCornerShape(8.dp)).padding(horizontal = 16.dp, vertical = 10.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Label(c.number.orEmpty(), style = MaterialTheme.typography.titleMedium, color = Accent, modifier = Modifier.width(56.dp))
        Box(Modifier.size(width = 72.dp, height = 40.dp), contentAlignment = Alignment.Center) {
            val logo = if (c.hasLogo) api.liveLogoUrl(c.id) else null
            if (logo != null) {
                AsyncImage(model = logo, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.size(width = 72.dp, height = 40.dp))
            } else {
                Label(c.name.take(3), style = MaterialTheme.typography.labelSmall, color = Gray)
            }
        }
        Text(c.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.width(240.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(LiveTv.nowLine(c), style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            c.now?.let { p ->
                Box(Modifier.fillMaxWidth().height(3.dp).background(Gray.copy(alpha = 0.3f), RoundedCornerShape(2.dp))) {
                    Box(Modifier.fillMaxWidth(LiveTv.progress(p, now)).height(3.dp).background(Accent, RoundedCornerShape(2.dp)))
                }
            }
            val next = LiveTv.nextLine(c, time)
            if (next.isNotEmpty()) Text(next, style = MaterialTheme.typography.bodyMedium, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}
