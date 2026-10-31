package io.github.pete_builds.vcrx.tv.ui.details

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.Extra
import io.github.pete_builds.vcrx.tv.net.Item
import io.github.pete_builds.vcrx.tv.trailers.LaunchResult
import io.github.pete_builds.vcrx.tv.trailers.TrailerSettings
import io.github.pete_builds.vcrx.tv.trailers.launchYouTube
import io.github.pete_builds.vcrx.tv.trailers.offeredTrailer
import io.github.pete_builds.vcrx.tv.trailers.trailerSource
import io.github.pete_builds.vcrx.tv.ui.LocalAppActions
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.FrameCard
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.theme.CornerRadius
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Gray

// Trailers, extras and theme music on the details screen. A local trailer or
// extra plays in the app's own player; a YouTube trailer (only when the server
// offers them and the viewer allows them) opens in the YouTube app.

/** The Trailer button, beside Play. Nothing when there is no trailer to offer. */
@Composable
fun TrailerButton(item: Item) {
    val context = LocalContext.current
    val actions = LocalAppActions.current
    // The server sends a YouTube trailer only when it offers them.
    val source = trailerSource(TrailerSettings.chosen(context.graph.preferences.current), serverOffersYouTube = item.trailer?.source == "youtube")
    val trailer = offeredTrailer(item.trailer, source) ?: return
    var problem by remember { mutableStateOf<String?>(null) }
    BrandButton("Trailer", onClick = {
        val extraId = trailer.extraId
        if (trailer.source == "local" && extraId != null) {
            actions.playExtra(item.id, extraId, "${item.title}  ${trailer.title}")
        } else {
            val result = launchYouTube(trailer.youtubeKey) { uri -> openView(context, uri) }
            if (result is LaunchResult.Failed) problem = result.message
        }
    })
    problem?.let { message ->
        val ok = remember { FocusRequester() }
        BrandDialog("Trailer", onDismiss = { problem = null }) {
            Text(message, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.padding(bottom = 16.dp))
            BrandButton("OK", onClick = { problem = null }, primary = true, modifier = Modifier.focusRequester(ok))
        }
        LaunchedEffect(Unit) { runCatching { ok.requestFocus() } }
    }
}

/** Starts an ACTION_VIEW for [uri]; false when nothing on the TV handles it. */
private fun openView(context: Context, uri: String): Boolean =
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(uri)))
        true
    } catch (e: ActivityNotFoundException) {
        false
    }

/** The Extras row: each local extra, which plays in the app's player. */
@Composable
fun ExtrasRow(item: Item) {
    if (item.extras.isEmpty()) return
    val actions = LocalAppActions.current
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Label("Extras", style = MaterialTheme.typography.labelLarge)
        LazyRow(Modifier.focusRestorer(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            items(item.extras, key = { it.id }) { extra ->
                FrameCard(onClick = { actions.playExtra(item.id, extra.id, "${item.title}  ${extra.title}") }, modifier = Modifier.width(300.dp)) {
                    Column(
                        Modifier.fillMaxWidth().background(Graphite, RoundedCornerShape(CornerRadius)).padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        Label(extraLine(extra), style = MaterialTheme.typography.labelSmall, color = Gray)
                        Text(extra.title, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
    }
}

private val kindNames = mapOf(
    "trailer" to "Trailer", "featurette" to "Featurette", "behind_the_scenes" to "Behind the scenes",
    "deleted_scene" to "Deleted scene", "interview" to "Interview", "scene" to "Scene", "short" to "Short",
)

private fun extraLine(extra: Extra): String =
    listOfNotNull(kindNames[extra.kind] ?: "Extra", extra.durationMs?.let { formatTime(it) }).joinToString("   ")

/** How loud a theme plays behind the details screen, out of 1. */
private const val THEME_VOLUME = 0.25f

/**
 * A show's theme.mp3, played once and quietly while its details screen is in
 * front, when Settings > Playback > Theme music is on (the default). It pauses whenever
 * the screen does, for the player or the YouTube app, and stops for good when
 * the screen goes.
 */
@OptIn(UnstableApi::class)
@Composable
fun ThemeMusic(item: Item) {
    val context = LocalContext.current
    val graph = context.graph
    val theme = item.themeMusic ?: return
    if (!graph.preferences.current.themeMusic) return
    val url = remember(theme.url) { graph.api.absoluteUrl(theme.url) } ?: return
    val player = remember(url) {
        ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(context, OkHttpDataSource.Factory(graph.httpClient))))
            .setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(), false)
            .build()
            .apply {
                volume = THEME_VOLUME
                setMediaItem(MediaItem.fromUri(url))
                prepare()
            }
    }
    DisposableEffect(player) { onDispose { player.release() } }
    LifecycleResumeEffect(player) {
        player.play()
        onPauseOrDispose { player.pause() }
    }
}
