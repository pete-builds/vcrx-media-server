package io.github.pete_builds.vcrx.tv.live

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.text.format.DateFormat
import android.view.KeyEvent
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.LiveChannel
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadingCursor
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.BrandTheme
import io.github.pete_builds.vcrx.tv.ui.theme.Gray
import io.github.pete_builds.vcrx.tv.ui.theme.Accent
import io.github.pete_builds.vcrx.tv.ui.theme.White
import java.util.Date
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Live TV: one channel playing from the server's own live HLS playlist. Up
 * and Down (or the remote's channel keys) change channel in the guide's order,
 * showing the next channel at once and tuning it once the presses stop; OK
 * shows what is on; Back leaves. Leaving a channel tells the server, so a
 * pull nobody watches stops at once. Nothing here writes watch state.
 */
@OptIn(UnstableApi::class)
class LivePlayerActivity : ComponentActivity() {
    private var player: ExoPlayer? = null
    private var channels: List<LiveChannel> = emptyList()
    private var shown by mutableStateOf<LiveChannel?>(null)
    private var info by mutableStateOf(true)
    private var tuning by mutableStateOf(true)
    private var problem by mutableStateOf<String?>(null)
    private var watching: Pair<Long, String>? = null
    private var tuneJob: Job? = null
    private var infoJob: Job? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        val first = intent.getLongExtra(EXTRA_CHANNEL_ID, -1)
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val p = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(this, http)))
            .build()
        p.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                tuning = state == Player.STATE_BUFFERING || state == Player.STATE_IDLE
            }

            override fun onPlayerError(error: PlaybackException) {
                if (error.errorCode == PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW) {
                    // Fell behind the sliding playlist: back to the live edge.
                    p.seekToDefaultPosition()
                    p.prepare()
                    return
                }
                tuning = false
                problem = "The channel stopped playing. Press Up or Down for another."
            }
        })
        player = p
        setContent { BrandTheme { LiveScreen() } }
        lifecycleScope.launch {
            channels = runCatching { graph.api.liveChannels() }.getOrDefault(emptyList())
            val start = channels.firstOrNull { it.id == first } ?: channels.firstOrNull()
            if (start == null) {
                tuning = false
                problem = "No channels to watch."
                return@launch
            }
            show(start)
            tune(start)
        }
    }

    // Nothing on this screen takes focus, so every key reaches the activity.
    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        when (LiveTv.key(keyCode)) {
            LiveTv.Key.ChannelUp -> surf(1)
            LiveTv.Key.ChannelDown -> surf(-1)
            LiveTv.Key.Info -> show(shown ?: return true)
            LiveTv.Key.Other -> return super.onKeyDown(keyCode, event)
        }
        return true
    }

    /** Shows the next channel at once and tunes it once the presses stop. */
    private fun surf(delta: Int) {
        val from = shown ?: return
        val next = LiveTv.step(channels, from.id, delta) ?: return
        show(next)
        tuneJob?.cancel()
        tuneJob = lifecycleScope.launch {
            delay(SURF_SETTLE_MS)
            tune(next)
        }
    }

    private fun show(c: LiveChannel) {
        shown = c
        info = true
        infoJob?.cancel()
        infoJob = lifecycleScope.launch {
            delay(INFO_MS)
            info = false
        }
    }

    private suspend fun tune(c: LiveChannel) {
        leave()
        problem = null
        tuning = true
        val p = player ?: return
        try {
            val w = graph.api.liveWatch(c.id)
            val url = graph.api.absoluteUrl(w.playlist) ?: error("The server sent a stream address this app cannot use.")
            watching = c.id to w.run
            p.setMediaItem(MediaItem.Builder().setUri(url).setMimeType(MimeTypes.APPLICATION_M3U8).build())
            p.prepare()
            p.playWhenReady = true
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            p.stop()
            tuning = false
            problem = LiveTv.failure(e)
        }
    }

    /** Tells the server this viewer has left, without waiting for the answer. */
    private fun leave() {
        val (id, run) = watching ?: return
        watching = null
        graph.appScope.launch { runCatching { graph.api.liveLeave(id, run) } }
    }

    override fun onStop() {
        super.onStop()
        // Home or the TV sleeping: leave the channel and stop.
        leave()
        player?.release()
        player = null
        if (!isFinishing) finish()
    }

    override fun onDestroy() {
        player?.release()
        player = null
        super.onDestroy()
    }

    @Composable
    private fun LiveScreen() {
        Box(Modifier.fillMaxSize().background(Black)) {
            val p = player
            if (p != null) {
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { context ->
                        PlayerView(context).apply {
                            useController = false
                            isFocusable = false
                            setShutterBackgroundColor(Black.toArgb())
                            player = p
                        }
                    },
                )
            }
            if (tuning) LoadingCursor(Modifier.align(Alignment.TopStart), description = "Tuning")
            val c = shown
            if (c != null && (info || problem != null)) {
                Column(
                    Modifier.align(Alignment.BottomStart).fillMaxWidth()
                        .background(Brush.verticalGradient(0f to Color.Transparent, 1f to Black.copy(alpha = 0.9f)))
                        .padding(horizontal = 48.dp, vertical = 32.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    Label(LiveTv.label(c), style = MaterialTheme.typography.labelLarge, color = Accent)
                    Text(LiveTv.nowLine(c), style = MaterialTheme.typography.headlineSmall, color = White, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    val next = LiveTv.nextLine(c) { DateFormat.getTimeFormat(this@LivePlayerActivity).format(Date(it)) }
                    if (next.isNotEmpty()) Text(next, style = MaterialTheme.typography.bodyLarge, color = Gray, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    problem?.let { Text(it, style = MaterialTheme.typography.bodyLarge, color = White) }
                }
            }
        }
    }

    companion object {
        private const val EXTRA_CHANNEL_ID = "channel_id"

        /** How long Up and Down presses must stop before the channel shown is tuned. */
        private const val SURF_SETTLE_MS = 600L

        /** How long the channel's name and what is on stay on screen. */
        private const val INFO_MS = 4000L

        fun intent(context: Context, channelId: Long): Intent =
            Intent(context, LivePlayerActivity::class.java).putExtra(EXTRA_CHANNEL_ID, channelId)
    }
}
