package io.github.pete_builds.vcrx.tv.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.DialogWindowProvider
import androidx.tv.material3.MaterialTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import io.github.pete_builds.vcrx.tv.ui.theme.Black
import io.github.pete_builds.vcrx.tv.ui.theme.CornerRadius
import io.github.pete_builds.vcrx.tv.ui.theme.Gray

/**
 * A picker panel: black with a 1dp gray edge. The platform dim is off, so a track
 * picker opened during playback leaves the picture visible around it.
 */
@Composable
fun BrandDialog(title: String, onDismiss: () -> Unit, content: @Composable ColumnScope.() -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        val window = (LocalView.current.parent as? DialogWindowProvider)?.window
        SideEffect { window?.setDimAmount(0f) }
        Column(
            Modifier
                .width(560.dp)
                .heightIn(max = 520.dp)
                .background(Black, RoundedCornerShape(CornerRadius))
                .border(1.dp, Gray, RoundedCornerShape(CornerRadius))
                .padding(24.dp)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Label(title, style = MaterialTheme.typography.labelLarge, modifier = Modifier.padding(bottom = 12.dp))
            content()
        }
    }
}
