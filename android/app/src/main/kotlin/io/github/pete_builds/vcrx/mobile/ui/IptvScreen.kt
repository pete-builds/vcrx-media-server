package io.github.pete_builds.vcrx.mobile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.mobile.graph
import io.github.pete_builds.vcrx.mobile.net.LiveProgram
import io.github.pete_builds.vcrx.mobile.player.VideoPlayerActivity
import java.text.DateFormat
import java.util.Date

/** IPTV: the server's channels with what is on now and next. Tap one to watch. */
@Composable
fun IptvScreen() {
    val context = LocalContext.current
    val graph = context.graph
    Loadable(Unit, { graph.api.liveChannels() }) { channels ->
        if (channels.isEmpty()) return@Loadable Message("No IPTV channels on this server.")
        LazyColumn(Modifier.fillMaxSize()) {
            items(channels, key = { it.id }) { ch ->
                Row(
                    Modifier.fillMaxWidth().clickable { VideoPlayerActivity.playChannel(context, ch.id, ch.name) }.padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Art(if (ch.hasLogo) graph.api.liveLogoUrl(ch.id) else null, Modifier.size(56.dp).clip(RoundedCornerShape(6.dp)), contentScale = ContentScale.Fit)
                    Column(Modifier.weight(1f)) {
                        Text(listOfNotNull(ch.number, ch.name).joinToString("  "), style = MaterialTheme.typography.titleMedium, maxLines = 1)
                        ch.now?.let { Text("Now: ${program(it)}", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                        ch.next?.let { Text("Next: ${program(it)}", style = MaterialTheme.typography.bodySmall, color = Brand.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                    }
                }
            }
        }
    }
}

private fun program(p: LiveProgram): String {
    val f = DateFormat.getTimeInstance(DateFormat.SHORT)
    return "${p.title}, ${f.format(Date(p.start))} to ${f.format(Date(p.stop))}"
}
