package io.github.pete_builds.vcrx.tv.player

import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.lifecycleScope
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.PlayerView
import androidx.tv.material3.MaterialTheme
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.BeforeClip
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.BrandTheme
import io.github.pete_builds.vcrx.tv.ui.theme.White
import kotlinx.coroutines.launch

/**
 * Before a movie: plays the trailers and pre-roll the profile's settings call
 * for (GET /items/{id}/before), one after another with one press of Skip each,
 * then hands over to [PlayerActivity]. With nothing to play it hands over at
 * once. Nothing here starts a playback session, so watch state counts only
 * the movie. Back leaves without playing it.
 */
@OptIn(UnstableApi::class)
class BeforeMovieActivity : ComponentActivity() {
    private var player: ExoPlayer? = null
    private var handedOver = false
    private var clips by mutableStateOf<List<BeforeClip>>(emptyList())
    private var index by mutableIntStateOf(0)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        val itemId = intent.getLongExtra(EXTRA_ITEM_ID, -1)
        setContent { BrandTheme { BeforeScreen() } }
        lifecycleScope.launch {
            // Offline or refused: the movie, never a dead end.
            val found = runCatching { graph.api.before(itemId).clips }.getOrDefault(emptyList())
                .mapNotNull { c -> graph.api.absoluteUrl(c.url)?.let { c.copy(url = it) } }
            if (found.isEmpty()) handOver() else play(found)
        }
    }

    private fun play(found: List<BeforeClip>) {
        clips = found
        val http = OkHttpDataSource.Factory(graph.httpClient)
        val p = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(DefaultDataSource.Factory(this, http)))
            .build()
        p.setMediaItems(found.map { MediaItem.fromUri(it.url) })
        p.addListener(object : Player.Listener {
            override fun onMediaItemTransition(mediaItem: MediaItem?, reason: Int) {
                index = p.currentMediaItemIndex
            }

            override fun onPlaybackStateChanged(state: Int) {
                if (state == Player.STATE_ENDED) handOver()
            }

            // A clip that will not play is passed over, never a dead end before the movie.
            override fun onPlayerError(error: PlaybackException) = skip()
        })
        p.prepare()
        p.playWhenReady = true
        player = p
    }

    private fun skip() {
        val p = player ?: return handOver()
        if (p.hasNextMediaItem()) {
            p.seekToNextMediaItem()
            p.prepare()
            p.playWhenReady = true
        } else {
            handOver()
        }
    }

    private fun handOver() {
        if (handedOver) return
        handedOver = true
        player?.release()
        player = null
        startActivity(Intent(intent).setClass(this, PlayerActivity::class.java))
        finish()
    }

    override fun onStop() {
        super.onStop()
        // Home or the TV sleeping: nothing to report, just stop.
        if (!handedOver) {
            player?.release()
            player = null
            if (!isFinishing) finish()
        }
    }

    override fun onDestroy() {
        player?.release()
        player = null
        super.onDestroy()
    }

    @Composable
    private fun BeforeScreen() {
        val skipFocus = remember { FocusRequester() }
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
            val clip = clips.getOrNull(index)
            if (clip != null) {
                Box(
                    Modifier.fillMaxWidth().background(Brush.verticalGradient(0f to Black.copy(alpha = 0.85f), 1f to Color.Transparent))
                        .padding(horizontal = 40.dp, vertical = 24.dp),
                ) {
                    Label(BeforeMovie.label(clip), style = MaterialTheme.typography.labelLarge, color = White)
                }
                BrandButton(
                    BeforeMovie.skipLabel(index, clips.size),
                    onClick = ::skip,
                    primary = true,
                    modifier = Modifier.align(Alignment.BottomEnd).padding(40.dp).focusRequester(skipFocus),
                )
                LaunchedEffect(clips) { runCatching { skipFocus.requestFocus() } }
            }
        }
    }

    companion object {
        private const val EXTRA_ITEM_ID = "item_id"

        /**
         * Wraps a [PlayerActivity] intent: the same extras, opened after the
         * clips. Only a playback from the beginning comes here ([BeforeMovie.asks]).
         */
        fun wrap(context: Context, playerIntent: Intent): Intent = Intent(playerIntent).setClass(context, BeforeMovieActivity::class.java)
    }
}
