package io.github.pete_builds.vcrx.tv.player

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.view.KeyEvent
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.addCallback
import androidx.activity.compose.setContent
import androidx.lifecycle.lifecycleScope
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.ui.theme.BrandTheme
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * Full-screen playback. Its own activity so leaving it, by Back, by the end of the
 * file or by the Home button, always passes through [onStop], where the final stop
 * event is sent with the last position.
 */
class PlayerActivity : ComponentActivity() {
    private lateinit var controller: PlaybackController

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        controller = PlaybackController(
            activity = this,
            itemId = intent.getLongExtra(EXTRA_ITEM_ID, -1),
            versionId = intent.getLongExtra(EXTRA_VERSION_ID, -1).takeIf { it > 0 },
            request = intent.getStringExtra(EXTRA_START)?.let { runCatching { StartRequest.valueOf(it) }.getOrNull() } ?: StartRequest.Default,
            title = intent.getStringExtra(EXTRA_TITLE).orEmpty(),
            extraId = intent.getLongExtra(EXTRA_EXTRA_ID, -1).takeIf { it > 0 },
        )
        onBackPressedDispatcher.addCallback(this) { exit() }
        setContent {
            BrandTheme {
                PlayerScreen(controller, onExit = ::exit)
            }
        }
        controller.start()
        // A lent tape that came back mid-film: close the player, and the main
        // screen says "Be kind, rewind."
        lifecycleScope.launch {
            graph.rewind.filterNotNull().first()
            exit()
        }
    }

    // Activity.dispatchKeyEvent is public framework API. androidx.core's ComponentActivity
    // overrides it with a restricted annotation, which lint reports on any override.
    @SuppressLint("RestrictedApi")
    override fun dispatchKeyEvent(event: KeyEvent): Boolean =
        controller.handleKey(event, ::exit) || super.dispatchKeyEvent(event)

    override fun onStop() {
        super.onStop()
        // Home, the input button or the TV sleeping: report the position and end the session.
        controller.finishPlayback()
        if (!isFinishing) finish()
    }

    override fun onDestroy() {
        controller.finishPlayback()
        super.onDestroy()
    }

    private fun exit() {
        controller.finishPlayback()
        finish()
    }

    companion object {
        private const val EXTRA_ITEM_ID = "item_id"
        private const val EXTRA_VERSION_ID = "version_id"
        private const val EXTRA_START = "start"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_EXTRA_ID = "extra_id"

        fun intent(context: Context, itemId: Long, versionId: Long?, start: StartRequest, title: String): Intent =
            Intent(context, PlayerActivity::class.java)
                .putExtra(EXTRA_ITEM_ID, itemId)
                .putExtra(EXTRA_VERSION_ID, versionId ?: -1L)
                .putExtra(EXTRA_START, start.name)
                .putExtra(EXTRA_TITLE, title)

        /** Plays one of the item's extras, such as its trailer, from the start. */
        fun extraIntent(context: Context, itemId: Long, extraId: Long, title: String): Intent =
            intent(context, itemId, null, StartRequest.Beginning, title).putExtra(EXTRA_EXTRA_ID, extraId)
    }
}
