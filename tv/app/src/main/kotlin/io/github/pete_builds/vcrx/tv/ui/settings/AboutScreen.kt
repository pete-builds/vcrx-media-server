package io.github.pete_builds.vcrx.tv.ui.settings

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import androidx.compose.ui.res.stringResource
import io.github.pete_builds.vcrx.tv.BuildConfig
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.caps.CapabilityProbe
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

private data class Entry(val label: String, val text: String)

/**
 * Version, licences, and what this TV reported it can play. Each entry is a
 * focusable row so the D-pad can scroll through all of it.
 */
@Composable
fun AboutScreen() {
    val context = LocalContext.current
    val graph = context.graph
    val first = remember { FocusRequester() }
    var caps by remember { mutableStateOf<Capabilities?>(null) }
    var ffmpeg by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        withContext(Dispatchers.Default) {
            ffmpeg = CapabilityProbe.ffmpegVersion()
            caps = CapabilityProbe.probe(context)
        }
    }

    val title = stringResource(R.string.app_title)
    val sourceUrl = stringResource(R.string.source_url)
    val entries = buildList {
        add(Entry("Version", "$title ${BuildConfig.VERSION_NAME} (build ${BuildConfig.VERSION_CODE})"))
        add(
            Entry(
                "Licence",
                "GNU Affero General Public License, version 3 or later. You may use, study, share and change this " +
                    "app; if you run a changed version for others, share your changes under the same licence.",
            ),
        )
        add(Entry("Source", sourceUrl))
        add(Entry("Server", listOfNotNull(graph.settings.serverName, graph.settings.serverUrl).joinToString("  ")))
        add(Entry("This device", "${Build.MANUFACTURER} ${Build.MODEL}, Android ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})"))
        add(
            Entry(
                "Audio decoding",
                (ffmpeg?.let { "FFmpeg $it" } ?: "FFmpeg extension not loaded") +
                    ", built from unmodified source as the Media3 FFmpeg extension. FFmpeg is licensed under the " +
                    "LGPL 2.1 or later; its source and build steps are in the repository.",
            ),
        )
        add(Entry("Playback", "AndroidX Media3 1.11.1, Apache License 2.0."))
        add(Entry("Fonts", "Rajdhani (Indian Type Foundry) and Inter 4.1, SIL Open Font License 1.1. Licence texts ship in the app."))
        add(Entry("Privacy", "No telemetry, analytics, ads or accounts. The app talks only to the server you connect it to."))
        val c = caps
        if (c == null) {
            add(Entry("Capabilities", "Reading this device's decoders"))
        } else {
            add(Entry("Containers", c.containers.joinToString(" ")))
            add(
                Entry(
                    "Video decoders",
                    c.videoCodecs.joinToString("\n") { v ->
                        val size = if (v.maxWidth != null) " up to ${v.maxWidth}x${v.maxHeight}" else ""
                        val profiles = if (v.profiles.isEmpty()) "any profile" else v.profiles.joinToString(", ")
                        "${v.codec} ${v.maxBitDepth ?: 8}-bit$size: $profiles"
                    },
                ),
            )
            add(Entry("Audio", c.audioCodecs.joinToString("\n") { "${it.codec} ${it.decoder} ${it.maxChannels ?: "?"}ch" }))
            add(Entry("HDR", c.hdrTypes.ifEmpty { listOf("none") }.joinToString(" ")))
            add(Entry("Display", "${c.maxWidth}x${c.maxHeight}"))
            add(Entry("Subtitles", c.subtitleCodecs.joinToString(" ")))
        }
    }

    LazyColumn(contentPadding = PaddingValues(48.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item(key = "wordmark") { Wordmark(height = 33.dp) }
        items(entries, key = { it.label }) { entry ->
            BrandRow(
                onClick = { },
                modifier = Modifier.width(820.dp).then(if (entry.label == "Version") Modifier.focusRequester(first) else Modifier),
            ) {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Label(entry.label, style = MaterialTheme.typography.labelMedium)
                    Text(entry.text, style = MaterialTheme.typography.bodyLarge)
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}
