package io.github.pete_builds.vcrx.tv.player

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import io.github.pete_builds.vcrx.tv.graph
import io.github.pete_builds.vcrx.tv.net.Marker
import io.github.pete_builds.vcrx.tv.net.PlaybackSettings
import io.github.pete_builds.vcrx.tv.settings.SkipIntroMode
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import kotlinx.coroutines.launch

/**
 * What the Skip button would do now: its label, where it seeks, whether it goes
 * straight to Up Next instead, and whether to do it without a press.
 */
data class SkipAction(
    val kind: String,
    val label: String,
    val targetMs: Long,
    val playsNext: Boolean,
    val auto: Boolean,
)

/**
 * Skip Intro and Skip Credits. Pure, so the rules are unit tested:
 * - The button shows while the playhead is inside a marker, except its last half second.
 * - Intro: seek to its end.
 * - Credits that run to the end of the file, with an episode after this one: Next
 *   Episode, straight into Up Next. Credits followed by something (a post-credits
 *   scene) seek to their end, which the server puts before the scene.
 * - skip_markers off: nothing. Only markers the server flagged auto_skip (confident
 *   intros, with "skip automatically" chosen) are skipped without a press.
 */
object SkipMarkers {
    const val INTRO = "intro"
    const val CREDITS = "credits"
    private const val TAIL_MS = 500L
    private const val TO_END_MS = 2_000L

    fun actionAt(markers: List<Marker>, positionMs: Long, durationMs: Long, hasNext: Boolean, mode: String): SkipAction? {
        if (mode == PlaybackSettings.OFF) return null
        val m = markers.firstOrNull { it.endMs > it.startMs && positionMs >= it.startMs && positionMs < it.endMs - TAIL_MS } ?: return null
        val toEnd = m.kind == CREDITS && durationMs > 0 && m.endMs >= durationMs - TO_END_MS
        return when {
            m.kind == INTRO -> SkipAction(INTRO, "Skip Intro", m.endMs, playsNext = false, auto = m.autoSkip && mode == PlaybackSettings.AUTO)
            toEnd && hasNext -> SkipAction(CREDITS, "Next Episode", durationMs, playsNext = true, auto = false)
            else -> SkipAction(CREDITS, "Skip Credits", m.endMs, playsNext = false, auto = false)
        }
    }

    /** The keys that press the Skip button while it shows. */
    fun isSelect(keyCode: Int): Boolean =
        keyCode == android.view.KeyEvent.KEYCODE_DPAD_CENTER || keyCode == android.view.KeyEvent.KEYCODE_ENTER ||
            keyCode == android.view.KeyEvent.KEYCODE_NUMPAD_ENTER
}

/**
 * The Skip button, bottom right, above the progress bar. It takes focus when it
 * appears, so one press of OK skips (the controller also routes OK to it while the
 * controls are hidden). An auto action runs once per marker kind per playback.
 */
@Composable
fun SkipButton(controller: PlaybackController) {
    val focus = remember { FocusRequester() }
    val autoDone = remember { mutableSetOf<String>() }
    val action = controller.skipAction ?: return
    Box(Modifier.fillMaxSize().padding(end = 48.dp, bottom = 140.dp), contentAlignment = Alignment.BottomEnd) {
        BrandButton(action.label, onClick = { controller.skipMarker(action) }, primary = true, modifier = Modifier.focusRequester(focus))
    }
    LaunchedEffect(action.kind, action.label) {
        if (action.auto && autoDone.add(action.kind)) {
            controller.skipMarker(action)
        } else {
            runCatching { focus.requestFocus() }
        }
    }
}

/** The Settings choice as the server stores it (skip_markers), shared with the web app. */
fun SkipIntroMode.wire(): String = when (this) {
    SkipIntroMode.ShowButton -> PlaybackSettings.BUTTON
    SkipIntroMode.Automatic -> PlaybackSettings.AUTO
    SkipIntroMode.Off -> PlaybackSettings.OFF
}

/** The server's skip_markers as the Settings choice; anything unknown is the button. */
fun skipIntroMode(wire: String): SkipIntroMode = when (wire) {
    PlaybackSettings.AUTO -> SkipIntroMode.Automatic
    PlaybackSettings.OFF -> SkipIntroMode.Off
    else -> SkipIntroMode.ShowButton
}

/**
 * For the Settings "Skip intro" row. The server's skip_markers is the one value: the
 * web app and every playback session read it. The local preference mirrors it: the
 * server's value is read when the row appears, and a pick is saved locally and on the
 * server. Offline, the pick stays local and the server keeps the old value.
 */
@Composable
fun rememberServerSkipIntro(current: SkipIntroMode, setLocal: (SkipIntroMode) -> Unit): (SkipIntroMode) -> Unit {
    val api = LocalContext.current.graph.api
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) {
        val server = runCatching { api.playbackSettings() }.getOrNull() ?: return@LaunchedEffect
        skipIntroMode(server.skipMarkers).takeIf { it != current }?.let(setLocal)
    }
    return { mode ->
        setLocal(mode)
        scope.launch { runCatching { api.setPlaybackSettings(PlaybackSettings(mode.wire())) } }
    }
}
