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
import io.github.pete_builds.vcrx.tv.ui.theme.BrandTheme

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
            fromStart = intent.getBooleanExtra(EXTRA_FROM_START, false),
            title = intent.getStringExtra(EXTRA_TITLE).orEmpty(),
        )
        onBackPressedDispatcher.addCallback(this) { exit() }
        setContent {
            BrandTheme {
                PlayerScreen(controller, onExit = ::exit)
            }
        }
        controller.start()
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
        private const val EXTRA_FROM_START = "from_start"
        private const val EXTRA_TITLE = "title"

        fun intent(context: Context, itemId: Long, versionId: Long?, fromStart: Boolean, title: String): Intent =
            Intent(context, PlayerActivity::class.java)
                .putExtra(EXTRA_ITEM_ID, itemId)
                .putExtra(EXTRA_VERSION_ID, versionId ?: -1L)
                .putExtra(EXTRA_FROM_START, fromStart)
                .putExtra(EXTRA_TITLE, title)
    }
}
