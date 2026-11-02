package io.github.pete_builds.vcrx.tv.player

import androidx.annotation.OptIn
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.PlayerView
import androidx.media3.ui.SubtitleView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.pete_builds.vcrx.tv.ui.components.BrandButton
import io.github.pete_builds.vcrx.tv.ui.components.BrandDialog
import io.github.pete_builds.vcrx.tv.ui.components.AppIcons
import io.github.pete_builds.vcrx.tv.ui.components.BrandRow
import io.github.pete_builds.vcrx.tv.ui.components.Label
import io.github.pete_builds.vcrx.tv.ui.components.LoadingCursor
import io.github.pete_builds.vcrx.tv.ui.components.LoadingView
import io.github.pete_builds.vcrx.tv.ui.components.Mark
import io.github.pete_builds.vcrx.tv.ui.components.MessageView
import io.github.pete_builds.vcrx.tv.ui.components.episodeCode
import io.github.pete_builds.vcrx.tv.ui.details.formatTime
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.Graphite
import io.github.pete_builds.vcrx.tv.ui.theme.Accent

@Composable
fun PlayerScreen(controller: PlaybackController, onExit: () -> Unit) {
    Box(Modifier.fillMaxSize().background(Black)) {
        Video(controller)
        when (val phase = controller.phase) {
            Phase.Starting -> LoadingView(message = "Starting playback")
            is Phase.Refused -> BlackField {
                MessageView("Cannot play this here", phase.message, primary = "Back" to onExit)
            }
            is Phase.Failed -> BlackField {
                MessageView(
                    when {
                        phase.unreachable -> "Server unreachable"
                        phase.busy -> "Server busy"
                        else -> "Playback stopped"
                    },
                    phase.message,
                    primary = "Retry" to controller::retry,
                    secondary = "Back" to onExit,
                )
            }
            is Phase.UpNext -> BlackField {
                val next = phase.next
                val name = listOfNotNull(episodeCode(next), next.title).joinToString("  ")
                MessageView(
                    "Up next",
                    phase.secondsLeft?.let { "$name starts in $it s." } ?: name,
                    primary = (if (phase.secondsLeft != null) "Play now" else "Play") to controller::playNext,
                    secondary = "Back" to onExit,
                )
            }
            is Phase.AskResume -> BlackField {
                MessageView(
                    "Resume?",
                    "You stopped at ${formatTime(phase.positionMs)}.",
                    primary = "Resume ${formatTime(phase.positionMs)}" to { controller.answerResume(true) },
                    secondary = "Start over" to { controller.answerResume(false) },
                )
            }
            Phase.Playing -> if (controller.overlay != Overlay.Hidden) Chrome(controller)
        }
        if (controller.phase == Phase.Playing && (controller.buffering || controller.reloading)) {
            // Keyed on the wait, so a short one shows nothing and each wait starts its own delay.
            androidx.compose.runtime.key(controller.reloading) {
                LoadingCursor(Modifier.align(Alignment.TopStart), description = "Loading")
            }
        }
        if (controller.phase == Phase.Playing && controller.dialog == null) SkipButton(controller)
        // What a subtitle press did, or that subtitles are being prepared; above the controls.
        controller.notice?.let { Notice(it) }
        controller.dialog?.let { TrackDialog(controller, it) }
    }
}

/** Text never sits on video without a solid black field behind it. */
@Composable
private fun BlackField(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().background(Black)) { content() }
}

@OptIn(UnstableApi::class)
@Composable
private fun Video(controller: PlaybackController) {
    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { context ->
            PlayerView(context).apply {
                useController = false
                isFocusable = false
                // No spinner: the loading cursor shows instead (PlayerScreen).
                setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                setKeepContentOnPlayerReset(true)
                setShutterBackgroundColor(Black.toArgb())
                // Settings > Subtitles > Style, or the TV's own caption settings by default.
                subtitleView?.let { CaptionStyles.apply(it, controller.captionStyle) }
                player = controller.player
            }
        },
    )
    // A converted stream carries no subtitles; the controller times the cues to the file
    // and they are drawn here, in the same style.
    if (controller.streamLabel != null) {
        AndroidView(
            modifier = Modifier.fillMaxSize(),
            factory = { context -> SubtitleView(context).apply { CaptionStyles.apply(this, controller.captionStyle) } },
            update = { it.setCues(controller.subtitleCues) },
        )
    }
}

@Composable
private fun Notice(text: String) {
    Box(Modifier.fillMaxSize().padding(top = 72.dp, end = 48.dp), contentAlignment = Alignment.TopEnd) {
        Label(
            text,
            style = MaterialTheme.typography.labelLarge,
            modifier = Modifier.background(Black, RoundedCornerShape(6.dp)).padding(horizontal = 18.dp, vertical = 10.dp),
        )
    }
}

/**
 * The on-screen chrome: solid black bars only, never a gradient over the picture.
 * Info mode is the bottom bar alone; controls mode adds the title and buttons.
 */
