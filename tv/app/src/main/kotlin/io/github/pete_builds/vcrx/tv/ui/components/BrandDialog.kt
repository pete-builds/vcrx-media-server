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
import io.github.pete_builds.vcrx.tv.ui.theme.Shapes
import io.github.pete_builds.vcrx.tv.ui.theme.Surfaces

/**
 * A picker panel: a raised dark surface with panel corners and a faint edge. The
 * platform dim is off, so a track picker opened during playback leaves the
 * picture visible around it.
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
                .background(Surfaces.Raised, Shapes.Panel)
                .border(1.dp, Surfaces.Wash, Shapes.Panel)
                .padding(24.dp)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            SectionTitle(title, modifier = Modifier.padding(start = 9.dp, bottom = 12.dp))
            content()
        }
    }
}
