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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import androidx.compose.ui.res.stringResource
import coil3.SingletonImageLoader
import io.github.pete_builds.vcrx.tv.BuildConfig
import io.github.pete_builds.vcrx.tv.R
import io.github.pete_builds.vcrx.tv.caps.CapabilityPolicy
import io.github.pete_builds.vcrx.tv.caps.CapabilityProbe
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.ApiException
import io.github.pete_builds.vcrx.tv.net.Capabilities
import io.github.pete_builds.vcrx.tv.settings.Describe
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.Wordmark
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class Entry(val label: String, val text: String)

/**
 * About and diagnostics: versions, what this TV decodes in plain words, a speed
 * test to the server, clearing the image cache, then the licences and the raw
 * capability list the server receives. Each entry is a focusable row so the
 * D-pad can scroll through all of it.
 */
@Composable
fun AboutScreen() {
    val context = LocalContext.current
    val graph = context.graph
    val scope = rememberCoroutineScope()
    val first = remember { FocusRequester() }
    var caps by remember { mutableStateOf<Capabilities?>(null) }
    var ffmpeg by remember { mutableStateOf<String?>(null) }
    var serverVersion by remember { mutableStateOf("Asking the server") }
    var speed by remember { mutableStateOf<String?>(null) }
    var testing by remember { mutableStateOf(false) }
    var cache by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(Unit) {
        withContext(Dispatchers.Default) {
            ffmpeg = CapabilityProbe.ffmpegVersion()
            // What the server is sent: the probe narrowed by Settings (quality cap, passthrough, HDR).
            caps = CapabilityPolicy.apply(CapabilityProbe.probe(context), graph.preferences.current)
        }
    }
    LaunchedEffect(Unit) {
        val base = graph.settings.serverUrl
        serverVersion = try {
            if (base == null) "No server" else graph.api.serverInfo(base).let { "${it.product.ifBlank { "Server" }} ${it.version} (API ${it.apiVersion})" }
        } catch (e: ApiException) {
            "Unavailable: ${e.message.orEmpty()}"
        }
    }
    LaunchedEffect(Unit) {
        cache = withContext(Dispatchers.IO) { SingletonImageLoader.get(context).diskCache?.size?.let { "${it / 1_000_000} MB on disk" } }
    }

    val title = stringResource(R.string.app_title)
    val sourceUrl = stringResource(R.string.source_url)
    val entries = buildList {
        add(
            Entry(
                "Licence",
                "GNU Affero General Public License, version 3 or later. You may use, study, share and change this " +
                    "app; if you run a changed version for others, share your changes under the same licence.",
            ),
        )
        add(Entry("Source", sourceUrl))
        add(Entry("Server address", listOfNotNull(graph.settings.serverName, graph.settings.serverUrl).joinToString("  ")))
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
        if (c != null) {
            add(Entry("Containers (as sent to the server)", c.containers.joinToString(" ")))
            add(
                Entry(
                    "Video decoders (as sent to the server)",
                    c.videoCodecs.joinToString("\n") { v ->
                        val size = if (v.maxWidth != null) " up to ${v.maxWidth}x${v.maxHeight}" else ""
                        val profiles = if (v.profiles.isEmpty()) "any profile" else v.profiles.joinToString(", ")
                        "${v.codec} ${v.maxBitDepth ?: 8}-bit$size: $profiles"
                    },
                ),
            )
            add(Entry("Audio (as sent to the server)", c.audioCodecs.joinToString("\n") { "${it.codec} ${it.decoder} ${it.maxChannels ?: "?"}ch" }))
            add(Entry("Subtitles (as sent to the server)", c.subtitleCodecs.joinToString(" ")))
        }
    }

    LazyColumn(contentPadding = PaddingValues(48.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        item(key = "wordmark") { Wordmark(height = 33.dp) }
        item(key = "version") {
            FactRow("App version", "$title ${BuildConfig.VERSION_NAME} (build ${BuildConfig.VERSION_CODE})", Modifier.focusRequester(first))
        }
        item(key = "server-version") { FactRow("Server version", serverVersion) }
        item(key = "speed") {
            SettingRow(
                "Network speed to the server",
                if (testing) "Testing" else speed ?: "Press to test",
                "Downloads up to 48 MB from the server and times it.",
                onClick = {
                    if (!testing) {
                        testing = true
                        scope.launch {
                            speed = try {
                                val (bytes, ms) = graph.api.speedTest(SMALL_TEST_BYTES)
                                // A fast network finishes the small test too quickly to time well; run a larger one.
                                if (ms < 1_500) graph.api.speedTest(LARGE_TEST_BYTES).let { (b, t) -> Describe.speed(b, t) } else Describe.speed(bytes, ms)
                            } catch (e: ApiException) {
                                "Failed: ${e.message.orEmpty()}"
                            }
                            testing = false
                        }
                    }
                },
                modifier = Modifier.width(820.dp),
            )
        }
        item(key = "cache") {
            SettingRow(
                "Clear the image cache",
                cache,
                "Posters and backdrops download again as they are shown.",
                onClick = {
                    scope.launch {
                        withContext(Dispatchers.IO) {
                            val loader = SingletonImageLoader.get(context)
                            loader.memoryCache?.clear()
                            loader.diskCache?.clear()
                        }
                        cache = "Cleared"
                    }
                },
                modifier = Modifier.width(820.dp),
            )
        }
        item(key = "decodes-heading") { SectionLabel("What this TV plays") }
        val c = caps
        if (c == null) {
            item(key = "decodes-loading") { FactRow("Reading this device's decoders", "One moment") }
        } else {
            items(Describe.decoding(c), key = { "decodes:" + it.first }) { (label, text) -> FactRow(label, text) }
        }
        item(key = "details-heading") { SectionLabel("Details") }
        items(entries, key = { it.label }) { entry ->
            BrandRow(onClick = { }, modifier = Modifier.width(820.dp)) {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Label(entry.label, style = MaterialTheme.typography.labelMedium)
                    Text(entry.text, style = MaterialTheme.typography.bodyLarge)
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

private const val SMALL_TEST_BYTES = 8L * 1_000_000
private const val LARGE_TEST_BYTES = 48L * 1_000_000