@Composable
private fun Chrome(controller: PlaybackController) {
    val controls = controller.overlay == Overlay.Controls
    val playFocus = remember { FocusRequester() }
    val subtitlesFocus = remember { FocusRequester() }
    Column(Modifier.fillMaxSize()) {
        if (controls) {
            Row(
                Modifier.fillMaxWidth().background(Black).padding(horizontal = 40.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Mark(height = 22.dp)
                Text(
                    controller.title,
                    style = MaterialTheme.typography.titleLarge,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                // Which kind of stream this is, as the web player's pill says it.
                controller.streamLabel?.let { Label(it, style = MaterialTheme.typography.labelLarge, color = Accent) }
            }
        }
        Spacer(Modifier.weight(1f))
        Column(
            Modifier.fillMaxWidth().background(Black).padding(horizontal = 48.dp, vertical = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            val target = controller.scrubTargetMs
            val shown = target ?: controller.positionMs
            Progress(controller.positionMs, target, controller.durationMs)
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Label(formatTime(shown), style = MaterialTheme.typography.labelLarge)
                Label(status(controller, target), style = MaterialTheme.typography.labelLarge)
                Label(if (controller.durationMs > 0) formatTime(controller.durationMs) else "", style = MaterialTheme.typography.labelLarge)
            }
            if (controls) {
                // Scrolls sideways if the buttons outgrow the screen; focus brings each into view.
                Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    BrandButton(
                        if (controller.isPlaying) "Pause" else "Play",
                        onClick = controller::togglePlayPause,
                        icon = if (controller.isPlaying) AppIcons.Pause else Icons.Filled.PlayArrow,
                        primary = true,
                        modifier = Modifier.focusRequester(playFocus),
                    )
                    // Beside Play, where Down lands: one press turns subtitles on or
                    // off; the next button picks the track.
                    BrandButton(
                        if (controller.subtitlesOn) "Subtitles on" else "Subtitles off",
                        onClick = controller::toggleSubtitles,
                        icon = AppIcons.Subtitles,
                        modifier = Modifier.focusRequester(subtitlesFocus),
                    )
                    BrandButton("Subtitle track", onClick = { controller.dialog = TrackKind.Subtitles })
                    BrandButton("Back ${seconds(controller.skipBackMs)}", onClick = { controller.skip(-controller.skipBackMs) }, icon = AppIcons.Rewind)
                    BrandButton("Forward ${seconds(controller.skipForwardMs)}", onClick = { controller.skip(controller.skipForwardMs) }, icon = AppIcons.Forward)
                    BrandButton("Audio", onClick = { controller.dialog = TrackKind.Audio }, icon = AppIcons.Audio)
                }
            }
        }
    }
    if (controls) {
        LaunchedEffect(Unit) {
            runCatching { (if (controller.controlsFocus == ControlsFocus.Subtitles) subtitlesFocus else playFocus).requestFocus() }
        }
    }
}

private fun seconds(ms: Long): String = if (ms % 60_000 == 0L) "${ms / 60_000}m" else "${ms / 1000}s"

private fun status(controller: PlaybackController, target: Long?): String = when {
    target != null -> {
        val delta = target - controller.positionMs
        (if (delta >= 0) "Forward " else "Back ") + formatTime(kotlin.math.abs(delta))
    }
    controller.reloading -> "Loading"
    !controller.isPlaying && !controller.player.playWhenReady -> "Paused"
    // A converted stream says so whenever the bar is up; direct play shows nothing here.
    else -> controller.streamLabel.orEmpty()
}

/** Accent fill on a graphite track, and a green scrubber at the playhead or seek target. */
@Composable
private fun Progress(positionMs: Long, targetMs: Long?, durationMs: Long) {
    BoxWithConstraints(Modifier.fillMaxWidth().height(22.dp)) {
        val fraction = if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f
        val head = if (durationMs > 0) ((targetMs ?: positionMs).toFloat() / durationMs).coerceIn(0f, 1f) else 0f
        Box(
            Modifier
                .align(Alignment.CenterStart)
                .fillMaxWidth()
                .height(6.dp)
                .background(Graphite, RoundedCornerShape(3.dp)),
        ) {
            Box(Modifier.fillMaxWidth(fraction).height(6.dp).background(Accent, RoundedCornerShape(3.dp)))
        }
        Box(
            Modifier
                .align(Alignment.CenterStart)
                .offset(x = (maxWidth - 18.dp) * head)
                .size(18.dp)
                .background(Accent, CircleShape),
        )
    }
}

@Composable
private fun TrackDialog(controller: PlaybackController, kind: TrackKind) {
    val options = remember(kind) { controller.trackOptions(kind) }
    val focus = remember { FocusRequester() }
    val focusIndex = options.indexOfFirst { it.selected }.coerceAtLeast(0)
    BrandDialog(
        if (kind == TrackKind.Audio) "Audio" else "Subtitles",
        onDismiss = {
            controller.dialog = null
            controller.touch()
        },
    ) {
        if (options.isEmpty() || (kind == TrackKind.Subtitles && options.size == 1)) {
            Label("No tracks in this file", style = MaterialTheme.typography.labelMedium)
        }
        if (kind == TrackKind.Subtitles && controller.pictureSubtitlesUnavailable) {
            // Converted streams show text subtitles only; picture ones would need burning into the video.
            Label("Picture subtitles (PGS, VobSub) need direct play", style = MaterialTheme.typography.labelMedium)
        }
        options.forEachIndexed { index, option ->
            BrandRow(
                onClick = {
                    if (option.supported) {
                        option.apply()
                        controller.dialog = null
                        controller.touch()
                    }
                },
                selected = option.selected,
                modifier = if (index == focusIndex) Modifier.focusRequester(focus) else Modifier,
            ) {
                Column {
                    Label(option.label)
                    if (!option.supported) Label("Not supported on this device", style = MaterialTheme.typography.labelSmall)
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}
